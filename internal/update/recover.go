package update

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/platform"
)

// RecoverMode says which caller runs Recover.
type RecoverMode int

const (
	// RecoverStart is the agent daemon's start: no rollback, no restarts.
	RecoverStart RecoverMode = iota
	// RecoverBoot is darkpanel-update-recover.service, ordered before the
	// services: rollbacks rename and record but restart nothing.
	RecoverBoot
	// RecoverFailure is darkpanel-rollback.service, started by OnFailure= of
	// a service or a runner: rollbacks restart the services.
	RecoverFailure
	// RecoverDeadline is darkpanel update recover --deadline <tx>, run by the
	// deadline timer: it rolls back tx only.
	RecoverDeadline
)

// Recover brings the update records to a consistent state, within
// RecoverBudget and under the update lock (spec section 8): pre-swap
// recovery, then, except at agent start, the rollback of every swapped or
// rolling-back record (with RecoverDeadline, of tx only), then stepping
// continuation, then the sweep of finished operations' reservations. Every
// step runs even when an earlier one failed; the errors are joined.
func (u *Updater) Recover(ctx context.Context, mode RecoverMode, tx string) error {
	if mode == RecoverDeadline && !recordID.MatchString(tx) {
		return fmt.Errorf("update: %q is not a transaction id", tx)
	}
	ctx, cancel := context.WithTimeout(ctx, RecoverBudget)
	defer cancel()
	var (
		l   *Lock
		err error
	)
	if mode == RecoverStart {
		// The agent restarting during an update must not stop the runner
		// that restarted it.
		l, err = u.lock(ctx)
	} else {
		l, err = u.recoveryLock(ctx)
	}
	if err != nil {
		return err
	}
	defer u.unlock(l)

	var errs []error
	all, err := ListRecords(u.cfg.Paths)
	if err != nil {
		return err
	}
	for _, r := range all {
		if err := u.recoverPreSwap(ctx, r); err != nil {
			errs = append(errs, err)
		}
	}
	if mode != RecoverStart {
		for _, r := range all {
			if r.State != core.UpdateSwapped && r.State != core.UpdateRollingBack {
				continue
			}
			if mode == RecoverDeadline && r.ID != tx {
				continue
			}
			if err := u.rollBackLocked(ctx, r.ID, mode == RecoverBoot, recoverCause(mode)); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if all, err = ListRecords(u.cfg.Paths); err != nil {
		return errors.Join(append(errs, err)...)
	}
	if err := u.continueSteps(ctx, all); err != nil {
		errs = append(errs, err)
	}
	if all, err = ListRecords(u.cfg.Paths); err != nil {
		return errors.Join(append(errs, err)...)
	}
	if err := u.sweep(ctx, all); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// recoverCause is the error of a record a recover pass rolls back.
func recoverCause(mode RecoverMode) *core.ErrorDetail {
	switch mode {
	case RecoverBoot:
		return interruptedBy("the host restarted before the update was committed")
	case RecoverDeadline:
		return interruptedBy("the services did not report healthy before the deadline")
	default:
		return interruptedBy("a service or the runner failed before the update was committed")
	}
}

// recoverPreSwap fails a record before its swap whose runner unit is gone:
// pending, downloading or staged with the unit inactive, failed or unknown
// to systemd. Its staged binary is deleted and its reservation released.
func (u *Updater) recoverPreSwap(ctx context.Context, r Record) error {
	switch r.State {
	case core.UpdatePending, core.UpdateDownloading, core.UpdateStaged:
	default:
		return nil
	}
	unit := runnerUnitOf(r)
	st, err := u.cfg.Systemd.State(ctx, unit)
	if err != nil {
		return fmt.Errorf("update: read %s: %w", unit, err)
	}
	if runnerAlive(st) {
		return nil
	}
	u.cfg.Log.Warn("update: the runner stopped before the swap", "transaction", r.ID, "unit", unit, "state", st.ActiveState)
	return u.failPreSwap(ctx, &r, interruptedBy("the runner stopped before the swap"))
}

// runnerAlive reports whether a runner unit is still running or starting.
// A unit that is stopping counts as alive: it may still hold files, and the
// next recover pass sees it gone.
func runnerAlive(st platform.UnitState) bool {
	if st.LoadState == "not-found" {
		return false
	}
	switch st.ActiveState {
	case "active", "activating", "reloading", "deactivating":
		return true
	}
	return false
}

// continueSteps starts the next step of every unfinished operation whose
// last record is a committed intermediate step: a crash between that commit
// and the next step's record leaves the operation holding its reservation
// with nothing running. The caller holds the update lock.
func (u *Updater) continueSteps(ctx context.Context, all []Record) error {
	var errs []error
	for _, op := range operations(all) {
		if Finished(op) {
			continue
		}
		last := op[len(op)-1]
		if last.Kind != core.UpdateKindUpdate || last.State != core.UpdateCommitted || last.ToVersion == last.TargetVersion {
			continue
		}
		u.cfg.Log.Info("update: continuing a stepped update", "operation", last.OperationID, "after", last.ToVersion)
		if err := u.nextStep(ctx, last); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// operations groups all, ordered as ListRecords orders it, by operation, in
// the order the operations started.
func operations(all []Record) [][]Record {
	index := map[string]int{}
	var out [][]Record
	for _, r := range all {
		i, ok := index[r.OperationID]
		if !ok {
			i = len(out)
			index[r.OperationID] = i
			out = append(out, nil)
		}
		out[i] = append(out[i], r)
	}
	return out
}

// sweep releases the reservation of every operation that has no record or
// is finished. Reservation owners start "op:", which no change id does, so a
// change's locks are never touched. The caller holds the update lock, under
// which every reservation gets its record.
func (u *Updater) sweep(ctx context.Context, all []Record) error {
	rows, err := u.cfg.DB.Read().QueryContext(ctx, "SELECT DISTINCT owner FROM core_locks WHERE owner LIKE 'op:%'")
	if err != nil {
		return fmt.Errorf("update: read the reservations: %w", err)
	}
	var owners []string
	for rows.Next() {
		var owner string
		if err := rows.Scan(&owner); err != nil {
			_ = rows.Close()
			return fmt.Errorf("update: read the reservations: %w", err)
		}
		owners = append(owners, owner)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("update: read the reservations: %w", err)
	}
	var errs []error
	for _, owner := range owners {
		op := strings.TrimPrefix(owner, lifecycle.ReservationOwner(""))
		if recs := OperationRecords(all, op); len(recs) > 0 && !Finished(recs) {
			continue
		}
		u.cfg.Log.Info("update: releasing the reservation of a finished operation", "operation", op)
		if err := lifecycle.Release(ctx, u.cfg.DB, owner); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
