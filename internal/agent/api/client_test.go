package api

import (
	"context"
	"encoding/json"
	"errors"
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
