package markdown

import (
	"bytes"
	"html"
	"path"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var kindWikilink = ast.NewNodeKind("Wikilink")

// wikilink is an Obsidian [[link]] or ![[embed]] and its resolved output.
type wikilink struct {
	ast.BaseInline
	ref   wikiRef
	embed bool

	// Set by resolveWikilink.
	path       string
	href       string
	unresolved bool
	// Set by resolveEmbed for embeds.
	kind    embedKind
	width   string
	height  string
	content string
}

func (n *wikilink) Kind() ast.NodeKind { return kindWikilink }

func (n *wikilink) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"Target": n.ref.target}, nil)
}

// wikiRef is the parsed inside of [[...]].
type wikiRef struct {
	target string
	// headings are the fragment segments; the last one is resolved.
	headings []string
	alias    string
	hasAlias bool
}

// parseWikiRef parses "target#heading|alias". In table cells the alias
// separator is written "\|".
func parseWikiRef(inner string) wikiRef {
	inner = strings.ReplaceAll(inner, `\|`, "|")
	var ref wikiRef
	if before, alias, ok := strings.Cut(inner, "|"); ok {
		inner, ref.alias, ref.hasAlias = before, strings.TrimSpace(alias), true
	}
	target, fragment, _ := strings.Cut(inner, "#")
	ref.target = strings.TrimSpace(target)
	for _, h := range strings.Split(fragment, "#") {
		if h = strings.TrimSpace(h); h != "" {
			ref.headings = append(ref.headings, h)
		}
	}
	return ref
}

// heading returns the fragment segment that resolution uses.
func (r wikiRef) heading() string {
	if len(r.headings) == 0 {
		return ""
	}
	return r.headings[len(r.headings)-1]
}

// text returns the display text: the alias, else "target > heading", else
// whichever of the two exists.
func (r wikiRef) text() string {
	if r.alias != "" {
		return r.alias
	}
	sub := strings.Join(r.headings, " > ")
	switch {
	case r.target != "" && sub != "":
		return r.target + " > " + sub
	case r.target != "":
		return r.target
	}
	return sub
}

// wikilinkParser parses [[links]] and ![[embeds]] before standard links.
type wikilinkParser struct{}

func (wikilinkParser) Trigger() []byte { return []byte{'[', '!'} }

func (wikilinkParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	line, _ := block.PeekLine()
	start := 0
	if len(line) > 0 && line[0] == '!' {
		start = 1
	}
	if !bytes.HasPrefix(line[start:], []byte("[[")) {
		return nil
	}
	open := start + 2
	end := bytes.Index(line[open:], []byte("]]"))
	if end < 0 {
		return nil
	}
	inner := line[open : open+end]
	if len(bytes.TrimSpace(inner)) == 0 || bytes.ContainsAny(inner, "[]\n") {
		return nil
	}
	block.Advance(open + end + 2)
	return &wikilink{ref: parseWikiRef(string(inner)), embed: start == 1}
}

// resolveRef finds the file and anchor that ref names from this document.
func (st *renderState) resolveRef(ref wikiRef) (file, fragment string, ok bool) {
	file, ok = st.from, true
	if ref.target != "" {
		file, ok = st.resolver.ResolveWikilink(st.from, ref.target)
	}
	h := ref.heading()
	switch {
	case h == "":
	case strings.HasPrefix(h, "^"):
		fragment = h
	case ok && file == st.from:
		fragment = st.headingID(h)
	case ok:
		if id, found := st.resolver.HeadingID(file, h); found {
			fragment = id
		} else {
			fragment = Slug(h)
		}
	default:
		fragment = Slug(h)
	}
	if !ok {
		file = ""
	}
	return file, fragment, ok
}

// hrefFor returns the link URL of ref, guessing a note beside this document
// when the target does not resolve.
func (st *renderState) hrefFor(ref wikiRef, file, fragment string, ok bool) string {
	if ok {
		return URL(file, fragment)
	}
	guess := path.Join(path.Dir(st.from), ref.target)
	if path.Ext(guess) == "" {
		guess += ".md"
	}
	if escapesRoot(guess) {
		guess = path.Base(guess)
	}
	return URL(guess, "")
}

func (st *renderState) resolveWikilink(n *wikilink, source []byte) {
	file, fragment, ok := st.resolveRef(n.ref)
	n.path, n.unresolved = file, !ok
	n.href = st.hrefFor(n.ref, file, fragment, ok)
	st.links = append(st.links, Link{
		Path:     file,
		Target:   n.ref.target,
		Fragment: fragment,
		Embed:    n.embed,
		Wikilink: true,
		Context:  linkContext(n, source),
	})
	if n.embed {
		st.resolveEmbed(n, fragment)
	}
}

func renderWikilink(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*wikilink)
	if n.embed {
		writeEmbed(w, n)
	} else {
		writeInternalLink(w, n)
	}
	return ast.WalkSkipChildren, nil
}

// writeInternalLink writes n as an internal link with its display text.
func writeInternalLink(w util.BufWriter, n *wikilink) {
	if n.unresolved {
		_, _ = w.WriteString(`<a class="internal-link is-unresolved" href="` + html.EscapeString(n.href) +
			`" data-path="" data-target="` + html.EscapeString(n.ref.target) + `">`)
	} else {
		_, _ = w.WriteString(`<a class="internal-link" href="` + html.EscapeString(n.href) +
			`" data-path="` + html.EscapeString(n.path) + `">`)
	}
	_, _ = w.WriteString(html.EscapeString(n.label()))
	_, _ = w.WriteString("</a>")
}
