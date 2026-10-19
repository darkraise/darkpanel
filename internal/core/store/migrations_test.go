package store

import (
	"context"
	"io/fs"
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
			"core_agent_certs", "core_enroll_tokens", "core_host_update"}},
		{SideAgent, []string{"core_meta"}},
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

// controllerSetUpTo returns the controller's core migrations numbered at most
// last, so a test can build a database as an older release left it.
func controllerSetUpTo(t *testing.T, last int) MigrationSet {
	t.Helper()
	set, err := CoreMigrations(SideController)
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
	db := openTemp(t)
	set, err := CoreMigrations(SideController)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(context.Background(), db, migrateOpts(t, "v1", time.Now()), set, nil); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestFleetMigrationKeepsTheM1LocalHost(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	if _, err := Migrate(ctx, db, migrateOpts(t, "m1", time.Now()), controllerSetUpTo(t, 4), nil); err != nil {
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
