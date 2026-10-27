package markdown

import (
	"fmt"
	"strings"
	"testing"
)

// marks returns the marks of a merged diagram, one per line, such as
// "node A added", "edge A B 0 removed", or "row T 1 added".
func marks(t *testing.T, old, cur string) ([]string, bool) {
	t.Helper()
	d, ok := mergeMermaid(old, cur)
	var got []string
	for _, m := range d.Marks {
		switch m.Kind {
		case "node":
			got = append(got, fmt.Sprintf("node %s %s", m.ID, m.Diff))
		case "edge":
			got = append(got, fmt.Sprintf("edge %s %s %d %s", m.From, m.To, m.N, m.Diff))
		case "row":
			got = append(got, fmt.Sprintf("row %s %d %s", m.ID, m.N, m.Diff))
		}
	}
	return got, ok
}

const erBase = "erDiagram\n  CUSTOMER ||--o{ ORDER : places\n  CUSTOMER {\n    string name\n    int id PK\n  }\n  ORDER {\n    int id\n  }\n"

func TestMergeMermaid(t *testing.T) {
	for _, tc := range []struct {
		name, old, new string
		want           []string
	}{
		{"attribute added", erBase, strings.Replace(erBase, "    int id PK\n", "    int id PK\n    date since\n", 1),
			[]string{"row CUSTOMER 2 added"}},
		{"attribute removed", erBase, strings.Replace(erBase, "    string name\n", "", 1),
			[]string{"row CUSTOMER 0 removed"}},
		{"attribute type changed", erBase, strings.Replace(erBase, "    int id\n", "    bigint id\n", 1),
			[]string{"row ORDER 0 removed", "row ORDER 1 added"}},
		{"entity added", erBase, erBase + "  LINE {\n    int qty\n  }\n  ORDER ||--|{ LINE : contains\n",
			[]string{"node LINE added", "row LINE 0 added", "edge ORDER LINE 0 added"}},
		{"entity block removed", erBase, strings.Replace(erBase, "  ORDER {\n    int id\n  }\n", "", 1),
			[]string{"row ORDER 0 removed"}},
		{"entity removed", erBase, "erDiagram\n  CUSTOMER {\n    string name\n    int id PK\n  }\n",
			[]string{"node ORDER removed", "edge CUSTOMER ORDER 0 removed", "row ORDER 0 removed"}},
		{"entity between two", "erDiagram\n  A {\n    int x\n  }\n  B {\n    int y\n  }\n", "erDiagram\n  A {\n    int x\n  }\n  C {\n    int z\n  }\n  B {\n    int y\n  }\n",
			[]string{"node C added", "row C 0 added"}},
		{"relationship added", erBase, erBase + "  ORDER }o--|| CUSTOMER : bills\n",
			[]string{"edge ORDER CUSTOMER 0 added"}},
		{"second edge of a pair", erBase, strings.Replace(erBase, "places\n", "places\n  CUSTOMER ||--o{ ORDER : returns\n", 1),
			[]string{"edge CUSTOMER ORDER 1 added"}},
		{"flowchart node and edge", "flowchart TD\n  A --> B\n", "flowchart TD\n  A --> B\n  B --> C[New]\n",
			[]string{"node C added", "edge B C 0 added"}},
		{"flowchart removed", "flowchart TD\n  A --> B\n  B -.-> C\n", "flowchart TD\n  A --> B\n",
			[]string{"node C removed", "edge B C 0 removed"}},
		{"flowchart chain and group", "graph LR\n  A --> B\n", "graph LR\n  A --> B\n  A & B -->|go| C --- D\n",
			[]string{"node C added", "node D added", "edge A C 0 added", "edge B C 0 added", "edge C D 0 added"}},
		{"flowchart edge text", "flowchart TD\n  A --> B\n", "flowchart TD\n  A --> B\n  my-node -- some text --> B\n",
			[]string{"node my-node added", "edge my-node B 0 added"}},
		{"flowchart label changed", "flowchart TD\n  A[Start] --> B\n", "flowchart TD\n  A[Begin] --> B\n",
			[]string{"node A changed", "edge A B 0 removed", "edge A B 1 added"}},
		{"flowchart shapes", "flowchart TD\n  A --> B\n", "flowchart TD\n  A --> B\n  B ==> C((round)):::hot\n  C --> D[(db)]\n  D --> E{{\"x [y]\"}}\n",
			[]string{"node C added", "node D added", "node E added", "edge B C 0 added", "edge C D 0 added", "edge D E 0 added"}},
	} {
		got, ok := marks(t, tc.old, tc.new)
		if !ok || strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
			t.Errorf("%s: ok %v\n got %q\nwant %q", tc.name, ok, got, tc.want)
		}
	}
}

func TestMergeMermaidSource(t *testing.T) {
	d, ok := mergeMermaid(erBase, strings.Replace(erBase, "    int id\n", "    bigint id\n", 1))
	want := strings.Replace(erBase, "    int id\n", "    int id\n    bigint id\n", 1)
	if !ok || d.source != want || d.Type != "er" {
		t.Errorf("ok %v type %q source:\n%s", ok, d.Type, d.source)
	}
}

func TestMergeMermaidUnsupported(t *testing.T) {
	for _, tc := range []struct{ name, old, new string }{
		{"sequence", "sequenceDiagram\n  A->>B: hi\n", "sequenceDiagram\n  A->>B: hello\n"},
		{"type change", "graph TD\n  A --> B\n", "erDiagram\n  A ||--|| B : x\n"},
		{"no marks", "graph TD\n  A --> B\n", "graph TD\n  A --> B\n  %% note\n"},
	} {
		if _, ok := mergeMermaid(tc.old, tc.new); ok {
			t.Errorf("%s: merged", tc.name)
		}
	}
}

func TestRenderDiffMermaid(t *testing.T) {
	note := "# T\n\n```mermaid\n" + erBase + "```\n\nAfter.\n"
	d := renderDiff(t, note, strings.Replace(note, "    int id\n", "    int id\n    date at\n", 1))
	for _, w := range []string{
		`<div class="diff-block diff-changed mermaid-diff" data-change="">`,
		`<pre class="mermaid" data-diff="{&quot;type&quot;:&quot;er&quot;,&quot;marks&quot;:[{&quot;kind&quot;:&quot;row&quot;,&quot;id&quot;:&quot;ORDER&quot;,&quot;n&quot;:1,&quot;diff&quot;:&quot;added&quot;}]}">erDiagram`,
		"    int id\n    date at\n  }\n</pre>\n<template>\n<div class=\"diff-block diff-removed\" data-change=\"\">\n<pre class=\"mermaid\">",
		"</pre>\n</div>\n</template>\n</div>\n<p>After.</p>",
	} {
		if !strings.Contains(d.HTML, w) {
			t.Errorf("HTML lacks %s\n%s", w, d.HTML)
		}
	}
	if d.Changes != 1 || strings.Count(d.HTML, `<pre class="mermaid"`) != 3 {
		t.Errorf("%d changes\n%s", d.Changes, d.HTML)
	}
	// A whole new or removed diagram keeps its block mark.
	d = renderDiff(t, "# T\n", "# T\n\n```mermaid\n"+erBase+"```\n")
	if strings.Contains(d.HTML, "mermaid-diff") || !strings.Contains(d.HTML, `<div class="diff-block diff-added" data-change="">`) {
		t.Errorf("added diagram:\n%s", d.HTML)
	}
}
