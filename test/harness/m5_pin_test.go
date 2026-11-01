//go:build harness

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/dispatch"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailscale"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

// m5pDeadline bounds TestM5Pin, a go test run of its own that needs -os, -m2
// and -m5. The -timeout of the workflow step that runs this test alone must
// stay at least m5pDeadline plus 10 minutes, for the cleanups that stop the
// VMs.
const m5pDeadline = 35 * time.Minute

// m5pStep is one scenario of TestM5Pin; a failed gate step ends the run.
type m5pStep struct {
	name string
	gate bool // later steps need this one
	run  func(t *testing.T)
}

// m5pSteps is the step table of TestM5Pin. Its closures read ctx and f only
// when they run, so a test may build the table with a nil fleet.
func m5pSteps(ctx context.Context, f *fleet) []m5pStep {
	return []m5pStep{
		{"install", true, func(t *testing.T) { scenarioInstall(ctx, t, f) }},
		{"package-install", true, func(t *testing.T) { m5pPackage(ctx, t, f) }},
		{"pin-rollback", false, func(t *testing.T) { m5pRollback(ctx, t, f) }},
		{"tailscaled-live", false, func(t *testing.T) { m5lLive(ctx, t, f) }},
	}
}

// TestM5Pin is the pin half of M5 (spec 4.1): a pin whose health check fails
// while the agent cannot reach the controller rolls back on the host alone.
// It declares no flag: TestM5Node registers -m5.
func TestM5Pin(t *testing.T) {
	registered, on := m5pFlagState(flag.CommandLine)
	if *osFlag != "" && *m2Flag != "" && !registered {
		t.Fatal("harness: the -m5 flag is not registered; TestM5Node declares it")
	}
	if *osFlag == "" || *m2Flag == "" || !on {
		t.Skip("no -os, -m2 or -m5 given: the VM tests run in .github/workflows/harness.yml")
	}
	ctx, cancel := context.WithTimeout(context.Background(), m5pDeadline)
	defer cancel()
	f := bootFleet(ctx, t)
	serveReleases(ctx, t, f)
	for _, s := range m5pSteps(ctx, f) {
		if !t.Run(s.name, s.run) && s.gate {
			return
		}
	}
}

// m5pFlagState reports whether fs has the flag m5 and whether it is true.
func m5pFlagState(fs *flag.FlagSet) (registered, on bool) {
	fl := fs.Lookup("m5")
	if fl == nil {
		return false, false
	}
	return true, fl.Value.String() == "true"
}

var m5pVersionPattern = regexp.MustCompile(`^[0-9][0-9A-Za-z.+~-]{0,63}$`)

// m5pTarget picks the version to pin from the output of apt-cache madison
// tailscale: the newest one other than installed that the pin accepts.
func m5pTarget(madison, installed string) (string, error) {
	for _, line := range strings.Split(madison, "\n") {
		fields := strings.Split(line, "|")
		if len(fields) < 3 || strings.TrimSpace(fields[0]) != "tailscale" {
			continue
		}
		version := strings.TrimSpace(fields[1])
		if version == installed || !m5pVersionPattern.MatchString(version) {
			continue
		}
		return version, nil
	}
	return "", fmt.Errorf("harness: apt-cache madison lists no tailscale version other than the installed %s\n%s", installed, madison)
}

// m5pPin is the part of a tailscale_pins row the scenario checks.
type m5pPin struct {
	Phase, Version, PriorVersion, PriorBackend, StagedSHA256 string
	PriorHeld                                                bool
}

var errM5pNoRow = errors.New("harness: no tailscale_pins row")

const (
	m5pPinsSQL  = "SELECT change_id, phase, version, prior_version, prior_held, prior_backend, staged_sha256 FROM tailscale_pins ORDER BY updated_at DESC"
	m5pStepsSQL = "SELECT change_id, seq, step, result FROM tailscale_pin_steps ORDER BY change_id, seq"
)

// m5pPhases are the phases a tailscale_pins row may be in.
var m5pPhases = []string{"recorded", "unheld", "staged", "installed", "held", "checking", "done", "rolling_back", "rolled_back", "failed"}

func m5pQuery(query string) string {
	return "sudo sqlite3 -separator '|' " + m3dxShell(paths.Production().AgentDB(), query)
}

func m5pPinsCommand() string  { return m5pQuery(m5pPinsSQL) }
func m5pStepsCommand() string { return m5pQuery(m5pStepsSQL) }

// m5pParsePin reads the lines m5pPinsCommand prints and returns the row of
// changeID, or errM5pNoRow when no line belongs to it.
func m5pParsePin(out, changeID string) (m5pPin, error) {
	var match []string
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) != 7 {
			return m5pPin{}, fmt.Errorf("harness: tailscale_pins line has %d fields, want 7 fields: %q", len(fields), line)
		}
		if fields[0] == changeID && match == nil {
			match = fields
		}
	}
	if match == nil {
		return m5pPin{}, errM5pNoRow
	}
	p := m5pPin{Phase: match[1], Version: match[2], PriorVersion: match[3], PriorBackend: match[5], StagedSHA256: match[6]}
	switch match[4] {
	case "0":
	case "1":
		p.PriorHeld = true
	default:
		return m5pPin{}, fmt.Errorf("harness: tailscale_pins prior_held is %q, want 0 or 1", match[4])
	}
	if !slices.Contains(m5pPhases, p.Phase) {
		return m5pPin{}, fmt.Errorf("harness: tailscale_pins phase is %q, want one of %s", p.Phase, strings.Join(m5pPhases, ", "))
	}
	if p.Version == "" {
		return m5pPin{}, errors.New("harness: tailscale_pins version is empty")
	}
	if p.PriorVersion == "" {
		return m5pPin{}, errors.New("harness: tailscale_pins prior_version is empty")
	}
	return p, nil
}

// m5pStepLines reads the lines m5pStepsCommand prints and returns the steps
// of changeID, one "<seq> <step>: <result>" each.
func m5pStepLines(out, changeID string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "|", 4)
		if len(parts) == 4 && parts[0] == changeID {
			lines = append(lines, parts[1]+" "+parts[2]+": "+parts[3])
		}
	}
	return lines
}

// m5pSettled accepts the states a rolled-back pin ends in: failed through the
// engine's failure table, or reverted through the recovery handler.
func m5pSettled(state string) error {
	if state == core.StateFailed || state == core.StateReverted {
		return nil
	}
	return fmt.Errorf("harness: the change ended %q, want failed or reverted", state)
}

// m5pCutRule is the rule that drops what the agent sends to the controller;
// dppriv is the private NIC the lab gives the two hosts.
const m5pCutRule = "-o dppriv -j DROP"

// m5pIptables is the iptables command of op (-C or -D) on the cut rule.
func m5pIptables(op string) string {
	return "sudo iptables " + op + " OUTPUT " + m5pCutRule
}

// m5pHooks names the programs and the flag file the apt-mark wrapper uses.
type m5pHooks struct{ Real, Flag, Iptables, Systemctl string }

// m5pProductionHooks is what the wrapper uses on a VM. The agent's runner sets
// PATH=/usr/sbin:/usr/bin:/sbin:/bin, so the bare names resolve.
var m5pProductionHooks = m5pHooks{Real: "/usr/bin/apt-mark.distrib", Flag: "/run/m5-pin-fail", Iptables: "iptables", Systemctl: "systemctl"}

// m5pWrapper is the script that replaces apt-mark. The first successful
// "hold tailscale" while the flag exists, which is phase 5 of the pin, cuts
// the link and stops tailscaled. The flag goes first, so the hold the
// rollback restores passes straight through.
func m5pWrapper(h m5pHooks) string {
	return "#!/bin/sh\n" +
		m3dxShell(h.Real) + ` "$@"` + "\n" +
		"rc=$?\n" +
		`if [ "$rc" -eq 0 ] && [ "$*" = 'hold tailscale' ] && [ -e ` + m3dxShell(h.Flag) + " ]; then\n" +
		"\trm -f " + m3dxShell(h.Flag) + "\n" +
		"\t" + m3dxShell(h.Iptables) + " -I OUTPUT 1 " + m5pCutRule + "\n" +
		"\t" + m3dxShell(h.Systemctl) + " stop tailscaled.service\n" +
		"fi\n" +
		"exit $rc\n"
}

// m5pHeldIn reports whether apt-mark showhold output lists tailscale.
func m5pHeldIn(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "tailscale" {
			return true
		}
	}
	return false
}

// m5pBackendState reads BackendState from tailscaled's local API status.
func m5pBackendState(out string) (string, error) {
	var st struct{ BackendState string }
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		return "", fmt.Errorf("harness: tailscaled status %q: %w", out, err)
	}
	if st.BackendState == "" {
		return "", fmt.Errorf("harness: tailscaled status %q holds no BackendState", out)
	}
	return st.BackendState, nil
}

// m5pWaitStatus waits for tailscale.status of the agent to answer.
func m5pWaitStatus(ctx context.Context, f *fleet, timeout time.Duration, check func(tailscale.Status) error) (tailscale.Status, error) {
	var st tailscale.Status
	err := eventually(ctx, timeout, func(ctx context.Context) error {
		st = tailscale.Status{}
		if err := f.api.Resource(ctx, f.agentID, "tailscale.status", &st); err != nil {
			return err
		}
		return check(st)
	})
	return st, err
}

func m5pAnswers(st tailscale.Status) error {
	if st.Version == "" {
		return errors.New("harness: tailscale.status holds no version")
	}
	return nil
}

// m5pPackage replaces the pinned .deb tailscaled of the VM image by the
// package Darkpanel installs, which is what writes the Tailscale repository
// the pin's staging needs, and holds it.
func m5pPackage(ctx context.Context, t *testing.T, f *fleet) {
	if _, err := m5pWaitStatus(ctx, f, 3*time.Minute, m5pAnswers); err != nil {
		t.Fatalf("tailscale.status never answered: %v", err)
	}
	m3Run(ctx, t, f.agent, "sudo env DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=600 -y remove tailscale")
	t.Log("tailscale.package.install needs pkgs.tailscale.com; a failure here names what was unreachable")
	m4ApplyAsync(ctx, t, f.api, f.agentID, "tailscale.package.install", tailscale.PackageInstallInput{})
	if _, err := m5pWaitStatus(ctx, f, 3*time.Minute, m5pAnswers); err != nil {
		t.Fatalf("tailscale.status never answered after the install: %v", err)
	}
	m3Run(ctx, t, f.agent, "sudo apt-mark hold tailscale")
	if out := m3Run(ctx, t, f.agent, "apt-mark showhold"); !m5pHeldIn(out) {
		t.Fatalf("apt-mark showhold does not list tailscale after the hold:\n%s", out)
	}
}

// m5pArm installs the apt-mark wrapper and its flag on v. Its cleanup removes
// the flag, the cut rule and the wrapper.
func m5pArm(ctx context.Context, t *testing.T, v *vm.VM) {
	t.Helper()
	const apt = "/usr/bin/apt-mark"
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		script := "rm -f " + m5pProductionHooks.Flag + "; " +
			"while iptables -D OUTPUT " + m5pCutRule + "; do :; done; " +
			"if [ -e " + apt + ".distrib ]; then rm -f " + apt + " && dpkg-divert --local --rename --remove " + apt + "; fi"
		_, _ = v.Run(cctx, "sudo sh -c "+m3Quote(script))
	})
	m3Run(ctx, t, v, "sudo dpkg-divert --local --rename --add "+apt)
	if out, err := v.RunInput(ctx, "sudo tee "+apt+" >/dev/null && sudo chmod 0755 "+apt, strings.NewReader(m5pWrapper(m5pProductionHooks))); err != nil {
		t.Fatalf("install the apt-mark wrapper: %v\n%s", err, out)
	}
	m3Run(ctx, t, v, "sudo touch "+m5pProductionHooks.Flag)
}

// m5pPlanWait bounds the retries of the pin's plan. The controller keeps a
// host's availability answer for up to dispatch.AvailabilityMaxAge (spec
// 1.7), and the answer it took when package-install was planned, while the
// package was removed and tailscaled absent, lists the pin unavailable.
const m5pPlanWait = dispatch.AvailabilityMaxAge + 2*time.Minute

// m5pPlanRetries reports whether a plan refused with err may pass once the
// controller asks the host again: only module.unavailable comes from the
// cached availability answer.
func m5pPlanRetries(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Code == string(errcode.ModuleUnavailable)
}

// m5pPlanPin plans the pin of target, again while the refusal is
// module.unavailable; any other refusal ends it at once.
func m5pPlanPin(ctx context.Context, f *fleet, target string) (m4PlanView, error) {
	var p m4PlanView
	var planErr error
	err := eventually(ctx, m5pPlanWait, func(ctx context.Context) error {
		p, planErr = m4Plan(ctx, f.api, f.agentID, "tailscale.package.pin", tailscale.PinInput{Version: target})
		if m5pPlanRetries(planErr) {
			return planErr
		}
		return nil
	})
	if err != nil {
		return m4PlanView{}, err
	}
	return p, planErr
}

var m5pSHAPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// m5pRollback pins another version with the agent's link to the controller
// cut and tailscaled stopped at phase 5, then checks that the host rolled the
// pin back by itself and that the controller learned of it once the link came
// back.
func m5pRollback(ctx context.Context, t *testing.T, f *fleet) {
	installed := strings.TrimSpace(m3Run(ctx, t, f.agent, "dpkg-query -W -f='${Version}' tailscale"))
	if installed == "" {
		t.Fatal("dpkg-query reports no installed tailscale version")
	}
	madison := m3Run(ctx, t, f.agent, "apt-cache madison tailscale")
	target, err := m5pTarget(madison, installed)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("installed tailscale %s, pinning %s", installed, target)

	m5pArm(ctx, t, f.agent)
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		cctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		for _, cmd := range []string{m5pStepsCommand(), "sudo journalctl -u " + update.AgentUnit + " -u 'darkpanel-task-*' --no-pager -n 150"} {
			out, err := f.agent.Run(cctx, cmd)
			t.Logf("%s (%v):\n%s", cmd, err, out)
		}
	})

	p, err := m5pPlanPin(ctx, f, target)
	if err != nil {
		t.Fatal(err)
	}
	path, err := actionPath(f.agentID, "tailscale.package.pin/v1", "apply")
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"planId": p.PlanID}
	if p.Confirmation != "" {
		body["confirmation"] = p.Confirmation
	}
	var change Change
	if err := f.api.do(ctx, http.MethodPost, path, body, &change); err != nil {
		t.Fatalf("apply tailscale.package.pin: %v", err)
	}

	var row m5pPin
	err = eventually(ctx, 8*time.Minute, func(ctx context.Context) error {
		out, err := f.agent.Run(ctx, m5pPinsCommand())
		if err != nil {
			return err
		}
		if row, err = m5pParsePin(out, change.ID); err != nil {
			return err
		}
		switch row.Phase {
		case "rolled_back", "failed", "done":
			return nil
		}
		return fmt.Errorf("harness: the pin is in phase %s", row.Phase)
	})
	if err != nil {
		t.Fatalf("the pin of change %s never ended: %v", change.ID, err)
	}
	stepsOut, _ := f.agent.Run(ctx, m5pStepsCommand())
	steps := m5pStepLines(stepsOut, change.ID)
	if row.Phase != "rolled_back" {
		t.Fatalf("the pin ended in phase %s, want rolled_back; steps:\n%s", row.Phase, strings.Join(steps, "\n"))
	}
	if row.Version != target {
		t.Errorf("the pin row names version %q, want %q", row.Version, target)
	}
	if row.PriorVersion != installed {
		t.Errorf("the pin row records prior version %q, want %q", row.PriorVersion, installed)
	}
	if !row.PriorHeld {
		t.Error("the pin row records no prior hold, want one")
	}
	if !m5pSHAPattern.MatchString(row.StagedSHA256) {
		t.Errorf("the pin row's staged_sha256 is %q, want 64 hex digits", row.StagedSHA256)
	}
	if len(steps) == 0 {
		t.Error("the rollback journaled no step")
	}
	t.Logf("pin steps:\n%s", strings.Join(steps, "\n"))

	if out, err := f.agent.Run(ctx, "test ! -e "+m5pProductionHooks.Flag); err != nil {
		t.Errorf("%s still exists, so the hook did not fire after the real apt-mark hold: %v\n%s", m5pProductionHooks.Flag, err, out)
	}
	if out, err := f.agent.Run(ctx, m5pIptables("-C")); err != nil {
		t.Errorf("the cut rule is gone, so the rollback ran with the controller reachable: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(m3Run(ctx, t, f.agent, "dpkg-query -W -f='${Version}' tailscale")); got != installed {
		t.Errorf("installed tailscale is %q after the rollback, want %q", got, installed)
	}
	if out := m3Run(ctx, t, f.agent, "apt-mark showhold"); !m5pHeldIn(out) {
		t.Errorf("tailscale is not held after the rollback, want the prior hold back:\n%s", out)
	}
	if got := unitState(ctx, f.agent, "tailscaled"); got != "active" {
		t.Errorf("tailscaled is %q after the rollback, want active", got)
	}
	status := "sudo curl -fsS --max-time 10 --unix-socket " + tslocal.DefaultSocket + " http://local-tailscaled.sock/localapi/v0/status"
	err = eventually(ctx, 30*time.Second, func(ctx context.Context) error {
		out, err := f.agent.Run(ctx, status)
		if err != nil {
			return err
		}
		got, err := m5pBackendState(out)
		if err != nil {
			return err
		}
		if got != row.PriorBackend {
			return fmt.Errorf("harness: tailscaled is in backend state %s, want %s", got, row.PriorBackend)
		}
		return nil
	})
	if err != nil {
		t.Errorf("tailscaled did not answer in its prior backend state: %v", err)
	}

	m3Run(ctx, t, f.agent, m5pIptables("-D"))
	waitHosts(ctx, t, f, 5*time.Minute, hostsAt(versionN, f.localID, f.agentID))
	ch, err := m4Settle(ctx, f.api, change.ID)
	if err != nil {
		t.Fatalf("change %s never settled: %v", change.ID, err)
	}
	if ch.Error != nil {
		t.Logf("change %s error: %s: %s", ch.ID, ch.Error.Code, ch.Error.Message)
	}
	if err := m5pSettled(ch.State); err != nil {
		t.Error(err)
	}

	if _, err := m5pWaitStatus(ctx, f, 2*time.Minute, func(st tailscale.Status) error {
		if st.BackendState != row.PriorBackend {
			return fmt.Errorf("harness: tailscale.status backend state is %s, want %s", st.BackendState, row.PriorBackend)
		}
		return nil
	}); err != nil {
		t.Errorf("tailscale.status after the link came back: %v", err)
	}
}
