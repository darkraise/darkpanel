package wire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	for _, typ := range []byte{FrameData, FrameResize, FrameExit, FrameClose} {
		for _, n := range []int{0, 1, MaxFramePayload} {
			p := bytes.Repeat([]byte{'a' + typ}, n)
			var buf bytes.Buffer
			if err := WriteFrame(&buf, typ, p); err != nil {
				t.Fatalf("WriteFrame(%d, %d bytes): %v", typ, n, err)
			}
			if buf.Len() != 5+n {
				t.Errorf("frame of %d bytes takes %d bytes, want %d", n, buf.Len(), 5+n)
			}
			gotT, gotP, err := ReadFrame(&buf)
			if err != nil || gotT != typ || !bytes.Equal(gotP, p) {
				t.Errorf("ReadFrame = %d, %d bytes, %v; want %d, %d bytes", gotT, len(gotP), err, typ, n)
			}
		}
	}
	for _, tc := range []struct {
		name string
		typ  byte
		n    int
	}{{"too large", FrameData, MaxFramePayload + 1}, {"unknown type", 9, 1}, {"type 0", 0, 1}} {
		if err := WriteFrame(io.Discard, tc.typ, make([]byte, tc.n)); !errors.Is(err, ErrFrame) {
			t.Errorf("WriteFrame %s = %v, want ErrFrame", tc.name, err)
		}
		hdr := make([]byte, 5, 5+tc.n)
		hdr[0] = tc.typ
		binary.BigEndian.PutUint32(hdr[1:], uint32(tc.n)) //nolint:gosec // G115: small test lengths
		if _, _, err := ReadFrame(bytes.NewReader(append(hdr, make([]byte, tc.n)...))); !errors.Is(err, ErrFrame) {
			t.Errorf("ReadFrame %s = %v, want ErrFrame", tc.name, err)
		}
	}
	hdr := []byte{FrameData, 0xff, 0xff, 0xff, 0xff}
	if _, _, err := ReadFrame(bytes.NewReader(hdr)); !errors.Is(err, ErrFrame) {
		t.Errorf("ReadFrame of a 4 GiB length = %v, want ErrFrame", err)
	}
	if _, _, err := ReadFrame(bytes.NewReader([]byte{FrameData, 0, 0, 0, 3, 'a'})); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("ReadFrame of a cut frame = %v, want io.ErrUnexpectedEOF", err)
	}
	if _, _, err := ReadFrame(bytes.NewReader([]byte{FrameData, 0, 0, 0, 3})); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("ReadFrame of a header without its payload = %v, want io.ErrUnexpectedEOF", err)
	}
	if _, _, err := ReadFrame(bytes.NewReader(nil)); !errors.Is(err, io.EOF) {
		t.Errorf("ReadFrame at the end = %v, want io.EOF", err)
	}
}
