package markdown

import (
	"bytes"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var (
	kindMathInline = ast.NewNodeKind("MathInline")
	kindMathBlock  = ast.NewNodeKind("MathBlock")
)

// mathInline is $TeX$, or $$TeX$$ in display mode, inside a paragraph.
type mathInline struct {
	ast.BaseInline
	tex     []byte
	display bool
}

func (n *mathInline) Kind() ast.NodeKind { return kindMathInline }

func (n *mathInline) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"TeX": string(n.tex)}, nil)
}

// mathParser parses inline math. The opening "$" is not followed by
// whitespace; the closing "$" is not preceded by whitespace or followed by
// a digit, so prices such as "$5 and $10" stay text.
type mathParser struct{}

func (mathParser) Trigger() []byte { return []byte{'$'} }

func (mathParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	line, _ := block.PeekLine()
	if bytes.HasPrefix(line, []byte("$$")) {
		end := bytes.Index(line[2:], []byte("$$"))
		if end <= 0 {
			return nil
		}
		block.Advance(end + 4)
		return &mathInline{tex: bytes.Clone(bytes.TrimSpace(line[2 : end+2])), display: true}
	}
	if len(line) < 3 || util.IsSpace(line[1]) {
		return nil
	}
	for i := 1; i < len(line); i++ {
		switch line[i] {
		case '\\':
			i++
		case '$':
			if util.IsSpace(line[i-1]) || (i+1 < len(line) && isDigit(line[i+1])) {
				continue
			}
			block.Advance(i + 1)
			return &mathInline{tex: bytes.Clone(line[1:i])}
		}
	}
	return nil
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

func renderMathInline(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		n := node.(*mathInline)
		class := "math math-inline"
		if n.display {
			class = "math math-block"
		}
		_, _ = w.WriteString(`<span class="` + class + `">`)
		_, _ = w.Write(util.EscapeHTML(n.tex))
		_, _ = w.WriteString("</span>")
	}
	return ast.WalkSkipChildren, nil
}

// mathBlock is display math from a line starting with "$$" through a line
// ending with "$$".
type mathBlock struct {
	fencedBlock
}

func (n *mathBlock) Kind() ast.NodeKind { return kindMathBlock }

func (n *mathBlock) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

var mathBlockParser = fencedParser{fence: []byte("$$"), new: func() fencedNode { return &mathBlock{} }}

func renderMathBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString(`<div class="math math-block">`)
		_, _ = w.Write(util.EscapeHTML(bytes.TrimSpace(node.Lines().Value(source))))
		_, _ = w.WriteString("</div>\n")
	}
	return ast.WalkSkipChildren, nil
}
