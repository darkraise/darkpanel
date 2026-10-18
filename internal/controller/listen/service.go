package listen

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// CheckInterval is how often the service reads the Tailscale addresses and
// checks the leaf's age (spec section 20).
const CheckInterval = 10 * time.Second

// Config configures Start.
type Config struct {
	Specs     []Spec // from ParseSpecs
	Hostname  string // the leaf's first DNS SAN
	DB        *store.DB
	Authority *Authority
	Clock     core.Clock
	Handler   http.Handler
	// Log receives the listener changes and the state message; nil discards.
	Log *slog.Logger
	// Interfaces lists interface addresses; nil means SystemInterfaceAddrs.
	Interfaces InterfaceAddrs
	// Bind opens the sockets; nil means Bind.
	Bind BindFunc
	// OnServeError is SetConfig.OnServeError.
	OnServeError func(error)
}

// State is what the UI footer and serve's log report.
type State struct {
	// TailscaleBound is true while at least one Tailscale address is served.
	TailscaleBound bool
	// Message is one sentence for the operator, such as "No Tailscale
	// address found; serving loopback only."
	Message   string
	Listeners []Bound
}

// Service runs the controller's listeners: it binds the endpoints of the
// specs, keeps the leaf current and, every CheckInterval, follows the
// Tailscale addresses and renews the leaf when due.
type Service struct {
	cfg   Config
	leaf  *Leaf
	set   *Set
	state atomic.Pointer[State]

	mu      sync.Mutex // serialises checks and Shutdown
	addrs   []netip.Addr
	unsaved bool // addrs differ from those saved in core_meta
	timer   core.Timer
	closed  bool
}

// Start binds the listeners and arms the periodic check. With the tailscale
// listener it first binds the addresses saved in core_meta, replaced by
// those of tailscale0 when that interface exists, so a restart serves the
// tailnet before tailscaled is up. A failed bind fails Start, except an
// IPv6 loopback address the host lacks.
func Start(ctx context.Context, cfg Config) (*Service, error) {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.Interfaces == nil {
		cfg.Interfaces = SystemInterfaceAddrs
	}
	s := &Service{cfg: cfg, leaf: NewLeaf(cfg.Authority, cfg.Clock, cfg.Hostname)}
	s.set = NewSet(SetConfig{Handler: cfg.Handler, TLS: s.leaf.TLSConfig(), Bind: cfg.Bind, Log: cfg.Log, OnServeError: cfg.OnServeError})
	if HasTailscale(cfg.Specs) {
		saved, err := LoadTailscaleAddrs(ctx, cfg.DB)
		if err != nil {
			return nil, err
		}
		s.addrs = saved
		s.refresh(ctx)
	}
	if err := s.apply(ctx); err != nil {
		_ = s.set.Shutdown(ctx)
		return nil, err
	}
	s.publish(true)
	s.mu.Lock()
	s.timer = cfg.Clock.AfterFunc(CheckInterval, s.check)
	s.mu.Unlock()
	return s, nil
}

// State returns the current listener state.
func (s *Service) State() State {
	st := *s.state.Load()
	st.Listeners = slices.Clone(st.Listeners)
	return st
}

// Shutdown stops the checks and the listeners, letting running requests
// finish until ctx ends.
func (s *Service) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	if s.timer != nil {
		s.timer.Stop()
	}
	s.mu.Unlock()
	err := s.set.Shutdown(ctx)
	st := &State{Message: "The listeners are shut down."}
	if prev := s.state.Swap(st); prev == nil || prev.Message != st.Message {
		s.cfg.Log.Info("listen: " + st.Message)
	}
	return err
}

func (s *Service) check() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), CheckInterval)
	defer cancel()
	if HasTailscale(s.cfg.Specs) {
		s.refresh(ctx)
	}
	if err := s.apply(ctx); err != nil {
		s.cfg.Log.Warn("listen: not every listener is bound; retrying at the next check", "err", err)
	}
	s.publish(false)
	s.timer = s.cfg.Clock.AfterFunc(CheckInterval, s.check)
}

// refresh reads tailscale0. An absent interface means tailscaled is not up,
// so the last addresses stay bound; a present one replaces them, even with
// none. A failed save is retried at every check.
func (s *Service) refresh(ctx context.Context) {
	list, present, err := s.cfg.Interfaces(TailscaleInterface)
	if err != nil {
		s.cfg.Log.Warn("listen: cannot read the Tailscale addresses", "err", err)
	} else if next := TailscaleAddrs(list); present && !slices.Equal(next, s.addrs) {
		s.cfg.Log.Info("listen: Tailscale addresses changed", "from", s.addrs, "to", next)
		s.addrs = next
		s.unsaved = true
	}
	if !s.unsaved {
		return
	}
	if err := SaveTailscaleAddrs(ctx, s.cfg.DB, s.addrs); err != nil {
		s.cfg.Log.Warn("listen: cannot save the Tailscale addresses", "err", err)
		return
	}
	s.unsaved = false
}

func (s *Service) apply(ctx context.Context) error {
	eps := Endpoints(s.cfg.Specs, s.addrs)
	var ips []netip.Addr
	for _, e := range eps {
		if e.TLS {
			ips = append(ips, e.Addr.Addr())
		}
	}
	if len(ips) > 0 {
		issued, err := s.leaf.Ensure(ips)
		if err != nil {
			return err
		}
		if issued {
			s.cfg.Log.Info("listen: issued the web certificate", "hostname", s.cfg.Hostname, "ips", ips,
				"expires", s.cfg.Clock.Now().Add(LeafValidity).UTC().Format(time.RFC3339))
		}
	}
	return s.set.Update(ctx, eps)
}

// publish stores the state for State and logs its message when it changed,
// or always when force is set.
func (s *Service) publish(force bool) {
	bound := s.set.Bound()
	st := &State{Listeners: bound}
	var tsURLs []string
	loopbackOnly, servesLoopback := true, false
	for _, b := range bound {
		if slices.Contains(s.addrs, b.Endpoint.Addr.Addr()) {
			tsURLs = append(tsURLs, b.Endpoint.URL())
		}
		if b.Endpoint.Addr.Addr().IsLoopback() {
			servesLoopback = true
		} else {
			loopbackOnly = false
		}
	}
	st.TailscaleBound = len(tsURLs) > 0
	switch {
	case !HasTailscale(s.cfg.Specs):
		st.Message = "Tailscale listener disabled by --listen."
	case st.TailscaleBound:
		st.Message = "Serving on Tailscale: " + strings.Join(tsURLs, ", ") + "."
	case len(s.addrs) > 0:
		st.Message = "Tailscale addresses found but not bound; see the serve log."
	case loopbackOnly && servesLoopback:
		st.Message = "No Tailscale address found; serving loopback only."
	default:
		st.Message = "No Tailscale address found; not serving on the tailnet."
	}
	prev := s.state.Swap(st)
	if force || prev == nil || prev.Message != st.Message {
		s.cfg.Log.Info("listen: " + st.Message)
	}
}
