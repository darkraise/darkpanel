package dockerapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newFakeDaemon serves mux on a Unix socket in a short temporary directory
// and returns a client for it. When apiVersion is not empty it also answers
// GET /version with that API version, so negotiation succeeds.
func newFakeDaemon(t *testing.T, apiVersion string, mux *http.ServeMux) *Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if apiVersion != "" {
		mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, `{"Version":"29.0.0","ApiVersion":%q,"MinAPIVersion":"1.40","Os":"linux","Arch":"amd64"}`, apiVersion)
		})
	}
	l, err := net.Listen("unix", filepath.Join(dir, "d.sock"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(mux)
	_ = srv.Listener.Close()
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	c := New(filepath.Join(dir, "d.sock"))
	t.Cleanup(c.Close)
	return c
}

func TestNegotiationCapsAtMax(t *testing.T) {
	cases := []struct {
		daemon, want string
	}{
		{"1.54", "1.49"},
		{"1.49", "1.49"},
		{"1.45", "1.45"},
		{"1.44", "1.44"},
	}
	for _, tc := range cases {
		t.Run(tc.daemon, func(t *testing.T) {
			mux := http.NewServeMux()
			var path string
			mux.HandleFunc("GET /v"+tc.want+"/info", func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				_, _ = io.WriteString(w, `{"ServerVersion":"29.0.0"}`)
			})
			c := newFakeDaemon(t, tc.daemon, mux)
			info, err := c.Info(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if path != "/v"+tc.want+"/info" || info.ServerVersion != "29.0.0" {
				t.Fatalf("path %q, info %+v", path, info)
			}
			got, err := c.APIVersion(t.Context())
			if err != nil || got != tc.want {
				t.Fatalf("APIVersion = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestNegotiationRefusesOldDaemon(t *testing.T) {
	c := newFakeDaemon(t, "1.43", http.NewServeMux())
	_, err := c.Info(t.Context())
	if !errors.Is(err, ErrUnsupportedVersion) || !strings.Contains(err.Error(), "1.43") {
		t.Fatalf("err = %v, want ErrUnsupportedVersion naming 1.43", err)
	}
}

func TestNegotiationRefusesMalformedVersion(t *testing.T) {
	c := newFakeDaemon(t, "one.two", http.NewServeMux())
	if _, err := c.Info(t.Context()); err == nil || !strings.Contains(err.Error(), `malformed API version "one.two"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestNegotiationRetriesAfterFailure(t *testing.T) {
	mux := http.NewServeMux()
	calls := 0
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, `{"ApiVersion":"1.47"}`)
	})
	c := newFakeDaemon(t, "", mux)
	if _, err := c.APIVersion(t.Context()); err == nil {
		t.Fatal("first negotiation succeeded")
	}
	v, err := c.APIVersion(t.Context())
	if err != nil || v != "1.47" {
		t.Fatalf("second negotiation = %q, %v", v, err)
	}
	if _, err := c.APIVersion(t.Context()); err != nil || calls != 2 {
		t.Fatalf("cached negotiation called /version %d times, err %v", calls, err)
	}
}

func TestAPIErrorClassification(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1.49/info", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("case") {
		case "json":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"no such container: x"}`)
		case "conflict":
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"message":"volume is in use"}`)
		default:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, "  plain failure \n")
		}
	})
	c := newFakeDaemon(t, "1.49", mux)
	call := func(kase string) error {
		_, err := c.call(t.Context(), request{method: http.MethodGet, path: "/info", query: map[string][]string{"case": {kase}}}, nil)
		return err
	}
	err := call("json")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Message != "no such container: x" || !IsNotFound(err) || IsConflict(err) {
		t.Fatalf("json error = %v", err)
	}
	if err := call("conflict"); !IsConflict(err) || IsNotFound(err) {
		t.Fatalf("conflict error = %v", err)
	}
	if err := call("plain"); !errors.As(err, &apiErr) || apiErr.StatusCode != 500 || apiErr.Message != "plain failure" {
		t.Fatalf("plain error = %v", err)
	}
}

func TestUnreachableSocketIsUnavailable(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "missing.sock"))
	defer c.Close()
	_, err := c.Version(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestInfoFirewallBackend(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1.49/info", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ID":"abc","ServerVersion":"28.1.0","Containers":3,"ContainersRunning":2,"Images":5,`+
			`"FirewallBackend":{"Driver":"iptables","Info":[["EnableUserlandProxy","true"]]}}`)
	})
	info, err := newFakeDaemon(t, "1.49", mux).Info(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if info.FirewallBackend == nil || info.FirewallBackend.Driver != "iptables" || info.ContainersRunning != 2 {
		t.Fatalf("info = %+v", info)
	}

	mux = http.NewServeMux()
	mux.HandleFunc("GET /v1.47/info", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ServerVersion":"28.0.4"}`)
	})
	info, err = newFakeDaemon(t, "1.47", mux).Info(t.Context())
	if err != nil || info.FirewallBackend != nil {
		t.Fatalf("info without the field = %+v, %v", info, err)
	}
}

func TestFiltersAndStream(t *testing.T) {
	mux := http.NewServeMux()
	var filters string
	mux.HandleFunc("GET /v1.49/events", func(w http.ResponseWriter, r *http.Request) {
		filters = r.URL.Query().Get("filters")
		_, _ = io.WriteString(w, `{"n":1}`+"\n"+`{"n":2}`+"\n")
	})
	c := newFakeDaemon(t, "1.49", mux)
	q := map[string][]string{}
	if err := (Filters{"label": {"a=b"}}).set(q); err != nil {
		t.Fatal(err)
	}
	if err := (Filters{}).set(q); err != nil || len(q) != 1 {
		t.Fatalf("an empty filter set changed the query: %v", q)
	}
	res, err := c.do(t.Context(), request{method: http.MethodGet, path: "/events", query: q})
	if err != nil {
		t.Fatal(err)
	}
	s := newStream[struct{ N int }](res)
	defer func() { _ = s.Close() }()
	for want := 1; want <= 2; want++ {
		v, err := s.Next()
		if err != nil || v.N != want {
			t.Fatalf("value %d = %+v, %v", want, v, err)
		}
	}
	if _, err := s.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("end of stream = %v, want io.EOF", err)
	}
	if filters != `{"label":["a=b"]}` {
		t.Fatalf("filters = %s", filters)
	}
}

func TestAPIVersionQueuedCallerHonoursItsContext(t *testing.T) {
	mux := http.NewServeMux()
	var versionCalls atomic.Int32
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		versionCalls.Add(1)
		started <- struct{}{}
		<-release
		_, _ = io.WriteString(w, `{"ApiVersion":"1.47"}`)
	})
	c := newFakeDaemon(t, "", mux)
	released := false
	releaseOnce := func() {
		if !released {
			released = true
			close(release)
		}
	}
	defer releaseOnce()

	type result struct {
		v   string
		err error
	}
	first := make(chan result, 1)
	go func() {
		v, err := c.APIVersion(context.Background())
		first <- result{v, err}
	}()
	<-started

	patient := make(chan result, 1)
	go func() {
		v, err := c.APIVersion(context.Background())
		patient <- result{v, err}
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	impatient := make(chan error, 1)
	go func() {
		_, err := c.APIVersion(ctx)
		impatient <- err
	}()
	select {
	case err := <-impatient:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("queued caller err = %v, want the context's deadline error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a queued caller ignored its context while another negotiation was in flight")
	}

	releaseOnce()
	for _, ch := range []chan result{first, patient} {
		if r := <-ch; r.err != nil || r.v != "1.47" {
			t.Fatalf("APIVersion = %q, %v", r.v, r.err)
		}
	}
	if n := versionCalls.Load(); n != 1 {
		t.Fatalf("/version called %d times, want 1", n)
	}
}

func TestNegotiationRepeatsAfterDaemonUnavailable(t *testing.T) {
	dir, err := os.MkdirTemp("", "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	serve := func(apiVersion string) *httptest.Server {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, `{"ApiVersion":%q}`, apiVersion)
		})
		mux.HandleFunc("GET /{version}/info", func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprintf(w, `{"ServerVersion":%q}`, strings.TrimPrefix(r.PathValue("version"), "v")) //nolint:gosec // G705: a test fake answering JSON
		})
		l, err := net.Listen("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewUnstartedServer(mux)
		_ = srv.Listener.Close()
		srv.Listener = l
		srv.Start()
		return srv
	}
	c := New(sock)
	defer c.Close()

	old := serve("1.45")
	if info, err := c.Info(t.Context()); err != nil || info.ServerVersion != "1.45" {
		t.Fatalf("first daemon: %+v, %v", info, err)
	}
	old.Close()
	if _, err := c.Info(t.Context()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("stopped daemon: err = %v, want ErrUnavailable", err)
	}
	upgraded := serve("1.54")
	defer upgraded.Close()
	if info, err := c.Info(t.Context()); err != nil || info.ServerVersion != "1.49" {
		t.Fatalf("replacement daemon: %+v, %v; want a fresh negotiation to 1.49", info, err)
	}
}

func TestStreamBoundsOneValue(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1.49/events", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"s":"ok"}`+"\n")
		_, _ = io.WriteString(w, `{"s":"`)
		_, _ = io.WriteString(w, strings.Repeat("x", 2*maxStreamValue))
		_, _ = io.WriteString(w, `"}`+"\n")
	})
	c := newFakeDaemon(t, "1.49", mux)
	res, err := c.do(t.Context(), request{method: http.MethodGet, path: "/events"})
	if err != nil {
		t.Fatal(err)
	}
	s := newStream[struct{ S string }](res)
	defer func() { _ = s.Close() }()
	if v, err := s.Next(); err != nil || v.S != "ok" {
		t.Fatalf("first value = %+v, %v", v, err)
	}
	if _, err := s.Next(); err == nil || errors.Is(err, io.EOF) || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized value err = %v, want a size error", err)
	}
}
