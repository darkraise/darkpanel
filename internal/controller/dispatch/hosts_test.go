package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

type stateRow struct {
	Enabled, Available bool
	Reason             string // "<null>" for NULL
	CheckedAt          string
}

func (h *harness) states() map[string]stateRow {
	h.t.Helper()
	rows, err := h.db.Read().Query(
		"SELECT module_id, enabled, available, reason, checked_at FROM core_module_state WHERE host_id = ?", testHost)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]stateRow{}
	for rows.Next() {
		var id string
		var r stateRow
		var reason sql.NullString
		if err := rows.Scan(&id, &r.Enabled, &r.Available, &reason, &r.CheckedAt); err != nil {
			h.t.Fatal(err)
		}
		r.Reason = "<null>"
		if reason.Valid {
			r.Reason = reason.String
		}
		out[id] = r
	}
	return out
}

func TestFirstReachWritesModuleState(t *testing.T) {
	h := newHarness(t)
	c, err := h.hosts.Agent(context.Background(), testHost)
	if err != nil {
		t.Fatal(err)
	}
	if c != h.rt {
		t.Errorf("Agent returned %v, want the runtime", c)
	}
	at := "2026-10-17T12:00:00.000000000Z"
	want := map[string]stateRow{
		"kv":    {true, true, "<null>", at},
		"bad":   {true, false, runtime.ReasonMigrationFailed, at},
		"off":   {false, true, "<null>", at},
		"flaky": {true, false, "flaky: the host is not ready", at},
		"ctl":   {true, true, "<null>", at},
	}
	if got := h.states(); !reflect.DeepEqual(got, want) {
		t.Errorf("module state =\n%+v\nwant\n%+v", got, want)
	}
}

func TestUsable(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.hosts.Agent(ctx, testHost); err != nil {
		t.Fatal(err)
	}
	if err := h.hosts.usable(ctx, testHost, "kv"); err != nil {
		t.Errorf("kv: %v, want usable", err)
	}
	for module, reason := range map[string]string{
		"bad":   runtime.ReasonMigrationFailed,
		"off":   ReasonDisabled,
		"flaky": "flaky: the host is not ready",
		"nope":  coreactions.ReasonNotReported,
	} {
		e := wantCode(t, h.hosts.usable(ctx, testHost, module), errcode.ModuleUnavailable)
		if want := "module " + module + " is unavailable on host h1: " + reason; e.Detail != want {
			t.Errorf("%s: detail %q, want %q", module, e.Detail, want)
		}
	}
	// Disabled wins over any availability.
	if _, err := h.db.Writer().Exec("UPDATE core_module_state SET enabled = 0 WHERE module_id = 'flaky'"); err != nil {
		t.Fatal(err)
	}
	e := wantCode(t, h.hosts.usable(ctx, testHost, "flaky"), errcode.ModuleUnavailable)
	if !strings.HasSuffix(e.Detail, ": "+ReasonDisabled) {
		t.Errorf("disabled flaky: detail %q", e.Detail)
	}
}

func TestHelloNeverChangesEnabled(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.hosts.Hello(ctx, testHost); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Writer().Exec(
		"UPDATE core_module_state SET enabled = 1 - enabled WHERE module_id IN ('kv', 'off')"); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(time.Minute)
	if err := h.hosts.Hello(ctx, testHost); err != nil {
		t.Fatal(err)
	}
	got := h.states()
	at := "2026-10-17T12:01:00.000000000Z"
	if got["kv"] != (stateRow{false, true, "<null>", at}) || got["off"] != (stateRow{true, true, "<null>", at}) {
		t.Errorf("after a second Hello: kv %+v, off %+v; want enabled kept and checked now", got["kv"], got["off"])
	}
}

func TestAgentSaysHelloOnceAndRetriesAFailure(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	hellos := 0
	fail := true
	h.addHost("h2")
	h.clients.Add("h2", &coretest.FakeAgent{HelloFunc: func(context.Context) (core.AgentHello, error) {
		hellos++
		if fail {
			return core.AgentHello{}, errors.New("agent offline")
		}
		return core.AgentHello{ProtocolVersion: 1, HostID: "h2"}, nil
	}})
	if _, err := h.hosts.Agent(ctx, "h2"); err == nil {
		t.Fatal("Agent succeeded although Hello failed")
	}
	fail = false
	for range 2 {
		if _, err := h.hosts.Agent(ctx, "h2"); err != nil {
			t.Fatal(err)
		}
	}
	if hellos != 2 {
		t.Errorf("Hello calls = %d, want 2 (one failure, one success)", hellos)
	}
}

// switchedClients is a ClientSource of one client at generation 1 whose
// lookup fails with err while err is set.
type switchedClients struct {
	client core.AgentClient
	err    error
}

func (s *switchedClients) Client(context.Context, string) (core.AgentClient, uint64, error) {
	if s.err != nil {
		return nil, 0, s.err
	}
	return s.client, 1, nil
}

// TestAFailedHelloForgetsTheGreeting: after a successful Hello, an explicit
// Hello that fails leaves no greeting behind, so the host's journal epoch is
// no longer reported and the next reach says Hello again on the same
// generation.
func TestAFailedHelloForgetsTheGreeting(t *testing.T) {
	cases := []struct {
		name   string
		answer func() (core.AgentHello, error) // the failing Hello; nil when the lookup fails
		lookup error
		hellos int // Hello calls over the first reach, the failure and the next reach
	}{
		{"Hello fails", func() (core.AgentHello, error) { return core.AgentHello{}, errors.New("agent offline") }, nil, 3},
		{"another host answers", func() (core.AgentHello, error) {
			return core.AgentHello{ProtocolVersion: 1, HostID: "h3"}, nil
		}, nil, 3},
		{"the client lookup fails", nil, errcode.New(errcode.HostOffline, "host h2 is offline"), 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			h.addHost("h2")
			hellos := 0
			var failing func() (core.AgentHello, error)
			src := &switchedClients{client: &coretest.FakeAgent{HelloFunc: func(context.Context) (core.AgentHello, error) {
				hellos++
				if failing != nil {
					return failing()
				}
				return core.AgentHello{ProtocolVersion: 1, HostID: "h2", JournalEpoch: "01EPOCHA"}, nil
			}}}
			hosts := NewHosts(HostsConfig{DB: h.db, Registry: h.reg, Clients: src, Clock: h.clock, Log: slog.New(slog.DiscardHandler)})
			if _, err := hosts.Agent(ctx, "h2"); err != nil {
				t.Fatal(err)
			}
			failing, src.err = c.answer, c.lookup
			if err := hosts.Hello(ctx, "h2"); err == nil {
				t.Fatal("Hello succeeded; want the failure")
			}
			if got := hosts.JournalEpoch("h2"); got != "" {
				t.Errorf("epoch after a failed Hello = %q, want empty", got)
			}
			failing, src.err = nil, nil
			if _, err := hosts.Agent(ctx, "h2"); err != nil {
				t.Fatal(err)
			}
			if hellos != c.hellos {
				t.Errorf("Hello calls = %d, want %d: the reach after a failed Hello says Hello again", hellos, c.hellos)
			}
		})
	}
}

func TestAgentRefusesUnknownAndMismatchedHosts(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_, err := h.hosts.Agent(ctx, "h9")
	_ = wantCode(t, err, errcode.NotFound)
	h.addHost("h2")
	h.clients.Add("h2", &coretest.FakeAgent{HelloFunc: func(context.Context) (core.AgentHello, error) {
		return core.AgentHello{ProtocolVersion: 1, HostID: "h3"}, nil
	}})
	_, err = h.hosts.Agent(ctx, "h2")
	var coded *errcode.Error
	if err == nil || errors.As(err, &coded) || !strings.Contains(err.Error(), "answered Hello as h3") {
		t.Errorf("Agent(h2) = %v, want a plain error naming the mismatch", err)
	}
	if n := h.count("SELECT count(*) FROM core_module_state WHERE host_id = 'h2'"); n != 0 {
		t.Errorf("rows of the mismatched host = %d, want 0", n)
	}
}

func TestHelloSkipsModulesTheRegistryLacks(t *testing.T) {
	h := newHarness(t)
	h.clients = NewStaticClients()
	h.hosts = NewHosts(HostsConfig{DB: h.db, Registry: h.reg, Clients: h.clients, Clock: h.clock, Log: slog.New(slog.DiscardHandler)})
	h.clients.Add(testHost, &coretest.FakeAgent{HelloFunc: func(context.Context) (core.AgentHello, error) {
		return core.AgentHello{ProtocolVersion: 1, HostID: testHost, Modules: []core.ModuleStatus{
			{ID: "kv", Version: 1, Available: true}, {ID: "ghost", Version: 1, Available: true},
			{ID: core.ModuleIDCore, Version: 1, Available: true},
		}}, nil
	}})
	if err := h.hosts.Hello(context.Background(), testHost); err != nil {
		t.Fatal(err)
	}
	if got := h.states(); len(got) != 1 || !got["kv"].Available {
		t.Errorf("module state = %+v, want only kv", got)
	}
}

func TestAgentSaysHelloAgainForANewGeneration(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	hellos := 0
	agent := func() *coretest.FakeAgent {
		return &coretest.FakeAgent{HelloFunc: func(context.Context) (core.AgentHello, error) {
			hellos++
			return core.AgentHello{ProtocolVersion: 1, HostID: "h2"}, nil
		}}
	}
	first := agent()
	h.addHost("h2")
	h.clients.Add("h2", first)
	for range 2 {
		c, err := h.hosts.Agent(ctx, "h2")
		if err != nil || c != first {
			t.Fatalf("Agent(h2) = %v, %v; want the first client", c, err)
		}
	}
	if hellos != 1 {
		t.Fatalf("Hello calls = %d, want 1 for one generation", hellos)
	}
	second := agent()
	h.clients.Add("h2", second)
	c, err := h.hosts.Agent(ctx, "h2")
	if err != nil || c != second {
		t.Fatalf("Agent(h2) after a new session = %v, %v; want the second client", c, err)
	}
	if hellos != 2 {
		t.Errorf("Hello calls = %d, want 2: a new generation says Hello again", hellos)
	}
}

// fixedClients is a ClientSource whose generation is chosen by the test.
type fixedClients struct {
	client core.AgentClient
	gen    uint64
}

func (f fixedClients) Client(context.Context, string) (core.AgentClient, uint64, error) {
	return f.client, f.gen, nil
}

func TestAgentSaysHelloForGenerationZero(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	hellos := 0
	h.addHost("h2")
	src := fixedClients{gen: 0, client: &coretest.FakeAgent{HelloFunc: func(context.Context) (core.AgentHello, error) {
		hellos++
		return core.AgentHello{ProtocolVersion: 1, HostID: "h2"}, nil
	}}}
	hosts := NewHosts(HostsConfig{DB: h.db, Registry: h.reg, Clients: src, Clock: h.clock, Log: slog.New(slog.DiscardHandler)})
	for range 2 {
		if _, err := hosts.Agent(ctx, "h2"); err != nil {
			t.Fatal(err)
		}
	}
	if hellos != 1 {
		t.Errorf("Hello calls = %d, want 1: generation 0 is greeted once, not never", hellos)
	}
}

func TestAHungHelloDelaysOnlyItsOwnHost(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.addHost("h2")
	started := make(chan struct{})
	release := make(chan struct{})
	h.clients.Add("h2", &coretest.FakeAgent{HelloFunc: func(context.Context) (core.AgentHello, error) {
		close(started)
		<-release
		return core.AgentHello{ProtocolVersion: 1, HostID: "h2"}, nil
	}})
	hung := make(chan error, 1)
	go func() {
		_, err := h.hosts.Agent(ctx, "h2")
		hung <- err
	}()
	released := false
	releaseOnce := func() {
		if !released {
			released = true
			close(release)
		}
	}
	defer releaseOnce()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the Hello of h2 never started")
	}

	other := make(chan error, 1)
	go func() {
		_, err := h.hosts.Agent(ctx, testHost)
		other <- err
	}()
	select {
	case err := <-other:
		if err != nil {
			t.Fatalf("Agent(%s) while h2's Hello hangs: %v", testHost, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("Agent(%s) is blocked by the hung Hello of h2", testHost)
	}
	releaseOnce()
	select {
	case err := <-hung:
		if err != nil {
			t.Errorf("Agent(h2) after the Hello returned: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Agent(h2) did not return after the Hello was released")
	}
}

func TestNewHostsRequiresClients(t *testing.T) {
	defer func() {
		if r := recover(); r != "dispatch: HostsConfig.Clients is required" {
			t.Errorf("recover() = %v, want the missing-Clients panic", r)
		}
	}()
	NewHosts(HostsConfig{})
}

// addHost inserts a core_hosts row for id, with no client.
func (h *harness) addHost(id string) {
	h.t.Helper()
	if _, err := h.db.Writer().Exec(
		"INSERT INTO core_hosts (id, name, is_local, created_at) VALUES (?, ?, 0, '2026-10-17T00:00:00Z')", id, id); err != nil {
		h.t.Fatal(err)
	}
}

func TestUnknownAndOfflineHosts(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.addHost("h8")
	tests := []struct {
		name, host string
		code       errcode.Code
		detail     string
	}{
		{"no core_hosts row", "h9", errcode.NotFound, "no host h9"},
		{"a row without a client", "h8", errcode.HostOffline, "host h8 is offline"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.hosts.Agent(ctx, tt.host)
			if e := wantCode(t, err, tt.code); e.Detail != tt.detail {
				t.Errorf("Agent detail = %q, want %q", e.Detail, tt.detail)
			}
			if e := wantCode(t, h.hosts.Hello(ctx, tt.host), tt.code); e.Detail != tt.detail {
				t.Errorf("Hello detail = %q, want %q", e.Detail, tt.detail)
			}
		})
	}
}
