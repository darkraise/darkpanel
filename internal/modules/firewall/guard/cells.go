package guard

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/modules/firewall/eval"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// cellFlows is fl once per cell of the internet that the checked ruleset
// of its family partitions the public space into, with the wild end set
// to the cell's representative address. Only the rules that can match fl
// cut cells. A flow without a wild end, or one that is not evaluated, is
// returned as it is.
func (r *router) cellFlows(fl flow) []flow {
	if fl.wild == wildNone || fl.unknown != "" {
		return []flow{fl}
	}
	v6 := fl.pkt.Family == fwstate.V6
	rs := r.st.V4
	if v6 {
		rs = r.st.V6
	}
	cells := eval.InternetCells(cutters(rs, v6, fl.pkt.Proto, fl.pkt.DstPort), v6)
	out := make([]flow, 0, len(cells))
	for _, c := range cells {
		cf := fl
		if fl.wild == wildSrc {
			cf.pkt.Src = c.Rep
		} else {
			cf.pkt.Dst = c.Rep
		}
		cf.span = c.From.String() + "-" + c.To.String()
		out = append(out, cf)
	}
	return out
}

// checkFlow evaluates fl in every cell of its wild end. Every cell must let
// the flow through, so the results of the first blocked cell are returned
// and later cells are not evaluated. When no cell is blocked, the results
// of the first cell that produced any stand for all of them; a cell without
// a route of its family produces none.
func (r *router) checkFlow(fl flow, ev func(*netfilter.Ruleset, fwstate.Packet, fwstate.Router) fwstate.Result) []FlowResult {
	var first []FlowResult
	for _, cf := range r.cellFlows(fl) {
		var results []FlowResult
		blocked := false
		for _, p := range r.probes(cf) {
			res := evaluate(r, p, ev)
			blocked = blocked || res.Verdict != fwstate.Accept
			results = append(results, FlowResult{Flow: p.desc, Result: res})
		}
		if blocked {
			return results
		}
		if first == nil {
			first = results
		}
	}
	return first
}

// hookChains are the chains the kernel runs packets through; every other
// chain is entered only by a jump.
var hookChains = []string{"PREROUTING", "INPUT", "FORWARD", "OUTPUT", "POSTROUTING"}

// cutters is rs without the rules that cannot match a packet of proto to
// port, and without the chains that only such rules lead to. A rule that
// cannot match gives every address the same verdict, so its prefixes need
// not cut cells; thousands of bans on another port would otherwise multiply
// the cells, and every cell is evaluated against the whole ruleset. A DNAT
// rule may rewrite the port before later rules see it, so every port a
// DNAT target names counts as the packet's too.
func cutters(rs *netfilter.Ruleset, v6 bool, proto string, port uint16) *netfilter.Ruleset {
	if rs == nil {
		return nil
	}
	ports := append([]uint16{port}, natPorts(rs, v6)...)
	out := &netfilter.Ruleset{Tables: make([]netfilter.Table, 0, len(rs.Tables))}
	for ti := range rs.Tables {
		t := &rs.Tables[ti]
		kept := map[string][]netfilter.Rule{}
		reached := map[string]bool{}
		var queue []string
		reach := func(name string) {
			if name != "" && !reached[name] {
				reached[name] = true
				queue = append(queue, name)
			}
		}
		for _, c := range t.Chains {
			if c.Builtin || slices.Contains(hookChains, c.Name) {
				reach(c.Name)
			}
		}
		for len(queue) > 0 {
			c := t.Chain(queue[0])
			queue = queue[1:]
			if c == nil {
				continue
			}
			for _, rule := range c.Rules {
				if !mayMatch(rule, proto, ports) {
					continue
				}
				kept[c.Name] = append(kept[c.Name], rule)
				if rule.Target.Chain {
					reach(rule.Target.Name)
				}
				reach(rule.Goto)
			}
		}
		nt := netfilter.Table{Name: t.Name, Incompatible: t.Incompatible}
		for _, c := range t.Chains {
			if reached[c.Name] {
				c.Rules = kept[c.Name]
				nt.Chains = append(nt.Chains, c)
			}
		}
		out.Tables = append(out.Tables, nt)
	}
	return out
}

// mayMatch reports whether rule can match a packet of proto to one of
// ports. It is false only when the walk skips the rule for every such
// packet, whatever its addresses.
func mayMatch(rule netfilter.Rule, proto string, ports []uint16) bool {
	if !protoMayMatch(rule.Proto, proto) {
		return false
	}
	// The walk decides an opaque rule without its -m matches.
	if rule.Opaque() {
		return true
	}
	for _, m := range rule.Matches {
		switch m.Module {
		case "tcp", "udp":
			if (proto == "tcp" || proto == "udp") && m.Module != proto {
				return false
			}
		case "multiport":
		default:
			continue
		}
		for _, o := range m.Options {
			if (o.Name == "dport" || o.Name == "dports") && len(o.Values) > 0 && !portsMayMatch(o, ports) {
				return false
			}
		}
	}
	return true
}

// cutProtos are the numbers of the protocol names iptables-save prints that
// protoMayMatch tells apart; any other name may match.
var cutProtos = map[string]int{
	"icmp": 1, "tcp": 6, "udp": 17, "dccp": 33, "gre": 47, "esp": 50, "ah": 51,
	"ipv6-icmp": 58, "icmpv6": 58, "icmp6": 58, "sctp": 132, "udplite": 136,
}

func protoNum(name string) (int, bool) {
	name = strings.ToLower(name)
	if n, ok := cutProtos[name]; ok {
		return n, true
	}
	n, err := strconv.Atoi(name)
	return n, err == nil && n > 0 && n <= 255
}

// protoMayMatch reports whether -p m can match a packet of proto.
func protoMayMatch(m netfilter.NameMatch, proto string) bool {
	if m.Name == "" {
		return true
	}
	rn, ok := protoNum(m.Name)
	pn, known := protoNum(proto)
	if !ok || !known {
		return true
	}
	return (rn == pn) != m.Neg
}

// portsMayMatch reports whether a --dport or --dports option lets one of
// ports through. A list the walk cannot read may match.
func portsMayMatch(o netfilter.Option, ports []uint16) bool {
	var spans [][2]uint16
	for item := range strings.SplitSeq(o.Values[0], ",") {
		a, b, isRange := strings.Cut(item, ":")
		if !isRange {
			b = a
		}
		lo, err1 := strconv.ParseUint(cmp.Or(a, "0"), 10, 16)
		hi, err2 := strconv.ParseUint(cmp.Or(b, "65535"), 10, 16)
		if err1 != nil || err2 != nil {
			return true
		}
		spans = append(spans, [2]uint16{uint16(lo), uint16(hi)})
	}
	for _, p := range ports {
		in := slices.ContainsFunc(spans, func(s [2]uint16) bool { return s[0] <= p && p <= s[1] })
		if in != o.Neg {
			return true
		}
	}
	return false
}

// natPorts are the ports the DNAT targets of rs rewrite a destination port
// to: "addr:port" or "[v6addr]:port". A destination the walk cannot follow
// stops the walk at its rule, so no later rule sees its port.
func natPorts(rs *netfilter.Ruleset, v6 bool) []uint16 {
	t := rs.Table("nat")
	if t == nil {
		return nil
	}
	var out []uint16
	for _, c := range t.Chains {
		for _, rule := range c.Rules {
			if rule.Target.Name != "DNAT" {
				continue
			}
			for _, o := range rule.Target.Options {
				if o.Name != "to-destination" || len(o.Values) == 0 {
					continue
				}
				var port string
				if rest, ok := strings.CutPrefix(o.Values[0], "["); ok {
					_, port, _ = strings.Cut(rest, "]:")
				} else if !v6 {
					_, port, _ = strings.Cut(o.Values[0], ":")
				}
				if n, err := strconv.ParseUint(port, 10, 16); err == nil && n != 0 {
					out = append(out, uint16(n))
				}
			}
		}
	}
	return out
}
