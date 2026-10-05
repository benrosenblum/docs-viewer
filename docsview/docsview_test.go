package docsview

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// vaults are the fixture's vault directories.
var vaults = []string{"docs", "openspec"}

// fixture writes a small repository and returns an open viewer for it.
func fixture(t *testing.T, files map[string]string) (*Handler, string) {
	t.Helper()
	repo := writeRepo(t, files)
	return openViewer(t, repo), repo
}

// writeRepo writes the vault directories and files into a new directory
// with an asset directory next to it, and returns the repository path.
func writeRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	for _, vault := range vaults {
		if err := os.MkdirAll(filepath.Join(repo, vault), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range files {
		write(t, filepath.Join(repo, name), content)
	}
	assets := filepath.Join(dir, "assets")
	write(t, filepath.Join(assets, "mermaid/mermaid.min.js"), "window.mermaid = {};")
	write(t, filepath.Join(assets, "mermaid/unlisted.js"), "unlisted")
	return repo
}

// openViewer opens a viewer for a repository that writeRepo made.
func openViewer(t *testing.T, repo string) *Handler {
	t.Helper()
	assets := filepath.Join(filepath.Dir(repo), "assets")
	h, err := New(Config{Root: repo, Vaults: vaults, Assets: assets, AssetFiles: map[string]bool{"mermaid/mermaid.min.js": true}, AssetVersion: "0123456789abcdef"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	return h
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, h http.Handler, target string, want int) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.Code != want {
		t.Fatalf("GET %s: %d, want %d\n%s", target, rec.Code, want, rec.Body)
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "script-src 'self';") {
		t.Fatalf("GET %s: missing content security policy", target)
	}
	return rec
}

var sample = map[string]string{
	"docs/README.md":           "# Documentation\n\nSee [[plan]], [[plan#Second part|the second part]], [[missing note]], and the [contract](../api/openapi.yaml).\n",
	"docs/ref/plan.md":         "---\ntitle: The plan\naliases: [Roadmap]\ntags: [design]\n---\n\n# Plan\n\n## Second part\n\nBack to [the index](../README.md). A diagram:\n\n```mermaid\ngraph LR\n  A-->B\n```\n",
	"docs/ref/figure.png":      "\x89PNG fixture",
	"docs/.hidden.md":          "hidden note body",
	"api/openapi.yaml":         "openapi: 3.1.0\ninfo:\n  title: Fixture\n",
	"api/notes.md":             "# API notes\n",
	"openspec/specs/x/spec.md": "# Spec\n\nSee [the docs](../../../docs/README.md).\n",
	"node_modules/pkg/x.md":    "dependency",
	".git/config":              "secret",
	".local/data/save.db":      "save",
}

func TestRepositorySurface(t *testing.T) {
	h, repo := fixture(t, sample)
	// Links that leave the repository, or lead to hidden files, are not served.
	for link, target := range map[string]string{"docs/escape.md": filepath.Join(filepath.Dir(repo), "assets/mermaid/unlisted.js"), "docs/cfg.txt": "../.git/config", "docs/git": "../.git"} {
		if err := os.Symlink(target, filepath.Join(repo, link)); err != nil {
			t.Fatal(err)
		}
	}
	if location := get(t, h, "/", 302).Header().Get("Location"); location != "/docs/README.md" {
		t.Fatal("home redirect:", location)
	}
	readme := get(t, h, "/docs/README.md", 200).Body.String()
	for _, want := range []string{`data-kind="note"`, `<h1 id="documentation"`, `href="/docs/ref/plan.md"`, `href="/docs/ref/plan.md#second-part"`, "is-unresolved", `href="/api/openapi.yaml"`, `class="tree-item is-active"`, `<a class="vault-name" href="/" title="repo">repo</a>`, `data-path="docs"><details open>`, `data-path="openspec"><details>`, `Linked mentions <span class="count">2</span>`} {
		if !strings.Contains(readme, want) {
			t.Errorf("README page lacks %s", want)
		}
	}
	if strings.Contains(readme, "cfg.txt") {
		t.Error("file tree lists a link to a hidden file")
	}
	plan := get(t, h, "/docs/ref/plan.md", 200).Body.String()
	for _, want := range []string{"<title>The plan · docs</title>", `class="properties"`, `data-level="2"`, `pre class="mermaid"`, `Linked mentions <span class="count">1</span>`, `<details open><summary class="tree-item"><span class="tree-name">ref</span>`} {
		if !strings.Contains(plan, want) {
			t.Errorf("plan page lacks %s", want)
		}
	}
	// OpenSpec notes are vault notes; other Markdown files are outside notes.
	spec := get(t, h, "/openspec/specs/x/spec.md", 200).Body.String()
	for _, want := range []string{"<title>Spec · openspec</title>", `href="/docs/README.md"`, `data-path="openspec"><details open>`, `class="tree-item is-active" href="/openspec/specs/x/spec.md"`} {
		if !strings.Contains(spec, want) {
			t.Errorf("OpenSpec page lacks %s", want)
		}
	}
	if strings.Contains(spec, "badge-outside") {
		t.Error("OpenSpec note is marked as outside the vault")
	}
	if outside := get(t, h, "/api/notes.md", 200).Body.String(); !strings.Contains(outside, "<title>API notes · repo</title>") || !strings.Contains(outside, `title="This repository file is outside docs/ and openspec/">Outside vault</span>`) {
		t.Error("outside note lacks its label")
	}
	if location := get(t, h, "/docs/ref", 301).Header().Get("Location"); location != "/docs/ref/" {
		t.Fatal("folder redirect:", location)
	}
	if folder := get(t, h, "/docs/ref/", 200).Body.String(); !strings.Contains(folder, `href="/docs/ref/plan.md"`) || !strings.Contains(folder, "figure.png") {
		t.Error("folder listing")
	}
	source := get(t, h, "/api/openapi.yaml", 200).Body.String()
	if !strings.Contains(source, `class="source-view" data-lang="yaml"`) || !strings.Contains(source, "3 lines") || strings.Contains(source, " style=") {
		t.Error("source view")
	}
	raw := get(t, h, "/api/openapi.yaml?raw=1", 200)
	if raw.Header().Get("Content-Type") != "text/plain; charset=utf-8" || raw.Body.String() != sample["api/openapi.yaml"] {
		t.Error("raw file")
	}
	if image := get(t, h, "/docs/ref/figure.png", 200); image.Header().Get("Content-Type") != "image/png" || image.Body.String() != sample["docs/ref/figure.png"] {
		t.Error("image")
	}
	asset := get(t, h, "/_/assets/0123456789abcdef/mermaid/mermaid.min.js", 200)
	if !strings.Contains(asset.Header().Get("Cache-Control"), "immutable") || asset.Header().Get("Content-Type") != "text/javascript; charset=utf-8" {
		t.Error("asset headers")
	}
	for _, static := range []string{"viewer.js", "viewer.css", "theme.js", "graph.js", "icons.svg", "highlight.css"} {
		get(t, h, "/_/static/"+static, 200)
	}
	for _, missing := range []string{"/.git/config", "/.local/data/save.db", "/node_modules/pkg/x.md", "/docs/.hidden.md", "/docs/escape.md", "/docs/cfg.txt", "/docs/cfg.txt?raw=1", "/docs/git/config", "/docs/ref/plan.md/", "/docs/../go.mod", "/docs//README.md", "/_/nothing", "/_/static/missing.js", "/_/static/../viewer.js", "/_/assets/0123456789abcdef/mermaid/unlisted.js", "/_/assets/ffffffffffffffff/mermaid/mermaid.min.js", "/missing.md"} {
		get(t, h, missing, 404)
	}
	if strings.Contains(get(t, h, "/docs/.hidden.md", 404).Body.String(), sample["docs/.hidden.md"]) {
		t.Error("hidden file content leaked")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/docs/README.md", nil))
	if rec.Code != 405 || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Fatal("method admission")
	}
	// A saved edit appears on the next request, including new backlinks.
	write(t, filepath.Join(repo, "docs/ref/plan.md"), "# Plan, revised\n")
	write(t, filepath.Join(repo, "docs/new.md"), "Links to [[plan]].\n")
	plan = get(t, h, "/docs/ref/plan.md", 200).Body.String()
	if !strings.Contains(plan, "Plan, revised") || !strings.Contains(plan, `Linked mentions <span class="count">2</span>`) {
		t.Error("stale page after an edit")
	}
}

// Vault notes are rendered once per snapshot, so a change to an outside file
// that a note links to must make a new snapshot.
func TestOutsideFileChanges(t *testing.T) {
	h, repo := fixture(t, map[string]string{"docs/README.md": "See [the code](../src/x.go).\n"})
	const unresolved = `is-unresolved" href="/src/x.go"`
	before, _, err := h.versions("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(get(t, h, "/docs/README.md", 200).Body.String(), unresolved) {
		t.Fatal("link to a missing outside file is not unresolved")
	}
	write(t, filepath.Join(repo, "src/x.go"), "package x\n")
	after, _, err := h.versions("")
	if err != nil {
		t.Fatal(err)
	}
	if after == before {
		t.Error("snapshot version did not change")
	}
	if strings.Contains(get(t, h, "/docs/README.md", 200).Body.String(), unresolved) {
		t.Error("link stays unresolved after the outside file was created")
	}
}

func TestNewRequiresVault(t *testing.T) {
	repo := t.TempDir()
	for _, dir := range []string{"docs/sub", "openspec"} {
		if err := os.MkdirAll(filepath.Join(repo, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for name, dirs := range map[string][]string{
		"no vault":                      nil,
		"missing vault":                 {"docs", "missing"},
		"vault outside the repository":  {"../docs"},
		"repository root as a vault":    {"."},
		"vault inside another vault":    {"docs", "docs/sub"},
		"vault that contains the other": {"docs/sub", "docs/"},
		"repeated vault":                {"openspec", "openspec"},
	} {
		if h, err := New(Config{Root: repo, Vaults: dirs, Assets: t.TempDir()}); err == nil {
			h.Close()
			t.Errorf("%s accepted", name)
		}
	}
}

func TestWikilinkResolution(t *testing.T) {
	h, _ := fixture(t, map[string]string{
		"docs/a.md":               "",
		"docs/notes/a.md":         "",
		"docs/notes/b.md":         "",
		"docs/deep/er/b.md":       "",
		"docs/deep/er/Mixed.md":   "",
		"docs/img/figure.png":     "",
		"docs/notes/v1.2.md":      "",
		"docs/specs/spec.md":      "",
		"openspec/specs/spec.md":  "",
		"openspec/changes/c/x.md": "",
	})
	idx, err := h.current()
	if err != nil {
		t.Fatal(err)
	}
	r := &resolver{h: h, idx: idx}
	for _, tt := range []struct{ from, target, want string }{
		{"docs/notes/b.md", "a", "docs/notes/a.md"},     // Same folder first.
		{"docs/deep/er/b.md", "a", "docs/a.md"},         // Then the shortest path.
		{"docs/a.md", "b", "docs/notes/b.md"},           // Shortest of two matches.
		{"docs/a.md", "deep/er/b", "docs/deep/er/b.md"}, // Vault path.
		{"docs/a.md", "er/b", "docs/deep/er/b.md"},      // Unique path suffix.
		{"docs/a.md", "mixed", "docs/deep/er/Mixed.md"},
		{"docs/a.md", "figure.png", "docs/img/figure.png"},
		{"docs/a.md", "v1.2", "docs/notes/v1.2.md"},
		{"docs/a.md", "notes/a.md", "docs/notes/a.md"},
		{"docs/notes/b.md", "../a", "docs/a.md"},
		{"docs/a.md", "../openspec/specs/spec", "openspec/specs/spec.md"},
		{"docs/notes/b.md", "specs/spec", "docs/specs/spec.md"},              // The linking vault first.
		{"openspec/changes/c/x.md", "specs/spec", "openspec/specs/spec.md"},  // Also for other vaults.
		{"api/x.md", "specs/spec", "docs/specs/spec.md"},                     // Else the first vault.
		{"docs/notes/b.md", "openspec/specs/spec", "openspec/specs/spec.md"}, // Then the repository root.
		{"openspec/specs/spec.md", "docs/a", "docs/a.md"},
		{"docs/a.md", "missing", ""},
		{"docs/a.md", "../.git/config", ""},
	} {
		got, ok := r.ResolveWikilink(tt.from, tt.target)
		if got != tt.want || ok != (tt.want != "") {
			t.Errorf("ResolveWikilink(%q, %q) = %q, %t; want %q", tt.from, tt.target, got, ok, tt.want)
		}
	}
}

func TestSearchAndGraph(t *testing.T) {
	h, _ := fixture(t, sample)
	var result searchResult
	decode(t, get(t, h, "/_/api/search?q=diagram+tag:%23design", 200), &result)
	if result.Total != 1 || result.Results[0].Path != "docs/ref/plan.md" || len(result.Results[0].Matches) != 1 || result.Results[0].Matches[0].Line != 11 {
		t.Fatalf("search: %+v", result)
	}
	decode(t, get(t, h, `/_/api/search?q=%22second+part%22+path:ref`, 200), &result)
	if result.Total != 1 || result.Terms[0] != "second part" {
		t.Fatalf("phrase search: %+v", result)
	}
	decode(t, get(t, h, "/_/api/search?q=roadmap", 200), &result)
	if result.Total != 1 || result.Results[0].Score < 10 {
		t.Fatalf("alias search: %+v", result)
	}
	decode(t, get(t, h, "/_/api/search?q=the+docs+path:openspec/", 200), &result)
	if result.Total != 1 || result.Results[0].Path != "openspec/specs/x/spec.md" {
		t.Fatalf("OpenSpec search: %+v", result)
	}
	decode(t, get(t, h, "/_/api/search?q=", 200), &result)
	if result.Total != 0 || result.Results == nil {
		t.Fatal("empty query")
	}
	if page := get(t, h, "/_/search?q=diagram", 200).Body.String(); !strings.Contains(page, `class="search-result"`) {
		t.Fatal("search page")
	}
	var index struct {
		Notes []indexNote `json:"notes"`
	}
	decode(t, get(t, h, "/_/api/index", 200), &index)
	if len(index.Notes) != 3 || index.Notes[2].Path != "openspec/specs/x/spec.md" || index.Notes[1].Title != "The plan" || index.Notes[1].Aliases[0] != "Roadmap" || len(index.Notes[1].Headings) != 2 {
		t.Fatalf("index: %+v", index)
	}
	var g graph
	decode(t, get(t, h, "/_/api/graph", 200), &g)
	kinds := map[string]string{}
	for _, n := range g.Nodes {
		kinds[n.ID] = n.Kind
	}
	if kinds["docs/README.md"] != "note" || kinds["openspec/specs/x/spec.md"] != "note" || kinds["unresolved:missing note"] != "unresolved" || len(g.Links) != 4 {
		t.Fatalf("graph: %+v %+v", kinds, g.Links)
	}
	if get(t, h, "/_/graph", 200).Body.String() == "" {
		t.Fatal("graph page")
	}
}

// OpenSpec repeats file names such as spec.md, so graph nodes and backlinks
// that share a name add their folder.
func TestSharedNameLabels(t *testing.T) {
	h, _ := fixture(t, map[string]string{
		"docs/README.md":                     "See [[a/spec]], [[b/spec]], and [[plan]].\n",
		"docs/plan.md":                       "",
		"openspec/specs/a/spec.md":           "Back to [[README]].\n",
		"openspec/changes/c/specs/b/spec.md": "Back to [[README]].\n",
	})
	var g graph
	decode(t, get(t, h, "/_/api/graph", 200), &g)
	names := map[string]string{}
	for _, n := range g.Nodes {
		names[n.ID] = n.Name
	}
	if names["openspec/specs/a/spec.md"] != "a/spec" || names["openspec/changes/c/specs/b/spec.md"] != "b/spec" || names["docs/plan.md"] != "plan" {
		t.Fatalf("graph names: %v", names)
	}
	readme := get(t, h, "/docs/README.md", 200).Body.String()
	for _, want := range []string{`data-path="openspec/specs/a/spec.md">a/spec</a>`, `data-path="openspec/changes/c/specs/b/spec.md">b/spec</a>`} {
		if !strings.Contains(readme, want) {
			t.Errorf("README backlinks lack %s", want)
		}
	}
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, value any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), value); err != nil {
		t.Fatal(err)
	}
}

func TestLiveReloadEvents(t *testing.T) {
	h, repo := fixture(t, sample)
	ticks := make(chan time.Time)
	h.ticker = func() (<-chan time.Time, func()) { return ticks, func() {} }
	server := httptest.NewServer(h)
	defer server.Close()
	response, err := http.Get(server.URL + "/_/events?path=docs/ref/plan.md")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal("event stream content type")
	}
	events := bufio.NewReader(response.Body)
	next := func() (string, map[string]any) {
		t.Helper()
		var name string
		for {
			line, err := events.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			line = strings.TrimSpace(line)
			if value, ok := strings.CutPrefix(line, "event: "); ok {
				name = value
			} else if value, ok := strings.CutPrefix(line, "data: "); ok {
				var data map[string]any
				if err := json.Unmarshal([]byte(value), &data); err != nil {
					t.Fatal(err)
				}
				return name, data
			}
		}
	}
	if name, _ := next(); name != "hello" {
		t.Fatal("first event:", name)
	}
	// A tick send returns before the scan ends, and reading its event is the
	// only proof that the scan is complete. Edits therefore wait for an event.
	write(t, filepath.Join(repo, "docs/README.md"), "# Index\n")
	ticks <- time.Time{}
	if name, data := next(); name != "change" || data["tree"] != true || data["page"] != false {
		t.Fatalf("unrelated edit: %s %v", name, data)
	}
	write(t, filepath.Join(repo, "docs/ref/plan.md"), "# Plan\n\nEdited.\n")
	ticks <- time.Time{}
	if name, data := next(); name != "change" || data["tree"] != true || data["page"] != true {
		t.Fatalf("viewed edit: %s %v", name, data)
	}
	// No change, and a change outside the vault and view, send no event, so
	// the next event is the one for the OpenSpec note.
	ticks <- time.Time{}
	write(t, filepath.Join(repo, "api/notes.md"), "# API notes, edited\n")
	ticks <- time.Time{}
	write(t, filepath.Join(repo, "openspec/specs/x/spec.md"), "# Spec, edited\n")
	ticks <- time.Time{}
	if name, data := next(); name != "change" || data["tree"] != true || data["page"] != false {
		t.Fatalf("file outside the vault and view: %s %v", name, data)
	}
}

func TestTheme(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	write(t, filepath.Join(repo, "docs/README.md"), "# Home\n")
	write(t, filepath.Join(repo, "openspec/x.md"), "# X\n")
	write(t, filepath.Join(repo, "ui/brand.css"), `@import url("tokens.css");`)
	write(t, filepath.Join(repo, "ui/tokens.css"), ":root { --accent: #fff; }")
	write(t, filepath.Join(repo, "ui/fonts/Text.woff2"), "font")
	write(t, filepath.Join(repo, "ui/fonts/notes.txt"), "not a font")
	write(t, filepath.Join(repo, "ui/theme.ts"), "export {}")
	write(t, filepath.Join(repo, "ui/.hidden.css"), "")
	assets := filepath.Join(dir, "assets")
	if err := os.MkdirAll(assets, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, theme := range map[string]string{"a missing file": "ui/none.css", "a directory": "ui", "a file that is not a stylesheet": "ui/theme.ts"} {
		if _, err := New(Config{Root: repo, Vaults: vaults, Assets: assets, Theme: theme}); err == nil {
			t.Errorf("accepted %s as the theme", name)
		}
	}
	// A relative path is below Root. An absolute path can be in any directory.
	for _, theme := range []string{"ui/brand.css", filepath.Join(repo, "ui/brand.css")} {
		h, err := New(Config{Root: repo, Vaults: vaults, Assets: assets, Theme: theme})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { h.Close() })

		body := get(t, h, "/docs/README.md", 200).Body.String()
		at, viewer := strings.Index(body, `href="/_/theme/brand.css"`), strings.Index(body, `href="/_/static/viewer.css"`)
		if at < 0 || at > viewer || strings.Contains(body, builtinTheme) {
			t.Fatalf("theme %s: the page does not load only the theme, then viewer.css", theme)
		}
		for target, want := range map[string]string{"/_/theme/brand.css": "text/css; charset=utf-8", "/_/theme/tokens.css": "text/css; charset=utf-8", "/_/theme/fonts/Text.woff2": "font/woff2"} {
			if got := get(t, h, target, 200).Header().Get("Content-Type"); got != want {
				t.Errorf("%s type %q, want %q", target, got, want)
			}
		}
		for _, missing := range []string{"/_/theme/theme.ts", "/_/theme/fonts/notes.txt", "/_/theme/.hidden.css", "/_/theme/fonts/../tokens.css", "/_/theme/../docs/README.md", "/_/theme/none.css"} {
			get(t, h, missing, 404)
		}
	}

	plain, _ := fixture(t, map[string]string{"docs/README.md": "# Home\n"})
	if body := get(t, plain, "/docs/README.md", 200).Body.String(); !strings.Contains(body, `href="`+builtinTheme+`"`) || strings.Contains(body, "/_/theme/") {
		t.Fatal("page without a theme does not load the built-in theme")
	}
	if rec := get(t, plain, builtinTheme, 200); rec.Header().Get("Content-Type") != "text/css; charset=utf-8" {
		t.Fatalf("built-in theme type %q", rec.Header().Get("Content-Type"))
	}
	get(t, plain, "/_/theme/brand.css", 404)
}

// TestThemeProperties checks that viewer.css and the built-in theme set each
// custom property that the viewer reads without a fallback.
func TestThemeProperties(t *testing.T) {
	read := func(names ...string) string {
		var b strings.Builder
		for _, name := range names {
			data, err := embedded.ReadFile("static/" + name)
			if err != nil {
				t.Fatal(err)
			}
			b.Write(data)
		}
		return b.String()
	}
	css, js := read("viewer.css", "theme.css"), read("viewer.js", "graph.js", "theme.js")
	set := map[string]bool{}
	for _, m := range regexp.MustCompile(`(--[a-z0-9-]+)\s*:`).FindAllStringSubmatch(css, -1) {
		set[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`setProperty\('(--[a-z0-9-]+)'`).FindAllStringSubmatch(js, -1) {
		set[m[1]] = true
	}
	reads := regexp.MustCompile(`var\((--[a-z0-9-]+)\)`).FindAllStringSubmatch(css, -1)
	reads = append(reads, regexp.MustCompile(`'(--[a-z0-9-]+)'`).FindAllStringSubmatch(js, -1)...)
	if len(reads) < 100 {
		t.Fatalf("found only %d reads", len(reads))
	}
	for _, m := range reads {
		if !set[m[1]] {
			t.Errorf("no stylesheet sets %s", m[1])
			set[m[1]] = true
		}
	}
}
