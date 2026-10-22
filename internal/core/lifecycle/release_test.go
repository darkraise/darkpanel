package lifecycle

import (
	"context"
	"database/sql"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

func TestReleaseAbandonsTheChange(t *testing.T) {
	u := newUfx(t)
	ctx := context.Background()
	c := u.change(ufxOp{ID: "a", Value: "1"}, ufxOp{ID: "b", Value: "1"}, ufxOp{ID: "d", Value: "1"})
	u.setEffectState(c.ID, 2, core.EffectUnresolved, core.ReasonUncertain)
	u.setEffectState(c.ID, 3, core.EffectUndone, "")
	u.setNeedsRepair(c.ID, ufxKey)
	reason := strings.Repeat("é", maxReasonLength) // characters, not bytes, count

	res, err := u.eng.Release(ctx, bob, c.ID, reason, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Change.State != core.StateAbandoned || res.Change.Owner != "" {
		t.Errorf("change = %s owned by %q, want abandoned", res.Change.State, res.Change.Owner)
	}
	var seqs []int
	for _, ev := range res.Abandoned {
		if ev.State != core.EffectAbandoned || ev.ChangeID != c.ID {
			t.Errorf("abandoned effect = %+v", ev)
		}
		seqs = append(seqs, ev.Seq)
	}
	if !reflect.DeepEqual(seqs, []int{1, 2}) {
		t.Errorf("abandoned seqs = %v, want [1 2]: the undone effect is terminal already", seqs)
	}
	want := map[int]core.EffectState{1: core.EffectAbandoned, 2: core.EffectAbandoned, 3: core.EffectUndone}
	for seq, s := range want {
		if got := u.journalEffect(c.ID, seq).State; got != s {
			t.Errorf("effect %d = %s, want %s", seq, got, s)
		}
	}
	if n := u.count("SELECT count(*) FROM core_locks"); n != 0 {
		t.Errorf("locks = %d, want none", n)
	}
	e := u.lastEntry()
	if e.Phase != phaseRelease || e.Result != resultOK || e.Actor != bob.Actor || e.ChangeID != c.ID ||
		!strings.Contains(string(e.Input), `"reason":"é`) {
		t.Errorf("journal entry = %+v, want a release by bob carrying the reason", e)
	}
	journaled := len(u.phases())

	again, err := u.eng.Release(ctx, bob, c.ID, "again", nil)
	if err != nil || again.Change.State != core.StateAbandoned || again.Abandoned == nil || len(again.Abandoned) != 0 {
		t.Fatalf("a repeated release = %+v, %v; want the abandoned change and no effects", again, err)
	}
	if n := len(u.phases()); n != journaled {
		t.Errorf("a repeated release journaled %d entries", n-journaled)
	}
}

func TestReleaseAbandonsTheEffectsOfFailedRepairChanges(t *testing.T) {
	u := newUfx(t)
	ctx := context.Background()
	target := u.change(ufxOp{ID: "a", Value: "1"})
	failed := u.change(ufxOp{ID: "b", Value: "1"})
	completed := u.change(ufxOp{ID: "d", Value: "1"})
	for _, id := range []string{failed.ID, completed.ID} {
		u.sqlExec("UPDATE core_plans SET repairs = ? WHERE id = (SELECT plan_id FROM core_changes WHERE id = ?)", target.ID, id)
	}
	u.sqlExec("UPDATE core_changes SET state = ? WHERE id = ?", core.StateFailed, failed.ID)
	u.setNeedsRepair(target.ID, ufxKey)

	effs, err := u.eng.Effects(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	var owners []string
	for _, ev := range effs {
		owners = append(owners, ev.ChangeID)
	}
	if want := []string{target.ID, failed.ID, completed.ID}; !reflect.DeepEqual(owners, want) {
		t.Errorf("effects of the target belong to %v, want %v", owners, want)
	}

	res, err := u.eng.Release(ctx, bob, target.ID, "given up", nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, ev := range res.Abandoned {
		got = append(got, ev.ChangeID)
	}
	if want := []string{target.ID, failed.ID}; !reflect.DeepEqual(got, want) {
		t.Errorf("abandoned effects of %v, want %v", got, want)
	}
	if s := u.journalEffect(completed.ID, 1).State; s != core.EffectMade {
		t.Errorf("the completed repair change's effect = %s, want made", s)
	}
}

func TestReleaseRefusals(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(u *ufxFixture, c core.ChangeView)
		p      core.Principal
		call   *core.CallContext
		reason string
		code   errcode.Code
		part   string
	}{
		{name: "a repair change", setup: func(u *ufxFixture, c core.ChangeView) {
			u.sqlExec("UPDATE core_plans SET repairs = 'TARGET1' WHERE id = (SELECT plan_id FROM core_changes WHERE id = ?)", c.ID)
		}, code: errcode.ChangeBusy, part: "release change TARGET1"},
		{name: "a step of a composite", setup: func(u *ufxFixture, c core.ChangeView) {
			u.sqlExec("UPDATE core_changes SET parent_change_id = 'COMPOSITE1' WHERE id = ?", c.ID)
		}, code: errcode.ChangeBusy, part: "release the composite"},
		{name: "a step released by an operator with its parent's call", setup: func(u *ufxFixture, c core.ChangeView) {
			u.sqlExec("UPDATE core_changes SET parent_change_id = 'COMPOSITE1' WHERE id = ?", c.ID)
		}, call: &core.CallContext{ParentChangeID: "COMPOSITE1"}, code: errcode.ChangeBusy, part: "release the composite"},
		{name: "an owned change", setup: func(u *ufxFixture, c core.ChangeView) {
			u.sqlExec("UPDATE core_changes SET recovery_owner = ? WHERE id = ?", core.OwnerResolve, c.ID)
		}, code: errcode.ChangeBusy, part: "owned by resolve"},
		{name: "an owned repair change", setup: func(u *ufxFixture, c core.ChangeView) {
			u.sqlExec("DELETE FROM core_locks")
			r := u.change(ufxOp{ID: "r", Value: "1"})
			u.setNeedsRepair(c.ID, ufxKey)
			u.sqlExec("UPDATE core_plans SET repairs = ? WHERE id = (SELECT plan_id FROM core_changes WHERE id = ?)", c.ID, r.ID)
			u.sqlExec("UPDATE core_changes SET recovery_owner = ? WHERE id = ?", core.OwnerEngine, r.ID)
		}, code: errcode.ChangeBusy, part: "is owned by engine"},
		{name: "a running task unit", setup: func(u *ufxFixture, c core.ChangeView) {
			u.sqlExec(`INSERT INTO core_task_runs (change_id, op, attempt, state, started_at)
				VALUES (?, 'apply', 1, 'running', ?)`, c.ID, formatTime(u.clock.Now()))
		}, code: errcode.ChangeBusy, part: "task unit"},
		{name: "an empty reason", reason: "-", code: errcode.InvalidInput, part: "1 to 500 characters"},
		{name: "a reason over 500 characters", reason: strings.Repeat("x", maxReasonLength+1),
			code: errcode.InvalidInput, part: "1 to 500 characters"},
		{name: "a change not in needs_repair", setup: func(u *ufxFixture, c core.ChangeView) {
			u.sqlExec("UPDATE core_changes SET state = ? WHERE id = ?", core.StateCompleted, c.ID)
		}, code: errcode.ChangeNotPending, part: "completed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := newUfx(t)
			c := u.change(ufxOp{ID: "a", Value: "1"})
			u.setNeedsRepair(c.ID, ufxKey)
			if tc.setup != nil {
				tc.setup(u, c)
			}
			p, reason := tc.p, tc.reason
			if p.Actor == "" {
				p = bob
			}
			switch reason {
			case "":
				reason = "given up"
			case "-":
				reason = ""
			}
			before := u.state(c.ID)
			_, err := u.eng.Release(context.Background(), p, c.ID, reason, tc.call)
			wantRefusal(t, err, tc.code, tc.part)
			if s := u.state(c.ID); s != before {
				t.Errorf("state = %s after the refusal, want %s", s, before)
			}
			if s := u.journalEffect(c.ID, 1).State; s == core.EffectAbandoned {
				t.Error("a refused release abandoned an effect")
			}
		})
	}
}

func TestReleaseOfAStepByItsComposite(t *testing.T) {
	u := newUfx(t)
	c := u.change(ufxOp{ID: "a", Value: "1"})
	u.setNeedsRepair(c.ID, ufxKey)
	u.sqlExec("UPDATE core_changes SET parent_change_id = 'COMPOSITE1' WHERE id = ?", c.ID)
	call := &core.CallContext{ParentChangeID: "COMPOSITE1", ParentAction: "cfx.deploy.add"}
	res, err := u.eng.Release(context.Background(), recoveryPrincipal, c.ID, "the composite was released", call)
	if err != nil || res.Change.State != core.StateAbandoned {
		t.Fatalf("release by the composite = %+v, %v; want abandoned", res.Change, err)
	}
}

func TestReleaseRacesAResolution(t *testing.T) {
	u := newUfx(t)
	ctx := context.Background()
	c := u.change(ufxOp{ID: "a", Value: "1"}, ufxOp{ID: "b", Value: "1"})
	u.setNeedsRepair(c.ID, ufxKey)
	u.sqlExec("UPDATE core_effects SET state = ?, reason = ?, commit_by = ? WHERE change_id = ?",
		string(core.EffectUnresolved), core.ReasonUncertain, formatTime(u.clock.Now()), c.ID)
	entered, release := make(chan struct{}, 1), make(chan struct{})
	u.store.mu.Lock()
	u.store.onObserve = func() {
		entered <- struct{}{}
		<-release
	}
	u.store.mu.Unlock()
	accept := core.ResolveRequest{Resolution: core.ResolutionAccept}
	done := make(chan error, 1)
	go func() {
		_, err := u.eng.Resolve(ctx, bob, c.ID, 2, accept)
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the resolution never observed")
	}
	_, err := u.eng.Release(ctx, bob, c.ID, "given up", nil)
	wantCode(t, err, errcode.ChangeBusy)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	u.store.mu.Lock()
	u.store.onObserve = nil
	u.store.mu.Unlock()

	res, err := u.eng.Release(ctx, bob, c.ID, "given up", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Abandoned) != 1 || res.Abandoned[0].Seq != 1 {
		t.Errorf("abandoned = %+v, want effect 1 only: effect 2 was accepted", res.Abandoned)
	}
	_, err = u.eng.Resolve(ctx, bob, c.ID, 1, accept)
	wantRefusal(t, err, errcode.ChangeNotPending, "abandoned")
}

func TestEffectsOfChangesWithoutEffects(t *testing.T) {
	u := newUfx(t)
	ctx := context.Background()
	reset := u.apply(alice, u.plan(alice, refUfxReset, `{"id":"a"}`).PlanID, core.ApplyOptions{})
	effs, err := u.eng.Effects(ctx, reset.ID)
	if err != nil || effs == nil || len(effs) != 0 {
		t.Errorf("effects = %v, %v; want an empty list", effs, err)
	}
	_, err = u.eng.Effects(ctx, "NOPE")
	wantCode(t, err, errcode.ChangeNotFound)
	steps, err := u.eng.Steps(ctx, reset.ID)
	if err != nil || steps == nil || len(steps) != 0 {
		t.Errorf("steps of an agent change = %v, %v; want an empty list", steps, err)
	}
}

func TestStepsListsACompositesSteps(t *testing.T) {
	ctx := context.Background()
	db := openDB(t, store.SideController)
	clock := coretest.NewFakeClock(time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC))
	reg, err := core.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(Config{
		Side: core.SideController, Registry: reg, Validator: coretest.Validator{}, Journal: &coretest.MemJournal{},
		Redact: testRedact,
		Base:   core.Env{DB: db, Sealer: coretest.Sealer{}, Clock: clock, Log: slog.New(slog.DiscardHandler)},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	now := formatTime(clock.Now())
	deadline := clock.Now().Add(time.Minute)
	err = db.Write(ctx, func(tx *sql.Tx) error {
		for _, row := range []struct{ id, class string }{{"COMPOSITE1", "composite"}, {"PLAIN1", "compensable"}} {
			if _, err := tx.Exec(`INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash,
				diff_json, guards_json, confirmation, created_at, expires_at)
				VALUES (?, '', 'cfx.deploy.add', 1, 'user:alice', '', '', '[]', '[]', '', ?, ?)`,
				"PLAN-"+row.id, now, now); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by,
				actor, state, input_json, created_at, updated_at)
				VALUES (?, ?, '', 'cfx.deploy.add', 1, ?, '', 'user:alice', 'applying', '{}', ?, ?)`,
				row.id, "PLAN-"+row.id, row.class, now, now); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`INSERT INTO core_composite_steps (change_id, step_id, ordinal, host_id, action,
			input_template, state, attempt, updated_at)
			VALUES ('COMPOSITE1', 'two', 2, '', 'cfx.store.put/v1', '{}', 'pending', 0, ?)`, now); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO core_composite_steps (change_id, step_id, ordinal, host_id, action,
			input_template, state, attempt, child_change_id, earlier_child_change_ids, child_deadline, error_code,
			error_message, updated_at)
			VALUES ('COMPOSITE1', 'one', 1, 'host-1', 'jfx.item.create/v3', '{}', 'applied', 2, 'CHILD2', '["CHILD1"]',
			?, 'core.child_failed', 'step one failed', ?)`, formatTime(deadline), now)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	steps, err := eng.Steps(ctx, "COMPOSITE1")
	if err != nil {
		t.Fatal(err)
	}
	want := []core.StepView{
		{
			ID: "one", Ordinal: 1, Host: "host-1", Action: core.ActionRef{Name: "jfx.item.create", Version: 3},
			State: "applied", Attempt: 2, ChildChangeID: "CHILD2", EarlierChildChangeIDs: []string{"CHILD1"},
			ChildDeadline: &deadline, Error: &core.ChangeError{Code: "core.child_failed", Message: "step one failed"},
		},
		{
			ID: "two", Ordinal: 2, Action: core.ActionRef{Name: "cfx.store.put", Version: 1}, State: "pending",
			EarlierChildChangeIDs: []string{},
		},
	}
	if !reflect.DeepEqual(steps, want) {
		t.Errorf("steps = %+v\nwant %+v", steps, want)
	}
	if steps, err := eng.Steps(ctx, "PLAIN1"); err != nil || steps == nil || len(steps) != 0 {
		t.Errorf("steps of a plain change = %v, %v; want an empty list", steps, err)
	}
	_, err = eng.Steps(ctx, "NOPE")
	wantCode(t, err, errcode.ChangeNotFound)
}
