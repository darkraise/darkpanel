package eval

import (
	"fmt"
	"strings"

	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// modelledModules are the -m modules the walk decides for a packet; every
// other module in the parser's contract (icmp, icmp6, hl, limit, recent,
// statistic) makes the rules that use it undecidable.
var modelledModules = map[string]bool{
	"comment": true, "tcp": true, "udp": true, "multiport": true, "conntrack": true, "state": true,
	"addrtype": true, "mark": true, "connmark": true, "rt": true,
}

// Unmodelled lists the rules of rs's mangle and security tables that the
// walk cannot model (spec 2.1: a non-empty chain of those tables the
// evaluator does not support makes the host an unsupported configuration),
// as "<table>/<chain>#<n>: <what>" in the order of the save output, using
// the references of a verdict's trace. A rule counts when no packet makes
// the walk decide it: it lies outside the parser's contract, its target is
// one the walk does not run or runs only with other options, or it has a
// match the walk cannot decide and a target that would change the verdict
// (a rule that only logs, clamps the MSS or copies a mark is harmless
// whatever it matches). What depends on the packet, such as an unknown
// source port, does not count. The nat, raw and filter tables are not
// listed. Unmodelled reads only rs.
func Unmodelled(rs *netfilter.Ruleset) []string {
	if rs == nil {
		return nil
	}
	var out []string
	for _, name := range []string{"mangle", "security"} {
		t := rs.Table(name)
		if t == nil {
			continue
		}
		for _, c := range t.Chains {
			for i := range c.Rules {
				if why := ruleGap(&c.Rules[i]); why != "" {
					out = append(out, fmt.Sprintf("%s/%s#%d: %s", name, c.Name, i+1, why))
				}
			}
		}
	}
	return out
}

// ruleGap says why the walk cannot model r, or "" when it can.
func ruleGap(r *netfilter.Rule) string {
	if r.Opaque() {
		return strings.Join(r.Unsupported, ", ")
	}
	if why := targetGap(r.Target); why != "" {
		return why
	}
	if !decides(r.Target) || copiesMark(r.Target) {
		return ""
	}
	for _, m := range r.Matches {
		if !modelledModules[m.Module] {
			return "match " + m.Module
		}
	}
	return ""
}

// targetGap says why the walk cannot run a target in a mangle or security
// table, or "" when it can.
func targetGap(tg netfilter.Target) string {
	if tg.Chain {
		return ""
	}
	switch tg.Name {
	case "", "ACCEPT", "DROP", "REJECT", "RETURN", "LOG", "TCPMSS":
		return ""
	case "MARK", "CONNMARK":
		if _, ok := parseMarkTarget(tg); !ok {
			return "target " + tg.Name + " with these options"
		}
		return ""
	case "DNAT", "SNAT", "MASQUERADE":
		return "target " + tg.Name + " outside the nat table"
	}
	return "target " + tg.Name
}

// copiesMark reports whether a target only copies one mark into the other,
// the case uncertainCopy handles when a rule's match is undecidable.
func copiesMark(tg netfilter.Target) bool {
	if tg.Name != "CONNMARK" || tg.Chain {
		return false
	}
	sp, ok := parseMarkTarget(tg)
	return ok && !sp.set
}
