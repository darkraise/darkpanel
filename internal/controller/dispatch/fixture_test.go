package dispatch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform"
)

// The fixture modules are registered only in these tests:
//   - kv keeps items in its agent table kv_items, with a read, a revertible
//     set and a resource;
//   - bad has an agent migration that fails;
//   - off is disabled by default;
//   - flaky fails its preflight check;
//   - ctl has controller-side actions only.

var t0 = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

const testHost = "h1"

var (
	refGet    = core.ActionRef{Name: "kv.item.get", Version: 1}
	refSet    = core.ActionRef{Name: "kv.item.set", Version: 1}
	refCtlGet = core.ActionRef{Name: "ctl.note.get", Version: 1}
	refCtlSet = core.ActionRef{Name: "ctl.note.set", Version: 1}
)

type itemIn struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

type itemOut struct {
	Value string `json:"value"`
	Actor string `json:"actor"`
}

type status struct {
	Count int    `json:"count"`
	Token string `json:"token" secret:"true"`
}

func readItem(ctx context.Context, env core.Env, name string) (string, bool, error) {
	var v string
	err := env.DB.Read().QueryRowContext(ctx, "SELECT value FROM kv_items WHERE name = ?", name).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

type getItem struct{}

func (getItem) Read(ctx context.Context, env core.Env, in itemIn) (itemOut, error) {
	v, _, err := readItem(ctx, env, in.Name)
	return itemOut{Value: v, Actor: env.Principal.Actor}, err
}

type itemSnap struct {
	Name, Value string
	Present     bool
}

type setItem struct{}

func (setItem) Plan(ctx context.Context, env core.Env, in itemIn) (core.PlanOutcome, error) {
	v, _, err := readItem(ctx, env, in.Name)
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: "+", Text: in.Name + "=" + in.Value}}, StateHash: v}, err
}

func (setItem) Apply(ctx context.Context, env core.Env, in itemIn) (itemOut, error) {
	return itemOut{Value: in.Value}, env.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO kv_items (name, value) VALUES (?, ?)
			ON CONFLICT (name) DO UPDATE SET value = excluded.value`, in.Name, in.Value)
		return err
	})
}

func (setItem) Verify(ctx context.Context, env core.Env, in itemIn, _ itemOut) error {
	v, _, err := readItem(ctx, env, in.Name)
	if err == nil && v != in.Value {
		err = fmt.Errorf("item %s is %q", in.Name, v)
	}
	return err
}

func (setItem) Snapshot(ctx context.Context, env core.Env, in itemIn) ([]byte, error) {
	v, ok, err := readItem(ctx, env, in.Name)
	if err != nil {
		return nil, err
	}
	return json.Marshal(itemSnap{Name: in.Name, Value: v, Present: ok})
}

func (setItem) Revert(ctx context.Context, env core.Env, snapshot []byte) (core.RevertOutcome, error) {
	var s itemSnap
	if err := json.Unmarshal(snapshot, &s); err != nil {
		return core.RevertOutcome{}, err
	}
	return core.RevertOutcome{}, env.DB.Write(ctx, func(tx *sql.Tx) error {
		if !s.Present {
			_, err := tx.ExecContext(ctx, "DELETE FROM kv_items WHERE name = ?", s.Name)
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE kv_items SET value = ? WHERE name = ?", s.Value, s.Name)
		return err
	})
}

// repairIn is empty: a repair request may send no input at all.
type repairIn struct{}

// repairItem is kv.item.set's repair action; it changes nothing.
type repairItem struct{}

func (repairItem) Plan(context.Context, core.Env, repairIn) (core.PlanOutcome, error) {
	return core.PlanOutcome{StateHash: "repair"}, nil
}

func (repairItem) Apply(context.Context, core.Env, repairIn) (repairIn, error) {
	return repairIn{}, nil
}

func (repairItem) Verify(context.Context, core.Env, repairIn, repairIn) error { return nil }

type kvModule struct{}

func (kvModule) Manifest() core.Manifest {
	return core.Manifest{
		ID: "kv", Version: 1, DefaultEnabled: true,
		Actions: []core.ActionDef{
			core.DefineRead(core.ActionSpec{Name: refGet.Name, Version: 1, Side: core.SideAgent}, getItem{}),
			core.DefineRevertible(core.ActionSpec{
				Name: refSet.Name, Version: 1, Side: core.SideAgent, LockKeys: []string{"kv"}, RepairedBy: "kv.item.repair",
			}, setItem{}),
			core.DefineMutating(core.ActionSpec{
				Name: "kv.item.repair", Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
				LockKeys: []string{"kv"}, ForwardOnly: true, Converges: true,
			}, repairItem{}),
		},
		Resources: []core.ResourceDef{core.DefineResource("kv.status", func(ctx context.Context, env core.Env) (status, error) {
			var n int
			err := env.DB.Read().QueryRowContext(ctx, "SELECT count(*) FROM kv_items").Scan(&n)
			return status{Count: n, Token: "kv-secret-token"}, err
		})},
		Migrations: map[core.Side]fs.FS{core.SideAgent: fstest.MapFS{
			"0001_kv.sql": {Data: []byte("CREATE TABLE kv_items (name TEXT PRIMARY KEY, value TEXT NOT NULL) STRICT;")},
		}},
	}
}

// echo is the read handler of the modules that only need an action.
type echo struct{}

func (echo) Read(_ context.Context, env core.Env, in itemIn) (itemOut, error) {
	return itemOut{Value: in.Name, Actor: env.Principal.Actor}, nil
}

// readOnly is a module with one agent read action <id>.item.get.
type readOnly struct {
	id             string
	defaultEnabled bool
	migrations     map[core.Side]fs.FS
	preflight      []core.PreflightCheck
}

func (m readOnly) Manifest() core.Manifest {
	return core.Manifest{
		ID: m.id, Version: 1, DefaultEnabled: m.defaultEnabled, Migrations: m.migrations, Preflight: m.preflight,
		Actions: []core.ActionDef{core.DefineRead(core.ActionSpec{Name: m.id + ".item.get", Version: 1, Side: core.SideAgent}, echo{})},
	}
}

// note is the controller-side state of module ctl.
type note struct{ text string }

type ctlGet struct{ n *note }

func (h ctlGet) Read(context.Context, core.Env, itemIn) (itemOut, error) {
	return itemOut{Value: h.n.text}, nil
}

type ctlSet struct{ n *note }

func (h ctlSet) Plan(_ context.Context, _ core.Env, in itemIn) (core.PlanOutcome, error) {
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: "+", Text: in.Value}}, StateHash: h.n.text}, nil
}

func (h ctlSet) Apply(_ context.Context, _ core.Env, in itemIn) (itemOut, error) {
	h.n.text = in.Value
	return itemOut{Value: in.Value}, nil
}

func (ctlSet) Verify(context.Context, core.Env, itemIn, itemOut) error { return nil }

type ctlModule struct{ state *note }

func (m ctlModule) Manifest() core.Manifest {
	return core.Manifest{ID: "ctl", Version: 1, DefaultEnabled: true, Actions: []core.ActionDef{
		core.DefineRead(core.ActionSpec{Name: refCtlGet.Name, Version: 1, Side: core.SideController}, ctlGet{m.state}),
		core.DefineMutating(core.ActionSpec{
			Name: refCtlSet.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			CompensatedBy: refCtlSet.Name,
		}, ctlSet{m.state}),
	}}
}

func modules() []core.Module {
	return []core.Module{
		kvModule{},
		readOnly{id: "bad", defaultEnabled: true, migrations: map[core.Side]fs.FS{core.SideAgent: fstest.MapFS{
			"0001_bad.sql": {Data: []byte("CREATE TABLE bad_items (")},
		}}},
		readOnly{id: "off"},
		readOnly{id: "flaky", defaultEnabled: true, preflight: []core.PreflightCheck{{ID: "ready",
			Run: func(context.Context, core.Env) error { return errors.New("flaky: the host is not ready") }}}},
		ctlModule{state: &note{}},
	}
}

// harness is a controller database with the local host h1, served by a real
// agent runtime over its own database.
type harness struct {
	t       *testing.T
	clock   *coretest.FakeClock
	reg     *core.Registry
	db      *store.DB // the controller database
	agentDB *store.DB
	rt      *runtime.Runtime
	clients *StaticClients // the agent client of each host
	hosts   *Hosts
}

func migrated(t *testing.T, path, side string) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(side)
	if err != nil {
		t.Fatal(err)
	}
	opts := store.MigrateOptions{Side: side, BinaryVersion: "test",
		BackupRoot: filepath.Join(filepath.Dir(path), "backups-"+side), Keep: 5, Now: t0}
	if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
		t.Fatal(err)
	}
	return db
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	h := &harness{t: t, clock: coretest.NewFakeClock(t0)}
	var err error
	if h.reg, err = core.NewRegistry(modules()...); err != nil {
		t.Fatal(err)
	}
	h.db = migrated(t, filepath.Join(dir, "controller.db"), store.SideController)
	if _, err := h.db.Writer().ExecContext(ctx,
		"INSERT INTO core_hosts (id, name, is_local, created_at) VALUES (?, 'local', 1, '2026-10-17T00:00:00Z')", testHost); err != nil {
		t.Fatal(err)
	}
	if h.agentDB, err = store.Open(ctx, filepath.Join(dir, "agent.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.agentDB.Close() })
	log := slog.New(slog.DiscardHandler)
	h.rt, err = runtime.Open(ctx, runtime.Config{
		DB: h.agentDB, Sealer: coretest.Sealer{}, Clock: h.clock, Log: log, Registry: h.reg, HostID: testHost,
		BinaryVersion: "test", BackupDir: filepath.Join(dir, "backups-agent"), Exec: platform.NewRunner(nil),
		FilesTestMode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.rt.Close)
	h.clients = NewStaticClients()
	h.clients.Add(testHost, h.rt)
	h.hosts = NewHosts(HostsConfig{DB: h.db, Registry: h.reg, Clients: h.clients, Clock: h.clock, Log: log})
	return h
}

func (h *harness) count(query string, args ...any) int {
	h.t.Helper()
	var n int
	if err := h.db.Read().QueryRow(query, args...).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

func wantCode(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("err = %v, want code %s", err, code)
	}
	return e
}
