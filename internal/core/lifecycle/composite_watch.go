package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// While a composite's agent is unreachable it waits until the child's
// deadline plus UnreachableGrace, then is recovering and polls every
// UnreachablePoll (1.3 Child watcher).
const (
	UnreachableGrace = 60 * time.Second
	UnreachablePoll  = 30 * time.Second
)

// Wake makes every composite following one of changeIDs look its child up
// now instead of at its next poll. The audit ingester calls it for the
// journal entries it stores; an id no composite follows is ignored.
func (e *Engine) Wake(changeIDs ...string) {
	if e.cfg.Side != core.SideController || len(changeIDs) == 0 {
		return
	}
	ctx := context.Background()
	seen := map[string]bool{}
	for _, child := range changeIDs {
		rows, err := e.cfg.Base.DB.Read().QueryContext(ctx,
			"SELECT change_id FROM core_composite_steps WHERE child_change_id = ?", child)
		if err != nil {
			e.cfg.Base.Log.Error("lifecycle: find the composite of a child", "child", child, "error", err)
			return
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err == nil {
				seen[id] = true
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			e.cfg.Base.Log.Error("lifecycle: find the composite of a child", "child", child, "error", err)
		}
	}
	for id := range seen {
		e.wakeComposite(id)
	}
}

// RecoverComposites claims every unfinished composite with owner composite,
// whatever owner a previous process left, and starts its runner in recovery
// mode, which follows each child by 1.3's tables but never plans a step or
// makes a new attempt (1.3a). A composite in needs_repair is unowned and is
// left alone. Run it once, after Recover and SetChildren; it returns once the
// runners are started, and CompositesRecovered reports when they settled.
func (e *Engine) RecoverComposites(ctx context.Context) error {
	if _, err := e.kids(); err != nil {
		return err
	}
	type unfinished struct{ id, state string }
	rows, err := e.cfg.Base.DB.Read().QueryContext(ctx,
		"SELECT id, state FROM core_changes WHERE class = ? AND state IN (?, ?, ?, ?, ?) ORDER BY id",
		string(core.ClassComposite), core.StateApplying, core.StateAwaitingConfirmation, core.StateUndoing,
		core.StateRecovering, core.StateReleasing)
	if err != nil {
		return fmt.Errorf("lifecycle: find unfinished composites: %w", err)
	}
	var todo []unfinished
	for rows.Next() {
		var u unfinished
		if err := rows.Scan(&u.id, &u.state); err != nil {
			_ = rows.Close()
			return fmt.Errorf("lifecycle: find unfinished composites: %w", err)
		}
		todo = append(todo, u)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("lifecycle: find unfinished composites: %w", err)
	}
	var claimed []string
	for _, u := range todo {
		var n int64
		err := e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
			res, err := tx.ExecContext(ctx,
				"UPDATE core_changes SET recovery_owner = ?, updated_at = ? WHERE id = ? AND state = ?",
				core.OwnerComposite, formatTime(e.cfg.Base.Clock.Now()), u.id, u.state)
			if err != nil {
				return err
			}
			n, err = res.RowsAffected()
			return err
		})
		if err != nil {
			return fmt.Errorf("lifecycle: claim composite %s: %w", u.id, err)
		}
		if n == 1 {
			claimed = append(claimed, u.id)
		}
	}
	e.compMu.Lock()
	e.compClaimed, e.compRecoverStarted = claimed, true
	e.compMu.Unlock()
	for _, id := range claimed {
		e.goRun(func() { e.runComposite(id, nil) })
	}
	return nil
}

// CompositesRecovered reports whether composite recovery has run and left
// none of the composites it claimed applying or undoing: each is terminal,
// needs repair, or is recovering, awaiting confirmation or releasing, which
// the runner keeps following (1.3a). Once true it stays true.
func (e *Engine) CompositesRecovered() bool {
	e.compMu.Lock()
	started, done, claimed := e.compRecoverStarted, e.compRecovered, slices.Clone(e.compClaimed)
	e.compMu.Unlock()
	switch {
	case !started:
		return false
	case done:
		return true
	}
	for _, id := range claimed {
		var state string
		err := e.cfg.Base.DB.Read().QueryRowContext(context.Background(), "SELECT state FROM core_changes WHERE id = ?", id).
			Scan(&state)
		if err != nil || state == core.StateApplying || state == core.StateUndoing {
			return false
		}
	}
	e.compMu.Lock()
	e.compRecovered = true
	e.compMu.Unlock()
	return true
}

// compositeGate refuses plans and applies of core.user.* and core.token.*
// actions with core.recovering until composite recovery has settled (1.3a),
// on an engine that runs composites.
func (e *Engine) compositeGate(ref core.ActionRef) error {
	if !strings.HasPrefix(ref.Name, "core.user.") && !strings.HasPrefix(ref.Name, "core.token.") {
		return nil
	}
	e.compMu.Lock()
	runs := e.children != nil
	e.compMu.Unlock()
	if !runs || e.CompositesRecovered() {
		return nil
	}
	return errcode.New(errcode.Recovering, "composite recovery is still running; try again shortly")
}

// probeChild ends recovering once the cursor step's host answers again: any
// answer but host.offline shows it is back, and the composite returns to the
// state its direction and cursor step give.
func (e *Engine) probeChild(ctx context.Context, c compositeRow, steps []stepRow) error {
	kids, err := e.kids()
	if err != nil {
		return err
	}
	to := core.StateApplying
	if c.Cursor >= 0 && c.Cursor < len(steps) {
		s := steps[c.Cursor]
		if _, err := kids.Lookup(ctx, s.Host, s.PlanID, false); errcode.From(err).Code == errcode.HostOffline {
			return waitChild(UnreachablePoll)
		}
		if c.Direction == dirForward && (s.State == core.StepApplied || s.State == core.StepConfirming) {
			to = core.StateAwaitingConfirmation
		}
	}
	if c.Direction == dirUnwind {
		to = core.StateUndoing
	}
	_, err = e.transition(ctx, c.ID, transition{from: core.StateRecovering, to: to})
	return err
}
