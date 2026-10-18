package listen

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// fakeBinder stands in for Bind: every endpoint, whatever its address, is
// bound on 127.0.0.1 with port 0, unless fail names an error for it.
type fakeBinder struct {
	mu    sync.Mutex
	calls []Endpoint
	fail  map[netip.AddrPort]error
}

func (b *fakeBinder) bind(ctx context.Context, e Endpoint) (net.Listener, error) {
	b.mu.Lock()
	b.calls = append(b.calls, e)
	err := b.fail[e.Addr]
	b.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("listen: bind %s: %w", e.URL(), err)
	}
	var lc net.ListenConfig
	return lc.Listen(ctx, "tcp4", "127.0.0.1:0")
}

func (b *fakeBinder) called() []Endpoint {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Endpoint(nil), b.calls...)
}

// testTLS returns a server configuration with httptest's certificate (valid
// for 127.0.0.1) and a client that trusts it.
func testTLS(t *testing.T) (*tls.Config, *http.Client) {
	t.Helper()
	ts := httptest.NewTLSServer(http.NotFoundHandler())
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: ts.TLS.Certificates}
	client := ts.Client()
	ts.Close()
	client.Timeout = 5 * time.Second
	return cfg, client
}

func schemeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		answer := "tls=false"
		if r.TLS != nil {
			answer = "tls=true"
		}
		_, _ = io.WriteString(w, answer)
	})
}

func newTestSet(t *testing.T, h http.Handler, b *fakeBinder) (*Set, *http.Client) {
	t.Helper()
	cfg, client := testTLS(t)
	s := NewSet(SetConfig{Handler: h, TLS: cfg, Bind: b.bind, Log: slog.New(slog.DiscardHandler)})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	return s, client
}

func get(t *testing.T, client *http.Client, url string) string {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// boundAddr returns the real address the set bound for e.
func boundAddr(t *testing.T, s *Set, e Endpoint) string {
	t.Helper()
	for _, b := range s.Bound() {
		if b.Endpoint == e {
			return b.Addr.String()
		}
	}
	t.Fatalf("%s is not bound; bound: %v", e.URL(), s.Bound())
	return ""
}

var (
	epHTTP  = Endpoint{Addr: netip.MustParseAddrPort("127.0.0.1:0")}
	epTS1   = Endpoint{Addr: netip.MustParseAddrPort("100.64.0.1:8443"), TLS: true, FreeBind: true}
	epTS2   = Endpoint{Addr: netip.MustParseAddrPort("100.64.0.2:8443"), TLS: true, FreeBind: true}
	epTS3   = Endpoint{Addr: netip.MustParseAddrPort("100.64.0.3:8443"), TLS: true, FreeBind: true}
	epLoop6 = Endpoint{Addr: netip.MustParseAddrPort("[::1]:8080"), Optional: true}
)

func TestSetServesEveryEndpoint(t *testing.T) {
	b := &fakeBinder{}
	s, client := newTestSet(t, schemeHandler(), b)
	if err := s.Update(context.Background(), []Endpoint{epHTTP, epTS1}); err != nil {
		t.Fatal(err)
	}
	if got := get(t, client, "http://"+boundAddr(t, s, epHTTP)+"/"); got != "tls=false" {
		t.Fatalf("HTTP endpoint answered %q", got)
	}
	if got := get(t, client, "https://"+boundAddr(t, s, epTS1)+"/"); got != "tls=true" {
		t.Fatalf("HTTPS endpoint answered %q", got)
	}
	bound := s.Bound()
	if len(bound) != 2 || bound[0].Endpoint != epTS1 || bound[1].Endpoint != epHTTP {
		t.Fatalf("Bound = %v, want the two endpoints sorted by address", bound)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for e, r := range s.servers {
		srv := r.srv
		if srv.ReadHeaderTimeout != 10*time.Second || srv.ReadTimeout != 30*time.Second ||
			srv.WriteTimeout != 60*time.Second || srv.IdleTimeout != 120*time.Second {
			t.Errorf("%s timeouts: header %v, read %v, write %v, idle %v", e.URL(),
				srv.ReadHeaderTimeout, srv.ReadTimeout, srv.WriteTimeout, srv.IdleTimeout)
		}
	}
}

func TestSetUpdateRebindsOnlyTheChange(t *testing.T) {
	b := &fakeBinder{}
	s, client := newTestSet(t, schemeHandler(), b)
	ctx := context.Background()
	if err := s.Update(ctx, []Endpoint{epTS1, epTS2}); err != nil {
		t.Fatal(err)
	}
	gone := boundAddr(t, s, epTS1)
	kept := boundAddr(t, s, epTS2)
	if err := s.Update(ctx, []Endpoint{epTS2, epTS3}); err != nil {
		t.Fatal(err)
	}
	if boundAddr(t, s, epTS2) != kept {
		t.Fatal("an unchanged endpoint was rebound")
	}
	if got := get(t, client, "https://"+boundAddr(t, s, epTS3)+"/"); got != "tls=true" {
		t.Fatalf("new endpoint answered %q", got)
	}
	if gone == boundAddr(t, s, epTS3) {
		t.Skip("the kernel reused the freed port for the new endpoint")
	}
	if _, err := client.Get("https://" + gone + "/"); err == nil {
		t.Fatal("a removed endpoint still answers")
	}
	calls := b.called()
	if len(calls) != 3 || calls[2] != epTS3 {
		t.Fatalf("bind calls = %v, want epTS1, epTS2, epTS3", calls)
	}
}

func TestSetSkipsAMissingOptionalAddress(t *testing.T) {
	b := &fakeBinder{fail: map[netip.AddrPort]error{
		epLoop6.Addr: &net.OpError{Op: "listen", Net: "tcp6", Err: os.NewSyscallError("bind", syscall.EADDRNOTAVAIL)},
		epTS2.Addr:   &net.OpError{Op: "listen", Net: "tcp4", Err: os.NewSyscallError("bind", syscall.EADDRINUSE)},
	}}
	s, _ := newTestSet(t, schemeHandler(), b)
	ctx := context.Background()
	for range 2 {
		if err := s.Update(ctx, []Endpoint{epHTTP, epLoop6}); err != nil {
			t.Fatalf("Update with a missing optional address: %v", err)
		}
	}
	if n := len(b.called()); n != 2 {
		t.Fatalf("bind called %d times, want 2 (the optional address only once)", n)
	}
	err := s.Update(ctx, []Endpoint{epHTTP, epLoop6, epTS1, epTS2})
	if err == nil || !strings.Contains(err.Error(), epTS2.URL()) || !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("Update with a busy address: error = %v", err)
	}
	if bound := s.Bound(); len(bound) != 2 {
		t.Fatalf("Bound = %v, want epHTTP and epTS1", bound)
	}
}

func TestSetRefusesHTTPSWithoutTLSConfig(t *testing.T) {
	b := &fakeBinder{}
	s := NewSet(SetConfig{Handler: schemeHandler(), Bind: b.bind})
	defer func() { _ = s.Shutdown(context.Background()) }()
	err := s.Update(context.Background(), []Endpoint{epTS1})
	if err == nil || !strings.Contains(err.Error(), "needs a TLS configuration") {
		t.Fatalf("Update = %v", err)
	}
}

func TestSetShutdownIsGraceful(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, "done")
	})
	b := &fakeBinder{}
	s, client := newTestSet(t, h, b)
	if err := s.Update(context.Background(), []Endpoint{epHTTP}); err != nil {
		t.Fatal(err)
	}
	url := "http://" + boundAddr(t, s, epHTTP) + "/"
	answer := make(chan string, 1)
	go func() {
		resp, err := client.Get(url)
		if err != nil {
			answer <- err.Error()
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		answer <- string(body)
	}()
	<-entered
	done := make(chan error, 1)
	go func() { done <- s.Shutdown(context.Background()) }()
	select {
	case err := <-done:
		t.Fatalf("Shutdown returned %v while a request was running", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if got := <-answer; got != "done" {
		t.Fatalf("the running request got %q", got)
	}
	if err := <-done; err != nil {
		t.Fatalf("Shutdown = %v", err)
	}
	if err := s.Update(context.Background(), []Endpoint{epHTTP}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Update after Shutdown = %v, want ErrClosed", err)
	}
	if len(s.Bound()) != 0 {
		t.Fatal("Bound is not empty after Shutdown")
	}
	if _, err := client.Get(url); err == nil {
		t.Fatal("the listener still answers after Shutdown")
	}
}

// brokenListener fails every Accept with errBroken.
type brokenListener struct{ net.Listener }

var errBroken = errors.New("accept is broken")

func (brokenListener) Accept() (net.Conn, error) { return nil, errBroken }

// TestSetReportsAServerThatStopsServing shows that a Serve ending with an
// error reaches OnServeError, and that a Shutdown does not.
func TestSetReportsAServerThatStopsServing(t *testing.T) {
	failed := make(chan error, 2)
	b := &fakeBinder{}
	broken := func(ctx context.Context, e Endpoint) (net.Listener, error) {
		ln, err := b.bind(ctx, e)
		if err != nil || e != epHTTP {
			return ln, err
		}
		return brokenListener{ln}, nil
	}
	cfg, _ := testTLS(t)
	s := NewSet(SetConfig{Handler: schemeHandler(), TLS: cfg, Bind: broken, OnServeError: func(err error) { failed <- err }})
	if err := s.Update(context.Background(), []Endpoint{epHTTP, epTS1}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-failed:
		if !errors.Is(err, errBroken) || !strings.Contains(err.Error(), epHTTP.URL()) {
			t.Errorf("OnServeError(%v), want the endpoint and the accept error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnServeError was not called")
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-failed:
		t.Errorf("OnServeError(%v) after Shutdown", err)
	default:
	}
}
