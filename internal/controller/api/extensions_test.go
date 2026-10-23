package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/ext"
)

// xrHandler answers 200 with the caller's actor, so a test sees who reached
// it.
var xrHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	c, _ := callerFrom(r.Context())
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(c.principal.Actor))
})

// xrHarness is newHarness whose server also serves routes, with the
// harness's clock as the streams' clock.
func xrHarness(t *testing.T, routes ...ext.Route) *harness {
	t.Helper()
	h := newHarness(t)
	h.cfg.Extensions, h.cfg.Clock = routes, h.clock
	srv, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.srv = srv
	return h
}

func TestExtensionWebRoute(t *testing.T) {
	h := xrHarness(t,
		ext.Route{Surface: ext.SurfaceWeb, Method: http.MethodGet, Path: "/api/v1/xr/items", Summary: "List items",
			Scope: core.ScopeAdmin, Handler: xrHandler},
		ext.Route{Surface: ext.SurfaceWeb, Method: http.MethodPost, Path: "/api/v1/xr/items/{id}/touch", Summary: "Touch an item",
			Scope: core.ScopeRead, Handler: xrHandler},
	)
	session, csrf := h.login()
	tests := []struct {
		name   string
		r      *http.Request
		status int
		code   string
	}{
		{"no credentials", request("GET", "/api/v1/xr/items", ""), http.StatusUnauthorized, "auth.required"},
		{"scope too low", withToken(request("GET", "/api/v1/xr/items", ""), h.token(core.ScopeOperate)), http.StatusForbidden, "auth.forbidden"},
		{"admin token", withToken(request("GET", "/api/v1/xr/items", ""), h.token(core.ScopeAdmin)), http.StatusOK, ""},
		{"session without CSRF", withSession(request("POST", "/api/v1/xr/items/i1/touch", ""), session, ""), http.StatusForbidden, "auth.csrf"},
		{"session with CSRF", withSession(request("POST", "/api/v1/xr/items/i1/touch", ""), session, csrf), http.StatusOK, ""},
		{"wrong method", withToken(request("DELETE", "/api/v1/xr/items", ""), h.token(core.ScopeAdmin)), http.StatusMethodNotAllowed, "method_not_allowed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := h.serve(tt.r)
			if rec.Code != tt.status {
				t.Fatalf("answer %d %s, want %d", rec.Code, rec.Body.String(), tt.status)
			}
			if tt.code != "" && !strings.Contains(rec.Body.String(), `"code":"`+tt.code+`"`) {
				t.Errorf("answer %s, want code %s", rec.Body.String(), tt.code)
			}
			if tt.code == "" && !strings.HasPrefix(rec.Body.String(), "user:") && !strings.HasPrefix(rec.Body.String(), "token:") {
				t.Errorf("the handler saw actor %q", rec.Body.String())
			}
		})
	}
}

func TestExtensionRouteRefused(t *testing.T) {
	web := func(method, path string, scope core.Scope) ext.Route {
		return ext.Route{Surface: ext.SurfaceWeb, Method: method, Path: path, Scope: scope, Handler: xrHandler}
	}
	tests := []struct {
		name   string
		routes []ext.Route
		want   string
	}{
		{"clash with a built-in route", []ext.Route{web("GET", "/api/v1/changes", core.ScopeRead)}, "GET /api/v1/changes"},
		{"the same route twice", []ext.Route{web("GET", "/api/v1/xr/a", core.ScopeRead), web("GET", "/api/v1/xr/a", core.ScopeRead)},
			"GET /api/v1/xr/a"},
		{"wildcards that overlap", []ext.Route{web("GET", "/api/v1/xr/{a}/b", core.ScopeRead), web("GET", "/api/v1/xr/a/{b}", core.ScopeRead)},
			"/api/v1/xr/a/{b}"},
		{"a UI path", []ext.Route{web("GET", "/settings/xr", core.ScopeRead)}, "must lie under /api/v1/"},
		{"a subtree", []ext.Route{web("GET", "/api/v1/xr/", core.ScopeRead)}, "not a subtree"},
		{"an unknown method", []ext.Route{web("TRACE", "/api/v1/xr/a", core.ScopeRead)}, `method "TRACE"`},
		{"no scope on the web", []ext.Route{web("GET", "/api/v1/xr/a", "")}, `scope ""`},
		{"no handler", []ext.Route{{Surface: ext.SurfaceWeb, Method: "GET", Path: "/api/v1/xr/a", Scope: core.ScopeRead}}, "no handler"},
		{"an unknown surface", []ext.Route{{Surface: 7, Method: "GET", Path: "/api/v1/xr/a", Handler: xrHandler}}, "unknown surface 7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.cfg.Extensions = tt.routes
			if _, err := New(h.cfg); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("New: %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

// TestExtensionStreamDeadline: a stream route's every write renews a
// SSEWriteTimeout deadline; an ordinary route sets none.
func TestExtensionStreamDeadline(t *testing.T) {
	writer := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("one"))
		_, _ = w.Write([]byte("two"))
	})
	h := xrHarness(t,
		ext.Route{Surface: ext.SurfaceWeb, Method: "GET", Path: "/api/v1/xr/stream", Scope: core.ScopeRead, Stream: true, Handler: writer},
		ext.Route{Surface: ext.SurfaceWeb, Method: "GET", Path: "/api/v1/xr/plain", Scope: core.ScopeRead, Handler: writer},
	)
	token := h.token(core.ScopeRead)
	rec := newSSERecorder()
	h.srv.Handler().ServeHTTP(rec, withToken(request("GET", "/api/v1/xr/stream", ""), token))
	if rec.text() != "onetwo" || len(rec.deadlines) != 4 {
		t.Fatalf("stream body %q with %d deadlines, want onetwo with 4", rec.text(), len(rec.deadlines))
	}
	for _, d := range rec.deadlines {
		if d <= SSEWriteTimeout-5*time.Second || d > SSEWriteTimeout {
			t.Errorf("deadline %v ahead, want about %v", d, SSEWriteTimeout)
		}
	}
	plain := newSSERecorder()
	h.srv.Handler().ServeHTTP(plain, withToken(request("GET", "/api/v1/xr/plain", ""), token))
	if plain.text() != "onetwo" || len(plain.deadlines) != 0 {
		t.Errorf("plain body %q with %d deadlines, want onetwo with none", plain.text(), len(plain.deadlines))
	}
}

// TestExtensionStreamRevoked: through a runtime listener a stream route
// gets X-Accel-Buffering: no and a context that ends with the listener.
func TestExtensionStreamRevoked(t *testing.T) {
	waiter := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		<-r.Context().Done()
	})
	h := xrHarness(t, ext.Route{Surface: ext.SurfaceWeb, Method: "GET", Path: "/api/v1/xr/follow", Scope: core.ScopeRead,
		Stream: true, Handler: waiter})
	revoked := make(chan struct{})
	r := withToken(request("GET", "/api/v1/xr/follow", ""), h.token(core.ScopeRead))
	r = r.WithContext(core.WithProxied(r.Context(), pxProxied(revoked)))
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.srv.Handler().ServeHTTP(rec, r)
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("the stream ended before the listener was revoked")
	case <-time.After(50 * time.Millisecond):
	}
	close(revoked)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end when the listener was revoked")
	}
	if got := rec.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering %q, want no", got)
	}
}

// xrWaiter is a stream handler that holds the response open until its
// context ends.
var xrWaiter = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	<-r.Context().Done()
})

// xrOpen serves r in the background and returns a channel closed once the
// handler returned, after the stream's watcher armed its timers.
func xrOpen(t *testing.T, h *harness, r *http.Request) <-chan struct{} {
	t.Helper()
	base := h.clock.Pending()
	done := make(chan struct{})
	go func() {
		h.srv.Handler().ServeHTTP(httptest.NewRecorder(), r)
		close(done)
	}()
	sseWait(t, "the stream's timers", func() bool { return h.clock.Pending() >= base+2 })
	return done
}

// xrEnded fails t unless done closes while step keeps being applied.
func xrEnded(t *testing.T, done <-chan struct{}, step func()) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-done:
			return
		case <-deadline:
			t.Fatal("the stream did not end")
		case <-time.After(5 * time.Millisecond):
			step()
		}
	}
}

// TestExtensionStreamEnds: a contributed stream route ends like the
// built-in streams: on a revoked token at the next re-authentication, at
// the hard limit and at CloseStreams, and not while its credentials hold.
func TestExtensionStreamEnds(t *testing.T) {
	route := ext.Route{Surface: ext.SurfaceWeb, Method: "GET", Path: "/api/v1/xr/follow", Scope: core.ScopeRead,
		Stream: true, Handler: xrWaiter}
	t.Run("credentials hold", func(t *testing.T) {
		h := xrHarness(t, route)
		done := xrOpen(t, h, withToken(request("GET", "/api/v1/xr/follow", ""), h.token(core.ScopeRead)))
		for range 3 {
			h.clock.Advance(SSERecheck)
			time.Sleep(10 * time.Millisecond)
		}
		select {
		case <-done:
			t.Fatal("the stream ended while its token holds")
		default:
		}
		h.srv.CloseStreams()
		xrEnded(t, done, func() {})
	})
	t.Run("token revoked mid-stream", func(t *testing.T) {
		h := xrHarness(t, route)
		info, token, err := h.auth.CreateToken(context.Background(), h.user.ID, "follow", core.ScopeRead, 0)
		if err != nil {
			t.Fatal(err)
		}
		done := xrOpen(t, h, withToken(request("GET", "/api/v1/xr/follow", ""), token))
		if _, err := h.auth.RevokeToken(context.Background(), h.user.ID, info.ID); err != nil {
			t.Fatal(err)
		}
		xrEnded(t, done, func() { h.clock.Advance(SSERecheck) })
	})
	t.Run("hard limit", func(t *testing.T) {
		h := xrHarness(t, route)
		done := xrOpen(t, h, withToken(request("GET", "/api/v1/xr/follow", ""), h.token(core.ScopeRead)))
		xrEnded(t, done, func() { h.clock.Advance(SSEHardLimit) })
	})
	t.Run("CloseStreams", func(t *testing.T) {
		h := xrHarness(t, route)
		done := xrOpen(t, h, withToken(request("GET", "/api/v1/xr/follow", ""), h.token(core.ScopeRead)))
		h.srv.CloseStreams()
		xrEnded(t, done, func() {})
	})
}

// xrAuth is an sseAuth that records whether each authentication was
// passive.
type xrAuth struct {
	sseAuth
	mu      sync.Mutex
	passive []bool
}

func (a *xrAuth) Authenticate(ctx context.Context, c auth.Credentials) (auth.Identity, error) {
	a.mu.Lock()
	a.passive = append(a.passive, c.Passive)
	a.mu.Unlock()
	return a.sseAuth.Authenticate(ctx, c)
}

// TestExtensionStreamPassive: requests for a contributed stream route
// authenticate passively, so an open stream never extends its session;
// other contributed routes do not.
func TestExtensionStreamPassive(t *testing.T) {
	h := newHarness(t)
	a := &xrAuth{}
	a.set(sseIdentity(h.clock.Now().Add(time.Hour)), nil)
	h.cfg.Auth, h.cfg.Clock = a, h.clock
	h.cfg.Extensions = []ext.Route{
		{Surface: ext.SurfaceWeb, Method: "GET", Path: "/api/v1/xr/{id}/follow", Scope: core.ScopeRead, Stream: true, Handler: xrHandler},
		{Surface: ext.SurfaceWeb, Method: "GET", Path: "/api/v1/xr/{id}", Scope: core.ScopeRead, Handler: xrHandler},
	}
	srv, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path    string
		passive bool
	}{
		{"/api/v1/xr/i1/follow", true},
		{"/api/v1/xr/i1", false},
		{"/api/v1/changes", false},
	} {
		a.mu.Lock()
		a.passive = nil
		a.mu.Unlock()
		srv.Handler().ServeHTTP(httptest.NewRecorder(), withToken(request("GET", tc.path, ""), "any"))
		a.mu.Lock()
		got := a.passive
		a.mu.Unlock()
		if len(got) == 0 || got[0] != tc.passive {
			t.Errorf("GET %s authenticated with Passive %v, want %v", tc.path, got, tc.passive)
		}
	}
}
