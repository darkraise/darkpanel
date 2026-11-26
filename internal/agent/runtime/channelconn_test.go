package runtime

import (
	"errors"
	"io"
	"net"
	"testing"

	"github.com/darkraise/darkpanel/internal/agent/wire"
)

// TestChannelConnClosed refuses reads and writes once the channel is
// closed, so no frame follows the close frame.
func TestChannelConnClosed(t *testing.T) {
	agentEnd, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	c := &channelConn{rw: agentEnd, cancel: func(error) {}, accepted: true}
	got := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(peer)
		got <- b
	}()
	if err := c.Close("ended"); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteFrame(wire.FrameData, []byte("late")); !errors.Is(err, errChannelClosed) {
		t.Errorf("WriteFrame after Close = %v, want errChannelClosed", err)
	}
	if _, _, err := c.ReadFrame(); !errors.Is(err, errChannelClosed) {
		t.Errorf("ReadFrame after Close = %v, want errChannelClosed", err)
	}
	if want := "\x04\x00\x00\x00\x12{\"reason\":\"ended\"}"; string(<-got) != want {
		t.Error("the peer did not get exactly the close frame")
	}
}
