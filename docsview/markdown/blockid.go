package markdown

import (
	"bytes"
	"regexp"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/util"
)

// blockIDPattern matches a trailing "^id" block ID.
var blockIDPattern = regexp.MustCompile(`\^([A-Za-z0-9-]+)[ \t]*$`)

// assignBlockIDs moves a trailing " ^id" of a paragraph or list item into
// an id="^id" attribute. A paragraph that holds only "^id" labels the block
// before it, as in Obsidian.
func assignBlockIDs(doc ast.Node, source []byte) {
	var blocks []ast.Node
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		switch n.(type) {
		case *ast.Paragraph, *ast.TextBlock:
			if entering {
				blocks = append(blocks, n)
			}
		}
		return ast.WalkContinue, nil
	})
	for _, b := range blocks {
		id, ok := cutBlockID(b, source)
		if !ok {
			continue
		}
		target := b
		if _, textBlock := b.(*ast.TextBlock); textBlock {
			if li, ok := b.Parent().(*ast.ListItem); ok {
				target = li
			}
		} else if prev := b.PreviousSibling(); prev != nil && !hasText(b, source) {
			target = prev
			b.Parent().RemoveChild(b.Parent(), b)
		}
		target.SetAttributeString("id", []byte(id))
	}
}

// cutBlockID removes a trailing block ID from the last text of b and
// returns it with its "^".
func cutBlockID(b ast.Node, source []byte) (string, bool) {
	t, ok := b.LastChild().(*ast.Text)
	if !ok {
		return "", false
	}
	value := t.Segment.Value(source)
	m := blockIDPattern.FindSubmatchIndex(value)
	if m == nil {
		return "", false
	}
	caret := t.Segment.Start + m[0]
	if caret > 0 && !util.IsSpace(source[caret-1]) {
		return "", false
	}
	t.Segment = t.Segment.WithStop(t.Segment.Start + len(bytes.TrimRight(value[:m[0]], " \t")))
	return string(value[m[0]:m[3]]), true
}
