package lifecycle

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// loseNext makes the next call of method commit and then answer
// errLostAnswer.
func (c *inprocChildren) loseNext(method string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lose[method] = true
}

// awaitingGate applies cfx.gate.add with a 15-minute confirm timeout and
// waits until it awaits confirmation.
func awaitingGate(t *testing.T, f *cfxFixture) (core.ChangeView, stepRow) {
	t.Helper()
	v := f.applyC(refCfxGate, `{"name":"web"}`, core.ApplyOptions{ConfirmTimeout: 15 * time.Minute})
	f.await(v.ID, core.StateAwaitingConfirmation)
	g := f.stepsOf(v.ID)["g"]
	if g.State != core.StepApplied || f.agentChange(g.ChildID).State != core.StatePending {
		t.Fatalf("step g = %s, child %s, want applied with a pending child", g.State, f.agentChange(g.ChildID).State)
	}
	return v, g
}

func TestCompositeConfirmCompletes(t *testing.T) {
	f := newCfxFixture(t)
	v, g := awaitingGate(t, f)
	got, err := f.ctrl.Confirm(context.Background(), cfxRoot, v.ID)
	if err != nil || got.State != core.StateCompleted || got.Owner != "" {
		t.Fatalf("Confirm = %s owner %q, %v; want completed and unowned", got.State, got.Owner, err)
	}
	if child := f.agentChange(g.ChildID); child.State != core.StateConfirmed {
		t.Errorf("child of g = %s, want confirmed", child.State)
	}
	if s := f.stepsOf(v.ID)["g"]; s.State != core.StepConfirmed {
		t.Errorf("step g = %s, want confirmed", s.State)
	}
	var out cfxOut
	if err := json.Unmarshal(got.Output, &out); err != nil {
		t.Fatal(err)
	}
	if _, hasRead := out.Steps["r"]; hasRead || len(out.Steps) != 2 {
		t.Errorf("output steps = %v, want a and g without the read step", out.Steps)
	}
	if v, _ := f.host.gate("web"); v != "web-a" {
		t.Errorf("gate web = %q, want web-a from step a's output", v)
	}
}

func TestCompositeRevertUnwinds(t *testing.T) {
	f := newCfxFixture(t)
	v, g := awaitingGate(t, f)
	if _, err := f.ctrl.Revert(context.Background(), cfxRoot, v.ID); err != nil {
		t.Fatal(err)
	}
	done := f.await(v.ID, core.StateReverted, core.StateCompleted, core.StateNeedsRepair)
	if done.State != core.StateReverted || done.Error == nil || done.Error.Code != string(errcode.ChildFailed) ||
		!strings.Contains(done.Error.Message, "step g ") || !strings.Contains(done.Error.Message, "reverted by "+cfxRoot.Actor) {
		t.Fatalf("composite = %s (%+v), want reverted naming step g and who reverted it", done.State, done.Error)
	}
	if child := f.agentChange(g.ChildID); child.State != core.StateReverted {
		t.Errorf("child of g = %s, want reverted", child.State)
	}
	if _, ok := f.host.gate("web"); ok {
		t.Error("gate web is still set")
	}
	if _, ok := f.host.item("web-a"); ok {
		t.Error("item web-a is still there")
	}
}

func TestCompositeConfirmAnswerLost(t *testing.T) {
	t.Run("the lookup settles it", func(t *testing.T) {
		f := newCfxFixture(t)
		v, _ := awaitingGate(t, f)
		f.kids.loseNext("Confirm")
		got, err := f.ctrl.Confirm(context.Background(), cfxRoot, v.ID)
		if err != nil || got.State != core.StateCompleted {
			t.Fatalf("Confirm = %s, %v; want completed from the lookup", got.State, err)
		}
	})
	t.Run("the runner settles it", func(t *testing.T) {
		f := newCfxFixture(t)
		v, _ := awaitingGate(t, f)
		f.kids.loseNext("Confirm")
		f.kids.loseNext("Lookup")
		got, err := f.ctrl.Confirm(context.Background(), cfxRoot, v.ID)
		if err != nil || got.State != core.StateAwaitingConfirmation {
			t.Fatalf("Confirm = %s, %v; want still awaiting confirmation", got.State, err)
		}
		// The background runner may settle the step between Confirm's answer
		// and this read, so only a state on the way to confirmed is checked
		// here; the runner's outcome is checked once the composite is done.
		if s := f.stepsOf(v.ID)["g"]; s.State != core.StepConfirming && s.State != core.StepConfirmed {
			t.Fatalf("step g = %s, want confirming or confirmed", s.State)
		}
		if done := f.await(v.ID, core.StateCompleted, core.StateReverted); done.State != core.StateCompleted {
			t.Fatalf("composite = %s, want completed", done.State)
		}
		g := f.stepsOf(v.ID)["g"]
		if g.State != core.StepConfirmed {
			t.Errorf("step g = %s, want confirmed", g.State)
		}
		if child := f.agentChange(g.ChildID); child.State != core.StateConfirmed {
			t.Errorf("child of g = %s, want confirmed", child.State)
		}
	})
}

func TestCompositeConfirmationWinsDuringUnwind(t *testing.T) {
	f := newCfxFixture(t)
	v, g := awaitingGate(t, f)
	if _, err := f.agent.Confirm(context.Background(), cfxRoot, g.ChildID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ctrl.Revert(context.Background(), cfxRoot, v.ID); err != nil {
		t.Fatal(err)
	}
	done := f.await(v.ID, core.StateCompleted, core.StateReverted, core.StateNeedsRepair)
	if done.State != core.StateCompleted || done.Error != nil {
		t.Fatalf("composite = %s (%+v), want completed without an error", done.State, done.Error)
	}
	if _, ok := f.host.item("web-a"); !ok {
		t.Error("item web-a was undone although confirmation won")
	}
}

// stepOutput opens step stepID's sealed output.
func stepOutput(t *testing.T, f *cfxFixture, id, stepID string) string {
	t.Helper()
	raw, err := f.ctrl.cfg.Base.Sealer.Open(stepOutputAD(id, stepID), f.stepsOf(id)[stepID].OutputCT)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestCompositeSealsAndAcksPrivateOutputs(t *testing.T) {
	for _, lose := range []bool{false, true} {
		t.Run(map[bool]string{false: "from the apply answer", true: "after a lost apply answer"}[lose], func(t *testing.T) {
			f := newCfxFixture(t)
			if lose {
				f.kids.loseNext("Apply")
			}
			v := f.applyC(refCfxAdd, `{"name":"web"}`, core.ApplyOptions{})
			if done := f.await(v.ID, core.StateCompleted, core.StateReverted); done.State != core.StateCompleted {
				t.Fatalf("composite = %s (%+v), want completed", done.State, done.Error)
			}
			for _, id := range []string{"a", "b", "c"} {
				if out := stepOutput(t, f, v.ID, id); !strings.Contains(out, `"token":"tok-web-`+id+`"`) {
					t.Errorf("step %s's sealed output = %s, want the unredacted token", id, out)
				}
			}
			var kept int
			if err := f.agentDB.Read().QueryRow(
				"SELECT count(*) FROM core_changes WHERE private_output_ct IS NOT NULL").Scan(&kept); err != nil {
				t.Fatal(err)
			}
			if kept != 0 {
				t.Errorf("%d private outputs are still on the agent, want every one acknowledged", kept)
			}
		})
	}
}

func TestCompositeTerminalClearsStepSecrets(t *testing.T) {
	f := newCfxFixture(t)
	secrets := func() int {
		t.Helper()
		var n int
		if err := f.agentDB.Read().QueryRow("SELECT count(*) FROM core_effects WHERE secret_ct IS NOT NULL").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// When step c applies, the children of steps a and b are made; their
	// effects get the sealed secret an inverse that needs one stores.
	f.host.mu.Lock()
	f.host.onApply = func(name string) {
		if name != "web-c" {
			return
		}
		res, err := f.agentDB.Writer().Exec("UPDATE core_effects SET secret_ct = X'01' WHERE state = ?",
			string(core.EffectMade))
		if err != nil {
			t.Error(err)
			return
		}
		if n, err := res.RowsAffected(); err != nil || n != 2 {
			t.Errorf("seeded the secrets of %d made effects (%v), want 2", n, err)
		}
	}
	f.host.mu.Unlock()
	v := f.applyC(refCfxAdd, `{"name":"web"}`, core.ApplyOptions{})
	if done := f.await(v.ID, core.StateCompleted, core.StateReverted); done.State != core.StateCompleted {
		t.Fatalf("composite = %s (%+v), want completed", done.State, done.Error)
	}
	deadline := time.Now().Add(10 * time.Second)
	for n := secrets(); n != 0; n = secrets() {
		if time.Now().After(deadline) {
			t.Fatalf("%d effects of the steps keep their secrets after the composite completed", n)
		}
		time.Sleep(time.Millisecond)
	}
}

// lossyChildren loses the answer of the loseAt-th Apply after deleting the
// child's private output, so the output is gone before it was sealed.
type lossyChildren struct {
	*inprocChildren
	db      *store.DB
	mu      sync.Mutex
	applies int
	loseAt  int
}

func (l *lossyChildren) Apply(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
	v, err := l.inprocChildren.Apply(ctx, p, hostID, a, planID, opts)
	l.mu.Lock()
	l.applies++
	n := l.applies
	l.mu.Unlock()
	if err != nil || n != l.loseAt {
		return v, err
	}
	if _, err := l.db.Writer().ExecContext(ctx,
		"UPDATE core_changes SET private_output_ct = NULL, private_output_at = NULL WHERE id = ?", v.ID); err != nil {
		return core.ChangeView{}, err
	}
	return core.ChangeView{}, errLostAnswer
}

func TestCompositeOutputLostUnwinds(t *testing.T) {
	f := newCfxFixture(t)
	f.ctrl.SetChildren(&lossyChildren{inprocChildren: f.kids, db: f.agentDB, loseAt: 2})
	v := f.applyC(refCfxAdd, `{"name":"web"}`, core.ApplyOptions{})
	done := f.await(v.ID, core.StateReverted, core.StateCompleted, core.StateNeedsRepair, core.StateFailed)
	if done.State != core.StateReverted || done.Error == nil || done.Error.Code != string(errcode.ChildFailed) ||
		!strings.Contains(done.Error.Message, "step b ") || !strings.Contains(done.Error.Message, "lost") {
		t.Fatalf("composite = %s (%+v), want reverted with core.child_failed: step b's output lost", done.State, done.Error)
	}
	b := f.stepsOf(v.ID)["b"]
	if b.State != core.StepUndone || f.agentChange(b.ChildID).State != core.StateReverted {
		t.Errorf("step b = %s with child %s, want undone with its child reverted", b.State, f.agentChange(b.ChildID).State)
	}
	for _, name := range []string{"web-a", "web-b"} {
		if _, ok := f.host.item(name); ok {
			t.Errorf("item %s is still there", name)
		}
	}
}
