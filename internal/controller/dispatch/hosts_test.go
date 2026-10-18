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
	h.hosts.Add("h2", &coretest.FakeAgent{HelloFunc: func(context.Context) (core.AgentHello, error) {
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

func TestAgentRefusesUnknownAndMismatchedHosts(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_, err := h.hosts.Agent(ctx, "h9")
	_ = wantCode(t, err, errcode.NotFound)
	h.hosts.Add("h2", &coretest.FakeAgent{HelloFunc: func(context.Context) (core.AgentHello, error) {
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
	h.hosts = NewHosts(HostsConfig{DB: h.db, Registry: h.reg, Clock: h.clock, Log: slog.New(slog.DiscardHandler)})
	h.hosts.Add(testHost, &coretest.FakeAgent{HelloFunc: func(context.Context) (core.AgentHello, error) {
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
