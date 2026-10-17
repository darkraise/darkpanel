package platform

import (
	"errors"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestReplaceBlock(t *testing.T) {
	tests := []struct {
		name, prefix, in, body, want string
	}{
		{"empty file", "#", "", "allow 22\n", "# BEGIN DARKPANEL fw/rules\nallow 22\n# END DARKPANEL fw/rules\n"},
		{"append after content", "#", "a\n", "x\n", "a\n# BEGIN DARKPANEL fw/rules\nx\n# END DARKPANEL fw/rules\n"},
		{"append adds a missing newline", "#", "a", "x", "a\n# BEGIN DARKPANEL fw/rules\nx\n# END DARKPANEL fw/rules\n"},
		{
			"replace keeps surrounding lines", "#",
			"a\n# BEGIN DARKPANEL fw/rules\nold\nold2\n# END DARKPANEL fw/rules\nb\n", "new\n",
			"a\n# BEGIN DARKPANEL fw/rules\nnew\n# END DARKPANEL fw/rules\nb\n",
		},
		{
			"other blocks untouched", "#",
			"# BEGIN DARKPANEL fw/other\nkeep\n# END DARKPANEL fw/other\n", "x\n",
			"# BEGIN DARKPANEL fw/other\nkeep\n# END DARKPANEL fw/other\n# BEGIN DARKPANEL fw/rules\nx\n# END DARKPANEL fw/rules\n",
		},
		{"empty body", "#", "", "", "# BEGIN DARKPANEL fw/rules\n# END DARKPANEL fw/rules\n"},
		{"comment prefix", "//", "", "x\n", "// BEGIN DARKPANEL fw/rules\nx\n// END DARKPANEL fw/rules\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := replaceBlock([]byte(tt.in), tt.prefix, "fw", "rules", []byte(tt.body))
			if err != nil {
				t.Fatalf("replaceBlock: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("got\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestRemoveBlock(t *testing.T) {
	in := "a\n# BEGIN DARKPANEL fw/rules\nx\n# END DARKPANEL fw/rules\nb\n"
	got, err := removeBlock([]byte(in), "#", "fw", "rules")
	if err != nil {
		t.Fatalf("removeBlock: %v", err)
	}
	if want := "a\nb\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
	got, err = removeBlock([]byte("a\n"), "#", "fw", "rules")
	if err != nil || string(got) != "a\n" {
		t.Errorf("absent block: got %q, %v; want the content unchanged", got, err)
	}
}

func TestInvalidMarkers(t *testing.T) {
	tests := map[string]string{
		"duplicated":        "# BEGIN DARKPANEL fw/rules\n# END DARKPANEL fw/rules\n# BEGIN DARKPANEL fw/rules\n# END DARKPANEL fw/rules\n",
		"nested":            "# BEGIN DARKPANEL fw/a\n# BEGIN DARKPANEL fw/b\n# END DARKPANEL fw/b\n# END DARKPANEL fw/a\n",
		"unterminated":      "a\n# BEGIN DARKPANEL fw/rules\nx\n",
		"end without begin": "# END DARKPANEL fw/rules\n",
		"mismatched end":    "# BEGIN DARKPANEL fw/a\n# END DARKPANEL fw/b\n",
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := replaceBlock([]byte(in), "#", "fw", "rules", []byte("x\n"))
			assertMarkersInvalid(t, err)
			_, err = removeBlock([]byte(in), "#", "fw", "rules")
			assertMarkersInvalid(t, err)
		})
	}
}

func TestBodyWithMarkerRefused(t *testing.T) {
	_, err := replaceBlock(nil, "#", "fw", "rules", []byte("# END DARKPANEL fw/rules\n"))
	assertMarkersInvalid(t, err)
}

func assertMarkersInvalid(t *testing.T, err error) {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.FilesMarkersBad {
		t.Errorf("err = %v, want %s", err, errcode.FilesMarkersBad)
	}
}
