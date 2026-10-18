package main

import (
	"bytes"
	"context"
	"errors"
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
	served := fakeServe(t, 1000, errors.New("the controller database is locked"))
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
		{[]string{"serve"}, cli.ExitUsage, "serve runs as root in production mode; run it with sudo, or add --dev"},
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
	served := fakeServe(t, 1000, nil)
	if out, code := runArgs("serve", "--dev", "--listen", "loopback", "--listen", "https://127.0.0.1:9443"); code != cli.ExitOK {
		t.Fatalf("serve --dev: exit %d: %s", code, out)
	}
	if len(*served) != 1 {
		t.Fatalf("serve ran %d times, want once", len(*served))
	}
	opts := (*served)[0]
	var listens []string
	for _, s := range opts.Listen {
		listens = append(listens, s.String())
	}
	if opts.Paths != paths.Under(filepath.Join(state, "darkpanel-dev")) || !opts.Dev || opts.Log == nil ||
		!slices.Equal(listens, []string{"loopback", "https://127.0.0.1:9443"}) {
		t.Errorf("serve --dev passed %+v, want the dev paths, Dev, a logger and both listen values", opts)
	}
}

func TestServeProductionOptions(t *testing.T) {
	served := fakeServe(t, 0, nil)
	if out, code := runArgs("serve"); code != cli.ExitOK {
		t.Fatalf("serve as root: exit %d: %s", code, out)
	}
	opts := (*served)[0]
	var listens []string
	for _, s := range opts.Listen {
		listens = append(listens, s.String())
	}
	if opts.Paths != paths.Production() || opts.Dev || !slices.Equal(listens, []string{"loopback", "tailscale"}) {
		t.Errorf("serve passed %+v, want the production paths and the default listeners", opts)
	}
}
