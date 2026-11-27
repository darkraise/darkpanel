package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/jobs"
	"github.com/darkraise/darkpanel/internal/modules/users"
)

// stepUpModule has one controller-side action that needs an elevated session.
type stepUpModule struct{}

func (stepUpModule) Manifest() core.Manifest {
	h := demoHandler[demoInput, demoOutput]{}
	return core.Manifest{ID: "stepup", Version: 1, Actions: []core.ActionDef{
		core.DefineMutating(core.ActionSpec{Name: "stepup.item.set", Version: 1, Class: core.ClassIrreversible,
			Side: core.SideController, StepUp: true}, h),
	}}
}

func stepUpHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarnessWith(t, stepUpModule{})
	h.disp.PlanFunc = func(context.Context, core.Principal, string, core.ActionRef, json.RawMessage, core.PlanOptions) (core.PlanView, error) {
		return core.PlanView{}, nil
	}
	h.disp.ApplyFunc = func(context.Context, core.Principal, string, core.ActionRef, string, core.ApplyOptions) (core.ChangeView, error) {
		return core.ChangeView{}, nil
	}
	return h
}

func wantProblem(t *testing.T, what string, rec interface {
	Result() *http.Response
}, status int, code errcode.Code) {
	t.Helper()
	res := rec.Result()
	defer func() { _ = res.Body.Close() }()
	var p struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(res.Body).Decode(&p)
	if res.StatusCode != status || p.Code != string(code) {
		t.Errorf("%s: %d %q, want %d %q", what, res.StatusCode, p.Code, status, code)
	}
}

const (
	stepUpPlan  = "/api/v1/actions/stepup.item.set/v1/plan"
	stepUpApply = "/api/v1/actions/stepup.item.set/v1/apply"
)

func TestStepUpActionGate(t *testing.T) {
	h := stepUpHarness(t)
	session, csrf := h.login()
	plan := func() *http.Request { return withSession(request("POST", stepUpPlan, `{"input":{}}`), session, csrf) }
	apply := func() *http.Request {
		return withSession(request("POST", stepUpApply, `{"planId":"p1"}`), session, csrf)
	}
	wantProblem(t, "plan before step-up", h.serve(plan()), http.StatusForbidden, errcode.StepUpRequired)
	wantProblem(t, "apply before step-up", h.serve(apply()), http.StatusForbidden, errcode.StepUpRequired)

	rec := h.serve(withSession(request("POST", "/api/v1/auth/step-up", `{"code":"`+h.code()+`"}`), session, csrf))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("step-up: %d %s", rec.Code, rec.Body)
	}
	if rec := h.serve(plan()); rec.Code != http.StatusOK {
		t.Errorf("plan after step-up: %d %s", rec.Code, rec.Body)
	}
	if rec := h.serve(apply()); rec.Code != http.StatusOK {
		t.Errorf("apply after step-up: %d %s", rec.Code, rec.Body)
	}
	if rec := h.serve(withSession(request("POST", "/api/v1/auth/step-up", `{"code":"`+h.code()+`"}`), session, "")); rec.Code == http.StatusNoContent {
		t.Error("step-up without the CSRF header succeeded")
	}

	tok := h.token(core.ScopeAdmin)
	wantProblem(t, "plan with a token", h.serve(withToken(request("POST", stepUpPlan, `{"input":{}}`), tok)),
		http.StatusForbidden, errcode.StepUpUnavailable)
	wantProblem(t, "step-up with a token", h.serve(withToken(request("POST", "/api/v1/auth/step-up", `{"code":"`+h.code()+`"}`), tok)),
		http.StatusForbidden, errcode.StepUpUnavailable)
}

func TestElevationSurvivesRestart(t *testing.T) {
	h := stepUpHarness(t)
	ctx := context.Background()
	session, csrf := h.login()
	if rec := h.serve(withSession(request("POST", "/api/v1/auth/step-up", `{"code":"`+h.code()+`"}`), session, csrf)); rec.Code != http.StatusNoContent {
		t.Fatalf("step-up: %d %s", rec.Code, rec.Body)
	}
	db, err := store.Open(ctx, h.db.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	reopened := auth.New(db, coretest.Sealer{}, h.clock, h.audit, auth.Options{Params: testParams})
	if ok, err := reopened.Elevated(ctx, session); err != nil || !ok {
		t.Fatalf("after reopen: Elevated = %v, %v; want true", ok, err)
	}
	if err := reopened.Logout(ctx, session, "192.0.2.10"); err != nil {
		t.Fatal(err)
	}
	if ok, err := reopened.Elevated(ctx, session); err != nil || ok {
		t.Errorf("after sign-out: Elevated = %v, %v; want false", ok, err)
	}
	var n int
	if err := db.Read().QueryRow(`SELECT count(*) FROM core_sessions WHERE elevated_until IS NOT NULL`).Scan(&n); err != nil || n != 0 {
		t.Errorf("elevated sessions after sign-out: %d, %v", n, err)
	}
}

// stepUpUndoModule has two revertible actions: surv.gate.set needs step-up
// itself, and surv.rule.set is repaired by surv.rule.fix, which needs it.
type stepUpUndoModule struct{}

func (stepUpUndoModule) Manifest() core.Manifest {
	h := demoHandler[demoInput, demoOutput]{}
	return core.Manifest{ID: "surv", Version: 1, Actions: []core.ActionDef{
		core.DefineRevertible(core.ActionSpec{Name: "surv.gate.set", Version: 1, Side: core.SideAgent, LockKeys: []string{"surv"},
			RepairedBy: "surv.gate.fix", StepUp: true}, h),
		core.DefineMutating(core.ActionSpec{Name: "surv.gate.fix", Version: 1, Class: core.ClassIrreversible,
			Side: core.SideAgent, LockKeys: []string{"surv"}}, h),
		core.DefineRevertible(core.ActionSpec{Name: "surv.rule.set", Version: 1, Side: core.SideAgent, LockKeys: []string{"surv"},
			RepairedBy: "surv.rule.fix"}, h),
		core.DefineMutating(core.ActionSpec{Name: "surv.rule.fix", Version: 1, Class: core.ClassIrreversible,
			Side: core.SideAgent, LockKeys: []string{"surv"}, StepUp: true}, h),
	}}
}

// stepUpChanges registers the shipped jobs and users modules beside
// stepUpUndoModule and answers Change for each id in actions with a change
// of that action on h1, in state. The dispatcher's Undo, Revert and Repair
// calls are counted in *calls; Undo of a users.sudo.remove change answers
// change.not_pending, as the engine does for an action with no undo handler.
func stepUpChanges(t *testing.T, state string, actions map[string]string, calls *int) *harness {
	t.Helper()
	h := newHarnessWith(t, jobs.Module(), users.Module(), stepUpUndoModule{})
	view := func(id string) core.ChangeView {
		def, _ := h.cfg.Registry.Action(core.ActionRef{Name: actions[id], Version: 1})
		return core.ChangeView{ID: id, HostID: "h1", Action: actions[id], Version: 1, Class: def.Spec.Class,
			State: state, Actor: "user:u1", CreatedAt: changeTime, UpdatedAt: changeTime}
	}
	h.disp.ChangeFunc = func(_ context.Context, _ core.Principal, id string) (core.ChangeView, error) {
		if _, ok := actions[id]; !ok {
			return core.ChangeView{}, errcode.New(errcode.NotFound, "no change "+id)
		}
		return view(id), nil
	}
	h.disp.UndoFunc = func(_ context.Context, _ core.Principal, id string) (core.ChangeView, error) {
		*calls++
		if actions[id] == "users.sudo.remove" {
			return core.ChangeView{}, errcode.New(errcode.ChangeNotPending, "nothing to undo")
		}
		v := view(id)
		v.State = core.StateUndoing
		return v, nil
	}
	h.disp.RevertFunc = func(_ context.Context, _ core.Principal, id string) (core.ChangeView, error) {
		*calls++
		return view(id), nil
	}
	h.disp.RepairFunc = func(_ context.Context, _ core.Principal, id string, _ core.RepairRequest) (core.RepairResult, error) {
		*calls++
		v := view(id)
		return core.RepairResult{Change: &v}, nil
	}
	return h
}

func (h *harness) stepUp(session, csrf string) {
	h.t.Helper()
	if rec := h.serve(withSession(request("POST", "/api/v1/auth/step-up", `{"code":"`+h.code()+`"}`), session, csrf)); rec.Code != http.StatusNoContent {
		h.t.Fatalf("step-up: %d %s", rec.Code, rec.Body)
	}
}

func TestUndoNeedsStepUp(t *testing.T) {
	var calls int
	h := stepUpChanges(t, core.StateCompleted, map[string]string{"cjob": "jobs.job.delete", "csudo": "users.sudo.remove"}, &calls)
	session, csrf := h.login()
	tok := h.token(core.ScopeAdmin)
	undo := func(id string) *http.Request { return request("POST", "/api/v1/changes/"+id+"/undo", "") }

	wantProblem(t, "undo of a job delete before step-up", h.serve(withSession(undo("cjob"), session, csrf)),
		http.StatusForbidden, errcode.StepUpRequired)
	wantProblem(t, "undo of a job delete with a token", h.serve(withToken(undo("cjob"), tok)),
		http.StatusForbidden, errcode.StepUpUnavailable)
	if calls != 0 {
		t.Fatalf("the dispatcher's Undo ran %d times before step-up, want 0", calls)
	}

	wantProblem(t, "undo of a sudo removal with a token", h.serve(withToken(undo("csudo"), tok)),
		http.StatusConflict, errcode.ChangeNotPending)
	wantProblem(t, "undo of a sudo removal before step-up", h.serve(withSession(undo("csudo"), session, csrf)),
		http.StatusConflict, errcode.ChangeNotPending)
	if calls != 2 {
		t.Fatalf("the dispatcher's Undo ran %d times for the sudo removal, want 2: no step-up check guards an action with no undo handler", calls)
	}

	h.stepUp(session, csrf)
	if rec := h.serve(withSession(undo("cjob"), session, csrf)); rec.Code != http.StatusAccepted && rec.Code != http.StatusOK {
		t.Errorf("undo of a job delete after step-up: %d %s", rec.Code, rec.Body)
	}
	if calls != 3 {
		t.Errorf("the dispatcher's Undo ran %d times, want 3", calls)
	}
}

func TestRevertAndRepairNeedStepUp(t *testing.T) {
	var calls int
	h := stepUpChanges(t, core.StatePending, map[string]string{
		"cgate": "surv.gate.set", "crule": "surv.rule.set", "cjob": "jobs.job.delete", "cplain": "demo.item.add",
	}, &calls)
	session, csrf := h.login()
	tok := h.token(core.ScopeAdmin)
	revert := func(id string) *http.Request {
		return withSession(request("POST", "/api/v1/changes/"+id+"/revert", ""), session, csrf)
	}
	repair := func(id string) *http.Request {
		return withSession(request("POST", "/api/v1/changes/"+id+"/repair", `{}`), session, csrf)
	}

	wantProblem(t, "revert of a step-up action", h.serve(revert("cgate")), http.StatusForbidden, errcode.StepUpRequired)
	wantProblem(t, "repair of a step-up action", h.serve(repair("cgate")), http.StatusForbidden, errcode.StepUpRequired)
	wantProblem(t, "repair by a step-up action", h.serve(repair("crule")), http.StatusForbidden, errcode.StepUpRequired)
	wantProblem(t, "repair of an UndoStepUp action", h.serve(repair("cjob")), http.StatusForbidden, errcode.StepUpRequired)
	wantProblem(t, "repair of a step-up action with a token",
		h.serve(withToken(request("POST", "/api/v1/changes/cgate/repair", `{}`), tok)), http.StatusForbidden, errcode.StepUpUnavailable)
	if calls != 0 {
		t.Fatalf("the dispatcher ran %d times before step-up, want 0", calls)
	}
	if rec := h.serve(revert("crule")); rec.Code != http.StatusOK {
		t.Errorf("revert of an action without step-up: %d %s", rec.Code, rec.Body)
	}
	if rec := h.serve(repair("cplain")); rec.Code != http.StatusAccepted {
		t.Errorf("repair of an action without step-up: %d %s", rec.Code, rec.Body)
	}

	h.stepUp(session, csrf)
	if rec := h.serve(revert("cgate")); rec.Code != http.StatusOK {
		t.Errorf("revert after step-up: %d %s", rec.Code, rec.Body)
	}
	for _, id := range []string{"cgate", "crule", "cjob"} {
		if rec := h.serve(repair(id)); rec.Code != http.StatusAccepted {
			t.Errorf("repair of %s after step-up: %d %s", id, rec.Code, rec.Body)
		}
	}
	if calls != 6 {
		t.Errorf("the dispatcher ran %d times, want 6", calls)
	}
}
