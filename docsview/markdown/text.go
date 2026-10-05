package markdown

import (
	"strings"
	"unicode"

	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/util"
)

// maxContext is the approximate length of Link.Context in runes.
const maxContext = 240

// plainText returns the text that the inline content of n renders.
func plainText(n ast.Node, source []byte) string {
	w := plainWriter{source: source}
	w.children(n)
	return w.String()
}

// plainWriter collects rendered text and, optionally, where mark starts.
type plainWriter struct {
	strings.Builder
	source []byte
	mark   ast.Node
	markAt int
}

func (w *plainWriter) children(n ast.Node) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		w.node(c)
	}
}

func (w *plainWriter) node(n ast.Node) {
	if n == w.mark {
		w.markAt = w.Len()
	}
	switch n := n.(type) {
	case *ast.Text:
		v := n.Segment.Value(w.source)
		if !n.IsRaw() {
			v = unescapeText(v)
		}
		w.Write(v)
		if n.SoftLineBreak() || n.HardLineBreak() {
			w.WriteByte(' ')
		}
	case *ast.String:
		w.Write(n.Value)
	case *ast.CodeSpan:
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			if t, ok := c.(*ast.Text); ok {
				w.WriteString(strings.ReplaceAll(string(t.Segment.Value(w.source)), "\n", " "))
			}
		}
	case *ast.AutoLink:
		w.Write(n.Label(w.source))
	case *ast.RawHTML, *ast.Image, *comment:
	case *wikilink:
		w.WriteString(n.label())
	case *tag:
		w.WriteString("#" + n.name)
	case *mathInline:
		w.Write(n.tex)
	default:
		w.children(n)
	}
}

// unescapeText resolves backslash escapes and character references.
func unescapeText(v []byte) []byte {
	return util.ResolveEntityNames(util.ResolveNumericReferences(util.UnescapePunctuations(v)))
}

// linkContext returns the collapsed text of the block around n, trimmed to
// about maxContext runes centred on n.
func linkContext(n ast.Node, source []byte) string {
	block := n.Parent()
	for block != nil && !isTextBlock(block) {
		block = block.Parent()
	}
	if block == nil {
		return ""
	}
	w := plainWriter{source: source, mark: n}
	w.children(block)
	return window(w.String(), w.markAt)
}

func isTextBlock(n ast.Node) bool {
	switch n.(type) {
	case *ast.Paragraph, *ast.TextBlock, *ast.Heading, *east.TableCell, *calloutTitle:
		return true
	}
	return false
}

// window collapses whitespace in s and keeps about maxContext runes around
// the byte offset at, cutting at word boundaries.
func window(s string, at int) string {
	runes := make([]rune, 0, len(s))
	pos := -1
	space := false
	for i, r := range s {
		if pos < 0 && i >= at {
			pos = len(runes)
		}
		if unicode.IsSpace(r) {
			space = len(runes) > 0
			continue
		}
		if space {
			runes = append(runes, ' ')
			space = false
		}
		runes = append(runes, r)
	}
	if len(runes) <= maxContext {
		return string(runes)
	}
	if pos < 0 {
		pos = len(runes)
	}
	start := min(max(pos-maxContext/3, 0), len(runes)-maxContext)
	end := start + maxContext
	if start > 0 {
		for start < pos && runes[start-1] != ' ' {
			start++
		}
	}
	if end < len(runes) {
		for end > pos+1 && runes[end] != ' ' {
			end--
		}
	}
	out := strings.TrimSpace(string(runes[start:end]))
	if start > 0 {
		out = "…" + out
	}
	if end < len(runes) {
		out += "…"
	}
	return out
}
