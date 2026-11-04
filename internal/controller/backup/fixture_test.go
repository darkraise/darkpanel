package backup

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules"
)

// cbTimeLayout is the lifecycle's time format, which the fixture rows use.
const cbTimeLayout = "2006-01-02T15:04:05.000000000Z"

var fxNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// fxDB opens <dir>/controller.db (dir from t.TempDir() unless given) migrated with
// store.CoreMigrations(store.SideController) and every modules.All() controller set, and closes it at cleanup.
func fxDB(t *testing.T, dir string) *store.DB {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(dir, "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	coreSet, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	var moduleSets []store.MigrationSet
	for _, m := range modules.All() {
		man := m.Manifest()
		if fsys, ok := man.Migrations[core.SideController]; ok && fsys != nil {
			moduleSets = append(moduleSets, store.MigrationSet{Module: man.ID, FS: fsys})
		}
	}
	res, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideController, BinaryVersion: "test", BackupRoot: t.TempDir(), Keep: 5, Now: fxNow,
	}, coreSet, moduleSets)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) != 0 {
		t.Fatalf("module migrations failed: %+v", res.Failed)
	}
	return db
}

// fxChange inserts a core_plans row and a core_changes row; empty fields take fixed defaults
// (Class core.ClassCompensable, Actor "user:fixture", Version 1, InputJSON "{}", times fxNow).
type fxChange struct{ ID, HostID, Action, Class, State, Owner, ParentID string }

func fxInsertChange(t *testing.T, db *store.DB, c fxChange) {
	t.Helper()
	if c.Class == "" {
		c.Class = string(core.ClassCompensable)
	}
	parent := sql.NullString{String: c.ParentID, Valid: c.ParentID != ""}
	now := fxNow.Format(cbTimeLayout)
	plan := "plan-" + c.ID
	err := db.Write(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash,
			diff_json, guards_json, confirmation, created_at, expires_at)
			VALUES (?, ?, ?, 1, 'user:fixture', '', '', '{}', '[]', '', ?, ?)`,
			plan, c.HostID, c.Action, now, now); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor,
			state, input_json, recovery_owner, parent_change_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, 1, ?, '', 'user:fixture', ?, '{}', ?, ?, ?, ?)`,
			c.ID, plan, c.HostID, c.Action, c.Class, c.State, c.Owner, parent, now, now)
		return err
	})
	if err != nil {
		t.Fatalf("insert change %s: %v", c.ID, err)
	}
}

// fxInsertComposite inserts the composite's change (class composite), its core_composites row
// (direction "forward" unless dir is "unwind", cursor 0) and one core_composite_steps row per step.
type fxStep struct{ ID, HostID, Action, State, ChildPlanID, ChildChangeID string }

func fxInsertComposite(t *testing.T, db *store.DB, id, state, dir string, steps []fxStep) {
	t.Helper()
	if dir == "" {
		dir = "forward"
	}
	fxInsertChange(t, db, fxChange{ID: id, Action: "core.fixture.composite", Class: string(core.ClassComposite), State: state})
	now := fxNow.Format(cbTimeLayout)
	err := db.Write(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO core_composites (change_id, direction, cursor, steps_deadline) VALUES (?, ?, 0, ?)`,
			id, dir, now); err != nil {
			return err
		}
		for i, s := range steps {
			if s.Action == "" {
				s.Action = "test.step/v1"
			}
			if s.State == "" {
				s.State = "pending"
			}
			if _, err := tx.Exec(`INSERT INTO core_composite_steps (change_id, step_id, ordinal, host_id, action,
				input_template, state, child_plan_id, child_change_id, updated_at)
				VALUES (?, ?, ?, ?, ?, '{}', ?, ?, ?, ?)`,
				id, s.ID, i, s.HostID, s.Action, s.State,
				sql.NullString{String: s.ChildPlanID, Valid: s.ChildPlanID != ""},
				sql.NullString{String: s.ChildChangeID, Valid: s.ChildChangeID != ""}, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("insert composite %s: %v", id, err)
	}
}

func fxInsertLock(t *testing.T, db *store.DB, key, owner string) {
	t.Helper()
	err := db.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO core_locks (key, owner) VALUES (?, ?)`, key, owner)
		return err
	})
	if err != nil {
		t.Fatalf("insert lock %s: %v", key, err)
	}
}

// fxInsertRollout inserts a core_rollouts row; a draining rollout gets outcome "failed".
func fxInsertRollout(t *testing.T, db *store.DB, id, state string, hosts ...string) {
	t.Helper()
	outcome := sql.NullString{String: "failed", Valid: state == "draining"}
	now := fxNow.Format(cbTimeLayout)
	err := db.Write(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO core_rollouts (id, version, channel, state, outcome, created_at, updated_at)
			VALUES (?, '1.0.0', 'stable', ?, ?, ?, ?)`, id, state, outcome, now, now); err != nil {
			return err
		}
		for i, h := range hosts {
			if _, err := tx.Exec(`INSERT INTO core_rollout_hosts (rollout_id, host_id, position, state) VALUES (?, ?, ?, 'waiting')`,
				id, h, i); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("insert rollout %s: %v", id, err)
	}
}

func fxInsertListener(t *testing.T, db *store.DB, id, state string) {
	t.Helper()
	now := fxNow.Format(cbTimeLayout)
	err := db.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO core_listeners (id, address, port, allowed_peer, client_header, public_host, change_id,
			state, created_at, updated_at)
			VALUES (?, '127.0.0.1', 8443, '100.64.0.1', 'X-Darkpanel-Client', 'fixture.example.com', '', ?, ?, ?)`,
			id, state, now, now)
		return err
	})
	if err != nil {
		t.Fatalf("insert listener %s: %v", id, err)
	}
}

func fxInsertHost(t *testing.T, db *store.DB, id, state string, local bool) {
	t.Helper()
	isLocal := 0
	if local {
		isLocal = 1
	}
	err := db.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO core_hosts (id, name, is_local, state, created_at) VALUES (?, ?, ?, ?, ?)`,
			id, "host-"+id, isLocal, state, fxNow.Format(cbTimeLayout))
		return err
	})
	if err != nil {
		t.Fatalf("insert host %s: %v", id, err)
	}
}

// fxKey writes a fresh controller key file (secret.LoadOrCreateKey) under dir and returns its path,
// its bytes and a sealer over it.
func fxKey(t *testing.T, dir string) (path string, raw []byte, sealer *secret.Sealer) {
	t.Helper()
	path = filepath.Join(dir, "controller.key")
	key, err := secret.LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path) //nolint:gosec // G304: a path under the test's directory
	if err != nil {
		t.Fatal(err)
	}
	sealer, err = secret.NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	return path, raw, sealer
}
