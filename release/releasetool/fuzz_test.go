package main

import (
	"cmp"
	"regexp"
	"testing"

	"github.com/darkraise/darkpanel/internal/update"
)

var fz9KeyIDPattern = regexp.MustCompile(`^[0-9A-F]{16}$`)

func fz9Head(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

// FuzzParseVersion checks that every version parseVersion accepts, with or without the v of parseTag, is also
// accepted by update.ParseVersion and orders against any other accepted version with the same sign, and that
// compare is antisymmetric.
func FuzzParseVersion(f *testing.F) {
	for _, pair := range [][2]string{
		{"1.0.0-rc.2", "1.0.0-rc.10"},
		{"1.0.0-rc.1", "1.0.0"},
		{"1.2.3", "1.10.0"},
		{"1.0.0-alpha", "1.0.0-alpha.1"},
		{"1.0.0-99999999999999999999", "1.0.0-100000000000000000000"},
		{"1.0.0-18446744073709551616", "1.0.0-18446744073709551615"},
		{"1.0.0-5", "1.0.0-18446744073709551616"},
		{"1.2.3-01", "1.2.3-1"},
		{"v1.2.3", "1.2"},
	} {
		f.Add(pair[0], pair[1])
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		va, errA := parseVersion(a)
		vb, errB := parseVersion(b)
		if _, errTag := parseTag("v" + a); (errA == nil) != (errTag == nil) {
			t.Fatalf("parseVersion(%q) error = %v but parseTag of its v form error = %v", fz9Head(a), errA, errTag)
		}
		ua, uerrA := update.ParseVersion(a)
		ub, uerrB := update.ParseVersion(b)
		if errA == nil && uerrA != nil {
			t.Fatalf("parseVersion accepts %q but update.ParseVersion refuses it: %v", fz9Head(a), uerrA)
		}
		if errB == nil && uerrB != nil {
			t.Fatalf("parseVersion accepts %q but update.ParseVersion refuses it: %v", fz9Head(b), uerrB)
		}
		if errA != nil || errB != nil {
			return
		}
		ab, ba := compare(va, vb), compare(vb, va)
		if ab != -ba {
			t.Fatalf("compare(%q, %q) = %d but compare(%q, %q) = %d", fz9Head(a), fz9Head(b), ab, fz9Head(b), fz9Head(a), ba)
		}
		if want := ua.Compare(ub); cmp.Compare(ab, 0) != want {
			t.Fatalf("compare(%q, %q) = %d but update.Version.Compare = %d", fz9Head(a), fz9Head(b), ab, want)
		}
	})
}

// TestFz9CompareBeyondUint64 pins that numeric prerelease identifiers too large for uint64 order by value, as
// update.Version.Compare orders them, and not as text.
func TestFz9CompareBeyondUint64(t *testing.T) {
	for _, tt := range []struct{ lo, hi string }{
		{"1.0.0-99999999999999999999", "1.0.0-100000000000000000000"},
		{"1.0.0-9999999999999999999999", "1.0.0-10000000000000000000000"},
		{"1.0.0-18446744073709551615", "1.0.0-18446744073709551616"},
		{"1.0.0-18446744073709551616", "1.0.0-99999999999999999999"},
		{"1.0.0-5", "1.0.0-18446744073709551616"},
		{"1.0.0-99999999999999999999", "1.0.0-alpha"},
	} {
		lo, err := parseVersion(tt.lo)
		if err != nil {
			t.Fatal(err)
		}
		hi, err := parseVersion(tt.hi)
		if err != nil {
			t.Fatal(err)
		}
		if got := compare(lo, hi); got != -1 {
			t.Errorf("compare(%s, %s) = %d, want -1", tt.lo, tt.hi, got)
		}
		if got := compare(hi, lo); got != 1 {
			t.Errorf("compare(%s, %s) = %d, want 1", tt.hi, tt.lo, got)
		}
		ulo, _ := update.ParseVersion(tt.lo)
		uhi, _ := update.ParseVersion(tt.hi)
		if got := ulo.Compare(uhi); got != -1 {
			t.Errorf("update.Compare(%s, %s) = %d, want -1", tt.lo, tt.hi, got)
		}
	}
}

// FuzzKeyID checks that a key id keyID returns is 16 upper-case hexadecimal characters.
func FuzzKeyID(f *testing.F) {
	f.Add(minisig("ED", 0x0123456789ABCDEF))
	f.Add(minisig("ED", 1))
	f.Add(minisig("Ed", 1))
	f.Add([]byte("untrusted comment: x\nAAAA\n"))
	f.Add([]byte("untrusted comment: x\n!!!!\n"))
	f.Add([]byte("not a signature\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, sig []byte) {
		id, err := keyID(sig)
		if err == nil && !fz9KeyIDPattern.MatchString(id) {
			t.Fatalf("keyID(%q) = %q, want 16 upper-case hexadecimal characters", fz9Head(string(sig)), id)
		}
	})
}
