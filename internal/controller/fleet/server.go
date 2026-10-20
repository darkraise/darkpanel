package fleet

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/transport"
)

// HandshakeTimeout bounds the TLS handshake of each accepted connection, so
// a client that connects and stays silent holds nothing for long.
const HandshakeTimeout = 10 * time.Second

// EnrollTimeout bounds an enrollment connection from the end of its
// handshake to the end of its answer.
const EnrollTimeout = 30 * time.Second

// Limits of the HTTP server on the streams an agent opens (renewal only).
const (
	streamHeaderTimeout = 10 * time.Second
	streamReadTimeout   = 30 * time.Second
	streamWriteTimeout  = 30 * time.Second
)

// ServerConfig configures NewServer.
type ServerConfig struct {
	Authority *Authority
	Registry  *Registry
	// Limiter admits the requests of enrollment connections.
	Limiter *EnrollLimiter
	// Log receives refused handshakes and failed requests; nil discards.
	Log *slog.Logger
	// HandshakeTimeout bounds each TLS handshake; zero means
	// HandshakeTimeout.
	HandshakeTimeout time.Duration
	// EnrollTimeout bounds each enrollment request; zero means
	// EnrollTimeout.
	EnrollTimeout time.Duration
}

// Server serves the agent listener's connections (spec section 4): each one
// is a TLS handshake whose ALPN picks the protocol. An agent connection
// becomes the host's session in the registry and serves the agent's
// certificate renewals; an enrollment connection serves one enrollment
// request and closes. Its methods are safe for concurrent use.
type Server struct {
	cfg    ServerConfig
	tls    transport.ServerConfig
	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	closed bool
	wg     sync.WaitGroup
}

// NewServer returns a server over cfg's authority and registry.
func NewServer(cfg ServerConfig) (*Server, error) {
	switch {
	case cfg.Authority == nil || cfg.Registry == nil || cfg.Limiter == nil:
		return nil, errors.New("fleet: the server needs an authority, a registry and an enrollment limiter")
	case cfg.HandshakeTimeout < 0 || cfg.EnrollTimeout < 0:
		return nil, errors.New("fleet: the server's timeouts must be zero or positive")
	}
	if cfg.HandshakeTimeout == 0 {
		cfg.HandshakeTimeout = HandshakeTimeout
	}
	if cfg.EnrollTimeout == 0 {
		cfg.EnrollTimeout = EnrollTimeout
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := cfg.Authority
	return &Server{
		cfg: cfg,
		// Every handshake builds its own tls.Config from this, so no session
		// ticket key is ever shared between connections.
		tls:    transport.ServerConfig{Certificate: a.GetCertificate, AgentRoots: a.Pool(), CheckPeer: a.CheckPeer},
		ctx:    ctx,
		cancel: cancel,
		conns:  map[net.Conn]struct{}{},
	}, nil
}

// Serve accepts connections on ln until ln is closed, then returns nil. A
// failed Accept is retried after a pause that grows to one second. After
// Close it closes every connection it accepts.
func (s *Server) Serve(ln net.Listener) error {
	var delay time.Duration
	for {
		raw, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			delay = min(max(2*delay, 5*time.Millisecond), time.Second)
			s.cfg.Log.Warn("fleet: accept failed; retrying", "addr", ln.Addr().String(), "err", err, "in", delay)
			time.Sleep(delay)
			continue
		}
		delay = 0
		if !s.track(raw) {
			_ = raw.Close()
			continue
		}
		go s.handle(raw)
	}
}

// Close closes every connection, which ends every agent session, and
// waits for their handlers. It does not close the listeners.
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	conns := s.conns
	s.conns = map[net.Conn]struct{}{}
	s.mu.Unlock()
	s.cancel()
	for c := range conns {
		_ = c.Close()
	}
	s.wg.Wait()
}

func (s *Server) track(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.conns[c] = struct{}{}
	s.wg.Add(1)
	return true
}

func (s *Server) untrack(c net.Conn) {
	_ = c.Close()
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
	s.wg.Done()
}

func (s *Server) handle(raw net.Conn) {
	defer s.untrack(raw)
	ctx, cancel := context.WithTimeout(s.ctx, s.cfg.HandshakeTimeout)
	peer, err := transport.Handshake(ctx, raw, s.tls)
	cancel()
	if err != nil {
		s.cfg.Log.Debug("fleet: handshake refused", "remote", raw.RemoteAddr().String(), "err", err)
		return
	}
	defer func() { _ = peer.Conn.Close() }()
	switch peer.Protocol {
	case transport.ALPNAgent:
		s.serveAgent(peer)
	case transport.ALPNEnroll:
		s.serveEnroll(peer)
	}
}

// serveAgent makes an agent connection its host's session and serves the
// streams the agent opens until the session ends.
func (s *Server) serveAgent(peer *transport.Peer) {
	log := s.cfg.Log.With("host", peer.HostID)
	if err := s.cfg.Authority.Handshake(s.ctx, peer.HostID, peer.Serial); err != nil {
		log.Error("fleet: cannot record the agent's handshake", "err", err)
		return
	}
	session, err := transport.ControllerSession(peer.Conn)
	if err != nil {
		log.Warn("fleet: cannot start the agent's session", "err", err)
		return
	}
	if err := s.cfg.Registry.Register(s.ctx, peer.HostID, peer.Serial, session); err != nil {
		log.Info("fleet: agent session refused", "err", err)
		return
	}
	srv := &http.Server{
		Handler:           s.counted(s.renewHandler(peer.HostID, log)),
		ReadHeaderTimeout: streamHeaderTimeout,
		ReadTimeout:       streamReadTimeout,
		WriteTimeout:      streamWriteTimeout,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		BaseContext:       func(net.Listener) context.Context { return s.ctx },
	}
	// Serve returns once the session ends, whoever ended it.
	_ = srv.Serve(session)
	_ = srv.Close()
}

// counted makes Close wait for h's requests: http.Server.Close does not wait
// for its handlers. The Add happens under s.mu while closed is false, so it
// cannot race Close's Wait. A request after Close is answered 503
// unavailable, because an empty answer would read as 200 and the agent could
// not tell it from a broken one.
func (s *Server) counted(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			writeProblem(w, s.cfg.Log, errcode.New(errcode.Unavailable, "the controller is shutting down"))
			return
		}
		s.wg.Add(1)
		s.mu.Unlock()
		defer s.wg.Done()
		h.ServeHTTP(w, r)
	})
}

// renewHandler serves POST transport.RenewPath for hostID, the host the
// session's certificate names; the request names no host of its own.
func (s *Server) renewHandler(hostID string, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != transport.RenewPath {
			writeProblem(w, log, errcode.New(errcode.NotFound, "the controller serves only "+transport.RenewPath+" to agents"))
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeProblem(w, log, errcode.New(errcode.MethodNotAllowed, transport.RenewPath+" takes POST"))
			return
		}
		var req transport.RenewRequest
		if err := decodeBody(w, r, &req); err != nil {
			writeProblem(w, log, err)
			return
		}
		cert, err := s.cfg.Authority.Renew(r.Context(), hostID, []byte(req.CSR))
		if err != nil {
			writeProblem(w, log, err)
			return
		}
		log.Info("fleet: renewed the agent's certificate")
		writeJSON(w, log, http.StatusOK, transport.RenewResponse{Certificate: string(cert)})
	})
}
