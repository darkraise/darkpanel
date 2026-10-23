package quarantine

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// qtDB is a migrated controller database in a temporary directory.
func qtDB(t *testing.T) *store.DB {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	opts := store.MigrateOptions{Side: store.SideController, BinaryVersion: "test",
		BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)}
	if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
		t.Fatal(err)
	}
	return db
}

func qtSet(t *testing.T, db *store.DB, value string) {
	t.Helper()
	if _, err := db.Writer().Exec(`INSERT INTO core_meta (key, value) VALUES (?, ?)`, MetaKey, value); err != nil {
		t.Fatal(err)
	}
}

func TestUnsetFlag(t *testing.T) {
	db := qtDB(t)
	ctx := context.Background()
	if m, ok, err := Read(ctx, db); err != nil || ok || m != (Marker{}) {
		t.Errorf("Read = %+v, %v, %v; want unset", m, ok, err)
	}
	if ok, err := Active(ctx, db); err != nil || ok {
		t.Errorf("Active = %v, %v; want false", ok, err)
	}
	if err := Check(ctx, db); err != nil {
		t.Errorf("Check = %v, want nil", err)
	}
}

func TestSetFlag(t *testing.T) {
	db := qtDB(t)
	ctx := context.Background()
	qtSet(t, db, `{"tx":"01RESTORE","restoredAt":"2026-10-23T12:00:00Z","backupCreatedAt":"2026-10-22T08:30:00Z"}`)
	want := Marker{Tx: "01RESTORE", RestoredAt: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC),
		BackupCreatedAt: time.Date(2026, 10, 22, 8, 30, 0, 0, time.UTC)}
	m, ok, err := Read(ctx, db)
	if err != nil || !ok || !m.RestoredAt.Equal(want.RestoredAt) || !m.BackupCreatedAt.Equal(want.BackupCreatedAt) || m.Tx != want.Tx {
		t.Errorf("Read = %+v, %v, %v; want %+v", m, ok, err, want)
	}
	if ok, err := Active(ctx, db); err != nil || !ok {
		t.Errorf("Active = %v, %v; want true", ok, err)
	}
	var e *errcode.Error
	if err := Check(ctx, db); !errors.As(err, &e) || e.Code != errcode.Quarantined {
		t.Errorf("Check = %v, want %s", err, errcode.Quarantined)
	}
}

// TestMalformedFlagStaysSet: a value that does not decode still means the
// controller is quarantined.
func TestMalformedFlagStaysSet(t *testing.T) {
	db := qtDB(t)
	ctx := context.Background()
	qtSet(t, db, `not json`)
	if _, ok, err := Read(ctx, db); err == nil || !ok {
		t.Errorf("Read of a malformed marker = %v, %v; want set and an error", ok, err)
	}
	if ok, err := Active(ctx, db); err != nil || !ok {
		t.Errorf("Active = %v, %v; want true", ok, err)
	}
	if err := Check(ctx, db); errcode.From(err).Code != errcode.Quarantined {
		t.Errorf("Check = %v, want %s", err, errcode.Quarantined)
	}
}

// TestFailedReadFailsClosed: Check answers the read's error, never nil.
func TestFailedReadFailsClosed(t *testing.T) {
	db := qtDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Active(ctx, db); err == nil {
		t.Fatal("Active with a cancelled context succeeded")
	}
	if err := Check(ctx, db); err == nil {
		t.Error("Check with a failed read = nil, want the error")
	}
}
