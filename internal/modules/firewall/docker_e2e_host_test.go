package firewall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// deeCapture is the capture the Docker end-to-end tests start from: the
// `base` scenario on ubuntu:24.04, where UFW is enabled with SSH allowed,
// Docker 29.9.1 runs the containers web, app and six, and iptables is the
// nft variant.
const deeCapture = "dockerfw/testdata/capture/ubuntu-24.04/base"

// deeHost is the end-to-end host with Docker: UFW's files and the kernel's
// tables of the capture, a fake Docker daemon answering with the capture's
// info, networks and containers, and a model of `*-restore -n` over the
// filter table, which the cleanup and the DOCKER-USER reload use. UFW runs
// as the predictor models it, as in e2eHost.
type deeHost struct {
	*e2eHost
	docker *fakeDocker
	ifaces []string // the capture's interface names
}

// deeFile reads one file of the capture.
func deeFile(t *testing.T, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(deeCapture, rel)) //nolint:gosec // G304: a capture under testdata
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// deeJSON decodes one JSON file of the capture into v.
func deeJSON(t *testing.T, rel string, v any) {
	t.Helper()
	if err := json.Unmarshal(deeFile(t, rel), v); err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
}

// deeSave parses the capture's nft save output of one family, every table
// that was captured.
func deeSave(t *testing.T, family string) *netfilter.Ruleset {
	t.Helper()
	out := &netfilter.Ruleset{}
	for _, table := range netfilter.Tables() {
		data, err := os.ReadFile(filepath.Join(deeCapture, "save", "nft-"+family+"-"+table+".txt")) //nolint:gosec // G304: a capture under testdata
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		rs, err := netfilter.Parse(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("nft-%s-%s: %v", family, table, err)
		}
		out.Tables = append(out.Tables, rs.Tables...)
	}
	if len(out.Tables) == 0 {
		t.Fatalf("the capture holds no nft %s tables", family)
	}
	return out
}

// deeNewHost builds the host. The golden "enabled" scenario supplies the
// files and sysctl values the capture does not hold; the capture's files and
// tables replace UFW's rule files and the kernel's chains.
func deeNewHost(t *testing.T) *deeHost {
	t.Helper()
	line, _, _ := strings.Cut(string(deeFile(t, "versions.txt")), "\n")
	if !strings.HasSuffix(line, " (nf_tables)") {
		t.Fatalf("the end-to-end host models the nft variant, but the capture's iptables is %q", line)
	}
	h := &deeHost{e2eHost: newE2EHost(t, "enabled", "enabled"), docker: newFakeDocker(),
		ifaces: strings.Fields(string(deeFile(t, "interfaces.txt")))}
	for _, rel := range []string{"etc/ufw/after.rules", "etc/ufw/after6.rules", "etc/ufw/user.rules", "etc/ufw/user6.rules", "etc/default/ufw"} {
		h.write("/"+rel, deeFile(t, "files/"+rel))
	}
	// A Docker host forwards packets; the golden scenario's values do not.
	for key := range h.sysctl {
		if key == "net/ipv4/ip_forward" || (strings.HasPrefix(key, "net/ipv6/conf/") && strings.HasSuffix(key, "/forwarding")) {
			h.sysctl[key] = "1"
		}
	}
	h.loaded = fwstate.Loaded{Variant: "nft", V4: deeSave(t, "v4"), V6: deeSave(t, "v6")}
	// The capture's IPv6 save lacks the last rule of ufw6-user-limit and of
	// ufw6-user-limit-accept, which user6.rules declares and UFW loads on a
	// working host; without them the host reads as out of sync with UFW.
	for _, line := range []string{"-A ufw6-user-limit -j REJECT --reject-with icmp6-port-unreachable", "-A ufw6-user-limit-accept -j ACCEPT"} {
		if !slices.Contains(deeTexts(h, fwstate.V6, strings.Fields(line)[1]), line) {
			if res := h.restore(fwstate.V6, []byte("*filter\n"+line+"\nCOMMIT\n")); res.ExitCode != 0 {
				t.Fatalf("add %q: %s", line, res.Stderr)
			}
		}
	}
	h.docker.networks, h.docker.containers = nil, nil
	deeJSON(t, "info.json", &h.docker.info)
	deeJSON(t, "version.json", &h.docker.version)
	deeJSON(t, "networks.json", &h.docker.networks)
	deeJSON(t, "containers.json", &h.docker.containers)
	return h
}

// deeBridge is the bridge interface of a Docker network.
func deeBridge(n dockerapi.Network) string {
	switch {
	case n.Options["com.docker.network.bridge.name"] != "":
		return n.Options["com.docker.network.bridge.name"]
	case n.Name == "bridge":
		return "docker0"
	}
	return "br-" + n.ID[:12]
}

// deeContainer returns the fake daemon's container name.
func (h *deeHost) deeContainer(t *testing.T, name string) *dockerapi.ContainerJSON {
	t.Helper()
	for i := range h.docker.containers {
		if h.docker.containers[i].Name == "/"+name {
			return &h.docker.containers[i]
		}
	}
	t.Fatalf("the capture has no container %s", name)
	return nil
}

// deeAddr is a container's IPv4 address on the default bridge.
func (h *deeHost) deeAddr(t *testing.T, name string) netip.Addr {
	t.Helper()
	return h.deeContainer(t, name).NetworkSettings.Networks["bridge"].IPAddress
}

// deePublishV6 makes web published on every IPv6 address as well, as Docker
// does for -p 8080:80 on an IPv4-only bridge, where docker-proxy serves the
// port and no kernel rule exists: the capture's inspect output may or may
// not hold that binding.
func (h *deeHost) deePublishV6(t *testing.T) {
	t.Helper()
	ports := h.deeContainer(t, "web").NetworkSettings.Ports
	bindings := ports["80/tcp"]
	if !slices.ContainsFunc(bindings, func(b dockerapi.PortBinding) bool { return b.HostIP == "::" && b.HostPort == "8080" }) {
		ports["80/tcp"] = append(bindings, dockerapi.PortBinding{HostIP: "::", HostPort: "8080"})
	}
}

// sockets are sshd's and, for every published TCP binding, docker-proxy's
// listener.
func (h *deeHost) sockets() ([]netstat.Socket, error) {
	socks, _ := e2eNet{}.Sockets()
	for _, c := range h.docker.containers {
		for _, key := range slices.Sorted(maps.Keys(c.NetworkSettings.Ports)) {
			if !strings.HasSuffix(key, "/tcp") {
				continue
			}
			for _, b := range c.NetworkSettings.Ports[key] {
				addr, err := netip.ParseAddr(b.HostIP)
				port, perr := strconv.ParseUint(b.HostPort, 10, 16)
				if err != nil || perr != nil {
					continue
				}
				family := netstat.IPv4
				if addr.Is6() {
					family = netstat.IPv6
				}
				socks = append(socks, netstat.Socket{Proto: netstat.ProtoTCP, Family: family, State: netstat.StateListen,
					Local: netip.AddrPortFrom(addr, uint16(port)), Processes: []netstat.Process{{PID: 900, Comm: "docker-proxy"}}})
			}
		}
	}
	return socks, nil
}

// interfaces are lo and eth0 of e2eNet and the capture's other interfaces,
// all up.
func (h *deeHost) interfaces() ([]netstat.Interface, error) {
	out, _ := e2eNet{}.Interfaces()
	for _, name := range h.ifaces {
		if !slices.ContainsFunc(out, func(i netstat.Interface) bool { return i.Name == name }) {
			out = append(out, netstat.Interface{Name: name, Up: true, Running: true})
		}
	}
	return out, nil
}

// routeGet routes a Docker subnet's addresses out of its bridge and every
// other address as e2eNet does.
func (h *deeHost) routeGet(q netstat.RouteQuery) (netstat.Route, error) {
	for _, n := range h.docker.networks {
		for _, c := range n.IPAM.Config {
			if c.Subnet.Contains(q.Dst) {
				return netstat.Route{Type: "unicast", Dst: netip.PrefixFrom(q.Dst, q.Dst.BitLen()), OutIface: deeBridge(n)}, nil
			}
		}
	}
	return e2eNet{}.RouteGet(q)
}

// readVariant serves the nft variant's tables from the kernel and nothing
// of the legacy variant.
func (h *deeHost) readVariant(_ context.Context, _ core.Env, f fwstate.Family, variant string, tables []string) (*netfilter.Ruleset, error) {
	rs := h.loaded.V4
	if f == fwstate.V6 {
		rs = h.loaded.V6
	}
	if variant != "nft" || rs == nil {
		return nil, nil
	}
	out := &netfilter.Ruleset{}
	for _, t := range rs.Tables {
		if slices.Contains(tables, t.Name) {
			out.Tables = append(out.Tables, t)
		}
	}
	return out, nil
}

// fw is the firewall over the host with the Docker seam set: the fake
// daemon, the variant reads, the sockets, interfaces and routes, a tailscaled
// without shields-up, and the production owned-chain cleanup in the mode a
// revert uses, which loads its input through the host's restore model.
func (h *deeHost) fw() fw {
	f := h.e2eHost.fw()
	f.d.docker = h.docker
	f.d.readVariant = h.readVariant
	f.d.sockets = h.sockets
	f.d.interfaces = h.interfaces
	f.d.routeGet = h.routeGet
	f.d.shieldsUp = func(context.Context) (bool, bool, error) { return false, false, nil }
	f.d.cleanChains = func(ctx context.Context, env core.Env, owner string) error {
		return f.cleanupChains(ctx, env, owner, cleanupPreserve)
	}
	return f
}

// env is a plan's Env on the host, running programs through the host.
func (h *deeHost) env() core.Env {
	env := h.e2eHost.env()
	env.Exec = h
	return env
}

// Run answers the iptables restore programs from the restore model and
// every other program as e2eHost does.
func (h *deeHost) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	var family fwstate.Family
	switch cmd.Path {
	case "/usr/sbin/iptables-nft-restore":
		family = fwstate.V4
	case "/usr/sbin/ip6tables-nft-restore":
		family = fwstate.V6
	default:
		return h.e2eHost.Run(ctx, cmd)
	}
	if !slices.Equal(cmd.Args, []string{"-n"}) {
		return core.CmdResult{}, fmt.Errorf("dee: %s %v is not modelled: %w", cmd.Path, cmd.Args, fs.ErrNotExist)
	}
	return h.restore(family, cmd.Stdin), nil
}

// restore loads input into the family's filter table as iptables-restore
// --noflush does, all or nothing.
func (h *deeHost) restore(f fwstate.Family, input []byte) core.CmdResult {
	cur := &h.loaded.V4
	if f == fwstate.V6 {
		cur = &h.loaded.V6
	}
	next, err := deeRestore(*cur, input)
	if err != nil {
		return core.CmdResult{ExitCode: 1, Stderr: []byte("iptables-restore: " + err.Error() + "\n")}
	}
	*cur = next
	return core.CmdResult{}
}

// addForeign appends a rule of another program to the IPv4 DOCKER-USER
// chain, as a tool run by the operator would.
func (h *deeHost) addForeign(t *testing.T, rule string) {
	t.Helper()
	if res := h.restore(fwstate.V4, []byte("*filter\n"+rule+"\nCOMMIT\n")); res.ExitCode != 0 {
		t.Fatalf("add %q: %s", rule, res.Stderr)
	}
}

// recreate gives container name a new address on the default bridge and
// rewrites that address in Docker's own IPv4 rules, those of its raw and nat
// tables and of its filter chains other than DOCKER-USER, as Docker does
// when it recreates the container.
func (h *deeHost) recreate(t *testing.T, name string, addr netip.Addr) {
	t.Helper()
	ep := h.deeContainer(t, name).NetworkSettings.Networks["bridge"]
	old := regexp.MustCompile(`\b` + regexp.QuoteMeta(ep.IPAddress.String()) + `\b`)
	ep.IPAddress = addr
	tables := deeModel(h.loaded.V4)
	for _, tb := range tables {
		for ci := range tb.chains {
			c := &tb.chains[ci]
			if tb.name == "filter" && (!strings.HasPrefix(c.name, "DOCKER") || c.name == "DOCKER-USER") {
				continue
			}
			for ri := range c.rules {
				c.rules[ri] = old.ReplaceAllString(c.rules[ri], addr.String())
			}
		}
	}
	rs, err := deeBuild(tables)
	if err != nil {
		t.Fatal(err)
	}
	h.loaded.V4 = rs
}

// deeChain is one chain of a table in the form a restore edits.
type deeChain struct {
	name, policy string // policy "" for a user chain
	rules        []string
}

// deeTable is one table of a ruleset in that form.
type deeTable struct {
	name   string
	chains []deeChain
}

// deeModel is rs as tables of chains with their rule lines.
func deeModel(rs *netfilter.Ruleset) []deeTable {
	var out []deeTable
	for _, t := range rs.Tables {
		dt := deeTable{name: t.Name}
		for _, c := range t.Chains {
			dc := deeChain{name: c.Name, policy: c.Policy}
			for _, r := range c.Rules {
				dc.rules = append(dc.rules, r.Text)
			}
			dt.chains = append(dt.chains, dc)
		}
		out = append(out, dt)
	}
	return out
}

// deeBuild parses tables back into a ruleset.
func deeBuild(tables []deeTable) (*netfilter.Ruleset, error) {
	var b strings.Builder
	for _, t := range tables {
		b.WriteString("*" + t.name + "\n")
		for _, c := range t.chains {
			policy := c.policy
			if policy == "" {
				policy = "-"
			}
			fmt.Fprintf(&b, ":%s %s [0:0]\n", c.name, policy)
		}
		for _, c := range t.chains {
			for _, r := range c.rules {
				b.WriteString(r + "\n")
			}
		}
		b.WriteString("COMMIT\n")
	}
	return netfilter.ParseRestore(strings.NewReader(b.String()))
}

// deeRestore applies iptables-restore --noflush input to rs and returns the
// new ruleset, leaving rs as it was: a declared chain that exists is flushed
// when it is a user chain and takes the declared policy when it is built-in,
// -A appends, -F flushes, and -X deletes a user chain only when it is empty
// and no rule jumps to it. Only these lines are modelled; anything else is an
// error, as is the failure of any line, which loads nothing.
func deeRestore(rs *netfilter.Ruleset, input []byte) (*netfilter.Ruleset, error) {
	tables := deeModel(rs)
	cur := -1
	for _, line := range strings.Split(string(input), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || strings.HasPrefix(line, "#") {
			continue
		}
		var chains *[]deeChain
		if cur >= 0 {
			chains = &tables[cur].chains
		}
		find := func(name string) int {
			if chains == nil {
				return -1
			}
			return slices.IndexFunc(*chains, func(c deeChain) bool { return c.name == name })
		}
		switch {
		case strings.HasPrefix(line, "*"):
			cur = slices.IndexFunc(tables, func(t deeTable) bool { return t.name == line[1:] })
			if cur < 0 {
				tables = append(tables, deeTable{name: line[1:]})
				cur = len(tables) - 1
			}
		case line == "COMMIT":
			cur = -1
		case chains == nil:
			return nil, fmt.Errorf("%q is outside a table", line)
		case strings.HasPrefix(line, ":") && len(f) >= 2:
			name, policy := f[0][1:], f[1]
			switch i := find(name); {
			case i < 0 && policy == "-":
				*chains = append(*chains, deeChain{name: name})
			case i < 0:
				*chains = append(*chains, deeChain{name: name, policy: policy})
			case (*chains)[i].policy == "":
				(*chains)[i].rules = nil
			default:
				(*chains)[i].policy = policy
			}
		case f[0] == "-A" && len(f) >= 2:
			i := find(f[1])
			if i < 0 {
				return nil, fmt.Errorf("-A: no chain %s", f[1])
			}
			(*chains)[i].rules = append((*chains)[i].rules, line)
		case f[0] == "-F" && len(f) == 2:
			i := find(f[1])
			if i < 0 {
				return nil, fmt.Errorf("-F: no chain %s", f[1])
			}
			(*chains)[i].rules = nil
		case f[0] == "-X" && len(f) == 2:
			i := find(f[1])
			if i < 0 || (*chains)[i].policy != "" {
				return nil, fmt.Errorf("-X: %s is not a user chain", f[1])
			}
			if len((*chains)[i].rules) > 0 {
				return nil, fmt.Errorf("-X: chain %s is not empty", f[1])
			}
			for _, c := range *chains {
				for _, r := range c.rules {
					if deeJumpsTo(r, f[1]) {
						return nil, fmt.Errorf("-X: chain %s is still referenced by %q", f[1], r)
					}
				}
			}
			*chains = slices.Delete(*chains, i, i+1)
		default:
			return nil, fmt.Errorf("line %q is not modelled", line)
		}
	}
	return deeBuild(tables)
}

// deeJumpsTo reports whether rule line jumps or goes to chain.
func deeJumpsTo(line, chain string) bool {
	f := strings.Fields(line)
	for i := 0; i+1 < len(f); i++ {
		if (f[i] == "-j" || f[i] == "-g") && f[i+1] == chain {
			return true
		}
	}
	return false
}

// deeEngine is the agent's lifecycle engine over the host, serving the
// firewall module with Docker.
func deeEngine(t *testing.T, h *deeHost) *lifecycle.Engine {
	t.Helper()
	return deeEngineWith(t, h, h)
}

// deeEngineWith is deeEngine with the changes running their programs
// through exec.
func deeEngineWith(t *testing.T, h *deeHost, exec core.Runner) *lifecycle.Engine {
	t.Helper()
	reg, err := core.NewRegistry(module{f: h.fw()})
	if err != nil {
		t.Fatal(err)
	}
	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideAgent, Registry: reg, Validator: schema.NewValidator(), Journal: &coretest.MemJournal{}, Redact: schema.Redact,
		Base: core.Env{HostID: "h1", DB: h.db, Exec: exec, Files: h, Sealer: coretest.Sealer{}, Clock: h.clock,
			Log: slog.New(slog.NewTextHandler(io.Discard, nil))},
		ModuleUnits: func(string) core.Units { return e2eUnits{} },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	return eng
}
