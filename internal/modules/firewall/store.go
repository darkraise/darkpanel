package firewall

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

//go:embed migrations/agent/*.sql
var migrationsFS embed.FS

// agentMigrations returns the module's agent-side migrations, the value of
// Manifest.Migrations[core.SideAgent].
func agentMigrations() fs.FS {
	sub, err := fs.Sub(migrationsFS, "migrations/agent")
	if err != nil {
		// fs.Sub fails only for an invalid path, and this one is a constant.
		panic(err)
	}
	return sub
}

// snapshotTimeFormat is the fixed-width form of every stored time, so times
// compare as text.
const snapshotTimeFormat = "2006-01-02T15:04:05.000000000Z"

// Retention (spec 2.5): the newest unpinned snapshots kept per kind.
const (
	keepSnapshots       = 50
	keepSystemSnapshots = 10
)

// errSnapshotNotFound is wrapped by every store function that finds no
// snapshot to act on.
var errSnapshotNotFound = errors.New("firewall: snapshot not found")

// snapshot is one firewall_snapshots row.
type snapshot struct {
	ID         string
	CreatedAt  time.Time
	ChangeID   string // "" until a change attaches it
	Actor      string
	System     bool // the change is a background task's (core_changes.system)
	Pinned     bool // never pruned; set while the change needs repair
	Baseline   bool // the host's first snapshot, never pruned
	Entries    []core.RestoreEntry
	State      snapshotState
	Hashes     map[string]string // fileHash of every entry at capture, by path
	PostHashes map[string]string // fileHash of every entry right after the change applied; nil until recorded
}

// snapshotState is a snapshot's logical state, the state column (spec 2.1,
// 2.3, 2.5).
type snapshotState struct {
	Enabled bool               `json:"enabled"`               // ufw.conf ENABLED at capture
	Sysctl  map[string]string  `json:"sysctl"`                // every supported key's live value at capture, in /proc/sys form
	Guard   *core.GuardContext `json:"guard,omitempty"`       // the guard context of the change's plan, for the connectivity exception
	Image   *beforeImage       `json:"beforeImage,omitempty"` // a repair's loaded-state before-image
	Docker  bool               `json:"docker"`                // the Docker integration flag; false until the integration exists
}

// snapshotFilter narrows listSnapshots.
type snapshotFilter struct {
	System *bool // only system (true) or only non-system (false) snapshots; nil for both
	Limit  int   // at most this many rows; 0 for all
}

// fileHash is the comparable form of a state file: "sha256:<hex>" of its
// bytes, or "absent".
func fileHash(e core.RestoreEntry) string {
	if !e.Present {
		return "absent"
	}
	sum := sha256.Sum256(e.Data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// sameFile reports whether two entries of a state file are the same file:
// the same fileHash and, when present, the same mode and owner, which
// fileHash leaves out so that the stored hashes stay comparable.
func sameFile(a, b core.RestoreEntry) bool {
	return fileHash(a) == fileHash(b) && (!a.Present || (a.Mode == b.Mode && a.UID == b.UID && a.GID == b.GID))
}

func formatSnapshotTime(t time.Time) string { return t.UTC().Format(snapshotTimeFormat) }

// saveSnapshot inserts s, makes it the baseline when the host has none yet,
// and prunes by retention, in one transaction. It returns s as stored.
func saveSnapshot(ctx context.Context, db *store.DB, s snapshot) (snapshot, error) {
	entries, err := json.Marshal(s.Entries)
	if err != nil {
		return s, fmt.Errorf("firewall: encode snapshot entries: %w", err)
	}
	state, err := json.Marshal(s.State)
	if err != nil {
		return s, fmt.Errorf("firewall: encode snapshot state: %w", err)
	}
	hashes, err := json.Marshal(s.Hashes)
	if err != nil {
		return s, fmt.Errorf("firewall: encode snapshot hashes: %w", err)
	}
	var post any
	if s.PostHashes != nil {
		b, err := json.Marshal(s.PostHashes)
		if err != nil {
			return s, fmt.Errorf("firewall: encode snapshot post hashes: %w", err)
		}
		post = string(b)
	}
	var change any
	if s.ChangeID != "" {
		change = s.ChangeID
	}
	err = db.Write(ctx, func(tx *sql.Tx) error {
		var baselines int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM firewall_snapshots WHERE baseline = 1").Scan(&baselines); err != nil {
			return err
		}
		s.Baseline = baselines == 0
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO firewall_snapshots (id, created_at, change_id, actor, system, pinned, baseline, entries, state,
			 hashes, post_hashes)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			s.ID, formatSnapshotTime(s.CreatedAt), change, s.Actor, s.System, s.Pinned, s.Baseline, string(entries),
			string(state), string(hashes), post); err != nil {
			return err
		}
		_, err := pruneSnapshots(ctx, tx)
		return err
	})
	if err != nil {
		return s, fmt.Errorf("firewall: save snapshot %s: %w", s.ID, err)
	}
	return s, nil
}

// pruneSnapshots deletes the snapshots retention no longer keeps (spec 2.5)
// and returns how many. It keeps the newest keepSnapshots unpinned
// snapshots of non-system changes and the newest keepSystemSnapshots of
// system changes, every pinned or baseline snapshot, every snapshot whose
// change is pending, applying or needs_repair, and the snapshot of a
// restore in progress. An unattached snapshot counts as a non-system
// change's unless its actor made it system.
func pruneSnapshots(ctx context.Context, tx *sql.Tx) (int64, error) {
	res, err := tx.ExecContext(ctx,
		`DELETE FROM firewall_snapshots
		 WHERE pinned = 0 AND baseline = 0
		   AND id NOT IN (SELECT snapshot_id FROM firewall_restores)
		   AND (change_id IS NULL OR change_id NOT IN
		        (SELECT id FROM core_changes WHERE state IN ('pending', 'applying', 'needs_repair')))
		   AND id NOT IN (SELECT id FROM firewall_snapshots WHERE pinned = 0 AND baseline = 0 AND system = 0
		                  ORDER BY created_at DESC, id DESC LIMIT ?)
		   AND id NOT IN (SELECT id FROM firewall_snapshots WHERE pinned = 0 AND baseline = 0 AND system = 1
		                  ORDER BY created_at DESC, id DESC LIMIT ?)`,
		keepSnapshots, keepSystemSnapshots)
	if err != nil {
		return 0, fmt.Errorf("firewall: prune snapshots: %w", err)
	}
	return res.RowsAffected()
}

const snapshotColumns = "id, created_at, change_id, actor, system, pinned, baseline, state, hashes, post_hashes"

type rowScanner interface{ Scan(dest ...any) error }

// scanSnapshot reads snapshotColumns, then entries when withEntries.
func scanSnapshot(row rowScanner, withEntries bool) (snapshot, error) {
	var s snapshot
	var created, state, hashes string
	var change, post, entries sql.NullString
	dest := []any{&s.ID, &created, &change, &s.Actor, &s.System, &s.Pinned, &s.Baseline, &state, &hashes, &post}
	if withEntries {
		dest = append(dest, &entries)
	}
	if err := row.Scan(dest...); err != nil {
		return s, err
	}
	t, err := time.Parse(snapshotTimeFormat, created)
	if err != nil {
		return s, fmt.Errorf("firewall: snapshot %s: created_at %q: %w", s.ID, created, err)
	}
	s.CreatedAt, s.ChangeID = t, change.String
	if err := json.Unmarshal([]byte(state), &s.State); err != nil {
		return s, fmt.Errorf("firewall: snapshot %s: state: %w", s.ID, err)
	}
	if err := json.Unmarshal([]byte(hashes), &s.Hashes); err != nil {
		return s, fmt.Errorf("firewall: snapshot %s: hashes: %w", s.ID, err)
	}
	if post.Valid {
		if err := json.Unmarshal([]byte(post.String), &s.PostHashes); err != nil {
			return s, fmt.Errorf("firewall: snapshot %s: post hashes: %w", s.ID, err)
		}
	}
	if withEntries {
		if err := json.Unmarshal([]byte(entries.String), &s.Entries); err != nil {
			return s, fmt.Errorf("firewall: snapshot %s: entries: %w", s.ID, err)
		}
	}
	return s, nil
}

// getSnapshot returns the snapshot id with its entries.
func getSnapshot(ctx context.Context, db *store.DB, id string) (snapshot, error) {
	s, err := scanSnapshot(db.Read().QueryRowContext(ctx,
		"SELECT "+snapshotColumns+", entries FROM firewall_snapshots WHERE id = ?", id), true)
	if errors.Is(err, sql.ErrNoRows) {
		return s, fmt.Errorf("%w: %s", errSnapshotNotFound, id)
	}
	if err != nil {
		return s, fmt.Errorf("firewall: read snapshot %s: %w", id, err)
	}
	return s, nil
}

// listSnapshots returns snapshots newest first, without their entries.
func listSnapshots(ctx context.Context, db *store.DB, f snapshotFilter) ([]snapshot, error) {
	q := "SELECT " + snapshotColumns + " FROM firewall_snapshots"
	var args []any
	if f.System != nil {
		q += " WHERE system = ?"
		args = append(args, *f.System)
	}
	q += " ORDER BY created_at DESC, id DESC"
	if f.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limit)
	}
	rows, err := db.Read().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("firewall: list snapshots: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []snapshot
	for rows.Next() {
		s, err := scanSnapshot(rows, false)
		if err != nil {
			return nil, fmt.Errorf("firewall: list snapshots: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("firewall: list snapshots: %w", err)
	}
	return out, nil
}

// attachTx records changeID on snapshot id and copies the change's system
// flag when the change row exists. Attaching the same change again is a
// no-op; a snapshot attached to another change is refused.
func attachTx(ctx context.Context, tx *sql.Tx, id, changeID string) error {
	res, err := tx.ExecContext(ctx,
		`UPDATE firewall_snapshots
		 SET change_id = ?, system = coalesce((SELECT system FROM core_changes WHERE id = ?), system)
		 WHERE id = ? AND (change_id IS NULL OR change_id = ?)`,
		changeID, changeID, id, changeID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 1 {
		return err
	}
	var other sql.NullString
	err = tx.QueryRowContext(ctx, "SELECT change_id FROM firewall_snapshots WHERE id = ?", id).Scan(&other)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%w: %s", errSnapshotNotFound, id)
	case err != nil:
		return err
	}
	return fmt.Errorf("firewall: snapshot %s belongs to change %s, not %s", id, other.String, changeID)
}

// attachSnapshot records changeID on snapshot id (attachTx).
func attachSnapshot(ctx context.Context, db *store.DB, id, changeID string) error {
	if err := db.Write(ctx, func(tx *sql.Tx) error { return attachTx(ctx, tx, id, changeID) }); err != nil {
		return fmt.Errorf("firewall: attach snapshot %s: %w", id, err)
	}
	return nil
}

// attachNewest attaches changeID to the snapshot captured for it and
// returns that snapshot's id: the one already attached to changeID, else
// the newest unattached one. The engine captures a revertible change's
// snapshot and persists the change under one lock, and the change holds the
// firewall lock from then on, so no other firewall snapshot can be captured
// between them; older unattached snapshots belong to applies that never
// started.
func attachNewest(ctx context.Context, db *store.DB, changeID string) (string, error) {
	var id string
	err := db.Write(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, "SELECT id FROM firewall_snapshots WHERE change_id = ?", changeID).Scan(&id)
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		err = tx.QueryRowContext(ctx,
			"SELECT id FROM firewall_snapshots WHERE change_id IS NULL ORDER BY rowid DESC LIMIT 1").Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: none is unattached", errSnapshotNotFound)
		}
		if err != nil {
			return err
		}
		return attachTx(ctx, tx, id, changeID)
	})
	if err != nil {
		return "", fmt.Errorf("firewall: attach the snapshot of change %s: %w", changeID, err)
	}
	return id, nil
}

// snapshotOfChange returns the id of the snapshot attached to changeID.
func snapshotOfChange(ctx context.Context, db *store.DB, changeID string) (string, error) {
	var id string
	err := db.Read().QueryRowContext(ctx, "SELECT id FROM firewall_snapshots WHERE change_id = ?", changeID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: change %s has none", errSnapshotNotFound, changeID)
	}
	if err != nil {
		return "", fmt.Errorf("firewall: read the snapshot of change %s: %w", changeID, err)
	}
	return id, nil
}

// updateSnapshot runs query, an UPDATE of one snapshot whose last
// parameter is the id, and fails when no row matched.
func updateSnapshot(ctx context.Context, db *store.DB, id, query string, args ...any) error {
	return db.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, query, append(args, id)...)
		if err != nil {
			return fmt.Errorf("firewall: update snapshot %s: %w", id, err)
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return errors.Join(err, fmt.Errorf("%w: %s", errSnapshotNotFound, id))
		}
		return nil
	})
}

// setPostHashes records the hash of every state file right after the
// change applied (spec 2.5).
func setPostHashes(ctx context.Context, db *store.DB, id string, hashes map[string]string) error {
	b, err := json.Marshal(hashes)
	if err != nil {
		return fmt.Errorf("firewall: encode post hashes: %w", err)
	}
	return updateSnapshot(ctx, db, id, "UPDATE firewall_snapshots SET post_hashes = ? WHERE id = ?", string(b))
}

// setPinned pins or unpins snapshot id. A pinned snapshot is never pruned;
// the baseline flag is separate, so unpinning never exposes the baseline.
func setPinned(ctx context.Context, db *store.DB, id string, pinned bool) error {
	return updateSnapshot(ctx, db, id, "UPDATE firewall_snapshots SET pinned = ? WHERE id = ?", pinned)
}

// restoreRecord is the record of an unfinished restore: the UFW transition
// it chose and, for an emergency restore (the connectivity exception), the
// record of what it overwrites, which finishes the restore as an override.
type restoreRecord struct {
	Transition fwstate.Transition
	Emergency  json.RawMessage // nil for a normal restore
}

// beginRestore records a normal restore of snapshot id with transition tr,
// in place of any unfinished restore.
func beginRestore(ctx context.Context, db *store.DB, id string, tr fwstate.Transition, now time.Time) error {
	return beginRestoreRecord(ctx, db, id, restoreRecord{Transition: tr}, now)
}

// beginRestoreRecord records a restore of snapshot id, in place of any
// unfinished restore.
func beginRestoreRecord(ctx context.Context, db *store.DB, id string, rec restoreRecord, now time.Time) error {
	kind, override := "normal", any(nil)
	if rec.Emergency != nil {
		kind, override = "emergency", string(rec.Emergency)
	}
	err := db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM firewall_restores"); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO firewall_restores (snapshot_id, transition, started_at, kind, override) VALUES (?, ?, ?, ?, ?)",
			id, string(rec.Transition), formatSnapshotTime(now), kind, override)
		return err
	})
	if err != nil {
		return fmt.Errorf("firewall: record the restore of snapshot %s: %w", id, err)
	}
	return nil
}

// markEmergency turns the unfinished restore of snapshot id into an
// emergency restore with the given record, keeping its transition. It fails
// when there is no such restore.
func markEmergency(ctx context.Context, db *store.DB, id string, emergency json.RawMessage) error {
	err := db.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "UPDATE firewall_restores SET kind = 'emergency', override = ? WHERE snapshot_id = ?", string(emergency), id)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return errors.New("no restore is recorded")
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("firewall: record the emergency restore of snapshot %s: %w", id, err)
	}
	return nil
}

// restoreRecordOf returns the record of an unfinished restore of snapshot
// id; ok is false when none is recorded.
func restoreRecordOf(ctx context.Context, db *store.DB, id string) (rec restoreRecord, ok bool, err error) {
	var tr string
	var override sql.NullString
	err = db.Read().QueryRowContext(ctx, "SELECT transition, override FROM firewall_restores WHERE snapshot_id = ?", id).Scan(&tr, &override)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return restoreRecord{}, false, nil
	case err != nil:
		return restoreRecord{}, false, fmt.Errorf("firewall: read the restore of snapshot %s: %w", id, err)
	}
	rec = restoreRecord{Transition: fwstate.Transition(tr)}
	if override.Valid {
		rec.Emergency = json.RawMessage(override.String)
	}
	return rec, true, nil
}

// restoreIntent returns the transition of an unfinished restore of snapshot
// id; ok is false when none is recorded.
func restoreIntent(ctx context.Context, db *store.DB, id string) (fwstate.Transition, bool, error) {
	rec, ok, err := restoreRecordOf(ctx, db, id)
	return rec.Transition, ok, err
}

// pendingRestore returns the snapshot of the unfinished restore, if any.
func pendingRestore(ctx context.Context, db *store.DB) (string, bool, error) {
	var id string
	err := db.Read().QueryRowContext(ctx, "SELECT snapshot_id FROM firewall_restores").Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("firewall: read the unfinished restore: %w", err)
	}
	return id, true, nil
}

// endRestore deletes the restore record of snapshot id.
func endRestore(ctx context.Context, db *store.DB, id string) error {
	err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM firewall_restores WHERE snapshot_id = ?", id)
		return err
	})
	if err != nil {
		return fmt.Errorf("firewall: clear the restore of snapshot %s: %w", id, err)
	}
	return nil
}
