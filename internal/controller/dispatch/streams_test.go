package dispatch

import (
	"context"
	"errors"
	"io"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/schemas"
)

type dstParams struct {
	Tail int `json:"tail,omitempty"`
}

type dstEvent struct {
	Line string `json:"line"`
}

// dstModule has one stream, dst.logs; the dispatcher never runs it.
type dstModule struct{}

func (dstModule) Manifest() core.Manifest {
	return core.Manifest{ID: "dst", Version: 1, DefaultEnabled: true}
}

func (dstModule) Streams() []core.StreamDef {
	return []core.StreamDef{core.DefineStream("logs", func(context.Context, core.Env, dstParams, func(dstEvent) error) error {
		return nil
	})}
}

// dstBody is an agent stream's body that records its closing.
type dstBody struct {
	io.Reader
	closed atomic.Bool
}

func (b *dstBody) Close() error {
	b.closed.Store(true)
	return nil
}

// dstOpen is one OpenStream the fake agent received.
type dstOpen struct {
	p            core.Principal
	module, name string
	params       url.Values
}

// dstAgent is h1's agent: the harness's runtime answers Hello, Streams
// answers served, and OpenStream records each call and fails while fail
// is set.
type dstAgent struct {
	coretest.FakeAgent
	served     []core.StreamInfo
	discovered int // Streams calls
	opens      []dstOpen
	fail       bool
}

// dstDispatcher is a dispatcher over a harness with dstModule whose
// host h1 is served by a dstAgent listing dst.logs with its true hash.
func dstDispatcher(t *testing.T) (*harness, *Dispatcher, *dstAgent) {
	t.Helper()
	h := newHarnessWith(t, dstModule{})
	def, _ := h.reg.Stream("dst", "logs")
	hash, err := schemas.StreamHash(def)
	if err != nil {
		t.Fatal(err)
	}
	a := &dstAgent{served: []core.StreamInfo{{Module: "dst", Name: "logs", SchemaHash: hash}}}
	a.HelloFunc = h.rt.Hello
	a.StreamsFunc = func(context.Context) ([]core.StreamInfo, error) {
		a.discovered++
		return a.served, nil
	}
	a.OpenStreamFunc = func(_ context.Context, p core.Principal, module, name string, params url.Values) (io.ReadCloser, error) {
		if a.fail {
			return nil, errcode.New(errcode.HostOffline, "host h1 is offline")
		}
		a.opens = append(a.opens, dstOpen{p, module, name, params})
		return &dstBody{Reader: strings.NewReader(`{"data":{"line":"a"}}` + "\n")}, nil
	}
	h.clients.Add(testHost, a)
	d, err := New(h.config(&coretest.MemAudit{}))
	if err != nil {
		t.Fatal(err)
	}
	return h, d, a
}

func TestOpenStreamRelaysAgentStream(t *testing.T) {
	_, d, a := dstDispatcher(t)
	params := url.Values{"tail": {"10"}}
	body, err := d.OpenStream(context.Background(), alice, testHost, "dst", "logs", params)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := io.ReadAll(body); err != nil || string(got) != `{"data":{"line":"a"}}`+"\n" {
		t.Errorf("body %q, %v", got, err)
	}
	if want := []dstOpen{{alice, "dst", "logs", params}}; !reflect.DeepEqual(a.opens, want) {
		t.Errorf("the agent opened %+v, want %+v", a.opens, want)
	}
}

func TestOpenStreamHash(t *testing.T) {
	tests := []struct {
		name   string
		served func(own core.StreamInfo) []core.StreamInfo
	}{
		{"another hash", func(s core.StreamInfo) []core.StreamInfo {
			s.SchemaHash = strings.Repeat("0", 64)
			return []core.StreamInfo{s}
		}},
		{"stream not listed", func(core.StreamInfo) []core.StreamInfo {
			return []core.StreamInfo{{Module: "dst", Name: "stats", SchemaHash: "ab"}}
		}},
		{"no streams", func(core.StreamInfo) []core.StreamInfo { return []core.StreamInfo{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, d, a := dstDispatcher(t)
			a.served = tt.served(a.served[0])
			_, err := d.OpenStream(context.Background(), alice, testHost, "dst", "logs", nil)
			e := wantCode(t, err, errcode.ModuleUnavailable)
			if e.Detail != "stream dst.logs on host h1: agent version differs" {
				t.Errorf("detail %q", e.Detail)
			}
			if len(a.opens) != 0 {
				t.Errorf("the agent opened %+v", a.opens)
			}
			if n := d.streams.byPrincipal[alice.Actor] + d.streams.byHost[testHost]; n != 0 {
				t.Errorf("a refused stream holds %d slots", n)
			}
		})
	}
}

func TestOpenStreamLimits(t *testing.T) {
	_, d, a := dstDispatcher(t)
	ctx := context.Background()
	open := func(actor string) (io.ReadCloser, error) {
		return d.OpenStream(ctx, core.Principal{Actor: actor, Scope: core.ScopeRead}, testHost, "dst", "logs", nil)
	}
	held := map[string][]io.ReadCloser{}
	for _, actor := range []string{"user:a", "user:b", "user:c", "user:d"} {
		for range MaxStreamsPerPrincipal {
			body, err := open(actor)
			if err != nil {
				t.Fatalf("%s: %v", actor, err)
			}
			held[actor] = append(held[actor], body)
		}
	}
	discovered := a.discovered
	_, err := open("user:a")
	if e := wantCode(t, err, errcode.StreamLimit); !strings.Contains(e.Detail, "user:a has 4 streams open") {
		t.Errorf("a principal's fifth stream: detail %q", e.Detail)
	}
	_, err = open("user:e")
	if e := wantCode(t, err, errcode.StreamLimit); !strings.Contains(e.Detail, "host h1 has 16 streams open") {
		t.Errorf("the host's seventeenth stream: detail %q", e.Detail)
	}
	if a.discovered != discovered {
		t.Errorf("refusals at the limit asked the agent's streams %d times, want none", a.discovered-discovered)
	}
	if err := held["user:a"][0].Close(); err != nil {
		t.Fatal(err)
	}
	if body, ok := held["user:a"][0].(*heldStream).ReadCloser.(*dstBody); !ok || !body.closed.Load() {
		t.Error("closing the reader did not close the agent's stream")
	}
	if _, err := open("user:a"); err != nil {
		t.Fatalf("after one of its streams closed: %v", err)
	}
	if err := held["user:b"][0].Close(); err != nil {
		t.Fatal(err)
	}
	a.fail = true
	_, err = open("user:e")
	_ = wantCode(t, err, errcode.HostOffline)
	a.fail = false
	if _, err := open("user:e"); err != nil {
		t.Fatalf("after a failed open: %v; it must hold no slot", err)
	}
}

func TestOpenStreamRefusals(t *testing.T) {
	ctx := context.Background()
	h, d, _ := dstDispatcher(t)
	_, err := d.OpenStream(ctx, alice, testHost, "dst", "nope", nil)
	_ = wantCode(t, err, errcode.NotFound)
	_, err = d.OpenStream(ctx, alice, "h9", "dst", "logs", nil)
	_ = wantCode(t, err, errcode.NotFound)
	if err := h.hosts.Hello(ctx, testHost); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Writer().Exec("UPDATE core_module_state SET enabled = 0 WHERE module_id = 'dst'"); err != nil {
		t.Fatal(err)
	}
	_, err = d.OpenStream(ctx, alice, testHost, "dst", "logs", nil)
	if e := wantCode(t, err, errcode.ModuleUnavailable); e.Detail != "module dst is unavailable on host h1: disabled" {
		t.Errorf("disabled: detail %q", e.Detail)
	}

	h, _, a := dstDispatcher(t)
	a.HelloFunc = func(ctx context.Context) (core.AgentHello, error) {
		hello, err := h.rt.Hello(ctx)
		hello.BinaryVersion = "1.1.0"
		return hello, err
	}
	cfg := h.config(&coretest.MemAudit{})
	cfg.Version = "1.3.0"
	old, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = old.OpenStream(ctx, alice, testHost, "dst", "logs", nil)
	_ = wantCode(t, err, errcode.HostVersionUnsupported)
	if len(a.opens) != 0 {
		t.Errorf("a refused stream reached the agent: %+v", a.opens)
	}
	_ = errors.New
}
