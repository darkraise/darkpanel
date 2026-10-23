package main

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/agent/daemon"
	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/transport"
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

func TestUninstallModuleOptionFlag(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		code   int
		want   map[string]map[string]string
		stderr string
	}{
		{"none", nil, cli.ExitOK, nil, ""},
		{"repeated", []string{"--module-option", "opt.rules=keep", "--module-option", "opt.x=y", "--module-option", "fw.mode=a=b"},
			cli.ExitOK, map[string]map[string]string{"opt": {"rules": "keep", "x": "y"}, "fw": {"mode": "a=b"}}, ""},
		{"no key", []string{"--module-option", "opt=keep"}, cli.ExitUsage, nil, `"opt=keep" is not <module>.<key>=<value>`},
		{"no value", []string{"--module-option", "opt.rules"}, cli.ExitUsage, nil, `"opt.rules" is not <module>.<key>=<value>`},
		{"no module", []string{"--module-option", ".rules=keep"}, cli.ExitUsage, nil, `".rules=keep" is not <module>.<key>=<value>`},
		{"twice", []string{"--module-option", "opt.rules=keep", "--module-option", "opt.rules=remove"}, cli.ExitUsage, nil,
			"--module-option opt.rules is given twice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeAgent(t, 0)
			var cfgs []install.UninstallConfig
			old := planUninstall
			t.Cleanup(func() { planUninstall = old })
			planUninstall = func(_ context.Context, cfg install.UninstallConfig) (plannedUninstall, error) {
				cfgs = append(cfgs, cfg)
				return &fakePlan{}, nil
			}
			_, stderr, code := runStdin(strings.NewReader(""), append([]string{"uninstall", "--yes"}, tt.args...)...)
			if code != tt.code || !strings.Contains(stderr, tt.stderr) {
				t.Fatalf("exit %d, stderr %q; want exit %d and %q", code, stderr, tt.code, tt.stderr)
			}
			if tt.code != cli.ExitOK {
				if len(cfgs) != 0 {
					t.Error("a malformed --module-option planned an uninstall")
				}
				return
			}
			if len(cfgs) != 1 || !reflect.DeepEqual(cfgs[0].ModuleOptions, tt.want) {
				t.Errorf("ModuleOptions = %v, want %v", cfgs, tt.want)
			}
		})
	}
}

func TestUninstallFlags(t *testing.T) {
	const hostID = "01HZY5S4ZK8V3J1M2N3P4Q5R6S"
	enrolled := func(string) (*daemon.Credentials, error) {
		uri, err := url.Parse(transport.HostURI(hostID))
		if err != nil {
			return nil, err
		}
		return &daemon.Credentials{Cert: &x509.Certificate{URIs: []*url.URL{uri}}}, nil
	}
	unenrolled := func(string) (*daemon.Credentials, error) { return nil, daemon.ErrNotEnrolled }
	broken := func(string) (*daemon.Credentials, error) {
		return nil, errors.New("daemon: generation 3: agent.crt: bad PEM")
	}
	const cleared = "the uninstall barrier is cleared; the agent accepts changes again"
	tests := []struct {
		name      string
		args      []string
		creds     func(string) (*daemon.Credentials, error)
		code      int
		plans     int
		stopTasks bool
		actor     string // the abort's actor; "" when no abort runs
		out       string
	}{
		{"stop tasks", []string{"--yes", "--stop-tasks"}, unenrolled, cli.ExitOK, 1, true, "", "removed the units"},
		{"without stop tasks", []string{"--yes"}, unenrolled, cli.ExitOK, 1, false, "", "removed the units"},
		{"abort", []string{"--abort"}, enrolled, cli.ExitOK, 0, false, "local:root@" + hostID, cleared},
		{"abort before enrollment", []string{"--abort"}, unenrolled, cli.ExitOK, 0, false, core.ActorUnenrolled, cleared},
		{"abort with unreadable credentials", []string{"--abort"}, broken, cli.ExitError, 0, false, "", "read the agent credentials"},
		{"abort with stop tasks", []string{"--abort", "--stop-tasks"}, enrolled, cli.ExitUsage, 0, false, "", "--abort takes none of"},
		{"abort with keep data", []string{"--abort", "--keep-data"}, enrolled, cli.ExitUsage, 0, false, "", "--abort takes none of"},
		{"abort with a module option", []string{"--abort", "--module-option", "opt.rules=keep"}, enrolled, cli.ExitUsage, 0, false, "",
			"--abort takes none of"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := fakeAgent(t, 0)
			var plans, aborts []install.UninstallConfig
			oldPlan, oldAbort, oldCreds := planUninstall, abortUninstall, loadCredentials
			t.Cleanup(func() { planUninstall, abortUninstall, loadCredentials = oldPlan, oldAbort, oldCreds })
			planUninstall = func(_ context.Context, cfg install.UninstallConfig) (plannedUninstall, error) {
				plans = append(plans, cfg)
				return &fakePlan{}, nil
			}
			abortUninstall = func(_ context.Context, cfg install.UninstallConfig) (string, error) {
				aborts = append(aborts, cfg)
				return cleared, nil
			}
			var dirs []string
			loadCredentials = func(dir string) (*daemon.Credentials, error) {
				dirs = append(dirs, dir)
				return tt.creds(dir)
			}
			stdout, stderr, code := runStdin(strings.NewReader(""), append([]string{"uninstall"}, tt.args...)...)
			if code != tt.code || !strings.Contains(stdout+stderr, tt.out) || len(plans) != tt.plans {
				t.Fatalf("exit %d, %d plans, stdout %q, stderr %q; want exit %d, %d plans and %q",
					code, len(plans), stdout, stderr, tt.code, tt.plans, tt.out)
			}
			if tt.plans == 1 && plans[0].StopTasks != tt.stopTasks {
				t.Errorf("StopTasks = %v, want %v", plans[0].StopTasks, tt.stopTasks)
			}
			if tt.actor == "" {
				if len(aborts) != 0 {
					t.Errorf("an abort ran: %+v", aborts)
				}
				return
			}
			if len(aborts) != 1 || aborts[0].Actor != tt.actor || aborts[0].Layout != s.layout || aborts[0].Systemd != s.systemd {
				t.Errorf("aborts %+v, want one as %s over the installed layout and the systemd connection", aborts, tt.actor)
			}
			if !reflect.DeepEqual(dirs, []string{s.layout.Paths.AgentTLSDir}) || s.systemd.closed != 1 {
				t.Errorf("credentials read from %q and systemd closed %d times; want %s and once", dirs, s.systemd.closed,
					s.layout.Paths.AgentTLSDir)
			}
		})
	}
}
