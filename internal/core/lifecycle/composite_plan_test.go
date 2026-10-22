package lifecycle

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestCompositePlanPreview(t *testing.T) {
	f := newCfxFixture(t)
	v, err := f.planC(refCfxAdd, `{"name":"web"}`)
	if err != nil {
		t.Fatal(err)
	}
	if v.Action != refCfxAdd.Name || v.HostID != "" || v.Confirmation != "" {
		t.Errorf("plan = %+v, want a controller plan of %s without confirmation", v, refCfxAdd)
	}
	if len(v.Diff) != 3 ||
		v.Diff[0] != (core.DiffLine{Op: "+", Text: "[a] web-a=1"}) ||
		v.Diff[1] != (core.DiffLine{Op: "+", Text: "[b] web-b=2"}) ||
		v.Diff[2].Op != " " ||
		!strings.HasPrefix(v.Diff[2].Text, "planned after step a: cst.item.add/v1 ") ||
		!strings.Contains(v.Diff[2].Text, "${steps.a.output.name}") ||
		!strings.Contains(v.Diff[2].Text, "web-c") {
		t.Errorf("diff = %+v, want the dry-runs of a and b and c planned after step a", v.Diff)
	}
	if len(v.Guards) != 2 || v.Guards[0] != (core.Guard{ID: "a/free", OK: true, Message: "the host is blocked"}) ||
		v.Guards[1].ID != "b/free" {
		t.Errorf("guards = %+v, want the dry-runs' guards a/free and b/free", v.Guards)
	}
	var plans int
	if err := f.agentDB.Read().QueryRow("SELECT count(*) FROM core_plans").Scan(&plans); err != nil {
		t.Fatal(err)
	}
	if plans != 2 {
		t.Errorf("agent plans = %d, want the dry-runs of a and b only", plans)
	}
}

func TestCompositePlanReadStepLine(t *testing.T) {
	f := newCfxFixture(t)
	v, err := f.planC(refCfxGate, `{"name":"web"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Diff) < 2 || v.Diff[0] != (core.DiffLine{Op: " ", Text: "read step r: cst.item.get/v1"}) ||
		v.Diff[1] != (core.DiffLine{Op: "+", Text: "[a] web-a=1"}) {
		t.Errorf("diff = %+v, want the read step line, then a's dry-run", v.Diff)
	}
	last := v.Diff[len(v.Diff)-1]
	if !strings.HasPrefix(last.Text, "planned after step a: cst.gate.set/v1 ") {
		t.Errorf("last line = %+v, want g planned after step a", last)
	}
}

func TestCompositePlanStateHash(t *testing.T) {
	f := newCfxFixture(t)
	hash := func() string {
		t.Helper()
		v, err := f.planC(refCfxAdd, `{"name":"web"}`)
		if err != nil {
			t.Fatal(err)
		}
		var h string
		if err := f.ctrlDB.Read().QueryRow("SELECT state_hash FROM core_plans WHERE id = ?", v.PlanID).Scan(&h); err != nil {
			t.Fatal(err)
		}
		return h
	}
	first, second := hash(), hash()
	if len(first) != 64 || first != second {
		t.Fatalf("state hashes %q and %q, want one SHA-256 for one state", first, second)
	}
	f.host.setItem("web-a", "0")
	if third := hash(); third == first {
		t.Error("the state hash did not change when a dry-run's state did")
	}
}

func TestCompositePlanCarriesFailedGuards(t *testing.T) {
	f := newCfxFixture(t)
	f.host.setBlocked(true)
	v, err := f.planC(refCfxAdd, `{"name":"web"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Guards) != 2 || v.Guards[0].ID != "a/free" || v.Guards[0].OK {
		t.Errorf("guards = %+v, want a/free failing", v.Guards)
	}
}

func TestCompositeDeadline(t *testing.T) {
	f := newCfxFixture(t)
	cases := []struct {
		ref  core.ActionRef
		want string
	}{
		{refCfxAdd, (3*DefaultApplyTimeout + StepsGrace).String()},
		{refCfxGate, (2*DefaultApplyTimeout + 40*time.Second + StepsGrace).String()},
	}
	for _, c := range cases {
		def, ok := f.reg.Action(c.ref)
		if !ok {
			t.Fatalf("%s is not registered", c.ref)
		}
		_, steps, err := f.ctrl.compositePlan(context.Background(), cfxRoot, def, json.RawMessage(`{"name":"web"}`))
		if err != nil {
			t.Fatal(err)
		}
		if got := f.ctrl.compositeTimeout(steps).String(); got != c.want {
			t.Errorf("%s: steps deadline %s, want %s", c.ref, got, c.want)
		}
	}
}

func TestCompositePlanAcceptsASecretFromAStep(t *testing.T) {
	f := newCfxFixture(t)
	f.bad.set(
		cstStep("a", refCstAdd, `{"name":"x","value":"1"}`),
		cstStep("b", refCstAdd, `{"name":"y","value":"2","secret":"${steps.a.output.token}"}`, "a"),
	)
	if _, err := f.planC(refCfxBad, `{"name":"x"}`); err != nil {
		t.Fatalf("a secret field holding a step reference: %v", err)
	}
}

func TestCompositePlanRefusals(t *testing.T) {
	add := func(id, input string, after ...string) core.Step { return cstStep(id, refCstAdd, input, after...) }
	cases := []struct {
		name  string
		ref   core.ActionRef
		steps []core.Step
		code  errcode.Code
		text  string
	}{
		{"no steps", refCfxBad, nil, errcode.Internal, "no step"},
		{"bad id", refCfxBad, []core.Step{add("A", `{"name":"x"}`)}, errcode.Internal, `step id "A"`},
		{"duplicate id", refCfxBad, []core.Step{add("a", `{"name":"x"}`), add("a", `{"name":"y"}`)},
			errcode.Internal, "used twice"},
		{"after a later step", refCfxBad, []core.Step{add("a", `{"name":"x"}`, "b"), add("b", `{"name":"y"}`)},
			errcode.Internal, "not an earlier step"},
		{"reference without after", refCfxBad,
			[]core.Step{add("a", `{"name":"x"}`), add("b", `{"name":"${steps.a.output.name}"}`)},
			errcode.Internal, "does not follow"},
		{"composite step", refCfxBad,
			[]core.Step{{ID: "a", Action: refCfxAdd, Input: json.RawMessage(`{"name":"x"}`)}},
			errcode.Internal, "never composite"},
		{"irreversible step", refCfxBad, []core.Step{cstStep("a", refCstWipe, `{"name":"x"}`)},
			errcode.Internal, "never irreversible"},
		{"two revertible steps", refCfxBad,
			[]core.Step{cstStep("a", refCstGate, `{"name":"x"}`), cstStep("b", refCstGate, `{"name":"y"}`)},
			errcode.Internal, "at most one revertible"},
		{"revertible not last", refCfxBad,
			[]core.Step{cstStep("a", refCstGate, `{"name":"x"}`), add("b", `{"name":"y"}`)},
			errcode.Internal, "must be the last"},
		{"no undo handler in reverse", refCfxBad, []core.Step{cstStep("a", refCstDel, `{"name":"x"}`)},
			errcode.Internal, "no undo handler"},
		{"no convergence in forward", refCfxDrop, []core.Step{cstStep("a", refCstGate, `{"name":"x"}`)},
			errcode.Internal, "does not converge"},
		{"controller action with a host", refCfxBad, []core.Step{cstStep("a", refCstNote, `{"name":"x"}`)},
			errcode.Internal, "takes no host"},
		{"agent action without a host", refCfxBad,
			[]core.Step{{ID: "a", Action: refCstAdd, Input: json.RawMessage(`{"name":"x"}`)}},
			errcode.Internal, "needs a host"},
		{"unregistered action", refCfxBad,
			[]core.Step{cstStep("a", core.ActionRef{Name: "cst.item.nope", Version: 1}, `{"name":"x"}`)},
			errcode.Internal, "not registered"},
		{"literal secret", refCfxBad, []core.Step{add("a", `{"name":"x","secret":"hunter2"}`)},
			errcode.Internal, "secret"},
		{"missing input path", refCfxBad, []core.Step{add("a", `{"name":"${input.nope}"}`)},
			errcode.InvalidInput, ""},
		{"input against the child's schema", refCfxBad, []core.Step{add("a", `{"name":"x","bogus":1}`)},
			errcode.InvalidInput, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newCfxFixture(t)
			f.bad.set(c.steps...)
			_, err := f.planC(c.ref, `{"name":"x"}`)
			if got := errcode.From(err).Code; err == nil || got != c.code || !strings.Contains(err.Error(), c.text) {
				t.Fatalf("Plan = %v (code %s), want %s containing %q", err, got, c.code, c.text)
			}
		})
	}
}

func TestCompositePlanNeedsChildren(t *testing.T) {
	f := newCfxFixture(t)
	eng, err := New(f.ctrlConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	_, err = eng.Plan(context.Background(), cfxRoot, refCfxAdd, json.RawMessage(`{"name":"web"}`), core.PlanOptions{})
	if err == nil || !strings.Contains(err.Error(), "SetChildren") {
		t.Fatalf("Plan without children = %v, want an error naming SetChildren", err)
	}
}
