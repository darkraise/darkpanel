package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

var metaSQL = "CREATE TABLE core_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);"

func sqlFS(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, body := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return fsys
}

func migrateOpts(t *testing.T, version string, now time.Time) MigrateOptions {
	return MigrateOptions{
		Side: "controller", BinaryVersion: version,
		BackupRoot: filepath.Join(t.TempDir(), "backups"), Keep: 5, Now: now,
	}
}

func tableExists(t *testing.T, db *DB, name string) bool {
	t.Helper()
	var n int
	if err := db.Read().QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestMigrateFreshDatabase(t *testing.T) {
	db := openTemp(t)
	core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
	mods := []MigrationSet{
		{"alpha", sqlFS(map[string]string{
			"0001_init.sql":  "CREATE TABLE alpha_items (id TEXT PRIMARY KEY);",
			"0002_names.sql": "ALTER TABLE alpha_items ADD COLUMN name TEXT;",
		})},
		{"beta", nil},
	}
	opts := migrateOpts(t, "v1", time.Date(2026, 10, 17, 1, 0, 0, 0, time.UTC))
	res, err := Migrate(context.Background(), db, opts, core, mods)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if want := filepath.Join(opts.BackupRoot, "none-v1-20261017T010000Z"); res.BackupDir != want {
		t.Errorf("backup dir = %q, want %q", res.BackupDir, want)
	}
	if len(res.Applied) != 3 || len(res.Failed) != 0 {
		t.Errorf("result = %+v, want 3 applied and no failures", res)
	}
	var v string
	if err := db.Read().QueryRow("SELECT value FROM core_meta WHERE key = 'binary_version'").Scan(&v); err != nil || v != "v1" {
		t.Errorf("binary_version = %q, %v; want v1", v, err)
	}
}

func TestMigrateRefusesClashingModuleNames(t *testing.T) {
	alpha := MigrationSet{"alpha", sqlFS(map[string]string{"0001_init.sql": "CREATE TABLE alpha_items (id TEXT PRIMARY KEY);"})}
	tests := []struct {
		name string
		mods []MigrationSet
	}{
		{"module named core", []MigrationSet{{"core", nil}}},
		{"module named twice", []MigrationSet{alpha, alpha}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openTemp(t)
			core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
			opts := migrateOpts(t, "v1", time.Date(2026, 10, 17, 1, 0, 0, 0, time.UTC))
			if _, err := Migrate(context.Background(), db, opts, core, tt.mods); err == nil || !strings.Contains(err.Error(), "module set") {
				t.Fatalf("Migrate error = %v, want one naming the module set", err)
			}
			if tableExists(t, db, "core_meta") {
				t.Error("core migrated despite the clashing module sets")
			}
		})
	}
}

func TestMigrateBacksUpOnlyWhenPending(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
	opts := migrateOpts(t, "v1", time.Date(2026, 10, 17, 1, 0, 0, 0, time.UTC))
	if _, err := Migrate(ctx, db, opts, core, nil); err != nil {
		t.Fatal(err)
	}

	res, err := Migrate(ctx, db, opts, core, nil)
	if err != nil || res.BackupDir != "" || len(res.Applied) != 0 {
		t.Fatalf("second run = %+v, %v; want nothing applied and no backup", res, err)
	}

	core.FS = sqlFS(map[string]string{
		"0001_core.sql":  metaSQL,
		"0002_hosts.sql": "CREATE TABLE core_hosts (id TEXT PRIMARY KEY);",
	})
	opts.BinaryVersion = "v2"
	res, err = Migrate(ctx, db, opts, core, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(opts.BackupRoot, "v1-v2-20261017T010000Z"); res.BackupDir != want {
		t.Errorf("backup dir = %q, want %q", res.BackupDir, want)
	}
	if _, err := os.Stat(filepath.Join(res.BackupDir, "test.db")); err != nil {
		t.Errorf("backup file: %v", err)
	}
}

func TestMigrateRecordsVersionWhenNothingPending(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
	opts := migrateOpts(t, "v1", time.Date(2026, 10, 17, 1, 0, 0, 0, time.UTC))
	if _, err := Migrate(ctx, db, opts, core, nil); err != nil {
		t.Fatal(err)
	}
	opts.BinaryVersion = "v2"
	if res, err := Migrate(ctx, db, opts, core, nil); err != nil || res.BackupDir != "" {
		t.Fatalf("v2 run = %+v, %v; want success and no backup", res, err)
	}
	core.FS = sqlFS(map[string]string{
		"0001_core.sql":  metaSQL,
		"0002_hosts.sql": "CREATE TABLE core_hosts (id TEXT PRIMARY KEY);",
	})
	opts.BinaryVersion = "v3"
	res, err := Migrate(ctx, db, opts, core, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(opts.BackupRoot, "v2-v3-20261017T010000Z"); res.BackupDir != want {
		t.Errorf("backup dir = %q, want %q", res.BackupDir, want)
	}
}

func TestMigrateSkipsBackupForSameVersion(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
	failing := []MigrationSet{{"broken", sqlFS(map[string]string{"0001_bad.sql": "NOT SQL;"})}}
	opts := migrateOpts(t, "v2", time.Date(2026, 10, 17, 1, 0, 0, 0, time.UTC))
	res, err := Migrate(ctx, db, opts, core, failing)
	if err != nil || res.BackupDir == "" || len(res.Failed) != 1 {
		t.Fatalf("first run = %+v, %v; want a backup and the module failure", res, err)
	}
	for range 6 {
		opts.Now = opts.Now.Add(time.Minute)
		res, err = Migrate(ctx, db, opts, core, failing)
		if err != nil || res.BackupDir != "" || len(res.Failed) != 1 {
			t.Fatalf("restart = %+v, %v; want the module failure again and no new backup", res, err)
		}
	}
	entries, err := os.ReadDir(opts.BackupRoot)
	if err != nil || len(entries) != 1 || entries[0].Name() != "none-v2-20261017T010000Z" {
		t.Errorf("backups = %v, %v; want only the first run's backup", entries, err)
	}
}

func TestMigrateCoreChecksumChangeStops(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	opts := migrateOpts(t, "v1", time.Now())
	core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
	if _, err := Migrate(ctx, db, opts, core, nil); err != nil {
		t.Fatal(err)
	}
	core.FS = sqlFS(map[string]string{"0001_core.sql": metaSQL + "\n-- edited"})
	_, err := Migrate(ctx, db, opts, core, nil)
	if err == nil || !strings.Contains(err.Error(), "changed after it was applied") {
		t.Fatalf("err = %v, want a checksum error", err)
	}
}

func TestMigrateModuleFailureIsIsolated(t *testing.T) {
	db := openTemp(t)
	core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
	mods := []MigrationSet{
		{"broken", sqlFS(map[string]string{
			"0001_init.sql": "CREATE TABLE broken_a (id TEXT);",
			"0002_bad.sql":  "THIS IS NOT SQL;",
			"0003_more.sql": "CREATE TABLE broken_c (id TEXT);",
		})},
		{"misnamed", sqlFS(map[string]string{"init.sql": "CREATE TABLE misnamed_a (id TEXT);"})},
		{"twice", sqlFS(map[string]string{
			"0001_a.sql": "CREATE TABLE twice_a (id TEXT);",
			"0001_b.sql": "CREATE TABLE twice_b (id TEXT);",
		})},
		{"zero", sqlFS(map[string]string{"0000_init.sql": "CREATE TABLE zero_a (id TEXT);"})},
		{"good", sqlFS(map[string]string{"0001_init.sql": "CREATE TABLE good_a (id TEXT);"})},
	}
	res, err := Migrate(context.Background(), db, migrateOpts(t, "v1", time.Now()), core, mods)
	if err != nil {
		t.Fatalf("Migrate: %v (a module failure must not fail the run)", err)
	}
	failed := map[string]bool{}
	for _, f := range res.Failed {
		failed[f.Module] = true
	}
	for _, m := range []string{"broken", "misnamed", "twice", "zero"} {
		if !failed[m] {
			t.Errorf("module %s not reported failed; failures = %+v", m, res.Failed)
		}
	}
	if failed["good"] || !tableExists(t, db, "good_a") {
		t.Error("module good did not migrate")
	}
	if !tableExists(t, db, "broken_a") || tableExists(t, db, "broken_c") {
		t.Error("broken: want 0001 applied and 0003 skipped after 0002 failed")
	}
}

func TestMigrateModuleChecksumChangeIsIsolated(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	opts := migrateOpts(t, "v1", time.Now())
	core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
	alpha := MigrationSet{"alpha", sqlFS(map[string]string{"0001_init.sql": "CREATE TABLE alpha_a (id TEXT);"})}
	if _, err := Migrate(ctx, db, opts, core, []MigrationSet{alpha}); err != nil {
		t.Fatal(err)
	}
	alpha.FS = sqlFS(map[string]string{"0001_init.sql": "CREATE TABLE alpha_a (id TEXT); -- edited"})
	beta := MigrationSet{"beta", sqlFS(map[string]string{"0001_init.sql": "CREATE TABLE beta_a (id TEXT);"})}
	res, err := Migrate(ctx, db, opts, core, []MigrationSet{alpha, beta})
	if err != nil {
		t.Fatalf("Migrate: %v (a module checksum change must not fail the run)", err)
	}
	if len(res.Failed) != 1 || res.Failed[0].Module != "alpha" || !strings.Contains(res.Failed[0].Err.Error(), "changed after it was applied") {
		t.Errorf("failures = %+v, want alpha with a checksum error", res.Failed)
	}
	if !tableExists(t, db, "beta_a") {
		t.Error("module beta did not migrate")
	}
}

func TestMigrateCoreFailureStops(t *testing.T) {
	db := openTemp(t)
	core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": "NOT SQL;"})}
	mods := []MigrationSet{{"good", sqlFS(map[string]string{"0001_init.sql": "CREATE TABLE good_a (id TEXT);"})}}
	if _, err := Migrate(context.Background(), db, migrateOpts(t, "v1", time.Now()), core, mods); err == nil {
		t.Fatal("Migrate succeeded with a broken core migration")
	}
	if tableExists(t, db, "good_a") {
		t.Error("module migrated after core failed")
	}
}

func TestMigrateRunsWholeFileAtomically(t *testing.T) {
	db := openTemp(t)
	core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
	mods := []MigrationSet{
		{"multi", sqlFS(map[string]string{
			"0001_init.sql": "CREATE TABLE multi_a (id TEXT);\nCREATE TABLE multi_b (id TEXT);",
		})},
		{"partial", sqlFS(map[string]string{
			"0001_init.sql": "CREATE TABLE partial_a (id TEXT);\nNOT SQL;",
		})},
	}
	res, err := Migrate(context.Background(), db, migrateOpts(t, "v1", time.Now()), core, mods)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !tableExists(t, db, "multi_a") || !tableExists(t, db, "multi_b") {
		t.Error("multi: want every statement of 0001 applied")
	}
	if len(res.Failed) != 1 || res.Failed[0].Module != "partial" {
		t.Errorf("failures = %+v, want partial", res.Failed)
	}
	if tableExists(t, db, "partial_a") {
		t.Error("partial: the failed migration's first statement was kept")
	}
	var n int
	if err := db.Read().QueryRow("SELECT count(*) FROM core_migrations WHERE module = 'partial'").Scan(&n); err != nil || n != 0 {
		t.Errorf("partial rows = %d, %v; want 0", n, err)
	}
}

func TestMigrateCoreFailureBacksUpOnce(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	opts := migrateOpts(t, "v1", time.Date(2026, 10, 17, 1, 0, 0, 0, time.UTC))
	core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
	if _, err := Migrate(ctx, db, opts, core, nil); err != nil {
		t.Fatal(err)
	}
	core.FS = sqlFS(map[string]string{"0001_core.sql": metaSQL, "0002_bad.sql": "NOT SQL;"})
	opts.BinaryVersion = "v2"
	for range 6 {
		opts.Now = opts.Now.Add(time.Minute)
		if _, err := Migrate(ctx, db, opts, core, nil); err == nil {
			t.Fatal("Migrate succeeded with a broken core migration")
		}
	}
	entries, err := os.ReadDir(opts.BackupRoot)
	if err != nil || len(entries) != 2 || entries[0].Name() != "none-v1-20261017T010000Z" || entries[1].Name() != "v1-v2-20261017T010100Z" {
		t.Errorf("backups = %v, %v; want the first run's backup and one pre-upgrade backup", entries, err)
	}
}

func TestMigrateIgnoresNewerRecordedMigrations(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	opts := migrateOpts(t, "v2", time.Now())
	newer := MigrationSet{"core", sqlFS(map[string]string{
		"0001_core.sql":  metaSQL,
		"0002_hosts.sql": "CREATE TABLE core_hosts (id TEXT PRIMARY KEY);",
	})}
	if _, err := Migrate(ctx, db, opts, newer, nil); err != nil {
		t.Fatal(err)
	}
	older := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
	opts.BinaryVersion = "v1"
	res, err := Migrate(ctx, db, opts, older, nil)
	if err != nil || len(res.Applied) != 0 {
		t.Fatalf("older binary: %+v, %v; want success with nothing applied", res, err)
	}
}

func TestMigrateAllowsGapsButNotOutOfOrder(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	opts := migrateOpts(t, "v1", time.Now())
	core := MigrationSet{"core", sqlFS(map[string]string{
		"0001_core.sql":  metaSQL,
		"0003_later.sql": "CREATE TABLE core_later (id TEXT PRIMARY KEY);",
	})}
	res, err := Migrate(ctx, db, opts, core, nil)
	if err != nil || len(res.Applied) != 2 {
		t.Fatalf("gap: %+v, %v; want 0001 and 0003 applied", res, err)
	}
	core.FS = sqlFS(map[string]string{
		"0001_core.sql":  metaSQL,
		"0002_early.sql": "CREATE TABLE core_early (id TEXT PRIMARY KEY);",
		"0003_later.sql": "CREATE TABLE core_later (id TEXT PRIMARY KEY);",
	})
	_, err = Migrate(ctx, db, opts, core, nil)
	if err == nil || !strings.Contains(err.Error(), "must run in order") {
		t.Fatalf("err = %v, want an ordering error", err)
	}
	if tableExists(t, db, "core_early") {
		t.Error("0002 ran after 0003")
	}
}

func TestSchemaVersion(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	if v, err := SchemaVersion(ctx, db, SideController); err != nil || v != 0 {
		t.Fatalf("before migrating: %d, %v; want 0", v, err)
	}
	core := MigrationSet{"core", sqlFS(map[string]string{
		"0001_core.sql":  metaSQL,
		"0002_hosts.sql": "CREATE TABLE core_hosts (id TEXT PRIMARY KEY);",
	})}
	mods := []MigrationSet{{"alpha", sqlFS(map[string]string{
		"0001_a.sql": "CREATE TABLE alpha_a (id TEXT);",
		"0002_b.sql": "CREATE TABLE alpha_b (id TEXT);",
		"0003_c.sql": "CREATE TABLE alpha_c (id TEXT);",
	})}}
	if _, err := Migrate(ctx, db, migrateOpts(t, "v1", time.Now()), core, mods); err != nil {
		t.Fatal(err)
	}
	if v, err := SchemaVersion(ctx, db, SideController); err != nil || v != 2 {
		t.Errorf("SchemaVersion = %d, %v; want 2 (module migrations do not count)", v, err)
	}
	if v, err := SchemaVersion(ctx, db, SideAgent); err != nil || v != 0 {
		t.Errorf("agent SchemaVersion = %d, %v; want 0", v, err)
	}
}
