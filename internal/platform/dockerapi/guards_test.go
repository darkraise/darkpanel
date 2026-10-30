package dockerapi

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEgEmptyIDRefusedBeforeAnyRequest(t *testing.T) {
	var requests atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	c := newFakeDaemon(t, "", mux)
	ctx := t.Context()

	cases := []struct {
		name string
		what string
		call func() error
	}{
		{"InspectContainer", "container", func() error { _, err := c.InspectContainer(ctx, ""); return err }},
		{"StartContainer", "container", func() error { _, err := c.StartContainer(ctx, ""); return err }},
		{"StopContainer", "container", func() error { _, err := c.StopContainer(ctx, "", time.Second); return err }},
		{"RestartContainer", "container", func() error { return c.RestartContainer(ctx, "", time.Second) }},
		{"RemoveContainer", "container", func() error { return c.RemoveContainer(ctx, "", RemoveContainerOptions{}) }},
		{"InspectImage", "image", func() error { _, err := c.InspectImage(ctx, ""); return err }},
		{"PullImage", "image", func() error { _, err := c.PullImage(ctx, ""); return err }},
		{"RemoveImage", "image", func() error { _, err := c.RemoveImage(ctx, "", RemoveImageOptions{}); return err }},
		{"InspectNetwork", "network", func() error { _, err := c.InspectNetwork(ctx, ""); return err }},
		{"RemoveNetwork", "network", func() error { return c.RemoveNetwork(ctx, "") }},
		{"ConnectNetwork without network", "network", func() error { return c.ConnectNetwork(ctx, "", "web", nil) }},
		{"ConnectNetwork without container", "container", func() error { return c.ConnectNetwork(ctx, "site", "", nil) }},
		{"DisconnectNetwork without network", "network", func() error { return c.DisconnectNetwork(ctx, "", "web", false) }},
		{"DisconnectNetwork without container", "container", func() error { return c.DisconnectNetwork(ctx, "site", "", false) }},
		{"InspectVolume", "volume", func() error { _, err := c.InspectVolume(ctx, ""); return err }},
		{"RemoveVolume", "volume", func() error { return c.RemoveVolume(ctx, "", false) }},
		{"Logs", "container", func() error { _, err := c.Logs(ctx, "", LogsOptions{Stdout: true}); return err }},
		{"Stats", "container", func() error { _, err := c.Stats(ctx, "", false); return err }},
		{"CopyFromContainer", "container", func() error { _, err := c.CopyFromContainer(ctx, "", "/etc"); return err }},
		{"CopyToContainer", "container", func() error { return c.CopyToContainer(ctx, "", "/etc", strings.NewReader("")) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if !errors.Is(err, ErrEmptyID) {
				t.Fatalf("err = %v, want one wrapping ErrEmptyID", err)
			}
			if !strings.Contains(err.Error(), tc.what) {
				t.Errorf("err = %q does not name the %s argument", err, tc.what)
			}
		})
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("the daemon received %d requests, want none", n)
	}
}

func TestEgAPIVersionCancelledWaiterError(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "unused.sock"))
	t.Cleanup(c.Close)
	c.negotiating <- struct{}{} // another caller's negotiation holds the only slot
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := c.APIVersion(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want one wrapping context.Canceled", err)
	}
	if !strings.HasPrefix(err.Error(), "dockerapi: negotiate API version: ") {
		t.Errorf("err = %q, want the dockerapi: negotiate API version: prefix", err)
	}
}
