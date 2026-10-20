package listen

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"reflect"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
)

func TestParseAgentListen(t *testing.T) {
	got, err := ParseAgentListen([]string{"10.77.0.10:7443", "[fd00::1]:9000", "10.77.0.10:7443", "[::ffff:10.0.0.5]:7443"})
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.AddrPort{
		netip.MustParseAddrPort("10.77.0.10:7443"),
		netip.MustParseAddrPort("[fd00::1]:9000"),
		netip.MustParseAddrPort("10.0.0.5:7443"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseAgentListen = %v, want %v", got, want)
	}
	if got, err := ParseAgentListen(nil); err != nil || got != nil {
		t.Fatalf("no values = %v, %v", got, err)
	}
	for _, v := range []string{
		"0.0.0.0:7443", "[::]:7443", "10.0.0.1", "10.0.0.1:0", "[fe80::1%eth0]:7443", "224.0.0.1:7443",
		"controller:7443", "https://10.0.0.1:7443", "",
	} {
		if _, err := ParseAgentListen([]string{v}); err == nil {
			t.Errorf("ParseAgentListen accepted %q", v)
		}
	}
}

func TestAgentEndpoints(t *testing.T) {
	got := AgentEndpoints(
		[]netip.AddrPort{netip.MustParseAddrPort("10.77.0.10:7443"), netip.MustParseAddrPort("127.0.0.1:7443")},
		addrs("fd7a:115c:a1e0::1", "100.64.0.1"))
	want := []Endpoint{
		{Addr: netip.MustParseAddrPort("10.77.0.10:7443"), TLS: true},
		{Addr: netip.MustParseAddrPort("100.64.0.1:7443"), TLS: true, FreeBind: true},
		{Addr: netip.MustParseAddrPort("127.0.0.1:7443"), TLS: true},
		{Addr: netip.MustParseAddrPort("[fd7a:115c:a1e0::1]:7443"), TLS: true, FreeBind: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AgentEndpoints =\n%v\nwant\n%v", got, want)
	}
	if got := AgentEndpoints(nil, nil); len(got) != 1 || got[0].Addr != netip.MustParseAddrPort("127.0.0.1:7443") {
		t.Fatalf("without Tailscale or extras: %v, want loopback alone", got)
	}
	for _, e := range AgentEndpoints(nil, addrs("100.64.0.1", "fd7a:115c:a1e0::1")) {
		if e.Addr.Addr().IsUnspecified() {
			t.Fatalf("an endpoint binds every interface: %v", e)
		}
	}
}

// greeter is an AgentConfig.Serve that answers every connection with
// "agent" and records the listeners it served.
type greeter struct {
	mu     sync.Mutex
	served []net.Listener
	ended  int
}

func (g *greeter) serve(ln net.Listener) error {
	g.mu.Lock()
	g.served = append(g.served, ln)
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		g.ended++
		g.mu.Unlock()
	}()
	for {
		c, err := ln.Accept()
		if errors.Is(err, net.ErrClosed) {
			return nil
		}
		if err != nil {
			return err
		}
		_, _ = c.Write([]byte("agent"))
		_ = c.Close()
	}
}

func (g *greeter) counts() (served, ended int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.served), g.ended
}

// greet dials addr and returns what the server sent, or the dial error.
func greet(addr net.Addr) (string, error) {
	var d net.Dialer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := d.DialContext(ctx, "tcp", addr.String())
	if err != nil {
		return "", err
	}
	defer func() { _ = c.Close() }()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	b, err := io.ReadAll(c)
	return string(b), err
}

type agentFixture struct {
	db     *store.DB
	clock  *coretest.FakeClock
	ifs    *fakeInterfaces
	binder *fakeBinder
	g      *greeter
}

func newAgentFixture(t *testing.T) *agentFixture {
	t.Helper()
	return &agentFixture{
		db: openControllerDB(t), clock: coretest.NewFakeClock(testStart),
		ifs: &fakeInterfaces{}, binder: &fakeBinder{}, g: &greeter{},
	}
}

func (f *agentFixture) start(t *testing.T, extra ...string) (*AgentService, error) {
	t.Helper()
	ex, err := ParseAgentListen(extra)
	if err != nil {
		t.Fatal(err)
	}
	s, err := StartAgent(context.Background(), AgentConfig{
		Extra: ex, DB: f.db, Clock: f.clock, Serve: f.g.serve, Log: slog.New(slog.DiscardHandler),
		Interfaces: f.ifs.list, Bind: f.binder.bind,
	})
	if err == nil {
		t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	}
	return s, err
}

func agentEndpointsOf(bound []Bound) []string {
	out := make([]string, 0, len(bound))
	for _, b := range bound {
		out = append(out, b.Endpoint.Addr.String())
	}
	return out
}

func TestAgentServiceFollowsTailscale(t *testing.T) {
	f := newAgentFixture(t)
	f.ifs.set(true, "100.64.0.1")
	s, err := f.start(t, "10.77.0.10:7443")
	if err != nil {
		t.Fatal(err)
	}
	bound := s.Bound()
	if got, want := agentEndpointsOf(bound), []string{"10.77.0.10:7443", "100.64.0.1:7443", "127.0.0.1:7443"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bound %v, want %v", got, want)
	}
	for _, b := range bound {
		if got, err := greet(b.Addr); err != nil || got != "agent" {
			t.Fatalf("%s answered %q, %v", b.Endpoint.Addr, got, err)
		}
	}
	if savedAddrs(t, f.db) != `["100.64.0.1"]` {
		t.Fatalf("saved = %s", savedAddrs(t, f.db))
	}

	f.ifs.set(true, "100.64.0.2")
	f.clock.Advance(CheckInterval)
	next := s.Bound()
	if got, want := agentEndpointsOf(next), []string{"10.77.0.10:7443", "100.64.0.2:7443", "127.0.0.1:7443"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after the change bound %v, want %v", got, want)
	}
	if _, err := greet(bound[1].Addr); !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("the old Tailscale socket still answers: %v", err)
	}
	if got, err := greet(bound[2].Addr); err != nil || got != "agent" {
		t.Fatalf("loopback stopped answering after the change: %q, %v", got, err)
	}

	f.ifs.set(false)
	f.clock.Advance(CheckInterval)
	if got := agentEndpointsOf(s.Bound()); !reflect.DeepEqual(got, agentEndpointsOf(next)) {
		t.Fatalf("an absent interface changed the binds to %v", got)
	}

	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if served, ended := f.g.counts(); served != 4 || ended != 4 {
		t.Fatalf("Serve ran on %d listeners and returned on %d, want 4 and 4", served, ended)
	}
	if got := s.Bound(); len(got) != 0 {
		t.Fatalf("after Shutdown bound %v", got)
	}
	for _, b := range next {
		if _, err := greet(b.Addr); err == nil {
			t.Fatalf("%s answers after Shutdown", b.Endpoint.Addr)
		}
	}
	pending := f.clock.Pending()
	f.clock.Advance(CheckInterval)
	if f.clock.Pending() > pending || len(f.binder.called()) != 4 {
		t.Fatal("a check ran after Shutdown")
	}
}

func TestAgentServiceStartsFromSavedAddresses(t *testing.T) {
	f := newAgentFixture(t)
	if err := SaveTailscaleAddrs(context.Background(), f.db, addrs("100.64.0.7")); err != nil {
		t.Fatal(err)
	}
	s, err := f.start(t)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := agentEndpointsOf(s.Bound()), []string{"100.64.0.7:7443", "127.0.0.1:7443"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bound %v, want %v", got, want)
	}
}

func TestAgentServiceStartFailsOnABusyAddress(t *testing.T) {
	f := newAgentFixture(t)
	f.binder.fail = map[netip.AddrPort]error{netip.MustParseAddrPort("127.0.0.1:7443"): syscall.EADDRINUSE}
	if _, err := f.start(t, "10.77.0.10:7443"); !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("StartAgent = %v, want the bind error", err)
	}
	if served, ended := f.g.counts(); served != ended {
		t.Fatalf("a failed start left %d of %d listeners serving", served-ended, served)
	}
}

func TestAgentServiceRetriesAFailedBind(t *testing.T) {
	f := newAgentFixture(t)
	s, err := f.start(t)
	if err != nil {
		t.Fatal(err)
	}
	f.binder.mu.Lock()
	f.binder.fail = map[netip.AddrPort]error{netip.MustParseAddrPort("100.64.0.1:7443"): syscall.EADDRINUSE}
	f.binder.mu.Unlock()
	f.ifs.set(true, "100.64.0.1")
	f.clock.Advance(CheckInterval)
	if got := agentEndpointsOf(s.Bound()); !reflect.DeepEqual(got, []string{"127.0.0.1:7443"}) {
		t.Fatalf("bound %v while the Tailscale bind fails", got)
	}
	f.binder.mu.Lock()
	f.binder.fail = nil
	f.binder.mu.Unlock()
	f.clock.Advance(CheckInterval)
	if got := agentEndpointsOf(s.Bound()); !reflect.DeepEqual(got, []string{"100.64.0.1:7443", "127.0.0.1:7443"}) {
		t.Fatalf("bound %v; the failed bind was not retried", got)
	}
}

func TestStartAgentRefusesBadConfig(t *testing.T) {
	f := newAgentFixture(t)
	for name, cfg := range map[string]AgentConfig{
		"no serve":    {DB: f.db, Clock: f.clock},
		"no database": {Clock: f.clock, Serve: f.g.serve},
		"no clock":    {DB: f.db, Serve: f.g.serve},
	} {
		if s, err := StartAgent(context.Background(), cfg); err == nil {
			_ = s.Shutdown(context.Background())
			t.Errorf("%s: StartAgent accepted the configuration", name)
		}
	}
}
