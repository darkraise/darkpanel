package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// eraIn has an erase field beside a plain secret and a nested erase field.
type eraIn struct {
	Name     string   `json:"name"`
	Password string   `json:"password" secret:"erase"`
	Token    string   `json:"token" secret:"true"`
	Keys     []eraKey `json:"keys,omitempty"`
}

type eraKey struct {
	ID         string `json:"id"`
	Passphrase string `json:"passphrase" secret:"erase"`
}

type eraHandler struct{}

func (eraHandler) Plan(context.Context, core.Env, eraIn) (core.PlanOutcome, error) {
	return core.PlanOutcome{StateHash: "h"}, nil
}

func (eraHandler) Apply(_ context.Context, _ core.Env, in eraIn) (struct{}, error) {
	if in.Name == "fail" {
		return struct{}{}, core.Definite(errors.New("apply failed"))
	}
	return struct{}{}, nil
}

func (eraHandler) Verify(context.Context, core.Env, eraIn, struct{}) error { return nil }

func (eraHandler) Snapshot(context.Context, core.Env, eraIn) ([]byte, error) {
	return []byte("{}"), nil
}

func (eraHandler) Revert(context.Context, core.Env, []byte) (core.RevertOutcome, error) {
	return core.RevertOutcome{}, nil
}

// eraModule has a compensable and a revertible agent action and a
// compensable controller action, each with eraIn as input.
type eraModule struct{}

func (eraModule) Manifest() core.Manifest {
	return core.Manifest{ID: "era", Version: 1, Actions: []core.ActionDef{
		core.DefineMutating[eraIn, struct{}](core.ActionSpec{Name: "era.pass.set", Version: 1, Class: core.ClassCompensable,
			Side: core.SideAgent, LockKeys: []string{"era"}, ForwardOnly: true, Converges: true}, eraHandler{}),
		core.DefineRevertible[eraIn, struct{}](core.ActionSpec{Name: "era.pass.apply", Version: 1, Side: core.SideAgent,
			LockKeys: []string{"era"}, RepairedBy: "era.pass.set"}, eraHandler{}),
		core.DefineMutating[eraIn, struct{}](core.ActionSpec{Name: "era.ctl.set", Version: 1, Class: core.ClassCompensable,
			Side: core.SideController, LockKeys: []string{"era"}, ForwardOnly: true, Converges: true}, eraHandler{}),
	}}
}

type eraEngine struct {
	eng   *Engine
	db    *store.DB
	clock *coretest.FakeClock
}

func newEraEngine(t *testing.T, side string) eraEngine {
	t.Helper()
	return newEraEngineOn(t, side, openDB(t, side), slog.New(slog.DiscardHandler))
}

// newEraEngineOn is newEraEngine on db, logging to log.
func newEraEngineOn(t *testing.T, side string, db *store.DB, log *slog.Logger) eraEngine {
	t.Helper()
	reg, err := core.NewRegistry(eraModule{})
	if err != nil {
		t.Fatal(err)
	}
	clock := coretest.NewFakeClock(time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC))
	cfg := Config{
		Side: core.Side(side), Registry: reg, Validator: coretest.Validator{}, Redact: schema.Redact,
		Base: core.Env{DB: db, Sealer: coretest.Sealer{}, Clock: clock, Log: log},
	}
	if side == store.SideAgent {
		cfg.Base.HostID, cfg.Journal = testHost, NewAgentJournal(db)
	} else {
		cfg.Journal = &coretest.MemJournal{}
	}
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	return eraEngine{eng: eng, db: db, clock: clock}
}

const eraInput = `{"name":"%s","password":"hunter2","token":"tok-1","keys":[{"id":"k1","passphrase":"pp-1"}]}`

// input is the plan's sealed input, opened; nil when the column is NULL
// and false when the row is gone.
func (x eraEngine) input(t *testing.T, planID string) (map[string]any, bool) {
	t.Helper()
	var ct []byte
	err := x.db.Read().QueryRow("SELECT input_ct FROM core_plans WHERE id = ?", planID).Scan(&ct)
	if err != nil {
		return nil, false
	}
	if ct == nil {
		return nil, true
	}
	plain, err := coretest.Sealer{}.Open(planInputAD(planID), ct)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(plain, &m); err != nil {
		t.Fatal(err)
	}
	return m, true
}

// stored is the input every change row, and on the agent every journal
// row, keeps in the clear.
func (x eraEngine) stored(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	queries := []string{"SELECT input_json FROM core_changes"}
	if x.eng.cfg.Side == core.SideAgent {
		queries = append(queries, "SELECT input_json FROM core_journal")
	}
	for _, q := range queries {
		rows, err := x.db.Read().Query(q)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			b.WriteString(s)
		}
		_ = rows.Close()
	}
	return b.String()
}

func TestEraseOnEnd(t *testing.T) {
	ctx := context.Background()
	alice := core.Principal{Actor: "user:alice", UserID: "alice", Scope: core.ScopeAdmin, Source: "test"}
	plan := func(t *testing.T, x eraEngine, action, name string) string {
		t.Helper()
		v, err := x.eng.Plan(ctx, alice, core.ActionRef{Name: action, Version: 1},
			json.RawMessage(strings.Replace(eraInput, "%s", name, 1)), core.PlanOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return v.PlanID
	}
	purgeLater := func(t *testing.T, x eraEngine) {
		t.Helper()
		x.clock.Advance(PlanTTL + time.Second)
		if err := x.eng.Purge(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, side := range []string{store.SideAgent, store.SideController} {
		action := "era.pass.set"
		if side == store.SideController {
			action = "era.ctl.set"
		}
		cases := map[string]func(t *testing.T, x eraEngine) string{
			"completes": func(t *testing.T, x eraEngine) string {
				id := plan(t, x, action, "ok")
				if v, err := x.eng.Apply(ctx, alice, id, core.ApplyOptions{}); err != nil || v.State != core.StateCompleted {
					t.Fatalf("apply: %+v, %v", v, err)
				}
				return id
			},
			"fails": func(t *testing.T, x eraEngine) string {
				id := plan(t, x, action, "fail")
				if v, err := x.eng.Apply(ctx, alice, id, core.ApplyOptions{}); err == nil && v.State != core.StateFailed {
					t.Fatalf("apply: %+v, want failed", v)
				}
				return id
			},
			"expires unapplied": func(t *testing.T, x eraEngine) string {
				id := plan(t, x, action, "late")
				purgeLater(t, x)
				return id
			},
		}
		if side == store.SideAgent {
			cases["is reverted"] = func(t *testing.T, x eraEngine) string {
				id := plan(t, x, "era.pass.apply", "rev")
				v, err := x.eng.Apply(ctx, alice, id, core.ApplyOptions{})
				if err != nil || v.State != core.StatePending {
					t.Fatalf("apply: %+v, %v", v, err)
				}
				if v, err = x.eng.Revert(ctx, alice, v.ID); err != nil || v.State != core.StateReverted {
					t.Fatalf("revert: %+v, %v", v, err)
				}
				return id
			}
		}
		for name, run := range cases {
			t.Run(side+"/"+name, func(t *testing.T) {
				x := newEraEngine(t, side)
				id := run(t, x)
				if in, ok := x.input(t, id); in != nil {
					t.Errorf("plan row %v (exists %v) still holds a sealed input", in, ok)
				}
				if s := x.stored(t); strings.Contains(s, "hunter2") || strings.Contains(s, "pp-1") || strings.Contains(s, "tok-1") {
					t.Errorf("a change row holds a secret in the clear: %s", s)
				}
			})
		}
	}

	// A change that has not ended, here one waiting for its repair, keeps a
	// plain secret sealed past its plan's expiry, but not its erase fields.
	t.Run("agent/expires while the change waits for repair", func(t *testing.T) {
		x := newEraEngine(t, store.SideAgent)
		id := plan(t, x, "era.pass.apply", "wait")
		v, err := x.eng.Apply(ctx, alice, id, core.ApplyOptions{})
		if err != nil || v.State != core.StatePending {
			t.Fatalf("apply: %+v, %v", v, err)
		}
		if _, err := x.db.Writer().Exec("UPDATE core_changes SET state = ?, recovery_owner = '' WHERE id = ?",
			core.StateNeedsRepair, v.ID); err != nil {
			t.Fatal(err)
		}
		if in, _ := x.input(t, id); in["password"] != "hunter2" {
			t.Fatalf("before the plan expired the input was %v", in)
		}
		purgeLater(t, x)
		in, ok := x.input(t, id)
		if !ok || in == nil {
			t.Fatalf("the plan row or its input is gone (row %v)", ok)
		}
		keys, _ := in["keys"].([]any)
		key, _ := keys[0].(map[string]any)
		if _, has := in["password"]; has || in["token"] != "tok-1" || in["name"] != "wait" || key["id"] != "k1" {
			t.Errorf("input after expiry %v, want password gone and the rest kept", in)
		}
		if _, has := key["passphrase"]; has {
			t.Errorf("nested erase field kept: %v", key)
		}
		// A change a mechanism holds keeps every field.
		y := newEraEngine(t, store.SideAgent)
		held := plan(t, y, "era.pass.apply", "held")
		hv, err := y.eng.Apply(ctx, alice, held, core.ApplyOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := y.db.Writer().Exec("UPDATE core_changes SET state = ?, recovery_owner = ? WHERE id = ?",
			core.StateNeedsRepair, core.OwnerRepair, hv.ID); err != nil {
			t.Fatal(err)
		}
		purgeLater(t, y)
		if in, _ := y.input(t, held); in["password"] != "hunter2" {
			t.Errorf("a held change lost its erase field: %v", in)
		}
		// A second purge finds nothing left to erase.
		before, _ := x.input(t, id)
		purgeLater(t, x)
		if after, _ := x.input(t, id); !reflect.DeepEqual(after, before) {
			t.Errorf("second purge changed %v to %v", before, after)
		}
	})

	// A change repaired from its stored input is repaired while its plan
	// lives, and refused once the purge may have erased its secrets.
	t.Run("agent/repair after the erase", func(t *testing.T) {
		x := newEraEngine(t, store.SideAgent)
		id := plan(t, x, "era.pass.set", "lost")
		// The change an apply cut off by a crash leaves in needs_repair.
		now := formatTime(x.clock.Now())
		if _, err := x.db.Writer().Exec(`INSERT INTO core_changes (id, plan_id, host_id, action, version, class,
			compensated_by, actor, state, input_json, created_at, updated_at)
			VALUES ('C-LOST', ?, ?, 'era.pass.set', 1, 'compensable', '', 'user:alice', ?, '{}', ?, ?)`,
			id, testHost, core.StateNeedsRepair, now, now); err != nil {
			t.Fatal(err)
		}
		if res, err := x.eng.Repair(ctx, alice, "C-LOST", core.RepairRequest{}); err != nil || res.Plan == nil {
			t.Fatalf("repair before the plan expired: %+v, %v", res, err)
		}
		purgeLater(t, x)
		_, err := x.eng.Repair(ctx, alice, "C-LOST", core.RepairRequest{})
		if err == nil || !strings.Contains(err.Error(), "secret inputs were erased") {
			t.Errorf("repair after the erase: %v, want a refusal naming the erase", err)
		}
	})
}

// lockedLog is a log sink safe for the engine's goroutines.
type lockedLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// TestEraseLeavesNoBytes: a sealed input the store deleted or replaced
// leaves no bytes in the database file or its WAL once a purge ran, and a
// reader that keeps the purge's checkpoint from completing is logged.
func TestEraseLeavesNoBytes(t *testing.T) {
	ctx := context.Background()
	alice := core.Principal{Actor: "user:alice", UserID: "alice", Scope: core.ScopeAdmin, Source: "test"}
	path := filepath.Join(t.TempDir(), "agent.db")
	db, err := store.Open(ctx, path, store.WithBusyTimeout(100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	mopts := store.MigrateOptions{Side: store.SideAgent, BinaryVersion: "test", BackupRoot: filepath.Join(filepath.Dir(path), "backups"),
		Keep: 5, Now: time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)}
	if _, err := store.Migrate(ctx, db, mopts, set, nil); err != nil {
		t.Fatal(err)
	}
	var secure int
	if err := db.Writer().QueryRowContext(ctx, "PRAGMA secure_delete").Scan(&secure); err != nil || secure != 1 {
		t.Errorf("writer PRAGMA secure_delete = %d, %v; want 1", secure, err)
	}
	logs := &lockedLog{}
	x := newEraEngineOn(t, store.SideAgent, db, slog.New(slog.NewTextHandler(logs, nil)))

	// sealed applies an era.pass.apply change, which stays pending, and
	// returns it with its plan's sealed input.
	sealed := func(t *testing.T, name string) (change, plan string, ct []byte) {
		t.Helper()
		v, err := x.eng.Plan(ctx, alice, core.ActionRef{Name: "era.pass.apply", Version: 1},
			json.RawMessage(strings.Replace(eraInput, "%s", name, 1)), core.PlanOptions{})
		if err != nil {
			t.Fatal(err)
		}
		c, err := x.eng.Apply(ctx, alice, v.PlanID, core.ApplyOptions{})
		if err != nil || c.State != core.StatePending {
			t.Fatalf("apply: %+v, %v", c, err)
		}
		if err := db.Read().QueryRowContext(ctx, "SELECT input_ct FROM core_plans WHERE id = ?", v.PlanID).Scan(&ct); err != nil || len(ct) == 0 {
			t.Fatalf("the plan's sealed input: %d bytes, %v", len(ct), err)
		}
		return c.ID, v.PlanID, ct
	}
	// onDisk reports which of the database and WAL files hold b.
	onDisk := func(t *testing.T, b []byte) []string {
		t.Helper()
		var in []string
		for _, p := range []string{path, path + "-wal"} {
			data, err := os.ReadFile(p) //nolint:gosec // G304: the test's database
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			if bytes.Contains(data, b) {
				in = append(in, filepath.Base(p))
			}
		}
		return in
	}
	purge := func(t *testing.T) {
		t.Helper()
		if err := x.eng.Purge(ctx); err != nil {
			t.Fatal(err)
		}
	}
	end := func(t *testing.T, change string) {
		t.Helper()
		if v, err := x.eng.Revert(ctx, alice, change); err != nil || v.State != core.StateReverted {
			t.Fatalf("revert: %+v, %v", v, err)
		}
	}

	// An unapplied plan's expiry deletes its row. The checkpoint first
	// copies the row's page into the database file; only secure_delete
	// zeroes the cell the delete frees there.
	v, err := x.eng.Plan(ctx, alice, core.ActionRef{Name: "era.pass.apply", Version: 1},
		json.RawMessage(strings.Replace(eraInput, "%s", "unapplied", 1)), core.PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var unappliedCT []byte
	if err := db.Read().QueryRowContext(ctx, "SELECT input_ct FROM core_plans WHERE id = ?", v.PlanID).Scan(&unappliedCT); err != nil {
		t.Fatal(err)
	}
	purge(t)
	if in := onDisk(t, unappliedCT); len(in) != 1 || in[0] != "agent.db" {
		t.Fatalf("the unapplied plan's sealed input is in %v after a checkpoint, want agent.db only", in)
	}
	x.clock.Advance(PlanTTL + time.Second)
	purge(t)
	if in := onDisk(t, unappliedCT); len(in) > 0 {
		t.Errorf("an expired plan's sealed input is still in %v after the purge deleted it", in)
	}

	// A change's end deletes its sealed input.
	ended, _, endedCT := sealed(t, "ended")
	if len(onDisk(t, endedCT)) == 0 {
		t.Fatal("the open change's sealed input is in neither file: the search finds nothing")
	}
	// The purge's checkpoint copies the page holding it into the database
	// file, where its end frees the cell: only secure_delete zeroes it.
	purge(t)
	if in := onDisk(t, endedCT); len(in) != 1 || in[0] != "agent.db" {
		t.Fatalf("the open change's sealed input is in %v after a checkpoint, want agent.db only", in)
	}
	end(t, ended)
	purge(t)
	if in := onDisk(t, endedCT); len(in) > 0 {
		t.Errorf("an ended change's sealed input is still in %v after a purge", in)
	}

	// A reader keeps the checkpoint from completing; the next purge
	// without one finishes it.
	held, _, heldCT := sealed(t, "held")
	tx, err := db.Read().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM core_plans").Scan(&n); err != nil {
		t.Fatal(err)
	}
	end(t, held)
	purge(t)
	if !strings.Contains(logs.String(), "checkpoint") {
		t.Errorf("log = %q, want a warning that the checkpoint did not complete", logs.String())
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	purge(t)
	if in := onDisk(t, heldCT); len(in) > 0 {
		t.Errorf("an ended change's sealed input is still in %v after a purge with no reader", in)
	}

	// A change waiting for its repair keeps its input past the plan's
	// expiry; the purge reseals it without its erase fields.
	waiting, waitingPlan, waitingCT := sealed(t, "waiting")
	if _, err := db.Writer().ExecContext(ctx, "UPDATE core_changes SET state = ?, recovery_owner = '' WHERE id = ?",
		core.StateNeedsRepair, waiting); err != nil {
		t.Fatal(err)
	}
	if len(onDisk(t, waitingCT)) == 0 {
		t.Fatal("the waiting change's sealed input is in neither file before the purge")
	}
	x.clock.Advance(PlanTTL + time.Second)
	purge(t)
	if in, _ := x.input(t, waitingPlan); in == nil || in["password"] != nil {
		t.Fatalf("the waiting change's input after the purge = %v, want it resealed without the password", in)
	}
	if in := onDisk(t, waitingCT); len(in) > 0 {
		t.Errorf("the sealed input the purge replaced is still in %v", in)
	}
}
