package markdown

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/util"

	"github.com/benrosenblum/docs-viewer/docsview/linediff"
)

// Diff is the rendered diff of two versions of a note.
type Diff struct {
	// Document is the merged body with diff marks, and the properties,
	// title, and aliases of the new version.
	*Document
	// PropertiesChanged is true when the front matter differs.
	PropertiesChanged bool
	// Approximate is true when the line diff gave up (linediff.MaxEdits).
	Approximate bool
	// Changes is the number of changes in the body. The first marked
	// element of each change has the attribute data-change.
	Changes int
}

// RenderDiff renders one merged note that shows the old and the new
// version: unchanged blocks once, and each changed block as its old version
// marked removed, then its new version marked added. Paired blocks also get
// word marks.
func RenderDiff(old, cur []byte, from string, r Resolver) (d *Diff, err error) {
	defer func() {
		if p := recover(); p != nil {
			d, err = nil, fmt.Errorf("rendered diff failed: %v", p)
		}
	}()
	oldFM, oldBody, oldHasFM := splitFrontmatter(old)
	newFM, newBody, newHasFM := splitFrontmatter(cur)
	merged, lines, approximate := mergeBodies(oldBody, newBody)
	state := &diffState{lines: lines}
	doc, err := render(newFM, merged, newHasFM, from, r, state)
	if err != nil {
		return nil, err
	}
	return &Diff{Document: doc, PropertiesChanged: oldHasFM != newHasFM || !bytes.Equal(oldFM, newFM), Approximate: approximate, Changes: state.changes}, nil
}

// Diff unit kinds. Only paragraphs, headings, items, and rows get word marks.
const (
	unitParagraph = "paragraph"
	unitHeading   = "heading"
	unitItem      = "item" // A tight list item: the ListItem of a TextBlock.
	unitCode      = "code"
	unitHTML      = "html"
	unitMath      = "math"
	unitComment   = "comment"
	unitRule      = "rule"
	unitRow       = "row"   // A table body row.
	unitTable     = "table" // A whole table, when its header or delimiter row changes.
)

// unit is a diff unit: the smallest block that renders its own element.
type unit struct {
	kind string
	node ast.Node
	// start and end are the line range [start, end) of the unit.
	start, end int
	// touchStart and touchEnd are the lines whose change selects the unit.
	// Only a table unit has a range that differs: its header and delimiter.
	touchStart, touchEnd int
	// tableStart is the first line of the table that contains a row.
	tableStart int
	// prefix is the text before the unit on its first line, such as "> ".
	prefix string
}

func (u unit) touches(lo, hi int) bool {
	if lo == hi {
		return u.touchStart < lo && lo < u.touchEnd
	}
	return u.touchStart < hi && lo < u.touchEnd
}

// lineIndex holds the start offset of each line of a source.
type lineIndex []int

func newLineIndex(source []byte) lineIndex {
	index := lineIndex{0}
	for i, c := range source {
		if c == '\n' && i+1 < len(source) {
			index = append(index, i+1)
		}
	}
	return index
}

// line returns the line that contains offset.
func (l lineIndex) line(offset int) int {
	return sort.Search(len(l), func(i int) bool { return l[i] > offset }) - 1
}

// startOffset returns the source offset where block n starts.
func startOffset(n ast.Node) int {
	if n.Pos() >= 0 {
		return n.Pos()
	}
	if n.Lines().Len() > 0 {
		return n.Lines().At(0).Start
	}
	return 0
}

// parseUnits returns the diff units of a note body.
func parseUnits(body []byte) []unit {
	st := &renderState{resolver: noResolver{}, headingsOnly: true}
	return diffUnits(st.parse(body), body)
}

// diffUnits lists the diff units of a parsed body in document order.
func diffUnits(doc ast.Node, source []byte) []unit {
	lines := newLineIndex(source)
	var units []unit
	lastLine := func(n ast.Node) int {
		if l := n.Lines(); l.Len() > 0 {
			s := l.At(l.Len() - 1)
			return lines.line(max(s.Start, s.Stop-1))
		}
		return lines.line(startOffset(n))
	}
	add := func(kind string, n ast.Node, start, end int) *unit {
		offset := startOffset(n)
		lineStart := lines[lines.line(offset)]
		units = append(units, unit{kind: kind, node: n, start: start, end: end, touchStart: start, touchEnd: end, prefix: string(source[lineStart:offset])})
		return &units[len(units)-1]
	}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || n.Type() != ast.TypeBlock {
			return ast.WalkContinue, nil
		}
		start := lines.line(startOffset(n))
		switch n := n.(type) {
		case *ast.Paragraph:
			add(unitParagraph, n, start, lastLine(n)+1)
		case *ast.TextBlock:
			if item, ok := n.Parent().(*ast.ListItem); ok && item.FirstChild() == n {
				add(unitItem, item, lines.line(startOffset(item)), lastLine(n)+1)
			}
		case *ast.Heading:
			end := start + 1
			if source[startOffset(n)] != '#' {
				end = lastLine(n) + 2 // A setext heading ends with its underline.
			}
			add(unitHeading, n, start, end)
		case *ast.FencedCodeBlock:
			last := start
			if n.Lines().Len() > 0 {
				last = lastLine(n)
			}
			if last+1 < len(lines) && closesFence(source, lines, last+1, startOffset(n)) {
				last++
			}
			add(unitCode, n, start, last+1)
		case *ast.CodeBlock:
			add(unitCode, n, start, lastLine(n)+1)
		case *ast.HTMLBlock:
			end := lastLine(n) + 1
			if n.HasClosure() {
				end = max(end, lines.line(n.ClosureLine.Start)+1)
			}
			add(unitHTML, n, start, end)
		case *mathBlock:
			add(unitMath, n, start, lastLine(n)+1)
		case *commentBlock:
			add(unitComment, n, start, lastLine(n)+1)
		case *ast.ThematicBreak:
			add(unitRule, n, start, start+1)
		case *east.Table:
			end := start + 2
			for row := n.FirstChild(); row != nil; row = row.NextSibling() {
				end = max(end, lines.line(startOffset(row))+1)
			}
			table := add(unitTable, n, start, end)
			table.touchEnd = start + 2
			for row := n.FirstChild(); row != nil; row = row.NextSibling() {
				if _, ok := row.(*east.TableRow); ok {
					line := lines.line(startOffset(row))
					add(unitRow, row, line, line+1).tableStart = start
				}
			}
		default:
			return ast.WalkContinue, nil
		}
		return ast.WalkSkipChildren, nil
	})
	return units
}

// closesFence reports whether line closes the fenced code block that opens
// at offset: after its container prefix, it repeats the opening fence
// character at least as often.
func closesFence(source []byte, lines lineIndex, line, offset int) bool {
	open := source[offset:]
	fence := open[:len(open)-len(bytes.TrimLeft(open, string(open[:1])))]
	end := len(source)
	if line+1 < len(lines) {
		end = lines[line+1]
	}
	text := bytes.TrimLeft(source[lines[line]:end], " \t>")
	return bytes.HasPrefix(text, fence) && util.IsBlank(bytes.TrimLeft(text, string(fence[:1])))
}

// Origins of merged lines.
type origin uint8

const (
	originBoth origin = iota
	originOld
	originNew
	originSeparator
	originMixed // Only from diffState.origin.
)

// mergedLine is the origin of one line of the merged source, and the change
// (hunk) that it belongs to.
type mergedLine struct {
	origin origin
	hunk   int
}

// span is a change between the old and the new lines: old [oa, ob) and new
// [na, nb). The lines between two spans are equal.
type span struct{ oa, ob, na, nb int }

// mergeBodies writes one body from an old and a new body: unchanged lines
// once, then for each change the old lines of the whole units it touches,
// then the new lines. It returns the origin of each merged line.
func mergeBodies(oldBody, newBody []byte) ([]byte, []mergedLine, bool) {
	a, b := linediff.Lines(string(oldBody)), linediff.Lines(string(newBody))
	edits, approximate := linediff.Diff(a, b)
	var spans []span
	for _, e := range edits {
		if e.Op == linediff.Equal {
			continue
		}
		if n := len(spans); n > 0 && spans[n-1].ob == e.OldStart && spans[n-1].nb == e.NewStart {
			spans[n-1].ob, spans[n-1].nb = e.OldEnd, e.NewEnd
			continue
		}
		spans = append(spans, span{e.OldStart, e.OldEnd, e.NewStart, e.NewEnd})
	}
	oldUnits, newUnits := parseUnits(oldBody), parseUnits(newBody)
	spans = expand(spans, oldUnits, newUnits)

	var out bytes.Buffer
	var lines []mergedLine
	write := func(line string, o origin, hunk int) {
		out.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			out.WriteByte('\n')
		}
		lines = append(lines, mergedLine{o, hunk})
	}
	ai, bi := 0, 0
	for h, s := range spans {
		for ; ai < s.oa; ai, bi = ai+1, bi+1 {
			write(b[bi], originBoth, -1)
		}
		for _, line := range a[s.oa:s.ob] {
			write(line, originOld, h)
		}
		if s.oa < s.ob && s.na < s.nb {
			last, lastOK := unitAt(oldUnits, s.ob-1)
			first, firstOK := unitAt(newUnits, s.na)
			if !lastOK || !firstOK || !continues(last, first, s) {
				write(separator(last, lastOK), originSeparator, h)
			}
		}
		for _, line := range b[s.na:s.nb] {
			write(line, originNew, h)
		}
		ai, bi = s.ob, s.nb
	}
	for ; bi < len(b); bi++ {
		write(b[bi], originBoth, -1)
	}
	return out.Bytes(), lines, approximate
}

// separator returns a blank line inside the block quotes that contain the
// last old unit: the leading ">" markers of its prefix.
func separator(last unit, ok bool) string {
	if !ok {
		return ""
	}
	lead := last.prefix[:len(last.prefix)-len(strings.TrimLeft(last.prefix, " \t>"))]
	return lead[:strings.LastIndexByte(lead, '>')+1]
}

// continues reports whether the first new unit of a change continues the
// container of the last old unit without a separator line: two list items,
// or two rows of a table whose header is before the change on both sides.
func continues(last, first unit, s span) bool {
	switch {
	case last.kind == unitItem && first.kind == unitItem:
		return true
	case last.kind == unitRow && first.kind == unitRow:
		return last.tableStart < s.oa && first.tableStart < s.na
	}
	return false
}

// unitAt returns the innermost unit that contains line.
func unitAt(units []unit, line int) (unit, bool) {
	found, ok := unit{}, false
	for _, u := range units {
		if u.start <= line && line < u.end && (!ok || u.end-u.start <= found.end-found.start) {
			found, ok = u, true
		}
	}
	return found, ok
}

// expand grows each span to whole units on both sides, moves the other
// side by the same number of equal lines, and joins spans that meet.
func expand(spans []span, oldUnits, newUnits []unit) []span {
	for changed := true; changed; {
		changed = false
		for i := range spans {
			s := &spans[i]
			lo, hi := 0, 1<<62 // The old lines between the neighbours.
			if i > 0 {
				lo = spans[i-1].ob
			}
			if i+1 < len(spans) {
				hi = spans[i+1].oa
			}
			grow := func(units []unit, a, b *int) {
				for _, u := range units {
					if !u.touches(*a, *b) {
						continue
					}
					if d := min(*a-u.start, s.oa-lo); d > 0 {
						s.oa, s.na, changed = s.oa-d, s.na-d, true
					}
					if d := min(u.end-*b, hi-s.ob); d > 0 {
						s.ob, s.nb, changed = s.ob+d, s.nb+d, true
					}
				}
			}
			grow(oldUnits, &s.oa, &s.ob)
			grow(newUnits, &s.na, &s.nb)
		}
		joined := spans[:0]
		for _, s := range spans {
			if n := len(joined); n > 0 && joined[n-1].ob >= s.oa {
				joined[n-1].ob, joined[n-1].nb = s.ob, s.nb
				changed = true
				continue
			}
			joined = append(joined, s)
		}
		spans = joined
	}
	return spans
}

// diffState carries the merged line origins into one render, and the
// number of changes out of it.
type diffState struct {
	lines   []mergedLine
	changes int
}

// unitPair is a removed unit and an added unit of one change, of the same kind.
type unitPair struct{ old, new unit }

// markUnits marks each unit of the merged document from the origin of its
// lines, and returns the pairs of units for word marks.
func (d *diffState) markUnits(doc ast.Node, source []byte) []unitPair {
	removed, added := map[int][]unit{}, map[int][]unit{}
	covered := map[ast.Node]bool{} // Tables marked as one unit.
	started := map[int]bool{}      // Changes that have their first mark.
	// start counts a change at its first mark. A mixed unit has no change
	// of its own lines, so it is a change of its own.
	start := func(hunk int) bool {
		if hunk >= 0 && started[hunk] {
			return false
		}
		started[hunk] = true
		d.changes++
		return true
	}
	mark := func(n ast.Node, class string, hunk int) {
		n.SetAttributeString("class", []byte(class))
		if start(hunk) {
			n.SetAttributeString("data-change", []byte(""))
		}
	}
	for _, u := range diffUnits(doc, source) {
		if u.kind == unitRow && covered[u.node.Parent()] {
			continue
		}
		o, hunk := d.origin(u.start, u.end)
		class := map[origin]string{originOld: "diff-removed", originNew: "diff-added", originMixed: "diff-changed"}[o]
		if u.kind == unitTable {
			if o == originOld || o == originNew {
				mark(u.node, class, hunk)
				covered[u.node] = true
			}
			continue
		}
		if class == "" {
			continue
		}
		switch u.kind {
		case unitCode, unitHTML, unitMath:
			wrapBlock(u.node, class, start(hunk))
		case unitComment:
		default:
			mark(u.node, class, hunk)
		}
		switch u.kind {
		case unitParagraph, unitHeading, unitItem, unitRow:
			if o == originOld {
				removed[hunk] = append(removed[hunk], u)
			} else if o == originNew {
				added[hunk] = append(added[hunk], u)
			}
		}
	}
	var pairs []unitPair
	for _, hunk := range slices.Sorted(maps.Keys(removed)) {
		for i, old := range removed[hunk] {
			if i < len(added[hunk]) && added[hunk][i].kind == old.kind {
				pairs = append(pairs, unitPair{old, added[hunk][i]})
			}
		}
	}
	return pairs
}

// origin returns the origin of the lines [start, end) and their change.
// Separator lines do not count. Mixed origins give originMixed, which marks
// the unit as changed; mergeBodies writes whole units, which prevents them.
func (d *diffState) origin(start, end int) (origin, int) {
	result, hunk, seen := originBoth, -1, false
	for i := start; i < end && i < len(d.lines); i++ {
		l := d.lines[i]
		if l.origin == originSeparator {
			continue
		}
		if !seen {
			result, hunk, seen = l.origin, l.hunk, true
		} else if l.origin != result {
			return originMixed, hunk
		}
	}
	return result, hunk
}

var (
	kindDiffBlock = ast.NewNodeKind("DiffBlock")
	kindDiffWord  = ast.NewNodeKind("DiffWord")
)

// diffBlock wraps a marked block whose renderer writes no attributes, such
// as a code block, a math block, or an HTML block.
type diffBlock struct {
	ast.BaseBlock
	class string
	start bool // The first mark of a change.
}

func (n *diffBlock) Kind() ast.NodeKind { return kindDiffBlock }

func (n *diffBlock) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"Class": n.class}, nil)
}

// wrapBlock puts n in a diffBlock with class.
func wrapBlock(n ast.Node, class string, start bool) {
	parent := n.Parent()
	block := &diffBlock{class: class, start: start}
	parent.ReplaceChild(parent, n, block)
	block.AppendChild(block, n)
}

func renderDiffBlock(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		block := node.(*diffBlock)
		_, _ = w.WriteString(`<div class="diff-block ` + block.class + `"`)
		if block.start {
			_, _ = w.WriteString(` data-change=""`)
		}
		_, _ = w.WriteString(">\n")
	} else {
		_, _ = w.WriteString("</div>\n")
	}
	return ast.WalkContinue, nil
}

// diffWord is a deleted or inserted run of words.
type diffWord struct {
	ast.BaseInline
	inserted bool
}

func (n *diffWord) Kind() ast.NodeKind { return kindDiffWord }

func (n *diffWord) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

func renderDiffWord(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	tag := "del"
	if node.(*diffWord).inserted {
		tag = "ins"
	}
	if entering {
		_, _ = w.WriteString("<" + tag + ` class="diff-word">`)
	} else {
		_, _ = w.WriteString("</" + tag + ">")
	}
	return ast.WalkContinue, nil
}

// markWords adds word marks to each pair. A table row compares as one
// text, with a boundary token for each column between its cells, so that
// cells pair by column and the similarity is that of the whole row.
func markWords(pairs []unitPair, source []byte) {
	for _, p := range pairs {
		switch p.old.kind {
		case unitRow:
			markInlines(childNodes(p.old.node), childNodes(p.new.node), source)
		case unitItem:
			markInlines([]ast.Node{p.old.node.FirstChild()}, []ast.Node{p.new.node.FirstChild()}, source)
		default:
			markInlines([]ast.Node{p.old.node}, []ast.Node{p.new.node}, source)
		}
	}
}

// piece is a text node or an atomic inline node in the joined text of a
// block. An atomic node is one private-use character, the same for equal
// nodes, so that a change to it compares as one token.
type piece struct {
	node       ast.Node
	start, end int // The byte range in the joined text.
	text       bool
}

// markInlines compares the inline content of two lists of blocks, such as
// the cells of two rows, and wraps the changed parts in diffWord nodes.
func markInlines(oldBlocks, newBlocks []ast.Node, source []byte) {
	atoms := map[string]rune{}
	oldPieces, oldText := inlinePieces(oldBlocks, source, atoms)
	newPieces, newText := inlinePieces(newBlocks, source, atoms)
	oldRanges, newRanges, ok := linediff.Words(oldText, newText)
	if !ok {
		return
	}
	wrapRanges(oldPieces, oldRanges, false)
	wrapRanges(newPieces, newRanges, true)
	for _, blocks := range [][]ast.Node{oldBlocks, newBlocks} {
		for _, block := range blocks {
			joinWords(block)
		}
	}
}

// joinWords merges adjacent word marks of the same kind. A change that
// crosses a text node edge then shows as one mark.
func joinWords(parent ast.Node) {
	for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
		w, ok := c.(*diffWord)
		if !ok {
			joinWords(c)
			continue
		}
		for next, ok := w.NextSibling().(*diffWord); ok && next.inserted == w.inserted; next, ok = w.NextSibling().(*diffWord) {
			for n := next.FirstChild(); n != nil; n = next.FirstChild() {
				w.AppendChild(w, n)
			}
			parent.RemoveChild(parent, next)
		}
	}
}

// inlinePieces walks the inline nodes of the blocks in order. A boundary
// token separates the blocks.
func inlinePieces(blocks []ast.Node, source []byte, atoms map[string]rune) ([]piece, string) {
	var pieces []piece
	var b strings.Builder
	atom := func(key string) rune {
		r, ok := atoms[key]
		if !ok {
			r = rune(0xE000 + len(atoms)%0x1900)
			atoms[key] = r
		}
		return r
	}
	var walk func(ast.Node)
	walk = func(parent ast.Node) {
		for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
			switch c := c.(type) {
			case *ast.Text:
				start := b.Len()
				b.Write(c.Segment.Value(source))
				pieces = append(pieces, piece{c, start, b.Len(), true})
				if c.SoftLineBreak() || c.HardLineBreak() {
					b.WriteByte(' ')
				}
			case *comment:
			case *ast.CodeSpan, *mathInline, *wikilink, *tag, *ast.Image, *ast.RawHTML, *ast.AutoLink, *ast.String:
				start := b.Len()
				b.WriteRune(atom(atomKey(c, source)))
				pieces = append(pieces, piece{c, start, b.Len(), false})
			default:
				walk(c)
			}
		}
	}
	for i, block := range blocks {
		if i > 0 {
			b.WriteRune(atom(fmt.Sprint("\x00boundary\x00", i)))
		}
		walk(block)
	}
	return pieces, b.String()
}

// atomKey identifies an atomic inline node by its kind and content.
func atomKey(n ast.Node, source []byte) string {
	key := n.Kind().String() + "\x00" + plainText(n, source)
	switch n := n.(type) {
	case *wikilink:
		key += fmt.Sprintf("\x00%v\x00%s\x00%q\x00%s", n.embed, n.ref.target, n.ref.headings, n.ref.alias)
	case *tag:
		key += "\x00" + n.name
	case *mathInline:
		key += "\x00" + string(n.tex)
	case *ast.Image:
		key += "\x00" + string(n.Destination) + "\x00" + string(n.Title)
	case *ast.RawHTML:
		for i := 0; i < n.Segments.Len(); i++ {
			segment := n.Segments.At(i)
			key += "\x00" + string(segment.Value(source))
		}
	case *ast.AutoLink:
		key += "\x00" + string(n.URL(source))
	case *ast.String:
		key += "\x00" + string(n.Value)
	}
	return key
}

// wrapRanges wraps the parts of the pieces that the ranges cover. A range
// that crosses a node edge gives one wrap in each node.
func wrapRanges(pieces []piece, ranges []linediff.Range, inserted bool) {
	for _, p := range pieces {
		var parts [][2]int // Covered byte ranges, relative to the piece.
		for _, r := range ranges {
			if s, e := max(r.Start, p.start), min(r.End, p.end); s < e {
				parts = append(parts, [2]int{s - p.start, e - p.start})
			}
		}
		if len(parts) == 0 {
			continue
		}
		if !p.text {
			wrapInline(p.node, inserted)
			continue
		}
		splitText(p.node.(*ast.Text), parts, inserted)
	}
}

func wrapInline(n ast.Node, inserted bool) {
	parent := n.Parent()
	w := &diffWord{inserted: inserted}
	parent.ReplaceChild(parent, n, w)
	w.AppendChild(w, n)
}

// splitText replaces t with text nodes split at the part edges and wraps
// the parts.
func splitText(t *ast.Text, parts [][2]int, inserted bool) {
	parent := t.Parent()
	seg := t.Segment
	length := seg.Stop - seg.Start
	var nodes []ast.Node
	add := func(s, e int, marked bool) {
		if s >= e {
			return
		}
		from := seg.WithStart(seg.Start + s)
		part := ast.NewTextSegment(from.WithStop(seg.Start + e))
		part.SetRaw(t.IsRaw())
		var n ast.Node = part
		if marked {
			w := &diffWord{inserted: inserted}
			w.AppendChild(w, part)
			n = w
		}
		nodes = append(nodes, n)
	}
	at := 0
	for _, p := range parts {
		add(at, p[0], false)
		add(p[0], p[1], true)
		at = p[1]
	}
	add(at, length, false)
	for _, n := range nodes {
		parent.InsertBefore(parent, t, n)
	}
	// The line break belongs to the last part.
	last := nodes[len(nodes)-1]
	if w, ok := last.(*diffWord); ok {
		last = w.LastChild()
	}
	last.(*ast.Text).SetSoftLineBreak(t.SoftLineBreak())
	last.(*ast.Text).SetHardLineBreak(t.HardLineBreak())
	parent.RemoveChild(parent, t)
}
