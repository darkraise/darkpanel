package predict

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// FuzzSetDefault feeds an arbitrary /etc/default/ufw or ufw.conf content, an
// upper-case key and a plain or double-quoted word to setDefault and
// ufwDefaults. Other input is left out: a value with a newline or an equals
// sign is not one setDefault is given, and UFW's set_default appends to a last
// line without a newline by joining the two, so the content gets its final
// newline first. A key that the file also spells in another case is left out
// too, because UFW reads both spellings as one key and the last one wins.
// setDefault is then idempotent and ufwDefaults reads back the value it set
// (R74).
func FuzzSetDefault(f *testing.F) {
	for _, seed := range []struct{ pattern, key, value string }{
		{filepath.Join(predictGolden, "*", "*", "files", "etc", "default", "ufw"), "DEFAULT_INPUT_POLICY", `"REJECT"`},
		{filepath.Join(predictGolden, "*", "*", "files", "etc", "ufw", "ufw.conf"), "ENABLED", "yes"},
		{filepath.Join(predictGolden, "*", "*", "files", "etc", "ufw", "ufw.conf"), "LOGLEVEL", "high"},
	} {
		names, err := filepath.Glob(seed.pattern)
		if err != nil {
			f.Fatal(err)
		}
		if len(names) == 0 {
			f.Fatalf("no seed matches %s", seed.pattern)
		}
		for _, name := range names {
			data, err := os.ReadFile(name) //nolint:gosec // G304: a fixture under testdata/
			if err != nil {
				f.Fatal(err)
			}
			f.Add(data, seed.key, seed.value)
		}
	}
	f.Add([]byte("IPV6=yes\nIPV6=no\n"), "IPV6", "no")
	f.Add([]byte("ENABLED=no\nenabled=yes\n"), "ENABLED", "yes")
	f.Add([]byte("A=1"), "B", `"x"`)
	f.Add([]byte(nil), "A", "a")
	key, word := regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`), regexp.MustCompile(`^(\w+|"\w+")$`)
	f.Fuzz(func(t *testing.T, data []byte, opt, value string) {
		if !key.MatchString(opt) || !word.MatchString(value) {
			return
		}
		if len(data) > 0 && data[len(data)-1] != '\n' {
			data = append(bytes.Clone(data), '\n')
		}
		for _, line := range strings.Split(string(data), "\n") {
			if k, _, _ := strings.Cut(line, "="); ufwDefaultRe.MatchString(line) && k != opt && strings.EqualFold(k, opt) {
				return
			}
		}
		once := setDefault(data, opt, value)
		if twice := setDefault(once, opt, value); !bytes.Equal(once, twice) {
			t.Fatalf("setDefault(%q, %q, %q) is not idempotent", data[:min(len(data), 64)], opt, value)
		}
		want := strings.Trim(strings.ToLower(value), `"'`)
		if got, ok := ufwDefaults(once)[strings.ToLower(opt)]; !ok || got != want {
			t.Fatalf("ufwDefaults reads %q, %v after setDefault(%q, %q, %q); want %q", got, ok, data[:min(len(data), 64)], opt, value, want)
		}
	})
}
