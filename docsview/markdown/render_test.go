package markdown

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

const from = "docs/web-client-plan.md"

func TestSlug(t *testing.T) {
	tests := []struct{ in, want string }{
		{"4.9 Bounded physics with an authoritative backend", "49-bounded-physics-with-an-authoritative-backend"},
		{"Field Guide — Version 2B Draft", "field-guide--version-2b-draft"},
		{"Requirements and setup", "requirements-and-setup"},
		{"Ünïcode Straße_x-y", "ünïcode-straße_x-y"},
		{"C++ & C#: (notes)!", "c--c-notes"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := Slug(tc.in); got != tc.want {
			t.Errorf("Slug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestURL(t *testing.T) {
	tests := []struct{ path, fragment, want string }{
		{"docs/x.md", "", "/docs/x.md"},
		{"docs/x.md", "sec-1", "/docs/x.md#sec-1"},
		{"docs/my file.md", "^blk", "/docs/my%20file.md#^blk"},
		{"docs/a#b?.md", "a b", "/docs/a%23b%3F.md#a%20b"},
		{".", "", "/"},
	}
	for _, tc := range tests {
		if got := URL(tc.path, tc.fragment); got != tc.want {
			t.Errorf("URL(%q, %q) = %q, want %q", tc.path, tc.fragment, got, tc.want)
		}
	}
}

func TestHeadings(t *testing.T) {
	src := "---\ntitle: T\n---\n# Intro\n\n## Intro\n\n## Intro\n\n### *Rich* `code` [[note|Link]] $x$\n\nSetext\n------\n\n> # Quoted\n\n```\n# not a heading\n```\n\n![[other#Section Two]]\n"
	want := []Heading{
		{1, "Intro", "intro"},
		{2, "Intro", "intro-1"},
		{2, "Intro", "intro-2"},
		{3, "Rich code Link x", "rich-code-link-x"},
		{2, "Setext", "setext"},
		{1, "Quoted", "quoted"},
	}
	d := mustRender(t, src, from, testVault())
	if !reflect.DeepEqual(d.Headings, want) {
		t.Errorf("Render().Headings = %+v, want %+v", d.Headings, want)
	}
	if got := Headings([]byte(src)); !reflect.DeepEqual(got, d.Headings) {
		t.Errorf("Headings() = %+v, want Render().Headings %+v", got, d.Headings)
	}
	for _, id := range []string{`<h1 id="intro">`, `<h2 id="intro-1">`, `<h2 id="intro-2">`, `<h1 id="quoted">`} {
		if !strings.Contains(d.HTML, id) {
			t.Errorf("HTML lacks %s:\n%s", id, d.HTML)
		}
	}
	if !strings.Contains(d.HTML, "<h2>Section Two</h2>") {
		t.Errorf("embedded heading has an id or is missing:\n%s", d.HTML)
	}
}

func TestTitle(t *testing.T) {
	tests := []struct{ name, src, want string }{
		{"frontmatter", "---\ntitle: From FM\n---\n# Heading\n", "From FM"},
		{"first h1", "## Sub\n\n# First *one*\n\n# Second\n", "First one"},
		{"none", "## Only h2\n", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustRender(t, tc.src, from, nil).Title; got != tc.want {
				t.Errorf("Title = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWikilinks(t *testing.T) {
	checkHTML(t, from, testVault(), []htmlCase{
		{name: "plain", src: "[[note]]",
			want: []string{`<a class="internal-link" href="/docs/note.md" data-path="docs/note.md">note</a>`}},
		{name: "alias", src: "[[note|The Note]]",
			want: []string{`href="/docs/note.md" data-path="docs/note.md">The Note</a>`}},
		{name: "heading uses resolver id", src: "[[development#Requirements and setup]]",
			want: []string{`<a class="internal-link" href="/docs/development.md#requirements-and-setup" data-path="docs/development.md">development &gt; Requirements and setup</a>`}},
		{name: "unknown heading falls back to slug", src: "[[note#No Such Heading]]",
			want: []string{`href="/docs/note.md#no-such-heading"`}},
		{name: "last heading segment", src: "[[other#Top#Sub]]",
			want: []string{`href="/docs/other.md#sub"`, `>other &gt; Top &gt; Sub</a>`}},
		{name: "block id", src: "[[other#^blk]]",
			want: []string{`href="/docs/other.md#^blk"`}},
		{name: "same document heading", src: "# 1. Identity and player authority\n\nSee [[#1. Identity and player authority]].",
			want: []string{`<a class="internal-link" href="/docs/web-client-plan.md#1-identity-and-player-authority" data-path="docs/web-client-plan.md">1. Identity and player authority</a>`}},
		{name: "same document loose match", src: "## Costs: #1 [draft]\n\n[[#costs  1 draft|costs]]",
			want: []string{`href="/docs/web-client-plan.md#costs-1-draft"`, `>costs</a>`}},
		{name: "same document duplicate heading", src: "# A\n\n# A\n\n[[#A]]",
			want: []string{`href="/docs/web-client-plan.md#a"`}},
		{name: "table alias", src: "| a | b |\n|---|---|\n| [[note\\|Alias]] | [[other#Sub\\|S]] |",
			want: []string{`<td><a class="internal-link" href="/docs/note.md" data-path="docs/note.md">Alias</a></td>`, `href="/docs/other.md#sub" data-path="docs/other.md">S</a></td>`}},
		{name: "unresolved", src: "[[missing-note]] [[sub/gone#H|alias]]",
			want: []string{
				`<a class="internal-link is-unresolved" href="/docs/missing-note.md" data-path="" data-target="missing-note">missing-note</a>`,
				`<a class="internal-link is-unresolved" href="/docs/sub/gone.md" data-path="" data-target="sub/gone">alias</a>`,
			}},
		{name: "not a wikilink", src: "[[]] [[ ]] [[a\nb]] [[a]b]] `[[note]]`",
			notWant: []string{"internal-link"}},
		{name: "html escaped", src: "[[<x>|a <b>]]",
			want: []string{`data-target="&lt;x&gt;">a &lt;b&gt;</a>`}},
	})
}

func TestEmbeds(t *testing.T) {
	checkHTML(t, from, testVault(), []htmlCase{
		{name: "image sizes", src: "![[pic.png]] ![[pic.png|300]] ![[pic.png|300x200]] ![[pic.png|A cat]]",
			want: []string{
				`<img class="internal-embed" src="/docs/pic.png" alt="pic.png">`,
				`<img class="internal-embed" src="/docs/pic.png" alt="pic.png" width="300">`,
				`<img class="internal-embed" src="/docs/pic.png" alt="pic.png" width="300" height="200">`,
				`<img class="internal-embed" src="/docs/pic.png" alt="A cat">`,
			}},
		{name: "note", src: "![[note]]",
			want: []string{`<div class="markdown-embed" data-path="docs/note.md"><div class="markdown-embed-title"><a class="internal-link" href="/docs/note.md" data-path="docs/note.md">note</a></div><div class="markdown-embed-content">
<h1>Note</h1>
</div></div>`},
			notWant: []string{"<p><div"}},
		{name: "section", src: "![[other#Section Two]]",
			want:    []string{"<h2>Section Two</h2>\n<p>Two body.</p>\n<h3>Sub</h3>\n<p>Sub body.</p>\n</div></div>"},
			notWant: []string{"Intro text", "Three body"}},
		{name: "nested section", src: "![[other#Sub]]",
			want:    []string{"<h3>Sub</h3>\n<p>Sub body.</p>\n</div>"},
			notWant: []string{"Two body", "Three body"}},
		{name: "block section", src: "![[other#^blk]]",
			want:    []string{`<div class="markdown-embed-content">` + "\n" + `<p id="^blk">Three body.</p>` + "\n</div>"},
			notWant: []string{"Section Three"}},
		{name: "other file type", src: "![[data.csv]]",
			want: []string{`<p><a class="internal-link" href="/docs/data.csv" data-path="docs/data.csv">data.csv</a></p>`}},
		{name: "unresolved", src: "![[nope]] ![[#Missing]] ![[other#Nope]] ![[gone.png|300]]",
			want: []string{
				`<span class="internal-embed is-unresolved">nope</span>`,
				`<span class="internal-embed is-unresolved">Missing</span>`,
				`<span class="internal-embed is-unresolved">other &gt; Nope</span>`,
				`<span class="internal-embed is-unresolved">gone.png</span>`,
			}},
		{name: "inline with text stays in paragraph", src: "Before ![[pic.png]] after",
			want: []string{`<p>Before <img class="internal-embed"`}},
	})
}

func TestEmbedCyclesAndDepth(t *testing.T) {
	v := vault{
		"docs/a.md":    "A body\n\n![[b]]\n",
		"docs/b.md":    "B body\n\n![[a]]\n",
		"docs/self.md": "# S\n\n![[#S]]\n",
		"docs/c1.md":   "![[c2]]", "docs/c2.md": "![[c3]]", "docs/c3.md": "![[c4]]",
		"docs/c4.md": "C4 body ![[c5]]", "docs/c5.md": "C5 body",
		"docs/fn.md": "B[^1].\n\n[^1]: b note\n",
	}
	tests := []struct {
		name, path string
		want       []string
		count      map[string]int
	}{
		{"cycle", "docs/a.md",
			[]string{`<p>B body</p>`, `<div class="markdown-embed is-cycle" data-path="docs/a.md"><a class="internal-link" href="/docs/a.md" data-path="docs/a.md">a</a></div>`},
			map[string]int{`class="markdown-embed"`: 1, "is-cycle": 1}},
		{"self section", "docs/self.md",
			[]string{`<div class="markdown-embed is-cycle" data-path="docs/self.md">`},
			map[string]int{`class="markdown-embed"`: 1, "is-cycle": 1}},
		{"depth limit", "docs/c1.md",
			[]string{"C4 body", `<a class="internal-link" href="/docs/c5.md" data-path="docs/c5.md">c5</a>`},
			map[string]int{`class="markdown-embed"`: 3, "C5 body": 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := mustRender(t, v[tc.path], tc.path, v)
			for _, w := range tc.want {
				if !strings.Contains(d.HTML, w) {
					t.Errorf("HTML lacks %q:\n%s", w, d.HTML)
				}
			}
			for s, n := range tc.count {
				if got := strings.Count(d.HTML, s); got != n {
					t.Errorf("count(%q) = %d, want %d:\n%s", s, got, n, d.HTML)
				}
			}
		})
	}
	t.Run("footnote ids", func(t *testing.T) {
		d := mustRender(t, "Top[^1].\n\n![[fn]]\n\n[^1]: top note\n", "docs/top.md", v)
		for _, w := range []string{`id="fnref:1"`, `id="embed-1-fnref:1"`, `href="#embed-1-fn:1"`} {
			if !strings.Contains(d.HTML, w) {
				t.Errorf("HTML lacks %q:\n%s", w, d.HTML)
			}
		}
	})
}

func TestStandardLinks(t *testing.T) {
	checkHTML(t, from, testVault(), []htmlCase{
		{name: "relative", src: "[x](../openspec/x.md)",
			want: []string{`<a class="internal-link" href="/openspec/x.md" data-path="openspec/x.md">x</a>`}},
		{name: "relative missing", src: "[x](../openspec/missing.md#Sec)",
			want: []string{`<a class="internal-link is-unresolved" href="/openspec/missing.md#Sec" data-path="openspec/missing.md">x</a>`}},
		{name: "outside repository", src: "[x](../../outside.md)",
			want: []string{`<a class="internal-link is-unresolved" href="../../outside.md" data-path="">x</a>`}},
		{name: "root relative", src: "[x](/AGENTS.md)",
			want: []string{`<a class="internal-link" href="/AGENTS.md" data-path="AGENTS.md">x</a>`}},
		{name: "percent decoded and query dropped", src: "[x](my%20file.md?raw=1#Part%20Two) [y](<my file.md>)",
			want: []string{`<a class="internal-link" href="/docs/my%20file.md#Part%20Two" data-path="docs/my file.md">x</a>`, `<a class="internal-link" href="/docs/my%20file.md" data-path="docs/my file.md">y</a>`}},
		{name: "directory", src: "[r](ref) [up](..)",
			want: []string{`class="internal-link is-unresolved" href="/docs/ref" data-path="docs/ref"`, `class="internal-link is-unresolved" href="/" data-path="."`}},
		{name: "fragment only", src: "[x](#frag)",
			want: []string{`<a href="#frag">x</a>`}},
		{name: "external", src: `[e](https://example.com/a?b=1 "T") [m](mailto:a@example.com) [p](//cdn.example.com/x)`,
			want: []string{
				`<a class="external-link" href="https://example.com/a?b=1" title="T" target="_blank" rel="noopener noreferrer">e</a>`,
				`<a class="external-link" href="mailto:a@example.com" target="_blank" rel="noopener noreferrer">m</a>`,
				`<a class="external-link" href="//cdn.example.com/x" target="_blank"`,
			}},
		{name: "autolinks", src: "<https://a.example.com> and www.b.example.com and https://c.example.com/#frag and me@example.com",
			want: []string{
				`<a class="external-link" href="https://a.example.com" target="_blank" rel="noopener noreferrer">https://a.example.com</a>`,
				`<a class="external-link" href="http://www.b.example.com"`,
				`<a class="external-link" href="https://c.example.com/#frag"`,
				`<a class="external-link" href="mailto:me@example.com" target="_blank" rel="noopener noreferrer">me@example.com</a>`,
			}},
		{name: "images", src: "![a](img/p.png) ![b](gone.png) ![c](https://example.com/c.png)",
			want: []string{`<img src="/docs/img/p.png" alt="a">`, `<img src="/docs/gone.png" alt="b" class="is-unresolved">`, `<img src="https://example.com/c.png" alt="c">`}},
	})
}

func TestCallouts(t *testing.T) {
	checkHTML(t, from, nil, []htmlCase{
		{name: "abstract with bold title", src: "> [!summary] The **game**\n> Body *text*.",
			want: []string{`<div class="callout" data-callout="abstract"><div class="callout-title"><span class="callout-icon" aria-hidden="true"></span><span class="callout-title-inner">The <strong>game</strong></span></div><div class="callout-content">
<p>Body <em>text</em>.</p>
</div></div>`}},
		{name: "default title", src: "> [!NOTE]\n> Body",
			want: []string{`data-callout="note"`, `<span class="callout-title-inner">Note</span>`}},
		{name: "foldable open", src: "> [!faq]+ Open one\n> Body",
			want: []string{`<details class="callout is-collapsible" data-callout="question" open><summary class="callout-title"><span class="callout-icon" aria-hidden="true"></span><span class="callout-title-inner">Open one</span></summary><div class="callout-content">`, "</div></details>"}},
		{name: "foldable closed", src: "> [!tip]- Closed",
			want:    []string{`<details class="callout is-collapsible" data-callout="tip">`, `<div class="callout-content">` + "\n</div></details>"},
			notWant: []string{" open>"}},
		{name: "nested", src: "> [!warning] Outer\n> Text\n> > [!error] Inner\n> > Deep",
			want: []string{`data-callout="warning"`, `<div class="callout" data-callout="danger"><div class="callout-title"><span class="callout-icon" aria-hidden="true"></span><span class="callout-title-inner">Inner</span></div><div class="callout-content">
<p>Deep</p>
</div></div>
</div></div>`}},
		{name: "unknown type", src: "> [!custom-kind] X", want: []string{`data-callout="custom-kind"`}},
		{name: "plain blockquote", src: "> Just a quote [!note]", want: []string{"<blockquote>"}, notWant: []string{"callout"}},
		{name: "block id on callout", src: "> [!note] T\n> Body ^cid", want: []string{`<p id="^cid">Body</p>`}},
	})
}

func TestMath(t *testing.T) {
	cases := []struct {
		htmlCase
		math bool
	}{
		{htmlCase{name: "inline with underscores and braces", src: "Exhaust $v_e=10{,}000$ m/s and $a_b$ c_d_",
			want:    []string{`<span class="math math-inline">v_e=10{,}000</span>`, `<span class="math math-inline">a_b</span>`},
			notWant: []string{"<em>"}}, true},
		{htmlCase{name: "currency", src: "Costs $5 and $10 today.\n\nPrice $10.\n\nPay $ 5 or 5$ now $x $y.",
			notWant: []string{"math"}}, false},
		{htmlCase{name: "escaped dollar", src: `Literal \$5 and $a\$b$.`,
			want: []string{`Literal $5 and <span class="math math-inline">a\$b</span>`}}, true},
		{htmlCase{name: "no markdown inside", src: "$a*b*c$ and $<x>$",
			want: []string{`<span class="math math-inline">a*b*c</span>`, `<span class="math math-inline">&lt;x&gt;</span>`}}, true},
		{htmlCase{name: "inline display", src: "Inline $$a+b$$ display.",
			want: []string{`<p>Inline <span class="math math-block">a+b</span> display.</p>`}}, true},
		{htmlCase{name: "block", src: "Before\n$$\nE = mc^2 < x\n\\frac{a}{b}\n$$\nAfter",
			want: []string{"<p>Before</p>\n<div class=\"math math-block\">E = mc^2 &lt; x\n\\frac{a}{b}</div>\n<p>After</p>"}}, true},
		{htmlCase{name: "single line block", src: "$$x^2$$\n\nNext *para*",
			want: []string{"<div class=\"math math-block\">x^2</div>\n<p>Next <em>para</em></p>"}}, true},
		{htmlCase{name: "block with closing text", src: "$$ a\nb $$",
			want: []string{`<div class="math math-block">a
b</div>`}}, true},
		{htmlCase{name: "code is not math", src: "`$x$`\n\n```\n$$\n```",
			notWant: []string{"math"}}, false},
	}
	for _, tc := range cases {
		checkHTML(t, from, nil, []htmlCase{tc.htmlCase})
		if got := mustRender(t, tc.src, from, nil).Math; got != tc.math {
			t.Errorf("%s: Math = %v, want %v", tc.name, got, tc.math)
		}
	}
}

func TestCode(t *testing.T) {
	checkHTML(t, from, nil, []htmlCase{
		{name: "mermaid", src: "```mermaid\ngraph TD\n  A-->B[Line<br>two]\n```",
			want:    []string{"<pre class=\"mermaid\">graph TD\n  A--&gt;B[Line&lt;br&gt;two]\n</pre>"},
			notWant: []string{"<br>", "chroma"}},
		{name: "go", src: "```go\nfunc main() {}\n```",
			want:    []string{`<pre class="code-block chroma" data-lang="go"><code>`, `<span class="kd">func</span>`, `<span class="nf">main</span>`, "</code></pre>"},
			notWant: []string{"style="}},
		{name: "unknown language", src: "```nosuchlang\n<b>x</b>\n```",
			want: []string{`<pre class="code-block chroma" data-lang=""><code>`, "&lt;b&gt;x&lt;/b&gt;"}},
		{name: "indented", src: "Para\n\n    code <here>\n",
			want: []string{`<pre class="code-block chroma" data-lang=""><code>`, "code &lt;here&gt;"}},
		{name: "raw html passes through", src: "<div class=\"x\">raw</div>\n\nText <kbd>K</kbd>",
			want: []string{`<div class="x">raw</div>`, "<kbd>K</kbd>"}},
		{name: "gfm", src: "~~gone~~\n\n- [x] done\n- [ ] todo",
			want: []string{"<del>gone</del>", `<input checked="" disabled="" type="checkbox"> done`}},
		{name: "soft breaks stay soft", src: "one\ntwo", want: []string{"<p>one\ntwo</p>"}},
	})
	for _, tc := range []struct {
		src  string
		want bool
	}{{"```mermaid\nx\n```", true}, {"```Mermaid\nx\n```", true}, {"```go\nx\n```", false}} {
		if got := mustRender(t, tc.src, from, nil).Mermaid; got != tc.want {
			t.Errorf("Mermaid(%q) = %v, want %v", tc.src, got, tc.want)
		}
	}
}

func TestHighlight(t *testing.T) {
	tests := []struct {
		name, code, lang, filename string
		lines                      bool
		want                       []string
	}{
		{"filename with line numbers", "package main\n\nfunc f() {}\n", "", "main.go", true,
			[]string{`<pre class="code-block chroma source" data-lang="go"><code>`, `<span class="ln" id="L1"><a class="lnlinks" href="#L1">1</a></span>`, `id="L3"`, `<span class="kn">package</span>`}},
		{"language wins", "x = 1", "Python", "main.go", false,
			[]string{`data-lang="python"`}},
		{"unknown", "<x>", "nope", "", false,
			[]string{`<pre class="code-block chroma" data-lang=""><code><span class="line"><span class="cl">&lt;x&gt;</span></span></code></pre>`}},
		{"dockerfile", "FROM x", "", "Dockerfile", false, []string{`data-lang="docker"`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Highlight([]byte(tc.code), tc.lang, tc.filename, tc.lines)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("lacks %q:\n%s", w, got)
				}
			}
			if strings.Contains(got, "style=") {
				t.Errorf("has inline style:\n%s", got)
			}
		})
	}
}

func TestHighlightCSS(t *testing.T) {
	css := HighlightCSS()
	for _, w := range []string{
		`html[data-theme="light"] .chroma .kd {`,
		`html[data-theme="dark"] .chroma .kd {`,
		`html[data-theme="light"] .chroma .ln, html[data-theme="light"] .chroma .ln a { color: #8c959f; }`,
		`html[data-theme="dark"] .chroma .ln, html[data-theme="dark"] .chroma .ln a { color: #6e7681; }`,
	} {
		if !strings.Contains(css, w) {
			t.Errorf("CSS lacks %q", w)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(css), "\n") {
		if !strings.HasPrefix(line, `html[data-theme="`) || strings.Contains(line, "/*") {
			t.Errorf("unscoped or commented rule %q", line)
		}
		if strings.Contains(line, ".bg ") || strings.HasPrefix(line[strings.Index(line, "] ")+2:], ".chroma {") && strings.Contains(line, "background") {
			t.Errorf("rule sets the code block background: %q", line)
		}
	}
}

func TestInlineSyntax(t *testing.T) {
	checkHTML(t, from, testVault(), []htmlCase{
		{name: "mark", src: "A ==marked **bold**== text", want: []string{"A <mark>marked <strong>bold</strong></mark> text"}},
		{name: "not mark", src: "a == b and x==y and ===three===", want: []string{"a == b and x==y and ===three==="}, notWant: []string{"<mark>"}},
		{name: "inline comment", src: "Text %%hidden%% visible %%multi\nline%% end", want: []string{"<p>Text  visible  end</p>"}, notWant: []string{"hidden", "multi"}},
		{name: "block comment", src: "Before\n\n%%\nsecret\n\nmore\n%%\n\n%% one line %%\n\nAfter", want: []string{"<p>Before</p>\n<p>After</p>"}, notWant: []string{"secret", "one line", "%%"}},
		{name: "not a comment", src: "100% sure, %%unclosed\n\n`%%code%%`", want: []string{"<p>100% sure, %%unclosed</p>", "<code>%%code%%</code>"}},
		{name: "tags", src: "#start mid #tag-a/b_1 #über end",
			want: []string{`<a class="tag" href="/_/search?q=tag%3A%23start">#start</a>`, `<a class="tag" href="/_/search?q=tag%3A%23tag-a%2Fb_1">#tag-a/b_1</a>`, `<a class="tag" href="/_/search?q=tag%3A%23%C3%BCber">#über</a>`}},
		{name: "not tags", src: "Issue #1 and #2024, C# code, (#paren), a#b, `#code`, [link #x](note.md), [[note|#y]], https://example.com/#frag, #",
			notWant: []string{`class="tag"`}},
		{name: "tag after heading marker", src: "# #heading-tag", want: []string{`<h1 id="heading-tag"><a class="tag"`}},
		{name: "block id paragraph", src: "Some text ^para-1", want: []string{`<p id="^para-1">Some text</p>`}},
		{name: "block id list item", src: "- one ^li1\n- two", want: []string{`<li id="^li1">one</li>`}},
		{name: "standalone block id", src: "| a |\n|---|\n| b |\n\n^tbl", want: []string{`<table id="^tbl">`}, notWant: []string{"^tbl</p>"}},
		{name: "not block ids", src: "x^2 and a ^b c\n\n`code ^id`", want: []string{"x^2 and a ^b c", "<code>code ^id</code>"}, notWant: []string{`id="^`}},
		{name: "footnotes", src: "Text[^n].\n\n[^n]: Note.", want: []string{`<sup id="fnref:1"><a href="#fn:1"`, `<li id="fn:1">`}},
	})
}

func TestTagsList(t *testing.T) {
	d := mustRender(t, "---\ntags: [Alpha, \"#beta\"]\ntag: \"gamma, delta #eps\"\n---\n#alpha #zeta #Zeta `#code` #beta\n", from, nil)
	want := []string{"Alpha", "beta", "gamma", "delta", "eps", "zeta"}
	if !reflect.DeepEqual(d.Tags, want) {
		t.Errorf("Tags = %q, want %q", d.Tags, want)
	}
}

func TestFrontmatter(t *testing.T) {
	src := "---\ntitle: Plan\naliases: [P, \"The Plan\"]\nrelated:\n  - \"[[outline-revised]]\"\n  - \"[[other#Sub|deep]]\"\nowner: \"[[note]]\"\ncount: 3\nempty:\nnested: {a: 1, b: [x, y]}\ntags: \"#one two\"\n---\n# Body\n"
	d := mustRender(t, src, from, testVault())
	want := []Property{
		{Key: "title", Values: []PropertyValue{{Text: "Plan"}}},
		{Key: "aliases", List: true, Values: []PropertyValue{{Text: "P"}, {Text: "The Plan"}}},
		{Key: "related", List: true, Values: []PropertyValue{
			{Text: "outline-revised", Href: "/docs/outline-revised.md", Link: true, Unresolved: true},
			{Text: "deep", Href: "/docs/other.md#sub", Path: "docs/other.md", Link: true},
		}},
		{Key: "owner", Values: []PropertyValue{{Text: "note", Href: "/docs/note.md", Path: "docs/note.md", Link: true}}},
		{Key: "count", Values: []PropertyValue{{Text: "3"}}},
		{Key: "empty"},
		{Key: "nested", Values: []PropertyValue{{Text: "{a: 1, b: [x, y]}"}}},
		{Key: "tags", Values: []PropertyValue{
			{Text: "one", Href: "/_/search?q=tag%3A%23one"},
			{Text: "two", Href: "/_/search?q=tag%3A%23two"},
		}},
	}
	if !reflect.DeepEqual(d.Properties, want) {
		t.Errorf("Properties =\n%+v\nwant\n%+v", d.Properties, want)
	}
	if d.Title != "Plan" || !reflect.DeepEqual(d.Aliases, []string{"P", "The Plan"}) || !reflect.DeepEqual(d.Tags, []string{"one", "two"}) {
		t.Errorf("Title %q, Aliases %q, Tags %q", d.Title, d.Aliases, d.Tags)
	}
	if d.PropertiesError != "" || strings.Contains(d.HTML, "related") || !strings.HasPrefix(d.HTML, `<h1 id="body">`) {
		t.Errorf("error %q, HTML:\n%s", d.PropertiesError, d.HTML)
	}
	wantLinks := []Link{
		{Target: "outline-revised", Wikilink: true, Context: "related: outline-revised"},
		{Path: "docs/other.md", Target: "other", Fragment: "sub", Wikilink: true, Context: "related: deep"},
		{Path: "docs/note.md", Target: "note", Wikilink: true, Context: "owner: note"},
	}
	if !reflect.DeepEqual(d.Links, wantLinks) {
		t.Errorf("Links =\n%+v\nwant\n%+v", d.Links, wantLinks)
	}
}

func TestFrontmatterEdgeCases(t *testing.T) {
	tests := []struct {
		name, src, err, body string
		props                int
	}{
		{"invalid yaml", "---\nkey: [unclosed\n---\n# Body\n", "yaml:", `<h1 id="body">Body</h1>`, 0},
		{"not a mapping", "---\n- a\n- b\n---\nText\n", "frontmatter is not a mapping of properties", "<p>Text</p>", 0},
		{"empty", "---\n---\nText\n", "", "<p>Text</p>", 0},
		{"dots end", "---\na: 1\n...\nText\n", "", "<p>Text</p>", 1},
		{"byte order mark", "\ufeff---\na: 1\n---\nText\n", "", "<p>Text</p>", 1},
		{"crlf", "---\r\na: 1\r\n---\r\nText\r\n", "", "<p>Text</p>", 1},
		{"unclosed is body", "---\na: 1\n", "", "<hr>", 0},
		{"not first line", "\n---\na: 1\n---\n", "", "<hr>", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := mustRender(t, tc.src, from, nil)
			if !strings.Contains(d.PropertiesError, tc.err) || (tc.err == "") != (d.PropertiesError == "") {
				t.Errorf("PropertiesError = %q, want %q", d.PropertiesError, tc.err)
			}
			if !strings.Contains(d.HTML, tc.body) {
				t.Errorf("HTML lacks %q:\n%s", tc.body, d.HTML)
			}
			if len(d.Properties) != tc.props {
				t.Errorf("Properties = %+v, want %d", d.Properties, tc.props)
			}
		})
	}
}

func TestLinks(t *testing.T) {
	long := strings.Repeat("word ", 80)
	src := "# Head [[note]]\n\nPara with [[other#Sub|a sub]] and [rel](../openspec/x.md) and [ext](https://example.com) and [top](#top) and ![img](img/p.png).\n\n- item [[#Head note]]\n  continued\n\n| h |\n|---|\n| cell ![[pic.png\\|300]] |\n\n> [!note] Title [[api]]\n\n![[other#Section Two]]\n\n" + long + "[[missing]] " + long + "\n"
	d := mustRender(t, src, from, testVault())
	para := "Para with a sub and rel and ext and top and ."
	want := []Link{
		{Path: "docs/note.md", Target: "note", Wikilink: true, Context: "Head note"},
		{Path: "docs/other.md", Target: "other", Fragment: "sub", Wikilink: true, Context: para},
		{Path: "openspec/x.md", Target: "../openspec/x.md", Context: para},
		{Path: "docs/img/p.png", Target: "img/p.png", Embed: true, Context: para},
		{Path: from, Fragment: "head-note", Wikilink: true, Context: "item Head note continued"},
		{Path: "docs/pic.png", Target: "pic.png", Embed: true, Wikilink: true, Context: "cell pic.png"},
		{Path: "docs/api.md", Target: "api", Wikilink: true, Context: "Title api"},
		{Path: "docs/other.md", Target: "other", Fragment: "section-two", Embed: true, Wikilink: true, Context: "other > Section Two"},
		{Target: "missing", Wikilink: true},
	}
	if len(d.Links) != len(want) {
		t.Fatalf("Links = %d:\n%+v", len(d.Links), d.Links)
	}
	for i, got := range d.Links {
		w := want[i]
		if w.Context == "" {
			w.Context = got.Context
		}
		if got != w {
			t.Errorf("Links[%d] =\n%+v\nwant\n%+v", i, got, w)
		}
	}
	ctx := d.Links[len(d.Links)-1].Context
	if n := len([]rune(ctx)); n > maxContext+2 || !strings.HasPrefix(ctx, "…") || !strings.HasSuffix(ctx, "…") || !strings.Contains(ctx, "missing") {
		t.Errorf("long context (%d runes) = %q", n, ctx)
	}
}

func TestNoResolver(t *testing.T) {
	d := mustRender(t, "[[a]] ![[b]] ![[c.png]] [d](d.md)", from, nil)
	for _, w := range []string{`data-target="a"`, `<span class="internal-embed is-unresolved">b</span>`, `<span class="internal-embed is-unresolved">c.png</span>`, `class="internal-link is-unresolved" href="/docs/d.md" data-path="docs/d.md"`} {
		if !strings.Contains(d.HTML, w) {
			t.Errorf("HTML lacks %q:\n%s", w, d.HTML)
		}
	}
	if d.Links == nil || d.Tags == nil || d.Aliases == nil || d.Headings == nil {
		t.Errorf("nil slices in %+v", d)
	}
}

func TestConcurrentRender(t *testing.T) {
	v := testVault()
	v["docs/big.md"] = "---\ntags: [x]\n---\n# Big #tag\n\n> [!note] N\n> $x$ [[other#Sub]] ==m==\n\n![[other#Section Two]]\n\n```go\nfunc f() {}\n```\n\n```mermaid\nA-->B\n```\n\n[^1]\n\n[^1]: fn\n"
	paths := []string{"docs/big.md", "docs/other.md", "docs/note.md"}
	want := map[string]*Document{}
	for _, p := range paths {
		want[p] = mustRender(t, v[p], p, v)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := paths[i%len(paths)]
			d, err := Render([]byte(v[p]), p, v)
			switch {
			case err != nil:
				errs <- err
			case !reflect.DeepEqual(d, want[p]):
				errs <- fmt.Errorf("%s: concurrent render differs", p)
			}
			_ = Headings([]byte(v[p]))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
