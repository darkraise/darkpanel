package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// openDBAt opens the database at path and applies side's core migrations.
func openDBAt(t *testing.T, path, side string) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(side)
	if err != nil {
		t.Fatal(err)
	}
	opts := store.MigrateOptions{
		Side: side, BinaryVersion: "test", BackupRoot: filepath.Join(filepath.Dir(path), "backups"),
		Keep: 5, Now: time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC),
	}
	if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

// openDB opens a new migrated database of side in a temporary directory.
func openDB(t *testing.T, side string) *store.DB {
	t.Helper()
	return openDBAt(t, filepath.Join(t.TempDir(), "test.db"), side)
}

func tableNames(t *testing.T, db *store.DB) map[string]bool {
	t.Helper()
	rows, err := db.Read().Query("SELECT name FROM sqlite_master WHERE type = 'table'")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	names := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names[n] = true
	}
	return names
}

func TestLifecycleTables(t *testing.T) {
	for _, side := range []string{store.SideController, store.SideAgent} {
		names := tableNames(t, openDB(t, side))
		for _, table := range []string{"core_plans", "core_changes", "core_locks"} {
			if !names[table] {
				t.Errorf("%s: table %s missing", side, table)
			}
		}
		if names["core_journal"] != (side == store.SideAgent) {
			t.Errorf("%s: core_journal present = %v, want %v", side, names["core_journal"], side == store.SideAgent)
		}
	}
}

func appendEntries(t *testing.T, db *store.DB, j *AgentJournal, entries ...core.JournalEntry) {
	t.Helper()
	for _, e := range entries {
		if err := db.Write(context.Background(), func(tx *sql.Tx) error { return j.Append(context.Background(), tx, e) }); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
}

func entry(change string) core.JournalEntry {
	return core.JournalEntry{
		At: time.Date(2026, 10, 17, 1, 2, 3, 4, time.UTC), Actor: "user:alice", Action: "fix.item.set",
		Version: 1, Phase: "apply", ChangeID: change, Input: json.RawMessage(`{"name":"a"}`), Result: "ok",
	}
}

func TestJournalEntriesAndAck(t *testing.T) {
	ctx := context.Background()
	db := openDB(t, store.SideAgent)
	j := NewAgentJournal(db)
	noInput := entry("c3")
	noInput.Input = nil
	noInput.Result, noInput.ErrorCode = "error", "internal"
	appendEntries(t, db, j, entry("c1"), entry("c2"), noInput)

	got, err := j.Entries(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("entries = %d, want 3", len(got))
	}
	want := entry("c1")
	want.Seq = 1
	if g := got[0]; g.Seq != want.Seq || !g.At.Equal(want.At) || g.Actor != want.Actor || g.Action != want.Action ||
		g.Version != want.Version || g.Phase != want.Phase || g.ChangeID != want.ChangeID ||
		string(g.Input) != string(want.Input) || g.Result != want.Result || g.ErrorCode != "" {
		t.Errorf("entry 1 = %+v, want %+v", g, want)
	}
	if g := got[2]; g.Seq != 3 || g.Input != nil || g.Result != "error" || g.ErrorCode != "internal" {
		t.Errorf("entry 3 = %+v, want seq 3, no input, error internal", g)
	}
	after, err := j.Entries(ctx, 1, 1)
	if err != nil {
		t.Fatalf("Entries(1, 1): %v", err)
	}
	if len(after) != 1 || after[0].Seq != 2 {
		t.Errorf("Entries(1, 1) = %+v, want only seq 2", after)
	}
	if err := j.Ack(ctx, 2); err != nil {
		t.Fatal(err)
	}
	left, err := j.Entries(ctx, 0, 10)
	if err != nil {
		t.Fatalf("Entries after Ack: %v", err)
	}
	if len(left) != 1 || left[0].Seq != 3 {
		t.Errorf("after Ack(2) = %+v, want only seq 3", left)
	}
}

func TestJournalAppendRollsBackWithTransaction(t *testing.T) {
	ctx := context.Background()
	db := openDB(t, store.SideAgent)
	j := NewAgentJournal(db)
	boom := errors.New("boom")
	err := db.Write(ctx, func(tx *sql.Tx) error {
		if err := j.Append(ctx, tx, entry("c1")); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Write = %v, want boom", err)
	}
	got, err := j.Entries(ctx, 0, 10)
	if err != nil {
		t.Fatalf("Entries after rollback: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("entries after rollback = %+v, want none", got)
	}
}

func TestJournalOverflowKeepsOneCountingEntry(t *testing.T) {
	ctx := context.Background()
	db := openDB(t, store.SideAgent)
	j := NewAgentJournal(db)
	if j.cap != JournalCap {
		t.Fatalf("cap = %d, want JournalCap", j.cap)
	}
	j.cap = 3
	for _, c := range []string{"c1", "c2", "c3", "c4", "c5"} {
		appendEntries(t, db, j, entry(c))
	}
	got, err := j.Entries(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("entries = %+v, want 3", got)
	}
	if got[0].ChangeID != "c4" || got[2].ChangeID != "c5" {
		t.Errorf("kept %s and %s, want c4 and c5", got[0].ChangeID, got[2].ChangeID)
	}
	m := got[1]
	if m.Phase != "interrupt" || m.Result != "error" || m.ErrorCode != "journal.overflow" ||
		m.Actor != "system:journal" || string(m.Input) != `{"dropped":3}` {
		t.Errorf("overflow entry = %+v, want interrupt/error/journal.overflow counting 3 dropped", m)
	}
}
