package install

import (
	"context"
	"strings"
	"testing"
)

// acceptanceExecStart returns the drop-in's last non-empty ExecStart line.
func acceptanceExecStart(t *testing.T, h *testHost) string {
	t.Helper()
	var last string
	for _, line := range strings.Split(string(readBytes(t, h.layout.DropInPath())), "\n") {
		if strings.HasPrefix(line, "ExecStart=") && line != "ExecStart=" {
			last = line
		}
	}
	if last == "" {
		t.Fatalf("the drop-in has no ExecStart value:\n%s", readBytes(t, h.layout.DropInPath()))
	}
	return last
}

func acceptanceHasLine(out, want string) bool {
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimRight(line, "\r") == want {
			return true
		}
	}
	return false
}

// TestA1WebListenWritesTheDropInAndPrintsTheListeners covers A1: the drop-in
// keeps the defaults, lists the web value and then the agent flags, and the
// installer prints the listeners.
func TestA1WebListenWritesTheDropInAndPrintsTheListeners(t *testing.T) {
	t.Run("A1: web listener with agent flags", func(t *testing.T) {
		h := newTestHost(t)
		cfg := h.config(RoleController)
		cfg.AgentListen = []string{"10.77.0.10:7443"}
		cfg.AgentAdvertise = "panel.example:7443"
		cfg.WebListen = []string{"http://203.0.113.7:8080"}
		if err := Install(context.Background(), cfg); err != nil {
			t.Fatalf("Install: %v\n%s", err, h.out)
		}
		want := "ExecStart=" + h.layout.Paths.Binary() +
			" serve --listen loopback --listen tailscale --listen http://203.0.113.7:8080" +
			" --agent-listen 10.77.0.10:7443 --agent-advertise panel.example:7443"
		if got := acceptanceExecStart(t, h); got != want {
			t.Errorf("ExecStart = %q, want %q", got, want)
		}
		if out := h.out.String(); !acceptanceHasLine(out, "web listeners: loopback, tailscale, http://203.0.113.7:8080") {
			t.Errorf("output lacks the line %q:\n%s", "web listeners: loopback, tailscale, http://203.0.113.7:8080", out)
		}
	})
	t.Run("A1: web listener alone", func(t *testing.T) {
		h := newTestHost(t)
		cfg := h.config(RoleController)
		cfg.WebListen = []string{"http://203.0.113.7:8080"}
		if err := Install(context.Background(), cfg); err != nil {
			t.Fatalf("Install: %v\n%s", err, h.out)
		}
		want := "ExecStart=" + h.layout.Paths.Binary() + " serve --listen loopback --listen tailscale --listen http://203.0.113.7:8080"
		if got := acceptanceExecStart(t, h); got != want {
			t.Errorf("ExecStart = %q, want %q", got, want)
		}
	})
}

// TestA2WithoutWebListenTheDropInIsUnchanged covers A2: no --web-listen leaves
// the ExecStart line as it was before the option, and a rerun without it
// removes a listener an earlier run wrote.
func TestA2WithoutWebListenTheDropInIsUnchanged(t *testing.T) {
	t.Run("A2: agent flags only", func(t *testing.T) {
		h := newTestHost(t)
		cfg := h.config(RoleController)
		cfg.AgentListen = []string{"10.77.0.10:7443"}
		cfg.AgentAdvertise = "panel.example:7443"
		if err := Install(context.Background(), cfg); err != nil {
			t.Fatalf("Install: %v\n%s", err, h.out)
		}
		want := "ExecStart=" + h.layout.Paths.Binary() + " serve --agent-listen 10.77.0.10:7443 --agent-advertise panel.example:7443"
		if got := acceptanceExecStart(t, h); got != want {
			t.Errorf("ExecStart = %q, want %q", got, want)
		}
		if out := h.out.String(); !acceptanceHasLine(out, "web listeners: loopback, tailscale") {
			t.Errorf("output lacks the line %q:\n%s", "web listeners: loopback, tailscale", out)
		}
	})
	t.Run("A2: no flags at all", func(t *testing.T) {
		h := newTestHost(t)
		if err := Install(context.Background(), h.config(RoleController)); err != nil {
			t.Fatalf("Install: %v\n%s", err, h.out)
		}
		if got, want := acceptanceExecStart(t, h), "ExecStart="+h.layout.Paths.Binary()+" serve"; got != want {
			t.Errorf("ExecStart = %q, want %q", got, want)
		}
	})
	t.Run("A2: a rerun without the flag removes the listener", func(t *testing.T) {
		h := newTestHost(t)
		cfg := h.config(RoleController)
		cfg.WebListen = []string{"http://203.0.113.7:8080"}
		if err := Install(context.Background(), cfg); err != nil {
			t.Fatalf("Install: %v\n%s", err, h.out)
		}
		h.out.Reset()
		cfg.WebListen = nil
		if err := Install(context.Background(), cfg); err != nil {
			t.Fatalf("second Install: %v\n%s", err, h.out)
		}
		if got, want := acceptanceExecStart(t, h), "ExecStart="+h.layout.Paths.Binary()+" serve"; got != want {
			t.Errorf("ExecStart = %q, want %q", got, want)
		}
		if out := h.out.String(); !acceptanceHasLine(out, "web listeners: loopback, tailscale") {
			t.Errorf("output lacks the line %q:\n%s", "web listeners: loopback, tailscale", out)
		}
	})
}
