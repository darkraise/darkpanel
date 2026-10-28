// Package exposure computes which source classes can reach a port (spec
// 2.7). It is pure: it reads only the rulesets and values it is given and
// touches no host state.
package exposure

import (
	"cmp"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// Iface is one network interface of the host as the exposure evaluation
// sees it.
type Iface struct {
	Name     string
	Up       bool
	Loopback bool
	Tailnet  bool // tailscale0
	Bridge   bool // a Docker bridge
	Addrs    []netip.Addr
}

// Class is one source class a port can be reached from.
type Class struct {
	Name  string       // internet | private | tailnet | localhost
	Range netip.Prefix // private only
}

// PortRange is an inclusive range of ports.
type PortRange struct{ From, To uint16 }

// Classes returns the source classes of family f in report order: internet,
// one private class per valid entry of private that belongs to f, tailnet
// and localhost.
func Classes(private []netip.Prefix, f fwstate.Family) []Class {
	out := []Class{{Name: "internet"}}
	for _, p := range private {
		if !p.IsValid() || p.Addr().Is4() != (f == fwstate.V4) {
			continue
		}
		out = append(out, Class{Name: "private", Range: p})
	}
	return append(out, Class{Name: "tailnet"}, Class{Name: "localhost"})
}

// Cuts returns the source prefixes, in-interface names and source-port
// ranges that any rule of rs names, each sorted and without duplicates.
// Opaque rules are read too: a superset of the cuts is always a correct
// partition. Negation is ignored for the same reason.
func Cuts(rs *netfilter.Ruleset) (srcs []netip.Prefix, ifaces []string, sports []PortRange) {
	if rs == nil {
		return nil, nil, nil
	}
	for _, t := range rs.Tables {
		for _, c := range t.Chains {
			for _, r := range c.Rules {
				if p := r.Source.Prefix; p.IsValid() {
					srcs = append(srcs, p.Masked())
				}
				if r.In.Name != "" {
					ifaces = append(ifaces, r.In.Name)
				}
				for _, m := range r.Matches {
					for _, o := range m.Options {
						if (o.Name == "sport" || o.Name == "sports") && len(o.Values) > 0 {
							sports = append(sports, ptSportRanges(o.Values[0])...)
						}
					}
				}
			}
		}
	}
	slices.SortFunc(srcs, func(a, b netip.Prefix) int {
		return cmp.Or(a.Addr().Compare(b.Addr()), cmp.Compare(a.Bits(), b.Bits()))
	})
	slices.SortFunc(sports, func(a, b PortRange) int {
		return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To))
	})
	slices.Sort(ifaces)
	return slices.Compact(srcs), slices.Compact(ifaces), slices.Compact(sports)
}

// ptSportRanges reads a comma list of the items n, a:b, :b and a:. An open
// start is 0 and an open end is 65535, as iptables reads them. An item that
// is not a number, or that runs backwards, is skipped.
func ptSportRanges(list string) []PortRange {
	var out []PortRange
	for item := range strings.SplitSeq(list, ",") {
		from, to, isRange := strings.Cut(item, ":")
		lo, hi := uint64(0), uint64(65535)
		var err error
		if from != "" || !isRange {
			if lo, err = strconv.ParseUint(from, 10, 16); err != nil {
				continue
			}
		}
		if !isRange {
			hi = lo
		} else if to != "" {
			if hi, err = strconv.ParseUint(to, 10, 16); err != nil {
				continue
			}
		}
		if lo > hi {
			continue
		}
		out = append(out, PortRange{From: uint16(lo), To: uint16(hi)})
	}
	return out
}
