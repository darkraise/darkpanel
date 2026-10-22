package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

// changeOpsEffect is the effect the stubbed change operations answer with.
var changeOpsEffect = core.EffectView{ChangeID: "c7", Seq: 2, Kind: core.EffectModify, Resource: "demo.item:i1", Local: true,
	State: core.EffectUnresolved, Reason: core.ReasonConflict, Before: core.StateDoc{State: json.RawMessage(`{"n":1}`)}}

// changeOpsStub answers the dispatcher's change operations and appends one
// line per call to *got. Undo of cdone answers it reverted and of any other
// change undoing; Repair of c7 answers a plan and of any other change the
// change undoing; Effects of c8 and Steps answer none.
func (h *harness) changeOpsStub(got *[]string) {
	d := h.disp
	view := func(id, state string) core.ChangeView {
		return core.ChangeView{ID: id, HostID: "h1", Action: "demo.item.add", Version: 1, Class: core.ClassRevertible,
			State: state, Actor: "user:u1", CreatedAt: changeTime, UpdatedAt: changeTime}
	}
	d.UndoFunc = func(_ context.Context, _ core.Principal, id string) (core.ChangeView, error) {
		*got = append(*got, "Undo "+id)
		if id == "cdone" {
			return view(id, core.StateReverted), nil
		}
		v := view(id, core.StateUndoing)
		v.Owner = core.OwnerUndo
		return v, nil
	}
	d.RepairFunc = func(_ context.Context, _ core.Principal, id string, req core.RepairRequest) (core.RepairResult, error) {
		*got = append(*got, fmt.Sprintf("Repair %s input=%s step=%s as=%s", id, string(req.Input), req.Step, req.As))
		if id == "c7" {
			return core.RepairResult{Plan: &core.PlanView{PlanID: "rp1", HostID: "h1", Action: "demo.item.repair", Version: 1,
				InputHash: "h", ExpiresAt: changeTime.Add(10 * time.Minute)}}, nil
		}
		v := view(id, core.StateUndoing)
		return core.RepairResult{Change: &v}, nil
	}
	d.ReleaseFunc = func(_ context.Context, _ core.Principal, id, reason string) (core.ReleaseResult, error) {
		*got = append(*got, "Release "+id+" "+reason)
		return core.ReleaseResult{Change: view(id, core.StateAbandoned), Abandoned: []core.EffectView{changeOpsEffect}}, nil
	}
	d.EffectsFunc = func(_ context.Context, _ core.Principal, id string) ([]core.EffectView, error) {
		*got = append(*got, "Effects "+id)
		if id == "c8" {
			return nil, nil
		}
		return []core.EffectView{changeOpsEffect}, nil
	}
	d.ObserveFunc = func(_ context.Context, _ core.Principal, id string, seq int) (core.ObserveResult, error) {
		*got = append(*got, fmt.Sprintf("Observe %s %d", id, seq))
		return core.ObserveResult{Effect: changeOpsEffect, Allowed: []string{core.ResolutionAccept},
			Observation: core.ObservationView{Current: core.StateDoc{State: json.RawMessage(`{"n":2}`)}, Matches: 2}}, nil
	}
	d.ResolveFunc = func(_ context.Context, _ core.Principal, id string, seq int, req core.ResolveRequest) (core.EffectView, error) {
		*got = append(*got, fmt.Sprintf("Resolve %s %d %s %s", id, seq, req.Resolution, req.Resource))
		return changeOpsEffect, nil
	}
	d.StepsFunc = func(_ context.Context, _ core.Principal, id string) ([]core.StepView, error) {
		*got = append(*got, "Steps "+id)
		return nil, nil
	}
}

func TestChangeOperationRoutes(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	var got []string
	h.changeOpsStub(&got)
	token := h.token(core.ScopeAdmin)
	undoing := `{"id":"c7","hostId":"h1","action":"demo.item.add","version":1,"class":"revertible","state":"undoing",` +
		`"actor":"user:u1","createdAt":"2026-10-17T12:05:00Z","updatedAt":"2026-10-17T12:05:00Z","revertDeadline":null,` +
		`"output":null,"error":null,"compensatedBy":null,"parentChangeId":null,"owner":"undo","repairedBy":null,` +
		`"repairOutcome":null,"override":null}`
	plan := `{"plan":{"planId":"rp1","hostId":"h1","action":"demo.item.repair","version":1,"inputHash":"h","diff":[],` +
		`"guards":[],"expiresAt":"2026-10-17T12:15:00Z","confirmation":null}}`
	for _, tc := range []struct {
		method, path, body string
		status             int
		call               string
		answer             []string
	}{
		{"POST", "/api/v1/changes/c7/undo", "", 202, "Undo c7", []string{undoing}},
		{"POST", "/api/v1/changes/cdone/undo", "", 200, "Undo cdone", []string{`"state":"reverted"`}},
		{"POST", "/api/v1/changes/c7/repair", `{"input":{"outcome":"keep"}}`, 200,
			`Repair c7 input={"outcome":"keep"} step= as=`, []string{plan}},
		{"POST", "/api/v1/changes/c8/repair", `{}`, 202, "Repair c8 input= step= as=",
			[]string{`{"change":{"id":"c8",`, `"state":"undoing"`}},
		{"POST", "/api/v1/changes/c9/repair", `{"step":"db","as":"applied"}`, 202, "Repair c9 input= step=db as=applied",
			[]string{`{"change":{"id":"c9",`}},
		{"POST", "/api/v1/changes/c7/release", `{"reason":"host rebuilt"}`, 200, "Release c7 host rebuilt",
			[]string{`{"change":{"id":"c7",`, `"state":"abandoned"`, `"abandoned":[{"changeId":"c7","seq":2,`}},
		{"GET", "/api/v1/changes/c7/effects", "", 200, "Effects c7", []string{`{"effects":[{"changeId":"c7","seq":2,`}},
		{"GET", "/api/v1/changes/c8/effects", "", 200, "Effects c8", []string{`{"effects":[]}`}},
		{"GET", "/api/v1/changes/c7/effects/2/observe", "", 200, "Observe c7 2", []string{`"allowed":["accept"]`, `"matches":2`}},
		{"POST", "/api/v1/changes/c7/effects/2/resolve", `{"resolution":"reconcile","resource":"demo.item:i2"}`, 200,
			"Resolve c7 2 reconcile demo.item:i2", []string{`{"changeId":"c7","seq":2,`}},
		{"GET", "/api/v1/changes/c7/steps", "", 200, "Steps c7", []string{`{"steps":[]}`}},
	} {
		got = nil
		rec := h.serve(withToken(request(tc.method, tc.path, tc.body), token))
		if rec.Code != tc.status {
			t.Errorf("%s %s: %d %s, want %d", tc.method, tc.path, rec.Code, rec.Body, tc.status)
			continue
		}
		if len(got) != 1 || got[0] != tc.call {
			t.Errorf("%s %s: dispatched %q, want %q", tc.method, tc.path, got, tc.call)
		}
		for _, want := range tc.answer {
			if !strings.Contains(rec.Body.String(), want) {
				t.Errorf("%s %s: answer %s lacks %s", tc.method, tc.path, rec.Body, want)
			}
		}
	}
}

func TestChangeOperationScopes(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	var got []string
	h.changeOpsStub(&got)
	tokens := map[core.Scope]string{}
	for _, s := range []core.Scope{core.ScopeRead, core.ScopeOperate, core.ScopeAdmin} {
		tokens[s] = h.token(s)
	}
	// c7 is a change of the agent-side revertible demo.item.add, c1 of the
	// controller-side compensable demo.item.remove.
	for _, tc := range []struct {
		method, path, body string
		need               core.Scope
	}{
		{"POST", "/api/v1/changes/c7/undo", "", core.ScopeOperate},
		{"POST", "/api/v1/changes/c1/undo", "", core.ScopeAdmin},
		{"POST", "/api/v1/changes/c7/repair", `{}`, core.ScopeOperate},
		{"POST", "/api/v1/changes/c1/repair", `{}`, core.ScopeAdmin},
		{"POST", "/api/v1/changes/c7/release", `{"reason":"r"}`, core.ScopeAdmin},
		{"GET", "/api/v1/changes/c7/effects/2/observe", "", core.ScopeAdmin},
		{"POST", "/api/v1/changes/c7/effects/2/resolve", `{"resolution":"accept"}`, core.ScopeAdmin},
		{"GET", "/api/v1/changes/c7/effects", "", core.ScopeRead},
		{"GET", "/api/v1/changes/c7/steps", "", core.ScopeRead},
	} {
		for scope, token := range tokens {
			got = nil
			rec := h.serve(withToken(request(tc.method, tc.path, tc.body), token))
			if scope.Allows(tc.need) {
				if (rec.Code != http.StatusOK && rec.Code != http.StatusAccepted) || len(got) != 1 {
					t.Errorf("%s %s with %s: %d %s, dispatched %q", tc.method, tc.path, scope, rec.Code, rec.Body, got)
				}
				continue
			}
			p := decodeProblem(t, rec, 403, errcode.Forbidden)
			if p.Detail != "this request needs the "+string(tc.need)+" scope" || len(got) != 0 {
				t.Errorf("%s %s with %s: detail %q, dispatched %q", tc.method, tc.path, scope, p.Detail, got)
			}
		}
	}
}

func TestChangeOperationsHideChanges(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	var got []string
	h.changeOpsStub(&got)
	token := h.token(core.ScopeAdmin)
	// cbg is a break-glass change, which the network hides; missing does not
	// exist. Both answer change.not_found.
	for _, id := range []string{"cbg", "missing"} {
		for _, rt := range []struct{ method, op, body string }{
			{"POST", "undo", ""},
			{"POST", "repair", `{}`},
			{"POST", "release", `{"reason":"r"}`},
			{"GET", "effects", ""},
			{"GET", "effects/2/observe", ""},
			{"POST", "effects/2/resolve", `{"resolution":"accept"}`},
			{"GET", "steps", ""},
		} {
			got = nil
			rec := h.serve(withToken(request(rt.method, "/api/v1/changes/"+id+"/"+rt.op, rt.body), token))
			decodeProblem(t, rec, 404, errcode.ChangeNotFound)
			if len(got) != 0 {
				t.Errorf("%s %s of %s dispatched %q", rt.method, rt.op, id, got)
			}
		}
	}
}

func TestChangeOperationBodies(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	var got []string
	h.changeOpsStub(&got)
	token := h.token(core.ScopeAdmin)
	repair, release := "/api/v1/changes/c7/repair", "/api/v1/changes/c7/release"
	for _, tc := range []struct {
		name, method, path, body string
		status                   int
		code                     errcode.Code
		field                    string
	}{
		{"step without as", "POST", repair, `{"step":"db"}`, 422, errcode.InvalidInput, "/as"},
		{"as without step", "POST", repair, `{"as":"reverted"}`, 422, errcode.InvalidInput, "/step"},
		{"unknown decision", "POST", repair, `{"step":"db","as":"undone"}`, 422, errcode.InvalidInput, "/as"},
		{"input with a step", "POST", repair, `{"input":{"a":1},"step":"db","as":"applied"}`, 422, errcode.InvalidInput, "/input"},
		{"input not an object", "POST", repair, `{"input":[1]}`, 422, errcode.InvalidInput, "/input"},
		{"unknown repair field", "POST", repair, `{"force":true}`, 400, errcode.InvalidJSON, ""},
		{"no repair body", "POST", repair, "", 415, errcode.UnsupportedMediaType, ""},
		{"empty reason", "POST", release, `{"reason":""}`, 422, errcode.InvalidInput, "/reason"},
		{"long reason", "POST", release, `{"reason":"` + strings.Repeat("é", 501) + `"}`, 422, errcode.InvalidInput, "/reason"},
		{"unknown resolution", "POST", "/api/v1/changes/c7/effects/2/resolve", `{"resolution":"ignore"}`, 422,
			errcode.InvalidInput, "/resolution"},
		{"zero seq", "GET", "/api/v1/changes/c7/effects/0/observe", "", 422, errcode.InvalidInput, "seq"},
		{"seq not a number", "POST", "/api/v1/changes/c7/effects/x/resolve", `{"resolution":"accept"}`, 422,
			errcode.InvalidInput, "seq"},
	} {
		got = nil
		rec := h.serve(withToken(request(tc.method, tc.path, tc.body), token))
		p := decodeProblem(t, rec, tc.status, tc.code)
		if tc.field != "" && (len(p.Errors) != 1 || p.Errors[0].Path != tc.field) {
			t.Errorf("%s: errors %+v, want one at %s", tc.name, p.Errors, tc.field)
		}
		if len(got) != 0 {
			t.Errorf("%s: dispatched %q", tc.name, got)
		}
	}
	// 500 characters of two bytes each are accepted.
	got = nil
	rec := h.serve(withToken(request("POST", release, `{"reason":"`+strings.Repeat("é", 500)+`"}`), token))
	if rec.Code != 200 || len(got) != 1 {
		t.Errorf("a 500-character reason: %d %s, dispatched %q", rec.Code, rec.Body, got)
	}
}

func TestApplyAnswersAcceptedWhileApplying(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	h.disp.ApplyFunc = func(_ context.Context, _ core.Principal, hostID string, a core.ActionRef, _ string, _ core.ApplyOptions) (core.ChangeView, error) {
		return core.ChangeView{ID: "change1", HostID: hostID, Action: a.Name, Version: a.Version, State: core.StateApplying,
			CreatedAt: changeTime, UpdatedAt: changeTime}, nil
	}
	token := h.token(core.ScopeAdmin)
	for _, path := range []string{"/api/v1/hosts/h1/actions/demo.item.add/v1/apply", "/api/v1/actions/demo.item.remove/v1/apply"} {
		rec := h.serve(withToken(request("POST", path, `{"planId":"plan1"}`), token))
		if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"state":"applying"`) {
			t.Errorf("%s: %d %s, want 202 with the applying change", path, rec.Code, rec.Body)
		}
	}
}

func TestOpenAPIDocumentsChangeOperations(t *testing.T) {
	reg, err := core.NewRegistry(demoModule{})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := schema.OpenAPI(reg, Routes())
	if err != nil {
		t.Fatal(err)
	}
	// A property's schema may be the boolean true (any value), so each one
	// is decoded on its own.
	var d struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(doc, &d); err != nil {
		t.Fatal(err)
	}
	enum := func(prop json.RawMessage) []string {
		var s struct {
			Enum []string `json:"enum"`
		}
		if err := json.Unmarshal(prop, &s); err != nil {
			t.Fatalf("property schema %s: %v", prop, err)
		}
		return s.Enum
	}
	for path, method := range map[string]string{
		"/api/v1/changes/{id}/undo":                  "post",
		"/api/v1/changes/{id}/repair":                "post",
		"/api/v1/changes/{id}/release":               "post",
		"/api/v1/changes/{id}/effects":               "get",
		"/api/v1/changes/{id}/effects/{seq}/observe": "get",
		"/api/v1/changes/{id}/effects/{seq}/resolve": "post",
		"/api/v1/changes/{id}/steps":                 "get",
	} {
		if _, ok := d.Paths[path][method]; !ok {
			t.Errorf("the document lacks %s %s", method, path)
		}
	}
	props := d.Components.Schemas["Change"].Properties
	for _, name := range []string{"parentChangeId", "owner", "repairedBy", "repairOutcome", "override"} {
		if _, ok := props[name]; !ok {
			t.Errorf("the Change schema lacks %s", name)
		}
	}
	states, classes := enum(props["state"]), enum(props["class"])
	if !slices.Contains(states, core.StateNeedsRepair) || !slices.Contains(classes, string(core.ClassComposite)) {
		t.Errorf("Change state enum %v and class enum %v lack the lifecycle values", states, classes)
	}
}
