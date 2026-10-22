package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// unresolvedChange applies a remote creation of object id and leaves its
// effect unresolved, resolvable from commitBy on, on a change in
// needs_repair that holds the ufx lock.
func (u *ufxFixture) unresolvedChange(id string, commitBy time.Time) core.ChangeView {
	u.t.Helper()
	c := u.change(ufxOp{ID: id, Value: "1", Remote: true})
	u.setNeedsRepair(c.ID, ufxKey)
	u.sqlExec("UPDATE core_effects SET state = ?, reason = ?, commit_by = ? WHERE change_id = ? AND seq = 1",
		string(core.EffectUnresolved), core.ReasonUncertain, formatTime(commitBy), c.ID)
	return c
}

// lastEntry is the newest journal entry.
func (f *fixture) lastEntry() core.JournalEntry {
	f.t.Helper()
	entries, err := f.journal.Entries(context.Background(), 0, 1000)
	if err != nil || len(entries) == 0 {
		f.t.Fatalf("journal: %v (%d entries)", err, len(entries))
	}
	return entries[len(entries)-1]
}

func TestObservePreviewsAnEffect(t *testing.T) {
	u := newUfx(t)
	ctx := context.Background()
	c := u.unresolvedChange("a", u.clock.Now())
	u.store.set("a", "other")
	journaled := len(u.phases())

	res, err := u.eng.Observe(ctx, bob, c.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Effect.ChangeID != c.ID || res.Effect.Seq != 1 || res.Effect.State != core.EffectUnresolved {
		t.Errorf("effect = %+v", res.Effect)
	}
	o := res.Observation
	if !strings.Contains(string(o.Current.State), "other") || o.Resource != "ufx.item:a" || o.Matches != 1 ||
		!o.Exclusive || o.Candidates == nil {
		t.Errorf("observation = %+v", o)
	}
	if want := []string{core.ResolutionAccept, core.ResolutionReconcile}; !reflect.DeepEqual(res.Allowed, want) {
		t.Errorf("allowed = %v, want %v", res.Allowed, want)
	}
	u.store.allowed["a"] = []string{core.ResolutionReconcile}
	if res, err = u.eng.Observe(ctx, bob, c.ID, 1); err != nil ||
		!reflect.DeepEqual(res.Allowed, []string{core.ResolutionReconcile}) {
		t.Errorf("allowed = %v, %v; want the module's [reconcile]", res.Allowed, err)
	}
	if n := len(u.phases()); n != journaled {
		t.Errorf("observing journaled %d entries", n-journaled)
	}
	if s := u.journalEffect(c.ID, 1).State; s != core.EffectUnresolved {
		t.Errorf("effect = %s after observing, want unresolved", s)
	}
	_, err = u.eng.Observe(ctx, bob, c.ID, 9)
	wantCode(t, err, errcode.NotFound)
	_, err = u.eng.Observe(ctx, bob, "NOPE", 1)
	wantCode(t, err, errcode.ChangeNotFound)
}

func TestResolveOutcomes(t *testing.T) {
	twoCandidates := func(s *ufxStore) {
		s.candidates["a"] = []core.Candidate{
			{Resource: "ufx.item:a1", State: core.StateDoc{State: ufxState("one")}},
			{Resource: "ufx.item:a2", State: core.StateDoc{State: ufxState("two")}},
		}
	}
	cases := []struct {
		name     string
		setup    func(s *ufxStore)
		req      core.ResolveRequest
		state    core.EffectState
		resource string
		value    string // the value in Current (accepted) or Post (made)
		refusal  string // the detail of a request.invalid_input refusal
	}{
		{name: "accept keeps what is there", setup: func(s *ufxStore) { s.set("a", "other") },
			req: core.ResolveRequest{Resolution: core.ResolutionAccept}, state: core.EffectAccepted, value: "other"},
		{name: "reconcile with the object gone", setup: func(s *ufxStore) { s.set("a", "") },
			req: core.ResolveRequest{Resolution: core.ResolutionReconcile}, state: core.EffectSkipped},
		{name: "reconcile with the object there",
			req: core.ResolveRequest{Resolution: core.ResolutionReconcile}, state: core.EffectMade,
			resource: "ufx.item:a", value: "1"},
		{name: "reconcile over several candidates", setup: twoCandidates,
			req: core.ResolveRequest{Resolution: core.ResolutionReconcile}, refusal: "the identity matches 2 objects"},
		{name: "reconcile naming a candidate", setup: twoCandidates,
			req:   core.ResolveRequest{Resolution: core.ResolutionReconcile, Resource: "ufx.item:a2"},
			state: core.EffectMade, resource: "ufx.item:a2", value: "two"},
		{name: "a resolution the module does not allow",
			setup: func(s *ufxStore) { s.allowed["a"] = []string{core.ResolutionReconcile} },
			req:   core.ResolveRequest{Resolution: core.ResolutionAccept}, refusal: "is not allowed"},
		{name: "a resource that is not the observed object",
			req:     core.ResolveRequest{Resolution: core.ResolutionReconcile, Resource: "ufx.item:zz"},
			refusal: "is not the observed object"},
		{name: "an unknown resolution", req: core.ResolveRequest{Resolution: "retry"}, refusal: "accept or reconcile"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := newUfx(t)
			ctx := context.Background()
			c := u.unresolvedChange("a", u.clock.Now())
			if tc.setup != nil {
				tc.setup(u.store)
			}
			ev, err := u.eng.Resolve(ctx, bob, c.ID, 1, tc.req)
			if v, verr := u.eng.Change(ctx, c.ID); verr != nil || v.State != core.StateNeedsRepair || v.Owner != "" {
				t.Fatalf("change = %s owned by %q (%v), want needs_repair and unowned", v.State, v.Owner, verr)
			}
			if tc.refusal != "" {
				wantRefusal(t, err, errcode.InvalidInput, tc.refusal)
				if s := u.journalEffect(c.ID, 1).State; s != core.EffectUnresolved {
					t.Errorf("effect = %s after a refusal, want unresolved", s)
				}
				if p := u.lastEntry().Phase; p == phaseResolve {
					t.Error("a refused resolution was journaled")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if ev.State != tc.state || ev.Resolution != tc.req.Resolution || ev.ResolvedBy != bob.Actor ||
				ev.ResolvedAt == nil || !ev.ResolvedAt.Equal(u.clock.Now()) {
				t.Errorf("effect = %+v", ev)
			}
			if tc.resource != "" && ev.Resource != tc.resource {
				t.Errorf("resource = %q, want %q", ev.Resource, tc.resource)
			}
			switch tc.state {
			case core.EffectAccepted:
				if ev.Current == nil || !strings.Contains(string(ev.Current.State), tc.value) {
					t.Errorf("current = %v, want the observed %q", ev.Current, tc.value)
				}
			case core.EffectMade:
				if ev.Post == nil || !strings.Contains(string(ev.Post.State), tc.value) {
					t.Errorf("post = %v, want the observed %q", ev.Post, tc.value)
				}
			}
			if s := u.journalEffect(c.ID, 1).State; s != tc.state {
				t.Errorf("stored effect = %s, want %s", s, tc.state)
			}
			if e := u.lastEntry(); e.Phase != phaseResolve || e.Actor != bob.Actor || e.ChangeID != c.ID {
				t.Errorf("last journal entry = %+v, want phase resolve by bob", e)
			}
			want := []string{c.ID + "/1=" + tc.req.Resolution + " " + string(tc.state)}
			if !reflect.DeepEqual(u.store.resolved, want) {
				t.Errorf("Resolved calls = %v, want %v", u.store.resolved, want)
			}
		})
	}
}

func TestResolveWaitsForCommitBy(t *testing.T) {
	u := newUfx(t)
	ctx := context.Background()
	c := u.unresolvedChange("a", u.clock.Now().Add(2*time.Minute))
	accept := core.ResolveRequest{Resolution: core.ResolutionAccept}
	_, err := u.eng.Resolve(ctx, bob, c.ID, 1, accept)
	wantRefusal(t, err, errcode.ChangeBusy, "the call may still commit until 2026-10-17T12:02:00Z")
	if v, _ := u.eng.Change(ctx, c.ID); v.Owner != "" {
		t.Errorf("owner = %q after the refusal, want none", v.Owner)
	}
	u.clock.Advance(2 * time.Minute)
	if ev, err := u.eng.Resolve(ctx, bob, c.ID, 1, accept); err != nil || ev.State != core.EffectAccepted {
		t.Fatalf("resolve after CommitBy = %s, %v; want accepted", ev.State, err)
	}
}

func TestResolveRefusesWhatIsNotInDoubt(t *testing.T) {
	u := newUfx(t)
	ctx := context.Background()
	accept := core.ResolveRequest{Resolution: core.ResolutionAccept}

	made := u.change(ufxOp{ID: "a", Value: "1"})
	u.setNeedsRepair(made.ID, ufxKey)
	_, err := u.eng.Resolve(ctx, bob, made.ID, 1, accept)
	wantRefusal(t, err, errcode.ChangeNotPending, "not unresolved")

	u.sqlExec("UPDATE core_changes SET state = ? WHERE id = ?", core.StateCompleted, made.ID)
	u.sqlExec("DELETE FROM core_locks")
	u.setEffectState(made.ID, 1, core.EffectUnresolved, core.ReasonUncertain)
	_, err = u.eng.Resolve(ctx, bob, made.ID, 1, accept)
	wantRefusal(t, err, errcode.ChangeNotPending, "needs_repair")

	_, err = u.eng.Resolve(ctx, bob, made.ID, 7, accept)
	wantCode(t, err, errcode.NotFound)
	_, err = u.eng.Resolve(ctx, bob, "NOPE", 1, accept)
	wantCode(t, err, errcode.ChangeNotFound)
}

func TestResolveOwnsTheChangeUntilItRecords(t *testing.T) {
	u := newUfx(t)
	ctx := context.Background()
	c := u.unresolvedChange("a", u.clock.Now())
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
		_, err := u.eng.Resolve(ctx, bob, c.ID, 1, accept)
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the resolution never observed")
	}
	if v, _ := u.eng.Change(ctx, c.ID); v.Owner != core.OwnerResolve {
		t.Errorf("owner during the resolution = %q, want resolve", v.Owner)
	}
	_, err := u.eng.Resolve(ctx, alice, c.ID, 1, accept)
	wantCode(t, err, errcode.ChangeBusy)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if v, _ := u.eng.Change(ctx, c.ID); v.Owner != "" {
		t.Errorf("owner after the resolution = %q, want none", v.Owner)
	}
}

func TestResolveRollsBackWhenResolvedFails(t *testing.T) {
	u := newUfx(t)
	ctx := context.Background()
	c := u.unresolvedChange("a", u.clock.Now())
	u.store.resolvedErr = errors.New("bookkeeping failed")
	journaled := len(u.phases())
	if _, err := u.eng.Resolve(ctx, bob, c.ID, 1, core.ResolveRequest{Resolution: core.ResolutionAccept}); err == nil {
		t.Fatal("Resolve succeeded; want the Resolved error")
	}
	if len(u.store.resolved) != 1 {
		t.Errorf("Resolved calls = %v, want one", u.store.resolved)
	}
	if s := u.journalEffect(c.ID, 1).State; s != core.EffectUnresolved {
		t.Errorf("effect = %s, want unresolved: the resolution rolled back", s)
	}
	if n := len(u.phases()); n != journaled {
		t.Errorf("a rolled-back resolution journaled %d entries", n-journaled)
	}
	if v, _ := u.eng.Change(ctx, c.ID); v.Owner != "" {
		t.Errorf("owner = %q, want none", v.Owner)
	}
}

func TestResolveOnAFailedRepairChangeClaimsItsTarget(t *testing.T) {
	u := newUfx(t)
	ctx := context.Background()
	target := u.change(ufxOp{ID: "a", Value: "1"})
	repair := u.change(ufxOp{ID: "b", Value: "1", Remote: true})
	u.sqlExec("UPDATE core_plans SET repairs = ? WHERE id = (SELECT plan_id FROM core_changes WHERE id = ?)",
		target.ID, repair.ID)
	u.sqlExec("UPDATE core_changes SET state = ? WHERE id = ?", core.StateFailed, repair.ID)
	u.sqlExec("UPDATE core_effects SET state = ?, reason = ?, commit_by = ? WHERE change_id = ? AND seq = 1",
		string(core.EffectUnresolved), core.ReasonUncertain, formatTime(u.clock.Now()), repair.ID)
	u.setNeedsRepair(target.ID, ufxKey)
	var owner string
	u.store.mu.Lock()
	u.store.onObserve = func() {
		v, _ := u.eng.Change(ctx, target.ID)
		owner = v.Owner
	}
	u.store.mu.Unlock()

	ev, err := u.eng.Resolve(ctx, bob, repair.ID, 1, core.ResolveRequest{Resolution: core.ResolutionAccept})
	if err != nil {
		t.Fatal(err)
	}
	if ev.ChangeID != repair.ID || ev.State != core.EffectAccepted {
		t.Errorf("effect = %+v, want the repair change's effect accepted", ev)
	}
	if owner != core.OwnerResolve {
		t.Errorf("target owner during the resolution = %q, want resolve", owner)
	}
	if v, _ := u.eng.Change(ctx, target.ID); v.Owner != "" || v.State != core.StateNeedsRepair {
		t.Errorf("target = %s owned by %q, want needs_repair and unowned", v.State, v.Owner)
	}
}
