package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// MetaUninstalling is the core_meta key of the uninstall barrier (1.3c).
// While it exists, plan, apply, undo, repair, resolve and the recovery
// claims answer core.uninstalling, and the agent task process starts
// nothing.
const MetaUninstalling = "uninstalling"

// uninstallPoll is how often an engine whose start skipped recovery claims
// under the barrier checks whether the barrier was cleared from outside the
// process.
const uninstallPoll = 10 * time.Second

// BeginUninstall sets the uninstall barrier in the agent database db in one
// transaction. Without stopTasks it refuses with core.change_busy, naming
// them, while any change is owned or any task run is starting or running;
// with stopTasks the caller stops those task units afterwards. Setting a
// barrier that is already set succeeds.
func BeginUninstall(ctx context.Context, db *store.DB, stopTasks bool) error {
	err := db.Write(ctx, func(tx *sql.Tx) error {
		if !stopTasks {
			busy, err := uninstallBusy(ctx, tx)
			if err != nil {
				return err
			}
			if len(busy) > 0 {
				return errcode.New(errcode.ChangeBusy, "the host has unfinished work: "+strings.Join(busy, "; ")+
					"; wait for it to finish, or stop the task units; an owner left by a stopped process is "+
					"cleared by the agent's start-up recovery once no uninstall barrier is set")
			}
		}
		_, err := tx.ExecContext(ctx,
			"INSERT INTO core_meta (key, value) VALUES (?, '1') ON CONFLICT (key) DO NOTHING", MetaUninstalling)
		return err
	})
	var coded *errcode.Error
	switch {
	case errors.As(err, &coded):
		return err
	case err != nil:
		return fmt.Errorf("lifecycle: begin uninstall: %w", err)
	}
	return nil
}

// AbortUninstall clears the uninstall barrier in the agent database db in
// one transaction; clearing an absent one succeeds. darkpanel uninstall
// --abort calls it whether or not the agent runs: a running engine notices
// the barrier is gone at its next mutating call or poll (resumeSkipped).
func AbortUninstall(ctx context.Context, db *store.DB) error {
	err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM core_meta WHERE key = ?", MetaUninstalling)
		return err
	})
	if err != nil {
		return fmt.Errorf("lifecycle: abort uninstall: %w", err)
	}
	return nil
}

// AbortUninstall clears the uninstall barrier of the engine's database.
// When the engine started while the barrier was set, recoverClaims claimed
// nothing; AbortUninstall then runs recoverSkipped at once instead of
// waiting for admit or the poll. Admission answers core.recovering from
// before the barrier is cleared until the pass has started, so no new call
// races the claims.
func (e *Engine) AbortUninstall(ctx context.Context) error {
	e.uninstallMu.Lock()
	defer e.uninstallMu.Unlock()
	if len(e.uninstallSkipped) == 0 {
		return AbortUninstall(ctx, e.cfg.Base.DB)
	}
	e.recovering.Store(true)
	defer e.recovering.Store(false)
	if err := AbortUninstall(ctx, e.cfg.Base.DB); err != nil {
		return err
	}
	return e.recoverSkipped(ctx)
}

// resumeSkipped runs recoverSkipped once the barrier the engine started
// under is gone, which darkpanel uninstall --abort clears from outside the
// process. admit calls it before each mutating call and the poll every
// uninstallPoll. It does nothing while the barrier is set or when no id is
// recorded, and it never clears the barrier itself.
func (e *Engine) resumeSkipped(ctx context.Context) error {
	e.uninstallMu.Lock()
	defer e.uninstallMu.Unlock()
	if len(e.uninstallSkipped) == 0 {
		return nil
	}
	if err := uninstallingErr(ctx, e.cfg.Base.DB.Read()); err != nil {
		var coded *errcode.Error
		if errors.As(err, &coded) {
			return nil
		}
		return err
	}
	e.recovering.Store(true)
	defer e.recovering.Store(false)
	return e.recoverSkipped(ctx)
}

// recoverSkipped runs the recovery claims, the lock sweep and the background
// pass the engine's start skipped, over exactly the changes it skipped, and
// forgets their ids, so the changes a stopped process left owned are
// recovered without another restart. A failed read of the candidates keeps
// the ids for the next attempt. The caller holds uninstallMu and has set
// recovering.
func (e *Engine) recoverSkipped(ctx context.Context) error {
	cands, err := e.recoverCandidates(ctx)
	if err != nil {
		return err
	}
	skipped := make(map[string]bool, len(e.uninstallSkipped))
	for _, id := range e.uninstallSkipped {
		skipped[id] = true
	}
	e.uninstallSkipped = nil
	var claims []recoverClaim
	var errs []error
	for _, c := range cands {
		if !skipped[c.id] {
			continue
		}
		cl, err := e.recoverClaimOne(ctx, c)
		switch {
		case err != nil:
			errs = append(errs, err)
		case cl != nil:
			claims = append(claims, *cl)
		}
	}
	errs = append(errs, e.recoverSweep(ctx))
	e.recoverPass(claims)
	return errors.Join(errs...)
}

// armUninstallPoll arms the next poll unless one is armed or the engine is
// closed.
func (e *Engine) armUninstallPoll() {
	e.timerMu.Lock()
	defer e.timerMu.Unlock()
	if e.closed || e.uninstallTimer != nil {
		return
	}
	e.uninstallTimer = e.cfg.Base.Clock.AfterFunc(uninstallPoll, e.uninstallPollFired)
}

// uninstallPollFired runs one poll as a run Close waits for, and arms the
// next while skipped ids remain.
func (e *Engine) uninstallPollFired() {
	e.timerMu.Lock()
	e.uninstallTimer = nil
	if e.closed {
		e.timerMu.Unlock()
		return
	}
	e.runs.Add(1)
	e.timerMu.Unlock()
	defer e.runs.Done()
	if err := e.resumeSkipped(e.runCtx); err != nil {
		e.cfg.Base.Log.Error("lifecycle: recover the changes skipped during the uninstall", "error", err)
	}
	e.uninstallMu.Lock()
	pending := len(e.uninstallSkipped) > 0
	e.uninstallMu.Unlock()
	if pending {
		e.armUninstallPoll()
	}
}

// uninstallBusy names every owned change and every starting or running
// task run.
func uninstallBusy(ctx context.Context, tx *sql.Tx) ([]string, error) {
	var busy []string
	owned, err := tx.QueryContext(ctx,
		"SELECT id, recovery_owner FROM core_changes WHERE recovery_owner <> '' ORDER BY id")
	if err != nil {
		return nil, err
	}
	for owned.Next() {
		var id, owner string
		if err := owned.Scan(&id, &owner); err != nil {
			_ = owned.Close()
			return nil, err
		}
		busy = append(busy, fmt.Sprintf("change %s is owned by %s", id, owner))
	}
	if err := errors.Join(owned.Err(), owned.Close()); err != nil {
		return nil, err
	}
	live, err := tx.QueryContext(ctx,
		"SELECT change_id, op, attempt, state FROM core_task_runs WHERE state IN (?, ?) ORDER BY change_id, op, attempt",
		taskRowStarting, taskRowRunning)
	if err != nil {
		return nil, err
	}
	for live.Next() {
		var id, op, state string
		var attempt int
		if err := live.Scan(&id, &op, &attempt, &state); err != nil {
			_ = live.Close()
			return nil, err
		}
		busy = append(busy, fmt.Sprintf("task unit %s is %s", TaskUnitName(id, op, attempt), state))
	}
	return busy, errors.Join(live.Err(), live.Close())
}

// uninstallingErr answers core.uninstalling while the barrier is set.
func uninstallingErr(ctx context.Context, q rowQuerier) error {
	var n int
	if err := q.QueryRowContext(ctx, "SELECT count(*) FROM core_meta WHERE key = ?", MetaUninstalling).Scan(&n); err != nil {
		return fmt.Errorf("lifecycle: read the uninstall barrier: %w", err)
	}
	if n > 0 {
		return errcode.New(errcode.Uninstalling, "the host is being uninstalled; abort the uninstall to change it again")
	}
	return nil
}
