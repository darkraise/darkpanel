package api

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// socketFake is an agent whose module "ufw" fails its preflight and whose
// module "hostinfo" passes; it records the principal of every call.
type socketFake struct {
	coretest.FakeAgent
	principals []core.Principal
	preflights []string
}

func newSocketFake() *socketFake {
	f := &socketFake{}
	record := func(p core.Principal) { f.principals = append(f.principals, p) }
	f.HelloFunc = func(context.Context) (core.AgentHello, error) { return core.AgentHello{HostID: "h1"}, nil }
	f.ReadFunc = func(_ context.Context, p core.Principal, _ core.ActionRef, _ json.RawMessage) (json.RawMessage, error) {
		record(p)
		return json.RawMessage(`{}`), nil
	}
	f.ResourceFunc = func(_ context.Context, p core.Principal, _ string) (json.RawMessage, error) {
		record(p)
		return json.RawMessage(`{}`), nil
	}
	f.PlanFunc = func(_ context.Context, p core.Principal, _ core.ActionRef, _ json.RawMessage, _ core.PlanOptions) (core.PlanView, error) {
		record(p)
		return testPlan, nil
	}
	f.ApplyFunc = func(_ context.Context, p core.Principal, _ string, _ core.ApplyOptions) (core.ChangeView, error) {
		record(p)
		return testChange, nil
	}
	f.ConfirmFunc = func(_ context.Context, p core.Principal, _ string) (core.ChangeView, error) {
		record(p)
		return testChange, nil
	}
	f.RevertFunc = func(_ context.Context, p core.Principal, _ string) (core.ChangeView, error) {
		record(p)
		return testChange, nil
	}
	f.ChangeFunc = func(context.Context, string) (core.ChangeView, error) { return testChange, nil }
	f.ChangesFunc = func(context.Context, core.ChangeFilter) ([]core.ChangeView, error) { return nil, nil }
	f.PreflightFunc = func(_ context.Context, id string) (core.ModuleStatus, error) {
		f.preflights = append(f.preflights, id)
		if id == "ufw" {
			return core.ModuleStatus{ID: id, Version: 1, Reason: "ufw is not installed"}, nil
		}
		return core.ModuleStatus{ID: id, Version: 1, Available: true}, nil
	}
	return f
}

// socketHandler serves fake on the socket; enrolled switches enrollment.
func socketHandler(fake *socketFake, enrolled *atomic.Bool) http.Handler {
	return NewSocketHandler(Config{Agent: fake, Log: discardLog()}, SocketConfig{
		Identity: func() (string, bool) { return "h1", enrolled.Load() },
	})
}

func TestSocketPrincipalIsLocalRoot(t *testing.T) {
	fake := newSocketFake()
	var enrolled atomic.Bool
	h := socketHandler(fake, &enrolled)
	// A peer of the socket cannot claim to be anyone else.
	spoof := http.Header{wire.HeaderPrincipal: {principalHeader(t, testPrincipal)}}

	if rec := call(t, h, "POST", "/agent/v1/actions/hostinfo.summary/v1/read", spoof, []byte(`{}`)); rec.Code != 200 {
		t.Fatalf("read answered %d %s", rec.Code, rec.Body)
	}
	enrolled.Store(true)
	for _, path := range []string{"/agent/v1/actions/hostinfo.summary/v1/read", "/agent/v1/actions/hostinfo.summary/v1/plan"} {
		if rec := call(t, h, "POST", path, spoof, []byte(`{}`)); rec.Code != 200 {
			t.Fatalf("%s answered %d %s", path, rec.Code, rec.Body)
		}
	}
	for _, path := range []string{"/agent/v1/plans/p1/apply", "/agent/v1/changes/c1/confirm", "/agent/v1/changes/c1/revert"} {
		var body []byte
		if path == "/agent/v1/plans/p1/apply" {
			body = []byte(`{}`)
		}
		if rec := call(t, h, "POST", path, spoof, body); rec.Code != 200 {
			t.Fatalf("%s answered %d %s", path, rec.Code, rec.Body)
		}
	}
	unenrolled := core.Principal{Actor: core.ActorUnenrolled, Scope: core.ScopeAdmin, Source: SocketSource}
	root := core.Principal{Actor: "local:root@h1", Scope: core.ScopeAdmin, Source: SocketSource}
	want := []core.Principal{unenrolled, root, root, root, root, root}
	if !reflect.DeepEqual(fake.principals, want) {
		t.Errorf("principals\n got %+v\nwant %+v", fake.principals, want)
	}
}

func TestSocketBeforeEnrollment(t *testing.T) {
	fake := newSocketFake()
	var enrolled atomic.Bool
	h := socketHandler(fake, &enrolled)
	for _, path := range []string{"/agent/v1/hello", "/agent/v1/changes/c1", "/agent/v1/changes", "/agent/v1/modules/hostinfo/preflight", "/agent/v1/resources/hostinfo/status"} {
		if rec := call(t, h, "GET", path, nil, nil); rec.Code != 200 {
			t.Errorf("GET %s before enrollment answered %d %s", path, rec.Code, rec.Body)
		}
	}
	for _, tt := range []struct {
		path string
		body []byte
	}{
		{"/agent/v1/actions/hostinfo.summary/v1/plan", []byte(`{}`)},
		{"/agent/v1/plans/p1/apply", []byte(`{}`)},
		{"/agent/v1/changes/c1/confirm", nil},
		{"/agent/v1/changes/c1/revert", nil},
	} {
		wantProblem(t, call(t, h, "POST", tt.path, nil, tt.body), 409, errcode.AgentNotEnrolled)
	}
	if len(fake.principals) != 1 {
		t.Errorf("the agent acted %d times before enrollment, want once (the resource read)", len(fake.principals))
	}
}

func TestSocketPreflight(t *testing.T) {
	fake := newSocketFake()
	var enrolled atomic.Bool
	enrolled.Store(true)
	h := socketHandler(fake, &enrolled)
	wantProblem(t, call(t, h, "POST", "/agent/v1/actions/ufw.status/v1/read", nil, []byte(`{}`)), 409, errcode.ModuleUnavailable)
	p := wantProblem(t, call(t, h, "POST", "/agent/v1/actions/ufw.allow/v1/plan", nil, []byte(`{}`)), 409, errcode.ModuleUnavailable)
	if p.Detail != "module ufw is unavailable: ufw is not installed" {
		t.Errorf("detail = %q", p.Detail)
	}
	wantProblem(t, call(t, h, "GET", "/agent/v1/resources/ufw/rules", nil, nil), 409, errcode.ModuleUnavailable)
	// The core's actions, such as an update, need no module.
	if rec := call(t, h, "POST", "/agent/v1/actions/core.update.status/v1/read", nil, []byte(`{}`)); rec.Code != 200 {
		t.Errorf("core read answered %d %s", rec.Code, rec.Body)
	}
	// Applying a plan is not checked again: its plan was.
	if rec := call(t, h, "POST", "/agent/v1/plans/p1/apply", nil, []byte(`{}`)); rec.Code != 200 {
		t.Errorf("apply answered %d %s", rec.Code, rec.Body)
	}
	if want := []string{"ufw", "ufw", "ufw"}; !reflect.DeepEqual(fake.preflights, want) {
		t.Errorf("preflights %v, want %v", fake.preflights, want)
	}
}

func TestSocketServesNoJournal(t *testing.T) {
	fake := newSocketFake()
	var enrolled atomic.Bool
	enrolled.Store(true)
	h := socketHandler(fake, &enrolled)
	wantProblem(t, call(t, h, "GET", "/agent/v1/journal", nil, nil), 404, errcode.NotFound)
	wantProblem(t, call(t, h, "POST", "/agent/v1/journal/ack", nil, []byte(`{"seq":1}`)), 404, errcode.NotFound)
	// Hello needs no controller headers on the socket.
	if rec := call(t, h, "GET", "/agent/v1/hello", nil, nil); rec.Code != 200 {
		t.Errorf("hello answered %d %s", rec.Code, rec.Body)
	}
}

func TestSocketPlanRefusesGuard(t *testing.T) {
	fake := newSocketFake()
	var inputs []string
	fake.PlanFunc = func(_ context.Context, _ core.Principal, _ core.ActionRef, in json.RawMessage, _ core.PlanOptions) (core.PlanView, error) {
		inputs = append(inputs, string(in))
		return testPlan, nil
	}
	var enrolled atomic.Bool
	enrolled.Store(true)
	sock := socketHandler(fake, &enrolled)
	const path = "/agent/v1/actions/hostinfo.summary/v1/plan"
	for _, tc := range []struct{ body, field string }{
		{`{"guard":{"client":null}}`, "/guard"},
		{`{"name":"x","GUARD":{}}`, "/GUARD"},
	} {
		p := wantProblem(t, call(t, sock, "POST", path, nil, []byte(tc.body)), 422, errcode.InvalidInput)
		if len(p.Errors) != 1 || p.Errors[0].Path != tc.field {
			t.Errorf("%s: field errors %+v, want %s", tc.body, p.Errors, tc.field)
		}
	}
	if len(inputs) != 0 {
		t.Fatalf("a refused plan reached the agent: %q", inputs)
	}
	for _, body := range []string{`{"name":"x"}`, `[1]`} {
		if rec := call(t, sock, "POST", path, nil, []byte(body)); rec.Code != 200 {
			t.Errorf("%s: %d %s, want it planned", body, rec.Code, rec.Body)
		}
	}
	// The controller's session passes the guard the dispatcher filled.
	ctrl := NewHandler(Config{Agent: fake, Log: discardLog()})
	hdr := http.Header{wire.HeaderPrincipal: {principalHeader(t, testPrincipal)}}
	if rec := call(t, ctrl, "POST", path, hdr, []byte(`{"guard":{"client":null}}`)); rec.Code != 200 {
		t.Errorf("controller plan: %d %s, want it planned", rec.Code, rec.Body)
	}
	if want := []string{`{"name":"x"}`, `[1]`, `{"guard":{"client":null}}`}; !reflect.DeepEqual(inputs, want) {
		t.Errorf("the agent planned %q, want %q", inputs, want)
	}
}
