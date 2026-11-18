package main

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/install"
)

// acceptanceNothingWritten fails when the unit directory of l holds any file.
func acceptanceNothingWritten(t *testing.T, id string, l install.Layout) {
	t.Helper()
	if _, err := os.Stat(l.DropInPath()); err == nil {
		t.Errorf("%s: the drop-in was written", id)
	}
	if entries, err := os.ReadDir(l.UnitDir); err == nil && len(entries) != 0 {
		t.Errorf("%s: unit files were written: %v", id, entries)
	}
}

func acceptanceListens(t *testing.T, args ...string) []string {
	t.Helper()
	served := fakeServe(t, 0, nil)
	if out, code := runArgs(args...); code != cli.ExitOK {
		t.Fatalf("darkpanel %s: exit %d: %s", strings.Join(args, " "), code, out)
	}
	if len(*served) != 1 {
		t.Fatalf("serve ran %d times, want once", len(*served))
	}
	var got []string
	for _, s := range (*served)[0].Listen {
		got = append(got, s.String())
	}
	return got
}

// TestA1SystemInstallPassesTheWebListener covers the command-line half of A1:
// the value reaches the installer's configuration beside the agent flags.
func TestA1SystemInstallPassesTheWebListener(t *testing.T) {
	got, _ := fakeInstall(t, 0, false)
	out, code := runArgs("system", "install", "--role", "controller", "--web-listen", "http://203.0.113.7:8080",
		"--agent-listen", "192.0.2.7:7443", "--agent-advertise", "panel.example:7443")
	if code != cli.ExitOK {
		t.Fatalf("A1: exit %d: %s", code, out)
	}
	if len(*got) != 1 {
		t.Fatalf("A1: the installer ran %d times, want once", len(*got))
	}
	cfg := (*got)[0]
	if !slices.Equal(cfg.WebListen, []string{"http://203.0.113.7:8080"}) ||
		!slices.Equal(cfg.AgentListen, []string{"192.0.2.7:7443"}) || cfg.AgentAdvertise != "panel.example:7443" {
		t.Errorf("A1: Config web %q, agent %q, advertise %q", cfg.WebListen, cfg.AgentListen, cfg.AgentAdvertise)
	}
}

// TestA2SystemInstallWithoutWebListen covers the command-line half of A2.
func TestA2SystemInstallWithoutWebListen(t *testing.T) {
	got, _ := fakeInstall(t, 0, false)
	if out, code := runArgs("system", "install", "--role", "controller", "--agent-listen", "192.0.2.7:7443"); code != cli.ExitOK {
		t.Fatalf("A2: exit %d: %s", code, out)
	}
	if len(*got) != 1 || len((*got)[0].WebListen) != 0 {
		t.Errorf("A2: configs %+v, want one without web listeners", *got)
	}
}

// TestA3ServeAcceptsAPlainHTTPListenerOnAHostAddress covers the parsing half
// of A3.
func TestA3ServeAcceptsAPlainHTTPListenerOnAHostAddress(t *testing.T) {
	t.Run("A3: non-loopback address alone", func(t *testing.T) {
		if got := acceptanceListens(t, "serve", "--dev", "--listen", "http://203.0.113.7:8080"); !slices.Equal(got, []string{"http://203.0.113.7:8080"}) {
			t.Errorf("A3: serve listens on %v", got)
		}
	})
	t.Run("A3: with loopback listed", func(t *testing.T) {
		got := acceptanceListens(t, "serve", "--dev", "--listen", "loopback", "--listen", "http://203.0.113.7:8080")
		if !slices.Equal(got, []string{"loopback", "http://203.0.113.7:8080"}) {
			t.Errorf("A3: serve listens on %v", got)
		}
	})
}

// TestA4RefusalsBeforeWritingAnything covers A4: serve and system install
// refuse unspecified addresses, and system install refuses the values the
// drop-in lists itself and the one that clashes with loopback.
func TestA4RefusalsBeforeWritingAnything(t *testing.T) {
	for _, v := range []string{"http://0.0.0.0:8080", "http://[::]:8080"} {
		t.Run("A4: serve "+v, func(t *testing.T) {
			served := fakeServe(t, 0, nil)
			out, code := runArgs("serve", "--dev", "--listen", v)
			if code != cli.ExitUsage || !strings.Contains(out, "the address must be a specific address of this host") {
				t.Errorf("A4: exit %d, output %q; want exit 2 and the specific-address refusal", code, out)
			}
			if len(*served) != 0 {
				t.Error("A4: serve started")
			}
		})
	}
	for _, v := range []string{"http://0.0.0.0:8080", "http://[::]:8080", "loopback", "tailscale", "https://127.0.0.1:8080"} {
		t.Run("A4: system install "+v, func(t *testing.T) {
			got, s := fakeInstall(t, 0, false)
			out, code := runArgs("system", "install", "--role", "controller", "--web-listen", v)
			if code != cli.ExitUsage {
				t.Errorf("A4: exit %d, output %q; want exit 2", code, out)
			}
			if len(*got) != 0 {
				t.Error("A4: the installer ran")
			}
			acceptanceNothingWritten(t, "A4", s.layout)
			if code == cli.ExitUsage && !strings.Contains(out, "--web-listen") && !strings.Contains(out, "specific address") && !strings.Contains(out, "HTTP and HTTPS") {
				t.Errorf("A4: the refusal %q names neither the flag nor the reason", out)
			}
		})
	}
	t.Run("A4: the specific-address refusal reaches system install", func(t *testing.T) {
		fakeInstall(t, 0, false)
		out, _ := runArgs("system", "install", "--role", "controller", "--web-listen", "http://0.0.0.0:8080")
		if !strings.Contains(out, "the address must be a specific address of this host") {
			t.Errorf("A4: output %q lacks the specific-address refusal", out)
		}
	})
	t.Run("A4: a root-less caller gets the value refusal first", func(t *testing.T) {
		got, _ := fakeInstall(t, 1000, false)
		out, code := runArgs("system", "install", "--role", "controller", "--web-listen", "tailscale")
		if code != cli.ExitUsage || strings.Contains(out, "runs as root") || len(*got) != 0 {
			t.Errorf("A4: exit %d, output %q; want the value refused before the root check", code, out)
		}
	})
}

// TestA5SystemInstallRefusesWebListenForAnAgent covers the system install half
// of A5.
func TestA5SystemInstallRefusesWebListenForAnAgent(t *testing.T) {
	got, _ := fakeInstall(t, 0, false)
	out, code := runArgs("system", "install", "--role", "agent", "--web-listen", "http://203.0.113.7:8080")
	if code != cli.ExitUsage || !strings.Contains(out, "--web-listen") {
		t.Errorf("A5: exit %d, output %q; want exit 2 naming --web-listen", code, out)
	}
	if len(*got) != 0 {
		t.Error("A5: the installer ran for the agent role")
	}
}

// TestA17SystemInstallRefusesAnAddressNoInterfaceHolds covers A17.
func TestA17SystemInstallRefusesAnAddressNoInterfaceHolds(t *testing.T) {
	for _, euid := range []int{0, 1000} {
		got, s := fakeInstall(t, euid, false)
		out, code := runArgs("system", "install", "--role", "controller", "--web-listen", "http://198.51.100.9:8080")
		if code != cli.ExitUsage || !strings.Contains(out, "198.51.100.9") {
			t.Errorf("A17 (euid %d): exit %d, output %q; want exit 2 naming 198.51.100.9", euid, code, out)
		}
		if len(*got) != 0 {
			t.Errorf("A17 (euid %d): the installer ran", euid)
		}
		acceptanceNothingWritten(t, "A17", s.layout)
	}
}
