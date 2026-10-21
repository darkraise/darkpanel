package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
)

var t0 = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

// openControllerDB returns a migrated controller database in a temporary
// directory.
func openControllerDB(t *testing.T) *store.DB {
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
		BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: t0}
	if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
		t.Fatal(err)
	}
	return db
}

func newTestStore(t *testing.T) (*Store, *store.DB, *coretest.FakeClock) {
	t.Helper()
	db := openControllerDB(t)
	clock := coretest.NewFakeClock(t0)
	return New(db, clock), db, clock
}

func count(t *testing.T, db *store.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.Read().QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func list(t *testing.T, s *Store, q core.AuditQuery) ([]core.AuditEntry, string) {
	t.Helper()
	entries, next, err := s.List(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	return entries, next
}

func TestAuditTables(t *testing.T) {
	db := openControllerDB(t)
	ctx := context.Background()
	for _, table := range []string{"core_audit", "core_journal_cursor", "core_change_index", "core_plan_index"} {
		if n := count(t, db, "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table); n != 1 {
			t.Errorf("table %s missing", table)
		}
	}
	insert := `INSERT INTO core_audit (id, seq, at, actor, host_id, action, version, phase, result)
		VALUES (?, ?, '2026-10-17T12:00:00.000000000Z', 'user:u1', ?, 'a.b.c', 1, 'apply', 'ok')`
	for _, row := range [][]any{{"01A", nil, nil}, {"01B", nil, nil}, {"01C", 1, "h1"}, {"01D", 1, "h2"}} {
		if _, err := db.Writer().ExecContext(ctx, insert, row...); err != nil {
			t.Fatalf("insert %v: %v", row, err)
		}
	}
	if _, err := db.Writer().ExecContext(ctx, insert, "01E", 1, "h1"); err == nil {
		t.Error("a second row for host h1 and seq 1 was accepted")
	}
	if _, err := db.Writer().ExecContext(ctx,
		`INSERT INTO core_audit (id, at, actor, action, version, phase, result)
		 VALUES ('01F', '2026-10-17T12:00:00.000000000Z', 'user:u1', 'a.b.c', 1, 'delete', 'ok')`); err == nil {
		t.Error("an unknown phase was accepted")
	}
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO core_plan_index (plan_id, host_id, action, version, expires_at)
		VALUES ('01P', '', 'a.b.c', 1, '2026-10-17T12:05:00.000000000Z')`); err != nil {
		t.Fatalf("insert into core_plan_index: %v", err)
	}
	if n := count(t, db, "SELECT applied FROM core_plan_index WHERE plan_id = '01P'"); n != 0 {
		t.Errorf("applied of a new plan = %d, want 0", n)
	}
}

func TestRecordKeepsTheIDAndLoginActions(t *testing.T) {
	s, db, _ := newTestStore(t)
	ctx := context.Background()
	login := core.AuditEntry{
		ID: "01JAAAAAAAAAAAAAAAAAAAAAAA", Actor: "anonymous", Action: "core.auth.login", Phase: "login",
		Result: "error", ErrorCode: "auth.invalid_credentials", Source: "192.0.2.1", At: t0, Version: 1,
		Input: json.RawMessage(`{"username":"mallory"}`),
	}
	logout := core.AuditEntry{
		Actor: "user:u1", Action: "core.auth.logout", Phase: "logout", Result: "ok", Source: "192.0.2.1",
		At: t0.Add(time.Second), Version: 1, Input: json.RawMessage(`{}`),
	}
	for _, e := range []core.AuditEntry{login, logout} {
		if err := s.Record(ctx, e); err != nil {
			t.Fatalf("Record %s: %v", e.Action, err)
		}
	}
	got, next := list(t, s, core.AuditQuery{})
	if len(got) != 2 || next != "" {
		t.Fatalf("List = %d entries, next %q; want 2 and no next page", len(got), next)
	}
	if len(got[0].ID) != 26 || got[0].ID == login.ID {
		t.Errorf("logout id = %q, want a new ULID", got[0].ID)
	}
	logout.ID = got[0].ID
	for i, want := range []core.AuditEntry{logout, login} {
		if !reflect.DeepEqual(got[i], want) {
			t.Errorf("entry %d =\n%+v\nwant\n%+v", i, got[i], want)
		}
	}
	if n := count(t, db, "SELECT count(*) FROM core_audit WHERE change_id IS NULL AND host_id IS NULL AND seq IS NULL"); n != 2 {
		t.Errorf("rows with null change, host and seq = %d, want 2", n)
	}
	if n := count(t, db, "SELECT count(*) FROM core_audit WHERE error_code IS NULL"); n != 1 {
		t.Errorf("rows with a null error code = %d, want 1 (the logout)", n)
	}
}

func TestAppendWritesInTheChangeTransaction(t *testing.T) {
	s, db, _ := newTestStore(t)
	ctx := context.Background()
	e := core.JournalEntry{
		At: t0, Actor: "user:u1", Action: "core.token.create", Version: 1, Phase: "apply", ChangeID: "01CHANGE",
		Input: json.RawMessage(`{"name":"ci","secret":"[redacted]"}`), Result: "ok", Source: "192.0.2.1",
	}
	boom := errors.New("boom")
	err := db.Write(ctx, func(tx *sql.Tx) error {
		if err := s.Append(ctx, tx, e); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Write = %v, want boom", err)
	}
	if n := count(t, db, "SELECT count(*) FROM core_audit"); n != 0 {
		t.Fatalf("rows after the rollback = %d, want 0", n)
	}
	system := e
	system.Actor, system.Source, system.Phase, system.Result, system.ErrorCode = "system:recovery", "", "interrupt", "error", "internal"
	for _, entry := range []core.JournalEntry{e, system} {
		if err := db.Write(ctx, func(tx *sql.Tx) error { return s.Append(ctx, tx, entry) }); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := list(t, s, core.AuditQuery{})
	want := core.AuditEntry{
		ID: got[1].ID, Actor: "user:u1", Action: "core.token.create", Phase: "apply", ChangeID: "01CHANGE",
		Result: "ok", Source: "192.0.2.1", At: t0, Version: 1, Input: e.Input,
	}
	if !reflect.DeepEqual(got[1], want) {
		t.Errorf("entry =\n%+v\nwant\n%+v", got[1], want)
	}
	if got[0].Source != "" || got[0].ErrorCode != "internal" {
		t.Errorf("system entry = %+v, want no source and error internal", got[0])
	}
	if n := count(t, db, "SELECT count(*) FROM core_audit WHERE source IS NULL"); n != 1 {
		t.Errorf("rows with a null source = %d, want 1", n)
	}
}

func journalEntry(seq int64, at time.Time) core.JournalEntry {
	return core.JournalEntry{
		Seq: seq, At: at, Actor: "user:u1", Action: "kv.item.set", Version: 1, Phase: "apply",
		ChangeID: "01C", Input: json.RawMessage(`{"name":"a"}`), Result: "ok", Source: "192.0.2.1",
	}
}

func TestIngestDeduplicatesByHostAndSeq(t *testing.T) {
	s, db, _ := newTestStore(t)
	ctx := context.Background()
	steps := []struct {
		host string
		seqs []int64
	}{{"h1", []int64{1, 2}}, {"h1", []int64{2, 3}}, {"h2", []int64{1}}}
	for _, step := range steps {
		var entries []core.JournalEntry
		for _, seq := range step.seqs {
			entries = append(entries, journalEntry(seq, t0.Add(time.Duration(seq)*time.Second)))
		}
		if err := s.Ingest(ctx, step.host, entries); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, db, "SELECT count(*) FROM core_audit WHERE host_id = 'h1'"); n != 3 {
		t.Errorf("h1 rows = %d, want 3", n)
	}
	got, _ := list(t, s, core.AuditQuery{HostID: "h2"})
	want := core.AuditEntry{
		ID: got[0].ID, Actor: "user:u1", HostID: "h2", Action: "kv.item.set", Phase: "apply", ChangeID: "01C",
		Result: "ok", Source: "192.0.2.1", At: t0.Add(time.Second), Version: 1, Seq: 1, Input: json.RawMessage(`{"name":"a"}`),
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("h2 entries = %+v, want %+v", got, want)
	}
}

func TestIngestRefusesANonPositiveSequence(t *testing.T) {
	s, db, _ := newTestStore(t)
	ctx := context.Background()
	for _, seq := range []int64{0, -1} {
		err := s.Ingest(ctx, "h1", []core.JournalEntry{journalEntry(1, t0), journalEntry(seq, t0)})
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("sequence %d", seq)) {
			t.Errorf("Ingest of sequence %d = %v, want a refusal", seq, err)
		}
	}
	if n := count(t, db, "SELECT count(*) FROM core_audit"); n != 0 {
		t.Errorf("rows after the refusals = %d, want 0: a refused batch stores nothing", n)
	}
}

func TestListFiltersAndPages(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	add := func(i int, actor, host, action string) {
		e := core.AuditEntry{Actor: actor, HostID: host, Action: action, Phase: "apply", Result: "ok",
			At: t0.Add(time.Duration(i) * time.Minute), Version: 1, Seq: int64(i), Input: json.RawMessage(`{}`)}
		if err := s.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	add(1, "user:a", "h1", "kv.item.set")
	add(2, "user:b", "", "core.token.create")
	add(3, "user:a", "h2", "kv.item.set")
	add(4, "user:a", "", "core.token.revoke")
	add(5, "user:b", "h1", "kv.item.get")
	seqs := func(entries []core.AuditEntry) []int64 {
		out := []int64{}
		for _, e := range entries {
			out = append(out, e.Seq)
		}
		return out
	}
	cases := []struct {
		name string
		q    core.AuditQuery
		want []int64
	}{
		{"all, newest first", core.AuditQuery{}, []int64{5, 4, 3, 2, 1}},
		{"actor", core.AuditQuery{Actor: "user:a"}, []int64{4, 3, 1}},
		{"host", core.AuditQuery{HostID: "h1"}, []int64{5, 1}},
		{"action", core.AuditQuery{Action: "kv.item.set"}, []int64{3, 1}},
		{"combined", core.AuditQuery{Actor: "user:a", HostID: "h1"}, []int64{1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, next := list(t, s, c.q)
			if !reflect.DeepEqual(seqs(got), c.want) || next != "" {
				t.Errorf("List = %v next %q, want %v and no next page", seqs(got), next, c.want)
			}
		})
	}
	first, next := list(t, s, core.AuditQuery{Limit: 2})
	if !reflect.DeepEqual(seqs(first), []int64{5, 4}) || next != first[1].ID {
		t.Fatalf("page 1 = %v next %q, want [5 4] and the last id", seqs(first), next)
	}
	second, next := list(t, s, core.AuditQuery{Limit: 2, Before: next})
	if !reflect.DeepEqual(seqs(second), []int64{3, 2}) || next != second[1].ID {
		t.Fatalf("page 2 = %v next %q, want [3 2] and the last id", seqs(second), next)
	}
	third, next := list(t, s, core.AuditQuery{Limit: 2, Before: next})
	if !reflect.DeepEqual(seqs(third), []int64{1}) || next != "" {
		t.Errorf("page 3 = %v next %q, want [1] and no next page", seqs(third), next)
	}
	empty, next := list(t, s, core.AuditQuery{Actor: "user:nobody"})
	if empty == nil || len(empty) != 0 || next != "" {
		t.Errorf("no match = %#v next %q, want an empty non-nil slice", empty, next)
	}
}

func TestListLimits(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	var entries []core.JournalEntry
	for i := range MaxListLimit + 1 {
		entries = append(entries, journalEntry(int64(i+1), t0.Add(time.Duration(i)*time.Millisecond)))
	}
	if err := s.Ingest(ctx, "h1", entries); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ limit, want int }{{0, DefaultListLimit}, {-1, DefaultListLimit}, {7, 7}, {MaxListLimit + 50, MaxListLimit}} {
		got, next := list(t, s, core.AuditQuery{Limit: c.limit})
		if len(got) != c.want || next == "" {
			t.Errorf("Limit %d: %d entries next %q, want %d and a next page", c.limit, len(got), next, c.want)
		}
	}
}

func TestZeroTimeTakesTheClock(t *testing.T) {
	s, _, clock := newTestStore(t)
	clock.Advance(time.Hour)
	if err := s.Record(context.Background(), core.AuditEntry{Actor: "user:u1", Action: "core.auth.logout",
		Phase: "logout", Result: "ok", Version: 1}); err != nil {
		t.Fatal(err)
	}
	got, _ := list(t, s, core.AuditQuery{})
	if len(got) != 1 || !got[0].At.Equal(t0.Add(time.Hour)) || got[0].Input != nil {
		t.Errorf("entry = %+v, want the clock's time and no input", got)
	}
}
