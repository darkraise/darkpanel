package jobs

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
)

var _ core.EnablementHook = module{}

// OnEnabled keeps every Darkpanel job from running while the module is off
// (M11 spec section 6): off stops each running job timer and remembers it in
// jobs_paused; on starts exactly the remembered timers again. The runtime
// calls it on each flag change and at agent start, so an agent that starts
// with the module off stops the timers a reboot started. The timers stay
// enabled, so the jobs keep their own enabled state.
func (m module) OnEnabled(ctx context.Context, env core.Env, enabled bool) error {
	slugs, err := m.slugs(ctx, env)
	if err != nil {
		return err
	}
	if !enabled {
		for _, slug := range slugs {
			timer := timerName(slug)
			st, err := env.Units.State(ctx, timer)
			if err != nil {
				return err
			}
			if st.ActiveState != "active" && st.ActiveState != "activating" {
				continue
			}
			// Remembered before the stop, so a crash in between still starts
			// the timer when the module comes back.
			if err := env.DB.Write(ctx, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO jobs_paused (timer) VALUES (?) ON CONFLICT (timer) DO NOTHING`, timer)
				return err
			}); err != nil {
				return fmt.Errorf("jobs: remember %s: %w", timer, err)
			}
			if err := env.Units.Stop(ctx, timer); err != nil {
				return err
			}
		}
		return nil
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
	for _, timer := range paused {
		// A job deleted while the module was off has no timer to start.
		var exists bool
		for _, slug := range slugs {
			exists = exists || timerName(slug) == timer
		}
		if exists {
			if err := env.Units.Start(ctx, timer); err != nil {
				return err
			}
		}
		if err := env.DB.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `DELETE FROM jobs_paused WHERE timer = ?`, timer)
			return err
		}); err != nil {
			return fmt.Errorf("jobs: forget %s: %w", timer, err)
		}
	}
	return nil
}
