package listen

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"sync"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// AgentPort is the agent listener's port on loopback and on each Tailscale
// address (spec section 4).
const AgentPort = 7443

var agentLoopback = netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), AgentPort)

// ParseAgentListen parses the --agent-listen values, each <ip>:<port> of a
// specific address of this host. Repeated values count once. It refuses
// an unspecified address, so the agent listener never binds every
// interface.
func ParseAgentListen(values []string) ([]netip.AddrPort, error) {
	var out []netip.AddrPort
	for _, v := range values {
		ap, err := netip.ParseAddrPort(v)
		if err != nil {
			return nil, fmt.Errorf("listen: --agent-listen %q is not <ip>:<port>", v)
		}
		addr := ap.Addr()
		switch {
		case addr.Zone() != "":
			return nil, fmt.Errorf("listen: --agent-listen %q: the address must not carry a zone", v)
		case ap.Port() == 0:
			return nil, fmt.Errorf("listen: --agent-listen %q needs a port between 1 and 65535", v)
		}
		addr = addr.Unmap()
		if addr.IsUnspecified() || addr.IsMulticast() {
			return nil, fmt.Errorf("listen: --agent-listen %q: the address must be a specific address of this host", v)
		}
		if ap = netip.AddrPortFrom(addr, ap.Port()); !slices.Contains(out, ap) {
			out = append(out, ap)
		}
	}
	return out, nil
}

// AgentEndpoints returns the agent listener's endpoints: 127.0.0.1:7443
// always, each Tailscale address on AgentPort (bound with FreeBind, as the
// web listener's are) and each extra address, sorted by address and without
// repeats. Every endpoint speaks TLS.
func AgentEndpoints(extra []netip.AddrPort, tailscale []netip.Addr) []Endpoint {
	byAddr := map[netip.AddrPort]Endpoint{agentLoopback: {Addr: agentLoopback, TLS: true}}
	for _, a := range tailscale {
		ap := netip.AddrPortFrom(a, AgentPort)
		byAddr[ap] = Endpoint{Addr: ap, TLS: true, FreeBind: true}
	}
	for _, ap := range extra {
		if _, ok := byAddr[ap]; !ok {
			byAddr[ap] = Endpoint{Addr: ap, TLS: true}
		}
	}
	out := make([]Endpoint, 0, len(byAddr))
	for _, e := range byAddr {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b Endpoint) int { return a.Addr.Compare(b.Addr) })
	return out
}

// AgentConfig configures StartAgent.
type AgentConfig struct {
	Extra []netip.AddrPort // from ParseAgentListen
	DB    *store.DB        // the Tailscale addresses saved in core_meta
	Clock core.Clock
	// Serve serves the agent connections of one bound socket until the
	// socket is closed, then returns nil: fleet.Server.Serve.
	Serve func(net.Listener) error
	// Log receives binds, failures and Tailscale changes; nil discards.
	Log *slog.Logger
	// Interfaces lists interface addresses; nil means SystemInterfaceAddrs.
	Interfaces InterfaceAddrs
	// Bind opens the sockets; nil means Bind.
	Bind BindFunc
}

// AgentService runs the agent listener: it binds AgentEndpoints and, every
// CheckInterval, follows the Tailscale addresses as the web listener does.
// It owns the sockets only; Serve owns the connections it accepts. Its
// methods are safe for concurrent use.
type AgentService struct {
	cfg AgentConfig

	mu     sync.Mutex // serialises checks and Shutdown
	ts     *TailscaleWatcher
	bound  map[Endpoint]net.Listener
	timer  core.Timer
	closed bool
	wg     sync.WaitGroup // Serve calls
}

// StartAgent binds the agent listener and arms the periodic check. A failed
// bind fails it, with every socket it bound closed again.
func StartAgent(ctx context.Context, cfg AgentConfig) (*AgentService, error) {
	if cfg.Serve == nil || cfg.DB == nil || cfg.Clock == nil {
		return nil, errors.New("listen: the agent listener needs Serve, a database and a clock")
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.Bind == nil {
		cfg.Bind = Bind
	}
	ts, err := NewTailscaleWatcher(ctx, cfg.DB, cfg.Interfaces, cfg.Log)
	if err != nil {
		return nil, err
	}
	s := &AgentService{cfg: cfg, ts: ts, bound: map[Endpoint]net.Listener{}}
	s.mu.Lock()
	err = s.update(ctx)
	s.mu.Unlock()
	if err != nil {
		_ = s.Shutdown(ctx)
		return nil, err
	}
	s.mu.Lock()
	s.timer = cfg.Clock.AfterFunc(CheckInterval, s.check)
	s.mu.Unlock()
	return s, nil
}

// update makes the bound sockets those of AgentEndpoints, closing the
// others; s.mu is held. It returns the bind failures joined; a later check
// retries them.
func (s *AgentService) update(ctx context.Context) error {
	want := AgentEndpoints(s.cfg.Extra, s.ts.Addrs())
	for e, ln := range s.bound {
		if !slices.Contains(want, e) {
			_ = ln.Close()
			delete(s.bound, e)
			s.cfg.Log.Info("listen: stopped serving agents", "addr", e.Addr.String())
		}
	}
	var errs []error
	for _, e := range want {
		if s.bound[e] != nil {
			continue
		}
		ln, err := s.cfg.Bind(ctx, e)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		s.bound[e] = ln
		s.wg.Add(1)
		go s.serve(e, ln)
		s.cfg.Log.Info("listen: serving agents", "addr", e.Addr.String(), "bound", ln.Addr().String())
	}
	return errors.Join(errs...)
}

func (s *AgentService) serve(e Endpoint, ln net.Listener) {
	defer s.wg.Done()
	if err := s.cfg.Serve(ln); err != nil {
		s.cfg.Log.Error("listen: serving agents stopped", "addr", e.Addr.String(), "err", err)
	}
}

func (s *AgentService) check() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), CheckInterval)
	defer cancel()
	s.ts.Refresh(ctx)
	if err := s.update(ctx); err != nil {
		s.cfg.Log.Warn("listen: not every agent listener is bound; retrying at the next check", "err", err)
	}
	s.timer = s.cfg.Clock.AfterFunc(CheckInterval, s.check)
}

// Bound returns the bound sockets sorted by endpoint address, none after
// Shutdown.
func (s *AgentService) Bound() []Bound {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Bound, 0, len(s.bound))
	for e, ln := range s.bound {
		out = append(out, Bound{Endpoint: e, Addr: ln.Addr()})
	}
	slices.SortFunc(out, func(a, b Bound) int { return a.Endpoint.Addr.Compare(b.Endpoint.Addr) })
	return out
}

// Shutdown stops the checks, closes every socket and waits until every
// Serve call has returned or ctx ends. Connections Serve accepted stay
// open; their owner closes them.
func (s *AgentService) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	if s.timer != nil {
		s.timer.Stop()
	}
	for e, ln := range s.bound {
		_ = ln.Close()
		delete(s.bound, e)
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("listen: agent listener shutdown: %w", ctx.Err())
	}
}
