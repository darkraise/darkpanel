package api

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/events"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// sseAuth is an Authenticator whose answer the test sets.
type sseAuth struct {
	mu  sync.Mutex
	id  auth.Identity
	err error
}

func (a *sseAuth) set(id auth.Identity, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.id, a.err = id, err
}

func (a *sseAuth) Authenticate(context.Context, auth.Credentials) (auth.Identity, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.id, a.err
}

func (a *sseAuth) Login(context.Context, auth.LoginRequest) (auth.LoginResult, error) {
	return auth.LoginResult{}, errors.New("not used")
}
func (a *sseAuth) Logout(context.Context, string, string) error { return errors.New("not used") }
func (a *sseAuth) CSRFToken(string) string                      { return "" }
func (a *sseAuth) VerifyCSRF(string, string) bool               { return false }

// sseIdentity is a token identity of the read scope that ends at expires.
func sseIdentity(expires time.Time) auth.Identity {
	return auth.Identity{Principal: core.Principal{Actor: "token:T1", Scope: core.ScopeRead, Source: "192.0.2.10"}, ExpiresAt: expires}
}

// sseRecorder is a ResponseWriter that records each header, deadline,
// write and flush in order. failWrites makes every write fail.
type sseRecorder struct {
	mu         sync.Mutex
	header     http.Header
	status     int
	ops        []string
	body       strings.Builder
	deadlines  []time.Duration // each write deadline, less the time it was set
	failWrites bool
}

func newSSERecorder() *sseRecorder { return &sseRecorder{header: http.Header{}} }

func (w *sseRecorder) Header() http.Header { return w.header }

func (w *sseRecorder) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status = status
	w.ops = append(w.ops, "header")
}

func (w *sseRecorder) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failWrites {
		return 0, errors.New("broken pipe")
	}
	w.ops = append(w.ops, "write "+string(b))
	w.body.Write(b)
	return len(b), nil
}

func (w *sseRecorder) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ops = append(w.ops, "flush")
}

func (w *sseRecorder) SetWriteDeadline(d time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ops = append(w.ops, "deadline")
	w.deadlines = append(w.deadlines, time.Until(d))
	return nil
}

func (w *sseRecorder) text() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.String()
}

func (w *sseRecorder) record() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.ops...)
}

// sseEnv is one sseStream call over a fake clock and a fake authenticator.
type sseEnv struct {
	t     *testing.T
	srv   *Server
	auth  *sseAuth
	clock *coretest.FakeClock
	rec   *sseRecorder
	src   chan sseFrame
	done  chan string
}

// sseServer is a server over a, with the bus and the clock.
func sseServer(t *testing.T, a Authenticator, bus *events.Bus, clock core.Clock) *Server {
	t.Helper()
	reg, err := core.NewRegistry(demoModule{})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{
		Dispatcher: &coretest.FakeDispatcher{}, Auth: a, Registry: reg, WebCA: fakeWebCA{},
		SchemaVersion: func(context.Context) (int, error) { return 3, nil }, HostID: testHostID,
		Events: bus, Clock: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// sseStart runs sseStream for a caller whose credentials end in an hour,
// and waits until its four timers are armed.
func sseStart(t *testing.T) *sseEnv {
	t.Helper()
	e := &sseEnv{t: t, auth: &sseAuth{}, clock: coretest.NewFakeClock(testStart), rec: newSSERecorder(),
		src: make(chan sseFrame), done: make(chan string, 1)}
	id := sseIdentity(testStart.Add(time.Hour))
	e.auth.set(id, nil)
	e.srv = sseServer(t, e.auth, nil, e.clock)
	r := request("GET", "/api/v1/events", "")
	r = r.WithContext(withCaller(r.Context(), caller{principal: id.Principal, identity: id}))
	go func() { e.done <- e.srv.sseStream(e.rec, r, e.src) }()
	sseWait(t, "the stream's timers", func() bool { return e.clock.Pending() == 4 })
	return e
}

// sseWait polls cond for up to five seconds.
func sseWait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// send hands f to the stream and waits until it is written.
func (e *sseEnv) send(f sseFrame) {
	e.t.Helper()
	before := len(e.rec.text())
	e.src <- f
	sseWait(e.t, "the frame to be written", func() bool { return len(e.rec.text()) > before })
}

// end waits for the stream's end reason.
func (e *sseEnv) end() string {
	e.t.Helper()
	select {
	case reason := <-e.done:
		return reason
	case <-time.After(5 * time.Second):
		e.t.Fatal("the stream did not end")
	}
	return ""
}

// running fails if the stream has already ended.
func (e *sseEnv) running() {
	e.t.Helper()
	select {
	case reason := <-e.done:
		e.t.Fatalf("the stream ended early with %s", reason)
	default:
	}
}

func TestSSEFrames(t *testing.T) {
	e := sseStart(t)
	e.send(sseFrame{ID: "0123456789abcdef-1", Event: "change", Data: []byte(`{"changeId": "c1",
		"state": "completed"}`)})
	e.send(sseFrame{Event: "end", Data: []byte(`{"reason":"done"}`)})
	close(e.src)
	if reason := e.end(); reason != "done" {
		t.Fatalf("end reason = %s, want done", reason)
	}
	want := "id: 0123456789abcdef-1\nevent: change\ndata: {\"changeId\":\"c1\",\"state\":\"completed\"}\n\n" +
		"event: end\ndata: {\"reason\":\"done\"}\n\n"
	if got := e.rec.text(); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if e.rec.status != 200 || e.rec.header.Get("Content-Type") != "text/event-stream" || e.rec.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d, headers %v; want 200 text/event-stream, no-store", e.rec.status, e.rec.header)
	}
	ops := e.rec.record()
	wantOps := []string{"header", "deadline", "flush",
		"deadline", "write " + strings.SplitAfter(want, "\n\n")[0], "flush",
		"deadline", "write " + strings.SplitAfter(want, "\n\n")[1], "flush"}
	if strings.Join(ops, "|") != strings.Join(wantOps, "|") {
		t.Fatalf("ops = %q, want %q", ops, wantOps)
	}
}

func TestSSEHeartbeat(t *testing.T) {
	e := sseStart(t)
	e.clock.Advance(SSEHeartbeat - time.Second)
	time.Sleep(10 * time.Millisecond)
	if strings.Contains(e.rec.text(), ": ping") {
		t.Fatal("a ping before the heartbeat interval")
	}
	e.clock.Advance(time.Second)
	sseWait(t, "the first ping", func() bool { return e.rec.text() == ": ping\n\n" })
	sseWait(t, "the heartbeat re-armed", func() bool { return e.clock.Pending() == 4 })
	e.clock.Advance(SSEHeartbeat)
	sseWait(t, "the second ping", func() bool { return e.rec.text() == ": ping\n\n: ping\n\n" })
	e.running()
}

func TestSSEEnds(t *testing.T) {
	cases := []struct {
		name string
		run  func(e *sseEnv)
		want string
	}{
		{"idle for ten minutes", func(e *sseEnv) {
			e.clock.Advance(SSEIdleTimeout - time.Second)
			e.running()
			e.clock.Advance(time.Second)
		}, "expired"},
		{"two hours while frames flow", func(e *sseEnv) {
			for range int(SSEHardLimit/(5*time.Minute)) - 1 {
				e.send(sseFrame{Event: "data", Data: []byte(`{}`)})
				e.clock.Advance(5 * time.Minute)
				e.running()
			}
			e.send(sseFrame{Event: "data", Data: []byte(`{}`)})
			e.clock.Advance(5 * time.Minute)
		}, "expired"},
		{"a revoked session", func(e *sseEnv) {
			e.auth.set(auth.Identity{}, errcode.New(errcode.AuthRequired, "authentication required"))
			e.clock.Advance(SSERecheck)
		}, "revoked"},
		{"an expired session", func(e *sseEnv) {
			e.auth.set(sseIdentity(testStart.Add(SSERecheck+time.Second)), nil)
			e.clock.Advance(SSERecheck)
			sseWait(e.t, "the recheck re-armed", func() bool { return e.clock.Pending() == 4 })
			e.running()
			e.auth.set(auth.Identity{}, errcode.New(errcode.AuthRequired, "authentication required"))
			e.clock.Advance(SSERecheck)
		}, "expired"},
		{"a failed check", func(e *sseEnv) {
			e.auth.set(auth.Identity{}, errors.New("auth: load session: disk I/O error"))
			e.clock.Advance(SSERecheck)
		}, "error"},
		{"a closed source", func(e *sseEnv) { close(e.src) }, "done"},
		{"the server closes its streams", func(e *sseEnv) { e.srv.CloseStreams() }, "error"},
		{"a failed write", func(e *sseEnv) {
			e.rec.mu.Lock()
			e.rec.failWrites = true
			e.rec.mu.Unlock()
			e.src <- sseFrame{Event: "data", Data: []byte(`{}`)}
		}, "error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := sseStart(t)
			tc.run(e)
			if got := e.end(); got != tc.want {
				t.Fatalf("end reason = %s, want %s", got, tc.want)
			}
			if n := e.clock.Pending(); n != 0 {
				t.Fatalf("%d timers armed after the end, want 0", n)
			}
		})
	}
}

func TestSSEWriteDeadline(t *testing.T) {
	e := sseStart(t)
	e.send(sseFrame{Event: "data", Data: []byte(`{}`)})
	e.clock.Advance(SSEHeartbeat)
	sseWait(t, "the ping", func() bool { return strings.HasSuffix(e.rec.text(), ": ping\n\n") })
	e.rec.mu.Lock()
	deadlines := e.rec.deadlines
	e.rec.mu.Unlock()
	if len(deadlines) != 3 {
		t.Fatalf("%d write deadlines, want 3", len(deadlines))
	}
	for i, d := range deadlines {
		if d <= SSEWriteTimeout-5*time.Second || d > SSEWriteTimeout {
			t.Errorf("write deadline %d is %v ahead, want %v", i, d, SSEWriteTimeout)
		}
	}

	// Through the real middleware chain and net/http, a stream outlives the
	// listeners' write timeout, here shortened to 100 ms.
	a := &sseAuth{}
	a.set(sseIdentity(testStart.Add(time.Hour)), nil)
	srv := sseServer(t, a, nil, coretest.NewFakeClock(testStart))
	src := make(chan sseFrame)
	ts := httptest.NewUnstartedServer(securityHeaders(recoverPanics(srv.log, srv.authenticate(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { srv.sseStream(w, r, src) })))))
	ts.Config.WriteTimeout = 100 * time.Millisecond
	ts.Start()
	t.Cleanup(ts.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer dpt_any")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	rd := bufio.NewReader(resp.Body)
	for i, pause := range []time.Duration{0, 300 * time.Millisecond} {
		time.Sleep(pause)
		select {
		case src <- sseFrame{Event: "data", Data: []byte(`{"n":` + strconv.Itoa(i) + `}`)}:
		case <-time.After(5 * time.Second):
			t.Fatalf("frame %d: the stream stopped reading", i)
		}
		if got, want := sseNext(t, rd), "event: data\ndata: {\"n\":"+strconv.Itoa(i)+"}"; got != want {
			t.Fatalf("frame %d = %q, want %q", i, got, want)
		}
	}
}

func TestSSEEventSlots(t *testing.T) {
	srv := sseServer(t, &sseAuth{}, nil, coretest.NewFakeClock(testStart))
	var releases []func()
	for range MaxEventStreams {
		release, err := srv.eventSlot("token:T1")
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	_, err := srv.eventSlot("token:T1")
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.StreamLimit || coded.Detail != "at most 4 event streams per principal" {
		t.Fatalf("a fifth stream: %v, want %s", err, errcode.StreamLimit)
	}
	other, err := srv.eventSlot("token:T2")
	if err != nil {
		t.Fatalf("another principal's stream: %v", err)
	}
	other()
	releases[0]()
	again, err := srv.eventSlot("token:T1")
	if err != nil {
		t.Fatalf("a stream after one was released: %v", err)
	}
	again()
	for _, release := range releases[1:] {
		release()
	}
	if len(srv.eventStreams) != 0 {
		t.Fatalf("open streams after every release: %v", srv.eventStreams)
	}
}

func TestSSEStreamRoute(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         bool
	}{
		{"GET", "/api/v1/events", true},
		{"HEAD", "/api/v1/events", true},
		{"POST", "/api/v1/events", false},
		{"GET", "/api/v1/events/", false},
		{"GET", "/api/v1/hosts/h1/streams/docker/logs", true},
		{"GET", "/api/v1/hosts/h1/streams/docker", false},
		{"GET", "/api/v1/hosts//streams/docker/logs", false},
		{"GET", "/api/v1/hosts/h1/streams/docker/logs/x", false},
		{"GET", "/api/v1/hosts/h1/resources/demo/status", false},
		{"GET", "/api/v1/changes", false},
	} {
		if got := streamRoute(httptest.NewRequest(tc.method, tc.path, nil)); got != tc.want {
			t.Errorf("streamRoute(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}

// TestSSEStreamsLeaveTheIdleClock opens a stream with a session cookie
// through the authentication middleware: neither the request nor its
// re-checks move the session's last_seen_at, so the session ends one idle
// limit after the login although frames keep flowing, and its stream ends
// with expired at the next re-check.
func TestSSEStreamsLeaveTheIdleClock(t *testing.T) {
	h := newHarness(t)
	session, _ := h.login()
	loginAt := h.clock.Now()
	idleEnd := func() time.Time {
		t.Helper()
		id, err := h.auth.Authenticate(context.Background(), auth.Credentials{Session: session, Passive: true})
		if err != nil {
			t.Fatal(err)
		}
		return id.ExpiresAt
	}
	srv := sseServer(t, h.auth, nil, h.clock)
	src := make(chan sseFrame)
	ended := make(chan string, 1)
	ts := httptest.NewServer(securityHeaders(recoverPanics(srv.log, srv.authenticate(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { ended <- srv.sseStream(w, r, src) })))))
	t.Cleanup(ts.Close)
	base := h.clock.Pending()
	armed := func() {
		t.Helper()
		sseWait(t, "the stream's timers", func() bool { return h.clock.Pending() == base+4 })
	}

	h.clock.Advance(2 * auth.LastSeenInterval) // an active request would now move last_seen_at
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Add("Cookie", SessionCookiePlain+"="+session)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status %d, want 200", resp.StatusCode)
	}
	rd := bufio.NewReader(resp.Body)
	armed()
	h.clock.Advance(SSERecheck)
	armed()
	if got, want := idleEnd(), loginAt.Add(auth.SessionIdle); !got.Equal(want) {
		t.Fatalf("after the stream request and a re-check the session ends at %s, want %s (last_seen_at moved)", got, want)
	}

	send := func() {
		t.Helper()
		select {
		case src <- sseFrame{Event: "data", Data: []byte(`{}`)}:
		case reason := <-ended:
			t.Fatalf("the stream ended with %s before the session's idle limit", reason)
		case <-time.After(5 * time.Second):
			t.Fatal("the stream stopped reading")
		}
		sseNext(t, rd)
	}
	lastOK := loginAt.Add(auth.SessionIdle - time.Second)
	for h.clock.Now().Add(5 * time.Minute).Before(lastOK) {
		send()
		h.clock.Advance(5 * time.Minute)
		armed()
	}
	send()
	h.clock.Advance(lastOK.Sub(h.clock.Now()))
	armed()
	h.clock.Advance(SSERecheck)
	select {
	case reason := <-ended:
		if reason != "expired" {
			t.Fatalf("end reason = %s, want expired", reason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stream outlived its session's idle limit")
	}
}

// sseNext reads the next frame other than a ping, without its blank line,
// within five seconds.
func sseNext(t *testing.T, rd *bufio.Reader) string {
	t.Helper()
	type result struct {
		frame string
		err   error
	}
	got := make(chan result, 1)
	go func() {
		var lines []string
		for {
			line, err := rd.ReadString('\n')
			if err != nil {
				got <- result{err: err}
				return
			}
			line = strings.TrimSuffix(line, "\n")
			switch {
			case line == "" && len(lines) > 0:
				got <- result{frame: strings.Join(lines, "\n")}
				return
			case line == "", strings.HasPrefix(line, ":"):
			default:
				lines = append(lines, line)
			}
		}
	}()
	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("read a frame: %v", r.err)
		}
		return r.frame
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a frame")
	}
	return ""
}
