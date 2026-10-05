package markdown

import (
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var kindMark = ast.NewNodeKind("Mark")

// mark is ==highlighted text==.
type mark struct {
	ast.BaseInline
}

func (n *mark) Kind() ast.NodeKind { return kindMark }

func (n *mark) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

type markDelimiter struct{}

func (markDelimiter) IsDelimiter(b byte) bool { return b == '=' }

func (markDelimiter) CanOpenCloser(opener, closer *parser.Delimiter) bool {
	return opener.Char == closer.Char
}

func (markDelimiter) OnMatch(int) ast.Node { return &mark{} }

// markParser parses "==" delimiters with the flanking rules of "~~".
type markParser struct{}

func (markParser) Trigger() []byte { return []byte{'='} }

func (markParser) Parse(_ ast.Node, block text.Reader, pc parser.Context) ast.Node {
	before := block.PrecendingCharacter()
	line, segment := block.PeekLine()
	d := parser.ScanDelimiter(line, before, 2, markDelimiter{})
	if d == nil || d.OriginalLength != 2 || before == '=' {
		return nil
	}
	d.Segment = segment.WithStop(segment.Start + d.OriginalLength)
	block.Advance(d.OriginalLength)
	pc.PushDelimiter(d)
	return d
}

func renderMark(w util.BufWriter, _ []byte, _ ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<mark>")
	} else {
		_, _ = w.WriteString("</mark>")
	}
	return ast.WalkContinue, nil
}
