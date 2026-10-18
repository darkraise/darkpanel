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
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/api"
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
)

// baseURL is the URL of the server's IPv4 loopback listener.
func baseURL(t *testing.T, s *Server) string {
	t.Helper()
	for _, b := range s.Listeners() {
		if b.Endpoint.Addr.Addr().Is4() {
			return "http://" + b.Addr.String()
		}
	}
	t.Fatalf("no IPv4 listener in %v", s.Listeners())
	return ""
}

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
	if code, body := get(t, http.DefaultClient, url+"/healthz", ""); code != 200 || strings.TrimSpace(body) != `{"status":"ok"}` {
		t.Errorf("GET /healthz = %d %s", code, body)
	}
	if code, _ := get(t, http.DefaultClient, url+"/api/v1/controller/listeners", ""); code != 401 {
		t.Errorf("GET /api/v1/controller/listeners without credentials = %d, want 401", code)
	}
	token := readToken(t, s, opts.Clock.(*coretest.FakeClock))
	code, body := get(t, http.DefaultClient, url+"/api/v1/controller/listeners", token)
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
	url := baseURL(t, s)
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if resp, err := http.Get(url + "/healthz"); err == nil {
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
		<-done
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
	if err := runUntilFailure(t, opts); !errors.Is(err, errBroken) || !strings.Contains(err.Error(), "http://127.0.0.1") {
		t.Fatalf("Run = %v, want the listener's accept error", err)
	}
	opts.Bind = localBind
	start(t, opts)
}
