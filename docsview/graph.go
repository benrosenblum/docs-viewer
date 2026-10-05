package docsview

import (
	"net/http"
	"path"
	"strings"

	"github.com/benrosenblum/docs-viewer/docsview/markdown"
)

type graphNode struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Kind   string   `json:"kind"` // note, outside, or unresolved.
	URL    string   `json:"url,omitempty"`
	Tags   []string `json:"tags"`
	Degree int      `json:"degree"`
}

type graphLink struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type graph struct {
	Nodes []*graphNode `json:"nodes"`
	Links []graphLink  `json:"links"`
}

// graph links every vault note to the Markdown files and unresolved
// wikilinks it references. Attachments are not nodes. File nodes use
// labels, so notes that share a name show their folder.
func (idx *index) graph() graph {
	g := graph{Nodes: []*graphNode{}, Links: []graphLink{}}
	nodes := map[string]*graphNode{}
	for _, n := range idx.notes {
		node := &graphNode{ID: n.path, Name: n.name, Kind: "note", URL: markdown.URL(n.path, ""), Tags: nonNil(n.doc.Tags)}
		nodes[n.path] = node
		g.Nodes = append(g.Nodes, node)
	}
	seen := map[graphLink]bool{}
	for _, n := range idx.notes {
		for _, link := range n.doc.Links {
			target := link.Path
			switch {
			case target == "" && link.Wikilink && link.Target != "":
				target = "unresolved:" + strings.ToLower(link.Target)
				if nodes[target] == nil {
					nodes[target] = &graphNode{ID: target, Name: link.Target, Kind: "unresolved", Tags: []string{}}
					g.Nodes = append(g.Nodes, nodes[target])
				}
			case target == "" || !isNote(target):
				continue
			case nodes[target] == nil:
				nodes[target] = &graphNode{ID: target, Name: noteName(path.Base(target)), Kind: "outside", URL: markdown.URL(target, ""), Tags: []string{}}
				g.Nodes = append(g.Nodes, nodes[target])
			}
			edge := graphLink{n.path, target}
			if target == n.path || seen[edge] {
				continue
			}
			seen[edge] = true
			g.Links = append(g.Links, edge)
			nodes[n.path].Degree++
			nodes[target].Degree++
		}
	}
	var files []string
	for _, node := range g.Nodes {
		if node.Kind != "unresolved" {
			files = append(files, node.ID)
		}
	}
	for id, label := range labels(files) {
		nodes[id].Name = label
	}
	return g
}

func (h *Handler) graphJSON(w http.ResponseWriter, r *http.Request, idx *index) {
	writeJSON(w, r, idx.graph())
}
