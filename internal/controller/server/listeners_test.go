package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/api"
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/ext"
)

// baseURL is the URL of the server's plain HTTP IPv4 loopback listener.
func baseURL(t *testing.T, s *Server) string {
	t.Helper()
	for _, b := range s.Listeners() {
		if b.Endpoint.Addr.Addr().Is4() && !b.Endpoint.TLS {
			return "http://" + b.Addr.String()
		}
	}
	t.Fatalf("no IPv4 listener in %v", s.Listeners())
	return ""
}

// plainClient is an HTTP client for the plain network listener.
var plainClient = &http.Client{Timeout: 10 * time.Second}

// socketClient is an HTTP client that dials the controller socket.
func socketClient(path string) *http.Client {
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
	}}
}

func get(t *testing.T, c *http.Client, url, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

// readToken creates an active user with a read token through the identity
// service.
func readToken(t *testing.T, s *Server, clock *coretest.FakeClock) string {
	t.Helper()
	ctx := context.Background()
	_, secret, err := s.authService.CreateUser(ctx, "reader", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	code, err := auth.TOTPCode(secret, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.authService.ActivateUser(ctx, "reader", code)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.authService.CreateToken(ctx, u.ID, "test", core.ScopeRead, 0)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// TestServesTheNetworkAndTheSocket checks the assembled handlers: /healthz
// and the listener state on the network listener, and full health and the
// web root on the controller socket.
func TestServesTheNetworkAndTheSocket(t *testing.T) {
	opts := testOptions(t)
	s := start(t, opts)
	url := baseURL(t, s)
	if code, body := get(t, plainClient, url+"/healthz", ""); code != 200 || strings.TrimSpace(body) != `{"status":"ok"}` {
		t.Errorf("GET /healthz = %d %s", code, body)
	}
	if code, _ := get(t, plainClient, url+"/api/v1/controller/listeners", ""); code != 401 {
		t.Errorf("GET /api/v1/controller/listeners without credentials = %d, want 401", code)
	}
	token := readToken(t, s, opts.Clock.(*coretest.FakeClock))
	code, body := get(t, plainClient, url+"/api/v1/controller/listeners", token)
	if code != 200 || strings.TrimSpace(body) != `{"tailscaleBound":false,"message":"Tailscale listener disabled by --listen."}` {
		t.Errorf("GET /api/v1/controller/listeners = %d %s, want the listener service's state", code, body)
	}

	sock := socketClient(s.SocketPath())
	code, body = get(t, sock, "http://controller/healthz", "")
	var health struct {
		Status, Version string
		SchemaVersion   int
	}
	if err := json.Unmarshal([]byte(body), &health); err != nil || code != 200 || health.Version != "dev" || health.SchemaVersion < 4 {
		t.Errorf("socket GET /healthz = %d %s, want full detail", code, body)
	}
	code, body = get(t, sock, "http://controller/api/v1/controller/web-ca", "")
	var ca struct{ PEM, Fingerprint string }
	if err := json.Unmarshal([]byte(body), &ca); err != nil || code != 200 || len(ca.Fingerprint) != 64 ||
		!strings.HasPrefix(ca.PEM, "-----BEGIN CERTIFICATE-----") {
		t.Errorf("socket GET /api/v1/controller/web-ca = %d %s", code, body)
	}
	st, err := os.Stat(s.SocketPath())
	if err != nil || st.Mode().Perm() != 0o600 || st.Mode().Type() != fs.ModeSocket {
		t.Errorf("controller socket %v (%v), want a socket with mode 0600", st, err)
	}
}

// TestShutdownClosesTheListeners shows that Shutdown stops the network
// listener and removes the controller socket.
func TestShutdownClosesTheListeners(t *testing.T) {
	s, err := Start(context.Background(), testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	url := baseURL(t, s)
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url+"/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := plainClient.Do(req); err == nil {
		_ = resp.Body.Close()
		t.Error("the network listener still answers after Shutdown")
	}
	if _, err := os.Stat(s.SocketPath()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("controller socket after Shutdown: %v, want it removed", err)
	}
}

// TestStartRefusesALiveSocket shows that a socket another process serves
// stops startup, and that the failed start releases the lock.
func TestStartRefusesALiveSocket(t *testing.T) {
	opts := testOptions(t)
	var lc net.ListenConfig
	other, err := lc.Listen(context.Background(), "unix", opts.Paths.ControllerSocket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	if _, err := Start(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "served by another process") {
		t.Fatalf("Start beside a live socket: %v", err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	start(t, opts)
}

// TestRunStopsWhenTheContextEnds is serve's life: Run serves until its
// context ends, then shuts down.
func TestRunStopsWhenTheContextEnds(t *testing.T) {
	opts := testOptions(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- Run(ctx, opts) }()
	sock := socketClient(opts.Paths.ControllerSocket)
	deadline := time.Now().Add(30 * time.Second)
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://controller/healthz", nil)
		if resp, err := sock.Do(req); err == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Run did not serve the controller socket within 30 s")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
	if _, err := os.Stat(opts.Paths.ControllerSocket); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("controller socket after Run: %v, want it removed", err)
	}
}

// TestDefaultListenValues shows that nil Listen means the defaults,
// loopback and tailscale, with no Tailscale interface present.
func TestDefaultListenValues(t *testing.T) {
	opts := testOptions(t)
	opts.Listen = nil
	opts.Interfaces = func(string) ([]netip.Addr, bool, error) { return nil, false, nil }
	s := start(t, opts)
	got := map[string]bool{}
	for _, b := range s.Listeners() {
		got[b.Endpoint.URL()] = true
	}
	if !got["http://127.0.0.1:8080"] {
		t.Errorf("listeners %v, want the loopback default", got)
	}
}

// brokenListener fails every Accept with errBroken.
type brokenListener struct{ net.Listener }

var errBroken = errors.New("accept is broken")

func (brokenListener) Accept() (net.Conn, error) { return nil, errBroken }

// runUntilFailure runs Run with opts and returns its error, failing the test
// when Run does not return on its own.
func runUntilFailure(t *testing.T, opts Options) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, opts) }()
	select {
	case err := <-done:
		return err
	case <-time.After(30 * time.Second):
		cancel()
		t.Logf("Run after cancel: %v", <-done)
		t.Fatal("Run did not return after a server stopped serving")
		return nil
	}
}

// TestRunStopsWhenTheSocketFails shows that Run shuts down and returns the
// error when the controller socket stops serving, and releases the lock.
func TestRunStopsWhenTheSocketFails(t *testing.T) {
	opts := testOptions(t)
	opts.ListenSocket = func(path string) (net.Listener, error) {
		ln, err := api.ListenSocket(path)
		if err != nil {
			return nil, err
		}
		return brokenListener{ln}, nil
	}
	if err := runUntilFailure(t, opts); !errors.Is(err, errBroken) || !strings.Contains(err.Error(), "controller socket") {
		t.Fatalf("Run = %v, want the controller socket's accept error", err)
	}
	if _, err := os.Stat(opts.Paths.ControllerSocket); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("controller socket after Run: %v, want it removed", err)
	}
	opts.ListenSocket = nil
	start(t, opts)
}

// TestRunStopsWhenAListenerFails shows that Run shuts down and returns the
// error when a network listener stops serving.
func TestRunStopsWhenAListenerFails(t *testing.T) {
	opts := testOptions(t)
	opts.Bind = func(ctx context.Context, e listen.Endpoint) (net.Listener, error) {
		ln, err := localBind(ctx, e)
		if err != nil {
			return nil, err
		}
		return brokenListener{ln}, nil
	}
	if err := runUntilFailure(t, opts); !errors.Is(err, errBroken) || !strings.Contains(err.Error(), "listen: serving http://") {
		t.Fatalf("Run = %v, want the listener's accept error", err)
	}
	opts.Bind = localBind
	start(t, opts)
}

// TestServesTheEventStream shows the server's event bus behind GET
// /api/v1/events, and that Shutdown ends an open stream instead of waiting
// for it until its deadline.
func TestServesTheEventStream(t *testing.T) {
	opts := testOptions(t)
	s := start(t, opts)
	token := readToken(t, s, opts.Clock.(*coretest.FakeClock))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL(t, s)+"/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("GET /api/v1/events = %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	s.events.Publish("host", map[string]any{"hostId": "h1", "online": true})
	want := "id: " + s.events.Epoch() + "-1\nevent: host\ndata: {\"hostId\":\"h1\",\"online\":true}\n\n"
	got := make([]byte, len(want))
	if _, err := io.ReadFull(resp.Body, got); err != nil || string(got) != want {
		t.Fatalf("stream = %q (%v), want %q", got, err, want)
	}

	stop, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := s.Shutdown(stop); err != nil {
		t.Fatalf("Shutdown with an open event stream: %v", err)
	}
	if rest, err := io.ReadAll(resp.Body); err != nil || len(rest) != 0 {
		t.Fatalf("after Shutdown the stream read %q, %v; want its end", rest, err)
	}
}

func TestListenerMessageNamesUnboundRuntimeListeners(t *testing.T) {
	for _, tc := range []struct {
		message string
		unbound int
		want    string
	}{
		{"Serving loopback only.", 0, "Serving loopback only."},
		{"Serving loopback only.", 1, "Serving loopback only. One runtime listener could not be bound; retrying."},
		{"Serving loopback only.", 3, "Serving loopback only. 3 runtime listeners could not be bound; retrying."},
		{"", 2, "2 runtime listeners could not be bound; retrying."},
	} {
		if got := withUnbound(tc.message, tc.unbound); got != tc.want {
			t.Errorf("withUnbound(%q, %d) = %q, want %q", tc.message, tc.unbound, got, tc.want)
		}
	}
}

// TestTxTopologyWaitsForRuntimeListeners: a runtime listener that is open
// but cannot be bound, as after a restart before its address exists, keeps
// the guard topology incomplete until the retry binds it.
func TestTxTopologyWaitsForRuntimeListeners(t *testing.T) {
	opts := testOptions(t)
	c := &asDeps{}
	opts.Contributors = []ext.Controller{c}
	first, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !first.topo.read().Complete {
		t.Errorf("a started server's topology %+v, want it complete", first.topo.read())
	}
	spec := core.ListenerSpec{
		Addr: netip.MustParseAddrPort("127.0.0.1:9443"), AllowedPeer: netip.MustParseAddr("127.0.0.1"),
		ClientHeader: core.ClientHeaderNPM, PublicHost: "panel.example.com", ChangeID: "01TOPOLOGYCHANGE000000000",
	}
	if _, err := c.deps.Listeners.Open(context.Background(), spec); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := first.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	var failBind atomic.Bool
	failBind.Store(true)
	opts.Bind = func(ctx context.Context, e listen.Endpoint) (net.Listener, error) {
		if e.Addr == spec.Addr && failBind.Load() {
			return nil, syscall.EADDRNOTAVAIL
		}
		return localBind(ctx, e)
	}
	s := start(t, opts)
	if n := s.runtimeListeners.Unbound(); n != 1 {
		t.Fatalf("Unbound = %d, want the recorded listener waiting for a bind", n)
	}
	if got := s.topo.read(); got.Complete {
		t.Errorf("the topology %+v is complete while a runtime listener is unbound", got)
	}

	failBind.Store(false)
	s.clock.(*coretest.FakeClock).Advance(listen.CheckInterval)
	addrs, _ := s.runtimeListeners.Addrs()
	got := s.topo.read()
	if len(addrs) != 1 || !got.Complete || !slices.Contains(got.PanelListen, addrs[0]) {
		t.Errorf("after the bind recovered the topology %+v with runtime addresses %v, want it complete and listing them", got, addrs)
	}
}

// TestTxRuntimeSourceRegistersAfterStart: the runtime listeners count toward
// the topology only once Start has bound the recorded listeners; a Start
// that fails leaves the topology incomplete.
func TestTxRuntimeSourceRegistersAfterStart(t *testing.T) {
	s := &Server{}
	s.runtimeListeners = listen.NewRuntime(listen.RuntimeConfig{})
	s.topo.setWeb(func() []listen.Bound { return nil })
	s.topo.setNoAgents()
	if err := s.startRuntimeListeners(context.Background(), nil, nil); err == nil {
		t.Fatal("startRuntimeListeners without a handler succeeded")
	}
	if got := s.topo.read(); got.Complete {
		t.Errorf("the topology %+v is complete although the runtime listeners did not start", got)
	}
}
