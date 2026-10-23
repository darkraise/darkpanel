package dispatch

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/schemas"
)

// logRecords is a slog.Handler that keeps each record as
// "<level> <message> host=<host attribute>".
type logRecords struct {
	mu  sync.Mutex
	got []string
}

func (l *logRecords) Enabled(context.Context, slog.Level) bool { return true }

func (l *logRecords) Handle(_ context.Context, r slog.Record) error {
	host := ""
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "host" {
			host = a.Value.String()
		}
		return true
	})
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, fmt.Sprintf("%s %s host=%s", r.Level, r.Message, host))
	return nil
}

func (l *logRecords) WithAttrs([]slog.Attr) slog.Handler { return l }

func (l *logRecords) WithGroup(string) slog.Handler { return l }

func (l *logRecords) lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.got)
}

// agentRow is the part of a core_hosts row Hello writes; "<null>" stands for
// NULL.
type agentRow struct {
	Version, OS, Arch, LastSeen string
	Protocol                    int64
}

func (h *harness) agentRow(hostID string) agentRow {
	h.t.Helper()
	var version, osName, arch, seen sql.NullString
	var protocol sql.NullInt64
	if err := h.db.Read().QueryRow(`SELECT agent_version, protocol_version, os, arch, last_seen_at FROM core_hosts WHERE id = ?`,
		hostID).Scan(&version, &protocol, &osName, &arch, &seen); err != nil {
		h.t.Fatal(err)
	}
	text := func(s sql.NullString) string {
		if !s.Valid {
			return "<null>"
		}
		return s.String
	}
	return agentRow{Version: text(version), OS: text(osName), Arch: text(arch), LastSeen: text(seen), Protocol: protocol.Int64}
}

func TestHelloRecordsTheAgent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.addHost("h2")
	hello := core.AgentHello{ProtocolVersion: 1, BinaryVersion: "1.2.3", OS: "linux", Arch: "arm64", HostID: "h2",
		JournalEpoch: "01EPOCHA"}
	h.clients.Add("h2", &coretest.FakeAgent{HelloFunc: func(context.Context) (core.AgentHello, error) { return hello, nil }})
	if got := h.hosts.JournalEpoch("h2"); got != "" {
		t.Errorf("epoch before Hello = %q, want empty", got)
	}
	h.clock.Advance(time.Minute)
	if _, err := h.hosts.Agent(ctx, "h2"); err != nil {
		t.Fatal(err)
	}
	want := agentRow{Version: "1.2.3", OS: "linux", Arch: "arm64", LastSeen: "2026-10-17T12:01:00.000000000Z", Protocol: 1}
	if got := h.agentRow("h2"); got != want {
		t.Errorf("core_hosts = %+v, want %+v", got, want)
	}
	if got := h.hosts.JournalEpoch("h2"); got != "01EPOCHA" {
		t.Errorf("epoch = %q, want 01EPOCHA", got)
	}

	// A reconnected agent says Hello again; an empty field is stored as NULL.
	hello.BinaryVersion, hello.Arch, hello.JournalEpoch = "1.3.0", "", "01EPOCHB"
	h.clients.Add("h2", &coretest.FakeAgent{HelloFunc: func(context.Context) (core.AgentHello, error) { return hello, nil }})
	h.clock.Advance(time.Minute)
	if _, err := h.hosts.Agent(ctx, "h2"); err != nil {
		t.Fatal(err)
	}
	want = agentRow{Version: "1.3.0", OS: "linux", Arch: "<null>", LastSeen: "2026-10-17T12:02:00.000000000Z", Protocol: 1}
	if got := h.agentRow("h2"); got != want {
		t.Errorf("after a reconnect core_hosts = %+v, want %+v", got, want)
	}
	if got := h.hosts.JournalEpoch("h2"); got != "01EPOCHB" {
		t.Errorf("epoch after a reconnect = %q, want 01EPOCHB", got)
	}
	var state string
	if err := h.db.Read().QueryRow(`SELECT state FROM core_hosts WHERE id = 'h2'`).Scan(&state); err != nil || state != core.HostPending {
		t.Errorf("state = %q, %v; Hello must not change it", state, err)
	}
}

var refCoreEcho = core.ActionRef{Name: "core.echo.get", Version: 1}

// TestCoreModuleSkipsEnablement: the core module has no core_module_state
// row, and its agent actions dispatch anyway.
func TestCoreModuleSkipsEnablement(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	echoAction := core.DefineRead(core.ActionSpec{Name: refCoreEcho.Name, Version: 1, Side: core.SideAgent}, echo{})
	var err error
	if h.reg, err = core.NewRegistry(append(modules(), coreactions.Module(coreactions.Deps{Extra: []core.ActionDef{echoAction}}))...); err != nil {
		t.Fatal(err)
	}
	h.hosts = NewHosts(HostsConfig{DB: h.db, Registry: h.reg, Clients: h.clients, Clock: h.clock, Log: slog.New(slog.DiscardHandler)})
	hash, err := schemas.ActionHash(echoAction)
	if err != nil {
		t.Fatal(err)
	}
	h.clients.Add(testHost, &coretest.FakeAgent{
		HelloFunc: func(ctx context.Context) (core.AgentHello, error) {
			hello, err := h.rt.Hello(ctx)
			hello.SchemaHashes = maps.Clone(hello.SchemaHashes)
			hello.SchemaHashes[schemas.HashKey(refCoreEcho)] = hash
			return hello, err
		},
		ReadFunc: func(_ context.Context, _ core.Principal, a core.ActionRef, _ json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"value":"` + a.Name + `"}`), nil
		},
	})
	d, err := New(h.config(&coretest.MemAudit{}))
	if err != nil {
		t.Fatal(err)
	}
	out, err := d.Read(ctx, alice, testHost, refCoreEcho, json.RawMessage(`{"name":"x"}`))
	if err != nil || string(out) != `{"value":"core.echo.get"}` {
		t.Errorf("Read = %s, %v; want the agent's answer", out, err)
	}
	if n := h.count("SELECT count(*) FROM core_module_state WHERE module_id = 'core'"); n != 0 {
		t.Errorf("core module rows = %d, want none", n)
	}
}

func TestChangesSkipOfflineHosts(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	logs := &logRecords{}
	d.cfg.Log = slog.New(logs)
	h.applyAgent(d, "a", core.ApplyOptions{})
	h.addHost("h9")
	vs, err := d.Changes(ctx, alice, core.ChangeFilter{})
	if err != nil || len(vs) != 1 || vs[0].HostID != testHost {
		t.Errorf("Changes = %+v, %v; want h1's one change", vs, err)
	}
	if got, want := logs.lines(), []string{"DEBUG dispatch: offline host skipped host=h9"}; !reflect.DeepEqual(got, want) {
		t.Errorf("logs = %q, want %q", got, want)
	}
	_, err = d.Changes(ctx, alice, core.ChangeFilter{HostID: "h9"})
	_ = wantCode(t, err, errcode.HostOffline)
}

func TestHelloPushesEnablement(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	var err error
	if h.reg, err = core.NewRegistry(append(modules(), coreactions.Module(coreactions.Deps{}))...); err != nil {
		t.Fatal(err)
	}
	logs := &logRecords{}
	h.hosts = NewHosts(HostsConfig{DB: h.db, Registry: h.reg, Clients: h.clients, Clock: h.clock, Log: slog.New(logs)})
	h.addHost("h2")
	// An operator disabled kv on h2 and enabled off, which is disabled by
	// default; flaky has no row yet and takes its DefaultEnabled.
	for module, enabled := range map[string]bool{"kv": false, "off": true} {
		if _, err := h.db.Writer().Exec(`INSERT INTO core_module_state (host_id, module_id, enabled, available, reason, checked_at)
			VALUES ('h2', ?, ?, 1, NULL, '2026-10-17T00:00:00.000000000Z')`, module, enabled); err != nil {
			t.Fatal(err)
		}
	}
	hello := core.AgentHello{ProtocolVersion: 1, BinaryVersion: "1.2.3", HostID: "h2", JournalEpoch: "01EPOCHA",
		Modules: []core.ModuleStatus{
			{ID: "core", Version: 1, Available: true}, {ID: "kv", Version: 1, Available: true},
			{ID: "off", Version: 1, Available: true}, {ID: "flaky", Version: 1, Reason: "flaky: the host is not ready"},
		}}
	var pushed []string
	refuse := ""
	var refuseErr error
	agent := &coretest.FakeAgent{
		HelloFunc: func(context.Context) (core.AgentHello, error) { return hello, nil },
		SetModuleEnabledFunc: func(_ context.Context, id string, enabled bool) error {
			pushed = append(pushed, fmt.Sprintf("%s=%t", id, enabled))
			if refuseErr != nil {
				return refuseErr
			}
			if id == refuse {
				return fmt.Errorf("the agent refused %s", id)
			}
			return nil
		},
	}
	want := []string{"kv=false", "off=true", "flaky=true"}
	h.clients.Add("h2", agent)
	if _, err := h.hosts.Agent(ctx, "h2"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pushed, want) {
		t.Errorf("pushes after the hello = %q, want %q", pushed, want)
	}

	// A reconnected agent that refuses one push still counts as greeted.
	pushed, refuse, hello.JournalEpoch = nil, "off", "01EPOCHB"
	h.clients.Add("h2", agent)
	if _, err := h.hosts.Agent(ctx, "h2"); err != nil {
		t.Fatalf("Agent after a refused push = %v, want the client", err)
	}
	if !reflect.DeepEqual(pushed, want) {
		t.Errorf("pushes after the second hello = %q, want %q", pushed, want)
	}
	if got := h.hosts.JournalEpoch("h2"); got != "01EPOCHB" {
		t.Errorf("epoch = %q, want the second hello's 01EPOCHB", got)
	}
	var warned []string
	for _, l := range logs.lines() {
		if strings.HasPrefix(l, "WARN") {
			warned = append(warned, l)
		}
	}
	if want := []string{"WARN dispatch: push module enablement host=h2"}; !reflect.DeepEqual(warned, want) {
		t.Errorf("warnings = %q, want %q", warned, want)
	}

	// The refused push is tried again at the next reach, on the same session.
	pushed, refuse = nil, ""
	if _, err := h.hosts.Agent(ctx, "h2"); err != nil {
		t.Fatalf("Agent after a refused push = %v, want the client", err)
	}
	if !reflect.DeepEqual(pushed, want) {
		t.Errorf("pushes at the next reach = %q, want %q", pushed, want)
	}
	if got := h.hosts.JournalEpoch("h2"); got != "01EPOCHB" {
		t.Errorf("epoch after the repush = %q, want 01EPOCHB", got)
	}
	pushed = nil
	if _, err := h.hosts.Agent(ctx, "h2"); err != nil {
		t.Fatal(err)
	}
	if len(pushed) != 0 {
		t.Errorf("pushes after a clean hello = %q, want none", pushed)
	}

	// An agent without the route cannot be helped by trying again.
	refuseErr = errcode.New(errcode.NotFound, "no such route")
	h.hosts.RepushEnablement("h2")
	if _, err := h.hosts.Agent(ctx, "h2"); err != nil {
		t.Fatal(err)
	}
	pushed = nil
	if _, err := h.hosts.Agent(ctx, "h2"); err != nil {
		t.Fatal(err)
	}
	if len(pushed) != 0 {
		t.Errorf("pushes after not_found = %q, want none", pushed)
	}
}
