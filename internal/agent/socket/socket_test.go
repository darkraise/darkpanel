package socket

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// socketPath returns a socket path in a new directory under os.TempDir,
// short enough for a Unix socket.
func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "run", "agent.sock")
}

// serve listens on path with allow and serves a handler that counts its
// calls until the test ends.
func serve(t *testing.T, path string, allow func(uint32) bool) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, "hello")
	})
	s, err := Listen(Config{Path: path, Handler: h, Log: slog.New(slog.DiscardHandler), Allow: allow})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := s.Serve(); err != nil {
			t.Errorf("Serve = %v", err)
		}
	}()
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close = %v", err)
		}
		wg.Wait()
	})
	return &calls
}

// get sends one request over the socket at path.
func get(path string) (string, error) {
	c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
	}}
	resp, err := c.Get("http://agent/agent/v1/hello")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	return string(body), err
}

func TestSocketAdmitsAllowedPeers(t *testing.T) {
	path := socketPath(t)
	var seen atomic.Int64
	seen.Store(-1)
	calls := serve(t, path, func(uid uint32) bool {
		seen.Store(int64(uid))
		return true
	})
	if body, err := get(path); err != nil || body != "hello" {
		t.Fatalf("request = %q, %v; want served", body, err)
	}
	if got := seen.Load(); got != int64(os.Getuid()) {
		t.Errorf("Allow saw uid %d, want this process's %d", got, os.Getuid())
	}
	if calls.Load() != 1 {
		t.Errorf("handler ran %d times, want 1", calls.Load())
	}
}

func TestSocketRefusesOtherPeers(t *testing.T) {
	path := socketPath(t)
	calls := serve(t, path, func(uint32) bool { return false })
	for range 3 {
		if body, err := get(path); err == nil {
			t.Fatalf("a refused peer was served %q", body)
		}
	}
	if calls.Load() != 0 {
		t.Errorf("handler ran %d times for refused peers", calls.Load())
	}
}

func TestSocketAdmitsRootByDefault(t *testing.T) {
	if !rootOnly(0) || rootOnly(1) || rootOnly(1000) {
		t.Error("rootOnly must admit uid 0 alone")
	}
	path := socketPath(t)
	serve(t, path, nil)
	_, err := get(path)
	if root := os.Getuid() == 0; (err == nil) != root {
		t.Errorf("as uid %d the request answered %v", os.Getuid(), err)
	}
}

func TestListenModes(t *testing.T) {
	path := socketPath(t)
	// A socket left by an agent that crashed is replaced, and a directory
	// created with a wider mode is narrowed.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // G301: the wider mode Listen must narrow
		t.Fatal(err)
	}
	old, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	old.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = old.Close()
	serve(t, path, func(uint32) bool { return true })
	for p, want := range map[string]os.FileMode{filepath.Dir(path): os.ModeDir | 0o700, path: os.ModeSocket | 0o600} {
		st, err := os.Lstat(p)
		if err != nil || st.Mode() != want {
			t.Errorf("%s: mode %v, %v; want %v", p, st.Mode(), err, want)
		}
	}
	if _, err := get(path); err != nil {
		t.Errorf("request over the replaced socket = %v", err)
	}
}

func TestListenRefusesAnotherFile(t *testing.T) {
	path := socketPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(Config{Path: path, Handler: http.NotFoundHandler(), Log: slog.New(slog.DiscardHandler)}); err == nil {
		t.Fatal("Listen replaced a regular file")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "not a socket" { //nolint:gosec // G304: a test's temporary path
		t.Errorf("the file is now %q, %v", data, err)
	}
}

func TestCloseRemovesTheSocket(t *testing.T) {
	path := socketPath(t)
	s, err := Listen(Config{Path: path, Handler: http.NotFoundHandler(), Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve() }()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Errorf("Serve after Close = %v, want nil", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("the socket is still there: %v", err)
	}
}

func TestListenWithoutALog(t *testing.T) {
	path := socketPath(t)
	s, err := Listen(Config{Path: path, Handler: http.NotFoundHandler(), Allow: func(uint32) bool { return false }})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve() }()
	// A refused peer is logged; without a logger the refusal is discarded.
	if _, err := get(path); err == nil {
		t.Error("a refused peer was served")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Errorf("Serve after Close = %v, want nil", err)
	}
}
