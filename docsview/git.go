package docsview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// gitTimeout limits each git command.
const gitTimeout = 5 * time.Second

// gitGlobal starts every git command. The viewer only reads: without
// --no-optional-locks, "git status" refreshes and rewrites the index.
var gitGlobal = []string{"--no-optional-locks", "--literal-pathspecs", "-c", "core.quotepath=off", "-c", "color.ui=never"}

// gitRepo runs read-only git commands in the repository root. A nil
// *gitRepo means that the diff features are off.
type gitRepo struct {
	dir    string   // The repository root, where commands run.
	prefix string   // The root below the top level: "" or a path ending in "/".
	keys   []string // HEAD, index, and logs/HEAD in the git directory.
	runs   atomic.Int64

	mu       sync.Mutex // Guards the caches below; held while status runs.
	state    *gitState
	stateKey string
	paths    map[string]pathStatus
	history  map[string][]commitInfo // Keyed by the HEAD hash and the path.
}

// pathStatus is the cached status of one file outside the vault.
type pathStatus struct{ key, status string }

// gitError reports a failed git command with the first line git wrote.
type gitError struct {
	command string
	err     error
	stderr  string
}

func (e *gitError) Error() string {
	if e.stderr != "" {
		return "git " + e.command + ": " + e.stderr
	}
	return "git " + e.command + ": " + e.err.Error()
}

func (e *gitError) Unwrap() error { return e.err }

// openGit detects the git work tree that contains root. It fails when git is
// not on PATH, when root is not in a work tree, or when git refuses to read
// the repository.
func openGit(root string) (*gitRepo, error) {
	g := &gitRepo{dir: root, paths: map[string]pathStatus{}, history: map[string][]commitInfo{}}
	out, err := g.run(context.Background(), "rev-parse", "--show-toplevel", "--show-prefix", "--absolute-git-dir")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != 3 || lines[2] == "" {
		return nil, fmt.Errorf("git rev-parse: unexpected output %q", out)
	}
	g.prefix = lines[1]
	gitDir := lines[2]
	g.keys = []string{filepath.Join(gitDir, "HEAD"), filepath.Join(gitDir, "index"), filepath.Join(gitDir, "logs", "HEAD")}
	return g, nil
}

// gitEnv is the process environment without GIT_ variables, which could
// select another repository, plus the variables that keep git read-only and
// non-interactive.
func gitEnv() []string {
	env := slices.DeleteFunc(os.Environ(), func(v string) bool { return strings.HasPrefix(v, "GIT_") || strings.HasPrefix(v, "LC_ALL=") })
	return append(env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
}

// run runs git with the global options and args, and returns its output.
// Callers put --end-of-options before revisions and -- before paths.
func (g *gitRepo) run(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append(slices.Clone(gitGlobal), args...)...)
	cmd.Dir = g.dir
	cmd.Env = gitEnv()
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	g.runs.Add(1)
	if err := cmd.Run(); err != nil {
		first, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n")
		return nil, &gitError{command: args[0], err: err, stderr: first}
	}
	return stdout.Bytes(), nil
}

// topPath converts a root-relative path to the top-level path git uses.
func (g *gitRepo) topPath(p string) string { return g.prefix + p }

// rootPath converts a top-level path to a root-relative path. It reports
// false for a path outside the root.
func (g *gitRepo) rootPath(top string) (string, bool) {
	if !strings.HasPrefix(top, g.prefix) {
		return "", false
	}
	return strings.TrimPrefix(top, g.prefix), true
}

// Base values in the diff parameter of a file page.
const (
	baseOff    = "off"
	baseHead   = "HEAD"
	baseBranch = "branch"
)

var hashPattern = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)

// validBase reports whether v matches the base grammar. Resolution is a
// separate step.
func validBase(v string) bool {
	return v == baseOff || v == baseHead || v == baseBranch || hashPattern.MatchString(v)
}

// baseError is an invalid or unknown base. Its message is for the page.
type baseError struct{ message string }

func (e *baseError) Error() string { return e.message }

// resolveBase returns the commit hash that a diff base names. It returns ""
// for HEAD in a repository without commits: then every line is added.
func (g *gitRepo) resolveBase(ctx context.Context, v string) (string, error) {
	switch {
	case !validBase(v) || v == baseOff:
		return "", &baseError{fmt.Sprintf("The base %q is not valid. Use HEAD, branch, off, or a commit hash of 7 to 64 hexadecimal characters.", v)}
	case v == baseHead:
		return g.commit(ctx, baseHead), nil
	case v == baseBranch:
		ref, name := g.mainRef(ctx)
		if ref == "" {
			return "", &baseError{"The repository has no main or master branch to compare with."}
		}
		out, err := g.run(ctx, "merge-base", "--end-of-options", "HEAD", ref)
		if err != nil {
			return "", &baseError{"The current branch has no merge base with " + name + "."}
		}
		return strings.TrimSpace(string(out)), nil
	}
	commit := g.commit(ctx, v)
	if commit == "" {
		return "", &baseError{fmt.Sprintf("The base %s is not a known commit.", v)}
	}
	return commit, nil
}

// commit resolves a revision to a commit hash, or returns "".
func (g *gitRepo) commit(ctx context.Context, rev string) string {
	out, err := g.run(ctx, "rev-parse", "--verify", "--quiet", "--end-of-options", rev+"^{commit}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// mainRef returns the ref and the name of the main branch: main, else
// master, a local branch before a branch of origin. Both are "" when the
// repository has none.
func (g *gitRepo) mainRef(ctx context.Context) (ref, name string) {
	for _, prefix := range []string{"refs/heads/", "refs/remotes/origin/"} {
		for _, name := range []string{"main", "master"} {
			if g.commit(ctx, prefix+name) != "" {
				return prefix + name, name
			}
		}
	}
	return "", ""
}

// Tree marks.
const (
	gitModified  = "M"
	gitAdded     = "A"
	gitUntracked = "U"
)

// gitState is an immutable snapshot of the git status of the vault.
type gitState struct {
	branch  string            // The current branch, or "" when HEAD is detached.
	head    string            // The HEAD commit, or "" before the first commit.
	files   map[string]string // Root-relative path to its tree mark.
	renamed map[string]string // Root-relative path to its top-level path at HEAD.
	digest  string            // Changes with any mark.
}

// statusOf returns the tree mark of a root-relative path, or "".
func (s *gitState) statusOf(p string) string {
	if s == nil {
		return ""
	}
	return s.files[p]
}

// dirty reports whether a folder contains a marked file at any depth.
func (s *gitState) dirty(dir string) bool {
	if s == nil {
		return false
	}
	for p := range s.files {
		if strings.HasPrefix(p, dir+"/") {
			return true
		}
	}
	return false
}

// ref returns the current branch, else the short hash of a detached HEAD,
// else "".
func (s *gitState) ref() string {
	if s == nil {
		return ""
	}
	if s.branch != "" {
		return s.branch
	}
	return s.head[:min(7, len(s.head))]
}

// key returns the stat lines of the git files that a commit, a stage, a
// reset, or a checkout changes.
func (g *gitRepo) key() string {
	var b strings.Builder
	for _, name := range g.keys {
		if info, err := os.Stat(name); err == nil {
			fmt.Fprintf(&b, "%d:%d;", info.Size(), info.ModTime().UnixNano())
		} else {
			b.WriteString("missing;")
		}
	}
	return b.String()
}

// status returns the git state of the vaults. It runs git only when the git
// key or the vault version changed since the last call, and all callers
// share the result. On a failure it keeps the last state.
func (g *gitRepo) status(vaults []string, vaultVersion string) *gitState {
	key := g.key() + vaultVersion
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != nil && g.stateKey == key {
		return g.state
	}
	args := []string{"status", "--porcelain=v2", "-z", "--branch", "--untracked-files=all", "--"}
	for _, v := range vaults {
		args = append(args, g.topPath(v))
	}
	out, err := g.run(context.Background(), args...)
	if err != nil {
		if g.state == nil {
			return &gitState{files: map[string]string{}, renamed: map[string]string{}}
		}
		return g.state
	}
	g.state, g.stateKey = g.parseStatus(out), key
	return g.state
}

// pathStatus returns the tree mark of one root-relative path outside the
// vault, cached by the git key and the file's state line.
func (g *gitRepo) pathStatus(p, line string) string {
	key := g.key() + line
	g.mu.Lock()
	defer g.mu.Unlock()
	if cached, ok := g.paths[p]; ok && cached.key == key {
		return cached.status
	}
	out, err := g.run(context.Background(), "status", "--porcelain=v2", "-z", "--untracked-files=all", "--", g.topPath(p))
	if err != nil {
		return g.paths[p].status
	}
	if len(g.paths) > 256 {
		clear(g.paths)
	}
	status := g.parseStatus(out).files[p]
	g.paths[p] = pathStatus{key, status}
	return status
}

// parseStatus reads "git status --porcelain=v2 -z" output.
func (g *gitRepo) parseStatus(out []byte) *gitState {
	s := &gitState{files: map[string]string{}, renamed: map[string]string{}}
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		line := fields[i]
		kind, rest, _ := strings.Cut(line, " ")
		var top, mark string
		switch kind {
		case "#":
			if v, ok := strings.CutPrefix(rest, "branch.oid "); ok && v != "(initial)" {
				s.head = v
			} else if v, ok := strings.CutPrefix(rest, "branch.head "); ok && v != "(detached)" {
				s.branch = v
			}
			continue
		case "?":
			top, mark = rest, gitUntracked
		case "1", "2", "u":
			// Fields before the path: 8 for "1", 9 for "2", 10 for "u".
			n := map[string]int{"1": 8, "2": 9, "u": 10}[kind]
			parts := strings.SplitN(line, " ", n+1)
			if len(parts) != n+1 {
				continue
			}
			xy, top := parts[1], parts[n]
			if kind == "2" {
				i++ // The original path follows in its own field.
			}
			if kind != "u" && strings.ContainsRune(xy, 'D') {
				continue // Deleted files are not in the tree.
			}
			mark = gitModified
			if kind == "2" || kind == "1" && xy[0] == 'A' {
				mark = gitAdded
			}
			if p, ok := g.rootPath(top); ok {
				s.files[p] = mark
				if kind == "2" && i < len(fields) {
					s.renamed[p] = fields[i]
				}
			}
			continue
		default:
			continue
		}
		if p, ok := g.rootPath(top); ok {
			s.files[p] = mark
		}
	}
	hash := sha256.New()
	fmt.Fprintf(hash, "%s\x00%s\x00", s.branch, s.head)
	for _, p := range slices.Sorted(maps.Keys(s.files)) {
		fmt.Fprintf(hash, "%s\x00%s\x00", p, s.files[p])
	}
	s.digest = hex.EncodeToString(hash.Sum(nil))[:16]
	return s
}

// errBlobTooLarge reports a base version above the display limit.
var errBlobTooLarge = errors.New("base version is too large to display")

// show returns the content of a top-level path at a commit, with the work
// tree filters (line endings) applied. It reports false when the path does
// not exist at the commit.
func (g *gitRepo) show(ctx context.Context, commit, top string, limit int64) ([]byte, bool, error) {
	if commit == "" {
		return nil, false, nil
	}
	object := commit + ":" + top
	out, err := g.run(ctx, "cat-file", "-s", "--end-of-options", object)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, false, nil
		}
		return nil, false, err
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return nil, false, err
	}
	if size > limit {
		return nil, true, errBlobTooLarge
	}
	out, err = g.run(ctx, "cat-file", "--filters", "--end-of-options", object)
	if err != nil {
		return nil, false, err
	}
	if int64(len(out)) > limit {
		return nil, true, errBlobTooLarge
	}
	return out, true, nil
}

// exists reports whether a top-level path exists at a commit.
func (g *gitRepo) exists(ctx context.Context, commit, top string) bool {
	_, err := g.run(ctx, "cat-file", "-e", "--end-of-options", commit+":"+top)
	return err == nil
}

// maxHistory is the most commits a file history lists.
const maxHistory = 100

// commitInfo is one commit in a file history.
type commitInfo struct {
	Hash    string `json:"hash"`
	Short   string `json:"short"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
	Date    string `json:"date"` // The author date, ISO 8601.
	Path    string `json:"path"` // The file's root-relative path at this commit.
	top     string // The file's top-level path at this commit.
}

// log returns the commits that changed a root-relative path, newest first,
// with renames followed. It is cached by the HEAD commit and the path.
func (g *gitRepo) log(ctx context.Context, head, p string) ([]commitInfo, error) {
	if head == "" {
		return nil, nil
	}
	key := head + "\x00" + p
	g.mu.Lock()
	cached, ok := g.history[key]
	g.mu.Unlock()
	if ok {
		return cached, nil
	}
	out, err := g.run(ctx, "log", "--follow", "-n", strconv.Itoa(maxHistory), "--format=%x1e%H%x00%h%x00%s%x00%an%x00%aI", "--name-status", "-z", "--end-of-options", head, "--", g.topPath(p))
	if err != nil {
		return nil, err
	}
	commits := g.parseLog(out)
	g.mu.Lock()
	if len(g.history) > 64 {
		clear(g.history)
	}
	g.history[key] = commits
	g.mu.Unlock()
	return commits, nil
}

// parseLog reads the records that log writes: header fields separated by
// NUL, then name-status entries ("M", path) or ("R100", old, new).
func (g *gitRepo) parseLog(out []byte) []commitInfo {
	var commits []commitInfo
	for _, record := range strings.Split(string(out), "\x1e") {
		fields := strings.Split(record, "\x00")
		if len(fields) < 6 {
			continue
		}
		c := commitInfo{Hash: fields[0], Short: fields[1], Subject: fields[2], Author: fields[3], Date: fields[4]}
		entries := fields[5:]
		entries[0] = strings.TrimLeft(entries[0], "\n")
		for i := 0; i < len(entries); i++ {
			status := entries[i]
			if status == "" {
				continue
			}
			if (status[0] == 'R' || status[0] == 'C') && i+2 < len(entries) {
				c.top = entries[i+2]
				i += 2
			} else if i+1 < len(entries) {
				c.top = entries[i+1]
				i++
			}
		}
		c.Path = c.top
		if p, ok := g.rootPath(c.top); ok {
			c.Path = p
		}
		commits = append(commits, c)
	}
	return commits
}

// pathAt returns the top-level path that the file at the root-relative path
// p had at commit, or "" when it did not exist there. A commit in the
// file's history gives its recorded path. For another commit, such as a
// merge base, the first of these that exists at the commit wins: the
// current path, the path before an uncommitted rename, then the older paths
// of the history.
func (g *gitRepo) pathAt(ctx context.Context, state *gitState, commit, p string) (string, error) {
	if commit == "" {
		return "", nil
	}
	history, err := g.log(ctx, state.head, p)
	if err != nil {
		return "", err
	}
	for _, c := range history {
		if c.Hash == commit {
			return c.top, nil
		}
	}
	candidates := []string{g.topPath(p)}
	if old, ok := state.renamed[p]; ok {
		candidates = append(candidates, old)
	}
	for _, c := range history {
		candidates = append(candidates, c.top)
	}
	seen := map[string]bool{}
	for _, top := range candidates {
		if top == "" || seen[top] {
			continue
		}
		seen[top] = true
		if g.exists(ctx, commit, top) {
			return top, nil
		}
	}
	return "", nil
}
