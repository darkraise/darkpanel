package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// The frame types of a module channel (spec 1.6): data carries the
// channel's bytes, resize {"cols":u16,"rows":u16}, exit {"code":int} and
// close {"reason":string}.
const (
	FrameData   byte = 1
	FrameResize byte = 2
	FrameExit   byte = 3
	FrameClose  byte = 4
)

// MaxFramePayload is the largest payload of one frame.
const MaxFramePayload = 32 << 10

// ChannelUpgrade is the Upgrade header value of a channel request and its
// 101 answer.
const ChannelUpgrade = "darkpanel-channel/1"

// ErrFrame is a frame with an unknown type or a payload above
// MaxFramePayload. A channel that reads one is closed: what follows cannot
// be framed.
var ErrFrame = errors.New("wire: bad frame")

const frameHeader = 5 // one type byte and a big-endian uint32 length

func validFrame(t byte, n int) bool {
	return t >= FrameData && t <= FrameClose && n <= MaxFramePayload
}

// WriteFrame writes one frame of type t with payload p in a single Write.
func WriteFrame(w io.Writer, t byte, p []byte) error {
	if !validFrame(t, len(p)) {
		return fmt.Errorf("%w: type %d with %d bytes", ErrFrame, t, len(p))
	}
	buf := make([]byte, frameHeader+len(p))
	buf[0] = t
	binary.BigEndian.PutUint32(buf[1:frameHeader], uint32(len(p))) //nolint:gosec // G115: validFrame bounds the length
	copy(buf[frameHeader:], p)
	_, err := w.Write(buf)
	return err
}

// ReadFrame reads one frame. A frame validFrame refuses is ErrFrame and its
// payload is not read; a stream that ends inside a frame is
// io.ErrUnexpectedEOF, and one that ends between frames io.EOF.
func ReadFrame(r io.Reader) (byte, []byte, error) {
	var h [frameHeader]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(h[1:])
	if n > MaxFramePayload || !validFrame(h[0], int(n)) {
		return 0, nil, fmt.Errorf("%w: type %d with %d bytes", ErrFrame, h[0], n)
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(r, p); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return 0, nil, err
	}
	return h[0], p, nil
}
