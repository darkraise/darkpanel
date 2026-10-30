package logfilter

import (
	"math/rand/v2"
	"strings"
	"testing"
)

func lfSecrets(ss ...string) [][]byte {
	out := make([][]byte, len(ss))
	for i, s := range ss {
		out[i] = []byte(s)
	}
	return out
}

// lfRun pushes each chunk to stdout in order, flushes stdout, and returns
// everything emitted.
func lfRun(f *Filter, chunks ...string) string {
	var out strings.Builder
	for _, c := range chunks {
		out.Write(f.Push("stdout", []byte(c)))
	}
	out.Write(f.Flush("stdout"))
	return out.String()
}

func TestLfMaskValue(t *testing.T) {
	if Mask != "[redacted]" {
		t.Errorf("Mask = %q, want [redacted]", Mask)
	}
}

func TestLfWithoutSecretsDataPassesUnchanged(t *testing.T) {
	for name, f := range map[string]*Filter{
		"nil":           New(nil),
		"empty list":    New([][]byte{}),
		"empty secrets": New([][]byte{nil, {}}),
	} {
		if got := string(f.Push("stdout", []byte("se"))); got != "se" {
			t.Errorf("%s: Push returned %q, want the data at once", name, got)
		}
		if got := string(f.Flush("stdout")); got != "" {
			t.Errorf("%s: Flush returned %q, want nothing held", name, got)
		}
	}
}

// lfCases are masked whole and at every split of the input into two or three
// pushes. The expected text is for the leftmost occurrence, the longest
// secret at that position, and scanning that resumes after the match.
var lfCases = []struct {
	name    string
	secrets []string
	text    string
	want    string
}{
	{"one occurrence", []string{"hunter2"}, "pw=hunter2 ok\n", "pw=[redacted] ok\n"},
	{"repeated and adjacent", []string{"hunter2"}, "hunter2hunter2 x hunter2", "[redacted][redacted] x [redacted]"},
	{"two secrets", []string{"alpha", "beta"}, "alpha beta gamma alphabeta", "[redacted] [redacted] gamma [redacted][redacted]"},
	{"one secret is a prefix of another", []string{"abc", "abcdef"}, "xabcdefy abcz abc", "x[redacted]y [redacted]z [redacted]"},
	{"the longer secret wins at one position", []string{"toke", "token"}, "token tokex", "[redacted] [redacted]x"},
	{"a false start is released", []string{"secret"}, "sesame secrecy secret", "sesame secrecy [redacted]"},
	{"a secret at the very end", []string{"end"}, "the end", "the [redacted]"},
	{"matches do not overlap", []string{"aa"}, "aaa", "[redacted]a"},
	{"a partial secret at the end stays", []string{"secret"}, "tail se", "tail se"},
	{"binary secret", []string{"\xff\x00k"}, "v=\xff\x00k;", "v=[redacted];"},
	{"secret with a newline", []string{"a\nb"}, "x a\nb y", "x [redacted] y"},
	{"nothing to mask", []string{"hunter2"}, "plain log line\n", "plain log line\n"},
}

func TestLfMasksWhole(t *testing.T) {
	for _, c := range lfCases {
		if got := lfRun(New(lfSecrets(c.secrets...)), c.text); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestLfMasksAtEverySplit(t *testing.T) {
	for _, c := range lfCases {
		for i := 0; i <= len(c.text); i++ {
			if got := lfRun(New(lfSecrets(c.secrets...)), c.text[:i], c.text[i:]); got != c.want {
				t.Errorf("%s: split at %d: got %q, want %q", c.name, i, got, c.want)
			}
			for j := i; j <= len(c.text); j++ {
				if got := lfRun(New(lfSecrets(c.secrets...)), c.text[:i], c.text[i:j], c.text[j:]); got != c.want {
					t.Errorf("%s: splits at %d and %d: got %q, want %q", c.name, i, j, got, c.want)
				}
			}
		}
	}
}

func TestLfRandomSplitsAgreeAndLeakNothing(t *testing.T) {
	secrets := []string{"xy", "xyz", "zx"}
	r := rand.New(rand.NewPCG(7, 11)) //nolint:gosec // G404: a fixed seed makes the cases reproducible
	for n := 0; n < 500; n++ {
		var text strings.Builder
		for k := r.IntN(40); k > 0; k-- {
			text.WriteByte("xyzw"[r.IntN(4)])
		}
		var chunks []string
		rest := text.String()
		for len(rest) > 0 {
			cut := 1 + r.IntN(len(rest))
			chunks = append(chunks, rest[:cut])
			rest = rest[cut:]
		}
		whole := lfRun(New(lfSecrets(secrets...)), text.String())
		split := lfRun(New(lfSecrets(secrets...)), chunks...)
		if split != whole {
			t.Fatalf("text %q in chunks %q: got %q, whole push gives %q", text.String(), chunks, split, whole)
		}
		visible := strings.ReplaceAll(split, Mask, "|")
		for _, s := range secrets {
			if strings.Contains(visible, s) {
				t.Fatalf("text %q in chunks %q: output %q still holds %q", text.String(), chunks, split, s)
			}
		}
	}
}

func TestLfHoldsBackOnlyTheLongestPrefixSuffix(t *testing.T) {
	f := New(lfSecrets("secret"))
	if got := string(f.Push("stdout", []byte("abc se"))); got != "abc " {
		t.Errorf("first push returned %q, want %q", got, "abc ")
	}
	if got := string(f.Push("stdout", []byte("cret ok"))); got != "[redacted] ok" {
		t.Errorf("second push returned %q, want %q", got, "[redacted] ok")
	}

	f = New(lfSecrets("secret"))
	if got := string(f.Push("stdout", []byte("se"))); got != "" {
		t.Errorf("a prefix of the secret was emitted: %q", got)
	}
	if got := string(f.Push("stdout", []byte("x"))); got != "sex" {
		t.Errorf("a false start returned %q, want %q", got, "sex")
	}

	f = New(lfSecrets("ab"))
	if got := string(f.Push("stdout", []byte("aaaa"))); got != "aaa" {
		t.Errorf("repeated first bytes returned %q, want %q", got, "aaa")
	}
	if got := string(f.Flush("stdout")); got != "a" {
		t.Errorf("Flush returned %q, want %q", got, "a")
	}
}

func TestLfStreamsAreIndependent(t *testing.T) {
	f := New(lfSecrets("token"))
	if got := string(f.Push("stdout", []byte("to"))); got != "" {
		t.Errorf("stdout returned %q, want the prefix held", got)
	}
	if got := string(f.Push("stderr", []byte("ken"))); got != "ken" {
		t.Errorf("stderr returned %q, want %q: stdout's held bytes are not stderr's", got, "ken")
	}
	if got := string(f.Flush("stderr")); got != "" {
		t.Errorf("stderr Flush returned %q, want nothing", got)
	}
	if got := string(f.Flush("stdout")); got != "to" {
		t.Errorf("stdout Flush returned %q, want %q", got, "to")
	}
}

func TestLfFlushMasksAHeldSecretAndForgets(t *testing.T) {
	f := New(lfSecrets("abc", "abcdef"))
	if got := string(f.Push("stdout", []byte("xabc"))); got != "x" {
		t.Errorf("Push returned %q, want %q: abc may still grow into abcdef", got, "x")
	}
	if got := string(f.Flush("stdout")); got != "[redacted]" {
		t.Errorf("Flush returned %q, want %q", got, "[redacted]")
	}
	if got := string(f.Flush("stdout")); got != "" {
		t.Errorf("a second Flush returned %q, want nothing", got)
	}
	if got := string(f.Push("stdout", []byte("def"))); got != "def" {
		t.Errorf("after Flush a push returned %q, want the earlier bytes forgotten", got)
	}
}

func TestLfNewCopiesTheSecrets(t *testing.T) {
	s := []byte("hunter2")
	f := New([][]byte{s})
	clear(s)
	if got := string(f.Push("stdout", []byte("pw=hunter2"))); got != "pw=[redacted]" {
		t.Errorf("Push returned %q after the caller cleared its slice", got)
	}
}
