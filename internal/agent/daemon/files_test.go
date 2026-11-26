package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/console"
	"github.com/darkraise/darkpanel/internal/platform"
)

// TestReopenDoesNotWaitForTransfers starts an upload through the session's
// guard and holds its body: a read still answers, and replacing the runtime
// ends the upload without waiting for its bytes.
func TestReopenDoesNotWaitForTransfers(t *testing.T) {
	h := newAgentHost(t)
	cfg := h.config()
	reg, err := core.NewRegistry(probeModule{}, console.Module())
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cfg.Runtime.Registry = reg
	cfg.Runtime.ConsoleFS = platform.NewConsoleFiles(platform.ConsoleFilesConfig{Root: root})
	d := &daemon{cfg: cfg, log: cfg.Runtime.Log, clock: cfg.Runtime.Clock}
	ctx := context.Background()
	id := identity{hostID: "01HOST", enrolled: true}
	if err := d.open(ctx, id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.rt.Close() })
	if err := d.rt.SetModuleEnabled(ctx, console.ID, true); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(d.guard(api.NewHandler(api.Config{Agent: d.rt, Log: d.log})))
	t.Cleanup(srv.Close)

	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/agent/v1/files/upload?"+url.Values{"path": {"/up"}}.Encode(), pr)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = 1 << 20
	req.Header.Set("Content-Type", "application/octet-stream")
	p, err := json.Marshal(wire.FromPrincipal(core.Principal{Actor: "user:u1", Scope: core.ScopeAdmin, Source: "192.0.2.7"}))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(wire.HeaderPrincipal, string(p))
	answered := make(chan int, 1)
	go func() {
		resp, err := srv.Client().Do(req)
		if err != nil {
			answered <- 0
			return
		}
		_ = resp.Body.Close()
		answered <- resp.StatusCode
	}()
	go func() { _, _ = pw.Write(make([]byte, 64<<10)) }()
	deadline := time.Now().Add(5 * time.Second)
	for !hasTemp(t, root) {
		if time.Now().After(deadline) {
			t.Fatal("the upload wrote no temporary file")
		}
		time.Sleep(10 * time.Millisecond)
	}

	c := api.NewClient(api.ClientConfig{Dial: func(ctx context.Context) (net.Conn, error) {
		var dl net.Dialer
		return dl.DialContext(ctx, "tcp", srv.Listener.Addr().String())
	}})
	hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := c.Read(hctx, core.Principal{Actor: "user:u1", Scope: core.ScopeRead}, refProbe, json.RawMessage(`{"name":"a"}`)); err != nil {
		t.Fatalf("a read during an upload: %v", err)
	}
	reopened := make(chan error, 1)
	go func() { reopened <- d.reopen(ctx, id) }()
	select {
	case err := <-reopened:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("replacing the runtime waited for the upload")
	}
	deadline = time.Now().Add(5 * time.Second)
	for hasTemp(t, root) {
		if time.Now().After(deadline) {
			t.Fatal("the upload did not end with its runtime")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The client still sends its body; let it end.
	_ = pw.Close()
	select {
	case status := <-answered:
		if status == http.StatusOK {
			t.Error("the upload the replacement ended answered 200")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the upload's request did not end")
	}
}

func hasTemp(t *testing.T, dir string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), platform.TempPrefix) {
			return true
		}
	}
	return false
}
