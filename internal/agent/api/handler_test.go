package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var (
	testPrincipal = core.Principal{Actor: "user:u1", UserID: "u1", Scope: core.ScopeOperate, Source: "203.0.113.7"}
	testTime      = time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	testChange    = core.ChangeView{ID: "c1", HostID: "h1", Action: "demo.set", Version: 1, Actor: "user:u1",
		Class: core.ClassCompensable, State: core.StateCompleted, CreatedAt: testTime, UpdatedAt: testTime,
		Output: json.RawMessage(`{"ok":true}`)}
	testPlan = core.PlanView{PlanID: "p1", HostID: "h1", Action: "demo.set", Version: 1, InputHash: "ab",
		Diff: []core.DiffLine{{Op: "+", Text: "x"}}, Guards: []core.Guard{{ID: "g", OK: true, Message: "fine"}}, ExpiresAt: testTime}
)

// discardLog is a logger for handlers whose log output no test reads.
func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// principalHeader is the Darkpanel-Principal header of p.
func principalHeader(t *testing.T, p core.Principal) string {
	t.Helper()
	b, err := json.Marshal(wire.FromPrincipal(p))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// call sends one request to h; a non-nil body is sent as application/json.
func call(t *testing.T, h http.Handler, method, path string, header http.Header, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	for k, v := range header {
		req.Header[k] = v
	}
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// wantProblem checks that rec is problem details with status and code, and
// returns it.
func wantProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code errcode.Code) problem {
	t.Helper()
	var p problem
	if rec.Code != status || rec.Header().Get("Content-Type") != problemContentType {
		t.Fatalf("answer %d %s %s, want %d problem details", rec.Code, rec.Header().Get("Content-Type"), rec.Body, status)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Code != code || p.Status != status {
		t.Fatalf("problem %s (%v), want code %s", rec.Body, err, code)
	}
	return p
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if rec.Code != http.StatusOK {
		t.Fatalf("answer %d %s, want 200", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return v
}

func TestHandlerActionRoutes(t *testing.T) {
	var got []any
	fake := &coretest.FakeAgent{
		ReadFunc: func(_ context.Context, p core.Principal, a core.ActionRef, in json.RawMessage) (json.RawMessage, error) {
			got = append(got, p, a, string(in))
			return json.RawMessage(`{"uptime":5}`), nil
		},
		ResourceFunc: func(_ context.Context, p core.Principal, name string) (json.RawMessage, error) {
			got = append(got, p, name)
			return nil, nil
		},
		PlanFunc: func(_ context.Context, p core.Principal, a core.ActionRef, in json.RawMessage, _ core.PlanOptions) (core.PlanView, error) {
			got = append(got, p, a, string(in))
			return testPlan, nil
		},
		ApplyFunc: func(_ context.Context, p core.Principal, id string, o core.ApplyOptions) (core.ChangeView, error) {
			got = append(got, p, id, o)
			return testChange, nil
		},
		ConfirmFunc: func(_ context.Context, p core.Principal, id string) (core.ChangeView, error) {
			got = append(got, "confirm", p, id)
			return testChange, nil
		},
		RevertFunc: func(_ context.Context, p core.Principal, id string) (core.ChangeView, error) {
			got = append(got, "revert", p, id)
			return testChange, nil
		},
	}
	h := NewHandler(Config{Agent: fake, Log: discardLog()})
	hdr := http.Header{wire.HeaderPrincipal: {principalHeader(t, testPrincipal)}}
	ref := core.ActionRef{Name: "hostinfo.summary", Version: 2}

	if out := call(t, h, "POST", "/agent/v1/actions/hostinfo.summary/v2/read", hdr, []byte(`{"a":1}`)); out.Body.String() != `{"uptime":5}` {
		t.Errorf("read answered %d %s", out.Code, out.Body)
	}
	if out := call(t, h, "GET", "/agent/v1/resources/hostinfo/status", hdr, nil); out.Code != 200 || out.Body.String() != "null" {
		t.Errorf("resource answered %d %s, want 200 null", out.Code, out.Body)
	}
	if v := decode[wire.PlanView](t, call(t, h, "POST", "/agent/v1/actions/hostinfo.summary/v2/plan", hdr, []byte(`{}`))); !reflect.DeepEqual(v.Core(), testPlan) {
		t.Errorf("plan = %+v", v)
	}
	apply := []byte(`{"confirmTimeoutSeconds":60,"confirmation":"yes","confirmNow":true,"later":1}`)
	if v := decode[wire.ChangeView](t, call(t, h, "POST", "/agent/v1/plans/p1/apply", hdr, apply)); !reflect.DeepEqual(v.Core(), testChange) {
		t.Errorf("apply = %+v", v)
	}
	decode[wire.ChangeView](t, call(t, h, "POST", "/agent/v1/changes/c1/confirm", hdr, nil))
	decode[wire.ChangeView](t, call(t, h, "POST", "/agent/v1/changes/c2/revert", hdr, nil))
	want := []any{
		testPrincipal, ref, `{"a":1}`,
		testPrincipal, "hostinfo.status",
		testPrincipal, ref, `{}`,
		testPrincipal, "p1", core.ApplyOptions{ConfirmTimeout: time.Minute, Confirmation: "yes", ConfirmNow: true},
		"confirm", testPrincipal, "c1",
		"revert", testPrincipal, "c2",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("agent calls\n got %v\nwant %v", got, want)
	}
}

func TestHandlerReadRoutes(t *testing.T) {
	var got []any
	entry := core.JournalEntry{Seq: 7, At: testTime, Actor: "user:u1", Action: "demo.set", Version: 1, Phase: "applied", Result: "ok"}
	fake := &coretest.FakeAgent{
		HelloFunc: func(context.Context) (core.AgentHello, error) {
			return core.AgentHello{ProtocolVersion: 1, BinaryVersion: "0.2.0", OS: "linux", Arch: "amd64", HostID: "h1"}, nil
		},
		ChangeFunc: func(_ context.Context, id string) (core.ChangeView, error) {
			got = append(got, "change", id)
			return testChange, nil
		},
		ChangesFunc: func(_ context.Context, f core.ChangeFilter) ([]core.ChangeView, error) {
			got = append(got, f)
			return nil, nil
		},
		PreflightFunc: func(_ context.Context, id string) (core.ModuleStatus, error) {
			got = append(got, "preflight", id)
			return core.ModuleStatus{ID: id, Version: 1, Reason: "ufw is missing"}, nil
		},
		JournalFunc: func(_ context.Context, after int64, limit int) ([]core.JournalEntry, error) {
			got = append(got, after, limit)
			return []core.JournalEntry{entry}, nil
		},
		AckFunc: func(_ context.Context, seq int64) error {
			got = append(got, "ack", seq)
			return nil
		},
	}
	var hellos []ControllerHello
	h := NewHandler(Config{Agent: fake, Log: discardLog(), OnHello: func(c ControllerHello) { hellos = append(hellos, c) }})
	hello := http.Header{wire.HeaderProtocol: {"1"}, wire.HeaderVersion: {"0.2.1"}, wire.HeaderControllerID: {"C1"}}

	if v := decode[wire.Hello](t, call(t, h, "GET", "/agent/v1/hello", hello, nil)); v.HostID != "h1" || v.Arch != "amd64" {
		t.Errorf("hello = %+v", v)
	}
	if want := []ControllerHello{{Protocol: 1, Version: "0.2.1", ControllerID: "C1"}}; !reflect.DeepEqual(hellos, want) {
		t.Errorf("OnHello got %+v, want %+v", hellos, want)
	}
	decode[wire.ChangeView](t, call(t, h, "GET", "/agent/v1/changes/c1", nil, nil))
	if rec := call(t, h, "GET", "/agent/v1/changes?state=pending&limit=5", nil, nil); rec.Body.String() != "[]" {
		t.Errorf("changes answered %s, want []", rec.Body)
	}
	if v := decode[wire.ModuleStatus](t, call(t, h, "GET", "/agent/v1/modules/ufw/preflight", nil, nil)); v.Reason != "ufw is missing" {
		t.Errorf("preflight = %+v", v)
	}
	if v := decode[[]wire.JournalEntry](t, call(t, h, "GET", "/agent/v1/journal?after=6&limit=50", nil, nil)); len(v) != 1 || !reflect.DeepEqual(v[0].Core(), entry) {
		t.Errorf("journal = %+v", v)
	}
	decode[[]wire.JournalEntry](t, call(t, h, "GET", "/agent/v1/journal", nil, nil))
	if rec := call(t, h, "POST", "/agent/v1/journal/ack", nil, []byte(`{"seq":7}`)); rec.Code != http.StatusNoContent {
		t.Errorf("ack answered %d %s, want 204", rec.Code, rec.Body)
	}
	want := []any{
		"change", "c1",
		core.ChangeFilter{State: "pending", Limit: 5},
		"preflight", "ufw",
		int64(6), 50,
		int64(0), 0,
		"ack", int64(7),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("agent calls\n got %v\nwant %v", got, want)
	}
}

func TestHandlerRefusals(t *testing.T) {
	fake := &coretest.FakeAgent{
		ReadFunc: func(context.Context, core.Principal, core.ActionRef, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		},
		HelloFunc: func(context.Context) (core.AgentHello, error) { return core.AgentHello{}, nil },
	}
	h := NewHandler(Config{Agent: fake, Log: discardLog()})
	good := principalHeader(t, testPrincipal)
	read := "/agent/v1/actions/demo.get/v1/read"
	tests := []struct {
		name, method, path string
		header             http.Header
		body               []byte
		status             int
		code               errcode.Code
	}{
		{"no principal", "POST", read, nil, []byte(`{}`), 422, errcode.InvalidInput},
		{"principal not JSON", "POST", read, http.Header{wire.HeaderPrincipal: {"user:u1"}}, []byte(`{}`), 422, errcode.InvalidInput},
		{"principal without actor", "POST", read, http.Header{wire.HeaderPrincipal: {`{"scope":"admin"}`}}, []byte(`{}`), 422, errcode.InvalidInput},
		{"bad version", "POST", "/agent/v1/actions/demo.get/1/read", http.Header{wire.HeaderPrincipal: {good}}, []byte(`{}`), 404, errcode.ActionUnknown},
		{"text body", "POST", read, http.Header{wire.HeaderPrincipal: {good}, "Content-Type": {"text/plain"}}, []byte(`{}`), 415, errcode.UnsupportedMediaType},
		{"no body", "POST", read, http.Header{wire.HeaderPrincipal: {good}, "Content-Type": {"application/json"}}, nil, 400, errcode.InvalidJSON},
		{"two values", "POST", read, http.Header{wire.HeaderPrincipal: {good}}, []byte(`{} {}`), 400, errcode.InvalidJSON},
		{"too large", "POST", read, http.Header{wire.HeaderPrincipal: {good}}, []byte(`"` + strings.Repeat("a", wire.MaxBody) + `"`), 413, errcode.TooLarge},
		{"apply body of the wrong type", "POST", "/agent/v1/plans/p1/apply", http.Header{wire.HeaderPrincipal: {good}}, []byte(`{"confirmNow":"yes"}`), 400, errcode.InvalidJSON},
		{"hello without headers", "GET", "/agent/v1/hello", nil, nil, 422, errcode.InvalidInput},
		{"hello with protocol 0", "GET", "/agent/v1/hello", http.Header{wire.HeaderProtocol: {"0"}, wire.HeaderVersion: {"1"}, wire.HeaderControllerID: {"C"}}, nil, 422, errcode.InvalidInput},
		{"unknown change filter", "GET", "/agent/v1/changes?host=h1", nil, nil, 422, errcode.InvalidInput},
		{"negative journal after", "GET", "/agent/v1/journal?after=-1", nil, nil, 422, errcode.InvalidInput},
		{"zero journal limit", "GET", "/agent/v1/journal?limit=0", nil, nil, 422, errcode.InvalidInput},
		{"unknown journal parameter", "GET", "/agent/v1/journal?before=3", nil, nil, 422, errcode.InvalidInput},
		{"negative ack", "POST", "/agent/v1/journal/ack", nil, []byte(`{"seq":-1}`), 422, errcode.InvalidInput},
		{"unknown route", "GET", "/agent/v1/nothing", nil, nil, 404, errcode.NotFound},
		{"wrong method", "POST", "/agent/v1/hello", nil, nil, 404, errcode.NotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantProblem(t, call(t, h, tt.method, tt.path, tt.header, tt.body), tt.status, tt.code)
		})
	}
}

func TestHandlerAgentErrors(t *testing.T) {
	var fail error
	fake := &coretest.FakeAgent{
		ChangeFunc: func(context.Context, string) (core.ChangeView, error) { return core.ChangeView{}, fail },
	}
	var logged bytes.Buffer
	h := NewHandler(Config{Agent: fake, Log: slog.New(slog.NewTextHandler(&logged, nil))})

	fail = &errcode.Error{Code: errcode.InvalidInput, Detail: "bad input", Fields: []errcode.FieldError{{Path: "/name", Message: "required"}}}
	p := wantProblem(t, call(t, h, "GET", "/agent/v1/changes/c1", nil, nil), 422, errcode.InvalidInput)
	if p.Detail != "bad input" || !reflect.DeepEqual(p.Errors, []errcode.FieldError{{Path: "/name", Message: "required"}}) || p.Title != "Invalid input" {
		t.Errorf("problem = %+v", p)
	}

	fail = errcode.New(errcode.ChangeNotFound, "no change c1")
	if p := wantProblem(t, call(t, h, "GET", "/agent/v1/changes/c1", nil, nil), 404, errcode.ChangeNotFound); p.Detail != "no change c1" {
		t.Errorf("detail = %q", p.Detail)
	}

	fail = errors.New("disk on fire at /var/lib/darkpanel/agent/agent.db")
	p = wantProblem(t, call(t, h, "GET", "/agent/v1/changes/c1", nil, nil), 500, errcode.Internal)
	if p.Detail != "Internal error" || strings.Contains(p.Detail, "disk") {
		t.Errorf("an internal problem shows %q", p.Detail)
	}
	if !strings.Contains(logged.String(), "disk on fire") {
		t.Errorf("the cause was not logged: %s", logged.String())
	}

	fail = &errcode.Error{Code: "newer.code", Detail: "from the future"}
	wantProblem(t, call(t, h, "GET", "/agent/v1/changes/c1", nil, nil), 500, errcode.Internal)
}

func TestCallHeader(t *testing.T) {
	ctx := context.Background()
	var plans []core.PlanOptions
	var applies []core.ApplyOptions
	fake := &coretest.FakeAgent{
		PlanFunc: func(_ context.Context, _ core.Principal, _ core.ActionRef, _ json.RawMessage, opts core.PlanOptions) (core.PlanView, error) {
			plans = append(plans, opts)
			return testPlan, nil
		},
		ApplyFunc: func(_ context.Context, _ core.Principal, _ string, opts core.ApplyOptions) (core.ChangeView, error) {
			applies = append(applies, opts)
			return testChange, nil
		},
	}
	h := NewHandler(Config{Agent: fake, Log: discardLog()})
	c := tcpClient(t, h, nil)
	ref := core.ActionRef{Name: "demo.item.set", Version: 1}
	// RepairOf is never sent: the agent derives it from the repair plan.
	sent := &core.CallContext{ParentChangeID: "01COMP", ParentAction: "cfx.deploy.add", StepID: "db", RepairOf: "01OLD",
		Retired: []string{"x1"}}
	want := &core.CallContext{ParentChangeID: "01COMP", ParentAction: "cfx.deploy.add", StepID: "db", Retired: []string{"x1"}}

	if _, err := c.Plan(ctx, testPrincipal, ref, json.RawMessage(`{}`), core.PlanOptions{Call: sent}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Plan(ctx, testPrincipal, ref, json.RawMessage(`{}`), core.PlanOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Apply(ctx, testPrincipal, "p1", core.ApplyOptions{ConfirmNow: true, Call: sent}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Apply(ctx, testPrincipal, "p1", core.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	if wantPlans := []core.PlanOptions{{Call: want}, {}}; !reflect.DeepEqual(plans, wantPlans) {
		t.Errorf("plan options %+v, want %+v", plans, wantPlans)
	}
	if wantApplies := []core.ApplyOptions{{ConfirmNow: true, Call: want}, {}}; !reflect.DeepEqual(applies, wantApplies) {
		t.Errorf("apply options %+v, want %+v", applies, wantApplies)
	}

	// On the wire the header is the JSON in base64url without padding.
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	principal := principalHeader(t, testPrincipal)
	hdr := http.Header{wire.HeaderPrincipal: {principal},
		wire.HeaderCall: {enc(`{"parentChangeId":"01COMP","parentAction":"cfx.deploy.add","stepId":"db"}`)}}
	if rec := call(t, h, "POST", "/agent/v1/actions/demo.item.set/v1/plan", hdr, []byte(`{}`)); rec.Code != http.StatusOK {
		t.Fatalf("plan with an encoded call: %d %s, want 200", rec.Code, rec.Body)
	}
	wantWire := &core.CallContext{ParentChangeID: "01COMP", ParentAction: "cfx.deploy.add", StepID: "db"}
	if got := plans[len(plans)-1].Call; !reflect.DeepEqual(got, wantWire) {
		t.Errorf("call from the wire = %+v, want %+v", got, wantWire)
	}

	for name, raw := range map[string]string{
		"repairOf":          enc(`{"parentChangeId":"01COMP","repairOf":"01OLD"}`),
		"not JSON":          enc(`parent 01COMP`),
		"wrong type":        enc(`{"stepId":7}`),
		"two values":        enc(`{} {}`),
		"plain JSON":        `{"stepId":"db"}`,
		"padded":            base64.URLEncoding.EncodeToString([]byte(`{}`)),
		"standard alphabet": base64.RawStdEncoding.EncodeToString([]byte(`{"stepId":"??>"}`)),
		"empty":             "",
	} {
		for _, path := range []string{"/agent/v1/actions/demo.item.set/v1/plan", "/agent/v1/plans/p1/apply"} {
			hdr := http.Header{wire.HeaderPrincipal: {principal}, wire.HeaderCall: {raw}}
			rec := call(t, h, "POST", path, hdr, []byte(`{}`))
			if rec.Code != 422 {
				t.Errorf("%s on %s: %d %s, want 422", name, path, rec.Code, rec.Body)
				continue
			}
			wantProblem(t, rec, 422, errcode.InvalidInput)
		}
	}
	if len(plans) != 3 || len(applies) != 2 {
		t.Errorf("a refused call context reached the agent: %d plans, %d applies", len(plans), len(applies))
	}

	// The agent socket refuses the header on every route.
	var enrolled atomic.Bool
	enrolled.Store(true)
	sock := socketHandler(newSocketFake(), &enrolled)
	for _, tc := range []struct {
		method, path string
		body         []byte
	}{
		{"POST", "/agent/v1/actions/hostinfo.summary/v1/plan", []byte(`{}`)},
		{"POST", "/agent/v1/plans/p1/apply", []byte(`{}`)},
		{"GET", "/agent/v1/changes/c1", nil},
	} {
		hdr := http.Header{wire.HeaderCall: {enc(`{"stepId":"db"}`)}}
		wantProblem(t, call(t, sock, tc.method, tc.path, hdr, tc.body), 422, errcode.InvalidInput)
	}
}

// routeOpsCalls records the agent calls the change-operation routes make.
type routeOpsCalls struct {
	mu   sync.Mutex
	list []any
}

func (c *routeOpsCalls) add(v ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.list = append(c.list, v...)
}

func (c *routeOpsCalls) all() []any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.list)
}

func TestHandlerChangeOpRoutes(t *testing.T) {
	ctx := context.Background()
	rec := &routeOpsCalls{}
	effect := core.EffectView{ChangeID: "c1", Seq: 2, Kind: core.EffectModify, Resource: "demo.item:i1", Local: true,
		State: core.EffectUnresolved, Reason: core.ReasonUncertain, Before: core.StateDoc{State: json.RawMessage(`{"n":1}`)}}
	undoing := testChange
	undoing.State = core.StateUndoing
	reverted := testChange
	reverted.State = core.StateReverted
	lookup := core.PlanLookup{Status: core.LookupRunning, ChangeID: "c1", State: core.StateApplying, Owner: core.OwnerEngine}
	fake := &coretest.FakeAgent{
		UndoFunc: func(_ context.Context, p core.Principal, id string, call *core.CallContext) (core.ChangeView, error) {
			rec.add("undo", p, id, call)
			if id == "c3" {
				return reverted, nil
			}
			return undoing, nil
		},
		RepairFunc: func(_ context.Context, p core.Principal, id string, req core.RepairRequest) (core.RepairResult, error) {
			rec.add("repair", p, id, string(req.Input), req.Step, req.As)
			if id == "c1" {
				return core.RepairResult{Plan: &testPlan}, nil
			}
			return core.RepairResult{Change: &undoing}, nil
		},
		ReleaseFunc: func(_ context.Context, p core.Principal, id, reason string, call *core.CallContext) (core.ReleaseResult, error) {
			rec.add("release", p, id, reason, call)
			return core.ReleaseResult{Change: testChange, Abandoned: []core.EffectView{effect}}, nil
		},
		EffectsFunc: func(_ context.Context, p core.Principal, id string) ([]core.EffectView, error) {
			rec.add("effects", p, id)
			if id == "c2" {
				return nil, nil
			}
			return []core.EffectView{effect}, nil
		},
		ObserveFunc: func(_ context.Context, p core.Principal, id string, seq int) (core.ObserveResult, error) {
			rec.add("observe", p, id, seq)
			return core.ObserveResult{Effect: effect, Allowed: []string{core.ResolutionAccept}}, nil
		},
		ResolveFunc: func(_ context.Context, p core.Principal, id string, seq int, req core.ResolveRequest) (core.EffectView, error) {
			rec.add("resolve", p, id, seq, req)
			return effect, nil
		},
		LookupFunc: func(_ context.Context, id string, private bool) (core.PlanLookup, error) {
			rec.add("lookup", id, private)
			return lookup, nil
		},
		RetireFunc: func(_ context.Context, id string) (core.PlanLookup, error) {
			rec.add("retire", id)
			return core.PlanLookup{Status: core.LookupRetired}, nil
		},
		AckPrivateFunc: func(_ context.Context, id string, final bool) error {
			rec.add("ack-private", id, final)
			return nil
		},
	}
	h := NewHandler(Config{Agent: fake, Log: discardLog()})
	c := tcpClient(t, h, nil)
	step := &core.CallContext{ParentChangeID: "01COMP", ParentAction: "cfx.deploy.add", StepID: "db"}

	if v, err := c.Undo(ctx, testPrincipal, "c1", step); err != nil || v.State != core.StateUndoing {
		t.Errorf("Undo = %+v, %v", v, err)
	}
	if res, err := c.Repair(ctx, testPrincipal, "c1", core.RepairRequest{Input: json.RawMessage(`{"outcome":"keep"}`)}); err != nil ||
		res.Plan == nil || !reflect.DeepEqual(*res.Plan, testPlan) || res.Change != nil {
		t.Errorf("Repair with a plan = %+v, %v", res, err)
	}
	if res, err := c.Repair(ctx, testPrincipal, "c2", core.RepairRequest{Step: "db", As: "applied"}); err != nil ||
		res.Change == nil || res.Change.State != core.StateUndoing || res.Plan != nil {
		t.Errorf("Repair with a change = %+v, %v", res, err)
	}
	want := core.ReleaseResult{Change: testChange, Abandoned: []core.EffectView{effect}}
	if rel, err := c.Release(ctx, testPrincipal, "c1", "host rebuilt", step); err != nil || !reflect.DeepEqual(rel, want) {
		t.Errorf("Release = %+v, %v", rel, err)
	}
	if es, err := c.Effects(ctx, testPrincipal, "c1"); err != nil || !reflect.DeepEqual(es, []core.EffectView{effect}) {
		t.Errorf("Effects = %+v, %v", es, err)
	}
	if o, err := c.Observe(ctx, testPrincipal, "c1", 2); err != nil || !reflect.DeepEqual(o.Effect, effect) {
		t.Errorf("Observe = %+v, %v", o, err)
	}
	if e, err := c.Resolve(ctx, testPrincipal, "c1", 2, core.ResolveRequest{Resolution: core.ResolutionAccept}); err != nil ||
		!reflect.DeepEqual(e, effect) {
		t.Errorf("Resolve = %+v, %v", e, err)
	}
	if lk, err := c.Lookup(ctx, "p1", true); err != nil || !reflect.DeepEqual(lk, lookup) {
		t.Errorf("Lookup = %+v, %v", lk, err)
	}
	if lk, err := c.Retire(ctx, "p1"); err != nil || lk.Status != core.LookupRetired {
		t.Errorf("Retire = %+v, %v", lk, err)
	}
	if err := c.AckPrivate(ctx, "c1", false); err != nil {
		t.Errorf("AckPrivate: %v", err)
	}
	if err := c.AckPrivate(ctx, "c1", true); err != nil {
		t.Errorf("final AckPrivate: %v", err)
	}
	wantCalls := []any{
		"undo", testPrincipal, "c1", step,
		"repair", testPrincipal, "c1", `{"outcome":"keep"}`, "", "",
		"repair", testPrincipal, "c2", "", "db", "applied",
		"release", testPrincipal, "c1", "host rebuilt", step,
		"effects", testPrincipal, "c1",
		"observe", testPrincipal, "c1", 2,
		"resolve", testPrincipal, "c1", 2, core.ResolveRequest{Resolution: core.ResolutionAccept},
		"lookup", "p1", true,
		"retire", "p1",
		"ack-private", "c1", false,
		"ack-private", "c1", true,
	}
	if got := rec.all(); !reflect.DeepEqual(got, wantCalls) {
		t.Errorf("agent calls\n got %v\nwant %v", got, wantCalls)
	}

	hdr := http.Header{wire.HeaderPrincipal: {principalHeader(t, testPrincipal)}}
	for _, tc := range []struct {
		method, path string
		body         []byte
		status       int
		contains     string
	}{
		{"POST", "/agent/v1/changes/c1/undo", nil, http.StatusAccepted, `"state":"undoing"`},
		{"POST", "/agent/v1/changes/c3/undo", nil, http.StatusOK, `"state":"reverted"`},
		{"POST", "/agent/v1/changes/c1/repair", []byte(`{}`), http.StatusOK, `{"plan":{"planId":"p1"`},
		{"POST", "/agent/v1/changes/c2/repair", []byte(`{}`), http.StatusAccepted, `{"change":{"id":"c1"`},
		{"GET", "/agent/v1/changes/c2/effects", nil, http.StatusOK, `{"effects":[]}`},
		{"POST", "/agent/v1/changes/c1/private-output/ack", nil, http.StatusNoContent, ""},
	} {
		got := call(t, h, tc.method, tc.path, hdr, tc.body)
		if got.Code != tc.status || !strings.Contains(got.Body.String(), tc.contains) {
			t.Errorf("%s %s: %d %s, want %d with %s", tc.method, tc.path, got.Code, got.Body, tc.status, tc.contains)
		}
	}
	for _, tc := range []struct {
		method, path string
		body         []byte
	}{
		{"GET", "/agent/v1/changes/c1/effects/0/observe", nil},
		{"GET", "/agent/v1/changes/c1/effects/x/observe", nil},
		{"POST", "/agent/v1/changes/c1/effects/-1/resolve", []byte(`{"resolution":"accept"}`)},
		{"GET", "/agent/v1/plans/p1?private=yes", nil},
		{"GET", "/agent/v1/plans/p1?other=1", nil},
		{"GET", "/agent/v1/plans/p1?private=1&private=1", nil},
	} {
		wantProblem(t, call(t, h, tc.method, tc.path, hdr, tc.body), 422, errcode.InvalidInput)
	}
}

func TestSocketServesNoChangeOps(t *testing.T) {
	var enrolled atomic.Bool
	enrolled.Store(true)
	sock := socketHandler(newSocketFake(), &enrolled)
	for _, tc := range []struct {
		method, path string
		body         []byte
	}{
		{"POST", "/agent/v1/changes/c1/undo", nil},
		{"POST", "/agent/v1/changes/c1/repair", []byte(`{}`)},
		{"POST", "/agent/v1/changes/c1/release", []byte(`{"reason":"r"}`)},
		{"GET", "/agent/v1/changes/c1/effects", nil},
		{"GET", "/agent/v1/changes/c1/effects/1/observe", nil},
		{"POST", "/agent/v1/changes/c1/effects/1/resolve", []byte(`{"resolution":"accept"}`)},
		{"GET", "/agent/v1/plans/p1", nil},
		{"POST", "/agent/v1/plans/p1/retire", nil},
		{"POST", "/agent/v1/changes/c1/private-output/ack", nil},
	} {
		wantProblem(t, call(t, sock, tc.method, tc.path, nil, tc.body), 404, errcode.NotFound)
	}
}

func TestHandlerApplyAnswersAccepted(t *testing.T) {
	applying := testChange
	applying.State = core.StateApplying
	fake := &coretest.FakeAgent{
		ApplyFunc: func(context.Context, core.Principal, string, core.ApplyOptions) (core.ChangeView, error) {
			return applying, nil
		},
	}
	h := NewHandler(Config{Agent: fake, Log: discardLog()})
	hdr := http.Header{wire.HeaderPrincipal: {principalHeader(t, testPrincipal)}}
	if rec := call(t, h, "POST", "/agent/v1/plans/p1/apply", hdr, []byte(`{}`)); rec.Code != http.StatusAccepted {
		t.Errorf("apply of an applying change answered %d %s, want 202", rec.Code, rec.Body)
	}
	c := tcpClient(t, h, nil)
	if v, err := c.Apply(context.Background(), testPrincipal, "p1", core.ApplyOptions{}); err != nil || v.State != core.StateApplying {
		t.Errorf("Apply = %+v, %v; want the applying change", v, err)
	}
}
