package guard

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/modules/firewall/eval"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// cellFlows is fl once per cell of the internet that the checked ruleset
// of its family partitions the public space into, with the wild end set
// to the cell's representative address. Only the rules that can match fl
// cut cells, and so does every route of the host's family outside the local
// table that sends or refuses a range (routeCutters) and every prefix of a
// policy rule, since a rule that matches an interface sets apart the addresses
// routed through it. A unicast flow ranges over the unicast space less the
// host's own addresses (eval.UnicastCells) instead of the internet. A flow to
// any internet address needs every cell to pass. A flow from any internet
// address, given a before-state, fails only on a cell the before-state
// neither drops nor rejects, so its cells are cut by the prefixes of both
// states. The longest route that contains a cell decides what the cell is.
// Its copy in a cell of a route without a path is a stand-in: when no reply
// reaches the cell, this host cannot answer a source there. A copy in a cell
// of a blackhole route is marked as such, since the kernel answers a lookup
// there with EINVAL. A flow without a wild end, or one that is not evaluated,
// is returned as it is; one whose host routes cannot be read is unknown. So is
// one over the cell limit: every cell is evaluated against the whole ruleset,
// so an unbounded check would hold the request, and a check that is not made
// must not pass.
func (r *router) cellFlows(fl flow, before *fwstate.Loaded) []flow {
	if fl.wild == wildNone || fl.unknown != "" {
		return []flow{fl}
	}
	v6 := fl.pkt.Family == fwstate.V6
	routes := r.routeCuts(v6)
	if routes.why != "" {
		fl.unknown = routes.why
		return []flow{fl}
	}
	rs := r.st.V4
	if v6 {
		rs = r.st.V6
	}
	var brs *netfilter.Ruleset
	if before != nil {
		brs = before.V4
		if v6 {
			brs = before.V6
		}
	}
	cut := cutters(rs, v6, fl.pkt.Proto, fl.pkt.DstPort)
	if fl.wild == wildSrc && brs != nil {
		cut = joinTables(cut, cutters(brs, v6, fl.pkt.Proto, fl.pkt.DstPort))
	}
	var cells []eval.Cell
	if fl.unicast {
		cells = eval.UnicastCells(joinTables(cut, routes.rs), v6, r.hostAddrs(v6))
	} else {
		cells = eval.InternetCells(joinTables(cut, routes.rs), v6)
	}
	rules := flowRules(fl, rs, brs)
	if limit, over := overCellLimit(len(cells), rules); over {
		space := "the internet"
		if fl.unicast {
			space = "the unicast address space"
		}
		fl.unknown = fmt.Sprintf("the rules and routes split %s into %d ranges for this flow; checking them against %d rules is more than the guard evaluates (limit %d)", space, len(cells), rules, limit)
		return []flow{fl}
	}
	refused := slices.DeleteFunc(slices.Clone(routes.noPath), func(p netip.Prefix) bool {
		return slices.Contains(routes.blackholes, p)
	})
	out := make([]flow, 0, len(cells))
	for _, c := range cells {
		cf := fl
		path := longestContaining(routes.paths, c.Rep)
		if fl.wild == wildSrc {
			cf.pkt.Src = c.Rep
			if n := longestContaining(routes.noPath, c.Rep); n >= 0 && n >= path {
				cf.standIn = true
			}
		} else {
			cf.pkt.Dst = c.Rep
		}
		n := longestContaining(routes.blackholes, c.Rep)
		cf.blackhole = n >= 0 && n >= path && n >= longestContaining(refused, c.Rep)
		cf.span = c.From.String() + "-" + c.To.String()
		out = append(out, cf)
	}
	return out
}

func (r *router) hostAddrs(v6 bool) []netip.Addr {
	var out []netip.Addr
	for a := range r.local {
		if a.Is6() == v6 {
			out = append(out, a)
		}
	}
	return out
}

// longestContaining is the length of the longest of ps that contains a, or
// -1 when none does.
func longestContaining(ps []netip.Prefix, a netip.Addr) int {
	longest := -1
	for _, p := range ps {
		if p.Bits() > longest && p.Contains(a) {
			longest = p.Bits()
		}
	}
	return longest
}

const (
	maxCells           = 65536
	maxCellEvaluations = 10_000_000
)

// overCellLimit reports whether a flow of cells cells against rules rules
// costs more than the guard evaluates, and the limit it exceeds.
func overCellLimit(cells, rules int) (limit int, over bool) {
	if cells > maxCells {
		return maxCells, true
	}
	if cells*(rules+1) > maxCellEvaluations {
		return maxCellEvaluations, true
	}
	return 0, false
}

// flowRules is the number of rules a cell of fl is evaluated against.
func flowRules(fl flow, rs, before *netfilter.Ruleset) int {
	n := ruleCount(rs)
	if fl.wild == wildSrc {
		n += ruleCount(before)
	}
	return n
}

// ruleCount is the number of rules in every chain of every table of rs.
func ruleCount(rs *netfilter.Ruleset) int {
	if rs == nil {
		return 0
	}
	n := 0
	for ti := range rs.Tables {
		for ci := range rs.Tables[ti].Chains {
			n += len(rs.Tables[ti].Chains[ci].Rules)
		}
	}
	return n
}

// joinTables is a ruleset holding a's tables followed by b's. The joined
// ruleset only cuts cells and is never evaluated, as it may hold two tables
// of one name.
func joinTables(a, b *netfilter.Ruleset) *netfilter.Ruleset {
	if a == nil && b == nil {
		return nil
	}
	out := &netfilter.Ruleset{}
	for _, rs := range []*netfilter.Ruleset{a, b} {
		if rs != nil {
			out.Tables = append(out.Tables, rs.Tables...)
		}
	}
	return out
}

// routeCut is routeCutters of the host's routes of one family, or why they
// cannot be read. Its prefix lists are compared by length, since the longest
// route containing an address decides that address. rules holds the family's
// policy rules, whose prefixes cut cells as well.
type routeCut struct {
	rs         *netfilter.Ruleset
	noPath     []netip.Prefix
	blackholes []netip.Prefix
	paths      []netip.Prefix
	rules      []netstat.Rule
	why        string
	rulesWhy   string
}

// routeCuts is the route cut of one family. The routes of every table are
// dumped on the first call for the family and kept for the check.
func (r *router) routeCuts(v6 bool) routeCut {
	if c, ok := r.routes[v6]; ok {
		return c
	}
	var c routeCut
	fam := netstat.IPv4
	if v6 {
		fam = netstat.IPv6
	}
	var rules []netstat.Rule
	rulesWhy := ""
	if d, ok := r.host.(ruleDumper); ok {
		var err error
		if rules, err = d.Rules(fam); err != nil {
			// Unlike why, this also makes a flow without a wild end unknown,
			// because that flow's route lookups depend on the rules.
			rulesWhy = fmt.Sprintf("the policy rules cannot be read: %v", err)
		}
	}
	if d, ok := r.host.(routeDumper); !ok {
		c.why = "the host cannot list routes"
	} else if routes, err := d.Routes(fam); err != nil {
		c.why = fmt.Sprintf("the routes cannot be read: %v", err)
	} else {
		c = routeCutters(routes, v6)
		c.why = rulesWhy
		r.addPolicyCuts(&c, rules)
	}
	c.rules, c.rulesWhy = rules, rulesWhy
	r.routes[v6] = c
	return c
}

// addPolicyCuts cuts cells at the prefixes of rules, since a policy rule sets
// apart the addresses it sends to another table.
func (r *router) addPolicyCuts(c *routeCut, rules []netstat.Rule) {
	chain := &c.rs.Tables[0].Chains[0]
	for _, rule := range rules {
		for _, p := range []netip.Prefix{rule.Src, rule.Dst} {
			// A cell of one of the host's own addresses would turn the flow
			// into input to the host.
			if !p.IsValid() || (p.IsSingleIP() && r.local[p.Addr().Unmap()]) {
				continue
			}
			chain.Rules = append(chain.Rules, netfilter.Rule{Dest: netfilter.AddrMatch{Prefix: p}})
		}
	}
}

// cutRouteTypes are the types of the routes that cut cells: unicast and local
// routes, which give a range a path, and the routes without a path, which
// hand a range on to a later table (throw) or refuse it.
var cutRouteTypes = []string{"unicast", "local", "throw", "unreachable", "prohibit", "blackhole"}

// routeCutters is the route cut of the routes of a type in cutRouteTypes and
// of the family outside the local table: a ruleset of one table with one
// chain that holds one rule per route, matching the route's destination; the
// destinations of the routes with a path (unicast and local); those of the
// routes without one; and those of the blackhole routes. Like joinTables'
// result the ruleset only cuts cells and is never evaluated. Every route that
// sends or refuses a range cuts it, so no cell mixes routed and unrouted
// addresses, and the lowest address of a cell takes the route every address
// of it takes. The local table holds the host's own addresses, which are no
// internet address a flow reaches out to or comes from; a cell of one would
// turn the flow into input to the host.
func routeCutters(routes []netstat.Route, v6 bool) routeCut {
	var c routeCut
	ch := netfilter.Chain{Name: "routes"}
	for _, rt := range routes {
		if !slices.Contains(cutRouteTypes, rt.Type) || rt.Table == unix.RT_TABLE_LOCAL || !rt.Dst.IsValid() || rt.Dst.Addr().Is6() != v6 {
			continue
		}
		ch.Rules = append(ch.Rules, netfilter.Rule{Dest: netfilter.AddrMatch{Prefix: rt.Dst}})
		switch rt.Type {
		case "unicast", "local":
			c.paths = append(c.paths, rt.Dst)
		case "blackhole":
			c.noPath = append(c.noPath, rt.Dst)
			c.blackholes = append(c.blackholes, rt.Dst)
		default:
			c.noPath = append(c.noPath, rt.Dst)
		}
	}
	c.rs = &netfilter.Ruleset{Tables: []netfilter.Table{{Name: "routes", Chains: []netfilter.Chain{ch}}}}
	return c
}

// checkFlow evaluates fl in every cell of its wild end and returns the
// results of the first blocked cell; later cells are not evaluated. When no
// cell is blocked, the results of the first cell that produced any stand for
// all of them. A cell produces none only where a missing route has nothing
// to protect: a wild destination without a route of its family, or a wild
// source in a cell of a route without a path that no reply reaches; a
// missing route anywhere else makes the flow unknown. A flow to
// any internet address needs every cell to pass. A flow from any internet
// address, given a before-state, fails only on a cell the before-state
// neither drops nor rejects: a probe the before-state already blocked is
// left out and counted in skipped, while one it cannot decide still blocks
// the cell. The before-state is routed by its own forwarding sysctl values.
func (r *router) checkFlow(fl flow, before *fwstate.Loaded, ev func(*netfilter.Ruleset, fwstate.Packet, fwstate.Router) fwstate.Result) (results []FlowResult, skipped int) {
	var prior *router
	if fl.wild == wildSrc && before != nil {
		prior = r.forState(*before)
	}
	for _, cf := range r.cellFlows(fl, before) {
		var cell []FlowResult
		blocked := false
		for _, p := range r.probes(cf) {
			res := evaluate(r, r.st, p, ev)
			if res.Verdict != fwstate.Accept {
				if prior != nil {
					if v := evaluate(prior, *before, p, ev).Verdict; v == fwstate.Drop || v == fwstate.Reject {
						skipped++
						continue
					}
				}
				blocked = true
			}
			cell = append(cell, FlowResult{Flow: p.desc, Result: res})
		}
		if blocked {
			return cell, skipped
		}
		if results == nil {
			results = cell
		}
	}
	return results, skipped
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
// DNAT target names counts as the packet's too. An unmodelled target may
// rewrite the packet in a way the port filter cannot see, so while rs holds
// one every rule of a reached chain is kept.
func cutters(rs *netfilter.Ruleset, v6 bool, proto string, port uint16) *netfilter.Ruleset {
	if rs == nil {
		return nil
	}
	ports := append([]uint16{port}, natPorts(rs, v6)...)
	filter := targetsModelled(rs)
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
				if filter && !mayMatch(rule, proto, ports) {
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

// modelledTargets are the targets whose effect on a packet's port the port
// filter of cutters accounts for.
var modelledTargets = []string{"", "ACCEPT", "DROP", "REJECT", "RETURN", "LOG", "TCPMSS", "MARK", "CONNMARK", "DNAT", "SNAT", "MASQUERADE"}

// targetsModelled reports whether every rule of rs jumps to a chain or has a
// target in modelledTargets.
func targetsModelled(rs *netfilter.Ruleset) bool {
	for ti := range rs.Tables {
		for ci := range rs.Tables[ti].Chains {
			for _, rule := range rs.Tables[ti].Chains[ci].Rules {
				if !rule.Target.Chain && !slices.Contains(modelledTargets, rule.Target.Name) {
					return false
				}
			}
		}
	}
	return true
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
