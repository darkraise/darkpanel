package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var (
	refDavStart = core.ActionRef{Name: "dav.thing.start", Version: 1}
	refDavList  = core.ActionRef{Name: "dav.thing.list", Version: 1}
	refDavNote  = core.ActionRef{Name: "dav.note.set", Version: 1}
)

type davHandler struct{}

func (davHandler) Read(_ context.Context, _ core.Env, in itemIn) (itemOut, error) {
	return itemOut{Value: in.Name}, nil
}

func (davHandler) Plan(context.Context, core.Env, itemIn) (core.PlanOutcome, error) {
	return core.PlanOutcome{StateHash: "dav"}, nil
}

func (davHandler) Apply(_ context.Context, _ core.Env, in itemIn) (itemOut, error) {
	return itemOut{Value: in.Value}, nil
}

func (davHandler) Verify(context.Context, core.Env, itemIn, itemOut) error { return nil }

// davModule has two agent actions and a controller-side action whose
// Available answers noteDown while it is set, counting its calls.
type davModule struct {
	noteDown *atomic.Value // string; "" is available
	asked    *atomic.Int32
}

func (m davModule) Manifest() core.Manifest {
	return core.Manifest{ID: "dav", Version: 1, DefaultEnabled: true, Actions: []core.ActionDef{
		core.DefineMutating(core.ActionSpec{Name: refDavStart.Name, Version: 1, Class: core.ClassCompensable,
			Side: core.SideAgent, ForwardOnly: true, Converges: true}, davHandler{}),
		core.DefineRead(core.ActionSpec{Name: refDavList.Name, Version: 1, Side: core.SideAgent}, davHandler{}),
		core.DefineMutating(core.ActionSpec{Name: refDavNote.Name, Version: 1, Class: core.ClassCompensable,
			Side: core.SideController, ForwardOnly: true, Converges: true,
			Available: func(context.Context, core.Env) error {
				m.asked.Add(1)
				if why, _ := m.noteDown.Load().(string); why != "" {
					return errors.New(why)
				}
				return nil
			}}, davHandler{}),
	}}
}

// davAgent is h1's runtime with its availability answered by answer and
// every ask counted.
type davAgent struct {
	mu     sync.Mutex
	answer core.ModuleAvailability
	err    error
	asked  int
}

func (a *davAgent) set(answer core.ModuleAvailability, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.answer, a.err = answer, err
}

func (a *davAgent) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.asked
}

func (a *davAgent) availability(_ context.Context, moduleID string) (core.ModuleAvailability, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.asked++
	if a.err != nil {
		return core.ModuleAvailability{}, a.err
	}
	m := a.answer
	m.Module = moduleID
	return m, nil
}

// davAnswer has dav.thing.start unavailable with reason and dav.thing.list
// available.
func davAnswer(reason string) core.ModuleAvailability {
	return core.ModuleAvailability{Available: true, Actions: []core.ActionAvailability{
		{Name: refDavStart.Name, Available: reason == "", Reason: reason},
		{Name: refDavList.Name, Available: true},
	}}
}

type davFixture struct {
	h     *harness
	d     *Dispatcher
	agent *davAgent
	down  *atomic.Value
	asked *atomic.Int32
}

func newDav(t *testing.T, more ...core.Module) *davFixture {
	t.Helper()
	f := &davFixture{agent: &davAgent{answer: davAnswer("")}, down: &atomic.Value{}, asked: &atomic.Int32{}}
	f.down.Store("")
	f.h = newHarnessWith(t, append([]core.Module{davModule{noteDown: f.down, asked: f.asked}}, more...)...)
	rt := f.h.rt
	f.h.clients.Add(testHost, &coretest.FakeAgent{
		HelloFunc: rt.Hello, ReadFunc: rt.Read, PlanFunc: rt.Plan, ApplyFunc: rt.Apply, ChangeFunc: rt.Change,
		SetModuleEnabledFunc: rt.SetModuleEnabled, AvailabilityFunc: f.agent.availability,
	})
	var err error
	if f.d, err = New(f.h.config(&coretest.MemAudit{})); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *davFixture) plan(ref core.ActionRef, hostID string) (core.PlanView, error) {
	return f.d.Plan(context.Background(), alice, hostID, ref, json.RawMessage(`{"name":"a","value":"1"}`), core.PlanOptions{})
}

func TestDispatchRefusesUnavailableAction(t *testing.T) {
	f := newDav(t)
	ctx := context.Background()
	pl, err := f.plan(refDavStart, testHost)
	if err != nil {
		t.Fatalf("Plan of an available action: %v", err)
	}
	f.agent.set(davAnswer("docker is down"), nil)
	f.h.clock.Advance(AvailabilityMaxAge + time.Second)
	const detail = "action dav.thing.start is unavailable on host h1: docker is down"
	for name, call := range map[string]func() error{
		"plan": func() error { _, err := f.plan(refDavStart, testHost); return err },
		"apply": func() error {
			_, err := f.d.Apply(ctx, alice, testHost, refDavStart, pl.PlanID, core.ApplyOptions{})
			return err
		},
	} {
		if e := wantCode(t, call(), errcode.ModuleUnavailable); e.Detail != detail {
			t.Errorf("%s: detail %q, want %q", name, e.Detail, detail)
		}
	}
	if n := f.h.count("SELECT count(*) FROM core_change_index"); n != 0 {
		t.Errorf("%d changes indexed, want the apply refused before the agent", n)
	}
	if out, err := f.d.Read(ctx, alice, testHost, refDavList, json.RawMessage(`{"name":"x"}`)); err != nil {
		t.Errorf("Read of an available action = %s, %v", out, err)
	}
	f.agent.set(core.ModuleAvailability{Available: true, Actions: []core.ActionAvailability{
		{Name: refDavList.Name, Reason: "the socket is gone"},
	}}, nil)
	f.h.clock.Advance(AvailabilityMaxAge + time.Second)
	_, err = f.d.Read(ctx, alice, testHost, refDavList, json.RawMessage(`{"name":"x"}`))
	if e := wantCode(t, err, errcode.ModuleUnavailable); e.Detail != "action dav.thing.list is unavailable on host h1: the socket is gone" {
		t.Errorf("read: detail %q", e.Detail)
	}
	f.agent.set(core.ModuleAvailability{Reason: "no docker"}, nil)
	f.h.clock.Advance(AvailabilityMaxAge + time.Second)
	_, err = f.plan(refDavStart, testHost)
	if e := wantCode(t, err, errcode.ModuleUnavailable); e.Detail != "action dav.thing.start is unavailable on host h1: no docker" {
		t.Errorf("a module-wide answer: detail %q", e.Detail)
	}
}

// davCoreModule is a core module with one agent read, so the registry knows
// the module an exempt action belongs to.
type davCoreModule struct{}

func (davCoreModule) Manifest() core.Manifest {
	return core.Manifest{ID: core.ModuleIDCore, Version: 1, DefaultEnabled: true, Actions: []core.ActionDef{
		core.DefineRead(core.ActionSpec{Name: "core.thing.get", Version: 1, Side: core.SideAgent}, davHandler{}),
	}}
}

// TestAvailabilityExemptsCore: an agent-side core action is dispatched
// whatever the agent's availability says, and the agent is not asked.
func TestAvailabilityExemptsCore(t *testing.T) {
	f := newDav(t, davCoreModule{})
	ctx := context.Background()
	f.agent.set(core.ModuleAvailability{Reason: "everything is down"}, nil)
	coreDef, ok := f.h.reg.Action(core.ActionRef{Name: "core.thing.get", Version: 1})
	if !ok {
		t.Fatal("core.thing.get is not registered")
	}
	if err := f.d.available(ctx, testHost, coreDef); err != nil {
		t.Errorf("a core action: %v, want it dispatched", err)
	}
	if n := f.agent.count(); n != 0 {
		t.Errorf("the agent was asked %d times for a core action, want 0", n)
	}
	davDef := core.ActionDef{Spec: core.ActionSpec{Name: refDavStart.Name, Version: 1, Side: core.SideAgent}}
	_ = wantCode(t, f.d.available(ctx, testHost, davDef), errcode.ModuleUnavailable)
}

func TestAvailabilityCached(t *testing.T) {
	f := newDav(t)
	ctx := context.Background()
	plan := func(want int) {
		t.Helper()
		if _, err := f.plan(refDavStart, testHost); err != nil {
			t.Fatal(err)
		}
		if n := f.agent.count(); n != want {
			t.Fatalf("the agent was asked %d times, want %d", n, want)
		}
	}
	plan(1)
	f.h.clock.Advance(AvailabilityMaxAge)
	plan(1)
	f.h.clock.Advance(time.Second)
	plan(2)

	f.h.clock.Advance(time.Second)
	if err := f.h.hosts.Hello(ctx, testHost); err != nil {
		t.Fatal(err)
	}
	plan(3)

	// core.module.enable rewrites the row's checked_at as a hello does.
	f.h.clock.Advance(time.Second)
	if _, err := f.h.db.Writer().ExecContext(ctx,
		"UPDATE core_module_state SET checked_at = ? WHERE host_id = ? AND module_id = 'dav'",
		f.h.clock.Now().UTC().Format(timeFormat), testHost); err != nil {
		t.Fatal(err)
	}
	plan(4)
	plan(4)
}

func TestControllerSideAvailable(t *testing.T) {
	f := newDav(t)
	if _, err := f.plan(refDavNote, ""); err != nil {
		t.Fatalf("Plan of an available controller action: %v", err)
	}
	f.down.Store("the vault is sealed")
	for range 2 {
		_, err := f.plan(refDavNote, "")
		if e := wantCode(t, err, errcode.ModuleUnavailable); e.Detail != "action dav.note.set is unavailable on the controller: the vault is sealed" {
			t.Errorf("detail %q", e.Detail)
		}
	}
	if n := f.asked.Load(); n != 3 {
		t.Errorf("Available ran %d times, want once per plan", n)
	}
	if n := f.agent.count(); n != 0 {
		t.Errorf("the agent was asked %d times about a controller action", n)
	}
}

func TestOlderAgentAvailability(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		asked int // after two plans
	}{
		{"an agent without the route", errcode.New(errcode.NotFound, "no agent API route GET /agent/v1/modules/dav/availability"), 1},
		{"an agent that cannot answer", errors.New("connection reset"), 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDav(t)
			f.agent.set(core.ModuleAvailability{}, tc.err)
			for range 2 {
				if _, err := f.plan(refDavStart, testHost); err != nil {
					t.Fatalf("Plan: %v, want every action available", err)
				}
			}
			if n := f.agent.count(); n != tc.asked {
				t.Errorf("the agent was asked %d times, want %d", n, tc.asked)
			}
		})
	}
}

func TestDispatcherAvailability(t *testing.T) {
	f := newDav(t)
	ctx := context.Background()
	f.agent.set(davAnswer("docker is down"), nil)
	want := davAnswer("docker is down")
	want.Module = "dav"
	for range 2 {
		got, err := f.d.Availability(ctx, testHost, "dav")
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("Availability = %+v, %v; want %+v", got, err, want)
		}
	}
	if _, err := f.plan(refDavStart, testHost); err == nil {
		t.Error("Plan passed; want the cached answer to refuse it")
	}
	if n := f.agent.count(); n != 1 {
		t.Errorf("the agent was asked %d times, want once", n)
	}
	f.agent.set(davAnswer(""), nil)
	f.h.clock.Advance(AvailabilityMaxAge + time.Second)
	if got, err := f.d.Availability(ctx, testHost, "dav"); err != nil || !got.Actions[0].Available || f.agent.count() != 2 {
		t.Errorf("Availability after %s = %+v, %v; want it asked again", AvailabilityMaxAge, got, err)
	}
	_, err := f.d.Availability(ctx, testHost, "nope")
	_ = wantCode(t, err, errcode.NotFound)
	f.agent.set(core.ModuleAvailability{}, errors.New("connection reset"))
	f.h.clock.Advance(AvailabilityMaxAge + time.Second)
	if _, err := f.d.Availability(ctx, testHost, "dav"); err == nil {
		t.Error("Availability of an agent that cannot answer succeeded")
	}
}
