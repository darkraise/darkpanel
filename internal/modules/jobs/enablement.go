package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var _ core.EnablementHook = module{}

// OnEnabled keeps every Darkpanel job from running while the module is off
// (M11 spec section 6): off stops every job timer and remembers in
// jobs_paused those that ran or were starting; on starts exactly the
// remembered timers again. The runtime calls it on each flag change and at
// agent start. Off also disables each timer, so a reboot does not start it,
// a catch-up run included, before the agent's start call; on enables the
// remembered ones again, so each job keeps its own enabled state.
func (m module) OnEnabled(ctx context.Context, env core.Env, enabled bool) error {
	slugs, err := m.slugs(ctx, env)
	if err != nil {
		return err
	}
	if !enabled {
		// One timer that cannot be stopped leaves the others to stop: "off
		// on this host" means no job runs (spec section 6).
		var errs []error
		for _, slug := range slugs {
			errs = append(errs, m.pause(ctx, env, timerName(slug)))
		}
		return errors.Join(errs...)
	}
	rows, err := env.DB.Read().QueryContext(ctx, `SELECT timer FROM jobs_paused ORDER BY timer`)
	if err != nil {
		return fmt.Errorf("jobs: read the paused timers: %w", err)
	}
	var paused []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			_ = rows.Close()
			return fmt.Errorf("jobs: read the paused timers: %w", err)
		}
		paused = append(paused, t)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	var errs []error
	for _, timer := range paused {
		// A job deleted while the module was off has no timer to start.
		exists := slices.ContainsFunc(slugs, func(slug string) bool { return timerName(slug) == timer })
		if exists {
			if err := errors.Join(env.Units.Enable(ctx, timer), env.Units.Start(ctx, timer)); err != nil {
				// Still remembered, so the next on starts it.
				errs = append(errs, err)
				continue
			}
		}
		if err := forget(ctx, env, timer); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// pause remembers timer when it runs, is starting or has a start job
// queued, then stops and disables it whatever its state: Stop also cancels
// a queued start, and a start that passed the runtime's guard just before
// off may not show as running yet. The timer is remembered first, so a
// crash in between still starts it when the module comes back. A timer
// whose state cannot be read, or that cannot be remembered, is still
// stopped and disabled, and the error is returned; a failed stop still
// disables it, so a reboot does not start it.
func (m module) pause(ctx context.Context, env core.Env, timer string) error {
	st, err := env.Units.State(ctx, timer)
	if err == nil && (st.ActiveState == "active" || st.ActiveState == "activating" || (st.PendingJob != "" && st.PendingJob != "stop")) {
		err = remember(ctx, env, timer)
	}
	return errors.Join(err, env.Units.Stop(ctx, timer), env.Units.Disable(ctx, timer))
}

// remember records timer in jobs_paused, for the on hook to start.
func remember(ctx context.Context, env core.Env, timer string) error {
	if err := env.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO jobs_paused (timer) VALUES (?) ON CONFLICT (timer) DO NOTHING`, timer)
		return err
	}); err != nil {
		return fmt.Errorf("jobs: remember %s: %w", timer, err)
	}
	return nil
}

// forget deletes timer's jobs_paused row.
func forget(ctx context.Context, env core.Env, timer string) error {
	if err := env.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM jobs_paused WHERE timer = ?`, timer)
		return err
	}); err != nil {
		return fmt.Errorf("jobs: forget %s: %w", timer, err)
	}
	return nil
}

// remembered reports whether timer has a jobs_paused row.
func remembered(ctx context.Context, env core.Env, timer string) (bool, error) {
	var n int
	if err := env.DB.Read().QueryRowContext(ctx, `SELECT count(*) FROM jobs_paused WHERE timer = ?`, timer).Scan(&n); err != nil {
		return false, fmt.Errorf("jobs: read the paused timers: %w", err)
	}
	return n > 0, nil
}

// errPaused ends a wait for a timer that the off hook remembered.
var errPaused = errors.New("jobs: the off hook paused the timer")

// jobsOff reports whether err is the runtime guard's refusal of a job
// unit's Start, Restart or Enable: jobs is off on this host.
func jobsOff(err error) bool {
	var e *errcode.Error
	return errors.As(err, &e) && e.Code == errcode.ModuleUnavailable
}
