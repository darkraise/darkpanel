package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/transport"
)

// tcpClient serves h on 127.0.0.1 and returns a client of it.
func tcpClient(t *testing.T, h http.Handler, controller *ControllerHello) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()
	return NewClient(ClientConfig{
		Dial: func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", addr)
		},
		Controller: controller,
	})
}

func TestClientRoundTrip(t *testing.T) {
	ctx := context.Background()
	var got []any
	hello := core.AgentHello{ProtocolVersion: 1, BinaryVersion: "0.2.0", OS: "linux", Arch: "arm64", HostID: "h1",
		JournalEpoch: "E1", Modules: []core.ModuleStatus{{ID: "ufw", Version: 1, Available: true}},
		SchemaHashes: map[string]core.SchemaHash{"core.update.apply@1": {Input: "aa", Output: "bb"}}}
	entry := core.JournalEntry{Seq: 3, At: testTime, Actor: "user:u1", Action: "demo.set", Version: 1, Phase: "applied",
		ChangeID: "c1", Input: json.RawMessage(`{"a":1}`), Result: "ok", Source: "203.0.113.7"}
	fake := &coretest.FakeAgent{
		HelloFunc: func(context.Context) (core.AgentHello, error) { return hello, nil },
		ReadFunc: func(_ context.Context, p core.Principal, a core.ActionRef, in json.RawMessage) (json.RawMessage, error) {
			got = append(got, "read", p, a, string(in))
			return json.RawMessage(`{"n":1}`), nil
		},
		ResourceFunc: func(_ context.Context, p core.Principal, name string) (json.RawMessage, error) {
			got = append(got, "resource", p, name)
			return nil, nil
		},
		PlanFunc: func(_ context.Context, p core.Principal, a core.ActionRef, in json.RawMessage, _ core.PlanOptions) (core.PlanView, error) {
			got = append(got, "plan", p, a, string(in))
			return testPlan, nil
		},
		ApplyFunc: func(_ context.Context, p core.Principal, id string, o core.ApplyOptions) (core.ChangeView, error) {
			got = append(got, "apply", p, id, o)
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
		ChangeFunc: func(_ context.Context, id string) (core.ChangeView, error) {
			got = append(got, "change", id)
			return testChange, nil
		},
		ChangesFunc: func(_ context.Context, f core.ChangeFilter) ([]core.ChangeView, error) {
			got = append(got, "changes", f)
			return []core.ChangeView{testChange}, nil
		},
		PreflightFunc: func(_ context.Context, id string) (core.ModuleStatus, error) {
			got = append(got, "preflight", id)
			return core.ModuleStatus{ID: id, Version: 2, Available: true}, nil
		},
		JournalFunc: func(_ context.Context, after int64, limit int) ([]core.JournalEntry, error) {
			got = append(got, "journal", after, limit)
			return []core.JournalEntry{entry}, nil
		},
		AckFunc: func(_ context.Context, seq int64) error {
			got = append(got, "ack", seq)
			return nil
		},
	}
	var hellos []ControllerHello
	h := NewHandler(Config{Agent: fake, Log: discardLog(), OnHello: func(c ControllerHello) { hellos = append(hellos, c) }})
	var mu sync.Mutex
	principals := map[string]string{}
	recorded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		principals[r.Method+" "+r.URL.Path] = r.Header.Get(wire.HeaderPrincipal)
		mu.Unlock()
		h.ServeHTTP(w, r)
	})
	c := tcpClient(t, recorded, &ControllerHello{Version: "0.2.1", ControllerID: "C1"})
	ref := core.ActionRef{Name: "hostinfo.summary", Version: 1}
	opts := core.ApplyOptions{ConfirmTimeout: 2 * time.Minute, Confirmation: "delete h1", ConfirmNow: true}
	filter := core.ChangeFilter{State: "pending", HostID: "h1", Before: "c9", Limit: 10}

	if v, err := c.Hello(ctx); err != nil || !reflect.DeepEqual(v, hello) {
		t.Errorf("Hello = %+v, %v", v, err)
	}
	if want := []ControllerHello{{Protocol: 1, Version: "0.2.1", ControllerID: "C1"}}; !reflect.DeepEqual(hellos, want) {
		t.Errorf("the agent saw the hello %+v, want %+v", hellos, want)
	}
	if out, err := c.Read(ctx, testPrincipal, ref, json.RawMessage(`{"x":true}`)); err != nil || string(out) != `{"n":1}` {
		t.Errorf("Read = %s, %v", out, err)
	}
	if out, err := c.Resource(ctx, testPrincipal, "hostinfo.status"); err != nil || out != nil {
		t.Errorf("Resource = %s, %v; want no output", out, err)
	}
	if v, err := c.Plan(ctx, testPrincipal, ref, nil, core.PlanOptions{}); err != nil || !reflect.DeepEqual(v, testPlan) {
		t.Errorf("Plan = %+v, %v", v, err)
	}
	for name, call := range map[string]func() (core.ChangeView, error){
		"Apply":   func() (core.ChangeView, error) { return c.Apply(ctx, testPrincipal, "p/1", opts) },
		"Confirm": func() (core.ChangeView, error) { return c.Confirm(ctx, testPrincipal, "c1") },
		"Revert":  func() (core.ChangeView, error) { return c.Revert(ctx, testPrincipal, "c1") },
		"Change":  func() (core.ChangeView, error) { return c.Change(ctx, "c1") },
	} {
		if v, err := call(); err != nil || !reflect.DeepEqual(v, testChange) {
			t.Errorf("%s = %+v, %v", name, v, err)
		}
	}
	if vs, err := c.Changes(ctx, filter); err != nil || !reflect.DeepEqual(vs, []core.ChangeView{testChange}) {
		t.Errorf("Changes = %+v, %v", vs, err)
	}
	if st, err := c.Preflight(ctx, "ufw"); err != nil || st != (core.ModuleStatus{ID: "ufw", Version: 2, Available: true}) {
		t.Errorf("Preflight = %+v, %v", st, err)
	}
	if es, err := c.Journal(ctx, 2, 0); err != nil || !reflect.DeepEqual(es, []core.JournalEntry{entry}) {
		t.Errorf("Journal = %+v, %v", es, err)
	}
	if err := c.Ack(ctx, 3); err != nil {
		t.Errorf("Ack: %v", err)
	}

	// Map iteration order varies, so the change calls are checked as a set.
	want := []any{
		"read", testPrincipal, ref, `{"x":true}`,
		"resource", testPrincipal, "hostinfo.status",
		"plan", testPrincipal, ref, "null",
	}
	if !reflect.DeepEqual(got[:len(want)], want) {
		t.Errorf("first calls\n got %v\nwant %v", got[:len(want)], want)
	}
	rest := got[len(want):]
	for _, w := range [][]any{
		{"apply", testPrincipal, "p/1", opts}, {"confirm", testPrincipal, "c1"}, {"revert", testPrincipal, "c1"}, {"change", "c1"},
	} {
		found := false
		for i := 0; i+len(w) <= len(rest); i++ {
			found = found || reflect.DeepEqual(rest[i:i+len(w)], w)
		}
		if !found {
			t.Errorf("no call %v in %v", w, rest)
		}
	}
	tail := []any{"changes", filter, "preflight", "ufw", "journal", int64(2), 0, "ack", int64(3)}
	if !reflect.DeepEqual(rest[len(rest)-len(tail):], tail) {
		t.Errorf("last calls\n got %v\nwant %v", rest[len(rest)-len(tail):], tail)
	}

	// The controller sends Darkpanel-Principal on every request: the caller's
	// where the method names one, its own elsewhere.
	if len(principals) != 12 {
		t.Errorf("the agent saw %d routes, want 12: %v", len(principals), principals)
	}
	for route, raw := range principals {
		var p wire.Principal
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Errorf("%s: Darkpanel-Principal %q: %v", route, raw, err)
			continue
		}
		want := controllerPrincipal
		if strings.Contains(route, "/actions/") || strings.Contains(route, "/resources/") || strings.Contains(route, "/plans/") ||
			strings.HasSuffix(route, "/confirm") || strings.HasSuffix(route, "/revert") {
			want = testPrincipal
		}
		if got := p.Core(); got != want {
			t.Errorf("%s: Darkpanel-Principal = %+v, want %+v", route, got, want)
		}
	}
}

func TestClientWithoutController(t *testing.T) {
	fake := &coretest.FakeAgent{HelloFunc: func(context.Context) (core.AgentHello, error) { return core.AgentHello{HostID: "h1"}, nil }}
	// A transport handler refuses a hello without the controller's half,
	// which is how a socket client's hello would look to it.
	h := NewHandler(Config{Agent: fake, Log: discardLog()})
	var principal atomic.Value
	c := tcpClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal.Store(r.Header.Get(wire.HeaderPrincipal))
		h.ServeHTTP(w, r)
	}), nil)
	var e *errcode.Error
	if _, err := c.Hello(context.Background()); !errors.As(err, &e) || e.Code != errcode.InvalidInput {
		t.Errorf("Hello without a controller = %v, want request.invalid_input from the transport handler", err)
	}
	// A socket client is not the controller and sends no principal of its own.
	if got := principal.Load(); got != "" {
		t.Errorf("Hello without a controller sent Darkpanel-Principal %q", got)
	}
}

func TestClientProblems(t *testing.T) {
	ctx := context.Background()
	fields := []errcode.FieldError{{Path: "/name", Message: "required"}}
	fail := error(&errcode.Error{Code: errcode.PlanExpired, Detail: "plan p1 expired", Fields: fields})
	fake := &coretest.FakeAgent{
		ApplyFunc: func(context.Context, core.Principal, string, core.ApplyOptions) (core.ChangeView, error) {
			return core.ChangeView{}, fail
		},
	}
	c := tcpClient(t, NewHandler(Config{Agent: fake, Log: discardLog()}), nil)
	_, err := c.Apply(ctx, testPrincipal, "p1", core.ApplyOptions{})
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != errcode.PlanExpired || e.Detail != "plan p1 expired" || !reflect.DeepEqual(e.Fields, fields) {
		t.Errorf("Apply = %v, want plan.expired with its detail and fields", err)
	}
	fail = errors.New("secret cause")
	if _, err = c.Apply(ctx, testPrincipal, "p1", core.ApplyOptions{}); !errors.As(err, &e) || e.Code != errcode.Internal || strings.Contains(e.Detail, "secret") {
		t.Errorf("Apply = %v, want internal without the cause", err)
	}

	// A code this binary does not know is kept, so the controller reports
	// what a newer agent said.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/agent/v1/changes/c1" {
			w.Header().Set("Content-Type", problemContentType)
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"status":409,"code":"newer.code","detail":"later"}`))
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	raw := NewClient(ClientConfig{Dial: func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", srv.Listener.Addr().String())
	}})
	if _, err := raw.Change(ctx, "c1"); !errors.As(err, &e) || e.Code != "newer.code" || e.Detail != "later" {
		t.Errorf("Change = %v, want the unknown code kept", err)
	}
	if _, err := raw.Change(ctx, "c2"); err == nil || errors.As(err, &e) || !strings.Contains(err.Error(), "502") {
		t.Errorf("Change = %v, want a plain error naming 502", err)
	}
	if _, err := raw.Resource(ctx, testPrincipal, "nodot"); !errors.As(err, &e) || e.Code != errcode.NotFound {
		t.Errorf("Resource without a module = %v, want not_found", err)
	}
}

func TestClientRefusesALargeAnswer(t *testing.T) {
	big := json.RawMessage(`"` + strings.Repeat("a", wire.MaxBody) + `"`)
	fake := &coretest.FakeAgent{
		ReadFunc: func(context.Context, core.Principal, core.ActionRef, json.RawMessage) (json.RawMessage, error) {
			return big, nil
		},
	}
	c := tcpClient(t, NewHandler(Config{Agent: fake, Log: discardLog()}), nil)
	_, err := c.Read(context.Background(), testPrincipal, core.ActionRef{Name: "demo.get", Version: 1}, json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("Read of a 4 MiB answer = %v, want it refused", err)
	}
}

// sessionPair connects a controller and an agent session over loopback TCP;
// the agent serves h on the streams the controller opens.
func sessionPair(t *testing.T, h http.Handler) *transport.Session {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			close(accepted)
			return
		}
		accepted <- c
	}()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	agentConn, ok := <-accepted
	if !ok {
		t.Fatal("accept failed")
	}
	ctrl, err := transport.ControllerSession(conn)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := transport.AgentSession(agentConn)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(agent) }()
	t.Cleanup(func() {
		_ = ctrl.Close()
		_ = srv.Close()
	})
	return ctrl
}

func TestClientOverSession(t *testing.T) {
	fake := &coretest.FakeAgent{HelloFunc: func(context.Context) (core.AgentHello, error) { return core.AgentHello{HostID: "h1"}, nil }}
	ctrl := sessionPair(t, NewHandler(Config{Agent: fake, Log: discardLog()}))
	c := NewClient(ClientConfig{Dial: ctrl.Open, Controller: &ControllerHello{Version: "0.2.0", ControllerID: "C1"}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// More requests than the session has stream slots, one after another and
	// then together: each request must give its stream back.
	for i := range 3 * transport.MaxControllerStreams {
		if _, err := c.Hello(ctx); err != nil {
			t.Fatalf("hello %d: %v", i, err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2*transport.MaxControllerStreams)
	for range 2 * transport.MaxControllerStreams {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Hello(ctx); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent hello: %v", err)
	}
}

func TestClientChangeOps(t *testing.T) {
	ctx := context.Background()
	deadline := testTime.Add(2 * time.Minute)
	change := core.ChangeView{ID: "c1", HostID: "h1", Action: "demo.set", Version: 1, Actor: "user:u1",
		Class: core.ClassCompensable, State: core.StateNeedsRepair, CreatedAt: testTime, UpdatedAt: testTime,
		ParentChangeID: "01COMP", Owner: core.OwnerRepair, RepairedBy: "c2", RepairOutcome: core.RepairKept,
		Override: json.RawMessage(`{"guard":"firewall.lockout"}`),
		Error:    &core.ChangeError{Code: "core.needs_repair", Message: "effect 2 is unresolved", Cause: "change.revert_failed"}}
	effect := core.EffectView{ChangeID: "c1", Seq: 2, Kind: core.EffectCreate, Resource: "demo.item:i1", Local: true,
		CommitBy: &deadline, State: core.EffectUnresolved, Reason: core.ReasonAmbiguous,
		Before:  core.StateDoc{State: json.RawMessage(`null`)},
		After:   &core.StateDoc{State: json.RawMessage(`{"n":1}`)},
		Current: &core.StateDoc{State: json.RawMessage(`{"n":2}`), Version: json.RawMessage(`{"etag":"e2"}`)}}
	observed := core.ObserveResult{Effect: effect, Allowed: []string{core.ResolutionReconcile},
		Observation: core.ObservationView{Current: core.StateDoc{State: json.RawMessage(`{"n":2}`)}, Matches: 2,
			Candidates: []core.Candidate{
				{Resource: "demo.item:i2", State: core.StateDoc{State: json.RawMessage(`{"n":2}`)}},
				{Resource: "demo.item:i3", State: core.StateDoc{State: json.RawMessage(`{"n":3}`)}},
			}}}
	lookup := core.PlanLookup{Status: core.LookupTerminal, ChangeID: "c1", State: core.StateCompleted, Deadline: &deadline,
		Made: 1, RepairOutcome: core.RepairRestored, Output: json.RawMessage(`{"id":"i1"}`),
		Private: json.RawMessage(`{"token":"s3"}`), Error: &core.ChangeError{Code: "core.child_failed", Message: "step db failed"}}
	release := core.ReleaseResult{Change: change, Abandoned: []core.EffectView{effect}}
	call := &core.CallContext{ParentChangeID: "01COMP", ParentAction: "cfx.deploy.add", StepID: "db"}

	// The agent side is a mux written here, so this test checks the client
	// and the wire views alone.
	type seen struct{ route, query, body, call, principal string }
	var mu sync.Mutex
	var got []seen
	mux := http.NewServeMux()
	answer := func(pattern string, status int, v any) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			got = append(got, seen{route: r.Method + " " + r.URL.Path, query: r.URL.RawQuery, body: string(body),
				call: r.Header.Get(wire.HeaderCall), principal: r.Header.Get(wire.HeaderPrincipal)})
			mu.Unlock()
			if v == nil {
				w.WriteHeader(status)
				return
			}
			b, err := json.Marshal(v)
			if err != nil {
				t.Error(err)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write(b)
		})
	}
	answer("POST /agent/v1/changes/c1/undo", http.StatusAccepted, wire.FromChangeView(change))
	answer("POST /agent/v1/changes/c1/repair", http.StatusOK, wire.FromRepairResult(core.RepairResult{Plan: &testPlan}))
	answer("POST /agent/v1/changes/c2/repair", http.StatusAccepted, wire.FromRepairResult(core.RepairResult{Change: &change}))
	answer("POST /agent/v1/changes/c1/release", http.StatusOK, wire.FromReleaseResult(release))
	answer("GET /agent/v1/changes/c1/effects", http.StatusOK, wire.Effects{Effects: []core.EffectView{effect}})
	answer("GET /agent/v1/changes/c1/effects/2/observe", http.StatusOK, observed)
	answer("POST /agent/v1/changes/c1/effects/2/resolve", http.StatusOK, effect)
	answer("GET /agent/v1/plans/p1", http.StatusOK, lookup)
	answer("POST /agent/v1/plans/p1/retire", http.StatusOK, core.PlanLookup{Status: core.LookupRetired})
	answer("POST /agent/v1/changes/c1/private-output/ack", http.StatusNoContent, nil)
	mux.HandleFunc("POST /agent/v1/plans/gone/retire", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", problemContentType)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"type":"about:blank","title":"Plan not found","status":404,"detail":"no plan gone","code":"plan.not_found"}`))
	})
	c := tcpClient(t, mux, nil)

	if v, err := c.Undo(ctx, testPrincipal, "c1", call); err != nil || !reflect.DeepEqual(v, change) {
		t.Errorf("Undo = %+v, %v", v, err)
	}
	res, err := c.Repair(ctx, testPrincipal, "c1", core.RepairRequest{Input: json.RawMessage(`{"outcome":"keep"}`)})
	if err != nil || res.Change != nil || res.Plan == nil || !reflect.DeepEqual(*res.Plan, testPlan) {
		t.Errorf("Repair answered with a plan = %+v, %v", res, err)
	}
	res, err = c.Repair(ctx, testPrincipal, "c2", core.RepairRequest{Step: "db", As: "reverted"})
	if err != nil || res.Plan != nil || res.Change == nil || !reflect.DeepEqual(*res.Change, change) {
		t.Errorf("Repair answered with a change = %+v, %v", res, err)
	}
	if rel, err := c.Release(ctx, testPrincipal, "c1", "host rebuilt", nil); err != nil || !reflect.DeepEqual(rel, release) {
		t.Errorf("Release = %+v, %v", rel, err)
	}
	if es, err := c.Effects(ctx, testPrincipal, "c1"); err != nil || !reflect.DeepEqual(es, []core.EffectView{effect}) {
		t.Errorf("Effects = %+v, %v", es, err)
	}
	if o, err := c.Observe(ctx, testPrincipal, "c1", 2); err != nil || !reflect.DeepEqual(o, observed) {
		t.Errorf("Observe = %+v, %v", o, err)
	}
	resolved, err := c.Resolve(ctx, testPrincipal, "c1", 2, core.ResolveRequest{Resolution: core.ResolutionReconcile, Resource: "demo.item:i2"})
	if err != nil || !reflect.DeepEqual(resolved, effect) {
		t.Errorf("Resolve = %+v, %v", resolved, err)
	}
	if lk, err := c.Lookup(ctx, "p1", true); err != nil || !reflect.DeepEqual(lk, lookup) {
		t.Errorf("Lookup = %+v, %v", lk, err)
	}
	if _, err := c.Lookup(ctx, "p1", false); err != nil {
		t.Errorf("Lookup without private: %v", err)
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
	_, err = c.Retire(ctx, "gone")
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != errcode.PlanNotFound || e.Detail != "no plan gone" {
		t.Errorf("Retire of an unknown plan = %v, want plan.not_found with its detail", err)
	}

	principal := principalHeader(t, testPrincipal)
	// The header is the call's JSON in base64url without padding (spec 1.5).
	callHeader := base64.RawURLEncoding.EncodeToString(
		[]byte(`{"parentChangeId":"01COMP","parentAction":"cfx.deploy.add","stepId":"db","retired":null}`))
	want := []seen{
		{route: "POST /agent/v1/changes/c1/undo", call: callHeader, principal: principal},
		{route: "POST /agent/v1/changes/c1/repair", body: `{"input":{"outcome":"keep"}}`, principal: principal},
		{route: "POST /agent/v1/changes/c2/repair", body: `{"step":"db","as":"reverted"}`, principal: principal},
		{route: "POST /agent/v1/changes/c1/release", body: `{"reason":"host rebuilt"}`, principal: principal},
		{route: "GET /agent/v1/changes/c1/effects", principal: principal},
		{route: "GET /agent/v1/changes/c1/effects/2/observe", principal: principal},
		{route: "POST /agent/v1/changes/c1/effects/2/resolve", body: `{"resolution":"reconcile","resource":"demo.item:i2"}`,
			principal: principal},
		{route: "GET /agent/v1/plans/p1", query: "private=1"},
		{route: "GET /agent/v1/plans/p1"},
		{route: "POST /agent/v1/plans/p1/retire"},
		{route: "POST /agent/v1/changes/c1/private-output/ack"},
		{route: "POST /agent/v1/changes/c1/private-output/ack", body: `{"final":true}`},
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("requests\n got %+v\nwant %+v", got, want)
	}
}

func TestClientSetModuleEnabled(t *testing.T) {
	type seen struct{ method, path, contentType, principal, body string }
	var mu sync.Mutex
	var got []seen
	c := tcpClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, seen{r.Method, r.URL.Path, r.Header.Get("Content-Type"), r.Header.Get(wire.HeaderPrincipal), string(body)})
		mu.Unlock()
		if r.URL.Path != "/agent/v1/modules/demo/enabled" {
			w.Header().Set("Content-Type", problemContentType)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":404,"code":"not_found","detail":"no module nope"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}), &ControllerHello{Version: "0.2.1", ControllerID: "C1"})
	ctx := context.Background()

	if err := c.SetModuleEnabled(ctx, "demo", false); err != nil {
		t.Errorf("SetModuleEnabled(demo, false) = %v", err)
	}
	if err := c.SetModuleEnabled(ctx, "demo", true); err != nil {
		t.Errorf("SetModuleEnabled(demo, true) = %v", err)
	}
	var e *errcode.Error
	if err := c.SetModuleEnabled(ctx, "nope", true); !errors.As(err, &e) || e.Code != errcode.NotFound || e.Detail != "no module nope" {
		t.Errorf("SetModuleEnabled(nope) = %v, want not_found with the agent's detail", err)
	}
	principal := principalHeader(t, controllerPrincipal)
	want := []seen{
		{"PUT", "/agent/v1/modules/demo/enabled", "application/json", principal, `{"enabled":false}`},
		{"PUT", "/agent/v1/modules/demo/enabled", "application/json", principal, `{"enabled":true}`},
		{"PUT", "/agent/v1/modules/nope/enabled", "application/json", principal, `{"enabled":true}`},
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %+v\nwant %+v", got, want)
	}
}

func TestClientAvailability(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.Method+" "+r.URL.Path)
		if r.URL.Path != "/agent/v1/modules/demo/availability" {
			w.Header().Set("Content-Type", problemContentType)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":404,"code":"not_found","detail":"no agent API route"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"module":"demo","available":false,"reason":"no docker","actions":[` +
			`{"name":"demo.set","available":false,"reason":"no docker"},{"name":"demo.get","available":true,"reason":null}]}`))
	}))
	t.Cleanup(srv.Close)
	c := NewClient(ClientConfig{Dial: func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", srv.Listener.Addr().String())
	}})
	ctx := context.Background()
	got, err := c.Availability(ctx, "demo")
	want := core.ModuleAvailability{Module: "demo", Reason: "no docker", Actions: []core.ActionAvailability{
		{Name: "demo.set", Reason: "no docker"}, {Name: "demo.get", Available: true},
	}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Availability = %+v, %v; want %+v", got, err, want)
	}
	_, err = c.Availability(ctx, "nope")
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != errcode.NotFound {
		t.Errorf("Availability of an agent without the route = %v, want not_found", err)
	}
	if len(asked) != 2 || asked[0] != "GET /agent/v1/modules/demo/availability" {
		t.Errorf("requests %v", asked)
	}
}

func TestClientReveal(t *testing.T) {
	ctx := context.Background()
	ref := core.ActionRef{Name: "rvl.key.reveal", Version: 3}
	var mu sync.Mutex
	var got []string
	srv := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, fmt.Sprintf("%s %s internal=%q principal=%s %s", r.Method, r.URL.Path,
			r.Header.Values(wire.HeaderInternal), r.Header.Get(wire.HeaderPrincipal), body))
		mu.Unlock()
		if string(body) == `{"name":"gone"}` {
			w.Header().Set("Content-Type", problemContentType)
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"type":"about:blank","title":"Unknown action","status":404,"detail":"unknown action","code":"action.unknown"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"key":"k-a"}`)
	})
	c := tcpClient(t, srv, &ControllerHello{Version: "0.2.1", ControllerID: "C1"})
	for _, internal := range []bool{false, true} {
		out, err := c.Reveal(ctx, testPrincipal, ref, json.RawMessage(`{"name":"a"}`), internal)
		if err != nil || string(out) != `{"key":"k-a"}` {
			t.Errorf("Reveal(internal %v) = %s, %v", internal, out, err)
		}
	}
	_, err := c.Reveal(ctx, testPrincipal, ref, json.RawMessage(`{"name":"gone"}`), false)
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.ActionUnknown {
		t.Errorf("Reveal answered with problem details = %v, want action.unknown", err)
	}
	p := principalHeader(t, testPrincipal)
	want := []string{
		`POST /agent/v1/reveal/rvl.key.reveal/v3 internal=[] principal=` + p + ` {"name":"a"}`,
		`POST /agent/v1/reveal/rvl.key.reveal/v3 internal=["1"] principal=` + p + ` {"name":"a"}`,
		`POST /agent/v1/reveal/rvl.key.reveal/v3 internal=[] principal=` + p + ` {"name":"gone"}`,
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("requests\n got %q\nwant %q", got, want)
	}
}
