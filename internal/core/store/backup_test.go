package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestBackupCopiesDatabase(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("CREATE TABLE t (x INTEGER); INSERT INTO t VALUES (42)")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "backups")
	now := time.Date(2026, 10, 17, 9, 30, 0, 0, time.UTC)

	dir, err := Backup(ctx, db, root, "v1.0.0+dirty", "v1.1.0", now, 5)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if want := filepath.Join(root, "v1.0.0_dirty-v1.1.0-20261017T093000Z"); dir != want {
		t.Errorf("backup dir = %s, want %s", dir, want)
	}
	copyPath := filepath.Join(dir, "test.db")
	info, err := os.Stat(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("backup mode = %o, want 600", info.Mode().Perm())
	}
	copyDB, err := Open(ctx, copyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = copyDB.Close() }()
	var x int
	if err := copyDB.Read().QueryRow("SELECT x FROM t").Scan(&x); err != nil || x != 42 {
		t.Errorf("backup row = %d, %v; want 42", x, err)
	}
}

func TestBackupKeepsNewest(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "unrelated"), 0o700); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	for i := range 7 {
		if _, err := Backup(ctx, db, root, "a", "b", start.Add(time.Duration(i)*time.Minute), 5); err != nil {
			t.Fatalf("Backup %d: %v", i, err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	want := []string{
		"a-b-20261017T000200Z", "a-b-20261017T000300Z", "a-b-20261017T000400Z",
		"a-b-20261017T000500Z", "a-b-20261017T000600Z", "unrelated",
	}
	if len(names) != len(want) {
		t.Fatalf("entries = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("entries = %v, want %v", names, want)
		}
	}
}

func TestBackupRefusesExistingDirectory(t *testing.T) {
	db := openTemp(t)
	root := t.TempDir()
	now := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	if _, err := Backup(context.Background(), db, root, "a", "b", now, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := Backup(context.Background(), db, root, "a", "b", now, 5); err == nil {
		t.Fatal("second backup with the same name succeeded, want an error")
	}
}
