package docsview

import (
	"bytes"
	"fmt"
	"html/template"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/benrosenblum/docs-viewer/docsview/linediff"
	"github.com/benrosenblum/docs-viewer/docsview/markdown"
)

// diffContext is the number of unchanged lines around each change.
const diffContext = 3

// maxHighlight is the largest version, in bytes, that the source diff
// highlights. Chroma needs seconds for a few megabytes of Markdown.
const maxHighlight = 256 << 10

// Messages of a diff page without lines, or with approximate lines.
const (
	msgTooLarge    = "The file is too large to show a diff."
	msgBinary      = "The file is binary. The viewer does not show a diff of a binary file."
	msgNewEmpty    = "The file is new and empty."
	msgApproximate = "The two versions are very different. The diff shows all changed lines as removed, then added."
)

// isBinary reports whether data is not UTF-8 text or has a NUL byte.
func isBinary(data []byte) bool { return !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 }

// noChanges is the message for two equal versions.
func noChanges(label string) string { return "The file has no changes against " + label + "." }

// sourceDiff renders the unified diff of two versions of a file as a table,
// and counts its changes: the runs of removed and added lines. The first
// row of each change has the attribute data-change. oldExists is false when
// the file did not exist at the base, and tooLarge is true when a version
// is above the display limit. label names the base in messages. Without
// lines, the table is empty and the message says why.
func sourceDiff(old, cur []byte, oldExists, tooLarge bool, filename, label string) (table template.HTML, changes int, message string) {
	switch {
	case tooLarge:
		return "", 0, msgTooLarge
	case isBinary(old) || isBinary(cur):
		return "", 0, msgBinary
	case oldExists && bytes.Equal(old, cur):
		return "", 0, noChanges(label)
	case !oldExists && len(cur) == 0:
		return "", 0, msgNewEmpty
	}
	a, b := linediff.Lines(string(old)), linediff.Lines(string(cur))
	edits, approximate := linediff.Diff(a, b)
	if len(old) > maxHighlight || len(cur) > maxHighlight {
		filename = "" // Plain text.
	}
	oldLines, lang := markdown.HighlightLines(old, "", filename)
	newLines, _ := markdown.HighlightLines(cur, "", filename)
	if len(oldLines) != len(a) || len(newLines) != len(b) {
		// Cannot happen: both split after each "\n".
		return "", 0, fmt.Sprintf("The diff failed: %d and %d highlighted lines for %d and %d lines.", len(oldLines), len(newLines), len(a), len(b))
	}
	oldMarks, newMarks := wordMarks(edits, oldLines, newLines)
	t := &diffTable{old: oldLines, new: newLines, oldMarks: oldMarks, newMarks: newMarks}
	t.b.WriteString(`<table class="diff-table chroma" data-lang="` + template.HTMLEscapeString(lang) + `">`)
	hunks := linediff.Hunks(edits, diffContext)
	oldPos, newPos := 0, 0
	for _, h := range hunks {
		t.hidden(oldPos, h.OldStart, newPos)
		t.b.WriteString(`<tbody class="diff-hunk">`)
		for i, e := range h.Edits {
			// A change starts at its removed lines, else at its added lines.
			t.start = e.Op == linediff.Delete || e.Op == linediff.Insert && (i == 0 || h.Edits[i-1].Op != linediff.Delete)
			t.rows(e)
		}
		t.b.WriteString("</tbody>")
		oldPos, newPos = h.OldEnd, h.NewEnd
	}
	t.hidden(oldPos, len(a), newPos)
	t.b.WriteString("</table>")
	if approximate {
		message = msgApproximate
	}
	return template.HTML(t.b.String()), t.changes, message
}

// wordMarks returns the word marks of each paired line, by line index.
func wordMarks(edits []linediff.Edit, oldLines, newLines []markdown.SourceLine) (map[int][][2]int, map[int][][2]int) {
	oldMarks, newMarks := map[int][][2]int{}, map[int][][2]int{}
	for _, p := range linediff.Pairs(edits) {
		oldRanges, newRanges, ok := linediff.Words(oldLines[p.Old].Text(), newLines[p.New].Text())
		if !ok {
			continue
		}
		oldMarks[p.Old], newMarks[p.New] = ranges(oldRanges), ranges(newRanges)
	}
	return oldMarks, newMarks
}

func ranges(rs []linediff.Range) [][2]int {
	out := make([][2]int, len(rs))
	for i, r := range rs {
		out[i] = [2]int{r.Start, r.End}
	}
	return out
}

// diffTable writes the rows of a source diff.
type diffTable struct {
	b                  strings.Builder
	old, new           []markdown.SourceLine
	oldMarks, newMarks map[int][][2]int
	start              bool // The next row is the first of a change.
	changes            int
}

// hidden writes a run of unchanged lines, from old line oldLo to oldHi, as
// a count row and a hidden body that the client shows in place.
func (t *diffTable) hidden(oldLo, oldHi, newLo int) {
	n := oldHi - oldLo
	if n <= 0 {
		return
	}
	label := strconv.Itoa(n) + " unchanged lines"
	if n == 1 {
		label = "1 unchanged line"
	}
	t.b.WriteString(`<tbody class="diff-fold"><tr><td colspan="4"><button type="button" class="diff-expand" data-action="diff-expand">` + label + `</button></td></tr></tbody><tbody class="diff-hidden" hidden>`)
	t.rows(linediff.Edit{Op: linediff.Equal, OldStart: oldLo, OldEnd: oldHi, NewStart: newLo, NewEnd: newLo + n})
	t.b.WriteString("</tbody>")
}

// rows writes one row for each line of an edit.
func (t *diffTable) rows(e linediff.Edit) {
	switch e.Op {
	case linediff.Equal:
		for i := range e.OldEnd - e.OldStart {
			t.row("is-context", e.OldStart+i+1, e.NewStart+i+1, " ", t.new[e.NewStart+i].HTML("", nil))
		}
	case linediff.Delete:
		for i := e.OldStart; i < e.OldEnd; i++ {
			t.row("is-removed", i+1, 0, "-", t.old[i].HTML("del", t.oldMarks[i]))
		}
	case linediff.Insert:
		for i := e.NewStart; i < e.NewEnd; i++ {
			t.row("is-added", 0, i+1, "+", t.new[i].HTML("ins", t.newMarks[i]))
		}
	}
}

func (t *diffTable) row(class string, oldNum, newNum int, marker, code string) {
	num := func(n int) string {
		if n == 0 {
			return ""
		}
		return strconv.Itoa(n)
	}
	stop := ""
	if t.start {
		stop, t.start = " data-change", false
		t.changes++
	}
	t.b.WriteString(`<tr class="diff-line ` + class + `"` + stop + `><td class="diff-num">` + num(oldNum) + `</td><td class="diff-num">` + num(newNum) + `</td><td class="diff-marker" aria-hidden="true">` + marker + `</td><td class="diff-code">` + code + "</td></tr>")
}
