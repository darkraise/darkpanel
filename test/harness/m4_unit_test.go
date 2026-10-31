//go:build harness

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// m4TestAPI returns a client of srv with the poll interval shortened for the
// test's duration.
func m4TestAPI(t *testing.T, srv *httptest.Server) *API {
	t.Helper()
	old := pollInterval
	pollInterval = 5 * time.Millisecond
	t.Cleanup(func() { pollInterval = old })
	return NewAPI(srv.URL, (&net.Dialer{}).DialContext)
}

func TestM4xFrames(t *testing.T) {
	const two = "id: 1\nevent: data\ndata: {\"text\":\"a\"}\n\n: keep-alive\n\nevent: data\ndata: {\"text\":\"b\"}\n\nevent: end\ndata: {\"reason\":\"done\"}\n\n"
	got, err := m4xFrames(strings.NewReader(two), 2)
	if err != nil || len(got) != 2 || string(got[0]) != `{"text":"a"}` || string(got[1]) != `{"text":"b"}` {
		t.Fatalf("m4xFrames = %s, %v; want the two data events", got, err)
	}
	bad := []struct{ name, in, want string }{
		{"end first", "event: end\ndata: {\"reason\":\"expired\"}\n\n", "expired"},
		{"end before n", "event: data\ndata: {\"n\":1}\n\nevent: end\ndata: {\"reason\":\"done\"}\n\n", "1 of 3"},
		{"not JSON", "event: data\ndata: {oops\n\n", "not JSON"},
		{"closed early", "event: data\ndata: {\"n\":1}\n\n", "closed after 1 of 3"},
	}
	for _, c := range bad {
		if _, err := m4xFrames(strings.NewReader(c.in), 3); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want one naming %q", c.name, err, c.want)
		}
	}
}

func TestM4xAtLeast(t *testing.T) {
	for _, c := range []struct {
		have, min string
		want      bool
	}{
		{"1.44", "1.44", true}, {"1.51", "1.44", true}, {"1.9", "1.44", false},
		{"2.0", "1.44", true}, {"1.43", "1.44", false}, {"", "1.44", false}, {"abc", "1.44", false},
	} {
		if got := m4xAtLeast(c.have, c.min); got != c.want {
			t.Errorf("m4xAtLeast(%q, %q) = %t, want %t", c.have, c.min, got, c.want)
		}
	}
}

func TestM4xUndoSettled(t *testing.T) {
	if err := m4xUndoSettled(Change{ID: "c1", State: core.StateReverted}); err != nil {
		t.Errorf("a reverted change: %v", err)
	}
	err := m4xUndoSettled(Change{ID: "c1", State: core.StateNeedsRepair})
	var ue *m4UndoError
	if !errors.As(err, &ue) || ue.Change.State != core.StateNeedsRepair || !strings.Contains(err.Error(), "needs_repair") {
		t.Errorf("a needs_repair change: %v, want an *m4UndoError naming the state", err)
	}
}

func TestM4Plan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/hosts/h1/actions/docker.compose.up/v1/plan":
			_, _ = w.Write([]byte(`{"planId":"p1","confirmation":"web","diff":[{"op":"+","text":"x"}],"guards":[]}`))
		case "/api/v1/hosts/h1/actions/docker.container.stop/v1/plan":
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"docker.owned","detail":"container web belongs to module npm"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	api := m4TestAPI(t, srv)
	p, err := m4Plan(context.Background(), api, "h1", "docker.compose.up", map[string]any{"project": "web"})
	if err != nil || p.PlanID != "p1" || p.Confirmation != "web" || len(p.Diff) != 1 {
		t.Fatalf("m4Plan = %+v, %v; want plan p1 with confirmation web", p, err)
	}
	_, err = m4Plan(context.Background(), api, "h1", "docker.container.stop", map[string]any{"container": "web"})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != "docker.owned" {
		t.Errorf("m4Plan of a refused action = %v, want an *APIError with code docker.owned", err)
	}
}

func TestM4ApplyAsync(t *testing.T) {
	var polls atomic.Int32
	var confirmed atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/hosts/h1/actions/docker.compose.up/v1/plan":
			_, _ = w.Write([]byte(`{"planId":"p1","confirmation":"web","diff":[],"guards":[]}`))
		case "/api/v1/hosts/h1/actions/docker.compose.up/v1/apply":
			var body struct {
				PlanID       string `json:"planId"`
				Confirmation string `json:"confirmation"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			confirmed.Store(body.PlanID + "/" + body.Confirmation)
			_, _ = w.Write([]byte(`{"id":"c1","state":"applying"}`))
		case "/api/v1/changes/c1":
			if polls.Add(1) < 3 {
				_, _ = w.Write([]byte(`{"id":"c1","state":"applying"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"c1","state":"completed","output":{"project":"web"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	ch := m4ApplyAsync(context.Background(), t, m4TestAPI(t, srv), "h1", "docker.compose.up", map[string]any{"project": "web"})
	if ch.State != core.StateCompleted || string(ch.Output) != `{"project":"web"}` {
		t.Errorf("change = %+v, want completed with the output", ch)
	}
	if got := confirmed.Load(); got != "p1/web" {
		t.Errorf("apply carried %v, want the plan id and the plan's confirmation p1/web", got)
	}
	if polls.Load() < 3 {
		t.Errorf("polled %d times, want it to wait while the change was applying", polls.Load())
	}
}

func TestM4Undo(t *testing.T) {
	for _, c := range []struct {
		name, final string
		wantErr     bool
	}{{"reverted", "reverted", false}, {"conflict", "needs_repair", true}} {
		t.Run(c.name, func(t *testing.T) {
			var polls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/changes/c1/undo":
					_, _ = w.Write([]byte(`{"id":"c1","state":"undoing"}`))
				case r.URL.Path == "/api/v1/changes/c1":
					state := "undoing"
					if polls.Add(1) >= 2 {
						state = c.final
					}
					_, _ = w.Write([]byte(`{"id":"c1","state":"` + state + `"}`))
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(srv.Close)
			err := m4Undo(context.Background(), m4TestAPI(t, srv), "c1")
			var ue *m4UndoError
			switch {
			case c.wantErr && (!errors.As(err, &ue) || ue.Change.State != c.final):
				t.Errorf("m4Undo = %v, want an *m4UndoError in state %s", err, c.final)
			case !c.wantErr && err != nil:
				t.Errorf("m4Undo = %v, want nil", err)
			}
		})
	}
}

func TestM4Stream(t *testing.T) {
	var query atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/hosts/h1/streams/docker/logs" {
			http.NotFound(w, r)
			return
		}
		query.Store(r.URL.RawQuery)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: data\ndata: {\"stream\":\"stdout\",\"text\":\"hello\"}\n\nevent: end\ndata: {\"reason\":\"done\"}\n\n"))
	}))
	t.Cleanup(srv.Close)
	api := m4TestAPI(t, srv)
	got, err := m4Stream(context.Background(), api, "h1", "docker", "logs", url.Values{"container": {"web"}, "tail": {"5"}}, 1)
	if err != nil || len(got) != 1 || !strings.Contains(string(got[0]), `"hello"`) {
		t.Fatalf("m4Stream = %s, %v; want the one logged line", got, err)
	}
	if q, _ := query.Load().(string); q != "container=web&tail=5" {
		t.Errorf("the stream was opened with %q, want container=web&tail=5", q)
	}
	if _, err := m4Stream(context.Background(), api, "h1", "docker", "logs", nil, 2); err == nil || !strings.Contains(err.Error(), "1 of 2") {
		t.Errorf("a stream that ends early: %v, want an error naming 1 of 2", err)
	}
	if _, err := m4Stream(context.Background(), api, "h1", "docker", "stats", nil, 1); err == nil {
		t.Error("a missing stream: no error")
	}
}

func TestM4xContainers(t *testing.T) {
	const list = `{"containers":[{"id":"a1","name":"web","image":"nginx","state":"running","owner":""},{"id":"b2","name":"cf","image":"cloudflared","state":"exited","owner":"cloudflare"}]}`
	cs, err := m4xContainers([]byte(list))
	if err != nil || len(cs) != 2 {
		t.Fatalf("m4xContainers = %+v, %v", cs, err)
	}
	if c, ok := m4xFindContainer(cs, "cf"); !ok || c.State != "exited" || c.Owner != "cloudflare" {
		t.Errorf("m4xFindContainer(cf) = %+v, %t", c, ok)
	}
	if _, ok := m4xFindContainer(cs, "absent"); ok {
		t.Error("m4xFindContainer found a container that is not listed")
	}
	if _, err := m4xContainers([]byte(`{"containers":`)); err == nil {
		t.Error("m4xContainers accepted truncated JSON")
	}
}

func TestM4RevealReleaseEffects(t *testing.T) {
	var released atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/hosts/h1/actions/docker.container.reveal-env/v1/reveal":
			_, _ = w.Write([]byte(`{"value":{"env":[{"name":"K","value":"v"}]}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/changes/c1/release":
			var b struct{ Reason string }
			_ = json.NewDecoder(r.Body).Decode(&b)
			released.Store(b.Reason)
			_, _ = w.Write([]byte(`{"change":{"id":"c1","state":"failed"},"abandoned":[]}`))
		case r.URL.Path == "/api/v1/changes/c1/effects":
			_, _ = w.Write([]byte(`{"effects":[{"seq":1,"resource":"docker.container:a1","state":"unresolved","reason":"conflict"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	api, ctx := m4TestAPI(t, srv), context.Background()
	var env struct {
		Env []struct{ Name, Value string } `json:"env"`
	}
	if err := m4Reveal(ctx, api, "h1", "docker.container.reveal-env", map[string]any{"container": "web"}, &env); err != nil || len(env.Env) != 1 || env.Env[0].Value != "v" {
		t.Errorf("m4Reveal = %+v, %v; want the revealed value", env, err)
	}
	if err := m4Release(ctx, api, "c1", "scenario cleanup"); err != nil || released.Load() != "scenario cleanup" {
		t.Errorf("m4Release = %v, reason %v; want the reason sent", err, released.Load())
	}
	effs, err := m4Effects(ctx, api, "c1")
	if err != nil || len(effs) != 1 || effs[0].State != "unresolved" || effs[0].Reason != "conflict" {
		t.Errorf("m4Effects = %+v, %v; want the one unresolved conflict", effs, err)
	}
}
