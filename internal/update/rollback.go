package update

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// active reports whether a record's operation may still be working on it.
func active(state string) bool {
	switch state {
	case core.UpdatePending, core.UpdateDownloading, core.UpdateStaged, core.UpdateSwapped, core.UpdateRollingBack:
		return true
	}
	return false
}

// runnerUnitOf is the transient unit that runs r.
func runnerUnitOf(r Record) string {
	if r.Kind == core.UpdateKindRollback {
		return RollbackRunnerUnit(r.ID)
	}
	return RunnerUnit(r.ID)
}

// recoveryLock takes the update lock for a recover path. A runner frozen
// while it holds the lock would block the deadline rollback, so after
// LockTimeout it kills the runner unit of every active record, which ends
// the flock with the process, and tries once more (spec section 8, Taking
// the update lock).
func (u *Updater) recoveryLock(ctx context.Context) (*Lock, error) {
	l, err := AcquireLock(ctx, u.cfg.Paths.UpdateLock(), u.lockTimeout)
	if !errors.Is(err, ErrLockTimeout) {
		return l, err
	}
	all, lerr := ListRecords(u.cfg.Paths)
	if lerr != nil {
		return nil, errors.Join(err, lerr)
	}
	for _, r := range all {
		if !active(r.State) {
			continue
		}
		unit := runnerUnitOf(r)
		u.cfg.Log.Warn("update: the update lock is held; stopping the runner", "unit", unit)
		if kerr := u.cfg.Systemd.Kill(ctx, unit, syscall.SIGKILL); kerr != nil {
			u.cfg.Log.Warn("update: kill the runner", "unit", unit, "error", kerr)
		}
		if serr := u.cfg.Systemd.Stop(ctx, unit); serr != nil {
			u.cfg.Log.Warn("update: stop the runner", "unit", unit, "error", serr)
		}
	}
	return AcquireLock(ctx, u.cfg.Paths.UpdateLock(), u.lockTimeout)
}

// RollBack is the one automatic rollback of a swapped record: under the
// update lock it claims swapped -> rolling-back, puts the previous binary
// back, records rolled-back, releases the reservation, stops the deadline
// timer, deletes darkpanel.recovery and, unless atBoot, restarts the
// services. A record already rolling-back is finished the same way; a record
// in any other state was handled by another caller, and RollBack returns nil.
func (u *Updater) RollBack(ctx context.Context, tx string, atBoot bool) error {
	l, err := u.recoveryLock(ctx)
	if err != nil {
		return err
	}
	defer u.unlock(l)
	return u.rollBackLocked(ctx, tx, atBoot, interruptedBy("the update was rolled back before it was committed"))
}

// interruptedBy is a record error with code update.interrupted.
func interruptedBy(msg string) *core.ErrorDetail {
	return &core.ErrorDetail{Code: string(errcode.UpdateInterrupted), Message: msg}
}

// rollBackLocked is RollBack with the update lock held. cause becomes the
// record's error unless the record already carries one.
func (u *Updater) rollBackLocked(ctx context.Context, tx string, atBoot bool, cause *core.ErrorDetail) error {
	r, err := ReadRecord(u.cfg.Paths, tx)
	if err != nil {
		return err
	}
	switch r.State {
	case core.UpdateSwapped:
		if r.Error == nil {
			r.Error = cause
		}
		if err := u.save(&r, core.UpdateRollingBack, r.Error); err != nil {
			return err
		}
	case core.UpdateRollingBack:
	default:
		return nil
	}
	if err := RestorePrevious(u.cfg.Paths, r.PrevSHA256, r.NewSHA256); err != nil {
		var coded *errcode.Error
		if !errors.As(err, &coded) {
			// The record stays rolling-back, so the next recover pass resumes.
			return err
		}
		u.cfg.Log.Error("update: the previous binary cannot be restored", "transaction", tx, "error", err)
		return errors.Join(err, u.failKeepingBinaries(ctx, &r, detail(err)))
	}
	// A crash between recording swapped and the swap leaves the staged binary.
	if err := removeIfExists(u.cfg.Paths.NewBinary(tx)); err != nil {
		return fmt.Errorf("update: remove the staged binary: %w", err)
	}
	return u.finish(ctx, &r, core.UpdateRolledBack, r.Error, !atBoot)
}

// finish records r terminal and ends its operation: the reservation is
// released, the deadline timer stopped and darkpanel.recovery deleted, and
// with restart the services restarted.
func (u *Updater) finish(ctx context.Context, r *Record, state string, cause *core.ErrorDetail, restart bool) error {
	if err := u.save(r, state, cause); err != nil {
		return err
	}
	var errs []error
	if err := u.release(ctx, r.OperationID); err != nil {
		errs = append(errs, err)
	}
	u.stopTimer(ctx, r.ID)
	if err := removeIfExists(u.cfg.Paths.RecoveryBinary()); err != nil {
		errs = append(errs, fmt.Errorf("update: remove the recovery binary: %w", err))
	}
	if restart {
		errs = append(errs, u.restartServices(ctx))
	}
	return errors.Join(errs...)
}

// failKeepingBinaries records r failed and ends its operation without
// touching BinaryDir: after a mismatch darkpanel.recovery may be the only
// copy left of the binary prevSha256 names, so it is kept for the operator.
func (u *Updater) failKeepingBinaries(ctx context.Context, r *Record, cause *core.ErrorDetail) error {
	if err := u.save(r, core.UpdateFailed, cause); err != nil {
		return err
	}
	err := u.release(ctx, r.OperationID)
	u.stopTimer(ctx, r.ID)
	return err
}

// stopTimer stops tx's deadline timer. A timer that already fired, or never
// existed, has nothing to stop.
func (u *Updater) stopTimer(ctx context.Context, tx string) {
	if err := u.cfg.Systemd.Stop(ctx, DeadlineTimer(tx)); err != nil {
		u.cfg.Log.Debug("update: stop the deadline timer", "timer", DeadlineTimer(tx), "error", err)
	}
}

// restartServices restarts the controller unit when the host has one, then
// the agent unit. A unit that hit its start limit refuses a plain start, so
// its failed state is reset first.
func (u *Updater) restartServices(ctx context.Context) error {
	var errs []error
	for _, unit := range []string{ControllerUnit, AgentUnit} {
		st, err := u.cfg.Systemd.State(ctx, unit)
		if err != nil {
			errs = append(errs, fmt.Errorf("update: read %s: %w", unit, err))
			continue
		}
		if st.LoadState == "not-found" {
			continue
		}
		if err := u.cfg.Systemd.ResetFailed(ctx, unit); err != nil {
			u.cfg.Log.Debug("update: reset the failed state", "unit", unit, "error", err)
		}
		if err := u.cfg.Systemd.Restart(ctx, unit); err != nil {
			errs = append(errs, fmt.Errorf("update: restart %s: %w", unit, err))
		}
	}
	return errors.Join(errs...)
}

// rollbackTarget applies the rollback guards: the newest operation is a
// committed update, darkpanel.prev is the binary it replaced, and, on the
// controller's host, no enrolled agent runs a newer version than that binary
// unless force. It returns the update's last record.
func (u *Updater) rollbackTarget(ctx context.Context, force bool) (Record, error) {
	all, err := ListRecords(u.cfg.Paths)
	if err != nil {
		return Record{}, err
	}
	if len(all) == 0 {
		return Record{}, errcode.New(errcode.UpdateNothingToRollBack, "no update has been installed on this host")
	}
	op := OperationRecords(all, all[len(all)-1].OperationID)
	last := op[len(op)-1]
	switch {
	case !Finished(op):
		return Record{}, errcode.New(errcode.UpdateInProgress, fmt.Sprintf("update operation %s is not finished", last.OperationID))
	case last.Kind != core.UpdateKindUpdate || last.State != core.UpdateCommitted:
		return Record{}, errcode.New(errcode.UpdateNothingToRollBack,
			fmt.Sprintf("the newest operation %s did not commit an update", last.OperationID))
	}
	prev, err := hashIfExists(u.cfg.Paths.PrevBinary())
	switch {
	case err != nil:
		return Record{}, err
	case prev == "":
		return Record{}, errcode.New(errcode.UpdateNothingToRollBack, "there is no previous binary to roll back to")
	case prev != last.PrevSHA256:
		return Record{}, errcode.New(errcode.UpdatePrevMismatch, "darkpanel.prev is not the binary the last update replaced")
	}
	if u.cfg.NewerAgents != nil && !force {
		hosts, err := u.cfg.NewerAgents(ctx, last.FromVersion)
		if err != nil {
			return Record{}, err
		}
		if len(hosts) > 0 {
			return Record{}, errcode.New(errcode.UpdateDowngradeRefused, fmt.Sprintf(
				"the agents of hosts %s run a version newer than %s; roll back with force to leave them unsupported",
				strings.Join(hosts, ", "), last.FromVersion))
		}
	}
	return last, nil
}

// CheckRollback applies the rollback guards without side effects: the
// rollback action's Plan.
func (u *Updater) CheckRollback(ctx context.Context, in core.UpdateRollbackInput) error {
	_, err := u.rollbackTarget(ctx, in.Force)
	return err
}

// newRollback applies the rollback guards, reserves every lock for a new
// rollback operation of the last update and returns its first record,
// pending. The caller holds the update lock.
func (u *Updater) newRollback(ctx context.Context, force bool, changeID string) (Record, error) {
	last, err := u.rollbackTarget(ctx, force)
	if err != nil {
		return Record{}, err
	}
	now, err := u.startTime()
	if err != nil {
		return Record{}, err
	}
	tx, err := u.reserve(ctx)
	if err != nil {
		return Record{}, err
	}
	return Record{
		ID: tx, OperationID: tx, Kind: core.UpdateKindRollback,
		FromVersion: last.ToVersion, ToVersion: last.FromVersion, TargetVersion: last.FromVersion,
		State: core.UpdatePending, StartedAt: now, UpdatedAt: now, ChangeID: changeID,
		Steps: []Release{}, PrevSHA256: last.PrevSHA256, NewSHA256: last.NewSHA256,
	}, nil
}

// StartRollback is core.update.rollback's Apply: the guards, the
// reservation, a rollback record pending, and the transient unit that runs
// it. Its result is observed through core.update.status, so the restart it
// causes never interrupts its own change.
func (u *Updater) StartRollback(ctx context.Context, changeID string, in core.UpdateRollbackInput) (core.UpdateTransaction, error) {
	l, err := u.lock(ctx)
	if err != nil {
		return core.UpdateTransaction{}, err
	}
	defer u.unlock(l)
	r, err := u.newRollback(ctx, in.Force, changeID)
	if err != nil {
		return core.UpdateTransaction{}, err
	}
	argv := []string{u.cfg.Paths.Binary(), "update", "rollback", "--direct", "--transaction", r.ID}
	if err := u.begin(ctx, r, RollbackRunnerUnit(r.ID), argv); err != nil {
		return core.UpdateTransaction{}, err
	}
	return r.Transaction(), nil
}

// RunRollback runs rollback record tx, as darkpanel update rollback --direct
// --transaction <tx> does: it links darkpanel.prev as darkpanel.recovery, so
// a crash mid-rename is finished by update recover like any other, claims
// rolling-back and performs an automatic rollback's renames and restarts.
// A rollback is final; there is no health window.
func (u *Updater) RunRollback(ctx context.Context, tx string) error {
	l, err := u.lock(ctx)
	if err != nil {
		return err
	}
	defer u.unlock(l)
	return u.runRollbackLocked(ctx, tx)
}

// runRollbackLocked is RunRollback with the update lock held.
func (u *Updater) runRollbackLocked(ctx context.Context, tx string) error {
	r, err := ReadRecord(u.cfg.Paths, tx)
	if err != nil {
		return err
	}
	if r.Kind != core.UpdateKindRollback {
		return fmt.Errorf("update: transaction %s is not a rollback", tx)
	}
	switch r.State {
	case core.UpdatePending:
	case core.UpdateRollingBack:
		return u.rollBackLocked(ctx, tx, false, nil)
	default:
		return nil
	}
	if err := u.linkRecovery(r); err != nil {
		return errors.Join(err, u.failKeepingBinaries(ctx, &r, detail(err)))
	}
	if err := u.save(&r, core.UpdateRollingBack, nil); err != nil {
		return err
	}
	return u.rollBackLocked(ctx, tx, false, nil)
}

// linkRecovery hard-links darkpanel.prev, which must still be the binary r
// restores, as darkpanel.recovery.
func (u *Updater) linkRecovery(r Record) error {
	p := u.cfg.Paths
	prev, err := hashIfExists(p.PrevBinary())
	switch {
	case err != nil:
		return err
	case prev == "":
		return errcode.New(errcode.UpdateNothingToRollBack, "there is no previous binary to roll back to")
	case prev != r.PrevSHA256:
		return errcode.New(errcode.UpdatePrevMismatch, "darkpanel.prev is not the binary the last update replaced")
	}
	return linkRecoveryBinary(p)
}

// DirectRollback is darkpanel update rollback --direct without a
// transaction, the break-glass path when no agent answers: the guards, a
// rollback record and RunRollback, all in this process. The update lock is
// held throughout: a recover pass in between would find a pending rollback
// record with no runner unit and mark it interrupted.
func (u *Updater) DirectRollback(ctx context.Context, force bool) (core.UpdateTransaction, error) {
	l, err := u.lock(ctx)
	if err != nil {
		return core.UpdateTransaction{}, err
	}
	defer u.unlock(l)
	r, err := u.newRollback(ctx, force, "")
	if err != nil {
		return core.UpdateTransaction{}, err
	}
	if err := WriteRecord(u.cfg.Paths, r); err != nil {
		return core.UpdateTransaction{}, errors.Join(err, u.release(ctx, r.OperationID))
	}
	if err := u.runRollbackLocked(ctx, r.ID); err != nil {
		return core.UpdateTransaction{}, err
	}
	done, err := ReadRecord(u.cfg.Paths, r.ID)
	if err != nil {
		return core.UpdateTransaction{}, err
	}
	return done.Transaction(), nil
}
