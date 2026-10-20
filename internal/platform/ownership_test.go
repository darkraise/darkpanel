package platform

import (
	"context"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// openAgentDB returns a temporary agent database with the core migrations
// applied.
func openAgentDB(t *testing.T) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	opts := store.MigrateOptions{
		Side: store.SideAgent, BinaryVersion: "test", BackupRoot: filepath.Join(t.TempDir(), "backups"),
		Keep: 5, Now: time.Now(),
	}
	if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestOwnershipRecordListForget(t *testing.T) {
	o := NewOwnership(openAgentDB(t))
	ctx := context.Background()
	at := time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC)
	file := core.OwnedEntry{Path: "/etc/darkpanel/a.conf", Module: "demo", Kind: "file", CreatedAt: at}
	block := core.OwnedEntry{Path: "/etc/hosts", Module: "demo", Kind: "block", BlockID: "names", CreatedAt: at}
	// The markers name the module, so another module's "names" block in the
	// same file is a different block on disk and needs a row of its own.
	otherBlock := core.OwnedEntry{Path: "/etc/hosts", Module: "other", Kind: "block", BlockID: "names", CreatedAt: at}
	for _, e := range []core.OwnedEntry{block, otherBlock, file, file} {
		if err := o.Record(ctx, e); err != nil {
			t.Fatalf("Record(%+v): %v", e, err)
		}
	}
	later := file
	later.CreatedAt = at.Add(time.Hour)
	if err := o.Record(ctx, later); err != nil {
		t.Fatalf("Record again: %v", err)
	}
	got, err := o.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []core.OwnedEntry{file, block, otherBlock}; !reflect.DeepEqual(got, want) {
		t.Errorf("List = %+v, want %+v (sorted by path, block id and module, first record kept)", got, want)
	}
	if err := o.Forget(ctx, "demo", "/etc/hosts", "names"); err != nil {
		t.Fatal(err)
	}
	if err := o.Forget(ctx, "other", "/etc/darkpanel/a.conf", ""); err != nil {
		t.Fatal(err)
	}
	got, err = o.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []core.OwnedEntry{file, otherBlock}; !reflect.DeepEqual(got, want) {
		t.Errorf("after Forget: %+v, want %+v (a module forgets only its own entries)", got, want)
	}
}

func TestOwnershipRefusesInvalidEntries(t *testing.T) {
	o := NewOwnership(openAgentDB(t))
	ctx := context.Background()
	bad := []core.OwnedEntry{
		{Path: "/etc/a", Module: "demo", Kind: "socket"},
		{Path: "/etc/a", Module: "demo", Kind: "file", BlockID: "x"},
		{Path: "/etc/a", Module: "demo", Kind: "dir", BlockID: "x"},
		{Path: "demo", Module: "demo", Kind: "user", BlockID: "x"},
		{Path: "/etc/a.service", Module: "demo", Kind: "unit", BlockID: "x"},
		{Path: "/etc/a", Module: "demo", Kind: "block"},
	}
	for _, e := range bad {
		if err := o.Record(ctx, e); err == nil {
			t.Errorf("Record(%+v) succeeded, want an error", e)
		}
	}
}

func TestOwnershipRecordsEveryKind(t *testing.T) {
	o := NewOwnership(openAgentDB(t))
	ctx := context.Background()
	at := time.Date(2026, 10, 20, 1, 2, 3, 0, time.UTC)
	want := []core.OwnedEntry{
		{Path: "/etc/systemd/system/darkpanel-agent.service", Module: "core", Kind: core.OwnedUnit, CreatedAt: at},
		{Path: "/var/lib/darkpanel", Module: "core", Kind: core.OwnedDir, CreatedAt: at},
		{Path: "darkpanel", Module: "core", Kind: core.OwnedUser, CreatedAt: at},
	}
	for _, e := range want {
		if err := o.Record(ctx, e); err != nil {
			t.Fatalf("Record(%+v): %v", e, err)
		}
	}
	got, err := o.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("List = %+v, want %+v", got, want)
	}
}

// TestOwnershipKindsMigrationKeepsEntries applies the agent migrations before
// the ownership kinds, records entries, and applies the rest.
func TestOwnershipKindsMigrationKeepsEntries(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	full, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	early := fstest.MapFS{}
	err = fs.WalkDir(full.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p >= "0005" {
			return err
		}
		b, err := fs.ReadFile(full.FS, p)
		early[p] = &fstest.MapFile{Data: b}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	migrate := func(set store.MigrationSet) {
		t.Helper()
		opts := store.MigrateOptions{
			Side: store.SideAgent, BinaryVersion: "test", BackupRoot: filepath.Join(t.TempDir(), "backups"), Keep: 5, Now: time.Now(),
		}
		if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
			t.Fatal(err)
		}
	}
	migrate(store.MigrationSet{Module: full.Module, FS: early})
	o := NewOwnership(db)
	at := time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC)
	before := []core.OwnedEntry{
		{Path: "/etc/a.conf", Module: "demo", Kind: core.OwnedFile, CreatedAt: at},
		{Path: "/etc/hosts", Module: "demo", Kind: core.OwnedBlock, BlockID: "names", CreatedAt: at},
	}
	for _, e := range before {
		if err := o.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := o.Record(ctx, core.OwnedEntry{Path: "/var/lib/x", Module: "demo", Kind: core.OwnedDir, CreatedAt: at}); err == nil {
		t.Fatal("the old table accepted kind dir; the test is not running before the migration")
	}
	migrate(full)
	got, err := o.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, before) {
		t.Errorf("after the migration List = %+v, want %+v", got, before)
	}
	var sql string
	if err := db.Read().QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE name = 'core_owned_files'`).Scan(&sql); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, "STRICT") || strings.Contains(sql, "core_owned_files_new") {
		t.Errorf("the rebuilt table is %s", sql)
	}
	if err := o.Record(ctx, core.OwnedEntry{Path: "/var/lib/x", Module: "demo", Kind: core.OwnedDir, CreatedAt: at}); err != nil {
		t.Errorf("after the migration Record(dir): %v", err)
	}
}
