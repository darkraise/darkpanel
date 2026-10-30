package policy

import (
	"strings"
	"testing"
)

func TestEDCanonical(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"comments, trailing commas and key order",
			"// c\n{\n\t\"b\": [1, 2, {\"d\": null, \"c\": true},], // x\n\t\"a\": /* y */ 1,\n}\n",
			`{"a":1,"b":[1,2,{"c":true,"d":null}]}`},
		{"a number keeps its digits", `{"n": 12345678901234567890, "f": 1.50}`, `{"f":1.50,"n":12345678901234567890}`},
		{"html characters are not escaped", `{"k": "<a & b>"}`, `{"k":"<a & b>"}`},
		{"a scalar", " \"x\" /* c */ ", `"x"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Canonical([]byte(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestEDCanonicalEqualDocuments(t *testing.T) {
	a := edCanon(t, edDoc)
	b := edCanon(t, `{"derpMap":{"Regions":{"900":{"RegionID":900}}},"tagOwners":{"tag:web":["autogroup:admin"]},`+
		`"acls":[{"dst":["*:*"],"src":["*"],"action":"accept"}]}`)
	if a != b {
		t.Errorf("same document, different canonical forms:\n%s\n%s", a, b)
	}
}

func TestEDCanonicalRejects(t *testing.T) {
	for _, in := range []string{"{", `{"a": }`, "", `{"a":1} {"b":2}`} {
		if _, err := Canonical([]byte(in)); err == nil || !strings.HasPrefix(err.Error(), "policy:") {
			t.Errorf("Canonical(%q) error %v, want one starting with \"policy:\"", in, err)
		}
	}
}
