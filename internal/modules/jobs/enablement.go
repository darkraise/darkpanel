package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/darkraise/darkpanel/internal/core"
)

var _ core.EnablementHook = module{}

// OnEnabled keeps every Darkpanel job from running while the module is off
// (M11 spec section 6): off stops each running job timer and remembers it in
// jobs_paused; on starts exactly the remembered timers again. The runtime
// calls it on each flag change and at agent start. Off also disables each
// stopped timer, so a reboot does not start it, a catch-up run included,
// before the agent's start call; on enables it again, so each job keeps its
// own enabled state.
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
		if err := env.DB.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `DELETE FROM jobs_paused WHERE timer = ?`, timer)
			return err
		}); err != nil {
			errs = append(errs, fmt.Errorf("jobs: forget %s: %w", timer, err))
		}
	}
	return errors.Join(errs...)
}

// pause stops and disables timer when it runs, remembering it first, so a
// crash in between still starts it when the module comes back. A failed stop
// still disables the timer, so a reboot does not start it.
func (m module) pause(ctx context.Context, env core.Env, timer string) error {
	st, err := env.Units.State(ctx, timer)
	if err != nil {
		return err
	}
	if st.ActiveState != "active" && st.ActiveState != "activating" {
		return nil
	}
	if err := env.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO jobs_paused (timer) VALUES (?) ON CONFLICT (timer) DO NOTHING`, timer)
		return err
	}); err != nil {
		return fmt.Errorf("jobs: remember %s: %w", timer, err)
	}
	return errors.Join(env.Units.Stop(ctx, timer), env.Units.Disable(ctx, timer))
}
