package secret

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func fz10Head(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

// FuzzOpen checks that Open opens only what Seal produced. Seal's nonce is random, so the target seals plain
// itself, requires the round trip, and then requires that the ciphertext with one byte changed, cut short,
// extended, or opened under other associated data, and any junk bytes, do not open.
func FuzzOpen(f *testing.F) {
	s, err := NewSealer(bytes.Repeat([]byte{1}, KeySize))
	if err != nil {
		f.Fatal(err)
	}
	const seedAD = "core_users.totp_secret_ct.01JROW"
	f.Add(seedAD, []byte("JBSWY3DPEHPK3PXP"), []byte("junk"), uint(0), byte(1))
	f.Add(seedAD, []byte{}, []byte{}, uint(1), byte(0x80))
	f.Add(seedAD, bytes.Repeat([]byte{0xff}, 300), []byte{0}, uint(40), byte(0xff))
	f.Add("", []byte("x"), []byte{1}, uint(9), byte(1))
	f.Fuzz(func(t *testing.T, ad string, plain, junk []byte, pos uint, mask byte) {
		ct, err := s.Seal(ad, plain)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := s.Open(ad, ct); err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("Open(Seal(%q)) = %q, %v", fz10Head(string(plain)), fz10Head(string(got)), err)
		}
		at := int(pos % uint(len(ct))) //nolint:gosec // G115: the remainder is below len(ct)
		if mask != 0 {
			changed := bytes.Clone(ct)
			changed[at] ^= mask
			if _, err := s.Open(ad, changed); err == nil {
				t.Fatalf("Open accepted a ciphertext with byte %d changed", at)
			}
		}
		if _, err := s.Open(ad, ct[:at]); err == nil {
			t.Fatalf("Open accepted a ciphertext cut to %d bytes", at)
		}
		if _, err := s.Open(ad+"x", ct); err == nil {
			t.Fatal("Open accepted other associated data")
		}
		if len(junk) > 0 {
			if _, err := s.Open(ad, append(bytes.Clone(ct), junk...)); err == nil {
				t.Fatal("Open accepted a ciphertext with bytes appended")
			}
		}
		if _, err := s.Open(ad, junk); err == nil {
			t.Fatalf("Open accepted the bytes %q that Seal did not produce", fz10Head(string(junk)))
		}
	})
}

const fz10Marker = "SECRETMARKER"

// fz10Document is an inSchema document whose every secret value starts with fz10Marker and whose other strings
// are plain.
func fz10Document(tb testing.TB, plain, secret string) []byte {
	tb.Helper()
	cred := map[string]any{"user": plain, "password": fz10Marker + secret}
	doc, err := json.Marshal(map[string]any{
		"name":  plain,
		"creds": []any{cred, cred},
		"main":  cred,
		"tags":  map[string]any{plain: plain},
	})
	if err != nil {
		tb.Fatal(err)
	}
	return doc
}

// FuzzRedactJSON checks that RedactJSON returns valid JSON for any document and schema and leaves its inputs
// alone, and that no value the schema marks secret appears in the output of a document built from fuzzed strings.
func FuzzRedactJSON(f *testing.F) {
	f.Add("web", "p1", fz10Document(f, "web", "p1"), []byte(inSchema))
	f.Add("", "", []byte(`{"name":"web","creds":[{"user":"a","password":"p1"}],"main":null,"tags":{"env":"prod"},"extra":{"k":[1,2]}}`), []byte(inSchema))
	f.Add("a", "b", []byte(`{"main":{"user":"c","password":{"nested":["x"]}},"name":"web"}`), []byte(`{"$ref":"#/$defs/Missing"}`))
	f.Add("a", "b", []byte(`[1,2,3]`), []byte(`true`))
	f.Add("a", "b", []byte(`{"a":1}{"b":2}`), []byte(inSchema))
	f.Add("a", "b", []byte(`not json`), []byte(`{`))
	f.Add("a", "b", []byte(`{"n":1e999}`), []byte(`{"properties":{"n":{"x-secret":false}}}`))
	f.Fuzz(func(t *testing.T, plain, secret string, raw, schema []byte) {
		rawBefore, schemaBefore := bytes.Clone(raw), bytes.Clone(schema)
		for _, out := range []json.RawMessage{
			RedactJSON(raw, schema),
			RedactJSON(raw, []byte(inSchema)),
			RedactJSON(fz10Document(t, plain, secret), schema),
		} {
			if !json.Valid(out) {
				t.Fatalf("RedactJSON(%q, %q) = %q, which is not valid JSON", fz10Head(string(raw)), fz10Head(string(schema)), fz10Head(string(out)))
			}
		}
		if !bytes.Equal(raw, rawBefore) || !bytes.Equal(schema, schemaBefore) {
			t.Fatalf("RedactJSON(%q, %q) modified its input", fz10Head(string(raw)), fz10Head(string(schema)))
		}
		if strings.Contains(plain, fz10Marker) {
			return
		}
		if out := RedactJSON(fz10Document(t, plain, secret), []byte(inSchema)); bytes.Contains(out, []byte(fz10Marker)) {
			t.Fatalf("RedactJSON left a secret value in %q", fz10Head(string(out)))
		}
	})
}
