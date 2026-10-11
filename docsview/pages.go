package docsview

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/benrosenblum/docs-viewer/docsview/markdown"
)

// page is the template model shared by every view.
type page struct {
	Kind      string // note, source, apispec, diff, folder, search, graph, or missing.
	Title     string
	Path      string
	Name      string
	Site      string   // The repository directory name.
	Branch    string   // The current branch or the short HEAD hash, or "" without git.
	Vault     string   // The vault directory that contains Path, or "".
	Vaults    []string // All vault directories.
	SpecVault string   // The vault of OpenAPI specs, or "".
	AssetBase string
	Theme     string // The URL of the theme stylesheet.
	Outside   bool
	Crumbs    []crumb
	Tree      *treeItem
	Doc       *markdown.Document
	Body      template.HTML
	Outline   []markdown.Heading
	Backlinks []backlink
	Folder    []folderEntry
	Search    *searchResult
	Source    *sourceView
	Git       *gitControls // Nil without git and on pages that are not files.
	Diff      *diffView    // Set on a diff page.
	Spec      *specView    // Set on the pages of a spec file.
	Math      bool
}

type crumb struct{ Name, URL string }

type treeItem struct {
	Path, Name, Ext, URL string
	Dir, Note, Active    bool
	Open                 bool
	Git                  string // The tree mark of a file: M, A, U, or "".
	Dirty                bool   // A folder that contains a marked file.
	Children             []*treeItem
}

type folderEntry struct {
	Name, URL, Path string
	Dir, Note       bool
}

type sourceView struct {
	Lang, Meta string
}

// specView links the two views of a spec file: the rendered API reference
// and the highlighted source.
type specView struct {
	RawURL, ReferenceURL, SourceURL string
	Source                          bool
}

func icon(name string) template.HTML {
	return template.HTML(`<svg class="icon" aria-hidden="true"><use href="/_/static/icons.svg#i-` + template.HTMLEscapeString(name) + `"></use></svg>`)
}

// newPage fills the parts of the layout that depend only on the location.
func (h *Handler) newPage(idx *index, kind, p string) *page {
	state := h.gitState(idx)
	pg := &page{Kind: kind, Path: p, Site: h.name, Branch: state.ref(), Vaults: h.vaults, SpecVault: h.specVault, AssetBase: h.assetBase, Theme: h.themeURL, Tree: viewTree(idx.tree, p, state)}
	if p != "" {
		pg.Vault = h.vaultOf(p)
		pg.Outside = pg.Vault == ""
		pg.Crumbs = crumbs(p)
		pg.Name = noteName(path.Base(p))
	}
	pg.Title = pg.Name
	return pg
}

// viewTree copies the vault tree with the active file and its folders
// marked, and with the git marks of state, which can be nil.
func viewTree(e *entry, active string, state *gitState) *treeItem {
	item := &treeItem{Path: e.path, Name: e.name, Dir: e.dir, URL: markdown.URL(e.path, "")}
	if e.dir {
		item.URL += "/"
		item.Open = active == e.path || strings.HasPrefix(active, e.path+"/")
		for _, child := range e.children {
			c := viewTree(child, active, state)
			item.Dirty = item.Dirty || c.Dirty || c.Git != ""
			item.Children = append(item.Children, c)
		}
		return item
	}
	item.Note = isNote(e.name)
	item.Active = e.path == active
	item.Git = state.statusOf(e.path)
	if item.Note {
		item.Name = noteName(e.name)
	} else if ext := path.Ext(e.name); ext != "" {
		item.Name, item.Ext = strings.TrimSuffix(e.name, ext), strings.TrimPrefix(ext, ".")
	}
	return item
}

func crumbs(p string) []crumb {
	parts := strings.Split(p, "/")
	result := make([]crumb, 0, len(parts))
	for i, part := range parts {
		c := crumb{Name: part}
		if i < len(parts)-1 {
			c.URL = markdown.URL(strings.Join(parts[:i+1], "/"), "") + "/"
		} else {
			c.Name = noteName(part)
		}
		result = append(result, c)
	}
	return result
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, pg *page) {
	var buffer bytes.Buffer
	if err := h.pages.ExecuteTemplate(&buffer, "layout.html", pg); err != nil {
		http.Error(w, "Page template failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(buffer.Bytes())
	}
}

func (h *Handler) missing(w http.ResponseWriter, r *http.Request, idx *index) {
	pg := h.newPage(idx, "missing", "")
	pg.Title = "Not found"
	h.render(w, r, http.StatusNotFound, pg)
}

// repository serves a repository path as a note, folder, source, or raw file.
func (h *Handler) repository(w http.ResponseWriter, r *http.Request, idx *index) {
	p := strings.TrimPrefix(r.URL.Path, "/")
	folder := strings.HasSuffix(p, "/")
	p = strings.TrimSuffix(p, "/")
	if p == "" || path.Clean(p) != p || !allowed(p) {
		h.missing(w, r, idx)
		return
	}
	info, err := h.stat(p)
	if err != nil {
		h.missing(w, r, idx)
		return
	}
	switch {
	case info.IsDir() && !folder:
		http.Redirect(w, r, markdown.URL(p, "")+"/", http.StatusMovedPermanently)
	case info.IsDir():
		h.folder(w, r, idx, p)
	case folder || !info.Mode().IsRegular():
		h.missing(w, r, idx)
	default:
		switch base := h.diffBase(r, idx, p); {
		case base != baseOff:
			h.diff(w, r, idx, p, base)
		case isNote(p) && r.URL.Query().Get("raw") == "":
			h.note(w, r, idx, p)
		default:
			h.file(w, r, idx, p)
		}
	}
}

func (h *Handler) note(w http.ResponseWriter, r *http.Request, idx *index, p string) {
	pg := h.newPage(idx, "note", p)
	doc := (*markdown.Document)(nil)
	if n, ok := idx.byPath[p]; ok {
		doc = n.doc
	} else {
		src, err := h.read(p, maxNote)
		if err != nil {
			h.unreadable(w, r, pg, err)
			return
		}
		if doc, err = markdown.Render(src, p, &resolver{h: h, idx: idx}); err != nil {
			h.unreadable(w, r, pg, err)
			return
		}
	}
	pg.Doc, pg.Body, pg.Outline, pg.Math = doc, template.HTML(doc.HTML), doc.Headings, doc.Math
	if doc.Title != "" {
		pg.Title = doc.Title
	}
	pg.Backlinks = idx.backlinks(p)
	pg.Git = h.controls(idx, p, baseOff)
	h.render(w, r, http.StatusOK, pg)
}

func (h *Handler) unreadable(w http.ResponseWriter, r *http.Request, pg *page, err error) {
	pg.Kind = "missing"
	pg.Body = template.HTML("<p class=\"render-error\">" + escapeText(err.Error()) + "</p>")
	status := http.StatusInternalServerError
	if errors.Is(err, fs.ErrNotExist) {
		status = http.StatusNotFound
	}
	h.render(w, r, status, pg)
}

// isSpec reports whether p is a YAML file in the spec vault.
func (h *Handler) isSpec(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	return h.specVault != "" && within(p, h.specVault) && (ext == ".yaml" || ext == ".yml")
}

// file serves images and raw requests as bytes, specs as API reference
// pages, and other text as a source page.
func (h *Handler) file(w http.ResponseWriter, r *http.Request, idx *index, p string) {
	raw := r.URL.Query().Get("raw") != ""
	if contentType, ok := rawTypes[strings.ToLower(path.Ext(p))]; ok || raw {
		f, err := h.root.Open(p)
		if err != nil {
			h.missing(w, r, idx)
			return
		}
		defer f.Close()
		if !ok {
			contentType = "text/plain; charset=utf-8"
		}
		w.Header().Set("Content-Type", contentType)
		if contentType == "image/svg+xml" {
			w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
		}
		http.ServeContent(w, r, path.Base(p), time.Time{}, f)
		return
	}
	pg := h.newPage(idx, "source", p)
	pg.Name = path.Base(p)
	pg.Title = pg.Name
	pg.Crumbs[len(pg.Crumbs)-1].Name = pg.Name
	pg.Backlinks = idx.backlinks(p)
	pg.Git = h.controls(idx, p, baseOff)
	if h.isSpec(p) {
		// The toggle selects a form of the page, so it keeps diff mode off.
		u := markdown.URL(p, "")
		pg.Spec = &specView{RawURL: u + "?raw=1", ReferenceURL: u + "?diff=off", SourceURL: u + "?diff=off&view=source", Source: r.URL.Query().Get("view") == "source"}
		if !pg.Spec.Source {
			pg.Kind = "apispec"
			w.Header().Set("Content-Security-Policy", specPolicy)
			h.render(w, r, http.StatusOK, pg)
			return
		}
	}
	data, err := h.read(p, maxSource)
	switch {
	case errors.Is(err, errTooLarge):
		pg.Source = &sourceView{Meta: "Too large to display"}
	case err != nil:
		h.unreadable(w, r, pg, err)
		return
	case !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0:
		pg.Source = &sourceView{Meta: fmt.Sprintf("Binary file · %s", size(len(data)))}
	default:
		lines := bytes.Count(data, []byte("\n"))
		if len(data) > 0 && data[len(data)-1] != '\n' {
			lines++
		}
		pg.Source = &sourceView{Lang: strings.TrimPrefix(path.Ext(p), "."), Meta: fmt.Sprintf("%d lines · %s", lines, size(len(data)))}
		pg.Body = template.HTML(markdown.Highlight(data, "", path.Base(p), true))
	}
	h.render(w, r, http.StatusOK, pg)
}

func size(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func (h *Handler) folder(w http.ResponseWriter, r *http.Request, idx *index, p string) {
	pg := h.newPage(idx, "folder", p)
	pg.Name = path.Base(p)
	pg.Title = pg.Name
	pg.Crumbs[len(pg.Crumbs)-1].Name = pg.Name
	var children []*entry
	if e, ok := idx.entries[p]; ok {
		children = e.children
	} else {
		dir, err := fs.ReadDir(h.root.FS(), p)
		if err != nil {
			h.missing(w, r, idx)
			return
		}
		for _, d := range dir {
			if child := path.Join(p, d.Name()); allowed(child) {
				if info, err := h.stat(child); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
					children = append(children, &entry{path: child, name: d.Name(), dir: info.IsDir()})
				}
			}
		}
		slices.SortFunc(children, compareEntries)
	}
	for _, c := range children {
		entry := folderEntry{Name: c.name, Path: c.path, Dir: c.dir, Note: isNote(c.name), URL: markdown.URL(c.path, "")}
		if c.dir {
			entry.URL += "/"
		}
		pg.Folder = append(pg.Folder, entry)
	}
	h.render(w, r, http.StatusOK, pg)
}

func (h *Handler) searchPage(w http.ResponseWriter, r *http.Request, idx *index) {
	pg := h.newPage(idx, "search", "")
	result := idx.search(r.URL.Query().Get("q"))
	pg.Search = &result
	pg.Title = "Search"
	if result.Query != "" {
		pg.Title = "Search: " + result.Query
	}
	h.render(w, r, http.StatusOK, pg)
}

func (h *Handler) graphPage(w http.ResponseWriter, r *http.Request, idx *index) {
	pg := h.newPage(idx, "graph", "")
	pg.Title = "Graph view"
	h.render(w, r, http.StatusOK, pg)
}
