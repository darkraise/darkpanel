//go:build harness

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/tailscale"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

// m5PendingSeconds is the revert timeout of the changes the scenarios revert
// by hand: long enough that the timer never fires first.
const m5PendingSeconds = 120

// TestM5Node is the node half of M5 (spec, Verification: the harness node tests
// that need no tailnet) on one agent whose tailscaled is installed and logged
// out. The workflow selects it with -run, -m2 and -m5.
func TestM5Node(t *testing.T) {
	lab, agent := m5Lab(t)
	t.Logf("lab %s", lab.Slug)
	steps := []struct {
		name string
		gate bool // later steps need this one
		run  func(t *testing.T, v *vm.VM)
	}{
		{"status-logged-out", true, m5NodeStatus},
		{"prefs-patch-and-revert", false, m5NodePrefs},
		{"guard-runssh-refusal", false, m5NodeRunSSH},
		{"latency-against-derp-map", false, m5NodeLatency},
		// The install scenario removes the package the image pinned, so it runs last.
		{"package-install", false, m5NodeInstall},
	}
	for _, s := range steps {
		if !t.Run(s.name, func(t *testing.T) { s.run(t, agent) }) && s.gate {
			return
		}
	}
}

// m5NodeStatus: the logged-out node answers tailscale.status as NeedsLogin
// with no self, tailnet or peers, running the version the image pins.
func m5NodeStatus(t *testing.T, v *vm.VM) {
	n := m5Lookup(t, v)
	pinned, err := m5xPinned(n.os)
	if err != nil {
		t.Fatal(err)
	}
	var st tailscale.Status
	err = eventually(n.ctx, 2*time.Minute, func(ctx context.Context) error {
		var err error
		if st, err = m5ReadStatus(ctx, n); err != nil {
			return err
		}
		return m5xLoggedOut(st, pinned)
	})
	if err != nil {
		t.Fatalf("tailscale.status of a logged-out node: %v", err)
	}
}

// m5NodePrefs: prefs.set changes only the fields it is given. A change left
// unconfirmed and reverted restores the old values; a confirmed one keeps its
// own and leaves the others alone, and the output names exactly the fields set.
func m5NodePrefs(t *testing.T, v *vm.VM) {
	n := m5Lookup(t, v)
	ctx := n.ctx
	before := m5HostPrefs(ctx, t, v)

	ch, err := n.api.m3Apply(ctx, n.hostID, "tailscale.prefs.set", map[string]any{"acceptRoutes": !before.RouteAll, "shieldsUp": !before.ShieldsUp}, m5PendingSeconds)
	m3RequirePending(t, ch, err)
	want := before
	want.RouteAll, want.ShieldsUp = !before.RouteAll, !before.ShieldsUp
	if got := m5HostPrefs(ctx, t, v); got != want {
		t.Errorf("preferences after the unconfirmed change: %+v, want %+v", got, want)
	}
	if _, err := n.api.m3Revert(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	m3WaitChange(ctx, t, n.api, ch.ID, core.StateReverted, 2*time.Minute)
	if got := m5HostPrefs(ctx, t, v); got != before {
		t.Errorf("preferences after the revert: %+v, want %+v", got, before)
	}

	done := m5Apply(t, v, "tailscale.prefs.set", map[string]any{"acceptRoutes": !before.RouteAll})
	var res tailscale.PrefsResult
	if err := json.Unmarshal(done.Output, &res); err != nil {
		t.Fatalf("output of the confirmed change: %v", err)
	}
	if got, wantOut := m3JSON(res.Prefs), fmt.Sprintf(`{"acceptRoutes":%t}`, !before.RouteAll); got != wantOut {
		t.Errorf("output prefs %s, want exactly %s", got, wantOut)
	}
	want = before
	want.RouteAll = !before.RouteAll
	if got := m5HostPrefs(ctx, t, v); got != want {
		t.Errorf("preferences after the confirmed change: %+v, want %+v", got, want)
	}
	m5Apply(t, v, "tailscale.prefs.set", map[string]any{"acceptRoutes": before.RouteAll})
	if got := m5HostPrefs(ctx, t, v); got != before {
		t.Errorf("preferences after setting the field back: %+v, want %+v", got, before)
	}
}

// m5NodeRunSSH: while Tailscale SSH is on, the tailscale.link guard refuses a
// change that would block it (shields-up), and an override lets the same plan
// through; once SSH is off again the same plan passes the guard. Tailscale SSH
// is turned on by a change that is left unconfirmed and reverted.
func m5NodeRunSSH(t *testing.T, v *vm.VM) {
	n := m5Lookup(t, v)
	ctx := n.ctx
	shields := map[string]any{"shieldsUp": true}

	ch, err := n.api.m3Apply(ctx, n.hostID, "tailscale.prefs.set", map[string]any{"runSSH": true}, m5PendingSeconds)
	m3RequirePending(t, ch, err)
	reverted := false
	t.Cleanup(func() {
		if !reverted {
			_, _ = n.api.m3Revert(ctx, ch.ID)
		}
	})
	if !m5HostPrefs(ctx, t, v).RunSSH {
		t.Fatal("tailscaled does not hold RunSSH after the change")
	}
	if st := m5Status(t, v); !st.RunSSH {
		t.Error("tailscale.status reports runSSH false while Tailscale SSH is on")
	}

	g, err := m5PlanLink(ctx, n, "tailscale.prefs.set", shields)
	switch {
	case err != nil:
		t.Fatal(err)
	case g.OK:
		t.Errorf("tailscale.link passed shieldsUp with Tailscale SSH on: %q", g.Message)
	case !g.Overridable || g.Message == "":
		t.Errorf("tailscale.link refusal %+v, want an overridable guard with a message", g)
	}
	g, err = m5PlanLink(ctx, n, "tailscale.prefs.set", map[string]any{"shieldsUp": true, "overrides": []string{"tailscale.link"}})
	if err != nil {
		t.Fatal(err)
	}
	if !g.OK || !g.Overridden {
		t.Errorf("tailscale.link with the override listed: %+v, want ok and overridden", g)
	}

	if _, err := n.api.m3Revert(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	reverted = true
	m3WaitChange(ctx, t, n.api, ch.ID, core.StateReverted, 2*time.Minute)
	if m5HostPrefs(ctx, t, v).RunSSH {
		t.Error("tailscaled still holds RunSSH after the revert")
	}
	g, err = m5PlanLink(ctx, n, "tailscale.prefs.set", shields)
	if err != nil {
		t.Fatal(err)
	}
	if !g.OK {
		t.Errorf("tailscale.link refused shieldsUp with Tailscale SSH off: %q", g.Message)
	}
}

// m5NodeLatency: tailscale.latency lists the regions of the DERP map tailscaled
// answers, whatever it holds while logged out, with consistent measurements.
func m5NodeLatency(t *testing.T, v *vm.VM) {
	n := m5Lookup(t, v)
	ctx := n.ctx
	out := m3Run(ctx, t, v, "sudo curl -fsS --unix-socket "+tslocal.DefaultSocket+" http://local-tailscaled.sock/localapi/v0/derpmap")
	var m tslocal.DERPMap
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("the DERP map tailscaled answers: %v\n%s", err, out)
	}
	var l tailscale.Latency
	if err := n.api.Resource(ctx, n.hostID, "tailscale.latency", &l); err != nil {
		t.Fatal(err)
	}
	if err := m5xCheckLatency(l, m); err != nil {
		t.Errorf("tailscale.latency against a DERP map of %d regions: %v", len(m.Regions), err)
	}
	if len(l.Regions) > 0 && !m5xAnyMeasured(l) {
		t.Errorf("none of the %d regions has a STUN or HTTPS measurement; the VM reaches the internet, so one should: %s", len(l.Regions), m3JSON(l))
	}
	t.Logf("tailscale.latency: %d regions", len(l.Regions))
}

// m5NodeInstall: package.install refuses while the package is present; once
// the image's pinned package is removed, the resource reports tailscale.unavailable
// and package.install installs the repository's current package, its repository
// files and a running tailscaled that is logged out and not held.
func m5NodeInstall(t *testing.T, v *vm.VM) {
	n := m5Lookup(t, v)
	ctx := n.ctx
	const action = "tailscale.package.install"

	_, err := m4Plan(ctx, n.api, n.hostID, action, struct{}{})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != "tailscale.package_present" {
		t.Fatalf("plan of %s with the package installed: %v, want code tailscale.package_present", action, err)
	}

	m3Run(ctx, t, v, "sudo DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=600 -y remove tailscale")
	err = eventually(ctx, time.Minute, func(ctx context.Context) error {
		_, err := m5ReadStatus(ctx, n)
		switch {
		case errors.As(err, &ae) && ae.Code == "tailscale.unavailable":
			return nil
		case err == nil:
			return errors.New("tailscale.status answered, want code tailscale.unavailable")
		}
		return fmt.Errorf("tailscale.status answered %w, want code tailscale.unavailable", err)
	})
	if err != nil {
		t.Fatal(err)
	}
	m3Run(ctx, t, v, "curl -sS --max-time 30 -o /dev/null https://pkgs.tailscale.com/")

	ch := m5Apply(t, v, action, struct{}{})
	var out tailscale.PackageInstalled
	if err := json.Unmarshal(ch.Output, &out); err != nil || out.Version == "" {
		t.Fatalf("output of %s: %s, %v; want the installed version", action, ch.Output, err)
	}
	if got := strings.TrimSpace(m3Run(ctx, t, v, "dpkg-query -W -f '${Version}' tailscale")); got != out.Version {
		t.Errorf("dpkg holds tailscale %q, the change reports %q", got, out.Version)
	}
	if err := eventually(ctx, 2*time.Minute, func(ctx context.Context) error {
		if state := unitState(ctx, v, "tailscaled"); state != "active" {
			return fmt.Errorf("tailscaled is %q", state)
		}
		st, err := m5ReadStatus(ctx, n)
		if err != nil {
			return err
		}
		return m5xLoggedOut(st, "")
	}); err != nil {
		t.Errorf("after the install: %v", err)
	}

	codename := strings.TrimSpace(m3Run(ctx, t, v, ". /etc/os-release && echo $VERSION_CODENAME"))
	if got, want := m3Run(ctx, t, v, "sudo cat /etc/apt/sources.list.d/tailscale.list"), m5xSourceLine(n.os.Family, codename); got != want {
		t.Errorf("tailscale.list is %q, want %q", got, want)
	}
	m3Run(ctx, t, v, "sudo test -s /usr/share/keyrings/tailscale-archive-keyring.gpg")
	if held := m3Run(ctx, t, v, "apt-mark showhold"); strings.Contains(held, "tailscale") {
		t.Errorf("apt-mark showhold lists tailscale after an install: %q", held)
	}
}
