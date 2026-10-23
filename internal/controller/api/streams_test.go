package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// relayBody is a dispatcher stream's body that records its closing.
type relayBody struct {
	io.Reader
	closed atomic.Bool
}

func (b *relayBody) Close() error {
	b.closed.Store(true)
	if c, ok := b.Reader.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// relayOpen is one OpenStream the fake dispatcher received.
type relayOpen struct {
	actor, host, module, name string
	params                    url.Values
}

// relayServer serves h's auth and fake dispatcher on 127.0.0.1 with h's
// clock as the SSE clock, and makes the dispatcher's OpenStream answer
// body and record the call on opened.
func relayServer(t *testing.T, h *harness, body io.ReadCloser) (*httptest.Server, chan relayOpen) {
	t.Helper()
	ts, opened, _ := relayServerOf(t, h, body)
	return ts, opened
}

// relayServerOf is relayServer that also returns the API server.
func relayServerOf(t *testing.T, h *harness, body io.ReadCloser) (*httptest.Server, chan relayOpen, *Server) {
	t.Helper()
	opened := make(chan relayOpen, 1)
	h.disp.OpenStreamFunc = func(_ context.Context, p core.Principal, hostID, module, name string, params url.Values) (io.ReadCloser, error) {
		if name == "busy" {
			return nil, errcode.New(errcode.StreamLimit, "user:admin has 4 streams open")
		}
		opened <- relayOpen{p.Actor, hostID, module, name, params}
		return body, nil
	}
	reg, err := core.NewRegistry(demoModule{})
	if err != nil {
		t.Fatal(err)
	}
	log, _ := testLog()
	srv, err := New(Config{Dispatcher: h.disp, Auth: h.auth, Registry: reg, WebCA: fakeWebCA{},
		SchemaVersion: func(context.Context) (int, error) { return 3, nil }, HostID: testHostID, Log: log, Clock: h.clock})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, opened, srv
}

// relayGet starts GET path with token and returns the response once its
// headers arrived.
func relayGet(t *testing.T, ts *httptest.Server, path, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// relayFrames reads body to its end and returns each frame as
// "<event> <data>", leaving out heartbeats.
func relayFrames(body io.Reader) []string {
	b, _ := io.ReadAll(body)
	var frames []string
	for _, block := range strings.Split(strings.TrimSuffix(string(b), "\n\n"), "\n\n") {
		var event, data string
		for _, line := range strings.Split(block, "\n") {
			if v, ok := strings.CutPrefix(line, "event: "); ok {
				event = v
			}
			if v, ok := strings.CutPrefix(line, "data: "); ok {
				data = v
			}
		}
		if event != "" {
			frames = append(frames, event+" "+data)
		}
	}
	return frames
}

func TestRelayStream(t *testing.T) {
	h := newHarness(t)
	body := &relayBody{Reader: strings.NewReader(`{"data":{"n":1}}` + "\n" + `{"data":{"n":2}}` + "\n" + `{"end":{"reason":"done"}}` + "\n")}
	ts, opened := relayServer(t, h, body)
	resp := relayGet(t, ts, "/api/v1/hosts/h1/streams/sm/logs?tail=2", h.token(core.ScopeRead))
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("answer %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	want := []string{`data {"n":1}`, `data {"n":2}`, `end {"reason":"done"}`}
	if got := relayFrames(resp.Body); !reflect.DeepEqual(got, want) {
		t.Errorf("frames\n%q\nwant\n%q", got, want)
	}
	if o := <-opened; o.host != "h1" || o.module != "sm" || o.name != "logs" || !reflect.DeepEqual(o.params, url.Values{"tail": {"2"}}) ||
		!strings.HasPrefix(o.actor, "token:") {
		t.Errorf("OpenStream got %+v", o)
	}
	if !body.closed.Load() {
		t.Error("the dispatcher's reader was not closed")
	}
}

func TestRelayAgentError(t *testing.T) {
	internal := `end {"reason":"error","error":{"code":"internal","detail":"Internal error"}}`
	tests := []struct {
		name, lines string
		want        []string
	}{
		{"agent error", `{"data":{"n":1}}` + "\n" + `{"end":{"reason":"error","error":{"code":"not_found","detail":"no container c1"}}}` + "\n",
			[]string{`data {"n":1}`, `end {"reason":"error","error":{"code":"not_found","detail":"no container c1"}}`}},
		{"agent error without a problem", `{"end":{"reason":"error","error":null}}` + "\n", []string{internal}},
		{"agent error with other members", `{"end":{"reason":"error","error":{"code":"not_found","detail":"gone","extra":"x"}}}` + "\n",
			[]string{`end {"reason":"error","error":{"code":"not_found","detail":"gone"}}`}},
		{"no end line", `{"data":{"n":1}}` + "\n", []string{`data {"n":1}`, internal}},
		{"broken line", `{"data":` + "\n", []string{internal}},
		{"neither data nor end", `{"other":1}` + "\n", []string{internal}},
		{"carriage return", "{\"data\":\r{\"n\":1}}\n" + `{"end":{"reason":"done"}}` + "\n", []string{`data {"n":1}`, `end {"reason":"done"}`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			ts, _ := relayServer(t, h, &relayBody{Reader: strings.NewReader(tt.lines)})
			resp := relayGet(t, ts, "/api/v1/hosts/h1/streams/sm/logs", h.token(core.ScopeRead))
			if got := relayFrames(resp.Body); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("frames\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestRelayOverflow(t *testing.T) {
	pr, pw := io.Pipe()
	body := &relayBody{Reader: pr}
	wrote := make(chan int, 1)
	go func() {
		n := 0
		for {
			if _, err := fmt.Fprintf(pw, `{"data":{"n":%d}}`+"\n", n+1); err != nil {
				wrote <- n
				return
			}
			n++
		}
	}()
	out := make(chan sseFrame, relayBuffer+1)
	relay(body, out)
	if n := <-wrote; n != relayBuffer+1 {
		t.Errorf("the agent wrote %d events before its stream closed, want %d", n, relayBuffer+1)
	}
	if !body.closed.Load() {
		t.Error("an overflow did not close the dispatcher's reader")
	}
	var frames []sseFrame
	for f := range out {
		frames = append(frames, f)
	}
	if len(frames) != relayBuffer+1 || frames[relayBuffer-1].Event != "data" ||
		frames[relayBuffer].Event != "end" || string(frames[relayBuffer].Data) != `{"reason":"overflow"}` {
		t.Fatalf("%d frames ending %+v, want %d data frames and an overflow end", len(frames), frames[len(frames)-1], relayBuffer)
	}
}

func TestRelayEnds(t *testing.T) {
	tests := []struct {
		name   string
		revoke bool
		want   string
	}{
		{"revoked token", true, `end {"reason":"revoked"}`},
		{"idle", false, `end {"reason":"expired"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			pr, pw := io.Pipe()
			t.Cleanup(func() { _ = pw.Close() })
			body := &relayBody{Reader: pr}
			ts, _ := relayServer(t, h, body)
			info, token, err := h.auth.CreateToken(context.Background(), h.user.ID, "relay", core.ScopeRead, 0)
			if err != nil {
				t.Fatal(err)
			}
			resp := relayGet(t, ts, "/api/v1/hosts/h1/streams/sm/logs", token)
			if tt.revoke {
				if _, err := h.auth.RevokeToken(context.Background(), h.user.ID, info.ID); err != nil {
					t.Fatal(err)
				}
			}
			frames := make(chan []string, 1)
			go func() { frames <- relayFrames(resp.Body) }()
			deadline := time.After(10 * time.Second)
			for {
				select {
				case got := <-frames:
					if len(got) == 0 || got[len(got)-1] != tt.want || strings.Count(strings.Join(got, "\n"), "end ") != 1 {
						t.Errorf("frames %q, want one end frame %s", got, tt.want)
					}
					if !body.closed.Load() {
						t.Error("the dispatcher's reader was not closed")
					}
					return
				case <-deadline:
					t.Fatal("the stream did not end")
				case <-time.After(5 * time.Millisecond):
					h.clock.Advance(SSERecheck)
				}
			}
		})
	}
}

// TestRelayEndsOnShutdown: CloseStreams ends a relayed stream with one
// error end frame.
func TestRelayEndsOnShutdown(t *testing.T) {
	h := newHarness(t)
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	body := &relayBody{Reader: pr}
	ts, opened, srv := relayServerOf(t, h, body)
	resp := relayGet(t, ts, "/api/v1/hosts/h1/streams/sm/logs", h.token(core.ScopeRead))
	<-opened
	srv.CloseStreams()
	got := relayFrames(resp.Body)
	want := []string{`end {"reason":"error","error":{"code":"internal","detail":"Internal error"}}`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("frames %q, want %q", got, want)
	}
	if !body.closed.Load() {
		t.Error("the dispatcher's reader was not closed")
	}
}

func TestRelayRefusals(t *testing.T) {
	h := newHarness(t)
	ts, _ := relayServer(t, h, &relayBody{Reader: strings.NewReader("")})
	resp := relayGet(t, ts, "/api/v1/hosts/h1/streams/sm/busy", h.token(core.ScopeRead))
	var p problem
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil || resp.StatusCode != http.StatusTooManyRequests ||
		p.Code != errcode.StreamLimit || resp.Header.Get("Content-Type") != problemContentType {
		t.Errorf("answer %d %s %+v %v, want 429 %s as problem details", resp.StatusCode, resp.Header.Get("Content-Type"), p, err, errcode.StreamLimit)
	}

	r := request(http.MethodGet, "/api/v1/hosts/h1/streams/sm/logs", "")
	r.SetPathValue("host", "h1")
	r.SetPathValue("module", "sm")
	r.SetPathValue("name", "logs")
	r = r.WithContext(withCaller(r.Context(), caller{principal: core.Principal{Actor: "token:t1", Scope: "none"}}))
	rec := httptest.NewRecorder()
	h.srv.handleStream(rec, r)
	decodeProblem(t, rec, http.StatusForbidden, errcode.Forbidden)
}
