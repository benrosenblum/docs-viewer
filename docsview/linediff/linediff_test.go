package linediff

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// script writes edits as "=a", "-b", "+c" items for comparison.
func script(a, b []string, edits []Edit) string {
	var out []string
	for _, e := range edits {
		switch e.Op {
		case Equal:
			for _, s := range a[e.OldStart:e.OldEnd] {
				out = append(out, "="+s)
			}
		case Delete:
			for _, s := range a[e.OldStart:e.OldEnd] {
				out = append(out, "-"+s)
			}
		case Insert:
			for _, s := range b[e.NewStart:e.NewEnd] {
				out = append(out, "+"+s)
			}
		}
	}
	return strings.Join(out, " ")
}

// check verifies that edits cover both sequences in order.
func check(t *testing.T, a, b []string, edits []Edit) {
	t.Helper()
	i, j := 0, 0
	for k, e := range edits {
		if e.OldStart != i || e.NewStart != j {
			t.Fatalf("edit %d %+v does not start at %d, %d", k, e, i, j)
		}
		switch e.Op {
		case Equal:
			if e.OldEnd-e.OldStart != e.NewEnd-e.NewStart {
				t.Fatalf("unequal equal run %+v", e)
			}
			for n := range e.OldEnd - e.OldStart {
				if a[e.OldStart+n] != b[e.NewStart+n] {
					t.Fatalf("equal run %+v differs", e)
				}
			}
		case Delete:
			if e.NewStart != e.NewEnd {
				t.Fatalf("delete with new items %+v", e)
			}
		case Insert:
			if e.OldStart != e.OldEnd {
				t.Fatalf("insert with old items %+v", e)
			}
		}
		if k > 0 && e.Op == edits[k-1].Op || k > 0 && e.Op == Delete && edits[k-1].Op == Insert {
			t.Fatalf("edits are not normal at %d: %v", k, edits)
		}
		i, j = e.OldEnd, e.NewEnd
	}
	if i != len(a) || j != len(b) {
		t.Fatalf("edits end at %d, %d, want %d, %d", i, j, len(a), len(b))
	}
}

func TestDiff(t *testing.T) {
	for _, tc := range []struct{ a, b, want string }{
		{"", "", ""},
		{"a b c", "a b c", "=a =b =c"},
		{"", "a b", "+a +b"},
		{"a b", "", "-a -b"},
		{"a b c", "a x b c y", "=a +x =b =c +y"},
		{"a b c d", "a c d e", "=a -b =c =d +e"},
		{"a b c", "x y z", "-a -b -c +x +y +z"},
		{"a b c d e", "a x c y e", "=a -b +x =c -d +y =e"},
	} {
		a, b := strings.Fields(tc.a), strings.Fields(tc.b)
		edits, approximate := Diff(a, b)
		check(t, a, b, edits)
		if got := script(a, b, edits); got != tc.want || approximate {
			t.Errorf("Diff(%q, %q) = %q, %v, want %q", tc.a, tc.b, got, approximate, tc.want)
		}
	}
}

func TestMissingFinalNewline(t *testing.T) {
	a, b := Lines("one\ntwo"), Lines("one\ntwo\n")
	if fmt.Sprintf("%q", a) != `["one\n" "two"]` {
		t.Fatalf("Lines: %q", a)
	}
	edits, _ := Diff(a, b)
	if len(edits) != 3 || edits[1].Op != Delete || edits[2].Op != Insert {
		t.Fatalf("final newline: %+v", edits)
	}
}

// lcs returns the length of the longest common subsequence.
func lcs(a, b []string) int {
	row := make([]int, len(b)+1)
	for i := range a {
		prev := 0
		for j := range b {
			cur := row[j+1]
			if a[i] == b[j] {
				row[j+1] = prev + 1
			} else {
				row[j+1] = max(row[j+1], row[j])
			}
			prev = cur
		}
	}
	return row[len(b)]
}

func TestDiffIsMinimal(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	random := func() []string {
		s := make([]string, rng.IntN(30))
		for i := range s {
			s[i] = string(rune('a' + rng.IntN(4)))
		}
		return s
	}
	for range 2000 {
		a, b := random(), random()
		edits, _ := Diff(a, b)
		check(t, a, b, edits)
		equal := 0
		for _, e := range edits {
			if e.Op == Equal {
				equal += e.OldEnd - e.OldStart
			}
		}
		if want := lcs(a, b); equal != want {
			t.Fatalf("Diff(%v, %v) keeps %d equal items, want %d", a, b, equal, want)
		}
	}
}

func TestDiffCap(t *testing.T) {
	var a, b []string
	for i := range MaxEdits {
		a = append(a, fmt.Sprint("a", i))
		b = append(b, fmt.Sprint("b", i))
	}
	edits, approximate := Diff(a, b)
	if !approximate || len(edits) != 2 || edits[0] != (Edit{Delete, 0, MaxEdits, 0, 0}) || edits[1] != (Edit{Insert, MaxEdits, MaxEdits, 0, MaxEdits}) {
		t.Fatalf("above the cap: %v %+v", approximate, edits[:min(len(edits), 3)])
	}
	// The common prefix and suffix do not count toward the cap.
	a = append(append([]string{"p"}, a[:10]...), "s")
	b = append(append([]string{"p"}, b[:10]...), "s")
	if _, approximate := Diff(a, b); approximate {
		t.Fatal("small diff is approximate")
	}
}

func TestHunks(t *testing.T) {
	var a []string
	for i := range 30 {
		a = append(a, fmt.Sprint(i))
	}
	b := append([]string(nil), a...)
	b[2] = "x"  // Hunk 1.
	b[9] = "y"  // Joins hunk 1: six equal lines between.
	b[25] = "z" // Hunk 2.
	edits, _ := Diff(a, b)
	hunks := Hunks(edits, 3)
	if len(hunks) != 2 {
		t.Fatalf("hunks: %+v", hunks)
	}
	if h := hunks[0]; h.OldStart != 0 || h.OldEnd != 13 || h.NewStart != 0 || h.NewEnd != 13 {
		t.Errorf("first hunk: %+v", h)
	}
	if h := hunks[1]; h.OldStart != 22 || h.OldEnd != 29 || script(a, b, h.Edits) != "=22 =23 =24 -25 +z =26 =27 =28" {
		t.Errorf("second hunk: %+v %s", h, script(a, b, h.Edits))
	}
	if Hunks(nil, 3) != nil {
		t.Error("equal inputs have hunks")
	}
}

func TestPairs(t *testing.T) {
	a, b := strings.Fields("k a b c k"), strings.Fields("k x y k z")
	edits, _ := Diff(a, b)
	if got := fmt.Sprint(Pairs(edits)); got != "[{1 1} {2 2}]" {
		t.Fatalf("pairs: %s (%s)", got, script(a, b, edits))
	}
}

// marked shows ranges in text with [brackets].
func marked(text string, ranges []Range) string {
	var b strings.Builder
	last := 0
	for _, r := range ranges {
		b.WriteString(text[last:r.Start] + "[" + text[r.Start:r.End] + "]")
		last = r.End
	}
	return b.String() + text[last:]
}

func TestWords(t *testing.T) {
	for _, tc := range []struct{ old, new, wantOld, wantNew string }{
		{"maxOpenFiles = 4", "maxOpenFiles = 8", "maxOpenFiles = [4]", "maxOpenFiles = [8]"},
		{"The pool holds four connections.", "The pool holds eight connections.", "The pool holds [four] connections.", "The pool holds [eight] connections."},
		{"it runs in the old slow way today", "it runs in a new fast way today", "it runs in [the old slow] way today", "it runs in [a new fast] way today"},
		{"call(a, b)", "call(a; b)", "call(a[,] b)", "call(a[;] b)"},
		{"Größe der Straße", "Größe der Gasse", "Größe der [Straße]", "Größe der [Gasse]"},
		{"a  b c", "a b c", "a  b c", "a b c"},
	} {
		oldRanges, newRanges, ok := Words(tc.old, tc.new)
		if !ok || marked(tc.old, oldRanges) != tc.wantOld || marked(tc.new, newRanges) != tc.wantNew {
			t.Errorf("Words(%q, %q) = %q, %q, %v", tc.old, tc.new, marked(tc.old, oldRanges), marked(tc.new, newRanges), ok)
		}
	}
	for _, tc := range [][2]string{
		{"The parser reads every line.", "Reload sends pages to browsers."},
		{strings.Repeat("word ", MaxWordText/5+1), strings.Repeat("word ", MaxWordText/5)},
		{"", "new"},
	} {
		if o, n, ok := Words(tc[0], tc[1]); ok || o != nil || n != nil {
			t.Errorf("Words(%.20q, %.20q) marks words", tc[0], tc[1])
		}
	}
	if got := fmt.Sprintf("%q", Tokens("a_1 ÿ-x  y")); got != `["a_1" " " "ÿ" "-" "x" "  " "y"]` {
		t.Error("tokens:", got)
	}
}
