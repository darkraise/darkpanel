package main

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/controller/server"
	"github.com/darkraise/darkpanel/internal/paths"
)

// fakeServe replaces serveRun and geteuid for one test and records the
// options serve passed.
func fakeServe(t *testing.T, euid int, result error) *[]server.Options {
	t.Helper()
	var got []server.Options
	oldRun, oldEUID := serveRun, geteuid
	t.Cleanup(func() { serveRun, geteuid = oldRun, oldEUID })
	serveRun = func(ctx context.Context, opts server.Options) error {
		if ctx.Err() != nil {
			t.Error("serve passed a context that has already ended")
		}
		got = append(got, opts)
		return result
	}
	geteuid = func() int { return euid }
	return &got
}

func runArgs(args ...string) (string, int) {
	var out bytes.Buffer
	code := run(args, cli.Options{Stdout: &out, Stderr: &out})
	return out.String(), code
}

// TestExitCodes is register row 30: usage errors exit 2, failed runs 1.
func TestExitCodes(t *testing.T) {
	served := fakeServe(t, 0, errors.New("the controller database is locked"))
	tests := []struct {
		args []string
		code int
		out  string
	}{
		{[]string{"version"}, cli.ExitOK, "darkpanel dev"},
		{[]string{"version", "extra"}, cli.ExitUsage, `unknown command "extra"`},
		{[]string{"nope"}, cli.ExitUsage, `unknown command "nope"`},
		{[]string{"serve", "--nope"}, cli.ExitUsage, "unknown flag: --nope"},
		{[]string{"serve", "--dev", "--listen", "http://192.0.2.1:80"}, cli.ExitUsage, "plain HTTP is allowed only on a loopback address"},
		{[]string{"serve", "--dev", "--listen", "ftp://127.0.0.1:21"}, cli.ExitUsage, "is not loopback, tailscale"},
		{[]string{"serve"}, cli.ExitUsage, "serve runs as the darkpanel user in production; start it with systemctl start darkpanel-controller, or add --dev"},
		{[]string{"serve", "--embedded-agent"}, cli.ExitUsage, "--embedded-agent runs the M1 in-process agent and needs --dev"},
		{[]string{"serve", "--dev", "--agent-listen", "0.0.0.0:7443"}, cli.ExitUsage, "the address must be a specific address of this host"},
		{[]string{"serve", "--dev", "--agent-listen", "panel.example:7443"}, cli.ExitUsage, `--agent-listen "panel.example:7443" is not <ip>:<port>`},
		{[]string{"serve", "--dev", "--agent-advertise", "panel.example"}, cli.ExitUsage, `agent address "panel.example" is not host:port`},
		{[]string{"serve", "--dev", "--embedded-agent", "--agent-listen", "192.0.2.7:7443"}, cli.ExitUsage, "--agent-listen does not apply with --embedded-agent, which opens no agent listener"},
		{[]string{"serve", "--dev"}, cli.ExitError, "darkpanel: the controller database is locked"},
		{[]string{"token", "list", "--help"}, cli.ExitOK, "List"},
		{[]string{"hostinfo", "summary", "get", "--help"}, cli.ExitOK, "--host"},
	}
	for _, tt := range tests {
		out, code := runArgs(tt.args...)
		if code != tt.code || !strings.Contains(out, tt.out) {
			t.Errorf("darkpanel %s: exit %d, output %q; want exit %d and %q", strings.Join(tt.args, " "), code, out, tt.code, tt.out)
		}
	}
	if len(*served) != 1 {
		t.Errorf("serve ran %d times, want once (only serve --dev passes its checks)", len(*served))
	}
}

func TestServeDevOptions(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_RUNTIME_DIR", "")
	served := fakeServe(t, 0, nil)
	if out, code := runArgs("serve", "--dev", "--listen", "loopback", "--listen", "https://127.0.0.1:9443"); code != cli.ExitOK {
		t.Fatalf("serve --dev: exit %d: %s", code, out)
	}
	if out, code := runArgs("serve", "--dev", "--embedded-agent"); code != cli.ExitOK {
		t.Fatalf("serve --dev --embedded-agent: exit %d: %s", code, out)
	}
	if len(*served) != 2 {
		t.Fatalf("serve ran %d times, want twice", len(*served))
	}
	opts := (*served)[0]
	var listens []string
	for _, s := range opts.Listen {
		listens = append(listens, s.String())
	}
	if opts.Paths != paths.Under(filepath.Join(state, "darkpanel-dev")) || !opts.Dev || opts.Log == nil ||
		!slices.Equal(listens, []string{"loopback", "https://127.0.0.1:9443"}) || opts.EmbeddedAgent {
		t.Errorf("serve --dev passed %+v, want the dev paths, Dev, a logger, both listen values and no embedded agent", opts)
	}
	if embedded := (*served)[1]; !embedded.EmbeddedAgent || !embedded.Dev {
		t.Errorf("serve --dev --embedded-agent passed %+v, want EmbeddedAgent and Dev", embedded)
	}
}

// TestServeProductionOptions runs serve with the arguments of the
// controller unit's agent listener drop-in, as the darkpanel user.
func TestServeProductionOptions(t *testing.T) {
	served := fakeServe(t, 998, nil)
	args := []string{"serve", "--agent-listen", "192.0.2.7:7443", "--agent-listen", "[2001:db8::7]:7443",
		"--agent-listen", "192.0.2.7:7443", "--agent-advertise", "panel.example:7443"}
	if out, code := runArgs(args...); code != cli.ExitOK {
		t.Fatalf("serve as the darkpanel user: exit %d: %s", code, out)
	}
	opts := (*served)[0]
	var listens []string
	for _, s := range opts.Listen {
		listens = append(listens, s.String())
	}
	wantAgent := []netip.AddrPort{netip.MustParseAddrPort("192.0.2.7:7443"), netip.MustParseAddrPort("[2001:db8::7]:7443")}
	if opts.Paths != paths.Production() || opts.Dev || !slices.Equal(listens, []string{"loopback", "tailscale"}) ||
		!slices.Equal(opts.AgentListen, wantAgent) || opts.AgentAdvertise != "panel.example:7443" ||
		opts.EmbeddedAgent || opts.HealthStatus != "" {
		t.Errorf("serve passed %+v, want the production paths, the default listeners, agent listen %v and the advertised address", opts, wantAgent)
	}
}
