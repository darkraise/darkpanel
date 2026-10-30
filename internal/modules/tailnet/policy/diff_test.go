package policy

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func edLines(ops ...string) []core.DiffLine {
	out := make([]core.DiffLine, len(ops))
	for i, o := range ops {
		out[i] = core.DiffLine{Op: o[:1], Text: o[1:]}
	}
	return out
}

func TestEDDiff(t *testing.T) {
	tests := []struct {
		name          string
		before, after string
		want          []core.DiffLine
	}{
		{"equal texts have no lines", "a\nb\n", "a\nb\n", nil},
		{"both empty", "", "", nil},
		{"a changed line with three lines of context",
			"a\nb\nc\nd\ne\nf\ng\nh\n", "a\nb\nc\nD\ne\nf\ng\nh\n",
			edLines(" a", " b", " c", "-d", "+D", " e", " f", " g")},
		{"a line added at the end", "a\nb\n", "a\nb\nc\n", edLines(" a", " b", "+c")},
		{"a line removed at the start", "a\nb\nc\n", "b\nc\n", edLines("-a", " b", " c")},
		{"a line inserted in the middle", "a\nc\n", "a\nb\nc\n", edLines(" a", "+b", " c")},
		{"everything new", "", "a\nb\n", edLines("+a", "+b")},
		{"everything removed", "a\nb\n", "", edLines("-a", "-b")},
		{"a missing final newline alone is not a line change", "a\nb", "a\nb\n", nil},
		{"changes far apart are separated",
			"a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\n", "A\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nM\n",
			edLines("-a", "+A", " b", " c", " d", " ...", " j", " k", " l", "-m", "+M")},
		{"changes close together form one hunk",
			"a\nb\nc\nd\ne\nf\n", "A\nb\nc\nd\ne\nF\n",
			edLines("-a", "+A", " b", " c", " d", " e", "-f", "+F")},
		{"a carriage return is part of the line", "a\r\nb\r\n", "a\nb\n",
			edLines("-a\r", "-b\r", "+a", "+b")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Diff([]byte(tc.before), []byte(tc.after))
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got  %v\nwant %v", got, tc.want)
			}
		})
	}
}

// TestEDDiffOfAnEdit: the diff of an applied edit shows the added lines and
// no removed one.
func TestEDDiffOfAnEdit(t *testing.T) {
	out, err := Apply([]byte(edDoc), []Edit{{Op: "set", Path: "/tagOwners/tag:db", Value: edVal(`["autogroup:admin"]`)}})
	if err != nil {
		t.Fatal(err)
	}
	var plus, minus int
	for _, l := range Diff([]byte(edDoc), out) {
		switch l.Op {
		case "+":
			plus++
		case "-":
			minus++
		}
	}
	if plus == 0 || minus != 0 {
		t.Errorf("%d added and %d removed lines, want some added and none removed", plus, minus)
	}
}

// TestEDDiffLargeMiddle: when the differing middle is too large to align
// (more than four million table cells), it is shown as removed then added,
// even where it has lines in common.
func TestEDDiffLargeMiddle(t *testing.T) {
	const n = 2500
	var a, b strings.Builder
	a.WriteString("head\n")
	b.WriteString("head\n")
	for i := range n {
		if i == n/2 {
			a.WriteString("common\n")
			b.WriteString("common\n")
			continue
		}
		fmt.Fprintf(&a, "old %d\n", i)
		fmt.Fprintf(&b, "new %d\n", i)
	}
	a.WriteString("tail\n")
	b.WriteString("tail\n")
	var plus, minus, common int
	for _, l := range Diff([]byte(a.String()), []byte(b.String())) {
		switch {
		case l.Op == "+":
			plus++
		case l.Op == "-":
			minus++
		case l.Text == "common":
			common++
		}
	}
	if plus != n || minus != n || common != 0 {
		t.Errorf("%d added, %d removed, %d common lines; want %d, %d, 0", plus, minus, common, n, n)
	}
}

// TestEDDiffAlignsSmallMiddle: below the limit the common line is kept.
func TestEDDiffAlignsSmallMiddle(t *testing.T) {
	got := Diff([]byte("head\nold 1\ncommon\nold 2\ntail\n"), []byte("head\nnew 1\ncommon\nnew 2\ntail\n"))
	want := edLines(" head", "-old 1", "+new 1", " common", "-old 2", "+new 2", " tail")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}
