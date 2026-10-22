package install

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/update"
)

const (
	barUnitA = "darkpanel-task-01A-apply-1.service"
	barUnitB = "darkpanel-task-01B-undo-1.service"
	barUnitC = "darkpanel-task-01C-apply-2.service"
)

// barDB runs f on h's agent database and closes it.
func barDB(t *testing.T, h *testHost, f func(db *store.DB) error) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, h.layout.Paths.AgentDB())
	if err != nil {
		t.Fatal(err)
	}
	err = f(db)
	if cerr := db.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
}

// barRows counts the uninstall barrier's rows in h's agent database.
func barRows(t *testing.T, h *testHost) int {
	t.Helper()
	var n int
	barDB(t, h, func(db *store.DB) error {
		return db.Read().QueryRow("SELECT count(*) FROM core_meta WHERE key = ?", lifecycle.MetaUninstalling).Scan(&n)
	})
	return n
}

// barOwnedChange leaves a change owned by the engine in h's agent database,
// as an apply that is still running does.
func barOwnedChange(t *testing.T, h *testHost) {
	t.Helper()
	barDB(t, h, func(db *store.DB) error {
		return db.Write(context.Background(), func(tx *sql.Tx) error {
			const at = "2026-10-22T12:00:00.000000000Z"
			if _, err := tx.Exec(`INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash,
				diff_json, guards_json, confirmation, created_at, expires_at)
				VALUES ('01BARPLAN', 'h1', 'demo.item.set', 1, 'user:u1', 'h', 's', '[]', '[]', '', ?, ?)`, at, at); err != nil {
				return err
			}
			_, err := tx.Exec(`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor,
				state, input_json, recovery_owner, created_at, updated_at)
				VALUES ('01BARCHANGE', '01BARPLAN', 'h1', 'demo.item.set', 1, 'revertible', '', 'user:u1', 'applying', '{}',
				'engine', ?, ?)`, at, at)
			return err
		})
	})
}

func TestUninstallBarrierRefusesBusy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(t *testing.T, h *testHost)
		want   string
		absent string
	}{
		{"owned change", barOwnedChange, "change 01BARCHANGE is owned by engine", ""},
		{"active task unit", func(_ *testing.T, h *testHost) {
			h.systemd.setState(barUnitA, "active")
			h.systemd.setState(barUnitB, "deactivating")
			h.systemd.setState(barUnitC, "failed")
		}, "the task units " + barUnitA + ", " + barUnitB + " are active", barUnitC},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, m := installHost(t, RoleController)
			tc.setup(t, h)
			_, err := runUninstall(t, h.uninstallConfig(m, false))
			if e := errcode.From(err); e.Code != errcode.ChangeBusy || !strings.Contains(e.Detail, tc.want) ||
				(tc.absent != "" && strings.Contains(e.Detail, tc.absent)) {
				t.Fatalf("Run = %v, want core.change_busy naming %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), "uninstall: barrier: ") || !strings.Contains(err.Error(), "not run: services, agent lock") {
				t.Errorf("Run = %v, want the barrier step named and every later step not run", err)
			}
			if len(h.systemd.recorded()) != 0 || len(m.calls) != 0 || !exists(t, h.layout.Paths.Binary()) {
				t.Error("a refused uninstall changed the host")
			}
			if n := barRows(t, h); n != 0 {
				t.Errorf("barrier rows after the refusal = %d, want 0", n)
			}
			if want := []string{"darkpanel-task-*.service"}; !reflect.DeepEqual(h.systemd.patterns, want) {
				t.Errorf("listed %q, want %q", h.systemd.patterns, want)
			}
		})
	}
}

func TestUninstallStopTasks(t *testing.T) {
	h, m := installHost(t, RoleController)
	barOwnedChange(t, h)
	h.systemd.setState(barUnitA, "active")
	h.systemd.setState(barUnitB, "activating")
	h.systemd.setState(barUnitC, "inactive")
	// The units step fails, so the run stops with agent.db still there.
	h.systemd.failOn("Reload", errors.New("bus gone"))
	cfg := h.uninstallConfig(m, false)
	cfg.StopTasks = true
	out, err := runUninstall(t, cfg)
	if err == nil || !strings.Contains(err.Error(), "units: bus gone") {
		t.Fatalf("Run = %v\n%s", err, out)
	}
	if !strings.HasPrefix(out, "barrier: set the uninstall barrier and stopped "+barUnitA+", "+barUnitB+"\n") {
		t.Errorf("output\n%s\nwant the barrier step to name the stopped units", out)
	}
	if got := h.systemd.recorded(); len(got) < 3 || !reflect.DeepEqual(got[:3], []string{"Stop " + barUnitA, "Stop " + barUnitB, "Stop darkpanel-controller.service"}) {
		t.Errorf("systemd calls %v, want the active task units stopped before the services", got)
	}
	if n := barRows(t, h); n != 1 {
		t.Errorf("barrier rows = %d, want 1", n)
	}

	h, m = installHost(t, RoleController)
	h.systemd.setState(barUnitA, "active")
	h.systemd.stuck[barUnitA] = true
	cfg = h.uninstallConfig(m, false)
	cfg.StopTasks, cfg.StopTimeout = true, 20*time.Millisecond
	_, err = runUninstall(t, cfg)
	if err == nil || !strings.Contains(err.Error(), "barrier: "+barUnitA+" is still active after 20ms") {
		t.Fatalf("Run = %v, want the barrier step to fail naming the unit", err)
	}
	if got := h.systemd.recorded(); !reflect.DeepEqual(got, []string{"Stop " + barUnitA}) {
		t.Errorf("systemd calls %v, want only the task unit's stop", got)
	}
	if n := barRows(t, h); n != 1 {
		t.Errorf("barrier rows = %d, want the barrier kept for --abort", n)
	}
}

func TestUninstallSetsBarrier(t *testing.T) {
	h, m := installHost(t, RoleController)
	h.systemd.failOn("Reload", errors.New("bus gone"))
	out, err := runUninstall(t, h.uninstallConfig(m, false))
	if err == nil || !strings.HasPrefix(out, "barrier: set the uninstall barrier; no change is unfinished\n") {
		t.Fatalf("Run = %v\n%s", err, out)
	}
	if n := barRows(t, h); n != 1 {
		t.Errorf("barrier rows = %d, want 1", n)
	}

	// --keep-data keeps agent.db; the finished uninstall leaves no barrier
	// for a later install.
	h, m = installHost(t, RoleController)
	if out, err := runUninstall(t, h.uninstallConfig(m, true)); err != nil {
		t.Fatalf("Run: %v\n%s", err, out)
	}
	if n := barRows(t, h); n != 0 {
		t.Errorf("barrier rows after an uninstall with --keep-data = %d, want 0", n)
	}
}

func TestInstallAbortUninstall(t *testing.T) {
	h, _ := installHost(t, RoleController)
	ctx := context.Background()
	barDB(t, h, func(db *store.DB) error { return lifecycle.BeginUninstall(ctx, db, true) })
	cfg := UninstallConfig{Layout: h.layout, Systemd: h.systemd, Actor: "local:root@h1"}
	h.systemd.setState(update.AgentUnit, "inactive")
	line, err := AbortUninstall(ctx, cfg)
	if err != nil || line != "the services were stopped; there is nothing to abort" {
		t.Fatalf("AbortUninstall with the agent stopped = %q, %v", line, err)
	}
	if n := barRows(t, h); n != 1 {
		t.Errorf("barrier rows = %d, want the barrier untouched", n)
	}
	h.systemd.setState(update.AgentUnit, "active")
	line, err = AbortUninstall(ctx, cfg)
	if err != nil || line != "the uninstall barrier is cleared; the agent accepts changes again" {
		t.Fatalf("AbortUninstall with the agent active = %q, %v", line, err)
	}
	if n := barRows(t, h); n != 0 {
		t.Errorf("barrier rows = %d, want 0", n)
	}
	var aborts []string
	barDB(t, h, func(db *store.DB) error {
		entries, err := lifecycle.NewAgentJournal(db).Entries(ctx, 0, 0)
		for _, e := range entries {
			aborts = append(aborts, e.Actor+" "+e.Action+" "+e.Phase+" "+e.Result)
		}
		return err
	})
	if want := []string{"local:root@h1 core.uninstall uninstall-abort ok"}; !reflect.DeepEqual(aborts, want) {
		t.Errorf("journal %q, want %q", aborts, want)
	}
	cfg.Actor = ""
	if _, err := AbortUninstall(ctx, cfg); err == nil || !strings.Contains(err.Error(), "UninstallConfig.Actor is required") {
		t.Errorf("AbortUninstall without an actor = %v", err)
	}
}
