package server

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/dispatch"
	"github.com/darkraise/darkpanel/internal/controller/events"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/registry"
)

// root is the controller socket's principal for uid 0 on host id.
func root(id string) core.Principal {
	return core.Principal{Actor: "local:root@" + id, Scope: core.ScopeAdmin, Source: "socket"}
}

var refDisable = core.ActionRef{Name: "core.module.disable", Version: 1}

func count(t *testing.T, s *Server, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.controllerDB.Read().QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// planDisable plans core.module.disable of hostinfo, which stores a plan.
func planDisable(t *testing.T, s *Server) core.PlanView {
	t.Helper()
	in := json.RawMessage(`{"hostId":"` + s.LocalHostID() + `","moduleId":"hostinfo"}`)
	v, err := s.dispatcher.Plan(context.Background(), root(s.LocalHostID()), "", refDisable, in, core.PlanOptions{})
	if err != nil {
		t.Fatalf("plan core.module.disable: %v", err)
	}
	return v
}

// TestStartSeedsAndServesModules shows the first start's module rows, seeded
// from DefaultEnabled by the agent's Hello, and a resource and a core action
// read through the dispatcher with the server's own registry.
func TestStartSeedsAndServesModules(t *testing.T) {
	s := start(t, embeddedOptions(t))
	ctx := context.Background()
	id := s.LocalHostID()
	if n := count(t, s, `SELECT count(*) FROM core_module_state WHERE host_id = ? AND module_id = 'hostinfo' AND enabled = 1 AND available = 1`, id); n != 1 {
		t.Fatalf("hostinfo module rows enabled and available on the local host: %d, want 1", n)
	}
	if _, ok := s.Registry().Resource("hostinfo.summary"); !ok {
		t.Fatal("the server's registry has no hostinfo.summary")
	}
	out, err := s.dispatcher.Resource(ctx, root(id), id, "hostinfo.summary")
	if err != nil {
		t.Fatalf("hostinfo.summary: %v", err)
	}
	var summary struct{ Hostname, DarkpanelVersion string }
	if err := json.Unmarshal(out, &summary); err != nil || summary.Hostname == "" || summary.DarkpanelVersion != "dev" {
		t.Errorf("hostinfo.summary = %s (%v), want a hostname and darkpanelVersion dev", out, err)
	}
	out, err = s.dispatcher.Read(ctx, root(id), "", core.ActionRef{Name: "core.host.list", Version: 1}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("core.host.list: %v", err)
	}
	var hosts struct {
		Hosts []struct {
			ID, Name string
			IsLocal  bool
			Modules  []struct {
				ID        string
				Enabled   bool
				Available bool
			}
		}
	}
	if err := json.Unmarshal(out, &hosts); err != nil {
		t.Fatal(err)
	}
	h := hosts.Hosts
	if len(h) != 1 || h[0].ID != id || h[0].Name != testHostname || !h[0].IsLocal ||
		!slices.ContainsFunc(h[0].Modules, func(m struct {
			ID        string
			Enabled   bool
			Available bool
		}) bool {
			return m.ID == "hostinfo" && m.Enabled && m.Available
		}) {
		t.Errorf("core.host.list = %s, want the local host with hostinfo enabled and available", out)
	}
	if n := count(t, s, `SELECT count(*) FROM core_module_state WHERE module_id = 'core'`); n != 0 {
		t.Errorf("the core module has %d state rows, want none", n)
	}
}

// TestRecoverRunsAtStart is register row 51: a controller change that a
// previous run left applying is settled by Recover before the server
// serves, and its audit row is written through the audit store.
func TestRecoverRunsAtStart(t *testing.T) {
	opts := testOptions(t)
	s, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	plan := planDisable(t, s)
	at := t0.Format(timeFormat)
	if _, err := s.controllerDB.Writer().ExecContext(context.Background(),
		`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state,
		 input_json, created_at, updated_at)
		 VALUES ('01CRASHED0000000000000000', ?, '', 'core.module.disable', 1, 'compensable', 'core.module.enable',
		 'local:root@h', 'applying', '{}', ?, ?)`, plan.PlanID, at, at); err != nil {
		t.Fatal(err)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	s = start(t, opts)
	// Recovery settles the change on a background goroutine after Start returns.
	var v core.ChangeView
	eventually(t, "the crashed change settled by recovery", nil, func() bool {
		v, err = s.dispatcher.Change(context.Background(), root(s.LocalHostID()), "01CRASHED0000000000000000")
		return err != nil || v.State != core.StateApplying
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.State != core.StateInterrupted || v.Error == nil || !strings.Contains(v.Error.Message, "interrupted by a restart") {
		t.Errorf("after restart the change is %s (%+v), want interrupted by a restart", v.State, v.Error)
	}
	if n := count(t, s, `SELECT count(*) FROM core_audit WHERE change_id = '01CRASHED0000000000000000' AND phase = 'interrupt'`); n != 1 {
		t.Errorf("interrupt audit rows: %d, want 1", n)
	}
}

// TestPurgesRunAtStartAndEveryTenMinutes is register row 51 and spec
// section 20: expired plans and sessions are purged at start and every
// PurgeInterval.
func TestPurgesRunAtStartAndEveryTenMinutes(t *testing.T) {
	opts := testOptions(t)
	clock := opts.Clock.(*coretest.FakeClock)
	ctx := context.Background()
	s, err := Start(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	planDisable(t, s)
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	clock.Advance(auth.SessionIdle) // the plan expired while the server was down
	s = start(t, opts)
	if n := count(t, s, `SELECT count(*) FROM core_plans`); n != 0 {
		t.Errorf("plans after a start: %d, want the expired plan purged", n)
	}

	planDisable(t, s)
	_, secret, err := s.authService.CreateUser(ctx, "admin", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	code, err := auth.TOTPCode(secret, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.authService.ActivateUser(ctx, "admin", code); err != nil {
		t.Fatal(err)
	}
	clock.Advance(auth.TOTPPeriod)
	code, _ = auth.TOTPCode(secret, clock.Now())
	if _, err := s.authService.Login(ctx, auth.LoginRequest{Username: "admin", Password: "correct horse battery", Code: code, Source: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	if count(t, s, `SELECT count(*) FROM core_plans`) != 1 || count(t, s, `SELECT count(*) FROM core_sessions`) != 1 {
		t.Fatal("want one plan and one session before the purges")
	}
	clock.Advance(PurgeInterval)
	if n := count(t, s, `SELECT count(*) FROM core_plans`); n != 0 {
		t.Errorf("plans after one purge interval: %d, want 0", n)
	}
	if n := count(t, s, `SELECT count(*) FROM core_sessions`); n != 1 {
		t.Errorf("sessions after one purge interval: %d, want the live one kept", n)
	}
	clock.Advance(auth.SessionIdle)
	if n := count(t, s, `SELECT count(*) FROM core_sessions`); n != 0 {
		t.Errorf("sessions an hour later: %d, want the idle session purged", n)
	}
}

// TestShutdownStopsTheTimers shows that after Shutdown no purge, ingestion,
// revert or listener timer of the server is left armed, with and without
// the embedded agent.
func TestShutdownStopsTheTimers(t *testing.T) {
	for name, opts := range map[string]Options{"controller only": testOptions(t), "embedded agent": embeddedOptions(t)} {
		t.Run(name, func(t *testing.T) {
			clock := opts.Clock.(*coretest.FakeClock)
			s, err := Start(context.Background(), opts)
			if err != nil {
				t.Fatal(err)
			}
			if clock.Pending() == 0 {
				t.Fatal("no timer is armed while the server runs")
			}
			if err := s.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
			if n := clock.Pending(); n != 0 {
				t.Errorf("%d timers armed after Shutdown, want 0", n)
			}
			clock.Advance(time.Hour) // nothing runs against the closed databases
		})
	}
}

// TestHostListAppliesTheSupportWindow shows the controller's version wired
// into the host actions: the embedded agent of the same development build is
// supported, and an agent reporting a release this build cannot compare
// with is not.
func TestHostListAppliesTheSupportWindow(t *testing.T) {
	s := start(t, embeddedOptions(t))
	supported := func() bool {
		t.Helper()
		out, err := s.dispatcher.Read(context.Background(), root(s.LocalHostID()), "", core.ActionRef{Name: "core.host.list", Version: 1}, json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("core.host.list: %v", err)
		}
		var list struct {
			Hosts []struct {
				Supported bool `json:"supported"`
			} `json:"hosts"`
		}
		if err := json.Unmarshal(out, &list); err != nil || len(list.Hosts) != 1 {
			t.Fatalf("core.host.list = %s (%v), want one host", out, err)
		}
		return list.Hosts[0].Supported
	}
	if !supported() {
		t.Error("the embedded agent of the same build is outside the support window")
	}
	if _, err := s.controllerDB.Writer().ExecContext(context.Background(),
		`UPDATE core_hosts SET agent_version = '0.1.0' WHERE id = ?`, s.LocalHostID()); err != nil {
		t.Fatal(err)
	}
	if supported() {
		t.Error("an agent at 0.1.0 is inside a dev controller's support window")
	}
}

// TestServerRegistryMatchesBuild compares the registry the server assembled
// with its real dependencies against registry.Build(), which the CLI and the
// schema generator use: the same actions, resources and schemas.
func TestServerRegistryMatchesBuild(t *testing.T) {
	s := start(t, testOptions(t))
	built, err := registry.Build()
	if err != nil {
		t.Fatal(err)
	}
	want, err := schema.Snapshots(built)
	if err != nil {
		t.Fatal(err)
	}
	got, err := schema.Snapshots(s.Registry())
	if err != nil {
		t.Fatal(err)
	}
	if !maps.EqualFunc(got, want, bytes.Equal) {
		t.Errorf("the server's registry snapshots %v differ from registry.Build()'s %v",
			slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
	}
}

func TestWakeCompositesPassesTheFollowedPhases(t *testing.T) {
	var woken [][]string
	wake := wakeComposites(func(ids ...string) { woken = append(woken, ids) })
	wake("h1", []core.JournalEntry{
		{Phase: "apply", ChangeID: "c1"}, {Phase: "confirm", ChangeID: "c2"}, {Phase: "interrupt", ChangeID: "c3"},
		{Phase: "revert", ChangeID: "c4"}, {Phase: "recover", ChangeID: "c5"}, {Phase: "undo", ChangeID: "c6"},
		{Phase: "resolve", ChangeID: "c7"}, {Phase: "release", ChangeID: "c8"}, {Phase: "apply", ChangeID: ""},
		{Phase: "undo", ChangeID: "c1"},
	})
	wake("h1", []core.JournalEntry{{Phase: "confirm", ChangeID: "c9"}})
	want := [][]string{{"c1", "c3", "c4", "c5", "c6", "c7", "c8"}}
	if !reflect.DeepEqual(woken, want) {
		t.Errorf("woken %v, want %v", woken, want)
	}
}

func TestCompositeRecoveryRunsAtStart(t *testing.T) {
	opts := testOptions(t)
	s, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !s.engine.CompositesRecovered() {
		t.Error("CompositesRecovered is false after a start with no composite")
	}
	plan := planDisable(t, s)
	at := t0.Format(timeFormat)
	const id = "01COMPOSITE00000000000000X"
	if _, err := s.controllerDB.Writer().ExecContext(context.Background(),
		`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state,
		 input_json, recovery_owner, created_at, updated_at)
		 VALUES (?, ?, '', 'cfx.deploy.add', 1, 'composite', '', 'local:root@h', 'undoing', '{}', 'composite', ?, ?)`,
		id, plan.PlanID, at, at); err != nil {
		t.Fatal(err)
	}
	if _, err := s.controllerDB.Writer().ExecContext(context.Background(),
		"INSERT INTO core_composites (change_id, direction, cursor, steps_deadline) VALUES (?, 'unwind', -1, ?)", id, at); err != nil {
		t.Fatal(err)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	s = start(t, opts)
	deadline := time.Now().Add(5 * time.Second)
	for {
		v, err := s.engine.Change(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if v.State == core.StateReverted && v.Owner == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("composite = %s owned by %q, want reverted by composite recovery", v.State, v.Owner)
		}
		time.Sleep(time.Millisecond)
	}
	if !s.engine.CompositesRecovered() {
		t.Error("CompositesRecovered is false after the recovered composite ended")
	}
	if n := count(t, s, "SELECT count(*) FROM core_audit WHERE change_id = ? AND phase = 'apply'", id); n != 1 {
		t.Errorf("audit rows of the composite's end = %d, want 1", n)
	}
}

// A controller-side release journals through the controller engine's
// journal, the audit.Store, whose phase CHECK now accepts release.
func TestControllerReleaseJournalsToTheAuditLog(t *testing.T) {
	s := start(t, testOptions(t))
	plan := planDisable(t, s)
	at := t0.Format(timeFormat)
	const id = "01RELEASE0000000000000000X"
	if _, err := s.controllerDB.Writer().ExecContext(context.Background(),
		`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state,
		 input_json, created_at, updated_at)
		 VALUES (?, ?, '', 'cfx.item.add', 1, 'compensable', 'cfx.item.remove', 'local:root@h', 'needs_repair', '{}', ?, ?)`,
		id, plan.PlanID, at, at); err != nil {
		t.Fatal(err)
	}
	res, err := s.engine.Release(context.Background(), root(s.LocalHostID()), id, "the disk was replaced", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Change.State != core.StateAbandoned {
		t.Errorf("released change = %s, want abandoned", res.Change.State)
	}
	if n := count(t, s, "SELECT count(*) FROM core_audit WHERE change_id = ? AND phase = 'release' AND result = 'ok'", id); n != 1 {
		t.Errorf("release audit rows = %d, want 1", n)
	}
}

// pubWant reads ch until an event of eventType arrives, within five
// seconds, and checks that its data marshals like want.
func pubWant(t *testing.T, ch <-chan events.Event, eventType string, want any) {
	t.Helper()
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type != eventType {
				continue
			}
			if string(ev.Data) != string(b) {
				t.Fatalf("%s event = %s, want %s", eventType, ev.Data, b)
			}
			return
		case <-deadline:
			t.Fatalf("no %s event", eventType)
		}
	}
}

// TestServerEventWiring shows the event bus fed by the controller engine's
// composite.step events, the change and module watchers over the
// controller database, and the ingester's agent changes next to its
// composite wake-ups.
func TestServerEventWiring(t *testing.T) {
	opts := embeddedOptions(t)
	clock := opts.Clock.(*coretest.FakeClock)
	s := start(t, opts)
	ctx := context.Background()
	ch, _ := s.events.Subscribe(t.Context())

	step := lifecycle.StepEvent{ChangeID: "C1", StepID: "s1", State: core.StepConfirmed, ChildChangeID: "K1"}
	s.engineConfig(s.registry).Publish(lifecycle.EventCompositeStep, step)
	pubWant(t, ch, events.TypeCompositeStep, step)

	plan := planDisable(t, s)
	const id = "01WIRED000000000000000000X"
	if _, err := s.controllerDB.Writer().ExecContext(ctx,
		`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state,
		 input_json, created_at, updated_at)
		 VALUES (?, ?, '', 'core.module.disable', 1, 'compensable', 'core.module.enable', 'local:root@h', 'completed', '{}', ?, ?)`,
		id, plan.PlanID, clock.Now().Format(timeFormat), clock.Now().Format(timeFormat)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.controllerDB.Writer().ExecContext(ctx,
		"UPDATE core_module_state SET enabled = 0 WHERE host_id = ? AND module_id = 'hostinfo'", s.LocalHostID()); err != nil {
		t.Fatal(err)
	}
	clock.Advance(events.PollInterval)
	pubWant(t, ch, events.TypeChange, events.ChangeEvent{ChangeID: id, Action: "core.module.disable", State: core.StateCompleted,
		UpdatedAt: clock.Now().Add(-events.PollInterval)})
	pubWant(t, ch, events.TypeModule, events.ModuleEvent{HostID: s.LocalHostID(), ModuleID: "hostinfo", Available: true})

	var woken []string
	onEntries := s.ingestedEntries(func(ids ...string) { woken = append(woken, ids...) },
		func(_ context.Context, hostID, changeID string) (core.ChangeView, error) {
			return core.ChangeView{ID: changeID, HostID: hostID, Action: "demo.item.add", State: core.StateCompleted, UpdatedAt: t0}, nil
		})
	onEntries("h1", []core.JournalEntry{{Phase: "apply", ChangeID: "A1"}, {Phase: "plan"}})
	if !slices.Equal(woken, []string{"A1"}) {
		t.Errorf("woken composites' changes = %v, want [A1]", woken)
	}
	pubWant(t, ch, events.TypeChange, events.ChangeEvent{HostID: "h1", ChangeID: "A1", Action: "demo.item.add",
		State: core.StateCompleted, UpdatedAt: t0})
}

// TestServerEventsHideBreakGlass: the bus carries no change of a break-glass
// action, from the controller's watcher or from ingested agent changes, as
// the network change routes hide them.
func TestServerEventsHideBreakGlass(t *testing.T) {
	opts := embeddedOptions(t)
	clock := opts.Clock.(*coretest.FakeClock)
	s := start(t, opts)
	ctx := context.Background()
	ch, _ := s.events.Subscribe(t.Context())

	at := clock.Now().Format(timeFormat)
	for _, c := range []struct{ id, action string }{
		{"01HIDDEN00000000000000000X", "core.user.reset-password"},
		{"01SHOWN000000000000000000X", "core.module.disable"},
	} {
		if _, err := s.controllerDB.Writer().ExecContext(ctx,
			`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state,
			 input_json, created_at, updated_at)
			 VALUES (?, ?, '', ?, 1, 'irreversible', '', 'local:root@h', 'completed', '{}', ?, ?)`,
			c.id, planDisable(t, s).PlanID, c.action, at, at); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(events.PollInterval)
	pubWant(t, ch, events.TypeChange, events.ChangeEvent{ChangeID: "01SHOWN000000000000000000X", Action: "core.module.disable",
		State: core.StateCompleted, UpdatedAt: clock.Now().Add(-events.PollInterval)})

	onEntries := s.ingestedEntries(func(...string) {},
		func(_ context.Context, hostID, changeID string) (core.ChangeView, error) {
			action := "demo.item.add"
			if changeID == "A1" {
				action = "core.user.reset-password"
			}
			return core.ChangeView{ID: changeID, HostID: hostID, Action: action, Version: 1, State: core.StateCompleted, UpdatedAt: t0}, nil
		})
	onEntries("h1", []core.JournalEntry{{Phase: "apply", ChangeID: "A1"}, {Phase: "apply", ChangeID: "A2"}})
	pubWant(t, ch, events.TypeChange, events.ChangeEvent{HostID: "h1", ChangeID: "A2", Action: "demo.item.add",
		State: core.StateCompleted, UpdatedAt: t0})
}

// TestServerPublishesHostEvents shows the session registry's OnOnline
// function of the server.
func TestServerPublishesHostEvents(t *testing.T) {
	bus := events.NewBus(nil)
	ch, _ := bus.Subscribe(t.Context())
	hostOnline(bus)("h1", true)
	pubWant(t, ch, events.TypeHost, events.HostEvent{HostID: "h1", Online: true})
	hostOnline(bus)("h1", false)
	pubWant(t, ch, events.TypeHost, events.HostEvent{HostID: "h1"})
}

func TestTopologyOf(t *testing.T) {
	web := []listen.Bound{{Addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8443}}}
	agent := []listen.Bound{
		{Addr: &net.TCPAddr{IP: net.IPv6unspecified, Port: 7443}},
		{Addr: &net.UnixAddr{Name: "/run/darkpanel/agent.sock", Net: "unix"}},
	}
	peers := []netip.Addr{netip.MustParseAddr("192.0.2.30")}
	want := dispatch.Topology{
		PanelListen: []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:8443")},
		AgentListen: []netip.AddrPort{netip.MustParseAddrPort("[::]:7443")},
		AgentPeers:  peers,
	}
	if got := topologyOf(web, nil, agent, peers); !reflect.DeepEqual(got, want) {
		t.Errorf("topologyOf = %+v, want %+v", got, want)
	}
	// A runtime listener is a panel address too; an IPv4-mapped one is unmapped.
	runtime := []netip.AddrPort{netip.MustParseAddrPort("[::ffff:172.31.255.1]:9443")}
	withRuntime := want
	withRuntime.PanelListen = []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:8443"), netip.MustParseAddrPort("172.31.255.1:9443")}
	if got := topologyOf(web, runtime, agent, peers); !reflect.DeepEqual(got, withRuntime) {
		t.Errorf("topologyOf with a runtime listener = %+v, want %+v", got, withRuntime)
	}

	// The sources count once they are registered.
	var tp topology
	if got := tp.read(); !reflect.DeepEqual(got, dispatch.Topology{}) {
		t.Errorf("an empty topology reads %+v", got)
	}
	tp.setWeb(func() []listen.Bound { return web })
	tp.setAgent(func() []listen.Bound { return agent })
	tp.setPeers(func() []netip.Addr { return peers })
	if got := tp.read(); !reflect.DeepEqual(got, want) {
		t.Errorf("the registered topology reads %+v, want %+v", got, want)
	}
	tp.setRuntime(func() []netip.AddrPort { return runtime })
	if got := tp.read(); !reflect.DeepEqual(got, withRuntime) {
		t.Errorf("the topology with runtime listeners reads %+v, want %+v", got, withRuntime)
	}
}

// TestServerTopology: a started server's dispatcher sees its web listeners;
// the embedded agent has no agent listener and no sessions.
func TestServerTopology(t *testing.T) {
	s := start(t, embeddedOptions(t))
	got := s.topo.read()
	if len(got.PanelListen) == 0 || !reflect.DeepEqual(got.PanelListen, boundAddrs(s.Listeners())) ||
		len(got.AgentListen) != 0 || len(got.AgentPeers) != 0 {
		t.Errorf("topology %+v, want the web listeners %v only", got, s.Listeners())
	}
}
