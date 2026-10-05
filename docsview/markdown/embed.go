package markdown

import (
	"errors"
	"html"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/util"
)

var errNoSource = errors.New("no source")

// embedKind is how an ![[embed]] renders.
type embedKind uint8

const (
	// embedLink renders a normal internal link.
	embedLink embedKind = iota
	embedMissing
	embedImage
	embedNote
	embedCycle
)

var (
	imageExts   = []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".avif", ".bmp"}
	sizePattern = regexp.MustCompile(`^(\d+)(?:x(\d+))?$`)
)

var kindEmbedBlock = ast.NewNodeKind("EmbedBlock")

// embedBlock replaces a paragraph that holds only embeds, so transcluded
// notes are not nested in <p>.
type embedBlock struct {
	ast.BaseBlock
}

func (n *embedBlock) Kind() ast.NodeKind { return kindEmbedBlock }

func (n *embedBlock) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

// embedKey identifies an embedded document or section for cycle detection.
func embedKey(file, heading string) string { return file + "#" + heading }

// resolveEmbed decides how n renders and transcludes Markdown notes.
func (st *renderState) resolveEmbed(n *wikilink, fragment string) {
	if n.unresolved {
		n.kind = embedMissing
		return
	}
	ext := strings.ToLower(path.Ext(n.path))
	switch {
	case slices.Contains(imageExts, ext):
		n.kind = embedImage
		n.href = URL(n.path, "")
		if m := sizePattern.FindStringSubmatch(n.ref.alias); m != nil {
			n.width, n.height = m[1], m[2]
		}
	case ext == ".md":
		st.transclude(n, fragment)
	default:
		n.kind = embedLink
	}
}

// transclude renders the embedded note, or its section, into n.
func (st *renderState) transclude(n *wikilink, fragment string) {
	key := embedKey(n.path, fragment)
	if slices.Contains(st.chain, key) {
		n.kind = embedCycle
		return
	}
	if st.depth >= maxEmbedDepth {
		n.kind = embedLink
		return
	}
	src, err := st.resolver.Source(n.path)
	if err != nil {
		n.kind = embedMissing
		return
	}
	child := st.child(n.path, key)
	_, body, _ := splitFrontmatter(src)
	doc := child.parse(body)
	if fragment != "" {
		nodes := child.section(doc, n.ref.heading(), fragment)
		if nodes == nil {
			n.kind = embedMissing
			return
		}
		doc = ast.NewDocument()
		for _, c := range nodes {
			doc.AppendChild(doc, c)
		}
	}
	*st.embeds++
	doc.AddMeta(footnotePrefixKey, "embed-"+strconv.Itoa(*st.embeds)+"-")
	out, err := renderHTML(body, doc)
	if err != nil {
		n.kind = embedMissing
		return
	}
	n.kind, n.content = embedNote, out
	st.math = st.math || child.math
	st.mermaid = st.mermaid || child.mermaid
}

// section returns the blocks that a heading name (or its anchor, fragment)
// selects: the heading and what follows it up to the next heading of the
// same or a higher level. A "^id" fragment selects the block with that ID.
func (st *renderState) section(doc ast.Node, name, fragment string) []ast.Node {
	if strings.HasPrefix(fragment, "^") {
		var found ast.Node
		_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			if entering && attribute(n, "id") == fragment {
				found = n
				return ast.WalkStop, nil
			}
			return ast.WalkContinue, nil
		})
		switch found.(type) {
		case nil:
			return nil
		case *ast.ListItem:
			return childNodes(found)
		}
		return []ast.Node{found}
	}
	found, ok := st.findHeading(name)
	if !ok {
		i := slices.IndexFunc(st.headings, func(h heading) bool { return h.ID == fragment })
		if i < 0 {
			return nil
		}
		found = st.headings[i]
	}
	h := found.node
	nodes := []ast.Node{h}
	for n := h.NextSibling(); n != nil; n = n.NextSibling() {
		if next, ok := n.(*ast.Heading); ok && next.Level <= h.Level {
			break
		}
		nodes = append(nodes, n)
	}
	return nodes
}

// attribute returns the string value of a node attribute, or "".
func attribute(n ast.Node, name string) string {
	v, _ := n.AttributeString(name)
	switch v := v.(type) {
	case []byte:
		return string(v)
	case string:
		return v
	}
	return ""
}

func childNodes(n ast.Node) []ast.Node {
	var nodes []ast.Node
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		nodes = append(nodes, c)
	}
	return nodes
}

// unwrapEmbedParagraphs replaces paragraphs that contain only embeds, at
// least one of them a note, with embedBlock nodes.
func unwrapEmbedParagraphs(nodes []ast.Node, source []byte) {
	for _, n := range nodes {
		w, ok := n.(*wikilink)
		if !ok || !w.embed || (w.kind != embedNote && w.kind != embedCycle) {
			continue
		}
		p, ok := w.Parent().(*ast.Paragraph)
		if !ok || !onlyEmbeds(p, source) {
			continue
		}
		block := &embedBlock{}
		for _, c := range childNodes(p) {
			block.AppendChild(block, c)
		}
		p.Parent().ReplaceChild(p.Parent(), p, block)
		if class := attribute(p, "class"); class != "" {
			wrapBlock(block, class) // A diff mark on the paragraph.
		}
	}
}

func onlyEmbeds(p *ast.Paragraph, source []byte) bool {
	for c := p.FirstChild(); c != nil; c = c.NextSibling() {
		switch c := c.(type) {
		case *wikilink:
			if !c.embed {
				return false
			}
		case *ast.Text:
			if !util.IsBlank(c.Segment.Value(source)) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// label is the display text of n. An embed's size alias is not text.
func (n *wikilink) label() string {
	if n.embed && sizePattern.MatchString(n.ref.alias) {
		return n.ref.target
	}
	return n.ref.text()
}

func writeEmbed(w util.BufWriter, n *wikilink) {
	switch n.kind {
	case embedMissing:
		_, _ = w.WriteString(`<span class="internal-embed is-unresolved">` + html.EscapeString(n.label()) + `</span>`)
	case embedImage:
		_, _ = w.WriteString(`<img class="internal-embed" src="` + html.EscapeString(n.href) + `" alt="` + html.EscapeString(n.label()) + `"`)
		if n.width != "" {
			_, _ = w.WriteString(` width="` + n.width + `"`)
		}
		if n.height != "" {
			_, _ = w.WriteString(` height="` + n.height + `"`)
		}
		_, _ = w.WriteString(">")
	case embedNote:
		_, _ = w.WriteString(`<div class="markdown-embed" data-path="` + html.EscapeString(n.path) + `"><div class="markdown-embed-title">`)
		writeInternalLink(w, n)
		_, _ = w.WriteString("</div><div class=\"markdown-embed-content\">\n" + n.content + "</div></div>\n")
	case embedCycle:
		_, _ = w.WriteString(`<div class="markdown-embed is-cycle" data-path="` + html.EscapeString(n.path) + `">`)
		writeInternalLink(w, n)
		_, _ = w.WriteString("</div>\n")
	default:
		writeInternalLink(w, n)
	}
}
