package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/transport"
)

// DialTimeout bounds one connection attempt: the dial, the handshake and the
// ping that proves the controller accepted the credentials.
const DialTimeout = 30 * time.Second

// ErrRefused wraps connect's error when the controller refused the agent's
// certificate.
var ErrRefused = errors.New("daemon: the controller refused this agent's credentials")

// Agent is install.Agent over the credentials in TLSDir (Paths.AgentTLSDir).
type Agent struct {
	TLSDir string
}

var _ install.Agent = Agent{}

// Check is darkpanel agent check.
func (a Agent) Check(ctx context.Context) (install.CheckResult, error) {
	return Check(ctx, a.TLSDir)
}

// Enroll consumes token and saves a new credential generation.
func (a Agent) Enroll(ctx context.Context, token string) error {
	_, err := Enroll(ctx, a.TLSDir, token)
	return err
}

// Check is darkpanel agent check: one mTLS handshake with
// the current credentials of dir. Without credentials it answers
// CheckRefused, because only a new enrollment helps; credentials that cannot
// be read answer CheckRefused with the error. A controller that is not the
// pinned one, or no answer within DialTimeout, is CheckUnreachable. The
// controller treats the check as the host's session, so it replaces a
// running daemon's session, which redials.
func Check(ctx context.Context, dir string) (install.CheckResult, error) {
	c, err := LoadCredentials(dir)
	if errors.Is(err, ErrNotEnrolled) {
		return install.CheckRefused, nil
	}
	if err != nil {
		return install.CheckRefused, err
	}
	ctx, cancel := context.WithTimeout(ctx, DialTimeout)
	defer cancel()
	sess, err := connect(ctx, c)
	switch {
	case err == nil:
		_ = sess.Close()
		return install.CheckAccepted, nil
	case errors.Is(err, ErrRefused):
		return install.CheckRefused, nil
	default:
		return install.CheckUnreachable, nil
	}
}

// connect dials the controller with c and returns a session the controller
// accepted. In TLS 1.3 the client's handshake completes before the
// controller has checked the client certificate, so a refusal only arrives
// as an alert on the first read; the ping waits for either. A refusal wraps
// ErrRefused.
func connect(ctx context.Context, c *Credentials) (*transport.Session, error) {
	conn, err := transport.Dial(ctx, c.Controller.Address, transport.AgentConfig(c.TLSCertificate(), c.CA, c.Controller.SAN))
	if err != nil {
		if isRefusal(err) {
			return nil, fmt.Errorf("%w: %w", ErrRefused, err)
		}
		return nil, err
	}
	rc := &readErrConn{Conn: conn}
	sess, err := transport.AgentSession(rc)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	// Buffered, so the ping's goroutine ends even when nobody waits for it.
	pinged := make(chan error, 1)
	go func() {
		_, err := sess.Ping()
		pinged <- err
	}()
	select {
	case err = <-pinged:
	case <-ctx.Done():
		_ = sess.Close()
		return nil, fmt.Errorf("daemon: ping the controller: %w", ctx.Err())
	}
	if err == nil {
		return sess, nil
	}
	_ = sess.Close()
	if readErr := rc.err(); isRefusal(readErr) {
		return nil, fmt.Errorf("%w: %w", ErrRefused, readErr)
	}
	return nil, fmt.Errorf("daemon: ping the controller: %w", err)
}

// isRefusal reports whether err is a TLS alert the controller sent.
func isRefusal(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "remote error"
}

// readErrConn keeps the first read error, which the yamux session hides
// behind its own shutdown error.
type readErrConn struct {
	net.Conn
	mu    sync.Mutex
	first error
}

func (c *readErrConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if err != nil {
		c.mu.Lock()
		if c.first == nil {
			c.first = err
		}
		c.mu.Unlock()
	}
	return n, err
}

func (c *readErrConn) err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.first
}
