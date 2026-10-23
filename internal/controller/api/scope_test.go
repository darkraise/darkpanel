package api

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestRequiredScope(t *testing.T) {
	const unknownClass, unknownSide = core.Class("bogus"), core.Side("bogus")
	want := map[core.Class]map[core.Side]core.Scope{
		core.ClassRead: {
			core.SideAgent: core.ScopeRead, core.SideController: core.ScopeRead, unknownSide: core.ScopeRead,
		},
		core.ClassRevertible: {
			core.SideAgent: core.ScopeOperate, core.SideController: core.ScopeAdmin, unknownSide: core.ScopeAdmin,
		},
		core.ClassCompensable: {
			core.SideAgent: core.ScopeOperate, core.SideController: core.ScopeAdmin, unknownSide: core.ScopeAdmin,
		},
		core.ClassIrreversible: {
			core.SideAgent: core.ScopeOperate, core.SideController: core.ScopeAdmin, unknownSide: core.ScopeAdmin,
		},
		unknownClass: {
			core.SideAgent: core.ScopeAdmin, core.SideController: core.ScopeAdmin, unknownSide: core.ScopeAdmin,
		},
	}
	// The needs each principal scope satisfies, written out rather than
	// derived, so the test does not share the code's ordering.
	satisfies := map[core.Scope]map[core.Scope]bool{
		core.ScopeRead:    {core.ScopeRead: true},
		core.ScopeOperate: {core.ScopeRead: true, core.ScopeOperate: true},
		core.ScopeAdmin:   {core.ScopeRead: true, core.ScopeOperate: true, core.ScopeAdmin: true},
		"":                {},
		"superuser":       {},
	}
	for class, sides := range want {
		for side, need := range sides {
			if got := RequiredScope(class, side); got != need {
				t.Errorf("RequiredScope(%s, %s) = %s, want %s", class, side, got, need)
			}
			for scope, ok := range satisfies {
				err := authorize(core.Principal{Actor: "token:t1", Scope: scope}, RequiredScope(class, side))
				if ok[need] != (err == nil) {
					t.Errorf("scope %q, class %s, side %s: authorize = %v, want allowed %v", scope, class, side, err, ok[need])
				}
				var e *errcode.Error
				if err != nil && (!errors.As(err, &e) || e.Code != errcode.Forbidden || e.Detail != "this request needs the "+string(need)+" scope") {
					t.Errorf("scope %q, need %s: error = %v, want auth.forbidden naming the scope", scope, need, err)
				}
			}
		}
	}
}

// mscComposite is the handler of msc.stack.deploy; no test runs it.
type mscComposite struct{}

func (mscComposite) Steps(context.Context, core.Env, demoInput) ([]core.Step, error) { return nil, nil }

func (mscComposite) Output(context.Context, core.Env, demoInput, map[string]json.RawMessage) (demoOutput, error) {
	return demoOutput{}, nil
}

// mscModule raises two agent actions to admin with MinScope and leaves a
// third at its default scope.
type mscModule struct{}

func (mscModule) Manifest() core.Manifest {
	h := demoHandler[demoInput, demoOutput]{}
	return core.Manifest{ID: "msc", Version: 1, Actions: []core.ActionDef{
		core.DefineRead(core.ActionSpec{Name: "msc.secret.list", Version: 1, Side: core.SideAgent, MinScope: core.ScopeAdmin}, h),
		core.DefineMutating(core.ActionSpec{Name: "msc.key.rotate", Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			ForwardOnly: true, Converges: true, MinScope: core.ScopeAdmin}, h),
		core.DefineMutating(core.ActionSpec{Name: "msc.item.add", Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			ForwardOnly: true, Converges: true}, h),
		core.DefineComposite(core.ActionSpec{Name: "msc.stack.deploy", Version: 1, Unwind: core.UnwindReverse}, mscComposite{}),
		core.DefineRevertible(core.ActionSpec{Name: "msc.route.set", Version: 1, Side: core.SideAgent, LockKeys: []string{"msc"},
			RepairedBy: "msc.route.repair"}, h),
		core.DefineMutating(core.ActionSpec{Name: "msc.route.repair", Version: 1, Class: core.ClassIrreversible,
			Side: core.SideAgent, LockKeys: []string{"msc"}, MinScope: core.ScopeAdmin}, h),
	}}
}

// mscReached records the dispatcher calls that passed authorization.
func mscReached(h *harness) *[]string {
	var reached []string
	h.disp.ReadFunc = func(_ context.Context, _ core.Principal, _ string, a core.ActionRef, _ json.RawMessage) (json.RawMessage, error) {
		reached = append(reached, "read "+a.Name)
		return json.RawMessage(`{}`), nil
	}
	h.disp.PlanFunc = func(_ context.Context, _ core.Principal, hostID string, a core.ActionRef, _ json.RawMessage, _ core.PlanOptions) (core.PlanView, error) {
		reached = append(reached, "plan "+a.Name)
		return core.PlanView{PlanID: "p1", HostID: hostID, Action: a.Name, Version: a.Version}, nil
	}
	return &reached
}

func TestMinScopeOnActionRoutes(t *testing.T) {
	h := newHarnessWith(t, mscModule{})
	reached := mscReached(h)
	operate, admin := h.token(core.ScopeOperate), h.token(core.ScopeAdmin)
	const host = "/api/v1/hosts/h1/actions/"
	body := `{"input":{"name":"x"}}`
	for _, tc := range []struct {
		path, token string
		need        core.Scope // "" when the call is authorized
	}{
		{host + "msc.secret.list/v1/read", operate, core.ScopeAdmin},
		{host + "msc.secret.list/v1/read", admin, ""},
		{host + "msc.key.rotate/v1/plan", operate, core.ScopeAdmin},
		{host + "msc.key.rotate/v1/plan", admin, ""},
		{host + "msc.item.add/v1/plan", operate, ""},
		{"/api/v1/actions/msc.stack.deploy/v1/plan", operate, core.ScopeAdmin},
		{"/api/v1/actions/msc.stack.deploy/v1/plan", admin, ""},
	} {
		rec := h.serve(withToken(request("POST", tc.path, body), tc.token))
		if tc.need == "" {
			if rec.Code != 200 {
				t.Errorf("%s: %d %s, want it dispatched", tc.path, rec.Code, rec.Body)
			}
			continue
		}
		p := decodeProblem(t, rec, 403, errcode.Forbidden)
		if p.Detail != "this request needs the "+string(tc.need)+" scope" {
			t.Errorf("%s: detail %q, want it to name %s", tc.path, p.Detail, tc.need)
		}
	}
	want := []string{"read msc.secret.list", "plan msc.key.rotate", "plan msc.item.add", "plan msc.stack.deploy"}
	if !slices.Equal(*reached, want) {
		t.Errorf("the dispatcher saw %v, want %v", *reached, want)
	}
}

func TestMinScopeOnChanges(t *testing.T) {
	h := newHarnessWith(t, mscModule{})
	changes := map[string]core.ChangeView{
		"c-raised": {ID: "c-raised", HostID: "h1", Action: "msc.key.rotate", Version: 1, Class: core.ClassCompensable,
			State: core.StateCompleted},
		"c-gone": {ID: "c-gone", HostID: "h1", Action: "gone.thing.set", Version: 1, Class: core.ClassCompensable,
			State: core.StateCompleted},
	}
	var reached []string
	h.disp.ChangeFunc = func(_ context.Context, _ core.Principal, id string) (core.ChangeView, error) {
		return changes[id], nil
	}
	answer := func(op string) func(context.Context, core.Principal, string) (core.ChangeView, error) {
		return func(_ context.Context, _ core.Principal, id string) (core.ChangeView, error) {
			reached = append(reached, op+" "+id)
			return changes[id], nil
		}
	}
	h.disp.ConfirmFunc, h.disp.RevertFunc, h.disp.UndoFunc = answer("confirm"), answer("revert"), answer("undo")
	operate, admin := h.token(core.ScopeOperate), h.token(core.ScopeAdmin)
	for _, op := range []string{"confirm", "revert", "undo"} {
		rec := h.serve(withToken(request("POST", "/api/v1/changes/c-raised/"+op, ""), operate))
		if p := decodeProblem(t, rec, 403, errcode.Forbidden); p.Detail != "this request needs the admin scope" {
			t.Errorf("%s by operate: detail %q", op, p.Detail)
		}
		if rec := h.serve(withToken(request("POST", "/api/v1/changes/c-raised/"+op, ""), admin)); rec.Code != 200 {
			t.Errorf("%s by admin: %d %s", op, rec.Code, rec.Body)
		}
		if p := decodeProblem(t, h.serve(withToken(request("POST", "/api/v1/changes/c-gone/"+op, ""), operate)), 403, errcode.Forbidden); p.Detail != "this request needs the admin scope" {
			t.Errorf("%s of an unregistered action by operate: detail %q", op, p.Detail)
		}
		if rec := h.serve(withToken(request("POST", "/api/v1/changes/c-gone/"+op, ""), admin)); rec.Code != 200 {
			t.Errorf("%s of an unregistered action by admin: %d %s", op, rec.Code, rec.Body)
		}
	}
	want := []string{"confirm c-raised", "confirm c-gone", "revert c-raised", "revert c-gone", "undo c-raised", "undo c-gone"}
	if !slices.Equal(reached, want) {
		t.Errorf("the dispatcher saw %v, want %v", reached, want)
	}
}

// TestMinScopeOnRepair: repairing a change plans its RepairedBy action, so
// the repair needs that action's raised scope too.
func TestMinScopeOnRepair(t *testing.T) {
	h := newHarnessWith(t, mscModule{})
	h.disp.ChangeFunc = func(_ context.Context, _ core.Principal, id string) (core.ChangeView, error) {
		return core.ChangeView{ID: id, HostID: "h1", Action: "msc.route.set", Version: 1, Class: core.ClassRevertible,
			State: core.StateNeedsRepair}, nil
	}
	var reached []string
	h.disp.RepairFunc = func(_ context.Context, _ core.Principal, id string, _ core.RepairRequest) (core.RepairResult, error) {
		reached = append(reached, "repair "+id)
		return core.RepairResult{Plan: &core.PlanView{PlanID: "p1", HostID: "h1", Action: "msc.route.repair", Version: 1}}, nil
	}
	operate, admin := h.token(core.ScopeOperate), h.token(core.ScopeAdmin)
	rec := h.serve(withToken(request("POST", "/api/v1/changes/c1/repair", `{}`), operate))
	if p := decodeProblem(t, rec, 403, errcode.Forbidden); p.Detail != "this request needs the admin scope" {
		t.Errorf("repair by operate: detail %q", p.Detail)
	}
	if rec := h.serve(withToken(request("POST", "/api/v1/changes/c1/repair", `{}`), admin)); rec.Code != 200 {
		t.Errorf("repair by admin: %d %s", rec.Code, rec.Body)
	}
	if !slices.Equal(reached, []string{"repair c1"}) {
		t.Errorf("the dispatcher saw %v, want only the admin's repair", reached)
	}
}
