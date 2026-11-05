package events

import (
	"regexp"
	"testing"
)

var fz10EpochPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

func fz10Head(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

// FuzzParseID checks that ParseID gives back the epoch and sequence number FormatID was given when the epoch is
// 16 lower-case hexadecimal characters and refuses it otherwise, and that an id it accepts has such an epoch and
// formats and parses back to the same pair.
func FuzzParseID(f *testing.F) {
	const epoch = "0123456789abcdef"
	f.Add(FormatID(epoch, 1), epoch, uint64(1))
	f.Add(FormatID(epoch, 0), epoch, uint64(0))
	f.Add(FormatID(epoch, 1<<64-1), epoch, uint64(1<<64-1))
	f.Add(epoch+"-007", "0123456789abcde", uint64(7))
	f.Add(epoch+"-18446744073709551616", "0123456789ABCDEF", uint64(2))
	f.Add(epoch+"-1-2", epoch+"-1", uint64(3))
	f.Add("-1", "", uint64(4))
	f.Add(epoch, epoch+"0", uint64(5))
	f.Add("", "-", uint64(6))
	f.Fuzz(func(t *testing.T, id, e string, seq uint64) {
		gotE, gotSeq, ok := ParseID(FormatID(e, seq))
		if want := fz10EpochPattern.MatchString(e); ok != want || (ok && (gotE != e || gotSeq != seq)) {
			t.Fatalf("ParseID(FormatID(%q, %d)) = %q, %d, %v", fz10Head(e), seq, fz10Head(gotE), gotSeq, ok)
		}
		parsedE, parsedSeq, ok := ParseID(id)
		if !ok {
			if parsedE != "" || parsedSeq != 0 {
				t.Fatalf("ParseID(%q) refused the id and returned %q, %d", fz10Head(id), parsedE, parsedSeq)
			}
			return
		}
		if !fz10EpochPattern.MatchString(parsedE) {
			t.Fatalf("ParseID(%q) accepted the epoch %q", fz10Head(id), parsedE)
		}
		if againE, againSeq, ok := ParseID(FormatID(parsedE, parsedSeq)); !ok || againE != parsedE || againSeq != parsedSeq {
			t.Fatalf("ParseID(%q) = %q, %d does not survive FormatID", fz10Head(id), parsedE, parsedSeq)
		}
	})
}
