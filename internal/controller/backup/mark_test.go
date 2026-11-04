package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/quarantine"
	"github.com/darkraise/darkpanel/internal/core/store"
)

var (
	mkNonTerminalStates = []string{"applying", "pending", "awaiting_confirmation", "undoing", "needs_repair", "recovering", "releasing"}
	mkTerminalStates    = []string{"confirmed", "reverted", "completed", "failed", "interrupted", "repaired", "kept", "abandoned"}
	mkRestoredAt        = time.Date(2026, 11, 4, 16, 30, 15, 123456789, time.FixedZone("plus1", 3600))
)

// mkStamp is mkRestoredAt in the lifecycle time format, in UTC.
const mkStamp = "2026-11-04T15:30:15.123456789Z"

func mkMarker() quarantine.Marker {
	return quarantine.Marker{
		Tx:              "01JTXMARKER000000000000000",
		RestoredAt:      mkRestoredAt,
		BackupCreatedAt: time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC),
	}
}

func mkMark(t *testing.T, db *store.DB) {
	t.Helper()
	if err := Mark(context.Background(), db, mkMarker()); err != nil {
		t.Fatalf("Mark: %v", err)
	}
}

// mkDump renders every row of query as one string, columns joined by "|"; a
// NULL renders as "<nil>".
func mkDump(t *testing.T, db *store.DB, query string) []string {
	t.Helper()
	rows, err := db.Read().QueryContext(context.Background(), query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(cols))
		for i, v := range vals {
			parts[i] = fmt.Sprint(v)
		}
		out = append(out, strings.Join(parts, "|"))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// mkByID maps the first column of each dumped row to the whole row.
func mkByID(rows []string) map[string]string {
	m := make(map[string]string, len(rows))
	for _, r := range rows {
		id, _, _ := strings.Cut(r, "|")
		m[id] = r
	}
	return m
}

func mkExec(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Writer().ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

const mkChangeQuery = `SELECT id, state, COALESCE(quarantined_from, 'NULL'), recovery_owner, updated_at FROM core_changes ORDER BY id`

func TestMkNonTerminalIsTheSevenUnfinishedStates(t *testing.T) {
	got := slices.Sorted(slices.Values(NonTerminal))
	want := slices.Sorted(slices.Values(mkNonTerminalStates))
	if !slices.Equal(got, want) {
		t.Errorf("NonTerminal = %v, want the states of spec 1.3 without quarantined: %v", got, want)
	}
}

func TestMkQuarantinesEveryNonTerminalChangeOnly(t *testing.T) {
	db := fxDB(t, "")
	for _, s := range mkNonTerminalStates {
		fxInsertChange(t, db, fxChange{ID: "n-" + s, HostID: "host-a", Action: "fixture.act", State: s, Owner: "engine"})
	}
	for _, s := range mkTerminalStates {
		fxInsertChange(t, db, fxChange{ID: "t-" + s, HostID: "host-a", Action: "fixture.act", State: s})
	}
	before := mkByID(mkDump(t, db, mkChangeQuery))
	mkMark(t, db)
	after := mkByID(mkDump(t, db, mkChangeQuery))
	for _, s := range mkNonTerminalStates {
		id := "n-" + s
		want := fmt.Sprintf("%s|quarantined|%s||%s", id, s, mkStamp)
		if after[id] != want {
			t.Errorf("change in %s:\n got %s\nwant %s", s, after[id], want)
		}
	}
	for _, s := range mkTerminalStates {
		id := "t-" + s
		if after[id] != before[id] {
			t.Errorf("terminal change in %s changed:\n got %s\nwant %s", s, after[id], before[id])
		}
		if !strings.Contains(after[id], "|"+s+"|NULL|") {
			t.Errorf("terminal change in %s: %s, want state kept and no quarantined_from", s, after[id])
		}
	}
}

func TestMkLeavesCompositesAndTheirStepsUnwound(t *testing.T) {
	db := fxDB(t, "")
	steps := []fxStep{
		{ID: "s1", HostID: "host-a", Action: "fixture.one/v1", State: "applied", ChildPlanID: "plan-child", ChildChangeID: "child-change"},
		{ID: "s2", HostID: "host-b", Action: "fixture.two/v1", State: "pending"},
	}
	for _, s := range mkNonTerminalStates {
		dir := "forward"
		if s == "undoing" || s == "releasing" {
			dir = "unwind"
		}
		fxInsertComposite(t, db, "k-"+s, s, dir, steps)
	}
	for _, s := range []string{"completed", "abandoned", "failed", "interrupted"} {
		fxInsertComposite(t, db, "kt-"+s, s, "forward", steps)
	}
	fxInsertChange(t, db, fxChange{ID: "child-of-applying", HostID: "host-a", Action: "fixture.one", State: "applying", ParentID: "k-applying"})
	compositesBefore := mkDump(t, db, `SELECT * FROM core_composites ORDER BY change_id`)
	stepsBefore := mkDump(t, db, `SELECT * FROM core_composite_steps ORDER BY change_id, step_id`)
	terminalBefore := mkByID(mkDump(t, db, mkChangeQuery))

	mkMark(t, db)

	after := mkByID(mkDump(t, db, mkChangeQuery))
	for _, s := range mkNonTerminalStates {
		id := "k-" + s
		if want := fmt.Sprintf("%s|quarantined|%s||%s", id, s, mkStamp); after[id] != want {
			t.Errorf("composite in %s:\n got %s\nwant %s", s, after[id], want)
		}
	}
	for _, s := range []string{"completed", "abandoned", "failed", "interrupted"} {
		if id := "kt-" + s; after[id] != terminalBefore[id] {
			t.Errorf("terminal composite %s changed:\n got %s\nwant %s", s, after[id], terminalBefore[id])
		}
	}
	if want := fmt.Sprintf("child-of-applying|quarantined|applying||%s", mkStamp); after["child-of-applying"] != want {
		t.Errorf("child change:\n got %s\nwant %s", after["child-of-applying"], want)
	}
	if got := mkDump(t, db, `SELECT * FROM core_composites ORDER BY change_id`); !slices.Equal(got, compositesBefore) {
		t.Errorf("core_composites changed:\n got %v\nwant %v", got, compositesBefore)
	}
	if got := mkDump(t, db, `SELECT * FROM core_composite_steps ORDER BY change_id, step_id`); !slices.Equal(got, stepsBefore) {
		t.Errorf("core_composite_steps changed:\n got %v\nwant %v", got, stepsBefore)
	}
}

func TestMkKeepsRecordsThatAreAlreadyQuarantined(t *testing.T) {
	db := fxDB(t, "")
	ctx := context.Background()
	fxInsertChange(t, db, fxChange{ID: "q-change", Action: "fixture.act", State: "quarantined"})
	mkExec(t, db, `UPDATE core_changes SET quarantined_from = 'needs_repair' WHERE id = 'q-change'`)
	fxInsertRollout(t, db, "q-rollout", "quarantined", "host-a")
	mkExec(t, db, `UPDATE core_rollouts SET quarantined_from = 'draining' WHERE id = 'q-rollout'`)
	fxInsertChange(t, db, fxChange{ID: "n-change", Action: "fixture.act", State: "applying"})
	mkExec(t, db, `INSERT INTO core_meta (key, value) VALUES (?, ?)`, quarantine.MetaKey,
		`{"tx":"OLDTX","restoredAt":"2026-10-01T00:00:00Z","backupCreatedAt":"2026-09-30T00:00:00Z"}`)
	changeBefore := mkByID(mkDump(t, db, mkChangeQuery))["q-change"]
	rolloutBefore := mkDump(t, db, `SELECT * FROM core_rollouts WHERE id = 'q-rollout'`)

	mkMark(t, db)

	if got := mkByID(mkDump(t, db, mkChangeQuery))["q-change"]; got != changeBefore {
		t.Errorf("already quarantined change:\n got %s\nwant %s", got, changeBefore)
	}
	if got := mkDump(t, db, `SELECT * FROM core_rollouts WHERE id = 'q-rollout'`); !slices.Equal(got, rolloutBefore) {
		t.Errorf("already quarantined rollout:\n got %v\nwant %v", got, rolloutBefore)
	}
	if got, want := mkByID(mkDump(t, db, mkChangeQuery))["n-change"], fmt.Sprintf("n-change|quarantined|applying||%s", mkStamp); got != want {
		t.Errorf("unfinished change next to them:\n got %s\nwant %s", got, want)
	}
	m, ok, err := quarantine.Read(ctx, db)
	if err != nil || !ok || m.Tx != mkMarker().Tx {
		t.Errorf("marker after a second restore = %+v, %v, %v; want tx %s from the new restore", m, ok, err, mkMarker().Tx)
	}
}

func TestMkRollouts(t *testing.T) {
	cases := []struct {
		name, state, preFrom, wantState, wantFrom string
		touched                                   bool
	}{
		{"running", "running", "", "quarantined", "running", true},
		{"draining", "draining", "", "quarantined", "draining", true},
		{"succeeded", "succeeded", "", "succeeded", "NULL", false},
		{"failed", "failed", "", "failed", "NULL", false},
		{"cancelled", "cancelled", "", "cancelled", "NULL", false},
		{"quarantined", "quarantined", "running", "quarantined", "running", false},
	}
	const q = `SELECT state, COALESCE(quarantined_from, 'NULL'), COALESCE(outcome, 'NULL'), updated_at FROM core_rollouts WHERE id = 'r1'`
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := fxDB(t, "")
			fxInsertRollout(t, db, "r1", c.state, "host-a", "host-b")
			if c.preFrom != "" {
				mkExec(t, db, `UPDATE core_rollouts SET quarantined_from = ? WHERE id = 'r1'`, c.preFrom)
			}
			before := strings.Split(mkDump(t, db, q)[0], "|")
			hostsBefore := mkDump(t, db, `SELECT * FROM core_rollout_hosts ORDER BY rollout_id, host_id`)
			if len(hostsBefore) != 2 {
				t.Fatalf("fixture rollout hosts = %v, want 2 rows", hostsBefore)
			}

			mkMark(t, db)

			got := strings.Split(mkDump(t, db, q)[0], "|")
			wantUpdated := before[3]
			if c.touched {
				wantUpdated = mkStamp
			}
			if got[0] != c.wantState || got[1] != c.wantFrom || got[2] != before[2] || got[3] != wantUpdated {
				t.Errorf("rollout %s = %v, want state %s, quarantined_from %s, outcome %s, updated_at %s",
					c.state, got, c.wantState, c.wantFrom, before[2], wantUpdated)
			}
			if after := mkDump(t, db, `SELECT * FROM core_rollout_hosts ORDER BY rollout_id, host_id`); !slices.Equal(after, hostsBefore) {
				t.Errorf("rollout hosts changed:\n got %v\nwant %v", after, hostsBefore)
			}
		})
	}
}

func TestMkClosesOnlyOpenListeners(t *testing.T) {
	db := fxDB(t, "")
	for id, state := range map[string]string{"l-open": "open", "l-revoked": "revoked", "l-closed": "closed"} {
		fxInsertListener(t, db, id, state)
	}
	const q = `SELECT id, state, updated_at FROM core_listeners ORDER BY id`
	before := mkByID(mkDump(t, db, q))
	mkMark(t, db)
	after := mkByID(mkDump(t, db, q))
	if want := "l-open|closed|" + mkStamp; after["l-open"] != want {
		t.Errorf("open listener = %s, want %s", after["l-open"], want)
	}
	for _, id := range []string{"l-revoked", "l-closed"} {
		if after[id] != before[id] {
			t.Errorf("listener %s changed:\n got %s\nwant %s", id, after[id], before[id])
		}
	}
}

func TestMkDisposesOfLocksByOwner(t *testing.T) {
	db := fxDB(t, "")
	fxInsertChange(t, db, fxChange{ID: "c-live", Action: "fixture.act", State: "applying", Owner: "engine"})
	fxInsertChange(t, db, fxChange{ID: "c-repair", Action: "fixture.act", State: "needs_repair"})
	fxInsertChange(t, db, fxChange{ID: "c-already", Action: "fixture.act", State: "quarantined"})
	fxInsertChange(t, db, fxChange{ID: "c-done", Action: "fixture.act", State: "completed"})
	fxInsertLock(t, db, "k.live1", "c-live")
	fxInsertLock(t, db, "k.live2", "c-live")
	fxInsertLock(t, db, "k.repair", "c-repair")
	fxInsertLock(t, db, "k.already", "c-already")
	fxInsertLock(t, db, "k.done", "c-done")
	fxInsertLock(t, db, "k.update", "01JUPDATE0000000000000000A")
	fxInsertLock(t, db, "k.orphan", "no-such-owner")

	mkMark(t, db)

	want := []string{"k.already|c-already", "k.live1|c-live", "k.live2|c-live", "k.repair|c-repair"}
	if got := mkDump(t, db, `SELECT key, owner FROM core_locks ORDER BY key`); !slices.Equal(got, want) {
		t.Errorf("locks after marking:\n got %v\nwant %v (a terminal change's, an update reservation's and an orphan's are deleted)", got, want)
	}
}

const mkTaskRunsDDL = `CREATE TABLE core_task_runs (
    change_id     TEXT NOT NULL REFERENCES core_changes (id),
    op            TEXT NOT NULL CHECK (op IN ('apply', 'undo', 'recover')),
    attempt       INTEGER NOT NULL CHECK (attempt >= 1),
    state         TEXT NOT NULL CHECK (state IN ('starting', 'running', 'succeeded', 'failed', 'cancelled')),
    output_ct     BLOB,
    error_code    TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    pid           INTEGER,
    started_at    TEXT NOT NULL,
    finished_at   TEXT,
    PRIMARY KEY (change_id, op, attempt)
) STRICT`

func TestMkTaskRuns(t *testing.T) {
	const tableCount = `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'core_task_runs'`
	t.Run("absent", func(t *testing.T) {
		db := fxDB(t, "")
		if got := mkDump(t, db, tableCount); got[0] != "0" {
			t.Fatalf("the controller schema has core_task_runs (%s); this case needs it absent", got[0])
		}
		fxInsertChange(t, db, fxChange{ID: "c1", Action: "fixture.act", State: "applying"})
		mkMark(t, db)
		if got := mkByID(mkDump(t, db, mkChangeQuery))["c1"]; !strings.Contains(got, "|quarantined|applying|") {
			t.Errorf("change = %s, want quarantined from applying", got)
		}
	})
	t.Run("present", func(t *testing.T) {
		db := fxDB(t, "")
		mkExec(t, db, mkTaskRunsDDL)
		fxInsertChange(t, db, fxChange{ID: "c1", Action: "fixture.act", State: "applying"})
		fxInsertChange(t, db, fxChange{ID: "c2", Action: "fixture.act", State: "completed"})
		for _, run := range [][]any{{"c1", "apply", 1, "running"}, {"c1", "recover", 2, "starting"}, {"c2", "apply", 1, "succeeded"}} {
			mkExec(t, db, `INSERT INTO core_task_runs (change_id, op, attempt, state, started_at) VALUES (?, ?, ?, ?, '2026-11-04T12:00:00Z')`, run...)
		}
		mkMark(t, db)
		if got := mkDump(t, db, `SELECT count(*) FROM core_task_runs`); got[0] != "0" {
			t.Errorf("core_task_runs rows after marking = %s, want 0", got[0])
		}
		if got := mkDump(t, db, tableCount); got[0] != "1" {
			t.Errorf("core_task_runs tables after marking = %s, want the table kept", got[0])
		}
	})
}

func TestMkIsAtomic(t *testing.T) {
	cases := []struct{ name, trigger string }{
		{"the listener update fails", `CREATE TRIGGER mk_fail BEFORE UPDATE ON core_listeners BEGIN SELECT RAISE(ABORT, 'mk: injected failure'); END`},
		{"the lock delete fails", `CREATE TRIGGER mk_fail BEFORE DELETE ON core_locks BEGIN SELECT RAISE(ABORT, 'mk: injected failure'); END`},
	}
	tables := []string{"core_meta", "core_changes", "core_rollouts", "core_locks", "core_listeners"}
	dump := func(t *testing.T, db *store.DB) map[string][]string {
		out := map[string][]string{}
		for _, tbl := range tables {
			out[tbl] = mkDump(t, db, "SELECT * FROM "+tbl+" ORDER BY 1")
		}
		return out
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := fxDB(t, "")
			fxInsertChange(t, db, fxChange{ID: "c1", Action: "fixture.act", State: "applying", Owner: "engine"})
			fxInsertRollout(t, db, "r1", "running", "host-a")
			fxInsertListener(t, db, "l1", "open")
			fxInsertLock(t, db, "k.one", "c1")
			fxInsertLock(t, db, "k.update", "01JUPDATE0000000000000000A")
			mkExec(t, db, c.trigger)
			before := dump(t, db)

			err := Mark(context.Background(), db, mkMarker())
			if err == nil || !strings.Contains(err.Error(), "injected failure") {
				t.Fatalf("Mark = %v, want the injected failure", err)
			}
			after := dump(t, db)
			for _, tbl := range tables {
				if !slices.Equal(after[tbl], before[tbl]) {
					t.Errorf("%s changed although Mark failed:\n got %v\nwant %v", tbl, after[tbl], before[tbl])
				}
			}
			if _, ok, err := quarantine.Read(context.Background(), db); err != nil || ok {
				t.Errorf("quarantine flag after a failed Mark = %v, %v; want unset", ok, err)
			}

			mkExec(t, db, `DROP TRIGGER mk_fail`)
			mkMark(t, db)
			if got := mkByID(mkDump(t, db, mkChangeQuery))["c1"]; !strings.Contains(got, "|quarantined|applying|") {
				t.Errorf("change after the retry = %s, want quarantined from applying", got)
			}
		})
	}
}

func TestMkWritesTheMarker(t *testing.T) {
	db := fxDB(t, "")
	ctx := context.Background()
	m := mkMarker()
	if err := Mark(ctx, db, m); err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	raw := mkDump(t, db, `SELECT value FROM core_meta WHERE key = 'restore_quarantine'`)
	if len(raw) != 1 || raw[0] != string(want) {
		t.Errorf("core_meta restore_quarantine = %v, want %s", raw, want)
	}
	got, ok, err := quarantine.Read(ctx, db)
	if err != nil || !ok || got.Tx != m.Tx || !got.RestoredAt.Equal(m.RestoredAt) || !got.BackupCreatedAt.Equal(m.BackupCreatedAt) {
		t.Errorf("quarantine.Read = %+v, %v, %v; want %+v", got, ok, err, m)
	}
	m.Tx = "01JTXOTHER0000000000000000"
	if err := Mark(ctx, db, m); err != nil {
		t.Fatalf("marking a marked database: %v", err)
	}
	if got, _, _ := quarantine.Read(ctx, db); got.Tx != m.Tx {
		t.Errorf("tx after a second Mark = %s, want %s", got.Tx, m.Tx)
	}
}
