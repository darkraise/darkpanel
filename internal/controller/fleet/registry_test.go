package fleet

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/transport"
)

// quietLog discards what the code under test logs.
var quietLog = slog.New(slog.DiscardHandler)

// registryEnv is a registry over a fresh authority whose database holds
// the enrolled host H1 with one certificate.
type registryEnv struct {
	r      *Registry
	a      *Authority
	db     *store.DB
	clock  *coretest.FakeClock
	serial string // H1's certificate
}

// newRegistry returns a registry checking peers with a fresh authority.
func newRegistry(t *testing.T, hb transport.HeartbeatConfig) *registryEnv {
	t.Helper()
	a, db, clock := newAuthority(t)
	addHost(t, db, "H1", core.HostEnrolled)
	serial := transport.SerialText(issue(t, a, db, "H1").SerialNumber)
	r, err := NewRegistry(RegistryConfig{
		DB: db, Clock: clock, CheckPeer: a.CheckPeer, Version: "1.2.3", ControllerID: "C1", Heartbeat: hb, Log: quietLog,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return &registryEnv{r: r, a: a, db: db, clock: clock, serial: serial}
}

// register registers s as H1's session with H1's serial.
func (e *registryEnv) register(t *testing.T, s *transport.Session) {
	t.Helper()
	if err := e.r.Register(context.Background(), "H1", e.serial, s); err != nil {
		t.Fatal(err)
	}
}

// livePair returns the two ends of one session over an in-memory
// connection, closed at the end of the test.
func livePair(t *testing.T) (ctrl, agent *transport.Session) {
	t.Helper()
	c1, c2 := net.Pipe()
	errs := make(chan error, 1)
	go func() {
		var err error
		agent, err = transport.AgentSession(c2)
		errs <- err
	}()
	ctrl, err := transport.ControllerSession(c1)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ctrl.Close()
		_ = agent.Close()
	})
	return ctrl, agent
}

// serveAgent serves the agent API over the streams the controller opens
// on s, and records the controller halves of the hellos it receives.
func serveAgent(t *testing.T, s *transport.Session, agent core.AgentClient) *helloLog {
	t.Helper()
	hellos := &helloLog{}
	srv := &http.Server{
		Handler:           api.NewHandler(api.Config{Agent: agent, Log: quietLog, OnHello: hellos.add}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = srv.Serve(s) }()
	t.Cleanup(func() { _ = srv.Close() })
	return hellos
}

type helloLog struct {
	mu   sync.Mutex
	list []api.ControllerHello
}

func (l *helloLog) add(c api.ControllerHello) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.list = append(l.list, c)
}

func (l *helloLog) all() []api.ControllerHello {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]api.ControllerHello(nil), l.list...)
}

// helloAgent answers Hello as hostID running version.
func helloAgent(hostID, version string) *coretest.FakeAgent {
	return &coretest.FakeAgent{HelloFunc: func(context.Context) (core.AgentHello, error) {
		return core.AgentHello{ProtocolVersion: 1, BinaryVersion: version, HostID: hostID}, nil
	}}
}

// hostHello calls Hello through the registry's client of hostID.
func hostHello(t *testing.T, r *Registry, hostID string) (core.AgentHello, uint64) {
	t.Helper()
	c, gen, err := r.Client(context.Background(), hostID)
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Hello(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return h, gen
}

// wantOffline fails unless hostID's client answers host.offline.
func wantOffline(t *testing.T, r *Registry, hostID string) {
	t.Helper()
	_, _, err := r.Client(context.Background(), hostID)
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.HostOffline || coded.Detail != "host "+hostID+" is offline" {
		t.Fatalf("Client(%s) error = %v, want host.offline", hostID, err)
	}
}

// eventually polls cond every few milliseconds for up to five seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ended reports whether s has ended.
func ended(s *transport.Session) bool {
	select {
	case <-s.Done():
		return true
	default:
		return false
	}
}

// lastSeen returns core_hosts.last_seen_at of hostID, "" while it is null.
func lastSeen(t *testing.T, db *store.DB, hostID string) string {
	t.Helper()
	var v *string
	if err := db.Read().QueryRow(`SELECT last_seen_at FROM core_hosts WHERE id = ?`, hostID).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v == nil {
		return ""
	}
	return *v
}

func TestRegistryOfflineHost(t *testing.T) {
	e := newRegistry(t, transport.HeartbeatConfig{Interval: time.Hour})
	wantOffline(t, e.r, "H1")
	e.r.Disconnect("H1")
	e.r.Disconnect("unknown")
	if got := lastSeen(t, e.db, "H1"); got != "" {
		t.Fatalf("last_seen_at = %q before any session", got)
	}
}

func TestRegistryServesTheSession(t *testing.T) {
	e := newRegistry(t, transport.HeartbeatConfig{Interval: time.Hour})
	ctrl, agent := livePair(t)
	hellos := serveAgent(t, agent, helloAgent("H1", "1.2.0"))
	e.register(t, ctrl)

	h, gen := hostHello(t, e.r, "H1")
	if h.HostID != "H1" || h.BinaryVersion != "1.2.0" || gen == 0 {
		t.Fatalf("hello = %+v, generation %d", h, gen)
	}
	want := api.ControllerHello{Protocol: 1, Version: "1.2.3", ControllerID: "C1"}
	if got := hellos.all(); len(got) != 1 || got[0] != want {
		t.Fatalf("the agent received the controller hellos %+v, want one %+v", got, want)
	}
	wantOffline(t, e.r, "H2")
}

func TestRegistryReplacesTheSession(t *testing.T) {
	e := newRegistry(t, transport.HeartbeatConfig{Interval: time.Hour})
	ctrl1, agent1 := livePair(t)
	serveAgent(t, agent1, helloAgent("H1", "first"))
	e.register(t, ctrl1)
	_, gen1 := hostHello(t, e.r, "H1")

	ctrl2, agent2 := livePair(t)
	serveAgent(t, agent2, helloAgent("H1", "second"))
	e.register(t, ctrl2)
	if !ended(ctrl1) {
		t.Fatal("the replaced session is still open")
	}
	eventually(t, "the agent to see its replaced session end", func() bool { return ended(agent1) })
	h, gen2 := hostHello(t, e.r, "H1")
	if h.BinaryVersion != "second" || gen2 <= gen1 {
		t.Fatalf("after the replacement: hello %+v, generation %d after %d", h, gen2, gen1)
	}

	// The replaced session's end must not take the new one with it.
	time.Sleep(20 * time.Millisecond)
	if h, gen := hostHello(t, e.r, "H1"); h.BinaryVersion != "second" || gen != gen2 {
		t.Fatalf("the replaced session's end changed the client: %+v, generation %d", h, gen)
	}

	_ = agent2.Close()
	eventually(t, "the ended session to leave the registry", func() bool {
		_, _, err := e.r.Client(context.Background(), "H1")
		return err != nil
	})
	wantOffline(t, e.r, "H1")
}

func TestRegistryGenerationsNeverRepeat(t *testing.T) {
	e := newRegistry(t, transport.HeartbeatConfig{Interval: time.Hour})
	seen := map[uint64]bool{}
	for i := range 3 {
		ctrl, agent := livePair(t)
		serveAgent(t, agent, helloAgent("H1", "v"))
		e.register(t, ctrl)
		_, gen, err := e.r.Client(context.Background(), "H1")
		if err != nil || gen == 0 || seen[gen] {
			t.Fatalf("session %d: generation %d, %v (seen %v)", i, gen, err, seen)
		}
		seen[gen] = true
		e.r.Disconnect("H1")
	}
}

func TestRegistryDisconnect(t *testing.T) {
	e := newRegistry(t, transport.HeartbeatConfig{Interval: time.Hour})
	ctrl, agent := livePair(t)
	serveAgent(t, agent, helloAgent("H1", "v"))
	e.register(t, ctrl)
	hostHello(t, e.r, "H1")

	e.r.Disconnect("H1")
	if !ended(ctrl) {
		t.Fatal("Disconnect left the session open")
	}
	wantOffline(t, e.r, "H1")
	eventually(t, "the agent to see its session end", func() bool { return ended(agent) })
}

// TestRegistryRefusesARevocationBeforeRegister interleaves a revocation
// between the handshake's peer check and Register: its Disconnect finds no
// session, so Register itself must refuse the session.
func TestRegistryRefusesARevocationBeforeRegister(t *testing.T) {
	e := newRegistry(t, transport.HeartbeatConfig{Interval: time.Hour})
	ctx := context.Background()
	if err := e.a.CheckPeer(ctx, "H1", e.serial); err != nil {
		t.Fatalf("the handshake's check refused H1: %v", err)
	}
	if err := e.db.Write(ctx, func(tx *sql.Tx) error { return RevokeHost(ctx, tx, "H1", e.clock.Now()) }); err != nil {
		t.Fatal(err)
	}
	e.r.Disconnect("H1")

	ctrl, agent := livePair(t)
	serveAgent(t, agent, helloAgent("H1", "v"))
	err := e.r.Register(ctx, "H1", e.serial, ctrl)
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.Forbidden {
		t.Fatalf("Register of a revoked host = %v, want auth.forbidden", err)
	}
	if !ended(ctrl) {
		t.Fatal("the refused session is still open")
	}
	wantOffline(t, e.r, "H1")
	eventually(t, "the agent to see the refused session end", func() bool { return ended(agent) })
}

func TestRegistryLastSeen(t *testing.T) {
	e := newRegistry(t, transport.HeartbeatConfig{Interval: time.Hour})
	ctrl, agent := livePair(t)
	serveAgent(t, agent, helloAgent("H1", "v"))
	e.register(t, ctrl)

	steps := []struct {
		advance time.Duration
		want    time.Time
	}{
		{0, testStart},
		{LastSeenInterval - time.Second, testStart},
		{time.Second, testStart.Add(LastSeenInterval)},
		{time.Hour, testStart.Add(LastSeenInterval + time.Hour)},
	}
	for i, s := range steps {
		e.clock.Advance(s.advance)
		hostHello(t, e.r, "H1")
		e.r.writes.Wait()
		if got, want := lastSeen(t, e.db, "H1"), formatTime(s.want); got != want {
			t.Fatalf("step %d: last_seen_at = %q, want %q", i, got, want)
		}
	}
}

func TestRegistryHeartbeatMarksTheHostSeen(t *testing.T) {
	e := newRegistry(t, transport.HeartbeatConfig{Interval: 5 * time.Millisecond})
	ctrl, agent := livePair(t)
	serveAgent(t, agent, helloAgent("H1", "v"))
	e.register(t, ctrl)
	eventually(t, "a ping to mark the host seen", func() bool { return lastSeen(t, e.db, "H1") == formatTime(testStart) })
}

func TestRegistryClose(t *testing.T) {
	e := newRegistry(t, transport.HeartbeatConfig{Interval: time.Hour})
	ctrl, agent := livePair(t)
	serveAgent(t, agent, helloAgent("H1", "v"))
	e.register(t, ctrl)

	e.r.Close()
	if !ended(ctrl) {
		t.Fatal("Close left a session open")
	}
	wantOffline(t, e.r, "H1")
	late, _ := livePair(t)
	if err := e.r.Register(context.Background(), "H1", e.serial, late); err == nil || !ended(late) {
		t.Fatalf("a session registered after Close: error %v, ended %v", err, ended(late))
	}
	wantOffline(t, e.r, "H1")
	e.r.Close()
}

func TestNewRegistryRefusesBadConfig(t *testing.T) {
	a, db, clock := newAuthority(t)
	good := RegistryConfig{DB: db, Clock: clock, CheckPeer: a.CheckPeer, Version: "1.2.3", ControllerID: "C1"}
	cases := map[string]func(*RegistryConfig){
		"negative interval": func(c *RegistryConfig) { c.Heartbeat.Interval = -time.Second },
		"negative timeout":  func(c *RegistryConfig) { c.Heartbeat.Timeout = -time.Second },
		"negative failures": func(c *RegistryConfig) { c.Heartbeat.Failures = -1 },
		"no version":        func(c *RegistryConfig) { c.Version = "" },
		"no controller id":  func(c *RegistryConfig) { c.ControllerID = "" },
		"no database":       func(c *RegistryConfig) { c.DB = nil },
		"no clock":          func(c *RegistryConfig) { c.Clock = nil },
		"no peer check":     func(c *RegistryConfig) { c.CheckPeer = nil },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := good
			change(&cfg)
			if r, err := NewRegistry(cfg); err == nil {
				r.Close()
				t.Fatal("NewRegistry accepted the configuration")
			}
		})
	}
	r, err := NewRegistry(good)
	if err != nil {
		t.Fatalf("NewRegistry refused the zero heartbeat: %v", err)
	}
	r.Close()
}

// honLog records the OnOnline calls as "<host> <online>".
type honLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *honLog) add(hostID string, online bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, fmt.Sprintf("%s %v", hostID, online))
}

func (l *honLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.calls...)
}

// want fails unless the calls so far are exactly calls.
func (l *honLog) want(t *testing.T, calls ...string) {
	t.Helper()
	if got := l.all(); !slices.Equal(got, calls) {
		t.Fatalf("OnOnline calls = %q, want %q", got, calls)
	}
}

// honRegistry is newRegistry with its OnOnline calls recorded.
func honRegistry(t *testing.T) (*registryEnv, *honLog) {
	t.Helper()
	a, db, clock := newAuthority(t)
	addHost(t, db, "H1", core.HostEnrolled)
	serial := transport.SerialText(issue(t, a, db, "H1").SerialNumber)
	l := &honLog{}
	r, err := NewRegistry(RegistryConfig{
		DB: db, Clock: clock, CheckPeer: a.CheckPeer, Version: "1.2.3", ControllerID: "C1",
		Heartbeat: transport.HeartbeatConfig{Interval: time.Hour}, Log: quietLog, OnOnline: l.add,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return &registryEnv{r: r, a: a, db: db, clock: clock, serial: serial}, l
}

func TestRegistryOnOnline(t *testing.T) {
	e, l := honRegistry(t)
	session := func() (ctrl, agent *transport.Session) {
		ctrl, agent = livePair(t)
		serveAgent(t, agent, helloAgent("H1", "v"))
		e.register(t, ctrl)
		return ctrl, agent
	}

	_, agent1 := session()
	l.want(t, "H1 true")
	_, agent2 := session()
	eventually(t, "the replaced session to end", func() bool { return ended(agent1) })
	time.Sleep(20 * time.Millisecond) // the replaced session's watcher has run
	l.want(t, "H1 true")
	_ = agent2.Close()
	eventually(t, "the ended session to go offline", func() bool { return len(l.all()) == 2 })
	l.want(t, "H1 true", "H1 false")

	_, agent3 := session()
	e.r.Disconnect("H1")
	eventually(t, "the disconnected session to end", func() bool { return ended(agent3) })
	time.Sleep(20 * time.Millisecond)
	l.want(t, "H1 true", "H1 false", "H1 true", "H1 false")

	session()
	e.r.Close()
	l.want(t, "H1 true", "H1 false", "H1 true", "H1 false", "H1 true", "H1 false")
}

// peerTCPPair returns the two ends of one session over a TCP connection on
// 127.0.0.1, closed at the end of the test.
func peerTCPPair(t *testing.T) (ctrl, agent *transport.Session) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	dialed, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := ln.Accept()
	if err != nil {
		_ = dialed.Close()
		t.Fatal(err)
	}
	if ctrl, err = transport.ControllerSession(conn); err != nil {
		t.Fatal(err)
	}
	if agent, err = transport.AgentSession(dialed); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ctrl.Close()
		_ = agent.Close()
	})
	return ctrl, agent
}

// peerRegister enrolls hostID with a certificate and registers s as its
// session.
func (e *registryEnv) peerRegister(t *testing.T, hostID string, s *transport.Session) {
	t.Helper()
	addHost(t, e.db, hostID, core.HostEnrolled)
	serial := transport.SerialText(issue(t, e.a, e.db, hostID).SerialNumber)
	if err := e.r.Register(context.Background(), hostID, serial, s); err != nil {
		t.Fatal(err)
	}
}

// peersLog is a slog handler's output, safe for the registry's goroutines.
type peersLog struct {
	mu sync.Mutex
	b  []byte
}

func (l *peersLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.b = append(l.b, p...)
	return len(p), nil
}

func (l *peersLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return string(l.b)
}

// TestRegistryPeers: the TCP sessions' addresses come once each; the pipe
// session, which has no IP address, is dropped by value and logged at Warn,
// so the guard topology can mark itself incomplete.
func TestRegistryPeers(t *testing.T) {
	e := newRegistry(t, transport.HeartbeatConfig{Interval: time.Hour})
	logs := &peersLog{}
	e.r.cfg.Log = slog.New(slog.NewTextHandler(logs, nil))
	if got, dropped := e.r.Peers(); len(got) != 0 || len(dropped) != 0 {
		t.Fatalf("Peers without a session = %v, dropped %v; want none", got, dropped)
	}
	ctrl1, agent1 := peerTCPPair(t)
	ctrl2, agent2 := peerTCPPair(t)
	pipe, pipeAgent := livePair(t)
	e.register(t, ctrl1)
	e.peerRegister(t, "H2", ctrl2)
	e.peerRegister(t, "H3", pipe)
	got, dropped := e.r.Peers()
	if want := []netip.Addr{netip.MustParseAddr("127.0.0.1")}; !slices.Equal(got, want) {
		t.Errorf("Peers = %v, want %v: the two TCP sessions once", got, want)
	}
	if want := []string{"pipe"}; !slices.Equal(dropped, want) {
		t.Errorf("dropped = %v, want %v: the pipe session has no IP address", dropped, want)
	}
	if out := logs.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, "addr=pipe") {
		t.Errorf("logs %q, want a warning naming addr=pipe", out)
	}
	for _, s := range []*transport.Session{agent1, agent2, pipeAgent} {
		_ = s.Close()
	}
	eventually(t, "the ended sessions to leave the registry", func() bool {
		got, dropped := e.r.Peers()
		return len(got) == 0 && len(dropped) == 0
	})
}

// TestRegistryDialsModuleStreams holds every module stream slot open and
// then needs every action slot at once: that works only when the client
// opens streams with OpenModule.
func TestRegistryDialsModuleStreams(t *testing.T) {
	e := newRegistry(t, transport.HeartbeatConfig{Interval: time.Hour})
	ctrl, agent := livePair(t)
	release := make(chan struct{})
	var reads atomic.Int32
	fake := helloAgent("H1", "1.2.0")
	fake.OpenStreamFunc = func(ctx context.Context, _ core.Principal, _, _ string, _ url.Values) (io.ReadCloser, error) {
		pr, pw := io.Pipe()
		go func() {
			_, _ = io.WriteString(pw, `{"data":{}}`+"\n")
			<-ctx.Done()
			_ = pw.CloseWithError(ctx.Err())
		}()
		return pr, nil
	}
	fake.ReadFunc = func(context.Context, core.Principal, core.ActionRef, json.RawMessage) (json.RawMessage, error) {
		reads.Add(1)
		<-release
		return json.RawMessage(`{}`), nil
	}
	serveAgent(t, agent, fake)
	e.register(t, ctrl)
	c, _, err := e.r.Client(context.Background(), "H1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for range transport.MaxModuleStreams {
		body, err := c.OpenStream(ctx, core.Principal{Actor: "user:u1"}, "sm", "logs", nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = body.Close() })
		if _, err := body.Read(make([]byte, 64)); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for range transport.MaxActionStreams {
		wg.Go(func() {
			if _, err := c.Read(ctx, core.Principal{Actor: "user:u1"}, core.ActionRef{Name: "sm.item.get", Version: 1}, json.RawMessage(`{}`)); err != nil {
				t.Error(err)
			}
		})
	}
	eventually(t, "every read to reach the agent while the streams are open", func() bool {
		return reads.Load() == transport.MaxActionStreams
	})
	close(release)
	wg.Wait()
}
