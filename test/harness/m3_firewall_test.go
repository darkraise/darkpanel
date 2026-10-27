//go:build harness

package harness

import (
	"context"
	"flag"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

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
