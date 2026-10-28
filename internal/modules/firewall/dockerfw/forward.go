package dockerfw

import (
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// ForeignDNAT returns every DNAT rule reachable from the PREROUTING chain of
// the nat table that is not in one of Docker's chains, in walk order. A chain
// is walked once, where the first jump or goto to it sits. A nil table, or
// one without PREROUTING, returns nil.
func ForeignDNAT(nat *netfilter.Table) []netfilter.Rule {
	if nat == nil || nat.Chain("PREROUTING") == nil {
		return nil
	}
	var out []netfilter.Rule
	walked := map[string]bool{"PREROUTING": true}
	var walk func(c *netfilter.Chain)
	walk = func(c *netfilter.Chain) {
		for _, r := range c.Rules {
			if r.Target.Name == "DNAT" {
				out = append(out, r)
				continue
			}
			next := r.Goto
			if r.Target.Chain {
				next = r.Target.Name
			}
			if next == "" || dtfDockerChain(next) || walked[next] {
				continue
			}
			if nc := nat.Chain(next); nc != nil {
				walked[next] = true
				walk(nc)
			}
		}
	}
	walk(nat.Chain("PREROUTING"))
	return out
}

func dtfDockerChain(name string) bool {
	return name == "DOCKER" || strings.HasPrefix(name, "DOCKER-")
}

// Propose turns a foreign DNAT rule into the exemption that names it exactly,
// or reports false with the zero Exemption when the rule is not one a single
// exemption can describe in family f.
func Propose(r netfilter.Rule, f fwstate.Family) (Exemption, bool) {
	if r.Target.Name != "DNAT" || r.Opaque() || r.Goto != "" {
		return Exemption{}, false
	}
	d := r.Dest
	if !d.Prefix.IsValid() || d.Neg || d.Prefix.Bits() != d.Prefix.Addr().BitLen() || dtfFamily(d.Prefix.Addr()) != f {
		return Exemption{}, false
	}
	if r.Proto.Neg || (r.Proto.Name != "tcp" && r.Proto.Name != "udp") || r.In.Neg {
		return Exemption{}, false
	}
	origPort, ok := dtfOrigPort(r)
	if !ok {
		return Exemption{}, false
	}
	dst, port, ok := dtfTarget(r, f)
	if !ok {
		return Exemption{}, false
	}
	if port == 0 {
		port = origPort
	}
	return Exemption{
		InInterface:     r.In.Name,
		OrigDestination: d.Prefix.Addr(),
		OrigPort:        origPort,
		Destination:     dst,
		Proto:           r.Proto.Name,
		Port:            port,
	}, true
}

// dtfOrigPort reads the one port the rule's tcp or udp module matches, 0 when
// it matches none. It is false for any module or option an exemption cannot
// express.
func dtfOrigPort(r netfilter.Rule) (uint16, bool) {
	var port uint16
	transport := false
	for _, m := range r.Matches {
		switch m.Module {
		case "comment":
		case "tcp", "udp":
			if m.Module != r.Proto.Name || transport {
				return 0, false
			}
			transport = true
			for _, o := range m.Options {
				if o.Name != "dport" || o.Neg || port != 0 || len(o.Values) != 1 {
					return 0, false
				}
				p, err := strconv.ParseUint(o.Values[0], 10, 16)
				if err != nil || p == 0 {
					return 0, false
				}
				port = uint16(p)
			}
		default:
			return 0, false
		}
	}
	return port, true
}

// dtfTarget reads the rule's --to-destination as one address of family f and
// an optional port, 0 when it names none.
func dtfTarget(r netfilter.Rule, f fwstate.Family) (netip.Addr, uint16, bool) {
	var value string
	for _, o := range r.Target.Options {
		if o.Name != "to-destination" || value != "" || len(o.Values) != 1 {
			return netip.Addr{}, 0, false
		}
		value = o.Values[0]
	}
	var addr netip.Addr
	var port uint16
	if ap, err := netip.ParseAddrPort(value); err == nil {
		addr, port = ap.Addr(), ap.Port()
		if port == 0 {
			return netip.Addr{}, 0, false
		}
	} else if a, err := netip.ParseAddr(value); err == nil {
		addr = a
	} else {
		return netip.Addr{}, 0, false
	}
	if addr.Zone() != "" || dtfFamily(addr) != f {
		return netip.Addr{}, 0, false
	}
	return addr, port, true
}

func dtfFamily(a netip.Addr) fwstate.Family {
	if a.Is4() {
		return fwstate.V4
	}
	return fwstate.V6
}

// Covered reports whether some exemption names the rule. An exemption that is
// broader than the rule covers it: an unset interface, original port or
// translated port matches any value. A narrower exemption does not.
func Covered(r netfilter.Rule, ex []Exemption) bool {
	if !r.Dest.Prefix.IsValid() {
		return false
	}
	p, ok := Propose(r, dtfFamily(r.Dest.Prefix.Addr()))
	if !ok {
		return false
	}
	return slices.ContainsFunc(ex, func(e Exemption) bool {
		return e.OrigDestination == p.OrigDestination && e.Destination == p.Destination && e.Proto == p.Proto &&
			(e.InInterface == "" || e.InInterface == p.InInterface) &&
			(e.OrigPort == 0 || e.OrigPort == p.OrigPort) &&
			(e.Port == 0 || e.Port == p.Port)
	})
}

// ExemptionConflicts returns one description per exemption and Docker network
// pair where a subnet of the network holds the exemption's destination, in
// exemption order and then network order.
func ExemptionConflicts(ex []Exemption, nets []Network) []string {
	var out []string
	for i, e := range ex {
		for _, n := range nets {
			for _, s := range n.Subnets {
				if s.Contains(e.Destination) {
					out = append(out, fmt.Sprintf("exemption %d: destination %s lies in Docker network %s (%s)", i, e.Destination, n.Name, s))
					break
				}
			}
		}
	}
	return out
}
