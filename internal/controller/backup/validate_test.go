package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/quarantine"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
)

var vfCreatedAt = time.Date(2026, 11, 3, 8, 15, 0, 500, time.UTC)

func vfExec(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Writer().ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// vfSealCanary stores a canary sealed by sealer in db's core_meta.
func vfSealCanary(t *testing.T, db *store.DB, sealer *secret.Sealer, createdAt time.Time) {
	t.Helper()
	plain, err := json.Marshal(Canary{CreatedAt: createdAt})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealer.Seal(CanaryAD, plain)
	if err != nil {
		t.Fatal(err)
	}
	vfExec(t, db, `INSERT INTO core_meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		CanaryKey, base64.StdEncoding.EncodeToString(sealed))
}

type vfStaged struct {
	path    string // the closed database
	keyFile []byte // the key file's bytes
	sealer  *secret.Sealer
}

// vfNewStaged builds a closed controller database holding a canary sealed
// with a fresh key, as a backup holds it, and returns it with that key.
func vfNewStaged(t *testing.T) vfStaged {
	t.Helper()
	keyPath, _, sealer := fxKey(t, t.TempDir())
	keyFile, err := os.ReadFile(keyPath) //nolint:gosec // G304: a path under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	db := fxDB(t, "")
	vfSealCanary(t, db, sealer, vfCreatedAt)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return vfStaged{path: db.Path(), keyFile: keyFile, sealer: sealer}
}

// vfReopen runs fn on the staged database opened for writing and closes it.
func vfReopen(t *testing.T, path string, fn func(db *store.DB)) {
	t.Helper()
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	fn(db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func vfNoSideFiles(t *testing.T, path string) {
	t.Helper()
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s%s exists (stat error %v); want none", path, suffix, err)
		}
	}
}

func TestVfValidateStagedAcceptsAGoodDatabase(t *testing.T) {
	s := vfNewStaged(t)
	got, err := ValidateStaged(context.Background(), s.path, s.keyFile)
	if err != nil {
		t.Fatalf("ValidateStaged: %v", err)
	}
	if !got.Equal(vfCreatedAt) {
		t.Errorf("created at = %s, want the canary's %s", got, vfCreatedAt)
	}
	vfNoSideFiles(t, s.path)
}

func TestVfValidateStagedAcceptsAnOlderSchema(t *testing.T) {
	s := vfNewStaged(t)
	vfReopen(t, s.path, func(db *store.DB) {
		vfExec(t, db, `DELETE FROM core_migrations WHERE module = 'cloudflare'`)
		vfExec(t, db, `DELETE FROM core_migrations WHERE module = 'core' AND number = (SELECT max(number) FROM core_migrations WHERE module = 'core')`)
	})
	if _, err := ValidateStaged(context.Background(), s.path, s.keyFile); err != nil {
		t.Errorf("a database that lacks migrations the binary has: %v; it is migrated in a later phase", err)
	}
}

func TestVfValidateStagedRefusals(t *testing.T) {
	_, _, otherSealer := fxKey(t, t.TempDir())
	insertMigration := func(module string) func(t *testing.T, s vfStaged) {
		return func(t *testing.T, s vfStaged) {
			vfReopen(t, s.path, func(db *store.DB) {
				vfExec(t, db, `INSERT INTO core_migrations (module, side, number, name, checksum, applied_at)
					VALUES (?, 'controller', 9999, 'from_the_future', 'x', '2026-11-04T12:00:00Z')`, module)
			})
		}
	}
	cases := []struct {
		name   string
		damage func(t *testing.T, s vfStaged)
		key    func(s vfStaged) []byte // nil: the staged database's key file
		want   string
	}{
		{"garbage instead of a database", func(t *testing.T, s vfStaged) {
			if err := os.WriteFile(s.path, bytes.Repeat([]byte("not a database "), 600), 0o600); err != nil {
				t.Fatal(err)
			}
		}, nil, ""},
		{"damaged pages", func(t *testing.T, s vfStaged) {
			f, err := os.OpenFile(s.path, os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			if info, err := f.Stat(); err != nil || info.Size() < 4*4096 {
				t.Fatalf("fixture database is %v (%v); the test damages pages 2 and 3", info, err)
			}
			if _, err := f.WriteAt(bytes.Repeat([]byte{0xFF}, 2*4096), 4096); err != nil {
				t.Fatal(err)
			}
		}, nil, "integrity"},
		{"a core migration newer than the binary", insertMigration("core"), nil, "core"},
		{"a module migration newer than the binary", insertMigration("cloudflare"), nil, "cloudflare"},
		{"a module the binary does not have", insertMigration("ghostmodule"), nil, "ghostmodule"},
		{"no canary", func(t *testing.T, s vfStaged) {
			vfReopen(t, s.path, func(db *store.DB) { vfExec(t, db, `DELETE FROM core_meta WHERE key = ?`, CanaryKey) })
		}, nil, "canary"},
		{"a canary sealed with another key", func(t *testing.T, s vfStaged) {
			vfReopen(t, s.path, func(db *store.DB) { vfSealCanary(t, db, otherSealer, vfCreatedAt) })
		}, nil, "canary"},
		{"a canary sealed for another place", func(t *testing.T, s vfStaged) {
			vfReopen(t, s.path, func(db *store.DB) {
				sealed, err := s.sealer.Seal("core_meta.value.other", []byte(`{"createdAt":"2026-11-03T08:15:00Z"}`))
				if err != nil {
					t.Fatal(err)
				}
				vfExec(t, db, `UPDATE core_meta SET value = ? WHERE key = ?`, base64.StdEncoding.EncodeToString(sealed), CanaryKey)
			})
		}, nil, "canary"},
		{"a key member that is not a key file", nil, func(vfStaged) []byte { return []byte("hello\n") }, "key"},
		{"a key member over the size limit", nil, func(s vfStaged) []byte {
			return append(bytes.Clone(s.keyFile[:len(s.keyFile)-1]), bytes.Repeat([]byte("A"), MaxKeyMember)...)
		}, "key"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := vfNewStaged(t)
			if c.damage != nil {
				c.damage(t, s)
			}
			key := s.keyFile
			if c.key != nil {
				key = c.key(s)
			}
			got, err := ValidateStaged(context.Background(), s.path, key)
			if err == nil {
				t.Fatal("ValidateStaged accepted it")
			}
			if !got.IsZero() {
				t.Errorf("created at = %s on a refusal, want the zero time", got)
			}
			if c.want != "" && !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not name %q", err, c.want)
			}
			if encoded := strings.TrimSpace(strings.TrimPrefix(string(s.keyFile), secret.KeyPrefix)); strings.Contains(err.Error(), encoded) {
				t.Errorf("error %q carries the key", err)
			}
		})
	}
}

func TestVfValidateStagedCreatesNothingForAMissingFile(t *testing.T) {
	s := vfNewStaged(t)
	missing := s.path + ".absent"
	if _, err := ValidateStaged(context.Background(), missing, s.keyFile); err == nil {
		t.Fatal("ValidateStaged accepted a database that does not exist")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ValidateStaged created %s (stat error %v)", missing, err)
	}
}

// vfInstalled builds a controller database as the restore's phase 6 leaves
// it: unfinished records marked by Mark with tx, in journal_mode=DELETE and
// closed. breakIt, when set, runs after Mark on the open database.
func vfInstalled(t *testing.T, tx string, breakIt func(t *testing.T, db *store.DB)) string {
	t.Helper()
	db := fxDB(t, "")
	fxInsertChange(t, db, fxChange{ID: "c-applying", HostID: "host-a", Action: "fixture.act", State: "applying", Owner: "engine"})
	fxInsertChange(t, db, fxChange{ID: "c-repair", HostID: "host-a", Action: "fixture.act", State: "needs_repair"})
	fxInsertChange(t, db, fxChange{ID: "c-done", HostID: "host-a", Action: "fixture.act", State: "completed"})
	fxInsertLock(t, db, "k.applying", "c-applying")
	fxInsertLock(t, db, "k.repair", "c-repair")
	fxInsertLock(t, db, "k.update", "01JUPDATE0000000000000000A")
	fxInsertRollout(t, db, "r1", "running", "host-a")
	fxInsertListener(t, db, "l1", "open")
	m := quarantine.Marker{Tx: tx, RestoredAt: fxNow, BackupCreatedAt: fxNow.Add(-time.Hour)}
	if err := Mark(context.Background(), db, m); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	if breakIt != nil {
		breakIt(t, db)
	}
	path := db.Path()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	vfDeleteJournal(t, path)
	return path
}

// vfDeleteJournal switches the closed database at path from WAL to the
// DELETE journal, which the installed database uses.
func vfDeleteJournal(t *testing.T, path string) {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := raw.QueryRow(`PRAGMA journal_mode=DELETE`).Scan(&mode); err != nil || mode != "delete" {
		t.Fatalf("journal_mode=DELETE answered %q, %v", mode, err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	vfNoSideFiles(t, path)
}

const vfTx = "01JTXVERIFY00000000000000A"

func TestVfVerifyInstalledAcceptsAMarkedDatabase(t *testing.T) {
	path := vfInstalled(t, vfTx, nil)
	if err := VerifyInstalled(context.Background(), path, vfTx); err != nil {
		t.Errorf("VerifyInstalled: %v", err)
	}
}

func TestVfVerifyInstalledRefusals(t *testing.T) {
	type refusal struct {
		name, tx string
		breakIt  func(t *testing.T, db *store.DB)
		want     string // the error names it
	}
	cases := []refusal{
		{"no marker", vfTx, func(t *testing.T, db *store.DB) {
			vfExec(t, db, `DELETE FROM core_meta WHERE key = ?`, quarantine.MetaKey)
		}, "restore_quarantine"},
		{"another transaction's marker", "01JTXOTHER0000000000000000", nil, vfTx},
		{"a marker that does not decode", vfTx, func(t *testing.T, db *store.DB) {
			vfExec(t, db, `UPDATE core_meta SET value = 'not json' WHERE key = ?`, quarantine.MetaKey)
		}, "restore_quarantine"},
		{"a running rollout", vfTx, func(t *testing.T, db *store.DB) {
			vfExec(t, db, `UPDATE core_rollouts SET state = 'running' WHERE id = 'r1'`)
		}, "r1"},
		{"a draining rollout", vfTx, func(t *testing.T, db *store.DB) {
			vfExec(t, db, `UPDATE core_rollouts SET state = 'draining', outcome = 'failed' WHERE id = 'r1'`)
		}, "r1"},
		{"a lock of an update reservation", vfTx, func(t *testing.T, db *store.DB) {
			fxInsertLock(t, db, "k.reservation", "01JUPDATE0000000000000000B")
		}, "k.reservation"},
		{"a lock of a finished change", vfTx, func(t *testing.T, db *store.DB) { fxInsertLock(t, db, "k.finished", "c-done") }, "k.finished"},
	}
	for _, state := range []string{"applying", "pending", "awaiting_confirmation", "undoing", "needs_repair", "recovering", "releasing"} {
		cases = append(cases, refusal{"a change left " + state, vfTx, func(t *testing.T, db *store.DB) {
			vfExec(t, db, `UPDATE core_changes SET state = ?, quarantined_from = NULL WHERE id = 'c-repair'`, state)
		}, "c-repair"})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := vfInstalled(t, c.tx, c.breakIt)
			err := VerifyInstalled(context.Background(), path, vfTx)
			if !errors.Is(err, ErrNotQuarantined) {
				t.Fatalf("VerifyInstalled = %v, want ErrNotQuarantined", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not name %q", err, c.want)
			}
		})
	}
}

func TestVfVerifyInstalledOpensReadOnly(t *testing.T) {
	path := vfInstalled(t, vfTx, nil)
	digest := func() [32]byte {
		b, err := os.ReadFile(path) //nolint:gosec // G304: a path under t.TempDir()
		if err != nil {
			t.Fatal(err)
		}
		return sha256.Sum256(b)
	}
	before := digest()
	if err := VerifyInstalled(context.Background(), path, vfTx); err != nil {
		t.Fatal(err)
	}
	vfNoSideFiles(t, path)
	if digest() != before {
		t.Error("VerifyInstalled changed the database file")
	}

	missing := path + ".absent"
	if err := VerifyInstalled(context.Background(), missing, vfTx); err == nil || errors.Is(err, ErrNotQuarantined) {
		t.Errorf("VerifyInstalled of a missing file = %v, want an open error", err)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("VerifyInstalled created %s (stat error %v)", missing, err)
	}
}
