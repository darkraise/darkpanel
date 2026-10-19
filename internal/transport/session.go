package transport

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

// Stream limits per session (spec section 4).
const (
	MaxControllerStreams = 32 // concurrent streams the controller opens on one session
	MaxAgentStreams      = 4  // concurrent streams the agent opens on one session
)

// Session is the yamux session over one authenticated agent connection.
// The controller opens streams to the agent API; the agent opens streams to
// the controller only to renew its certificate. A stream holds its slot
// until both ends have closed it, so an HTTP client's idle pooled
// connections count.
// Session is a net.Listener for the streams the peer opens, so
// http.Server.Serve can serve them.
type Session struct {
	mux     *yamux.Session
	opens   chan struct{} // one token per stream this end opened
	accepts chan struct{} // one token per stream this end accepted
}

var _ net.Listener = (*Session)(nil)

// ControllerSession is the yamux server end of conn: Open is limited to
// MaxControllerStreams, Accept to MaxAgentStreams.
func ControllerSession(conn net.Conn) (*Session, error) {
	mux, err := yamux.Server(conn, muxConfig())
	if err != nil {
		return nil, fmt.Errorf("transport: controller session: %w", err)
	}
	return newSession(mux, MaxControllerStreams, MaxAgentStreams), nil
}

// AgentSession is the yamux client end of conn: Open is limited to
// MaxAgentStreams, Accept to MaxControllerStreams.
func AgentSession(conn net.Conn) (*Session, error) {
	mux, err := yamux.Client(conn, muxConfig())
	if err != nil {
		return nil, fmt.Errorf("transport: agent session: %w", err)
	}
	return newSession(mux, MaxAgentStreams, MaxControllerStreams), nil
}

func newSession(mux *yamux.Session, opens, accepts int) *Session {
	return &Session{mux: mux, opens: make(chan struct{}, opens), accepts: make(chan struct{}, accepts)}
}

func muxConfig() *yamux.Config {
	c := yamux.DefaultConfig()
	c.AcceptBacklog = 64
	// The controller's heartbeat decides when a session is dead.
	c.EnableKeepAlive = false
	// yamux bounds a ping by the write timeout.
	c.ConnectionWriteTimeout = 10 * time.Second
	c.StreamOpenTimeout = 10 * time.Second
	c.StreamCloseTimeout = 30 * time.Second
	c.LogOutput = io.Discard
	return c
}

// Open waits for a free slot until ctx ends, then opens a stream; the slot
// frees when the stream is closed at both ends.
func (s *Session) Open(ctx context.Context) (net.Conn, error) {
	select {
	case s.opens <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("transport: open stream: %w", ctx.Err())
	case <-s.mux.CloseChan():
		return nil, fmt.Errorf("transport: open stream: %w", net.ErrClosed)
	}
	st, err := s.mux.OpenStream()
	if err != nil {
		<-s.opens
		return nil, fmt.Errorf("transport: open stream: %w", err)
	}
	return &stream{Stream: st, release: func() { <-s.opens }}, nil
}

// Accept waits for a free slot, then returns the next stream the peer
// opened. Streams beyond the limit wait in yamux's accept backlog of 64,
// and yamux resets any stream beyond that. Once the session is closed it
// fails with an error wrapping net.ErrClosed.
func (s *Session) Accept() (net.Conn, error) {
	select {
	case s.accepts <- struct{}{}:
	case <-s.mux.CloseChan():
		return nil, fmt.Errorf("transport: accept stream: %w", net.ErrClosed)
	}
	st, err := s.mux.AcceptStream()
	if err != nil {
		<-s.accepts
		if s.mux.IsClosed() {
			err = net.ErrClosed
		}
		return nil, fmt.Errorf("transport: accept stream: %w", err)
	}
	return &stream{Stream: st, release: func() { <-s.accepts }}, nil
}

// Addr is the local address of the session's connection.
func (s *Session) Addr() net.Addr { return s.mux.Addr() }

// Close ends the session, its streams and its connection.
func (s *Session) Close() error { return s.mux.Close() }

// Done is closed when the session ends, from either side.
func (s *Session) Done() <-chan struct{} { return s.mux.CloseChan() }

// Ping measures one round trip; yamux fails it after the 10 s write
// timeout.
func (s *Session) Ping() (time.Duration, error) { return s.mux.Ping() }

// stream returns its slot once it is closed at both ends.
type stream struct {
	*yamux.Stream
	once    sync.Once
	release func()
}

func (st *stream) Close() error {
	err := st.Stream.Close()
	st.once.Do(func() { go st.releaseWhenClosed() })
	return err
}

// releaseWhenClosed frees the slot when the read side ends: the peer closed
// its half, reset the stream, or yamux gave up on it StreamCloseTimeout
// after the local close. Freeing it at the local close would let a peer
// that keeps its halves open cycle more streams than the limit.
func (st *stream) releaseWhenClosed() {
	_ = st.SetReadDeadline(time.Time{})
	_, _ = io.Copy(io.Discard, st.Stream)
	st.release()
}
