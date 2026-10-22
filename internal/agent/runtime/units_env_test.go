package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// The test module "uex" owns the unit darkpanel-uex.service; each of its
// preflight check, resource and action handlers, and the read handler of
// the test core module, probes the unit client it got.

const uexUnit = "darkpanel-uex.service"

var (
	uexRefSet  = core.ActionRef{Name: "uex.unit.set", Version: 1}
	uexRefCore = core.ActionRef{Name: "core.units.get", Version: 1}
)

// uexUnits answers State for every unit; the scoped client decides which
// calls reach it.
type uexUnits struct{ core.Units }

func (uexUnits) State(context.Context, string) (core.UnitState, error) {
	return core.UnitState{LoadState: "loaded", ActiveState: "active"}, nil
}

type uexHost struct {
	mu  sync.Mutex
	saw map[string]string // call → what its unit client allowed
}

// probe records what env's unit client allows: "nil" without one, else
// whether State of the module's own unit and of nginx.service passed.
func (h *uexHost) probe(ctx context.Context, call string, env core.Env) {
	got := "nil"
	if env.Units != nil {
		_, own := env.Units.State(ctx, uexUnit)
		_, other := env.Units.State(ctx, "nginx.service")
		got = fmt.Sprintf("own=%v other=%v", own == nil, other == nil)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.saw[call] = got
}

func (h *uexHost) seen() map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return maps.Clone(h.saw)
}

type uexIn struct {
	Name string `json:"name"`
}

type uexSet struct{ h *uexHost }

func (x uexSet) Plan(context.Context, core.Env, uexIn) (core.PlanOutcome, error) {
	return core.PlanOutcome{StateHash: "uex"}, nil
}

func (x uexSet) Apply(ctx context.Context, env core.Env, in uexIn) (uexIn, error) {
	x.h.probe(ctx, "apply", env)
	return in, nil
}

func (x uexSet) Verify(context.Context, core.Env, uexIn, uexIn) error { return nil }

type uexCoreGet struct{ h *uexHost }

func (x uexCoreGet) Read(ctx context.Context, env core.Env, in uexIn) (uexIn, error) {
	x.h.probe(ctx, "core read", env)
	return in, nil
}

type uexModule struct{ h *uexHost }

func (m uexModule) Manifest() core.Manifest {
	return core.Manifest{
		ID: "uex", Version: 1, DefaultEnabled: true,
		OwnedPaths: []core.OwnedPath{{Path: "/etc/systemd/system/" + uexUnit, Kind: core.OwnedUnit}},
		Actions: []core.ActionDef{core.DefineMutating(core.ActionSpec{
			Name: uexRefSet.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{"uex"}, ForwardOnly: true, Converges: true,
		}, uexSet(m))},
		Resources: []core.ResourceDef{core.DefineResource("uex.status", func(ctx context.Context, env core.Env) (uexIn, error) {
			m.h.probe(ctx, "resource", env)
			return uexIn{}, nil
		})},
		Preflight: []core.PreflightCheck{{ID: "probe", Run: func(ctx context.Context, env core.Env) error {
			m.h.probe(ctx, "preflight", env)
			return nil
		}}},
	}
}

// uexCoreModule is a core module with one read action.
type uexCoreModule struct{ h *uexHost }

func (m uexCoreModule) Manifest() core.Manifest {
	return core.Manifest{ID: core.ModuleIDCore, Version: 1, Actions: []core.ActionDef{
		core.DefineRead(core.ActionSpec{Name: uexRefCore.Name, Version: 1, Side: core.SideAgent}, uexCoreGet(m)),
	}}
}

// uexRun opens a runtime over the uex modules with units as Config.Units,
// reads the uex resource, applies uex.unit.set and reads core.units.get,
// and returns what each handler's unit client allowed.
func uexRun(t *testing.T, units core.Units) map[string]string {
	t.Helper()
	h := &uexHost{saw: map[string]string{}}
	f := newFixtureWith(t, uexModule{h}, uexCoreModule{h})
	f.units = units
	f.restart()
	ctx := context.Background()
	if _, err := f.rt.Resource(ctx, alice, "uex.status"); err != nil {
		t.Fatal(err)
	}
	pl, err := f.rt.Plan(ctx, alice, uexRefSet, json.RawMessage(`{"name":"a"}`), core.PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if v, err := f.rt.Apply(ctx, alice, pl.PlanID, core.ApplyOptions{}); err != nil || v.State != core.StateCompleted {
		t.Fatalf("Apply = %+v, %v; want completed", v, err)
	}
	if _, err := f.rt.Read(ctx, alice, uexRefCore, json.RawMessage(`{"name":"a"}`)); err != nil {
		t.Fatal(err)
	}
	return h.seen()
}

func TestRuntimeScopesModuleUnits(t *testing.T) {
	scoped := "own=true other=false"
	want := map[string]string{"preflight": scoped, "resource": scoped, "apply": scoped, "core read": "own=true other=true"}
	if got := uexRun(t, uexUnits{}); !maps.Equal(got, want) {
		t.Errorf("unit clients allowed %v, want %v", got, want)
	}
}

func TestRuntimeWithoutUnits(t *testing.T) {
	want := map[string]string{"preflight": "nil", "resource": "nil", "apply": "nil", "core read": "nil"}
	if got := uexRun(t, nil); !maps.Equal(got, want) {
		t.Errorf("unit clients allowed %v, want %v", got, want)
	}
}
