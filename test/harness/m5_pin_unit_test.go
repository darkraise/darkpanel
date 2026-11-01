//go:build harness

package harness

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/paths"
)

func TestM5pUnitTarget(t *testing.T) {
	const source = "https://pkgs.tailscale.com/stable/ubuntu jammy/main amd64 Packages"
	line := func(pkg, version string) string { return fmt.Sprintf(" %s | %s | %s\n", pkg, version, source) }
	cases := []struct {
		name, madison, installed string
		want                     string // "" when an error is expected
	}{
		{"the newest other version", line("tailscale", "1.104.1") + line("tailscale", "1.102.0") + line("tailscale", "1.100.0"), "1.104.1", "1.102.0"},
		{"installed is not the newest", line("tailscale", "1.106.0") + line("tailscale", "1.104.1"), "1.104.1", "1.106.0"},
		{"installed is not listed", line("tailscale", "1.106.0"), "1.104.1", "1.106.0"},
		{
			"one version on two architectures",
			line("tailscale", "1.104.1") + strings.Replace(line("tailscale", "1.104.1"), "amd64", "arm64", 1) + line("tailscale", "1.102.0"),
			"1.104.1", "1.102.0",
		},
		{"another package is ignored", line("tailscale-archive-keyring", "1.35.181") + line("tailscale", "1.104.1"), "1.104.1", ""},
		{"a version the pin refuses is skipped", line("tailscale", "1.2 beta") + line("tailscale", "1:1.3") + line("tailscale", "1.102.0"), "1.104.1", "1.102.0"},
		{"only the installed version", line("tailscale", "1.104.1"), "1.104.1", ""},
		{"nothing listed", "", "1.104.1", ""},
	}
	for _, c := range cases {
		got, err := m5pTarget(c.madison, c.installed)
		switch {
		case c.want == "" && (err == nil || !strings.Contains(err.Error(), "no tailscale version other than the installed "+c.installed)):
			t.Errorf("%s: m5pTarget = %q, %v, want an error naming the installed version", c.name, got, err)
		case c.want != "" && (err != nil || got != c.want):
			t.Errorf("%s: m5pTarget = %q, %v, want %q", c.name, got, err, c.want)
		}
	}
}

func TestM5pUnitPinRows(t *testing.T) {
	sha := strings.Repeat("ab", 32)
	out := "01JOTHER|done|1.100.0|1.099.0|0|NeedsLogin|\n" +
		"01JPIN|rolled_back|1.102.0|1.104.1|1|NeedsLogin|" + sha + "\n"
	got, err := m5pParsePin(out, "01JPIN")
	want := m5pPin{Phase: "rolled_back", Version: "1.102.0", PriorVersion: "1.104.1", PriorHeld: true, PriorBackend: "NeedsLogin", StagedSHA256: sha}
	if err != nil || got != want {
		t.Errorf("m5pParsePin = %+v, %v, want %+v", got, err, want)
	}
	got, err = m5pParsePin("01JPIN|held|1.102.0|1.104.1|0|Running|\n", "01JPIN")
	if err != nil || got.Phase != "held" || got.PriorHeld || got.StagedSHA256 != "" {
		t.Errorf("m5pParsePin of an unstaged row = %+v, %v", got, err)
	}

	for name, tc := range map[string]struct{ out, id string }{
		"empty output":         {"", "01JPIN"},
		"a blank line":         {"\n", "01JPIN"},
		"only another change":  {"01JOTHER|done|1.100.0|1.099.0|0|NeedsLogin|\n", "01JPIN"},
		"an id that is a stem": {"01JPINX|done|1.100.0|1.099.0|0|NeedsLogin|\n", "01JPIN"},
	} {
		if _, err := m5pParsePin(tc.out, tc.id); !errors.Is(err, errM5pNoRow) {
			t.Errorf("%s: m5pParsePin = %v, want errM5pNoRow", name, err)
		}
	}
	for name, tc := range map[string]struct{ out, want string }{
		"too few fields":      {"01JPIN|held|1.102.0|1.104.1|0|Running\n", "7 fields"},
		"a bad hold flag":     {"01JPIN|held|1.102.0|1.104.1|2|Running|\n", "prior_held"},
		"an unknown phase":    {"01JPIN|paused|1.102.0|1.104.1|0|Running|\n", "phase"},
		"an empty prior":      {"01JPIN|held|1.102.0||0|Running|\n", "prior_version"},
		"an empty version":    {"01JPIN|held||1.104.1|0|Running|\n", "version"},
		"a malformed sibling": {"broken\n01JPIN|held|1.102.0|1.104.1|0|Running|\n", "7 fields"},
	} {
		if _, err := m5pParsePin(tc.out, "01JPIN"); err == nil || errors.Is(err, errM5pNoRow) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: m5pParsePin = %v, want an error containing %q", name, err, tc.want)
		}
	}
}

func TestM5pUnitStepLines(t *testing.T) {
	out := "01JOTHER|1|unhold|ok\n01JPIN|1|unhold|ok\n01JPIN|2|rollback apt-mark unhold|ok\n\n01JPIN|3|restore hold|failed: exit 100\n"
	want := []string{"1 unhold: ok", "2 rollback apt-mark unhold: ok", "3 restore hold: failed: exit 100"}
	if got := m5pStepLines(out, "01JPIN"); !slices.Equal(got, want) {
		t.Errorf("m5pStepLines = %q, want %q", got, want)
	}
	if got := m5pStepLines(out, "01JNONE"); len(got) != 0 {
		t.Errorf("m5pStepLines of an unknown change = %q, want none", got)
	}
}

func TestM5pUnitSQL(t *testing.T) {
	db := paths.Production().AgentDB()
	for name, tc := range map[string]struct{ got, query string }{
		"pins":  {m5pPinsCommand(), m5pPinsSQL},
		"steps": {m5pStepsCommand(), m5pStepsSQL},
	} {
		want := "sudo sqlite3 -separator '|' " + db + " " + m3Quote(tc.query)
		if tc.got != want {
			t.Errorf("%s command =\n%s\nwant\n%s", name, tc.got, want)
		}
		if strings.Contains(tc.query, "'") {
			t.Errorf("%s query %q holds a quote; the change id is matched in Go, never put in SQL", name, tc.query)
		}
	}
}

func TestM5pUnitSettled(t *testing.T) {
	for _, state := range []string{core.StateFailed, core.StateReverted} {
		if err := m5pSettled(state); err != nil {
			t.Errorf("m5pSettled(%q) = %v, want it accepted", state, err)
		}
	}
	for _, state := range []string{core.StateCompleted, core.StateNeedsRepair, core.StateInterrupted, core.StateApplying, ""} {
		err := m5pSettled(state)
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%q", state)) {
			t.Errorf("m5pSettled(%q) = %v, want an error naming the state", state, err)
		}
	}
}

func TestM5pUnitPlanRetries(t *testing.T) {
	stale := &APIError{Status: 409, Code: "module.unavailable", Detail: "action tailscale.package.pin is unavailable"}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"a cached unavailable answer", stale, true},
		{"wrapped by m4Plan", fmt.Errorf("plan tailscale.package.pin: %w", stale), true},
		{"the handler's own refusal", &APIError{Status: 409, Code: "tailscale.unavailable"}, false},
		{"another refusal", &APIError{Status: 400, Code: "invalid_input"}, false},
		{"not an API answer", errors.New("connection refused"), false},
		{"no error", nil, false},
	}
	for _, tc := range cases {
		if got := m5pPlanRetries(tc.err); got != tc.want {
			t.Errorf("%s: m5pPlanRetries(%v) = %v, want %v", tc.name, tc.err, got, tc.want)
		}
	}
	if m5pPlanWait <= 60*time.Second {
		t.Errorf("m5pPlanWait = %s, want longer than the controller's 60 s availability cache", m5pPlanWait)
	}
}

func TestM5pUnitFlag(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	if registered, on := m5pFlagState(fs); registered || on {
		t.Errorf("an empty set = (%t, %t), want (false, false)", registered, on)
	}
	fs.Bool("m5", false, "")
	if registered, on := m5pFlagState(fs); !registered || on {
		t.Errorf("-m5 not given = (%t, %t), want (true, false)", registered, on)
	}
	if err := fs.Parse([]string{"-m5"}); err != nil {
		t.Fatal(err)
	}
	if registered, on := m5pFlagState(fs); !registered || !on {
		t.Errorf("-m5 given = (%t, %t), want (true, true)", registered, on)
	}
	if registered, _ := m5pFlagState(flag.CommandLine); !registered {
		t.Error("the harness registers no -m5 flag; TestM5Node declares it, and TestM5Pin skipping without it would hide a missing job flag")
	}
}

func TestM5pUnitSteps(t *testing.T) {
	steps := m5pSteps(context.Background(), nil)
	want := []struct {
		name string
		gate bool
	}{{"install", true}, {"package-install", true}, {"pin-rollback", false}}
	if len(steps) < len(want) {
		t.Fatalf("m5pSteps has %d steps, want at least %d", len(steps), len(want))
	}
	seen := map[string]bool{}
	for i, s := range steps {
		if s.run == nil || seen[s.name] {
			t.Errorf("step %d %q has no run function or repeats a name", i, s.name)
		}
		seen[s.name] = true
		if i < len(want) && (s.name != want[i].name || s.gate != want[i].gate) {
			t.Errorf("step %d = %q (gate %t), want %q (gate %t)", i, s.name, s.gate, want[i].name, want[i].gate)
		}
	}
	if m5pDeadline <= 0 {
		t.Errorf("m5pDeadline = %v, want a positive bound", m5pDeadline)
	}
}

func TestM5pUnitCutRule(t *testing.T) {
	for op, want := range map[string]string{
		"-C": "sudo iptables -C OUTPUT -o dppriv -j DROP",
		"-D": "sudo iptables -D OUTPUT -o dppriv -j DROP",
	} {
		if got := m5pIptables(op); got != want {
			t.Errorf("m5pIptables(%q) = %q, want %q", op, got, want)
		}
	}
}

// TestM5pUnitWrapper runs the apt-mark wrapper under sh with stand-ins for
// the real apt-mark, iptables and systemctl, all named by absolute path in a
// temporary directory, so no program of this host runs.
func TestM5pUnitWrapper(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on this host")
	}
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	stub := func(name string, exit int) string {
		p := filepath.Join(dir, name)
		body := "#!/bin/sh\necho " + m3Quote(name) + ` "$@" >> ` + m3Quote(calls) + "\nexit " + strconv.Itoa(exit) + "\n"
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil { //nolint:gosec // G306: a stand-in program the test runs
			t.Fatal(err)
		}
		return p
	}
	hooks := m5pHooks{Real: stub("real", 0), Flag: filepath.Join(dir, "flag"), Iptables: stub("iptables", 0), Systemctl: stub("systemctl", 0)}
	wrapper := filepath.Join(dir, "wrapper")
	if err := os.WriteFile(wrapper, []byte(m5pWrapper(hooks)), 0o755); err != nil { //nolint:gosec // G306: the script under test
		t.Fatal(err)
	}

	cut := []string{"real hold tailscale", "iptables -I OUTPUT 1 " + m5pCutRule, "systemctl stop tailscaled.service"}
	cases := []struct {
		name     string
		args     []string
		flag     bool
		realExit int
		runs     int
		want     []string
		wantExit int
		flagKept bool
	}{
		{"hold with the flag cuts the link, then stops tailscaled", []string{"hold", "tailscale"}, true, 0, 1, cut, 0, false},
		{"it fires once", []string{"hold", "tailscale"}, true, 0, 2, append(append([]string{}, cut...), "real hold tailscale"), 0, false},
		{"hold without the flag passes through", []string{"hold", "tailscale"}, false, 0, 1, []string{"real hold tailscale"}, 0, false},
		{"unhold leaves the flag", []string{"unhold", "tailscale"}, true, 0, 1, []string{"real unhold tailscale"}, 0, true},
		{"showhold leaves the flag", []string{"showhold"}, true, 0, 1, []string{"real showhold"}, 0, true},
		{"another argument leaves the flag", []string{"hold", "tailscale", "extra"}, true, 0, 1, []string{"real hold tailscale extra"}, 0, true},
		{"a failed hold keeps its status and the flag", []string{"hold", "tailscale"}, true, 3, 1, []string{"real hold tailscale"}, 3, true},
	}
	for _, c := range cases {
		stub("real", c.realExit)
		_ = os.Remove(calls)
		_ = os.Remove(hooks.Flag)
		if c.flag {
			if err := os.WriteFile(hooks.Flag, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < c.runs; i++ {
			cmd := exec.Command(wrapper, c.args...) //nolint:gosec // G204: the script the test wrote
			cmd.Env = []string{"PATH=/usr/bin:/bin"}
			out, err := cmd.CombinedOutput()
			exit := 0
			var ee *exec.ExitError
			switch {
			case errors.As(err, &ee):
				exit = ee.ExitCode()
			case err != nil:
				t.Fatalf("%s: %v", c.name, err)
			}
			if i == 0 && exit != c.wantExit {
				t.Errorf("%s: exit status %d, want %d (output %q)", c.name, exit, c.wantExit, out)
			}
		}
		b, _ := os.ReadFile(calls) //nolint:gosec // G304: a file in the test's temporary directory
		if got := strings.Split(strings.TrimSpace(string(b)), "\n"); !slices.Equal(got, c.want) {
			t.Errorf("%s: calls =\n%q\nwant\n%q", c.name, got, c.want)
		}
		_, err := os.Stat(hooks.Flag)
		if kept := err == nil; kept != c.flagKept {
			t.Errorf("%s: flag kept = %t, want %t", c.name, kept, c.flagKept)
		}
	}
}

func TestM5pUnitHeld(t *testing.T) {
	for name, tc := range map[string]struct {
		out  string
		want bool
	}{
		"held":                   {"tailscale\n", true},
		"held among others":      {"docker-ce\ntailscale\n", true},
		"padded":                 {"  tailscale  \n", true},
		"none":                   {"", false},
		"a package of that stem": {"tailscale-archive-keyring\n", false},
	} {
		if got := m5pHeldIn(tc.out); got != tc.want {
			t.Errorf("%s: m5pHeldIn(%q) = %t, want %t", name, tc.out, got, tc.want)
		}
	}
}

func TestM5pUnitBackend(t *testing.T) {
	if got, err := m5pBackendState(`{"Version":"1.104.1","BackendState":"NeedsLogin","Peer":{}}`); err != nil || got != "NeedsLogin" {
		t.Errorf("m5pBackendState = %q, %v, want NeedsLogin", got, err)
	}
	for name, out := range map[string]string{
		"not JSON":       "Logged out.",
		"no state":       `{"Version":"1.104.1"}`,
		"an empty state": `{"BackendState":""}`,
		"empty":          "",
	} {
		if got, err := m5pBackendState(out); err == nil {
			t.Errorf("%s: m5pBackendState = %q, want an error", name, got)
		}
	}
}
