package lifecycle

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var privateStepCall = &core.CallContext{ParentChangeID: "COMPOSITE1", ParentAction: "cfx.deploy.add", StepID: "one"}

// childAdd plans and applies fix.item.add as a composite's step.
func (f *fixture) childAdd(input string) (string, core.ChangeView) {
	f.t.Helper()
	pl, err := f.eng.Plan(context.Background(), alice, refAdd, json.RawMessage(input),
		core.PlanOptions{Call: privateStepCall})
	if err != nil {
		f.t.Fatal(err)
	}
	return pl.PlanID, f.apply(alice, pl.PlanID, core.ApplyOptions{Call: privateStepCall})
}

func (f *fixture) privateStored(changeID string) bool {
	f.t.Helper()
	return f.count(`SELECT count(*) FROM core_changes
		WHERE id = ? AND private_output_ct IS NOT NULL AND private_output_at IS NOT NULL`, changeID) == 1
}

func TestStepStoresItsPrivateOutput(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	planID, v := f.childAdd(`{"name":"a","value":"1"}`)
	if v.State != core.StateCompleted || v.ParentChangeID != privateStepCall.ParentChangeID {
		t.Fatalf("step = %s with parent %q, want completed with parent COMPOSITE1", v.State, v.ParentChangeID)
	}
	if !f.privateStored(v.ID) {
		t.Fatal("the step stored no private output")
	}
	var ct []byte
	if err := f.db.Read().QueryRow("SELECT private_output_ct FROM core_changes WHERE id = ?", v.ID).Scan(&ct); err != nil {
		t.Fatal(err)
	}
	if _, err := (coretest.Sealer{}).Open("core_changes.private_output_ct."+v.ID, ct); err != nil {
		t.Errorf("the private output does not open with its associated data: %v", err)
	}

	lk, err := f.eng.Lookup(ctx, planID, false)
	if err != nil || lk.Private != nil || string(lk.Output) != `{"previous":"","token":"[redacted]"}` {
		t.Errorf("lookup = %+v, %v; want the redacted output and no private one", lk, err)
	}
	lk, err = f.eng.Lookup(ctx, planID, true)
	if err != nil || string(lk.Private) != `{"previous":"","token":"tok-a"}` {
		t.Errorf("private lookup = %s, %v; want the unredacted output", lk.Private, err)
	}

	if err := f.eng.AckPrivate(ctx, v.ID, false); err != nil {
		t.Fatal(err)
	}
	if f.privateStored(v.ID) {
		t.Error("the acknowledged private output is still stored")
	}
	if lk, err = f.eng.Lookup(ctx, planID, true); err != nil || lk.Private != nil {
		t.Errorf("private lookup after the ack = %s, %v; want none", lk.Private, err)
	}
	if err := f.eng.AckPrivate(ctx, v.ID, false); err != nil {
		t.Errorf("a repeated ack: %v", err)
	}
	wantCode(t, f.eng.AckPrivate(ctx, "NOPE", false), errcode.ChangeNotFound)
	wantCode(t, f.eng.AckPrivate(ctx, "NOPE", true), errcode.ChangeNotFound)
}

func TestFinalAckClearsAStepsEffectSecrets(t *testing.T) {
	u := newUfx(t)
	ctx := context.Background()
	pl, err := u.eng.Plan(ctx, alice, refUfx, json.RawMessage(`{"ops":[{"id":"a","value":"1"}]}`),
		core.PlanOptions{Call: privateStepCall})
	if err != nil {
		t.Fatal(err)
	}
	step := u.apply(alice, pl.PlanID, core.ApplyOptions{Call: privateStepCall})
	top := u.change(ufxOp{ID: "b", Value: "1"})
	for _, id := range []string{step.ID, top.ID} {
		u.sqlExec("UPDATE core_effects SET secret_ct = X'01' WHERE change_id = ? AND state = ?", id, string(core.EffectMade))
	}
	secrets := func(id string) int {
		return u.count("SELECT count(*) FROM core_effects WHERE change_id = ? AND secret_ct IS NOT NULL", id)
	}
	if secrets(step.ID) != 1 || secrets(top.ID) != 1 {
		t.Fatalf("seeded secrets: step %d, top-level %d; want one made effect each", secrets(step.ID), secrets(top.ID))
	}
	if err := u.eng.AckPrivate(ctx, step.ID, false); err != nil {
		t.Fatal(err)
	}
	if secrets(step.ID) != 1 {
		t.Error("an ack that is not final cleared a step's secret")
	}
	if err := u.eng.AckPrivate(ctx, step.ID, true); err != nil {
		t.Fatal(err)
	}
	if secrets(step.ID) != 0 {
		t.Error("a final ack left the step's made effect its secret")
	}
	if err := u.eng.AckPrivate(ctx, top.ID, true); err != nil {
		t.Fatal(err)
	}
	if secrets(top.ID) != 1 {
		t.Error("a final ack cleared the secret of a change that is no step")
	}
}

func TestOnlyASecretStepOutputIsStored(t *testing.T) {
	f := newFixture(t)
	top := f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	if f.privateStored(top.ID) {
		t.Error("a top-level change stored a private output")
	}

	u := newUfx(t)
	pl, err := u.eng.Plan(context.Background(), alice, refUfx, json.RawMessage(`{"ops":[{"id":"a","value":"1"}]}`),
		core.PlanOptions{Call: privateStepCall})
	if err != nil {
		t.Fatal(err)
	}
	step := u.apply(alice, pl.PlanID, core.ApplyOptions{Call: privateStepCall})
	if u.privateStored(step.ID) {
		t.Error("a step whose output has no secret stored a private output")
	}
}

func TestPurgeClearsUnacknowledgedPrivateOutputs(t *testing.T) {
	f := newFixture(t)
	_, old := f.childAdd(`{"name":"a","value":"1"}`)
	f.clock.Advance(PrivateOutputTTL / 2)
	_, fresh := f.childAdd(`{"name":"b","value":"1"}`)
	f.clock.Advance(PrivateOutputTTL / 2)
	if err := f.eng.Purge(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.privateStored(old.ID) {
		t.Error("a private output 24 hours old is still stored")
	}
	if !f.privateStored(fresh.ID) {
		t.Error("a private output 12 hours old was cleared")
	}
}

func TestPurgeClearsSecretsOfChangesThatCanNoLongerBeUndone(t *testing.T) {
	u := newUfx(t)
	ctx := context.Background()
	first := u.change(ufxOp{ID: "a", Value: "1"}, ufxOp{ID: "b", Value: "1"})
	u.sqlExec("UPDATE core_effects SET secret_ct = X'01' WHERE change_id = ?", first.ID)
	u.setEffectState(first.ID, 2, core.EffectUnresolved, core.ReasonUncertain)
	secrets := func(seq int) int {
		return u.count("SELECT count(*) FROM core_effects WHERE change_id = ? AND seq = ? AND secret_ct IS NOT NULL",
			first.ID, seq)
	}
	if err := u.eng.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	if secrets(1) != 1 {
		t.Fatal("the purge cleared a secret while the change can still be undone")
	}
	u.change(ufxOp{ID: "a", Value: "2"})
	if err := u.eng.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	if secrets(1) != 0 {
		t.Error("the made effect kept its secret after a later change took its key")
	}
	if secrets(2) != 1 {
		t.Error("the purge cleared the secret of an effect that is not made")
	}
}
