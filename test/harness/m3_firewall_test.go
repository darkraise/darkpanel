//go:build harness

package harness

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/update"
)

var m3Flag = flag.Bool("m3", false, "run TestM3Firewall, which needs -os and -m2; its own go test run, since TestM1 and TestM2 fill the workflow's timeout")

const (
	// m3Deadline bounds TestM3Firewall, a go test run of its own.
	m3Deadline = 60 * time.Minute
	// m3MgmtPeer is where QEMU's user-mode network makes the harness's SSH
	// connections come from.
	m3MgmtPeer = "10.0.2.2"
	// m3PanelPort is the https listener the direct-panel scenario opens on
	// the controller's internet address.
	m3PanelPort = "8443"
)

// m3PanelAddr is that listener's address.
var m3PanelAddr = net.JoinHostPort(targetIPv4, m3PanelPort)

// TestM3Firewall is the firewall half of M3 (spec, Verification): the
// lockout guard, auto-revert, snapshots and supported configurations on
// the M2 fleet, whose controller and agent run the harness build of this
// commit. Docker integration and exposure scenarios belong to later waves.
func TestM3Firewall(t *testing.T) {
	if *osFlag == "" || *m2Flag == "" || !*m3Flag {
		t.Skip("no -os, -m2 or -m3 given: the VM tests run in .github/workflows/harness.yml")
	}
	ctx, cancel := context.WithTimeout(context.Background(), m3Deadline)
	defer cancel()
	f := bootFleet(ctx, t)
	serveReleases(ctx, t, f)
	steps := []struct {
		name string
		gate bool // later steps need this one
		run  func(t *testing.T)
	}{
		{"install", true, func(t *testing.T) { scenarioInstall(ctx, t, f) }},
		{"firewall-ready", true, func(t *testing.T) { m3Ready(ctx, t, f) }},
		{"refuse-ssh", false, func(t *testing.T) { m3RefuseSSH(ctx, t, f) }},
		{"refuse-agent-link", false, func(t *testing.T) { m3RefuseAgentLink(ctx, t, f) }},
		{"refuse-controller-link", false, func(t *testing.T) { m3RefuseControllerLink(ctx, t, f) }},
		{"refuse-tailscaled-egress", false, func(t *testing.T) { m3RefuseTailscaled(ctx, t, f) }},
		{"refuse-direct-panel", false, func(t *testing.T) { m3RefuseDirectPanel(ctx, t, f) }},
		{"revert-on-disconnect", false, func(t *testing.T) { m3RevertOnDisconnect(ctx, t, f) }},
		{"revert-on-reboot", false, func(t *testing.T) { m3RevertOnReboot(ctx, t, f) }},
		{"prediction-mismatch", false, func(t *testing.T) { m3PredictionMismatch(ctx, t, f) }},
		{"manual-edit-survives", false, func(t *testing.T) { m3ManualEditSurvives(ctx, t, f) }},
		{"disable-reverts", false, func(t *testing.T) { m3DisableReverts(ctx, t, f) }},
		{"repair-reverts", false, func(t *testing.T) { m3RepairReverts(ctx, t, f) }},
		{"repair-reverts-reboot", false, func(t *testing.T) { m3RepairRevertsReboot(ctx, t, f) }},
		{"unsupported-before-init", false, func(t *testing.T) { m3UnsupportedBeforeInit(ctx, t, f) }},
		{"unsupported-nftables-chain", false, func(t *testing.T) { m3UnsupportedNftables(ctx, t, f) }},
		{"ipv6no-foreign-rules", false, func(t *testing.T) { m3IPv6NoForeignRules(ctx, t, f) }},
		{"ipv6-ssh-session", false, func(t *testing.T) { m3IPv6SSHSession(ctx, t, f) }},
		{"asymmetric-rp-filter", false, func(t *testing.T) { m3AsymmetricRoute(ctx, t, f) }},
		{"sysctl-revert", false, func(t *testing.T) { m3SysctlRevert(ctx, t, f) }},
		// The golden re-check resets UFW's rules on the agent host, so it runs last.
		{"golden-recheck", false, func(t *testing.T) { m3GoldenRecheck(ctx, t, f) }},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) && s.gate {
			return
		}
	}
}

// m3Ready waits until both hosts report a supported firewall, in sync and
// active.
func m3Ready(ctx context.Context, t *testing.T, f *fleet) {
	for _, host := range []string{f.localID, f.agentID} {
		m3WaitStatus(ctx, t, f.api, host, 2*time.Minute, m3Healthy)
	}
}

// m3RequireCut plans action on host with input through api and requires
// its firewall.lockout guard to fail, overridably, naming want. Nothing is
// applied.
func m3RequireCut(ctx context.Context, t *testing.T, api *API, host, action string, input any, want string) {
	t.Helper()
	p, err := api.m3Plan(ctx, host, action, input)
	if err != nil {
		t.Fatal(err)
	}
	g, ok := p.m3Lockout()
	switch {
	case !ok:
		t.Fatalf("the plan of %s has no firewall.lockout guard: %s", action, m3JSON(p.Guards))
	case g.OK:
		t.Fatalf("firewall.lockout passed (%s); want it to fail naming %q", g.Message, want)
	case !strings.Contains(g.Message, want):
		t.Errorf("firewall.lockout failed with %q; want it to name %q", g.Message, want)
	case !g.Overridable:
		t.Errorf("firewall.lockout is not overridable on a supported host in sync: %q", g.Message)
	}
}

// m3RefuseSSH: deleting the rule that admits SSH would cut the harness's
// own SSH session.
func m3RefuseSSH(ctx context.Context, t *testing.T, f *fleet) {
	st := m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, m3Healthy)
	r, ok := m3FindRule(st.Rules, m3ManualPort("22", ""))
	if !ok {
		t.Fatalf("the agent host has no manual allow rule for 22/tcp: %s", m3JSON(st.Rules))
	}
	m3RequireCut(ctx, t, f.api, f.agentID, "firewall.rule.delete", map[string]any{"id": r.ID, "manual": true},
		"ssh from "+m3MgmtPeer+" to ")
}

// m3RefuseAgentLink: on the controller's host, deleting the rule that
// admits agents on the private link would cut the agent's link.
func m3RefuseAgentLink(ctx context.Context, t *testing.T, f *fleet) {
	st := m3WaitStatus(ctx, t, f.api, f.localID, time.Minute, m3Healthy)
	_, port, _ := net.SplitHostPort(agentAddress)
	r, ok := m3FindRule(st.Rules, m3ManualPort(port, "dppriv"))
	if !ok {
		t.Fatalf("the controller host has no manual allow rule for %s/tcp on dppriv: %s", port, m3JSON(st.Rules))
	}
	m3RequireCut(ctx, t, f.api, f.localID, "firewall.rule.delete", map[string]any{"id": r.ID, "manual": true},
		"from "+agentPriv+" to "+agentAddress)
}

// m3DenyOutgoing is the default policy change that cuts every outbound
// flow of the agent host.
var m3DenyOutgoing = map[string]any{"outgoing": "DROP"}

// m3RefuseControllerLink: on the agent's host, denying outgoing traffic
// would cut the agent's link to the controller.
func m3RefuseControllerLink(ctx context.Context, t *testing.T, f *fleet) {
	m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, m3Healthy)
	m3RequireCut(ctx, t, f.api, f.agentID, "firewall.defaults.set", m3DenyOutgoing,
		"controller link from "+agentPriv+" to "+agentAddress)
}

// m3RefuseTailscaled: denying outgoing traffic would cut tailscaled's own
// traffic, which the guard checks whenever tailscaled holds a socket.
func m3RefuseTailscaled(ctx context.Context, t *testing.T, f *fleet) {
	out := m3Run(ctx, t, f.agent, `sudo ss -Htuanp`)
	if !strings.Contains(out, `"tailscaled"`) {
		t.Skip("tailscaled holds no TCP or UDP socket on the agent host while it is logged out, so the guard has no tailscaled flow to protect")
	}
	m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, m3Healthy)
	m3RequireCut(ctx, t, f.api, f.agentID, "firewall.defaults.set", m3DenyOutgoing, "tailscale from ")
}

// m3RefuseDirectPanel opens an https listener on the controller's
// internet address, signs in through it from the scanner, and requires that
// deleting the rule admitting it would cut that panel session.
func m3RefuseDirectPanel(ctx context.Context, t *testing.T, f *fleet) {
	dropIn := "/etc/systemd/system/" + update.ControllerUnit + ".d/20-m3-listen.conf"
	unit := fmt.Sprintf("[Service]\nExecStart=\nExecStart=%s serve --agent-listen %s --listen loopback --listen https://%s\n",
		paths.Production().Binary(), agentAddress, m3PanelAddr)
	t.Cleanup(func() {
		_, _ = f.controller.Run(ctx, "sudo rm -f "+dropIn+" && sudo ufw delete allow "+m3PanelPort+"/tcp")
		_, _ = f.controller.Run(ctx, "sudo systemctl daemon-reload && sudo systemctl restart "+update.ControllerUnit)
		if err := eventually(ctx, time.Minute, func(ctx context.Context) error {
			_, err := f.controller.Run(ctx, healthz)
			return err
		}); err != nil {
			t.Errorf("the controller did not come back after the direct-panel scenario: %v", err)
			return
		}
		login(ctx, t, f)
		waitHosts(ctx, t, f, 2*time.Minute, hostsAt(versionN, f.localID, f.agentID))
	})
	if out, err := f.controller.RunInput(ctx, "sudo mkdir -p "+dropIn[:strings.LastIndex(dropIn, "/")]+" && sudo tee "+dropIn+" >/dev/null",
		strings.NewReader(unit)); err != nil {
		t.Fatalf("write %s: %v\n%s", dropIn, err, out)
	}
	m3Run(ctx, t, f.controller, "sudo ufw allow "+m3PanelPort+"/tcp")
	m3Run(ctx, t, f.controller, "sudo systemctl daemon-reload && sudo systemctl restart "+update.ControllerUnit)
	err := eventually(ctx, time.Minute, func(ctx context.Context) error {
		_, err := f.scanner.Run(ctx, fmt.Sprintf("timeout 5 bash -c '</dev/tcp/%s/%s'", targetIPv4, m3PanelPort))
		return err
	})
	if err != nil {
		journal, _ := f.controller.Run(ctx, "sudo journalctl -u "+update.ControllerUnit+" --no-pager -n 40")
		t.Fatalf("the https listener %s does not answer from the scanner: %v\n%s", m3PanelAddr, err, journal)
	}
	api, err := m3TLSAPI("https://"+m3PanelAddr, f.scanner.DialContext, m3WebCA(ctx, t, f.controller))
	if err != nil {
		t.Fatal(err)
	}
	if err := api.Login(ctx, adminName, f.password, nextCode(ctx, t, f)); err != nil {
		t.Fatal(err)
	}
	st := m3WaitStatus(ctx, t, api, f.localID, 2*time.Minute, m3Healthy)
	r, ok := m3FindRule(st.Rules, m3ManualPort(m3PanelPort, ""))
	if !ok {
		t.Fatalf("the controller host has no manual allow rule for %s/tcp: %s", m3PanelPort, m3JSON(st.Rules))
	}
	m3RequireCut(ctx, t, api, f.localID, "firewall.rule.delete", map[string]any{"id": r.ID, "manual": true},
		"panel from "+scannerIPv4+" to "+m3PanelAddr)
}

// m3RequirePending fails the test unless ch is pending.
func m3RequirePending(t *testing.T, ch Change, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if ch.State != core.StatePending {
		t.Fatalf("change %s is %s, want %s: %s", ch.ID, ch.State, core.StatePending, m3JSON(ch.Error))
	}
}

// m3RequireGone waits until the agent host is healthy again and requires
// that no rule for port is left.
func m3RequireGone(ctx context.Context, t *testing.T, f *fleet, port int) m3Status {
	t.Helper()
	st := m3WaitStatus(ctx, t, f.api, f.agentID, 2*time.Minute, m3Healthy)
	if m3HasPort(st.Rules, strconv.Itoa(port)) {
		t.Errorf("a rule for port %d is still there after the revert: %s", port, m3JSON(st.Rules))
	}
	return st
}

// m3RevertOnDisconnect applies a change through a client of its own that
// then goes away; the agent's timer reverts the change without it.
func m3RevertOnDisconnect(ctx context.Context, t *testing.T, f *fleet) {
	m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, m3Healthy)
	client := NewAPI(apiURL, f.controller.DialContext)
	if err := client.Login(ctx, adminName, f.password, nextCode(ctx, t, f)); err != nil {
		t.Fatal(err)
	}
	ch, err := client.m3Apply(ctx, f.agentID, "firewall.rule.add", m3PortRule(8091), 10)
	m3RequirePending(t, ch, err)
	// The applying client is never used again: each of its requests had a
	// connection of its own, so nothing of it is left open.
	m3WaitChange(ctx, t, f.api, ch.ID, core.StateReverted, 2*time.Minute)
	m3RequireGone(ctx, t, f, 8091)
}

// m3RevertOnReboot reboots the agent host while a change is pending; the
// change is reverted once the host is back.
func m3RevertOnReboot(ctx context.Context, t *testing.T, f *fleet) {
	m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, m3Healthy)
	ch, err := f.api.m3Apply(ctx, f.agentID, "firewall.rule.add", m3PortRule(8092), 120)
	m3RequirePending(t, ch, err)
	if err := f.agent.Reboot(ctx); err != nil {
		t.Fatal(err)
	}
	waitHosts(ctx, t, f, 3*time.Minute, hostsAt(versionN, f.agentID))
	m3WaitChange(ctx, t, f.api, ch.ID, core.StateReverted, 4*time.Minute)
	m3RequireGone(ctx, t, f, 8092)
}

// m3MismatchWrapper replaces /usr/sbin/ufw: once /run/m3-mismatch exists,
// the next ufw reload also adds a rule no prediction holds, so the change
// that reloads fails verify.
const m3MismatchWrapper = `#!/bin/sh
/usr/sbin/ufw.distrib "$@"
rc=$?
case " $* " in
*" reload "*)
	if [ -e /run/m3-mismatch ]; then
		rm -f /run/m3-mismatch
		iptables -A ufw-user-input -s 192.0.2.99/32 -j ACCEPT
	fi
	;;
esac
exit $rc
`

// m3PredictionMismatch makes the loaded state differ from the prediction
// right after a change reloads UFW: verify fails with
// firewall.prediction_mismatch and the change is reverted, which also
// removes the stray rule.
func m3PredictionMismatch(ctx context.Context, t *testing.T, f *fleet) {
	m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, m3Healthy)
	t.Cleanup(func() {
		// Only a diverted ufw has a wrapper to remove; without the diversion
		// /usr/sbin/ufw is UFW itself.
		_, _ = f.agent.Run(ctx, "sudo sh -c 'rm -f /run/m3-mismatch; if [ -e /usr/sbin/ufw.distrib ]; then "+
			"rm -f /usr/sbin/ufw && dpkg-divert --local --rename --remove /usr/sbin/ufw; fi; ufw reload'")
	})
	m3Run(ctx, t, f.agent, "sudo dpkg-divert --local --rename --add /usr/sbin/ufw")
	if out, err := f.agent.RunInput(ctx, "sudo tee /usr/sbin/ufw >/dev/null && sudo chmod 0755 /usr/sbin/ufw", strings.NewReader(m3MismatchWrapper)); err != nil {
		t.Fatalf("install the ufw wrapper: %v\n%s", err, out)
	}
	m3Run(ctx, t, f.agent, "sudo touch /run/m3-mismatch")
	ch, err := f.api.m3Apply(ctx, f.agentID, "firewall.rule.add", m3PortRule(8093), 60)
	if err != nil {
		t.Fatal(err)
	}
	if ch.State != core.StateReverted || ch.Error == nil || ch.Error.Code != string(errcode.FirewallPredictionMismatch) {
		t.Fatalf("change %s is %s with error %s; want it reverted with %s", ch.ID, ch.State, m3JSON(ch.Error), errcode.FirewallPredictionMismatch)
	}
	m3RequireGone(ctx, t, f, 8093)
	if out := m3Run(ctx, t, f.agent, "sudo iptables -S ufw-user-input"); strings.Contains(out, "192.0.2.99") {
		t.Errorf("the stray rule survived the revert:\n%s", out)
	}
}

// m3ManualEditSurvives adds a rule by hand while a change is pending; the
// revert takes back only the change's own rule.
func m3ManualEditSurvives(ctx context.Context, t *testing.T, f *fleet) {
	m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, m3Healthy)
	ch, err := f.api.m3Apply(ctx, f.agentID, "firewall.rule.add", m3PortRule(8094), 120)
	m3RequirePending(t, ch, err)
	t.Cleanup(func() { _, _ = f.agent.Run(ctx, "sudo ufw delete allow 8095/tcp") })
	m3Run(ctx, t, f.agent, "sudo ufw allow 8095/tcp")
	if _, err := f.api.m3Revert(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	m3WaitChange(ctx, t, f.api, ch.ID, core.StateReverted, 2*time.Minute)
	st := m3RequireGone(ctx, t, f, 8094)
	if _, ok := m3FindRule(st.Rules, m3ManualPort("8095", "")); !ok {
		t.Errorf("the rule added by hand during the pending window is gone: %s", m3JSON(st.Rules))
	}
}

// m3DisableReverts leaves firewall.state.disable unconfirmed; the revert
// enables UFW again.
func m3DisableReverts(ctx context.Context, t *testing.T, f *fleet) {
	m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, m3Healthy)
	ch, err := f.api.m3Apply(ctx, f.agentID, "firewall.state.disable", map[string]any{}, 10)
	m3RequirePending(t, ch, err)
	m3WaitChange(ctx, t, f.api, ch.ID, core.StateReverted, 2*time.Minute)
	m3WaitStatus(ctx, t, f.api, f.agentID, 2*time.Minute, m3Healthy)
	if out := m3Run(ctx, t, f.agent, "sudo ufw status"); !strings.HasPrefix(out, "Status: active") {
		t.Errorf("ufw status after the revert:\n%s", out)
	}
}

// m3StrayRule is the rule m3RepairReverts loads by hand into a UFW chain,
// which puts the loaded state out of sync with UFW's files.
const m3StrayRule = "ufw-user-input -s 192.0.2.98/32 -j ACCEPT"

// m3xPutOutOfSync loads m3StrayRule by hand into a UFW chain of the agent
// host and waits until the host reports it out of sync; the cleanup loads
// UFW's files again.
func m3xPutOutOfSync(ctx context.Context, t *testing.T, f *fleet) {
	t.Helper()
	m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, m3Healthy)
	t.Cleanup(func() { _, _ = f.agent.Run(ctx, "sudo ufw reload") })
	m3Run(ctx, t, f.agent, "sudo iptables -A "+m3StrayRule)
	m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, m3xOutOfSync)
}

// m3RepairReverts puts the loaded state out of sync, requires an ordinary
// change to be refused, then leaves firewall.state.repair unconfirmed: the
// revert loads the out-of-sync state again.
func m3RepairReverts(ctx context.Context, t *testing.T, f *fleet) {
	m3xPutOutOfSync(ctx, t, f)
	// The refusal is a plan whose guard fails and that an override cannot pass.
	p, err := f.api.m3Plan(ctx, f.agentID, "firewall.rule.add", m3PortRule(8097))
	overridden := m3PortRule(8097)
	overridden["overrides"] = []string{"firewall.lockout"}
	po, errO := f.api.m3Plan(ctx, f.agentID, "firewall.rule.add", overridden)
	if err := m3xOutOfSyncRefusal(p, err, po, errO); err != nil {
		t.Fatalf("an ordinary change on an out-of-sync host: %v", err)
	}
	ch, err := f.api.m3Apply(ctx, f.agentID, "firewall.state.repair", map[string]any{}, 30)
	m3RequirePending(t, ch, err)
	if out := m3Run(ctx, t, f.agent, "sudo iptables -S ufw-user-input"); strings.Contains(out, "192.0.2.98") {
		t.Errorf("the repair left the stray rule loaded:\n%s", out)
	}
	m3WaitChange(ctx, t, f.api, ch.ID, core.StateReverted, 2*time.Minute)
	if out := m3Run(ctx, t, f.agent, "sudo iptables -S ufw-user-input"); !strings.Contains(out, "192.0.2.98") {
		t.Errorf("the revert did not load the out-of-sync state again:\n%s", out)
	}
	m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, m3xOutOfSync)
}

// m3RepairRevertsReboot puts the loaded state out of sync and reboots the
// agent host while firewall.state.repair is unconfirmed. UFW loads its files
// at boot, so the stray rule is gone until the agent, once the host is back,
// reverts the change and loads the out-of-sync state again.
func m3RepairRevertsReboot(ctx context.Context, t *testing.T, f *fleet) {
	m3xPutOutOfSync(ctx, t, f)
	ch, err := f.api.m3Apply(ctx, f.agentID, "firewall.state.repair", map[string]any{}, 120)
	m3RequirePending(t, ch, err)
	if err := f.agent.Reboot(ctx); err != nil {
		t.Fatal(err)
	}
	waitHosts(ctx, t, f, 3*time.Minute, hostsAt(versionN, f.agentID))
	m3WaitChange(ctx, t, f.api, ch.ID, core.StateReverted, 4*time.Minute)
	if out := m3Run(ctx, t, f.agent, "sudo iptables -S ufw-user-input"); !strings.Contains(out, "192.0.2.98") {
		t.Errorf("the revert after the reboot did not load the out-of-sync state again:\n%s", out)
	}
	m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, m3xOutOfSync)
}

// m3RequireUnsupported plans action on the agent host and requires the
// plan to be refused with firewall.unsupported_configuration naming want.
func m3RequireUnsupported(ctx context.Context, t *testing.T, f *fleet, action string, input any, want string) {
	t.Helper()
	p, err := f.api.m3Plan(ctx, f.agentID, action, input)
	var apiErr *APIError
	switch {
	case err == nil:
		t.Fatalf("%s planned on an unsupported configuration: %s %s", action, p.m3DiffText(), m3JSON(p.Guards))
	case !errors.As(err, &apiErr):
		t.Fatal(err)
	case apiErr.Code != string(errcode.FirewallUnsupportedConfiguration):
		t.Fatalf("%s was refused with %s (%s); want %s", action, apiErr.Code, apiErr.Detail, errcode.FirewallUnsupportedConfiguration)
	case !strings.Contains(apiErr.Detail, want):
		t.Errorf("%s was refused with %q; want it to name %q", action, apiErr.Detail, want)
	}
}

// m3UnsupportedNamed accepts a status that reports an unsupported
// configuration with a reason naming want.
func m3UnsupportedNamed(want string) func(m3Status) error {
	return func(st m3Status) error {
		if st.Supported || !slices.ContainsFunc(st.Unsupported, func(r string) bool { return strings.Contains(r, want) }) {
			return fmt.Errorf("supported %t, reasons %q; want a reason naming %q", st.Supported, st.Unsupported, want)
		}
		return nil
	}
}

// m3UnsupportedBeforeInit makes /etc/ufw/before.init executable, which UFW
// would run at start: the host is unsupported and every change refused.
func m3UnsupportedBeforeInit(ctx context.Context, t *testing.T, f *fleet) {
	m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, m3Healthy)
	mode := strings.TrimSpace(m3Run(ctx, t, f.agent, "sudo stat -c %a /etc/ufw/before.init"))
	t.Cleanup(func() { _, _ = f.agent.Run(ctx, "sudo chmod "+mode+" /etc/ufw/before.init") })
	m3Run(ctx, t, f.agent, "sudo chmod 0755 /etc/ufw/before.init")
	m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, m3UnsupportedNamed("before.init"))
	m3RequireUnsupported(ctx, t, f, "firewall.rule.add", m3PortRule(8098), "before.init")
}

// m3UnsupportedNftables adds a native nftables base chain with a rule,
// which the firewall cannot evaluate: the host is unsupported.
func m3UnsupportedNftables(ctx context.Context, t *testing.T, f *fleet) {
	m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, m3Healthy)
	t.Cleanup(func() { _, _ = f.agent.Run(ctx, "sudo nft delete table inet m3") })
	m3Run(ctx, t, f.agent, "sudo nft add table inet m3 && "+
		"sudo nft 'add chain inet m3 input { type filter hook input priority 0 ; policy accept ; }' && "+
		"sudo nft add rule inet m3 input tcp dport 9999 drop")
	m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, m3UnsupportedNamed("m3"))
	m3RequireUnsupported(ctx, t, f, "firewall.rule.add", m3PortRule(8099), "m3")
}

// m3StoppedIPv6No disables UFW and sets IPV6=no, as an operator preparing
// to enable it would; the cleanup sets IPV6=yes and enables UFW again.
func m3StoppedIPv6No(ctx context.Context, t *testing.T, f *fleet) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = f.agent.Run(ctx, "sudo sed -i 's/^IPV6=.*/IPV6=yes/' /etc/default/ufw && sudo ufw --force enable")
		m3WaitStatus(ctx, t, f.api, f.agentID, 2*time.Minute, m3Healthy)
	})
	m3Run(ctx, t, f.agent, "sudo ufw disable && sudo sed -i 's/^IPV6=.*/IPV6=no/' /etc/default/ufw")
	m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, func(st m3Status) error {
		if !st.Supported || !st.InSync || st.Active {
			return fmt.Errorf("supported %t (%v), in sync %t (%v), active %t; want supported, in sync and inactive",
				st.Supported, st.Unsupported, st.InSync, st.OutOfSync, st.Active)
		}
		return nil
	})
}

// m3IPv6NoForeignRules: starting UFW with IPV6=no would remove every IPv6
// filter chain UFW did not create, so state.enable is refused while one
// is loaded.
func m3IPv6NoForeignRules(ctx context.Context, t *testing.T, f *fleet) {
	m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, m3Healthy)
	m3StoppedIPv6No(ctx, t, f)
	// Registered after m3StoppedIPv6No's cleanup, so it runs before UFW is
	// enabled again.
	t.Cleanup(func() {
		_, _ = f.agent.Run(ctx, "sudo ip6tables -D INPUT -j m3-foreign; sudo ip6tables -F m3-foreign; sudo ip6tables -X m3-foreign")
	})
	m3Run(ctx, t, f.agent, "sudo ip6tables -N m3-foreign && sudo ip6tables -A m3-foreign -j RETURN && sudo ip6tables -I INPUT -j m3-foreign")
	m3RequireUnsupported(ctx, t, f, "firewall.state.enable", map[string]any{}, "m3-foreign")
}

// m3ClearForeignIPv6 deletes every IPv6 filter chain UFW did not create,
// and the built-in chains' jumps into them, once Docker is stopped.
const m3ClearForeignIPv6 = `set -e
ip6tables -S | awk '$1 == "-A" && ($2 == "INPUT" || $2 == "FORWARD" || $2 == "OUTPUT") && $0 !~ / -j ufw6-/ { sub(/^-A/, "-D"); print }' |
	while read -r rule; do eval "ip6tables $rule"; done
for c in $(ip6tables -S | awk '$1 == "-N" && $2 !~ /^ufw6-/ { print $2 }'); do ip6tables -F "$c"; done
for c in $(ip6tables -S | awk '$1 == "-N" && $2 !~ /^ufw6-/ { print $2 }'); do ip6tables -X "$c"; done
`

// m3IPv6SSHSession holds an SSH connection from the scanner over IPv6;
// with IPV6=no, enabling UFW would load an IPv6 table that drops it, so
// the guard refuses state.enable naming that session.
func m3IPv6SSHSession(ctx context.Context, t *testing.T, f *fleet) {
	m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, m3Healthy)
	t.Cleanup(func() {
		m3Release(ctx, f.scanner, "m3-ssh6")
		_, _ = f.agent.Run(ctx, "sudo systemctl start docker")
	})
	m3Run(ctx, t, f.agent, "sudo systemctl stop docker.socket docker")
	if out, err := f.agent.RunInput(ctx, "sudo sh -s", strings.NewReader(m3ClearForeignIPv6)); err != nil {
		t.Fatalf("clear the foreign IPv6 chains: %v\n%s", err, out)
	}
	m3StoppedIPv6No(ctx, t, f)
	m3Hold(ctx, t, f.scanner, "m3-ssh6", agentIPv6, 22)
	m3WaitSession(ctx, t, f.agent, 22, scannerIPv6)
	m3RequireCut(ctx, t, f.api, f.agentID, "firewall.state.enable", map[string]any{}, "ssh from "+scannerIPv6+" to ")
}

// m3AsymmetricRoute holds an SSH connection from the scanner arriving on
// dpinet while the route back to the scanner leaves through dppriv. With
// loose reverse-path filtering every interface is eligible, so the guard
// protects the session on dpinet, where it really arrives.
func m3AsymmetricRoute(ctx context.Context, t *testing.T, f *fleet) {
	st := m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, m3Healthy)
	before := strings.Fields(m3Run(ctx, t, f.agent, "sysctl -n net.ipv4.conf.all.rp_filter net.ipv4.conf.dpinet.rp_filter"))
	if len(before) != 2 {
		t.Fatalf("rp_filter values %q", before)
	}
	t.Cleanup(func() {
		m3Release(ctx, f.scanner, "m3-ssh4")
		_, _ = f.agent.Run(ctx, "sudo ip route del "+scannerIPv4+"/32 dev dppriv; sudo sysctl -w net.ipv4.conf.all.rp_filter="+before[0]+
			" net.ipv4.conf.dpinet.rp_filter="+before[1])
	})
	m3Run(ctx, t, f.agent, "sudo sysctl -w net.ipv4.conf.all.rp_filter=2 net.ipv4.conf.dpinet.rp_filter=2")
	m3Hold(ctx, t, f.scanner, "m3-ssh4", agentIPv4, 22)
	m3WaitSession(ctx, t, f.agent, 22, scannerIPv4)
	m3Run(ctx, t, f.agent, "sudo ip route add "+scannerIPv4+"/32 dev dppriv")
	r, ok := m3FindRule(st.Rules, m3ManualPort("22", ""))
	if !ok {
		t.Fatalf("the agent host has no manual allow rule for 22/tcp: %s", m3JSON(st.Rules))
	}
	m3RequireCut(ctx, t, f.api, f.agentID, "firewall.rule.delete", map[string]any{"id": r.ID, "manual": true},
		"ssh from "+scannerIPv4+" to "+net.JoinHostPort(agentIPv4, "22")+" on dpinet")
}

// m3SysctlRevert makes UFW's sysctl.conf change a supported key: the plan
// shows it, the change applies it, and the revert writes the old value
// back.
func m3SysctlRevert(ctx context.Context, t *testing.T, f *fleet) {
	const key = "net.ipv4.conf.all.accept_source_route"
	m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, m3Healthy)
	read := func() string { return strings.TrimSpace(m3Run(ctx, t, f.agent, "sysctl -n "+key)) }
	if v := read(); v != "0" {
		t.Fatalf("%s is %s before the scenario, want 0", key, v)
	}
	t.Cleanup(func() {
		_, _ = f.agent.Run(ctx, "sudo cp -p /root/m3-sysctl.conf /etc/ufw/sysctl.conf && sudo rm -f /root/m3-sysctl.conf; sudo sysctl -w "+key+"=0")
	})
	m3Run(ctx, t, f.agent, "sudo cp -p /etc/ufw/sysctl.conf /root/m3-sysctl.conf && "+
		"echo net/ipv4/conf/all/accept_source_route=1 | sudo tee -a /etc/ufw/sysctl.conf >/dev/null")
	p, err := f.api.m3Plan(ctx, f.agentID, "firewall.rule.add", m3PortRule(8096))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.m3DiffText(), "accept_source_route") {
		t.Errorf("the plan does not show the sysctl change:\n%s", p.m3DiffText())
	}
	ch, err := f.api.m3Apply(ctx, f.agentID, "firewall.rule.add", m3PortRule(8096), 30)
	m3RequirePending(t, ch, err)
	if v := read(); v != "1" {
		t.Errorf("%s is %s while the change is pending, want 1", key, v)
	}
	m3WaitChange(ctx, t, f.api, ch.ID, core.StateReverted, 2*time.Minute)
	if v := read(); v != "0" {
		t.Errorf("%s is %s after the revert, want 0", key, v)
	}
	m3RequireGone(ctx, t, f, 8096)
}

// m3GoldenRecheck replays the golden "rules" scenario on the agent host's
// own UFW and requires the lines UFW loads to equal the golden capture of
// this OS, which the firewall's prediction is tested against.
func m3GoldenRecheck(ctx context.Context, t *testing.T, f *fleet) {
	dir := filepath.Join("..", "..", "internal", "modules", "firewall", "testdata", "golden", f.os.ID, "rules")
	capture := os.DirFS(dir)
	cmds, err := fs.ReadFile(capture, "commands.txt")
	if err != nil {
		t.Fatalf("the golden capture of %s: %v", f.os.ID, err)
	}
	m3Run(ctx, t, f.agent, "sudo ufw --force reset")
	for line := range strings.SplitSeq(strings.TrimSpace(string(cmds)), "\n") {
		m3Run(ctx, t, f.agent, "sudo sh -c "+m3Quote(line))
	}
	for _, fam := range []struct{ name, save string }{{"v4", "iptables-save"}, {"v6", "ip6tables-save"}} {
		golden, err := fs.ReadFile(capture, "save/nft-"+fam.name+"-filter.txt")
		if err != nil {
			t.Fatal(err)
		}
		want := m3UFWLines(string(golden))
		got := m3UFWLines(m3Run(ctx, t, f.agent, "sudo "+fam.save+" -t filter"))
		if !slices.Equal(got, want) {
			t.Errorf("%s: the %s filter lines UFW loaded differ from the golden capture %s (regenerate it with capture.sh if UFW changed):\ngot\n%s\nwant\n%s",
				f.os.ID, fam.name, dir, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
}
