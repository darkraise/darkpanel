package eval

import (
	"net/netip"
	"slices"

	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// Cell is one range of the internet address space that every address
// prefix of a ruleset either wholly contains or wholly excludes.
type Cell struct {
	From, To netip.Addr // inclusive bounds
	Rep      netip.Addr // the address the guard evaluates for the cell
}

var (
	space4 = netip.MustParsePrefix("0.0.0.0/0")
	space6 = netip.MustParsePrefix("2000::/3")

	holes4 = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("224.0.0.0/4"),
		netip.MustParsePrefix("240.0.0.0/4"),
	}
	holes6 = []netip.Prefix{
		netip.MustParsePrefix("2001:db8::/32"),
	}
)

// InternetCells partitions the public unicast space of one family by
// every address prefix any rule of rs matches on.
func InternetCells(rs *netfilter.Ruleset, v6 bool) []Cell {
	space := space4
	holes := holes4
	if v6 {
		space = space6
		holes = holes6
	}
	spaceFirst := space.Addr()
	spaceLast := lastAddr(space)

	cuts := []netip.Addr{spaceFirst}
	addCut := func(a netip.Addr) {
		if a.IsValid() && a.Compare(spaceFirst) > 0 && a.Compare(spaceLast) <= 0 {
			cuts = append(cuts, a)
		}
	}
	addPrefix := func(p netip.Prefix) {
		if !p.IsValid() || p.Addr().Is4In6() || p.Addr().Is6() != v6 {
			return
		}
		p = p.Masked()
		addCut(p.Addr())
		addCut(lastAddr(p).Next())
	}

	if rs != nil {
		for _, t := range rs.Tables {
			for _, ch := range t.Chains {
				for _, r := range ch.Rules {
					addPrefix(r.Source.Prefix)
					addPrefix(r.Dest.Prefix)
					for _, m := range r.Matches {
						for _, o := range m.Options {
							if o.Name == "ctorigdst" && len(o.Values) > 0 {
								if p, ok := origDstPrefix(o.Values[0]); ok {
									addPrefix(p)
								}
							}
						}
					}
				}
			}
		}
	}

	slices.SortFunc(cuts, netip.Addr.Compare)
	cuts = slices.Compact(cuts)

	var cells []Cell
	for i, start := range cuts {
		end := spaceLast
		if i+1 < len(cuts) {
			end = cuts[i+1].Prev()
		}
		from, ok := skipHolesForward(start, holes)
		if !ok {
			continue
		}
		to, ok := skipHolesBackward(end, holes)
		if !ok || from.Compare(to) > 0 {
			continue
		}
		cells = append(cells, Cell{From: from, To: to, Rep: from})
	}
	return cells
}

func lastAddr(p netip.Prefix) netip.Addr {
	b := p.Masked().Addr().AsSlice()
	for i := p.Bits(); i < len(b)*8; i++ {
		b[i/8] |= 0x80 >> (i % 8)
	}
	a, _ := netip.AddrFromSlice(b)
	return a
}

func skipHolesForward(a netip.Addr, holes []netip.Prefix) (netip.Addr, bool) {
	for {
		moved := false
		for _, h := range holes {
			if h.Contains(a) {
				a = lastAddr(h).Next()
				if !a.IsValid() {
					return netip.Addr{}, false
				}
				moved = true
			}
		}
		if !moved {
			return a, true
		}
	}
}

func skipHolesBackward(a netip.Addr, holes []netip.Prefix) (netip.Addr, bool) {
	for {
		moved := false
		for _, h := range holes {
			if h.Contains(a) {
				a = h.Addr().Prev()
				if !a.IsValid() {
					return netip.Addr{}, false
				}
				moved = true
			}
		}
		if !moved {
			return a, true
		}
	}
}
