package dockerfw

import (
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// Realized is what a set of container rules needs against the running
// containers: the input of the Docker block and the tagged UFW rules.
type Realized struct {
	Block BlockInput   // CustomBridges and Settings filled
	Rules []model.Rule // the tagged UFW rules, in the order UFW must hold them; ID empty
}

// rzGroup holds the rows of one binding, each list in row ID order.
type rzGroup struct {
	binding     Binding
	allow, deny []ContainerRule
}

// Realize computes the block input and the UFW rules the container rules need
// against facts. It is pure and its output does not depend on the order of
// rows or on the order in which the facts list equal entries: the block, the
// rule operations and reconcile compare it byte for byte between passes.
func Realize(rows []ContainerRule, facts Facts, s Settings) Realized {
	out := Realized{Block: BlockInput{CustomBridges: rzCustomBridges(facts.Networks), Settings: s}}

	var generated []model.Rule
	seen := map[model.Rule]bool{}
	add := func(r model.Rule) {
		r = r.Normalize()
		key := r
		key.Comment = ""
		if !seen[key] {
			seen[key] = true
			generated = append(generated, r)
		}
	}

	for _, g := range rzGroups(rows) {
		if len(g.allow) == 0 {
			continue
		}
		b := g.binding
		lines := 0
		for _, c := range facts.Containers {
			if !b.Selects(c.Name, c.Project, c.Service) {
				continue
			}
			ports, published := rzContainerPorts(b, c)
			for i, v6 := range [2]bool{false, true} {
				if !published[i] {
					continue
				}
				allow := rzApplying(g.allow, v6)
				if len(allow) == 0 {
					continue
				}
				deny := rzApplying(g.deny, v6)
				family := model.FamilyV4
				if v6 {
					family = model.FamilyV6
				}
				targets := rzTargets(c, v6)
				if len(targets) == 0 {
					to := "any"
					if b.HostAddr.IsValid() {
						to = b.HostAddr.String()
					}
					for _, row := range append(slices.Clone(allow), deny...) {
						add(rzRule(b, row, model.DirIn, "", to, b.HostPort, family))
					}
					continue
				}
				for _, t := range targets {
					out.Block.Lines = append(out.Block.Lines, BindingLine{
						Proto: b.Proto, ContainerAddr: t.addr, ContainerPort: ports[i],
						HostAddr: b.HostAddr, HostPort: b.HostPort, Chain: b.Chain(),
					})
					lines++
					for _, row := range append(slices.Clone(allow), deny...) {
						add(rzRule(b, row, model.DirRoute, t.bridge, t.addr.String(), ports[i], family))
					}
				}
			}
		}
		if lines > 0 {
			out.Block.Chains = append(out.Block.Chains, rzChain(g))
		}
	}

	var denyIn, allowIn, routeAllow, routeDeny []model.Rule
	for _, r := range generated {
		switch {
		case r.Direction == model.DirIn && r.Action == model.ActionDeny:
			denyIn = append(denyIn, r)
		case r.Direction == model.DirIn:
			allowIn = append(allowIn, r)
		case r.Action == model.ActionAllow:
			routeAllow = append(routeAllow, r)
		default:
			routeDeny = append(routeDeny, r)
		}
	}
	out.Rules = slices.Concat(denyIn, rzMergeFamilies(allowIn), routeAllow, routeDeny)
	if len(out.Rules) == 0 {
		out.Rules = nil
	}
	return out
}

// rzCustomBridges lists the bridge interfaces of the bridge networks that the
// block's br-+ wildcard and docker0 do not already cover, sorted.
func rzCustomBridges(networks []Network) []string {
	var bridges []string
	for _, n := range networks {
		if n.Driver == "bridge" && n.Bridge != "" && n.Bridge != "docker0" && !strings.HasPrefix(n.Bridge, "br-") {
			bridges = append(bridges, n.Bridge)
		}
	}
	slices.Sort(bridges)
	return slices.Compact(bridges)
}

// rzGroups groups the allow and deny rows by binding. The groups follow the
// smallest row ID of each; rows of another kind are ignored.
func rzGroups(rows []ContainerRule) []*rzGroup {
	sorted := slices.Clone(rows)
	slices.SortStableFunc(sorted, func(a, b ContainerRule) int { return strings.Compare(a.ID, b.ID) })
	var groups []*rzGroup
	byID := map[string]*rzGroup{}
	for _, r := range sorted {
		if r.Kind != Allow && r.Kind != Deny {
			continue
		}
		id := r.Binding.ID()
		g := byID[id]
		if g == nil {
			g = &rzGroup{binding: r.Binding}
			byID[id] = g
			groups = append(groups, g)
		}
		if r.Kind == Allow {
			g.allow = append(g.allow, r)
		} else {
			g.deny = append(g.deny, r)
		}
	}
	return groups
}

func rzIs6(a netip.Addr) bool { return !a.Is4() }

// rzContainerPorts returns, per family (IPv4 then IPv6), the container port
// the binding's host port maps to and whether the container publishes it on
// the binding's host address in that family. The first matching entry wins.
func rzContainerPorts(b Binding, c Container) (ports [2]uint16, published [2]bool) {
	for _, p := range c.Ports {
		if p.HostPort != b.HostPort || p.Proto != b.Proto {
			continue
		}
		v6 := rzIs6(p.HostAddr)
		if b.HostAddr.IsValid() {
			v6 = rzIs6(b.HostAddr)
			unspecified := netip.IPv4Unspecified()
			if v6 {
				unspecified = netip.IPv6Unspecified()
			}
			if p.HostAddr != b.HostAddr && p.HostAddr != unspecified {
				continue
			}
		}
		i := 0
		if v6 {
			i = 1
		}
		if !published[i] {
			published[i] = true
			ports[i] = p.ContainerPort
		}
	}
	return ports, published
}

// rzApplying returns the rows that apply to a family: a row without a source
// is for every family, otherwise it is for the family of its source.
func rzApplying(rows []ContainerRule, v6 bool) []ContainerRule {
	var out []ContainerRule
	for _, r := range rows {
		if !r.From.IsValid() || rzIs6(r.From.Addr()) == v6 {
			out = append(out, r)
		}
	}
	return out
}

// rzTarget is a container's address on a bridge network.
type rzTarget struct {
	addr   netip.Addr
	bridge string
}

func rzTargets(c Container, v6 bool) []rzTarget {
	var out []rzTarget
	for _, e := range c.Endpoints {
		addr := e.Addr4
		if v6 {
			addr = e.Addr6
		}
		if addr.IsValid() && e.Bridge != "" {
			out = append(out, rzTarget{addr: addr, bridge: e.Bridge})
		}
	}
	return out
}

// rzRule builds the UFW rule of one container rule. A route rule leaves
// through bridge; an incoming rule passes an empty one.
func rzRule(b Binding, row ContainerRule, direction model.Direction, bridge, to string, port uint16, family model.Family) model.Rule {
	action := model.ActionAllow
	if row.Kind == Deny {
		action = model.ActionDeny
	}
	from := "any"
	if row.From.IsValid() {
		from = row.From.String()
	}
	return model.Rule{
		Action: action, Direction: direction, Proto: model.Proto(b.Proto),
		From: from, To: to, ToPort: strconv.Itoa(int(port)),
		OutIface: bridge, Family: family, Log: model.LogOff,
		Comment: LabelPrefix + row.ID, Origin: model.OriginDarkpanel,
	}
}

// rzChain is the binding's chain: every deny source, and every allow source
// unless one allow row admits any source, which makes the others redundant.
func rzChain(g *rzGroup) BindingChain {
	chain := BindingChain{Name: g.binding.Chain()}
	for _, r := range g.deny {
		chain.Deny = append(chain.Deny, r.From)
	}
	for _, r := range g.allow {
		if !r.From.IsValid() {
			chain.Allow = nil
			break
		}
		chain.Allow = append(chain.Allow, r.From)
	}
	return chain
}

// rzMergeFamilies joins an allow-in rule for IPv4 with the equal rule for
// IPv6 when both ends are any: UFW stores them as one rule for both families.
// The merged rule keeps the position of the IPv4 one.
func rzMergeFamilies(rules []model.Rule) []model.Rule {
	both := make([]bool, len(rules))
	dropped := make([]bool, len(rules))
	for i, r := range rules {
		if r.Family != model.FamilyV4 || r.From != "any" || r.To != "any" {
			continue
		}
		twin := r
		twin.Family = model.FamilyV6
		for j, o := range rules {
			if !dropped[j] && o == twin {
				both[i], dropped[j] = true, true
				break
			}
		}
	}
	var out []model.Rule
	for i, r := range rules {
		if dropped[i] {
			continue
		}
		if both[i] {
			r.Family = model.FamilyBoth
		}
		out = append(out, r)
	}
	return out
}
