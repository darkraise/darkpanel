package policy

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/tailscale/hujson"
)

// fz8Head returns at most the first 64 bytes of b, for a failure message.
func fz8Head(b []byte) []byte { return b[:min(len(b), 64)] }

// fz8Repeats reports whether an object anywhere in v names a member twice.
// hujson keeps both, and an edit reaches only the first, so such a document
// is outside the convergence Apply promises.
func fz8Repeats(v hujson.Value) bool {
	switch c := v.Value.(type) {
	case *hujson.Object:
		seen := map[string]bool{}
		for _, m := range c.Members {
			name := m.Name.Value.(hujson.Literal).String()
			if seen[name] || fz8Repeats(m.Value) {
				return true
			}
			seen[name] = true
		}
	case *hujson.Array:
		for _, e := range c.Elements {
			if fz8Repeats(e) {
				return true
			}
		}
	}
	return false
}

// FuzzApply feeds arbitrary HuJSON text and two arbitrary edits to Apply: it
// never panics, the text is nil on an error and parses as HuJSON otherwise, no
// edits give the text back byte for byte, and applying either edit to its own
// result changes nothing (R74).
func FuzzApply(f *testing.F) {
	f.Add([]byte(edDoc), "set", "/tagOwners/tag:db", `["autogroup:admin"]`, "addItem", "/acls", `{"action":"accept","src":["tag:web"],"dst":["tag:db:5432"]}`)
	f.Add([]byte(edDoc), "delete", "/derpMap/Regions/900", "", "removeItem", "/tagOwners/tag:web", `"autogroup:admin"`)
	f.Add([]byte(edDoc), "set", "/a~1b", `1`, "set", "/a~", `2`)
	f.Add([]byte(`{}`), "addItem", "/acls", `1`, "removeItem", "/acls", `1`)
	f.Add([]byte(`{"a":1,"a":2}`), "delete", "/a", "", "set", "/a", `3`)
	f.Add([]byte(`[1]`), "set", "/0", `2`, "bogus", "/x", `1`)
	f.Add([]byte(``), "set", "/a", `1`, "set", "/a", `1`)
	f.Fuzz(func(t *testing.T, text []byte, op1, path1, value1, op2, path2, value2 string) {
		edits := []Edit{
			{Op: op1, Path: path1, Value: json.RawMessage(value1)},
			{Op: op2, Path: path2, Value: json.RawMessage(value2)},
		}
		out, err := Apply(text, edits)
		if err != nil && out != nil {
			t.Fatalf("Apply of %q returned text with the error %v", fz8Head(text), err)
		}
		if err == nil {
			if _, err := hujson.Parse(bytes.Clone(out)); err != nil {
				t.Fatalf("Apply of %q returned text that does not parse: %v", fz8Head(text), err)
			}
		}
		doc, err := hujson.Parse(bytes.Clone(text))
		if err != nil {
			return
		}
		if same, err := Apply(text, nil); err != nil || !bytes.Equal(same, text) {
			t.Fatalf("Apply of %q without edits returned %q, %v", fz8Head(text), fz8Head(same), err)
		}
		if fz8Repeats(doc) {
			return
		}
		for _, e := range edits {
			once, err := Apply(text, []Edit{e})
			if err != nil {
				continue
			}
			if twice, err := Apply(once, []Edit{e}); err != nil || !bytes.Equal(twice, once) {
				t.Fatalf("edit %+v of %q does not converge: %v", e, fz8Head(text), err)
			}
		}
	})
}

// FuzzCanonical feeds arbitrary text to Canonical and two arbitrary texts to
// Diff: neither panics, Canonical returns nil on an error and otherwise valid
// JSON that is its own canonical form, Diff of a text with itself is empty,
// every line of a diff is a context, added or removed line, and the lines one
// direction adds are the lines the other removes (R74).
func FuzzCanonical(f *testing.F) {
	f.Add([]byte(edDoc), []byte(`{}`))
	f.Add([]byte("{\"b\":1,\"a\":[1.50,\"é\"], // c\n}"), []byte(`{"a":1}`))
	f.Add([]byte(`// only a comment`), []byte(``))
	f.Add([]byte("a\nb\nc\nd\ne\nf\ng\n"), []byte("a\nb\nX\nd\ne\nf\ng\n"))
	f.Fuzz(func(t *testing.T, a, b []byte) {
		canon, err := Canonical(a)
		switch {
		case err != nil && canon != nil:
			t.Fatalf("Canonical of %q returned text with the error %v", fz8Head(a), err)
		case err == nil && !json.Valid(canon):
			t.Fatalf("Canonical of %q is not valid JSON", fz8Head(a))
		case err == nil:
			if again, err := Canonical(canon); err != nil || !bytes.Equal(again, canon) {
				t.Fatalf("Canonical of %q is not its own canonical form: %v", fz8Head(a), err)
			}
		}
		if d := Diff(a, a); len(d) != 0 {
			t.Fatalf("Diff of %q with itself has %d lines", fz8Head(a), len(d))
		}
		fwd, back := Diff(a, b), Diff(b, a)
		var added, removed, addedBack, removedBack int
		for _, l := range fwd {
			switch l.Op {
			case " ":
			case "+":
				added++
			case "-":
				removed++
			default:
				t.Fatalf("Diff of %q and %q has the operation %q", fz8Head(a), fz8Head(b), l.Op)
			}
		}
		for _, l := range back {
			switch l.Op {
			case "+":
				addedBack++
			case "-":
				removedBack++
			}
		}
		if added != removedBack || removed != addedBack || (len(fwd) > 0 && added+removed == 0) {
			t.Fatalf("Diff of %q and %q adds %d and removes %d lines, its reverse adds %d and removes %d",
				fz8Head(a), fz8Head(b), added, removed, addedBack, removedBack)
		}
	})
}
