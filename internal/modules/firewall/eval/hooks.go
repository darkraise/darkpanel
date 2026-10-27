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

// step is one table's built-in chain on the packet's path.
type step struct{ table, chain string }

// run walks the hooks the packet traverses (spec 2.3), each in the
// kernel's priority order: raw, mangle and nat before routing; mangle,
// filter and security on the input or forward path; then mangle and nat
// POSTROUTING for a packet that leaves.
func (w *walker) run() fwstate.Result {
	before := []step{{"raw", "PREROUTING"}, {"mangle", "PREROUTING"}, {"nat", "PREROUTING"}}
	if w.pkt.Path == "output" {
		before = []step{{"raw", "OUTPUT"}, {"mangle", "OUTPUT"}, {"nat", "OUTPUT"}}
	}
	if !w.steps(before) || !w.reroute() {
		return w.res
	}
	var after []step
	switch w.pkt.Path {
	case "input":
		after = []step{{"mangle", "INPUT"}, {"filter", "INPUT"}, {"security", "INPUT"}}
	case "forward":
		after = []step{{"mangle", "FORWARD"}, {"filter", "FORWARD"}, {"security", "FORWARD"},
			{"mangle", "POSTROUTING"}, {"nat", "POSTROUTING"}}
	default:
		after = []step{{"filter", "OUTPUT"}, {"security", "OUTPUT"}, {"mangle", "POSTROUTING"}, {"nat", "POSTROUTING"}}
	}
	if !w.steps(after) {
		return w.res
	}
	// nat INPUT runs after security INPUT; its SNAT rules are outside the
	// model.
	if w.pkt.Path == "input" && (w.pkt.CtState == "NEW" || w.pkt.CtState == "RELATED") && w.natRules("INPUT") {
		w.stop(fwstate.Unknown, "nat/INPUT has rules; the walk does not model that hook")
		return w.res
	}
	w.res.Verdict = fwstate.Accept
	return w.res
}

// steps walks each step's hook and reports whether the packet survived.
func (w *walker) steps(seq []step) bool {
	for _, s := range seq {
		// Only a connection's first packet passes the nat table's chains;
		// later packets reuse the mapping conntrack recorded. A related
		// connection passes them only when its expectation set no mapping,
		// which the walk cannot see.
		if s.table == "nat" && w.pkt.CtState != "NEW" {
			if w.pkt.CtState == "RELATED" && w.natRules(s.chain) {
				w.stop(fwstate.Unknown, "nat/"+s.chain+" has rules a related connection may or may not pass")
				return false
			}
			continue
		}
		if w.hook(s.table, s.chain) == final {
			return false
		}
	}
	return true
}

// natRules reports whether the nat table's built-in chain has rules, or
// cannot be read.
func (w *walker) natRules(chain string) bool {
	t := w.rs.Table("nat")
	if t == nil {
		return false
	}
	if t.Incompatible != "" {
		return true
	}
	c := t.Chain(chain)
	return c != nil && len(c.Rules) > 0
}

// reroute asks the router where the packet goes after nat rewrote its
// destination, and reports whether the walk can go on.
func (w *walker) reroute() bool {
	if !w.dnat {
		return true
	}
	dst := w.pkt.Dst.String()
	if w.route == nil {
		w.stop(fwstate.Unknown, "the destination was rewritten to "+dst+" and no router was given")
		return false
	}
	path, out, err := w.route(w.pkt)
	if err != nil {
		w.stop(fwstate.Unknown, fmt.Sprintf("route to %s: %v", dst, err))
		return false
	}
	valid := path == "input" || path == "forward" && out != ""
	if w.pkt.Path == "output" {
		valid = path == "output" && out != ""
	}
	if !valid {
		w.stop(fwstate.Unknown, fmt.Sprintf("route to %s gave path %q out %q", dst, path, out))
		return false
	}
	w.pkt.Path, w.pkt.OutIface = path, out
	if path == "input" {
		w.pkt.OutIface = ""
	}
	return true
}

// natTarget applies DNAT, SNAT and MASQUERADE, which end the nat table's
// hook. Only DNAT matters to the verdict: it changes where the packet goes.
func (w *walker) natTarget(t *netfilter.Table, tg netfilter.Target, ref string) outcome {
	if t.Name != "nat" {
		return w.stop(fwstate.Unknown, ref+": target "+tg.Name+" outside the nat table")
	}
	if tg.Name != "DNAT" {
		return accept
	}
	to := ""
	for _, o := range tg.Options {
		if o.Name == "to-destination" {
			to = o.Values[0]
		}
	}
	addr, port, err := natDestination(to, w.pkt.Family)
	if err != nil {
		return w.stop(fwstate.Unknown, ref+": "+err.Error())
	}
	w.pkt.Dst, w.dnat = addr, true
	if port != 0 {
		w.pkt.DstPort = port
	}
	return accept
}

// natDestination parses DNAT's --to-destination as iptables-save prints it:
// "addr", "addr:port", "[v6addr]" or "[v6addr]:port". Address and port
// ranges spread connections over several destinations, which a single walk
// cannot follow.
func natDestination(s string, fam fwstate.Family) (netip.Addr, uint16, error) {
	if s == "" {
		return netip.Addr{}, 0, errors.New("DNAT without --to-destination")
	}
	host, port := s, ""
	switch {
	case strings.HasPrefix(s, "["):
		h, rest, ok := strings.Cut(s[1:], "]")
		p, hasPort := strings.CutPrefix(rest, ":")
		if !ok || rest != "" && !hasPort {
			return netip.Addr{}, 0, fmt.Errorf("DNAT destination %q is malformed", s)
		}
		host, port = h, p
	case fam == fwstate.V4:
		host, port, _ = strings.Cut(s, ":")
	}
	if strings.Contains(host, "-") || strings.Contains(port, "-") {
		return netip.Addr{}, 0, fmt.Errorf("DNAT to a range %q is not modelled", s)
	}
	a, err := netip.ParseAddr(host)
	if err != nil || a.Is4() != (fam == fwstate.V4) || a.Zone() != "" {
		return netip.Addr{}, 0, fmt.Errorf("DNAT destination %q is not a %s address", s, fam)
	}
	if port == "" {
		return a, 0, nil
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 {
		return netip.Addr{}, 0, fmt.Errorf("DNAT destination %q has a bad port", s)
	}
	return a, uint16(n), nil
}
