package markdown

import (
	"strings"
	"testing"
)

func TestHighlightLines(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		lines      int
		want       map[int]string // Line index to a class that the whole line keeps.
	}{
		{"x.go", "package x\n\n/* first\nsecond\nthird */\nvar a = 4\n", 6, map[int]string{2: "cm", 3: "cm", 4: "cm"}},
		{"x.yaml", "key: \"first\n  second\"\nother: 1", 3, map[int]string{1: "s2"}},
	} {
		lines, _ := HighlightLines([]byte(tc.code), "", tc.name)
		if len(lines) != tc.lines {
			t.Fatalf("%s: %d lines, want %d", tc.name, len(lines), tc.lines)
		}
		var texts []string
		for _, l := range lines {
			texts = append(texts, l.Text())
		}
		if joined := strings.Join(texts, "\n"); joined != strings.TrimSuffix(tc.code, "\n") {
			t.Fatalf("%s: lines give %q", tc.name, joined)
		}
		for i, class := range tc.want {
			for _, s := range lines[i] {
				if strings.TrimSpace(s.Text) != "" && s.Class != class {
					t.Errorf("%s line %d: %q has class %q, want %q (%+v)", tc.name, i, s.Text, s.Class, class, lines[i])
				}
			}
		}
	}
	// Each line break ends a line, as in linediff.Lines.
	for code, want := range map[string]int{"": 0, "\n": 1, "a": 1, "a\n": 1, "a\n\n": 2, "a\n\nb": 3, "a\n\n\n": 3} {
		for _, name := range []string{"x.txt", "x.go", "x.md"} {
			if lines, _ := HighlightLines([]byte(code), "", name); len(lines) != want {
				t.Errorf("HighlightLines(%q, %s): %d lines, want %d", code, name, len(lines), want)
			}
		}
	}
	// Marks split tokens and keep their classes.
	lines, _ := HighlightLines([]byte("var maxOpenFiles = 4\n"), "", "x.go")
	got := lines[0].HTML("ins", [][2]int{{7, 11}, {19, 20}})
	want := `<span class="kd">var</span><span class="w"> </span><span class="nx">max<ins class="diff-word">Open</ins>Files</span><span class="w"> </span><span class="p">=</span><span class="w"> </span><span class="mi"><ins class="diff-word">4</ins></span>`
	if got != want {
		t.Errorf("marked line:\n%s\nwant\n%s", got, want)
	}
}
