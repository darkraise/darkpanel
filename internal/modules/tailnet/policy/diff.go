package policy

import (
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
)

const (
	diffContext   = 3
	maxDiffCells  = 4_000_000
	diffGapMarker = "..."
)

// Diff is a line diff of two texts as core.DiffLine values (" ", "-", "+").
// Equal texts give nil; unchanged lines further than three lines from a change
// are left out, a gap between two kept lines shown as one "..." line.
func Diff(before, after []byte) []core.DiffLine {
	a, b := splitLines(before), splitLines(after)
	if equalLines(a, b) {
		return nil
	}
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}

	var all []core.DiffLine
	for _, l := range a[:prefix] {
		all = append(all, core.DiffLine{Op: " ", Text: l})
	}
	all = append(all, alignMiddle(a[prefix:len(a)-suffix], b[prefix:len(b)-suffix])...)
	for _, l := range a[len(a)-suffix:] {
		all = append(all, core.DiffLine{Op: " ", Text: l})
	}
	return withContext(all)
}

func splitLines(b []byte) []string {
	lines := strings.Split(string(b), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// alignMiddle aligns the differing middles with a longest-common-subsequence
// table. A middle too large for the table is shown whole as removed, then
// added.
func alignMiddle(a, b []string) []core.DiffLine {
	var out []core.DiffLine
	if (len(a)+1)*(len(b)+1) > maxDiffCells {
		for _, l := range a {
			out = append(out, core.DiffLine{Op: "-", Text: l})
		}
		for _, l := range b {
			out = append(out, core.DiffLine{Op: "+", Text: l})
		}
		return out
	}
	w := len(b) + 1
	t := make([]int32, (len(a)+1)*w)
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				t[i*w+j] = t[(i+1)*w+j+1] + 1
			case t[(i+1)*w+j] >= t[i*w+j+1]:
				t[i*w+j] = t[(i+1)*w+j]
			default:
				t[i*w+j] = t[i*w+j+1]
			}
		}
	}
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, core.DiffLine{Op: " ", Text: a[i]})
			i++
			j++
		case t[(i+1)*w+j] >= t[i*w+j+1]:
			out = append(out, core.DiffLine{Op: "-", Text: a[i]})
			i++
		default:
			out = append(out, core.DiffLine{Op: "+", Text: b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		out = append(out, core.DiffLine{Op: "-", Text: a[i]})
	}
	for ; j < len(b); j++ {
		out = append(out, core.DiffLine{Op: "+", Text: b[j]})
	}
	return out
}

func withContext(all []core.DiffLine) []core.DiffLine {
	keep := make([]bool, len(all))
	for k, l := range all {
		if l.Op == " " {
			continue
		}
		for c := max(0, k-diffContext); c <= min(len(all)-1, k+diffContext); c++ {
			keep[c] = true
		}
	}
	var out []core.DiffLine
	last := -1
	for k, l := range all {
		if !keep[k] {
			continue
		}
		if last >= 0 && k > last+1 {
			out = append(out, core.DiffLine{Op: " ", Text: diffGapMarker})
		}
		out = append(out, l)
		last = k
	}
	return out
}
