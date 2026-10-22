package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// The test module "rfx" has a revertible action repaired by its RepairedBy
// action, a converging action repaired by applying it again, and an action
// with no repair at all. Its handlers record the call context they see.

const rfxKey = "rfx"

var (
	refRfxSet      = core.ActionRef{Name: "rfx.item.set", Version: 1}      // revertible, RepairedBy rfx.item.repair
	refRfxRepair   = core.ActionRef{Name: "rfx.item.repair", Version: 1}   // compensable, forward only; reports repairOutcome
	refRfxConverge = core.ActionRef{Name: "rfx.item.converge", Version: 1} // compensable, Converges, forward only
	refRfxOnce     = core.ActionRef{Name: "rfx.item.once", Version: 1}     // compensable with a compensating action: no repair
)

type rfxHost struct {
	mu      sync.Mutex
	values  map[string]string
	calls   []string // "<action> <call> repairOf=<id>" for every Plan, Apply and the repair's Verify
	onApply func()   // runs at the start of the repair action's Apply, without mu held
}

func (h *rfxHost) saw(action, call string, env core.Env) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, fmt.Sprintf("%s %s repairOf=%s", action, call, env.Call.RepairOf))
}

func (h *rfxHost) sawCall(want string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Contains(h.calls, want)
}

func (h *rfxHost) hash() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	b, err := json.Marshal(h.values)
	if err != nil {
		panic(err)
	}
	return string(b)
}

type rfxIn struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
	Fail  bool   `json:"fail,omitempty"`
}

type rfxOut struct {
	Previous string `json:"previous"`
}

type rfxSnap struct {
	Name, Value string
	Present     bool
}

type rfxRepairIn struct {
	Outcome    string `json:"outcome,omitempty"`    // "keep" reports kept; anything else restored
	Fail       bool   `json:"fail,omitempty"`       // Apply fails before any call
	FailRemote bool   `json:"failRemote,omitempty"` // Apply intends a remote call, then fails
}

type rfxRepairOut struct {
	RepairOutcome string `json:"repairOutcome"`
}

type rfxSetHandler struct{ h *rfxHost }

func (x rfxSetHandler) Plan(_ context.Context, env core.Env, _ rfxIn) (core.PlanOutcome, error) {
	x.h.saw("set", "Plan", env)
	return core.PlanOutcome{StateHash: x.h.hash()}, nil
}

func (x rfxSetHandler) Apply(_ context.Context, env core.Env, in rfxIn) (rfxOut, error) {
	x.h.saw("set", "Apply", env)
	if in.Fail {
		return rfxOut{}, errors.New("rfx: set failed")
	}
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	prev := x.h.values[in.Name]
	x.h.values[in.Name] = in.Value
	return rfxOut{Previous: prev}, nil
}

func (x rfxSetHandler) Verify(context.Context, core.Env, rfxIn, rfxOut) error { return nil }

func (x rfxSetHandler) Snapshot(_ context.Context, _ core.Env, in rfxIn) ([]byte, error) {
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	v, ok := x.h.values[in.Name]
	return json.Marshal(rfxSnap{Name: in.Name, Value: v, Present: ok})
}

func (x rfxSetHandler) Revert(_ context.Context, _ core.Env, snapshot []byte) (core.RevertOutcome, error) {
	var s rfxSnap
	if err := json.Unmarshal(snapshot, &s); err != nil {
		return core.RevertOutcome{}, err
	}
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	if s.Present {
		x.h.values[s.Name] = s.Value
	} else {
		delete(x.h.values, s.Name)
	}
	return core.RevertOutcome{}, nil
}

type rfxPlainHandler struct {
	h    *rfxHost
	name string
}

func (x rfxPlainHandler) Plan(_ context.Context, env core.Env, _ rfxIn) (core.PlanOutcome, error) {
	x.h.saw(x.name, "Plan", env)
	return core.PlanOutcome{StateHash: x.h.hash()}, nil
}

func (x rfxPlainHandler) Apply(_ context.Context, env core.Env, in rfxIn) (rfxOut, error) {
	x.h.saw(x.name, "Apply", env)
	if in.Fail {
		return rfxOut{}, errors.New("rfx: " + x.name + " failed")
	}
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	prev := x.h.values[in.Name]
	x.h.values[in.Name] = in.Value
	return rfxOut{Previous: prev}, nil
}

func (x rfxPlainHandler) Verify(context.Context, core.Env, rfxIn, rfxOut) error { return nil }

type rfxRepairHandler struct{ h *rfxHost }

func (x rfxRepairHandler) Plan(_ context.Context, env core.Env, _ rfxRepairIn) (core.PlanOutcome, error) {
	x.h.saw("repair", "Plan", env)
	return core.PlanOutcome{StateHash: x.h.hash()}, nil
}

func (x rfxRepairHandler) Apply(ctx context.Context, env core.Env, in rfxRepairIn) (rfxRepairOut, error) {
	x.h.mu.Lock()
	hook := x.h.onApply
	x.h.mu.Unlock()
	if hook != nil {
		hook()
	}
	x.h.saw("repair", "Apply", env)
	switch {
	case in.Fail:
		return rfxRepairOut{}, errors.New("rfx: the repair failed")
	case in.FailRemote:
		if _, err := env.Effects.Intend(ctx, core.Effect{
			Kind: core.EffectModify, Resource: "rfx.item:remote", CommitWait: time.Minute,
			Before: core.StateDoc{State: json.RawMessage(`{"value":""}`)},
		}, nil); err != nil {
			return rfxRepairOut{}, err
		}
		return rfxRepairOut{}, errors.New("rfx: the remote call failed")
	case in.Outcome == "keep":
		return rfxRepairOut{RepairOutcome: core.RepairKept}, nil
	}
	return rfxRepairOut{RepairOutcome: core.RepairRestored}, nil
}

func (x rfxRepairHandler) Verify(_ context.Context, env core.Env, _ rfxRepairIn, _ rfxRepairOut) error {
	x.h.saw("repair", "Verify", env)
	return nil
}

func (x rfxRepairHandler) Observe(context.Context, core.Env, core.Effect) (core.Observation, error) {
	return core.Observation{Current: core.StateDoc{State: json.RawMessage(`{"value":""}`)}, Matches: 1, Exclusive: true}, nil
}

type rfxModule struct{ h *rfxHost }

func (m rfxModule) Manifest() core.Manifest {
	keys := []string{rfxKey}
	return core.Manifest{ID: "rfx", Version: 1, Actions: []core.ActionDef{
		core.DefineRevertible(core.ActionSpec{
			Name: refRfxSet.Name, Version: 1, Side: core.SideAgent, LockKeys: keys, RepairedBy: refRfxRepair.Name,
		}, rfxSetHandler(m)),
		core.DefineMutating(core.ActionSpec{
			Name: refRfxRepair.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: keys,
			ForwardOnly: true, Converges: true,
		}, rfxRepairHandler(m)),
		core.DefineMutating(core.ActionSpec{
			Name: refRfxConverge.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: keys,
			ForwardOnly: true, Converges: true,
		}, rfxPlainHandler{m.h, "converge"}),
		core.DefineMutating(core.ActionSpec{
			Name: refRfxOnce.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: keys,
			CompensatedBy: refRfxConverge.Name,
		}, rfxPlainHandler{m.h, "once"}),
	}}
}

type rfxFixture struct {
	*fixture
	rh *rfxHost
}

// newRfx returns an agent engine whose registry holds only the rfx module.
func newRfx(t *testing.T) *rfxFixture {
	t.Helper()
	h := &rfxHost{values: map[string]string{}}
	reg, err := core.NewRegistry(rfxModule{h})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		t: t, reg: reg, path: filepath.Join(t.TempDir(), "agent.db"),
		clock: coretest.NewFakeClock(time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)),
	}
	f.open()
	return &rfxFixture{fixture: f, rh: h}
}

// revertibleTarget applies rfx.item.set, confirms it, and leaves it in
// needs_repair holding the rfx lock, as a change whose revert failed is.
func (r *rfxFixture) revertibleTarget() core.ChangeView {
	r.t.Helper()
	v := r.apply(alice, r.plan(alice, refRfxSet, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	if _, err := r.eng.Confirm(context.Background(), alice, v.ID); err != nil {
		r.t.Fatal(err)
	}
	r.setNeedsRepair(v.ID, rfxKey)
	return v
}

// convergeTarget applies rfx.item.converge with input and leaves it in
// needs_repair, holding the rfx lock and its sealed input, as an apply cut
// off by a restart leaves it.
func (r *rfxFixture) convergeTarget(input string) core.ChangeView {
	r.t.Helper()
	v := r.apply(alice, r.plan(alice, refRfxConverge, input).PlanID, core.ApplyOptions{})
	r.setNeedsRepair(v.ID, rfxKey)
	var planID string
	if err := r.db.Read().QueryRow("SELECT plan_id FROM core_changes WHERE id = ?", v.ID).Scan(&planID); err != nil {
		r.t.Fatal(err)
	}
	sealed, err := coretest.Sealer{}.Seal(planInputAD(planID), []byte(input))
	if err != nil {
		r.t.Fatal(err)
	}
	r.sqlExec("UPDATE core_plans SET input_ct = ? WHERE id = ?", sealed, planID)
	return v
}

// planRepairsOf reads the change a plan repairs.
func (f *fixture) planRepairsOf(planID string) string {
	f.t.Helper()
	var repairs *string
	if err := f.db.Read().QueryRow("SELECT repairs FROM core_plans WHERE id = ?", planID).Scan(&repairs); err != nil {
		f.t.Fatal(err)
	}
	if repairs == nil {
		return ""
	}
	return *repairs
}

func TestRepairPlansTheRepairedByAction(t *testing.T) {
	r := newRfx(t)
	ctx := context.Background()
	target := r.revertibleTarget()

	res, err := r.eng.Repair(ctx, alice, target.ID, core.RepairRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Change != nil || res.Plan == nil || res.Plan.Action != refRfxRepair.Name || res.Plan.Version != 1 {
		t.Fatalf("repair = %+v, want a plan of %s", res, refRfxRepair)
	}
	if got := r.planRepairsOf(res.Plan.PlanID); got != target.ID {
		t.Errorf("the plan repairs %q, want %s", got, target.ID)
	}
	if want := "repair Plan repairOf=" + target.ID; !r.rh.sawCall(want) {
		t.Errorf("calls = %v, want %q", r.rh.calls, want)
	}
	if s := r.state(target.ID); s != core.StateNeedsRepair {
		t.Errorf("target = %s, want needs_repair: planning changes nothing", s)
	}

	keep, err := r.eng.Repair(ctx, alice, target.ID, core.RepairRequest{Input: json.RawMessage(`{"outcome":"keep"}`)})
	if err != nil || keep.Plan == nil || keep.Plan.PlanID == res.Plan.PlanID {
		t.Fatalf("repair with input = %+v, %v; want a new plan", keep, err)
	}
	_, err = r.eng.Repair(ctx, alice, target.ID, core.RepairRequest{Input: json.RawMessage(`{"bogus":1}`)})
	wantCode(t, err, errcode.InvalidInput)
}

func TestRepairPlansAConvergingReapply(t *testing.T) {
	r := newRfx(t)
	ctx := context.Background()
	target := r.convergeTarget(`{"name":"a","value":"1"}`)

	res, err := r.eng.Repair(ctx, alice, target.ID, core.RepairRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Plan == nil || res.Plan.Action != refRfxConverge.Name {
		t.Fatalf("repair = %+v, want a plan of %s", res, refRfxConverge)
	}
	if got := r.planRepairsOf(res.Plan.PlanID); got != target.ID {
		t.Errorf("the plan repairs %q, want %s", got, target.ID)
	}
	if want := "converge Plan repairOf=" + target.ID; !r.rh.sawCall(want) {
		t.Errorf("calls = %v, want %q", r.rh.calls, want)
	}
	_, err = r.eng.Repair(ctx, alice, target.ID, core.RepairRequest{Input: json.RawMessage(`{"name":"b"}`)})
	wantRefusal(t, err, errcode.InvalidInput, "without input")
}

func TestRepairUndoesAChangeWithAnUndoHandler(t *testing.T) {
	u := newUfx(t)
	c := u.change(ufxOp{ID: "a", Value: "1"})
	u.setNeedsRepair(c.ID, ufxKey)
	res, err := u.eng.Repair(context.Background(), bob, c.ID, core.RepairRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Plan != nil || res.Change == nil || res.Change.State != core.StateUndoing {
		t.Fatalf("repair = %+v, want the change undoing", res)
	}
	if v := u.settled(c.ID); v.State != core.StateReverted {
		t.Fatalf("change = %s, want reverted", v.State)
	}
}

func TestRepairRefusals(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		setup func(r *rfxFixture) string
		req   core.RepairRequest
		code  errcode.Code
		part  string
	}{
		{name: "an unresolved effect", setup: func(r *rfxFixture) string {
			target := r.revertibleTarget()
			r.putEffect(target.ID, 1, core.EffectUnresolved, core.ReasonUncertain)
			return target.ID
		}, code: errcode.NeedsRepair, part: "resolve its effects first"},
		{name: "an unresolved effect on a failed repair change", setup: func(r *rfxFixture) string {
			other := r.apply(alice, r.plan(alice, refRfxOnce, `{"name":"b"}`).PlanID, core.ApplyOptions{})
			target := r.revertibleTarget()
			r.sqlExec("UPDATE core_plans SET repairs = ? WHERE id = (SELECT plan_id FROM core_changes WHERE id = ?)",
				target.ID, other.ID)
			r.sqlExec("UPDATE core_changes SET state = ? WHERE id = ?", core.StateFailed, other.ID)
			r.putEffect(other.ID, 1, core.EffectUnresolved, core.ReasonUncertain)
			return target.ID
		}, code: errcode.NeedsRepair, part: "resolve its effects first"},
		{name: "a step decision on a single change", setup: func(r *rfxFixture) string { return r.revertibleTarget().ID },
			req: core.RepairRequest{Step: "one", As: "reverted"}, code: errcode.InvalidInput, part: "only a composite"},
		{name: "an input naming repairs", setup: func(r *rfxFixture) string { return r.revertibleTarget().ID },
			req: core.RepairRequest{Input: json.RawMessage(`{"repairs":"X"}`)}, code: errcode.InvalidInput, part: "repairs"},
		{name: "an input that is not an object", setup: func(r *rfxFixture) string { return r.revertibleTarget().ID },
			req: core.RepairRequest{Input: json.RawMessage(`[1]`)}, code: errcode.InvalidInput, part: "JSON object"},
		{name: "a repair change", setup: func(r *rfxFixture) string {
			target := r.revertibleTarget()
			r.sqlExec(`UPDATE core_plans SET repairs = 'TARGET1'
				WHERE id = (SELECT plan_id FROM core_changes WHERE id = ?)`, target.ID)
			return target.ID
		}, code: errcode.ChangeBusy, part: "repair change TARGET1"},
		{name: "an owned change", setup: func(r *rfxFixture) string {
			target := r.revertibleTarget()
			r.sqlExec("UPDATE core_changes SET recovery_owner = ? WHERE id = ?", core.OwnerResolve, target.ID)
			return target.ID
		}, code: errcode.ChangeBusy, part: "owned by resolve"},
		{name: "a change not in needs_repair", setup: func(r *rfxFixture) string {
			return r.apply(alice, r.plan(alice, refRfxOnce, `{"name":"b"}`).PlanID, core.ApplyOptions{}).ID
		}, code: errcode.ChangeNotPending, part: "completed"},
		{name: "a change with no repair", setup: func(r *rfxFixture) string {
			v := r.apply(alice, r.plan(alice, refRfxOnce, `{"name":"b"}`).PlanID, core.ApplyOptions{})
			r.setNeedsRepair(v.ID, rfxKey)
			return v.ID
		}, code: errcode.ChangeNotPending, part: "no repair"},
		{name: "a missing change", setup: func(*rfxFixture) string { return "NOPE" }, code: errcode.ChangeNotFound,
			part: "NOPE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRfx(t)
			id := tc.setup(r)
			res, err := r.eng.Repair(ctx, alice, id, tc.req)
			wantRefusal(t, err, tc.code, tc.part)
			if res.Plan != nil || res.Change != nil {
				t.Errorf("a refused repair answered %+v", res)
			}
		})
	}
}

// refuseEmptyInput is a validator that refuses the input {} of one action.
type refuseEmptyInput struct{ name string }

func (v refuseEmptyInput) ValidateInput(def core.ActionDef, raw json.RawMessage) error {
	if def.Spec.Name == v.name && strings.TrimSpace(string(raw)) == "{}" {
		return errcode.New(errcode.InvalidInput, "outcome is required")
	}
	return coretest.Validator{}.ValidateInput(def, raw)
}

func TestNewRefusesARepairedByActionThatNeedsInput(t *testing.T) {
	r := newRfx(t)
	cfg := r.config()
	cfg.Validator = refuseEmptyInput{name: refRfxRepair.Name}
	if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), refRfxRepair.Name) {
		t.Fatalf("New = %v, want an error naming %s", err, refRfxRepair.Name)
	}
	cfg.Validator = coretest.Validator{}
	eng, err := New(cfg)
	if err != nil {
		t.Fatalf("New with a validator that accepts {}: %v", err)
	}
	eng.Close()
}
