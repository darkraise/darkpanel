package dockerfw

import (
	"fmt"
	"net/netip"
	"strconv"

	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// Detection is what the host shows about Docker and UFW that decides whether
// the integration is supported.
type Detection struct {
	Version       string // daemon ServerVersion
	Driver        string // FirewallBackend.Driver, "iptables" when absent
	DockerVariant string // "nft" | "legacy" | "" (neither holds DOCKER-USER and DOCKER-FORWARD) | "both"
	UFWVariant    string
	RawProtection bool
}

// Unsupported returns the reasons the integration cannot manage this host, in
// a fixed order. An empty result means the host is supported.
func Unsupported(d Detection) []string {
	var reasons []string
	if major, ok := detMajor(d.Version); !ok {
		reasons = append(reasons, fmt.Sprintf("Docker version %q is not recognised", d.Version))
	} else if major < 28 {
		reasons = append(reasons, fmt.Sprintf("Docker %s is older than 28.0", d.Version))
	}
	if d.Driver != "iptables" {
		reasons = append(reasons, fmt.Sprintf(`Docker's firewall backend is %q; only "iptables" is supported`, d.Driver))
	}
	ufwKnown := d.UFWVariant == "nft" || d.UFWVariant == "legacy"
	if !ufwKnown {
		reasons = append(reasons, "UFW's iptables variant is not known")
	}
	switch d.DockerVariant {
	case "nft", "legacy":
		if ufwKnown && d.DockerVariant != d.UFWVariant {
			reasons = append(reasons, fmt.Sprintf("Docker's chains are in iptables-%s but UFW uses iptables-%s", d.DockerVariant, d.UFWVariant))
		}
	case "both":
		reasons = append(reasons, "Docker's chains are in both iptables variants")
	default:
		reasons = append(reasons, "Docker's chains DOCKER-USER and DOCKER-FORWARD are in neither iptables variant")
	}
	if !d.RawProtection {
		reasons = append(reasons, "Docker's raw-table protection of published ports is not loaded")
	}
	return reasons
}

// detMajor reads the leading <major>.<minor> of a Docker version and returns
// the major number. Whatever follows the minor is ignored.
func detMajor(version string) (int, bool) {
	i := 0
	for i < len(version) && version[i] >= '0' && version[i] <= '9' {
		i++
	}
	if i == 0 || i == len(version) || version[i] != '.' {
		return 0, false
	}
	major, err := strconv.Atoi(version[:i])
	if err != nil {
		return 0, false
	}
	j := i + 1
	for j < len(version) && version[j] >= '0' && version[j] <= '9' {
		j++
	}
	if j == i+1 {
		return 0, false
	}
	return major, true
}

// DockerChains reports whether the filter table declares both DOCKER-USER and
// DOCKER-FORWARD, with or without rules. A table nft cannot fully show counts
// as false even when it still lists both chains.
func DockerChains(filter *netfilter.Table) bool {
	return filter != nil && filter.Incompatible == "" && filter.Chain("DOCKER-USER") != nil && filter.Chain("DOCKER-FORWARD") != nil
}

// RawProtected reports whether the IPv4 raw table holds the protection Docker
// 28 and later loads for the running containers in facts. An idle Docker
// loads no raw rule, so the absence of rules proves nothing until a container
// exists; a host with nothing to protect is therefore protected.
func RawProtected(raw *netfilter.Table, facts Facts) bool {
	if raw == nil || raw.Incompatible != "" {
		return false
	}
	pre := raw.Chain("PREROUTING")
	var rules []netfilter.Rule
	if pre != nil {
		rules = pre.Rules
	}
	noRule := map[string]bool{}
	for _, n := range facts.Networks {
		if n.GatewayModeV4 == "routed" || n.GatewayModeV4 == "nat-unprotected" {
			noRule[n.Name] = true
		}
	}
	for _, c := range facts.Containers {
		for _, e := range c.Endpoints {
			if e.Addr4.IsValid() && !noRule[e.Network] && !detHasDrop(rules, e.Addr4, e.Bridge, "", 0) {
				return false
			}
		}
		for _, p := range c.Ports {
			if p.HostAddr.Is4() && p.HostAddr.IsLoopback() && !detHasDrop(rules, p.HostAddr, "lo", p.Proto, p.HostPort) {
				return false
			}
		}
	}
	return true
}

// detHasDrop reports whether rules hold a DROP of traffic to addr that does
// not arrive on iface. With proto empty the rule has no protocol and no
// match; otherwise it matches that protocol and the single destination port.
// A rule with parts the parser did not model, or restricted by source, drops
// only some of that traffic and does not count.
func detHasDrop(rules []netfilter.Rule, addr netip.Addr, iface, proto string, port uint16) bool {
	dest := netip.PrefixFrom(addr, 32)
	for _, r := range rules {
		if r.Opaque() || r.Source.Prefix.IsValid() {
			continue
		}
		if r.Target.Name != "DROP" || r.Target.Chain || r.Goto != "" {
			continue
		}
		if r.Dest.Neg || r.Dest.Prefix != dest || !r.In.Neg || r.In.Name != iface {
			continue
		}
		if proto == "" {
			if r.Proto.Name == "" && len(r.Matches) == 0 {
				return true
			}
			continue
		}
		if r.Proto.Neg || r.Proto.Name != proto || len(r.Matches) != 1 {
			continue
		}
		m := r.Matches[0]
		if m.Module == proto && len(m.Options) == 1 {
			o := m.Options[0]
			if o.Name == "dport" && !o.Neg && len(o.Values) == 1 && o.Values[0] == strconv.Itoa(int(port)) {
				return true
			}
		}
	}
	return false
}
