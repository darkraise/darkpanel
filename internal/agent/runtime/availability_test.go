package runtime

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

type avIn struct {
	Name string `json:"name"`
}

type avOut struct {
	OK bool `json:"ok"`
}

type avHandler struct{}

func (avHandler) Read(context.Context, core.Env, avIn) (avOut, error) { return avOut{OK: true}, nil }

func (avHandler) Plan(context.Context, core.Env, avIn) (core.PlanOutcome, error) {
	return core.PlanOutcome{StateHash: "av"}, nil
}

func (avHandler) Apply(context.Context, core.Env, avIn) (avOut, error) { return avOut{OK: true}, nil }

func (avHandler) Verify(context.Context, core.Env, avIn, avOut) error { return nil }

// avModule's preflight fails while noDocker is set. av.thing.stop v1 is
// unavailable and v2 available, so the answer shows which version was asked.
type avModule struct{ noDocker *atomic.Bool }

func (m avModule) Manifest() core.Manifest {
	compensable := func(name string, version int, available func(context.Context, core.Env) error) core.ActionDef {
		return core.DefineMutating(core.ActionSpec{Name: name, Version: version, Class: core.ClassCompensable,
			Side: core.SideAgent, ForwardOnly: true, Converges: true, Available: available}, avHandler{})
	}
	return core.Manifest{
		ID: "av", Version: 1, DefaultEnabled: true,
		Preflight: []core.PreflightCheck{{ID: "docker", Run: func(context.Context, core.Env) error {
			if m.noDocker.Load() {
				return errors.New("no docker")
			}
			return nil
		}}},
		Actions: []core.ActionDef{
			compensable("av.thing.start", 1, func(context.Context, core.Env) error { return errors.New("docker is down") }),
			core.DefineRead(core.ActionSpec{Name: "av.thing.list", Version: 1, Side: core.SideAgent}, avHandler{}),
			compensable("av.thing.stop", 1, func(context.Context, core.Env) error { return errors.New("v1 is retired") }),
			compensable("av.thing.stop", 2, nil),
			core.DefineRead(core.ActionSpec{Name: "av.note.get", Version: 1, Side: core.SideController}, avHandler{}),
		},
	}
}

func TestRuntimeAvailability(t *testing.T) {
	var noDocker atomic.Bool
	f := newFixtureWith(t, avModule{&noDocker})
	ctx := context.Background()
	got, err := f.rt.Availability(ctx, "av")
	if err != nil {
		t.Fatal(err)
	}
	want := core.ModuleAvailability{Module: "av", Available: true, Actions: []core.ActionAvailability{
		{Name: "av.thing.list", Available: true},
		{Name: "av.thing.start", Reason: "docker is down"},
		{Name: "av.thing.stop", Available: true},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Availability = %+v\nwant %+v", got, want)
	}

	noDocker.Store(true)
	got, err = f.rt.Availability(ctx, "av")
	if err != nil {
		t.Fatal(err)
	}
	want = core.ModuleAvailability{Module: "av", Reason: "no docker", Actions: []core.ActionAvailability{
		{Name: "av.thing.list", Reason: "no docker"},
		{Name: "av.thing.start", Reason: "no docker"},
		{Name: "av.thing.stop", Reason: "no docker"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Availability with a failing preflight = %+v\nwant %+v", got, want)
	}
	hello, err := f.rt.Hello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range hello.Modules {
		if m.ID == "av" && (m.Available || m.Reason != "no docker") {
			t.Errorf("Hello reports %+v, want the preflight Availability ran", m)
		}
	}

	_, err = f.rt.Availability(ctx, "nope")
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != errcode.NotFound {
		t.Errorf("Availability of an unknown module = %v, want not_found", err)
	}
}
