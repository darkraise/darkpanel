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

	// The same rollout moves on once the flag is gone, so the host stayed
	// waiting because of the quarantine and not for want of a precondition.
	clock.Advance(rollout.PollInterval)
	aqEventually(t, "the rollout host to leave waiting after the quarantine", func() bool {
		var state string
		if err := s.controllerDB.Read().QueryRowContext(ctx,
			`SELECT state FROM core_rollout_hosts WHERE rollout_id = ? AND host_id = ?`, r.ID, id).Scan(&state); err != nil {
			t.Fatal(err)
		}
		return state != core.RolloutHostWaiting
	})
}

// aqEventually fails t unless cond holds within five seconds.
func aqEventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
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

	// The window is still open, so the scheduler claims it once the flag is
	// gone: it held because of the quarantine alone.
	clock.Advance(rollout.ScheduleInterval)
	aqEventually(t, "a rollout after the quarantine", func() bool {
		return count(t, s, `SELECT count(*) FROM core_rollouts`) > 0
	})
}

// TestQuarantineHoldsCompositeRecoveryAtStart: a controller started in
// quarantine leaves an unfinished composite to its owner, and a start after
// the flag is cleared recovers it.
func TestQuarantineHoldsCompositeRecoveryAtStart(t *testing.T) {
	opts := testOptions(t)
	ctx := context.Background()
	s, err := Start(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	plan := planDisable(t, s)
	at := t0.Format(timeFormat)
	const id = "01COMPOSITE0000000000000QX"
	exec(t, s, `INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state,
		 input_json, recovery_owner, created_at, updated_at)
		 VALUES (?, ?, '', 'cfx.deploy.add', 1, 'composite', '', 'local:root@h', 'undoing', '{}', 'composite', ?, ?)`,
		id, plan.PlanID, at, at)
	exec(t, s, "INSERT INTO core_composites (change_id, direction, cursor, steps_deadline) VALUES (?, 'unwind', -1, ?)", id, at)
	exec(t, s, `INSERT INTO core_meta (key, value) VALUES (?, ?)`, quarantine.MetaKey,
		`{"tx":"01RESTORE","restoredAt":"2026-10-18T12:00:00Z","backupCreatedAt":"2026-10-17T12:00:00Z"}`)
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	s = start(t, opts)
	time.Sleep(200 * time.Millisecond)
	if v, err := s.engine.Change(ctx, id); err != nil || v.State != core.StateUndoing || v.Owner != "composite" {
		t.Fatalf("composite in quarantine = %s owned by %q, %v; want it left undoing and owned by composite", v.State, v.Owner, err)
	}
	exec(t, s, `DELETE FROM core_meta WHERE key = ?`, quarantine.MetaKey)
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	s = start(t, opts)
	aqEventually(t, "composite recovery after the quarantine", func() bool {
		v, err := s.engine.Change(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return v.State == core.StateReverted && v.Owner == ""
	})
}
