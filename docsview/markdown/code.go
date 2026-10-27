package markdown

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/util"
)

// isMermaid reports whether a fenced code block holds a Mermaid diagram.
func isMermaid(n *ast.FencedCodeBlock, source []byte) bool {
	return strings.EqualFold(string(n.Language(source)), "mermaid")
}

// renderCodeSpan writes an inline code span. Unlike goldmark's renderer, it
// accepts word marks (diffWord) between the text nodes.
func renderCodeSpan(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, _ = w.WriteString("</code>")
		return ast.WalkContinue, nil
	}
	_, _ = w.WriteString("<code>")
	var write func(ast.Node)
	write = func(parent ast.Node) {
		for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
			switch c := c.(type) {
			case *ast.Text:
				_, _ = w.Write(util.EscapeHTML(bytes.ReplaceAll(c.Segment.Value(source), []byte("\n"), []byte(" "))))
			case *diffWord:
				_, _ = renderDiffWord(w, source, c, true)
				write(c)
				_, _ = renderDiffWord(w, source, c, false)
			}
		}
	}
	write(node)
	return ast.WalkSkipChildren, nil
}

// renderCode writes Mermaid sources for the browser and highlights other
// fenced and indented code blocks.
func renderCode(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	code := node.Lines().Value(source)
	lang := ""
	if n, ok := node.(*ast.FencedCodeBlock); ok {
		if isMermaid(n, source) {
			_, _ = w.WriteString(`<pre class="mermaid">`)
			_, _ = w.Write(util.EscapeHTML(code))
			_, _ = w.WriteString("</pre>\n")
			return ast.WalkSkipChildren, nil
		}
		lang = string(unescapeText(n.Language(source)))
	}
	_, _ = w.WriteString(Highlight(code, lang, "", false))
	_ = w.WriteByte('\n')
	return ast.WalkSkipChildren, nil
}
