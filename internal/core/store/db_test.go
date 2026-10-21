package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("CREATE TABLE t (x INTEGER)")
		return err
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	for _, path := range []string{db.Path(), db.Path() + "-wal", db.Path() + "-shm"} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("%s mode = %o, want 600", filepath.Base(path), mode)
		}
	}
}

func TestOpenTightensExistingModes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	files := []string{path, path + "-wal", path + "-shm"}
	for _, p := range files {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, 0o644); err != nil { //nolint:gosec // G302: the loose mode Open must tighten
			t.Fatal(err)
		}
	}
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, p := range files {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("%s mode = %o, want 600", filepath.Base(p), mode)
		}
	}
}

func TestReaderPoolSetsPragmasOnEveryConnection(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	// Holding four leases at once makes the pool open its every connection.
	conns := make([]*sql.Conn, 4)
	for i := range conns {
		c, err := db.Read().Conn(ctx)
		if err != nil {
			t.Fatalf("reader connection %d: %v", i, err)
		}
		t.Cleanup(func() { _ = c.Close() })
		conns[i] = c
	}
	pragmas := map[string]string{
		"journal_mode": "wal", "synchronous": "2", "foreign_keys": "1", "busy_timeout": "5000", "query_only": "1",
	}
	for i, c := range conns {
		for name, want := range pragmas {
			var got string
			if err := c.QueryRowContext(ctx, "PRAGMA "+name).Scan(&got); err != nil {
				t.Fatalf("reader %d PRAGMA %s: %v", i, name, err)
			}
			if got != want {
				t.Errorf("reader %d PRAGMA %s = %s, want %s", i, name, got, want)
			}
		}
	}
}

func TestReadDuringWriteSeesLastCommit(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("CREATE TABLE t (x INTEGER); INSERT INTO t VALUES (1)")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO t VALUES (2)"); err != nil {
			return err
		}
		var n int
		if err := db.Read().QueryRowContext(ctx, "SELECT count(*) FROM t").Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("rows a reader sees during the write = %d, want 1 (the last commit)", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Read().QueryRowContext(ctx, "SELECT count(*) FROM t").Scan(&n); err != nil || n != 2 {
		t.Errorf("rows after the commit = %d, %v; want 2", n, err)
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

func TestWriteRollsBackOnPanic(t *testing.T) {
	db := openTemp(t)
	var p any
	func() {
		defer func() { p = recover() }()
		_ = db.Write(context.Background(), func(*sql.Tx) error { panic("boom") })
	}()
	if p == nil {
		t.Fatal("Write swallowed the panic")
	}
	if n := db.Writer().Stats().InUse; n != 0 {
		t.Errorf("writer connections in use after a panic = %d, want 0", n)
	}
}

func TestWriteRollsBackOnGoexit(t *testing.T) {
	db := openTemp(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = db.Write(context.Background(), func(*sql.Tx) error {
			runtime.Goexit()
			return nil
		})
	}()
	<-done
	if n := db.Writer().Stats().InUse; n != 0 {
		t.Errorf("writer connections in use after runtime.Goexit = %d, want 0", n)
	}
}

func TestOpenRefusesURICharacters(t *testing.T) {
	for _, name := range []string{"a?b.db", "a#b.db", "a%41.db"} {
		if _, err := Open(context.Background(), filepath.Join(t.TempDir(), name)); err == nil {
			t.Errorf("Open accepted %q", name)
		}
	}
}
