package docsview

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// numbered returns lines "line 1" to "line n", each with a newline.
func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func TestSourceDiff(t *testing.T) {
	// Two changes with 41 unchanged lines between them.
	old := "package x\n\nconst maxOpenFiles = 4\n\n" + numbered(40) + "var greeting = \"hello world\"\nvar other = \"the parser reads every line\"\n"
	cur := "package x\n\nconst maxOpenFiles = 8\n\n" + numbered(40) + "var greeting = \"hello there world\"\nvar other = \"reload sends pages to browsers\"\n"
	table, changes, message := sourceDiff([]byte(old), []byte(cur), true, false, "x.go", "HEAD")
	got := string(table)
	if message != "" {
		t.Fatal("message:", message)
	}
	// Each change has one stop: its first row.
	if n := strings.Count(got, "data-change"); changes != 2 || n != 2 {
		t.Errorf("%d changes and %d stops, want 2", changes, n)
	}
	for _, want := range []string{
		`<table class="diff-table chroma" data-lang="go">`,
		// One word in a source line: only 4 and 8, in their token spans.
		`<tr class="diff-line is-removed" data-change><td class="diff-num">3</td><td class="diff-num"></td><td class="diff-marker" aria-hidden="true">-</td>`,
		// The added line of the same change is not a stop.
		`<tr class="diff-line is-added"><td class="diff-num"></td><td class="diff-num">3</td>`,
		`<span class="mi"><del class="diff-word">4</del></span>`,
		`<span class="mi"><ins class="diff-word">8</ins></span>`,
		// A change inside a string token keeps the string class.
		`hello <ins class="diff-word">there</ins> world`,
		// 41 unchanged lines: three after the first change, three before
		// the second, and 35 folded.
		`data-action="diff-expand">35 unchanged lines</button>`,
		`<tbody class="diff-hidden" hidden><tr class="diff-line is-context"><td class="diff-num">7</td><td class="diff-num">7</td>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("source diff lacks %s", want)
		}
	}
	if n := strings.Count(got, `class="diff-word"`); n != 3 {
		t.Errorf("%d word marks, want 3 (a pair below the threshold has none)", n)
	}
	if strings.Contains(got, `the parser`) && strings.Contains(got, `<del class="diff-word">the`) {
		t.Error("a pair below the threshold has word marks")
	}
	if !regexp.MustCompile(`<span class="s">&#34;hello <ins class="diff-word">there</ins> world&#34;</span>`).MatchString(got) {
		t.Error("string token is split:", regexp.MustCompile(`<tr[^>]*is-added.*?</tr>`).FindAllString(got, -1)[1])
	}

	for _, tc := range []struct {
		name          string
		old, cur      string
		exists, large bool
		message       string
	}{
		{"binary", "\x89PNG\x00", "\x89PNG\x00\x01", true, false, msgBinary},
		{"too large", "", "", true, true, msgTooLarge},
		{"no changes", "same\n", "same\n", true, false, "The file has no changes against HEAD."},
		{"new and empty", "", "", false, false, msgNewEmpty},
	} {
		if table, changes, message := sourceDiff([]byte(tc.old), []byte(tc.cur), tc.exists, tc.large, "x.txt", "HEAD"); table != "" || changes != 0 || message != tc.message {
			t.Errorf("%s: %q, %d, %q", tc.name, table, changes, message)
		}
	}

	// A trailing blank line is a line.
	table, _, message = sourceDiff([]byte("a\n"), []byte("a\n\n"), true, false, "x.go", "HEAD")
	if got := string(table); message != "" || strings.Count(got, "is-added") != 1 || strings.Count(got, "is-context") != 1 {
		t.Errorf("trailing blank line: %q %s", message, got)
	}

	// An untracked file shows every line as added.
	table, changes, _ = sourceDiff(nil, []byte("one\ntwo\n"), false, false, "x.txt", "HEAD")
	if got := string(table); changes != 1 || strings.Count(got, "is-added") != 2 || strings.Contains(got, "is-removed") || strings.Contains(got, "is-context") {
		t.Errorf("untracked file: %s", got)
	}
}
