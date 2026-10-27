package listen

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// RevokeGrace bounds how long a revoked listener lets its running responses
// finish before it cuts the connections. A response cannot outlast the
// write timeout anyway; streams end as soon as the listener is revoked.
const RevokeGrace = WriteTimeout

var errRuntimeShutDown = errors.New("listen: the runtime listeners are shut down")

// RuntimeConfig configures NewRuntime. DB and Clock are required.
type RuntimeConfig struct {
	DB    *store.DB
	Clock core.Clock
	Log   *slog.Logger // nil discards
	Bind  BindFunc     // nil: Bind
}

// RuntimeHandlerFunc builds the handler of one runtime listener
// (api.Server.RuntimeHandler). revoked is closed once the listener is
// revoked; isRevoked reads the durable state.
type RuntimeHandlerFunc func(info core.ListenerInfo, revoked <-chan struct{}, isRevoked func(ctx context.Context) (bool, error)) http.Handler

// Runtime runs the runtime listeners recorded in core_listeners (spec 1.10):
// HTTPS listeners the controller opens and revokes without a restart, each
// accepting connections from one proxy only. It implements core.Listeners.
// Its methods are safe for concurrent use.
type Runtime struct {
	cfg RuntimeConfig

	// mu guards everything below. It is held while Start reads the open rows
	// and binds them, and while Open records a row and binds it, so an Open
	// that races Start is bound by one of the two.
	mu      sync.Mutex
	started bool
	closed  bool
	handler RuntimeHandlerFunc
	tls     *tls.Config
	live    map[string]*runtimeListener
	pending map[string]core.ListenerInfo // open rows whose bind failed at Start, retried every CheckInterval
	timer   core.Timer
	serving sync.WaitGroup // the Serve goroutines
}

var _ core.Listeners = (*Runtime)(nil)

// runtimeListener is one bound listener.
type runtimeListener struct {
	info    core.ListenerInfo
	srv     *http.Server
	ln      net.Listener
	revoked chan struct{}
	stopped bool // the listener no longer accepts
}

// NewRuntime returns the runtime listeners over cfg; nothing is bound until
// Start.
func NewRuntime(cfg RuntimeConfig) *Runtime {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.Bind == nil {
		cfg.Bind = Bind
	}
	return &Runtime{cfg: cfg, live: map[string]*runtimeListener{}, pending: map[string]core.ListenerInfo{}}
}

// Start binds every listener in state open, those Open recorded before
// Start included, with handler and tlsConfig, which each listener clones. A
// listener found revoked or closed is never opened. A listener whose bind
// fails stays open and is retried every CheckInterval; Start fails only when
// the rows cannot be read.
func (r *Runtime) Start(ctx context.Context, handler RuntimeHandlerFunc, tlsConfig *tls.Config) error {
	if handler == nil || tlsConfig == nil {
		return errors.New("listen: Runtime.Start needs a handler and a TLS configuration")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started || r.closed {
		return errors.New("listen: the runtime listeners were already started")
	}
	rows, err := listListeners(ctx, r.cfg.DB, core.ListenerOpen)
	if err != nil {
		return err
	}
	r.started, r.handler, r.tls = true, handler, tlsConfig
	for _, info := range rows {
		if err := r.bindLocked(ctx, info); err != nil {
			r.cfg.Log.Warn("listen: a runtime listener could not be bound; retrying", "id", info.ID,
				"addr", info.Spec.Addr.String(), "err", err)
			r.pendLocked(info)
		}
	}
	return nil
}

// Open records spec as a listener in state open and, once Start has run,
// binds it; before Start the row waits for Start, which binds every open
// row. When the bind fails the listener is closed and the error returned.
func (r *Runtime) Open(ctx context.Context, spec core.ListenerSpec) (string, error) {
	spec, err := validListenerSpec(spec)
	if err != nil {
		return "", err
	}
	now := r.cfg.Clock.Now()
	id := ulid.MustNew(ulid.Timestamp(now), rand.Reader).String()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return "", errRuntimeShutDown
	}
	if err := r.cfg.DB.Write(ctx, func(tx *sql.Tx) error { return insertListener(ctx, tx, id, spec, now) }); err != nil {
		return "", err
	}
	if !r.started {
		return id, nil
	}
	err = r.bindLocked(ctx, core.ListenerInfo{ID: id, Spec: spec, State: core.ListenerOpen})
	if err == nil {
		return id, nil
	}
	// The bind may have failed because ctx ended; the row must close anyway,
	// or the next Start would bind a listener whose Open failed.
	closeCtx := context.WithoutCancel(ctx)
	closeErr := r.cfg.DB.Write(closeCtx, func(tx *sql.Tx) error {
		_, err := setListenerState(closeCtx, tx, id, core.ListenerClosed, []string{core.ListenerOpen}, r.cfg.Clock.Now())
		return err
	})
	return "", errors.Join(err, closeErr)
}

// Revoke commits state revoked first, then ends the listener's streams,
// stops accepting, and closes its connections once their current response
// is written. Revoking a revoked or closed listener succeeds.
func (r *Runtime) Revoke(ctx context.Context, id string) error {
	var changed bool
	err := r.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
		var err error
		changed, err = setListenerState(ctx, tx, id, core.ListenerRevoked, []string{core.ListenerOpen}, r.cfg.Clock.Now())
		return err
	})
	if err != nil {
		return err
	}
	if !changed {
		if _, err := getListener(ctx, r.cfg.DB, id); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.pending, id)
	if l := r.live[id]; l != nil {
		r.stopLocked(l)
	}
	return nil
}

// Close moves a revoked listener to closed. Closing a closed listener
// succeeds; an open one must be revoked first.
func (r *Runtime) Close(ctx context.Context, id string) error {
	var changed bool
	err := r.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
		var err error
		changed, err = setListenerState(ctx, tx, id, core.ListenerClosed, []string{core.ListenerRevoked}, r.cfg.Clock.Now())
		return err
	})
	if err != nil {
		return err
	}
	if !changed {
		info, err := getListener(ctx, r.cfg.DB, id)
		if err != nil {
			return err
		}
		if info.State == core.ListenerOpen {
			return errcode.New(errcode.InvalidInput, "runtime listener "+id+" is open; revoke it first")
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if l := r.live[id]; l != nil {
		r.stopLocked(l)
	}
	return nil
}

// Get reads listener id.
func (r *Runtime) Get(ctx context.Context, id string) (core.ListenerInfo, error) {
	return getListener(ctx, r.cfg.DB, id)
}

// Unbound is the number of open listeners waiting for a bind retry.
func (r *Runtime) Unbound() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pending)
}

// Addrs are the bound addresses of the listeners that still accept, sorted.
// A listener whose address is not an IP address and port is returned in
// dropped, sorted, and logged at Warn, because the guard topology built
// from Addrs is then incomplete.
func (r *Runtime) Addrs() (addrs []netip.AddrPort, dropped []string) {
	r.mu.Lock()
	addrs = make([]netip.AddrPort, 0, len(r.live))
	for _, l := range r.live {
		if l.stopped {
			continue
		}
		bound := l.ln.Addr().String()
		ap, err := netip.ParseAddrPort(bound)
		if err != nil {
			dropped = append(dropped, bound)
			continue
		}
		addrs = append(addrs, netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()))
	}
	r.mu.Unlock()
	slices.Sort(dropped)
	for _, d := range dropped {
		r.cfg.Log.Warn("listen: a runtime listener's address is not an IP address", "addr", d)
	}
	slices.SortFunc(addrs, netip.AddrPort.Compare)
	return addrs, dropped
}

// Shutdown stops every listener, letting running requests finish until ctx
// ends, and waits for the serving goroutines. The rows keep their state, so
// the next Start reopens the open ones.
func (r *Runtime) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	r.closed = true
	if r.timer != nil {
		r.timer.Stop()
	}
	live := r.live
	r.live = map[string]*runtimeListener{}
	r.pending = map[string]core.ListenerInfo{}
	r.mu.Unlock()
	var errs []error
	for _, l := range live {
		l.srv.SetKeepAlivesEnabled(false)
		if err := l.srv.Shutdown(ctx); err != nil {
			_ = l.srv.Close()
			errs = append(errs, fmt.Errorf("listen: runtime listener %s: %w", l.info.ID, err))
		}
		_ = l.ln.Close()
	}
	r.serving.Wait()
	return errors.Join(errs...)
}

// bindLocked binds info and serves it, unless its row is no longer open: a
// Revoke that committed after the rows were read must win.
func (r *Runtime) bindLocked(ctx context.Context, info core.ListenerInfo) error {
	if r.closed {
		return errRuntimeShutDown
	}
	ln, err := r.cfg.Bind(ctx, Endpoint{Addr: info.Spec.Addr, TLS: true})
	if err != nil {
		return err
	}
	row, err := getListener(ctx, r.cfg.DB, info.ID)
	if err != nil || row.State != core.ListenerOpen {
		_ = ln.Close()
		if err != nil {
			return err
		}
		return nil
	}
	l := &runtimeListener{info: row, revoked: make(chan struct{}),
		ln: &peerListener{Listener: ln, allowed: info.Spec.AllowedPeer, id: info.ID, log: r.cfg.Log, clock: r.cfg.Clock}}
	id := info.ID
	isRevoked := func(ctx context.Context) (bool, error) {
		row, err := getListener(ctx, r.cfg.DB, id)
		if err != nil {
			return true, err
		}
		return row.State != core.ListenerOpen, nil
	}
	l.srv = &http.Server{
		Handler:           r.handler(row, l.revoked, isRevoked),
		TLSConfig:         r.tls.Clone(),
		ReadHeaderTimeout: ReadHeaderTimeout,
		ReadTimeout:       ReadTimeout,
		WriteTimeout:      WriteTimeout,
		IdleTimeout:       IdleTimeout,
		ErrorLog:          slog.NewLogLogger(r.cfg.Log.Handler(), slog.LevelWarn),
	}
	r.live[id] = l
	r.serving.Go(func() {
		err := l.srv.ServeTLS(l.ln, "", "")
		r.mu.Lock()
		defer r.mu.Unlock()
		if l.stopped || r.closed || errors.Is(err, http.ErrServerClosed) {
			return
		}
		// The row is still open, so the listener goes back to the pending
		// binds: Addrs must not report an address nothing serves.
		r.cfg.Log.Error("listen: a runtime listener stopped serving; rebinding", "id", id, "err", err)
		l.stopped = true
		_ = l.srv.Close()
		if r.live[id] == l {
			delete(r.live, id)
		}
		r.pendLocked(row)
	})
	r.cfg.Log.Info("listen: serving a runtime listener", "id", id, "addr", ln.Addr().String(),
		"peer", info.Spec.AllowedPeer.String())
	return nil
}

// stopLocked ends l's streams, stops accepting at once, and closes l's
// connections after their current response, cutting them after RevokeGrace.
// l stays in live until its connections are gone, so that Shutdown can cut
// them when its context ends.
func (r *Runtime) stopLocked(l *runtimeListener) {
	if l.stopped {
		return
	}
	l.stopped = true
	close(l.revoked)
	l.srv.SetKeepAlivesEnabled(false)
	_ = l.ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	timer := r.cfg.Clock.AfterFunc(RevokeGrace, cancel)
	r.serving.Go(func() {
		defer cancel()
		defer timer.Stop()
		if err := l.srv.Shutdown(ctx); err != nil {
			_ = l.srv.Close()
		}
		r.mu.Lock()
		if r.live[l.info.ID] == l {
			delete(r.live, l.info.ID)
		}
		r.mu.Unlock()
	})
	r.cfg.Log.Info("listen: revoked a runtime listener", "id", l.info.ID)
}

// pendLocked adds info to the pending binds, arming the retry unless one is
// already armed for the binds pending before it.
func (r *Runtime) pendLocked(info core.ListenerInfo) {
	armed := len(r.pending) > 0
	r.pending[info.ID] = info
	if !armed {
		r.armLocked()
	}
}

// armLocked schedules the next retry of the pending binds.
func (r *Runtime) armLocked() {
	if len(r.pending) == 0 || r.closed {
		return
	}
	r.timer = r.cfg.Clock.AfterFunc(CheckInterval, r.retry)
}

func (r *Runtime) retry() {
	ctx, cancel := context.WithTimeout(context.Background(), CheckInterval)
	defer cancel()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	for id, info := range r.pending {
		if err := r.bindLocked(ctx, info); err != nil {
			r.cfg.Log.Warn("listen: a runtime listener could not be bound; retrying", "id", id, "err", err)
			continue
		}
		delete(r.pending, id)
	}
	r.armLocked()
}

// validListenerSpec checks spec and returns it with its addresses unmapped
// and its public host in lower case without a trailing dot, the one form the
// API compares a request's Origin with.
func validListenerSpec(spec core.ListenerSpec) (core.ListenerSpec, error) {
	bad := func(detail string) (core.ListenerSpec, error) {
		return core.ListenerSpec{}, errcode.New(errcode.InvalidInput, "runtime listener: "+detail)
	}
	spec.PublicHost = strings.TrimSuffix(strings.ToLower(spec.PublicHost), ".")
	switch {
	case !spec.Addr.IsValid() || spec.Addr.Port() == 0 || spec.Addr.Addr().Zone() != "":
		return bad("the address needs an IP address without a zone and a port")
	case !spec.AllowedPeer.IsValid() || spec.AllowedPeer.Zone() != "":
		return bad("the allowed peer needs an IP address without a zone")
	case spec.ClientHeader != core.ClientHeaderNPM && spec.ClientHeader != core.ClientHeaderTunnel:
		return bad("the client header must be " + core.ClientHeaderNPM + " or " + core.ClientHeaderTunnel)
	case spec.PublicHost == "" || spec.ChangeID == "":
		return bad("the public host and the change id are required")
	}
	spec.Addr = netip.AddrPortFrom(spec.Addr.Addr().Unmap(), spec.Addr.Port())
	spec.AllowedPeer = spec.AllowedPeer.Unmap()
	return spec, nil
}

// refusalLogInterval is the least time between two log lines about refused
// connections to one runtime listener. Any host that reaches the address can
// open connections, and a line for each would let it flood the log.
const refusalLogInterval = time.Minute

// peerListener accepts connections from the allowed peer only and closes
// every other one before any byte is read. Accept runs on the one serving
// goroutine, so the log throttle needs no lock.
type peerListener struct {
	net.Listener
	allowed netip.Addr
	id      string
	log     *slog.Logger
	clock   core.Clock

	quietUntil time.Time // no refusal is logged before it
	suppressed int       // refusals not logged since the last line
}

func (l *peerListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		ap, err := netip.ParseAddrPort(c.RemoteAddr().String())
		if err == nil && ap.Addr().Unmap().WithZone("") == l.allowed {
			return c, nil
		}
		l.refused(c.RemoteAddr().String())
		_ = c.Close()
	}
}

func (l *peerListener) refused(peer string) {
	now := l.clock.Now()
	if now.Before(l.quietUntil) {
		l.suppressed++
		return
	}
	l.log.Warn("listen: refused a connection to a runtime listener from another peer", "id", l.id,
		"peer", peer, "suppressed", l.suppressed)
	l.quietUntil, l.suppressed = now.Add(refusalLogInterval), 0
}
