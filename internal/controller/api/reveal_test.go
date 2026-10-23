package api

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

type arvIn struct {
	Name string `json:"name"`
}

type arvOut struct {
	Password string `json:"password" secret:"true"`
}

func arvValue(context.Context, core.Env, arvIn) (core.Revealed[arvOut], error) {
	return core.Revealed[arvOut]{}, nil
}

// arvModule has a reveal action and an Internal read on each side, and a
// plain agent read.
type arvModule struct{}

func (arvModule) Manifest() core.Manifest {
	return core.Manifest{ID: "arv", Version: 1, Actions: []core.ActionDef{
		core.DefineReveal(core.ActionSpec{Name: "arv.key.reveal", Version: 1, Side: core.SideAgent, MinScope: core.ScopeAdmin}, arvValue),
		core.DefineReveal(core.ActionSpec{Name: "arv.ctl.reveal", Version: 1, Side: core.SideController, MinScope: core.ScopeAdmin}, arvValue),
		core.DefineInternalRead(core.ActionSpec{Name: "arv.cert.get", Version: 1, Side: core.SideAgent}, arvValue),
		core.DefineInternalRead(core.ActionSpec{Name: "arv.ctl.get", Version: 1, Side: core.SideController}, arvValue),
		core.DefineRead(core.ActionSpec{Name: "arv.item.get", Version: 1, Side: core.SideAgent}, demoHandler[arvIn, arvOut]{}),
	}}
}

// arvCalls records what the fake dispatcher received, one line per call.
type arvCalls struct {
	mu    sync.Mutex
	lines []string
}

func (c *arvCalls) add(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, fmt.Sprintf(format, args...))
}

func (c *arvCalls) recorded() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.lines)
}

// arvHarness serves arvModule over a fake dispatcher whose reveal, read,
// plan and apply record their calls.
func arvHarness(t *testing.T) (*harness, *arvCalls) {
	t.Helper()
	h := newHarnessWith(t, arvModule{})
	calls := &arvCalls{}
	h.disp.RevealFunc = func(_ context.Context, p core.Principal, hostID string, a core.ActionRef, in json.RawMessage) (json.RawMessage, error) {
		calls.add("reveal %s on %q by %s: %s", a, hostID, strings.SplitN(p.Actor, ":", 2)[0], in)
		return json.RawMessage(`{"password": "s3cret"}`), nil
	}
	h.disp.ReadFunc = func(_ context.Context, _ core.Principal, _ string, a core.ActionRef, _ json.RawMessage) (json.RawMessage, error) {
		calls.add("read %s", a)
		return json.RawMessage(`{}`), nil
	}
	h.disp.PlanFunc = func(_ context.Context, _ core.Principal, _ string, a core.ActionRef, _ json.RawMessage, _ core.PlanOptions) (core.PlanView, error) {
		calls.add("plan %s", a)
		return core.PlanView{}, nil
	}
	h.disp.ApplyFunc = func(_ context.Context, _ core.Principal, _ string, a core.ActionRef, _ string, _ core.ApplyOptions) (core.ChangeView, error) {
		calls.add("apply %s", a)
		return core.ChangeView{}, nil
	}
	return h, calls
}

func TestInternalActionsHidden(t *testing.T) {
	h, calls := arvHarness(t)
	admin := h.token(core.ScopeAdmin)
	for _, base := range []string{"/api/v1/hosts/h1/actions/arv.cert.get/v1/", "/api/v1/actions/arv.ctl.get/v1/"} {
		for _, op := range []string{opPlan, opApply, opRead} {
			body := `{"input":{"name":"x"}}`
			if op == opApply {
				body = `{"planId":"p1"}`
			}
			decodeProblem(t, h.serve(withToken(request("POST", base+op, body), admin)), 404, errcode.ActionUnknown)
		}
	}
	if got := calls.recorded(); len(got) != 0 {
		t.Errorf("the dispatcher was called: %q", got)
	}
}

func TestRevealNotOnReadRoute(t *testing.T) {
	h, calls := arvHarness(t)
	admin := h.token(core.ScopeAdmin)
	for _, path := range []string{
		"/api/v1/hosts/h1/actions/arv.key.reveal/v1/read",
		"/api/v1/actions/arv.ctl.reveal/v1/read",
		"/api/v1/hosts/h1/actions/arv.key.reveal/v1/plan",
		"/api/v1/actions/arv.ctl.reveal/v1/apply",
	} {
		decodeProblem(t, h.serve(withToken(request("POST", path, `{"input":{"name":"x"}}`), admin)), 404, errcode.ActionUnknown)
	}
	if rec := h.serve(withToken(request("POST", "/api/v1/hosts/h1/actions/arv.item.get/v1/read", `{"input":{"name":"x"}}`), admin)); rec.Code != 200 {
		t.Errorf("the plain read answered %d %s", rec.Code, rec.Body)
	}
	if want := []string{"read arv.item.get/v1"}; !slices.Equal(calls.recorded(), want) {
		t.Errorf("dispatcher calls %q, want %q", calls.recorded(), want)
	}
}
