package markdown

import (
	"bytes"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

var (
	kindComment      = ast.NewNodeKind("Comment")
	kindCommentBlock = ast.NewNodeKind("CommentBlock")
)

var commentFence = []byte("%%")

// comment is an inline %%comment%%, which renders nothing.
type comment struct {
	ast.BaseInline
}

func (n *comment) Kind() ast.NodeKind { return kindComment }

func (n *comment) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

// commentParser parses %%comments%%, which may span lines of a paragraph.
type commentParser struct{}

func (commentParser) Trigger() []byte { return []byte{'%'} }

func (commentParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	line, _ := block.PeekLine()
	if !bytes.HasPrefix(line, commentFence) {
		return nil
	}
	l, pos := block.Position()
	offset := len(commentFence)
	for line != nil {
		if i := bytes.Index(line[offset:], commentFence); i >= 0 {
			block.Advance(offset + i + len(commentFence))
			return &comment{}
		}
		block.AdvanceLine()
		line, _ = block.PeekLine()
		offset = 0
	}
	block.SetPosition(l, pos)
	return nil
}

// commentBlock is a %% comment %% that starts a line and may span lines.
type commentBlock struct {
	fencedBlock
}

func (n *commentBlock) Kind() ast.NodeKind { return kindCommentBlock }

func (n *commentBlock) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

var commentBlockParser = fencedParser{fence: commentFence, new: func() fencedNode { return &commentBlock{} }}
