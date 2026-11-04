package backup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/controller/quarantine"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules"
)

// RestoreResult is what a completed restore reports.
type RestoreResult struct {
	Tx              string    // the restore transaction, which the installed quarantine marker carries
	BackupCreatedAt time.Time // when the backup was taken, from its canary
}

// Restore runs spec 10's phases 1-8 under h.lock() after h.checkRestore: it stages the backup read
// from in beside the installation, validates and quarantines the staged database, sets the
// originals aside, installs the staged database and key, verifies the installed database and
// removes the transaction's files and every earlier restore's set-aside files. A failure before the
// originals begin to move leaves the installation as it was; a later one keeps the journal for
// recovery. The passphrase is cleared before Restore returns.
func Restore(ctx context.Context, h Host, in io.Reader, passphrase []byte) (RestoreResult, error) {
	defer clear(passphrase)
	if err := h.checkRestore(ctx); err != nil {
		return RestoreResult{}, err
	}
	if _, _, err := h.Owner(); err != nil {
		return RestoreResult{}, fmt.Errorf("backup: look up the owner of the controller files: %w", err)
	}
	release, err := h.lock()
	if err != nil {
		return RestoreResult{}, err
	}
	defer release()
	// checkRestore releases the lock before returning, so another restore may
	// have left a journal before this one took the lock again.
	journals, err := pendingJournals(h.Paths.ControllerDir)
	if err != nil {
		return RestoreResult{}, err
	}
	if len(journals) > 0 {
		return RestoreResult{}, fmt.Errorf("backup: an unfinished restore exists (%s); start the controller or run `darkpanel controller restore --recover` first",
			journals[0])
	}

	tx := h.NewTx()
	journalPath := JournalPath(h.Paths.ControllerDir, tx)
	j := Journal{Format: 1, Tx: tx, Phase: PhaseBegun}
	var key []byte
	defer func() { clear(key) }()
	createdAt, err := restorePrepare(ctx, h, journalPath, &j, in, passphrase, &key)
	if err != nil {
		return RestoreResult{}, errors.Join(err, removeTx(h, tx))
	}
	if err := restoreSwap(ctx, h, journalPath, &j); err != nil {
		return RestoreResult{}, fmt.Errorf("backup: restore %s stopped after it began to move the installation aside; run darkpanel controller restore --recover: %w",
			tx, err)
	}
	return RestoreResult{Tx: tx, BackupCreatedAt: createdAt}, nil
}

// restorePrepare runs the phases that leave the installation untouched, from the first journal
// through the save of PhaseMovedAside. *key receives the key member's bytes for the caller to clear.
func restorePrepare(ctx context.Context, h Host, journalPath string, j *Journal, in io.Reader, passphrase []byte, key *[]byte) (time.Time, error) {
	if err := SaveJournal(h.Ops, journalPath, *j); err != nil {
		return time.Time{}, err
	}

	stagedDB := StagedName(h.Paths.ControllerDB(), j.Tx)
	stagedKey := StagedName(h.Paths.ControllerKey, j.Tx)
	sink := func(name string, size int64, body io.Reader) error {
		if name == MemberDB {
			return restoreWriteFile(h.Ops, stagedDB, body)
		}
		if size > MaxKeyMember {
			return fmt.Errorf("backup: the key member is over %d bytes", MaxKeyMember)
		}
		*key = make([]byte, size)
		if _, err := io.ReadFull(body, *key); err != nil {
			return fmt.Errorf("backup: read the key member: %w", err)
		}
		return restoreWriteFile(h.Ops, stagedKey, bytes.NewReader(*key))
	}
	if _, err := Read(in, passphrase, sink); err != nil {
		return time.Time{}, err
	}
	for _, dir := range []string{filepath.Dir(stagedDB), filepath.Dir(stagedKey)} {
		if err := h.Ops.SyncDir(dir); err != nil {
			return time.Time{}, fmt.Errorf("backup: sync the staging directory %s: %w", dir, err)
		}
	}
	j.Phase = PhaseStaged
	if err := SaveJournal(h.Ops, journalPath, *j); err != nil {
		return time.Time{}, err
	}

	createdAt, err := ValidateStaged(ctx, stagedDB, *key)
	if err != nil {
		return time.Time{}, err
	}
	j.Phase = PhaseValidated
	if err := SaveJournal(h.Ops, journalPath, *j); err != nil {
		return time.Time{}, err
	}

	if err := restoreQuarantine(ctx, h, j.Tx, createdAt); err != nil {
		return time.Time{}, err
	}
	j.Phase = PhaseQuarantined
	if err := SaveJournal(h.Ops, journalPath, *j); err != nil {
		return time.Time{}, err
	}

	for _, target := range Targets(h.Paths) {
		existed, err := h.Ops.Exists(target)
		if err != nil {
			return time.Time{}, fmt.Errorf("backup: look up %s: %w", target, err)
		}
		j.Files = append(j.Files, JournalFile{Path: target, Existed: existed})
	}
	j.Phase = PhaseJournaled
	if err := SaveJournal(h.Ops, journalPath, *j); err != nil {
		return time.Time{}, err
	}

	j.Phase = PhaseMovedAside
	if err := SaveJournal(h.Ops, journalPath, *j); err != nil {
		return time.Time{}, err
	}
	return createdAt, nil
}

// restoreSwap runs the phases from the first set-aside rename on: phases 5 to 8.
func restoreSwap(ctx context.Context, h Host, journalPath string, j *Journal) error {
	for _, f := range j.Files {
		if !f.Existed {
			continue
		}
		if err := RenameJournaled(h.Ops, journalPath, j, PhaseMovedAside, f.Path, AsideName(f.Path, j.Tx)); err != nil {
			return err
		}
	}
	j.Phase = PhaseInstalled
	if err := SaveJournal(h.Ops, journalPath, *j); err != nil {
		return err
	}
	if err := installStaged(h, journalPath, j); err != nil {
		return err
	}
	return finishTx(ctx, h, journalPath, j)
}

// restoreWriteFile creates path through ops with mode 0600, copies body to it, fsyncs and closes
// it; the file is closed on every path.
func restoreWriteFile(ops Ops, path string, body io.Reader) error {
	f, err := ops.Create(path, 0o600)
	if err != nil {
		return fmt.Errorf("backup: create %s: %w", path, err)
	}
	if _, err := io.Copy(f, body); err != nil {
		_ = f.Close()
		return fmt.Errorf("backup: write %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("backup: sync %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("backup: close %s: %w", path, err)
	}
	return nil
}

// restoreQuarantine is phase 3: it migrates the staged database to this binary's schema, marks it
// quarantined, takes it out of WAL mode and makes it durable.
func restoreQuarantine(ctx context.Context, h Host, tx string, createdAt time.Time) error {
	path := StagedName(h.Paths.ControllerDB(), tx)
	db, err := store.Open(ctx, path)
	if err != nil {
		return fmt.Errorf("backup: staged database: %w", err)
	}
	open := true
	defer func() {
		if open {
			_ = db.Close()
		}
	}()

	// store.Migrate copies a database from another binary version with
	// VACUUM INTO under BackupRoot before migrating it. The backup file
	// already is the pre-migration copy, and a copy would leave a directory
	// no rollback removes, so the staged database is labelled with this
	// binary's version first.
	if err := db.Write(ctx, func(sqlTx *sql.Tx) error {
		_, err := sqlTx.ExecContext(ctx,
			`INSERT INTO core_meta (key, value) VALUES ('binary_version', ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
			buildinfo.Version)
		return err
	}); err != nil {
		return fmt.Errorf("backup: staged database: write binary_version: %w", err)
	}
	coreSet, err := store.CoreMigrations(store.SideController)
	if err != nil {
		return fmt.Errorf("backup: staged database: %w", err)
	}
	var sets []store.MigrationSet
	for _, m := range modules.All() {
		man := m.Manifest()
		if fsys, ok := man.Migrations[core.SideController]; ok && fsys != nil {
			sets = append(sets, store.MigrationSet{Module: man.ID, FS: fsys})
		}
	}
	res, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideController, BinaryVersion: buildinfo.Version,
		BackupRoot: filepath.Join(h.Paths.ControllerDir, "restore-"+tx+".nobackup"), Keep: 1, Now: h.Clock.Now(),
	}, coreSet, sets)
	if err != nil {
		return fmt.Errorf("backup: staged database: migrate: %w", err)
	}
	for _, f := range res.Failed {
		// The module's error may quote SQL, so only the module is named.
		h.Log.Warn("backup: module controller migrations failed on the restored database", "module", f.Module)
	}
	if err := Mark(ctx, db, quarantine.Marker{Tx: tx, RestoredAt: h.Clock.Now().UTC(), BackupCreatedAt: createdAt}); err != nil {
		return err
	}

	// SQLite refuses to leave WAL mode while another connection is open, so
	// the reader pool is closed before the writer switches the journal mode.
	if err := db.Read().Close(); err != nil {
		return fmt.Errorf("backup: staged database: close the readers: %w", err)
	}
	if _, err := db.Writer().ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("backup: staged database: checkpoint: %w", err)
	}
	var mode string
	if err := db.Writer().QueryRowContext(ctx, `PRAGMA journal_mode=DELETE`).Scan(&mode); err != nil {
		return fmt.Errorf("backup: staged database: leave WAL mode: %w", err)
	}
	if mode != "delete" {
		return fmt.Errorf("backup: staged database: journal_mode is %s after asking for delete", mode)
	}
	open = false
	if err := db.Close(); err != nil {
		return fmt.Errorf("backup: staged database: close: %w", err)
	}

	for _, name := range []string{path + "-wal", path + "-shm"} {
		if err := h.Ops.Remove(name); err != nil {
			return fmt.Errorf("backup: remove %s: %w", name, err)
		}
	}
	// Ops cannot reopen an existing file, so the staged database is fsynced
	// directly; SQLite's synchronous=FULL already made each commit durable.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0) //nolint:gosec // G304: the path is the staged database under paths.Paths
	if err != nil {
		return fmt.Errorf("backup: staged database: open for fsync: %w", err)
	}
	errSync := f.Sync()
	if err := errors.Join(errSync, f.Close()); err != nil {
		return fmt.Errorf("backup: staged database: fsync: %w", err)
	}
	if err := h.Ops.SyncDir(h.Paths.ControllerDir); err != nil {
		return fmt.Errorf("backup: sync %s: %w", h.Paths.ControllerDir, err)
	}
	return nil
}

// removeTx applies the rollback rule to tx: it removes the staged database, its -wal and -shm, the
// staged key, the journal's .new and last the journal, each followed by an fsync of its directory.
func removeTx(h Host, tx string) error {
	stagedDB := StagedName(h.Paths.ControllerDB(), tx)
	journal := JournalPath(h.Paths.ControllerDir, tx)
	for _, name := range []string{
		stagedDB, stagedDB + "-wal", stagedDB + "-shm", StagedName(h.Paths.ControllerKey, tx), journal + ".new", journal,
	} {
		if err := h.Ops.Remove(name); err != nil {
			return fmt.Errorf("backup: remove %s: %w", name, err)
		}
		if err := h.Ops.SyncDir(filepath.Dir(name)); err != nil {
			return fmt.Errorf("backup: sync the directory of %s: %w", name, err)
		}
	}
	return nil
}

// installStaged is phase 6 and safe to repeat: for the staged database, then the staged key, it skips
// a rename Settle reports done, and otherwise chowns the staged file to h.Owner(), chmods it 0600 and
// calls RenameJournaled(h.Ops, journalPath, j, PhaseInstalled, staged, installed).
func installStaged(h Host, journalPath string, j *Journal) error {
	uid, gid, err := h.Owner()
	if err != nil {
		return fmt.Errorf("backup: look up the owner of the controller files: %w", err)
	}
	for _, to := range []string{h.Paths.ControllerDB(), h.Paths.ControllerKey} {
		from := StagedName(to, j.Tx)
		done, err := Settle(h.Ops, Rename{From: from, To: to})
		if err != nil {
			return fmt.Errorf("backup: journal %s: %s and %s: %w", journalPath, from, to, err)
		}
		if done {
			continue
		}
		// Chmod follows a symbolic link and lchown on a hard link changes the
		// linked inode, so a file planted under the staged name could hand a
		// root recovery any file on the host. os.Lstat is called directly so
		// the check adds no Ops call.
		fi, err := os.Lstat(from)
		if err != nil {
			return fmt.Errorf("backup: look up the staged file %s: %w", from, err)
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); !fi.Mode().IsRegular() || !ok || st.Nlink != 1 {
			return fmt.Errorf("backup: staged file %s is not a single regular file; the journal %s is kept", from, journalPath)
		}
		// The owner and mode are set before the rename, so the installed name
		// never carries the wrong ones, even after a crash.
		if err := h.Ops.Chown(from, uid, gid); err != nil {
			return fmt.Errorf("backup: chown %s: %w", from, err)
		}
		if err := h.Ops.Chmod(from, 0o600); err != nil {
			return fmt.Errorf("backup: chmod %s: %w", from, err)
		}
		if err := RenameJournaled(h.Ops, journalPath, j, PhaseInstalled, from, to); err != nil {
			return err
		}
	}
	return nil
}

// finishTx is phases 7 and 8: VerifyInstalled (a failure keeps the journal), save PhaseVerified,
// removeTx, then dropOldAside.
func finishTx(ctx context.Context, h Host, journalPath string, j *Journal) error {
	if err := VerifyInstalled(ctx, h.Paths.ControllerDB(), j.Tx); err != nil {
		return fmt.Errorf("backup: restore %s: the installed database failed verification and the journal %s is kept: %w",
			j.Tx, journalPath, err)
	}
	j.Phase = PhaseVerified
	if err := SaveJournal(h.Ops, journalPath, *j); err != nil {
		return err
	}
	if err := removeTx(h, j.Tx); err != nil {
		return err
	}
	return dropOldAside(h, j.Tx)
}

// dropOldAside removes every <target>.pre-<tx'> of Targets with tx' an upper-case ULID other than tx,
// and fsyncs each directory that lost a file. Any other .pre- name, such as an operator's own copy,
// is not a restore's and stays.
func dropOldAside(h Host, tx string) error {
	for _, target := range Targets(h.Paths) {
		dir := filepath.Dir(target)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("backup: list %s: %w", dir, err)
		}
		prefix := filepath.Base(target) + ".pre-"
		removed := false
		for _, e := range entries {
			other, ok := strings.CutPrefix(e.Name(), prefix)
			if !ok || other == tx || !canonicalTx(other) {
				continue
			}
			name := filepath.Join(dir, e.Name())
			if err := h.Ops.Remove(name); err != nil {
				return fmt.Errorf("backup: remove %s: %w", name, err)
			}
			removed = true
		}
		if removed {
			if err := h.Ops.SyncDir(dir); err != nil {
				return fmt.Errorf("backup: sync %s: %w", dir, err)
			}
		}
	}
	return nil
}
