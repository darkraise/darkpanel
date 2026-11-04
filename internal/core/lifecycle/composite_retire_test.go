package lifecycle

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

var refRtPut = core.ActionRef{Name: "rtm.mark.put", Version: 1} // forward only, converges, records the call context it sees

// rtCall is one Plan or Apply of rtm.mark.put: the item it named and the Retired list of its call context. Run is set
// when a composite's runner made the call, not the composite's own dry run.
type rtCall struct {
	Phase, Name string
	Run         bool
	Retired     []string
}

type rtHost struct {
	mu    sync.Mutex
	calls []rtCall
}

func (h *rtHost) record(phase, name string, call core.CallContext) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, rtCall{Phase: phase, Name: name, Run: call.ParentChangeID != "", Retired: slices.Clone(call.Retired)})
}

// of returns the calls of a phase, by item name; dry selects the composite's own dry runs instead of the runner's.
func (h *rtHost) of(phase string, dry bool) map[string][]rtCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string][]rtCall{}
	for _, c := range h.calls {
		if c.Phase == phase && c.Run == !dry {
			out[c.Name] = append(out[c.Name], c)
		}
	}
	return out
}

type rtIn struct {
	Name string `json:"name"`
}

type rtOut struct {
	Name string `json:"name"`
}

type rtPut struct{ h *rtHost }

func (x rtPut) Plan(_ context.Context, env core.Env, in rtIn) (core.PlanOutcome, error) {
	x.h.record("plan", in.Name, env.Call)
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: "+", Text: in.Name}}, StateHash: "rt"}, nil
}

func (x rtPut) Apply(_ context.Context, env core.Env, in rtIn) (rtOut, error) {
	x.h.record("apply", in.Name, env.Call)
	return rtOut(in), nil
}

func (rtPut) Verify(context.Context, core.Env, rtIn, rtOut) error { return nil }

type rtModule struct{ h *rtHost }

func (m rtModule) Manifest() core.Manifest {
	return core.Manifest{ID: "rtm", Version: 1, Actions: []core.ActionDef{
		core.DefineMutating(core.ActionSpec{
			Name: refRtPut.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{"rtm"}, ForwardOnly: true, Converges: true,
		}, rtPut(m)),
	}}
}

// rtStep is a step of rtm.mark.put on testHost that retires exposure.
func rtStep(id, retires string, after ...string) core.Step {
	s := cstStep(id, refRtPut, `{"name":"`+id+`"}`, after...)
	s.Retires = retires
	return s
}

func TestRtRunnerGivesLaterStepsTheRetiredExposures(t *testing.T) {
	h := &rtHost{}
	f := newCfxFixture(t, rtModule{h})
	f.bad.set(
		rtStep("a", "exp1"),
		rtStep("b", "", "a"),
		rtStep("c", "exp2", "b"),
		rtStep("d", "exp1", "c"),
		rtStep("e", "", "d"),
	)
	pl, err := f.planC(refCfxDrop, `{"name":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	dry := h.of("plan", true)
	if len(dry) != 5 {
		t.Fatalf("the composite's dry run planned %d steps, want 5", len(dry))
	}
	for name, calls := range dry {
		for _, c := range calls {
			if len(c.Retired) != 0 {
				t.Errorf("the dry run of step %s had Retired %v, want none", name, c.Retired)
			}
		}
	}

	v, err := f.ctrl.Apply(context.Background(), cfxRoot, pl.PlanID, core.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if done := f.await(v.ID, core.StateCompleted, core.StateNeedsRepair, core.StateFailed, core.StateReverted); done.State != core.StateCompleted {
		t.Fatalf("composite = %s (%+v), want completed", done.State, done.Error)
	}
	want := map[string][]string{
		"a": nil,
		"b": {"exp1"},
		"c": {"exp1"},
		"d": {"exp1", "exp2"},
		"e": {"exp1", "exp2"},
	}
	for _, phase := range []string{"plan", "apply"} {
		got := h.of(phase, false)
		for name, retired := range want {
			if len(got[name]) == 0 {
				t.Errorf("%s of step %s never ran", phase, name)
			}
			for _, c := range got[name] {
				if !slices.Equal(c.Retired, retired) {
					t.Errorf("%s of step %s had Retired %v, want %v", phase, name, c.Retired, retired)
				}
			}
		}
	}
	steps := f.stepsOf(v.ID)
	for id, retires := range map[string]string{"a": "exp1", "b": "", "c": "exp2", "d": "exp1", "e": ""} {
		if steps[id].Retires != retires {
			t.Errorf("stored retires of step %s = %q, want %q", id, steps[id].Retires, retires)
		}
	}
}

func TestRtStepRetired(t *testing.T) {
	row := func(id string, ordinal int, state, retires string) stepRow {
		return stepRow{ID: id, Ordinal: ordinal, State: state, Retires: retires}
	}
	steps := []stepRow{
		row("a", 0, core.StepConfirmed, "exp1"),
		row("b", 1, core.StepApplied, "exp2"),
		row("c", 2, core.StepApplying, "exp3"),
		row("d", 3, core.StepPlanned, "exp4"),
		row("e", 4, core.StepApplied, "exp1"),
		row("f", 5, core.StepUndone, "exp5"),
		row("g", 6, core.StepRead, ""),
		row("h", 7, core.StepPending, ""),
	}
	for _, tc := range []struct {
		name string
		step int
		want []string
	}{
		{"the first step has nothing retired before it", 0, nil},
		{"a confirmed step counts", 1, []string{"exp1"}},
		{"an applying step does not count yet", 3, []string{"exp1", "exp2"}},
		{"a planned step does not count", 4, []string{"exp1", "exp2"}},
		{"an exposure retired twice is listed once, in ordinal order", 5, []string{"exp1", "exp2"}},
		{"an undone step does not count", 7, []string{"exp1", "exp2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stepRetired(steps, steps[tc.step]); !slices.Equal(got, tc.want) {
				t.Errorf("stepRetired = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRtRegistryRefusesAMisplacedRetires(t *testing.T) {
	retiring := func(s core.Step) core.Step {
		s.Retires = "exp1"
		return s
	}
	for _, tc := range []struct {
		name  string
		ref   core.ActionRef
		steps []core.Step
		want  string
	}{
		{"a reverse composite", refCfxBad,
			[]core.Step{retiring(cstStep("a", refCstAdd, `{"name":"x","value":"1"}`))},
			"step a: retires is allowed only on a step of a forward composite"},
		{"a read step", refCfxDrop,
			[]core.Step{cstStep("a", refCstDel, `{"name":"x"}`), retiring(cstStep("r", refCstGet, `{"name":"x"}`, "a"))},
			"step r: retires is not allowed on a read step"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCfxFixture(t)
			f.bad.set(tc.steps...)
			_, err := f.planC(tc.ref, `{"name":"x"}`)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Plan error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}
