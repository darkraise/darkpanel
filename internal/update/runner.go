package update

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

// changePoll is how often the runner reads the state of the change that
// started it.
const changePoll = 500 * time.Millisecond

// timerAccuracy is the deadline timer's AccuracySec, kept small so the
// rollback stays inside the time budget.
const timerAccuracy = time.Second

// errMoved reports that another process moved a record on while the runner
// was not holding the update lock.
var errMoved = errors.New("update: the record moved on")

// Run is darkpanel update run <tx>: it moves update record tx through
// pending, downloading, staged, swapped and committed (spec section 8, The
// runner), rolls back on a failed health check, and after an intermediate
// commit starts the next step of a stepped operation.
func (u *Updater) Run(ctx context.Context, tx string) error {
	r, err := ReadRecord(u.cfg.Paths, tx)
	if err != nil {
		return err
	}
	if r.Kind != core.UpdateKindUpdate {
		return fmt.Errorf("update: transaction %s is not an update", tx)
	}
	if r.State != core.UpdatePending {
		u.cfg.Log.Info("update: the transaction is already past pending", "transaction", tx, "state", r.State)
		return nil
	}
	if err := u.waitForChange(ctx, r.ChangeID); err != nil {
		return u.failBeforeSwap(ctx, tx, err)
	}
	err = u.locked(ctx, tx, core.UpdatePending, func(r *Record) error {
		return u.save(r, core.UpdateDownloading, nil)
	})
	if err != nil {
		return u.failBeforeSwap(ctx, tx, err)
	}
	if err := u.stage(ctx, r); err != nil {
		return u.failBeforeSwap(ctx, tx, err)
	}
	var deadline time.Time
	err = u.locked(ctx, tx, core.UpdateStaged, func(r *Record) error {
		var err error
		deadline, err = u.swap(ctx, r)
		return err
	})
	switch {
	case errors.Is(err, errSwapFailed):
		return err
	case err != nil:
		return u.failBeforeSwap(ctx, tx, err)
	}
	health := u.restartServices(ctx)
	if health == nil {
		health = u.probes(ctx).Wait(ctx, u.cfg.Clock, r.FromVersion, r.ToVersion, deadline)
	}
	l, err := u.lock(ctx)
	if err != nil {
		// The deadline timer rolls the update back.
		return err
	}
	defer u.unlock(l)
	r, err = ReadRecord(u.cfg.Paths, tx)
	if err != nil {
		return err
	}
	if r.State != core.UpdateSwapped {
		u.cfg.Log.Info("update: the transaction was finished by another process", "transaction", tx, "state", r.State)
		return nil
	}
	if health != nil {
		return errors.Join(health, u.rollBackLocked(ctx, tx, false, interruptedBy(health.Error())))
	}
	return u.commit(ctx, r)
}

// waitForChange waits up to ChangeWait for the change that started the
// operation to complete, so the restart the update causes can never
// interrupt it. A stepping child has no change and does not wait.
func (u *Updater) waitForChange(ctx context.Context, changeID string) error {
	if changeID == "" {
		return nil
	}
	deadline := u.cfg.Clock.Now().Add(ChangeWait)
	for {
		var state string
		err := u.cfg.DB.Read().QueryRowContext(ctx, "SELECT state FROM core_changes WHERE id = ?", changeID).Scan(&state)
		switch {
		case err == nil && state == core.StateCompleted:
			return nil
		case err == nil && (state == core.StateFailed || state == core.StateInterrupted):
			return errcode.New(errcode.UpdateInterrupted, fmt.Sprintf("change %s, which started the update, is %s", changeID, state))
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("update: read change %s: %w", changeID, err)
		}
		if !u.cfg.Clock.Now().Before(deadline) {
			return errcode.New(errcode.UpdateInterrupted,
				fmt.Sprintf("change %s, which started the update, did not complete within %s", changeID, ChangeWait))
		}
		if err := sleepOn(ctx, u.cfg.Clock, changePoll); err != nil {
			return err
		}
	}
}

// locked runs fn on record tx under the update lock when the record is in
// state from; errMoved when it is not.
func (u *Updater) locked(ctx context.Context, tx, from string, fn func(r *Record) error) error {
	l, err := u.lock(ctx)
	if err != nil {
		return err
	}
	defer u.unlock(l)
	r, err := ReadRecord(u.cfg.Paths, tx)
	if err != nil {
		return err
	}
	if r.State != from {
		return fmt.Errorf("%w: transaction %s is %s, not %s", errMoved, tx, r.State, from)
	}
	return fn(&r)
}

func (u *Updater) downloadDir(tx string) string {
	return filepath.Join(u.cfg.Paths.UpdateDir, "download-"+tx)
}

// stage downloads and verifies r's release, stages the binary next to
// darkpanel, records its hash, self-tests it and records staged.
func (u *Updater) stage(ctx context.Context, r Record) error {
	i := slices.IndexFunc(r.Steps, func(s Release) bool { return s.Version == r.ToVersion })
	if i < 0 {
		return fmt.Errorf("update: transaction %s has no step for %s", r.ID, r.ToVersion)
	}
	dir := u.downloadDir(r.ID)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("update: clear the download directory: %w", err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return fmt.Errorf("update: create the download directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	u.cfg.Log.Info("update: downloading", "transaction", r.ID, "version", r.ToVersion)
	binary, err := Download(ctx, u.cfg.HTTP, u.cfg.BaseURL, u.cfg.Keys, r.Steps[i], u.cfg.Arch, dir)
	if err != nil {
		return err
	}
	err = u.locked(ctx, r.ID, core.UpdateDownloading, func(r *Record) error {
		sum, err := Stage(u.cfg.Paths, r.ID, binary)
		if err != nil {
			return err
		}
		r.NewSHA256 = sum
		return u.save(r, core.UpdateDownloading, nil)
	})
	if err != nil {
		return err
	}
	if err := u.cfg.SelfTest(ctx, u.cfg.Paths.NewBinary(r.ID)); err != nil {
		return err
	}
	return u.locked(ctx, r.ID, core.UpdateDownloading, func(r *Record) error {
		return u.save(r, core.UpdateStaged, nil)
	})
}

// errSwapFailed marks a failure after the record says swapped; the
// rollback has run already.
var errSwapFailed = errors.New("update: the swap failed and was rolled back")

// swap keeps the previous binary, arms the deadline timer, records swapped
// with its deadline and renames the staged binary over darkpanel, returning
// the deadline. The timer runs darkpanel.recovery, which KeepPrevious has
// just linked, so the rollback works whatever the new binary does.
func (u *Updater) swap(ctx context.Context, r *Record) (time.Time, error) {
	p := u.cfg.Paths
	prev, err := KeepPrevious(p)
	if err != nil {
		return time.Time{}, err
	}
	r.PrevSHA256 = prev
	if err := u.save(r, core.UpdateStaged, nil); err != nil {
		return time.Time{}, err
	}
	err = u.cfg.Systemd.StartTransientTimer(ctx, DeadlineTimer(r.ID), platform.TransientTimer{
		Description: "darkpanel update " + r.ID + " health deadline",
		OnActive:    HealthDeadline,
		Accuracy:    timerAccuracy,
		Service: platform.TransientService{
			Description: "darkpanel update " + r.ID + " deadline rollback",
			ExecStart:   []string{p.RecoveryBinary(), "update", "recover", "--deadline", r.ID},
		},
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("update: start the deadline timer: %w", err)
	}
	deadline := u.now().Add(HealthDeadline)
	r.Deadline = &deadline
	if err := u.save(r, core.UpdateSwapped, nil); err != nil {
		u.stopTimer(ctx, r.ID)
		return time.Time{}, err
	}
	if err := Swap(p, r.ID); err != nil {
		return time.Time{}, errors.Join(errSwapFailed, err, u.rollBackLocked(ctx, r.ID, false, detail(err)))
	}
	return deadline, nil
}

// probes are the configured probes, without the controller's on a host
// that has no controller unit.
func (u *Updater) probes(ctx context.Context) Probes {
	p := u.cfg.Probes
	if st, err := u.cfg.Systemd.State(ctx, ControllerUnit); err == nil && st.LoadState == "not-found" {
		p.Controller = nil
	}
	return p
}

// failBeforeSwap records tx failed with cause when it is still before the
// swap, deletes what it staged and ends its operation. It returns cause.
func (u *Updater) failBeforeSwap(ctx context.Context, tx string, cause error) error {
	if errors.Is(cause, errMoved) {
		u.cfg.Log.Info("update: the transaction was finished by another process", "transaction", tx, "detail", cause)
		return nil
	}
	l, err := u.lock(ctx)
	if err != nil {
		return errors.Join(cause, err)
	}
	defer u.unlock(l)
	r, err := ReadRecord(u.cfg.Paths, tx)
	if err != nil {
		return errors.Join(cause, err)
	}
	switch r.State {
	case core.UpdatePending, core.UpdateDownloading, core.UpdateStaged:
	default:
		return cause
	}
	u.cfg.Log.Error("update: the update failed before the swap", "transaction", tx, "error", cause)
	return errors.Join(cause, u.failPreSwap(ctx, &r, detail(cause)))
}

// failPreSwap records r failed before its swap: the staged binary and the
// download are deleted and the operation ends. The caller holds the lock.
func (u *Updater) failPreSwap(ctx context.Context, r *Record, cause *core.ErrorDetail) error {
	var errs []error
	if err := removeIfExists(u.cfg.Paths.NewBinary(r.ID)); err != nil {
		errs = append(errs, fmt.Errorf("update: remove the staged binary: %w", err))
	}
	if err := os.RemoveAll(u.downloadDir(r.ID)); err != nil {
		errs = append(errs, fmt.Errorf("update: remove the download: %w", err))
	}
	return errors.Join(append(errs, u.finish(ctx, r, core.UpdateFailed, cause, false))...)
}

// commit records r committed after a passed health check. The last step
// ends the operation; an intermediate one keeps the reservation and starts
// the next step. The caller holds the lock.
func (u *Updater) commit(ctx context.Context, r Record) error {
	if err := u.save(&r, core.UpdateCommitted, nil); err != nil {
		return err
	}
	u.stopTimer(ctx, r.ID)
	if r.ToVersion == r.TargetVersion {
		u.cfg.Log.Info("update: committed", "transaction", r.ID, "version", r.ToVersion)
		var errs []error
		if err := removeIfExists(u.cfg.Paths.RecoveryBinary()); err != nil {
			errs = append(errs, fmt.Errorf("update: remove the recovery binary: %w", err))
		}
		return errors.Join(append(errs, u.release(ctx, r.OperationID))...)
	}
	return u.nextStep(ctx, r)
}

// nextStep records the step after the committed intermediate step r pending
// and starts its runner with the binary r installed. The caller holds the
// lock; the new runner waits for it.
func (u *Updater) nextStep(ctx context.Context, r Record) error {
	i := slices.IndexFunc(r.Steps, func(s Release) bool { return s.Version == r.ToVersion })
	if i < 0 || i+1 >= len(r.Steps) {
		return fmt.Errorf("update: transaction %s has no step after %s", r.ID, r.ToVersion)
	}
	now, err := u.startTime()
	if err != nil {
		return err
	}
	parent := r.ID
	child := Record{
		ID: u.newID(), OperationID: r.OperationID, ParentID: &parent, Kind: core.UpdateKindUpdate,
		FromVersion: r.ToVersion, ToVersion: r.Steps[i+1].Version, TargetVersion: r.TargetVersion,
		State: core.UpdatePending, StartedAt: now, UpdatedAt: now, Steps: r.Steps,
	}
	if err := WriteRecord(u.cfg.Paths, child); err != nil {
		return err
	}
	u.cfg.Log.Info("update: starting the next step", "transaction", child.ID, "version", child.ToVersion)
	argv := []string{u.cfg.Paths.Binary(), "update", "run", child.ID}
	if err := u.startRunner(ctx, RunnerUnit(child.ID), argv); err != nil {
		return errors.Join(err, u.failPreSwap(ctx, &child, detail(err)))
	}
	return nil
}
