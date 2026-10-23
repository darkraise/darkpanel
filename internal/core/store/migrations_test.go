package store

import (
	"context"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestCoreMigrationsApply(t *testing.T) {
	tests := []struct {
		side   string
		tables []string
	}{
		{SideController, []string{"core_meta", "core_hosts", "core_module_state", "core_ca",
			"core_agent_certs", "core_enroll_tokens", "core_host_update",
			"core_effects", "core_lock_epochs", "core_composites", "core_composite_steps"}},
		{SideAgent, []string{"core_meta", "core_effects", "core_lock_epochs", "core_task_runs"}},
	}
	for _, tt := range tests {
		t.Run(tt.side, func(t *testing.T) {
			db := openTemp(t)
			set, err := CoreMigrations(tt.side)
			if err != nil {
				t.Fatal(err)
			}
			opts := migrateOpts(t, "v1", time.Now())
			opts.Side = tt.side
			res, err := Migrate(context.Background(), db, opts, set, nil)
			if err != nil {
				t.Fatalf("Migrate: %v", err)
			}
			if len(res.Applied) == 0 {
				t.Fatal("no core migration applied")
			}
			for _, table := range tt.tables {
				if !tableExists(t, db, table) {
					t.Errorf("table %s missing", table)
				}
			}
		})
	}
}

func TestCoreMigrationsUnknownSide(t *testing.T) {
	if _, err := CoreMigrations("browser"); err == nil {
		t.Fatal("CoreMigrations accepted an unknown side")
	}
}

func TestOneLocalHost(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	set, _ := CoreMigrations(SideController)
	if _, err := Migrate(ctx, db, migrateOpts(t, "v1", time.Now()), set, nil); err != nil {
		t.Fatal(err)
	}
	insert := "INSERT INTO core_hosts (id, name, is_local, created_at) VALUES (?, ?, 1, '2026-10-17T00:00:00Z')"
	if _, err := db.Writer().ExecContext(ctx, insert, "01A", "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer().ExecContext(ctx, insert, "01B", "two"); err == nil {
		t.Fatal("a second local host was accepted")
	}
}

// coreSetUpTo returns side's core migrations numbered at most last, so a
// test can build a database as an older release left it.
func coreSetUpTo(t *testing.T, side string, last int) MigrationSet {
	t.Helper()
	set, err := CoreMigrations(side)
	if err != nil {
		t.Fatal(err)
	}
	files, err := fs.Glob(set.FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sub := fstest.MapFS{}
	for _, name := range files {
		m := migrationName.FindStringSubmatch(name)
		if n, _ := strconv.Atoi(m[1]); n > last {
			continue
		}
		data, err := fs.ReadFile(set.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		sub[name] = &fstest.MapFile{Data: data}
	}
	return MigrationSet{Module: set.Module, FS: sub}
}

// migratedController returns a controller database with every core
// migration applied.
func migratedController(t *testing.T) *DB {
	t.Helper()
	return migratedCore(t, SideController)
}

// migratedCore returns a database of side with every core migration applied.
func migratedCore(t *testing.T, side string) *DB {
	t.Helper()
	db := openTemp(t)
	set, err := CoreMigrations(side)
	if err != nil {
		t.Fatal(err)
	}
	opts := migrateOpts(t, "v1", time.Now())
	opts.Side = side
	if _, err := Migrate(context.Background(), db, opts, set, nil); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestFleetMigrationKeepsTheM1LocalHost(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	if _, err := Migrate(ctx, db, migrateOpts(t, "m1", time.Now()), coreSetUpTo(t, SideController, 4), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer().ExecContext(ctx,
		"INSERT INTO core_hosts (id, name, is_local, created_at) VALUES ('01L', 'local', 1, '2026-10-17T00:00:00.000000000Z')"); err != nil {
		t.Fatal(err)
	}
	set, err := CoreMigrations(SideController)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Migrate(ctx, db, migrateOpts(t, "m2", time.Now()), set, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) == 0 || res.Applied[0].Number != 5 || res.Applied[0].Name != "fleet" {
		t.Fatalf("applied %+v, want 0005_fleet first", res.Applied)
	}
	var state string
	var enrolled, seen, version, os, arch *string
	var protocol *int64
	if err := db.Read().QueryRowContext(ctx,
		`SELECT state, enrolled_at, last_seen_at, agent_version, protocol_version, os, arch FROM core_hosts WHERE id = '01L'`).
		Scan(&state, &enrolled, &seen, &version, &protocol, &os, &arch); err != nil {
		t.Fatal(err)
	}
	if state != "pending" || enrolled != nil || seen != nil || version != nil || protocol != nil || os != nil || arch != nil {
		t.Fatalf("migrated local host: state %q, enrolled %v, seen %v, version %v, protocol %v, os %v, arch %v",
			state, enrolled, seen, version, protocol, os, arch)
	}
}

func TestFleetMigrationConstraints(t *testing.T) {
	ctx := context.Background()
	db := migratedController(t)
	exec := func(query string, args ...any) error {
		_, err := db.Writer().ExecContext(ctx, query, args...)
		return err
	}
	const at = "2026-10-19T00:00:00.000000000Z"
	if err := exec("INSERT INTO core_hosts (id, name, created_at) VALUES ('01H', 'web', ?)", at); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := db.Read().QueryRowContext(ctx, "SELECT state FROM core_hosts WHERE id = '01H'").Scan(&state); err != nil || state != "pending" {
		t.Fatalf("default state = %q, %v; want pending", state, err)
	}
	for _, s := range []string{"enrolled", "revoked", "pending"} {
		if err := exec("UPDATE core_hosts SET state = ? WHERE id = '01H'", s); err != nil {
			t.Fatalf("state %s refused: %v", s, err)
		}
	}
	if err := exec(`INSERT INTO core_agent_certs (serial, host_id, not_before, not_after, created_at) VALUES ('a1', '01H', ?, ?, ?)`, at, at, at); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO core_enroll_tokens (host_id, hash, expires_at, created_at) VALUES ('01H', x'00', ?, ?)`, at, at); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO core_host_update (host_id, channel) VALUES ('01H', 'beta')`); err != nil {
		t.Fatal(err)
	}

	refused := []struct {
		name, query string
		args        []any
	}{
		{"unknown host state", "UPDATE core_hosts SET state = 'lost' WHERE id = '01H'", nil},
		{"null host state", "UPDATE core_hosts SET state = NULL WHERE id = '01H'", nil},
		{"certificate of an unknown host", `INSERT INTO core_agent_certs (serial, host_id, not_before, not_after, created_at) VALUES ('a2', 'nope', ?, ?, ?)`, []any{at, at, at}},
		{"duplicate serial", `INSERT INTO core_agent_certs (serial, host_id, not_before, not_after, created_at) VALUES ('a1', '01H', ?, ?, ?)`, []any{at, at, at}},
		{"second token of a host", `INSERT INTO core_enroll_tokens (host_id, hash, expires_at, created_at) VALUES ('01H', x'01', ?, ?)`, []any{at, at}},
		{"token without a hash", `INSERT INTO core_enroll_tokens (host_id, expires_at, created_at) VALUES ('01H', ?, ?)`, []any{at, at}},
		{"text hash", `UPDATE core_enroll_tokens SET hash = 'abc' WHERE host_id = '01H'`, nil},
		{"unknown channel", `UPDATE core_host_update SET channel = 'nightly' WHERE host_id = '01H'`, nil},
		{"update policy of an unknown host", `INSERT INTO core_host_update (host_id, channel) VALUES ('nope', 'stable')`, nil},
	}
	for _, tc := range refused {
		if err := exec(tc.query, tc.args...); err == nil {
			t.Errorf("%s: accepted", tc.name)
		}
	}

	if err := exec("DELETE FROM core_hosts WHERE id = '01H'"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"core_agent_certs", "core_enroll_tokens", "core_host_update"} {
		var n int
		if err := db.Read().QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s rows after the host was deleted = %d, %v; want 0", table, n, err)
		}
	}
}

func TestFleetTablesAreStrict(t *testing.T) {
	db := migratedController(t)
	for _, table := range []string{"core_agent_certs", "core_enroll_tokens", "core_host_update"} {
		var sql string
		if err := db.Read().QueryRow("SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&sql); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(strings.TrimSpace(sql), "STRICT") {
			t.Errorf("%s is not STRICT: %s", table, sql)
		}
	}
}

// lifecycleRows inserts one plan and one change, as every release since M1
// writes them.
func lifecycleRows(t *testing.T, db *DB) {
	t.Helper()
	const at = "2026-10-21T00:00:00.000000000Z"
	if _, err := db.Writer().ExecContext(context.Background(),
		`INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash, diff_json, guards_json,
		 confirmation, created_at, expires_at) VALUES ('P1', 'H1', 'fix.item.add', 1, 'user:alice', 'ih', 'sh', '[]', '[]', '', ?, ?)`,
		at, at); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer().ExecContext(context.Background(),
		`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state, input_json,
		 created_at, updated_at) VALUES ('C1', 'P1', 'H1', 'fix.item.add', 1, 'compensable', 'fix.item.remove', 'user:alice',
		 'completed', '{}', ?, ?)`, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleMigrationKeepsChangesAndPlans(t *testing.T) {
	tests := []struct {
		side     string
		previous int
		number   int
	}{
		{SideAgent, 5, 6},
		{SideController, 8, 9},
	}
	for _, tt := range tests {
		t.Run(tt.side, func(t *testing.T) {
			ctx := context.Background()
			db := openTemp(t)
			opts := migrateOpts(t, "w0", time.Now())
			opts.Side = tt.side
			if _, err := Migrate(ctx, db, opts, coreSetUpTo(t, tt.side, tt.previous), nil); err != nil {
				t.Fatal(err)
			}
			lifecycleRows(t, db)
			set, err := CoreMigrations(tt.side)
			if err != nil {
				t.Fatal(err)
			}
			opts = migrateOpts(t, "w0a", time.Now())
			opts.Side = tt.side
			res, err := Migrate(ctx, db, opts, set, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Applied) == 0 || res.Applied[0].Number != tt.number || res.Applied[0].Name != "lifecycle_core" {
				t.Fatalf("applied %+v, want %04d_lifecycle_core first", res.Applied, tt.number)
			}
			var state, owner string
			var retry int
			var parent, repairedBy, outcome, epochs, override, cause *string
			if err := db.Read().QueryRowContext(ctx,
				`SELECT state, recovery_owner, recovery_retry, parent_change_id, repaired_by, repair_outcome, lock_epochs,
				 override_json, error_cause FROM core_changes WHERE id = 'C1'`).
				Scan(&state, &owner, &retry, &parent, &repairedBy, &outcome, &epochs, &override, &cause); err != nil {
				t.Fatal(err)
			}
			if state != "completed" || owner != "" || retry != 0 || parent != nil || repairedBy != nil || outcome != nil ||
				epochs != nil || override != nil || cause != nil {
				t.Errorf("migrated change: state %q, owner %q, retry %d, parent %v, repaired by %v, outcome %v, epochs %v, override %v, cause %v",
					state, owner, retry, parent, repairedBy, outcome, epochs, override, cause)
			}
			var action string
			var repairs, call, retired *string
			if err := db.Read().QueryRowContext(ctx, `SELECT action, repairs, call, retired_at FROM core_plans WHERE id = 'P1'`).
				Scan(&action, &repairs, &call, &retired); err != nil {
				t.Fatal(err)
			}
			if action != "fix.item.add" || repairs != nil || call != nil || retired != nil {
				t.Errorf("migrated plan: action %q, repairs %v, call %v, retired %v", action, repairs, call, retired)
			}
		})
	}
}

func TestLifecycleMigrationConstraints(t *testing.T) {
	const at = "2026-10-21T00:00:00.000000000Z"
	effect := `INSERT INTO core_effects (change_id, seq, kind, resource, local, inverse_local, before_json, state, reason,
		resolution, created_at, updated_at) VALUES (?, ?, ?, 'fix.item:a', ?, 0, '{"state":null}', ?, ?, ?, ?, ?)`
	taskRun := `INSERT INTO core_task_runs (change_id, op, attempt, state, started_at) VALUES ('C1', ?, ?, ?, ?)`
	step := `INSERT INTO core_composite_steps (change_id, step_id, ordinal, host_id, action, input_template, state, updated_at)
		VALUES ('C1', ?, ?, 'H1', 'fix.item.add/v1', '{}', ?, ?)`
	composite := `INSERT INTO core_composites (change_id, direction, cursor, steps_deadline) VALUES ('C1', ?, 0, ?)`
	type stmt struct {
		name, query string
		args        []any
	}
	tests := []struct {
		side              string
		accepted, refused []stmt
	}{
		{SideAgent, []stmt{
			{"effect", effect, []any{"C1", 1, "create", 1, "unresolved", "uncertain", "", at, at}},
			{"task run", taskRun, []any{"apply", 1, "starting", at}},
		}, []stmt{
			{"task op", taskRun, []any{"restart", 1, "starting", at}},
			{"task state", taskRun, []any{"undo", 1, "done", at}},
			{"task attempt 0", taskRun, []any{"undo", 0, "running", at}},
		}},
		{SideController, []stmt{
			{"effect", effect, []any{"C1", 1, "delete", 0, "accepted", "", "accept", at, at}},
			{"composite", composite, []any{"forward", at}},
			{"step", step, []any{"join", 0, "pending", at}},
		}, []stmt{
			{"step state", step, []any{"route", 1, "done", at}},
			{"duplicate ordinal", step, []any{"route", 0, "pending", at}},
			{"composite direction", `UPDATE core_composites SET direction = 'backward' WHERE change_id = 'C1'`, nil},
		}},
	}
	shared := []stmt{
		{"effect state", effect, []any{"C1", 2, "modify", 1, "pending", "", "", at, at}},
		{"effect reason", effect, []any{"C1", 2, "modify", 1, "unresolved", "stale", "", at, at}},
		{"effect resolution", effect, []any{"C1", 2, "modify", 1, "made", "", "ignore", at, at}},
		{"effect kind", effect, []any{"C1", 2, "rename", 1, "made", "", "", at, at}},
		{"effect seq 0", effect, []any{"C1", 0, "modify", 1, "made", "", "", at, at}},
		{"effect locality", effect, []any{"C1", 2, "modify", 2, "made", "", "", at, at}},
		{"effect of an unknown change", effect, []any{"C9", 1, "modify", 1, "made", "", "", at, at}},
		{"recovery retry", `UPDATE core_changes SET recovery_retry = 2 WHERE id = 'C1'`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.side, func(t *testing.T) {
			db := migratedCore(t, tt.side)
			lifecycleRows(t, db)
			exec := func(s stmt) error {
				_, err := db.Writer().ExecContext(context.Background(), s.query, s.args...)
				return err
			}
			for _, s := range tt.accepted {
				if err := exec(s); err != nil {
					t.Fatalf("%s refused: %v", s.name, err)
				}
			}
			for _, s := range append(slices.Clone(shared), tt.refused...) {
				if err := exec(s); err == nil {
					t.Errorf("%s: accepted", s.name)
				}
			}
		})
	}
}

func TestLifecycleTablesAreStrict(t *testing.T) {
	tests := []struct {
		side   string
		tables []string
	}{
		{SideAgent, []string{"core_effects", "core_lock_epochs", "core_task_runs"}},
		{SideController, []string{"core_effects", "core_lock_epochs", "core_composites", "core_composite_steps"}},
	}
	for _, tt := range tests {
		db := migratedCore(t, tt.side)
		for _, table := range tt.tables {
			var sql string
			if err := db.Read().QueryRow("SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&sql); err != nil {
				t.Fatalf("%s %s: %v", tt.side, table, err)
			}
			if !strings.HasSuffix(strings.TrimSpace(sql), "STRICT") {
				t.Errorf("%s %s is not STRICT: %s", tt.side, table, sql)
			}
		}
	}
}

func TestAuditMigrationWidensPhasesAndKeepsRows(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	opts := migrateOpts(t, "w0", time.Now())
	opts.Side = SideController
	if _, err := Migrate(ctx, db, opts, coreSetUpTo(t, SideController, 9), nil); err != nil {
		t.Fatal(err)
	}
	const at = "2026-10-21T00:00:00.000000000Z"
	insert := `INSERT INTO core_audit (id, seq, epoch, at, actor, host_id, action, version, phase, change_id, input_json,
		result, error_code, source) VALUES (?, ?, ?, ?, ?, ?, 'fix.item.add', 1, ?, ?, ?, ?, ?, ?)`
	exec := func(args ...any) error {
		_, err := db.Writer().ExecContext(ctx, insert, args...)
		return err
	}
	// A controller-side row (no host, no sequence, the empty epoch) and an
	// ingested agent row, as releases before this migration wrote them.
	if err := exec("A1", nil, "", at, "user:alice", nil, "apply", "C1", `{"name":"a"}`, "ok", nil, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if err := exec("A2", 7, "E1", at, "system:recovery", "H1", "interrupt", "C2", nil, "error", "core.interrupted", nil); err != nil {
		t.Fatal(err)
	}
	snapshot := func() []string {
		t.Helper()
		rows, err := db.Read().QueryContext(ctx, `SELECT json_array(id, seq, epoch, at, actor, host_id, action, version, phase,
			change_id, input_json, result, error_code, source) FROM core_audit ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var row string
			if err := rows.Scan(&row); err != nil {
				t.Fatal(err)
			}
			out = append(out, row)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := snapshot()
	if len(before) != 2 {
		t.Fatalf("rows before the migration = %v, want 2", before)
	}

	set, err := CoreMigrations(SideController)
	if err != nil {
		t.Fatal(err)
	}
	opts = migrateOpts(t, "w0a", time.Now())
	opts.Side = SideController
	res, err := Migrate(ctx, db, opts, set, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) == 0 || res.Applied[0].Number != 10 || res.Applied[0].Name != "audit_phases" {
		t.Fatalf("applied %+v, want 0010_audit_phases first", res.Applied)
	}
	if after := snapshot(); !slices.Equal(after, before) {
		t.Errorf("rows after the migration = %v, want %v", after, before)
	}
	var tableSQL, indexSQL string
	if err := db.Read().QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'core_audit'").
		Scan(&tableSQL); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.TrimSpace(tableSQL), "STRICT") {
		t.Errorf("core_audit is not STRICT: %s", tableSQL)
	}
	if err := db.Read().QueryRowContext(ctx,
		"SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'core_audit_host_epoch_seq' AND tbl_name = 'core_audit'").
		Scan(&indexSQL); err != nil {
		t.Fatalf("index core_audit_host_epoch_seq: %v", err)
	}
	if !strings.Contains(indexSQL, "UNIQUE") {
		t.Errorf("core_audit_host_epoch_seq is not unique: %s", indexSQL)
	}

	accepted := []struct{ id, phase, result string }{
		{"N1", "undo", "ok"}, {"N2", "resolve", "ok"}, {"N3", "release", "ok"},
		{"N4", "revert-watchdog", "error"}, {"N5", "revert", "override"}, {"N6", "logout", "ok"},
	}
	for i, a := range accepted {
		if err := exec(a.id, i+1, "E2", at, "user:alice", "H1", a.phase, "C3", nil, a.result, nil, nil); err != nil {
			t.Errorf("phase %s with result %s refused: %v", a.phase, a.result, err)
		}
	}
	refused := []struct {
		name string
		args []any
	}{
		{"unknown phase", []any{"R1", nil, "", at, "user:alice", nil, "bogus", "C3", nil, "ok", nil, nil}},
		{"unknown result", []any{"R2", nil, "", at, "user:alice", nil, "undo", "C3", nil, "partial", nil, nil}},
		{"duplicate host, epoch and seq", []any{"R3", 7, "E1", at, "system:recovery", "H1", "undo", "C2", nil, "ok", nil, nil}},
	}
	for _, r := range refused {
		if err := exec(r.args...); err == nil {
			t.Errorf("%s: accepted", r.name)
		}
	}
}

// surfacesRows lists a table's rows, each as one JSON array of columns, in a
// stable order.
func surfacesRows(t *testing.T, db *DB, query string) []string {
	t.Helper()
	rows, err := db.Read().QueryContext(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var row string
		if err := rows.Scan(&row); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// surfacesMigrate migrates db, built to side's migration previous, to the
// newest, and checks that 00<number>_platform_surfaces ran first and that
// core_changes.system reads 0 for lifecycleRows' change C1, accepts 1 and
// refuses 2.
func surfacesMigrate(t *testing.T, db *DB, side string, number int) {
	t.Helper()
	ctx := context.Background()
	set, err := CoreMigrations(side)
	if err != nil {
		t.Fatal(err)
	}
	opts := migrateOpts(t, "w0b", time.Now())
	opts.Side = side
	res, err := Migrate(ctx, db, opts, set, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) == 0 || res.Applied[0].Number != number || res.Applied[0].Name != "platform_surfaces" {
		t.Fatalf("applied %+v, want %04d_platform_surfaces first", res.Applied, number)
	}
	var system int
	if err := db.Read().QueryRowContext(ctx, "SELECT system FROM core_changes WHERE id = 'C1'").Scan(&system); err != nil || system != 0 {
		t.Errorf("system of the old change = %d, %v; want 0", system, err)
	}
	if _, err := db.Writer().ExecContext(ctx, "UPDATE core_changes SET system = 1 WHERE id = 'C1'"); err != nil {
		t.Errorf("system = 1 refused: %v", err)
	}
	if _, err := db.Writer().ExecContext(ctx, "UPDATE core_changes SET system = 2 WHERE id = 'C1'"); err == nil {
		t.Error("system = 2 accepted")
	}
}

func TestAgentPlatformSurfacesMigration(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	opts := migrateOpts(t, "w0a", time.Now())
	opts.Side = SideAgent
	if _, err := Migrate(ctx, db, opts, coreSetUpTo(t, SideAgent, 6), nil); err != nil {
		t.Fatal(err)
	}
	lifecycleRows(t, db)
	const at = "2026-10-22T00:00:00.000000000Z"
	insert := "INSERT INTO core_owned_files (path, module, kind, block_id, created_at) VALUES (?, ?, ?, ?, ?)"
	exec := func(path, kind, blockID string) error {
		_, err := db.Writer().ExecContext(ctx, insert, path, "demo", kind, blockID, at)
		return err
	}
	for _, r := range []struct{ path, kind, blockID string }{
		{"/etc/demo.conf", "file", ""}, {"/etc/hosts", "block", "demo-hosts"}, {"/var/lib/demo", "dir", ""},
		{"darkpanel-demo", "user", ""}, {"/etc/systemd/system/darkpanel-demo.service", "unit", ""},
	} {
		if err := exec(r.path, r.kind, r.blockID); err != nil {
			t.Fatalf("owned %s row: %v", r.kind, err)
		}
	}
	const owned = `SELECT json_array(path, module, kind, block_id, created_at) FROM core_owned_files
		ORDER BY path, module, block_id`
	before := surfacesRows(t, db, owned)

	surfacesMigrate(t, db, SideAgent, 7)

	if after := surfacesRows(t, db, owned); !slices.Equal(after, before) {
		t.Errorf("owned rows after the migration = %v, want %v", after, before)
	}
	var tableSQL string
	if err := db.Read().QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'core_owned_files'").
		Scan(&tableSQL); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.TrimSpace(tableSQL), "STRICT") {
		t.Errorf("core_owned_files is not STRICT: %s", tableSQL)
	}
	if err := exec("/var/lib/darkpanel/agent/demo-debs", "managed-dir", ""); err != nil {
		t.Errorf("kind managed-dir refused: %v", err)
	}
	if err := exec("/var/lib/darkpanel-demo", "service-dir", ""); err != nil {
		t.Errorf("kind service-dir refused: %v", err)
	}
	refused := []struct{ name, path, kind, blockID string }{
		{"unknown kind", "/run/demo.sock", "socket", ""},
		{"block without a block id", "/etc/fstab", "block", ""},
		{"managed-dir with a block id", "/var/lib/demo-cache", "managed-dir", "x"},
		{"duplicate path, module and block id", "/etc/demo.conf", "file", ""},
	}
	for _, r := range refused {
		if err := exec(r.path, r.kind, r.blockID); err == nil {
			t.Errorf("%s: accepted", r.name)
		}
	}
}

func TestControllerPlatformSurfacesMigration(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	opts := migrateOpts(t, "w0a", time.Now())
	opts.Side = SideController
	if _, err := Migrate(ctx, db, opts, coreSetUpTo(t, SideController, 10), nil); err != nil {
		t.Fatal(err)
	}
	lifecycleRows(t, db)
	const at = "2026-10-22T00:00:00.000000000Z"
	insert := `INSERT INTO core_audit (id, seq, epoch, at, actor, host_id, action, version, phase, change_id, input_json,
		result, error_code, source) VALUES (?, ?, ?, ?, ?, ?, 'fix.item.add', 1, ?, ?, ?, ?, ?, ?)`
	exec := func(args ...any) error {
		_, err := db.Writer().ExecContext(ctx, insert, args...)
		return err
	}
	for _, args := range [][]any{
		{"A1", nil, "", at, "user:alice", nil, "apply", "C1", `{"name":"a"}`, "ok", nil, "127.0.0.1"},
		{"A2", 7, "E1", at, "system:recovery", "H1", "interrupt", "C2", nil, "error", "core.interrupted", nil},
		{"A3", 8, "E1", at, "user:alice", "H1", "undo", "C2", nil, "ok", nil, "192.0.2.7"},
		{"A4", 9, "E1", at, "user:alice", "H1", "revert", "C2", nil, "override", nil, nil},
	} {
		if err := exec(args...); err != nil {
			t.Fatalf("audit row %v: %v", args[0], err)
		}
	}
	const audit = `SELECT json_array(id, seq, epoch, at, actor, host_id, action, version, phase, change_id, input_json,
		result, error_code, source) FROM core_audit ORDER BY id`
	before := surfacesRows(t, db, audit)

	surfacesMigrate(t, db, SideController, 11)

	if after := surfacesRows(t, db, audit); !slices.Equal(after, before) {
		t.Errorf("audit rows after the migration = %v, want %v", after, before)
	}
	var tableSQL, indexSQL string
	if err := db.Read().QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'core_audit'").
		Scan(&tableSQL); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.TrimSpace(tableSQL), "STRICT") {
		t.Errorf("core_audit is not STRICT: %s", tableSQL)
	}
	if err := db.Read().QueryRowContext(ctx,
		"SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'core_audit_host_epoch_seq' AND tbl_name = 'core_audit'").
		Scan(&indexSQL); err != nil {
		t.Fatalf("index core_audit_host_epoch_seq: %v", err)
	}
	if !strings.Contains(indexSQL, "UNIQUE") {
		t.Errorf("core_audit_host_epoch_seq is not unique: %s", indexSQL)
	}
	// The change watcher's query reads core_changes through the new index.
	var node, parent, notUsed int
	var plan string
	if err := db.Read().QueryRowContext(ctx, "EXPLAIN QUERY PLAN SELECT id FROM core_changes WHERE updated_at > ?", at).
		Scan(&node, &parent, &notUsed, &plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "USING INDEX core_changes_updated") {
		t.Errorf("query plan %q, want it to use core_changes_updated", plan)
	}
	accepted := []struct{ id, phase, result string }{
		{"N1", "reveal", "ok"}, {"N2", "uninstall-abort", "ok"}, {"N3", "release", "ok"}, {"N4", "revert-watchdog", "error"},
	}
	for i, a := range accepted {
		if err := exec(a.id, i+1, "E2", at, "user:alice", "H1", a.phase, nil, nil, a.result, nil, nil); err != nil {
			t.Errorf("phase %s refused: %v", a.phase, err)
		}
	}
	refused := []struct {
		name string
		args []any
	}{
		{"unknown phase", []any{"R1", nil, "", at, "user:alice", nil, "bogus", nil, nil, "ok", nil, nil}},
		{"unknown result", []any{"R2", nil, "", at, "user:alice", nil, "reveal", nil, nil, "partial", nil, nil}},
		{"duplicate host, epoch and seq", []any{"R3", 7, "E1", at, "system:recovery", "H1", "reveal", nil, nil, "ok", nil, nil}},
	}
	for _, r := range refused {
		if err := exec(r.args...); err == nil {
			t.Errorf("%s: accepted", r.name)
		}
	}
}
