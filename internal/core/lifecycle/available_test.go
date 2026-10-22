package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// avUnits is a units client that only says which module it was made for.
type avUnits struct {
	core.Units
	module string
}

// avHost records the Env.Units each Available call saw.
type avHost struct{ units []core.Units }

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

type avModule struct{ h *avHost }

func (m avModule) Manifest() core.Manifest {
	return core.Manifest{ID: "av", Version: 1, Actions: []core.ActionDef{
		core.DefineMutating(core.ActionSpec{
			Name: "av.thing.start", Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			ForwardOnly: true, Converges: true,
			Available: func(_ context.Context, env core.Env) error {
				m.h.units = append(m.h.units, env.Units)
				return errors.New("docker is down")
			},
		}, avHandler{}),
		core.DefineRead(core.ActionSpec{Name: "av.thing.list", Version: 1, Side: core.SideAgent}, avHandler{}),
		core.DefineRead(core.ActionSpec{Name: "av.thing.boom", Version: 1, Side: core.SideAgent,
			Available: func(context.Context, core.Env) error { panic("av boom") }}, avHandler{}),
		core.DefineRead(core.ActionSpec{Name: "av.note.get", Version: 1, Side: core.SideController,
			Available: func(context.Context, core.Env) error { return errors.New("never asked") }}, avHandler{}),
	}}
}

// newAvEngine is an agent engine over the fixture's database whose handlers
// of module m get avUnits{module: m}.
func newAvEngine(t *testing.T) (*Engine, *avHost) {
	t.Helper()
	h := &avHost{}
	f := newFixtureWith(t, avModule{h})
	cfg := f.config()
	cfg.ModuleUnits = func(module string) core.Units { return avUnits{module: module} }
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	return eng, h
}

func TestEngineAvailable(t *testing.T) {
	eng, h := newAvEngine(t)
	ctx := context.Background()
	cases := []struct {
		name   string
		ref    core.ActionRef
		reason string       // "" with code "": available
		code   errcode.Code // a coded refusal instead of a reason
	}{
		{"declared and failing", core.ActionRef{Name: "av.thing.start", Version: 1}, "docker is down", ""},
		{"not declared", core.ActionRef{Name: "av.thing.list", Version: 1}, "", ""},
		{"panicking", core.ActionRef{Name: "av.thing.boom", Version: 1}, "Available of av.thing.boom/v1 panicked", ""},
		{"unknown", core.ActionRef{Name: "av.thing.nope", Version: 1}, "", errcode.ActionUnknown},
		{"other side", core.ActionRef{Name: "av.note.get", Version: 1}, "", errcode.ActionUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := eng.Available(ctx, tc.ref)
			switch {
			case tc.code != "":
				wantCode(t, err, tc.code)
			case tc.reason == "" && err != nil:
				t.Errorf("Available = %v, want nil", err)
			case tc.reason != "" && (err == nil || err.Error() != tc.reason):
				t.Errorf("Available = %v, want %q", err, tc.reason)
			}
		})
	}
	if err := eng.Available(ctx, core.ActionRef{Name: "av.thing.list", Version: 1}); err != nil {
		t.Errorf("after a panic Available = %v, want the engine still serving", err)
	}
	if len(h.units) != 1 || h.units[0] != (avUnits{module: "av"}) {
		t.Errorf("Available saw units %v, want the av module's", h.units)
	}
	if err := eng.Available(ctx, core.ActionRef{Name: "av.thing.boom", Version: 1}); err == nil ||
		strings.Contains(err.Error(), "av boom") {
		t.Errorf("a panic answered %v, want the panic value left out", err)
	}
}
