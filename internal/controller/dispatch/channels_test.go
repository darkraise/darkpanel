package dispatch

import (
	"context"
	"io"
	"net"
	"net/url"
	"reflect"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

type dchParams struct {
	Cols int `json:"cols"`
}

// dchModule has one channel, dch.term.
type dchModule struct{}

func (dchModule) Manifest() core.Manifest {
	return core.Manifest{ID: "dch", Version: 1, DefaultEnabled: true}
}

func (dchModule) Channels() []core.ChannelDef {
	return []core.ChannelDef{core.DefineChannel("term", func(context.Context, core.Env, dchParams, core.ChannelConn) error {
		return nil
	})}
}

type dchOpen struct {
	p            core.Principal
	module, name string
	params       url.Values
}

// dchAgent is an agent client that opens channels.
type dchAgent struct {
	coretest.FakeAgent
	opens []dchOpen
}

func (a *dchAgent) OpenChannel(_ context.Context, p core.Principal, module, name string, params url.Values) (io.ReadWriteCloser, error) {
	a.opens = append(a.opens, dchOpen{p, module, name, params})
	c, _ := net.Pipe()
	return c, nil
}

func dchDispatcher(t *testing.T, client func(h *harness) core.AgentClient) (*harness, *Dispatcher) {
	t.Helper()
	h := newHarnessWith(t, dchModule{})
	h.clients.Add(testHost, client(h))
	d, err := New(h.config(&coretest.MemAudit{}))
	if err != nil {
		t.Fatal(err)
	}
	return h, d
}

func TestOpenChannel(t *testing.T) {
	ctx := context.Background()
	var a *dchAgent
	h, d := dchDispatcher(t, func(h *harness) core.AgentClient {
		a = &dchAgent{}
		a.HelloFunc = h.rt.Hello
		return a
	})
	params := url.Values{"cols": {"80"}}
	c, err := d.OpenChannel(ctx, alice, testHost, "dch", "term", params)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	if want := []dchOpen{{alice, "dch", "term", params}}; !reflect.DeepEqual(a.opens, want) {
		t.Errorf("the agent opened %+v, want %+v", a.opens, want)
	}
	_, err = d.OpenChannel(ctx, alice, testHost, "dch", "nope", nil)
	_ = wantCode(t, err, errcode.NotFound)

	if err := d.ModuleUsable(ctx, testHost, "dch"); err != nil {
		t.Errorf("ModuleUsable of an enabled module: %v", err)
	}
	if _, err := h.db.Writer().Exec("UPDATE core_module_state SET enabled = 0 WHERE module_id = 'dch'"); err != nil {
		t.Fatal(err)
	}
	_ = wantCode(t, d.ModuleUsable(ctx, testHost, "dch"), errcode.ModuleUnavailable)
	_, err = d.OpenChannel(ctx, alice, testHost, "dch", "term", nil)
	if e := wantCode(t, err, errcode.ModuleUnavailable); e.Detail != "module dch is unavailable on host h1: disabled" {
		t.Errorf("disabled: detail %q", e.Detail)
	}
	if len(a.opens) != 1 {
		t.Errorf("a refused channel reached the agent: %+v", a.opens)
	}
}

func TestOpenChannelOldAgent(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		client func(h *harness) core.AgentClient
	}{
		{"hello without the module", func(h *harness) core.AgentClient {
			a := &dchAgent{}
			a.HelloFunc = func(ctx context.Context) (core.AgentHello, error) {
				hello, err := h.rt.Hello(ctx)
				hello.Modules = slices.DeleteFunc(hello.Modules, func(m core.ModuleStatus) bool { return m.ID == "dch" })
				return hello, err
			}
			return a
		}},
		{"a client without channels", func(h *harness) core.AgentClient {
			a := &coretest.FakeAgent{}
			a.HelloFunc = h.rt.Hello
			return a
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, d := dchDispatcher(t, tc.client)
			_, err := d.OpenChannel(ctx, alice, testHost, "dch", "term", nil)
			if e := wantCode(t, err, errcode.ModuleUnavailable); e.Detail != ReasonChannelUnserved {
				t.Errorf("detail %q, want %q", e.Detail, ReasonChannelUnserved)
			}
		})
	}
}
