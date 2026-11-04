package backup

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/oklog/ulid/v2"
)

// Recover processes every journal oldest first under h.lock(); with no journal it returns nil at once
// without taking the lock. It never checks the services (spec 10) and never uses h.Units, which is nil
// in production recovery. A journal in phase installed or verified is completed; any other is rolled
// back. The first journal that fails stops recovery and is kept, with every younger one.
func Recover(ctx context.Context, h Host) error {
	journals, err := ListJournals(h.Paths.ControllerDir)
	if err != nil {
		return fmt.Errorf("backup: list restore journals: %w", err)
	}
	if len(journals) == 0 {
		return nil
	}
	release, err := h.lock()
	if err != nil {
		return err
	}
	defer release()
	// The holder of the lock that was just released may have finished the
	// transactions listed before it.
	journals, err = ListJournals(h.Paths.ControllerDir)
	if err != nil {
		return fmt.Errorf("backup: list restore journals: %w", err)
	}
	for _, journalPath := range journals {
		if err := recoverOne(ctx, h, journalPath); err != nil {
			return err
		}
	}
	return nil
}

// recoverOne completes the transaction of the journal at journalPath once phase 6 has begun, and
// rolls it back otherwise.
func recoverOne(ctx context.Context, h Host, journalPath string) error {
	j, err := ReadJournal(journalPath)
	if err != nil {
		return fmt.Errorf("backup: read journal %s: %w", journalPath, err)
	}
	if err := checkJournal(h, journalPath, j); err != nil {
		return err
	}
	h.Log.Info("backup: recovering an unfinished restore", "tx", j.Tx, "phase", string(j.Phase))
	switch j.Phase {
	case PhaseInstalled, PhaseVerified:
		if err := installStaged(h, journalPath, &j); err != nil {
			return err
		}
		return finishTx(ctx, h, journalPath, &j)
	default:
		return rollbackTx(h, journalPath, &j)
	}
}

// checkJournal refuses a journal recovery must not act on: one whose tx is not a canonical ULID,
// whose name does not carry its tx, or whose files are not this controller's Targets in order. The
// controller service can write its own directory, so a journal there is not trusted to name the
// paths a root recovery renames or removes. A refused journal is left as it is.
func checkJournal(h Host, journalPath string, j Journal) error {
	refuse := func(reason string) error {
		return fmt.Errorf("backup: journal %s does not belong to this controller (%s); it is kept", journalPath, reason)
	}
	if id, err := ulid.ParseStrict(j.Tx); err != nil || id.String() != j.Tx {
		return refuse(fmt.Sprintf("tx %q is not an upper-case ULID", j.Tx))
	}
	if JournalPath(h.Paths.ControllerDir, j.Tx) != journalPath {
		return refuse("its name does not carry its tx " + j.Tx)
	}
	if len(j.Files) == 0 {
		return nil
	}
	targets := Targets(h.Paths)
	if len(j.Files) != len(targets) {
		return refuse(fmt.Sprintf("it lists %d files, not the %d controller files", len(j.Files), len(targets)))
	}
	for i, f := range j.Files {
		if f.Path != targets[i] {
			return refuse(fmt.Sprintf("file %d is %s, not %s", i+1, f.Path, targets[i]))
		}
	}
	return nil
}

// rollbackTx puts every original of j back under its own name, removes what the transaction
// created in place of an original that did not exist, and then removes the transaction's files, the
// journal last. Earlier restores' set-aside files are left alone.
func rollbackTx(h Host, journalPath string, j *Journal) error {
	if j.Phase != PhaseRollback {
		j.Phase = PhaseRollback
		if err := SaveJournal(h.Ops, journalPath, *j); err != nil {
			return err
		}
	}
	for _, f := range j.Files {
		if !f.Existed {
			if err := h.Ops.Remove(f.Path); err != nil {
				return fmt.Errorf("backup: remove %s: %w", f.Path, err)
			}
			if err := h.Ops.SyncDir(filepath.Dir(f.Path)); err != nil {
				return fmt.Errorf("backup: sync the directory of %s: %w", f.Path, err)
			}
			continue
		}
		aside := AsideName(f.Path, j.Tx)
		done, err := Settle(h.Ops, Rename{From: aside, To: f.Path})
		if err != nil {
			return fmt.Errorf("backup: journal %s: %s and %s: %w", journalPath, aside, f.Path, err)
		}
		if done {
			continue
		}
		if err := RenameJournaled(h.Ops, journalPath, j, PhaseRollback, aside, f.Path); err != nil {
			return err
		}
	}
	return removeTx(h, j.Tx)
}
