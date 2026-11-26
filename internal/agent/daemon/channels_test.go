package daemon

import (
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
)

type dlkChParams struct{}

// dlkChModule's channel runs until its context ends.
type dlkChModule struct{}

func (dlkChModule) Manifest() core.Manifest {
	return core.Manifest{ID: "dlkch", Version: 1, DefaultEnabled: true}
}

func (dlkChModule) Channels() []core.ChannelDef {
	return []core.ChannelDef{core.DefineChannel("hold", func(ctx context.Context, _ core.Env, _ dlkChParams, _ core.ChannelConn) error {
		<-ctx.Done()
		return nil
	})}
}

// TestReopenDoesNotWaitForChannels opens a channel through the session's
// guard: a read still answers while it is open, and replacing the runtime
// closes the channel with "agent restarted" without waiting for it.
func TestReopenDoesNotWaitForChannels(t *testing.T) {
	h := newAgentHost(t)
	cfg := h.config()
	reg, err := core.NewRegistry(probeModule{}, dlkChModule{})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Runtime.Registry = reg
	d := &daemon{cfg: cfg, log: cfg.Runtime.Log, clock: cfg.Runtime.Clock}
	ctx := context.Background()
	id := identity{hostID: "01HOST", enrolled: true}
	if err := d.open(ctx, id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.rt.Close() })
	srv := httptest.NewServer(d.guard(api.NewHandler(api.Config{Agent: d.rt, Log: d.log})))
	t.Cleanup(srv.Close)
	c := api.NewClient(api.ClientConfig{Dial: func(ctx context.Context) (net.Conn, error) {
		var dl net.Dialer
		return dl.DialContext(ctx, "tcp", srv.Listener.Addr().String())
	}})
	rw, err := c.OpenChannel(ctx, core.Principal{Actor: "user:u1", Scope: core.ScopeAdmin}, "dlkch", "hold", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rw.Close() })
	hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := c.Read(hctx, core.Principal{Actor: "user:u1", Scope: core.ScopeRead}, refProbe, json.RawMessage(`{"name":"a"}`)); err != nil {
		t.Fatalf("a read while a channel is open: %v", err)
	}
	reopened := make(chan error, 1)
	go func() { reopened <- d.reopen(ctx, id) }()
	select {
	case err := <-reopened:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("replacing the runtime waited for the open channel")
	}
	typ, p, err := wire.ReadFrame(rw)
	var closed struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(p, &closed)
	if err != nil || typ != wire.FrameClose || closed.Reason != "agent restarted" {
		t.Errorf("after the replacement: type %d %s, %v; want the close frame with reason agent restarted", typ, p, err)
	}
}
