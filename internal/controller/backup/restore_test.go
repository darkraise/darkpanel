package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/controller/quarantine"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/paths"
)

const (
	rtPass     = "restore passphrase"
	rtChangeID = "01RTCHANGE00000000000000AA"

	rtControllerUnit = "darkpanel-controller.service"
	rtAgentUnit      = "darkpanel-agent.service"

	rtOwnerUID = 4242
	rtOwnerGID = 4343
)

// rtTx is the n-th transaction id: upper-case ULIDs, so a higher n sorts later.
func rtTx(n int) string {
	return ulid.MustNew(ulid.Timestamp(fxNow.Add(time.Duration(n)*time.Minute)), nil).String()
}

// rtUnits answers core.Units.State from a table; the embedded nil interface
// makes any other call panic.
type rtUnits struct {
	core.Units
	active map[string]string // unit -> ActiveState; a unit not listed is inactive
}

func (u *rtUnits) State(_ context.Context, unit string) (core.UnitState, error) {
	state := "inactive"
	if s, ok := u.active[unit]; ok {
		state = s
	}
	return core.UnitState{LoadState: "loaded", ActiveState: state}, nil
}

// rtLayout is an installed controller under a temporary directory and a Host over it.
type rtLayout struct {
	paths paths.Paths
	host  Host
	units *rtUnits
	clock *coretest.FakeClock
}

// rtNewLayout installs a controller: a migrated database carrying rt_marker
// "installed" with a stale -wal and -shm beside it, and a key file. Restore
// transaction ids come from tx.
func rtNewLayout(t *testing.T, tx string) *rtLayout {
	t.Helper()
	root := t.TempDir()
	p := paths.Under(root)
	if err := p.MkdirAll(); err != nil {
		t.Fatal(err)
	}
	db := fxDB(t, p.ControllerDir)
	rtExec(t, db, `INSERT INTO core_meta (key, value) VALUES ('rt_marker', 'installed')`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	keyPath, _, _ := fxKey(t, t.TempDir())
	if err := os.Rename(keyPath, p.ControllerKey); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{p.ControllerDB() + "-wal", p.ControllerDB() + "-shm"} {
		if err := os.WriteFile(f, []byte("stale "+filepath.Base(f)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	unit := filepath.Join(root, "units", "darkpanel-controller.service")
	run := filepath.Join(root, "run")
	for _, dir := range []string{filepath.Dir(unit), run} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(unit, []byte("[Service]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	units := &rtUnits{active: map[string]string{}}
	clock := coretest.NewFakeClock(fxNow.Add(time.Hour))
	return &rtLayout{paths: p, units: units, clock: clock, host: Host{
		Paths: p, UnitFile: unit, LockFile: filepath.Join(run, "restore.lock"), Units: units,
		Euid:  func() int { return 0 },
		Owner: func() (int, int, error) { return os.Getuid(), os.Getgid(), nil },
		Ops:   OSOps(), Clock: clock, NewTx: func() string { return tx },
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), JournalUID: os.Getuid(),
	}}
}

func rtExec(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	if err := db.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), query, args...)
		return err
	}); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func rtRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304: a path under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func rtSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// rtBackup is a backup file and what it holds.
type rtBackup struct {
	file      []byte
	db, key   []byte    // the plaintext members
	createdAt time.Time // the sealed canary's createdAt
}

// rtPack writes a backup file holding the given members, under rtPass.
func rtPack(t *testing.T, dbBytes, key []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	err := Write(&buf, rand.Reader, []byte(rtPass), TestParams, []Member{
		{Name: MemberDB, Size: int64(len(dbBytes)), Body: bytes.NewReader(dbBytes)},
		{Name: MemberKey, Size: int64(len(key)), Body: bytes.NewReader(key)},
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	return buf.Bytes()
}

// rtMakeBackup builds a backup from a second fixture database: its canary is
// sealed with a second key, rt_marker is "backup", and it holds an applying
// change with a lock plus an update reservation lock. Each mutate runs on the
// database before it is closed and packed.
func rtMakeBackup(t *testing.T, mutate ...func(*testing.T, *store.DB)) rtBackup {
	t.Helper()
	db := fxDB(t, t.TempDir())
	_, key, sealer := fxKey(t, t.TempDir())
	created := fxNow.Add(-24 * time.Hour)
	plain, err := json.Marshal(Canary{CreatedAt: created})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealer.Seal(CanaryAD, plain)
	if err != nil {
		t.Fatal(err)
	}
	rtExec(t, db, `INSERT INTO core_meta (key, value) VALUES (?, ?)`, CanaryKey, base64.StdEncoding.EncodeToString(sealed))
	rtExec(t, db, `INSERT INTO core_meta (key, value) VALUES ('rt_marker', 'backup')`)
	fxInsertChange(t, db, fxChange{ID: rtChangeID, HostID: "h1", Action: "firewall.rule-add", State: core.StateApplying, Owner: core.OwnerEngine})
	fxInsertLock(t, db, "firewall:h1", rtChangeID)
	fxInsertLock(t, db, "update:h1", "update-tx-1")
	for _, m := range mutate {
		m(t, db)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	dbBytes := rtRead(t, db.Path())
	return rtBackup{file: rtPack(t, dbBytes, key), db: dbBytes, key: key, createdAt: created}
}

// rtDigest maps every file under dirs to the SHA-256 of its content and every
// directory to "dir", so two states compare without printing file content.
func rtDigest(t *testing.T, dirs ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				out[path] = "dir"
				return nil
			}
			out[path] = rtSum(rtRead(t, path))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func (l *rtLayout) dirs() []string {
	return []string{l.paths.ControllerDir, filepath.Dir(l.paths.ControllerKey)}
}

func (l *rtLayout) digest(t *testing.T) map[string]string {
	t.Helper()
	return rtDigest(t, l.dirs()...)
}

// rtDiff names what differs between two digests, "" when they are equal.
func rtDiff(want, got map[string]string) string {
	if maps.Equal(want, got) {
		return ""
	}
	var out []string
	for path, sum := range want {
		switch g, ok := got[path]; {
		case !ok:
			out = append(out, "missing "+filepath.Base(path))
		case g != sum:
			out = append(out, "changed "+filepath.Base(path))
		}
	}
	for path := range got {
		if _, ok := want[path]; !ok {
			out = append(out, "new "+filepath.Base(path))
		}
	}
	slices.Sort(out)
	return strings.Join(out, ", ")
}

// rtFiles lists the regular files directly in dir, sorted.
func rtFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.Type().IsRegular() {
			out = append(out, e.Name())
		}
	}
	slices.Sort(out)
	return out
}

func rtOpenRO(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// rtMeta is core_meta's value for key, "" when the row is missing.
func rtMeta(t *testing.T, db *sql.DB, key string) string {
	t.Helper()
	var v string
	err := db.QueryRowContext(context.Background(), `SELECT value FROM core_meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatalf("read core_meta %s: %v", key, err)
	}
	return v
}

func rtMarker(t *testing.T, db *sql.DB) quarantine.Marker {
	t.Helper()
	var m quarantine.Marker
	if err := json.Unmarshal([]byte(rtMeta(t, db, quarantine.MetaKey)), &m); err != nil {
		t.Fatalf("decode the restore_quarantine marker: %v", err)
	}
	return m
}

func rtRestore(t *testing.T, l *rtLayout, bk rtBackup) RestoreResult {
	t.Helper()
	res, err := Restore(context.Background(), l.host, bytes.NewReader(bk.file), []byte(rtPass))
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	return res
}

// rtOps records the renames, chowns and chmods of a restore. Chown only
// records: the test process cannot give a file to another user.
type rtOps struct {
	Ops
	mu     sync.Mutex
	events []string
}

func (o *rtOps) note(format string, args ...any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, fmt.Sprintf(format, args...))
}

func (o *rtOps) Chown(path string, uid, gid int) error {
	o.note("chown %s %d %d", filepath.Base(path), uid, gid)
	return nil
}

func (o *rtOps) Chmod(path string, perm fs.FileMode) error {
	o.note("chmod %s %v", filepath.Base(path), perm)
	return o.Ops.Chmod(path, perm)
}

func (o *rtOps) Rename(from, to string) error {
	o.note("rename %s %s", filepath.Base(from), filepath.Base(to))
	return o.Ops.Rename(from, to)
}

func TestRtRestoreInstallsTheBackup(t *testing.T) {
	tx := rtTx(1)
	l := rtNewLayout(t, tx)
	bk := rtMakeBackup(t)
	p := l.paths
	ops := &rtOps{Ops: OSOps()}
	l.host.Ops = ops
	l.host.Owner = func() (int, int, error) { return rtOwnerUID, rtOwnerGID, nil }
	originals := map[string]string{}
	for _, f := range Targets(p) {
		originals[f] = rtSum(rtRead(t, f))
	}

	res := rtRestore(t, l, bk)
	if res.Tx != tx || !res.BackupCreatedAt.Equal(bk.createdAt) {
		t.Errorf("result = %+v, want tx %s and backupCreatedAt %s", res, tx, bk.createdAt)
	}

	db := rtOpenRO(t, p.ControllerDB())
	if got := rtMeta(t, db, "rt_marker"); got != "backup" {
		t.Errorf("installed database rt_marker = %q, want the backup's", got)
	}
	m := rtMarker(t, db)
	if m.Tx != tx || !m.RestoredAt.Equal(l.clock.Now()) || !m.BackupCreatedAt.Equal(bk.createdAt) {
		t.Errorf("marker = %+v, want tx %s, restoredAt %s and backupCreatedAt %s", m, tx, l.clock.Now(), bk.createdAt)
	}
	var state string
	if err := db.QueryRowContext(context.Background(),
		`SELECT state || '/' || quarantined_from FROM core_changes WHERE id = ?`, rtChangeID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "quarantined/applying" {
		t.Errorf("the applying change is %q, want quarantined/applying", state)
	}
	var locks []string
	rows, err := db.QueryContext(context.Background(), `SELECT key FROM core_locks ORDER BY key`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		locks = append(locks, k)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(locks, []string{"firewall:h1"}) {
		t.Errorf("locks = %v, want only the quarantined change's", locks)
	}
	if !bytes.Equal(rtRead(t, p.ControllerKey), bk.key) {
		t.Error("the installed key is not the backup's key")
	}

	for _, f := range Targets(p) {
		aside := AsideName(f, tx)
		if got := rtSum(rtRead(t, aside)); got != originals[f] {
			t.Errorf("%s does not hold the original %s", filepath.Base(aside), filepath.Base(f))
		}
	}
	for _, f := range []string{p.ControllerDB(), p.ControllerKey} {
		if info, err := os.Stat(f); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("%s: mode %v, error %v, want 0600", filepath.Base(f), info, err)
		}
	}
	wantDir := []string{"controller.db", "controller.db-shm.pre-" + tx, "controller.db-wal.pre-" + tx, "controller.db.pre-" + tx}
	slices.Sort(wantDir)
	if got := rtFiles(t, p.ControllerDir); !slices.Equal(got, wantDir) {
		t.Errorf("controller directory files = %v, want %v (no journal, staging or -wal/-shm file)", got, wantDir)
	}
	wantKey := []string{"controller.key", "controller.key.pre-" + tx}
	if got := rtFiles(t, filepath.Dir(p.ControllerKey)); !slices.Equal(got, wantKey) {
		t.Errorf("key directory files = %v, want %v", got, wantKey)
	}

	var staged []string
	for _, e := range ops.events {
		if strings.Contains(e, ".restore-"+tx) {
			staged = append(staged, e)
		}
	}
	owner := fmt.Sprintf("%d %d", rtOwnerUID, rtOwnerGID)
	wantEvents := []string{
		"chown controller.db.restore-" + tx + " " + owner,
		"chmod controller.db.restore-" + tx + " -rw-------",
		"rename controller.db.restore-" + tx + " controller.db",
		"chown controller.key.restore-" + tx + " " + owner,
		"chmod controller.key.restore-" + tx + " -rw-------",
		"rename controller.key.restore-" + tx + " controller.key",
	}
	if !slices.Equal(staged, wantEvents) {
		t.Errorf("install steps =\n%s\nwant\n%s", strings.Join(staged, "\n"), strings.Join(wantEvents, "\n"))
	}
}

func TestRtRestoreRefusals(t *testing.T) {
	bk := rtMakeBackup(t)
	cases := []struct {
		name    string
		arrange func(t *testing.T, l *rtLayout)
		want    string
	}{
		{"not root", func(_ *testing.T, l *rtLayout) { l.host.Euid = func() int { return 1000 } },
			"a restore must run as root"},
		{"unit file missing", func(t *testing.T, l *rtLayout) {
			if err := os.Remove(l.host.UnitFile); err != nil {
				t.Fatal(err)
			}
		}, "no controller is installed here"},
		{"key file missing", func(t *testing.T, l *rtLayout) {
			if err := os.Remove(l.paths.ControllerKey); err != nil {
				t.Fatal(err)
			}
		}, "no controller is installed here"},
		{"controller running", func(_ *testing.T, l *rtLayout) { l.units.active[rtControllerUnit] = "active" },
			"darkpanel-controller.service is active"},
		{"agent running", func(_ *testing.T, l *rtLayout) { l.units.active[rtAgentUnit] = "active" },
			"darkpanel-agent.service is active"},
		{"journal exists", func(t *testing.T, l *rtLayout) {
			if err := os.WriteFile(JournalPath(l.paths.ControllerDir, rtTx(0)), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "an unfinished restore exists"},
		{"lock held", func(t *testing.T, l *rtLayout) {
			release, err := l.host.lock()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(release)
		}, "another restore or recovery holds"},
		{"owner unknown", func(_ *testing.T, l *rtLayout) {
			l.host.Owner = func() (int, int, error) { return 0, 0, errors.New("user darkpanel not found") }
		}, "user darkpanel not found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := rtNewLayout(t, rtTx(1))
			c.arrange(t, l)
			before := l.digest(t)
			_, err := Restore(context.Background(), l.host, bytes.NewReader(bk.file), []byte(rtPass))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Restore error = %v, want one containing %q", err, c.want)
			}
			if diff := rtDiff(before, l.digest(t)); diff != "" {
				t.Errorf("the refused restore changed the installation: %s", diff)
			}
		})
	}
}

func TestRtRestoreRejectsABadBackup(t *testing.T) {
	good := rtMakeBackup(t)
	_, otherKey, _ := fxKey(t, t.TempDir())
	is := func(target error) func(error) bool {
		return func(err error) bool { return errors.Is(err, target) }
	}
	cases := []struct {
		name string
		file []byte
		pass string
		ok   func(error) bool
	}{
		{"wrong passphrase", good.file, "not the passphrase", is(ErrAuth)},
		{"truncated file", good.file[:len(good.file)-100], rtPass,
			func(err error) bool { return errors.Is(err, ErrTruncated) || errors.Is(err, ErrAuth) }},
		{"not a backup", append([]byte("NOPE"), make([]byte, 200)...), rtPass, is(ErrNotBackup)},
		{"database that is not SQLite", rtPack(t, []byte("this is not a database"), good.key), rtPass,
			func(err error) bool { return err != nil }},
		{"key that does not open the canary", rtPack(t, good.db, otherKey), rtPass,
			func(err error) bool { return err != nil }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := rtNewLayout(t, rtTx(1))
			before := l.digest(t)
			_, err := Restore(context.Background(), l.host, bytes.NewReader(c.file), []byte(c.pass))
			if err == nil || !c.ok(err) {
				t.Fatalf("Restore error = %v, want the failure of this row", err)
			}
			if diff := rtDiff(before, l.digest(t)); diff != "" {
				t.Errorf("the failed restore left changes behind (originals, staging or a journal): %s", diff)
			}
		})
	}
}

func TestRtSecondRestoreRemovesTheFirstRestoresAsides(t *testing.T) {
	tx1, tx2 := rtTx(1), rtTx(2)
	l := rtNewLayout(t, tx1)
	bk := rtMakeBackup(t)
	p := l.paths

	rtRestore(t, l, bk)
	installedByFirst := rtSum(rtRead(t, p.ControllerDB()))
	l.host.NewTx = func() string { return tx2 }
	res := rtRestore(t, l, bk)
	if res.Tx != tx2 {
		t.Fatalf("second restore tx = %s, want %s", res.Tx, tx2)
	}

	wantDir := []string{"controller.db", "controller.db.pre-" + tx2}
	if got := rtFiles(t, p.ControllerDir); !slices.Equal(got, wantDir) {
		t.Errorf("controller directory files = %v, want %v (the first restore's .pre files are gone)", got, wantDir)
	}
	wantKey := []string{"controller.key", "controller.key.pre-" + tx2}
	if got := rtFiles(t, filepath.Dir(p.ControllerKey)); !slices.Equal(got, wantKey) {
		t.Errorf("key directory files = %v, want %v", got, wantKey)
	}
	if got := rtSum(rtRead(t, AsideName(p.ControllerDB(), tx2))); got != installedByFirst {
		t.Error("the second restore did not set aside the database the first one installed")
	}
	if got := rtMarker(t, rtOpenRO(t, p.ControllerDB())).Tx; got != tx2 {
		t.Errorf("installed marker tx = %s, want %s", got, tx2)
	}
}

func TestRtRestoreKeepsPreFilesNoRestoreNamed(t *testing.T) {
	tx1, tx2 := rtTx(1), rtTx(2)
	l := rtNewLayout(t, tx1)
	bk := rtMakeBackup(t)
	p := l.paths
	rtRestore(t, l, bk)

	keep := []string{
		p.ControllerDB() + ".pre-restore",
		p.ControllerKey + ".pre-rotation",
		AsideName(p.ControllerDB(), strings.ToLower(tx1)),
		AsideName(p.ControllerKey, tx1+"0"),
	}
	for _, f := range keep {
		if err := os.WriteFile(f, []byte("operator copy"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	l.host.NewTx = func() string { return tx2 }
	rtRestore(t, l, bk)

	for _, f := range keep {
		if b, err := os.ReadFile(f); err != nil || string(b) != "operator copy" { //nolint:gosec // G304: a path under t.TempDir()
			t.Errorf("%s: %q, error %v; want it kept as it was", filepath.Base(f), b, err)
		}
	}
	for _, f := range Targets(p) {
		if _, err := os.Lstat(AsideName(f, tx1)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: error %v, want the first restore's set-aside file removed", filepath.Base(AsideName(f, tx1)), err)
		}
	}
}

func TestRtRestoreMigratesAnOlderBackupWithoutAnotherCopy(t *testing.T) {
	tx := rtTx(1)
	l := rtNewLayout(t, tx)
	older := rtMakeBackup(t, func(t *testing.T, db *store.DB) {
		rtExec(t, db, `DROP TABLE tailnet_keys`)
		rtExec(t, db, `DROP TABLE tailnet_accounts`)
		rtExec(t, db, `DELETE FROM core_migrations WHERE module = 'tailnet'`)
		rtExec(t, db, `INSERT INTO core_meta (key, value) VALUES ('binary_version', '0.0.1')
			ON CONFLICT (key) DO UPDATE SET value = excluded.value`)
	})
	dirsOf := func(m map[string]string) []string {
		var out []string
		for path, sum := range m {
			if sum == "dir" {
				out = append(out, path)
			}
		}
		slices.Sort(out)
		return out
	}
	before := dirsOf(l.digest(t))

	rtRestore(t, l, older)

	if after := dirsOf(l.digest(t)); !slices.Equal(before, after) {
		t.Errorf("directories before %v, after %v: the migration of the staged database made a copy of it", before, after)
	}
	db := rtOpenRO(t, l.paths.ControllerDB())
	var n int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM tailnet_keys`).Scan(&n); err != nil {
		t.Errorf("the tailnet tables were not migrated into the restored database: %v", err)
	}
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM core_migrations WHERE module = 'tailnet'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("tailnet migrations recorded = %d (error %v), want 1", n, err)
	}
}
