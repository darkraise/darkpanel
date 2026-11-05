package artifact

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// FuzzParseManifest feeds arbitrary bytes to ParseManifest: it never panics,
// an error starts with the package name, and a manifest it accepts encodes,
// parses back to itself and encodes to the same bytes (R74).
func FuzzParseManifest(f *testing.F) {
	enc, err := mfManifest().Encode()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(enc)
	f.Add([]byte(mfDoc(mfDarkpanel, mfDerper, mfArtifacts)))
	f.Add([]byte(mfDoc(`"darkpanelVersion":"1.5.0-beta.1"`, mfDerper, mfArtifacts)))
	f.Add([]byte(mfDoc(mfDarkpanel, mfDerper)))
	f.Add([]byte(mfDoc(mfDarkpanel, mfDerper, mfArtifacts, `"extra":1`)))
	f.Add([]byte(`null`))
	f.Add([]byte(mfDoc(mfDarkpanel, mfDerper, mfArtifacts) + `{}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		m, err := ParseManifest(b)
		if err != nil {
			if !strings.HasPrefix(err.Error(), "artifact:") {
				t.Fatalf("ParseManifest(%q) error %q does not start with the package name", b[:min(len(b), 64)], err)
			}
			return
		}
		enc, err := m.Encode()
		if err != nil {
			t.Fatalf("a manifest ParseManifest accepted from %q does not encode: %v", b[:min(len(b), 64)], err)
		}
		back, err := ParseManifest(enc)
		if err != nil || !reflect.DeepEqual(back, m) {
			t.Fatalf("ParseManifest(Encode()) of %q gave %+v, %v; want %+v", b[:min(len(b), 64)], back, err, m)
		}
		if again, err := back.Encode(); err != nil || !bytes.Equal(again, enc) {
			t.Fatalf("Encode of %q is not stable: %v", b[:min(len(b), 64)], err)
		}
	})
}
