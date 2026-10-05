// Package docsview serves a read-only, Obsidian-style viewer for a
// repository's documentation directory. Markdown renders on the server; the
// browser adds navigation, search, the graph, Mermaid, and KaTeX.
package docsview

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/benrosenblum/docs-viewer/docsview/markdown"
)

//go:embed static templates
var embedded embed.FS

// Config selects the repository, the vault directories inside it, and
// verified assets.
type Config struct {
	// Root is the repository directory. Only paths below it are served.
	Root string
	// Vaults are the note directories relative to Root, such as "docs" and
	// "openspec". Together they form the vault. The first holds the home note.
	Vaults []string
	// Assets is a verified directory of pinned browser assets.
	Assets string
	// AssetFiles lists the servable "name/path" entries under Assets.
	AssetFiles map[string]bool
	// AssetVersion is an immutable URL segment for Assets.
	AssetVersion string
	// Theme is the path of a theme stylesheet, absolute or relative to Root,
	// or "" for the built-in theme. Pages load it before viewer.css. The
	// .css and .woff2 files below its directory are served, so the
	// stylesheet can import the files next to it.
	Theme string
	// Poll is the live-reload check interval. Zero selects 500 ms.
	Poll time.Duration
	// SpecVault is the vault of OpenAPI specs, or "". Its YAML files render
	// as API reference pages.
	SpecVault string
}

// Handler serves the viewer. It is safe for concurrent use.
type Handler struct {
	root, assets *os.Root
	theme        *os.Root // The directory of Config.Theme, or nil.
	themeURL     string
	rootPath     string // The repository directory without symbolic links.
	name         string // The repository directory name.
	vaults       []string
	specVault    string
	assetFiles   map[string]bool
	assetBase    string
	pages        *template.Template
	highlight    []byte
	ticker       func() (<-chan time.Time, func())
	git          *gitRepo // Nil when git is unavailable; see gitErr.
	gitErr       error

	mu       sync.Mutex
	idx      *index
	headings headingCache
}

// New opens the repository and asset roots. Each vault directory must exist,
// and no vault directory can contain another.
func New(config Config) (*Handler, error) {
	if len(config.Vaults) == 0 {
		return nil, errors.New("no documentation directory")
	}
	var vaults []string
	for _, v := range config.Vaults {
		vault := path.Clean(strings.Trim(v, "/"))
		if !allowed(vault) || vault == "." {
			return nil, fmt.Errorf("invalid documentation directory %q", v)
		}
		for _, other := range vaults {
			if within(vault, other) || within(other, vault) {
				return nil, fmt.Errorf("documentation directories %s and %s overlap", other, vault)
			}
		}
		vaults = append(vaults, vault)
	}
	specVault := ""
	if config.SpecVault != "" {
		specVault = path.Clean(strings.Trim(config.SpecVault, "/"))
		if !slices.Contains(vaults, specVault) {
			return nil, fmt.Errorf("spec directory %q is not a documentation directory", config.SpecVault)
		}
	}
	rootPath, err := filepath.EvalSymlinks(config.Root)
	if err == nil {
		rootPath, err = filepath.Abs(rootPath)
	}
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	for _, vault := range vaults {
		if info, err := root.Stat(vault); err != nil || !info.IsDir() {
			root.Close()
			return nil, fmt.Errorf("documentation directory %s is missing or not a directory", vault)
		}
	}
	assets, err := os.OpenRoot(config.Assets)
	if err != nil {
		root.Close()
		return nil, err
	}
	theme, themeURL, err := openTheme(rootPath, config.Theme)
	if err != nil {
		root.Close()
		assets.Close()
		return nil, err
	}
	pages, err := template.New("pages").Funcs(template.FuncMap{"icon": icon}).ParseFS(embedded, "templates/*.html")
	if err != nil {
		root.Close()
		assets.Close()
		if theme != nil {
			theme.Close()
		}
		return nil, err
	}
	poll := config.Poll
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	h := &Handler{
		root: root, assets: assets, theme: theme, themeURL: themeURL, rootPath: rootPath, name: filepath.Base(rootPath), vaults: vaults, specVault: specVault, assetFiles: config.AssetFiles,
		assetBase: "/_/assets/" + config.AssetVersion, pages: pages,
		highlight: []byte(markdown.HighlightCSS()),
		ticker: func() (<-chan time.Time, func()) {
			t := time.NewTicker(poll)
			return t.C, t.Stop
		},
		headings: headingCache{entries: map[string]cachedHeadings{}},
	}
	h.git, h.gitErr = openGit(rootPath)
	return h, nil
}

// GitError returns the reason why the git diff features are off, or nil
// when they are on.
func (h *Handler) GitError() error { return h.gitErr }

// Close releases the directory handles.
func (h *Handler) Close() error {
	err := errors.Join(h.root.Close(), h.assets.Close())
	if h.theme != nil {
		err = errors.Join(err, h.theme.Close())
	}
	return err
}

// contentPolicy allows only this origin. Mermaid and KaTeX set inline styles.
const contentPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; object-src 'none'; frame-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"

// specPolicy adds the blob worker that Redoc starts for its search.
const specPolicy = contentPolicy + "; worker-src 'self' blob:"

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", contentPolicy)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := r.URL.Path
	if strings.ContainsAny(p, "\\\x00") || !strings.HasPrefix(p, "/") {
		http.NotFound(w, r)
		return
	}
	switch {
	case p == "/":
		h.home(w, r)
	case strings.HasPrefix(p, "/_/static/"):
		h.static(w, r, strings.TrimPrefix(p, "/_/static/"))
	case strings.HasPrefix(p, "/_/theme/"):
		h.themeFile(w, r, strings.TrimPrefix(p, "/_/theme/"))
	case strings.HasPrefix(p, h.assetBase+"/"):
		h.asset(w, r, strings.TrimPrefix(p, h.assetBase+"/"))
	case p == "/_/api/index":
		h.withIndex(w, r, h.indexJSON)
	case p == "/_/api/search":
		h.withIndex(w, r, h.searchJSON)
	case p == "/_/api/graph":
		h.withIndex(w, r, h.graphJSON)
	case p == "/_/api/history":
		h.withIndex(w, r, h.historyJSON)
	case p == "/_/events":
		h.events(w, r)
	case p == "/_/search":
		h.withIndex(w, r, h.searchPage)
	case p == "/_/graph":
		h.withIndex(w, r, h.graphPage)
	case strings.HasPrefix(p, "/_/"):
		h.withIndex(w, r, h.missing)
	default:
		h.withIndex(w, r, h.repository)
	}
}

func (h *Handler) withIndex(w http.ResponseWriter, r *http.Request, serve func(http.ResponseWriter, *http.Request, *index)) {
	idx, err := h.current()
	if err != nil {
		http.Error(w, "Cannot read the documentation directory: "+err.Error(), http.StatusInternalServerError)
		return
	}
	serve(w, r, idx)
}

func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	readme := path.Join(h.vaults[0], "README.md")
	if info, err := h.stat(readme); err == nil && info.Mode().IsRegular() {
		http.Redirect(w, r, markdown.URL(readme, ""), http.StatusFound)
		return
	}
	http.Redirect(w, r, markdown.URL(h.vaults[0], "")+"/", http.StatusFound)
}

var staticTypes = map[string]string{".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8", ".svg": "image/svg+xml"}

func (h *Handler) static(w http.ResponseWriter, r *http.Request, name string) {
	var data []byte
	if name == "highlight.css" {
		data = h.highlight
	} else if contentType := staticTypes[path.Ext(name)]; contentType == "" || !fs.ValidPath(name) || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	} else if data, _ = embedded.ReadFile("static/" + name); data == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", staticTypes[path.Ext(name)])
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

// builtinTheme is the URL of the default theme stylesheet.
const builtinTheme = "/_/static/theme.css"

var themeTypes = map[string]string{".css": "text/css; charset=utf-8", ".woff2": "font/woff2"}

// openTheme opens the directory of a custom theme stylesheet and returns the
// URL of the stylesheet. An empty name selects the built-in theme.
func openTheme(rootPath, name string) (*os.Root, string, error) {
	if name == "" {
		return nil, builtinTheme, nil
	}
	file := name
	if !filepath.IsAbs(file) {
		file = filepath.Join(rootPath, file)
	}
	if info, err := os.Stat(file); err != nil || !info.Mode().IsRegular() || filepath.Ext(file) != ".css" {
		return nil, "", fmt.Errorf("theme %s is not a stylesheet", name)
	}
	dir, err := os.OpenRoot(filepath.Dir(file))
	if err != nil {
		return nil, "", err
	}
	return dir, "/_/theme/" + filepath.Base(file), nil
}

// themeFile serves the stylesheets and fonts below the theme's directory.
func (h *Handler) themeFile(w http.ResponseWriter, r *http.Request, name string) {
	contentType := themeTypes[path.Ext(name)]
	if h.theme == nil || contentType == "" || !fs.ValidPath(name) || !allowed(name) {
		http.NotFound(w, r)
		return
	}
	file, err := h.theme.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, name, time.Time{}, file)
}

// asset serves a pinned file. Versioned URLs let browsers keep them.
func (h *Handler) asset(w http.ResponseWriter, r *http.Request, name string) {
	if !fs.ValidPath(name) || !h.assetFiles[name] {
		http.NotFound(w, r)
		return
	}
	file, err := h.assets.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	switch path.Ext(name) {
	case ".js":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	case ".css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case ".woff2":
		w.Header().Set("Content-Type", "font/woff2")
	default:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, name, time.Time{}, file)
}

func writeJSON(w http.ResponseWriter, r *http.Request, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}

type indexNote struct {
	Path     string             `json:"path"`
	Name     string             `json:"name"`
	Title    string             `json:"title"`
	Aliases  []string           `json:"aliases"`
	Tags     []string           `json:"tags"`
	Headings []markdown.Heading `json:"headings"`
}

func (h *Handler) indexJSON(w http.ResponseWriter, r *http.Request, idx *index) {
	notes := make([]indexNote, 0, len(idx.notes))
	for _, n := range idx.notes {
		notes = append(notes, indexNote{n.path, n.name, n.doc.Title, nonNil(n.doc.Aliases), nonNil(n.doc.Tags), nonNil(n.doc.Headings)})
	}
	writeJSON(w, r, map[string]any{"vaults": h.vaults, "notes": notes})
}

func nonNil[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

func escapeText(s string) string { return html.EscapeString(s) }

// escapeFragmentText percent-encodes everything except unreserved characters,
// as text fragment directives require for "-", "&", and ",".
func escapeFragmentText(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
