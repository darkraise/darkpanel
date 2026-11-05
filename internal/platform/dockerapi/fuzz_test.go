package dockerapi

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

// fz5Head returns at most the first 64 bytes of s, for a failure message.
func fz5Head(s string) string { return s[:min(len(s), 64)] }

// FuzzLogStream feeds arbitrary bytes to a log stream, multiplexed or raw:
// Next never panics, no frame is longer than the stream's frame limit or
// carries another stream than stdout or stderr, the frames together hold no
// more bytes than the input, and the stream ends (R74).
func FuzzLogStream(f *testing.F) {
	oversized := make([]byte, 8)
	oversized[0] = 1
	binary.BigEndian.PutUint32(oversized[4:], maxFrame+1)
	f.Add(append(stFrame(1, "out\n"), stFrame(2, "err\n")...), true)
	f.Add([]byte("plain tty output\n"), false)
	f.Add(stFrame(1, "abcdef")[:10], true)
	f.Add(stFrame(1, "abcdef")[:8], true)
	f.Add([]byte{1, 0, 0}, true)
	f.Add(stFrame(7, "x"), true)
	f.Add(oversized, true)
	f.Fuzz(func(t *testing.T, in []byte, multiplexed bool) {
		r := bytes.NewReader(in)
		s := &LogStream{body: io.NopCloser(r), r: bufio.NewReader(r), multiplexed: multiplexed}
		total := 0
		for range len(in) + 1 {
			frame, err := s.Next()
			if err != nil {
				return
			}
			if len(frame.Data) > maxFrame {
				t.Fatalf("frame of %d bytes exceeds the limit for %q", len(frame.Data), fz5Head(string(in)))
			}
			if frame.Stream != "stdout" && frame.Stream != "stderr" {
				t.Fatalf("frame of stream %q for %q", frame.Stream, fz5Head(string(in)))
			}
			if total += len(frame.Data); total > len(in) {
				t.Fatalf("frames hold %d bytes of an input of %d: %q", total, len(in), fz5Head(string(in)))
			}
		}
		t.Fatalf("no end of stream for %q", fz5Head(string(in)))
	})
}

// FuzzCompareVersions feeds two arbitrary strings to compareVersions and
// parseVersion: they never panic, a pair of which one fails to parse fails
// both ways, and a pair that parses compares to opposite signs when swapped
// and to zero when equal (R74).
func FuzzCompareVersions(f *testing.F) {
	for _, pair := range [][2]string{
		{"1.44", "1.49"}, {"1.49", "1.49"}, {"1.50", "1.9"}, {"2.0", "1.99"}, {"1.-0", "1.0"},
		{"+1.+2", "1.2"}, {"1.2.3", "1.2"}, {"", "1.44"}, {"1.", ".1"}, {"99999999999999999999.1", "1.1"},
	} {
		f.Add(pair[0], pair[1])
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		ab, errAB := compareVersions(a, b)
		ba, errBA := compareVersions(b, a)
		if (errAB == nil) != (errBA == nil) {
			t.Fatalf("compareVersions(%q, %q) and its swap disagree on whether both parse: %v, %v", fz5Head(a), fz5Head(b), errAB, errBA)
		}
		if errAB != nil {
			return
		}
		if ab != -ba || ab < -1 || ab > 1 || (a == b && ab != 0) {
			t.Fatalf("compareVersions(%q, %q) = %d, swapped %d", fz5Head(a), fz5Head(b), ab, ba)
		}
	})
}
