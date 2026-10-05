package markdown

import (
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/util"
)

// isMermaid reports whether a fenced code block holds a Mermaid diagram.
func isMermaid(n *ast.FencedCodeBlock, source []byte) bool {
	return strings.EqualFold(string(n.Language(source)), "mermaid")
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
