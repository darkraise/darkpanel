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

	unicastSpace6 = netip.MustParsePrefix("::/0")

	unicastHoles4 = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("224.0.0.0/4"),
		netip.MustParsePrefix("240.0.0.0/4"),
	}
	unicastHoles6 = []netip.Prefix{
		netip.MustParsePrefix("::/128"),
		netip.MustParsePrefix("::1/128"),
		netip.MustParsePrefix("ff00::/8"),
	}
)

// InternetCells partitions the public unicast space of one family by
// every address prefix any rule of rs matches on.
func InternetCells(rs *netfilter.Ruleset, v6 bool) []Cell {
	if v6 {
		return partition(rs, v6, space6, holes6, false)
	}
	return partition(rs, v6, space4, holes4, false)
}

// UnicastCells partitions the unicast space of one family, private and
// special-purpose ranges included, by every address prefix any rule of rs
// matches on. It leaves out the unspecified, loopback, multicast and
// reserved ranges and every address of host, the addresses a packet from
// this host's own stack would carry.
func UnicastCells(rs *netfilter.Ruleset, v6 bool, host []netip.Addr) []Cell {
	space, holes := space4, unicastHoles4
	if v6 {
		space, holes = unicastSpace6, unicastHoles6
	}
	holes = slices.Clone(holes)
	for _, a := range host {
		if a = a.Unmap(); a.IsValid() && a.Is6() == v6 {
			holes = append(holes, netip.PrefixFrom(a, a.BitLen()))
		}
	}
	return partition(rs, v6, space, holes, true)
}

// partition splits space at every address prefix any rule of rs matches on
// and trims each cell's ends out of holes. A cell may still span a hole
// between its ends unless cutHoles also splits space at every hole, which
// leaves the holes in no cell.
func partition(rs *netfilter.Ruleset, v6 bool, space netip.Prefix, holes []netip.Prefix, cutHoles bool) []Cell {
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

	if cutHoles {
		for _, h := range holes {
			addPrefix(h)
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
