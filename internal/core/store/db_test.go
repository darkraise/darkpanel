package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func openTemp(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestOpenSetsPragmasAndMode(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	pragmas := map[string]string{"journal_mode": "wal", "synchronous": "2", "foreign_keys": "1", "busy_timeout": "5000"}
	for name, want := range pragmas {
		var got string
		if err := db.Writer().QueryRowContext(ctx, "PRAGMA "+name).Scan(&got); err != nil {
			t.Fatalf("PRAGMA %s: %v", name, err)
		}
		if got != want {
			t.Errorf("writer PRAGMA %s = %s, want %s", name, got, want)
		}
	}
	info, err := os.Stat(db.Path())
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("database mode = %o, want 600", mode)
	}
}

func TestReaderCannotWrite(t *testing.T) {
	db := openTemp(t)
	if _, err := db.Read().Exec("CREATE TABLE t (x INTEGER)"); err == nil {
		t.Fatal("reader created a table, want an error")
	}
}

func TestWriteCommitsAndRollsBack(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("CREATE TABLE t (x INTEGER); INSERT INTO t VALUES (1)")
		return err
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	boom := errors.New("boom")
	err := db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO t VALUES (2)"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Write error = %v, want boom", err)
	}
	var n int
	if err := db.Read().QueryRow("SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("rows = %d, want 1 (the second insert must roll back)", n)
	}
}

func TestOpenRefusesURICharacters(t *testing.T) {
	if _, err := Open(context.Background(), filepath.Join(t.TempDir(), "a?b.db")); err == nil {
		t.Fatal("Open accepted a path containing '?'")
	}
}
