package listen

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"sync"
	"syscall"
	"time"
)

// Server limits of every listener (spec section 12).
const (
	ReadHeaderTimeout = 10 * time.Second
	ReadTimeout       = 30 * time.Second
	WriteTimeout      = 60 * time.Second
	IdleTimeout       = 120 * time.Second
)

// ErrClosed is returned by Update after Shutdown.
var ErrClosed = errors.New("listen: the listener set is shut down")

// SetConfig configures a Set.
type SetConfig struct {
	Handler http.Handler
	// TLS serves the HTTPS endpoints; each server takes a clone.
	TLS *tls.Config
	// Bind opens the sockets; nil means Bind.
	Bind BindFunc
	// Log receives one line per bind, skip and serve failure; nil discards.
	Log *slog.Logger
	// OnServeError, when set, receives the error of each server whose Serve
	// stops other than by a shutdown.
	OnServeError func(error)
}

// Bound is one running listener: the endpoint and the address its socket is
// bound to (the real port when the endpoint asked for port 0).
type Bound struct {
	Endpoint Endpoint
	Addr     net.Addr
}

// Set runs Handler on every bound endpoint. HTTPS endpoints terminate TLS in
// the server, so a request has a non-nil Request.TLS exactly when it arrived
// over HTTPS. Its methods are safe for concurrent use.
type Set struct {
	cfg     SetConfig
	mu      sync.Mutex
	servers map[Endpoint]*running
	skipped map[Endpoint]bool
	closed  bool
	wg      sync.WaitGroup
}

type running struct {
	srv *http.Server
	ln  net.Listener
}

// NewSet returns a set with nothing bound.
func NewSet(cfg SetConfig) *Set {
	if cfg.Bind == nil {
		cfg.Bind = Bind
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	return &Set{cfg: cfg, servers: map[Endpoint]*running{}, skipped: map[Endpoint]bool{}}
}

// Update makes want the bound endpoints: it shuts down the servers of
// endpoints not in want (gracefully until ctx ends, then forcibly) and binds
// those not yet bound, leaving unchanged ones untouched. An optional endpoint
// whose address the host lacks is skipped once with a log line and never
// retried. It returns every other bind failure joined; the endpoints that did
// bind keep serving, and a later Update retries the failed ones.
func (s *Set) Update(ctx context.Context, want []Endpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	for e, r := range s.servers {
		if !slices.Contains(want, e) {
			if err := r.stop(ctx); err != nil {
				s.cfg.Log.Warn("listen: stopped, cutting off running requests", "url", e.URL(), "err", err)
			} else {
				s.cfg.Log.Info("listen: stopped", "url", e.URL())
			}
			delete(s.servers, e)
		}
	}
	var errs []error
	for _, e := range want {
		if s.servers[e] != nil || s.skipped[e] {
			continue
		}
		if e.TLS && s.cfg.TLS == nil {
			errs = append(errs, fmt.Errorf("listen: %s needs a TLS configuration", e.URL()))
			continue
		}
		ln, err := s.cfg.Bind(ctx, e)
		if err != nil {
			if e.Optional && addrUnavailable(err) {
				s.skipped[e] = true
				s.cfg.Log.Info("listen: skipped: this host lacks the address", "url", e.URL(), "err", err)
				continue
			}
			errs = append(errs, err)
			continue
		}
		s.servers[e] = s.serve(e, ln)
		s.cfg.Log.Info("listen: serving", "url", e.URL(), "addr", ln.Addr().String())
	}
	return errors.Join(errs...)
}

func addrUnavailable(err error) bool {
	return errors.Is(err, syscall.EADDRNOTAVAIL) || errors.Is(err, syscall.EAFNOSUPPORT)
}

func (s *Set) serve(e Endpoint, ln net.Listener) *running {
	srv := &http.Server{
		Handler:           s.cfg.Handler,
		ReadHeaderTimeout: ReadHeaderTimeout,
		ReadTimeout:       ReadTimeout,
		WriteTimeout:      WriteTimeout,
		IdleTimeout:       IdleTimeout,
		ErrorLog:          slog.NewLogLogger(s.cfg.Log.Handler(), slog.LevelWarn),
	}
	if e.TLS {
		srv.TLSConfig = s.cfg.TLS.Clone()
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		var err error
		if e.TLS {
			err = srv.ServeTLS(ln, "", "")
		} else {
			err = srv.Serve(ln)
		}
		if !errors.Is(err, http.ErrServerClosed) {
			s.cfg.Log.Error("listen: serving stopped", "url", e.URL(), "err", err)
			if s.cfg.OnServeError != nil {
				s.cfg.OnServeError(fmt.Errorf("listen: serving %s stopped: %w", e.URL(), err))
			}
		}
	}()
	return &running{srv: srv, ln: ln}
}

// stop shuts the server down and closes its socket even when Serve has not
// started yet, so the address is free when stop returns. It returns ctx's
// error when running requests had to be cut off.
func (r *running) stop(ctx context.Context) error {
	err := r.srv.Shutdown(ctx)
	if err != nil {
		_ = r.srv.Close()
	}
	_ = r.ln.Close()
	return err
}

// Bound returns the running listeners sorted by endpoint address.
func (s *Set) Bound() []Bound {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Bound, 0, len(s.servers))
	for e, r := range s.servers {
		out = append(out, Bound{Endpoint: e, Addr: r.ln.Addr()})
	}
	slices.SortFunc(out, func(a, b Bound) int { return a.Endpoint.Addr.Compare(b.Endpoint.Addr) })
	return out
}

// Shutdown stops every server, letting running requests finish until ctx
// ends, and waits for the serving goroutines. Later Updates fail with
// ErrClosed. It returns ctx's error when requests had to be cut off.
func (s *Set) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	servers := s.servers
	s.servers = map[Endpoint]*running{}
	s.mu.Unlock()
	errs := make([]error, 0, len(servers))
	var mu sync.Mutex
	var stops sync.WaitGroup
	for _, r := range servers {
		stops.Go(func() {
			if err := r.stop(ctx); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		})
	}
	stops.Wait()
	s.wg.Wait()
	if len(errs) > 0 {
		return fmt.Errorf("listen: shutdown: %w", errs[0])
	}
	return nil
}
