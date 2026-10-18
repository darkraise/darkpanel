package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// call records what the fake dispatcher received.
type call struct {
	method, hostID, planID, changeID, name string
	ref                                    core.ActionRef
	input                                  string
	opts                                   core.ApplyOptions
	filter                                 core.ChangeFilter
	principal                              core.Principal
}

var (
	changeTime = time.Date(2026, 10, 17, 12, 5, 0, 0, time.UTC)
	deadline   = changeTime.Add(time.Minute)
)

// stubDispatcher answers every dispatcher method and records the last call
// in *last.
func (h *harness) stubDispatcher(last *call) {
	d := h.disp
	d.ReadFunc = func(_ context.Context, p core.Principal, hostID string, a core.ActionRef, in json.RawMessage) (json.RawMessage, error) {
		*last = call{method: "Read", principal: p, hostID: hostID, ref: a, input: string(in)}
		return json.RawMessage(`{"ok":true}`), nil
	}
	d.ResourceFunc = func(_ context.Context, p core.Principal, hostID, name string) (json.RawMessage, error) {
		*last = call{method: "Resource", principal: p, hostID: hostID, name: name}
		return json.RawMessage(`{"healthy": true}`), nil
	}
	d.PlanFunc = func(_ context.Context, p core.Principal, hostID string, a core.ActionRef, in json.RawMessage) (core.PlanView, error) {
		*last = call{method: "Plan", principal: p, hostID: hostID, ref: a, input: string(in)}
		return core.PlanView{PlanID: "plan1", HostID: hostID, Action: a.Name, Version: a.Version, InputHash: "h",
			ExpiresAt: changeTime.Add(10 * time.Minute)}, nil
	}
	d.ApplyFunc = func(_ context.Context, p core.Principal, hostID string, a core.ActionRef, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
		*last = call{method: "Apply", principal: p, hostID: hostID, ref: a, planID: planID, opts: opts}
		return core.ChangeView{ID: "change1", HostID: hostID, Action: a.Name, Version: a.Version, Class: core.ClassRevertible,
			State: core.StateFailed, Actor: p.Actor, CreatedAt: changeTime, UpdatedAt: changeTime,
			Error: &core.ChangeError{Code: "files.markers_invalid", Message: "markers out of order"}}, nil
	}
	d.ConfirmFunc = func(_ context.Context, p core.Principal, id string) (core.ChangeView, error) {
		*last = call{method: "Confirm", principal: p, changeID: id}
		return core.ChangeView{ID: id, State: core.StateConfirmed}, nil
	}
	d.RevertFunc = func(_ context.Context, p core.Principal, id string) (core.ChangeView, error) {
		*last = call{method: "Revert", principal: p, changeID: id}
		return core.ChangeView{ID: id, State: core.StateReverted}, nil
	}
	// Change c1 is a controller-side compensable change, cbg a break-glass
	// change, cunk a controller-side change of an unregistered action, cua
	// an agent-side one, missing does not exist, and any other id is a
	// pending change of the agent-side revertible demo.item.add.
	d.ChangeFunc = func(_ context.Context, p core.Principal, id string) (core.ChangeView, error) {
		*last = call{method: "Change", principal: p, changeID: id}
		v := core.ChangeView{ID: id, HostID: "h1", Action: "demo.item.add", Version: 1, Class: core.ClassRevertible,
			State: core.StatePending, Actor: "user:u1", CreatedAt: changeTime, UpdatedAt: changeTime}
		switch id {
		case "c1":
			v.HostID, v.Action, v.Class, v.State = "", "demo.item.remove", core.ClassCompensable, core.StateCompleted
		case "cbg":
			v.HostID, v.Action, v.Class, v.State = "", "demo.secret.reset", core.ClassIrreversible, core.StateCompleted
		case "cunk":
			v.HostID, v.Action, v.Class = "", "demo.gone.away", core.ClassCompensable
		case "cua":
			v.Action = "other.item.set"
		case "missing":
			return core.ChangeView{}, errcode.New(errcode.ChangeNotFound, "no change missing")
		}
		return v, nil
	}
	d.ChangesFunc = func(_ context.Context, p core.Principal, f core.ChangeFilter) ([]core.ChangeView, error) {
		*last = call{method: "Changes", principal: p, filter: f}
		return nil, nil
	}
}

func TestActionRoutesDispatch(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	token := h.token(core.ScopeAdmin)
	var recs []*httptest.ResponseRecorder
	for _, tc := range []struct {
		path, body string
		want       call
		response   string
	}{
		{"/api/v1/hosts/h1/actions/demo.item.get/v1/read", `{"input":{"name":"x"}}`,
			call{method: "Read", hostID: "h1", ref: core.ActionRef{Name: "demo.item.get", Version: 1}, input: `{"name":"x"}`},
			`{"output":{"ok":true}}`},
		{"/api/v1/actions/demo.item.list/v1/read", `{"input":{}}`,
			call{method: "Read", ref: core.ActionRef{Name: "demo.item.list", Version: 1}, input: `{}`},
			`{"output":{"ok":true}}`},
		{"/api/v1/hosts/h1/actions/demo.item.add/v1/plan", `{"input":{"name":"x"}}`,
			call{method: "Plan", hostID: "h1", ref: core.ActionRef{Name: "demo.item.add", Version: 1}, input: `{"name":"x"}`},
			`{"planId":"plan1","hostId":"h1","action":"demo.item.add","version":1,"inputHash":"h","diff":[],"guards":[],"expiresAt":"2026-10-17T12:15:00Z","confirmation":null}`},
		{"/api/v1/actions/demo.item.remove/v1/plan", `{"input":{"name":"x"}}`,
			call{method: "Plan", ref: core.ActionRef{Name: "demo.item.remove", Version: 1}, input: `{"name":"x"}`},
			`{"planId":"plan1","hostId":null,"action":"demo.item.remove","version":1,"inputHash":"h","diff":[],"guards":[],"expiresAt":"2026-10-17T12:15:00Z","confirmation":null}`},
		{"/api/v1/hosts/h1/actions/demo.item.add/v1/apply", `{"planId":"plan1","confirmTimeoutSeconds":30,"confirmNow":true}`,
			call{method: "Apply", hostID: "h1", ref: core.ActionRef{Name: "demo.item.add", Version: 1}, planID: "plan1",
				opts: core.ApplyOptions{ConfirmTimeout: 30 * time.Second, ConfirmNow: true}},
			`{"id":"change1","hostId":"h1","action":"demo.item.add","version":1,"class":"revertible","state":"failed","actor":"ACTOR","createdAt":"2026-10-17T12:05:00Z","updatedAt":"2026-10-17T12:05:00Z","revertDeadline":null,"output":null,"error":{"code":"files.markers_invalid","message":"markers out of order"},"compensatedBy":null}`},
		{"/api/v1/hosts/h1/actions/demo.item.purge/v1/apply", `{"planId":"plan2","confirmation":"purge it"}`,
			call{method: "Apply", hostID: "h1", ref: core.ActionRef{Name: "demo.item.purge", Version: 1}, planID: "plan2",
				opts: core.ApplyOptions{Confirmation: "purge it"}},
			`{"id":"change1","hostId":"h1","action":"demo.item.purge","version":1,"class":"revertible","state":"failed","actor":"ACTOR","createdAt":"2026-10-17T12:05:00Z","updatedAt":"2026-10-17T12:05:00Z","revertDeadline":null,"output":null,"error":{"code":"files.markers_invalid","message":"markers out of order"},"compensatedBy":null}`},
	} {
		last = call{}
		rec := h.serve(withToken(request("POST", tc.path, tc.body), token))
		recs = append(recs, rec)
		if rec.Code != 200 {
			t.Errorf("%s: status %d %s", tc.path, rec.Code, rec.Body)
			continue
		}
		if !strings.HasPrefix(last.principal.Actor, "token:") || last.principal.Scope != core.ScopeAdmin || last.principal.Source != "192.0.2.10" {
			t.Errorf("%s: principal %+v", tc.path, last.principal)
		}
		want := strings.Replace(tc.response, "ACTOR", last.principal.Actor, 1)
		tc.want.principal = last.principal
		if last != tc.want {
			t.Errorf("%s: dispatched %+v, want %+v", tc.path, last, tc.want)
		}
		if rec.Body.String() != want {
			t.Errorf("%s: body\n%s\nwant\n%s", tc.path, rec.Body, want)
		}
		if rec.Header().Get("Content-Type") != "application/json" || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: headers %v", tc.path, rec.Header())
		}
	}
	noCredentials(t, h, recs, map[string]string{"token secret": tokenSecret(token)})
}

func TestActionRouteRefusals(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	h.disp.PlanFunc = func(context.Context, core.Principal, string, core.ActionRef, json.RawMessage) (core.PlanView, error) {
		return core.PlanView{}, errcode.New(errcode.ModuleUnavailable, "module demo is disabled")
	}
	token := h.token(core.ScopeAdmin)
	plan := `{"input":{"name":"x"}}`
	var recs []*httptest.ResponseRecorder
	for _, tc := range []struct {
		name, path, body, contentType string
		status                        int
		code                          errcode.Code
		field                         string
	}{
		{"unknown action", "/api/v1/hosts/h1/actions/demo.item.nope/v1/plan", plan, "", 404, errcode.ActionUnknown, ""},
		{"unknown version", "/api/v1/hosts/h1/actions/demo.item.add/v2/plan", plan, "", 404, errcode.ActionUnknown, ""},
		{"version without v", "/api/v1/hosts/h1/actions/demo.item.add/1/plan", plan, "", 404, errcode.ActionUnknown, ""},
		{"version with a leading zero", "/api/v1/hosts/h1/actions/demo.item.add/v01/plan", plan, "", 404, errcode.ActionUnknown, ""},
		{"agent action on the controller route", "/api/v1/actions/demo.item.get/v1/read", plan, "", 404, errcode.ActionUnknown, ""},
		{"controller action on a host route", "/api/v1/hosts/h1/actions/demo.item.remove/v1/plan", plan, "", 404, errcode.ActionUnknown, ""},
		{"read of a mutating action", "/api/v1/hosts/h1/actions/demo.item.add/v1/read", plan, "", 404, errcode.ActionUnknown, ""},
		{"plan of a read action", "/api/v1/hosts/h1/actions/demo.item.get/v1/plan", plan, "", 404, errcode.ActionUnknown, ""},
		{"break-glass plan", "/api/v1/actions/demo.secret.reset/v1/plan", plan, "", 403, errcode.Forbidden, ""},
		{"break-glass apply", "/api/v1/actions/demo.secret.reset/v1/apply", `{"planId":"p"}`, "", 403, errcode.Forbidden, ""},
		{"break-glass read", "/api/v1/actions/demo.secret.show/v1/read", `{"input":{}}`, "", 403, errcode.Forbidden, ""},
		{"missing input", "/api/v1/hosts/h1/actions/demo.item.get/v1/read", `{}`, "", 422, errcode.InvalidInput, "/input"},
		{"missing plan id", "/api/v1/hosts/h1/actions/demo.item.add/v1/apply", `{"confirmNow":true}`, "", 422, errcode.InvalidInput, "/planId"},
		{"short confirm timeout", "/api/v1/hosts/h1/actions/demo.item.add/v1/apply", `{"planId":"p","confirmTimeoutSeconds":9}`, "", 422, errcode.InvalidInput, "/confirmTimeoutSeconds"},
		{"long confirm timeout", "/api/v1/hosts/h1/actions/demo.item.add/v1/apply", `{"planId":"p","confirmTimeoutSeconds":901}`, "", 422, errcode.InvalidInput, "/confirmTimeoutSeconds"},
		{"huge confirm timeout", "/api/v1/hosts/h1/actions/demo.item.add/v1/apply", `{"planId":"p","confirmTimeoutSeconds":9223372036854775807}`, "", 422, errcode.InvalidInput, "/confirmTimeoutSeconds"},
		{"unknown apply field", "/api/v1/hosts/h1/actions/demo.item.add/v1/apply", `{"planId":"p","force":true}`, "", 400, errcode.InvalidJSON, ""},
		{"text body", "/api/v1/hosts/h1/actions/demo.item.add/v1/plan", plan, "text/plain", 415, errcode.UnsupportedMediaType, ""},
		{"dispatcher refusal", "/api/v1/hosts/h1/actions/demo.item.add/v1/plan", plan, "", 409, errcode.ModuleUnavailable, ""},
	} {
		last = call{}
		r := withToken(request("POST", tc.path, tc.body), token)
		if tc.contentType != "" {
			r.Header.Set("Content-Type", tc.contentType)
		}
		rec := h.serve(r)
		recs = append(recs, rec)
		p := decodeProblem(t, rec, tc.status, tc.code)
		if tc.field != "" && (len(p.Errors) != 1 || p.Errors[0].Path != tc.field) {
			t.Errorf("%s: errors %+v, want one at %s", tc.name, p.Errors, tc.field)
		}
		if tc.code != errcode.ModuleUnavailable && last.method != "" {
			t.Errorf("%s: dispatched %s", tc.name, last.method)
		}
	}
	noCredentials(t, h, recs, map[string]string{"token secret": tokenSecret(token)})
}

func TestScopes(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	tokens := map[core.Scope]string{}
	for _, s := range []core.Scope{core.ScopeRead, core.ScopeOperate, core.ScopeAdmin} {
		tokens[s] = h.token(s)
	}
	session, csrf := h.login()
	var recs []*httptest.ResponseRecorder
	for _, tc := range []struct {
		method, path, body string
		need               core.Scope
	}{
		{"GET", "/api/v1/hosts/h1/resources/demo/status", "", core.ScopeRead},
		{"POST", "/api/v1/hosts/h1/actions/demo.item.get/v1/read", `{"input":{}}`, core.ScopeRead},
		{"POST", "/api/v1/actions/demo.item.list/v1/read", `{"input":{}}`, core.ScopeRead},
		{"POST", "/api/v1/hosts/h1/actions/demo.item.add/v1/plan", `{"input":{}}`, core.ScopeOperate},
		{"POST", "/api/v1/hosts/h1/actions/demo.item.add/v1/apply", `{"planId":"p"}`, core.ScopeOperate},
		{"POST", "/api/v1/hosts/h1/actions/demo.item.purge/v1/plan", `{"input":{}}`, core.ScopeOperate},
		{"POST", "/api/v1/actions/demo.item.remove/v1/plan", `{"input":{}}`, core.ScopeAdmin},
		{"POST", "/api/v1/actions/demo.item.remove/v1/apply", `{"planId":"p"}`, core.ScopeAdmin},
		{"GET", "/api/v1/changes", "", core.ScopeRead},
		{"GET", "/api/v1/changes/c1", "", core.ScopeRead},
		{"POST", "/api/v1/changes/c7/confirm", "", core.ScopeOperate},
		{"POST", "/api/v1/changes/c7/revert", "", core.ScopeOperate},
		{"POST", "/api/v1/changes/c1/confirm", "", core.ScopeAdmin},
		{"POST", "/api/v1/changes/c1/revert", "", core.ScopeAdmin},
	} {
		for scope, token := range tokens {
			last = call{}
			rec := h.serve(withToken(request(tc.method, tc.path, tc.body), token))
			recs = append(recs, rec)
			if scope.Allows(tc.need) {
				if rec.Code != 200 || last.method == "" {
					t.Errorf("%s %s with %s: %d %s, want dispatched", tc.method, tc.path, scope, rec.Code, rec.Body)
				}
				continue
			}
			p := decodeProblem(t, rec, 403, errcode.Forbidden)
			// A refused confirm or revert has looked the change up, nothing more.
			if p.Detail != "this request needs the "+string(tc.need)+" scope" || (last.method != "" && last.method != "Change") {
				t.Errorf("%s %s with %s: detail %q, dispatched %q", tc.method, tc.path, scope, p.Detail, last.method)
			}
		}
		rec := h.serve(withSession(request(tc.method, tc.path, tc.body), session, csrf))
		recs = append(recs, rec)
		if rec.Code != 200 {
			t.Errorf("%s %s with an admin session: %d %s", tc.method, tc.path, rec.Code, rec.Body)
		}
	}
	noCredentials(t, h, recs, map[string]string{"session id": session, "CSRF token": csrf,
		"read token secret": tokenSecret(tokens[core.ScopeRead]), "operate token secret": tokenSecret(tokens[core.ScopeOperate]),
		"admin token secret": tokenSecret(tokens[core.ScopeAdmin])})
}

func TestResourceRoute(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	token := h.token(core.ScopeRead)
	rec := h.serve(withToken(request("GET", "/api/v1/hosts/h1/resources/demo/status", ""), token))
	recs := []*httptest.ResponseRecorder{rec}
	if rec.Code != 200 || rec.Body.String() != `{"healthy":true}` {
		t.Errorf("resource: %d %s", rec.Code, rec.Body)
	}
	if last.method != "Resource" || last.hostID != "h1" || last.name != "demo.status" {
		t.Errorf("dispatched %+v", last)
	}

	last = call{}
	rec = h.serve(withToken(request("GET", "/api/v1/hosts/h1/resources/demo/nope", ""), token))
	recs = append(recs, rec)
	decodeProblem(t, rec, 404, errcode.NotFound)
	if last.method != "" {
		t.Errorf("an unknown resource was dispatched: %+v", last)
	}

	h.disp.ResourceFunc = func(context.Context, core.Principal, string, string) (json.RawMessage, error) { return nil, nil }
	rec = h.serve(withToken(request("GET", "/api/v1/hosts/h1/resources/demo/status", ""), token))
	recs = append(recs, rec)
	if rec.Body.String() != "null" {
		t.Errorf("empty output: %s", rec.Body)
	}
	noCredentials(t, h, recs, map[string]string{"token secret": tokenSecret(token)})
}

func TestChangeRoutes(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	token := h.token(core.ScopeOperate)

	rec := h.serve(withToken(request("GET", "/api/v1/changes?state=pending&hostId=h1&before=c9&limit=5", ""), token))
	recs := []*httptest.ResponseRecorder{rec}
	if rec.Code != 200 || rec.Body.String() != `{"changes":[]}` {
		t.Errorf("list: %d %s", rec.Code, rec.Body)
	}
	if want := (core.ChangeFilter{State: "pending", HostID: "h1", Before: "c9", Limit: 5}); last.filter != want {
		t.Errorf("filter %+v, want %+v", last.filter, want)
	}
	for _, q := range []string{"limit=0", "limit=501", "limit=x", "state=done"} {
		last = call{}
		rec := h.serve(withToken(request("GET", "/api/v1/changes?"+q, ""), token))
		recs = append(recs, rec)
		p := decodeProblem(t, rec, 422, errcode.InvalidInput)
		if len(p.Errors) != 1 || !strings.HasPrefix(q, p.Errors[0].Path+"=") || last.method != "" {
			t.Errorf("%s: errors %+v, dispatched %q", q, p.Errors, last.method)
		}
	}

	rec = h.serve(withToken(request("GET", "/api/v1/changes/c1", ""), token))
	recs = append(recs, rec)
	want := `{"id":"c1","hostId":null,"action":"demo.item.remove","version":1,"class":"compensable","state":"completed","actor":"user:u1","createdAt":"2026-10-17T12:05:00Z","updatedAt":"2026-10-17T12:05:00Z","revertDeadline":null,"output":null,"error":null,"compensatedBy":null}`
	if rec.Code != 200 || rec.Body.String() != want || last.method != "Change" || last.changeID != "c1" {
		t.Errorf("get: %d %s (dispatched %+v)", rec.Code, rec.Body, last)
	}

	h.disp.ChangesFunc = func(context.Context, core.Principal, core.ChangeFilter) ([]core.ChangeView, error) {
		return []core.ChangeView{{ID: "c2", HostID: "h1", Action: "demo.item.add", Version: 1, Class: core.ClassRevertible,
			State: core.StatePending, RevertDeadline: &deadline, Output: json.RawMessage(`{"ok":true}`), CompensatedBy: "c3"}}, nil
	}
	rec = h.serve(withToken(request("GET", "/api/v1/changes", ""), token))
	recs = append(recs, rec)
	if !strings.Contains(rec.Body.String(), `"hostId":"h1"`) || !strings.Contains(rec.Body.String(), `"revertDeadline":"2026-10-17T12:06:00Z"`) ||
		!strings.Contains(rec.Body.String(), `"output":{"ok":true}`) || !strings.Contains(rec.Body.String(), `"compensatedBy":"c3"`) {
		t.Errorf("list body %s", rec.Body)
	}

	for path, method := range map[string]string{"/api/v1/changes/c7/confirm": "Confirm", "/api/v1/changes/c7/revert": "Revert"} {
		last = call{}
		rec := h.serve(withToken(request("POST", path, ""), token))
		recs = append(recs, rec)
		if rec.Code != 200 || last.method != method || last.changeID != "c7" {
			t.Errorf("%s: %d %s (dispatched %+v)", path, rec.Code, rec.Body, last)
		}
	}

	h.disp.ConfirmFunc = func(context.Context, core.Principal, string) (core.ChangeView, error) {
		return core.ChangeView{}, errcode.New(errcode.ChangeNotPending, "change c7 is confirmed")
	}
	rec = h.serve(withToken(request("POST", "/api/v1/changes/c7/confirm", ""), token))
	recs = append(recs, rec)
	decodeProblem(t, rec, 409, errcode.ChangeNotPending)
	rec = h.serve(withToken(request("PUT", "/api/v1/changes/c7/confirm", ""), token))
	recs = append(recs, rec)
	decodeProblem(t, rec, http.StatusMethodNotAllowed, errcode.MethodNotAllowed)
	if rec.Header().Get("Allow") != "POST" {
		t.Errorf("Allow = %q", rec.Header().Get("Allow"))
	}
	noCredentials(t, h, recs, map[string]string{"token secret": tokenSecret(token)})
}

func TestChangeOpsAuthorizeTheChangesAction(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	operate, admin := h.token(core.ScopeOperate), h.token(core.ScopeAdmin)
	session, csrf := h.login()
	var recs []*httptest.ResponseRecorder
	for _, op := range []string{"confirm", "revert"} {
		for _, tc := range []struct {
			name, id, token string
			session         bool
			status          int
			code            errcode.Code
		}{
			{"agent change with operate", "c7", operate, false, 200, ""},
			{"unregistered agent action with operate", "cua", operate, false, 200, ""},
			{"controller change with operate", "c1", operate, false, 403, errcode.Forbidden},
			{"controller change with admin", "c1", admin, false, 200, ""},
			{"break-glass change with admin", "cbg", admin, false, 403, errcode.Forbidden},
			{"break-glass change with a session", "cbg", "", true, 403, errcode.Forbidden},
			{"unregistered controller action with admin", "cunk", admin, false, 403, errcode.Forbidden},
			{"missing change", "missing", admin, false, 404, errcode.ChangeNotFound},
		} {
			last = call{}
			r := request("POST", "/api/v1/changes/"+tc.id+"/"+op, "")
			if tc.session {
				r = withSession(r, session, csrf)
			} else {
				r = withToken(r, tc.token)
			}
			rec := h.serve(r)
			recs = append(recs, rec)
			if tc.status == 200 {
				want := "Confirm"
				if op == "revert" {
					want = "Revert"
				}
				if rec.Code != 200 || last.method != want || last.changeID != tc.id {
					t.Errorf("%s %s: %d %s, dispatched %+v", op, tc.name, rec.Code, rec.Body, last)
				}
				continue
			}
			decodeProblem(t, rec, tc.status, tc.code)
			if last.method != "Change" || last.changeID != tc.id || !strings.HasPrefix(last.principal.Actor, "token:") && !tc.session {
				t.Errorf("%s %s: last call %+v, want only the change lookup as the caller", op, tc.name, last)
			}
		}
	}
	noCredentials(t, h, recs, map[string]string{"session id": session, "CSRF token": csrf,
		"operate token secret": tokenSecret(operate), "admin token secret": tokenSecret(admin)})
}
