package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentapi "github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/transport"
)

func filePath(op, query string) string {
	p := "/api/v1/hosts/" + testHostID + "/files/" + op
	if query != "" {
		p += "?" + query
	}
	return p
}

// fileCase is one file endpoint and a request the fake agent accepts.
type fileCase struct {
	op, method, query, body, contentType string
	stepUp                               bool
}

var fileCases = []fileCase{
	{op: "list", method: "GET", query: "path=/etc"},
	{op: "stat", method: "GET", query: "path=/etc/hosts"},
	{op: "read", method: "GET", query: "path=/etc/hosts", stepUp: true},
	{op: "download", method: "GET", query: "path=/etc/hosts", stepUp: true},
	{op: "write", method: "POST", query: "path=/etc/hosts&sha256=ab&extra=1", body: "127.0.0.1 localhost\n", contentType: "application/octet-stream", stepUp: true},
	{op: "upload", method: "POST", query: "path=/tmp/x", body: "bytes", contentType: "application/octet-stream", stepUp: true},
	{op: "mkdir", method: "POST", body: `{"path":"/tmp/d"}`, contentType: "application/json", stepUp: true},
	{op: "rename", method: "POST", body: `{"path":"/tmp/d","to":"/tmp/e"}`, contentType: "application/json", stepUp: true},
	{op: "delete", method: "POST", body: `{"path":"/tmp/e","confirm":"/tmp/e"}`, contentType: "application/json", stepUp: true},
	{op: "chmod", method: "POST", body: `{"path":"/tmp/x","mode":"0600"}`, contentType: "application/json", stepUp: true},
	{op: "chown", method: "POST", body: `{"path":"/tmp/x","uid":0}`, contentType: "application/json", stepUp: true},
}

func (fc fileCase) request() *http.Request {
	r := httptest.NewRequest(fc.method, filePath(fc.op, fc.query), strings.NewReader(fc.body))
	r.RemoteAddr = testRemote
	if fc.contentType != "" {
		r.Header.Set("Content-Type", fc.contentType)
	}
	return r
}

// fakeFiles answers every file request the way the agent would, and
// records what reached it.
func fakeFiles(h *harness) *[]core.FileRequest {
	var mu sync.Mutex
	var got []core.FileRequest
	h.disp.FileFunc = func(_ context.Context, _ core.Principal, host string, req core.FileRequest) (core.FileAnswer, error) {
		if host != testHostID {
			return core.FileAnswer{}, errcode.New(errcode.NotFound, "no host "+host)
		}
		if req.Body != nil {
			if _, err := io.Copy(io.Discard, req.Body); err != nil {
				return core.FileAnswer{}, err
			}
		}
		mu.Lock()
		got = append(got, req)
		mu.Unlock()
		switch req.Op {
		case "list", "stat", "write", "upload":
			return core.FileAnswer{Status: 200, ContentType: "application/json", Size: 2, Body: io.NopCloser(strings.NewReader("{}"))}, nil
		case "read", "download":
			return core.FileAnswer{Status: 200, ContentType: "text/plain", Size: 3, SHA256: "ab", Body: io.NopCloser(strings.NewReader("abc"))}, nil
		}
		return core.FileAnswer{Status: http.StatusNoContent}, nil
	}
	return &got
}

func stepUp(t *testing.T, h *harness, code, session, csrf string) {
	t.Helper()
	rec := h.serve(withSession(request("POST", "/api/v1/auth/step-up", `{"code":"`+code+`"}`), session, csrf))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("step-up: %d %s", rec.Code, rec.Body)
	}
}

// TestStepUpScope: listing and stat need admin on the host; reading text,
// downloading and every change also need the step-up window; a member and
// an API token without a window never get through.
func TestStepUpScope(t *testing.T) {
	h := newHarness(t)
	got := fakeFiles(h)
	session, csrf := h.login()
	serve := func(fc fileCase) *httptest.ResponseRecorder {
		return h.serve(withSession(fc.request(), session, csrf))
	}
	ok := func(what string, fc fileCase, rec *httptest.ResponseRecorder) {
		t.Helper()
		if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
			t.Errorf("%s %s: %d %s", what, fc.op, rec.Code, rec.Body)
		}
	}

	for _, fc := range fileCases {
		before := len(*got)
		rec := serve(fc)
		if !fc.stepUp {
			ok("admin without step-up", fc, rec)
			continue
		}
		wantProblem(t, "admin without step-up: "+fc.op, rec, http.StatusForbidden, errcode.StepUpRequired)
		if len(*got) != before {
			t.Errorf("%s without step-up reached the agent", fc.op)
		}
	}

	stepUp(t, h, h.code(), session, csrf)
	for _, fc := range fileCases {
		ok("inside the window", fc, serve(fc))
	}
	if len(*got) != 2+len(fileCases) {
		t.Fatalf("%d requests reached the agent, want %d", len(*got), 2+len(fileCases))
	}
	last := (*got)[len(*got)-len(fileCases):]
	for i, fc := range fileCases {
		if last[i].Op != fc.op {
			t.Errorf("request %d reached the agent as %q, want %q", i, last[i].Op, fc.op)
		}
	}
	if q := last[4].Query; len(q) != 2 || q.Get("path") != "/etc/hosts" || q.Get("sha256") != "ab" || last[4].Size != int64(len(fileCases[4].body)) || last[4].JSON {
		t.Errorf("write reached the agent as %+v", last[4])
	}
	if !last[7].JSON {
		t.Errorf("rename reached the agent without its JSON: %+v", last[7])
	}

	h.clock.Advance(16 * time.Minute)
	for _, fc := range fileCases {
		if fc.stepUp {
			wantProblem(t, "after the window: "+fc.op, serve(fc), http.StatusForbidden, errcode.StepUpRequired)
		}
	}

	// An operator grant on every host shows the host but is below admin.
	member, _ := qaGrantedUser(t, h, "member1", "*=operator")
	for _, fc := range fileCases {
		wantProblem(t, "member: "+fc.op, h.serve(member(fc.request())), http.StatusForbidden, errcode.Forbidden)
	}
	tok := h.token(core.ScopeAdmin)
	for _, fc := range fileCases {
		rec := h.serve(withToken(fc.request(), tok))
		if !fc.stepUp {
			ok("token", fc, rec)
			continue
		}
		wantProblem(t, "token: "+fc.op, rec, http.StatusForbidden, errcode.StepUpUnavailable)
	}
}

// TestFileRequestChecks: the controller refuses a bad body or method before
// any byte reaches the agent.
func TestFileRequestChecks(t *testing.T) {
	h := newHarness(t)
	got := fakeFiles(h)
	session, csrf := h.login()
	stepUp(t, h, h.code(), session, csrf)
	raw := func(method, op, contentType string, body io.Reader, size int64) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, filePath(op, "path=/tmp/x"), body)
		r.RemoteAddr = testRemote
		r.ContentLength = size
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		return h.serve(withSession(r, session, csrf))
	}
	big := bytes.Repeat([]byte("a"), MaxFileWrite+1)
	wantProblem(t, "write over 1 MiB", raw("POST", "write", "application/octet-stream", bytes.NewReader(big), int64(len(big))),
		http.StatusRequestEntityTooLarge, errcode.TooLarge)
	wantProblem(t, "upload over 512 MiB", raw("POST", "upload", "application/octet-stream", strings.NewReader(""), MaxFileUpload+1),
		http.StatusRequestEntityTooLarge, errcode.TooLarge)
	wantProblem(t, "upload without Content-Length", raw("POST", "upload", "application/octet-stream", strings.NewReader("x"), -1),
		http.StatusUnprocessableEntity, errcode.InvalidInput)
	wantProblem(t, "write as JSON", raw("POST", "write", "application/json", strings.NewReader(`"x"`), 3),
		http.StatusUnsupportedMediaType, errcode.UnsupportedMediaType)
	wantProblem(t, "mkdir with an unknown member", raw("POST", "mkdir", "application/json", strings.NewReader(`{"path":"/a","bogus":1}`), -1),
		http.StatusBadRequest, errcode.InvalidJSON)
	wantProblem(t, "HEAD download", raw("HEAD", "download", "", nil, 0), http.StatusMethodNotAllowed, errcode.MethodNotAllowed)
	wantProblem(t, "GET write", raw("GET", "write", "", nil, 0), http.StatusNotFound, errcode.NotFound)
	wantProblem(t, "an unknown operation", raw("POST", "truncate", "application/json", strings.NewReader(`{}`), 2), http.StatusNotFound, errcode.NotFound)
	if len(*got) != 0 {
		t.Errorf("refused requests reached the agent: %+v", *got)
	}
}

// diskFiles is a console file manager over dir, for the agent end of the
// round trips.
type diskFiles struct{ dir string }

func (d diskFiles) at(p string) string { return filepath.Join(d.dir, filepath.Clean("/"+p)) }

func (d diskFiles) List(context.Context, string, string) (core.DirPage, error) {
	return core.DirPage{}, nil
}

func (d diskFiles) Stat(_ context.Context, p string) (core.FileInfo, error) {
	if p == "/missing" {
		return core.FileInfo{}, errcode.New(errcode.NotFound, "no file /missing")
	}
	return core.FileInfo{}, nil
}

func (d diskFiles) ReadText(_ context.Context, p string) ([]byte, string, error) {
	b, err := os.ReadFile(d.at(p))
	sum := sha256.Sum256(b)
	return b, hex.EncodeToString(sum[:]), err
}

func (d diskFiles) Open(_ context.Context, p string) (io.ReadCloser, int64, error) {
	f, err := os.Open(d.at(p))
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	return f, st.Size(), nil
}

func (d diskFiles) WriteText(ctx context.Context, p string, data []byte, _ string) (string, error) {
	return d.Upload(ctx, p, bytes.NewReader(data), int64(len(data)))
}

func (d diskFiles) Upload(_ context.Context, p string, r io.Reader, size int64) (string, error) {
	f, err := os.Create(d.at(p))
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	sum := sha256.New()
	n, err := io.CopyBuffer(io.MultiWriter(f, sum), r, make([]byte, 32<<10))
	if err != nil {
		return "", err
	}
	if n != size {
		return "", errcode.New(errcode.InvalidInput, "short body")
	}
	return hex.EncodeToString(sum.Sum(nil)), f.Close()
}

func (d diskFiles) Mkdir(_ context.Context, p string) error { return os.Mkdir(d.at(p), 0o750) }

func (diskFiles) Rename(context.Context, string, string) error     { return nil }
func (diskFiles) Delete(context.Context, string, string) error     { return nil }
func (diskFiles) Chmod(context.Context, string, fs.FileMode) error { return nil }
func (diskFiles) Chown(context.Context, string, int, int) error    { return nil }
func (diskFiles) RemoveTemp(context.Context, string) error         { return nil }

// fileAgent is the agent runtime of the round trips: a file manager over a
// directory and confirmations that answer at once.
type fileAgent struct {
	coretest.FakeAgent
	files diskFiles
}

var _ agentapi.FileOpener = (*fileAgent)(nil)

func (a *fileAgent) OpenFiles(ctx context.Context, _ core.Principal, _ bool) (context.Context, core.ConsoleFiles, func(), error) {
	return ctx, a.files, func() {}, nil
}

// fileLink serves the agent API of a fileAgent over a transport session
// whose connection wrap may slow, and sends h's file requests to it on
// module-stream slots, as production does.
type fileLink struct {
	agent   *fileAgent
	client  *agentapi.Client
	ctrl    *transport.Session
	actions atomic.Int32 // connections opened on action slots
	modules atomic.Int32 // connections opened on module-stream slots
}

func newFileLink(t *testing.T, h *harness, wrap func(net.Conn) net.Conn) *fileLink {
	t.Helper()
	a, b := net.Pipe()
	type made struct {
		s   *transport.Session
		err error
	}
	agentEnd := make(chan made, 1)
	go func() {
		s, err := transport.AgentSession(wrap(b))
		agentEnd <- made{s, err}
	}()
	ctrl, err := transport.ControllerSession(wrap(a))
	if err != nil {
		t.Fatal(err)
	}
	ae := <-agentEnd
	if ae.err != nil {
		t.Fatal(ae.err)
	}
	fa := &fileAgent{files: diskFiles{dir: t.TempDir()}}
	fa.ConfirmFunc = func(_ context.Context, _ core.Principal, id string) (core.ChangeView, error) {
		return core.ChangeView{ID: id, State: core.StateConfirmed}, nil
	}
	srv := &http.Server{Handler: agentapi.NewHandler(agentapi.Config{Agent: fa, Log: h.srv.log}), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ae.s) }()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = ctrl.Close()
		_ = ae.s.Close()
	})
	l := &fileLink{agent: fa, ctrl: ctrl}
	l.client = agentapi.NewClient(agentapi.ClientConfig{
		Dial: func(ctx context.Context) (net.Conn, error) {
			l.actions.Add(1)
			return ctrl.Open(ctx)
		},
		DialStream: func(ctx context.Context) (net.Conn, error) {
			l.modules.Add(1)
			return ctrl.OpenModule(ctx)
		},
		HostID: testHostID,
	})
	h.disp.FileFunc = func(ctx context.Context, p core.Principal, _ string, req core.FileRequest) (core.FileAnswer, error) {
		return l.client.File(ctx, p, req)
	}
	return l
}

// browser signs the admin in, steps up and serves h on a real listener.
type browser struct {
	ts            *httptest.Server
	client        *http.Client
	cookie        string
	session, csrf string
}

func newBrowser(t *testing.T, h *harness) *browser {
	t.Helper()
	return newBrowserTimeouts(t, h, 0, false)
}

// newBrowserTimeouts is newBrowser on a listener whose read and write
// timeouts are timeout, as production's are (0 sets none); with h2 it
// serves HTTP/2 over TLS, as the TLS listeners do.
func newBrowserTimeouts(t *testing.T, h *harness, timeout time.Duration, h2 bool) *browser {
	t.Helper()
	session, csrf := h.login()
	stepUp(t, h, h.code(), session, csrf)
	ts := httptest.NewUnstartedServer(h.srv.Handler())
	ts.Config.ReadTimeout, ts.Config.WriteTimeout = timeout, timeout
	b := &browser{ts: ts, session: session, csrf: csrf, cookie: SessionCookiePlain}
	if h2 {
		ts.EnableHTTP2 = true
		ts.StartTLS()
		b.cookie = SessionCookieSecure
	} else {
		ts.Start()
	}
	t.Cleanup(ts.Close)
	b.client = ts.Client()
	return b
}

func (b *browser) do(t *testing.T, method, op, query string, body io.Reader, size int64) *http.Response {
	t.Helper()
	r, err := http.NewRequest(method, b.ts.URL+filePath(op, query), body)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		r.ContentLength = size
		r.Header.Set("Content-Type", "application/octet-stream")
	}
	r.Header.Set("Cookie", b.cookie+"="+b.session)
	r.Header.Set(CSRFHeader, b.csrf)
	res, err := b.client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (b *browser) written(t *testing.T, res *http.Response) wire.FileWritten {
	t.Helper()
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%s: %d %s", res.Request.URL.Path, res.StatusCode, body)
	}
	var out wire.FileWritten
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// heapPeak samples HeapInuse every 100 ms until stop is called, which
// returns the largest sample.
func heapPeak() (stop func() uint64) {
	var peak atomic.Uint64
	done, finished := make(chan struct{}), make(chan struct{})
	sample := func() {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		if m.HeapInuse > peak.Load() {
			peak.Store(m.HeapInuse)
		}
	}
	go func() {
		defer close(finished)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				sample()
				return
			case <-tick.C:
				sample()
			}
		}
	}()
	return func() uint64 {
		close(done)
		<-finished
		return peak.Load()
	}
}

func heapInuse() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapInuse
}

// TestStreamingRoundTrip: a 512 MiB upload and its download pass through
// the controller and the agent API byte for byte without being held in
// memory, and a 1 MiB text write passes the API's 1 MiB JSON limit.
func TestStreamingRoundTrip(t *testing.T) {
	h := newHarness(t)
	link := newFileLink(t, h, func(c net.Conn) net.Conn { return c })
	b := newBrowser(t, h)

	const size = MaxFileUpload
	base := heapInuse()
	stop := heapPeak()
	sent := sha256.New()
	src := io.TeeReader(io.LimitReader(rand.NewChaCha8([32]byte{1}), size), sent)
	up := b.written(t, b.do(t, "POST", "upload", "path=/big.bin", src, size))
	wantSum := hex.EncodeToString(sent.Sum(nil))
	if up.Size != size || up.SHA256 != wantSum {
		t.Errorf("upload answered %+v, want size %d sha256 %s", up, size, wantSum)
	}

	res := b.do(t, "GET", "download", "path=/big.bin", nil, 0)
	got := sha256.New()
	n, err := io.CopyBuffer(got, res.Body, make([]byte, 32<<10))
	_ = res.Body.Close()
	peak := stop()
	if err != nil || res.StatusCode != http.StatusOK || n != size {
		t.Fatalf("download: %d, %d bytes, %v", res.StatusCode, n, err)
	}
	if sum := hex.EncodeToString(got.Sum(nil)); sum != wantSum {
		t.Errorf("download sha256 %s, want %s", sum, wantSum)
	}
	if cd := res.Header.Get("Content-Disposition"); cd != `attachment; filename=big.bin` {
		t.Errorf("download Content-Disposition %q", cd)
	}
	if peak > base && peak-base >= 16<<20 {
		t.Errorf("the heap in use grew by %d MiB during the transfer, want under 16", (peak-base)>>20)
	}

	text := bytes.Repeat([]byte("0123456789abcde\n"), MaxFileWrite/16)
	w := b.written(t, b.do(t, "POST", "write", "path=/text.txt&sha256=", bytes.NewReader(text), int64(len(text))))
	sum := sha256.Sum256(text)
	if w.Size != MaxFileWrite || w.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("1 MiB write answered %+v", w)
	}
	if on, err := os.ReadFile(link.agent.files.at("/text.txt")); err != nil || !bytes.Equal(on, text) {
		t.Errorf("the agent holds %d bytes, %v; want the 1 MiB written", len(on), err)
	}
	for _, op := range []string{"write", "upload"} {
		e := b.written(t, b.do(t, "POST", op, "path=/empty-"+op+"&sha256=", bytes.NewReader(nil), 0))
		empty := sha256.Sum256(nil)
		if e.Size != 0 || e.SHA256 != hex.EncodeToString(empty[:]) {
			t.Errorf("an empty %s answered %+v", op, e)
		}
		if st, err := os.Stat(link.agent.files.at("/empty-" + op)); err != nil || st.Size() != 0 {
			t.Errorf("an empty %s left %v, %v on the agent", op, st, err)
		}
	}

	mk, err := http.NewRequest("POST", b.ts.URL+filePath("mkdir", ""), strings.NewReader(`{"path":"/made"}`))
	if err != nil {
		t.Fatal(err)
	}
	mk.Header.Set("Content-Type", "application/json")
	mk.Header.Set("Cookie", SessionCookiePlain+"="+b.session)
	mk.Header.Set(CSRFHeader, b.csrf)
	if res, err := http.DefaultClient.Do(mk); err != nil || res.StatusCode != http.StatusNoContent {
		t.Errorf("mkdir through the agent: %v %v", res, err)
	} else if st, err := os.Stat(link.agent.files.at("/made")); err != nil || !st.IsDir() {
		t.Errorf("mkdir made no directory on the agent: %v", err)
	}

	res = b.do(t, "GET", "stat", "path=/missing", nil, 0)
	var p problem
	_ = json.NewDecoder(res.Body).Decode(&p)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound || p.Code != errcode.NotFound || p.Detail != "no file /missing" {
		t.Errorf("the agent's refusal reached the browser as %d %+v", res.StatusCode, p)
	}

	res = b.do(t, "GET", "read", "path=/text.txt", nil, 0)
	read, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !bytes.Equal(read, text) || res.Header.Get(wire.HeaderSHA256) != w.SHA256 {
		t.Errorf("read text: %d, %d bytes, hash %q", res.StatusCode, len(read), res.Header.Get(wire.HeaderSHA256))
	}
}

// slowConn writes at most rate bytes a second, so a transfer fills the
// link the way a slow network does.
type slowConn struct {
	net.Conn
	rate int
	mu   sync.Mutex
	next time.Time
}

func (c *slowConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	written := 0
	for len(p) > 0 {
		n := min(len(p), 1<<10)
		if d := time.Until(c.next); d > 0 {
			time.Sleep(d)
		}
		m, err := c.Conn.Write(p[:n])
		written += m
		if c.next.Before(time.Now()) {
			c.next = time.Now()
		}
		c.next = c.next.Add(time.Duration(m) * time.Second / time.Duration(c.rate))
		if err != nil {
			return written, err
		}
		p = p[n:]
	}
	return written, nil
}

// TestHeartbeatDuringUpload: while an upload fills a 1 Mbit/s link, the
// session's heartbeat answers in well under its 45 s budget and an action
// confirm on an action slot completes within the shortest confirm limit.
func TestHeartbeatDuringUpload(t *testing.T) {
	h := newHarness(t)
	const rate = 1_000_000 / 8
	link := newFileLink(t, h, func(c net.Conn) net.Conn { return &slowConn{Conn: c, rate: rate} })
	b := newBrowser(t, h)

	const size = 8 << 20
	sent := sha256.New()
	var moved atomic.Int64
	src := io.TeeReader(io.LimitReader(rand.NewChaCha8([32]byte{2}), size), writerFunc(func(p []byte) (int, error) {
		moved.Add(int64(len(p)))
		return sent.Write(p)
	}))
	type result struct {
		w   wire.FileWritten
		err error
	}
	done := make(chan result, 1)
	go func() {
		res := b.do(t, "POST", "upload", "path=/slow.bin", src, size)
		body, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		var w wire.FileWritten
		if err == nil && res.StatusCode != http.StatusOK {
			err = errors.New(res.Status + " " + string(body))
		}
		if err == nil {
			err = json.Unmarshal(body, &w)
		}
		done <- result{w, err}
	}()

	for moved.Load() < 1<<20 {
		time.Sleep(100 * time.Millisecond)
	}
	var worstPing, worstConfirm time.Duration
	for i := 0; i < 5; i++ {
		start := time.Now()
		if _, err := link.ctrl.Ping(); err != nil {
			t.Fatalf("heartbeat during the upload: %v", err)
		}
		worstPing = max(worstPing, time.Since(start))

		start = time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), lifecycle.MinConfirmTimeout)
		v, err := link.client.Confirm(ctx, core.Principal{Actor: "user:admin", Scope: core.ScopeAdmin}, "c"+strconv.Itoa(i))
		cancel()
		if err != nil || v.State != core.StateConfirmed {
			t.Fatalf("confirm during the upload: %+v, %v", v, err)
		}
		worstConfirm = max(worstConfirm, time.Since(start))
		time.Sleep(2 * time.Second)
	}
	if left := size - moved.Load(); left < 1<<20 {
		t.Fatalf("the upload was nearly done (%d bytes left) when the checks ended; slow the link", left)
	}
	if worstPing >= 45*time.Second {
		t.Errorf("a heartbeat took %v during the upload, want under 45 s", worstPing)
	}
	if worstConfirm >= lifecycle.MinConfirmTimeout {
		t.Errorf("a confirm took %v during the upload, want under %v", worstConfirm, lifecycle.MinConfirmTimeout)
	}
	t.Logf("during the upload: slowest heartbeat %v, slowest confirm %v", worstPing, worstConfirm)

	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	if want := hex.EncodeToString(sent.Sum(nil)); r.w.Size != size || r.w.SHA256 != want {
		t.Errorf("upload answered %+v, want sha256 %s", r.w, want)
	}
	if a, m := link.actions.Load(), link.modules.Load(); a != 5 || m != 1 {
		t.Errorf("%d action and %d module-stream connections, want the 5 confirms on action slots and the upload on a module slot", a, m)
	}
}

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// heldUploads makes h's fake agent read each upload until its body fails,
// telling started when the first byte arrived; the error that ended each
// is sent on ended.
func heldUploads(h *harness) (started chan struct{}, ended chan error) {
	started, ended = make(chan struct{}, 8), make(chan error, 8)
	h.disp.FileFunc = func(ctx context.Context, _ core.Principal, _ string, req core.FileRequest) (core.FileAnswer, error) {
		buf := make([]byte, 1)
		if _, err := io.ReadFull(req.Body, buf); err != nil {
			ended <- err
			return core.FileAnswer{}, err
		}
		started <- struct{}{}
		_, err := io.Copy(io.Discard, req.Body)
		if err == nil {
			err = ctx.Err()
		}
		ended <- err
		return core.FileAnswer{}, err
	}
	return started, ended
}

// openUpload starts an upload of 1 MiB whose body sends one byte and then
// waits; the answer arrives on the returned channel.
func (b *browser) openUpload(t *testing.T) (<-chan *http.Response, *io.PipeWriter) {
	t.Helper()
	pr, pw := io.Pipe()
	out := make(chan *http.Response, 1)
	go func() {
		r, _ := http.NewRequest("POST", b.ts.URL+filePath("upload", "path=/tmp/x"), pr)
		r.ContentLength = 1 << 20
		r.Header.Set("Content-Type", "application/octet-stream")
		r.Header.Set("Cookie", SessionCookiePlain+"="+b.session)
		r.Header.Set(CSRFHeader, b.csrf)
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			res = &http.Response{StatusCode: 0, Status: err.Error(), Body: io.NopCloser(strings.NewReader(""))}
		}
		out <- res
	}()
	go func() { _, _ = pw.Write([]byte{1}) }()
	return out, pw
}

func wantAnswer(t *testing.T, what string, res *http.Response, status int, code errcode.Code) {
	t.Helper()
	defer func() { _ = res.Body.Close() }()
	var p problem
	_ = json.NewDecoder(res.Body).Decode(&p)
	if res.StatusCode != status || p.Code != code {
		t.Errorf("%s: %d %q (%s), want %d %q", what, res.StatusCode, p.Code, p.Detail, status, code)
	}
}

func waitFor[T any](t *testing.T, what string, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatalf("no %s within 10 s", what)
		var zero T
		return zero
	}
}

// advanceUntil moves h's clock a recheck at a time until res answers: a
// transfer arms its recheck timer when it starts, which may be after the
// test's last move.
func advanceUntil(t *testing.T, h *harness, res <-chan *http.Response) *http.Response {
	t.Helper()
	for range 200 {
		h.clock.Advance(SSERecheck)
		select {
		case r := <-res:
			return r
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatal("no answer after 200 rechecks")
	return nil
}

// TestTransferLifecycle: transfers are long-lived requests: at most
// MaxHostTransfers per host, ended by sign-out or expiry at the next
// recheck and by CloseStreams, each with its own answer.
func TestTransferLifecycle(t *testing.T) {
	newRig := func(t *testing.T) (*harness, *browser, chan struct{}, chan error) {
		h := newHarness(t)
		h.cfg.Clock = h.clock
		srv, err := New(h.cfg)
		if err != nil {
			t.Fatal(err)
		}
		h.srv = srv
		started, ended := heldUploads(h)
		return h, newBrowser(t, h), started, ended
	}

	t.Run("limit and sign-out", func(t *testing.T) {
		h, b, started, ended := newRig(t)
		var answers []<-chan *http.Response
		for range MaxHostTransfers {
			res, pw := b.openUpload(t)
			defer func() { _ = pw.Close() }()
			waitFor(t, "upload start", started)
			answers = append(answers, res)
		}
		third, pw := b.openUpload(t)
		defer func() { _ = pw.Close() }()
		wantAnswer(t, "a third transfer on the host", waitFor(t, "third answer", third), http.StatusTooManyRequests, errcode.ConsoleLimit)

		if rec := h.serve(withSession(request("POST", "/api/v1/auth/logout", ""), b.session, b.csrf)); rec.Code/100 != 2 {
			t.Fatalf("logout: %d %s", rec.Code, rec.Body)
		}
		for i, res := range answers {
			wantAnswer(t, "upload "+strconv.Itoa(i)+" after sign-out", advanceUntil(t, h, res), http.StatusForbidden, errcode.Forbidden)
			if err := waitFor(t, "agent end", ended); err == nil {
				t.Errorf("upload %d ended on the agent without an error", i)
			}
		}
	})

	t.Run("expiry", func(t *testing.T) {
		h, b, started, _ := newRig(t)
		res, pw := b.openUpload(t)
		defer func() { _ = pw.Close() }()
		waitFor(t, "upload start", started)
		// Rechecks every 30 s are passive: with no chunk moving the
		// session ends at its idle limit.
		begin := h.clock.Now()
		wantAnswer(t, "upload past the session's idle limit", advanceUntil(t, h, res), http.StatusUnauthorized, errcode.AuthRequired)
		if d := h.clock.Now().Sub(begin); d < auth.SessionIdle-time.Minute || d > auth.SessionIdle+2*time.Minute {
			t.Errorf("the upload ended after %v, want the session's idle limit %v", d, auth.SessionIdle)
		}
	})

	t.Run("chunks keep the session", func(t *testing.T) {
		h, b, started, _ := newRig(t)
		res, pw := b.openUpload(t)
		defer func() { _ = pw.Close() }()
		waitFor(t, "upload start", started)
		lastSeen := func() string {
			var v string
			if err := h.db.Writer().QueryRow("SELECT MAX(last_seen_at) FROM core_sessions").Scan(&v); err != nil {
				t.Fatal(err)
			}
			return v
		}
		for range 13 {
			before := lastSeen()
			h.clock.Advance(5 * time.Minute)
			if _, err := pw.Write([]byte{1}); err != nil {
				t.Fatal(err)
			}
			for i := 0; lastSeen() == before; i++ {
				if i == 200 {
					t.Fatal("a chunk did not touch the session")
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		select {
		case r := <-res:
			wantAnswer(t, "an upload whose chunks kept moving", r, 0, "")
		case <-time.After(100 * time.Millisecond):
		}
	})

	t.Run("console turned off", func(t *testing.T) {
		h, b, started, _ := newRig(t)
		res, pw := b.openUpload(t)
		defer func() { _ = pw.Close() }()
		waitFor(t, "upload start", started)
		h.disp.ModuleUsableFunc = func(_ context.Context, host, module string) error {
			if host == testHostID && module == "console" {
				return errcode.New(errcode.ModuleUnavailable, "module console is unavailable on host: disabled")
			}
			return nil
		}
		wantAnswer(t, "upload after the console was turned off", advanceUntil(t, h, res), http.StatusForbidden, errcode.Forbidden)
	})

	t.Run("CloseStreams", func(t *testing.T) {
		h, b, started, _ := newRig(t)
		res, pw := b.openUpload(t)
		defer func() { _ = pw.Close() }()
		waitFor(t, "upload start", started)
		h.srv.CloseStreams()
		wantAnswer(t, "upload at shutdown", waitFor(t, "answer", res), http.StatusServiceUnavailable, errcode.Unavailable)
	})
}

// TestTransferStalls: a transfer that moves no byte for the chunk timeout
// ends: an upload the agent stops reading answers unavailable, and a
// download the agent stops sending is cut, never passed off as whole.
func TestTransferStalls(t *testing.T) {
	h := newHarness(t)
	h.srv.transferIdle = 300 * time.Millisecond
	h.disp.FileFunc = func(ctx context.Context, _ core.Principal, _ string, req core.FileRequest) (core.FileAnswer, error) {
		if req.Op == "upload" && req.Query.Get("path") == "/moving" {
			if _, err := io.Copy(io.Discard, req.Body); err != nil {
				return core.FileAnswer{}, err
			}
			return core.FileAnswer{Status: 200, ContentType: "application/json", Size: 2, Body: io.NopCloser(strings.NewReader("{}"))}, nil
		}
		if req.Op == "upload" {
			<-ctx.Done()
			return core.FileAnswer{}, ctx.Err()
		}
		pr, pw := io.Pipe()
		go func() {
			_, _ = pw.Write([]byte("half"))
			<-ctx.Done()
			_ = pw.CloseWithError(ctx.Err())
		}()
		return core.FileAnswer{Status: 200, ContentType: "application/octet-stream", Size: 8, Body: pr}, nil
	}
	b := newBrowser(t, h)
	start := time.Now()
	res, pw := b.openUpload(t)
	defer func() { _ = pw.Close() }()
	wantAnswer(t, "an upload the agent stopped reading", waitFor(t, "answer", res), http.StatusServiceUnavailable, errcode.Unavailable)
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("the stalled upload ended after %v", d)
	}

	// A byte every 100 ms keeps a transfer three times the idle timeout.
	pr, mpw := io.Pipe()
	go func() {
		for range 10 {
			time.Sleep(100 * time.Millisecond)
			if _, err := mpw.Write([]byte{1}); err != nil {
				return
			}
		}
		_ = mpw.Close()
	}()
	mv := b.do(t, "POST", "upload", "path=/moving", pr, 10)
	if mv.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(mv.Body)
		t.Errorf("an upload that kept moving: %d %s", mv.StatusCode, body)
	}
	_ = mv.Body.Close()

	dl := b.do(t, "GET", "download", "path=/x", nil, 0)
	got, err := io.ReadAll(dl.Body)
	_ = dl.Body.Close()
	if err == nil || string(got) != "half" {
		t.Errorf("a stalled download read %q, %v; want the half sent and an error", got, err)
	}
}

// TestFileAnswerTypes: the controller sets each answer's Content-Type
// itself, whatever the agent claims, so no file is rendered on the panel's
// origin.
func TestFileAnswerTypes(t *testing.T) {
	h := newHarness(t)
	h.disp.FileFunc = func(_ context.Context, _ core.Principal, _ string, req core.FileRequest) (core.FileAnswer, error) {
		if req.Body != nil {
			_, _ = io.Copy(io.Discard, req.Body)
		}
		return core.FileAnswer{Status: 200, ContentType: "text/html", Size: 13, Body: io.NopCloser(strings.NewReader("<script>x</script>"[:13]))}, nil
	}
	session, csrf := h.login()
	stepUp(t, h, h.code(), session, csrf)
	want := map[string]string{"list": "application/json", "stat": "application/json", "read": "text/plain; charset=utf-8",
		"download": "application/octet-stream", "write": "application/json", "upload": "application/json"}
	for _, fc := range fileCases {
		ct, ok := want[fc.op]
		if !ok {
			continue
		}
		rec := h.serve(withSession(fc.request(), session, csrf))
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != ct || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: %d, Content-Type %q, nosniff %q; want %q", fc.op, rec.Code, rec.Header().Get("Content-Type"),
				rec.Header().Get("X-Content-Type-Options"), ct)
		}
	}
	rec := h.serve(withSession(fileCase{op: "download", method: "GET"}.request(), session, csrf))
	if cd := rec.Header().Get("Content-Disposition"); cd != "attachment" {
		t.Errorf("a download without a path: Content-Disposition %q, want a bare attachment", cd)
	}
}

// TestTransfersOutlastListenerTimeouts: an upload and a download that take
// longer than the listener's read and write timeouts complete, since a
// transfer renews its own deadlines per chunk.
func TestTransfersOutlastListenerTimeouts(t *testing.T) {
	const timeout = 300 * time.Millisecond
	for _, h2 := range []bool{false, true} {
		t.Run(map[bool]string{false: "HTTP/1.1", true: "HTTP/2"}[h2], func(t *testing.T) {
			h := newHarness(t)
			link := newFileLink(t, h, func(c net.Conn) net.Conn { return c })
			b := newBrowserTimeouts(t, h, timeout, h2)

			pr, pw := io.Pipe()
			go func() {
				for range 12 {
					time.Sleep(100 * time.Millisecond)
					if _, err := pw.Write([]byte{7}); err != nil {
						return
					}
				}
				_ = pw.Close()
			}()
			start := time.Now()
			up := b.written(t, b.do(t, "POST", "upload", "path=/paced.bin", pr, 12))
			if up.Size != 12 || time.Since(start) < 4*timeout {
				t.Errorf("the paced upload answered %+v after %v", up, time.Since(start))
			}

			// Big enough that the handler outlives the timeouts even though the
			// kernel's socket buffers take several MiB of it at once.
			data := bytes.Repeat([]byte("darkpanel"), 4<<20)
			if err := os.WriteFile(link.agent.files.at("/big.txt"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			start = time.Now()
			res := b.do(t, "GET", "download", "path=/big.txt", nil, 0)
			var got bytes.Buffer
			buf := make([]byte, 256<<10)
			var err error
			for err == nil {
				var n int
				n, err = res.Body.Read(buf)
				got.Write(buf[:n])
				time.Sleep(10 * time.Millisecond)
			}
			_ = res.Body.Close()
			if !errors.Is(err, io.EOF) || !bytes.Equal(got.Bytes(), data) || time.Since(start) < 4*timeout {
				t.Errorf("the slow download read %d of %d bytes in %v, ending %v", got.Len(), len(data), time.Since(start), err)
			}
			if h2 && res.ProtoMajor != 2 {
				t.Errorf("the download went over %s, want HTTP/2", res.Proto)
			}
		})
	}
}
