package daemon

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/core"
)

type dlkParams struct{}

type dlkEvent struct {
	N int `json:"n"`
}

// dlkModule's stream emits one event, then runs until its context ends.
type dlkModule struct{}

func (dlkModule) Manifest() core.Manifest {
	return core.Manifest{ID: "dlk", Version: 1, DefaultEnabled: true}
}

func (dlkModule) Streams() []core.StreamDef {
	return []core.StreamDef{core.DefineStream("logs", func(ctx context.Context, _ core.Env, _ dlkParams, emit func(dlkEvent) error) error {
		if err := emit(dlkEvent{N: 1}); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	})}
}

// TestReopenDoesNotWaitForStreams opens a module stream through the
// session's guard and replaces the runtime under it: the replacement does
// not wait for the stream, and the stream ends with unavailable.
func TestReopenDoesNotWaitForStreams(t *testing.T) {
	h := newAgentHost(t)
	cfg := h.config()
	reg, err := core.NewRegistry(probeModule{}, dlkModule{})
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
	body, err := c.OpenStream(ctx, core.Principal{Actor: "user:u1", Scope: core.ScopeRead}, "dlk", "logs", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = body.Close() })
	br := bufio.NewReader(body)
	if line, err := br.ReadString('\n'); err != nil || line != `{"data":{"n":1}}`+"\n" {
		t.Fatalf("first line %q, %v", line, err)
	}
	reopened := make(chan error, 1)
	go func() { reopened <- d.reopen(ctx, id) }()
	select {
	case err := <-reopened:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("replacing the runtime waited for the open stream")
	}
	rest, err := io.ReadAll(br)
	if want := `{"end":{"reason":"error","error":{"code":"unavailable","detail":"Unavailable"}}}` + "\n"; err != nil || string(rest) != want {
		t.Errorf("after the replacement: %q, %v; want %q", rest, err, want)
	}
}
