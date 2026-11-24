package rollout_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/rollout"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// Acceptance test A25 of the M10 foundations spec for the rollout driver: a
// failed rollout and a rolled-back update each emit their event kind.

type qaFleet struct {
	state string // the transaction state the host's update ends in
}

func (f qaFleet) Read(_ context.Context, _ core.Principal, _ string, _ core.ActionRef, _ json.RawMessage) (json.RawMessage, error) {
	return json.Marshal(core.UpdateStatus{Transactions: []core.UpdateTransaction{{ID: "t1", OperationID: "op1", Kind: "update",
		FromVersion: "0.1.0", ToVersion: "0.2.0", TargetVersion: "0.2.0", State: f.state}}})
}

func (f qaFleet) Plan(_ context.Context, _ core.Principal, hostID string, a core.ActionRef, _ json.RawMessage, _ core.PlanOptions) (core.PlanView, error) {
	return core.PlanView{PlanID: "plan-1", HostID: hostID, Action: a.Name, Version: a.Version, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (f qaFleet) Apply(_ context.Context, _ core.Principal, hostID string, a core.ActionRef, _ string, _ core.ApplyOptions) (core.ChangeView, error) {
	out, _ := json.Marshal(core.UpdateTransaction{ID: "t1", OperationID: "op1"})
	return core.ChangeView{ID: "chg-1", HostID: hostID, Action: a.Name, Version: a.Version, State: core.StateCompleted, Output: out}, nil
}

type qaNotifier struct {
	mu     sync.Mutex
	events []core.NotifyEvent
}

func (n *qaNotifier) Emit(_ context.Context, _ *sql.Tx, e core.NotifyEvent) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events = append(n.events, e)
	return nil
}

func (n *qaNotifier) kinds() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	var k []string
	for _, e := range n.events {
		k = append(k, e.Kind+"@"+e.HostID)
	}
	return strings.Join(k, ",")
}

func qaRun(t *testing.T, endState string) *qaNotifier {
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
	start := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	opts := store.MigrateOptions{Side: store.SideController, BinaryVersion: "test", BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: start}
	if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer().Exec(`INSERT INTO core_hosts (id, name, is_local, state, created_at) VALUES ('h1', 'host-one', 0, 'enrolled', '2026-11-01T00:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	clock := coretest.NewFakeClock(start)
	n := &qaNotifier{}
	d, err := rollout.NewDriver(rollout.DriverConfig{DB: db, Dispatcher: qaFleet{state: endState}, Clock: clock, Notifier: n})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Stop)
	var r core.Rollout
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		r, err = rollout.Create(ctx, tx, rollout.CreateParams{Input: core.RolloutInput{Hosts: []string{"h1"}, Version: "0.2.0", Channel: core.ChannelStable},
			ControllerVersion: "0.2.0", Now: clock.Now()})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	d.Start()
	for i := 0; i < 12; i++ {
		clock.Advance(rollout.PollInterval)
		var state string
		if err := db.Read().QueryRow(`SELECT state FROM core_rollouts WHERE id = ?`, r.ID).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == core.RolloutFailed {
			return n
		}
	}
	t.Fatalf("the rollout did not fail after 12 polls")
	return n
}

func TestA25_rollout_failed_and_update_rolled_back(t *testing.T) {
	if got := qaRun(t, core.UpdateRolledBack).kinds(); got != "rollout.failed@h1,update.rolled_back@h1" && got != "update.rolled_back@h1,rollout.failed@h1" {
		t.Errorf("a rolled-back update emitted %q, want rollout.failed and update.rolled_back for h1", got)
	}
	if got := qaRun(t, core.UpdateFailed).kinds(); got != "rollout.failed@h1" {
		t.Errorf("a failed update emitted %q, want only rollout.failed for h1", got)
	}
}
