package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/modules"
)

// fakePlan is a planned uninstall that records what was done with it.
type fakePlan struct {
	calls []string
}

func (f *fakePlan) Describe(w io.Writer) {
	f.calls = append(f.calls, "describe")
	_, _ = fmt.Fprintln(w, "will remove the units")
}

func (f *fakePlan) Run(_ context.Context, w io.Writer) error {
	f.calls = append(f.calls, "run")
	_, _ = fmt.Fprintln(w, "removed the units")
	return nil
}

func (f *fakePlan) Close() error {
	f.calls = append(f.calls, "close")
	return nil
}

func TestUninstall(t *testing.T) {
	const described, removed = "will remove the units\n", "removed the units\n"
	tests := []struct {
		name     string
		args     []string
		euid     int
		terminal bool
		stdin    string
		code     int
		calls    []string
		stdout   string
		stderr   string
	}{
		{"yes", []string{"--yes"}, 0, false, "", cli.ExitOK, []string{"describe", "run"}, described + removed, ""},
		{"keep data", []string{"--yes", "--keep-data"}, 0, false, "", cli.ExitOK, []string{"describe", "run"}, described + removed, ""},
		{"no terminal without yes", nil, 0, false, "y\n", cli.ExitRefused, []string{"describe", "close"}, described,
			"darkpanel: uninstall needs --yes when standard input is not a terminal\n"},
		{"confirmed on the terminal", nil, 0, true, "y\n", cli.ExitOK, []string{"describe", "run"}, described + removed, "Proceed? [y/N] "},
		{"declined on the terminal", nil, 0, true, "\n", cli.ExitRefused, []string{"describe", "close"}, described, "Proceed? [y/N] darkpanel: uninstall declined\n"},
		{"not root", []string{"--yes"}, 1000, false, "", cli.ExitUsage, nil, "", "darkpanel: uninstall runs as root; run it with sudo\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := fakeAgent(t, tt.euid)
			plan := &fakePlan{}
			var cfgs []install.UninstallConfig
			oldPlan, oldTerm := planUninstall, stdinTerminal
			t.Cleanup(func() { planUninstall, stdinTerminal = oldPlan, oldTerm })
			planUninstall = func(_ context.Context, cfg install.UninstallConfig) (plannedUninstall, error) {
				cfgs = append(cfgs, cfg)
				return plan, nil
			}
			stdinTerminal = func(io.Reader) bool { return tt.terminal }
			stdout, stderr, code := runStdin(strings.NewReader(tt.stdin), append([]string{"uninstall"}, tt.args...)...)
			if code != tt.code || !slices.Equal(plan.calls, tt.calls) || stdout != tt.stdout || stderr != tt.stderr {
				t.Errorf("exit %d, calls %q, stdout %q, stderr %q; want exit %d, calls %q, %q and %q",
					code, plan.calls, stdout, stderr, tt.code, tt.calls, tt.stdout, tt.stderr)
			}
			if tt.calls == nil {
				return
			}
			cfg := cfgs[0]
			if cfg.Layout != s.layout || cfg.KeepData != slices.Contains(tt.args, "--keep-data") || len(cfg.Modules) != len(modules.All()) ||
				cfg.Systemd != s.systemd || cfg.Exec == nil || cfg.Users != (install.SystemUsers{}) || cfg.Clock == nil || cfg.Log == nil || cfg.FilesTestMode {
				t.Errorf("UninstallConfig = %+v, want the installed layout, the modules and the production providers", cfg)
			}
			if s.systemd.closed != 1 {
				t.Errorf("systemd closed %d times, want once", s.systemd.closed)
			}
		})
	}
}
