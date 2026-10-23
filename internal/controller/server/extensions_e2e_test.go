package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/ext"
)

// xcContributor is a test controller contribution: a read-scoped web route,
// a socket route and one service. It records the deps it was given; its
// service closes ran when it starts and stopped when its Run returns.
type xcContributor struct {
	deps    chan ext.Deps
	ran     chan struct{}
	stopped chan struct{}
	routes  []ext.Route // nil: the default two routes
	err     error
}

func newXcContributor() *xcContributor {
	return &xcContributor{deps: make(chan ext.Deps, 1), ran: make(chan struct{}), stopped: make(chan struct{})}
}

func xcText(text string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, text) })
}

func (c *xcContributor) Controller(deps ext.Deps) ([]ext.Route, []ext.Service, error) {
	c.deps <- deps
	if c.err != nil {
		return nil, nil, c.err
	}
	routes := c.routes
	if routes == nil {
		routes = []ext.Route{
			{Surface: ext.SurfaceWeb, Method: http.MethodGet, Path: "/api/v1/test/xc", Summary: "Test route",
				Scope: core.ScopeRead, Handler: xcText("web")},
			{Surface: ext.SurfaceSocket, Method: http.MethodGet, Path: "/api/v1/controller/test-xc", Summary: "Test socket route",
				Handler: xcText("socket")},
		}
	}
	svc := ext.Service{Name: "test.xc", Run: func(ctx context.Context) error {
		close(c.ran)
		<-ctx.Done()
		close(c.stopped)
		return ctx.Err()
	}}
	return routes, []ext.Service{svc}, nil
}

// xcWait fails unless ch closes within five seconds.
func xcWait(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not happen", what)
	}
}

func TestContributionsAreServed(t *testing.T) {
	opts := testOptions(t)
	c := newXcContributor()
	opts.Contributors = []ext.Controller{c}
	s := start(t, opts)

	var deps ext.Deps
	select {
	case deps = <-c.deps:
	case <-time.After(5 * time.Second):
		t.Fatal("the contribution was not called")
	}
	switch {
	case deps.DB != s.controllerDB, deps.Sealer != core.Sealer(s.controllerSealer), deps.Clock != opts.Clock,
		deps.Log == nil, deps.Hosts != core.HostDirectory(s.hosts), deps.Dispatch != ext.Dispatcher(s.dispatcher),
		deps.Events != ext.Publisher(s.events), deps.Audit != core.Audit(s.auditLog), deps.LocalHostID != s.LocalHostID():
		t.Errorf("deps = %+v, want the server's stores and services", deps)
	}

	url := baseURL(t, s)
	if code, _ := get(t, plainClient, url+"/api/v1/test/xc", ""); code != http.StatusUnauthorized {
		t.Errorf("GET the contributed web route without credentials = %d, want 401", code)
	}
	token := readToken(t, s, opts.Clock.(*coretest.FakeClock))
	if code, body := get(t, plainClient, url+"/api/v1/test/xc", token); code != http.StatusOK || body != "web" {
		t.Errorf("GET the contributed web route = %d %q, want 200 web", code, body)
	}
	if code, body := get(t, socketClient(s.SocketPath()), "http://controller/api/v1/controller/test-xc", ""); code != http.StatusOK || body != "socket" {
		t.Errorf("socket GET the contributed route = %d %q, want 200 socket", code, body)
	}

	xcWait(t, c.ran, "the contributed service's start")
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.stopped:
	default:
		t.Error("Shutdown returned before the contributed service's Run returned")
	}
}

func TestContributionErrorFailsStart(t *testing.T) {
	opts := testOptions(t)
	c := newXcContributor()
	c.err = errors.New("the contribution is broken")
	opts.Contributors = []ext.Controller{c}
	_, err := Start(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "contributor 0") || !strings.Contains(err.Error(), "the contribution is broken") {
		t.Fatalf("Start = %v, want the contribution's error", err)
	}
	// The failed Start undid itself: a second one with the same paths starts.
	opts.Contributors = nil
	start(t, opts)
}

func TestContributedRouteClashFailsStart(t *testing.T) {
	opts := testOptions(t)
	c := newXcContributor()
	c.routes = []ext.Route{{Surface: ext.SurfaceWeb, Method: http.MethodGet, Path: "/api/v1/changes", Scope: core.ScopeRead,
		Handler: xcText("clash")}}
	opts.Contributors = []ext.Controller{c}
	_, err := Start(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "/api/v1/changes") {
		t.Fatalf("Start = %v, want an error naming the clashing pattern", err)
	}
}
