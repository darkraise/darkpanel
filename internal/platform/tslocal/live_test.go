package tslocal

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"testing"
	"time"
)

// TestTailscaledLive drives a real tailscaled, so it runs only when
// DARKPANEL_TAILSCALED_SOCKET names its socket. It needs no tailnet: it
// reads a logged-out node's status, preferences and DERP map, patches one
// preference and restores it, replaces the preferences through Start and
// restores them, logs out, and looks up an address no node holds.
func TestTailscaledLive(t *testing.T) {
	socket := os.Getenv("DARKPANEL_TAILSCALED_SOCKET")
	if socket == "" {
		t.Skip("set DARKPANEL_TAILSCALED_SOCKET to drive a real tailscaled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c := New(socket)
	defer c.Close()

	var s *Status
	var err error
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		s, err = c.Status(ctx)
		if err == nil && s.BackendState != "NoState" || time.Now().After(deadline) {
			break
		}
	}
	if err != nil || s.Version == "" || s.BackendState == "" {
		t.Fatalf("status = %+v, %v", s, err)
	}

	before, err := c.Prefs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.raw) <= 12 {
		t.Fatalf("prefs kept %d fields; tailscaled sends more than the 12 modelled", len(before.raw))
	}
	flipped := !before.ShieldsUp
	after, err := c.EditPrefs(ctx, PrefsPatch{ShieldsUp: &flipped})
	if err != nil {
		t.Fatal(err)
	}
	if after.ShieldsUp != flipped || after.CorpDNS != before.CorpDNS || after.ControlURL != before.ControlURL {
		t.Fatalf("patched prefs = %+v, before %+v", after, before)
	}
	restore := before.ShieldsUp
	if _, err := c.EditPrefs(ctx, PrefsPatch{ShieldsUp: &restore}); err != nil {
		t.Fatal(err)
	}
	if restored, err := c.Prefs(ctx); err != nil || restored.ShieldsUp != before.ShieldsUp {
		t.Fatalf("restored prefs = %+v, %v", restored, err)
	}

	// Start with UpdatePrefs replaces every preference: the changed host
	// name must arrive, and NetfilterMode, which this package does not model
	// and tailscaled sets non-zero on Linux, must come back unchanged.
	cur, err := c.Prefs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mode := string(cur.raw["NetfilterMode"])
	if mode == "" || mode == "0" {
		t.Fatalf("NetfilterMode = %q, want a non-zero value to carry through Start", mode)
	}
	renamed := *cur
	renamed.Hostname = "dp-live-check"
	if err := c.Start(ctx, StartOptions{UpdatePrefs: &renamed}); err != nil {
		t.Fatal(err)
	}
	got, err := c.Prefs(ctx)
	if err != nil || got.Hostname != "dp-live-check" || string(got.raw["NetfilterMode"]) != mode || got.WantRunning != cur.WantRunning {
		t.Fatalf("prefs after Start = %+v (NetfilterMode %s), %v", got, got.raw["NetfilterMode"], err)
	}
	if err := c.Start(ctx, StartOptions{UpdatePrefs: cur}); err != nil {
		t.Fatal(err)
	}
	if err := c.Logout(ctx); err != nil {
		t.Fatalf("logout of a logged-out node = %v", err)
	}

	if _, err := c.DERPMap(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.WhoIs(ctx, netip.MustParseAddr("192.0.2.1")); !errors.Is(err, ErrPeerNotFound) {
		t.Fatalf("whois of an unheld address err = %v, want ErrPeerNotFound", err)
	}
}
