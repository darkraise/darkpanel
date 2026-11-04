package backup

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/store"
)

// pxMigrations is the panel module's controller migration directory, read from the source tree because the module's
// manifest may not list it yet.
const pxMigrations = "../../modules/panel/migrations/controller"

// pxExposure is one panel_exposures row; Change, when set, is the state of the removal change the row names.
type pxExposure struct{ ID, State, Removal, Change string }

// pxMigrate creates panel_exposures from the panel module's migration files unless the database has it already.
func pxMigrate(t *testing.T, db *store.DB) {
	t.Helper()
	ctx := context.Background()
	var n int
	err := db.Read().QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'panel_exposures'`).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n > 0 {
		return
	}
	dir := os.DirFS(pxMigrations)
	files, err := fs.Glob(dir, "*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("panel migrations in %s: %v, %v", pxMigrations, files, err)
	}
	slices.Sort(files)
	for _, name := range files {
		script, err := fs.ReadFile(dir, name)
		if err != nil {
			t.Fatal(err)
		}
		err = db.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, string(script))
			return err
		})
		if err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
}

// pxInsert stores the exposures, with the removal changes they name, in a database that has panel_exposures.
func pxInsert(t *testing.T, db *store.DB, rows ...pxExposure) {
	t.Helper()
	now := fxNow.Format(cbTimeLayout)
	for _, r := range rows {
		if r.Change != "" {
			fxInsertChange(t, db, fxChange{ID: r.Removal, HostID: "host-a", Action: "panel.exposure.remove", Class: "composite", State: r.Change})
		}
		mkExec(t, db, `INSERT INTO panel_exposures (id, via, hostname, host_id, tunnel, subnet, gateway, proxy, port, listener_id,
			rule_id, state, removal_change_id, created_at, updated_at)
			VALUES (?, 'npm', 'panel.example.com', 'host-a', '', '172.31.255.248/29', '172.31.255.249', '172.31.255.250', 9443, 'l1',
			'0RULE', ?, ?, ?, ?)`, r.ID, r.State, r.Removal, now, now)
	}
}

// pxRows maps each exposure id to its state and updated_at.
func pxRows(t *testing.T, db *store.DB) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, row := range mkDump(t, db, `SELECT id, state, updated_at FROM panel_exposures ORDER BY id`) {
		id, rest, _ := strings.Cut(row, "|")
		out[id] = rest
	}
	return out
}

// TestPxMarkSetsEveryLiveExposureToNeedsRepair: an exposure that is open, revoked without a completed removal or
// already marked becomes (or stays) needs_repair; one revoked by a removal that completed is gone and is not touched.
func TestPxMarkSetsEveryLiveExposureToNeedsRepair(t *testing.T) {
	fixture := fxNow.Format(cbTimeLayout)
	tests := []struct {
		name string
		rows []pxExposure
		want map[string]string // id -> "<state>|<updated_at>"
	}{
		{"an open exposure", []pxExposure{{ID: "e1", State: "open"}},
			map[string]string{"e1": "needs_repair|" + mkStamp}},
		{"a revoked exposure with no removal", []pxExposure{{ID: "e1", State: "revoked"}},
			map[string]string{"e1": "needs_repair|" + mkStamp}},
		{"a revoked exposure whose removal is unfinished", []pxExposure{{ID: "e1", State: "revoked", Removal: "rm1", Change: "needs_repair"}},
			map[string]string{"e1": "needs_repair|" + mkStamp}},
		{"a revoked exposure whose removal failed", []pxExposure{{ID: "e1", State: "revoked", Removal: "rm1", Change: "failed"}},
			map[string]string{"e1": "needs_repair|" + mkStamp}},
		{"a revoked exposure whose removal completed", []pxExposure{{ID: "e1", State: "revoked", Removal: "rm1", Change: "completed"}},
			map[string]string{"e1": "revoked|" + fixture}},
		{"an exposure already marked", []pxExposure{{ID: "e1", State: "needs_repair"}},
			map[string]string{"e1": "needs_repair|" + fixture}},
		{"a gone exposure beside an open one", []pxExposure{
			{ID: "e1", State: "revoked", Removal: "rm1", Change: "completed"}, {ID: "e2", State: "open"}},
			map[string]string{"e1": "revoked|" + fixture, "e2": "needs_repair|" + mkStamp}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := pxDB(t, "")
			pxInsert(t, db, tc.rows...)
			mkMark(t, db)
			got := pxRows(t, db)
			if len(got) != len(tc.want) {
				t.Errorf("%d exposures after Mark, want %d: %v", len(got), len(tc.want), got)
			}
			for id, want := range tc.want {
				if got[id] != want {
					t.Errorf("exposure %s after Mark: %q, want %q", id, got[id], want)
				}
			}
		})
	}
}

// TestPxMarkWithoutThePanelTable: a controller database that has no panel_exposures table is marked as before.
func TestPxMarkWithoutThePanelTable(t *testing.T) {
	db := fxDB(t, "")
	mkExec(t, db, `DROP TABLE IF EXISTS panel_exposures`)
	fxInsertChange(t, db, fxChange{ID: "c1", HostID: "host-a", Action: "fixture.act", State: "applying", Owner: "engine"})
	mkMark(t, db)
	if got := mkDump(t, db, `SELECT state FROM core_changes WHERE id = 'c1'`); !slices.Equal(got, []string{"quarantined"}) {
		t.Errorf("change c1 is %v after Mark, want quarantined", got)
	}
}

// pxDB opens a controller database that has panel_exposures.
func pxDB(t *testing.T, dir string) *store.DB {
	t.Helper()
	db := fxDB(t, dir)
	pxMigrate(t, db)
	return db
}

// pxInstalled builds the installed database of vfInstalled with the exposures stored after Mark, as a restore
// leaves them when marking missed one.
func pxInstalled(t *testing.T, rows ...pxExposure) string {
	t.Helper()
	return vfInstalled(t, vfTx, func(t *testing.T, db *store.DB) {
		pxMigrate(t, db)
		pxInsert(t, db, rows...)
	})
}

// TestPxVerifyInstalledAcceptsMarkedExposures: exposures in needs_repair and exposures that are gone pass.
func TestPxVerifyInstalledAcceptsMarkedExposures(t *testing.T) {
	tests := []struct {
		name string
		rows []pxExposure
	}{
		{"no exposure", nil},
		{"an exposure in needs_repair", []pxExposure{{ID: "e1", State: "needs_repair"}}},
		{"a gone exposure", []pxExposure{{ID: "e1", State: "revoked", Removal: "rm1", Change: "completed"}}},
		{"a gone exposure beside one in needs_repair", []pxExposure{
			{ID: "e1", State: "revoked", Removal: "rm1", Change: "completed"}, {ID: "e2", State: "needs_repair"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := pxInstalled(t, tc.rows...)
			if err := VerifyInstalled(context.Background(), path, vfTx); err != nil {
				t.Errorf("VerifyInstalled: %v", err)
			}
		})
	}

	t.Run("a database without the panel table", func(t *testing.T) {
		path := vfInstalled(t, vfTx, func(t *testing.T, db *store.DB) { vfExec(t, db, `DROP TABLE IF EXISTS panel_exposures`) })
		if err := VerifyInstalled(context.Background(), path, vfTx); err != nil {
			t.Errorf("VerifyInstalled: %v", err)
		}
	})
}

// TestPxVerifyInstalledRefusesAnExposureInAnotherState: a live exposure that is not in needs_repair means marking
// did not reach it, so the installed database is not this restore's quarantine, and the error names the exposure.
func TestPxVerifyInstalledRefusesAnExposureInAnotherState(t *testing.T) {
	tests := []struct {
		name string
		rows []pxExposure
		id   string
		want string // the state the error names
	}{
		{"an open exposure", []pxExposure{{ID: "e1", State: "open"}}, "e1", "open"},
		{"a revoked exposure with no removal", []pxExposure{{ID: "e1", State: "revoked"}}, "e1", "revoked"},
		{"a revoked exposure whose removal failed", []pxExposure{{ID: "e1", State: "revoked", Removal: "rm1", Change: "failed"}}, "e1", "revoked"},
		{"an open exposure beside one in needs_repair", []pxExposure{{ID: "e1", State: "needs_repair"}, {ID: "e2", State: "open"}}, "e2", "open"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := pxInstalled(t, tc.rows...)
			err := VerifyInstalled(context.Background(), path, vfTx)
			if !errors.Is(err, ErrNotQuarantined) {
				t.Fatalf("VerifyInstalled = %v, want ErrNotQuarantined", err)
			}
			if !strings.Contains(err.Error(), tc.id) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name exposure %s in state %s", err, tc.id, tc.want)
			}
		})
	}
}
