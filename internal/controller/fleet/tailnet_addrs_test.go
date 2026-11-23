package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/transport"
)

// remoteConn is a connection whose remote address is remote.
type remoteConn struct {
	net.Conn
	remote net.Addr
}

func (c remoteConn) RemoteAddr() net.Addr { return c.remote }

// readerFunc is a ResourceReader.
type readerFunc func(ctx context.Context, p core.Principal, hostID, name string) (json.RawMessage, error)

func (f readerFunc) Resource(ctx context.Context, p core.Principal, hostID, name string) (json.RawMessage, error) {
	return f(ctx, p, hostID, name)
}

// tailnetEnv is a registry whose resource reader asks the host's agent over
// its live session; the agent answers tailscale.status with status, or with
// statusErr when that is set.
type tailnetEnv struct {
	r      *Registry
	env    *registryEnv
	mu     sync.Mutex
	status string
	err    error
	reads  []string // "<actor> <host> <name>"
}

func newTailnetEnv(t *testing.T) *tailnetEnv {
	t.Helper()
	a, db, clock := newAuthority(t)
	addHost(t, db, "H1", core.HostEnrolled)
	serial := transport.SerialText(issue(t, a, db, "H1").SerialNumber)
	e := &tailnetEnv{}
	reader := readerFunc(func(ctx context.Context, p core.Principal, hostID, name string) (json.RawMessage, error) {
		e.mu.Lock()
		e.reads = append(e.reads, p.Actor+" "+hostID+" "+name)
		e.mu.Unlock()
		c, _, err := e.r.Client(ctx, hostID)
		if err != nil {
			return nil, err
		}
		return c.Resource(ctx, p, name)
	})
	r, err := NewRegistry(RegistryConfig{
		DB: db, Clock: clock, CheckPeer: a.CheckPeer, Version: "1.2.3", ControllerID: "C1", Log: quietLog,
		Resources: func() ResourceReader { return reader },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	e.r = r
	e.env = &registryEnv{r: r, a: a, db: db, clock: clock, serial: serial}
	return e
}

func (e *tailnetEnv) answer(status string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.status, e.err = status, err
}

// connect registers a session of H1 arriving from remote, served by an
// agent answering tailscale.status.
func (e *tailnetEnv) connect(t *testing.T, remote string) *transport.Session {
	t.Helper()
	c1, c2 := net.Pipe()
	ap := netip.MustParseAddrPort(remote)
	errs := make(chan error, 1)
	var agentSide *transport.Session
	go func() {
		var err error
		agentSide, err = transport.AgentSession(c2)
		errs <- err
	}()
	ctrl, err := transport.ControllerSession(remoteConn{Conn: c1, remote: net.TCPAddrFromAddrPort(ap)})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ctrl.Close()
		_ = agentSide.Close()
	})
	serveAgent(t, agentSide, &coretest.FakeAgent{ResourceFunc: func(context.Context, core.Principal, string) (json.RawMessage, error) {
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.err != nil {
			return nil, e.err
		}
		return json.RawMessage(e.status), nil
	}})
	e.env.register(t, ctrl)
	return ctrl
}

// rows returns H1's core_host_tailnet rows as addr -> "stable_id seen_at".
func (e *tailnetEnv) rows(t *testing.T) map[string]string {
	t.Helper()
	q, err := e.env.db.Read().Query(`SELECT addr, stable_id, seen_at FROM core_host_tailnet WHERE host_id = 'H1'`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q.Close() }()
	out := map[string]string{}
	for q.Next() {
		var addr, stable, seen string
		if err := q.Scan(&addr, &stable, &seen); err != nil {
			t.Fatal(err)
		}
		out[addr] = stable + " " + seen
	}
	if err := q.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// waitRows waits until H1's rows are want.
func (e *tailnetEnv) waitRows(t *testing.T, want map[string]string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := e.rows(t)
		if maps.Equal(got, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("core_host_tailnet rows\n got %v\nwant %v", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAddressesPersistAtConnect(t *testing.T) {
	ctx := context.Background()
	e := newTailnetEnv(t)
	t0 := formatTime(e.env.clock.Now())

	// Over the tailnet: every self address with the node's stable id; the
	// session's own address is one of them.
	e.answer(`{"backendState":"Running","self":{"id":"nH1","addresses":["100.64.0.7","fd7a:115c:a1e0::7"]}}`, nil)
	s := e.connect(t, "100.64.0.7:41000")
	want := map[string]string{"100.64.0.7": "nH1 " + t0, "fd7a:115c:a1e0::7": "nH1 " + t0}
	e.waitRows(t, want)
	e.mu.Lock()
	reads := append([]string(nil), e.reads...)
	e.mu.Unlock()
	if len(reads) != 1 || reads[0] != "system:fleet H1 tailscale.status" {
		t.Errorf("reads %v, want one tailscale.status read of H1 as system:fleet", reads)
	}

	// Over the LAN: the status addresses are recorded, the LAN address is not.
	e.env.clock.Advance(time.Minute)
	t1 := formatTime(e.env.clock.Now())
	e.answer(`{"backendState":"Running","self":{"id":"nH1","addresses":["100.64.0.8"]}}`, nil)
	_ = s.Close()
	s = e.connect(t, "192.168.1.10:41000")
	want["100.64.0.8"] = "nH1 " + t1
	e.waitRows(t, want)

	// The read fails (tailscale module disabled): the session's tailnet
	// address is recorded with no stable id and every earlier row stays.
	e.env.clock.Advance(time.Minute)
	t2 := formatTime(e.env.clock.Now())
	e.answer("", errcode.New(errcode.ModuleUnavailable, "module tailscale is disabled on host H1"))
	_ = s.Close()
	s = e.connect(t, "100.127.255.254:41000")
	want["100.127.255.254"] = " " + t2
	e.waitRows(t, want)

	// A known address seen again keeps its stable id and gets the new time,
	// even when the read fails; an IPv6 tailnet session address is recorded.
	e.env.clock.Advance(time.Minute)
	t3 := formatTime(e.env.clock.Now())
	_ = s.Close()
	s = e.connect(t, "100.64.0.7:41000")
	want["100.64.0.7"] = "nH1 " + t3
	e.waitRows(t, want)
	_ = s.Close()
	s = e.connect(t, "[fd7a:115c:a1e0::99]:41000")
	want["fd7a:115c:a1e0::99"] = " " + t3
	e.waitRows(t, want)

	// Outside 100.64.0.0/10 and fd7a:115c:a1e0::/48 neither a session address
	// nor a status address is recorded, and addresses that do not parse are
	// skipped. Each connect
	// records 100.64.0.7 at a new time in the same transaction, so the rows
	// show when its write is done.
	e.answer(`{"backendState":"Running","self":{"id":"nH1","addresses":["bogus","192.168.5.5","8.8.8.8","fd7a:115c:a1e1::5","100.64.0.7"]}}`, nil)
	for _, remote := range []string{"100.128.0.1:41000", "100.63.255.255:41000", "[fd7a:115c:a1e1::1]:41000"} {
		e.env.clock.Advance(time.Minute)
		_ = s.Close()
		s = e.connect(t, remote)
		want["100.64.0.7"] = "nH1 " + formatTime(e.env.clock.Now())
		e.waitRows(t, want)
	}

	// A logged-out node (self null) adds nothing from its status; its
	// tailnet session address is still recorded.
	e.answer(`{"backendState":"NeedsLogin","self":null}`, nil)
	_ = s.Close()
	s = e.connect(t, "100.64.0.9:41000")
	want["100.64.0.9"] = " " + formatTime(e.env.clock.Now())
	e.waitRows(t, want)

	// One answer adds at most 16 addresses.
	many := make([]string, 20)
	for i := range many {
		many[i] = fmt.Sprintf("100.64.1.%d", i)
	}
	raw, err := json.Marshal(map[string]any{"backendState": "Running", "self": map[string]any{"id": "nH1", "addresses": many}})
	if err != nil {
		t.Fatal(err)
	}
	e.answer(string(raw), nil)
	_ = s.Close()
	s = e.connect(t, "192.168.1.10:41000")
	for _, addr := range many[:16] {
		want[addr] = "nH1 " + formatTime(e.env.clock.Now())
	}
	e.waitRows(t, want)

	// Offline: the rows stay.
	e.r.Disconnect("H1")
	_ = s.Close()
	e.r.Close()
	if got := e.rows(t); !maps.Equal(got, want) {
		t.Errorf("after the host went offline: rows %v, want %v", got, want)
	}

	svc := auth.New(e.env.db, newSealer(t), e.env.clock, nil, auth.Options{})
	if err := listen.SaveTailscaleAddrs(ctx, e.env.db, []netip.Addr{netip.MustParseAddr("100.64.0.1")}); err != nil {
		t.Fatal(err)
	}
	a := netip.MustParseAddr
	for _, tc := range []struct {
		name   string
		addrs  []netip.Addr
		stable string
		want   bool
	}{
		{"a stored address", []netip.Addr{a("100.99.0.1"), a("fd7a:115c:a1e0::7")}, "", true},
		{"a stored address, mapped", []netip.Addr{a("::ffff:100.64.0.8")}, "nOther", true},
		{"the stored stable id", []netip.Addr{a("100.99.0.1")}, "nH1", true},
		{"the controller's own address", []netip.Addr{a("100.64.0.1")}, "nOther", true},
		{"an unknown node", []netip.Addr{a("100.99.0.1")}, "nOther", false},
		{"no stable id never matches a row without one", []netip.Addr{a("100.99.0.1")}, "", false},
		{"nothing to look up", nil, "", false},
	} {
		got, err := svc.IsManagedTailnetNode(ctx, tc.addrs, tc.stable)
		if err != nil || got != tc.want {
			t.Errorf("%s: IsManagedTailnetNode = %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := svc.IsManagedTailnetNode(cancelled, []netip.Addr{a("100.99.0.1")}, "nOther"); err == nil {
		t.Error("a failed table lookup answered no error")
	}
	if _, err := e.env.db.Writer().ExecContext(ctx, `UPDATE core_meta SET value = 'not json' WHERE key = 'tailscale_addrs'`); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.IsManagedTailnetNode(ctx, []netip.Addr{a("100.99.0.1")}, "nOther"); err == nil {
		t.Errorf("a failed read of the controller's addresses answered %v, no error", got)
	}
}
