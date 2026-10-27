package markdown

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/util"

	"github.com/benrosenblum/docs-viewer/docsview/linediff"
)

// A changed Mermaid diagram of a supported type shows as one diagram from a
// merged source: the unchanged lines once, then the old and the new lines of
// each change. Marks name the added, removed, and changed parts; viewer.js
// finds them in the SVG.

// mermaidMark is one marked part of a merged diagram: a node (a flowchart
// node or an ER entity), the n-th edge from From to To, or the n-th
// attribute row of the entity ID.
type mermaidMark struct {
	Kind string `json:"kind"` // node, edge, or row
	ID   string `json:"id,omitempty"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	N    int    `json:"n"`
	Diff string `json:"diff"` // added, removed, or changed
}

// mermaidDiff is the data of a merged diagram.
type mermaidDiff struct {
	Type   string        `json:"type"` // er or flowchart
	Marks  []mermaidMark `json:"marks"`
	source string
}

// mergeMermaid merges two versions of a diagram. ok is false when the type
// is not supported, the types differ, or nothing could be marked.
func mergeMermaid(old, cur string) (d mermaidDiff, ok bool) {
	a, b := linediff.Lines(old), linediff.Lines(cur)
	kind := mermaidType(a)
	if kind == "" || kind != mermaidType(b) {
		return d, false
	}
	edits, approximate := linediff.Diff(a, b)
	if approximate {
		return d, false
	}
	skeleton := flowchartSkeleton
	if kind == "er" {
		skeleton = erSkeleton
	}
	var lines []mermaidLine
	add := func(text []string, o origin) {
		for _, line := range text {
			lines = append(lines, mermaidLine{text: strings.TrimSuffix(line, "\n"), origin: o})
		}
	}
	for i, e := range edits {
		switch e.Op {
		case linediff.Equal:
			add(b[e.NewStart:e.NewEnd], originBoth)
		case linediff.Delete:
			add(a[e.OldStart:e.OldEnd], originOld)
		case linediff.Insert:
			first := len(lines)
			add(b[e.NewStart:e.NewEnd], originNew)
			// Pair each added line with the removed line at the same place.
			if i > 0 && edits[i-1].Op == linediff.Delete {
				removed := lines[first-(edits[i-1].OldEnd-edits[i-1].OldStart) : first]
				for j := range min(len(removed), len(lines)-first) {
					o, n := &removed[j], &lines[first+j]
					if k := skeleton(o.text); k != "" && k == skeleton(n.text) {
						o.relabel, n.relabel = true, true
					}
				}
			}
		}
	}
	var source strings.Builder
	for _, l := range lines {
		if !l.relabel || l.origin != originOld {
			source.WriteString(l.text + "\n")
		}
	}
	d = mermaidDiff{Type: kind, source: source.String()}
	if kind == "er" {
		d.Marks = erMarks(lines)
	} else {
		d.Marks = flowchartMarks(lines)
	}
	return d, len(d.Marks) > 0
}

// mermaidLine is a line of a merged diagram.
type mermaidLine struct {
	text   string
	origin origin
	// relabel marks a removed line and its added line that name the same
	// nodes and edges and differ only in labels or shapes. The removed line
	// is not in the merged source, so each edge shows one time; its labels
	// still count for the changed mark.
	relabel bool
}

// mermaidType returns er or flowchart from the first statement line.
func mermaidType(lines []string) string {
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) == 0 || strings.HasPrefix(f[0], "%%") {
			continue
		}
		switch f[0] {
		case "erDiagram":
			return "er"
		case "flowchart", "graph":
			return "flowchart"
		}
		return ""
	}
	return ""
}

// lineDiff is the mark of a part from the origins of the lines that name
// it: added when all are new, removed when all are old.
func lineDiff(origins []origin) string {
	o := origins[0]
	for _, x := range origins[1:] {
		if x != o {
			return ""
		}
	}
	return map[origin]string{originOld: "removed", originNew: "added"}[o]
}

// edgeCounter numbers the edges of each pair in source order. viewer.js
// takes the n-th SVG edge of the pair.
type edgeCounter map[[2]string]int

func (c edgeCounter) next(from, to string) int {
	n := c[[2]string{from, to}]
	c[[2]string{from, to}] = n + 1
	return n
}

var (
	erEntityOpen   = regexp.MustCompile(`^\s*("[^"]+"|[\w-]+)(?:\[[^\]]*\])?\s*\{\s*$`)
	erRelationship = regexp.MustCompile(`^\s*("[^"]+"|[\w-]+)\s+([|}o][|o]\s*(?:--|\.\.)\s*[|o][|{o])\s+("[^"]+"|[\w-]+)`)
)

// erSkeleton returns the entities and the cardinality of a relationship
// line without its label, or "".
func erSkeleton(line string) string {
	m := erRelationship.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	return m[1] + " " + strings.Join(strings.Fields(m[2]), "") + " " + m[3]
}

// erMarks marks entities, attribute rows, and relationships. A relationship
// whose label changed is one changed edge.
func erMarks(lines []mermaidLine) []mermaidMark {
	var marks []mermaidMark
	mentions := map[string][]origin{}
	var order []string
	mention := func(name string, o origin) {
		name = strings.Trim(name, `"`)
		if _, ok := mentions[name]; !ok {
			order = append(order, name)
		}
		mentions[name] = append(mentions[name], o)
	}
	rows := map[string]int{}
	edges := edgeCounter{}
	entity := "" // The open entity block.
	for _, l := range lines {
		line, o := l.text, l.origin
		if l.relabel && o == originOld {
			continue
		}
		text := strings.TrimSpace(line)
		switch {
		case text == "" || strings.HasPrefix(text, "%%"):
		case entity != "":
			if text == "}" {
				entity = ""
				continue
			}
			n := rows[entity]
			rows[entity]++
			if o != originBoth {
				marks = append(marks, mermaidMark{Kind: "row", ID: entity, N: n, Diff: lineDiff([]origin{o})})
			}
		case erEntityOpen.MatchString(line):
			entity = strings.Trim(erEntityOpen.FindStringSubmatch(line)[1], `"`)
			mention(entity, o)
		case erRelationship.MatchString(line):
			m := erRelationship.FindStringSubmatch(line)
			from, to := strings.Trim(m[1], `"`), strings.Trim(m[3], `"`)
			diff := lineDiff([]origin{o})
			if l.relabel {
				o, diff = originBoth, "changed"
			}
			mention(from, o)
			mention(to, o)
			n := edges.next(from, to)
			if diff != "" {
				marks = append(marks, mermaidMark{Kind: "edge", From: from, To: to, N: n, Diff: diff})
			}
		}
	}
	return append(nodeMarks(order, mentions, nil), marks...)
}

// nodeMarks marks the nodes whose mentions are all added or all removed,
// and the nodes in changed whose label changed.
func nodeMarks(order []string, mentions map[string][]origin, changed map[string]bool) []mermaidMark {
	var marks []mermaidMark
	for _, id := range order {
		diff := lineDiff(mentions[id])
		if diff == "" && changed[id] {
			diff = "changed"
		}
		if diff != "" {
			marks = append(marks, mermaidMark{Kind: "node", ID: id, Diff: diff})
		}
	}
	return marks
}

var (
	// flowchartSkip starts a statement that names no node by its shape.
	flowchartSkip = regexp.MustCompile(`^\s*(?:%%|subgraph\b|end\b|direction\b|classDef\b|class\b|style\b|linkStyle\b|click\b|accTitle\b|accDescr\b|flowchart\b|graph\b)`)
	flowchartID   = regexp.MustCompile(`^\s*([\w]+(?:-[\w]+)*)`)
	flowchartEdge = regexp.MustCompile(`^\s*(?:<?(?:--|==|-\.)\s+[^|]*?\s+(?:-{2,}[>ox]|-{3,}|={2,}>|={3,}|\.-+>|\.-+)|<?(?:-{2,}|={2,}|-\.+-)[>ox]?|~~~)(?:\s*\|[^|]*\|)?`)
	flowchartAnd  = regexp.MustCompile(`^\s*&`)
	flowchartCls  = regexp.MustCompile(`^:::[\w-]+`)
)

// flowchartNode is a node in a flowchart line, with its shape text such as
// "[label]", or "".
type flowchartNode struct{ id, shape string }

// parseFlowchart returns the nodes and the edges of a flowchart line, and
// its skeleton: the line without shapes, classes, and spaces.
func parseFlowchart(line string) (nodes []flowchartNode, edges [][2]string, skeleton string) {
	if flowchartSkip.MatchString(line) {
		return nil, nil, ""
	}
	var k strings.Builder
	for statement := range strings.SplitSeq(line, ";") {
		var prev []string
		rest := statement
		for {
			var group []string
			for {
				m := flowchartID.FindStringSubmatch(rest)
				if m == nil {
					break
				}
				id := m[1]
				rest = rest[len(m[0]):]
				shape := shapeEnd(rest)
				nodes = append(nodes, flowchartNode{id, rest[:shape]})
				rest = rest[shape:]
				rest = rest[len(flowchartCls.FindString(rest)):]
				group = append(group, id)
				k.WriteString(id)
				and := flowchartAnd.FindString(rest)
				if and == "" {
					break
				}
				rest = rest[len(and):]
				k.WriteString("&")
			}
			for _, from := range prev {
				for _, to := range group {
					edges = append(edges, [2]string{from, to})
				}
			}
			edge := flowchartEdge.FindString(rest)
			if edge == "" || len(group) == 0 {
				break
			}
			rest = rest[len(edge):]
			k.WriteString(strings.Join(strings.Fields(edge), " "))
			prev = group
		}
		k.WriteString(";")
	}
	if len(nodes) == 0 {
		return nil, nil, ""
	}
	return nodes, edges, k.String()
}

func flowchartSkeleton(line string) string {
	_, _, k := parseFlowchart(line)
	return k
}

// flowchartMarks marks nodes and edges. A node whose shape or label
// differs between the old and the new lines is changed.
func flowchartMarks(lines []mermaidLine) []mermaidMark {
	var marks []mermaidMark
	mentions := map[string][]origin{}
	var order []string
	oldShape, newShape := map[string]string{}, map[string]string{}
	edges := edgeCounter{}
	for _, l := range lines {
		nodes, lineEdges, _ := parseFlowchart(l.text)
		o := l.origin
		for _, n := range nodes {
			if n.shape != "" && o != originNew {
				oldShape[n.id] = n.shape
			}
			if n.shape != "" && o != originOld {
				newShape[n.id] = n.shape
			}
		}
		if l.relabel {
			if o == originOld {
				continue // Not in the merged source.
			}
			o = originBoth
		}
		for _, n := range nodes {
			if _, ok := mentions[n.id]; !ok {
				order = append(order, n.id)
			}
			mentions[n.id] = append(mentions[n.id], o)
		}
		for _, e := range lineEdges {
			n := edges.next(e[0], e[1])
			if o != originBoth {
				marks = append(marks, mermaidMark{Kind: "edge", From: e[0], To: e[1], N: n, Diff: lineDiff([]origin{o})})
			}
		}
	}
	// A node without a shape shows its id, so "" is a shape too.
	changed := map[string]bool{}
	for _, id := range order {
		changed[id] = oldShape[id] != newShape[id]
	}
	return append(nodeMarks(order, mentions, changed), marks...)
}

// shapeEnd returns the length of the node shape at the start of s, such as
// "[label]" or "((label))", or 0.
func shapeEnd(s string) int {
	if s == "" || !strings.ContainsRune("[({>", rune(s[0])) {
		return 0
	}
	depth, quoted := 0, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			quoted = !quoted
		case quoted:
		case c == '[' || c == '(' || c == '{' || (c == '>' && i == 0):
			depth++
		case c == ']' || c == ')' || c == '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return 0
}

var kindMermaidDiff = ast.NewNodeKind("MermaidDiff")

// mermaidDiffBlock holds the old and the new diagram blocks of a change. It
// renders the merged diagram and keeps the two blocks in a template, which
// viewer.js shows when the merged diagram does not render.
type mermaidDiffBlock struct {
	ast.BaseBlock
	diff  mermaidDiff
	start bool // The first mark of a change.
}

func (n *mermaidDiffBlock) Kind() ast.NodeKind { return kindMermaidDiff }

func (n *mermaidDiffBlock) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

// joinMermaid replaces two adjacent marked diagram blocks with one merged
// diagram, when mergeMermaid supports them.
func joinMermaid(old, cur *ast.FencedCodeBlock, source []byte) {
	oldBlock, ok1 := old.Parent().(*diffBlock)
	newBlock, ok2 := cur.Parent().(*diffBlock)
	if !ok1 || !ok2 || oldBlock.NextSibling() != newBlock {
		return
	}
	d, ok := mergeMermaid(string(old.Lines().Value(source)), string(cur.Lines().Value(source)))
	if !ok {
		return
	}
	parent := oldBlock.Parent()
	block := &mermaidDiffBlock{diff: d, start: oldBlock.start}
	parent.InsertBefore(parent, oldBlock, block)
	block.AppendChild(block, oldBlock)
	block.AppendChild(block, newBlock)
}

func renderMermaidDiff(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, _ = w.WriteString("</template>\n</div>\n")
		return ast.WalkContinue, nil
	}
	n := node.(*mermaidDiffBlock)
	_, _ = w.WriteString(`<div class="diff-block diff-changed mermaid-diff"`)
	if n.start {
		_, _ = w.WriteString(` data-change=""`)
	}
	data, _ := json.Marshal(n.diff)
	_, _ = w.WriteString(`>` + "\n" + `<pre class="mermaid" data-diff="`)
	_, _ = w.Write(util.EscapeHTML(data))
	_, _ = w.WriteString(`">`)
	_, _ = w.Write(util.EscapeHTML([]byte(n.diff.source)))
	_, _ = w.WriteString("</pre>\n<template>\n")
	return ast.WalkContinue, nil
}
