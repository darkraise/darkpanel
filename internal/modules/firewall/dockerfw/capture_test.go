package dockerfw

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// capDir holds the captures of testdata/capture/capture.sh, one directory
// per image slug and scenario.
const capDir = "testdata/capture"

var (
	capImages    = []string{"ubuntu-22.04", "ubuntu-24.04", "debian-12", "debian-13"}
	capScenarios = []string{"base", "block", "cleanup", "routed"}
	capFamilies  = []fwstate.Family{fwstate.V4, fwstate.V6}
)

const (
	capBeginMarker = "# BEGIN DARKPANEL docker"
	capEndMarker   = "# END DARKPANEL docker"
	capBridgeOpt   = "com.docker.network.bridge.name"
)

// capFile returns one file of a capture.
func capFile(t *testing.T, image, scenario, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(capDir, image, scenario, rel)) //nolint:gosec // G304: the path is built from constants of this test
	if err != nil {
		t.Fatalf("%v; run testdata/capture/capture.sh for %s", err, image)
	}
	return data
}

// capVariant is the iptables variant of the capture's iptables.
func capVariant(t *testing.T, image, scenario string) string {
	t.Helper()
	line, _, _ := strings.Cut(string(capFile(t, image, scenario, "versions.txt")), "\n")
	switch {
	case strings.HasSuffix(line, " (nf_tables)"):
		return "nft"
	case strings.HasSuffix(line, " (legacy)"):
		return "legacy"
	}
	t.Fatalf("%s/%s: cannot tell the iptables variant from %q", image, scenario, line)
	return ""
}

// capLoad parses the capture's save output of the variant UFW uses, every
// table that exists, into one ruleset per family.
func capLoad(t *testing.T, image, scenario string) map[fwstate.Family]*netfilter.Ruleset {
	t.Helper()
	variant := capVariant(t, image, scenario)
	out := map[fwstate.Family]*netfilter.Ruleset{}
	for _, f := range capFamilies {
		merged := &netfilter.Ruleset{}
		for _, table := range netfilter.Tables() {
			path := filepath.Join(capDir, image, scenario, "save", variant+"-"+string(f)+"-"+table+".txt")
			data, err := os.ReadFile(path) //nolint:gosec // G304: the path is built from constants of this test
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			rs, err := netfilter.Parse(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			merged.Tables = append(merged.Tables, rs.Tables...)
		}
		if len(merged.Tables) == 0 {
			t.Fatalf("%s/%s: no %s %s tables captured", image, scenario, variant, f)
		}
		out[f] = merged
	}
	return out
}

type capNetwork struct {
	Name       string
	ID         string `json:"Id"`
	Driver     string
	Options    map[string]string
	EnableIPv6 bool
	IPAM       struct{ Config []struct{ Subnet string } }
}

type capContainer struct {
	Name            string
	State           struct{ Running bool }
	NetworkSettings struct {
		Networks map[string]struct {
			NetworkID         string
			IPAddress         string
			GlobalIPv6Address string
		}
	}
}

func capJSON[T any](t *testing.T, image, scenario, rel string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(capFile(t, image, scenario, rel), &v); err != nil {
		t.Fatalf("%s/%s/%s: %v", image, scenario, rel, err)
	}
	return v
}

// capBridge is the interface name of a Docker network's bridge.
func capBridge(n capNetwork) string {
	switch {
	case n.Options[capBridgeOpt] != "":
		return n.Options[capBridgeOpt]
	case n.Name == "bridge":
		return "docker0"
	}
	return "br-" + n.ID[:12]
}

func capEach(t *testing.T, scenarios []string, fn func(t *testing.T, image, scenario string)) {
	t.Helper()
	for _, image := range capImages {
		for _, scenario := range scenarios {
			t.Run(image+"/"+scenario, func(t *testing.T) { fn(t, image, scenario) })
		}
	}
}

func capChain(rs *netfilter.Ruleset, table, chain string) *netfilter.Chain {
	if tb := rs.Table(table); tb != nil {
		return tb.Chain(chain)
	}
	return nil
}

type capCmd struct{ status, text string }

// capCommands returns the commands the capture recorded, in order.
func capCommands(t *testing.T, image, scenario string) []capCmd {
	t.Helper()
	var out []capCmd
	for _, line := range strings.Split(strings.TrimSpace(string(capFile(t, image, scenario, "commands.txt"))), "\n") {
		status, text, ok := strings.Cut(line, "\t")
		if !ok || status == "" || strings.Trim(status, "0123456789") != "" || text == "" {
			t.Fatalf("%s/%s commands.txt: %q is not <exit status><tab><command>", image, scenario, line)
		}
		out = append(out, capCmd{status, text})
	}
	return out
}

// capStatus is the exit status of the last recorded command that starts
// with prefix.
func capStatus(t *testing.T, image, scenario, prefix string) string {
	t.Helper()
	status := ""
	for _, c := range capCommands(t, image, scenario) {
		if strings.HasPrefix(c.text, prefix) {
			status = c.status
		}
	}
	if status == "" {
		t.Fatalf("%s/%s: no recorded command starts with %q", image, scenario, prefix)
	}
	return status
}

func TestCapTree(t *testing.T) {
	files := []string{
		"files/etc/ufw/after.rules", "files/etc/ufw/after6.rules", "files/etc/ufw/user.rules", "files/etc/ufw/user6.rules",
		"files/etc/default/ufw", "info.json", "version.json", "networks.json", "containers.json",
		"interfaces.txt", "commands.txt", "versions.txt",
	}
	counters := regexp.MustCompile(`^:\S+ \S+ \[(\d+):(\d+)\]$`)
	capEach(t, capScenarios, func(t *testing.T, image, scenario string) {
		for _, rel := range files {
			if len(capFile(t, image, scenario, rel)) == 0 {
				t.Errorf("%s is empty", rel)
			}
		}
		for _, f := range capFamilies {
			for _, table := range netfilter.Tables() {
				save := capFile(t, image, scenario, filepath.Join("save", "nft-"+string(f)+"-"+table+".txt"))
				for _, line := range strings.Split(string(save), "\n") {
					if strings.HasPrefix(line, "# Generated by") || strings.HasPrefix(line, "# Completed on") {
						t.Errorf("save %s %s keeps %q", f, table, line)
					}
					if m := counters.FindStringSubmatch(line); m != nil && (m[1] != "0" || m[2] != "0") {
						t.Errorf("save %s %s: counters of %q are not [0:0]", f, table, line)
					}
				}
			}
		}
		for _, rel := range []string{"info.json", "version.json", "networks.json", "containers.json"} {
			if !json.Valid(capFile(t, image, scenario, rel)) {
				t.Errorf("%s is not JSON", rel)
			}
		}
		if ifaces := strings.Fields(string(capFile(t, image, scenario, "interfaces.txt"))); !slices.Contains(ifaces, "lo") || !slices.Contains(ifaces, "docker0") {
			t.Errorf("interfaces %v hold no lo and docker0", ifaces)
		}
		for _, name := range []string{"web", "app", "six"} {
			found := false
			for _, c := range capJSON[[]capContainer](t, image, scenario, "containers.json") {
				found = found || c.Name == "/"+name && c.State.Running
			}
			if !found {
				t.Errorf("container %s is not running", name)
			}
		}
		capCommands(t, image, scenario)
	})
}

func TestCapVersions(t *testing.T) {
	iptablesLine := regexp.MustCompile(`^iptables v1\.8\.\d+ \((nf_tables|legacy)\)$`)
	capEach(t, []string{"base"}, func(t *testing.T, image, scenario string) {
		lines := strings.Split(strings.TrimSpace(string(capFile(t, image, scenario, "versions.txt"))), "\n")
		if len(lines) != 3 {
			t.Fatalf("versions.txt has %d lines, want iptables, ufw and dockerd: %q", len(lines), lines)
		}
		if !iptablesLine.MatchString(lines[0]) {
			t.Errorf("iptables line %q", lines[0])
		}
		if lines[1] != "ufw 0.36.1" && lines[1] != "ufw 0.36.2" {
			t.Errorf("ufw line %q, want UFW 0.36.1 or 0.36.2", lines[1])
		}
		if !strings.HasPrefix(lines[2], "Docker version 29.9.1,") {
			t.Errorf("dockerd line %q, want Docker 29.9.1", lines[2])
		}
		if got := capVariant(t, image, scenario); got != "nft" {
			t.Errorf("variant %s: the images' default alternative is nf_tables", got)
		}
		var info struct {
			ServerVersion   string
			FirewallBackend struct{ Driver string }
		}
		if err := json.Unmarshal(capFile(t, image, scenario, "info.json"), &info); err != nil {
			t.Fatal(err)
		}
		if info.ServerVersion != "29.9.1" || info.FirewallBackend.Driver != "iptables" {
			t.Errorf("info = %+v, want server 29.9.1 and firewall backend iptables", info)
		}
		var version struct{ Version string }
		if err := json.Unmarshal(capFile(t, image, scenario, "version.json"), &version); err != nil || version.Version != "29.9.1" {
			t.Errorf("version.json = %+v, %v", version, err)
		}
		defaults := string(capFile(t, image, scenario, "files/etc/default/ufw"))
		for _, want := range []string{`DEFAULT_INPUT_POLICY="DROP"`, `DEFAULT_FORWARD_POLICY="DROP"`, "IPV6=yes"} {
			if !strings.Contains(defaults, want) {
				t.Errorf("/etc/default/ufw lacks %s", want)
			}
		}
		if !strings.Contains(string(capFile(t, image, scenario, "files/etc/ufw/user.rules")), "### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 in") {
			t.Error("user.rules lacks the SSH rule")
		}
	})
}

func TestCapDockerChains(t *testing.T) {
	capEach(t, capScenarios, func(t *testing.T, image, scenario string) {
		for f, rs := range capLoad(t, image, scenario) {
			for _, chain := range []string{"DOCKER-USER", "DOCKER-FORWARD", "DOCKER"} {
				if capChain(rs, "filter", chain) == nil {
					t.Errorf("%s filter has no %s chain", f, chain)
				}
			}
			forward := capChain(rs, "filter", "FORWARD")
			if forward == nil {
				t.Fatalf("%s filter has no FORWARD chain", f)
			}
			user, docker := -1, -1
			for i, r := range forward.Rules {
				switch r.Target.Name {
				case "DOCKER-USER":
					if user < 0 {
						user = i
					}
				case "DOCKER-FORWARD":
					if docker < 0 {
						docker = i
					}
				}
			}
			if user < 0 || docker < 0 || user > docker {
				t.Errorf("%s FORWARD jumps to DOCKER-USER at %d and DOCKER-FORWARD at %d; want DOCKER-USER first", f, user, docker)
			}
		}
	})
}

func TestCapRawProtection(t *testing.T) {
	capEach(t, capScenarios, func(t *testing.T, image, scenario string) {
		rules := capLoad(t, image, scenario)
		bridges := map[string]string{}
		for _, n := range capJSON[[]capNetwork](t, image, scenario, "networks.json") {
			bridges[n.ID] = capBridge(n)
		}
		protected := func(f fwstate.Family, addr, bridge string) bool {
			chain := capChain(rules[f], "raw", "PREROUTING")
			if chain == nil {
				return false
			}
			bits := 32
			if f == fwstate.V6 {
				bits = 128
			}
			want := netip.PrefixFrom(netip.MustParseAddr(addr), bits)
			for _, r := range chain.Rules {
				if r.Dest.Prefix == want && !r.Dest.Neg && r.In == (netfilter.NameMatch{Name: bridge, Neg: true}) && r.Target.Name == "DROP" && len(r.Matches) == 0 {
					return true
				}
			}
			return false
		}
		for _, c := range capJSON[[]capContainer](t, image, scenario, "containers.json") {
			for netName, ep := range c.NetworkSettings.Networks {
				bridge := bridges[ep.NetworkID]
				if bridge == "" {
					t.Fatalf("container %s is on network %s, which networks.json lacks", c.Name, netName)
				}
				if ep.IPAddress != "" && !protected(fwstate.V4, ep.IPAddress, bridge) {
					t.Errorf("no raw PREROUTING drop of %s outside %s (%s on %s)", ep.IPAddress, bridge, c.Name, netName)
				}
				if ep.GlobalIPv6Address != "" && !protected(fwstate.V6, ep.GlobalIPv6Address, bridge) {
					t.Errorf("no IPv6 raw PREROUTING drop of %s outside %s (%s on %s)", ep.GlobalIPv6Address, bridge, c.Name, netName)
				}
			}
		}
		loopback := false
		for _, r := range capChain(rules[fwstate.V4], "raw", "PREROUTING").Rules {
			loopback = loopback || r.Dest.Prefix == netip.MustParsePrefix("127.0.0.1/32") &&
				r.In == (netfilter.NameMatch{Name: "lo", Neg: true}) && r.Target.Name == "DROP" && strings.Contains(r.Text, "--dport 8081")
		}
		if !loopback {
			t.Error("no raw PREROUTING drop of port 8081 for the loopback-published binding")
		}
	})
}

func TestCapPublishedPorts(t *testing.T) {
	capEach(t, capScenarios, func(t *testing.T, image, scenario string) {
		rules := capLoad(t, image, scenario)
		dnat := func(f fwstate.Family, hostPort, to string) bool {
			chain := capChain(rules[f], "nat", "DOCKER")
			if chain == nil {
				t.Fatalf("%s nat has no DOCKER chain", f)
			}
			for _, r := range chain.Rules {
				if r.Target.Name == "DNAT" && strings.Contains(r.Text, "--dport "+hostPort+" ") && strings.HasSuffix(r.Text, "--to-destination "+to) {
					return true
				}
			}
			return false
		}
		containers := map[string]capContainer{}
		for _, c := range capJSON[[]capContainer](t, image, scenario, "containers.json") {
			containers[strings.TrimPrefix(c.Name, "/")] = c
		}
		for name, port := range map[string]string{"web": "8080", "app": "9090", "six": "8443"} {
			var v4, v6 string
			for _, ep := range containers[name].NetworkSettings.Networks {
				v4, v6 = ep.IPAddress, ep.GlobalIPv6Address
			}
			if !dnat(fwstate.V4, port, v4+":80") {
				t.Errorf("no DNAT of host port %s to %s:80 (%s)", port, v4, name)
			}
			if got, want := dnat(fwstate.V6, port, "["+v6+"]:80"), name == "six"; got != want {
				t.Errorf("IPv6 DNAT of host port %s for %s = %v, want %v: only a container with an IPv6 address has one", port, name, got, want)
			}
		}
	})
}

func TestCapNetworks(t *testing.T) {
	capEach(t, capScenarios, func(t *testing.T, image, scenario string) {
		nets := map[string]capNetwork{}
		for _, n := range capJSON[[]capNetwork](t, image, scenario, "networks.json") {
			nets[n.Name] = n
		}
		subnets := func(n capNetwork) []string {
			var out []string
			for _, c := range n.IPAM.Config {
				out = append(out, c.Subnet)
			}
			return out
		}
		ifaces := strings.Fields(string(capFile(t, image, scenario, "interfaces.txt")))
		custom := nets["custom"]
		if custom.Options[capBridgeOpt] != "dpcustom" || !slices.Equal(subnets(custom), []string{"172.30.0.0/24"}) || !slices.Contains(ifaces, "dpcustom") {
			t.Errorf("network custom = %+v, interfaces %v", custom, ifaces)
		}
		v6 := nets["v6net"]
		if !v6.EnableIPv6 || !slices.Contains(subnets(v6), "fd00:dead::/64") || !slices.Contains(ifaces, capBridge(v6)) {
			t.Errorf("network v6net = %+v, interfaces %v", v6, ifaces)
		}
		if bridge := nets["bridge"]; bridge.Options[capBridgeOpt] != "docker0" || capBridge(bridge) != "docker0" {
			t.Errorf("default bridge options %v", bridge.Options)
		}
		routed, ok := nets["routednet"]
		if scenario != "routed" {
			if ok {
				t.Error("routednet exists outside the routed scenario")
			}
			return
		}
		if !ok || routed.Options["com.docker.network.bridge.gateway_mode_ipv4"] != "routed" ||
			!slices.Equal(subnets(routed), []string{"198.51.100.0/24"}) || !slices.Contains(ifaces, capBridge(routed)) {
			t.Errorf("network routednet = %+v (found %v), interfaces %v", routed, ok, ifaces)
		}
	})
}

// capBlockText returns the marked block of an after file and how many
// marker lines the file holds.
func capBlockText(file string) (block string, begins, ends int) {
	var lines []string
	in := false
	for _, line := range strings.Split(file, "\n") {
		switch line {
		case capBeginMarker:
			begins++
			in = true
		case capEndMarker:
			ends++
			lines = append(lines, line)
			in = false
		}
		if in {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n") + "\n", begins, ends
}

func capOwned(name string) bool {
	return strings.HasPrefix(name, "darkpanel-") || strings.HasPrefix(name, "dp-b-")
}

func TestCapBlock(t *testing.T) {
	capEach(t, []string{"block"}, func(t *testing.T, image, scenario string) {
		rules := capLoad(t, image, scenario)
		for _, f := range capFamilies {
			name := "after.rules"
			if f == fwstate.V6 {
				name = "after6.rules"
			}
			text, begins, ends := capBlockText(string(capFile(t, image, scenario, "files/etc/ufw/"+name)))
			if begins != 1 || ends != 1 {
				t.Fatalf("%s holds %d begin and %d end markers, want one block", name, begins, ends)
			}
			block, err := netfilter.ParseRestore(strings.NewReader(text))
			if err != nil {
				t.Fatalf("%s block: %v", name, err)
			}
			chains := block.Table("filter").Chains
			owned := 0
			for _, bc := range chains {
				if bc.Name != "DOCKER-USER" && !capOwned(bc.Name) {
					continue
				}
				if capOwned(bc.Name) {
					owned++
				}
				loaded := capChain(rules[f], "filter", bc.Name)
				if loaded == nil {
					t.Errorf("%s: chain %s of the block is not loaded", f, bc.Name)
					continue
				}
				var want, got []string
				for _, r := range bc.Rules {
					want = append(want, netfilter.Key(r, f == fwstate.V6))
				}
				for _, r := range loaded.Rules {
					got = append(got, netfilter.Key(r, f == fwstate.V6))
				}
				if !slices.Equal(got, want) {
					t.Errorf("%s: loaded %s differs from the block\n got %q\nwant %q", f, bc.Name, got, want)
				}
			}
			loadedOwned := 0
			for _, c := range rules[f].Table("filter").Chains {
				if capOwned(c.Name) {
					loadedOwned++
				}
			}
			if owned == 0 || loadedOwned != owned {
				t.Errorf("%s: the block declares %d owned chains, %d are loaded", f, owned, loadedOwned)
			}
		}
		if got := len(capChain(rules[fwstate.V4], "filter", "DOCKER-USER").Rules); got < 10 {
			t.Errorf("DOCKER-USER holds %d rules, want the block's", got)
		}
		sum := sha256.Sum256([]byte("web|all|8080|tcp"))
		if chain := "dp-b-" + hex.EncodeToString(sum[:])[:12]; capChain(rules[fwstate.V4], "filter", chain) == nil {
			t.Errorf("the binding's chain %s is not loaded", chain)
		}
		forward := capChain(rules[fwstate.V4], "filter", "ufw-user-forward")
		if forward == nil || len(forward.Rules) != 2 {
			t.Fatalf("ufw-user-forward = %+v, want the two tagged route rules loaded after the block's declaration", forward)
		}
		allow, deny := forward.Rules[0], forward.Rules[1]
		if allow.Target.Name != "ACCEPT" || allow.Out.Name != "docker0" || deny.Target.Name != "DROP" ||
			deny.Source.Prefix != netip.MustParsePrefix("203.0.113.0/24") || allow.Dest.Prefix != deny.Dest.Prefix {
			t.Errorf("ufw-user-forward rules %q and %q: want the route allow, then the route deny of 203.0.113.0/24", allow.Text, deny.Text)
		}
		// UFW stores a rule's comment hex-encoded in user.rules.
		user := string(capFile(t, image, scenario, "files/etc/ufw/user.rules"))
		allowAt := strings.Index(user, "comment="+hex.EncodeToString([]byte("darkpanel:01J00000000000000000000000 ctr 01J00000000000000000000001")))
		denyAt := strings.Index(user, "comment="+hex.EncodeToString([]byte("darkpanel:01J00000000000000000000002 ctr 01J00000000000000000000003")))
		if allowAt < 0 || denyAt < allowAt {
			t.Errorf("user.rules holds the tagged route allow at %d and the route deny at %d; want both, the allow first", allowAt, denyAt)
		}
		if got := capStatus(t, image, scenario, "ufw reload"); got != "0" {
			t.Errorf("ufw reload exited %s, want 0", got)
		}
	})
}

func TestCapCleanup(t *testing.T) {
	capEach(t, []string{"cleanup"}, func(t *testing.T, image, scenario string) {
		before := capLoad(t, image, "block")
		rules := capLoad(t, image, scenario)
		for _, f := range capFamilies {
			name := "after.rules"
			if f == fwstate.V6 {
				name = "after6.rules"
			}
			file := string(capFile(t, image, scenario, "files/etc/ufw/"+name))
			if strings.Contains(file, "DARKPANEL docker") || strings.Contains(file, "darkpanel-docker") {
				t.Errorf("%s still holds the block", name)
			}
			for _, c := range rules[f].Table("filter").Chains {
				if capOwned(c.Name) {
					t.Errorf("%s: chain %s is still loaded after the cleanup", f, c.Name)
				}
			}
			user := capChain(rules[f], "filter", "DOCKER-USER")
			if user == nil || len(user.Rules) != 1 || user.Rules[0].Text != "-A DOCKER-USER -j RETURN" {
				t.Errorf("%s: DOCKER-USER = %+v, want only -j RETURN", f, user)
			}
			forward := capChain(rules[f], "filter", "FORWARD")
			if len(forward.Rules) == 0 || forward.Rules[0].Target.Name != "DOCKER-USER" {
				t.Errorf("%s: FORWARD does not start with the jump to DOCKER-USER", f)
			}

			input := string(capFile(t, image, scenario, "restore-"+string(f)+".txt"))
			if !strings.HasPrefix(input, "*filter\n:DOCKER-USER - [0:0]\n-A DOCKER-USER -j RETURN\n") || !strings.HasSuffix(input, "COMMIT\n") {
				t.Errorf("restore-%s.txt = %q", f, input)
			}
			var flushed, deleted, owned []string
			for _, line := range strings.Split(input, "\n") {
				if c, ok := strings.CutPrefix(line, "-F "); ok {
					flushed = append(flushed, c)
				}
				if c, ok := strings.CutPrefix(line, "-X "); ok {
					deleted = append(deleted, c)
				}
			}
			for _, c := range before[f].Table("filter").Chains {
				if capOwned(c.Name) {
					owned = append(owned, c.Name)
				}
			}
			if len(owned) == 0 || !slices.Equal(flushed, owned) || !slices.Equal(deleted, owned) {
				t.Errorf("restore-%s.txt flushes %v and deletes %v, want the loaded owned chains %v", f, flushed, deleted, owned)
			}
		}
		variant := capVariant(t, image, scenario)
		for _, tool := range []string{"iptables", "ip6tables"} {
			prefix := tool + "-" + variant + "-restore -n"
			if got := capStatus(t, image, scenario, prefix); got != "0" {
				t.Errorf("%s exited %s, want 0", prefix, got)
			}
		}
		if got := capStatus(t, image, scenario, "ufw reload"); got != "0" {
			t.Errorf("ufw reload exited %s, want 0", got)
		}
		forward := capChain(rules[fwstate.V4], "filter", "ufw-user-forward")
		if forward == nil || len(forward.Rules) != 2 {
			t.Errorf("ufw-user-forward = %+v: the cleanup must leave UFW's tagged rules", forward)
		}
	})
}
