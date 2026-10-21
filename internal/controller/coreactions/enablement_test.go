package coreactions

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// operator is a network principal with the admin scope.
var operator = core.Principal{Actor: "user:u1", UserID: "u1", Scope: core.ScopeAdmin, Source: "127.0.0.1"}

func (e *testEnv) plan(p core.Principal, name string, in any) (core.PlanView, error) {
	return e.engine.Plan(context.Background(), p, ref(name), encodeInput(e.t, in), core.PlanOptions{})
}

func (e *testEnv) apply(p core.Principal, planID, confirmation string) (core.ChangeView, error) {
	return e.engine.Apply(context.Background(), p, planID, core.ApplyOptions{Confirmation: confirmation})
}

// run plans and applies name, repeating the plan's confirmation phrase, and
// fails the test unless the change completes; it decodes the apply
// response's output into out when out is not nil.
func (e *testEnv) run(p core.Principal, name string, in, out any) core.ChangeView {
	e.t.Helper()
	pv, err := e.plan(p, name, in)
	if err != nil {
		e.t.Fatalf("plan %s: %v", name, err)
	}
	v, err := e.apply(p, pv.PlanID, pv.Confirmation)
	if err != nil {
		e.t.Fatalf("apply %s: %v", name, err)
	}
	if v.State != core.StateCompleted {
		e.t.Fatalf("apply %s: state %s, error %+v", name, v.State, v.Error)
	}
	if out != nil {
		if err := json.Unmarshal(v.Output, out); err != nil {
			e.t.Fatalf("%s output %s: %v", name, v.Output, err)
		}
	}
	return v
}

// verify runs name's Verify as startup recovery does: with a null output.
func (e *testEnv) verify(name string, in any) error {
	def, ok := e.reg.Action(ref(name))
	if !ok {
		e.t.Fatalf("%s is not registered", name)
	}
	env := core.Env{DB: e.db, Clock: e.clock, Principal: core.Principal{Actor: "system:recovery", Scope: core.ScopeAdmin}}
	return def.Handler.Verify(context.Background(), env, encodeInput(e.t, in), json.RawMessage("null"))
}

// preflight answers every Preflight with status and counts the calls.
func preflight(status core.ModuleStatus, calls *int) *coretest.FakeAgent {
	return &coretest.FakeAgent{PreflightFunc: func(_ context.Context, moduleID string) (core.ModuleStatus, error) {
		*calls++
		status.ID = moduleID
		return status, nil
	}}
}

func newModuleEnv(t *testing.T) *testEnv {
	e := newTestEnv(t, fakeModule{"demo", 3})
	e.addHost("h1", "alpha", true)
	return e
}

func TestModuleEnable(t *testing.T) {
	e := newModuleEnv(t)
	e.setModuleState("h1", "demo", false, true, "")
	calls := 0
	e.hosts["h1"] = preflight(core.ModuleStatus{Version: 3, Available: true}, &calls)
	in := ModuleInput{HostID: "h1", ModuleID: "demo"}

	pv, err := e.plan(operator, "core.module.enable", in)
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := []core.DiffLine{
		{Op: "-", Text: "module demo on host h1: disabled"},
		{Op: "+", Text: "module demo on host h1: enabled; its preflight checks run again and set its availability"},
		{Op: " ", Text: "compensated by core.module.disable"},
	}
	if !reflect.DeepEqual(pv.Diff, wantDiff) || len(pv.Guards) != 0 || pv.Confirmation != "" {
		t.Errorf("plan = %+v, want diff %+v, no guards, no confirmation", pv, wantDiff)
	}
	if calls != 0 {
		t.Errorf("plan ran preflight %d times; plans must not touch the host", calls)
	}

	e.clock.Advance(time.Minute)
	v, err := e.apply(operator, pv.PlanID, "")
	if err != nil || v.State != core.StateCompleted {
		t.Fatalf("apply = %+v, %v", v, err)
	}
	if v.CompensatedBy != "core.module.disable" || calls != 1 {
		t.Errorf("compensated by %q, preflight calls %d; want core.module.disable, 1", v.CompensatedBy, calls)
	}
	var got core.ModuleState
	if err := json.Unmarshal(v.Output, &got); err != nil {
		t.Fatal(err)
	}
	want := core.ModuleState{ID: "demo", Version: 3, Enabled: true, Available: true, CheckedAt: ptr(t0.Add(time.Minute))}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("output = %+v, want %+v", got, want)
	}
	if err := e.verify("core.module.enable", in); err != nil {
		t.Errorf("verify after enable: %v", err)
	}
	if err := e.verify("core.module.disable", in); err == nil {
		t.Error("disable's verify passed on an enabled module")
	}
}

func TestModuleEnableRecordsFailedPreflight(t *testing.T) {
	e := newModuleEnv(t)
	calls := 0
	e.hosts["h1"] = preflight(core.ModuleStatus{Version: 3, Reason: "ufw is not installed"}, &calls)

	pv, err := e.plan(operator, "core.module.enable", ModuleInput{HostID: "h1", ModuleID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if pv.Diff[0] != (core.DiffLine{Op: "-", Text: "module demo on host h1: not reported"}) {
		t.Errorf("diff = %+v, want the module shown as not reported", pv.Diff)
	}
	v, err := e.apply(operator, pv.PlanID, "")
	if err != nil || v.State != core.StateCompleted {
		t.Fatalf("apply = %+v, %v", v, err)
	}
	var got ModuleList
	if err := e.read(operator, "core.module.list", ModuleListInput{HostID: "h1"}, &got); err != nil {
		t.Fatal(err)
	}
	want := []core.ModuleState{{ID: "demo", Version: 3, Enabled: true, Reason: ptr("ufw is not installed"), CheckedAt: ptr(t0)}}
	if !reflect.DeepEqual(got.Modules, want) {
		t.Errorf("modules = %+v, want %+v", got.Modules, want)
	}
}

func TestModuleEnableRefusals(t *testing.T) {
	e := newModuleEnv(t)
	for _, tt := range []struct {
		name string
		in   ModuleInput
		code errcode.Code
	}{
		{"core module", ModuleInput{HostID: "h1", ModuleID: "core"}, errcode.InvalidInput},
		{"unknown module", ModuleInput{HostID: "h1", ModuleID: "nope"}, errcode.NotFound},
		{"unknown host", ModuleInput{HostID: "h9", ModuleID: "demo"}, errcode.NotFound},
		{"empty host", ModuleInput{ModuleID: "demo"}, errcode.InvalidInput},
	} {
		for _, action := range []string{"core.module.enable", "core.module.disable"} {
			_, err := e.plan(operator, action, tt.in)
			var ce *errcode.Error
			if !errors.As(err, &ce) || ce.Code != tt.code {
				t.Errorf("%s, %s: err = %v, want %s", tt.name, action, err, tt.code)
			}
		}
	}
}

func TestModuleEnableFailsWithoutTheAgent(t *testing.T) {
	e := newModuleEnv(t)
	e.setModuleState("h1", "demo", false, true, "")
	pv, err := e.plan(operator, "core.module.enable", ModuleInput{HostID: "h1", ModuleID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.apply(operator, pv.PlanID, "")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != core.StateFailed || v.Error == nil || v.Error.Code != string(errcode.NotFound) {
		t.Errorf("change = %+v, want failed with not_found from the host directory", v)
	}
	var got ModuleList
	if err := e.read(operator, "core.module.list", ModuleListInput{HostID: "h1"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Modules[0].Enabled {
		t.Error("a failed enable left the module enabled")
	}
}

func TestModuleDisable(t *testing.T) {
	e := newModuleEnv(t)
	e.setModuleState("h1", "demo", true, true, "")
	in := ModuleInput{HostID: "h1", ModuleID: "demo"}

	pv, err := e.plan(operator, "core.module.disable", in)
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := []core.DiffLine{
		{Op: "-", Text: "module demo on host h1: enabled"},
		{Op: "+", Text: "module demo on host h1: disabled; its actions and resources answer module.unavailable"},
		{Op: " ", Text: "compensated by core.module.enable"},
	}
	if !reflect.DeepEqual(pv.Diff, wantDiff) {
		t.Errorf("diff = %+v, want %+v", pv.Diff, wantDiff)
	}
	v, err := e.apply(operator, pv.PlanID, "")
	if err != nil || v.State != core.StateCompleted || v.CompensatedBy != "core.module.enable" {
		t.Fatalf("apply = %+v, %v", v, err)
	}
	var got core.ModuleState
	if err := json.Unmarshal(v.Output, &got); err != nil {
		t.Fatal(err)
	}
	want := core.ModuleState{ID: "demo", Version: 3, Available: true, CheckedAt: ptr(t0)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("output = %+v, want %+v (availability kept)", got, want)
	}
	if err := e.verify("core.module.disable", in); err != nil {
		t.Errorf("verify after disable: %v", err)
	}

	calls := 0
	e.hosts["h1"] = preflight(core.ModuleStatus{Version: 3, Available: true}, &calls)
	var back core.ModuleState
	e.run(operator, "core.module.enable", in, &back)
	if !back.Enabled || calls != 1 {
		t.Errorf("compensating enable = %+v after %d preflight calls", back, calls)
	}
}

func TestModuleDisableNotReported(t *testing.T) {
	e := newModuleEnv(t)
	var got core.ModuleState
	e.run(operator, "core.module.disable", ModuleInput{HostID: "h1", ModuleID: "demo"}, &got)
	want := core.ModuleState{ID: "demo", Version: 3, Reason: ptr(ReasonNotReported), CheckedAt: ptr(t0)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("output = %+v, want %+v", got, want)
	}
}

func TestModuleStateHash(t *testing.T) {
	e := newModuleEnv(t)
	e.setModuleState("h1", "demo", false, true, "")
	calls := 0
	e.hosts["h1"] = preflight(core.ModuleStatus{Version: 3, Available: true}, &calls)
	in := ModuleInput{HostID: "h1", ModuleID: "demo"}

	pv, err := e.plan(operator, "core.module.enable", in)
	if err != nil {
		t.Fatal(err)
	}
	// An agent hello rewrites availability; the plan still applies.
	e.setModuleState("h1", "demo", false, false, "tool missing")
	if v, err := e.apply(operator, pv.PlanID, ""); err != nil || v.State != core.StateCompleted {
		t.Fatalf("apply after an availability change = %+v, %v", v, err)
	}

	pv, err = e.plan(operator, "core.module.disable", in)
	if err != nil {
		t.Fatal(err)
	}
	e.setModuleState("h1", "demo", false, true, "")
	_, err = e.apply(operator, pv.PlanID, "")
	wantCode(t, err, errcode.PlanStateChanged)
}
