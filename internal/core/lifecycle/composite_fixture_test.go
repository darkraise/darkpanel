package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// The composite fixture: module cst holds the step actions, all but one on
// the agent of testHost, and module cfx the composites, which run on a
// controller engine that reaches the agent engine through inprocChildren.
// Each engine has its own database and clock, so advancing the controller's
// clock never fires an agent timer.

var (
	refCstAdd  = core.ActionRef{Name: "cst.item.add", Version: 1}  // compensable, undo handler, converges
	refCstDel  = core.ActionRef{Name: "cst.item.del", Version: 1}  // compensable, forward only, converges
	refCstGet  = core.ActionRef{Name: "cst.item.get", Version: 1}  // read
	refCstWipe = core.ActionRef{Name: "cst.item.wipe", Version: 1} // irreversible
	refCstGate = core.ActionRef{Name: "cst.gate.set", Version: 1}  // revertible
	refCstFix  = core.ActionRef{Name: "cst.gate.fix", Version: 1}  // cst.gate.set's RepairedBy
	refCstNote = core.ActionRef{Name: "cst.note.get", Version: 1}  // controller-side read

	refCfxAdd    = core.ActionRef{Name: "cfx.deploy.add", Version: 1}    // reverse: a, b, c
	refCfxRemove = core.ActionRef{Name: "cfx.deploy.remove", Version: 1} // forward: x, y
	refCfxGate   = core.ActionRef{Name: "cfx.gate.add", Version: 1}      // reverse: read r, a, revertible g
	refCfxBad    = core.ActionRef{Name: "cfx.bad.run", Version: 1}       // reverse, steps set by the test
	refCfxDrop   = core.ActionRef{Name: "cfx.bad.drop", Version: 1}      // forward, steps set by the test

	cfxRoot = core.Principal{Actor: "user:root", UserID: "root", Scope: core.ScopeAdmin, Source: "test"}
)

// cstHost is the state the cst actions manage on testHost.
type cstHost struct {
	mu       sync.Mutex
	items    map[string]string
	gates    map[string]string
	failures map[string]int // the next n applies of a name fail before they change anything
	blocked  bool           // cst.item.add's free guard fails while set
	onPlan   func(name string)
	onApply  func(name string)
}

func newCstHost() *cstHost {
	return &cstHost{items: map[string]string{}, gates: map[string]string{}, failures: map[string]int{}}
}

func (h *cstHost) failing(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failures[name] > 0 {
		h.failures[name]--
		return true
	}
	return false
}

func (h *cstHost) setBlocked(v bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.blocked = v
}

func (h *cstHost) planning(name string) {
	h.mu.Lock()
	f := h.onPlan
	h.mu.Unlock()
	if f != nil {
		f(name)
	}
}

func (h *cstHost) applying(name string) {
	h.mu.Lock()
	f := h.onApply
	h.mu.Unlock()
	if f != nil {
		f(name)
	}
}

func (h *cstHost) item(name string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.items[name]
	return v, ok
}

func (h *cstHost) setItem(name, value string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.items[name] = value
}

func (h *cstHost) gate(name string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.gates[name]
	return v, ok
}

type cstIn struct {
	Name   string `json:"name"`
	Value  string `json:"value,omitempty"`
	Secret string `json:"secret,omitempty" secret:"true"`
	Remote bool   `json:"remote,omitempty"` // the add's effect is not Local
}

type cstOut struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Token string `json:"token" secret:"true"`
}

type cstView struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type cstFixIn struct {
	Outcome string `json:"outcome,omitempty"`
}

type cstFixOut struct {
	RepairOutcome string `json:"repairOutcome"`
}

type cstGateSnap struct {
	Name, Value string
	Present     bool
}

var errCstFailed = errors.New("cst: the apply failed")

func cstJSON(v string) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// cstAdd creates an item and journals the creation, so the engine can undo
// it.
type cstAdd struct{ h *cstHost }

func (x cstAdd) Plan(_ context.Context, _ core.Env, in cstIn) (core.PlanOutcome, error) {
	x.h.planning(in.Name)
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	diff := []core.DiffLine{{Op: "+", Text: in.Name + "=" + in.Value}}
	if old, ok := x.h.items[in.Name]; ok {
		diff = append([]core.DiffLine{{Op: "-", Text: in.Name + "=" + old}}, diff...)
	}
	return core.PlanOutcome{
		Diff:      diff,
		Guards:    []core.Guard{{ID: "free", OK: !x.h.blocked, Message: "the host is blocked"}},
		StateHash: "item=" + x.h.items[in.Name],
	}, nil
}

func (x cstAdd) Apply(ctx context.Context, env core.Env, in cstIn) (cstOut, error) {
	x.h.applying(in.Name)
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectCreate, Resource: "cst.item:?" + in.Name,
		Recovery: &core.RecoveryID{Kind: "name", Value: in.Name},
		Local:    !in.Remote, InverseLocal: true,
		Before: core.StateDoc{State: json.RawMessage("null")},
		After:  &core.StateDoc{State: cstJSON(in.Value)},
	}, nil)
	if err != nil {
		return cstOut{}, err
	}
	if x.h.failing(in.Name) {
		return cstOut{}, errCstFailed
	}
	x.h.setItem(in.Name, in.Value)
	if err := env.Effects.Made(ctx, seq, "cst.item:"+in.Name, core.StateDoc{State: cstJSON(in.Value)}); err != nil {
		return cstOut{}, err
	}
	return cstOut{Name: in.Name, Value: in.Value, Token: "tok-" + in.Name}, nil
}

func (x cstAdd) Verify(_ context.Context, _ core.Env, in cstIn, _ cstOut) error {
	if v, ok := x.h.item(in.Name); !ok || v != in.Value {
		return errors.New("cst: the item is missing")
	}
	return nil
}

func (x cstAdd) Observe(_ context.Context, _ core.Env, e core.Effect) (core.Observation, error) {
	_, identity, _ := strings.Cut(e.Resource, ":")
	name := strings.TrimPrefix(identity, "?")
	v, ok := x.h.item(name)
	if !ok {
		return core.Observation{Current: core.StateDoc{State: json.RawMessage("null")}, Exclusive: true}, nil
	}
	return core.Observation{
		Current: core.StateDoc{State: cstJSON(v)}, Resource: "cst.item:" + name, Matches: 1, Exclusive: true,
	}, nil
}

func (x cstAdd) Inverse(_ context.Context, _ core.Env, e core.Effect) (core.InverseResult, error) {
	_, name, _ := strings.Cut(e.Resource, ":")
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	delete(x.h.items, name)
	return core.InverseResult{}, nil
}

// cstDel removes an item; repeated, it ends in the same state.
type cstDel struct{ h *cstHost }

func (x cstDel) Plan(_ context.Context, _ core.Env, in cstIn) (core.PlanOutcome, error) {
	v, ok := x.h.item(in.Name)
	if !ok {
		return core.PlanOutcome{Diff: []core.DiffLine{}, StateHash: "absent"}, nil
	}
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: "-", Text: in.Name + "=" + v}}, StateHash: "item=" + v}, nil
}

func (x cstDel) Apply(_ context.Context, _ core.Env, in cstIn) (cstView, error) {
	x.h.applying(in.Name)
	if x.h.failing(in.Name) {
		return cstView{}, errCstFailed
	}
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	delete(x.h.items, in.Name)
	return cstView{Name: in.Name}, nil
}

func (x cstDel) Verify(_ context.Context, _ core.Env, in cstIn, _ cstView) error {
	if _, ok := x.h.item(in.Name); ok {
		return errors.New("cst: the item is still there")
	}
	return nil
}

type cstGet struct{ h *cstHost }

func (x cstGet) Read(_ context.Context, _ core.Env, in cstIn) (cstView, error) {
	v, _ := x.h.item(in.Name)
	return cstView{Name: in.Name, Value: v}, nil
}

type cstWipe struct{}

func (cstWipe) Plan(context.Context, core.Env, cstIn) (core.PlanOutcome, error) {
	return core.PlanOutcome{Diff: []core.DiffLine{}, StateHash: "wipe", Confirmation: "wipe"}, nil
}

func (cstWipe) Apply(context.Context, core.Env, cstIn) (cstView, error) { return cstView{}, nil }

func (cstWipe) Verify(context.Context, core.Env, cstIn, cstView) error { return nil }

// cstGate sets a gate and reverts it from a snapshot.
type cstGate struct{ h *cstHost }

func (x cstGate) Plan(_ context.Context, _ core.Env, in cstIn) (core.PlanOutcome, error) {
	v, _ := x.h.gate(in.Name)
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: "+", Text: "gate " + in.Name + "=" + in.Value}}, StateHash: "gate=" + v}, nil
}

func (x cstGate) Apply(_ context.Context, _ core.Env, in cstIn) (cstView, error) {
	x.h.applying(in.Name)
	if x.h.failing(in.Name) {
		return cstView{}, errCstFailed
	}
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	x.h.gates[in.Name] = in.Value
	return cstView{Name: in.Name, Value: in.Value}, nil
}

func (x cstGate) Verify(_ context.Context, _ core.Env, in cstIn, _ cstView) error {
	if v, ok := x.h.gate(in.Name); !ok || v != in.Value {
		return errors.New("cst: the gate is not set")
	}
	return nil
}

func (x cstGate) Snapshot(_ context.Context, _ core.Env, in cstIn) ([]byte, error) {
	v, ok := x.h.gate(in.Name)
	return json.Marshal(cstGateSnap{Name: in.Name, Value: v, Present: ok})
}

func (x cstGate) Revert(_ context.Context, _ core.Env, snapshot []byte) (core.RevertOutcome, error) {
	var s cstGateSnap
	if err := json.Unmarshal(snapshot, &s); err != nil {
		return core.RevertOutcome{}, err
	}
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	if s.Present {
		x.h.gates[s.Name] = s.Value
	} else {
		delete(x.h.gates, s.Name)
	}
	return core.RevertOutcome{}, nil
}

type cstFix struct{}

func (cstFix) Plan(context.Context, core.Env, cstFixIn) (core.PlanOutcome, error) {
	return core.PlanOutcome{Diff: []core.DiffLine{}, StateHash: "fix"}, nil
}

func (cstFix) Apply(context.Context, core.Env, cstFixIn) (cstFixOut, error) {
	return cstFixOut{RepairOutcome: core.RepairKept}, nil
}

func (cstFix) Verify(context.Context, core.Env, cstFixIn, cstFixOut) error { return nil }

type cstNote struct{}

func (cstNote) Read(context.Context, core.Env, cstIn) (cstView, error) { return cstView{}, nil }

type cstModule struct{ h *cstHost }

func (m cstModule) Manifest() core.Manifest {
	return core.Manifest{ID: "cst", Version: 1, Actions: []core.ActionDef{
		core.DefineMutating(core.ActionSpec{
			Name: refCstAdd.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{"cst"}, Converges: true,
		}, cstAdd(m)),
		core.DefineMutating(core.ActionSpec{
			Name: refCstDel.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{"cst"}, ForwardOnly: true, Converges: true,
		}, cstDel(m)),
		core.DefineRead(core.ActionSpec{Name: refCstGet.Name, Version: 1, Side: core.SideAgent}, cstGet(m)),
		core.DefineMutating(core.ActionSpec{
			Name: refCstWipe.Name, Version: 1, Class: core.ClassIrreversible, Side: core.SideAgent, LockKeys: []string{"cst"},
		}, cstWipe{}),
		core.DefineRevertible(core.ActionSpec{
			Name: refCstGate.Name, Version: 1, Side: core.SideAgent, LockKeys: []string{"cst-gate"},
			RepairedBy: refCstFix.Name, ApplyTimeout: 40 * time.Second,
		}, cstGate(m)),
		core.DefineMutating(core.ActionSpec{
			Name: refCstFix.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{"cst-gate"}, ForwardOnly: true, Converges: true,
		}, cstFix{}),
		core.DefineRead(core.ActionSpec{Name: refCstNote.Name, Version: 1, Side: core.SideController}, cstNote{}),
	}}
}

type cfxIn struct {
	Name string `json:"name"`
}

type cfxOut struct {
	Steps map[string]json.RawMessage `json:"steps"`
}

type cfxComposer struct{ steps func() []core.Step }

func (c cfxComposer) Steps(context.Context, core.Env, cfxIn) ([]core.Step, error) {
	return c.steps(), nil
}

func (cfxComposer) Output(_ context.Context, _ core.Env, _ cfxIn, steps map[string]json.RawMessage) (cfxOut, error) {
	return cfxOut{Steps: steps}, nil
}

// cfxSteps are the steps of cfx.bad.run and cfx.bad.drop, set by a test.
type cfxSteps struct {
	mu    sync.Mutex
	steps []core.Step
}

func (s *cfxSteps) set(steps ...core.Step) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps = steps
}

func (s *cfxSteps) get() []core.Step {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.steps)
}

// cstStep is a step of a cst action on testHost.
func cstStep(id string, ref core.ActionRef, input string, after ...string) core.Step {
	return core.Step{ID: id, Host: testHost, Action: ref, Input: json.RawMessage(input), After: after}
}

type cfxModule struct{ bad *cfxSteps }

func (m cfxModule) Manifest() core.Manifest {
	fixed := func(steps ...core.Step) cfxComposer {
		return cfxComposer{steps: func() []core.Step { return slices.Clone(steps) }}
	}
	return core.Manifest{ID: "cfx", Version: 1, Actions: []core.ActionDef{
		core.DefineComposite(core.ActionSpec{Name: refCfxAdd.Name, Version: 1, Unwind: core.UnwindReverse}, fixed(
			cstStep("a", refCstAdd, `{"name":"${input.name}-a","value":"1"}`),
			cstStep("b", refCstAdd, `{"name":"${input.name}-b","value":"2"}`, "a"),
			cstStep("c", refCstAdd, `{"name":"${input.name}-c","value":"${steps.a.output.name}"}`, "b"),
		)),
		core.DefineComposite(core.ActionSpec{Name: refCfxRemove.Name, Version: 1, Unwind: core.UnwindForward}, fixed(
			cstStep("x", refCstDel, `{"name":"${input.name}-a"}`),
			cstStep("y", refCstDel, `{"name":"${input.name}-b"}`, "x"),
		)),
		core.DefineComposite(core.ActionSpec{Name: refCfxGate.Name, Version: 1, Unwind: core.UnwindReverse}, fixed(
			cstStep("r", refCstGet, `{"name":"${input.name}-a"}`),
			cstStep("a", refCstAdd, `{"name":"${input.name}-a","value":"1"}`, "r"),
			cstStep("g", refCstGate, `{"name":"${input.name}","value":"${steps.a.output.name}"}`, "a"),
		)),
		core.DefineComposite(core.ActionSpec{Name: refCfxBad.Name, Version: 1, Unwind: core.UnwindReverse},
			cfxComposer{steps: m.bad.get}),
		core.DefineComposite(core.ActionSpec{Name: refCfxDrop.Name, Version: 1, Unwind: core.UnwindForward},
			cfxComposer{steps: m.bad.get}),
	}}
}

var errLostAnswer = errors.New("cfx: the answer was lost")

// inprocChildren is the Children of the controller engine: hostID "" is the
// controller engine itself, testHost the agent engine. While unreachable,
// every testHost call answers host.offline; a method named in lose makes
// its call and then answers errLostAnswer once.
type inprocChildren struct {
	mu          sync.Mutex
	ctrl, agent *Engine
	unreachable bool
	lose        map[string]bool
	calls       []string // "<method> <host>", in call order
}

func (c *inprocChildren) setEngines(ctrl, agent *Engine) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctrl != nil {
		c.ctrl = ctrl
	}
	if agent != nil {
		c.agent = agent
	}
}

func (c *inprocChildren) on(method, hostID string) (*Engine, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, method+" "+hostID)
	switch {
	case hostID == "":
		return c.ctrl, nil
	case hostID != testHost || c.unreachable:
		return nil, errcode.New(errcode.HostOffline, "host "+hostID+" is offline")
	}
	return c.agent, nil
}

// owner finds the engine holding changeID, the agent's first.
func (c *inprocChildren) owner(method, changeID string) (*Engine, error) {
	agent, err := c.on(method, testHost)
	if err != nil {
		return nil, err
	}
	if _, err := agent.Change(context.Background(), changeID); err == nil {
		return agent, nil
	}
	return c.ctrl, nil
}

func (c *inprocChildren) answer(method string, err error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lose[method] {
		delete(c.lose, method)
		return errLostAnswer
	}
	return err
}

func (c *inprocChildren) Plan(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage, opts core.PlanOptions) (core.PlanView, error) {
	e, err := c.on("Plan", hostID)
	if err != nil {
		return core.PlanView{}, err
	}
	v, err := e.Plan(ctx, p, a, input, opts)
	if err = c.answer("Plan", err); err != nil {
		return core.PlanView{}, err
	}
	return v, nil
}

func (c *inprocChildren) Apply(ctx context.Context, p core.Principal, hostID string, _ core.ActionRef, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
	e, err := c.on("Apply", hostID)
	if err != nil {
		return core.ChangeView{}, err
	}
	v, err := e.Apply(ctx, p, planID, opts)
	if err = c.answer("Apply", err); err != nil {
		return core.ChangeView{}, err
	}
	return v, nil
}

func (c *inprocChildren) Read(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
	e, err := c.on("Read", hostID)
	if err != nil {
		return nil, err
	}
	out, err := e.Read(ctx, p, a, input)
	return out, c.answer("Read", err)
}

func (c *inprocChildren) Confirm(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	e, err := c.owner("Confirm", changeID)
	if err != nil {
		return core.ChangeView{}, err
	}
	v, err := e.Confirm(ctx, p, changeID)
	if err = c.answer("Confirm", err); err != nil {
		return core.ChangeView{}, err
	}
	return v, nil
}

func (c *inprocChildren) Revert(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	e, err := c.owner("Revert", changeID)
	if err != nil {
		return core.ChangeView{}, err
	}
	v, err := e.Revert(ctx, p, changeID)
	if err = c.answer("Revert", err); err != nil {
		return core.ChangeView{}, err
	}
	return v, nil
}

func (c *inprocChildren) Undo(ctx context.Context, p core.Principal, hostID, changeID string, call *core.CallContext) (core.ChangeView, error) {
	e, err := c.on("Undo", hostID)
	if err != nil {
		return core.ChangeView{}, err
	}
	v, err := e.Undo(ctx, p, changeID, call)
	if err = c.answer("Undo", err); err != nil {
		return core.ChangeView{}, err
	}
	return v, nil
}

func (c *inprocChildren) Release(ctx context.Context, p core.Principal, hostID, changeID, reason string, call *core.CallContext) (core.ReleaseResult, error) {
	e, err := c.on("Release", hostID)
	if err != nil {
		return core.ReleaseResult{}, err
	}
	r, err := e.Release(ctx, p, changeID, reason, call)
	if err = c.answer("Release", err); err != nil {
		return core.ReleaseResult{}, err
	}
	return r, nil
}

func (c *inprocChildren) Lookup(ctx context.Context, hostID, planID string, private bool) (core.PlanLookup, error) {
	e, err := c.on("Lookup", hostID)
	if err != nil {
		return core.PlanLookup{}, err
	}
	lk, err := e.Lookup(ctx, planID, private)
	if err = c.answer("Lookup", err); err != nil {
		return core.PlanLookup{}, err
	}
	return lk, nil
}

func (c *inprocChildren) Retire(ctx context.Context, hostID, planID string) (core.PlanLookup, error) {
	e, err := c.on("Retire", hostID)
	if err != nil {
		return core.PlanLookup{}, err
	}
	lk, err := e.Retire(ctx, planID)
	if err = c.answer("Retire", err); err != nil {
		return core.PlanLookup{}, err
	}
	return lk, nil
}

func (c *inprocChildren) AckPrivate(ctx context.Context, hostID, changeID string, final bool) error {
	e, err := c.on("AckPrivate", hostID)
	if err != nil {
		return err
	}
	return c.answer("AckPrivate", e.AckPrivate(ctx, changeID, final))
}

// cfxFixture is a controller engine running the cfx composites over an
// agent engine on testHost.
type cfxFixture struct {
	t                     *testing.T
	host                  *cstHost
	bad                   *cfxSteps
	reg                   *core.Registry
	ctrlClock, agentClock *coretest.FakeClock
	ctrlPath, agentPath   string
	ctrlDB, agentDB       *store.DB
	ctrl, agent           *Engine
	kids                  *inprocChildren
}

func newCfxFixture(t *testing.T) *cfxFixture {
	t.Helper()
	host, bad := newCstHost(), &cfxSteps{}
	reg, err := core.NewRegistry(cstModule{host}, cfxModule{bad})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	f := &cfxFixture{
		t: t, host: host, bad: bad, reg: reg,
		ctrlClock: coretest.NewFakeClock(start), agentClock: coretest.NewFakeClock(start),
		ctrlPath: filepath.Join(t.TempDir(), "controller.db"), agentPath: filepath.Join(t.TempDir(), "agent.db"),
		kids: &inprocChildren{lose: map[string]bool{}},
	}
	f.openAgent()
	f.openCtrl()
	return f
}

func (f *cfxFixture) openAgent() {
	f.t.Helper()
	f.agentDB = openDBAt(f.t, f.agentPath, store.SideAgent)
	eng, err := New(Config{
		Side: core.SideAgent, Registry: f.reg, Validator: coretest.Validator{}, Journal: NewAgentJournal(f.agentDB),
		Redact: testRedact,
		Base: core.Env{
			HostID: testHost, DB: f.agentDB, Sealer: coretest.Sealer{}, Clock: f.agentClock,
			Log: slog.New(slog.DiscardHandler),
		},
	})
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(eng.Close)
	if err := eng.Recover(context.Background()); err != nil {
		f.t.Fatal(err)
	}
	f.agent = eng
	f.kids.setEngines(nil, eng)
}

func (f *cfxFixture) ctrlConfig() Config {
	return Config{
		Side: core.SideController, Registry: f.reg, Validator: coretest.Validator{}, Journal: &coretest.MemJournal{},
		Redact: testRedact,
		Base: core.Env{
			DB: f.ctrlDB, Sealer: coretest.Sealer{}, Clock: f.ctrlClock, Log: slog.New(slog.DiscardHandler),
		},
	}
}

// openCtrl opens the controller database and a new controller engine over
// it with inprocChildren, and runs Recover; RecoverComposites is the test's.
func (f *cfxFixture) openCtrl() {
	f.t.Helper()
	f.ctrlDB = openDBAt(f.t, f.ctrlPath, store.SideController)
	eng, err := New(f.ctrlConfig())
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(eng.Close)
	f.kids.setEngines(eng, nil)
	eng.SetChildren(f.kids)
	if err := eng.Recover(context.Background()); err != nil {
		f.t.Fatal(err)
	}
	f.ctrl = eng
}

// planC plans a composite (or any controller action) as cfxRoot.
func (f *cfxFixture) planC(ref core.ActionRef, input string) (core.PlanView, error) {
	return f.ctrl.Plan(context.Background(), cfxRoot, ref, json.RawMessage(input), core.PlanOptions{})
}
