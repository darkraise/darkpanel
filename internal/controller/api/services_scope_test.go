package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/services"
)

// svcUnits is the systemd of the fake agent: one nginx.service.
type svcUnits struct {
	core.Units
	active string
	calls  []string
}

func (u *svcUnits) Inspect(_ context.Context, name string) (core.UnitInfo, error) {
	if name != "nginx.service" && name != "ssh.service" {
		return core.UnitInfo{ID: name, LoadState: "not-found"}, nil
	}
	return core.UnitInfo{ID: name, Names: []string{name}, LoadState: "loaded", ActiveState: u.active, UnitFileState: "enabled",
		SuccessAction: "none", FailureAction: "none", JobTimeoutAction: "none", StartLimitAction: "none"}, nil
}

func (u *svcUnits) State(context.Context, string) (core.UnitState, error) {
	return core.UnitState{LoadState: "loaded", ActiveState: u.active}, nil
}

func (u *svcUnits) ListAll(context.Context) ([]core.UnitInfo, error) { return nil, nil }

func (u *svcUnits) do(call, name, active string) error {
	u.calls, u.active = append(u.calls, call+" "+name), active
	return nil
}

func (u *svcUnits) Start(_ context.Context, n string) error      { return u.do("start", n, "active") }
func (u *svcUnits) Stop(_ context.Context, n string) error       { return u.do("stop", n, "inactive") }
func (u *svcUnits) Restart(_ context.Context, n string) error    { return u.do("restart", n, "active") }
func (u *svcUnits) ReloadUnit(_ context.Context, n string) error { return u.do("reload", n, "active") }

// svcAgent makes h's dispatcher an agent that runs the services module's
// handlers on units, with the principal the controller's client sends.
func svcAgent(t *testing.T, h *harness, units *svcUnits) {
	type stored struct {
		ref   core.ActionRef
		input json.RawMessage
	}
	plans := map[string]stored{}
	env := func(p core.Principal, hostID string) core.Env {
		b, err := json.Marshal(wire.FromPrincipalOn(p, hostID))
		if err != nil {
			t.Fatal(err)
		}
		var w wire.Principal
		if err := json.Unmarshal(b, &w); err != nil {
			t.Fatal(err)
		}
		return core.Env{HostID: hostID, Principal: w.Core(), Units: units}
	}
	h.disp.PlanFunc = func(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, in json.RawMessage, _ core.PlanOptions) (core.PlanView, error) {
		def, _ := h.cfg.Registry.Action(a)
		var req struct{ Input json.RawMessage }
		_ = json.Unmarshal(in, &req)
		if req.Input == nil {
			req.Input = in
		}
		out, err := def.Handler.Plan(ctx, env(p, hostID), req.Input)
		if err != nil {
			return core.PlanView{}, err
		}
		id := fmt.Sprintf("p%d", len(plans))
		plans[id] = stored{a, req.Input}
		return core.PlanView{PlanID: id, HostID: hostID, Action: a.Name, Version: a.Version, Confirmation: out.Confirmation,
			Diff: out.Diff, Guards: out.Guards}, nil
	}
	h.disp.ApplyFunc = func(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, planID string, _ core.ApplyOptions) (core.ChangeView, error) {
		pl, ok := plans[planID]
		if !ok || pl.ref != a {
			return core.ChangeView{}, errcode.New(errcode.NotFound, "no plan "+planID)
		}
		def, _ := h.cfg.Registry.Action(a)
		e := env(p, hostID)
		out, err := def.Handler.Apply(ctx, e, pl.input)
		if err == nil {
			err = def.Handler.Verify(ctx, e, pl.input, out)
		}
		if err != nil {
			return core.ChangeView{}, err
		}
		return core.ChangeView{ID: "c-" + planID, HostID: hostID, Action: a.Name, Version: a.Version, State: core.StateCompleted}, nil
	}
}

func TestOperatorDayToDay(t *testing.T) {
	h := accHarness(t, services.Module())
	units := &svcUnits{active: "inactive"}
	svcAgent(t, h, units)
	_, secret := accMember(t, h, "opg", "G=operator")
	cred := accSession(t, h, "opg", secret)
	path := func(op string) string { return "/api/v1/hosts/" + accHostG + "/actions/services.unit." + op + "/v1/" }
	for _, op := range []string{"start", "stop", "restart", "reload"} {
		rec := h.serve(cred(request("POST", path(op)+"plan", `{"input":{"unit":"nginx.service"}}`)))
		var plan core.PlanView
		if err := json.Unmarshal(rec.Body.Bytes(), &plan); rec.Code != http.StatusOK || err != nil {
			t.Fatalf("plan %s: %d %s", op, rec.Code, rec.Body)
		}
		if rec := h.serve(cred(request("POST", path(op)+"apply", `{"planId":"`+plan.PlanID+`"}`))); rec.Code != http.StatusOK {
			t.Errorf("apply %s: %d %s", op, rec.Code, rec.Body)
		}
	}
	want := []string{"start nginx.service", "stop nginx.service", "restart nginx.service", "reload nginx.service"}
	if !slices.Equal(units.calls, want) {
		t.Errorf("the agent ran %v, want %v", units.calls, want)
	}
	// The agent raises ssh.service to admin from the scope the client sends.
	wantProblem(t, "start ssh.service as an operator", h.serve(cred(request("POST", path("start")+"plan", `{"input":{"unit":"ssh.service"}}`))),
		http.StatusForbidden, errcode.Forbidden)
	wantProblem(t, "enable as an operator", h.serve(cred(request("POST", path("enable")+"plan", `{"input":{"unit":"nginx.service"}}`))),
		http.StatusForbidden, errcode.Forbidden)
}
