// Package linediff compares two sequences of strings with the Myers O(ND)
// algorithm in linear space. The same core compares lines and words.
package linediff

import "strings"

// Op is the operation of an edit.
type Op uint8

const (
	Equal Op = iota
	Delete
	Insert
)

// Edit is one run of one operation. Old and new are half-open ranges of
// positions. A Delete has an empty new range at its position, and an Insert
// has an empty old range at its position.
type Edit struct {
	Op               Op
	OldStart, OldEnd int
	NewStart, NewEnd int
}

// MaxEdits caps the edit distance. Above it, Diff gives up.
const MaxEdits = 10000

// Diff returns the edits that change a into b. Equal runs alternate with
// changes, and each change is at most one Delete followed by at most one
// Insert. When the edit distance is above MaxEdits, the result replaces
// everything between the common prefix and suffix, and approximate is true.
func Diff(a, b []string) (edits []Edit, approximate bool) {
	ids := map[string]int{}
	intern := func(values []string) []int {
		out := make([]int, len(values))
		for i, v := range values {
			id, ok := ids[v]
			if !ok {
				id = len(ids)
				ids[v] = id
			}
			out[i] = id
		}
		return out
	}
	d := &differ{a: intern(a), b: intern(b)}
	d.compare(0, len(a), 0, len(b), true)
	return normalize(d.edits), d.approximate
}

type differ struct {
	a, b        []int
	edits       []Edit
	approximate bool
}

// emit appends a run and joins it with the last run of the same operation.
func (d *differ) emit(op Op, aLo, aHi, bLo, bHi int) {
	if aLo == aHi && bLo == bHi {
		return
	}
	if n := len(d.edits); n > 0 {
		last := &d.edits[n-1]
		if last.Op == op && last.OldEnd == aLo && last.NewEnd == bLo {
			last.OldEnd, last.NewEnd = aHi, bHi
			return
		}
	}
	d.edits = append(d.edits, Edit{op, aLo, aHi, bLo, bHi})
}

// compare diffs a[aLo:aHi] and b[bLo:bHi]. Only the top call has the cap.
func (d *differ) compare(aLo, aHi, bLo, bHi int, top bool) {
	a0, b0 := aLo, bLo
	for aLo < aHi && bLo < bHi && d.a[aLo] == d.b[bLo] {
		aLo++
		bLo++
	}
	d.emit(Equal, a0, aLo, b0, bLo)
	a1, b1 := aHi, bHi
	for aLo < aHi && bLo < bHi && d.a[aHi-1] == d.b[bHi-1] {
		aHi--
		bHi--
	}
	switch {
	case aLo == aHi:
		d.emit(Insert, aLo, aLo, bLo, bHi)
	case bLo == bHi:
		d.emit(Delete, aLo, aHi, bLo, bLo)
	default:
		limit := 0
		if top {
			limit = MaxEdits/2 + 1
		}
		if x, y, ok, cut := d.bisect(aLo, aHi, bLo, bHi, limit); ok {
			d.compare(aLo, x, bLo, y, false)
			d.compare(x, aHi, y, bHi, false)
		} else {
			d.approximate = d.approximate || cut
			d.emit(Delete, aLo, aHi, bLo, bLo)
			d.emit(Insert, aHi, aHi, bLo, bHi)
		}
	}
	d.emit(Equal, aHi, a1, bHi, b1)
}

// bisect finds the middle snake of a[aLo:aHi] and b[bLo:bHi] and returns the
// split point. The forward and reverse searches each take one step per
// round, so round r has an edit distance of about 2r. A limit above zero
// stops the search after that many rounds; then cut is true. Without a
// split point, the sequences have nothing in common.
func (d *differ) bisect(aLo, aHi, bLo, bHi, limit int) (x, y int, ok, cut bool) {
	a, b := d.a[aLo:aHi], d.b[bLo:bHi]
	n, m := len(a), len(b)
	maxD := (n + m + 1) / 2
	rounds := maxD
	if limit > 0 && limit < rounds {
		rounds = limit
	}
	offset := maxD
	v1 := make([]int, 2*maxD+2)
	v2 := make([]int, 2*maxD+2)
	for i := range v1 {
		v1[i], v2[i] = -1, -1
	}
	v1[offset+1], v2[offset+1] = 0, 0
	delta := n - m
	front := delta%2 != 0
	k1start, k1end, k2start, k2end := 0, 0, 0, 0
	for r := 0; r < rounds; r++ {
		for k1 := -r + k1start; k1 <= r-k1end; k1 += 2 {
			i := offset + k1
			var x1 int
			if k1 == -r || k1 != r && v1[i-1] < v1[i+1] {
				x1 = v1[i+1]
			} else {
				x1 = v1[i-1] + 1
			}
			y1 := x1 - k1
			for x1 < n && y1 < m && a[x1] == b[y1] {
				x1++
				y1++
			}
			v1[i] = x1
			switch {
			case x1 > n:
				k1end += 2
			case y1 > m:
				k1start += 2
			case front:
				if j := offset + delta - k1; j >= 0 && j < len(v2) && v2[j] != -1 && x1 >= n-v2[j] {
					return aLo + x1, bLo + y1, true, false
				}
			}
		}
		for k2 := -r + k2start; k2 <= r-k2end; k2 += 2 {
			i := offset + k2
			var x2 int
			if k2 == -r || k2 != r && v2[i-1] < v2[i+1] {
				x2 = v2[i+1]
			} else {
				x2 = v2[i-1] + 1
			}
			y2 := x2 - k2
			for x2 < n && y2 < m && a[n-x2-1] == b[m-y2-1] {
				x2++
				y2++
			}
			v2[i] = x2
			switch {
			case x2 > n:
				k2end += 2
			case y2 > m:
				k2start += 2
			case !front:
				if j := offset + delta - k2; j >= 0 && j < len(v1) && v1[j] != -1 {
					x1 := v1[j]
					y1 := offset + x1 - j
					if x1 >= n-x2 {
						return aLo + x1, bLo + y1, true, false
					}
				}
			}
		}
	}
	return 0, 0, false, rounds < maxD
}

// normalize joins each run of changes into one Delete and one Insert.
func normalize(edits []Edit) []Edit {
	var out []Edit
	for i := 0; i < len(edits); {
		if edits[i].Op == Equal {
			out = append(out, edits[i])
			i++
			continue
		}
		start := edits[i]
		aHi, bHi := start.OldStart, start.NewStart
		for ; i < len(edits) && edits[i].Op != Equal; i++ {
			aHi, bHi = max(aHi, edits[i].OldEnd), max(bHi, edits[i].NewEnd)
		}
		if start.OldStart < aHi {
			out = append(out, Edit{Delete, start.OldStart, aHi, start.NewStart, start.NewStart})
		}
		if start.NewStart < bHi {
			out = append(out, Edit{Insert, aHi, aHi, start.NewStart, bHi})
		}
	}
	return out
}

// Lines splits text after each "\n". Each line keeps its "\n", so a missing
// final newline is a difference.
func Lines(text string) []string {
	var lines []string
	for text != "" {
		i := strings.IndexByte(text, '\n')
		if i < 0 {
			lines = append(lines, text)
			break
		}
		lines = append(lines, text[:i+1])
		text = text[i+1:]
	}
	return lines
}

// Hunk is a group of changes with up to context equal items around each
// change. Its edits cover the hunk ranges.
type Hunk struct {
	OldStart, OldEnd int
	NewStart, NewEnd int
	Edits            []Edit
}

// Hunks groups edits into hunks. Two changes share a hunk when at most
// 2*context equal items separate them.
func Hunks(edits []Edit, context int) []Hunk {
	var hunks []Hunk
	var cur *Hunk
	add := func(e Edit) {
		if e.OldStart == e.OldEnd && e.NewStart == e.NewEnd {
			return
		}
		if cur == nil {
			hunks = append(hunks, Hunk{OldStart: e.OldStart, NewStart: e.NewStart})
			cur = &hunks[len(hunks)-1]
		}
		cur.Edits = append(cur.Edits, e)
		cur.OldEnd, cur.NewEnd = e.OldEnd, e.NewEnd
	}
	for i, e := range edits {
		if e.Op != Equal {
			if cur == nil && i > 0 && edits[i-1].Op == Equal {
				prev := edits[i-1]
				n := min(context, prev.OldEnd-prev.OldStart)
				add(Edit{Equal, prev.OldEnd - n, prev.OldEnd, prev.NewEnd - n, prev.NewEnd})
			}
			add(e)
			continue
		}
		if cur == nil {
			continue
		}
		if n := e.OldEnd - e.OldStart; n <= 2*context && i < len(edits)-1 {
			add(e)
			continue
		}
		n := min(context, e.OldEnd-e.OldStart)
		add(Edit{Equal, e.OldStart, e.OldStart + n, e.NewStart, e.NewStart + n})
		cur = nil
	}
	return hunks
}

// Pair is one removed item and one added item of the same change.
type Pair struct{ Old, New int }

// Pairs pairs the removed and added items of each change in order: the
// first removed item with the first added item, and so on, up to the
// shorter side. Extra items stay unpaired.
func Pairs(edits []Edit) []Pair {
	var pairs []Pair
	for i, e := range edits {
		if e.Op != Delete || i+1 >= len(edits) || edits[i+1].Op != Insert {
			continue
		}
		ins := edits[i+1]
		for j := 0; j < e.OldEnd-e.OldStart && j < ins.NewEnd-ins.NewStart; j++ {
			pairs = append(pairs, Pair{e.OldStart + j, ins.NewStart + j})
		}
	}
	return pairs
}
