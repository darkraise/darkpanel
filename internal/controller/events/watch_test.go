package events

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
)

var pubStart = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

// pubEnv is a migrated controller database, a fake clock and a bus with
// one subscriber.
type pubEnv struct {
	t     *testing.T
	db    *store.DB
	clock *coretest.FakeClock
	bus   *Bus
	ch    <-chan Event
}

func newPubEnv(t *testing.T) *pubEnv {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	opts := store.MigrateOptions{Side: store.SideController, BinaryVersion: "test",
		BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: pubStart}
	if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
		t.Fatal(err)
	}
	e := &pubEnv{t: t, db: db, clock: coretest.NewFakeClock(pubStart), bus: NewBus(nil)}
	e.ch, _ = e.bus.Subscribe(t.Context())
	return e
}

// start starts the watchers and stops them when the test ends.
func (e *pubEnv) start() {
	stop := StartWatchers(e.db, e.clock, e.bus, hideSecret, nil)
	e.t.Cleanup(stop)
}

// hideSecret hides the changes of demo.secret.reset version 1.
func hideSecret(v core.ChangeView) bool {
	return v.Action != "demo.secret.reset" || v.Version != 1
}

func (e *pubEnv) exec(query string, args ...any) {
	e.t.Helper()
	if _, err := e.db.Writer().ExecContext(context.Background(), query, args...); err != nil {
		e.t.Fatalf("%s: %v", query, err)
	}
}

// pubAt formats t as the lifecycle engine stores times.
func pubAt(t time.Time) string { return t.UTC().Format(timeFormat) }

// insertChange inserts change id of host in state, updated at.
func (e *pubEnv) insertChange(id, host, state string, at time.Time) {
	e.t.Helper()
	e.exec(`INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash, diff_json, guards_json,
		confirmation, created_at, expires_at) VALUES (?, ?, 'demo.item.add', 1, 'user:u1', 'h', 's', '[]', '[]', '', ?, ?)`,
		"P"+id, host, pubAt(at), pubAt(at.Add(10*time.Minute)))
	e.exec(`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state,
		input_json, created_at, updated_at) VALUES (?, ?, ?, 'demo.item.add', 1, 'compensable', '', 'user:u1', ?, '{}', ?, ?)`,
		id, "P"+id, host, state, pubAt(at), pubAt(at))
}

// next receives the next event within five seconds.
func (e *pubEnv) next() Event {
	e.t.Helper()
	return busRecv(e.t, e.ch)
}

// none fails if an event is waiting.
func (e *pubEnv) none() {
	e.t.Helper()
	busEmpty(e.t, e.ch)
}

func (e *pubEnv) want(eventType, data string) {
	e.t.Helper()
	ev := e.next()
	if ev.Type != eventType || string(ev.Data) != data {
		e.t.Fatalf("event = %s %s, want %s %s", ev.Type, ev.Data, eventType, data)
	}
}

func TestWatchChanges(t *testing.T) {
	e := newPubEnv(t)
	e.insertChange("OLD", "", "completed", pubStart.Add(-time.Minute))
	e.insertChange("RECENT", "", "completed", pubStart.Add(-2*time.Second))
	e.start()
	e.clock.Advance(PollInterval)
	e.none()

	e.insertChange("C1", "", "applying", e.clock.Now())
	e.clock.Advance(PollInterval)
	e.want(TypeChange, `{"hostId":"","changeId":"C1","action":"demo.item.add","state":"applying","updatedAt":"2026-10-22T12:00:01Z"}`)
	e.none()

	e.exec("UPDATE core_changes SET state = 'completed', updated_at = ? WHERE id = 'C1'", pubAt(e.clock.Now()))
	e.clock.Advance(PollInterval)
	e.want(TypeChange, `{"hostId":"","changeId":"C1","action":"demo.item.add","state":"completed","updatedAt":"2026-10-22T12:00:02Z"}`)

	e.clock.Advance(PollInterval)
	e.none()

	// A writer stamps updated_at before its commit, so a row can appear with
	// a time an earlier poll has passed.
	e.insertChange("C2", "h1", "pending", e.clock.Now().Add(-2*PollInterval))
	e.clock.Advance(PollInterval)
	e.want(TypeChange, `{"hostId":"h1","changeId":"C2","action":"demo.item.add","state":"pending","updatedAt":"2026-10-22T12:00:02Z"}`)
	e.clock.Advance(10 * PollInterval)
	e.none()
}

func TestWatchHidesInvisibleChanges(t *testing.T) {
	e := newPubEnv(t)
	e.start()
	e.insertChange("HIDDEN", "", "applying", e.clock.Now())
	e.exec("UPDATE core_changes SET action = 'demo.secret.reset' WHERE id = 'HIDDEN'")
	e.insertChange("C1", "", "applying", e.clock.Now())
	e.clock.Advance(PollInterval)
	e.want(TypeChange, `{"hostId":"","changeId":"C1","action":"demo.item.add","state":"applying","updatedAt":"2026-10-22T12:00:00Z"}`)
	e.none()

	e.exec("UPDATE core_changes SET state = 'completed', updated_at = ? WHERE id = 'HIDDEN'", pubAt(e.clock.Now()))
	e.clock.Advance(PollInterval)
	e.none()
}

func TestWatchSkipsUnparsableTime(t *testing.T) {
	e := newPubEnv(t)
	e.start()
	e.insertChange("BAD", "", "applying", e.clock.Now())
	e.exec("UPDATE core_changes SET updated_at = ? WHERE id = 'BAD'", pubAt(e.clock.Now())+"x")
	e.insertChange("C1", "", "applying", e.clock.Now())
	e.clock.Advance(PollInterval)
	e.want(TypeChange, `{"hostId":"","changeId":"C1","action":"demo.item.add","state":"applying","updatedAt":"2026-10-22T12:00:00Z"}`)
	e.want(TypeReset, `{}`)
	e.clock.Advance(PollInterval)
	e.none()
}

func TestWatchModules(t *testing.T) {
	e := newPubEnv(t)
	for _, h := range []string{"h1", "h2"} {
		e.exec("INSERT INTO core_hosts (id, name, created_at) VALUES (?, ?, ?)", h, "host-"+h, pubAt(pubStart))
	}
	module := func(host string, enabled, available int) {
		e.exec(`INSERT INTO core_module_state (host_id, module_id, enabled, available, checked_at) VALUES (?, 'demo', ?, ?, ?)
			ON CONFLICT (host_id, module_id) DO UPDATE SET enabled = excluded.enabled, available = excluded.available`,
			host, enabled, available, pubAt(e.clock.Now()))
	}
	module("h1", 1, 1)
	e.start()
	e.clock.Advance(PollInterval)
	e.none()

	module("h1", 0, 1)
	e.clock.Advance(PollInterval)
	e.want(TypeModule, `{"hostId":"h1","moduleId":"demo","enabled":false,"available":true}`)
	module("h1", 0, 0)
	e.clock.Advance(PollInterval)
	e.want(TypeModule, `{"hostId":"h1","moduleId":"demo","enabled":false,"available":false}`)
	module("h2", 1, 1)
	e.clock.Advance(PollInterval)
	e.want(TypeModule, `{"hostId":"h2","moduleId":"demo","enabled":true,"available":true}`)
	e.clock.Advance(PollInterval)
	e.none()
}

func TestWatchStop(t *testing.T) {
	e := newPubEnv(t)
	stop := StartWatchers(e.db, e.clock, e.bus, hideSecret, nil)
	stop()
	if n := e.clock.Pending(); n != 0 {
		t.Fatalf("%d timers armed after stop, want 0", n)
	}
	e.insertChange("C1", "", "applying", e.clock.Now())
	e.clock.Advance(PollInterval)
	e.none()
}

func TestAgentChanges(t *testing.T) {
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	entries := []core.JournalEntry{
		{Phase: "apply", ChangeID: "A"}, {Phase: "confirm", ChangeID: "A"}, {Phase: "plan"}, {Phase: "apply", ChangeID: "B"},
	}
	cases := []struct {
		name    string
		failing string
		hidden  string
		want    []string
	}{
		{"both read", "", "", []string{
			`change {"hostId":"h1","changeId":"A","action":"demo.item.add","state":"confirmed","updatedAt":"2026-10-22T12:00:00Z"}`,
			`change {"hostId":"h1","changeId":"B","action":"demo.item.add","state":"confirmed","updatedAt":"2026-10-22T12:00:00Z"}`,
		}},
		{"A fails", "A", "", []string{
			`change {"hostId":"h1","changeId":"B","action":"demo.item.add","state":"confirmed","updatedAt":"2026-10-22T12:00:00Z"}`,
			`reset {}`,
		}},
		{"A hidden", "", "A", []string{
			`change {"hostId":"h1","changeId":"B","action":"demo.item.add","state":"confirmed","updatedAt":"2026-10-22T12:00:00Z"}`,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := NewBus(nil)
			ch, _ := bus.Subscribe(t.Context())
			var mu sync.Mutex
			reads := map[string]int{}
			var buf bytes.Buffer
			publish := AgentChanges(bus, func(_ context.Context, hostID, changeID string) (core.ChangeView, error) {
				mu.Lock()
				defer mu.Unlock()
				reads[hostID+"/"+changeID]++
				if changeID == tc.failing {
					return core.ChangeView{}, errors.New("host.offline")
				}
				action := "demo.item.add"
				if changeID == tc.hidden {
					action = "demo.secret.reset"
				}
				return core.ChangeView{ID: changeID, HostID: hostID, Action: action, Version: 1, State: core.StateConfirmed, UpdatedAt: at}, nil
			}, hideSecret, slog.New(slog.NewTextHandler(&buf, nil)))
			publish("h1", entries)
			for _, want := range tc.want {
				if ev := busRecv(t, ch); ev.Type+" "+string(ev.Data) != want {
					t.Fatalf("event = %s %s, want %s", ev.Type, ev.Data, want)
				}
			}
			// The reads end with the page; a late reset would follow them.
			time.Sleep(50 * time.Millisecond)
			busEmpty(t, ch)
			mu.Lock()
			defer mu.Unlock()
			if len(reads) != 2 || reads["h1/A"] != 1 || reads["h1/B"] != 1 {
				t.Fatalf("reads = %v, want h1/A and h1/B once each", reads)
			}
			if logged := strings.Contains(buf.String(), "events: read an ingested change"); logged != (tc.failing != "") {
				t.Fatalf("log = %q, want the failed read logged: %v", buf.String(), tc.failing != "")
			}
		})
	}
	publish := AgentChanges(NewBus(nil), func(context.Context, string, string) (core.ChangeView, error) {
		t.Error("a page without change ids read a change")
		return core.ChangeView{}, nil
	}, hideSecret, nil)
	publish("h1", []core.JournalEntry{{Phase: "plan"}})
}
