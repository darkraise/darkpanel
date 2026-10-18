package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// setUID makes the service's own uid uid for the test.
func setUID(t *testing.T, uid int) {
	t.Helper()
	old := getuid
	getuid = func() int { return uid }
	t.Cleanup(func() { getuid = old })
}

// socketRequest serves r on the socket handler as if from a peer with uid.
func (h *harness) socketRequest(r *http.Request, uid uint32) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.srv.SocketHandler().ServeHTTP(rec, r.WithContext(withPeerUID(r.Context(), uid)))
	return rec
}

func TestSocketPeers(t *testing.T) {
	h := newHarness(t)
	setUID(t, 1000)
	for _, tc := range []struct {
		uid   uint32
		dev   bool
		actor string // "" means refused
	}{
		{0, false, "local:root@" + testHostID},
		{0, true, "local:root@" + testHostID},
		{1000, false, ""},
		{1000, true, "local:uid-1000@" + testHostID},
		{1001, true, ""},
		{1001, false, ""},
	} {
		h.srv.cfg.Dev = tc.dev
		if got := h.srv.socketAllowed(tc.uid); got != (tc.actor != "") {
			t.Errorf("uid %d dev %v: allowed %v", tc.uid, tc.dev, got)
		}
		var last call
		h.stubDispatcher(&last)
		rec := h.socketRequest(request("POST", "/api/v1/actions/demo.secret.show/v1/read", `{"input":{}}`), tc.uid)
		if tc.actor == "" {
			decodeProblem(t, rec, 403, errcode.Forbidden)
			continue
		}
		want := core.Principal{Actor: tc.actor, Scope: core.ScopeAdmin, Source: SocketSource}
		if rec.Code != 200 || last.principal != want {
			t.Errorf("uid %d dev %v: %d %s, principal %+v, want %+v", tc.uid, tc.dev, rec.Code, rec.Body, last.principal, want)
		}
	}

	rec := httptest.NewRecorder()
	h.srv.SocketHandler().ServeHTTP(rec, request("GET", "/healthz", ""))
	decodeProblem(t, rec, 403, errcode.Forbidden)
}

func TestSocketRoutes(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)

	rec := h.socketRequest(request("GET", "/healthz", ""), 0)
	want := fmt.Sprintf(`{"status":"ok","version":%q,"commit":%q,"schemaVersion":3}`, buildinfo.Version, buildinfo.Commit)
	if rec.Code != 200 || rec.Body.String() != want {
		t.Errorf("healthz: %d %s", rec.Code, rec.Body)
	}

	rec = h.socketRequest(request("GET", "/api/v1/controller/web-ca", ""), 0)
	var ca webCAResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ca); err != nil || rec.Code != 200 ||
		ca.PEM != string((fakeWebCA{}).RootPEM()) || ca.Fingerprint != (fakeWebCA{}).Fingerprint() {
		t.Errorf("web-ca: %d %s", rec.Code, rec.Body)
	}

	for path, method := range map[string]string{
		"/api/v1/actions/demo.secret.reset/v1/plan":  "Plan",
		"/api/v1/actions/demo.secret.reset/v1/apply": "Apply",
		"/api/v1/actions/demo.secret.show/v1/read":   "Read",
	} {
		last = call{}
		body := `{"input":{"name":"x"}}`
		if method == "Apply" {
			body = `{"planId":"plan1","confirmation":"x"}`
		}
		rec := h.socketRequest(request("POST", path, body), 0)
		if rec.Code != 200 || last.method != method || last.hostID != "" || last.principal.Actor != "local:root@"+testHostID {
			t.Errorf("%s: %d %s, dispatched %+v", path, rec.Code, rec.Body, last)
		}
	}

	for _, tc := range []struct {
		method, path, body string
		status             int
		code               errcode.Code
	}{
		{"POST", "/api/v1/actions/demo.item.remove/v1/plan", `{"input":{}}`, 403, errcode.Forbidden},
		{"POST", "/api/v1/actions/demo.item.list/v1/read", `{"input":{}}`, 403, errcode.Forbidden},
		{"POST", "/api/v1/actions/demo.secret.nope/v1/plan", `{"input":{}}`, 404, errcode.ActionUnknown},
		{"POST", "/api/v1/hosts/h1/actions/demo.item.add/v1/plan", `{"input":{}}`, 404, errcode.NotFound},
		{"GET", "/api/v1/changes", "", 404, errcode.NotFound},
		{"POST", "/api/v1/auth/login", `{}`, 404, errcode.NotFound},
		{"GET", "/", "", 404, errcode.NotFound},
		{"GET", "/api/v1/actions/demo.secret.reset/v1/plan", "", 405, errcode.MethodNotAllowed},
		{"POST", "/api/v1/actions/demo.secret.reset/v1/plan", `{"input":{}`, 400, errcode.InvalidJSON},
	} {
		last = call{}
		decodeProblem(t, h.socketRequest(request(tc.method, tc.path, tc.body), 0), tc.status, tc.code)
		if last.method != "" {
			t.Errorf("%s %s: dispatched %s", tc.method, tc.path, last.method)
		}
	}
}

// pipeListener hands out the server ends of net.Pipe connections.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return &net.UnixAddr{Name: "pipe", Net: "unix"} }

func TestSocketListenerRefusesPeers(t *testing.T) {
	h := newHarness(t)
	setUID(t, 1000)
	h.srv.cfg.Dev = true
	pl := newPipeListener()
	var mu sync.Mutex
	uids := map[net.Conn]uint32{}
	peerUID := func(c net.Conn) (uint32, error) {
		mu.Lock()
		defer mu.Unlock()
		uid, ok := uids[c]
		if !ok {
			return 0, errors.New("no credentials")
		}
		return uid, nil
	}
	ln := h.srv.SocketListener(pl, peerUID)
	accepted := make(chan net.Conn)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				close(accepted)
				return
			}
			accepted <- c
		}
	}()
	defer func() {
		_ = ln.Close()
		for range accepted {
		}
	}()

	for _, tc := range []struct {
		name     string
		uid      uint32
		known    bool
		accepted bool
	}{
		{"another user", 1001, true, false},
		{"unreadable credentials", 0, false, false},
		{"root", 0, true, true},
		{"the service's own uid in dev mode", 1000, true, true},
	} {
		server, client := net.Pipe()
		mu.Lock()
		if tc.known {
			uids[server] = tc.uid
		}
		mu.Unlock()
		pl.conns <- server
		if !tc.accepted {
			_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := client.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
				t.Errorf("%s: read %v, want the connection closed", tc.name, err)
			}
			continue
		}
		select {
		case c := <-accepted:
			if pc, ok := c.(*peerConn); !ok || pc.uid != tc.uid {
				t.Errorf("%s: accepted %#v", tc.name, c)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: not accepted", tc.name)
		}
		_ = client.Close()
	}
}

func TestSocketOverUnixSocket(t *testing.T) {
	h := newHarness(t)
	h.srv.cfg.Dev = true
	dir, err := os.MkdirTemp("", "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "controller.sock")
	if err := os.WriteFile(path, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenSocket(path); err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Fatalf("ListenSocket over a regular file: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	ln, err := ListenSocket(path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode %v, %v; want 0600", st.Mode(), err)
	}
	if _, err := ListenSocket(path); err == nil || !strings.Contains(err.Error(), "another process") {
		t.Errorf("ListenSocket over a live socket: %v", err)
	}

	var seen []uint32
	var mu sync.Mutex
	recordUID := func(c net.Conn) (uint32, error) {
		uid, err := SocketPeerUID(c)
		mu.Lock()
		seen = append(seen, uid)
		mu.Unlock()
		return uid, err
	}
	srv := h.srv.SocketServer()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(h.srv.SocketListener(ln, recordUID)) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			t.Error(err)
		}
	}()

	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		},
	}}
	defer client.CloseIdleConnections()
	resp, err := client.Get("http://controller/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"schemaVersion":3`) {
		t.Errorf("healthz over the socket: %d %s", resp.StatusCode, body)
	}
	mu.Lock()
	defer mu.Unlock()
	// ListenSocket's probe of the live socket is accepted too, so every
	// connection so far must show this process's uid.
	if len(seen) == 0 {
		t.Fatal("no peer uid was read")
	}
	for _, uid := range seen {
		if int64(uid) != int64(os.Getuid()) {
			t.Errorf("peer uids %v, want only %d", seen, os.Getuid())
		}
	}
}

func socketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestListenSocketReplacesStaleSocket(t *testing.T) {
	path := filepath.Join(socketDir(t), "controller.sock")
	old, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	old.SetUnlinkOnClose(false)
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	ln, err := ListenSocket(path)
	if err != nil {
		t.Fatalf("ListenSocket over a stale socket: %v", err)
	}
	defer func() { _ = ln.Close() }()
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode %v, %v; want 0600", st.Mode(), err)
	}
}

// A live socket whose backlog is full refuses a probe with EAGAIN, not
// ECONNREFUSED; ListenSocket must keep it.
func TestListenSocketKeepsBusySocket(t *testing.T) {
	path := filepath.Join(socketDir(t), "controller.sock")
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(fd) })
	if err := unix.Bind(fd, &unix.SockaddrUnix{Name: path}); err != nil {
		t.Fatal(err)
	}
	if err := unix.Listen(fd, 0); err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	if ln, err := ListenSocket(path); err == nil || !errors.Is(err, unix.EAGAIN) {
		if ln != nil {
			_ = ln.Close()
		}
		t.Errorf("ListenSocket over a busy socket: %v, want EAGAIN", err)
	}
	if st, err := os.Lstat(path); err != nil || st.Mode().Type() != os.ModeSocket {
		t.Errorf("busy socket after ListenSocket: %v, %v", st, err)
	}
}

func TestSocketPeerUIDRefusesOtherConns(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = a.Close(); _ = b.Close() }()
	if _, err := SocketPeerUID(a); err == nil {
		t.Error("SocketPeerUID read credentials of a pipe")
	}
}
