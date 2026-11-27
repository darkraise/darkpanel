package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"reflect"
	"testing"

	"github.com/coder/websocket"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/jobs"
	"github.com/darkraise/darkpanel/internal/modules/users"
)

func qaCode(t *testing.T, what string, rec interface{ Result() *http.Response }) (status int, code string) {
	t.Helper()
	res := rec.Result()
	defer func() { _ = res.Body.Close() }()
	var p struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(res.Body).Decode(&p)
	return res.StatusCode, p.Code
}

// qaStepUpModule has a revertible action that needs step-up, a revertible
// action whose repair needs step-up and one whose neither does.
type qaStepUpModule struct{}

func (qaStepUpModule) Manifest() core.Manifest {
	h := demoHandler[demoInput, demoOutput]{}
	return core.Manifest{ID: "qa3", Version: 1, Actions: []core.ActionDef{
		core.DefineRevertible(core.ActionSpec{Name: "qa3.gate.set", Version: 1, Side: core.SideAgent, LockKeys: []string{"qa3"},
			RepairedBy: "qa3.gate.fix", StepUp: true}, h),
		core.DefineMutating(core.ActionSpec{Name: "qa3.gate.fix", Version: 1, Class: core.ClassIrreversible, Side: core.SideAgent, LockKeys: []string{"qa3"}}, h),
		core.DefineRevertible(core.ActionSpec{Name: "qa3.rule.set", Version: 1, Side: core.SideAgent, LockKeys: []string{"qa3"},
			RepairedBy: "qa3.rule.fix"}, h),
		core.DefineMutating(core.ActionSpec{Name: "qa3.rule.fix", Version: 1, Class: core.ClassIrreversible, Side: core.SideAgent, LockKeys: []string{"qa3"}, StepUp: true}, h),
		core.DefineRevertible(core.ActionSpec{Name: "qa3.plain.set", Version: 1, Side: core.SideAgent, LockKeys: []string{"qa3"},
			RepairedBy: "qa3.plain.fix"}, h),
		core.DefineMutating(core.ActionSpec{Name: "qa3.plain.fix", Version: 1, Class: core.ClassIrreversible, Side: core.SideAgent, LockKeys: []string{"qa3"}}, h),
	}}
}

// qaChanges is a harness whose dispatcher knows one change per entry of
// actions (id -> action) in state, counting the calls that reach it.
func qaChanges(t *testing.T, state string, actions map[string]string, calls *[]string) *harness {
	t.Helper()
	h := newHarnessWith(t, jobs.Module(), users.Module(), qaStepUpModule{})
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
		*calls = append(*calls, "undo "+id)
		if actions[id] == "users.sudo.remove" {
			return core.ChangeView{}, errcode.New(errcode.ChangeNotPending, "nothing to undo")
		}
		v := view(id)
		v.State = core.StateUndoing
		return v, nil
	}
	h.disp.RevertFunc = func(_ context.Context, _ core.Principal, id string) (core.ChangeView, error) {
		*calls = append(*calls, "revert "+id)
		return view(id), nil
	}
	h.disp.RepairFunc = func(_ context.Context, _ core.Principal, id string, _ core.RepairRequest) (core.RepairResult, error) {
		*calls = append(*calls, "repair "+id)
		v := view(id)
		return core.RepairResult{Change: &v}, nil
	}
	return h
}

func qaStepUp(t *testing.T, h *harness, session, csrf string) {
	t.Helper()
	rec := h.serve(withSession(request("POST", "/api/v1/auth/step-up", `{"code":"`+h.code()+`"}`), session, csrf))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("step-up: %d %s", rec.Code, rec.Body)
	}
}

func TestA1_UndoOfAJobDeleteNeedsAStepUpWindow(t *testing.T) {
	var calls []string
	h := qaChanges(t, core.StateCompleted, map[string]string{"cjob": "jobs.job.delete", "csudo": "users.sudo.remove"}, &calls)
	session, csrf := h.login()
	tok := h.token(core.ScopeAdmin)
	undo := func(id string) *http.Request { return request("POST", "/api/v1/changes/"+id+"/undo", "") }

	status, code := qaCode(t, "session without a window", h.serve(withSession(undo("cjob"), session, csrf)))
	if status != http.StatusForbidden || code != "auth.step_up_required" {
		t.Errorf("undo of a job delete without a window: %d %q, want 403 auth.step_up_required", status, code)
	}
	status, code = qaCode(t, "token", h.serve(withToken(undo("cjob"), tok)))
	if status != http.StatusForbidden || code != "auth.step_up_unavailable" {
		t.Errorf("undo of a job delete with a token: %d %q, want 403 auth.step_up_unavailable", status, code)
	}
	if len(calls) != 0 {
		t.Errorf("an undo reached the dispatcher (%q) although neither caller had a step-up window; the job would be restored", calls)
	}

	// users.sudo.remove: the undo is the engine's to refuse; no code is asked for, by session or token.
	for who, req := range map[string]*http.Request{
		"session": withSession(undo("csudo"), session, csrf),
		"token":   withToken(undo("csudo"), tok),
	} {
		status, code = qaCode(t, who, h.serve(req))
		if status != http.StatusConflict || code != "change.not_pending" {
			t.Errorf("undo of a sudo removal by %s: %d %q, want 409 change.not_pending", who, status, code)
		}
	}

	// users.sudo.remove has no undo handler, so nothing can re-grant sudo.
	for _, a := range users.Module().Manifest().Actions {
		if a.Spec.Name == "users.sudo.remove" && a.Handler.Inverse != nil {
			t.Error("users.sudo.remove has an undo handler, so an undo could grant sudo")
		}
	}

	qaStepUp(t, h, session, csrf)
	rec := h.serve(withSession(undo("cjob"), session, csrf))
	if rec.Code != http.StatusAccepted && rec.Code != http.StatusOK {
		t.Errorf("undo of a job delete after step-up: %d %s", rec.Code, rec.Body)
	}
}

func TestA3_RevertAndRepairNeedAStepUpWindow(t *testing.T) {
	var calls []string
	h := qaChanges(t, core.StatePending, map[string]string{
		"cgate": "qa3.gate.set", "crule": "qa3.rule.set", "cplain": "qa3.plain.set", "cjob": "jobs.job.delete",
	}, &calls)
	session, csrf := h.login()
	tok := h.token(core.ScopeAdmin)
	revert := func(id string) *http.Request { return request("POST", "/api/v1/changes/"+id+"/revert", "") }
	repair := func(id string) *http.Request { return request("POST", "/api/v1/changes/"+id+"/repair", `{}`) }

	wantRefused := func(what string, req *http.Request, wantCode string) {
		t.Helper()
		status, code := qaCode(t, what, h.serve(req))
		if status != http.StatusForbidden || code != wantCode {
			t.Errorf("%s: %d %q, want 403 %s", what, status, code, wantCode)
		}
	}
	wantRefused("revert of a step-up action, no window", withSession(revert("cgate"), session, csrf), "auth.step_up_required")
	wantRefused("revert of a step-up action, token", withToken(revert("cgate"), tok), "auth.step_up_unavailable")
	wantRefused("repair of a change whose RepairedBy action is step-up, no window", withSession(repair("crule"), session, csrf), "auth.step_up_required")
	wantRefused("repair of a change whose RepairedBy action is step-up, token", withToken(repair("crule"), tok), "auth.step_up_unavailable")
	if len(calls) != 0 {
		t.Fatalf("calls reached the dispatcher before step-up: %q", calls)
	}

	// Controls: an action with no step-up anywhere is not gated.
	if rec := h.serve(withSession(revert("cplain"), session, csrf)); rec.Code >= 400 {
		t.Errorf("revert of a plain action: %d %s", rec.Code, rec.Body)
	}
	if rec := h.serve(withSession(repair("cplain"), session, csrf)); rec.Code >= 400 {
		t.Errorf("repair of a plain action: %d %s", rec.Code, rec.Body)
	}

	qaStepUp(t, h, session, csrf)
	if rec := h.serve(withSession(revert("cgate"), session, csrf)); rec.Code >= 400 {
		t.Errorf("revert after step-up: %d %s", rec.Code, rec.Body)
	}
	if rec := h.serve(withSession(repair("crule"), session, csrf)); rec.Code >= 400 {
		t.Errorf("repair after step-up: %d %s", rec.Code, rec.Body)
	}
}

func TestA6_ErrStepUpRequiredIsAnErrcodeError(t *testing.T) {
	want := reflect.TypeFor[*errcode.Error]()
	if got := reflect.TypeOf(auth.ErrStepUpRequired()); got != want {
		t.Errorf("auth.ErrStepUpRequired() has type %v, want %v", got, want)
	}
	var e *errcode.Error = auth.ErrStepUpRequired()
	if e.Code != errcode.StepUpRequired {
		t.Errorf("code %q, want %q", e.Code, errcode.StepUpRequired)
	}
}

func TestA29_BrowserMessagesAbove32KiBCloseWithBadFrame(t *testing.T) {
	for _, size := range []int{40 << 10, 2 << 20} {
		t.Run(fmt.Sprintf("%d bytes", size), func(t *testing.T) {
			r := newTermRig(t, false, 0)
			ws, agent, _ := r.dial(r.admin, termHost, r.ticket(r.admin), nil)
			go func() { _ = ws.Write(context.Background(), websocket.MessageBinary, make([]byte, size)) }()
			if got := closeReason(t, ws); got != "bad frame" {
				t.Errorf("the terminal closed with %q, want %q", got, "bad frame")
			}
			_ = agent.Close()
		})
	}
}

func TestA30_KeepaliveAndActivityTestsHaveNoWallClockSleep(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "channels_test.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "TestKeepaliveAndActivity" {
			continue
		}
		found = true
		ast.Inspect(fn, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Sleep" {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "time" {
					t.Errorf("channels_test.go:%d: TestKeepaliveAndActivity calls time.Sleep", fset.Position(call.Pos()).Line)
				}
			}
			return true
		})
	}
	if !found {
		t.Fatal("TestKeepaliveAndActivity not found in channels_test.go")
	}
}

func TestA31_RecheckFailuresCloseWithError(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause func(r *termRig)
		want  string
	}{
		{"identity read error", func(r *termRig) { _ = r.h.db.Close() }, "error"},
		{"database error inside ModuleUsable", func(r *termRig) {
			r.h.disp.ModuleUsableFunc = func(context.Context, string, string) error {
				return errors.New("read the host's modules: database is locked")
			}
		}, "error"},
		{"console disabled", func(r *termRig) {
			r.h.disp.ModuleUsableFunc = func(context.Context, string, string) error {
				return errcode.New(errcode.ModuleUnavailable, "module console is disabled")
			}
		}, "revoked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newTermRig(t, false, 0)
			ws, agent, _ := r.dial(r.admin, termHost, r.ticket(r.admin), nil)
			tc.cause(r)
			r.h.clock.Advance(SSERecheck)
			if got := closeReason(t, ws); got != tc.want {
				t.Errorf("the terminal closed with %q, want %q", got, tc.want)
			}
			_ = agent.Close()
		})
	}
}
