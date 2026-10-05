package markdown

import (
	"bytes"
	"strings"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// maxEmbedDepth limits nested note transclusion.
const maxEmbedDepth = 3

// stateKey holds the *renderState of one parse in its parser.Context.
var stateKey = parser.NewContextKey()

// engine is shared by all renders. Its parsers and renderers are stateless;
// per-render state travels in parser.Context and on AST nodes.
var engine = sync.OnceValue(func() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.NewFootnote(extension.WithFootnoteIDPrefixFunction(footnotePrefix)),
			obsidian{},
		),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)
})

// renderState is the state of one document render, including nested embeds.
type renderState struct {
	from     string
	resolver Resolver
	// depth is 0 for the rendered document and grows by one per embed.
	depth int
	// chain lists embedKey values of the documents being rendered, outermost first.
	chain []string
	// headingsOnly stops the transformer after heading IDs.
	headingsOnly bool
	// embeds counts transclusions below the outermost render.
	embeds *int
	// diff marks the units of a merged diff source; nil for a normal render.
	diff *diffState

	headings []heading
	links    []Link
	tags     []string
	math     bool
	mermaid  bool
}

// heading is a Heading with its AST node for section extraction.
type heading struct {
	Heading
	node *ast.Heading
}

// Render converts Markdown to HTML with the Obsidian dialect. from is the
// repository path of src; r answers questions about other files.
func Render(src []byte, from string, r Resolver) (*Document, error) {
	fm, body, hasFM := splitFrontmatter(src)
	return render(fm, body, hasFM, from, r, nil)
}

// render renders a split note. diff, when set, gives the merged line
// origins of a rendered diff.
func render(fm, body []byte, hasFM bool, from string, r Resolver, diff *diffState) (*Document, error) {
	if r == nil {
		r = noResolver{}
	}
	st := &renderState{from: from, resolver: r, chain: []string{embedKey(from, "")}, embeds: new(int), diff: diff}
	doc := st.parse(body)
	out, err := renderHTML(body, doc)
	if err != nil {
		return nil, err
	}
	d := &Document{HTML: out, Aliases: []string{}}
	var propertyLinks []Link
	if hasFM {
		propertyLinks = st.readProperties(fm, d)
	}
	d.Headings = st.headingList()
	d.Links = append(propertyLinks, st.links...)
	if d.Links == nil {
		d.Links = []Link{}
	}
	d.Tags = uniqueFold(append(d.Tags, st.tags...))
	d.Math, d.Mermaid = st.math, st.mermaid
	if d.Title == "" {
		for _, h := range d.Headings {
			if h.Level == 1 {
				d.Title = h.Text
				break
			}
		}
	}
	return d, nil
}

// Headings lists the body headings of src with the IDs Render assigns.
func Headings(src []byte) []Heading {
	st := &renderState{resolver: noResolver{}, headingsOnly: true}
	_, body, _ := splitFrontmatter(src)
	st.parse(body)
	return st.headingList()
}

// parse builds and transforms the AST of body with st as its state.
func (st *renderState) parse(body []byte) *ast.Document {
	pc := parser.NewContext()
	pc.Set(stateKey, st)
	return engine().Parser().Parse(text.NewReader(body), parser.WithContext(pc)).(*ast.Document)
}

func renderHTML(source []byte, doc ast.Node) (string, error) {
	var buf bytes.Buffer
	if err := engine().Renderer().Render(&buf, source, doc); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (st *renderState) headingList() []Heading {
	list := make([]Heading, len(st.headings))
	for i, h := range st.headings {
		list[i] = h.Heading
	}
	return list
}

// child returns the state for embedding path; key identifies the embed.
func (st *renderState) child(path, key string) *renderState {
	return &renderState{
		from:     path,
		resolver: st.resolver,
		depth:    st.depth + 1,
		chain:    append(append([]string(nil), st.chain...), key),
		embeds:   st.embeds,
	}
}

// footnotePrefixKey is the Document meta key for footnote ID prefixes, which
// keep footnote IDs of transcluded notes distinct from the host's.
const footnotePrefixKey = "docsview-footnote-prefix"

func footnotePrefix(n ast.Node) []byte {
	if doc := n.OwnerDocument(); doc != nil {
		if prefix, ok := doc.Meta()[footnotePrefixKey].(string); ok {
			return []byte(prefix)
		}
	}
	return nil
}

// obsidian adds the Obsidian syntax to goldmark.
type obsidian struct{}

func (obsidian) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(
		parser.WithBlockParsers(
			util.Prioritized(mathBlockParser, 650),
			util.Prioritized(commentBlockParser, 650),
		),
		parser.WithInlineParsers(
			util.Prioritized(wikilinkParser{}, 150),
			util.Prioritized(mathParser{}, 150),
			util.Prioritized(commentParser{}, 150),
			util.Prioritized(tagParser{}, 150),
			util.Prioritized(markParser{}, 500),
		),
		parser.WithASTTransformers(util.Prioritized(transformer{}, 2000)),
	)
	m.Renderer().AddOptions(renderer.WithNodeRenderers(util.Prioritized(nodeRenderer{}, 100)))
}

// transformer applies the Obsidian rewrites after inline parsing.
type transformer struct{}

func (transformer) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	st, ok := pc.Get(stateKey).(*renderState)
	if !ok {
		return
	}
	source := reader.Source()
	var pairs []unitPair
	if st.diff != nil {
		pairs = st.diff.markUnits(doc, source)
	}
	convertCallouts(doc, source)
	assignBlockIDs(doc, source)
	unwrapLinkTags(doc)
	st.assignHeadingIDs(doc, source)
	if st.headingsOnly {
		return
	}
	markWords(pairs, source)
	st.resolve(doc, source)
}

// resolve rewrites links and embeds and collects links, tags, and flags in
// document order.
func (st *renderState) resolve(doc ast.Node, source []byte) {
	var nodes []ast.Node
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			switch n.(type) {
			case *wikilink, *ast.Link, *ast.Image, *ast.AutoLink, *tag, *mathInline, *mathBlock, *ast.FencedCodeBlock:
				nodes = append(nodes, n)
			}
		}
		return ast.WalkContinue, nil
	})
	for _, n := range nodes {
		switch n := n.(type) {
		case *wikilink:
			st.resolveWikilink(n, source)
		case *ast.Link:
			st.resolveLink(n, source)
		case *ast.Image:
			st.resolveImage(n, source)
		case *ast.AutoLink:
			setExternal(n)
		case *tag:
			st.tags = append(st.tags, n.name)
		case *mathInline, *mathBlock:
			st.math = true
		case *ast.FencedCodeBlock:
			if isMermaid(n, source) {
				st.mermaid = true
			}
		}
	}
	unwrapEmbedParagraphs(nodes, source)
}

// nodeRenderer renders the custom nodes and overrides code blocks and links.
// It reads only node data, so one instance serves concurrent renders.
type nodeRenderer struct{}

func (nodeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindWikilink, renderWikilink)
	reg.Register(kindTag, renderTag)
	reg.Register(kindMark, renderMark)
	reg.Register(kindComment, renderNothing)
	reg.Register(kindCommentBlock, renderNothing)
	reg.Register(kindMathInline, renderMathInline)
	reg.Register(kindMathBlock, renderMathBlock)
	reg.Register(kindCallout, renderCallout)
	reg.Register(kindCalloutTitle, renderCalloutTitle)
	reg.Register(kindCalloutContent, renderCalloutContent)
	reg.Register(kindEmbedBlock, renderTransparent)
	reg.Register(kindDiffBlock, renderDiffBlock)
	reg.Register(kindDiffWord, renderDiffWord)
	reg.Register(ast.KindFencedCodeBlock, renderCode)
	reg.Register(ast.KindCodeBlock, renderCode)
	reg.Register(ast.KindLink, renderLink)
	reg.Register(ast.KindAutoLink, renderAutoLink)
}

func renderNothing(util.BufWriter, []byte, ast.Node, bool) (ast.WalkStatus, error) {
	return ast.WalkSkipChildren, nil
}

func renderTransparent(util.BufWriter, []byte, ast.Node, bool) (ast.WalkStatus, error) {
	return ast.WalkContinue, nil
}

// noResolver resolves nothing.
type noResolver struct{}

func (noResolver) ResolveWikilink(string, string) (string, bool) { return "", false }
func (noResolver) Exists(string) bool                            { return false }
func (noResolver) HeadingID(string, string) (string, bool)       { return "", false }
func (noResolver) Source(string) ([]byte, error)                 { return nil, errNoSource }

// uniqueFold removes empty strings and case-insensitive duplicates, keeping
// the first spelling. It never returns nil.
func uniqueFold(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		key := strings.ToLower(v)
		if v == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, v)
	}
	return out
}
