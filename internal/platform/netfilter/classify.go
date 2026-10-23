package netfilter

import "fmt"

// arity is how many values an option takes.
type arity map[string]int

// modules are the -m modules inside the contract, with their options.
// They cover spec 2.3's list and everything the pinned UFW, Docker and
// tailscaled generate; the evaluator decides which of them it models.
var modules = map[string]arity{
	"tcp":       {"sport": 1, "dport": 1, "tcp-flags": 2},
	"udp":       {"sport": 1, "dport": 1},
	"icmp":      {"icmp-type": 1},
	"icmp6":     {"icmpv6-type": 1},
	"multiport": {"sports": 1, "dports": 1},
	"conntrack": {"ctstate": 1, "ctorigdst": 1, "ctorigdstport": 1},
	"state":     {"state": 1},
	"addrtype":  {"src-type": 1, "dst-type": 1},
	"comment":   {"comment": 1},
	"mark":      {"mark": 1},
	"connmark":  {"mark": 1},
	"limit":     {"limit": 1, "limit-burst": 1},
	"recent": {
		"set": 0, "update": 0, "rcheck": 0, "remove": 0, "name": 1, "mask": 1,
		"rsource": 0, "rdest": 0, "seconds": 1, "hitcount": 1, "reap": 0, "rttl": 0,
	},
	"statistic": {"mode": 1, "probability": 1, "every": 1, "packet": 1},
	"hl":        {"hl-eq": 1, "hl-lt": 1, "hl-gt": 1},
	"rt":        {"rt-type": 1},
}

// targets are the -j targets inside the contract, with their options.
var targets = map[string]arity{
	"ACCEPT":     {},
	"DROP":       {},
	"RETURN":     {},
	"REJECT":     {"reject-with": 1},
	"DNAT":       {"to-destination": 1},
	"SNAT":       {"to-source": 1},
	"MASQUERADE": {"to-ports": 1},
	"MARK":       {"set-xmark": 1},
	"CONNMARK":   {"set-xmark": 1, "save-mark": 0, "restore-mark": 0, "nfmask": 1, "ctmask": 1},
	"LOG": {
		"log-prefix": 1, "log-level": 1, "log-tcp-sequence": 0, "log-tcp-options": 0,
		"log-ip-options": 0, "log-uid": 0,
	},
	"TCPMSS": {"clamp-mss-to-pmtu": 0, "set-mss": 1},
}

// negatable are the module options iptables-save prints with "!".
var negatable = map[string]bool{
	"sport": true, "dport": true, "sports": true, "dports": true, "tcp-flags": true,
	"icmp-type": true, "icmpv6-type": true, "ctstate": true, "ctorigdst": true,
	"ctorigdstport": true, "state": true, "src-type": true, "dst-type": true,
	"mark": true, "rt-type": true,
}

// classify appends to r.Unsupported every module, target or option
// outside the contract. A jump to a declared chain takes no options.
func classify(r *Rule) {
	for _, m := range r.Matches {
		known, ok := modules[m.Module]
		if !ok {
			r.Unsupported = append(r.Unsupported, "match "+m.Module)
			continue
		}
		checkOptions(r, "match "+m.Module, known, m.Options, true)
	}
	switch {
	case r.Target.Name == "":
	case r.Target.Chain:
		checkOptions(r, "jump to "+r.Target.Name, arity{}, r.Target.Options, false)
	default:
		known, ok := targets[r.Target.Name]
		if !ok {
			r.Unsupported = append(r.Unsupported, "target "+r.Target.Name)
			return
		}
		checkOptions(r, "target "+r.Target.Name, known, r.Target.Options, false)
	}
}

// negOK allows "!" on the options negatable lists; no target option is.
func checkOptions(r *Rule, where string, known arity, opts []Option, negOK bool) {
	for _, o := range opts {
		n, ok := known[o.Name]
		switch {
		case !ok:
			r.Unsupported = append(r.Unsupported, fmt.Sprintf("%s option --%s", where, o.Name))
		case len(o.Values) != n:
			r.Unsupported = append(r.Unsupported, fmt.Sprintf("%s option --%s with %d values", where, o.Name, len(o.Values)))
		case o.Neg && (!negOK || !negatable[o.Name]):
			r.Unsupported = append(r.Unsupported, fmt.Sprintf("%s option ! --%s", where, o.Name))
		}
	}
}
