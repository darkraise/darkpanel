package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/ext"
)

// asDeps is a contribution that only keeps the deps it was given.
type asDeps struct{ deps ext.Deps }

func (c *asDeps) Controller(deps ext.Deps) ([]ext.Route, []ext.Service, error) {
	c.deps = deps
	return nil, nil, nil
}

// asAdminToken creates an active user with an admin token.
func asAdminToken(t *testing.T, s *Server, clock *coretest.FakeClock) string {
	t.Helper()
	ctx := context.Background()
	_, secret, err := s.authService.CreateUser(ctx, "proxy-admin", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	code, err := auth.TOTPCode(secret, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.authService.ActivateUser(ctx, "proxy-admin", code)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.authService.CreateToken(ctx, u.ID, "proxy", core.ScopeAdmin, 0)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// asWebCAClient is an HTTPS client that trusts the controller's web
// authority and names localhost, a name every web leaf carries.
func asWebCAClient(t *testing.T, s *Server) *http.Client {
	t.Helper()
	code, body := get(t, socketClient(s.SocketPath()), "http://controller/api/v1/controller/web-ca", "")
	var ca struct{ PEM string }
	if err := json.Unmarshal([]byte(body), &ca); err != nil || code != http.StatusOK {
		t.Fatalf("web CA = %d %s", code, body)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(ca.PEM)) {
		t.Fatal("the web CA does not parse")
	}
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS12},
	}}
}

// asPost posts body to the runtime listener at addr as the proxy would,
// naming client in X-Darkpanel-Client; an empty client sends no header.
func asPost(t *testing.T, c *http.Client, addr netip.AddrPort, path, token, client string, body any) (int, []byte, error) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://"+addr.String()+path, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Forwarded-Proto", "https")
	if client != "" {
		req.Header.Set(core.ClientHeaderNPM, client)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	return resp.StatusCode, out, err
}

// TestRuntimeListenerReachesTheAPI: a runtime listener opened through the
// contributions' Listeners serves the API over the web certificate, records
// the proxied client with the proxy's address in the audit log, counts as a
// panel address in the guard topology, and stops serving once revoked.
func TestRuntimeListenerReachesTheAPI(t *testing.T) {
	opts := testOptions(t)
	clock := opts.Clock.(*coretest.FakeClock)
	c := &asDeps{}
	opts.Contributors = []ext.Controller{c}
	s := start(t, opts)
	ctx := context.Background()
	if c.deps.Listeners != core.Listeners(s.runtimeListeners) || c.deps.Listeners == nil {
		t.Fatalf("deps.Listeners = %v, want the server's runtime listeners", c.deps.Listeners)
	}
	if got := s.engineConfig(s.registry).Base.Listeners; got != core.Listeners(s.runtimeListeners) {
		t.Errorf("the controller engine's Env.Listeners = %v, want the server's runtime listeners", got)
	}

	id, err := c.deps.Listeners.Open(ctx, core.ListenerSpec{
		Addr: netip.MustParseAddrPort("127.0.0.1:9443"), AllowedPeer: netip.MustParseAddr("127.0.0.1"),
		ClientHeader: core.ClientHeaderNPM, PublicHost: "panel.example.com", ChangeID: "01ASSEMBLYCHANGE0000000000",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	addrs := s.runtimeListeners.Addrs()
	if len(addrs) != 1 {
		t.Fatalf("runtime listener addresses %v, want one", addrs)
	}
	addr := addrs[0]
	if !slices.Contains(s.topo.read().PanelListen, addr) {
		t.Errorf("topology %v does not list the runtime listener %s", s.topo.read().PanelListen, addr)
	}

	hc := asWebCAClient(t, s)
	token := asAdminToken(t, s, clock)
	in := map[string]any{"input": map[string]any{"name": "via-proxy", "scope": "read", "expiresInDays": 1}}
	code, body, err := asPost(t, hc, addr, "/api/v1/actions/core.token.create/v1/plan", token, "203.0.113.9", in)
	if err != nil || code != http.StatusOK {
		t.Fatalf("plan through the runtime listener = %d %s, %v", code, body, err)
	}
	var plan struct {
		PlanID string `json:"planId"`
	}
	if err := json.Unmarshal(body, &plan); err != nil {
		t.Fatal(err)
	}
	code, body, err = asPost(t, hc, addr, "/api/v1/actions/core.token.create/v1/apply", token, "203.0.113.9",
		map[string]any{"planId": plan.PlanID})
	if err != nil || code != http.StatusOK {
		t.Fatalf("apply through the runtime listener = %d %s, %v", code, body, err)
	}
	var source string
	if err := s.controllerDB.Read().QueryRowContext(ctx,
		`SELECT source FROM core_audit WHERE action = 'core.token.create' AND phase = 'apply' ORDER BY seq DESC, at DESC LIMIT 1`).
		Scan(&source); err != nil {
		t.Fatal(err)
	}
	if source != "203.0.113.9 via 127.0.0.1" {
		t.Errorf("audit source %q, want the client and the proxy's address", source)
	}
	if code, body, err := asPost(t, hc, addr, "/api/v1/actions/core.token.create/v1/plan", token, "", in); err == nil && code < 400 {
		t.Errorf("a request without the client header = %d %s, want a refusal", code, body)
	}

	if err := c.deps.Listeners.Revoke(ctx, id); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if slices.Contains(s.topo.read().PanelListen, addr) {
		t.Errorf("topology %v still lists the revoked listener %s", s.topo.read().PanelListen, addr)
	}
	if code, _, err := asPost(t, hc, addr, "/api/v1/actions/core.token.create/v1/plan", token, "203.0.113.9", in); err == nil && code < 400 {
		t.Errorf("a request after Revoke = %d, want a refusal or no connection", code)
	}
}
