package rollout

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// testStart is when every rollout test's clock starts: a Monday.
var testStart = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// openDB returns a migrated controller database in a temporary directory.
func openDB(t *testing.T) *store.DB {
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
	opts := store.MigrateOptions{
		Side: store.SideController, BinaryVersion: "test", BackupRoot: filepath.Join(dir, "backups"), Keep: 5,
		Now: testStart,
	}
	if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
		t.Fatal(err)
	}
	return db
}

// exec runs one statement in a write transaction of its own.
func exec(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	err := db.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(query, args...)
		return err
	})
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// addHost adds an enrolled host whose agent runs version; local marks the
// controller's own host.
func addHost(t *testing.T, db *store.DB, id string, local bool, version string) {
	t.Helper()
	exec(t, db, `INSERT INTO core_hosts (id, name, is_local, created_at, state, agent_version)
		VALUES (?, ?, ?, ?, 'enrolled', ?)`, id, "host-"+id, local, testStart.Format(timeLayout), version)
}

// setHostState sets a host's enrollment state.
func setHostState(t *testing.T, db *store.DB, id, state string) {
	t.Helper()
	exec(t, db, "UPDATE core_hosts SET state = ? WHERE id = ?", state, id)
}

// createRollout runs Create in a write transaction of its own.
func createRollout(db *store.DB, in core.RolloutInput, controllerVersion string) (core.Rollout, error) {
	var r core.Rollout
	err := db.Write(context.Background(), func(tx *sql.Tx) error {
		var err error
		r, err = Create(context.Background(), tx, CreateParams{
			Input: in, ControllerVersion: controllerVersion, Now: testStart,
		})
		return err
	})
	return r, err
}

// mustCreate creates a rollout of version to hosts on a controller running
// controllerVersion.
func mustCreate(t *testing.T, db *store.DB, version, controllerVersion string, hosts ...string) core.Rollout {
	t.Helper()
	r, err := createRollout(db, core.RolloutInput{Hosts: hosts, Version: version, Channel: core.ChannelStable},
		controllerVersion)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// wantCode fails unless err carries code.
func wantCode(t *testing.T, err error, code errcode.Code) {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

// hostOrder lists a rollout's host ids in position order.
func hostOrder(r core.Rollout) []string {
	var ids []string
	for _, h := range r.Hosts {
		ids = append(ids, h.HostID)
	}
	return ids
}

func TestRolloutMigration(t *testing.T) {
	db := openDB(t)
	at := testStart.Format(timeLayout)
	insert := `INSERT INTO core_rollouts (id, version, channel, state, outcome, created_at, updated_at)
		VALUES (?, '1.3.0', 'stable', ?, ?, ?, ?)`
	exec(t, db, insert, "R1", core.RolloutSucceeded, nil, at, at)
	exec(t, db, insert, "R2", core.RolloutFailed, core.RolloutFailed, at, at)
	exec(t, db, insert, "R3", core.RolloutRunning, nil, at, at)
	refused := func(what, query string, args ...any) {
		t.Helper()
		err := db.Write(context.Background(), func(tx *sql.Tx) error {
			_, err := tx.Exec(query, args...)
			return err
		})
		if err == nil {
			t.Errorf("%s was accepted", what)
		}
	}
	refused("a second running rollout", insert, "R4", core.RolloutRunning, nil, at, at)
	refused("a draining rollout beside a running one", insert, "R4", core.RolloutDraining, core.RolloutCancelled, at, at)
	refused("an unknown state", insert, "R4", "paused", nil, at, at)
	exec(t, db, "UPDATE core_rollouts SET state = 'cancelled' WHERE id = 'R3'")
	refused("a draining rollout without an outcome", insert, "R4", core.RolloutDraining, nil, at, at)
	exec(t, db, insert, "R4", core.RolloutDraining, core.RolloutCancelled, at, at)

	hostInsert := `INSERT INTO core_rollout_hosts (rollout_id, host_id, position, state) VALUES (?, ?, ?, ?)`
	exec(t, db, hostInsert, "R4", "a", 0, core.RolloutHostUpdating)
	refused("a second updating host", hostInsert, "R4", "b", 1, core.RolloutHostUpdating)
	refused("a repeated position", hostInsert, "R4", "b", 0, core.RolloutHostWaiting)
	refused("an unknown host state", hostInsert, "R4", "b", 1, "paused")
	refused("a host of no rollout", hostInsert, "R9", "b", 0, core.RolloutHostWaiting)
	exec(t, db, hostInsert, "R4", "b", 1, core.RolloutHostWaiting)
}

func TestCreate(t *testing.T) {
	db := openDB(t)
	addHost(t, db, "A", false, "1.2.0")
	addHost(t, db, "B", false, "1.2.0")
	addHost(t, db, "OWN", true, "1.2.0")
	var r core.Rollout
	err := db.Write(context.Background(), func(tx *sql.Tx) error {
		var err error
		r, err = Create(context.Background(), tx, CreateParams{
			Input:             core.RolloutInput{Hosts: []string{"B", "OWN", "A"}, Version: "1.3.0", Channel: core.ChannelBeta},
			ControllerVersion: "1.2.0", ChangeID: "CHANGE1", Now: testStart,
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.ID == "" || r.Version != "1.3.0" || r.Channel != core.ChannelBeta || r.State != core.RolloutRunning ||
		r.Error != nil || !r.CreatedAt.Equal(testStart) || !r.UpdatedAt.Equal(testStart) {
		t.Errorf("rollout = %+v", r)
	}
	if got := hostOrder(r); !slices.Equal(got, []string{"OWN", "B", "A"}) {
		t.Errorf("hosts = %v, want the own host first, then the given order", got)
	}
	for i, h := range r.Hosts {
		if h.Position != i || h.State != core.RolloutHostWaiting || h.PlanID != nil || h.OperationID != nil || h.Error != nil {
			t.Errorf("host %d = %+v", i, h)
		}
	}
	rec, err := load(context.Background(), db.Read(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ChangeID != "CHANGE1" || rec.Outcome != "" {
		t.Errorf("record = %+v", rec)
	}
	if _, err := load(context.Background(), db.Read(), "MISSING"); err == nil {
		t.Error("load found a missing rollout")
	} else {
		wantCode(t, err, errcode.NotFound)
	}
}

func TestCreateRefusals(t *testing.T) {
	db := openDB(t)
	addHost(t, db, "OWN", true, "1.2.0")
	addHost(t, db, "A", false, "1.2.0")
	addHost(t, db, "P", false, "")
	setHostState(t, db, "P", core.HostPending)
	addHost(t, db, "R", false, "1.2.0")
	setHostState(t, db, "R", core.HostRevoked)
	tests := []struct {
		name       string
		hosts      []string
		version    string
		channel    string
		controller string
		code       errcode.Code // "" when the rollout is created
	}{
		{"no hosts", nil, "1.3.0", core.ChannelStable, "1.3.0", errcode.InvalidInput},
		{"a host twice", []string{"A", "A"}, "1.3.0", core.ChannelStable, "1.3.0", errcode.InvalidInput},
		{"an unknown host", []string{"A", "X"}, "1.3.0", core.ChannelStable, "1.3.0", errcode.NotFound},
		{"a pending host", []string{"P"}, "1.3.0", core.ChannelStable, "1.3.0", errcode.InvalidInput},
		{"a revoked host", []string{"R"}, "1.3.0", core.ChannelStable, "1.3.0", errcode.InvalidInput},
		{"a malformed version", []string{"A"}, "v1.3.0", core.ChannelStable, "1.3.0", errcode.InvalidInput},
		{"an unknown channel", []string{"A"}, "1.3.0", "nightly", "1.3.0", errcode.InvalidInput},
		{"ahead of the controller", []string{"A"}, "1.3.0", core.ChannelStable, "1.2.0", errcode.InvalidInput},
		{"ahead of a development controller", []string{"A"}, "1.3.0", core.ChannelStable, "dev", errcode.InvalidInput},
		{"ahead of the controller with the own host", []string{"A", "OWN"}, "1.3.0", core.ChannelStable, "1.2.0", ""},
		{"the controller's version", []string{"A"}, "1.3.0", core.ChannelStable, "1.3.0", ""},
		{"behind the controller", []string{"A"}, "1.2.5", core.ChannelStable, "1.3.0", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := createRollout(db, core.RolloutInput{Hosts: tt.hosts, Version: tt.version, Channel: tt.channel}, tt.controller)
			if tt.code != "" {
				wantCode(t, err, tt.code)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			exec(t, db, "UPDATE core_rollouts SET state = 'succeeded' WHERE id = ?", r.ID)
		})
	}
}

func TestCreateRefusesASecondActiveRollout(t *testing.T) {
	db := openDB(t)
	addHost(t, db, "OWN", true, "1.2.0")
	addHost(t, db, "A", false, "1.2.0")
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Go(func() {
			_, errs[i] = createRollout(db, core.RolloutInput{Hosts: []string{"OWN", "A"}, Version: "1.3.0",
				Channel: core.ChannelStable}, "1.2.0")
		})
	}
	wg.Wait()
	created := 0
	for _, err := range errs {
		if err == nil {
			created++
			continue
		}
		wantCode(t, err, errcode.RolloutInProgress)
	}
	if created != 1 {
		t.Fatalf("%d concurrent rollouts were created, want 1", created)
	}
	r, ok, err := active(context.Background(), db.Read())
	if err != nil || !ok {
		t.Fatalf("active = %v, %v", ok, err)
	}
	exec(t, db, "UPDATE core_rollouts SET state = 'draining', outcome = 'cancelled' WHERE id = ?", r.ID)
	_, err = createRollout(db, core.RolloutInput{Hosts: []string{"A"}, Version: "1.2.0", Channel: core.ChannelStable}, "1.2.0")
	wantCode(t, err, errcode.RolloutInProgress)
	exec(t, db, "UPDATE core_rollouts SET state = 'cancelled' WHERE id = ?", r.ID)
	mustCreate(t, db, "1.2.0", "1.2.0", "A")
}

func TestOlderThan(t *testing.T) {
	tests := []struct {
		current, target string
		want            bool
	}{
		{"1.2.0", "1.3.0", true},
		{"1.3.0", "1.3.0", false},
		{"1.3.1", "1.3.0", false},
		{"1.3.0-beta.1", "1.3.0", true},
		{"dev", "1.3.0", true},
		{"dev", "dev", false},
		{"1.3.0", "dev", false},
	}
	for _, tt := range tests {
		if got := olderThan(tt.current, tt.target); got != tt.want {
			t.Errorf("olderThan(%q, %q) = %v, want %v", tt.current, tt.target, got, tt.want)
		}
	}
}
