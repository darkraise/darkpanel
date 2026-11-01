//go:build harness

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/tailscale"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

var m5Flag = flag.Bool("m5", false, "run the M5 node scenarios; needs -os and -m2, and each M5 test is a go test run of its own")

// m5Deadline bounds TestM5Node, a go test run of its own that needs -os, -m2
// and -m5. The vm-m5 job runs it in a step whose -timeout is 70 minutes: this
// deadline plus 10 minutes for the cleanups that stop the VMs.
const m5Deadline = 60 * time.Minute

// VM is the name Contracts "Harness M5" gives to *vm.VM.
type VM = vm.VM

// ChangeView is the name Contracts "Harness M5" gives to the harness's change type.
type ChangeView = Change

// m5Node is what the helpers that take only the VM know about its lab.
type m5Node struct {
	ctx    context.Context // one per lab
	api    *API
	hostID string
	os     OS
}

// m5Nodes maps the agent *vm.VM of each running M5 test to its m5Node.
var m5Nodes sync.Map

// m5Register keeps n under v until t ends.
func m5Register(t *testing.T, v *vm.VM, n m5Node) {
	t.Helper()
	m5Nodes.Store(v, n)
	t.Cleanup(func() { m5Nodes.Delete(v) })
}

// m5Lookup returns the node registered for v.
func m5Lookup(t *testing.T, v *vm.VM) m5Node {
	t.Helper()
	n, ok := m5Nodes.Load(v)
	if !ok {
		t.Fatal("harness: the VM is not the agent of an M5 lab; get it from m5Lab")
	}
	return n.(m5Node)
}

// m5Configs returns the controller, the agent and the scanner of the M2 fleet
// on the lab's own links.
func m5Configs(lab *Lab, o OS, targetImg, scannerImg string, key ssh.Signer) []vm.Config {
	target := func(role string, host int, inet []string, priv string) vm.Config {
		return vm.Config{
			Name: lab.VMName(role), Image: targetImg, MemoryMiB: 2048, CPUs: 2,
			MgmtMAC: lab.MAC(NICMgmt, host), InetMAC: lab.MAC(NICInet, host), PrivMAC: lab.MAC(NICPriv, host),
			InetAddrs: inet, PrivAddrs: []string{priv + "/24"},
			McastPort: lab.McastPort, PrivMcastPort: lab.PrivMcastPort,
			Provision: TargetScript(o), Key: key,
		}
	}
	return []vm.Config{
		target("controller", 10, []string{targetIPv4 + "/15", targetIPv6 + "/64"}, controllerPriv),
		target("agent", 11, []string{agentIPv4 + "/15", agentIPv6 + "/64"}, agentPriv),
		{
			Name: lab.VMName("scanner"), Image: scannerImg, MemoryMiB: 2048, CPUs: 1,
			MgmtMAC: lab.MAC(NICMgmt, 20), InetMAC: lab.MAC(NICInet, 20),
			InetAddrs: []string{scannerIPv4 + "/15", scannerIPv6 + "/64"},
			McastPort: lab.McastPort, Provision: ScannerScript(), Key: key,
		},
	}
}

// m5Lab boots the controller, the agent and the scanner of -os on a lab of
// their own, installs the harness build, enrolls the agent, and returns the
// lab and the agent. It skips the test unless -os, -m2 and -m5 are given.
func m5Lab(t *testing.T) (*Lab, *vm.VM) {
	t.Helper()
	if *osFlag == "" || *m2Flag == "" || !*m5Flag {
		t.Skip("no -os, -m2 or -m5 given: the VM tests run in .github/workflows/harness.yml")
	}
	ctx, cancel := context.WithTimeout(context.Background(), m5Deadline)
	t.Cleanup(cancel)
	lab := NewLab(t)
	o, targetImg, scannerImg := harnessImages(ctx, t)
	key, err := vm.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	vms := lab.Boot(ctx, t, m5Configs(lab, o, targetImg, scannerImg, key))
	f := &fleet{os: o, controller: vms[0], agent: vms[1], scanner: vms[2]}
	serveReleases(ctx, t, f)
	scenarioInstall(ctx, t, f)
	m5Register(t, f.agent, m5Node{ctx: ctx, api: f.api, hostID: f.agentID, os: o})
	return lab, f.agent
}

// m5ReadStatus reads tailscale.status of the node's host.
func m5ReadStatus(ctx context.Context, n m5Node) (tailscale.Status, error) {
	var st tailscale.Status
	err := n.api.Resource(ctx, n.hostID, "tailscale.status", &st)
	return st, err
}

// m5Status reads tailscale.status of v's host and fails the test on an error.
func m5Status(t *testing.T, v *vm.VM) tailscale.Status {
	t.Helper()
	n := m5Lookup(t, v)
	st, err := m5ReadStatus(n.ctx, n)
	if err != nil {
		t.Fatalf("tailscale.status: %v", err)
	}
	return st
}

// m5Terminal reports whether a change in state will not move on its own; a
// confirmed change is settled too.
func m5Terminal(state string) bool {
	return m4xTerminal(state) || state == core.StateConfirmed
}

// m5Settle polls change id until it reaches a state m5Terminal accepts.
func m5Settle(ctx context.Context, api *API, id string) (Change, error) {
	var ch Change
	err := eventually(ctx, m4SettleTimeout, func(ctx context.Context) error {
		c, err := api.m3Change(ctx, id)
		if err != nil {
			return err
		}
		if !m5Terminal(c.State) {
			return fmt.Errorf("harness: change %s is %s", id, c.State)
		}
		ch = c
		return nil
	})
	return ch, err
}

// m5Apply plans version 1 of action on v's host, applies it confirmed in the
// same request, waits for the change to settle and fails the test unless it
// completed or was confirmed.
func m5Apply(t *testing.T, v *vm.VM, action string, input any) ChangeView {
	t.Helper()
	n := m5Lookup(t, v)
	p, err := m4Plan(n.ctx, n.api, n.hostID, action, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range p.Guards {
		if !g.OK {
			t.Fatalf("plan %s: guard %s failed: %s", action, g.ID, g.Message)
		}
	}
	path, err := actionPath(n.hostID, action+"/v1", "apply")
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"planId": p.PlanID, "confirmNow": true}
	if p.Confirmation != "" {
		body["confirmation"] = p.Confirmation
	}
	var applied Change
	if err := n.api.do(n.ctx, http.MethodPost, path, body, &applied); err != nil {
		t.Fatalf("apply %s: %v", action, err)
	}
	ch, err := m5Settle(n.ctx, n.api, applied.ID)
	if err != nil {
		t.Fatalf("change %s of %s: %v", applied.ID, action, err)
	}
	if ch.State != core.StateCompleted && ch.State != core.StateConfirmed {
		t.Fatalf("%s ended %s, want %s or %s: error %s", action, ch.State, core.StateCompleted, core.StateConfirmed, m3JSON(ch.Error))
	}
	return ch
}

// m5Prefs is the part of tailscaled's preferences the scenarios compare, by
// the member names of tailscale debug prefs; RouteAll is accept-routes.
type m5Prefs struct{ WantRunning, RouteAll, ShieldsUp, RunSSH bool }

// m5xParsePrefs decodes the output of tailscale debug prefs.
func m5xParsePrefs(out string) (m5Prefs, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return m5Prefs{}, fmt.Errorf("harness: tailscale debug prefs is not a JSON object: %w", err)
	}
	var p m5Prefs
	for _, m := range []struct {
		name string
		dst  *bool
	}{{"WantRunning", &p.WantRunning}, {"RouteAll", &p.RouteAll}, {"ShieldsUp", &p.ShieldsUp}, {"RunSSH", &p.RunSSH}} {
		b, ok := raw[m.name]
		if !ok {
			return m5Prefs{}, fmt.Errorf("harness: tailscale debug prefs has no member %s", m.name)
		}
		if err := json.Unmarshal(b, m.dst); err != nil {
			return m5Prefs{}, fmt.Errorf("harness: tailscale debug prefs member %s is not a bool: %w", m.name, err)
		}
	}
	return p, nil
}

// m5HostPrefs reads the preferences tailscaled holds on v.
func m5HostPrefs(ctx context.Context, t *testing.T, v *vm.VM) m5Prefs {
	t.Helper()
	p, err := m5xParsePrefs(m3Run(ctx, t, v, "sudo tailscale debug prefs"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// m5xPinned is the version of the tailscale package the image of o pins.
func m5xPinned(o OS) (string, error) {
	for _, p := range o.Packages {
		if p.Name == "tailscale" {
			return p.Version, nil
		}
	}
	return "", fmt.Errorf("harness: %s pins no tailscale package", o.ID)
}

// m5xLoggedOut reports every way st is not that of a logged-out node running a
// version beginning with version; an empty version accepts any non-empty one.
func m5xLoggedOut(st tailscale.Status, version string) error {
	var errs []error
	if st.BackendState != "NeedsLogin" {
		errs = append(errs, fmt.Errorf("backend state %q, want NeedsLogin", st.BackendState))
	}
	if st.Self != nil {
		errs = append(errs, fmt.Errorf("self is %+v, want null while logged out", *st.Self))
	}
	if st.Tailnet != "" {
		errs = append(errs, fmt.Errorf("tailnet %q, want none", st.Tailnet))
	}
	if len(st.Peers) > 0 {
		errs = append(errs, fmt.Errorf("%d peers, want none", len(st.Peers)))
	}
	if st.Version == "" || !strings.HasPrefix(st.Version, version) {
		errs = append(errs, fmt.Errorf("version %q, want one beginning %q", st.Version, version))
	}
	return errors.Join(errs...)
}

// m5xHasSTUN reports whether some server of r answers STUN.
func m5xHasSTUN(r *tslocal.DERPRegion) bool {
	return slices.ContainsFunc(r.Nodes, func(n *tslocal.DERPNode) bool {
		return n != nil && n.STUNPort != -1 && (n.IPv4 != "" || n.IPv6 != "")
	})
}

// m5xMeasurement checks that m is either a median without a reason or a reason
// without a median.
func m5xMeasurement(m tailscale.Measurement) error {
	if m.Available && (m.MedianMs <= 0 || m.Reason != "") {
		return fmt.Errorf("available with median %v ms and reason %q", m.MedianMs, m.Reason)
	}
	if !m.Available && (m.MedianMs != 0 || m.Reason == "") {
		return fmt.Errorf("unavailable with median %v ms and reason %q", m.MedianMs, m.Reason)
	}
	return nil
}

// m5xCheckLatency checks l against the DERP map m it was measured from.
func m5xCheckLatency(l tailscale.Latency, m tslocal.DERPMap) error {
	want := make([]int, 0, len(m.Regions))
	for id := range m.Regions {
		want = append(want, id)
	}
	slices.Sort(want)
	got := make([]int, 0, len(l.Regions))
	for _, r := range l.Regions {
		got = append(got, r.RegionID)
	}
	if !slices.Equal(got, want) {
		return fmt.Errorf("tailscale.latency lists regions %v, want the DERP map's %v in ascending order", got, want)
	}
	var errs []error
	for _, r := range l.Regions {
		mr := m.Regions[r.RegionID]
		if mr == nil {
			continue
		}
		if r.Code != mr.RegionCode || r.Name != mr.RegionName {
			errs = append(errs, fmt.Errorf("region %d is %q %q, the DERP map says %q %q", r.RegionID, r.Code, r.Name, mr.RegionCode, mr.RegionName))
		}
		if err := m5xMeasurement(r.STUN); err != nil {
			errs = append(errs, fmt.Errorf("region %d STUN: %w", r.RegionID, err))
		}
		if err := m5xMeasurement(r.HTTPS); err != nil {
			errs = append(errs, fmt.Errorf("region %d HTTPS: %w", r.RegionID, err))
		}
		if r.STUN.Available && !m5xHasSTUN(mr) {
			errs = append(errs, fmt.Errorf("region %d reports STUN latency but has no STUN endpoint", r.RegionID))
		}
	}
	return errors.Join(errs...)
}

// m5xAnyMeasured reports whether some region of l has a STUN or HTTPS latency.
func m5xAnyMeasured(l tailscale.Latency) bool {
	return slices.ContainsFunc(l.Regions, func(r tailscale.RegionLatency) bool {
		return r.STUN.Available || r.HTTPS.Available
	})
}

// m5xSourceLine is the line of tailscale.list for a host of family and codename.
func m5xSourceLine(family, codename string) string {
	return "deb [signed-by=/usr/share/keyrings/tailscale-archive-keyring.gpg] https://pkgs.tailscale.com/stable/" + family + " " + codename + " main\n"
}

// m5xLinkGuard returns the tailscale.link guard of a plan.
func m5xLinkGuard(guards []core.Guard) (core.Guard, error) {
	for _, g := range guards {
		if g.ID == "tailscale.link" {
			return g, nil
		}
	}
	return core.Guard{}, fmt.Errorf("harness: the plan has no tailscale.link guard: %s", m3JSON(guards))
}

// m5PlanLink plans version 1 of action on the node's host, applies nothing,
// and returns the tailscale.link guard of the plan.
func m5PlanLink(ctx context.Context, n m5Node, action string, input any) (core.Guard, error) {
	p, err := m4Plan(ctx, n.api, n.hostID, action, input)
	if err != nil {
		return core.Guard{}, err
	}
	return m5xLinkGuard(p.Guards)
}
