package lifecycle

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var bob = core.Principal{Actor: "user:bob", UserID: "bob", Scope: core.ScopeOperate, Source: "test"}

func (f *fixture) apply(p core.Principal, planID string, opts core.ApplyOptions) core.ChangeView {
	f.t.Helper()
	v, err := f.eng.Apply(context.Background(), p, planID, opts)
	if err != nil {
		f.t.Fatalf("Apply: %v", err)
	}
	return v
}

// phases lists the journal as "<phase>/<result>[/<error code>]" lines.
func (f *fixture) phases() []string {
	f.t.Helper()
	entries, err := f.journal.Entries(context.Background(), 0, 100)
	if err != nil {
		f.t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		line := e.Phase + "/" + e.Result
		if e.ErrorCode != "" {
			line += "/" + e.ErrorCode
		}
		out = append(out, line)
	}
	return out
}

func (f *fixture) count(query string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.db.Read().QueryRow(query, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

// planInputCleared reports whether the plan's sealed input is gone.
func (f *fixture) planInputCleared(planID string) bool {
	f.t.Helper()
	var ct []byte
	if err := f.db.Read().QueryRow("SELECT input_ct FROM core_plans WHERE id = ?", planID).Scan(&ct); err != nil {
		f.t.Fatal(err)
	}
	return ct == nil
}

func TestApplyCompensableCompletes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	pl := f.plan(alice, refAdd, `{"name":"a","value":"1","secret":"s"}`)
	v := f.apply(alice, pl.PlanID, core.ApplyOptions{})

	if v.State != core.StateCompleted || v.Class != core.ClassCompensable || v.CompensatedBy != refRemove.Name ||
		v.HostID != testHost || v.Actor != alice.Actor || v.Action != refAdd.Name || v.Version != 1 ||
		v.Error != nil || v.RevertDeadline != nil {
		t.Errorf("change = %+v", v)
	}
	if want := `{"previous":"","token":"tok-a"}`; string(v.Output) != want {
		t.Errorf("apply output = %s, want %s", v.Output, want)
	}
	if f.host.items["a"] != "1" {
		t.Errorf("item a = %q, want 1", f.host.items["a"])
	}
	stored, err := f.eng.Change(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"previous":"","token":"[redacted]"}`; string(stored.Output) != want {
		t.Errorf("stored output = %s, want %s", stored.Output, want)
	}
	if n := f.count("SELECT count(*) FROM core_locks"); n != 0 {
		t.Errorf("locks = %d, want 0", n)
	}
	if !f.planInputCleared(pl.PlanID) {
		t.Error("the plan's sealed input was kept after the result")
	}
	entries, err := f.journal.Entries(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("journal = %+v, want one entry", entries)
	}
	e := entries[0]
	if e.Phase != "apply" || e.Result != "ok" || e.Actor != alice.Actor || e.ChangeID != v.ID ||
		e.Action != refAdd.Name || e.Version != 1 || string(e.Input) != `{"name":"a","secret":"[redacted]","value":"1"}` {
		t.Errorf("journal entry = %+v", e)
	}

	again := f.apply(alice, pl.PlanID, core.ApplyOptions{})
	if again.ID != v.ID || string(again.Output) != `{"previous":"","token":"[redacted]"}` {
		t.Errorf("repeated apply = %+v, want the stored change", again)
	}
	if f.host.applies != 1 {
		t.Errorf("handler applied %d times, want 1", f.host.applies)
	}
}

func TestApplyIrreversibleNeedsTheConfirmation(t *testing.T) {
	f := newFixture(t)
	pl := f.plan(alice, refRemove, `{"name":"a","value":"x"}`)
	for _, phrase := range []string{"", "b", "A"} {
		_, err := f.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{Confirmation: phrase})
		wantCode(t, err, errcode.PlanConfirmationNeeded)
	}
	if n := f.count("SELECT count(*) FROM core_changes"); n != 0 {
		t.Fatalf("changes after refused confirmations = %d, want 0", n)
	}
	if v := f.apply(alice, pl.PlanID, core.ApplyOptions{Confirmation: "a"}); v.State != core.StateCompleted {
		t.Errorf("state = %s, want completed", v.State)
	}
}

func TestApplyRevertiblePends(t *testing.T) {
	f := newFixture(t)
	pl := f.plan(alice, refSet, `{"name":"a","value":"1"}`)
	v := f.apply(alice, pl.PlanID, core.ApplyOptions{})
	if v.State != core.StatePending || v.Class != core.ClassRevertible {
		t.Fatalf("change = %+v, want a pending revertible change", v)
	}
	if want := f.clock.Now().Add(DefaultConfirmTimeout); v.RevertDeadline == nil || !v.RevertDeadline.Equal(want) {
		t.Errorf("revert deadline = %v, want %v", v.RevertDeadline, want)
	}
	var owner string
	if err := f.db.Read().QueryRow("SELECT owner FROM core_locks WHERE key = 'fix'").Scan(&owner); err != nil || owner != v.ID {
		t.Errorf("lock fix owner = %q, %v; want %s", owner, err, v.ID)
	}
	if got := f.phases(); !reflect.DeepEqual(got, []string{"apply/ok"}) {
		t.Errorf("journal = %v", got)
	}
	var ct []byte
	if err := f.db.Read().QueryRow("SELECT snapshot_ct FROM core_changes WHERE id = ?", v.ID).Scan(&ct); err != nil || ct == nil {
		t.Fatalf("snapshot = %v, %v; want a sealed snapshot", ct, err)
	}
	if _, err := f.eng.cfg.Base.Sealer.Open("core_changes.snapshot_ct."+v.ID, ct); err != nil {
		t.Errorf("snapshot does not open under its associated data: %v", err)
	}
}

func TestApplyFailures(t *testing.T) {
	cases := []struct {
		name    string
		ref     core.ActionRef
		input   string
		opts    core.ApplyOptions
		state   string
		code    string
		journal []string
		item    string
	}{
		{"compensable apply fails", refAdd, `{"name":"a","value":"1","failApply":true}`, core.ApplyOptions{},
			core.StateFailed, "internal", []string{"apply/error/internal"}, "old"},
		{"irreversible verify fails", refRemove, `{"name":"a","value":"1","failVerify":true}`, core.ApplyOptions{Confirmation: "a"},
			core.StateFailed, "internal", []string{"apply/error/internal"}, "1"},
		{"revertible apply fails and reverts", refSet, `{"name":"a","value":"1","failApply":true}`, core.ApplyOptions{},
			core.StateReverted, "internal", []string{"apply/error/internal"}, "old"},
		{"revertible verify fails and reverts", refSet, `{"name":"a","value":"1","failVerify":true}`, core.ApplyOptions{},
			core.StateReverted, "internal", []string{"apply/error/internal"}, "old"},
		{"revertible revert fails", refSet, `{"name":"a","value":"1","failVerify":true,"failRevert":true}`, core.ApplyOptions{},
			core.StateNeedsRepair, "core.needs_repair", []string{"revert/error/core.needs_repair"}, "1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.host.items["a"] = "old"
			pl := f.plan(alice, c.ref, c.input)
			v := f.apply(alice, pl.PlanID, c.opts)
			if v.State != c.state || v.Error == nil || v.Error.Code != c.code || v.Error.Message == "" {
				t.Errorf("change = %+v (error %+v), want %s with %s", v, v.Error, c.state, c.code)
			}
			if got := f.phases(); !reflect.DeepEqual(got, c.journal) {
				t.Errorf("journal = %v, want %v", got, c.journal)
			}
			if f.host.items["a"] != c.item {
				t.Errorf("item a = %q, want %q", f.host.items["a"], c.item)
			}
			// A change that needs repair keeps its locks and its plan's input.
			finished := c.state != core.StateNeedsRepair
			if n := f.count("SELECT count(*) FROM core_locks"); (n == 0) != finished {
				t.Errorf("locks = %d with the change %s", n, c.state)
			}
			if f.planInputCleared(pl.PlanID) != finished {
				t.Errorf("sealed input cleared = %v with the change %s", f.planInputCleared(pl.PlanID), c.state)
			}
		})
	}
}

func TestApplyErrorMessageHidesInternalCauses(t *testing.T) {
	// failApply returns a plain error; failVerify an internal errcode error
	// with a detail.
	for _, flag := range []string{"failApply", "failVerify"} {
		f := newFixture(t)
		v := f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1","`+flag+`":true}`).PlanID, core.ApplyOptions{})
		if v.Error == nil || v.Error.Message != "Internal error" {
			t.Errorf("%s: error = %+v, want the internal title only", flag, v.Error)
		}
	}
}

func TestApplyRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *fixture) // runs between plan and apply
		input string
		p     core.Principal
		opts  core.ApplyOptions
		code  errcode.Code
	}{
		{"principal mismatch", nil, `{"name":"a","value":"1"}`, bob, core.ApplyOptions{}, errcode.PlanPrincipalMismatch},
		{"expired", func(f *fixture) { f.clock.Advance(PlanTTL) }, `{"name":"a","value":"1"}`, alice, core.ApplyOptions{}, errcode.PlanExpired},
		{"state changed", func(f *fixture) { f.host.items["b"] = "x" }, `{"name":"a","value":"1"}`, alice, core.ApplyOptions{}, errcode.PlanStateChanged},
		{"guard passed then fails", func(f *fixture) { f.host.blocked = true }, `{"name":"a","value":"1"}`, alice, core.ApplyOptions{}, errcode.PlanStateChanged},
		{"confirm timeout too short", nil, `{"name":"a","value":"1"}`, alice, core.ApplyOptions{ConfirmTimeout: 9 * time.Second}, errcode.InvalidInput},
		{"confirm timeout too long", nil, `{"name":"a","value":"1"}`, alice, core.ApplyOptions{ConfirmTimeout: 15*time.Minute + time.Second}, errcode.InvalidInput},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			pl := f.plan(alice, refSet, c.input)
			if c.setup != nil {
				c.setup(f)
			}
			_, err := f.eng.Apply(context.Background(), c.p, pl.PlanID, c.opts)
			wantCode(t, err, c.code)
			if n := f.count("SELECT count(*) FROM core_changes"); n != 0 || f.host.applies != 0 {
				t.Errorf("changes = %d, applies = %d; want none", n, f.host.applies)
			}
		})
	}
}

func TestApplyUnknownPlan(t *testing.T) {
	f := newFixture(t)
	_, err := f.eng.Apply(context.Background(), alice, "01NOPLAN", core.ApplyOptions{})
	wantCode(t, err, errcode.PlanNotFound)
}

func TestApplyGuardFailedAtPlan(t *testing.T) {
	f := newFixture(t)
	f.host.blocked = true
	pl := f.plan(alice, refAdd, `{"name":"a","value":"1"}`)
	_, err := f.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{})
	wantCode(t, err, errcode.PlanGuardFailed)
}

func TestApplyConfirmTimeoutBounds(t *testing.T) {
	for _, d := range []time.Duration{MinConfirmTimeout, MaxConfirmTimeout} {
		f := newFixture(t)
		v := f.apply(alice, f.plan(alice, refSet, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{ConfirmTimeout: d})
		if want := f.clock.Now().Add(d); v.RevertDeadline == nil || !v.RevertDeadline.Equal(want) {
			t.Errorf("timeout %v: deadline %v, want %v", d, v.RevertDeadline, want)
		}
	}
}

func TestApplyLockHeld(t *testing.T) {
	f := newFixture(t)
	first := f.apply(alice, f.plan(alice, refSet, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	pl := f.plan(alice, refAdd, `{"name":"b","value":"2"}`)
	_, err := f.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{})
	wantCode(t, err, errcode.LockHeld)
	if n := f.count("SELECT count(*) FROM core_changes WHERE id != ?", first.ID); n != 0 {
		t.Errorf("changes besides the lock holder = %d, want 0", n)
	}
	if n := f.count("SELECT count(*) FROM core_locks WHERE owner = ?", first.ID); n != 1 {
		t.Errorf("locks of the holder = %d, want 1", n)
	}
}

// A plan made while another change holds its lock describes the state with
// that change applied, so its snapshot must not be taken until the lock is
// known to be free: if the holder were reverted between the snapshot and the
// lock, reverting this change would re-apply the reverted one.
func TestApplyLockHeldRefusedBeforeSnapshot(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	holder := f.apply(alice, f.plan(alice, refSet, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	pl := f.plan(alice, refSet, `{"name":"b","value":"2"}`)

	entered, release := make(chan struct{}), make(chan struct{})
	f.host.onSnapshot = func() {
		close(entered)
		<-release
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.eng.Apply(ctx, alice, pl.PlanID, core.ApplyOptions{})
		done <- err
	}()
	var err error
	select {
	case <-entered:
		if _, rerr := f.eng.Revert(ctx, alice, holder.ID); rerr != nil {
			t.Errorf("Revert holder: %v", rerr)
		}
		close(release)
		err = <-done
	case err = <-done:
	}
	wantCode(t, err, errcode.LockHeld)
	if n := f.count("SELECT count(*) FROM core_changes WHERE id != ?", holder.ID); n != 0 {
		t.Errorf("changes besides the lock holder = %d, want 0", n)
	}
}

func TestApplySamePlanConcurrentlyActsOnce(t *testing.T) {
	f := newFixture(t)
	pl := f.plan(alice, refAdd, `{"name":"a","value":"1"}`)
	ids := make([]string, 8)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Go(func() {
			v, err := f.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{})
			if err != nil {
				t.Error(err)
			}
			ids[i] = v.ID
		})
	}
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("change ids = %v, want one change", ids)
		}
	}
	if f.host.applies != 1 {
		t.Errorf("handler applied %d times, want 1", f.host.applies)
	}
}

func TestChangesFilters(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	f.apply(alice, f.plan(alice, refAdd, `{"name":"b","value":"1"}`).PlanID, core.ApplyOptions{})
	f.apply(alice, f.plan(alice, refSet, `{"name":"c","value":"1"}`).PlanID, core.ApplyOptions{})

	all, err := f.eng.Changes(ctx, core.ChangeFilter{})
	if err != nil || len(all) != 3 {
		t.Fatalf("Changes = %d, %v; want 3", len(all), err)
	}
	if all[0].ID <= all[1].ID || all[1].ID <= all[2].ID || all[0].State != core.StatePending {
		t.Errorf("changes not newest first: %s %s %s", all[0].ID, all[1].ID, all[2].ID)
	}
	filters := []struct {
		f    core.ChangeFilter
		want int
	}{
		{core.ChangeFilter{State: core.StatePending}, 1},
		{core.ChangeFilter{State: core.StateCompleted}, 2},
		{core.ChangeFilter{Before: all[0].ID}, 2},
		{core.ChangeFilter{Limit: 1}, 1},
		{core.ChangeFilter{HostID: testHost}, 3},
		{core.ChangeFilter{HostID: "other"}, 0},
	}
	for _, c := range filters {
		got, err := f.eng.Changes(ctx, c.f)
		if err != nil || len(got) != c.want {
			t.Errorf("Changes(%+v) = %d, %v; want %d", c.f, len(got), err, c.want)
		}
	}
}

func TestChangesLimitDefaultAndCap(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	at := formatTime(f.clock.Now())
	id := func(i int) string { return fmt.Sprintf("%026d", i) } // sorts like a ULID
	err := f.db.Write(ctx, func(tx *sql.Tx) error {
		for i := range maxChangeLimit + 1 {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash,
				 diff_json, guards_json, confirmation, created_at, expires_at)
				 VALUES (?, ?, ?, 1, ?, '', '', '[]', '[]', '', ?, ?)`,
				id(i), testHost, refAdd.Name, alice.Actor, at, at); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state,
				 input_json, created_at, updated_at)
				 VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?, '{}', ?, ?)`,
				id(i), id(i), testHost, refAdd.Name, string(core.ClassCompensable), refRemove.Name, alice.Actor,
				core.StateCompleted, at, at); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ limit, want int }{
		{0, defaultChangeLimit}, {-1, defaultChangeLimit}, {maxChangeLimit + 1, maxChangeLimit}, {maxChangeLimit, maxChangeLimit},
	} {
		got, err := f.eng.Changes(ctx, core.ChangeFilter{Limit: c.limit})
		if err != nil || len(got) != c.want {
			t.Errorf("Changes(Limit %d) = %d, %v; want %d", c.limit, len(got), err, c.want)
			continue
		}
		if got[0].ID != id(maxChangeLimit) {
			t.Errorf("Changes(Limit %d) starts at %s, want the newest %s", c.limit, got[0].ID, id(maxChangeLimit))
		}
	}
}
