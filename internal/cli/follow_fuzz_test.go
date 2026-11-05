package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode"
)

func fz10Head(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

// fz10Hazard reports whether r is one a terminal would act on: a control character other than the line feed
// that ends a line (C0, DEL and C1), a line or paragraph separator, or a bidirectional control.
func fz10Hazard(r rune) bool {
	switch {
	case r == '\n':
		return false
	case unicode.IsControl(r), r == 0x061c, r == 0x2028, r == 0x2029, r == 0x200e, r == 0x200f:
		return true
	}
	return (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}

// FuzzPrintEvents checks that whatever a controller sends, printEvents writes only complete lines of valid JSON,
// none holding a control or bidirectional character a terminal would act on.
func FuzzPrintEvents(f *testing.F) {
	end := "event: end\ndata: {\"reason\":\"done\"}\n\n"
	f.Add("event: data\ndata: {\"line\":\"a\"}\n\n: ping\n\n" + end)
	f.Add("event: data\ndata: {\"line\":\"a\u009b31mb\u202ec\u00e9\"}\n\n" + end)
	f.Add("event: data\ndata: {\"line\":\"a\u007fb\u2028c\u0085d\"}\n\n" + end)
	f.Add("event: data\ndata: {\"line\":\r\"a\",\t\"n\"\ndata: :\r1}\n\n" + end)
	f.Add("event: data\ndata: {\"line\":\"\\u001b[31m\"}\n\n" + end)
	f.Add("event: data\ndata: {\"line\":\n\n")
	f.Add("event: end\ndata: {\"reason\":\"error\",\"error\":{\"code\":\"not_found\",\"detail\":\"no container c1\"}}\n\n")
	f.Add("event: data\ndata: \"\x1b[2J\"\n\n" + end)
	f.Add("")
	f.Fuzz(func(t *testing.T, in string) {
		var out strings.Builder
		_ = printEvents(strings.NewReader(in), &out)
		text := out.String()
		if text == "" {
			return
		}
		if !strings.HasSuffix(text, "\n") {
			t.Fatalf("printEvents(%q) wrote %q, which does not end a line", fz10Head(in), fz10Head(text))
		}
		for _, r := range text {
			if fz10Hazard(r) {
				t.Fatalf("printEvents(%q) wrote %q, which holds U+%04X", fz10Head(in), fz10Head(text), r)
			}
		}
		for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
			if !json.Valid([]byte(line)) {
				t.Fatalf("printEvents(%q) wrote the line %q, which is not JSON", fz10Head(in), fz10Head(line))
			}
		}
	})
}

// TestFz10PrintEventsEscapesDEL pins that a DEL in an event reaches the terminal escaped, as the C1 controls do.
func TestFz10PrintEventsEscapesDEL(t *testing.T) {
	in := "event: data\ndata: {\"line\":\"a\x7fb\"}\n\nevent: end\ndata: {\"reason\":\"done\"}\n\n"
	var out strings.Builder
	if err := printEvents(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if want := `{"line":"a\u007fb"}` + "\n"; out.String() != want {
		t.Fatalf("printed %q, want %q", out.String(), want)
	}
}
