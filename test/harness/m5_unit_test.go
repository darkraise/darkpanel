//go:build harness

package harness

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/tailscale"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

// m5TestNode registers a node for a VM that was never booted: the helpers
// use the *vm.VM only as the key of their node.
func m5TestNode(t *testing.T, srv *httptest.Server) *vm.VM {
	t.Helper()
	v := new(vm.VM)
	m5Register(t, v, m5Node{ctx: context.Background(), api: m4TestAPI(t, srv), hostID: "h1", os: OS{ID: "ubuntu-24.04", Family: "ubuntu"}})
	return v
}

func TestM5UnitRegister(t *testing.T) {
	v := new(vm.VM)
	t.Run("registered", func(t *testing.T) {
		m5Register(t, v, m5Node{hostID: "h1"})
		if got := m5Lookup(t, v).hostID; got != "h1" {
			t.Errorf("m5Lookup = host %q, want h1", got)
		}
	})
	if _, ok := m5Nodes.Load(v); ok {
		t.Error("the node of a finished test is still registered")
	}
}

func TestM5UnitConfigs(t *testing.T) {
	pins, err := LoadPins(PinsFile)
	if err != nil {
		t.Fatal(err)
	}
	o, err := pins.Find(pins.OS[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	lab, err := newLabAllocator(labPortMin).lab("TestM5Node", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key, err := vm.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	cfgs := m5Configs(lab, o, "/images/target.img", "/images/scanner.img", key)
	if len(cfgs) != 3 {
		t.Fatalf("%d VM configs, want the controller, the agent and the scanner", len(cfgs))
	}
	roles := []string{"controller", "agent", "scanner"}
	macs := map[string]bool{}
	for i, c := range cfgs {
		if c.Name != lab.VMName(roles[i]) {
			t.Errorf("VM %d is named %q, want %q", i, c.Name, lab.VMName(roles[i]))
		}
		if c.McastPort != lab.McastPort {
			t.Errorf("%s uses internet link port %d, want the lab's %d", c.Name, c.McastPort, lab.McastPort)
		}
		if (c.PrivMcastPort != 0) != (roles[i] != "scanner") || (c.PrivMcastPort != 0 && c.PrivMcastPort != lab.PrivMcastPort) {
			t.Errorf("%s uses private link port %d, want the lab's %d on the targets only", c.Name, c.PrivMcastPort, lab.PrivMcastPort)
		}
		if c.Dir != "" {
			t.Errorf("%s has Dir %q; Lab.Boot sets it", c.Name, c.Dir)
		}
		for _, mac := range []string{c.MgmtMAC, c.InetMAC, c.PrivMAC} {
			if mac == "" {
				continue
			}
			if !strings.HasPrefix(mac, "52:54:00:") || macs[mac] {
				t.Errorf("%s MAC %q is outside the lab's range or repeated", c.Name, mac)
			}
			macs[mac] = true
		}
	}
	if !slices.Equal(cfgs[1].InetAddrs, []string{agentIPv4 + "/15", agentIPv6 + "/64"}) || !slices.Equal(cfgs[1].PrivAddrs, []string{agentPriv + "/24"}) {
		t.Errorf("agent addresses %v and %v, want the fleet's", cfgs[1].InetAddrs, cfgs[1].PrivAddrs)
	}
	if !slices.Equal(cfgs[0].PrivAddrs, []string{controllerPriv + "/24"}) {
		t.Errorf("controller private address %v, want %s/24", cfgs[0].PrivAddrs, controllerPriv)
	}
	if cfgs[0].Provision != TargetScript(o) || cfgs[1].Provision != TargetScript(o) || cfgs[2].Provision != ScannerScript() {
		t.Error("a VM is not provisioned with its role's script")
	}
}

func TestM5UnitApply(t *testing.T) {
	var polls atomic.Int32
	var body atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/hosts/h1/actions/tailscale.prefs.set/v1/plan":
			_, _ = w.Write([]byte(`{"planId":"p1","diff":[],"guards":[{"id":"tailscale.link","ok":true,"message":"","overridable":true,"overridden":false}]}`))
		case "/api/v1/hosts/h1/actions/tailscale.prefs.set/v1/apply":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			body.Store(b)
			_, _ = w.Write([]byte(`{"id":"c1","state":"pending"}`))
		case "/api/v1/hosts/h1/actions/tailscale.package.install/v1/plan":
			_, _ = w.Write([]byte(`{"planId":"p2","diff":[],"guards":[]}`))
		case "/api/v1/hosts/h1/actions/tailscale.package.install/v1/apply":
			_, _ = w.Write([]byte(`{"id":"c2","state":"applying"}`))
		case "/api/v1/changes/c1":
			if polls.Add(1) < 3 {
				_, _ = w.Write([]byte(`{"id":"c1","state":"pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"c1","state":"confirmed","output":{"prefs":{"acceptRoutes":true}}}`))
		case "/api/v1/changes/c2":
			_, _ = w.Write([]byte(`{"id":"c2","state":"completed","output":{"version":"1.104.2"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	v := m5TestNode(t, srv)

	ch := m5Apply(t, v, "tailscale.prefs.set", map[string]any{"acceptRoutes": true})
	if ch.State != core.StateConfirmed || string(ch.Output) != `{"prefs":{"acceptRoutes":true}}` {
		t.Errorf("change = %+v, want confirmed with the output", ch)
	}
	if b, _ := body.Load().(map[string]any); b["planId"] != "p1" || b["confirmNow"] != true {
		t.Errorf("apply carried %v, want planId p1 and confirmNow true", b)
	}
	if polls.Load() < 3 {
		t.Errorf("polled %d times, want it to wait while the change was pending", polls.Load())
	}
	if ch := m5Apply(t, v, "tailscale.package.install", struct{}{}); ch.State != core.StateCompleted || string(ch.Output) != `{"version":"1.104.2"}` {
		t.Errorf("task change = %+v, want completed with the version", ch)
	}
}

func TestM5UnitStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/hosts/h1/resources/tailscale/status" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"version":"1.104.1-tabc","backendState":"NeedsLogin","self":null,"tailnet":"","runSSH":true,"peers":[],"health":[]}`))
	}))
	t.Cleanup(srv.Close)
	st := m5Status(t, m5TestNode(t, srv))
	if st.BackendState != "NeedsLogin" || st.Self != nil || !st.RunSSH || st.Version != "1.104.1-tabc" {
		t.Errorf("m5Status = %+v, want the NeedsLogin node with SSH on", st)
	}
}

func TestM5UnitParsePrefs(t *testing.T) {
	good := `{"ControlURL":"https://controlplane.tailscale.com","RouteAll":true,"ShieldsUp":false,"RunSSH":true,"WantRunning":false,"AdvertiseRoutes":null}`
	got, err := m5xParsePrefs(good)
	if want := (m5Prefs{RouteAll: true, RunSSH: true}); err != nil || got != want {
		t.Errorf("m5xParsePrefs = %+v, %v; want %+v", got, err, want)
	}
	for _, c := range []struct{ name, in, want string }{
		{"not JSON", "Logged out.", "tailscale debug prefs"},
		{"member missing", `{"RouteAll":true,"ShieldsUp":false,"RunSSH":true}`, "WantRunning"},
		{"member not a bool", `{"RouteAll":"yes","ShieldsUp":false,"RunSSH":true,"WantRunning":false}`, "RouteAll"},
	} {
		if _, err := m5xParsePrefs(c.in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want one naming %q", c.name, err, c.want)
		}
	}
}

func TestM5UnitPinned(t *testing.T) {
	pins, err := LoadPins(PinsFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range pins.OS {
		if v, err := m5xPinned(o); err != nil || v == "" {
			t.Errorf("m5xPinned(%s) = %q, %v; want the pinned version", o.ID, v, err)
		}
	}
	if _, err := m5xPinned(OS{ID: "x", Packages: []Package{{Name: "docker-ce", Version: "1"}}}); err == nil {
		t.Error("an OS without a tailscale pin: no error")
	}
}

func TestM5UnitLoggedOut(t *testing.T) {
	good := tailscale.Status{Version: "1.104.1-t123", BackendState: "NeedsLogin"}
	if err := m5xLoggedOut(good, "1.104.1"); err != nil {
		t.Errorf("a logged-out node: %v", err)
	}
	if err := m5xLoggedOut(good, ""); err != nil {
		t.Errorf("a logged-out node, any version: %v", err)
	}
	for _, c := range []struct {
		name string
		edit func(*tailscale.Status)
		want string
	}{
		{"running", func(s *tailscale.Status) { s.BackendState = "Running" }, "NeedsLogin"},
		{"self", func(s *tailscale.Status) { s.Self = &tailscale.Node{ID: "n1"} }, "self"},
		{"tailnet", func(s *tailscale.Status) { s.Tailnet = "example.com" }, "tailnet"},
		{"peers", func(s *tailscale.Status) { s.Peers = []tailscale.Peer{{}} }, "1 peers"},
		{"version", func(s *tailscale.Status) { s.Version = "1.90.0" }, "1.90.0"},
		{"no version", func(s *tailscale.Status) { s.Version = "" }, "version"},
	} {
		s := good
		c.edit(&s)
		if err := m5xLoggedOut(s, "1.104.1"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want one naming %q", c.name, err, c.want)
		}
	}
}

func TestM5UnitCheckLatency(t *testing.T) {
	m := tslocal.DERPMap{Regions: map[int]*tslocal.DERPRegion{
		1: {RegionID: 1, RegionCode: "nyc", RegionName: "New York City", Nodes: []*tslocal.DERPNode{{Name: "1a", RegionID: 1, HostName: "derp1.example", IPv4: "192.0.2.1"}}},
		2: {RegionID: 2, RegionCode: "tok", RegionName: "Tokyo", Nodes: []*tslocal.DERPNode{{Name: "2a", RegionID: 2, HostName: "derp2.example", IPv4: "192.0.2.2", STUNPort: -1}}},
	}}
	measured := tailscale.Measurement{Available: true, MedianMs: 12.5}
	missing := tailscale.Measurement{Reason: "no STUN endpoint"}
	good := func() tailscale.Latency {
		return tailscale.Latency{Regions: []tailscale.RegionLatency{
			{RegionID: 1, Code: "nyc", Name: "New York City", STUN: measured, HTTPS: measured},
			{RegionID: 2, Code: "tok", Name: "Tokyo", STUN: missing, HTTPS: measured},
		}}
	}
	if err := m5xCheckLatency(good(), m); err != nil {
		t.Errorf("a consistent answer: %v", err)
	}
	if err := m5xCheckLatency(tailscale.Latency{}, tslocal.DERPMap{}); err != nil {
		t.Errorf("no regions for an empty map: %v", err)
	}
	for _, c := range []struct {
		name string
		edit func(*tailscale.Latency)
		want string
	}{
		{"region missing", func(l *tailscale.Latency) { l.Regions = l.Regions[:1] }, "lists regions [1]"},
		{"region added", func(l *tailscale.Latency) { l.Regions = append(l.Regions, tailscale.RegionLatency{RegionID: 3}) }, "lists regions [1 2 3]"},
		{"unsorted", func(l *tailscale.Latency) { slices.Reverse(l.Regions) }, "ascending"},
		{"wrong code", func(l *tailscale.Latency) { l.Regions[0].Code = "sfo" }, `region 1 is "sfo"`},
		{"available without median", func(l *tailscale.Latency) { l.Regions[0].HTTPS.MedianMs = 0 }, "region 1 HTTPS: available"},
		{"available with reason", func(l *tailscale.Latency) { l.Regions[0].STUN.Reason = "x" }, "region 1 STUN: available"},
		{"unavailable without reason", func(l *tailscale.Latency) { l.Regions[1].STUN.Reason = "" }, "region 2 STUN: unavailable"},
		{"unavailable with median", func(l *tailscale.Latency) { l.Regions[1].STUN.MedianMs = 3 }, "region 2 STUN: unavailable"},
		{"STUN without an endpoint", func(l *tailscale.Latency) { l.Regions[1].STUN = measured }, "region 2 reports STUN latency but has no STUN endpoint"},
	} {
		l := good()
		c.edit(&l)
		if err := m5xCheckLatency(l, m); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want one naming %q", c.name, err, c.want)
		}
	}
	if err := m5xCheckLatency(tailscale.Latency{Regions: []tailscale.RegionLatency{{RegionID: 1}}}, tslocal.DERPMap{}); err == nil {
		t.Error("a region for an empty map: no error")
	}
	if !m5xAnyMeasured(good()) {
		t.Error("m5xAnyMeasured of an answer with measurements = false")
	}
	if m5xAnyMeasured(tailscale.Latency{Regions: []tailscale.RegionLatency{{RegionID: 2, STUN: missing, HTTPS: missing}}}) {
		t.Error("m5xAnyMeasured of an answer with none = true")
	}
}

func TestM5UnitSourceLine(t *testing.T) {
	const want = "deb [signed-by=/usr/share/keyrings/tailscale-archive-keyring.gpg] https://pkgs.tailscale.com/stable/debian bookworm main\n"
	if got := m5xSourceLine("debian", "bookworm"); got != want {
		t.Errorf("m5xSourceLine = %q, want %q", got, want)
	}
}

func TestM5UnitLinkGuard(t *testing.T) {
	g, err := m5xLinkGuard([]core.Guard{{ID: "other", OK: true}, {ID: "tailscale.link", OK: false, Overridable: true, Message: "x"}})
	if err != nil || g.OK || !g.Overridable || g.Message != "x" {
		t.Errorf("m5xLinkGuard = %+v, %v; want the tailscale.link guard", g, err)
	}
	if _, err := m5xLinkGuard([]core.Guard{{ID: "firewall.lockout", OK: true}}); err == nil || !strings.Contains(err.Error(), "tailscale.link") {
		t.Errorf("a plan without the guard: %v, want an error naming tailscale.link", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/hosts/h1/actions/tailscale.prefs.set/v1/plan" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"planId":"p1","diff":[],"guards":[{"id":"tailscale.link","ok":false,"message":"ssh","overridable":true,"overridden":false}]}`))
	}))
	t.Cleanup(srv.Close)
	n := m5Lookup(t, m5TestNode(t, srv))
	g, err = m5PlanLink(context.Background(), n, "tailscale.prefs.set", map[string]any{"shieldsUp": true})
	if err != nil || g.OK || g.Message != "ssh" {
		t.Errorf("m5PlanLink = %+v, %v; want the failing guard", g, err)
	}
}
