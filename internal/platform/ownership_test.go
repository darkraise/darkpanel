package platform

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
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
	for _, e := range []core.OwnedEntry{block, file, file} {
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
	if want := []core.OwnedEntry{file, block}; !reflect.DeepEqual(got, want) {
		t.Errorf("List = %+v, want %+v (sorted by path, first record kept)", got, want)
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
	if want := []core.OwnedEntry{file}; !reflect.DeepEqual(got, want) {
		t.Errorf("after Forget: %+v, want %+v (another module cannot forget demo's file)", got, want)
	}
}

func TestOwnershipRefusesInvalidEntries(t *testing.T) {
	o := NewOwnership(openAgentDB(t))
	ctx := context.Background()
	bad := []core.OwnedEntry{
		{Path: "/etc/a", Module: "demo", Kind: "dir"},
		{Path: "/etc/a", Module: "demo", Kind: "file", BlockID: "x"},
		{Path: "/etc/a", Module: "demo", Kind: "block"},
	}
	for _, e := range bad {
		if err := o.Record(ctx, e); err == nil {
			t.Errorf("Record(%+v) succeeded, want an error", e)
		}
	}
}
