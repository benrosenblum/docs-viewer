package docsview

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitFixture writes files, commits them on main in a new repository with
// fixed names and dates, and returns an open viewer for it. It skips the
// test when git is not on PATH.
func gitFixture(t *testing.T, files map[string]string) (*Handler, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	repo := writeRepo(t, files)
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "Initial commit")
	h := openViewer(t, repo)
	if h.git == nil {
		t.Fatal("git detection failed:", h.gitErr)
	}
	return h, repo
}

// runGit runs a git command in dir without user or system configuration.
// Each commit gets a date one minute after the one before.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	gitClock++
	date := fmt.Sprintf("@%d +0000", 1767225600+60*gitClock)
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(gitEnv(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Ada Author", "GIT_AUTHOR_EMAIL=ada@example.com", "GIT_AUTHOR_DATE="+date,
		"GIT_COMMITTER_NAME=Cy Committer", "GIT_COMMITTER_EMAIL=cy@example.com", "GIT_COMMITTER_DATE="+date,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// gitClock orders commit dates. Tests that use it do not run in parallel.
var gitClock int64

func TestGitDetection(t *testing.T) {
	h, repo := gitFixture(t, map[string]string{"docs/README.md": "# Home\n"})
	if h.gitErr != nil || h.GitError() != nil || h.git.prefix != "" {
		t.Fatalf("detection: %v, prefix %q", h.gitErr, h.git.prefix)
	}
	// A viewer rooted in a subdirectory gives git top-level paths.
	sub := filepath.Join(repo, "docs")
	write(t, filepath.Join(sub, "docs/x.md"), "x")
	write(t, filepath.Join(sub, "openspec/y.md"), "y")
	g, err := openGit(sub)
	if err != nil || g.prefix != "docs/" || g.topPath("docs/x.md") != "docs/docs/x.md" {
		t.Fatalf("subdirectory: %v %q", err, g.prefix)
	}

	plain := openViewer(t, writeRepo(t, map[string]string{"docs/README.md": "# Home\n"}))
	if plain.git != nil || plain.GitError() == nil {
		t.Fatal("a directory outside a work tree enables git")
	}
	t.Setenv("PATH", "")
	none := openViewer(t, repo)
	if none.git != nil || !errors.Is(none.GitError(), exec.ErrNotFound) {
		t.Fatalf("no git on PATH: %v", none.GitError())
	}
}

func TestResolveBase(t *testing.T) {
	h, repo := gitFixture(t, map[string]string{"docs/README.md": "# Home\n"})
	ctx := context.Background()
	head := runGit(t, repo, "rev-parse", "HEAD")
	blob := runGit(t, repo, "rev-parse", "HEAD:docs/README.md")
	runGit(t, repo, "switch", "-q", "-c", "feature")
	write(t, filepath.Join(repo, "docs/README.md"), "# Home\n\nMore.\n")
	runGit(t, repo, "commit", "-q", "-am", "More")
	out := filepath.Join(t.TempDir(), "x")
	for _, tc := range []struct {
		value, want string
		ok          bool
	}{
		{"HEAD", runGit(t, repo, "rev-parse", "HEAD"), true},
		{"branch", head, true},
		{head[:7], head, true},
		{strings.ToUpper(head), head, true},
		{"--output=" + out, "", false},
		{"HEAD~1", "", false},
		{"main", "", false},
		{"off", "", false},
		{"", "", false},
		{blob, "", false},
		{"0000000", "", false},
	} {
		got, err := h.git.resolveBase(ctx, tc.value)
		var be *baseError
		if tc.ok && (err != nil || got != tc.want) || !tc.ok && !errors.As(err, &be) {
			t.Errorf("resolveBase(%q) = %q, %v", tc.value, got, err)
		}
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Error("an option-like base wrote a file")
	}
	// Without main or master, the branch base fails.
	runGit(t, repo, "branch", "-q", "-m", "main", "trunk")
	if _, err := h.git.resolveBase(ctx, "branch"); err == nil {
		t.Error("branch base without main")
	}
	// master is the main branch of a repository without main.
	runGit(t, repo, "branch", "-q", "-m", "trunk", "master")
	if got, err := h.git.resolveBase(ctx, "branch"); err != nil || got != head {
		t.Errorf("branch base with master = %q, %v", got, err)
	}
	if ref, name := h.git.mainRef(ctx); ref != "refs/heads/master" || name != "master" {
		t.Errorf("mainRef = %q, %q", ref, name)
	}
}

func TestGitStatus(t *testing.T) {
	h, repo := gitFixture(t, map[string]string{
		"docs/README.md":     "# Home\n",
		"docs/ref/plan.md":   "# Plan\n",
		"docs/old.md":        "# Old\n",
		"openspec/clean.md":  "# Clean\n",
		"docs/remove.md":     "# Remove\n",
		"outside/example.go": "package x\n",
	})
	g := h.git
	idx, err := h.current()
	if err != nil {
		t.Fatal(err)
	}
	clean := g.status(h.vaults, idx.version)
	if len(clean.files) != 0 || clean.branch != "main" || clean.head == "" {
		t.Fatalf("clean tree: %+v", clean)
	}
	write(t, filepath.Join(repo, "docs/ref/plan.md"), "# Plan\n\nEdited.\n")
	write(t, filepath.Join(repo, "docs/staged.md"), "# Staged\n")
	write(t, filepath.Join(repo, "docs/new folder/untracked.md"), "# Untracked\n")
	runGit(t, repo, "add", "docs/staged.md")
	runGit(t, repo, "mv", "docs/old.md", "docs/renamed.md")
	runGit(t, repo, "rm", "-q", "docs/remove.md")
	idx, _ = h.current()
	s := g.status(h.vaults, idx.version)
	want := map[string]string{"docs/ref/plan.md": "M", "docs/staged.md": "A", "docs/new folder/untracked.md": "U", "docs/renamed.md": "A"}
	if fmt.Sprint(s.files) != fmt.Sprint(want) || s.renamed["docs/renamed.md"] != "docs/old.md" {
		t.Fatalf("status: %v, renamed %v", s.files, s.renamed)
	}
	if !s.dirty("docs") || !s.dirty("docs/ref") || s.dirty("openspec") || s.digest == clean.digest {
		t.Error("folder marks or digest")
	}

	// Status never writes the index, also for a racily clean index.
	index := filepath.Join(repo, ".git/index")
	before, err := os.Stat(index)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	for i := range 20 {
		g.status(h.vaults, fmt.Sprint("version", i))
	}
	if after, _ := os.Stat(index); !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Error("status changed the index file")
	}

	// A second read with the same key runs no command.
	g.status(h.vaults, idx.version)
	runs := g.runs.Load()
	if g.status(h.vaults, idx.version) != g.state || g.runs.Load() != runs {
		t.Error("an unchanged key ran git again")
	}
	runGit(t, repo, "add", "-A")
	if g.status(h.vaults, idx.version).files["docs/new folder/untracked.md"] != "A" || g.runs.Load() == runs {
		t.Error("git add did not change the status")
	}

	// A file outside the vault has its own status.
	line := h.statLine("outside/example.go")
	if g.pathStatus("outside/example.go", line) != "" {
		t.Error("clean outside file has a mark")
	}
	write(t, filepath.Join(repo, "outside/example.go"), "package y\n")
	if g.pathStatus("outside/example.go", h.statLine("outside/example.go")) != "M" {
		t.Error("changed outside file has no mark")
	}
}

func TestGitHistoryAndContent(t *testing.T) {
	h, repo := gitFixture(t, map[string]string{"docs/OLD.md": "one\n", "docs/big.txt": strings.Repeat("x", 100)})
	g := h.git
	ctx := context.Background()
	first := runGit(t, repo, "rev-parse", "HEAD")
	write(t, filepath.Join(repo, "docs/OLD.md"), "one\ntwo\n")
	runGit(t, repo, "commit", "-q", "-am", "Second")
	second := runGit(t, repo, "rev-parse", "HEAD")
	runGit(t, repo, "mv", "docs/OLD.md", "docs/NEW.md")
	runGit(t, repo, "commit", "-q", "-m", "Rename")
	write(t, filepath.Join(repo, "docs/NEW.md"), "one\ntwo\nthree\n")
	runGit(t, repo, "commit", "-q", "-am", "Third line")
	head := runGit(t, repo, "rev-parse", "HEAD")

	history, err := g.log(ctx, head, "docs/NEW.md")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range history {
		got = append(got, c.Subject+"@"+c.Path)
	}
	if want := "[Third line@docs/NEW.md Rename@docs/NEW.md Second@docs/OLD.md Initial commit@docs/OLD.md]"; fmt.Sprint(got) != want {
		t.Fatalf("history: %v", got)
	}
	if c := history[0]; c.Hash != head || c.Short == "" || c.Author != "Ada Author" || !strings.HasPrefix(c.Date, "2026-") {
		t.Fatalf("commit fields: %+v", c)
	}

	// A commit before the rename uses the old path.
	idx, _ := h.current()
	state := g.status(h.vaults, idx.version)
	top, err := g.pathAt(ctx, state, second, "docs/NEW.md")
	if err != nil || top != "docs/OLD.md" {
		t.Fatalf("path at the second commit: %q %v", top, err)
	}
	old, ok, err := g.show(ctx, second, top, maxNote)
	if err != nil || !ok || string(old) != "one\ntwo\n" {
		t.Fatalf("content at the second commit: %q %v %v", old, ok, err)
	}
	// A merge base that the history does not list uses the first path that exists.
	if top, _ := g.pathAt(ctx, state, first, "docs/NEW.md"); top != "docs/OLD.md" {
		t.Fatal("path at the first commit:", top)
	}
	// A path that does not exist at the base has no content.
	if _, ok, err := g.show(ctx, first, "docs/NEW.md", maxNote); ok || err != nil {
		t.Fatal("missing path:", ok, err)
	}
	if top, _ := g.pathAt(ctx, state, first, "docs/big.txt"); top != "docs/big.txt" {
		t.Fatal("unchanged path:", top)
	}
	if _, _, err := g.show(ctx, first, "docs/big.txt", 50); !errors.Is(err, errBlobTooLarge) {
		t.Fatal("large blob:", err)
	}
}
