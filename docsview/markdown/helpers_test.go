package markdown

import (
	"io/fs"
	"path"
	"slices"
	"strings"
	"testing"
)

// vault is an in-memory Resolver keyed by repository path.
type vault map[string]string

func (v vault) ResolveWikilink(from, target string) (string, bool) {
	dir := path.Dir(from)
	for _, p := range []string{target, target + ".md", path.Join(dir, target), path.Join(dir, target+".md")} {
		if _, ok := v[p]; ok {
			return p, true
		}
	}
	paths := make([]string, 0, len(v))
	for p := range v {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		if base := path.Base(p); base == target || strings.TrimSuffix(base, ".md") == target {
			return p, true
		}
	}
	return "", false
}

func (v vault) Exists(p string) bool {
	_, ok := v[p]
	return ok
}

func (v vault) HeadingID(p, heading string) (string, bool) {
	for _, h := range Headings([]byte(v[p])) {
		if strings.EqualFold(h.Text, heading) {
			return h.ID, true
		}
	}
	return "", false
}

func (v vault) Source(p string) ([]byte, error) {
	src, ok := v[p]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return []byte(src), nil
}

func mustRender(t *testing.T, src, from string, r Resolver) *Document {
	t.Helper()
	d, err := Render([]byte(src), from, r)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return d
}

// htmlCase renders src and checks for substrings of the HTML.
type htmlCase struct {
	name    string
	src     string
	want    []string
	notWant []string
}

func checkHTML(t *testing.T, from string, r Resolver, cases []htmlCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := mustRender(t, tc.src, from, r)
			for _, w := range tc.want {
				if !strings.Contains(d.HTML, w) {
					t.Errorf("HTML lacks %q:\n%s", w, d.HTML)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(d.HTML, w) {
					t.Errorf("HTML contains %q:\n%s", w, d.HTML)
				}
			}
		})
	}
}

// otherNote is a note with headings, sections, and a block ID.
const otherNote = `# Top

Intro text.

## Section Two

Two body.

### Sub

Sub body.

## Section Three

Three body. ^blk
`

func testVault() vault {
	return vault{
		"docs/note.md":            "# Note\n",
		"docs/other.md":           otherNote,
		"docs/pic.png":            "",
		"docs/data.csv":           "",
		"docs/development.md":     "## Requirements and setup\n",
		"docs/api.md":             "# API\n",
		"docs/my file.md":         "x\n",
		"docs/img/p.png":          "",
		"openspec/x.md":           "# X\n",
		"AGENTS.md":               "# Agents\n",
		"docs/ref/nested.md":      "Nested.\n",
		"docs/ref/extra-x.md":     "x\n",
		"docs/web-client-plan.md": "",
	}
}
