package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// fakeNotifier records the events emitted, each in a transaction.
type fakeNotifier struct {
	mu     sync.Mutex
	events []core.NotifyEvent
}

func (n *fakeNotifier) Emit(_ context.Context, tx *sql.Tx, e core.NotifyEvent) error {
	if tx == nil {
		return errors.New("an event emitted outside a transaction")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events = append(n.events, e)
	return nil
}

func (n *fakeNotifier) take() []core.NotifyEvent {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := n.events
	n.events = nil
	return out
}

// sessions is a fleet whose hosts in online have a live session.
type sessions struct {
	mu     sync.Mutex
	online map[string]bool
}

func (s *sessions) Agent(_ context.Context, id string) (core.AgentClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.online[id] {
		return nil, nil
	}
	return nil, errcode.New(errcode.HostOffline, "host "+id+" is offline")
}

func (s *sessions) set(id string, online bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.online[id] = online
}

type alertEnv struct {
	db    *store.DB
	clock *coretest.FakeClock
	n     *fakeNotifier
	hosts core.HostDirectory
	ev    *evaluator
}

func newAlertEnv(t *testing.T, hosts core.HostDirectory) *alertEnv {
	t.Helper()
	e := &alertEnv{db: testDB(t), clock: coretest.NewFakeClock(t0), n: &fakeNotifier{}, hosts: hosts}
	e.restart()
	return e
}

// restart replaces the evaluator, as a controller restart does.
func (e *alertEnv) restart() { e.ev = newEvaluator(e.db, e.hosts, e.n, e.clock, nil) }

func (e *alertEnv) eval(t *testing.T) []core.NotifyEvent {
	t.Helper()
	if err := e.ev.evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return e.n.take()
}

// onlyRules disables every rule but ids.
func onlyRules(t *testing.T, db *store.DB, ids ...string) {
	t.Helper()
	exec(t, db, `UPDATE metrics_alert_rules SET enabled = 0`)
	for _, id := range ids {
		exec(t, db, `UPDATE metrics_alert_rules SET enabled = 1 WHERE id = ?`, id)
	}
}

func addRule(t *testing.T, db *store.DB, id, metric, cmp string, threshold float64, minutes int, target string) {
	t.Helper()
	exec(t, db, `INSERT INTO metrics_alert_rules (id, name, metric, comparator, threshold, duration_min, severity, target,
		enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, 'warning', ?, 1, '2026-11-24T00:00:00Z', '2026-11-24T00:00:00Z')`,
		id, "rule "+id, metric, cmp, threshold, minutes, target)
}

func alertState(t *testing.T, db *store.DB, rule, host, subject string) string {
	t.Helper()
	var state string
	err := db.Read().QueryRow(`SELECT state FROM metrics_alerts WHERE rule_id = ? AND host_id = ? AND subject = ?`,
		rule, host, subject).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func kinds(es []core.NotifyEvent) []string {
	out := []string{}
	for _, e := range es {
		out = append(out, e.Kind+" "+e.HostID)
	}
	return out
}

func TestFireAndResolve(t *testing.T) {
	e := newAlertEnv(t, nil)
	onlyRules(t, e.db, "disk-almost-full")
	addHost(t, e.db, "h1", "enrolled")
	base := t0.Truncate(time.Minute)
	var firstKey string
	// Ten minutes above 90 % fire; an eleventh fires nothing more; one
	// minute below does not resolve, two do; ten more above fire again.
	values := []float64{95, 95, 95, 95, 95, 95, 95, 95, 95, 95, 95, 50, 50,
		95, 95, 95, 95, 95, 95, 95, 95, 95, 95}
	for i, v := range values {
		minuteRow(t, e.db, "h1", base.Add(time.Duration(i)*time.Minute), v)
		e.clock.Advance(time.Minute)
		got := e.eval(t)
		now := e.clock.Now().UTC().Truncate(time.Second).Unix()
		switch i {
		case 9, 22:
			if len(got) != 1 || got[0].Kind != core.NotifyAlertFiring {
				t.Fatalf("minute %d: events %v, want one alert.firing", i, kinds(got))
			}
			ev := got[0]
			if want := fmt.Sprintf("alert.firing:disk-almost-full:h1:/:%d", now); ev.DedupeKey != want {
				t.Errorf("minute %d: key %q, want %q", i, ev.DedupeKey, want)
			}
			if ev.Severity != core.SeverityWarning || ev.HostID != "h1" || ev.Title != "Disk almost full on name-h1" ||
				ev.Body != "Disk / on name-h1 has been above 90% for 10 minutes; it is 95.0%." {
				t.Errorf("minute %d: event %+v", i, ev)
			}
			if i == 9 {
				firstKey = ev.DedupeKey
			} else if ev.DedupeKey == firstKey {
				t.Errorf("the fire after a resolve reused the key %q", firstKey)
			}
			if s := alertState(t, e.db, "disk-almost-full", "h1", "/"); s != stateFiring {
				t.Errorf("minute %d: state %q, want firing", i, s)
			}
		case 12:
			if len(got) != 1 || got[0].Kind != core.NotifyAlertResolved {
				t.Fatalf("minute %d: events %v, want one alert.resolved", i, kinds(got))
			}
			ev := got[0]
			if want := fmt.Sprintf("alert.resolved:disk-almost-full:h1:/:%d", now); ev.DedupeKey != want {
				t.Errorf("key %q, want %q", ev.DedupeKey, want)
			}
			if ev.Severity != core.SeverityWarning || ev.Title != "Resolved: Disk almost full on name-h1" ||
				ev.Body != "Disk / on name-h1 is no longer above 90%; it is 50.0%." {
				t.Errorf("resolved event %+v", ev)
			}
			if s := alertState(t, e.db, "disk-almost-full", "h1", "/"); s != stateResolved {
				t.Errorf("state %q, want resolved", s)
			}
		default:
			if len(got) != 0 {
				t.Fatalf("minute %d: events %v, want none", i, kinds(got))
			}
		}
	}

	t.Run("load per logical CPU", func(t *testing.T) {
		e := newAlertEnv(t, nil)
		onlyRules(t, e.db)
		addRule(t, e.db, "load", metricLoad, ">", 1, 1, allHosts)
		for host, cpus := range map[string]any{"h-four": 4, "h-three": 3, "h-two": 2, "h-unknown": nil} {
			addHost(t, e.db, host, "enrolled")
			exec(t, e.db, `INSERT INTO metrics_poll_status (host_id, cpus) VALUES (?, ?)`, host, cpus)
			minuteRow(t, e.db, host, base, 3)
		}
		e.clock.Advance(time.Minute)
		got := e.eval(t)
		if len(got) != 1 || got[0].HostID != "h-two" {
			t.Fatalf("events %v, want one for h-two (load 3 on 2 CPUs; 3 CPUs is 1, not above it; no count is no figure)", kinds(got))
		}
		if !strings.Contains(got[0].Body, "it is 1.5 per CPU") {
			t.Errorf("body %q, want the load per CPU 1.5", got[0].Body)
		}
	})

	t.Run("a group rule covers its members alone", func(t *testing.T) {
		e := newAlertEnv(t, nil)
		onlyRules(t, e.db)
		exec(t, e.db, `INSERT INTO core_host_groups (id, name, created_at) VALUES ('G', 'web', ?)`, t0.Format(time.RFC3339Nano))
		addRule(t, e.db, "busy", metricCPU, ">", 50, 1, "G")
		for _, h := range []string{"h-member", "h-other"} {
			addHost(t, e.db, h, "enrolled")
			minuteRow(t, e.db, h, base, 90)
		}
		exec(t, e.db, `INSERT INTO core_host_group_members (group_id, host_id) VALUES ('G', 'h-member')`)
		e.clock.Advance(time.Minute)
		if got := e.eval(t); len(got) != 1 || got[0].HostID != "h-member" {
			t.Fatalf("events %v, want one for h-member alone", kinds(got))
		}
	})

	t.Run("disk is the percent of the filesystem used", func(t *testing.T) {
		e := newAlertEnv(t, nil)
		onlyRules(t, e.db)
		addRule(t, e.db, "disk", metricDisk, ">", 50, 1, allHosts)
		addHost(t, e.db, "h1", "enrolled")
		ts := base.Format(tsFormat)
		exec(t, e.db, `INSERT INTO metrics_minute (host_id, ts, cpu, mem_used, mem_total, swap_used, swap_total, load1, load5, load15)
			VALUES ('h1', ?, 1, 1, 4000, 0, 0, 0, 0, 0)`, ts)
		exec(t, e.db, `INSERT INTO metrics_minute_fs (host_id, ts, mount, used, total) VALUES ('h1', ?, '/big', 600, 1000), ('h1', ?, '/', 400, 1000)`, ts, ts)
		e.clock.Advance(time.Minute)
		got := e.eval(t)
		if len(got) != 1 || !strings.HasPrefix(got[0].DedupeKey, "alert.firing:disk:h1:/big:") || !strings.Contains(got[0].Body, "it is 60.0%") {
			t.Fatalf("events %+v, want /big alone at 60 %%", got)
		}
	})

	t.Run("below and other metrics", func(t *testing.T) {
		e := newAlertEnv(t, nil)
		onlyRules(t, e.db)
		addRule(t, e.db, "cpu-low", metricCPU, "<", 10, 2, allHosts)
		addRule(t, e.db, "mem", metricMemory, ">", 20, 2, allHosts)
		addRule(t, e.db, "swap", metricSwap, ">", 0, 1, allHosts)
		addHost(t, e.db, "h1", "enrolled")
		minuteRow(t, e.db, "h1", base, 5)
		e.clock.Advance(time.Minute)
		if got := e.eval(t); len(got) != 0 {
			t.Fatalf("after one minute: %v, want none (two-minute rules; no swap is no figure)", kinds(got))
		}
		minuteRow(t, e.db, "h1", base.Add(time.Minute), 90)
		e.clock.Advance(time.Minute)
		if got := e.eval(t); len(got) != 0 {
			t.Fatalf("after a high minute: %v, want none", kinds(got))
		}
		minuteRow(t, e.db, "h1", base.Add(2*time.Minute), 90)
		e.clock.Advance(time.Minute)
		got := e.eval(t)
		// Memory is used / total: 900 of 4000 bytes is 22.5 %.
		if len(got) != 1 || !strings.HasPrefix(got[0].DedupeKey, "alert.firing:mem:h1::") ||
			!strings.Contains(got[0].Body, "Memory on name-h1 has been above 20% for 2 minutes; it is 22.5%.") {
			t.Fatalf("events %+v, want the memory rule alone", got)
		}
	})
}

func TestNoStaleOrPartialFiring(t *testing.T) {
	e := newAlertEnv(t, nil)
	onlyRules(t, e.db, "disk-almost-full")
	base := t0.Truncate(time.Minute)
	for _, h := range []string{"h-backfill", "h-gap", "h-full", "h-stale"} {
		addHost(t, e.db, h, "enrolled")
	}
	// A 40-minute outage, backfilled in one poll: the disk was full for
	// 30 minutes and then not, so the condition ended before the newest
	// minute.
	for i := range 40 {
		v := 95.0
		if i >= 30 {
			v = 50
		}
		minuteRow(t, e.db, "h-backfill", base.Add(time.Duration(i)*time.Minute), v)
	}
	// Ten full minutes ending at the newest, one of them missing on h-gap.
	for i := 30; i < 40; i++ {
		if i != 34 {
			minuteRow(t, e.db, "h-gap", base.Add(time.Duration(i)*time.Minute), 95)
		}
		minuteRow(t, e.db, "h-full", base.Add(time.Duration(i)*time.Minute), 95)
	}
	// Ten full minutes whose newest is 21 minutes old: more than the
	// duration and 2 minutes.
	for i := 10; i < 20; i++ {
		minuteRow(t, e.db, "h-stale", base.Add(time.Duration(i)*time.Minute), 95)
	}
	e.clock.Advance(40 * time.Minute)
	got := e.eval(t)
	if len(got) != 1 || got[0].HostID != "h-full" || got[0].Kind != core.NotifyAlertFiring {
		t.Fatalf("events %v, want one alert.firing for h-full alone", kinds(got))
	}
	for _, h := range []string{"h-backfill", "h-gap", "h-stale"} {
		if s := alertState(t, e.db, "disk-almost-full", h, "/"); s != "" {
			t.Errorf("%s: alert state %q, want none", h, s)
		}
	}

	t.Run("a stale window does not resolve", func(t *testing.T) {
		e.clock.Advance(20 * time.Minute)
		if got := e.eval(t); len(got) != 0 {
			t.Fatalf("events %v, want none", kinds(got))
		}
		if s := alertState(t, e.db, "disk-almost-full", "h-full", "/"); s != stateFiring {
			t.Errorf("h-full state %q, want firing", s)
		}
	})

	t.Run("a vanished mount resolves", func(t *testing.T) {
		e.clock.Advance(time.Minute)
		at := e.clock.Now().Truncate(time.Minute)
		for _, m := range []time.Time{at.Add(-2 * time.Minute), at.Add(-time.Minute)} {
			exec(t, e.db, `INSERT INTO metrics_minute (host_id, ts, cpu, mem_used, mem_total, swap_used, swap_total, load1, load5, load15)
				VALUES ('h-full', ?, 1, 1, 4000, 0, 0, 0, 0, 0)`, m.Format(tsFormat))
		}
		got := e.eval(t)
		if len(got) != 1 || got[0].Kind != core.NotifyAlertResolved || got[0].HostID != "h-full" {
			t.Fatalf("events %v, want h-full's alert resolved", kinds(got))
		}
	})
}

func TestOfflineAfterRestart(t *testing.T) {
	s := &sessions{online: map[string]bool{"c": true}}
	e := newAlertEnv(t, s)
	onlyRules(t, e.db, "host-offline")
	for _, h := range []string{"a", "b", "c"} {
		addHost(t, e.db, h, "enrolled")
	}
	addHost(t, e.db, "p", "pending")
	addHost(t, e.db, "r", "revoked")
	// Before the restart, c was offline and its alert fired.
	exec(t, e.db, `INSERT INTO metrics_alerts (rule_id, host_id, subject, state, since, value)
		VALUES ('host-offline', 'c', '', 'firing', ?, 30)`, t0.Add(-time.Hour).Format(tsFormat))
	// The controller starts at t0: a reconnects after 4 minutes, b never
	// does, c is back but drops out at minute 1, so its 2 minutes online
	// start again at minute 2.
	for minute := 0; minute <= 7; minute++ {
		switch minute {
		case 1:
			s.set("c", false)
		case 2:
			s.set("c", true)
		case 4:
			s.set("a", true)
		}
		got := e.eval(t)
		switch minute {
		case 4:
			if len(got) != 1 || got[0].Kind != core.NotifyAlertResolved || got[0].HostID != "c" ||
				got[0].Body != "name-c is connected to the controller again." {
				t.Fatalf("minute 4: events %+v, want c resolved", got)
			}
		case 5:
			if len(got) != 1 || got[0].Kind != core.NotifyAlertFiring || got[0].HostID != "b" {
				t.Fatalf("minute 5: events %v, want b firing", kinds(got))
			}
			ev := got[0]
			now := e.clock.Now().UTC().Truncate(time.Second).Unix()
			if ev.Severity != core.SeverityCritical || ev.DedupeKey != fmt.Sprintf("alert.firing:host-offline:b::%d", now) ||
				ev.Title != "Host offline on name-b" || ev.Body != "name-b has had no connection to the controller for 5 minutes." {
				t.Errorf("event %+v", ev)
			}
		default:
			if len(got) != 0 {
				t.Fatalf("minute %d: events %v, want none", minute, kinds(got))
			}
		}
		e.clock.Advance(time.Minute)
	}
	if st := alertState(t, e.db, "host-offline", "a", ""); st != "" {
		t.Errorf("a, back within the duration, has alert state %q", st)
	}

	t.Run("a second restart fires nothing again", func(t *testing.T) {
		e.restart()
		s.set("a", false)
		for minute := 0; minute <= 6; minute++ {
			got := e.eval(t)
			if minute == 5 {
				if len(got) != 1 || got[0].HostID != "a" {
					t.Fatalf("minute 5: events %v, want a alone (b still fires)", kinds(got))
				}
			} else if len(got) != 0 {
				t.Fatalf("minute %d: events %v, want none", minute, kinds(got))
			}
			e.clock.Advance(time.Minute)
		}
	})

	t.Run("a host the rule no longer covers loses its alert", func(t *testing.T) {
		exec(t, e.db, `UPDATE core_hosts SET state = 'revoked' WHERE id = 'b'`)
		if got := e.eval(t); len(got) != 0 {
			t.Fatalf("events %v, want none", kinds(got))
		}
		if st := alertState(t, e.db, "host-offline", "b", ""); st != "" {
			t.Errorf("revoked b keeps alert state %q", st)
		}
	})
}

// ctlModule declares the controller half's actions, as the metrics module
// does, which this package cannot import.
type ctlModule struct{}

func (ctlModule) Manifest() core.Manifest {
	return core.Manifest{ID: moduleID, Version: 1, Actions: New().Actions}
}

var admin = core.Principal{Actor: "user:u1", UserID: "u1", Scope: core.ScopeAdmin, Access: core.Access{Admin: true, Fleet: true}}

type ruleEnv struct {
	t      *testing.T
	db     *store.DB
	clock  *coretest.FakeClock
	engine *lifecycle.Engine
}

func (e *ruleEnv) plan(name string, in any) (core.PlanView, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		e.t.Fatal(err)
	}
	return e.engine.Plan(context.Background(), admin, core.ActionRef{Name: name, Version: 1}, raw, core.PlanOptions{})
}

func (e *ruleEnv) run(name string, in, out any) {
	e.t.Helper()
	pv, err := e.plan(name, in)
	if err != nil {
		e.t.Fatalf("%s: %v", name, err)
	}
	cv, err := e.engine.Apply(context.Background(), admin, pv.PlanID, core.ApplyOptions{Confirmation: pv.Confirmation})
	if err != nil || cv.State != core.StateCompleted {
		e.t.Fatalf("%s: %v, %+v", name, err, cv)
	}
	if out != nil {
		if err := json.Unmarshal(cv.Output, out); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *ruleEnv) rules() []Rule {
	e.t.Helper()
	raw, err := e.engine.Read(context.Background(), admin, core.ActionRef{Name: ActionAlertListRules, Version: 1}, json.RawMessage(`{}`))
	if err != nil {
		e.t.Fatal(err)
	}
	var out RuleList
	if err := json.Unmarshal(raw, &out); err != nil {
		e.t.Fatal(err)
	}
	return out.Rules
}

// migrate runs the migrations again, as every controller start does.
func migrate(t *testing.T, db *store.DB) {
	t.Helper()
	coreSet, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	sets := []store.MigrationSet{{Module: moduleID, FS: os.DirFS("../migrations/controller")}}
	res, err := store.Migrate(context.Background(), db, store.MigrateOptions{Side: store.SideController, BinaryVersion: "test",
		BackupRoot: filepath.Join(t.TempDir(), "backups"), Keep: 5, Now: t0}, coreSet, sets)
	if err != nil || len(res.Failed) != 0 {
		t.Fatalf("migrate: %+v, %v", res, err)
	}
}

func TestSeededRulesAndLists(t *testing.T) {
	e := &ruleEnv{t: t, db: testDB(t), clock: coretest.NewFakeClock(t0)}
	audit := &coretest.MemAudit{}
	a := auth.New(e.db, coretest.Sealer{}, e.clock, audit,
		auth.Options{Params: auth.Params{Memory: 8, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}})
	reg, err := core.NewRegistry(coreactions.Module(coreactions.Deps{Auth: a, Audit: audit}), ctlModule{})
	if err != nil {
		t.Fatal(err)
	}
	e.engine, err = lifecycle.New(lifecycle.Config{Side: core.SideController, Registry: reg, Validator: schema.NewValidator(),
		Journal: audit, Redact: schema.Redact, Base: core.Env{DB: e.db, Sealer: coretest.Sealer{}, Clock: e.clock,
			Log: slog.New(slog.DiscardHandler), Agents: coretest.MapHosts{}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.engine.Close)
	if svcs := New().Services(ext.Deps{}); svcs != nil {
		t.Fatalf("services with no database: %v", svcs)
	}

	type seeded struct {
		name, metric, cmp, severity, target string
		threshold                           float64
		minutes                             int
	}
	var got []seeded
	for _, r := range e.rules() {
		got = append(got, seeded{r.Name, r.Metric, r.Comparator, r.Severity, r.Target, r.Threshold, r.DurationMin})
		if !r.Enabled {
			t.Errorf("seeded rule %s is disabled", r.Name)
		}
	}
	want := []seeded{{"Disk almost full", metricDisk, ">", core.SeverityWarning, allHosts, 90, 10},
		{"Host offline", metricOffline, ">", core.SeverityCritical, allHosts, 0, 5}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("seeded rules %+v, want %+v", got, want)
	}

	t.Run("a deleted seeded rule stays deleted", func(t *testing.T) {
		e.run(ActionAlertDelete, RuleIDInput{ID: "host-offline"}, nil)
		migrate(t, e.db)
		rules := e.rules()
		if len(rules) != 1 || rules[0].Name != "Disk almost full" {
			t.Fatalf("after a restart the rules are %+v, want Disk almost full alone", rules)
		}
	})

	t.Run("rule validation", func(t *testing.T) {
		base := RuleInput{Name: "Busy", Metric: metricCPU, Comparator: ">", Threshold: 80, DurationMin: 5,
			Severity: core.SeverityWarning, Target: allHosts}
		for _, c := range []struct {
			name string
			mod  func(*RuleInput)
			code errcode.Code
		}{
			{"unknown group", func(in *RuleInput) { in.Target = "nope" }, errcode.NotFound},
			{"name taken", func(in *RuleInput) { in.Name = "disk ALMOST full" }, errcode.InvalidInput},
			{"no comparator", func(in *RuleInput) { in.Comparator = "" }, errcode.InvalidInput},
			{"percent over 100", func(in *RuleInput) { in.Threshold = 101 }, errcode.InvalidInput},
			{"load over 1000", func(in *RuleInput) { in.Metric, in.Threshold = metricLoad, 1001 }, errcode.InvalidInput},
			{"bad name", func(in *RuleInput) { in.Name = " lead" }, errcode.InvalidInput},
			{"negative threshold", func(in *RuleInput) { in.Threshold = -1 }, errcode.InvalidInput},
		} {
			in := base
			c.mod(&in)
			if _, err := e.plan(ActionAlertCreate, in); errcode.From(err).Code != c.code {
				t.Errorf("%s: %v, want %s", c.name, err, c.code)
			}
		}
		var r Rule
		in := base
		in.Metric, in.Comparator, in.Threshold = metricOffline, "", 50
		e.run(ActionAlertCreate, in, &r)
		if r.Comparator != ">" || r.Threshold != 0 || !r.Enabled || r.Name != "Busy" {
			t.Errorf("an offline rule is stored as %+v", r)
		}
		in = base
		in.Metric, in.Threshold = metricLoad, 2.5
		e.run(ActionAlertUpdate, RuleUpdateInput{ID: r.ID, RuleInput: in, Enabled: false}, &r)
		if r.Metric != metricLoad || r.Threshold != 2.5 || r.Enabled {
			t.Errorf("the updated rule is %+v", r)
		}
	})

	t.Run("a rule targeting a group blocks its deletion", func(t *testing.T) {
		var g coreactions.Group
		e.run("core.group.create", coreactions.GroupCreateInput{Name: "ops"}, &g)
		var r Rule
		e.run(ActionAlertCreate, RuleInput{Name: "Ops CPU", Metric: metricCPU, Comparator: ">", Threshold: 80, DurationMin: 5,
			Severity: core.SeverityCritical, Target: g.ID}, &r)
		if r.TargetName != "ops" {
			t.Errorf("the rule's target name is %q, want ops", r.TargetName)
		}
		_, err := e.plan("core.group.delete", coreactions.GroupIDInput{ID: g.ID})
		if errcode.From(err).Code != errcode.GroupInUse || !strings.Contains(err.Error(), "alert rule Ops CPU") {
			t.Fatalf("deleting the group: %v, want group.in_use naming alert rule Ops CPU", err)
		}
		e.run(ActionAlertDelete, RuleIDInput{ID: r.ID}, nil)
		e.run("core.group.delete", coreactions.GroupIDInput{ID: g.ID}, nil)
	})

	t.Run("the alert list shows only visible hosts", func(t *testing.T) {
		addHost(t, e.db, "h-in", "enrolled")
		addHost(t, e.db, "h-out", "enrolled")
		now := e.clock.Now().UTC()
		for _, row := range []struct {
			host, subject, state string
			since                time.Time
		}{
			{"h-in", "/", stateFiring, now.Add(-3 * time.Hour)},
			{"h-in", "/srv", stateResolved, now.Add(-time.Hour)},
			{"h-in", "/old", stateResolved, now.Add(-25 * time.Hour)},
			{"h-out", "/", stateFiring, now.Add(-time.Hour)},
		} {
			exec(t, e.db, `INSERT INTO metrics_alerts (rule_id, host_id, subject, state, since, value) VALUES ('disk-almost-full', ?, ?, ?, ?, 93)`,
				row.host, row.subject, row.state, row.since.Format(tsFormat))
		}
		list := func(p core.Principal) string {
			out, err := alertListRead{}.Read(context.Background(), core.Env{DB: e.db, Clock: e.clock, Principal: p}, NoInput{})
			if err != nil {
				t.Fatal(err)
			}
			var s []string
			for _, a := range out.Alerts {
				s = append(s, a.HostID+a.Subject+" "+a.State+" "+a.RuleName)
			}
			return strings.Join(s, ", ")
		}
		if got, want := list(admin), "h-out/ firing Disk almost full, h-in/ firing Disk almost full, h-in/srv resolved Disk almost full"; got != want {
			t.Errorf("admin list %q, want %q", got, want)
		}
		member := core.Principal{Actor: "user:m", UserID: "m", Access: core.Access{Hosts: map[string]core.Scope{"h-in": core.ScopeRead}}}
		if got, want := list(member), "h-in/ firing Disk almost full, h-in/srv resolved Disk almost full"; got != want {
			t.Errorf("member list %q, want %q", got, want)
		}
	})

	t.Run("updating a rule clears its alerts", func(t *testing.T) {
		var r Rule
		e.run(ActionAlertUpdate, RuleUpdateInput{ID: "disk-almost-full", RuleInput: RuleInput{Name: "Disk almost full",
			Metric: metricDisk, Comparator: ">", Threshold: 95, DurationMin: 10, Severity: core.SeverityWarning, Target: allHosts},
			Enabled: true}, &r)
		if n := count(t, e.db, `SELECT count(*) FROM metrics_alerts WHERE rule_id = 'disk-almost-full'`); n != 0 {
			t.Errorf("%d alerts remain after the update", n)
		}
	})
}

// TestEvaluatedAfterEachPoll runs the poll service as the module contributes
// it, so the evaluator is known to run in production and not only when a
// test calls it.
func TestEvaluatedAfterEachPoll(t *testing.T) {
	db := testDB(t)
	onlyRules(t, db, "host-offline")
	addHost(t, db, "h1", "enrolled")
	clock := coretest.NewFakeClock(t0)
	n := &fakeNotifier{}
	disp := &coretest.FakeDispatcher{
		ReadFunc: func(context.Context, core.Principal, string, core.ActionRef, json.RawMessage) (json.RawMessage, error) {
			return nil, errcode.New(errcode.HostOffline, "offline")
		},
	}
	var poll *ext.Service
	for _, s := range New().Services(ext.Deps{DB: db, Dispatch: disp, Hosts: &sessions{online: map[string]bool{}}, Notifier: n, Clock: clock}) {
		if s.Name == "metrics.poll" {
			poll = &s
		}
	}
	if poll == nil {
		t.Fatal("no metrics.poll service")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- poll.Run(ctx) }()
	armed := func() {
		deadline := time.Now().Add(5 * time.Second)
		for clock.Pending() == 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
	}
	for range 5 {
		armed()
		clock.Advance(time.Minute)
	}
	armed()
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run returned %v", err)
	}
	if got := n.take(); len(got) != 1 || got[0].Kind != core.NotifyAlertFiring || got[0].HostID != "h1" {
		t.Fatalf("after five minutes offline the poll service emitted %v, want h1 firing", kinds(got))
	}
}
