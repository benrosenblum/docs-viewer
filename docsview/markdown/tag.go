package markdown

import (
	"html"
	"net/url"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var kindTag = ast.NewNodeKind("Tag")

// tag is an inline #tag.
type tag struct {
	ast.BaseInline
	name string
}

func (n *tag) Kind() ast.NodeKind { return kindTag }

func (n *tag) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"Name": n.name}, nil)
}

// tagParser parses "#tag" after whitespace or at the start of the text. A
// tag has letters, numbers, "_", "/", and "-", and is not only digits.
type tagParser struct{}

func (tagParser) Trigger() []byte { return []byte{'#'} }

func (tagParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	if before := block.PrecendingCharacter(); !unicode.IsSpace(before) {
		return nil
	}
	line, _ := block.PeekLine()
	end, digitsOnly := 1, true
	for end < len(line) {
		r, size := utf8.DecodeRune(line[end:])
		if !isTagRune(r) {
			break
		}
		digitsOnly = digitsOnly && unicode.IsDigit(r)
		end += size
	}
	if end == 1 || digitsOnly {
		return nil
	}
	block.Advance(end)
	return &tag{name: string(line[1:end])}
}

func isTagRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '/' || r == '-'
}

// tagURL is the search page for a tag.
func tagURL(name string) string {
	return "/_/search?q=" + url.QueryEscape("tag:#"+name)
}

func renderTag(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		n := node.(*tag)
		_, _ = w.WriteString(`<a class="tag" href="` + html.EscapeString(tagURL(n.name)) + `">#` + html.EscapeString(n.name) + "</a>")
	}
	return ast.WalkSkipChildren, nil
}

// unwrapLinkTags turns tags inside link text back into text.
func unwrapLinkTags(doc ast.Node) {
	var tags []*tag
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if t, ok := n.(*tag); ok && entering && insideLink(t) {
			tags = append(tags, t)
		}
		return ast.WalkContinue, nil
	})
	for _, t := range tags {
		t.Parent().ReplaceChild(t.Parent(), t, rawString("#"+t.name))
	}
}

func insideLink(n ast.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.(type) {
		case *ast.Link, *ast.Image:
			return true
		}
	}
	return false
}
