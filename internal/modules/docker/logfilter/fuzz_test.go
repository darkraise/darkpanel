package logfilter

import (
	"bytes"
	"testing"
)

// fz6Head returns at most the first 64 bytes of b, for a failure message.
func fz6Head(b []byte) []byte { return b[:min(len(b), 64)] }

// fz6Run pushes the chunks to a new filter of secrets on one stream and
// returns everything it emits, the flush included.
func fz6Run(secrets [][]byte, chunks ...[]byte) []byte {
	f := New(secrets)
	var out []byte
	for _, c := range chunks {
		out = append(out, f.Push("stdout", c)...)
	}
	return append(out, f.Flush("stdout")...)
}

// FuzzFilterChunking feeds arbitrary log bytes and two arbitrary secrets to a
// Filter: the output is the same whether the bytes arrive whole, in three
// chunks or one byte at a time, and no secret appears in it once every Mask
// is replaced by a NUL byte, so Mask's own letters cannot hide or fake a leak;
// a secret holding a NUL is left out of that check (R74).
func FuzzFilterChunking(f *testing.F) {
	f.Add("hunter2", "tok", []byte("pw=hunter2 and tok\n"), uint16(5), uint16(11))
	f.Add("ab", "abc", []byte("ababc abab"), uint16(1), uint16(3))
	f.Add("aab", "b", []byte("aaab aab"), uint16(2), uint16(6))
	f.Add("", "", []byte("nothing to hide"), uint16(0), uint16(0))
	f.Add("QX9", "HUNT3R", []byte("key=QX9 pass=HUNT3R QX9HUNT3R\n"), uint16(4), uint16(17))
	f.Fuzz(func(t *testing.T, s1, s2 string, data []byte, c1, c2 uint16) {
		secrets := [][]byte{[]byte(s1), []byte(s2)}
		whole := fz6Run(secrets, data)
		p1, p2 := int(c1)%(len(data)+1), int(c2)%(len(data)+1)
		if p1 > p2 {
			p1, p2 = p2, p1
		}
		if got := fz6Run(secrets, data[:p1], data[p1:p2], data[p2:]); !bytes.Equal(got, whole) {
			t.Fatalf("output of %q depends on the split at %d and %d", fz6Head(data), p1, p2)
		}
		bytewise := make([][]byte, len(data))
		for i := range data {
			bytewise[i] = data[i : i+1]
		}
		if got := fz6Run(secrets, bytewise...); !bytes.Equal(got, whole) {
			t.Fatalf("output of %q depends on a split into single bytes", fz6Head(data))
		}
		masked := bytes.ReplaceAll(whole, []byte(Mask), []byte{0})
		for _, s := range secrets {
			if len(s) > 0 && bytes.IndexByte(s, 0) < 0 && bytes.Contains(masked, s) {
				t.Fatalf("a secret of %d bytes appears in the output for %q", len(s), fz6Head(data))
			}
		}
	})
}
