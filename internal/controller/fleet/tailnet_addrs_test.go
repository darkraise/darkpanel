package fleet

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"maps"
	"net"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
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
	return newTailnetEnvOn(t, openControllerDB(t))
}

// newTailnetEnvOn is newTailnetEnv over db, adding host H1 unless db holds it.
func newTailnetEnvOn(t *testing.T, db *store.DB) *tailnetEnv {
	t.Helper()
	clock := coretest.NewFakeClock(testStart)
	a, err := LoadOrCreateAuthority(context.Background(), db, newSealer(t), clock)
	if err != nil {
		t.Fatal(err)
	}
	var hosts int
	if err := db.Read().QueryRow(`SELECT count(*) FROM core_hosts WHERE id = 'H1'`).Scan(&hosts); err != nil {
		t.Fatal(err)
	}
	if hosts == 0 {
		addHost(t, db, "H1", core.HostEnrolled)
	}
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

// logTo makes the registry log at Info and above to a buffer it returns.
func (e *tailnetEnv) logTo() *peersLog {
	logs := &peersLog{}
	e.r.cfg.Log = slog.New(slog.NewTextHandler(logs, nil))
	return logs
}

// notRecordedMsg is C3's log message.
const notRecordedMsg = `msg="fleet: the host's agent session is not from a tailnet address; it is not recorded"`

func TestAddressesPersistAtConnect(t *testing.T) {
	ctx := context.Background()
	e := newTailnetEnv(t)
	t0 := formatTime(e.env.clock.Now())

	// Over the tailnet: the session's own address, with no stable id; the
	// host's tailscale.status is never read.
	e.answer(`{"backendState":"Running","self":{"id":"nH1","addresses":["100.64.0.7","fd7a:115c:a1e0::7"]}}`, nil)
	s := e.connect(t, "100.64.0.7:41000")
	want := map[string]string{"100.64.0.7": " " + t0}
	e.waitRows(t, want)

	// Over the LAN: nothing is recorded, whatever the status says.
	e.env.clock.Advance(time.Minute)
	e.answer(`{"backendState":"Running","self":{"id":"nH1","addresses":["100.64.0.8"]}}`, nil)
	_ = s.Close()
	s = e.connect(t, "192.168.1.10:41000")

	// The status read would fail: the session's tailnet address is recorded
	// all the same, and every earlier row stays.
	e.env.clock.Advance(time.Minute)
	t2 := formatTime(e.env.clock.Now())
	e.answer("", errcode.New(errcode.ModuleUnavailable, "module tailscale is disabled on host H1"))
	_ = s.Close()
	s = e.connect(t, "100.127.255.254:41000")
	want["100.127.255.254"] = " " + t2
	e.waitRows(t, want)

	// A known address seen again gets the new time; an IPv6 tailnet session
	// address is recorded.
	e.env.clock.Advance(time.Minute)
	t3 := formatTime(e.env.clock.Now())
	_ = s.Close()
	s = e.connect(t, "100.64.0.7:41000")
	want["100.64.0.7"] = " " + t3
	e.waitRows(t, want)
	_ = s.Close()
	s = e.connect(t, "[fd7a:115c:a1e0::99]:41000")
	want["fd7a:115c:a1e0::99"] = " " + t3
	e.waitRows(t, want)

	// Outside 100.64.0.0/10 and fd7a:115c:a1e0::/48 no session address is
	// recorded. A tailnet connect after each shows when its write is done.
	for _, remote := range []string{"100.128.0.1:41000", "100.63.255.255:41000", "[fd7a:115c:a1e1::1]:41000"} {
		e.env.clock.Advance(time.Minute)
		_ = s.Close()
		s = e.connect(t, remote)
		_ = s.Close()
		s = e.connect(t, "100.64.0.7:41000")
		want["100.64.0.7"] = " " + formatTime(e.env.clock.Now())
		e.waitRows(t, want)
	}

	// Offline: the rows stay.
	e.r.Disconnect("H1")
	_ = s.Close()
	e.r.Close()
	if got := e.rows(t); !maps.Equal(got, want) {
		t.Errorf("after the host went offline: rows %v, want %v", got, want)
	}
	e.mu.Lock()
	reads := append([]string(nil), e.reads...)
	e.mu.Unlock()
	if len(reads) != 0 {
		t.Errorf("reads %v, want none: the host's own report is never asked for", reads)
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
		{"a session address", []netip.Addr{a("100.99.0.1"), a("fd7a:115c:a1e0::7")}, "", false},
		{"a recorded session address", []netip.Addr{a("100.99.0.1"), a("fd7a:115c:a1e0::99")}, "", true},
		{"a recorded session address, mapped", []netip.Addr{a("::ffff:100.127.255.254")}, "nOther", true},
		{"a self-reported address", []netip.Addr{a("100.64.0.8")}, "nOther", false},
		{"the self-reported stable id", []netip.Addr{a("100.99.0.1")}, "nH1", false},
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

// TestRecordTailnetFromSessionOnly: a connect records only the address the
// controller saw the session come from, never what the host reports about
// itself, and says so for a session from outside the tailnet (A7, A8).
func TestRecordTailnetFromSessionOnly(t *testing.T) {
	const self = `{"backendState":"Running","self":{"id":"nOther","addresses":["100.64.0.9"]}}`
	for _, tc := range []struct {
		name   string
		remote string
		want   map[string]string // addr -> stable id
		logged string            // the addr C3 names, or "" for no C3 line
	}{
		{"an IPv4 tailnet session", "100.64.0.7:41000", map[string]string{"100.64.0.7": ""}, ""},
		{"an IPv6 tailnet session", "[fd7a:115c:a1e0::7]:41000", map[string]string{"fd7a:115c:a1e0::7": ""}, ""},
		{"a LAN session", "192.168.1.20:41000", map[string]string{}, "192.168.1.20"},
		{"a loopback session", "127.0.0.1:41000", map[string]string{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTailnetEnv(t)
			logs := e.logTo()
			e.answer(self, nil)
			s := e.connect(t, tc.remote)
			want := map[string]string{}
			for addr, stable := range tc.want {
				want[addr] = stable + " " + formatTime(e.env.clock.Now())
			}
			if len(want) > 0 {
				e.waitRows(t, want)
			}
			// Close waits for the connect's recording to end.
			_ = s.Close()
			e.r.Close()
			if got := e.rows(t); !maps.Equal(got, want) {
				t.Errorf("rows %v, want %v", got, want)
			}
			out := logs.String()
			lines := strings.Count(out, notRecordedMsg)
			switch {
			case tc.logged == "" && lines != 0:
				t.Errorf("logs %q, want no line saying the session is not recorded", out)
			case tc.logged != "" && (lines != 1 || !strings.Contains(out, "level=INFO "+notRecordedMsg+" host=H1 addr="+tc.logged)):
				t.Errorf("logs %q, want one Info line %s with host=H1 addr=%s", out, notRecordedMsg, tc.logged)
			}
		})
	}

	// A second connect from the same address updates seen_at and keeps one row.
	e := newTailnetEnv(t)
	e.answer(self, nil)
	s := e.connect(t, "100.64.0.7:41000")
	e.waitRows(t, map[string]string{"100.64.0.7": " " + formatTime(e.env.clock.Now())})
	e.env.clock.Advance(time.Minute)
	_ = s.Close()
	_ = e.connect(t, "100.64.0.7:41000")
	e.waitRows(t, map[string]string{"100.64.0.7": " " + formatTime(e.env.clock.Now())})
}

// TestRecordTailnetAfterMigration: 0019 drops the rows an older binary wrote
// from the host's own report, and the host's next connect records its session
// address again, with no stable id (A9).
func TestRecordTailnetAfterMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "controller.db")
	old, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	set, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	files, err := fs.ReadDir(set.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Name() >= "0019" {
			continue
		}
		data, err := fs.ReadFile(set.FS, f.Name())
		if err != nil {
			t.Fatal(err)
		}
		before[f.Name()] = &fstest.MapFile{Data: data}
	}
	opts := store.MigrateOptions{
		Side: store.SideController, BinaryVersion: "old",
		BackupRoot: filepath.Join(filepath.Dir(path), "backups"), Keep: 5, Now: time.Now(),
	}
	if _, err := store.Migrate(ctx, old, opts, store.MigrationSet{Module: set.Module, FS: before}, nil); err != nil {
		t.Fatal(err)
	}
	addHost(t, old, "H1", core.HostEnrolled)
	if _, err := old.Writer().ExecContext(ctx, `INSERT INTO core_host_tailnet (host_id, addr, stable_id, seen_at)
		VALUES ('H1', '100.64.0.7', 'nH1', '2026-11-24T00:00:00.000000000Z'), ('H1', '100.64.0.9', 'nH1', '2026-11-24T00:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	e := newTailnetEnvOn(t, openControllerDBAt(t, path))
	if got := e.rows(t); len(got) != 0 {
		t.Fatalf("rows after 0019 = %v, want none", got)
	}
	e.answer(`{"backendState":"Running","self":{"id":"nH1","addresses":["100.64.0.9"]}}`, nil)
	_ = e.connect(t, "100.64.0.7:41000")
	e.waitRows(t, map[string]string{"100.64.0.7": " " + formatTime(e.env.clock.Now())})
}
