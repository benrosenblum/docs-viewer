package linediff

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxWordText is the longest text, in bytes, that Words compares.
const MaxWordText = 10000

// Similarity is the lowest share of equal text for which Words marks words.
const Similarity = 0.5

// Range is a half-open byte range in a text.
type Range struct{ Start, End int }

// Tokens splits s into runs of letters, digits, and underscores, runs of
// white space, and single other characters.
func Tokens(s string) []string {
	var tokens []string
	for s != "" {
		r, size := utf8.DecodeRuneInString(s)
		n := size
		switch {
		case isWord(r):
			n = runEnd(s, isWord)
		case unicode.IsSpace(r):
			n = runEnd(s, unicode.IsSpace)
		}
		tokens = append(tokens, s[:n])
		s = s[n:]
	}
	return tokens
}

func isWord(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r)
}

// runEnd returns the byte length of the leading run of runes that match.
func runEnd(s string, match func(rune) bool) int {
	for i, r := range s {
		if !match(r) {
			return i
		}
	}
	return len(s)
}

func blank(s string) bool { return strings.TrimFunc(s, unicode.IsSpace) == "" }

// weight is the length of a token in runes, or 0 for white space.
func weight(token string) int {
	if blank(token) {
		return 0
	}
	return utf8.RuneCountInString(token)
}

// Words compares two texts word by word. It returns the ranges of old that
// are not in new and the ranges of new that are not in old. Marks that only
// white space separates join into one. ok is false, with no ranges, when a
// text is longer than MaxWordText, when the token diff is approximate, or
// when less than Similarity of the text is equal.
func Words(old, new string) (oldRanges, newRanges []Range, ok bool) {
	if len(old) > MaxWordText || len(new) > MaxWordText {
		return nil, nil, false
	}
	a, b := Tokens(old), Tokens(new)
	edits, approximate := Diff(a, b)
	if approximate {
		return nil, nil, false
	}
	total := 0
	for _, side := range [][]string{a, b} {
		n := 0
		for _, t := range side {
			n += weight(t)
		}
		total = max(total, n)
	}
	equal := 0
	for _, e := range edits {
		if e.Op == Equal {
			for _, t := range a[e.OldStart:e.OldEnd] {
				equal += weight(t)
			}
		}
	}
	if total == 0 || float64(equal) < Similarity*float64(total) {
		return nil, nil, false
	}
	oldOffsets, newOffsets := offsets(a), offsets(b)
	for _, e := range edits {
		switch e.Op {
		case Delete:
			oldRanges = append(oldRanges, Range{oldOffsets[e.OldStart], oldOffsets[e.OldEnd]})
		case Insert:
			newRanges = append(newRanges, Range{newOffsets[e.NewStart], newOffsets[e.NewEnd]})
		}
	}
	return join(old, oldRanges), join(new, newRanges), true
}

// offsets returns the byte offset of each token and the end of the text.
func offsets(tokens []string) []int {
	out := make([]int, len(tokens)+1)
	for i, t := range tokens {
		out[i+1] = out[i] + len(t)
	}
	return out
}

// join trims white space from the edges of each range, drops ranges of only
// white space, and joins ranges that only white space separates.
func join(text string, ranges []Range) []Range {
	var out []Range
	for _, r := range ranges {
		part := text[r.Start:r.End]
		trimmed := strings.TrimLeftFunc(part, unicode.IsSpace)
		r.Start += len(part) - len(trimmed)
		r.End = r.Start + len(strings.TrimRightFunc(trimmed, unicode.IsSpace))
		if r.Start == r.End {
			continue
		}
		if n := len(out); n > 0 && blank(text[out[n-1].End:r.Start]) {
			out[n-1].End = r.End
			continue
		}
		out = append(out, r)
	}
	return out
}
