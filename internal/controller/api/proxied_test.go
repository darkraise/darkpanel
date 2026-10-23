package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
)

// pxProxied is a request through the NPM runtime listener of
// panel.example.com from the client 203.0.113.7.
func pxProxied(revoked <-chan struct{}) core.Proxied {
	return core.Proxied{
		ListenerID: "L1",
		Client:     netip.MustParseAddr("203.0.113.7"),
		Transport:  netip.MustParseAddrPort("172.31.255.2:40000"),
		PublicHost: "panel.example.com",
		Path:       core.ProxyPath{ExposureID: "C1", Kind: "npm", ProxyAddr: netip.MustParseAddr("172.31.255.2")},
		Revoked:    revoked,
	}
}

// pxProbe records the caller and the guard peer of the request that
// reached it.
type pxProbe struct {
	ran     bool
	caller  caller
	peer    core.Peer
	hasPeer bool
}

func (p *pxProbe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.ran = true
	p.caller, _ = callerFrom(r.Context())
	p.peer, p.hasPeer = core.ClientPeer(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// pxThrough runs r through authenticate over a pxProbe, proxied when p is
// set.
func pxThrough(h *harness, r *http.Request, p *core.Proxied) (*httptest.ResponseRecorder, *pxProbe) {
	if p != nil {
		r = r.WithContext(core.WithProxied(r.Context(), *p))
	}
	probe := &pxProbe{}
	rec := httptest.NewRecorder()
	h.srv.authenticate(probe).ServeHTTP(rec, r)
	return rec, probe
}

func TestProxiedSourceAndPeer(t *testing.T) {
	h := newHarness(t)
	px := pxProxied(nil)
	tests := []struct {
		name    string
		proxied *core.Proxied
		source  string
		peer    core.Peer
	}{
		{"direct", nil, "192.0.2.10", core.Peer{Addr: netip.MustParseAddr("192.0.2.10"), Port: 51234}},
		{"through a runtime listener", &px, "203.0.113.7 via 172.31.255.2", core.Peer{Addr: netip.MustParseAddr("203.0.113.7")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, p := pxThrough(h, withToken(request("GET", "/api/v1/auth/session", ""), h.token(core.ScopeRead)), tt.proxied)
			if rec.Code != http.StatusNoContent || !p.ran {
				t.Fatalf("answer %d %s", rec.Code, rec.Body.String())
			}
			if p.caller.principal.Source != tt.source {
				t.Errorf("Source %q, want %q", p.caller.principal.Source, tt.source)
			}
			if !p.hasPeer || p.peer != tt.peer {
				t.Errorf("guard peer %+v (%v), want %+v", p.peer, p.hasPeer, tt.peer)
			}
		})
	}
}

// TestProxiedLoginSource: a login through a runtime listener is audited
// with both addresses.
func TestProxiedLoginSource(t *testing.T) {
	h := newHarness(t)
	body := `{"username":"admin","password":"` + testPassword + `","code":"` + h.code() + `"}`
	r := request("POST", loginPath, body)
	r = r.WithContext(core.WithProxied(r.Context(), pxProxied(nil)))
	rec := h.serve(r)
	if rec.Code != http.StatusOK {
		t.Fatalf("login answer %d %s", rec.Code, rec.Body.String())
	}
	entries, _, err := h.audit.List(context.Background(), core.AuditQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.Action == auth.AuditLoginAction && e.Source == "203.0.113.7 via 172.31.255.2" {
			found = true
		}
	}
	if !found {
		t.Errorf("no login entry with source 203.0.113.7 via 172.31.255.2 in %+v", entries)
	}
}

func TestProxiedOrigin(t *testing.T) {
	h := newHarness(t)
	session, csrf := h.login()
	px := pxProxied(nil)
	tests := []struct {
		name    string
		method  string
		path    string
		origin  []string
		proxied *core.Proxied
		status  int
	}{
		{"same origin", "POST", "/api/v1/auth/logout", []string{"https://panel.example.com"}, &px, http.StatusNoContent},
		{"no origin", "POST", "/api/v1/auth/logout", nil, &px, http.StatusNoContent},
		{"another origin", "POST", "/api/v1/auth/logout", []string{"https://evil.example"}, &px, http.StatusForbidden},
		{"plain http origin", "POST", "/api/v1/auth/logout", []string{"http://panel.example.com"}, &px, http.StatusForbidden},
		{"two origins", "POST", "/api/v1/auth/logout", []string{"https://panel.example.com", "https://evil.example"}, &px, http.StatusForbidden},
		{"login from another origin", "POST", loginPath, []string{"https://evil.example"}, &px, http.StatusForbidden},
		{"safe method from another origin", "GET", "/api/v1/auth/session", []string{"https://evil.example"}, &px, http.StatusNoContent},
		{"direct request from another origin", "POST", "/api/v1/auth/logout", []string{"https://evil.example"}, nil, http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := withSession(request(tt.method, tt.path, ""), session, csrf)
			for _, o := range tt.origin {
				r.Header.Add("Origin", o)
			}
			rec, _ := pxThrough(h, r, tt.proxied)
			if rec.Code != tt.status {
				t.Fatalf("answer %d %s, want %d", rec.Code, rec.Body.String(), tt.status)
			}
			if tt.status == http.StatusForbidden && !strings.Contains(rec.Body.String(), `"code":"auth.csrf"`) {
				t.Errorf("refusal %s is not auth.csrf", rec.Body.String())
			}
		})
	}
}

// TestProxiedStreamRevoked: a stream through a runtime listener carries
// X-Accel-Buffering: no and ends with a revoked end frame when the
// listener is revoked.
func TestProxiedStreamRevoked(t *testing.T) {
	e := &sseEnv{t: t, auth: &sseAuth{}, clock: coretest.NewFakeClock(testStart), rec: newSSERecorder(),
		src: make(chan sseFrame), done: make(chan string, 1)}
	id := sseIdentity(testStart.Add(time.Hour))
	e.auth.set(id, nil)
	e.srv = sseServer(t, e.auth, nil, e.clock)
	revoked := make(chan struct{})
	r := request("GET", "/api/v1/events", "")
	r = r.WithContext(core.WithProxied(withCaller(r.Context(), caller{principal: id.Principal, identity: id}), pxProxied(revoked)))
	go func() { e.done <- e.srv.sseStream(e.rec, r, e.src) }()
	sseWait(t, "the stream's timers", func() bool { return e.clock.Pending() == 4 })
	if got := e.rec.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering %q, want no", got)
	}
	close(revoked)
	if reason := e.end(); reason != "revoked" {
		t.Errorf("end reason %q, want revoked", reason)
	}
	if text := e.rec.text(); !strings.HasSuffix(text, "event: end\ndata: {\"reason\":\"revoked\"}\n\n") {
		t.Errorf("stream %q does not end with a revoked end frame", text)
	}
}

// TestDirectStreamHeaders: a stream on an ordinary listener keeps its
// headers.
func TestDirectStreamHeaders(t *testing.T) {
	e := sseStart(t)
	if got := e.rec.Header().Get("X-Accel-Buffering"); got != "" {
		t.Errorf("X-Accel-Buffering %q on a direct stream", got)
	}
	close(e.src)
	if reason := e.end(); reason != "done" {
		t.Errorf("end reason %q", reason)
	}
}
