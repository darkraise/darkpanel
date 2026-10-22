package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// The fixture module "gfx" has one guarded action, gfx.rule.set, whose plan
// reports its two declared guards, and its repair action gfx.rule.fix.

var (
	refGfxSet = core.ActionRef{Name: "gfx.rule.set", Version: 1}
	refGfxFix = core.ActionRef{Name: "gfx.rule.fix", Version: 1}
)

// gfxInput is gfx.rule.set's input. Failing names the declared guards its
// plan fails unless overridden; Extra adds one passing guard per id, which
// gfx.rule.set does not declare.
type gfxInput struct {
	core.Guarded
	Name    string   `json:"name"`
	Failing []string `json:"failing,omitempty"`
	Extra   []string `json:"extra,omitempty"`
}

type gfxFixInput struct {
	Name string `json:"name"`
}

type gfxOut struct {
	Name string `json:"name"`
}

// gfxCall is one handler call of module gfx: its method and the Env.Call it
// saw.
type gfxCall struct {
	Method string
	Call   core.CallContext
}

// gfxHost is module gfx's host: the rules it holds, the extra guard ids every
// plan of gfx.rule.set reports passing, and every handler call.
type gfxHost struct {
	mu    sync.Mutex
	rules map[string]bool
	extra []string
	calls []gfxCall
}

func (h *gfxHost) saw(method string, env core.Env) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, gfxCall{Method: method, Call: env.Call})
}

// seen returns the handler calls so far.
func (h *gfxHost) seen() []gfxCall { //nolint:unused // shared helper for the tests of guarded actions in this package
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.calls)
}

// setExtra makes every later plan of gfx.rule.set report ids passing too.
func (h *gfxHost) setExtra(ids ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.extra = ids
}

type gfxSet struct{ host *gfxHost }

func (x gfxSet) Plan(_ context.Context, env core.Env, in gfxInput) (core.PlanOutcome, error) {
	x.host.saw("Plan", env)
	x.host.mu.Lock()
	defer x.host.mu.Unlock()
	out := core.PlanOutcome{
		Diff:      []core.DiffLine{{Op: "+", Text: "rule " + in.Name}},
		StateHash: fmt.Sprint(x.host.rules[in.Name]),
	}
	for _, id := range []string{"gfx.lockout", "gfx.quota"} {
		g := core.Guard{ID: id, OK: true, Message: id + " holds"}
		if slices.Contains(in.Failing, id) {
			g.OK, g.Message = false, id+" fails"
			if slices.Contains(in.Overrides, id) {
				g.OK, g.Overridden = true, true
			}
		}
		out.Guards = append(out.Guards, g)
	}
	for _, id := range slices.Concat(in.Extra, x.host.extra) {
		out.Guards = append(out.Guards, core.Guard{ID: id, OK: true, Message: id + " holds"})
	}
	return out, nil
}

func (x gfxSet) Apply(_ context.Context, env core.Env, in gfxInput) (gfxOut, error) {
	x.host.saw("Apply", env)
	x.host.mu.Lock()
	defer x.host.mu.Unlock()
	x.host.rules[in.Name] = true
	return gfxOut{Name: in.Name}, nil
}

func (x gfxSet) Verify(_ context.Context, env core.Env, in gfxInput, _ gfxOut) error {
	x.host.saw("Verify", env)
	x.host.mu.Lock()
	defer x.host.mu.Unlock()
	if !x.host.rules[in.Name] {
		return fmt.Errorf("rule %s is missing", in.Name)
	}
	return nil
}

func (x gfxSet) Snapshot(_ context.Context, env core.Env, in gfxInput) ([]byte, error) {
	x.host.saw("Snapshot", env)
	x.host.mu.Lock()
	defer x.host.mu.Unlock()
	return json.Marshal(map[string]bool{in.Name: x.host.rules[in.Name]})
}

func (x gfxSet) Revert(_ context.Context, env core.Env, snapshot []byte) (core.RevertOutcome, error) {
	x.host.saw("Revert", env)
	var before map[string]bool
	if err := json.Unmarshal(snapshot, &before); err != nil {
		return core.RevertOutcome{}, err
	}
	x.host.mu.Lock()
	defer x.host.mu.Unlock()
	for name, present := range before {
		if present {
			x.host.rules[name] = true
		} else {
			delete(x.host.rules, name)
		}
	}
	return core.RevertOutcome{}, nil
}

type gfxFix struct{ host *gfxHost }

func (x gfxFix) Plan(_ context.Context, env core.Env, in gfxFixInput) (core.PlanOutcome, error) {
	x.host.saw("Plan", env)
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: "+", Text: "rule " + in.Name}}}, nil
}

func (x gfxFix) Apply(_ context.Context, env core.Env, in gfxFixInput) (gfxOut, error) {
	x.host.saw("Apply", env)
	x.host.mu.Lock()
	defer x.host.mu.Unlock()
	x.host.rules[in.Name] = true
	return gfxOut(in), nil
}

func (x gfxFix) Verify(_ context.Context, env core.Env, _ gfxFixInput, _ gfxOut) error {
	x.host.saw("Verify", env)
	return nil
}

type gfxModule struct{ h *gfxHost }

func (m gfxModule) Manifest() core.Manifest {
	return core.Manifest{ID: "gfx", Version: 1, Actions: []core.ActionDef{
		core.DefineRevertible(core.ActionSpec{
			Name: refGfxSet.Name, Version: 1, Side: core.SideAgent, LockKeys: []string{"gfx"}, RepairedBy: refGfxFix.Name,
			Guards: []core.GuardSpec{{ID: "gfx.lockout", Overridable: true}, {ID: "gfx.quota"}},
		}, gfxSet{m.h}),
		core.DefineMutating(core.ActionSpec{
			Name: refGfxFix.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{"gfx"}, ForwardOnly: true, Converges: true,
		}, gfxFix{m.h}),
	}}
}

// newGfx is a fixture whose registry also has module gfx, and gfx's host.
func newGfx(t *testing.T) (*fixture, *gfxHost) {
	t.Helper()
	h := &gfxHost{rules: map[string]bool{}}
	return newFixtureWith(t, gfxModule{h}), h
}
