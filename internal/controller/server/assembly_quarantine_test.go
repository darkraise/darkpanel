package server

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/controller/quarantine"
	"github.com/darkraise/darkpanel/internal/controller/rollout"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// TestQuarantinedControllerHoldsMutations: a controller started on a
// database whose restore-quarantine flag is set refuses applies, does not
// start the contributed services or drive rollouts, and starts the
// services once the flag is cleared.
func TestQuarantinedControllerHoldsMutations(t *testing.T) {
	opts := testOptions(t)
	clock := opts.Clock.(*coretest.FakeClock)
	ctx := context.Background()
	first, err := Start(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, opts.Paths.ControllerDB())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO core_meta (key, value) VALUES (?, ?)`, quarantine.MetaKey,
		`{"tx":"01RESTORE","restoredAt":"2026-10-18T12:00:00Z","backupCreatedAt":"2026-10-17T12:00:00Z"}`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	c := newXcContributor()
	opts.Contributors = []ext.Controller{c}
	s := start(t, opts)
	id := s.LocalHostID()
	plan := planDisable(t, s)
	_, err = s.dispatcher.Apply(ctx, root(id), "", refDisable, plan.PlanID, core.ApplyOptions{})
	if e := new(errcode.Error); !errors.As(err, &e) || e.Code != errcode.Quarantined {
		t.Errorf("apply in quarantine = %v, want %s", err, errcode.Quarantined)
	}

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
	var hostState string
	if err := s.controllerDB.Read().QueryRowContext(ctx,
		`SELECT state FROM core_rollout_hosts WHERE rollout_id = ? AND host_id = ?`, r.ID, id).Scan(&hostState); err != nil {
		t.Fatal(err)
	}
	if hostState != core.RolloutHostWaiting {
		t.Errorf("rollout host in quarantine is %s, want %s: the driver must not run", hostState, core.RolloutHostWaiting)
	}
	select {
	case <-c.ran:
		t.Fatal("a contributed service started in quarantine")
	case <-time.After(100 * time.Millisecond):
	}

	exec(t, s, `DELETE FROM core_meta WHERE key = ?`, quarantine.MetaKey)
	clock.Advance(serviceDefaultPoll)
	xcWait(t, c.ran, "the contributed service's start after the quarantine")
}

// TestQuarantineSuspendsSchedulerAndEngine: once the flag is set on a
// running controller, the update scheduler claims no open window, and the
// controller engine's configuration answers suspended, so composite
// recovery, the child watcher and recovery handlers hold.
func TestQuarantineSuspendsSchedulerAndEngine(t *testing.T) {
	opts := embeddedOptions(t)
	opts.ReleaseIndex = fakeIndex{{Version: "0.2.0", Channel: core.ChannelStable}}
	clock := opts.Clock.(*coretest.FakeClock)
	s := start(t, opts)
	ctx := context.Background()
	id := s.LocalHostID()
	exec(t, s, `UPDATE core_hosts SET state = 'enrolled', agent_version = '0.1.0' WHERE id = ?`, id)
	exec(t, s, `INSERT INTO core_host_update (host_id, channel, days, time_utc) VALUES (?, 'stable', 'mon,tue,wed,thu,fri,sat,sun', ?)`,
		id, t0.Format("15:04"))
	exec(t, s, `INSERT INTO core_meta (key, value) VALUES (?, ?)`, quarantine.MetaKey,
		`{"tx":"01RESTORE","restoredAt":"2026-10-18T12:00:00Z","backupCreatedAt":"2026-10-17T12:00:00Z"}`)

	clock.Advance(rollout.ScheduleInterval)
	if n := count(t, s, `SELECT count(*) FROM core_rollouts`); n != 0 {
		t.Errorf("rollouts after one ScheduleInterval in quarantine: %d, want 0: the scheduler must not run", n)
	}

	suspended := s.engineConfig(s.registry).Suspended
	if suspended == nil {
		t.Fatal("engineConfig().Suspended is nil; restore quarantine must suspend the controller engine")
	}
	if on, err := suspended(ctx); err != nil || !on {
		t.Errorf("engine Suspended in quarantine = %v, %v; want true, nil", on, err)
	}
	exec(t, s, `DELETE FROM core_meta WHERE key = ?`, quarantine.MetaKey)
	if on, err := suspended(ctx); err != nil || on {
		t.Errorf("engine Suspended after the quarantine = %v, %v; want false, nil", on, err)
	}
}
