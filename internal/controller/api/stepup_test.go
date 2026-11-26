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
