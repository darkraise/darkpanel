//go:build harness

package harness

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/backup"
	ctlfleet "github.com/darkraise/darkpanel/internal/controller/fleet"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/controller/quarantine"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/paths"
)

func TestM7rUnitDecode(t *testing.T) {
	st, err := m7rDecode[backup.Status](`{"active":true,"tx":"T","restoredAt":"2026-11-04T12:00:00Z","records":[]}` + "\n")
	if err != nil || !st.Active || st.Tx != "T" || st.RestoredAt == nil || st.Records == nil || len(st.Records) != 0 {
		t.Errorf("m7rDecode of a status = %+v, %v; want it decoded with an empty list", st, err)
	}
	for _, tc := range []struct{ name, in string }{
		{"an unknown member", `{"active":false,"records":[],"extra":1}`},
		{"a second document", `{"active":false,"records":[]} {}`},
		{"text before the document", "sudo: unable to resolve host\n" + `{"active":false,"records":[]}`},
		{"nothing", ""},
	} {
		if _, err := m7rDecode[backup.Status](tc.in); err == nil {
			t.Errorf("%s: m7rDecode accepted %q", tc.name, tc.in)
		}
	}
}

// m7rUnitArchive is a backup file under pass holding db and key.
func m7rUnitArchive(t *testing.T, pass, db, key string) []byte {
	t.Helper()
	var buf bytes.Buffer
	err := backup.Write(&buf, rand.Reader, []byte(pass), backup.TestParams, []backup.Member{
		{Name: backup.MemberDB, Size: int64(len(db)), Body: strings.NewReader(db)},
		{Name: backup.MemberKey, Size: int64(len(key)), Body: strings.NewReader(key)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestM7rUnitCheckBackup(t *testing.T) {
	const pass = "m7r unit passphrase" //nolint:gosec // G101: a test passphrase
	sqlite := "SQLite format 3\x00" + strings.Repeat("d", 100000)
	key := secret.KeyPrefix + strings.Repeat("A", 43) + "=\n"
	good := m7rUnitArchive(t, pass, sqlite, key)
	if err := m7rCheckBackup(good, []byte(pass)); err != nil {
		t.Fatalf("a good backup: %v", err)
	}
	for _, tc := range []struct {
		name string
		data []byte
		pass string
		want string
	}{
		{"a wrong passphrase", good, "another passphrase", "wrong passphrase"},
		{"a database that is not SQLite", m7rUnitArchive(t, pass, strings.Repeat("d", 1000), key), pass, "not an SQLite database"},
		{"a key that is not a key file", m7rUnitArchive(t, pass, sqlite, "not a key"), pass, "not a controller key file"},
		{"the passphrase in the clear", []byte("header " + pass + " trailer"), pass, "in the clear"},
		{"a truncated file", good[:len(good)-5], pass, "backup file"},
	} {
		err := m7rCheckBackup(tc.data, []byte(tc.pass))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error containing %q", tc.name, err, tc.want)
		}
	}
}

func TestM7rUnitCheckQuarantine(t *testing.T) {
	restored := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	want := backup.Record{
		ID: m7rCompositeID, Kind: "composite", Action: m7rCompositeAction, HostID: "",
		QuarantinedFrom: core.StateApplying, Locks: []string{m7rLockKey},
	}
	good := func() backup.Status {
		r := want
		r.Locks = append([]string(nil), want.Locks...)
		return backup.Status{Active: true, Tx: "01JTX", RestoredAt: &restored, Records: []backup.Record{r}}
	}
	if err := m7rCheckQuarantine(good(), want); err != nil {
		t.Fatalf("the good status: %v", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*backup.Status)
		want   string
	}{
		{"inactive", func(s *backup.Status) { s.Active = false }, "not active"},
		{"no transaction", func(s *backup.Status) { s.Tx = "" }, "no restore transaction"},
		{"no restore time", func(s *backup.Status) { s.RestoredAt = nil }, "restoredAt"},
		{"no record", func(s *backup.Status) { s.Records = nil }, "0 records"},
		{"two records", func(s *backup.Status) { s.Records = append(s.Records, s.Records[0]) }, "2 records"},
		{"another record", func(s *backup.Status) { s.Records[0].ID = "c2" }, "is c2, want " + m7rCompositeID},
		{"a single change", func(s *backup.Status) { s.Records[0].Kind = "change" }, "kind"},
		{"another action", func(s *backup.Status) { s.Records[0].Action = "derp.server.upgrade" }, "action"},
		{"a host", func(s *backup.Status) { s.Records[0].HostID = "h2" }, "host"},
		{"another prior state", func(s *backup.Status) { s.Records[0].QuarantinedFrom = core.StatePending }, "quarantinedFrom"},
		{"null locks", func(s *backup.Status) { s.Records[0].Locks = nil }, "locks are null"},
		{"another lock", func(s *backup.Status) { s.Records[0].Locks = []string{"other"} }, "locks are [other]"},
	} {
		st := good()
		tc.change(&st)
		if err := m7rCheckQuarantine(st, want); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error containing %q", tc.name, err, tc.want)
		}
	}
}

// TestM7rUnitSeed runs the seed on a migrated controller database and marks
// it as a restore does: the rows fit every CHECK and reference, and marking
// quarantines the composite, keeps its lock and deletes the update's.
func TestM7rUnitSeed(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	coreSet, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	var sets []store.MigrationSet
	for _, m := range modules.All() {
		man := m.Manifest()
		if fsys, ok := man.Migrations[core.SideController]; ok && fsys != nil {
			sets = append(sets, store.MigrationSet{Module: man.ID, FS: fsys})
		}
	}
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	if _, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideController, BinaryVersion: "dev", BackupRoot: filepath.Join(dir, "backups"), Keep: 1, Now: now,
	}, coreSet, sets); err != nil {
		t.Fatal(err)
	}
	const agent = "01JW7RAGENT0000000000000AG"
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		for _, stmt := range m7rSeedStatements(agent, now) {
			if strings.HasSuffix(strings.TrimSpace(stmt), ";") {
				return fmt.Errorf("a statement ends in a semicolon: %s", stmt)
			}
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("%s: %w", stmt, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("the seed does not fit the controller schema: %v", err)
	}
	q := func(query string, args ...any) string {
		t.Helper()
		var v string
		if err := db.Read().QueryRowContext(ctx, query, args...).Scan(&v); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return v
	}
	if got := q("SELECT state || '|' || recovery_owner || '|' || class FROM core_changes WHERE id = ?", m7rCompositeID); got != "applying|composite|composite" {
		t.Errorf("the seeded composite = %q, want applying|composite|composite", got)
	}
	if got := q("SELECT count(*) FROM core_locks WHERE (key = ? AND owner = ?) OR (key = ? AND owner = ?)",
		m7rLockKey, m7rCompositeID, m7rReservationKey, m7rReservationOwner); got != "2" {
		t.Errorf("seeded lock rows = %s, want 2", got)
	}
	if err := backup.Mark(ctx, db, quarantine.Marker{Tx: "01JTX", RestoredAt: now, BackupCreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, query, want string }{
		{"the composite", "SELECT state || '|' || quarantined_from FROM core_changes WHERE id = ?", "quarantined|applying"},
		{"its position", "SELECT direction || '|' || cursor FROM core_composites WHERE change_id = ?", "forward|0"},
		{"its step", "SELECT state || '|' || host_id || '|' || coalesce(child_plan_id, '') FROM core_composite_steps WHERE change_id = ?", "pending|" + agent + "|"},
		{"its children", "SELECT count(*) FROM core_changes WHERE parent_change_id = ?", "0"},
		{"its locks", "SELECT group_concat(key) FROM core_locks WHERE owner = ?", m7rLockKey},
	} {
		if got := q(c.query, m7rCompositeID); got != c.want {
			t.Errorf("%s after marking = %q, want %q", c.name, got, c.want)
		}
	}
	if got := q("SELECT count(*) FROM core_locks WHERE key = ?", m7rReservationKey); got != "0" {
		t.Errorf("update reservation rows after marking = %s, want 0: marking deletes a lock whose owner is not a change", got)
	}
}

func TestM7rUnitCheckToken(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		res  backup.EnrollTokenResult
		want string // "" accepts
	}{
		{"an hour", backup.EnrollTokenResult{Token: "t", ExpiresAt: now.Add(ctlfleet.TokenTTL)}, ""},
		{"an hour less clock skew", backup.EnrollTokenResult{Token: "t", ExpiresAt: now.Add(ctlfleet.TokenTTL - 4*time.Minute)}, ""},
		{"no token", backup.EnrollTokenResult{ExpiresAt: now.Add(ctlfleet.TokenTTL)}, "empty"},
		{"expired", backup.EnrollTokenResult{Token: "t", ExpiresAt: now.Add(-time.Second)}, "already expired"},
		{"two hours", backup.EnrollTokenResult{Token: "t", ExpiresAt: now.Add(2 * time.Hour)}, "more than"},
		{"ten minutes", backup.EnrollTokenResult{Token: "t", ExpiresAt: now.Add(10 * time.Minute)}, "less than"},
	} {
		err := m7rCheckToken(tc.res, now)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: %v, want an error containing %q", tc.name, err, tc.want)
		}
	}
}

func TestM7rUnitCheckNames(t *testing.T) {
	dbDir := "backups\ncontroller.db\ncontroller.db-shm\ncontroller.db-wal\ncontroller.db-wal.pre-01JTX\ncontroller.db.pre-01JTX\n"
	for _, tc := range []struct {
		name, listing, base, want string
	}{
		{"the database directory", dbDir, "controller.db", ""},
		{"the key directory", "agent\ncontroller.key\ncontroller.key.pre-01JTX\n", "controller.key", ""},
		{"no database", "controller.db.pre-01JTX\n", "controller.db", "controller.db is missing"},
		{"no set-aside copy", "controller.db\ncontroller.db-wal.pre-01JTX\n", "controller.db", "set-aside"},
		{"a staged file left", dbDir + "controller.db.restore-01JTX\n", "controller.db", "staged file controller.db.restore-01JTX"},
		{"a staged wal left", dbDir + "controller.db-wal.restore-01JTX\n", "controller.db", "staged file"},
		{"a journal left", dbDir + "restore-01JTX.journal\n", "controller.db", "journal restore-01JTX.journal"},
		{"nothing", "", "controller.key", "missing"},
	} {
		err := m7rCheckNames(tc.listing, tc.base)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: %v, want an error containing %q", tc.name, err, tc.want)
		}
	}
}

func TestM7rUnitExecStartPre(t *testing.T) {
	const bin = "/usr/local/lib/darkpanel/darkpanel"
	show := func(argv, tail string) string {
		return "ExecStartPre={ path=" + bin + " ; argv[]=" + argv + " ; ignore_errors=no ; start_time=[Wed 2026-11-04 12:00:01 UTC] ; stop_time=[Wed 2026-11-04 12:00:01 UTC] ; pid=812 ; " + tail + " }\n"
	}
	for _, tc := range []struct {
		name, out, want string
	}{
		{"a clean run", show(bin+" controller restore --recover", "code=exited ; status=0"), ""},
		{"a run that failed", show(bin+" controller restore --recover", "code=exited ; status=1"), "clean exit"},
		{"a run that was killed", show(bin+" controller restore --recover", "code=killed ; status=9/KILL"), "clean exit"},
		{"a unit that never ran it", "ExecStartPre={ path=" + bin + " ; argv[]=" + bin + " controller restore --recover ; ignore_errors=no ; start_time=[n/a] ; stop_time=[n/a] ; pid=0 ; code=(null) ; status=0/0 }\n", "clean exit"},
		{"another command", show(bin+" serve", "code=exited ; status=0"), "other than controller restore --recover"},
		{"no line", "", "no ExecStartPre"},
	} {
		err := m7rExecStartPre(tc.out)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: %v, want an error containing %q", tc.name, err, tc.want)
		}
	}
}

// TestM7rUnitLab: the restored controller's addresses are the next ones of
// the M2 fleet and are valid agent addresses, and the commands that install
// the binary and the role are the ones install.sh runs.
func TestM7rUnitLab(t *testing.T) {
	if want := net.JoinHostPort(m7rRestoredPriv, strconv.Itoa(listen.AgentPort)); m7rRestoredAddress != want {
		t.Errorf("m7rRestoredAddress = %q, want %q", m7rRestoredAddress, want)
	}
	if err := install.CheckAgentAddress(m7rRestoredAddress); err != nil {
		t.Errorf("the restored controller's address: %v", err)
	}
	seen := map[string]string{}
	for name, addr := range map[string]string{
		"controller IPv4": targetIPv4, "agent IPv4": agentIPv4, "restored IPv4": m7rRestoredIPv4,
		"controller IPv6": targetIPv6, "agent IPv6": agentIPv6, "restored IPv6": m7rRestoredIPv6,
		"controller private": controllerPriv, "agent private": agentPriv, "restored private": m7rRestoredPriv,
	} {
		if other, dup := seen[addr]; dup {
			t.Errorf("%s and %s share the address %s", name, other, addr)
		}
		seen[addr] = name
	}
	p := paths.Production()
	prepare, link := m7rBinaryCommands()
	if prepare != "sudo install -d -m 0755 "+p.BinaryDir || link != "sudo ln -sfn "+p.Binary()+" "+p.BinaryLink {
		t.Errorf("m7rBinaryCommands = %q, %q; want the binary directory made and the link on PATH set", prepare, link)
	}
	want := "sudo darkpanel system install --role controller --agent-listen 10.77.0.12:" + strconv.Itoa(listen.AgentPort) +
		" --agent-advertise 10.77.0.12:" + strconv.Itoa(listen.AgentPort)
	if got := m7rControllerInstall(m7rRestoredAddress); got != want {
		t.Errorf("m7rControllerInstall = %q, want %q", got, want)
	}
	if m7rDeadline < 30*time.Minute || m7rDeadline > 60*time.Minute {
		t.Errorf("m7rDeadline = %v, want 30 to 60 minutes: three VMs boot, install and restore", m7rDeadline)
	}
}
