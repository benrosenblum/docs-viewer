package docsview

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/benrosenblum/docs-viewer/docsview/markdown"
)

// maxRenderedDiff is the largest note body, in bytes, that gets a rendered
// diff. Larger notes get the source diff.
const maxRenderedDiff = 1 << 20

// gitControls is the model of the diff controls in the page header.
type gitControls struct {
	Path   string
	Status string // The tree mark of the file: M, A, U, or "".
	Base   string // The diff parameter of the page: off, HEAD, branch, or a hash.
	Label  string // The picker button text.
	// ShowToggle shows the "Show page" and "Show changes" toggle.
	ShowToggle bool
	PageURL    string
	ChangesURL string
	Diff       *diffView // Nil on a normal page.
}

// diffView is the model of a diff page.
type diffView struct {
	Note        bool // A Markdown note: the rendered and source toggle shows.
	Source      bool // The page shows the source diff.
	RenderedURL string
	SourceURL   string
	Message     string
	Table       template.HTML
	// PropertiesChanged marks the properties section of a rendered diff.
	PropertiesChanged bool
}

// fileURL returns the URL of a file page with the diff and as parameters.
func fileURL(p, diff, as string) string {
	q := url.Values{}
	if diff != "" {
		q.Set("diff", diff)
	}
	if as != "" {
		q.Set("as", as)
	}
	u := markdown.URL(p, "")
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// gitState returns the shared git state of the vault, or nil without git.
func (h *Handler) gitState(idx *index) *gitState {
	if h.git == nil {
		return nil
	}
	return h.git.status(h.vaults, idx.version)
}

// fileStatus returns the tree mark of a file. A file outside the vault gets
// its own status command.
func (h *Handler) fileStatus(idx *index, p string) string {
	if h.git == nil {
		return ""
	}
	if h.vaultOf(p) != "" {
		return h.gitState(idx).statusOf(p)
	}
	return h.git.pathStatus(p, h.statLine(p))
}

// diffBase returns the base of a file page request: "off" for the normal
// page. A URL without a diff parameter shows the diff against HEAD when the
// file has uncommitted changes. Images keep their raw bytes, because notes
// embed them by the bare URL.
func (h *Handler) diffBase(r *http.Request, idx *index, p string) string {
	values, set := r.URL.Query()["diff"]
	switch {
	case h.git == nil || r.URL.Query().Get("raw") != "":
		return baseOff
	case set:
		return values[0]
	}
	if _, raw := rawTypes[strings.ToLower(path.Ext(p))]; raw || h.fileStatus(idx, p) == "" {
		return baseOff
	}
	return baseHead
}

// controls returns the header controls of a file page.
func (h *Handler) controls(idx *index, p, base string) *gitControls {
	if h.git == nil {
		return nil
	}
	c := &gitControls{Path: p, Status: h.fileStatus(idx, p), Base: base, Label: "Compare", PageURL: fileURL(p, baseOff, "")}
	c.ChangesURL = fileURL(p, baseHead, "")
	if c.Status != "" {
		c.ChangesURL = fileURL(p, "", "")
	}
	c.ShowToggle = c.Status != "" || base != baseOff
	switch base {
	case baseOff:
	case baseHead:
		c.Label = "Uncommitted"
	case baseBranch:
		c.Label = "Branch"
	default:
		c.Label = base[:min(len(base), 7)]
	}
	return c
}

// badBase answers a request with an invalid or unknown base.
func (h *Handler) badBase(w http.ResponseWriter, r *http.Request, idx *index, p string, err error) {
	pg := h.newPage(idx, "missing", p)
	pg.Title = "Invalid base"
	pg.Body = template.HTML(`<p class="render-error">` + escapeText(err.Error()) + `</p><p><a href="` + escapeText(fileURL(p, baseOff, "")) + `">Show the page</a></p>`)
	h.render(w, r, http.StatusBadRequest, pg)
}

// diff serves the diff of a file against a base.
func (h *Handler) diff(w http.ResponseWriter, r *http.Request, idx *index, p, base string) {
	ctx := r.Context()
	commit, err := h.git.resolveBase(ctx, base)
	if err != nil {
		var be *baseError
		if errors.As(err, &be) {
			h.badBase(w, r, idx, p, err)
		} else {
			h.withError(w, r, idx, p, err)
		}
		return
	}
	note := isNote(p)
	limit := int64(maxSource)
	if note {
		limit = maxNote
	}
	pg := h.newPage(idx, "diff", p)
	if !note {
		pg.Name = path.Base(p)
		pg.Title = pg.Name
		pg.Crumbs[len(pg.Crumbs)-1].Name = pg.Name
	}
	pg.Backlinks = idx.backlinks(p)
	pg.Git = h.controls(idx, p, base)
	as := ""
	if note && r.URL.Query().Get("as") == "source" {
		as = "source"
	}
	d := &diffView{Note: note, Source: !note || as == "source", RenderedURL: fileURL(p, base, ""), SourceURL: fileURL(p, base, "source")}
	pg.Git.Diff, pg.Diff = d, d

	state := h.gitState(idx)
	top, err := h.git.pathAt(ctx, state, commit, p)
	if err != nil {
		h.withError(w, r, idx, p, err)
		return
	}
	var old []byte
	exists, tooLarge := false, false
	if top != "" {
		old, exists, err = h.git.show(ctx, commit, top, limit)
		if errors.Is(err, errBlobTooLarge) {
			tooLarge = true
		} else if err != nil {
			h.withError(w, r, idx, p, err)
			return
		}
	}
	cur, err := h.read(p, limit)
	if errors.Is(err, errTooLarge) {
		tooLarge = true
	} else if err != nil {
		h.unreadable(w, r, pg, err)
		return
	}
	var label string
	switch base {
	case baseHead:
		label = "HEAD"
	case baseBranch:
		_, name := h.git.mainRef(ctx)
		label = "the merge base with " + name
	default:
		label = "commit " + base[:min(len(base), 7)]
	}

	// The outline and the title are those of the current version.
	if n, ok := idx.byPath[p]; ok {
		pg.Outline = n.doc.Headings
		if n.doc.Title != "" {
			pg.Title = n.doc.Title
		}
	} else if note {
		pg.Outline = markdown.Headings(cur)
	}
	if !d.Source {
		switch {
		case tooLarge || isBinary(old) || isBinary(cur):
			d.Source = true
		case exists && string(old) == string(cur):
			d.Message = noChanges(label)
		case len(old) > maxRenderedDiff || len(cur) > maxRenderedDiff:
			d.Source, d.Message = true, "The note is too large for the rendered diff. The page shows the source diff."
		default:
			rendered, err := markdown.RenderDiff(old, cur, p, &resolver{h: h, idx: idx})
			if err != nil {
				d.Source, d.Message = true, "The rendered diff failed: "+err.Error()+". The page shows the source diff."
				break
			}
			pg.Doc, pg.Body, pg.Math = rendered.Document, template.HTML(rendered.HTML), rendered.Math
			d.PropertiesChanged = rendered.PropertiesChanged
			if rendered.Approximate {
				d.Message = msgApproximate
			}
		}
	}
	if d.Source {
		table, message := sourceDiff(old, cur, exists, tooLarge, path.Base(p), label)
		d.Table = table
		if message != "" {
			d.Message = strings.TrimSpace(d.Message + " " + message)
		}
	}
	h.render(w, r, http.StatusOK, pg)
}

// withError answers with status 500 inside the viewer layout.
func (h *Handler) withError(w http.ResponseWriter, r *http.Request, idx *index, p string, err error) {
	h.unreadable(w, r, h.newPage(idx, "missing", p), err)
}

// historyResponse is the body of /_/api/history.
type historyResponse struct {
	Branch     string       `json:"branch"`
	Main       string       `json:"main"` // The main branch, or "".
	BranchBase bool         `json:"branchBase"`
	Commits    []commitInfo `json:"commits"`
}

// historyJSON lists the commits that changed a file.
func (h *Handler) historyJSON(w http.ResponseWriter, r *http.Request, idx *index) {
	p := strings.Trim(r.URL.Query().Get("path"), "/")
	if h.git == nil || p == "" || path.Clean(p) != p || !allowed(p) {
		http.NotFound(w, r)
		return
	}
	if info, err := h.stat(p); err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	state := h.gitState(idx)
	commits, err := h.git.log(r.Context(), state.head, p)
	if err != nil {
		http.Error(w, "Cannot read the history: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_, main := h.git.mainRef(r.Context())
	branchBase := main != "" && state.branch != main && state.head != ""
	writeJSON(w, r, historyResponse{Branch: state.branch, Main: main, BranchBase: branchBase, Commits: nonNil(commits)})
}
