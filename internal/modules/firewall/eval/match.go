package eval

import (
	"net/netip"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// modules evaluates the rule's -m matches. One false match makes the rule
// miss whatever the others say; otherwise the first undecidable match makes
// the rule undecidable.
func (w *walker) modules(t *netfilter.Table, r *netfilter.Rule) (tri, string) {
	res, why := yes, ""
	for _, m := range r.Matches {
		v, reason := w.module(t, m)
		switch {
		case v == no:
			return no, ""
		case v == maybe && res == yes:
			res, why = maybe, reason
		}
	}
	return res, why
}

func (w *walker) module(t *netfilter.Table, m netfilter.Match) (tri, string) {
	switch m.Module {
	case "comment":
		return yes, ""
	case "tcp", "udp":
		if !sameProto(m.Module, w.pkt.Proto) {
			return no, ""
		}
		return w.options(m, w.portOption)
	case "multiport":
		if !hasPorts(w.pkt.Proto) {
			return no, ""
		}
		return w.options(m, w.portOption)
	case "icmp", "icmp6":
		proto := map[string]string{"icmp": "icmp", "icmp6": "ipv6-icmp"}[m.Module]
		if !sameProto(proto, w.pkt.Proto) {
			return no, ""
		}
		return maybe, "ICMP types are not modelled"
	case "conntrack", "state":
		// The raw table runs before connection tracking attaches a
		// connection to the packet.
		if t.Name == "raw" {
			return maybe, "the raw table runs before connection tracking"
		}
		return w.options(m, w.conntrackOption)
	case "addrtype":
		return w.options(m, w.addrtypeOption)
	case "mark", "connmark":
		return w.markMatch(m)
	case "rt":
		// The packets the guard evaluates carry no IPv6 extension headers,
		// and the kernel's rt match never matches a packet without a
		// routing header, negated or not.
		return no, ""
	case "hl":
		return maybe, "the hop limit is not modelled"
	case "limit", "recent", "statistic":
		return maybe, "match " + m.Module + " depends on rates or earlier packets"
	}
	return maybe, "match " + m.Module + " is not modelled"
}

// options combines a module's options, each decided by check; a negated
// option inverts a decided result.
func (w *walker) options(m netfilter.Match, check func(o netfilter.Option) (tri, string)) (tri, string) {
	res, why := yes, ""
	for _, o := range m.Options {
		v, reason := check(o)
		if o.Neg && v != maybe {
			v = toTri(v == no)
		}
		switch {
		case v == no:
			return no, ""
		case v == maybe && res == yes:
			res, why = maybe, reason
		}
	}
	return res, why
}

func hasPorts(proto string) bool {
	for _, p := range []string{"tcp", "udp", "sctp", "dccp", "udplite"} {
		if sameProto(p, proto) {
			return true
		}
	}
	return false
}

func (w *walker) portOption(o netfilter.Option) (tri, string) {
	switch o.Name {
	case "sport", "sports":
		// Port 0 is the caller's way of saying the connection does not
		// exist yet, so the kernel has not chosen its source port.
		if w.pkt.SrcPort == 0 {
			return maybe, "the source port of a connection that does not exist yet is unknown"
		}
		return portIn(o.Values[0], w.pkt.SrcPort)
	case "dport", "dports":
		return portIn(o.Values[0], w.pkt.DstPort)
	case "tcp-flags":
		return w.tcpFlags(o.Values[0], o.Values[1])
	}
	return maybe, "option --" + o.Name + " is not modelled"
}

// portIn reports whether port is in a list of ports and ranges as
// iptables-save prints them ("22", "1000:2000", "443,8000:8100").
func portIn(list string, port uint16) (tri, string) {
	for item := range strings.SplitSeq(list, ",") {
		lo, hi, ok := portRange(item)
		if !ok {
			return maybe, "port " + strconv.Quote(item) + " is not a number"
		}
		if lo <= port && port <= hi {
			return yes, ""
		}
	}
	return no, ""
}

func portRange(s string) (lo, hi uint16, ok bool) {
	a, b, isRange := strings.Cut(s, ":")
	if !isRange {
		b = a
	}
	if a == "" {
		a = "0"
	}
	if b == "" {
		b = "65535"
	}
	l, err1 := strconv.ParseUint(a, 10, 16)
	h, err2 := strconv.ParseUint(b, 10, 16)
	return uint16(l), uint16(h), err1 == nil && err2 == nil
}

var tcpFlagBits = map[string]uint8{
	"FIN": 0x01, "SYN": 0x02, "RST": 0x04, "PSH": 0x08, "ACK": 0x10, "URG": 0x20, "ALL": 0x3f, "NONE": 0,
}

// tcpFlags applies --tcp-flags. Only a new connection's first packet has
// known flags: SYN alone.
func (w *walker) tcpFlags(maskList, setList string) (tri, string) {
	if w.pkt.CtState != "NEW" {
		return maybe, "TCP flags are known only for a new connection's first packet"
	}
	mask, ok1 := flagSet(maskList)
	set, ok2 := flagSet(setList)
	if !ok1 || !ok2 {
		return maybe, "TCP flags " + maskList + " " + setList + " are not modelled"
	}
	if 0x02&mask == set {
		return yes, ""
	}
	return no, ""
}

func flagSet(list string) (uint8, bool) {
	var bits uint8
	for name := range strings.SplitSeq(list, ",") {
		b, ok := tcpFlagBits[name]
		if !ok {
			return 0, false
		}
		bits |= b
	}
	return bits, true
}

func (w *walker) conntrackOption(o netfilter.Option) (tri, string) {
	isNew := w.pkt.CtState == "NEW"
	switch o.Name {
	case "ctstate", "state":
		return w.ctstate(o.Values[0])
	case "ctorigdst":
		if !isNew {
			return maybe, "the original destination is known only for a new connection"
		}
		pfx, err := netip.ParsePrefix(o.Values[0])
		if err != nil {
			a, err := netip.ParseAddr(o.Values[0])
			if err != nil {
				return maybe, "original destination " + strconv.Quote(o.Values[0]) + " is not an address"
			}
			pfx = netip.PrefixFrom(a, a.BitLen())
		}
		return toTri(pfx.Contains(w.origDst)), ""
	case "ctorigdstport":
		if !isNew {
			return maybe, "the original destination is known only for a new connection"
		}
		return portIn(o.Values[0], w.origPort)
	}
	return maybe, "option --" + o.Name + " is not modelled"
}

// ctstate matches a conntrack state list. The DNAT and SNAT states say
// whether the connection's destination or source was rewritten, which the
// walk knows only for a connection it sees start.
func (w *walker) ctstate(list string) (tri, string) {
	has := func(dnat, snat bool) bool {
		for s := range strings.SplitSeq(list, ",") {
			if s == w.pkt.CtState || s == "DNAT" && dnat || s == "SNAT" && snat {
				return true
			}
		}
		return false
	}
	if w.pkt.CtState == "NEW" {
		// SNAT is applied in nat POSTROUTING, after every filtering hook.
		return toTri(has(w.dnat, false)), ""
	}
	v := has(false, false)
	if v != has(true, false) || v != has(false, true) || v != has(true, true) {
		return maybe, "the connection's NAT state is known only for a new connection"
	}
	return toTri(v), ""
}

func (w *walker) addrtypeOption(o netfilter.Option) (tri, string) {
	var dst bool
	switch o.Name {
	case "src-type":
	case "dst-type":
		dst = true
	default:
		return maybe, "option --" + o.Name + " is not modelled"
	}
	res, why := no, ""
	for typ := range strings.SplitSeq(o.Values[0], ",") {
		switch v, reason := w.addrType(dst, typ); v {
		case yes:
			return yes, ""
		case maybe:
			res, why = maybe, reason
		}
	}
	return res, why
}

// addrType reports whether the packet's source or destination has an
// address type, as the routing decision implies: a packet on the input
// path is addressed to this host and one on the forward path is not; a
// locally generated packet is addressed to this host when it leaves
// through lo. Only a packet arriving on lo or generated here has a local
// source.
func (w *walker) addrType(dst bool, typ string) (tri, string) {
	a := w.pkt.Src
	local := w.pkt.Path == "output" || w.pkt.InIface == "lo"
	if dst {
		a = w.pkt.Dst
		local = w.pkt.Path == "input" || w.pkt.Path == "output" && w.pkt.OutIface == "lo"
	}
	switch {
	case a.IsMulticast():
		return toTri(typ == "MULTICAST"), ""
	case dst && a == netip.AddrFrom4([4]byte{255, 255, 255, 255}):
		return toTri(typ == "BROADCAST"), ""
	}
	switch typ {
	case "LOCAL":
		return toTri(local), ""
	case "BROADCAST", "MULTICAST":
		return no, ""
	case "UNICAST":
		if local || dst {
			return toTri(!local), ""
		}
		return maybe, "whether the kernel has a unicast route back to the source is not known"
	}
	return maybe, "address type " + typ + " is not modelled"
}

func toTri(b bool) tri {
	if b {
		return yes
	}
	return no
}
