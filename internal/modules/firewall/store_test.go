package firewall

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

// fwTestDB opens a temporary agent database with the core's and the
// firewall's migrations applied.
func fwTestDB(t *testing.T) *store.DB {
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
	res, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideAgent, BinaryVersion: "test", BackupRoot: filepath.Join(t.TempDir(), "backups"),
		Keep: 5, Now: time.Now(),
	}, set, []store.MigrationSet{{Module: ID, FS: agentMigrations()}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) > 0 {
		t.Fatalf("firewall migrations failed: %v", res.Failed[0].Err)
	}
	return db
}

var stTestT0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

// stTestSnap is a snapshot created at stTestT0 plus n seconds.
func stTestSnap(n int, system bool) snapshot {
	return snapshot{
		ID:        fmt.Sprintf("01J0000000000000000000%04d", n),
		CreatedAt: stTestT0.Add(time.Duration(n) * time.Second),
		Actor:     "user:u1",
		System:    system,
		Entries: []core.RestoreEntry{
			{Path: "/etc/ufw/user.rules", Present: true, Mode: 0o640, Data: []byte("*filter\n\x00binary\n")},
			{Path: "/etc/ufw/before.init"},
		},
		State:  snapshotState{Enabled: true, Sysctl: map[string]string{"net/ipv4/ip_forward": "0"}},
		Hashes: map[string]string{"/etc/ufw/user.rules": "sha256:aa", "/etc/ufw/before.init": "absent"},
	}
}

// stTestChange inserts a change row (and its plan) in state.
func stTestChange(t *testing.T, db *store.DB, id, state string, system bool) {
	t.Helper()
	at := formatSnapshotTime(stTestT0)
	err := db.Write(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash,
			diff_json, guards_json, confirmation, created_at, expires_at)
			VALUES (?, 'h1', 'firewall.rule.add', 1, 'user:u1', '', '', '[]', '[]', '', ?, ?)`, "p-"+id, at, at); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor,
			state, input_json, system, created_at, updated_at)
			VALUES (?, ?, 'h1', 'firewall.rule.add', 1, 'revertible', '', 'user:u1', ?, '{}', ?, ?, ?)`,
			id, "p-"+id, state, system, at, at)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func stTestIDs(t *testing.T, db *store.DB) map[string]bool {
	t.Helper()
	all, err := listSnapshots(context.Background(), db, snapshotFilter{})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, s := range all {
		out[s.ID] = true
	}
	return out
}

func TestFileHash(t *testing.T) {
	if got := fileHash(core.RestoreEntry{Path: "/x"}); got != "absent" {
		t.Errorf("absent file hashes to %q", got)
	}
	got := fileHash(core.RestoreEntry{Path: "/x", Present: true, Data: []byte("abc")})
	if want := "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"; got != want {
		t.Errorf("fileHash = %q, want %q", got, want)
	}
	if got := fileHash(core.RestoreEntry{Path: "/x", Present: true}); got == "absent" {
		t.Error("an empty present file hashes like an absent one")
	}
}

func TestSnapshotSaveGetRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := fwTestDB(t)
	in := stTestSnap(1, false)
	in.State.Guard = &core.GuardContext{Complete: true}
	in.State.Image = &beforeImage{V4: []imageTable{{Name: "filter", Chains: []imageChain{{Name: "INPUT", Builtin: true, Policy: "DROP"}}}}}
	saved, err := saveSnapshot(ctx, db, in)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Baseline {
		t.Error("the host's first snapshot is not the baseline")
	}
	got, err := getSnapshot(ctx, db, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, saved) {
		t.Errorf("getSnapshot =\n%+v\nwant\n%+v", got, saved)
	}
	second, err := saveSnapshot(ctx, db, stTestSnap(2, false))
	if err != nil {
		t.Fatal(err)
	}
	if second.Baseline {
		t.Error("a second snapshot became a baseline")
	}
	if _, err := getSnapshot(ctx, db, "missing"); !errors.Is(err, errSnapshotNotFound) {
		t.Errorf("getSnapshot(missing) = %v, want errSnapshotNotFound", err)
	}
}

func TestSnapshotListNewestFirst(t *testing.T) {
	ctx := context.Background()
	db := fwTestDB(t)
	for i, system := range []bool{false, true, false, true} {
		if _, err := saveSnapshot(ctx, db, stTestSnap(i+1, system)); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(f snapshotFilter) []string {
		list, err := listSnapshots(ctx, db, f)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, s := range list {
			if s.Entries != nil {
				t.Errorf("list returned the entries of %s", s.ID)
			}
			out = append(out, s.ID[len(s.ID)-1:])
		}
		return out
	}
	yes, no := true, false
	for _, c := range []struct {
		f    snapshotFilter
		want []string
	}{
		{snapshotFilter{}, []string{"4", "3", "2", "1"}},
		{snapshotFilter{System: &yes}, []string{"4", "2"}},
		{snapshotFilter{System: &no}, []string{"3", "1"}},
		{snapshotFilter{Limit: 2}, []string{"4", "3"}},
	} {
		if got := ids(c.f); !reflect.DeepEqual(got, c.want) {
			t.Errorf("listSnapshots(%+v) = %v, want %v", c.f, got, c.want)
		}
	}
}

func TestSnapshotAttach(t *testing.T) {
	ctx := context.Background()
	db := fwTestDB(t)
	for i := 1; i <= 3; i++ {
		if _, err := saveSnapshot(ctx, db, stTestSnap(i, false)); err != nil {
			t.Fatal(err)
		}
	}
	stTestChange(t, db, "c-sys", "applying", true)
	id, err := attachNewest(ctx, db, "c-sys")
	if err != nil {
		t.Fatal(err)
	}
	if id != stTestSnap(3, false).ID {
		t.Errorf("attachNewest picked %s, want the newest unattached snapshot", id)
	}
	if again, err := attachNewest(ctx, db, "c-sys"); err != nil || again != id {
		t.Errorf("attachNewest again = %s, %v; want %s", again, err, id)
	}
	got, err := getSnapshot(ctx, db, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ChangeID != "c-sys" || !got.System {
		t.Errorf("attached snapshot has change %q system %v, want c-sys true", got.ChangeID, got.System)
	}
	if of, err := snapshotOfChange(ctx, db, "c-sys"); err != nil || of != id {
		t.Errorf("snapshotOfChange = %s, %v; want %s", of, err, id)
	}
	if next, err := attachNewest(ctx, db, "c-2"); err != nil || next != stTestSnap(2, false).ID {
		t.Errorf("attachNewest for a second change = %s, %v", next, err)
	}
	first := stTestSnap(1, false).ID
	if err := attachSnapshot(ctx, db, first, "c-3"); err != nil {
		t.Fatal(err)
	}
	if err := attachSnapshot(ctx, db, first, "c-3"); err != nil {
		t.Errorf("attaching the same change again: %v", err)
	}
	if err := attachSnapshot(ctx, db, first, "c-4"); err == nil {
		t.Error("a snapshot attached to another change was attached again")
	}
	if err := attachSnapshot(ctx, db, "missing", "c-4"); !errors.Is(err, errSnapshotNotFound) {
		t.Errorf("attachSnapshot(missing) = %v", err)
	}
	if _, err := attachNewest(ctx, db, "c-5"); !errors.Is(err, errSnapshotNotFound) {
		t.Errorf("attachNewest with nothing unattached = %v", err)
	}
	if _, err := snapshotOfChange(ctx, db, "c-5"); !errors.Is(err, errSnapshotNotFound) {
		t.Errorf("snapshotOfChange without one = %v", err)
	}
}

func TestSnapshotPostHashesAndPin(t *testing.T) {
	ctx := context.Background()
	db := fwTestDB(t)
	s := stTestSnap(1, false)
	if _, err := saveSnapshot(ctx, db, s); err != nil {
		t.Fatal(err)
	}
	post := map[string]string{"/etc/ufw/user.rules": "sha256:bb"}
	if err := setPostHashes(ctx, db, s.ID, post); err != nil {
		t.Fatal(err)
	}
	if err := setPinned(ctx, db, s.ID, true); err != nil {
		t.Fatal(err)
	}
	got, err := getSnapshot(ctx, db, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.PostHashes, post) || !got.Pinned {
		t.Errorf("post hashes %v pinned %v", got.PostHashes, got.Pinned)
	}
	if err := setPinned(ctx, db, s.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := getSnapshot(ctx, db, s.ID); got.Pinned || !got.Baseline {
		t.Errorf("after unpin: pinned %v baseline %v, want false true", got.Pinned, got.Baseline)
	}
	if err := setPinned(ctx, db, "missing", true); !errors.Is(err, errSnapshotNotFound) {
		t.Errorf("setPinned(missing) = %v", err)
	}
	if err := setPostHashes(ctx, db, "missing", post); !errors.Is(err, errSnapshotNotFound) {
		t.Errorf("setPostHashes(missing) = %v", err)
	}
}

func TestSnapshotRetention(t *testing.T) {
	ctx := context.Background()
	db := fwTestDB(t)
	save := func(s snapshot) {
		t.Helper()
		if _, err := saveSnapshot(ctx, db, s); err != nil {
			t.Fatal(err)
		}
	}
	// n=1 becomes the baseline; 2 to 6 are old and each protected one way.
	save(stTestSnap(1, false))
	pinned := stTestSnap(2, false)
	pinned.Pinned = true
	save(pinned)
	for n, state := range map[int]string{3: "needs_repair", 4: "pending", 5: "applying", 6: "confirmed"} {
		s := stTestSnap(n, false)
		s.ChangeID = fmt.Sprintf("c-%d", n)
		stTestChange(t, db, s.ChangeID, state, false)
		save(s)
	}
	save(stTestSnap(7, false))
	if err := beginRestore(ctx, db, stTestSnap(7, false).ID, fwstate.TransReload, stTestT0); err != nil {
		t.Fatal(err)
	}
	save(stTestSnap(8, true))
	for n := 100; n < 100+keepSnapshots; n++ {
		save(stTestSnap(n, false))
	}
	for n := 200; n < 200+keepSystemSnapshots; n++ {
		save(stTestSnap(n, true))
	}
	ids := stTestIDs(t, db)
	for n, want := range map[int]bool{1: true, 2: true, 3: true, 4: true, 5: true, 6: false, 7: true, 8: false, 100: true, 149: true, 200: true, 209: true} {
		if got := ids[stTestSnap(n, false).ID]; got != want {
			t.Errorf("snapshot %d kept = %v, want %v", n, got, want)
		}
	}
	if want := 6 + keepSnapshots + keepSystemSnapshots; len(ids) != want {
		t.Errorf("%d snapshots kept, want %d", len(ids), want)
	}
	// One more of each kind pushes out the oldest unprotected one of that kind only.
	save(stTestSnap(150, false))
	save(stTestSnap(210, true))
	ids = stTestIDs(t, db)
	if ids[stTestSnap(100, false).ID] || ids[stTestSnap(200, true).ID] {
		t.Error("the oldest unpinned snapshot of each kind was not pruned")
	}
	if !ids[stTestSnap(101, false).ID] || !ids[stTestSnap(201, true).ID] || !ids[stTestSnap(7, false).ID] {
		t.Error("retention pruned a snapshot it keeps")
	}
}

func TestRestoreIntent(t *testing.T) {
	ctx := context.Background()
	db := fwTestDB(t)
	a, b := stTestSnap(1, false), stTestSnap(2, false)
	for _, s := range []snapshot{a, b} {
		if _, err := saveSnapshot(ctx, db, s); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, err := pendingRestore(ctx, db); err != nil || ok {
		t.Fatalf("pendingRestore on a new database = %v, %v", ok, err)
	}
	if err := beginRestore(ctx, db, a.ID, fwstate.TransDisable, stTestT0); err != nil {
		t.Fatal(err)
	}
	if tr, ok, err := restoreIntent(ctx, db, a.ID); err != nil || !ok || tr != fwstate.TransDisable {
		t.Errorf("restoreIntent(a) = %q, %v, %v", tr, ok, err)
	}
	if err := beginRestore(ctx, db, b.ID, fwstate.TransEnable, stTestT0); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := restoreIntent(ctx, db, a.ID); ok {
		t.Error("a new restore kept the superseded restore's record")
	}
	if id, ok, err := pendingRestore(ctx, db); err != nil || !ok || id != b.ID {
		t.Errorf("pendingRestore = %s, %v, %v", id, ok, err)
	}
	if err := endRestore(ctx, db, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := pendingRestore(ctx, db); ok {
		t.Error("endRestore left the record")
	}
}

func TestAxRestoreRecordKind(t *testing.T) {
	ctx := context.Background()
	db := fwTestDB(t)
	a, b := stTestSnap(1, false), stTestSnap(2, false)
	for _, s := range []snapshot{a, b} {
		if _, err := saveSnapshot(ctx, db, s); err != nil {
			t.Fatal(err)
		}
	}
	if err := beginRestore(ctx, db, a.ID, fwstate.TransReload, stTestT0); err != nil {
		t.Fatal(err)
	}
	rec, ok, err := restoreRecordOf(ctx, db, a.ID)
	if err != nil || !ok || rec.Transition != fwstate.TransReload || rec.Emergency != nil {
		t.Fatalf("restoreRecordOf(a) = %+v, %v, %v, want a normal reload", rec, ok, err)
	}
	emergency := json.RawMessage(`{"detail":{"guard":"ssh is dropped"}}`)
	if err := markEmergency(ctx, db, a.ID, emergency); err != nil {
		t.Fatal(err)
	}
	rec, _, err = restoreRecordOf(ctx, db, a.ID)
	if err != nil || rec.Transition != fwstate.TransReload || string(rec.Emergency) != string(emergency) {
		t.Errorf("after markEmergency: %+v, %v", rec, err)
	}
	if err := markEmergency(ctx, db, b.ID, emergency); err == nil {
		t.Error("markEmergency of a snapshot without a record succeeded")
	}
	if err := beginRestoreRecord(ctx, db, b.ID, restoreRecord{Transition: fwstate.TransDisable, Emergency: emergency}, stTestT0); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := restoreRecordOf(ctx, db, a.ID); ok {
		t.Error("a new restore kept the superseded restore's record")
	}
	rec, _, err = restoreRecordOf(ctx, db, b.ID)
	if err != nil || rec.Transition != fwstate.TransDisable || string(rec.Emergency) != string(emergency) {
		t.Errorf("restoreRecordOf(b) = %+v, %v", rec, err)
	}
	if tr, ok, err := restoreIntent(ctx, db, b.ID); err != nil || !ok || tr != fwstate.TransDisable {
		t.Errorf("restoreIntent(b) = %q, %v, %v", tr, ok, err)
	}
}

func TestAxRestoreRecordMigrationKeepsOldRows(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	coreSet, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	first, err := fs.ReadFile(agentMigrations(), "0001_firewall.sql")
	if err != nil {
		t.Fatal(err)
	}
	only1 := fstest.MapFS{"0001_firewall.sql": &fstest.MapFile{Data: first}}
	opts := store.MigrateOptions{Side: store.SideAgent, BinaryVersion: "test", BackupRoot: filepath.Join(t.TempDir(), "backups"), Keep: 5, Now: time.Now()}
	if res, err := store.Migrate(ctx, db, opts, coreSet, []store.MigrationSet{{Module: ID, FS: only1}}); err != nil || len(res.Failed) > 0 {
		t.Fatalf("migrate 0001: %v %v", err, res.Failed)
	}
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO firewall_snapshots (id, created_at, actor, entries, state, hashes) VALUES ('s1', 't', 'a', '[]', '{}', '{}')`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO firewall_restores (snapshot_id, transition, started_at) VALUES ('s1', 'reload', 't')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	opts.BinaryVersion = "test2"
	if res, err := store.Migrate(ctx, db, opts, coreSet, []store.MigrationSet{{Module: ID, FS: agentMigrations()}}); err != nil || len(res.Failed) > 0 {
		t.Fatalf("migrate 0002: %v %v", err, res.Failed)
	}
	rec, ok, err := restoreRecordOf(ctx, db, "s1")
	if err != nil || !ok || rec.Transition != fwstate.TransReload || rec.Emergency != nil {
		t.Errorf("a record written before the kind column = %+v, %v, %v, want a normal reload", rec, ok, err)
	}
}
