package docsview

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// diffSample is a committed repository for the diff page tests.
var diffSample = map[string]string{
	"docs/README.md":        "# Documentation\n\nSee [[USAGE]] and [[CLI]].\n",
	"docs/USAGE.md":         "# Usage\n\nThe pool holds four connections.\n\nOther text.\n",
	"docs/CLI.md":           "# CLI\n\nThe tool prints rows.\n",
	"docs/figure.png":       "\x89PNG\x00one",
	"internal/pool/pool.go": "package pool\n\nconst maxOpenFiles = 4\n",
	"openspec/README.md":    "# OpenSpec\n",
}

func contains(t *testing.T, name, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("%s lacks %s", name, w)
		}
	}
}

func TestDiffMode(t *testing.T) {
	h, repo := gitFixture(t, diffSample)
	write(t, filepath.Join(repo, "docs/USAGE.md"), "# Usage\n\nThe pool holds eight connections.\n\nOther text.\n")

	// A changed note opens in the rendered diff against HEAD.
	page := get(t, h, "/docs/USAGE.md", 200).Body.String()
	contains(t, "changed note", page, `data-kind="diff"`, `<p class="diff-removed" data-change="">The pool holds <del class="diff-word">four</del> connections.</p>`,
		`<p class="diff-added">The pool holds <ins class="diff-word">eight</ins> connections.</p>`, "<p>Other text.</p>", `<span class="diff-count">1 change</span>`, `data-action="diff-prev"`, `data-action="diff-next"`,
		`href="/docs/USAGE.md?diff=off">Page</a>`, `data-base="HEAD"`, "<span>Uncommitted</span>", `href="/docs/USAGE.md?as=source&amp;diff=HEAD">Source</a>`,
		`data-level="1"><a href="#usage">Usage</a>`)
	// A clean note opens as the normal page, with a picker and no toggle.
	page = get(t, h, "/docs/CLI.md", 200).Body.String()
	contains(t, "clean note", page, `data-kind="note"`, `data-action="diff-picker"`, "<span>Compare</span>")
	if strings.Contains(page, `aria-label="Page or changes"`) {
		t.Error("clean note has the page and changes toggle")
	}
	// diff=off shows the normal page; the toggle returns to the bare URL.
	page = get(t, h, "/docs/USAGE.md?diff=off", 200).Body.String()
	contains(t, "normal page", page, `data-kind="note"`, `href="/docs/USAGE.md">Changes</a>`, `aria-current="page">Page</a>`)
	// The source diff of the note.
	page = get(t, h, "/docs/USAGE.md?diff=HEAD&as=source", 200).Body.String()
	contains(t, "source diff", page, `class="diff-table chroma"`, `<del class="diff-word">four</del>`, `data-as="source"`, `<span class="diff-count">1 change</span>`)
	// An explicit base on a clean file says that nothing changed, with no count.
	page = get(t, h, "/docs/CLI.md?diff=HEAD", 200).Body.String()
	contains(t, "clean file", page, "The file has no changes against HEAD.")
	if strings.Contains(page, "diff-nav") {
		t.Error("clean file has a change count and its buttons")
	}

	// Invalid and unknown bases get status 400 in the viewer layout.
	for _, target := range []string{"/docs/USAGE.md?diff=0000000", "/docs/USAGE.md?diff=--output%3D%2Ftmp%2Fx", "/docs/USAGE.md?diff=HEAD~1", "/docs/USAGE.md?diff="} {
		page := get(t, h, target, 400).Body.String()
		contains(t, target, page, `class="render-error"`, `class="view-header"`)
	}
	contains(t, "unknown commit", get(t, h, "/docs/USAGE.md?diff=0000000", 400).Body.String(), "The base 0000000 is not a known commit.")

	// An untracked note shows as added.
	write(t, filepath.Join(repo, "docs/NEW.md"), "# New\n\nFresh text.\n")
	page = get(t, h, "/docs/NEW.md", 200).Body.String()
	contains(t, "untracked note", page, `<h1 class="diff-added" data-change="" id="new">New</h1>`, `<p class="diff-added">Fresh text.</p>`, `<span class="diff-count">1 change</span>`)

	// A file outside the vault gets the source diff; an image stays raw.
	write(t, filepath.Join(repo, "internal/pool/pool.go"), "package pool\n\nconst maxOpenFiles = 8\n")
	contains(t, "Go file", get(t, h, "/internal/pool/pool.go", 200).Body.String(), `data-kind="diff"`, `<span class="mi"><ins class="diff-word">8</ins></span>`)
	write(t, filepath.Join(repo, "docs/figure.png"), "\x89PNG\x00two")
	if rec := get(t, h, "/docs/figure.png", 200); rec.Header().Get("Content-Type") != "image/png" {
		t.Error("changed image is not raw")
	}
	contains(t, "changed image", get(t, h, "/docs/figure.png?diff=HEAD", 200).Body.String(), msgBinary)

	// A note above the rendered diff limit falls back to the source diff.
	large := "# Usage\n\n" + strings.Repeat("A long line of text.\n", maxRenderedDiff/20)
	write(t, filepath.Join(repo, "docs/USAGE.md"), large)
	contains(t, "large note", get(t, h, "/docs/USAGE.md", 200).Body.String(), "The note is too large for the rendered diff.", `class="diff-table chroma"`)

	// A changed property marks the properties section, which is a change.
	write(t, filepath.Join(repo, "docs/CLI.md"), "---\nstatus: final\n---\n# CLI\n\nThe tool prints more rows.\n")
	contains(t, "changed property", get(t, h, "/docs/CLI.md", 200).Body.String(), `<div class="diff-properties diff-changed" data-change>`, `<span class="property-text">final</span>`,
		`<span class="diff-count">2 changes</span>`)

	// After a commit, the bare URL shows the normal note.
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "Edit")
	contains(t, "committed note", get(t, h, "/docs/CLI.md", 200).Body.String(), `data-kind="note"`)
}

func TestDiffWithoutGit(t *testing.T) {
	h, _ := fixture(t, sample)
	for _, target := range []string{"/docs/README.md", "/docs/README.md?diff=HEAD", "/docs/README.md?diff=--x"} {
		page := get(t, h, target, 200).Body.String()
		contains(t, target, page, `data-kind="note"`)
		if strings.Contains(page, "diff-picker") || strings.Contains(page, "data-git") || strings.Contains(page, `class="branch"`) {
			t.Errorf("%s has diff controls or a branch", target)
		}
	}
	get(t, h, "/_/api/history?path=docs/README.md", 404)
}

func TestHistoryAPI(t *testing.T) {
	h, repo := gitFixture(t, diffSample)
	write(t, filepath.Join(repo, "docs/USAGE.md"), "# Usage\n\nChanged.\n")
	runGit(t, repo, "commit", "-q", "-am", "Change usage")
	history := func(p string) historyResponse {
		t.Helper()
		var r historyResponse
		if err := json.Unmarshal(get(t, h, "/_/api/history?path="+p, 200).Body.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	r := history("docs/USAGE.md")
	if r.Branch != "main" || r.Main != "main" || r.BranchBase || len(r.Commits) != 2 || r.Commits[0].Subject != "Change usage" || r.Commits[1].Subject != "Initial commit" || r.Commits[0].Path != "docs/USAGE.md" {
		t.Fatalf("history on main: %+v", r)
	}
	runGit(t, repo, "switch", "-q", "-c", "feature-docs")
	runGit(t, repo, "mv", "docs/USAGE.md", "docs/SITE.md")
	runGit(t, repo, "commit", "-q", "-m", "Rename")
	r = history("docs/SITE.md")
	if r.Branch != "feature-docs" || r.Main != "main" || !r.BranchBase || len(r.Commits) != 3 || r.Commits[2].Path != "docs/USAGE.md" {
		t.Fatalf("history of a renamed file on a branch: %+v", r)
	}
	// Branch changes compare with the merge base, where the file had its old name.
	contains(t, "branch changes", get(t, h, "/docs/SITE.md?diff=branch", 200).Body.String(), "The file has no changes against the merge base with main.")
	get(t, h, "/_/api/history?path=.env", 404)
	get(t, h, "/_/api/history?path=docs/missing.md", 404)
}

func TestTreeMarks(t *testing.T) {
	h, repo := gitFixture(t, diffSample)
	page := get(t, h, "/docs/README.md", 200).Body.String()
	if strings.Contains(page, "data-git") {
		t.Error("clean tree has marks")
	}
	write(t, filepath.Join(repo, "docs/USAGE.md"), "# Usage, edited\n")
	write(t, filepath.Join(repo, "docs/ref/NEW.md"), "# New\n")
	write(t, filepath.Join(repo, "docs/STAGED.md"), "# Staged\n")
	runGit(t, repo, "add", "docs/STAGED.md")
	page = get(t, h, "/docs/README.md", 200).Body.String()
	contains(t, "tree", page, `data-path="docs/USAGE.md" data-git="M"`, `data-path="docs/ref/NEW.md" data-git="U"`, `data-path="docs/STAGED.md" data-git="A"`,
		`<li class="tree-folder" data-path="docs" data-git-dirty>`, `<li class="tree-folder" data-path="docs/ref" data-git-dirty>`, `<li class="tree-folder" data-path="openspec"><details>`)
}

func TestBranch(t *testing.T) {
	h, repo := gitFixture(t, diffSample)
	check := func(ref string) {
		t.Helper()
		contains(t, "note on "+ref, get(t, h, "/docs/USAGE.md", 200).Body.String(), "<title>Usage · docs · "+ref+"</title>", `<span class="branch-name">`+ref+"</span>")
		// A page with no file controls shows the branch also.
		contains(t, "search on "+ref, get(t, h, "/_/search", 200).Body.String(), "<title>Search · repo · "+ref+"</title>", `<span class="branch-name">`+ref+"</span>")
	}
	check("main")
	runGit(t, repo, "switch", "-q", "-c", "feature/docs")
	check("feature/docs")
	// A detached HEAD shows the short hash.
	runGit(t, repo, "switch", "-q", "--detach")
	check(runGit(t, repo, "rev-parse", "HEAD")[:7])
}

func TestLiveReloadFromGit(t *testing.T) {
	h, repo := gitFixture(t, diffSample)
	ticks := make(chan time.Time)
	h.ticker = func() (<-chan time.Time, func()) { return ticks, func() {} }
	server := httptest.NewServer(h)
	defer server.Close()
	write(t, filepath.Join(repo, "docs/NEW.md"), "# New\n")
	client := &http.Client{Timeout: 10 * time.Second} // A missing event fails the test.
	response, err := client.Get(server.URL + "/_/events?path=docs/NEW.md")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	events := bufio.NewReader(response.Body)
	next := func() map[string]any {
		t.Helper()
		for {
			line, err := events.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if value, ok := strings.CutPrefix(strings.TrimSpace(line), "data: "); ok {
				var data map[string]any
				if err := json.Unmarshal([]byte(value), &data); err != nil {
					t.Fatal(err)
				}
				return data
			}
		}
	}
	next() // hello
	// Each git operation changes no vault file but sends an event: the stage
	// changes the mark from untracked to added, the commit removes it, and
	// the switch changes the branch.
	for _, step := range [][]string{{"add", "docs/NEW.md"}, {"commit", "-q", "-m", "Add"}, {"switch", "-q", "-c", "other"}} {
		runGit(t, repo, step...)
		ticks <- time.Time{}
		if data := next(); data["tree"] != true && data["page"] != true {
			t.Fatalf("git %s: %v", step[0], data)
		}
	}
}
