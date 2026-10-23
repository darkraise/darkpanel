package listen

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// rtSpec is a listener on an address the test binder ignores, for a proxy at
// 127.0.0.1, the address every test client dials from.
var rtSpec = core.ListenerSpec{
	Addr:         netip.MustParseAddrPort("172.31.255.1:9443"),
	AllowedPeer:  netip.MustParseAddr("127.0.0.1"),
	ClientHeader: core.ClientHeaderNPM,
	PublicHost:   "panel.example.com",
	ChangeID:     "01CHANGE",
}

// rtFixture is a Runtime over a fresh controller database, binding every
// listener on 127.0.0.1 with a free port, with handlers from rtHandler.
type rtFixture struct {
	db      *store.DB
	clock   *coretest.FakeClock
	rt      *Runtime
	tls     *tls.Config
	client  *http.Client
	binder  *rtBinder
	release chan struct{} // closed to let /slow answer
	entered chan string   // receives the path of every request that reached a handler

	mu    sync.Mutex
	built []core.ListenerInfo
	isRev map[string]func(context.Context) (bool, error)
}

// rtBinder binds 127.0.0.1:0 for every endpoint, unless fail is set.
type rtBinder struct {
	mu   sync.Mutex
	fail error
}

func (b *rtBinder) bind(ctx context.Context, e Endpoint) (net.Listener, error) {
	b.mu.Lock()
	err := b.fail
	b.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("listen: bind %s: %w", e.URL(), err)
	}
	var lc net.ListenConfig
	return lc.Listen(ctx, "tcp4", "127.0.0.1:0")
}

func (b *rtBinder) setFail(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fail = err
}

func newRtFixture(t *testing.T) *rtFixture {
	t.Helper()
	cfg, client := testTLS(t)
	f := &rtFixture{
		db: openControllerDB(t), clock: coretest.NewFakeClock(testStart), tls: cfg, client: client,
		binder: &rtBinder{}, release: make(chan struct{}), entered: make(chan string, 16),
		isRev: map[string]func(context.Context) (bool, error){},
	}
	f.rt = NewRuntime(RuntimeConfig{DB: f.db, Clock: f.clock, Log: slog.New(slog.DiscardHandler), Bind: f.binder.bind})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = f.rt.Shutdown(ctx)
	})
	return f
}

// rtHandler answers "/" with the listener id and whether the connection
// resumed a TLS session, holds "/slow" until release is closed, and streams
// "/stream" until the listener is revoked.
func (f *rtFixture) rtHandler(info core.ListenerInfo, revoked <-chan struct{}, isRevoked func(context.Context) (bool, error)) http.Handler {
	f.mu.Lock()
	f.built = append(f.built, info)
	f.isRev[info.ID] = isRevoked
	f.mu.Unlock()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.entered <- r.URL.Path
		switch r.URL.Path {
		case "/slow":
			<-f.release
			_, _ = io.WriteString(w, "slow done")
		case "/stream":
			_, _ = io.WriteString(w, "event 1\n")
			w.(http.Flusher).Flush()
			<-revoked
			_, _ = io.WriteString(w, "end revoked\n")
		default:
			_, _ = io.WriteString(w, info.ID+" "+rtResumed(r))
		}
	})
}

// rtResumed says whether r's connection resumed a TLS session, as a fixed
// string.
func rtResumed(r *http.Request) string {
	if r.TLS != nil && r.TLS.DidResume {
		return "resumed=true"
	}
	return "resumed=false"
}

func (f *rtFixture) start(t *testing.T) {
	t.Helper()
	if err := f.rt.Start(context.Background(), f.rtHandler, f.tls); err != nil {
		t.Fatal(err)
	}
}

// open opens spec and returns the listener's id and base URL: the one
// address Addrs gained.
func (f *rtFixture) open(t *testing.T, spec core.ListenerSpec) (string, string) {
	t.Helper()
	before := f.rt.Addrs()
	id, err := f.rt.Open(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range f.rt.Addrs() {
		if !slices.Contains(before, a) {
			return id, "https://" + a.String()
		}
	}
	t.Fatalf("Open bound no new address; Addrs = %v", f.rt.Addrs())
	return "", ""
}

func (f *rtFixture) state(t *testing.T, id string) string {
	t.Helper()
	info, err := f.rt.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return info.State
}

func rtGet(client *http.Client, url string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

func TestRuntimeOpenServesHTTPSToTheAllowedPeer(t *testing.T) {
	f := newRtFixture(t)
	f.start(t)
	id, url := f.open(t, rtSpec)
	if body, err := rtGet(f.client, url+"/"); err != nil || !strings.HasPrefix(body, id+" ") {
		t.Fatalf("GET = %q, %v; want the listener's answer", body, err)
	}
	if got := f.state(t, id); got != core.ListenerOpen {
		t.Errorf("state %s, want open", got)
	}
	f.mu.Lock()
	built := slices.Clone(f.built)
	f.mu.Unlock()
	want := core.ListenerInfo{ID: id, Spec: rtSpec, State: core.ListenerOpen}
	if len(built) != 1 || built[0] != want {
		t.Errorf("handler built for %+v, want %+v", built, want)
	}
	plain := &http.Client{Timeout: 5 * time.Second}
	if body, err := rtGet(plain, strings.Replace(url, "https://", "http://", 1)+"/"); err == nil && strings.HasPrefix(body, id) {
		t.Fatal("a plain HTTP request was served")
	}

	other := rtSpec
	other.AllowedPeer = netip.MustParseAddr("127.0.0.2")
	_, otherURL := f.open(t, other)
	if body, err := rtGet(f.client, otherURL+"/"); err == nil {
		t.Fatalf("a request from 127.0.0.1 to a listener for 127.0.0.2 was answered: %q", body)
	}
}

func TestRuntimeOpenValidates(t *testing.T) {
	f := newRtFixture(t)
	f.start(t)
	for _, tc := range []struct {
		name string
		edit func(s *core.ListenerSpec)
	}{
		{"no address", func(s *core.ListenerSpec) { s.Addr = netip.AddrPort{} }},
		{"port 0", func(s *core.ListenerSpec) { s.Addr = netip.MustParseAddrPort("172.31.255.1:0") }},
		{"a zoned address", func(s *core.ListenerSpec) { s.Addr = netip.MustParseAddrPort("[fe80::1%dp-panel]:9443") }},
		{"no allowed peer", func(s *core.ListenerSpec) { s.AllowedPeer = netip.Addr{} }},
		{"a forwarded header", func(s *core.ListenerSpec) { s.ClientHeader = "X-Forwarded-For" }},
		{"no public host", func(s *core.ListenerSpec) { s.PublicHost = "" }},
		{"no change", func(s *core.ListenerSpec) { s.ChangeID = "" }},
	} {
		spec := rtSpec
		tc.edit(&spec)
		var e *errcode.Error
		if _, err := f.rt.Open(context.Background(), spec); !errors.As(err, &e) || e.Code != errcode.InvalidInput {
			t.Errorf("%s: Open = %v, want request.invalid_input", tc.name, err)
		}
	}
	if rows, err := listListeners(context.Background(), f.db, core.ListenerOpen); err != nil || len(rows) != 0 {
		t.Fatalf("rows %+v, %v; want none recorded", rows, err)
	}
	mapped := rtSpec
	mapped.AllowedPeer = netip.MustParseAddr("::ffff:127.0.0.1")
	id, url := f.open(t, mapped)
	if info, err := f.rt.Get(context.Background(), id); err != nil || info.Spec.AllowedPeer != rtSpec.AllowedPeer {
		t.Fatalf("Get = %+v, %v; want the allowed peer unmapped", info, err)
	}
	if _, err := rtGet(f.client, url+"/"); err != nil {
		t.Fatalf("a request from the unmapped peer was refused: %v", err)
	}
}

// The API compares a request's Origin with the public host exactly, so Open
// records it in one form: lower case, without a trailing dot.
func TestRuntimeOpenNormalisesThePublicHost(t *testing.T) {
	ctx := context.Background()
	f := newRtFixture(t)
	f.start(t)
	for _, host := range []string{"Panel.Example.COM.", "PANEL.example.com", "panel.example.com."} {
		spec := rtSpec
		spec.PublicHost = host
		id, _ := f.open(t, spec)
		info, err := f.rt.Get(ctx, id)
		if err != nil || info.Spec.PublicHost != "panel.example.com" {
			t.Errorf("%q: Get = %+v, %v; want the public host panel.example.com", host, info, err)
		}
	}
	f.mu.Lock()
	built := slices.Clone(f.built)
	f.mu.Unlock()
	for _, info := range built {
		if info.Spec.PublicHost != "panel.example.com" {
			t.Errorf("handler built with the public host %q, want panel.example.com", info.Spec.PublicHost)
		}
	}
	spec := rtSpec
	spec.PublicHost = "."
	var e *errcode.Error
	if _, err := f.rt.Open(ctx, spec); !errors.As(err, &e) || e.Code != errcode.InvalidInput {
		t.Errorf("Open with the public host %q = %v, want request.invalid_input", spec.PublicHost, err)
	}
}

// Composite recovery may open a listener before the network listeners
// exist: the row is recorded open, and Start binds it; a listener revoked in
// between is never bound.
func TestRuntimeOpenBeforeStart(t *testing.T) {
	ctx := context.Background()
	f := newRtFixture(t)
	early, err := f.rt.Open(ctx, rtSpec)
	if err != nil {
		t.Fatalf("Open before Start = %v", err)
	}
	gone, err := f.rt.Open(ctx, rtSpec)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.rt.Revoke(ctx, gone); err != nil {
		t.Fatalf("Revoke before Start = %v", err)
	}
	if addrs := f.rt.Addrs(); len(addrs) != 0 {
		t.Fatalf("Addrs = %v before Start, want none", addrs)
	}
	if got := f.state(t, early); got != core.ListenerOpen {
		t.Fatalf("state before Start %s, want open", got)
	}
	f.start(t)
	addrs := f.rt.Addrs()
	if len(addrs) != 1 {
		t.Fatalf("Addrs = %v after Start, want the open listener only", addrs)
	}
	if body, err := rtGet(f.client, "https://"+addrs[0].String()+"/"); err != nil || !strings.HasPrefix(body, early+" ") {
		t.Fatalf("GET = %q, %v; want the listener opened before Start", body, err)
	}
	if got := f.state(t, gone); got != core.ListenerRevoked {
		t.Errorf("state of the listener revoked before Start %s, want revoked", got)
	}
	f.mu.Lock()
	built := slices.Clone(f.built)
	f.mu.Unlock()
	if len(built) != 1 || built[0].ID != early {
		t.Errorf("handlers built for %+v, want only %s", built, early)
	}

	if err := f.rt.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rt.Open(ctx, rtSpec); !errors.Is(err, errRuntimeShutDown) {
		t.Errorf("Open after Shutdown = %v, want shut down", err)
	}
}

func TestRuntimeBindFailureClosesTheListener(t *testing.T) {
	f := newRtFixture(t)
	f.start(t)
	f.binder.setFail(syscall.EADDRINUSE)
	if _, err := f.rt.Open(context.Background(), rtSpec); !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("Open = %v, want the bind error", err)
	}
	if closed, err := listListeners(context.Background(), f.db, core.ListenerClosed); err != nil || len(closed) != 1 {
		t.Fatalf("closed rows %+v, %v; want the failed listener closed", closed, err)
	}
}

// A bind that fails because Open's context ended still closes the row, so no
// later Start binds a listener whose Open failed.
func TestRuntimeBindFailureClosesTheListenerOnACancelledContext(t *testing.T) {
	f := newRtFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt := NewRuntime(RuntimeConfig{DB: f.db, Clock: f.clock, Bind: func(ctx context.Context, _ Endpoint) (net.Listener, error) {
		cancel()
		return nil, ctx.Err()
	}})
	if err := rt.Start(context.Background(), f.rtHandler, f.tls); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })
	if _, err := rt.Open(ctx, rtSpec); !errors.Is(err, context.Canceled) {
		t.Fatalf("Open = %v, want the bind's error", err)
	}
	if open, err := listListeners(context.Background(), f.db, core.ListenerOpen); err != nil || len(open) != 0 {
		t.Fatalf("open rows %+v, %v; want the failed listener closed", open, err)
	}
}

// Shutdown cuts the responses a revoked listener is still finishing once its
// context ends, a listener already closed included.
func TestRuntimeShutdownCutsARevokedListenersResponses(t *testing.T) {
	ctx := context.Background()
	f := newRtFixture(t)
	f.start(t)
	id, url := f.open(t, rtSpec)
	slowErr := make(chan error, 1)
	go func() {
		_, err := rtGet(f.client, url+"/slow")
		slowErr <- err
	}()
	<-f.entered
	defer close(f.release)
	if err := f.rt.Revoke(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := f.rt.Close(ctx, id); err != nil {
		t.Fatal(err)
	}
	sctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = f.rt.Shutdown(sctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown waited for a revoked listener's response past its context")
	}
	if err := <-slowErr; err == nil {
		t.Error("the running response was not cut")
	}
}

func TestRuntimeRevokeEndsStreamsAndLetsResponsesFinish(t *testing.T) {
	ctx := context.Background()
	f := newRtFixture(t)
	f.start(t)
	id, url := f.open(t, rtSpec)

	slow := make(chan *http.Response, 1)
	slowErr := make(chan error, 1)
	go func() {
		resp, err := f.client.Get(url + "/slow")
		if err != nil {
			slowErr <- err
			return
		}
		slow <- resp
	}()
	stream, err := f.client.Get(url + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Body.Close() }()
	lines := bufio.NewReader(stream.Body)
	if line, err := lines.ReadString('\n'); err != nil || line != "event 1\n" {
		t.Fatalf("first stream line %q, %v", line, err)
	}
	for range 2 {
		<-f.entered
	}

	if err := f.rt.Revoke(ctx, id); err != nil {
		t.Fatal(err)
	}
	if got := f.state(t, id); got != core.ListenerRevoked {
		t.Errorf("state after Revoke %s, want revoked", got)
	}
	f.mu.Lock()
	isRevoked := f.isRev[id]
	f.mu.Unlock()
	if revoked, err := isRevoked(ctx); err != nil || !revoked {
		t.Errorf("isRevoked = %v, %v; want true", revoked, err)
	}
	if line, err := lines.ReadString('\n'); err != nil || line != "end revoked\n" {
		t.Errorf("stream after Revoke %q, %v; want its end", line, err)
	}
	if _, err := lines.ReadString('\n'); !errors.Is(err, io.EOF) {
		t.Errorf("the stream stayed open: %v", err)
	}
	if len(f.rt.Addrs()) != 0 {
		t.Errorf("Addrs = %v after Revoke, want none", f.rt.Addrs())
	}
	if _, err := net.DialTimeout("tcp", strings.TrimPrefix(url, "https://"), time.Second); err == nil {
		t.Error("a revoked listener accepted a new connection")
	}

	close(f.release)
	select {
	case resp := <-slow:
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || string(body) != "slow done" || !resp.Close {
			t.Errorf("the running response %q, %v, close %v; want it written whole and its connection closed", body, err, resp.Close)
		}
	case err := <-slowErr:
		t.Fatalf("the running request failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the running request never finished")
	}

	if err := f.rt.Revoke(ctx, id); err != nil {
		t.Errorf("a second Revoke = %v, want nil", err)
	}
	if err := f.rt.Close(ctx, id); err != nil || f.state(t, id) != core.ListenerClosed {
		t.Errorf("Close = %v, state %s; want closed", err, f.state(t, id))
	}
	if err := f.rt.Close(ctx, id); err != nil {
		t.Errorf("a second Close = %v, want nil", err)
	}
	if err := f.rt.Revoke(ctx, id); err != nil || f.state(t, id) != core.ListenerClosed {
		t.Errorf("Revoke of a closed listener = %v, state %s; want nil and still closed", err, f.state(t, id))
	}
}

func TestRuntimeCloseAndGetRefusals(t *testing.T) {
	ctx := context.Background()
	f := newRtFixture(t)
	f.start(t)
	id, _ := f.open(t, rtSpec)
	var e *errcode.Error
	if err := f.rt.Close(ctx, id); !errors.As(err, &e) || e.Code != errcode.InvalidInput {
		t.Errorf("Close of an open listener = %v, want request.invalid_input", err)
	}
	for name, err := range map[string]error{
		"Get":    func() error { _, err := f.rt.Get(ctx, "01MISSING"); return err }(),
		"Revoke": f.rt.Revoke(ctx, "01MISSING"),
		"Close":  f.rt.Close(ctx, "01MISSING"),
	} {
		if !errors.As(err, &e) || e.Code != errcode.NotFound {
			t.Errorf("%s of a missing listener = %v, want not_found", name, err)
		}
	}
}

func TestRuntimeStartReopensOnlyOpenListeners(t *testing.T) {
	f := newRtFixture(t)
	rsInsert(t, f.db, "L1", rtSpec, testStart)
	rsInsert(t, f.db, "L2", rtSpec, testStart.Add(time.Second))
	rsInsert(t, f.db, "L3", rtSpec, testStart.Add(2*time.Second))
	rsSet(t, f.db, "L2", core.ListenerRevoked, core.ListenerOpen)
	rsSet(t, f.db, "L3", core.ListenerRevoked, core.ListenerOpen)
	rsSet(t, f.db, "L3", core.ListenerClosed, core.ListenerRevoked)
	f.start(t)
	addrs := f.rt.Addrs()
	if len(addrs) != 1 {
		t.Fatalf("Addrs = %v, want only L1 bound", addrs)
	}
	if body, err := rtGet(f.client, "https://"+addrs[0].String()+"/"); err != nil || !strings.HasPrefix(body, "L1 ") {
		t.Fatalf("GET = %q, %v; want L1", body, err)
	}
	if err := f.rt.Start(context.Background(), f.rtHandler, f.tls); err == nil {
		t.Error("a second Start succeeded")
	}
}

func TestRuntimeStartRetriesAFailedBind(t *testing.T) {
	f := newRtFixture(t)
	rsInsert(t, f.db, "L1", rtSpec, testStart)
	rsInsert(t, f.db, "L2", rtSpec, testStart.Add(time.Second))
	f.binder.setFail(syscall.EADDRNOTAVAIL)
	f.start(t)
	if addrs := f.rt.Addrs(); len(addrs) != 0 {
		t.Fatalf("Addrs = %v while every bind fails", addrs)
	}
	if err := f.rt.Revoke(context.Background(), "L2"); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(CheckInterval)
	if addrs := f.rt.Addrs(); len(addrs) != 0 {
		t.Fatalf("Addrs = %v; the bind still fails", addrs)
	}
	f.binder.setFail(nil)
	f.clock.Advance(CheckInterval)
	addrs := f.rt.Addrs()
	if len(addrs) != 1 {
		t.Fatalf("Addrs = %v after the bind recovered, want L1 only", addrs)
	}
	if body, err := rtGet(f.client, "https://"+addrs[0].String()+"/"); err != nil || !strings.HasPrefix(body, "L1 ") {
		t.Fatalf("GET = %q, %v; want L1, never the revoked L2", body, err)
	}
	if f.clock.Pending() != 0 {
		t.Errorf("%d timers armed with nothing left to bind", f.clock.Pending())
	}
}

// TestRuntimeSessionResumption shares one tls.Config between a web listener
// and a runtime listener with shared ticket keys: a session from the web
// listener resumes on the runtime listener, and a resumed session gives no
// way past a revocation.
func TestRuntimeSessionResumption(t *testing.T) {
	f := newRtFixture(t)
	f.tls.SetSessionTicketKeys([][32]byte{{7, 7, 7}})
	f.client.Transport.(*http.Transport).TLSClientConfig.ClientSessionCache = tls.NewLRUClientSessionCache(8)
	f.client.Transport.(*http.Transport).DisableKeepAlives = true

	web := NewSet(SetConfig{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "web "+rtResumed(r))
	}), TLS: f.tls, Bind: (&fakeBinder{}).bind, Log: slog.New(slog.DiscardHandler)})
	t.Cleanup(func() { _ = web.Shutdown(context.Background()) })
	ep := Endpoint{Addr: netip.MustParseAddrPort("127.0.0.1:0"), TLS: true}
	if err := web.Update(context.Background(), []Endpoint{ep}); err != nil {
		t.Fatal(err)
	}
	if body, err := rtGet(f.client, "https://"+boundAddr(t, web, ep)+"/"); err != nil || body != "web resumed=false" {
		t.Fatalf("first web GET = %q, %v; want a full handshake", body, err)
	}

	f.start(t)
	id, url := f.open(t, rtSpec)
	if body, err := rtGet(f.client, url+"/"); err != nil || body != id+" resumed=true" {
		t.Fatalf("runtime GET = %q, %v; want the web listener's session resumed", body, err)
	}
	if err := f.rt.Revoke(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if body, err := rtGet(f.client, url+"/"); err == nil {
		t.Fatalf("a resumed session reached a revoked listener: %q", body)
	}
}
