// Package eval walks a loaded or predicted netfilter state for one packet
// and decides whether the kernel would accept, drop or reject it (spec
// 2.3). It is pure: it reads only the ruleset, the packet and the router it
// is given. Whatever it cannot model makes the verdict unknown, which every
// caller treats as a failure, so a gap in the model is never a false accept.
package eval

import (
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// maxDepth bounds nested jumps. The kernel refuses jump loops, but a
// predicted or hand-written ruleset has not been through the kernel.
const maxDepth = 64

// Evaluate walks rs, one family's tables, for packet p and returns the
// verdict, the reason for anything but an accept, and the trace of every
// rule that matched. route says where the packet goes once its destination
// has been rewritten; it may be nil when nothing rewrites it.
func Evaluate(rs *netfilter.Ruleset, p fwstate.Packet, route fwstate.Router) fwstate.Result {
	w, err := newWalker(rs, p, route)
	if err != nil {
		return fwstate.Result{Verdict: fwstate.Unknown, Reason: err.Error()}
	}
	return w.run()
}

// tri is the outcome of a match the walk may not be able to decide.
type tri int

const (
	no tri = iota
	yes
	maybe
)

// outcome is how a rule, a chain or a table's hook ended.
type outcome int

const (
	next   outcome = iota // fall through to the next rule, or back to the calling chain
	ret                   // RETURN: leave the current chain
	accept                // ACCEPT or a NAT target: this table's hook is done
	final                 // a drop, reject or unknown verdict
)

var ctStates = map[string]bool{"NEW": true, "ESTABLISHED": true, "RELATED": true, "INVALID": true, "UNTRACKED": true}

type walker struct {
	rs    *netfilter.Ruleset
	route fwstate.Router
	pkt   fwstate.Packet // the packet as the current hook sees it
	in    string         // the in-interface the current hook sees, "" for none
	out   string         // the out-interface the current hook sees, "" for none
	res   fwstate.Result
}

func newWalker(rs *netfilter.Ruleset, p fwstate.Packet, route fwstate.Router) (*walker, error) {
	if rs == nil {
		return nil, errors.New("eval: no ruleset for the packet's family")
	}
	var ok func(netip.Addr) bool
	switch p.Family {
	case fwstate.V4:
		ok = netip.Addr.Is4
	case fwstate.V6:
		ok = func(a netip.Addr) bool { return a.Is6() && !a.Is4In6() }
	default:
		return nil, fmt.Errorf("eval: family %q", p.Family)
	}
	if !ok(p.Src) {
		return nil, fmt.Errorf("eval: source %s is not a %s address", p.Src, p.Family)
	}
	if !ok(p.Dst) {
		return nil, fmt.Errorf("eval: destination %s is not a %s address", p.Dst, p.Family)
	}
	switch {
	case p.Path != "input" && p.Path != "forward" && p.Path != "output":
		return nil, fmt.Errorf("eval: path %q", p.Path)
	case p.Path != "output" && p.InIface == "":
		return nil, fmt.Errorf("eval: path %s needs an in-interface", p.Path)
	case p.Path != "input" && p.OutIface == "":
		return nil, fmt.Errorf("eval: path %s needs an out-interface", p.Path)
	case !ctStates[p.CtState]:
		return nil, fmt.Errorf("eval: conntrack state %q", p.CtState)
	case p.Proto == "":
		return nil, errors.New("eval: no protocol")
	}
	p.Proto = strings.ToLower(p.Proto)
	if p.Path == "output" {
		p.InIface = ""
	}
	return &walker{rs: rs, route: route, pkt: p, res: fwstate.Result{Trace: []string{}}}, nil
}

// run walks the hooks the packet traverses.
func (w *walker) run() fwstate.Result {
	chain := map[string]string{"input": "INPUT", "forward": "FORWARD", "output": "OUTPUT"}[w.pkt.Path]
	if w.hook("filter", chain) == final {
		return w.res
	}
	w.res.Verdict = fwstate.Accept
	return w.res
}

func (w *walker) stop(v fwstate.Verdict, reason string) outcome {
	w.res.Verdict, w.res.Reason = v, reason
	return final
}

// hook walks one built-in chain of one table. An absent table or chain
// registers no hook in the kernel, so it lets the packet through.
func (w *walker) hook(table, chain string) outcome {
	t := w.rs.Table(table)
	if t == nil {
		return accept
	}
	if t.Incompatible != "" {
		return w.stop(fwstate.Unknown, table+": "+t.Incompatible)
	}
	c := t.Chain(chain)
	if c == nil {
		return accept
	}
	// The kernel gives PREROUTING and INPUT no out-interface, and OUTPUT
	// and POSTROUTING no in-interface, whatever the packet's path.
	w.in, w.out = w.pkt.InIface, w.pkt.OutIface
	switch chain {
	case "PREROUTING", "INPUT":
		w.out = ""
	case "OUTPUT", "POSTROUTING":
		w.in = ""
	}
	if o := w.walkChain(t, c, 0); o == accept || o == final {
		return o
	}
	ref := table + "/" + chain
	switch c.Policy {
	case "ACCEPT":
		return accept
	case "DROP":
		return w.stop(fwstate.Drop, ref+" policy DROP")
	}
	return w.stop(fwstate.Unknown, ref+" has no policy")
}

func (w *walker) walkChain(t *netfilter.Table, c *netfilter.Chain, depth int) outcome {
	for i := range c.Rules {
		r := &c.Rules[i]
		ref := t.Name + "/" + c.Name + "#" + strconv.Itoa(i+1)
		if !w.header(r) {
			continue
		}
		if r.Opaque() {
			return w.stop(fwstate.Unknown, ref+": "+strings.Join(r.Unsupported, ", "))
		}
		m, why := w.modules(t, r)
		if m == no {
			continue
		}
		if m == maybe {
			if decides(r.Target) {
				return w.stop(fwstate.Unknown, ref+": "+why)
			}
			continue
		}
		w.res.Trace = append(w.res.Trace, ref)
		switch o := w.target(t, r, ref, depth); o {
		case next:
		case ret:
			return next
		default:
			return o
		}
	}
	return next
}

// decides reports whether a rule's target can change the verdict when the
// rule matches; a rule whose match the walk cannot decide is harmless only
// when it cannot.
func decides(tg netfilter.Target) bool {
	if tg.Chain {
		return true
	}
	switch tg.Name {
	case "", "LOG", "TCPMSS":
		return false
	}
	return true
}

func (w *walker) target(t *netfilter.Table, r *netfilter.Rule, ref string, depth int) outcome {
	tg := r.Target
	if tg.Chain {
		c := t.Chain(tg.Name)
		if c == nil {
			return w.stop(fwstate.Unknown, ref+": no chain "+tg.Name)
		}
		if depth >= maxDepth {
			return w.stop(fwstate.Unknown, fmt.Sprintf("%s: jumps nest deeper than %d", ref, maxDepth))
		}
		return w.walkChain(t, c, depth+1)
	}
	switch tg.Name {
	case "", "LOG", "TCPMSS":
		return next
	case "ACCEPT":
		return accept
	case "DROP":
		return w.stop(fwstate.Drop, ref+" drops")
	case "REJECT":
		return w.stop(fwstate.Reject, ref+" rejects")
	case "RETURN":
		return ret
	}
	return w.stop(fwstate.Unknown, ref+": target "+tg.Name+" is not modelled")
}

// header evaluates the options outside any -m module.
func (w *walker) header(r *netfilter.Rule) bool {
	return ifaceMatch(r.In, w.in) && ifaceMatch(r.Out, w.out) &&
		addrMatch(r.Source, w.pkt.Src) && addrMatch(r.Dest, w.pkt.Dst) &&
		protoMatch(r.Proto, w.pkt.Proto)
}

// ifaceMatch applies -i or -o; a trailing "+" matches every name with that
// prefix, and no interface at all is the empty name, as in the kernel.
func ifaceMatch(m netfilter.NameMatch, name string) bool {
	if m.Name == "" {
		return true
	}
	var ok bool
	if prefix, wild := strings.CutSuffix(m.Name, "+"); wild {
		ok = strings.HasPrefix(name, prefix)
	} else {
		ok = name == m.Name
	}
	return ok != m.Neg
}

func addrMatch(m netfilter.AddrMatch, a netip.Addr) bool {
	if !m.Prefix.IsValid() {
		return true
	}
	return m.Prefix.Contains(a) != m.Neg
}

var protoNumbers = map[string]int{
	"icmp": 1, "igmp": 2, "tcp": 6, "dccp": 33, "udp": 17, "ipv6": 41, "gre": 47,
	"esp": 50, "ah": 51, "ipv6-icmp": 58, "icmpv6": 58, "sctp": 132, "udplite": 136,
}

// protoNumber returns a protocol's number from its name as iptables-save or
// a caller spells it, or from its decimal number.
func protoNumber(name string) (int, bool) {
	name = strings.ToLower(name)
	if n, ok := protoNumbers[name]; ok {
		return n, true
	}
	n, err := strconv.Atoi(name)
	return n, err == nil && n >= 0 && n <= 255
}

func sameProto(a, b string) bool {
	na, oka := protoNumber(a)
	nb, okb := protoNumber(b)
	if oka && okb {
		return na == nb
	}
	return strings.EqualFold(a, b)
}

func protoMatch(m netfilter.NameMatch, proto string) bool {
	if m.Name == "" {
		return true
	}
	return (m.Name == "all" || sameProto(m.Name, proto)) != m.Neg
}

// modules evaluates the rule's -m matches.
func (w *walker) modules(_ *netfilter.Table, r *netfilter.Rule) (tri, string) {
	for _, m := range r.Matches {
		if m.Module != "comment" {
			return maybe, "match " + m.Module + " is not modelled"
		}
	}
	return yes, ""
}
