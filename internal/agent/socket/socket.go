// Package socket is the agent's break-glass socket (spec section 5, R31):
// a Unix socket in a directory only root can enter, serving the agent API
// to local root alone, whether or not the controller is reachable.
package socket

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// Config is what Listen needs.
type Config struct {
	Path    string       // Paths.AgentSocket
	Handler http.Handler // api.NewSocketHandler over the agent runtime
	Log     *slog.Logger // nil discards
	// Allow decides whether a peer whose SO_PEERCRED uid is uid may use the
	// socket; nil admits uid 0 alone. Tests that do not run as root replace
	// it.
	Allow func(uid uint32) bool
}

// Server serves the agent API on the socket.
type Server struct {
	srv *http.Server
	ln  *peerListener
}

// Listen creates the socket's directory with mode 0700 (narrowing an
// existing one), replaces a socket an earlier agent left behind, and listens
// with mode 0600. The caller holds the agent lock, so no other agent serves
// the old socket.
func Listen(cfg Config) (*Server, error) {
	if cfg.Allow == nil {
		cfg.Allow = rootOnly
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	dir := filepath.Dir(cfg.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("socket: create %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // G302: a directory needs its search bit
		return nil, fmt.Errorf("socket: %w", err)
	}
	st, err := os.Lstat(cfg.Path)
	switch {
	case err == nil && st.Mode().Type() != fs.ModeSocket:
		return nil, fmt.Errorf("socket: %s exists and is not a socket", cfg.Path)
	case err == nil:
		if err := os.Remove(cfg.Path); err != nil {
			return nil, fmt.Errorf("socket: remove the old socket: %w", err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("socket: %w", err)
	}
	ln, err := net.Listen("unix", cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("socket: %w", err)
	}
	// The directory already keeps everyone but root out; the mode keeps the
	// socket private should the directory ever be shared.
	if err := os.Chmod(cfg.Path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("socket: %w", err)
	}
	return &Server{
		srv: &http.Server{Handler: cfg.Handler, ReadHeaderTimeout: 10 * time.Second, ErrorLog: slog.NewLogLogger(cfg.Log.Handler(), slog.LevelWarn)},
		ln:  &peerListener{Listener: ln, allow: cfg.Allow, log: cfg.Log},
	}, nil
}

// Serve serves until Close and then returns nil.
func (s *Server) Serve() error {
	if err := s.srv.Serve(s.ln); !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("socket: %w", err)
	}
	return nil
}

// Close stops serving, closes every connection and removes the socket.
func (s *Server) Close() error {
	return s.srv.Close()
}

func rootOnly(uid uint32) bool { return uid == 0 }

// peerListener hands over only connections whose peer Allow admits; it
// closes the others before a byte is read from them.
type peerListener struct {
	net.Listener
	allow func(uint32) bool
	log   *slog.Logger
}

func (l *peerListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		uid, err := peerUID(c)
		switch {
		case err != nil:
			l.log.Warn("socket: refused a peer whose credentials cannot be read", "error", err)
		case !l.allow(uid):
			l.log.Warn("socket: refused a peer that is not root", "uid", uid)
		default:
			return c, nil
		}
		_ = c.Close()
	}
}

// peerUID is the uid the kernel recorded for the peer of c when it
// connected (SO_PEERCRED).
func peerUID(c net.Conn) (uint32, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("socket: %T is not a Unix connection", c)
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if credErr != nil {
		return 0, credErr
	}
	return cred.Uid, nil
}
