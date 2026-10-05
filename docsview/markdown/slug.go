package markdown

import (
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/yuin/goldmark/ast"
)

// Slug returns the GitHub anchor for heading text, without de-duplication.
func Slug(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsNumber(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// uniqueSlug returns base, or base with the next free "-N" suffix, and
// records the result in used, like GitHub's slugger.
func uniqueSlug(used map[string]int, base string) string {
	id := base
	for {
		if _, taken := used[id]; !taken {
			break
		}
		used[base]++
		id = base + "-" + strconv.Itoa(used[base])
	}
	used[id] = 0
	return id
}

// URL returns the viewer URL of a repository path and optional fragment.
func URL(path, fragment string) string {
	var b strings.Builder
	if path == "." {
		path = ""
	}
	for _, segment := range strings.Split(path, "/") {
		b.WriteByte('/')
		b.WriteString(url.PathEscape(segment))
	}
	if fragment != "" {
		b.WriteByte('#')
		b.WriteString(escapeFragment(fragment))
	}
	return b.String()
}

// escapeFragment percent-encodes only the bytes a fragment cannot hold
// literally, so anchors such as "^id" and "fn:1" stay readable.
func escapeFragment(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= ' ' || c == 0x7f || strings.IndexByte("\"#%<>`", c) >= 0 {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// assignHeadingIDs gives every heading a de-duplicated GitHub ID computed
// from its rendered text. Embedded documents record IDs without emitting
// them, so the host page keeps unique anchors.
func (st *renderState) assignHeadingIDs(doc ast.Node, source []byte) {
	used := map[string]int{}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		h, ok := n.(*ast.Heading)
		if !ok || !entering {
			return ast.WalkContinue, nil
		}
		txt := strings.TrimSpace(plainText(h, source))
		id := uniqueSlug(used, Slug(txt))
		if st.depth == 0 && id != "" {
			h.SetAttributeString("id", []byte(id))
		}
		st.headings = append(st.headings, heading{Heading{Level: h.Level, Text: txt, ID: id}, h})
		return ast.WalkSkipChildren, nil
	})
}

// findHeading finds a heading of this document by name, ignoring case and
// repeated whitespace, then also ignoring the characters Obsidian strips.
func (st *renderState) findHeading(name string) (heading, bool) {
	for _, norm := range []func(string) string{foldSpace, looseHeading} {
		want := norm(name)
		for _, h := range st.headings {
			if norm(h.Text) == want {
				return h, true
			}
		}
	}
	return heading{}, false
}

// headingID returns the anchor of a heading of this document, falling back
// to the slug of name.
func (st *renderState) headingID(name string) string {
	if h, ok := st.findHeading(name); ok {
		return h.ID
	}
	return Slug(name)
}

func foldSpace(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

func looseHeading(s string) string {
	return foldSpace(strings.Map(func(r rune) rune {
		if strings.ContainsRune(`#|^:%[]\`, r) {
			return -1
		}
		return r
	}, s))
}
