package model

import "testing"

// fz7Head returns at most the first 64 bytes of s, for a failure message.
func fz7Head(s string) string { return s[:min(len(s), 64)] }

// TestFz7ParseTagTrailingSpace: Tag never writes a space after an id without
// a label, so such a comment is a manual rule's and not a tag.
func TestFz7ParseTagTrailingSpace(t *testing.T) {
	comment := TagPrefix + ruleTestID + " "
	if id, label, ok := ParseTag(comment); ok {
		t.Fatalf("ParseTag(%q) = %q, %q, true", comment, id, label)
	}
}

// FuzzParseTag feeds an arbitrary rule comment to ParseTag: it never panics,
// a comment it refuses gives empty parts, and a comment it accepts holds a
// ULID and is exactly what Tag writes for the id and label it returns (R74).
func FuzzParseTag(f *testing.F) {
	for _, c := range []string{
		Tag(ruleTestID, ""), Tag(ruleTestID, "ssh from office"), Tag(ruleTestID, " two"),
		TagPrefix + ruleTestID + " ", TagPrefix + ruleTestID + "x", TagPrefix + "01J0000000000000000000000",
		"Darkpanel:" + ruleTestID, "hand rule", "",
	} {
		f.Add(c)
	}
	f.Fuzz(func(t *testing.T, comment string) {
		id, label, ok := ParseTag(comment)
		if !ok {
			if id != "" || label != "" {
				t.Fatalf("ParseTag(%q) refused the comment and returned %q, %q", fz7Head(comment), id, fz7Head(label))
			}
			return
		}
		if !ulidRe.MatchString(id) {
			t.Fatalf("ParseTag(%q) returned the id %q", fz7Head(comment), id)
		}
		if got := Tag(id, label); got != comment {
			t.Fatalf("Tag(%q, %q) = %q, want the comment %q", id, fz7Head(label), fz7Head(got), fz7Head(comment))
		}
	})
}
