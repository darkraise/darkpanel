package api

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// SocketSource is the Principal.Source of every controller socket request.
const SocketSource = "socket"

// The controller socket server's timeouts, the listeners' values of spec
// section 12.
const (
	socketReadHeaderTimeout = 10 * time.Second
	socketReadTimeout       = 30 * time.Second
	socketWriteTimeout      = 60 * time.Second
	socketIdleTimeout       = 120 * time.Second
)

// getuid is the service's own uid; tests replace it.
var getuid = os.Getuid

// PeerUIDFunc returns the uid of the process at the other end of c.
type PeerUIDFunc func(c net.Conn) (uint32, error)

// SocketPeerUID reads the peer's uid of a unix connection with SO_PEERCRED.
func SocketPeerUID(c net.Conn) (uint32, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("api: peer credentials of a %T", c)
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("api: peer credentials: %w", err)
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, fmt.Errorf("api: peer credentials: %w", err)
	}
	if credErr != nil {
		return 0, fmt.Errorf("api: peer credentials: %w", credErr)
	}
	return cred.Uid, nil
}

// maxSocketPath is the longest socket path Linux accepts: sun_path holds 108
// bytes including the terminating NUL.
const maxSocketPath = len(unix.RawSockaddrUnix{}.Path) - 1

// ListenSocket listens on the controller socket at path with mode 0600. It
// replaces a stale socket file, but refuses a path that is not a socket or
// that another process still serves. A socket is stale only when connecting
// to it is refused; any other probe failure, such as a timeout or a full
// backlog, may come from a live controller, so the file is kept and the
// error returned. Until the chmod the file has the umask's mode; the peer
// check on every connection covers that window.
// A path longer than maxSocketPath is refused before anything is touched.
func ListenSocket(path string) (net.Listener, error) {
	if len(path) > maxSocketPath {
		return nil, fmt.Errorf("api: the socket path %s is %d bytes, and a Unix socket path holds at most %d bytes; "+
			"use a shorter runtime directory (XDG_RUNTIME_DIR with --dev)", path, len(path), maxSocketPath)
	}
	if st, err := os.Lstat(path); err == nil {
		if st.Mode().Type() != fs.ModeSocket {
			return nil, fmt.Errorf("api: %s exists and is not a socket", path)
		}
		c, err := net.DialTimeout("unix", path, time.Second)
		if err == nil {
			_ = c.Close()
			return nil, fmt.Errorf("api: %s is served by another process", path)
		}
		if !errors.Is(err, unix.ECONNREFUSED) {
			return nil, fmt.Errorf("api: probe %s: %w", path, err)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("api: remove stale socket: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("api: %w", err)
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = l.Close()
		return nil, fmt.Errorf("api: %w", err)
	}
	return l, nil
}

// socketAllowed reports whether a peer uid may use the controller socket:
// root always, and the service's own uid only in dev mode (ADR 0011).
func (s *Server) socketAllowed(uid uint32) bool {
	return uid == 0 || (s.cfg.Dev && int64(uid) == int64(getuid()))
}

// socketPrincipal is the actor of a socket peer (ADR 0011). The peer
// credentials are its authentication, so it carries the admin scope.
func (s *Server) socketPrincipal(uid uint32) core.Principal {
	actor := "local:root@" + s.cfg.HostID
	if uid != 0 {
		actor = fmt.Sprintf("local:uid-%d@%s", uid, s.cfg.HostID)
	}
	return core.Principal{Actor: actor, Scope: core.ScopeAdmin, Source: SocketSource}
}

// peerListener accepts only connections from allowed peers.
type peerListener struct {
	net.Listener
	s       *Server
	peerUID PeerUIDFunc
}

type peerConn struct {
	net.Conn
	uid uint32
}

// Accept returns the next connection whose peer is allowed, closing the
// others.
func (l *peerListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		uid, err := l.peerUID(c)
		if err != nil || !l.s.socketAllowed(uid) {
			l.s.log.Warn("api: refused a controller socket connection", "uid", uid, "err", err)
			_ = c.Close()
			continue
		}
		return &peerConn{Conn: c, uid: uid}, nil
	}
}

// SocketListener wraps l so that Accept returns only connections from uid 0
// or, in dev mode, the service's own uid. A nil peerUID means SocketPeerUID.
func (s *Server) SocketListener(l net.Listener, peerUID PeerUIDFunc) net.Listener {
	if peerUID == nil {
		peerUID = SocketPeerUID
	}
	return &peerListener{Listener: l, s: s, peerUID: peerUID}
}

type peerUIDKey struct{}

func withPeerUID(ctx context.Context, uid uint32) context.Context {
	return context.WithValue(ctx, peerUIDKey{}, uid)
}

func peerUIDFrom(ctx context.Context) (uint32, bool) {
	uid, ok := ctx.Value(peerUIDKey{}).(uint32)
	return uid, ok
}

// SocketServer returns the controller socket's server: SocketHandler with
// the listeners' timeouts and a ConnContext that attaches each connection's
// peer uid. Serve it on SocketListener's result.
func (s *Server) SocketServer() *http.Server {
	return &http.Server{
		Handler:           s.SocketHandler(),
		ReadHeaderTimeout: socketReadHeaderTimeout,
		ReadTimeout:       socketReadTimeout,
		WriteTimeout:      socketWriteTimeout,
		IdleTimeout:       socketIdleTimeout,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			if pc, ok := c.(*peerConn); ok {
				return withPeerUID(ctx, pc.uid)
			}
			return ctx
		},
	}
}

// SocketHandler serves the controller socket's own mux (spec section 14):
// GET /healthz with full detail, GET /api/v1/controller/web-ca, and plan,
// apply and read of break-glass actions. No session, token or CSRF applies;
// the peer uid is the authentication, and a request without an allowed one
// is refused.
func (s *Server) SocketHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+healthzPath, s.writeHealthDetail)
	mux.HandleFunc("GET /api/v1/controller/web-ca", s.handleWebCA)
	for _, op := range []string{opPlan, opApply, opRead} {
		mux.HandleFunc("POST /api/v1/actions/{name}/{version}/"+op, s.socketAction(op))
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { s.unrouted(w, r, mux, false) })
	return securityHeaders(recoverPanics(s.log, s.socketPeer(mux)))
}

// socketPeer puts the socket principal of the request's peer in its context.
func (s *Server) socketPeer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, ok := peerUIDFrom(r.Context())
		if !ok || !s.socketAllowed(uid) {
			writeProblem(w, s.log, errcode.New(errcode.Forbidden, "the controller socket accepts only root or the service's own user"))
			return
		}
		next.ServeHTTP(w, r.WithContext(withCaller(r.Context(), caller{principal: s.socketPrincipal(uid)})))
	})
}

type webCAResponse struct {
	PEM         string `json:"pem"`
	Fingerprint string `json:"fingerprint"`
}

func (s *Server) handleWebCA(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.log, http.StatusOK, webCAResponse{PEM: string(s.cfg.WebCA.RootPEM()), Fingerprint: s.cfg.WebCA.Fingerprint()})
}

// socketAction serves op of a break-glass action; the socket serves no other
// action.
func (s *Server) socketAction(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, _ := callerFrom(r.Context())
		def, err := s.lookupAction(r.PathValue("name"), r.PathValue("version"), core.SideController, op)
		if err == nil && !def.Spec.BreakGlass {
			err = errcode.New(errcode.Forbidden, "the controller socket serves only break-glass actions")
		}
		if err == nil {
			err = authorize(c.principal, core.RequiredScope(def.Spec))
		}
		if err != nil {
			writeProblem(w, s.log, err)
			return
		}
		s.runAction(w, r, c.principal, "", def, op)
	}
}
