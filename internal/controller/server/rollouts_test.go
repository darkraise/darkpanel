package server

import (
	"context"
	"database/sql"
	"testing"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/controller/rollout"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/update"
)

// fakeIndex is a release index that lists its releases.
type fakeIndex []update.Release

func (f fakeIndex) Releases(context.Context) ([]update.Release, error) { return f, nil }

// exec runs a statement on the server's controller database.
func exec(t *testing.T, s *Server, query string, args ...any) {
	t.Helper()
	if _, err := s.controllerDB.Writer().ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// TestRolloutDriverRuns shows the driver started with the server: within one
// PollInterval it takes the first host of a new rollout, and with the host
// offline it waits without planning, the rollout still running.
func TestRolloutDriverRuns(t *testing.T) {
	opts := testOptions(t)
	clock := opts.Clock.(*coretest.FakeClock)
	s := start(t, opts)
	ctx := context.Background()
	id := s.LocalHostID()
	exec(t, s, `UPDATE core_hosts SET state = 'enrolled' WHERE id = ?`, id)
	var r core.Rollout
	if err := s.controllerDB.Write(ctx, func(tx *sql.Tx) error {
		var err error
		r, err = rollout.Create(ctx, tx, rollout.CreateParams{
			Input:             core.RolloutInput{Hosts: []string{id}, Version: "0.2.0", Channel: core.ChannelStable},
			ControllerVersion: buildinfo.Version, Now: clock.Now(),
		})
		return err
	}); err != nil {
		t.Fatalf("create the rollout: %v", err)
	}

	clock.Advance(rollout.PollInterval)
	var rolloutState, hostState string
	var planID sql.NullString
	if err := s.controllerDB.Read().QueryRowContext(ctx,
		`SELECT r.state, h.state, h.plan_id FROM core_rollouts r JOIN core_rollout_hosts h ON h.rollout_id = r.id
		 WHERE r.id = ? AND h.host_id = ?`, r.ID, id).Scan(&rolloutState, &hostState, &planID); err != nil {
		t.Fatal(err)
	}
	if rolloutState != core.RolloutRunning || hostState != core.RolloutHostUpdating || planID.Valid {
		t.Errorf("after one PollInterval the rollout is %s with its host %s and plan %v; want running, updating and no plan while the host is offline",
			rolloutState, hostState, planID)
	}
}

// TestEmbeddedRolloutFails shows that a rollout to the embedded agent, whose
// process runs no updater, fails at once instead of waiting for an update
// that can never start.
func TestEmbeddedRolloutFails(t *testing.T) {
	opts := embeddedOptions(t)
	clock := opts.Clock.(*coretest.FakeClock)
	s := start(t, opts)
	ctx := context.Background()
	id := s.LocalHostID()
	exec(t, s, `UPDATE core_hosts SET state = 'enrolled' WHERE id = ?`, id)
	var r core.Rollout
	if err := s.controllerDB.Write(ctx, func(tx *sql.Tx) error {
		var err error
		r, err = rollout.Create(ctx, tx, rollout.CreateParams{
			Input:             core.RolloutInput{Hosts: []string{id}, Version: "0.2.0", Channel: core.ChannelStable},
			ControllerVersion: buildinfo.Version, Now: clock.Now(),
		})
		return err
	}); err != nil {
		t.Fatalf("create the rollout: %v", err)
	}

	clock.Advance(rollout.PollInterval)
	var rolloutState, hostState string
	var code sql.NullString
	if err := s.controllerDB.Read().QueryRowContext(ctx,
		`SELECT r.state, h.state, h.error_code FROM core_rollouts r JOIN core_rollout_hosts h ON h.rollout_id = r.id
		 WHERE r.id = ? AND h.host_id = ?`, r.ID, id).Scan(&rolloutState, &hostState, &code); err != nil {
		t.Fatal(err)
	}
	if rolloutState != core.RolloutFailed || hostState != core.RolloutHostFailed || code.String != string(errcode.Unavailable) {
		t.Errorf("after one PollInterval the rollout is %s with its host %s (%v); want failed, failed with unavailable",
			rolloutState, hostState, code)
	}
}

// TestUpdateSchedulerClaimsAWindow shows the scheduler started with the
// server over its release index and the embedded agent's clients: an
// online local host inside its update window, behind the newest release of
// its channel, gets a one-host rollout written by system:update-schedule.
func TestUpdateSchedulerClaimsAWindow(t *testing.T) {
	opts := embeddedOptions(t)
	opts.ReleaseIndex = fakeIndex{{Version: "0.2.0", Channel: core.ChannelStable}}
	clock := opts.Clock.(*coretest.FakeClock)
	s := start(t, opts)
	id := s.LocalHostID()
	exec(t, s, `UPDATE core_hosts SET state = 'enrolled', agent_version = '0.1.0' WHERE id = ?`, id)
	exec(t, s, `INSERT INTO core_host_update (host_id, channel, days, time_utc) VALUES (?, 'stable', 'mon,tue,wed,thu,fri,sat,sun', ?)`,
		id, t0.Format("15:04"))

	clock.Advance(rollout.ScheduleInterval)
	if n := count(t, s, `SELECT count(*) FROM core_rollouts r JOIN core_rollout_hosts h ON h.rollout_id = r.id
		WHERE r.version = '0.2.0' AND h.host_id = ?`, id); n != 1 {
		t.Errorf("rollouts of the local host to 0.2.0 after one ScheduleInterval: %d, want 1", n)
	}
	if n := count(t, s, `SELECT count(*) FROM core_audit WHERE actor = ? AND action = ?`,
		core.ActorUpdateSchedule, rollout.ActionRollout); n != 1 {
		t.Errorf("audit entries of the scheduled rollout: %d, want 1", n)
	}
}
