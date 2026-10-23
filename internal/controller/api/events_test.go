package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/events"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// sseHarness is newHarness with an event bus, served on a real
// 127.0.0.1 listener.
func sseHarness(t *testing.T) (*harness, *events.Bus, string) {
	t.Helper()
	h := newHarness(t)
	bus := events.NewBus(nil)
	log, buf := testLog()
	h.log = buf
	reg, err := core.NewRegistry(demoModule{})
	if err != nil {
		t.Fatal(err)
	}
	h.srv, err = New(Config{
		Dispatcher: h.disp, Auth: h.auth, Registry: reg, WebCA: fakeWebCA{},
		SchemaVersion: func(context.Context) (int, error) { return 3, nil }, HostID: testHostID,
		Log: log, Events: bus, Clock: h.clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.srv.Handler())
	t.Cleanup(ts.Close)
	return h, bus, ts.URL
}

// sseChange publishes a change event for change id.
func sseChange(bus *events.Bus, id string) {
	bus.Publish(events.TypeChange, events.ChangeEvent{ChangeID: id, Action: "demo.item.remove", State: "completed",
		UpdatedAt: testStart})
}

// sseChangeFrame is the frame of sseChange's event seq.
func sseChangeFrame(epoch string, seq uint64, id string) string {
	return "id: " + events.FormatID(epoch, seq) + "\nevent: change\n" +
		`data: {"hostId":"","changeId":"` + id + `","action":"demo.item.remove","state":"completed","updatedAt":"2026-10-17T12:00:00Z"}`
}

func TestEventsRoute(t *testing.T) {
	h, bus, base := sseHarness(t)

	r := request("GET", "/api/v1/events", "")
	r = r.WithContext(withCaller(r.Context(), caller{principal: core.Principal{Actor: "token:T0"}}))
	rec := httptest.NewRecorder()
	h.srv.handleEvents(rec, r)
	decodeProblem(t, rec, 403, errcode.Forbidden)

	plain := newHarness(t)
	decodeProblem(t, plain.serve(withToken(request("GET", "/api/v1/events", ""), plain.token(core.ScopeRead))), 404, errcode.NotFound)

	resp, rd := sseOpen(t, base, h.token(core.ScopeRead), "")
	defer func() { _ = resp.Body.Close() }()
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
	sseChange(bus, "c1")
	bus.Publish(events.TypeModule, events.ModuleEvent{HostID: "h1", ModuleID: "demo", Enabled: true})
	if got, want := sseNext(t, rd), sseChangeFrame(bus.Epoch(), 1, "c1"); got != want {
		t.Fatalf("frame = %q, want %q", got, want)
	}
	want := "id: " + bus.Epoch() + "-2\nevent: module\n" + `data: {"hostId":"h1","moduleId":"demo","enabled":true,"available":false}`
	if got := sseNext(t, rd); got != want {
		t.Fatalf("frame = %q, want %q", got, want)
	}
}

// TestEventsLeaveTheIdleClock shows GET /api/v1/events authenticated
// without moving the session's last_seen_at, where an ordinary request
// moves it.
func TestEventsLeaveTheIdleClock(t *testing.T) {
	h, _, base := sseHarness(t)
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
	h.clock.Advance(2 * auth.LastSeenInterval)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Add("Cookie", SessionCookiePlain+"="+session)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/events = %d, want 200", resp.StatusCode)
	}
	if got, want := idleEnd(), loginAt.Add(auth.SessionIdle); !got.Equal(want) {
		t.Fatalf("after an events request the session ends at %s, want %s", got, want)
	}

	if rec := h.serve(withSession(request("GET", "/api/v1/auth/session", ""), session, "")); rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/auth/session = %d", rec.Code)
	}
	if got, want := idleEnd(), h.clock.Now().Add(auth.SessionIdle); !got.Equal(want) {
		t.Fatalf("after an ordinary request the session ends at %s, want %s", got, want)
	}
}

func TestEventsReplay(t *testing.T) {
	other := events.NewBus(nil).Epoch()
	reset := func(epoch string, seq uint64) string {
		return "id: " + events.FormatID(epoch, seq) + "\nevent: reset\ndata: {}"
	}
	cases := []struct {
		name   string
		before uint64
		lastID func(epoch string) string
		want   func(epoch string) []string // the frames before the live event before+1
	}{
		{"this epoch, seq 3 of 5", 5, func(e string) string { return e + "-3" }, func(e string) []string {
			return []string{sseChangeFrame(e, 4, "c4"), sseChangeFrame(e, 5, "c5")}
		}},
		{"this epoch, the last seq", 5, func(e string) string { return e + "-5" }, func(string) []string { return nil }},
		{"another epoch", 5, func(string) string { return other + "-3" }, func(e string) []string { return []string{reset(e, 5)} }},
		{"a malformed id", 5, func(string) string { return "nonsense" }, func(e string) []string { return []string{reset(e, 5)} }},
		{"a seq after the last", 5, func(e string) string { return e + "-9" }, func(e string) []string { return []string{reset(e, 5)} }},
		{"a seq older than the ring", events.RingSize + 5, func(e string) string { return e + "-3" }, func(e string) []string {
			return []string{reset(e, events.RingSize+5)}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, bus, base := sseHarness(t)
			for i := uint64(1); i <= tc.before; i++ {
				sseChange(bus, "c"+strconv.FormatUint(i, 10))
			}
			resp, rd := sseOpen(t, base, h.token(core.ScopeRead), tc.lastID(bus.Epoch()))
			defer func() { _ = resp.Body.Close() }()
			live := "c" + strconv.FormatUint(tc.before+1, 10)
			sseChange(bus, live)
			for _, want := range append(tc.want(bus.Epoch()), sseChangeFrame(bus.Epoch(), tc.before+1, live)) {
				if got := sseNext(t, rd); got != want {
					t.Fatalf("frame = %q, want %q", got, want)
				}
			}
		})
	}
}

// TestEventBacklogTwoIDs: two Last-Event-ID headers, even two valid ones,
// get a reset.
func TestEventBacklogTwoIDs(t *testing.T) {
	bus := events.NewBus(nil)
	for i := range 5 {
		sseChange(bus, "c"+strconv.Itoa(i+1))
	}
	id := events.FormatID(bus.Epoch(), 3)
	if got := eventBacklog(bus, []string{id}, 5); len(got) != 2 {
		t.Fatalf("one id: %d events, want the 2 after it", len(got))
	}
	got := eventBacklog(bus, []string{id, id}, 5)
	if len(got) != 1 || got[0].Type != events.TypeReset || got[0].Seq != 5 {
		t.Fatalf("two ids: %+v, want one reset at seq 5", got)
	}
}

func TestEventsLimit(t *testing.T) {
	h, _, base := sseHarness(t)
	token := h.token(core.ScopeRead)
	var open []*http.Response
	for range MaxEventStreams {
		resp, _ := sseOpen(t, base, token, "")
		open = append(open, resp)
	}
	defer func() {
		for _, resp := range open {
			_ = resp.Body.Close()
		}
	}()

	fifth := sseGet(t, base, token, "")
	body, err := io.ReadAll(fifth.Body)
	_ = fifth.Body.Close()
	var p problem
	if err != nil || fifth.StatusCode != http.StatusTooManyRequests || json.Unmarshal(body, &p) != nil || p.Code != errcode.StreamLimit {
		t.Fatalf("fifth stream = %d %s (%v), want 429 %s", fifth.StatusCode, body, err, errcode.StreamLimit)
	}

	other, _ := sseOpen(t, base, h.token(core.ScopeRead), "")
	_ = other.Body.Close()

	_ = open[0].Body.Close()
	open = open[1:]
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp := sseGet(t, base, token, "")
		if resp.StatusCode == http.StatusOK {
			open = append(open, resp)
			break
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusTooManyRequests || time.Now().After(deadline) {
			t.Fatalf("after a stream closed: %d, want a new stream accepted", resp.StatusCode)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// sseOpen opens GET /api/v1/events on base with a bearer token and, when
// set, a Last-Event-ID, and checks the stream's status.
func sseOpen(t *testing.T, base, token, lastID string) (*http.Response, *bufio.Reader) {
	t.Helper()
	resp := sseGet(t, base, token, lastID)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		_ = resp.Body.Close()
		t.Fatalf("GET /api/v1/events = %d %s, want 200 text/event-stream", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	return resp, bufio.NewReader(resp.Body)
}

// sseGet sends GET /api/v1/events; the caller closes the body.
func sseGet(t *testing.T, base, token, lastID string) *http.Response {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
