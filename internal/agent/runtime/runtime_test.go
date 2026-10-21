package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
)

func TestOpenRequiresItsConfig(t *testing.T) {
	f := newFixture(t)
	breaks := map[string]func(*Config){
		"DB":            func(c *Config) { c.DB = nil },
		"Sealer":        func(c *Config) { c.Sealer = nil },
		"Clock":         func(c *Config) { c.Clock = nil },
		"Log":           func(c *Config) { c.Log = nil },
		"Registry":      func(c *Config) { c.Registry = nil },
		"HostID":        func(c *Config) { c.HostID = "" },
		"BinaryVersion": func(c *Config) { c.BinaryVersion = "" },
		"BackupDir":     func(c *Config) { c.BackupDir = "" },
		"Exec":          func(c *Config) { c.Exec = nil },
	}
	for name, brk := range breaks {
		c := f.config()
		brk(&c)
		rt, err := Open(context.Background(), c)
		if err == nil {
			rt.Close()
		}
		if err == nil || !strings.Contains(err.Error(), "Config."+name) {
			t.Errorf("%s: Open = %v, want an error naming Config.%s", name, err, name)
		}
	}
}

func TestOpenMigratesAndChecksEveryModule(t *testing.T) {
	f := newFixture(t)
	if n := f.count("SELECT count(*) FROM sqlite_master WHERE name = 'kv_items'"); n != 1 {
		t.Error("kv's agent migration did not run")
	}
	if n := f.count("SELECT count(*) FROM pragma_table_info('core_journal') WHERE name = 'source'"); n != 1 {
		t.Error("the core agent migrations did not run")
	}
	want := []core.ModuleStatus{
		{ID: "kv", Version: 1, Available: true},
		{ID: "bad", Version: 1, Reason: ReasonMigrationFailed},
		{ID: "flaky", Version: 2, Reason: "flaky: the host is not ready"},
		{ID: "boom", Version: 1, Reason: "preflight check explode of boom panicked"},
	}
	got := f.rt.statuses()
	if len(got) != len(want) {
		t.Fatalf("statuses = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("status %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if f.host.badChecks != 0 {
		t.Errorf("bad's preflight ran %d times, want 0 after its migration failed", f.host.badChecks)
	}
}

func TestOpenLogsAFailedModuleMigration(t *testing.T) {
	f := newFixture(t)
	f.rt.Close()
	var buf bytes.Buffer
	c := f.config()
	c.Log = slog.New(slog.NewTextHandler(&buf, nil))
	rt, err := Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	// The failed migration is retried on every start and logged with its cause.
	if text := buf.String(); !strings.Contains(text, "runtime: module migrations failed") ||
		!strings.Contains(text, "module=bad") || !strings.Contains(text, "0001_bad") {
		t.Errorf("log does not report bad's migration:\n%s", text)
	}
}

func TestOpenWarnsWhenBackupsCannotBePruned(t *testing.T) {
	f := newFixture(t)
	f.rt.Close()
	notDir := filepath.Join(f.dir, "not-a-directory")
	if err := os.WriteFile(notDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	c := f.config()
	c.BackupDir = notDir
	c.Log = slog.New(slog.NewTextHandler(&buf, nil))
	rt, err := Open(context.Background(), c)
	if err != nil {
		t.Fatalf("Open: %v (a failed prune must not stop the runtime)", err)
	}
	defer rt.Close()
	if text := buf.String(); !strings.Contains(text, `level=WARN msg="runtime: old agent database backups could not be pruned"`) {
		t.Errorf("log lacks the prune warning:\n%s", text)
	}
}

func TestHandlersGetTheAgentEnv(t *testing.T) {
	f := newFixture(t)
	raw, err := f.rt.engine.Read(context.Background(), alice, refGet, json.RawMessage(`{"name":"a"}`))
	if err != nil {
		t.Fatal(err)
	}
	var out itemOut
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.HostID != testHost || out.Actor != alice.Actor || !out.HasFiles || !out.HasExec {
		t.Errorf("handler saw %+v, want host h1, alice, files and exec", out)
	}
}

func TestOpenRecoversAndPurges(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	pl, err := f.rt.engine.Plan(ctx, alice, refSet, json.RawMessage(`{"name":"a","value":"1"}`))
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.rt.engine.Apply(ctx, alice, pl.PlanID, core.ApplyOptions{ConfirmTimeout: lifecycle.MaxConfirmTimeout})
	if err != nil || v.State != core.StatePending {
		t.Fatalf("Apply = %+v, %v; want pending", v, err)
	}
	unused, err := f.rt.engine.Plan(ctx, alice, refSet, json.RawMessage(`{"name":"b","value":"1"}`))
	if err != nil {
		t.Fatal(err)
	}
	f.rt.Close() // stops the purge loop and the revert timer; the change stays pending
	f.clock.Advance(lifecycle.PlanTTL)
	f.restart()

	got, err := f.rt.engine.Change(ctx, v.ID)
	if err != nil || got.State != core.StateReverted {
		t.Errorf("change after restart = %+v, %v; want reverted by recovery", got, err)
	}
	if n := f.count("SELECT count(*) FROM kv_items"); n != 0 {
		t.Errorf("kv_items rows = %d, want 0 after the revert", n)
	}
	if n := f.count("SELECT count(*) FROM core_journal WHERE phase = 'recover' AND change_id = ?", v.ID); n != 1 {
		t.Errorf("recover entries = %d, want 1", n)
	}
	if n := f.count("SELECT count(*) FROM core_plans WHERE id = ?", unused.PlanID); n != 0 {
		t.Errorf("the expired unused plan was kept")
	}
}

func TestPurgeRunsEveryIntervalUntilClose(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if p := f.clock.Pending(); p != 1 {
		t.Fatalf("armed timers = %d, want the purge timer", p)
	}
	pl, err := f.rt.engine.Plan(ctx, alice, refSet, json.RawMessage(`{"name":"a","value":"1"}`))
	if err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(PurgeInterval)
	if n := f.count("SELECT count(*) FROM core_plans WHERE id = ?", pl.PlanID); n != 0 {
		t.Error("the purge loop kept an expired plan")
	}
	if p := f.clock.Pending(); p != 1 {
		t.Errorf("armed timers after a purge = %d, want 1", p)
	}
	f.rt.Close()
	if p := f.clock.Pending(); p != 0 {
		t.Errorf("armed timers after Close = %d, want 0", p)
	}
	f.rt.Close() // a second Close is harmless
}
