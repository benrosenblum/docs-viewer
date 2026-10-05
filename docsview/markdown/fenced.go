package markdown

import (
	"bytes"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// fencedBlock is raw text from a line starting with a fence, such as "$$"
// or "%%", through the line that contains the closing fence.
type fencedBlock struct {
	ast.BaseBlock
	// closed is set when the opening line also closes the block.
	closed bool
}

func (n *fencedBlock) IsRaw() bool { return true }

func (n *fencedBlock) block() *fencedBlock { return n }

type fencedNode interface {
	ast.Node
	block() *fencedBlock
}

// fencedParser parses fencedBlock nodes that new creates. An opening line
// that closes the fence and has text after it is left to inline parsers.
type fencedParser struct {
	fence []byte
	new   func() fencedNode
}

func (p fencedParser) Trigger() []byte { return p.fence[:1] }

func (p fencedParser) Open(_ ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, segment := reader.PeekLine()
	pos := pc.BlockOffset()
	if pos < 0 || !bytes.HasPrefix(line[pos:], p.fence) {
		return nil, parser.NoChildren
	}
	rest := line[pos+len(p.fence):]
	if i := bytes.Index(rest, p.fence); i >= 0 && !util.IsBlank(rest[i+len(p.fence):]) {
		return nil, parser.NoChildren
	}
	n := p.new()
	start := segment.Start + pos + len(p.fence)
	n.block().closed = p.appendLine(n, reader, text.NewSegment(start, segment.Stop))
	return n, parser.NoChildren
}

func (p fencedParser) Continue(node ast.Node, reader text.Reader, _ parser.Context) parser.State {
	if node.(fencedNode).block().closed {
		return parser.Close
	}
	_, segment := reader.PeekLine()
	if p.appendLine(node, reader, segment) {
		return parser.Close
	}
	return parser.Continue | parser.NoChildren
}

// appendLine adds segment to n up to a closing fence, consumes the line,
// and reports whether the fence closed the block.
func (p fencedParser) appendLine(n ast.Node, reader text.Reader, segment text.Segment) bool {
	closed := false
	if i := bytes.Index(reader.Value(segment), p.fence); i >= 0 {
		segment = segment.WithStop(segment.Start + i)
		closed = true
	}
	n.Lines().Append(segment)
	reader.AdvanceToEOL()
	return closed
}

func (fencedParser) Close(ast.Node, text.Reader, parser.Context) {}
func (fencedParser) CanInterruptParagraph() bool                 { return true }
func (fencedParser) CanAcceptIndentedLine() bool                 { return false }
