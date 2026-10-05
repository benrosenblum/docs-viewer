package markdown

import (
	"fmt"
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"
)

// unitsNote has each unit kind at the top level, in a list, in a block
// quote, and in a callout.
const unitsNote = "# Title\n\nPara one\nline two\n\n- item a\n- item b\n  - nested c\n  ```go\n  code\n  ```\n\n1. loose\n\n   para in item\n\n" +
	"> quote para\n> ```go\n> x := 1\n> ```\n>\n> $$\n> x^2\n> $$\n> - quoted item\n\n" +
	"> [!note] Title\n> callout para\n>\n> ---\n>\n> | A | B |\n> |---|---|\n> | 1 | 2 |\n\n" +
	"| A | B |\n|---|---|\n| 1 | 2 |\n| 3 | 4 |\n\n<div>\nhi\n</div>\n\n%% comment %%\n\nSetext\n===\n\n    indented\n\n***\n"

func TestDiffUnits(t *testing.T) {
	var got []string
	for _, u := range parseUnits([]byte(unitsNote)) {
		s := fmt.Sprintf("%s %d-%d", u.kind, u.start, u.end)
		if u.touchStart != u.start || u.touchEnd != u.end {
			s += fmt.Sprintf(" touch %d-%d", u.touchStart, u.touchEnd)
		}
		got = append(got, s)
	}
	want := []string{
		"heading 0-1", "paragraph 2-4", "item 5-6", "item 6-7", "item 7-8", "code 8-11", "paragraph 12-13", "paragraph 14-15",
		// Block quote.
		"paragraph 16-17", "code 17-20", "math 21-24", "item 24-25",
		// Callout.
		"paragraph 26-28", "rule 29-30", "table 31-34 touch 31-33", "row 33-34",
		"table 35-39 touch 35-37", "row 37-38", "row 38-39", "html 40-43", "comment 44-45", "heading 46-48", "code 49-50", "rule 51-52",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("units:\n%s", strings.Join(got, "\n"))
	}
}

// merged returns the merged source and one letter per line for its origin:
// B both, O old, S separator, N new.
func merged(oldBody, newBody string) (string, string) {
	out, lines, _ := mergeBodies([]byte(oldBody), []byte(newBody))
	var origins strings.Builder
	for _, l := range lines {
		origins.WriteByte("BOSN"[map[origin]int{originBoth: 0, originOld: 1, originSeparator: 2, originNew: 3}[l.origin]])
	}
	return string(out), origins.String()
}

func TestMergeBodies(t *testing.T) {
	for _, tc := range []struct{ name, old, new, want, origins string }{
		{"paragraph", "A\n\nold para\nsecond line\n\nB\n", "A\n\nnew para\nsecond line\n\nB\n",
			"A\n\nold para\nsecond line\n\nnew para\nsecond line\n\nB\n", "BBOOSNNBB"},
		{"block quote", "> A\n>\n> old para\n>\n> B\n", "> A\n>\n> new para\n>\n> B\n",
			"> A\n>\n> old para\n>\n> new para\n>\n> B\n", "BBOSNBB"},
		{"list item", "- a\n- old\n- c\n", "- a\n- new\n- c\n", "- a\n- old\n- new\n- c\n", "BONB"},
		{"table row, then paragraph", "| A |\n|---|\n| 1 |\n\nold para\n", "| A |\n|---|\n| 2 |\n\nnew para\n",
			"| A |\n|---|\n| 1 |\n| 2 |\n\nold para\n\nnew para\n", "BBONBOSN"},
		{"table row and a new paragraph", "| A |\n|---|\n| 1 |\n", "| A |\n|---|\n| 2 |\n\npara\n",
			"| A |\n|---|\n| 1 |\n| 2 |\n\npara\n", "BBONNN"},
		{"table header", "| A |\n|---|\n| 1 |\n\nend\n", "| B |\n|---|\n| 1 |\n\nend\n",
			"| A |\n|---|\n| 1 |\n\n| B |\n|---|\n| 1 |\n\nend\n", "OOOSNNNBB"},
		{"inserted line in a paragraph", "one\ntwo\n", "one\nnew\ntwo\n", "one\ntwo\n\none\nnew\ntwo\n", "OOSNNN"},
		{"missing final newline", "a\n\nold", "a\n\nnew", "a\n\nold\n\nnew\n", "BBOSN"},
	} {
		got, origins := merged(tc.old, tc.new)
		if got != tc.want || origins != tc.origins {
			t.Errorf("%s: merged %q %s, want %q %s", tc.name, got, origins, tc.want, tc.origins)
		}
		checkReconstruction(t, tc.old, tc.new)
	}
}

// checkReconstruction verifies that the merged lines of origin both and old
// give the old body, and those of origin both and new give the new body.
func checkReconstruction(t *testing.T, oldBody, newBody string) {
	t.Helper()
	out, lines, _ := mergeBodies([]byte(oldBody), []byte(newBody))
	var gotOld, gotNew strings.Builder
	for i, line := range strings.SplitAfter(string(out), "\n")[:len(lines)] {
		switch lines[i].origin {
		case originBoth:
			gotOld.WriteString(line)
			gotNew.WriteString(line)
		case originOld:
			gotOld.WriteString(line)
		case originNew:
			gotNew.WriteString(line)
		}
	}
	terminated := func(s string) string {
		if s != "" && !strings.HasSuffix(s, "\n") {
			return s + "\n"
		}
		return s
	}
	if gotOld.String() != terminated(oldBody) || gotNew.String() != terminated(newBody) {
		t.Fatalf("merge of %q and %q loses lines: %q", oldBody, newBody, out)
	}
}

func TestMergeBodiesRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	pool := []string{"", "", "para a", "para b", "- item a", "- item b", "  - nested", "> quote", "> [!note] T", "| A | B |", "|---|---|", "| 1 | 2 |", "```", "code", "# Heading", "Setext", "===", "$$", "x^2", "<div>", "</div>", "***"}
	body := func() string {
		var lines []string
		for range rng.IntN(25) {
			lines = append(lines, pool[rng.IntN(len(pool))])
		}
		return strings.Join(lines, "\n") + "\n"
	}
	for range 3000 {
		old := body()
		// The new body keeps most old lines, so the changes are small.
		var lines []string
		for _, line := range strings.Split(strings.TrimSuffix(old, "\n"), "\n") {
			switch rng.IntN(6) {
			case 0:
			case 1:
				lines = append(lines, pool[rng.IntN(len(pool))])
			default:
				lines = append(lines, line)
			}
		}
		cur := strings.Join(lines, "\n") + "\n"
		checkReconstruction(t, old, cur)
		if _, err := RenderDiff([]byte(old), []byte(cur), "x.md", nil); err != nil {
			t.Fatalf("RenderDiff(%q, %q): %v", old, cur, err)
		}
	}
}

// renderDiff renders a diff and returns its HTML.
func renderDiff(t *testing.T, old, cur string) *Diff {
	t.Helper()
	d, err := RenderDiff([]byte(old), []byte(cur), "docs/x.md", vault{"docs/CLI.md": "# CLI\n"})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// tags returns the opening tags in html that carry class, in order.
func tags(html, class string) []string {
	return regexp.MustCompile(`<[a-z0-9]+[^>]*class="[^"]*\b`+class+`\b[^"]*"[^>]*>`).FindAllString(html, -1)
}

func TestRenderDiffBlocks(t *testing.T) {
	items := func(n int, extra string) string {
		var b strings.Builder
		for i := 1; i <= n; i++ {
			fmt.Fprintf(&b, "- item %d\n", i)
			if i == 5 {
				b.WriteString(extra)
			}
		}
		return b.String()
	}
	for _, tc := range []struct {
		name, old, new string
		removed, added []string // Tag names with each mark, in order.
		want           []string
	}{
		{"changed paragraph", "# T\n\nThe pool holds four connections.\n\nOther.\n", "# T\n\nThe pool is rebuilt at start.\n\nOther.\n",
			[]string{"p"}, []string{"p"}, []string{"<p>Other.</p>", `<h1 id="t">T</h1>`}},
		{"added list item", items(10, ""), items(10, "- new item\n"), nil, []string{"li"}, []string{"<li>item 6</li>"}},
		{"removed section", "# T\n\n## Gone\n\nText.\n\n## Kept\n", "# T\n\n## Kept\n", []string{"h2", "p"}, nil, nil},
		{"changed table row", "| A | B |\n|---|---|\n| 1 | 2 |\n| 3 | 4 |\n", "| A | B |\n|---|---|\n| 1 | 2 |\n| 3 | 5 |\n",
			[]string{"tr"}, []string{"tr"}, []string{"<table>"}},
		{"new table column", "| A |\n|---|\n| 1 |\n", "| A | B |\n|---|---|\n| 1 | 2 |\n", []string{"table"}, []string{"table"}, nil},
		{"changed code block", "```go\nx := 1\n```\n", "```go\nx := 2\n```\n", []string{"div"}, []string{"div"},
			[]string{`<div class="diff-block diff-removed">` + "\n" + `<pre class="code-block chroma" data-lang="go">`}},
		{"changed callout paragraph", "> [!note] Title\n> old text here\n", "> [!note] Title\n> new text here\n", []string{"p"}, []string{"p"},
			[]string{`<div class="callout" data-callout="note">`}},
	} {
		d := renderDiff(t, tc.old, tc.new)
		check := func(class string, want []string) {
			var got []string
			for _, tag := range tags(d.HTML, class) {
				got = append(got, regexp.MustCompile(`^<([a-z0-9]+)`).FindStringSubmatch(tag)[1])
			}
			if fmt.Sprint(got) != fmt.Sprint(want) && !(len(got) == 0 && len(want) == 0) {
				t.Errorf("%s: %s on %v, want %v\n%s", tc.name, class, got, want, d.HTML)
			}
		}
		check("diff-removed", tc.removed)
		check("diff-added", tc.added)
		check("diff-changed", nil)
		for _, w := range tc.want {
			if !strings.Contains(d.HTML, w) {
				t.Errorf("%s: HTML lacks %s\n%s", tc.name, w, d.HTML)
			}
		}
		if strings.Count(d.HTML, "<ul>") > 1 || strings.Count(d.HTML, "<table") > 2 {
			t.Errorf("%s: a container split\n%s", tc.name, d.HTML)
		}
	}
}

func TestRenderDiffProperties(t *testing.T) {
	d := renderDiff(t, "---\nstatus: draft\n---\n# T\n", "---\nstatus: final\n---\n# T\n")
	if !d.PropertiesChanged || d.Properties[0].Values[0].Text != "final" || strings.Contains(d.HTML, "diff-") {
		t.Errorf("changed property: %v %+v %s", d.PropertiesChanged, d.Properties, d.HTML)
	}
	if d := renderDiff(t, "---\nstatus: draft\n---\n# T\n", "---\nstatus: draft\n---\n# T\n\nNew.\n"); d.PropertiesChanged {
		t.Error("equal properties are marked changed")
	}
}

func TestRenderDiffWords(t *testing.T) {
	for _, tc := range []struct {
		name, old, new string
		want, notWant  []string
	}{
		{"one word in a paragraph", "The pool holds four connections.\n", "The pool holds eight connections.\n",
			[]string{`<p class="diff-removed">The pool holds <del class="diff-word">four</del> connections.</p>`,
				`<p class="diff-added">The pool holds <ins class="diff-word">eight</ins> connections.</p>`}, nil},
		{"changed link text", "See [the engine](https://example.com/engine) for more.\n", "See [the render engine](https://example.com/engine) for more.\n",
			[]string{`>the engine</a>`, `>the <ins class="diff-word">render</ins> engine</a>`}, []string{"<del"}},
		{"changed table cell", "| Name | Size |\n|---|---|\n| pool | 4 |\n", "| Name | Size |\n|---|---|\n| pool | 8 |\n",
			[]string{`<td>pool</td>`, `<td><del class="diff-word">4</del></td>`, `<td><ins class="diff-word">8</ins></td>`}, nil},
		{"rewritten paragraph", "The parser reads every line.\n", "Reload sends pages to browsers.\n",
			[]string{`<p class="diff-removed">`, `<p class="diff-added">`}, []string{"diff-word"}},
		{"word in emphasis", "A *very old* text.\n", "A *very new* text.\n",
			[]string{`<em>very <del class="diff-word">old</del></em>`, `<em>very <ins class="diff-word">new</ins></em>`}, nil},
		{"inline code span", "Use `foo` for the pool now.\n", "Use `bar` for the pool now.\n",
			[]string{`<del class="diff-word"><code>foo</code></del>`, `<ins class="diff-word"><code>bar</code></ins>`}, nil},
		{"heading", "## Old name here\n", "## New name here\n",
			[]string{`<del class="diff-word">Old</del> name here</h2>`, `<ins class="diff-word">New</ins> name here</h2>`}, nil},
		{"list item", "- keep the old words\n- other\n", "- keep the new words\n- other\n",
			[]string{`<li class="diff-removed">keep the <del class="diff-word">old</del> words</li>`}, nil},
		{"code block", "```\nx := 1\n```\n", "```\nx := 2\n```\n", []string{"diff-block"}, []string{"diff-word"}},
		{"soft line break", "one two\nthree four\n", "one two\nthree five\n",
			[]string{"one two\nthree <del class=\"diff-word\">four</del></p>"}, nil},
		{"change across text nodes", "| Port | Use |\n|---|---|\n| 7000 | Docs for `docs/` |\n", "| Port | Use |\n|---|---|\n| 7000 | Docs for `docs/`, with git diffs |\n",
			[]string{`<code>docs/</code><ins class="diff-word">, with git diffs</ins></td>`}, nil},
	} {
		d := renderDiff(t, tc.old, tc.new)
		for _, w := range tc.want {
			if !strings.Contains(d.HTML, w) {
				t.Errorf("%s: HTML lacks %s\n%s", tc.name, w, d.HTML)
			}
		}
		for _, w := range tc.notWant {
			if strings.Contains(d.HTML, w) {
				t.Errorf("%s: HTML has %s\n%s", tc.name, w, d.HTML)
			}
		}
	}
	// The link target does not change.
	d := renderDiff(t, "See [the engine](CLI.md).\n", "See [the render engine](CLI.md).\n")
	if hrefs := regexp.MustCompile(`href="[^"]*"`).FindAllString(d.HTML, -1); len(hrefs) != 2 || hrefs[0] != hrefs[1] || hrefs[0] != `href="/docs/CLI.md"` {
		t.Errorf("link targets: %v\n%s", hrefs, d.HTML)
	}
}
