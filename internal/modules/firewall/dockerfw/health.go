package dockerfw

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// Level says how much a problem costs.
type Level string

const (
	// Degraded is a problem that leaves the integration working but exposes
	// or hides something the administrator should see.
	Degraded Level = "degraded"
	// Broken is a problem that leaves the integration not doing its job.
	Broken Level = "broken"
)

// Issue is one problem Health found.
type Issue struct {
	Level  Level  `json:"level"`
	Reason string `json:"reason"`
}

// HealthInput is everything Health reads. It is filled by the caller from the
// host; Health reads nothing itself.
type HealthInput struct {
	// Want is true when the block belongs in the files: the flag is on and
	// UFW is enabled.
	Want bool
	// Files holds after.rules (V4) and after6.rules (V6) as on disk.
	Files map[fwstate.Family][]byte
	// Expected holds Render of the realized block per family.
	Expected map[fwstate.Family][]byte
	// Loaded holds the rules the kernel holds per family.
	Loaded map[fwstate.Family]*netfilter.Ruleset
	// IPv6 is the IPV6 setting of /etc/default/ufw.
	IPv6 bool
	// DefaultInput is the configured DEFAULT_INPUT_POLICY.
	DefaultInput string
	Facts        Facts
	Settings     Settings
	// Interfaces is every interface name present on the host.
	Interfaces []string
}

// Health reports every problem with the Docker integration, in a fixed order:
// check by check, IPv4 before IPv6 within a check. A healthy host gives nil.
// With Want false, the block, its chains, FORWARD, foreign forwards and the
// loaded incoming policy are not checked.
func Health(in HealthInput) []Issue {
	var out []Issue
	fams := hlFamilies(in)
	known := map[fwstate.Family][]netfilter.Rule{}
	for _, f := range fams {
		known[f] = hlKnownRules(in, f)
	}
	perFamily := func(check func(f fwstate.Family) []Issue) {
		for _, f := range fams {
			out = append(out, check(f)...)
		}
	}

	if in.Want {
		perFamily(func(f fwstate.Family) []Issue { return hlBlockPresent(in, f) })
		perFamily(func(f fwstate.Family) []Issue { return hlLoadedChains(in, f, known[f]) })
		perFamily(func(f fwstate.Family) []Issue { return hlForward(in, f) })
	}
	perFamily(func(f fwstate.Family) []Issue { return hlForeignUser(in, f, known[f]) })
	if in.Want {
		perFamily(func(f fwstate.Family) []Issue { return hlForeignForwards(in, f) })
	}
	for _, reason := range ExemptionConflicts(in.Settings.ForwardExemptions, in.Facts.Networks) {
		out = append(out, Issue{Broken, reason})
	}
	if in.DefaultInput != "DROP" && in.DefaultInput != "REJECT" {
		out = append(out, Issue{Broken, "DEFAULT_INPUT_POLICY is " + in.DefaultInput + ", not DROP or REJECT"})
	}
	if in.Want {
		perFamily(func(f fwstate.Family) []Issue { return hlLoadedPolicy(in, f) })
	}
	bridges := hlBridges(in.Facts.Networks)
	for _, name := range in.Interfaces {
		if strings.HasPrefix(name, "br-") && !slices.Contains(bridges, name) {
			out = append(out, Issue{Degraded, "interface " + name + " is a br-* bridge that is not a Docker network's bridge"})
		}
	}
	for _, n := range in.Facts.Networks {
		if reason, bad := Unprotected(n, in.Settings); bad {
			out = append(out, Issue{Degraded, "network " + n.Name + " is unprotected: " + reason})
		}
	}
	if !in.IPv6 {
		for _, n := range in.Facts.Networks {
			if n.IPv6 {
				out = append(out, Issue{Degraded, "IPV6=no in /etc/default/ufw while Docker network " + n.Name + " has IPv6 enabled"})
			}
		}
	}
	return out
}

// Unprotected reports whether Docker's own DNAT gate does not cover the
// network, with the reason: a routed or nat-unprotected gateway mode, or a
// custom bridge name (which the block's custom bridge rules cover only for
// private subnets) with a subnet outside every private source.
func Unprotected(n Network, s Settings) (reason string, unprotected bool) {
	if n.Driver != "bridge" {
		return "", false
	}
	for _, mode := range []string{n.GatewayModeV4, n.GatewayModeV6} {
		if mode == "routed" || mode == "nat-unprotected" {
			return "gateway mode " + mode + " bypasses the DNAT gate", true
		}
	}
	bridge := Bridge(n)
	if bridge == "docker0" || (len(n.ID) >= 12 && bridge == "br-"+n.ID[:12]) {
		return "", false
	}
	for _, sub := range n.Subnets {
		held := slices.ContainsFunc(s.PrivateSources, func(p netip.Prefix) bool {
			return p.Contains(sub.Addr()) && p.Bits() <= sub.Bits()
		})
		if !held {
			return fmt.Sprintf("custom bridge name %s with non-private subnet %s", bridge, sub), true
		}
	}
	return "", false
}

// hlFamilies returns the families UFW reads: after6.rules and the IPv6 chains
// are ignored with IPV6=no.
func hlFamilies(in HealthInput) []fwstate.Family {
	if in.IPv6 {
		return []fwstate.Family{fwstate.V4, fwstate.V6}
	}
	return []fwstate.Family{fwstate.V4}
}

func hlFamilyName(f fwstate.Family) string {
	if f == fwstate.V6 {
		return "IPv6"
	}
	return "IPv4"
}

func hlFileName(f fwstate.Family) string {
	if f == fwstate.V6 {
		return "after6.rules"
	}
	return "after.rules"
}

func hlChainOf(t *netfilter.Table, name string) *netfilter.Chain {
	if t == nil {
		return nil
	}
	return t.Chain(name)
}

func hlFilter(rs *netfilter.Ruleset) *netfilter.Table {
	if rs == nil {
		return nil
	}
	return rs.Table("filter")
}

// hlKnownRules returns the DOCKER-USER rules that are Darkpanel's: the
// expected block's and the block on disk's, so a stale block's rules are not
// called foreign.
func hlKnownRules(in HealthInput, f fwstate.Family) []netfilter.Rule {
	var known []netfilter.Rule
	collect := func(block []byte) {
		rs, err := BlockRules(block)
		if err != nil {
			return
		}
		if c := hlChainOf(rs.Table("filter"), ChainDockerUser); c != nil {
			known = append(known, c.Rules...)
		}
	}
	if len(in.Expected[f]) > 0 {
		collect(in.Expected[f])
	}
	if block, ok, err := BlockOf(in.Files[f]); err == nil && ok {
		collect(block)
	}
	return known
}

func hlBlockPresent(in HealthInput, f fwstate.Family) []Issue {
	_, ok, err := BlockOf(in.Files[f])
	switch {
	case err != nil:
		return []Issue{{Broken, hlFileName(f) + ": " + err.Error()}}
	case !ok:
		return []Issue{{Broken, hlFileName(f) + " holds no Darkpanel Docker block"}}
	}
	return nil
}

// hlLoadedChains compares the block's chains, then any other owned chain
// loaded, with what the kernel holds.
func hlLoadedChains(in HealthInput, f fwstate.Family, known []netfilter.Rule) []Issue {
	loaded := in.Loaded[f]
	if loaded == nil || len(in.Expected[f]) == 0 {
		return nil
	}
	expected, err := BlockRules(in.Expected[f])
	if err != nil {
		return nil
	}
	want, got := expected.Table("filter"), loaded.Table("filter")
	var names []string
	if want != nil {
		for _, c := range want.Chains {
			if Owned(c.Name) {
				names = append(names, c.Name)
			}
		}
	}
	if got != nil {
		for _, c := range got.Chains {
			if Owned(c.Name) && !slices.Contains(names, c.Name) {
				names = append(names, c.Name)
			}
		}
	}
	var out []Issue
	for _, name := range names {
		detail := hlChainDiff(hlChainOf(want, name), hlChainOf(got, name), known, f)
		if detail != "" {
			out = append(out, Issue{Broken, "loaded " + name + " (" + hlFamilyName(f) + ") does not match the Docker block: " + detail})
		}
	}
	return out
}

// hlChainDiff describes how the loaded chain differs from the expected one,
// "" when they match. Foreign DOCKER-USER rules are left out of the loaded
// chain because they are reported on their own.
func hlChainDiff(want, got *netfilter.Chain, known []netfilter.Rule, f fwstate.Family) string {
	switch {
	case want == nil:
		return "chain is not in the block"
	case got == nil:
		return "chain is not loaded"
	}
	rules := got.Rules
	if got.Name == ChainDockerUser {
		foreign := Foreign(got, known, f)
		rules = nil
		for _, r := range got.Rules {
			if len(foreign) > 0 && r.Line == foreign[0].Line && r.Text == foreign[0].Text {
				foreign = foreign[1:]
				continue
			}
			rules = append(rules, r)
		}
	}
	if len(rules) != len(want.Rules) {
		return fmt.Sprintf("it holds %d rules, the block has %d", len(rules), len(want.Rules))
	}
	v6 := f == fwstate.V6
	for i := range rules {
		if netfilter.Key(rules[i], v6) != netfilter.Key(want.Rules[i], v6) {
			return fmt.Sprintf("rule %d differs from the block", i+1)
		}
	}
	return ""
}

func hlBridges(nets []Network) []string {
	out := make([]string, 0, len(nets))
	for _, n := range nets {
		out = append(out, Bridge(n))
	}
	return out
}

// hlForward checks that FORWARD reaches DOCKER-USER before any rule of
// Docker's own that would accept the traffic first.
func hlForward(in HealthInput, f fwstate.Family) []Issue {
	if f == fwstate.V6 && !slices.ContainsFunc(in.Facts.Networks, func(n Network) bool { return n.IPv6 }) {
		return nil
	}
	loaded := in.Loaded[f]
	if loaded == nil {
		return nil
	}
	missing := []Issue{{Broken, "FORWARD (" + hlFamilyName(f) + ") does not jump to DOCKER-USER"}}
	forward := hlChainOf(loaded.Table("filter"), "FORWARD")
	if forward == nil {
		return missing
	}
	jump := slices.IndexFunc(forward.Rules, func(r netfilter.Rule) bool { return r.Target.Name == ChainDockerUser })
	if jump < 0 {
		return missing
	}
	bridges := hlBridges(in.Facts.Networks)
	for _, r := range forward.Rules[:jump] {
		if hlDockerRule(r, bridges) {
			return []Issue{{Broken, "FORWARD (" + hlFamilyName(f) + ") reaches Docker's rule " + r.Text + " before DOCKER-USER"}}
		}
	}
	return nil
}

func hlDockerRule(r netfilter.Rule, bridges []string) bool {
	switch r.Target.Name {
	case "DOCKER-FORWARD", "DOCKER", "DOCKER-ISOLATION-STAGE-1":
		return true
	case "ACCEPT":
		return hlBridgeMatch(r.In, bridges) || hlBridgeMatch(r.Out, bridges)
	}
	return false
}

func hlBridgeMatch(m netfilter.NameMatch, bridges []string) bool {
	return m.Name != "" && !m.Neg && (m.Name == "br-+" || slices.Contains(bridges, m.Name))
}

func hlForeignUser(in HealthInput, f fwstate.Family, known []netfilter.Rule) []Issue {
	chain := hlChainOf(hlFilter(in.Loaded[f]), ChainDockerUser)
	var out []Issue
	for _, r := range Foreign(chain, known, f) {
		out = append(out, Issue{Broken, ChainDockerUser + " (" + hlFamilyName(f) + ") holds a foreign rule: " + r.Text})
	}
	return out
}

func hlForeignForwards(in HealthInput, f fwstate.Family) []Issue {
	loaded := in.Loaded[f]
	if loaded == nil {
		return nil
	}
	nat := loaded.Table("nat")
	if nat == nil {
		return nil
	}
	var out []Issue
	for _, r := range ForeignDNAT(nat) {
		if !Covered(r, in.Settings.ForwardExemptions) {
			out = append(out, Issue{Degraded, "foreign DNAT forward has no exemption (" + hlFamilyName(f) + "): " + r.Text})
		}
	}
	return out
}

// hlLoadedPolicy checks the loaded INPUT policy. UFW's REJECT default is
// realized in rules, so the loaded policy is DROP either way.
func hlLoadedPolicy(in HealthInput, f fwstate.Family) []Issue {
	input := hlChainOf(hlFilter(in.Loaded[f]), "INPUT")
	if input == nil || input.Policy == "DROP" {
		return nil
	}
	return []Issue{{Broken, "loaded INPUT policy (" + hlFamilyName(f) + ") is " + input.Policy + ", not DROP"}}
}
