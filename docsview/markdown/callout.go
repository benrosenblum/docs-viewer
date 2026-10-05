package markdown

import (
	"html"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark/ast"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

var (
	kindCallout        = ast.NewNodeKind("Callout")
	kindCalloutTitle   = ast.NewNodeKind("CalloutTitle")
	kindCalloutContent = ast.NewNodeKind("CalloutContent")
)

// calloutPattern matches "[!type]", "[!type]+", or "[!type]-" at the start
// of a blockquote.
var calloutPattern = regexp.MustCompile(`^[ \t]*\[!([^\]\s]+)\]([+-]?)[ \t]*`)

// calloutAliases maps callout type aliases to their canonical type.
var calloutAliases = map[string]string{
	"summary": "abstract", "tldr": "abstract",
	"hint": "tip", "important": "tip",
	"check": "success", "done": "success",
	"help": "question", "faq": "question",
	"caution": "warning", "attention": "warning",
	"fail": "failure", "missing": "failure",
	"error": "danger",
	"cite":  "quote",
}

// callout is an Obsidian callout; its children are a calloutTitle and a
// calloutContent.
type callout struct {
	ast.BaseBlock
	calloutType string
	foldable    bool
	open        bool
}

func (n *callout) Kind() ast.NodeKind { return kindCallout }

func (n *callout) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"Type": n.calloutType}, nil)
}

type calloutTitle struct{ ast.BaseBlock }

func (n *calloutTitle) Kind() ast.NodeKind            { return kindCalloutTitle }
func (n *calloutTitle) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

type calloutContent struct{ ast.BaseBlock }

func (n *calloutContent) Kind() ast.NodeKind            { return kindCalloutContent }
func (n *calloutContent) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

// convertCallouts replaces blockquotes that start with a callout marker.
func convertCallouts(doc ast.Node, source []byte) {
	var quotes []*ast.Blockquote
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if q, ok := n.(*ast.Blockquote); ok && entering {
			quotes = append(quotes, q)
		}
		return ast.WalkContinue, nil
	})
	for _, q := range quotes {
		convertCallout(q, source)
	}
}

func convertCallout(q *ast.Blockquote, source []byte) {
	p, ok := q.FirstChild().(*ast.Paragraph)
	if !ok || p.Lines().Len() == 0 {
		return
	}
	line := p.Lines().At(0)
	value := line.Value(source)
	m := calloutPattern.FindSubmatchIndex(value)
	if m == nil {
		return
	}
	kind := strings.ToLower(string(value[m[2]:m[3]]))
	fold := string(value[m[4]:m[5]])
	c := &callout{calloutType: kind, foldable: fold != "", open: fold == "+"}
	if canonical, ok := calloutAliases[kind]; ok {
		c.calloutType = canonical
	}
	title := &calloutTitle{}
	moveTitle(p, title, line.Start+m[1])
	if !hasText(title, source) {
		title.RemoveChildren(title)
		title.AppendChild(title, rawString(capitalize(kind)))
	}
	if p.ChildCount() == 0 {
		q.RemoveChild(q, p)
	}
	content := &calloutContent{}
	for _, n := range childNodes(q) {
		content.AppendChild(content, n)
	}
	c.AppendChild(c, title)
	c.AppendChild(c, content)
	q.Parent().ReplaceChild(q.Parent(), q, c)
}

// moveTitle moves the inline nodes of the first line of p into title and
// drops the callout marker, which ends at markerEnd in the source.
func moveTitle(p *ast.Paragraph, title *calloutTitle, markerEnd int) {
	for n := p.FirstChild(); n != nil; {
		next := n.NextSibling()
		lineEnd := false
		if t, ok := n.(*ast.Text); ok {
			lineEnd = t.SoftLineBreak() || t.HardLineBreak()
			t.SetSoftLineBreak(false)
			t.SetHardLineBreak(false)
			if t.Segment.Start < markerEnd {
				t.Segment = t.Segment.WithStart(min(markerEnd, t.Segment.Stop))
			}
		} else if n.Pos() >= 0 && n.Pos() < markerEnd {
			p.RemoveChild(p, n)
			n = next
			continue
		}
		title.AppendChild(title, n)
		if lineEnd {
			return
		}
		n = next
	}
}

func hasText(n ast.Node, source []byte) bool {
	return strings.TrimSpace(plainText(n, source)) != ""
}

func capitalize(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

// rawString returns an inline node that renders s as escaped text.
func rawString(s string) *ast.String {
	n := ast.NewString([]byte(s))
	n.SetRaw(true)
	return n
}

func renderCallout(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*callout)
	tag := "div"
	if n.foldable {
		tag = "details"
	}
	if !entering {
		_, _ = w.WriteString("</" + tag + ">\n")
		return ast.WalkContinue, nil
	}
	_, _ = w.WriteString("<" + tag + ` class="callout`)
	if n.foldable {
		_, _ = w.WriteString(" is-collapsible")
	}
	_, _ = w.WriteString(`" data-callout="` + html.EscapeString(n.calloutType) + `"`)
	gmhtml.RenderAttributes(w, n, gmhtml.GlobalAttributeFilter)
	if n.open {
		_, _ = w.WriteString(" open")
	}
	_ = w.WriteByte('>')
	return ast.WalkContinue, nil
}

func renderCalloutTitle(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	tag := "div"
	if c, ok := node.Parent().(*callout); ok && c.foldable {
		tag = "summary"
	}
	if entering {
		_, _ = w.WriteString("<" + tag + ` class="callout-title"><span class="callout-icon" aria-hidden="true"></span><span class="callout-title-inner">`)
	} else {
		_, _ = w.WriteString("</span></" + tag + ">")
	}
	return ast.WalkContinue, nil
}

func renderCalloutContent(w util.BufWriter, _ []byte, _ ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<div class=\"callout-content\">\n")
	} else {
		_, _ = w.WriteString("</div>")
	}
	return ast.WalkContinue, nil
}
