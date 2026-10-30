package policy

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const edDoc = `// Tailnet policy
{
	// who may do what
	"acls": [
		{"action": "accept", "src": ["*"], "dst": ["*:*"]}, // allow all
	],
	"tagOwners": {
		"tag:web": ["autogroup:admin"], // web servers
	},
	"derpMap": {
		"Regions": {
			"900": {"RegionID": 900},
		},
	},
}
`

func edVal(s string) json.RawMessage { return json.RawMessage(s) }

func edCanon(t *testing.T, text string) string {
	t.Helper()
	c, err := Canonical([]byte(text))
	if err != nil {
		t.Fatalf("canonical of %q: %v", text, err)
	}
	return string(c)
}

func edInvalid(t *testing.T, err error, field string) {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) {
		t.Fatalf("error %v, want *errcode.Error", err)
	}
	if e.Code != errcode.InvalidInput {
		t.Errorf("code %s, want %s", e.Code, errcode.InvalidInput)
	}
	if len(e.Fields) != 1 || e.Fields[0].Path != field {
		t.Errorf("fields %+v, want one at %s", e.Fields, field)
	}
}

// TestEDApplyEdits: each edit kind produces the expected document, keeps the
// comments around what it does not touch, and converges: applying the same
// edits to the result changes nothing, byte for byte.
func TestEDApplyEdits(t *testing.T) {
	tests := []struct {
		name      string
		in        string // edDoc when empty
		edits     []Edit
		want      string // the expected document; empty: the text is returned unchanged
		wantExact string // substrings the output must still contain
	}{
		{name: "set adds a member",
			edits: []Edit{{Op: "set", Path: "/tagOwners/tag:db", Value: edVal(`["autogroup:admin"]`)}},
			want: `{"acls":[{"action":"accept","src":["*"],"dst":["*:*"]}],"tagOwners":{"tag:web":["autogroup:admin"],` +
				`"tag:db":["autogroup:admin"]},"derpMap":{"Regions":{"900":{"RegionID":900}}}}`,
			wantExact: "// allow all|// web servers|// who may do what|// Tailnet policy"},
		{name: "set replaces a member and keeps its comment",
			edits: []Edit{{Op: "set", Path: "/tagOwners/tag:web", Value: edVal(`["group:ops"]`)}},
			want: `{"acls":[{"action":"accept","src":["*"],"dst":["*:*"]}],"tagOwners":{"tag:web":["group:ops"]},` +
				`"derpMap":{"Regions":{"900":{"RegionID":900}}}}`,
			wantExact: "// web servers|// allow all"},
		{name: "set an equal value is no change",
			edits: []Edit{{Op: "set", Path: "/derpMap/Regions/900", Value: edVal(`{ "RegionID":900 }`)}}},
		{name: "set an equal object with other key order is no change",
			in:    `{"a": {"x": 1, "y": [1, 2]}}`,
			edits: []Edit{{Op: "set", Path: "/a", Value: edVal(`{"y":[1,2],"x":1}`)}}},
		{name: "delete removes a member",
			edits: []Edit{{Op: "delete", Path: "/derpMap/Regions/900"}},
			want: `{"acls":[{"action":"accept","src":["*"],"dst":["*:*"]}],"tagOwners":{"tag:web":["autogroup:admin"]},` +
				`"derpMap":{"Regions":{}}}`,
			wantExact: "// web servers|// allow all"},
		{name: "delete an absent member is no change",
			edits: []Edit{{Op: "delete", Path: "/tagOwners/tag:none"}}},
		{name: "addItem appends an element",
			edits: []Edit{{Op: "addItem", Path: "/acls", Value: edVal(`{"action":"accept","src":["a"],"dst":["b:1"]}`)}},
			want: `{"acls":[{"action":"accept","src":["*"],"dst":["*:*"]},{"action":"accept","src":["a"],"dst":["b:1"]}],` +
				`"tagOwners":{"tag:web":["autogroup:admin"]},"derpMap":{"Regions":{"900":{"RegionID":900}}}}`,
			wantExact: "// allow all|// web servers"},
		{name: "addItem of a present element is no change, whatever its formatting",
			edits: []Edit{{Op: "addItem", Path: "/acls",
				Value: edVal(`{ "dst": ["*:*"], "src": ["*"], "action": "accept" }`)}}},
		{name: "addItem to a string array",
			edits: []Edit{{Op: "addItem", Path: "/tagOwners/tag:web", Value: edVal(`"group:ops"`)}},
			want: `{"acls":[{"action":"accept","src":["*"],"dst":["*:*"]}],"tagOwners":{"tag:web":["autogroup:admin","group:ops"]},` +
				`"derpMap":{"Regions":{"900":{"RegionID":900}}}}`,
			wantExact: "// web servers"},
		{name: "addItem creates an absent array",
			edits: []Edit{{Op: "addItem", Path: "/grants", Value: edVal(`{"src":["*"],"dst":["*"],"ip":["*"]}`)}},
			want: `{"acls":[{"action":"accept","src":["*"],"dst":["*:*"]}],"tagOwners":{"tag:web":["autogroup:admin"]},` +
				`"derpMap":{"Regions":{"900":{"RegionID":900}}},"grants":[{"src":["*"],"dst":["*"],"ip":["*"]}]}`,
			wantExact: "// allow all"},
		{name: "removeItem removes every equal element",
			in:    `{"a": ["x", "y", "x", {"k": 1}, "x"]}`,
			edits: []Edit{{Op: "removeItem", Path: "/a", Value: edVal(`"x"`)}},
			want:  `{"a":["y",{"k":1}]}`},
		{name: "removeItem compares objects by canonical JSON",
			in:    `{"a": [{"k": 1, "j": 2}, {"k": 2}]}`,
			edits: []Edit{{Op: "removeItem", Path: "/a", Value: edVal(`{"j":2,"k":1}`)}},
			want:  `{"a":[{"k":2}]}`},
		{name: "removeItem with no match is no change",
			edits: []Edit{{Op: "removeItem", Path: "/tagOwners/tag:web", Value: edVal(`"group:none"`)}}},
		{name: "removeItem from an absent array is no change",
			edits: []Edit{{Op: "removeItem", Path: "/grants", Value: edVal(`"x"`)}}},
		{name: "edits apply in order",
			edits: []Edit{
				{Op: "set", Path: "/tagOwners/tag:db", Value: edVal(`["group:a"]`)},
				{Op: "addItem", Path: "/tagOwners/tag:db", Value: edVal(`"group:b"`)},
				{Op: "removeItem", Path: "/tagOwners/tag:db", Value: edVal(`"group:a"`)},
				{Op: "delete", Path: "/derpMap"},
			},
			want: `{"acls":[{"action":"accept","src":["*"],"dst":["*:*"]}],` +
				`"tagOwners":{"tag:web":["autogroup:admin"],"tag:db":["group:b"]}}`},
		{name: "a pointer escapes ~1 and ~0",
			in:    `{"a/b": {"c~d": 1}}`,
			edits: []Edit{{Op: "set", Path: "/a~1b/c~0d", Value: edVal(`2`)}},
			want:  `{"a/b":{"c~d":2}}`},
		{name: "no edits",
			edits: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			if in == "" {
				in = edDoc
			}
			orig := []byte(in)
			got, err := Apply(orig, tc.edits)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if string(orig) != in {
				t.Fatal("Apply changed its input slice")
			}
			if tc.want == "" {
				if string(got) != in {
					t.Fatalf("no change expected, got\n%s", got)
				}
			} else if c, w := edCanon(t, string(got)), edCanon(t, tc.want); c != w {
				t.Fatalf("canonical\n got %s\nwant %s", c, w)
			}
			for _, s := range strings.Split(tc.wantExact, "|") {
				if s != "" && !strings.Contains(string(got), s) {
					t.Errorf("output lost %q:\n%s", s, got)
				}
			}
			again, err := Apply(got, tc.edits)
			if err != nil {
				t.Fatalf("second Apply: %v", err)
			}
			if string(again) != string(got) {
				t.Errorf("not convergent: the second Apply changed the text\nfirst:\n%s\nsecond:\n%s", got, again)
			}
		})
	}
}

// TestEDApplyChangesOnlyTheEditedLines: an edit that adds content adds lines
// to the text and leaves every existing line as it was.
func TestEDApplyChangesOnlyTheEditedLines(t *testing.T) {
	got, err := Apply([]byte(edDoc), []Edit{
		{Op: "set", Path: "/tagOwners/tag:db", Value: edVal(`["autogroup:admin"]`)},
		{Op: "addItem", Path: "/acls", Value: edVal(`{"action":"accept","src":["a"],"dst":["b:1"]}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSuffix(edDoc, "\n"), "\n") {
		if !strings.Contains(string(got), line+"\n") {
			t.Errorf("line %q is gone or changed:\n%s", line, got)
		}
	}
}

func TestEDApplyRefusals(t *testing.T) {
	tests := []struct {
		name  string
		in    string // edDoc when empty
		edits []Edit
		field string
	}{
		{name: "a path through an array index (set)", field: "/edits/0",
			edits: []Edit{{Op: "set", Path: "/acls/0/action", Value: edVal(`"deny"`)}}},
		{name: "a path ending at an array index (delete)", field: "/edits/0",
			edits: []Edit{{Op: "delete", Path: "/acls/0"}}},
		{name: "a path through an array index (addItem)", field: "/edits/0",
			edits: []Edit{{Op: "addItem", Path: "/acls/0/src", Value: edVal(`"x"`)}}},
		{name: "the end-of-array token", field: "/edits/0",
			edits: []Edit{{Op: "set", Path: "/acls/-", Value: edVal(`1`)}}},
		{name: "unknown op replace", field: "/edits/0",
			edits: []Edit{{Op: "replace", Path: "/derpMap", Value: edVal(`{}`)}}},
		{name: "unknown op test", field: "/edits/0",
			edits: []Edit{{Op: "test", Path: "/derpMap", Value: edVal(`{}`)}}},
		{name: "unknown op move", field: "/edits/0",
			edits: []Edit{{Op: "move", Path: "/derpMap"}}},
		{name: "empty op", field: "/edits/0",
			edits: []Edit{{Path: "/derpMap"}}},
		{name: "missing parent (set)", field: "/edits/0",
			edits: []Edit{{Op: "set", Path: "/nope/x", Value: edVal(`1`)}}},
		{name: "missing parent (delete)", field: "/edits/0",
			edits: []Edit{{Op: "delete", Path: "/derpMap/Missing/900"}}},
		{name: "missing parent (addItem)", field: "/edits/0",
			edits: []Edit{{Op: "addItem", Path: "/nope/list", Value: edVal(`"x"`)}}},
		{name: "a parent that is a literal", field: "/edits/0", in: `{"a": 1}`,
			edits: []Edit{{Op: "set", Path: "/a/b", Value: edVal(`2`)}}},
		{name: "addItem to an object", field: "/edits/0",
			edits: []Edit{{Op: "addItem", Path: "/tagOwners", Value: edVal(`"x"`)}}},
		{name: "removeItem from a string", field: "/edits/0", in: `{"a": "text"}`,
			edits: []Edit{{Op: "removeItem", Path: "/a", Value: edVal(`"t"`)}}},
		{name: "set without a value", field: "/edits/0",
			edits: []Edit{{Op: "set", Path: "/derpMap"}}},
		{name: "addItem without a value", field: "/edits/0",
			edits: []Edit{{Op: "addItem", Path: "/acls"}}},
		{name: "removeItem without a value", field: "/edits/0",
			edits: []Edit{{Op: "removeItem", Path: "/acls"}}},
		{name: "a value that is not JSON", field: "/edits/0",
			edits: []Edit{{Op: "set", Path: "/derpMap", Value: edVal(`{`)}}},
		{name: "the root path", field: "/edits/0",
			edits: []Edit{{Op: "set", Path: "", Value: edVal(`{}`)}}},
		{name: "a path without a leading slash", field: "/edits/0",
			edits: []Edit{{Op: "set", Path: "derpMap", Value: edVal(`{}`)}}},
		{name: "a bad pointer escape", field: "/edits/0",
			edits: []Edit{{Op: "set", Path: "/a~2b", Value: edVal(`1`)}}},
		{name: "the failing edit is named by its index", field: "/edits/2",
			edits: []Edit{
				{Op: "set", Path: "/tagOwners/tag:db", Value: edVal(`["group:a"]`)},
				{Op: "delete", Path: "/derpMap"},
				{Op: "set", Path: "/derpMap/Regions", Value: edVal(`{}`)},
			}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			if in == "" {
				in = edDoc
			}
			got, err := Apply([]byte(in), tc.edits)
			if err == nil {
				t.Fatalf("Apply succeeded with\n%s", got)
			}
			edInvalid(t, err, tc.field)
			if got != nil {
				t.Errorf("Apply returned text %q with its error", got)
			}
		})
	}
}

func TestEDApplyRejectsUnparsableText(t *testing.T) {
	for _, in := range []string{"{", `{"a": }`, ""} {
		_, err := Apply([]byte(in), []Edit{{Op: "delete", Path: "/a"}})
		if err == nil || !strings.HasPrefix(err.Error(), "policy:") {
			t.Errorf("Apply(%q) error %v, want one starting with \"policy:\"", in, err)
		}
	}
}
