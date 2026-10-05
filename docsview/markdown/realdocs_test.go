package markdown

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// repoRoot is the repository root relative to this package.
const repoRoot = "../.."

// fsResolver resolves against the repository on disk, with wikilinks
// matched by path or by base name as Obsidian does.
type fsResolver struct {
	root  string
	files []string
}

func newFSResolver(t *testing.T, root string) *fsResolver {
	t.Helper()
	r := &fsResolver{root: root}
	for _, vault := range []string{"docs"} {
		err := filepath.WalkDir(filepath.Join(root, vault), func(p string, e fs.DirEntry, err error) error {
			if err == nil && !e.IsDir() {
				rel, _ := filepath.Rel(root, p)
				r.files = append(r.files, filepath.ToSlash(rel))
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func (r *fsResolver) ResolveWikilink(from, target string) (string, bool) {
	for _, p := range []string{path.Join(path.Dir(from), target), target} {
		for _, c := range []string{p, p + ".md"} {
			if slices.Contains(r.files, c) {
				return c, true
			}
		}
	}
	for _, p := range r.files {
		if base := path.Base(p); base == target || strings.TrimSuffix(base, ".md") == target {
			return p, true
		}
	}
	return "", false
}

func (r *fsResolver) Exists(p string) bool {
	_, err := os.Stat(filepath.Join(r.root, filepath.FromSlash(p)))
	return err == nil
}

func (r *fsResolver) HeadingID(p, heading string) (string, bool) {
	src, err := r.Source(p)
	if err != nil {
		return "", false
	}
	for _, h := range Headings(src) {
		if strings.EqualFold(h.Text, heading) {
			return h.ID, true
		}
	}
	return "", false
}

func (r *fsResolver) Source(p string) ([]byte, error) {
	return os.ReadFile(filepath.Join(r.root, filepath.FromSlash(p)))
}

var fenceLine = regexp.MustCompile("^(?:[ \t]*>)*[ \t]{0,3}(`{3,}|~{3,})[ \t]*([^ \t`]*)")

// mermaidFences counts the fenced code blocks tagged mermaid, ignoring
// fences shown inside longer fences.
func mermaidFences(src []byte) int {
	count, open := 0, ""
	for _, line := range strings.Split(string(src), "\n") {
		m := fenceLine.FindStringSubmatch(strings.TrimRight(line, " \t\r"))
		switch {
		case m == nil:
		case open == "":
			open = m[1]
			if strings.EqualFold(m[2], "mermaid") {
				count++
			}
		case m[1][0] == open[0] && len(m[1]) >= len(open) && m[2] == "":
			open = ""
		}
	}
	return count
}

func TestRealDocs(t *testing.T) {
	r := newFSResolver(t, repoRoot)
	var checked int
	for _, p := range r.files {
		if path.Ext(p) != ".md" {
			continue
		}
		t.Run(p, func(t *testing.T) {
			src, err := r.Source(p)
			if err != nil {
				t.Fatal(err)
			}
			d, err := Render(src, p, r)
			if err != nil {
				t.Fatal(err)
			}
			checked++
			if d.PropertiesError != "" {
				t.Errorf("PropertiesError: %s", d.PropertiesError)
			}
			if got, want := strings.Count(d.HTML, `<pre class="mermaid">`), mermaidFences(src); got != want {
				t.Errorf("%d mermaid blocks, want %d", got, want)
			}
			if d.Mermaid != strings.Contains(d.HTML, `<pre class="mermaid">`) {
				t.Errorf("Mermaid = %v", d.Mermaid)
			}
			ids := map[string]bool{}
			for _, h := range d.Headings {
				ids[h.ID] = true
			}
			for _, l := range d.Links {
				if l.Path == p && l.Fragment != "" && !strings.HasPrefix(l.Fragment, "^") && !ids[l.Fragment] {
					t.Errorf("link to missing heading #%s (context %q)", l.Fragment, l.Context)
				}
			}
		})
	}
	if checked == 0 {
		t.Fatal("rendered no files")
	}
}
