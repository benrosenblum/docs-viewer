package docsview

import (
	"path/filepath"
	"strings"
	"testing"
)

// specFiles is a repository with an apispec vault and notes that link to it.
var specFiles = map[string]string{
	"docs/README.md":     "# Documentation\n",
	"docs/USAGE.md":      "# Usage\n\nThe [consumer API](../apispec/app.yaml), [[apispec/admin.yaml]], and [[apispec/missing.yaml]].\n",
	"apispec/app.yaml":   "openapi: 3.1.0\ninfo:\n  title: Consumer\n",
	"apispec/admin.yaml": "openapi: 3.1.0\ninfo:\n  title: Operator\n",
	"api/other.yaml":     "openapi: 3.1.0\n",
}

// specViewer opens a viewer with the apispec vault.
func specViewer(t *testing.T) *Handler {
	t.Helper()
	repo := writeRepo(t, specFiles)
	h, err := New(Config{Root: repo, Vaults: append(vaults[:len(vaults):len(vaults)], "apispec"),
		Assets: filepath.Join(filepath.Dir(repo), "assets"), AssetVersion: "0123456789abcdef", SpecVault: "apispec"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	return h
}

func TestSpecVaultTreeAndLinks(t *testing.T) {
	h := specViewer(t)
	home := get(t, h, "/docs/README.md", 200).Body.String()
	for _, want := range []string{`data-path="apispec"`, `href="/apispec/app.yaml" data-path="apispec/app.yaml"`, `href="/apispec/admin.yaml" data-path="apispec/admin.yaml"`} {
		if !strings.Contains(home, want) {
			t.Errorf("tree has no %s", want)
		}
	}
	note := get(t, h, "/docs/USAGE.md", 200).Body.String()
	for _, want := range []string{
		`<a class="internal-link" href="/apispec/app.yaml" data-path="apispec/app.yaml">consumer API</a>`,
		`<a class="internal-link" href="/apispec/admin.yaml" data-path="apispec/admin.yaml">`,
		`<a class="internal-link is-unresolved" href="/docs/apispec/missing.yaml" data-path="" data-target="apispec/missing.yaml">`,
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note has no link %s", want)
		}
	}
}

func TestSpecPage(t *testing.T) {
	h := specViewer(t)
	w := get(t, h, "/apispec/app.yaml", 200)
	body := w.Body.String()
	for _, want := range []string{
		`data-kind="apispec"`,
		`<script defer src="/_/assets/0123456789abcdef/redoc/redoc.standalone.js"></script>`,
		`<div id="redoc" class="apispec" data-spec="/apispec/app.yaml?raw=1">`,
		`<a href="/apispec/app.yaml?diff=off" aria-current="page">Reference</a><a href="/apispec/app.yaml?diff=off&amp;view=source">Source</a>`,
		`<a class="internal-link" href="/docs/USAGE.md" data-path="docs/USAGE.md">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("spec page has no %s", want)
		}
	}
	if got := w.Header().Get("Content-Security-Policy"); got != contentPolicy+"; worker-src 'self' blob:" {
		t.Errorf("spec page policy = %q", got)
	}

	raw := get(t, h, "/apispec/app.yaml?raw=1", 200)
	if raw.Body.String() != specFiles["apispec/app.yaml"] || raw.Header().Get("Content-Security-Policy") != contentPolicy {
		t.Errorf("raw spec = %q, policy %q", raw.Body.String(), raw.Header().Get("Content-Security-Policy"))
	}

	source := get(t, h, "/apispec/app.yaml?view=source", 200)
	for _, want := range []string{`data-kind="source"`, `<a href="/apispec/app.yaml?diff=off">Reference</a><a href="/apispec/app.yaml?diff=off&amp;view=source" aria-current="page">Source</a>`} {
		if !strings.Contains(source.Body.String(), want) {
			t.Errorf("source view has no %s", want)
		}
	}
	if source.Header().Get("Content-Security-Policy") != contentPolicy {
		t.Errorf("source view policy = %q", source.Header().Get("Content-Security-Policy"))
	}

	for _, page := range []string{"/docs/README.md", "/apispec/app.yaml?view=source", "/api/other.yaml"} {
		if strings.Contains(get(t, h, page, 200).Body.String(), "redoc") {
			t.Errorf("%s loads Redoc", page)
		}
	}
	other := get(t, h, "/api/other.yaml", 200).Body.String()
	if !strings.Contains(other, `data-kind="source"`) || strings.Contains(other, "Reference") {
		t.Error("a YAML file outside the spec vault is not a plain source page")
	}
}

func TestSpecVaultMustBeAVault(t *testing.T) {
	repo := writeRepo(t, specFiles)
	if _, err := New(Config{Root: repo, Vaults: []string{"docs"}, Assets: repo, SpecVault: "apispec"}); err == nil {
		t.Fatal("New accepts a spec directory that is not a vault")
	}
}
