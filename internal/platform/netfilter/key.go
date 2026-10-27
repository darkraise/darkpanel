package netfilter

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Key is the canonical text of what r matches and does, in a family (ipv6
// false for iptables, true for ip6tables), for comparing a rule read from
// restore input with one read from save output: prediction, the in-sync
// check and verify compare rules by Key, never by text. It renders -s, -d,
// -i, -o and -p in that order, then each module in the rule's order with
// its options sorted, then the target, and it fills in what iptables-save
// prints although the input may omit it: the protocol's implicit module
// (ParseRestore adds it), REJECT's default --reject-with, recent's default
// --name, --mask and --rsource, limit's default --limit; it drops what
// iptables-save leaves out: a non-negated /0 address, -p all, limit's
// default --limit-burst, LOG's default --log-level; and it spells values as
// iptables-save does: masked prefixes, protocol names, ICMP types as
// numbers, limit units, conntrack states in the kernel's order, marks in
// hexadecimal. The key of an opaque rule is its text, so two opaque rules
// compare equal only when written alike.
func Key(r Rule, ipv6 bool) string {
	if r.Opaque() {
		return "opaque " + strings.Join(strings.Fields(r.Text), " ")
	}
	var b []string
	add := func(neg bool, opt string, vals ...string) {
		if neg {
			b = append(b, "!")
		}
		b = append(b, opt)
		for _, v := range vals {
			b = append(b, keyQuote(v))
		}
	}
	for _, a := range []struct {
		opt string
		m   AddrMatch
	}{{"-s", r.Source}, {"-d", r.Dest}} {
		if a.m.Prefix.IsValid() && (a.m.Neg || a.m.Prefix.Bits() != 0) {
			add(a.m.Neg, a.opt, a.m.Prefix.Masked().String())
		}
	}
	for _, n := range []struct {
		opt string
		m   NameMatch
	}{{"-i", r.In}, {"-o", r.Out}} {
		if n.m.Name != "" {
			add(n.m.Neg, n.opt, n.m.Name)
		}
	}
	if p := canonicalProto(r.Proto.Name); p != "" && (p != "all" || r.Proto.Neg) {
		add(r.Proto.Neg, "-p", p)
	}
	for _, m := range r.Matches {
		add(false, "-m", m.Module)
		for _, o := range keyOptions(m.Module, m.Options, ipv6, keyMatchDefaults) {
			add(o.Neg, "--"+o.Name, o.Values...)
		}
	}
	if r.Target.Name != "" {
		add(false, "-j", r.Target.Name)
		if !r.Target.Chain {
			for _, o := range keyOptions(r.Target.Name, r.Target.Options, ipv6, keyTargetDefaults) {
				add(o.Neg, "--"+o.Name, o.Values...)
			}
		}
	}
	return strings.Join(b, " ")
}

// keyQuote quotes a value that is empty or holds a space, a quote or a
// backslash, so a key splits back into its tokens unambiguously.
func keyQuote(v string) string {
	if v == "" || strings.ContainsAny(v, " \t\"'\\") {
		return strconv.Quote(v)
	}
	return v
}

// keyOptions returns an extension's options normalized and sorted by name:
// values spelled as iptables-save prints them, defaults iptables-save prints
// added, and defaults it omits dropped. A repeated option is kept, so two
// rules that differ only by it never share a key.
func keyOptions(ext string, opts []Option, ipv6 bool, defaults func(string, []Option, bool) []Option) []Option {
	out := make([]Option, 0, len(opts))
	for _, o := range opts {
		o.Values = slices.Clone(o.Values)
		for i, v := range o.Values {
			o.Values[i] = keyValue(ext, o.Name, v, ipv6)
		}
		out = append(out, o)
	}
	out = defaults(ext, out, ipv6)
	slices.SortStableFunc(out, func(a, b Option) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func hasOption(opts []Option, name string) bool {
	return slices.ContainsFunc(opts, func(o Option) bool { return o.Name == name })
}

// keyMatchDefaults applies the defaults of module ext to its options.
func keyMatchDefaults(ext string, opts []Option, ipv6 bool) []Option {
	switch ext {
	case "recent":
		if !hasOption(opts, "name") {
			opts = append(opts, Option{Name: "name", Values: []string{"DEFAULT"}})
		}
		if !hasOption(opts, "mask") {
			mask := "255.255.255.255"
			if ipv6 {
				mask = "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"
			}
			opts = append(opts, Option{Name: "mask", Values: []string{mask}})
		}
		if !hasOption(opts, "rsource") && !hasOption(opts, "rdest") {
			opts = append(opts, Option{Name: "rsource"})
		}
	case "limit":
		if !hasOption(opts, "limit") {
			opts = append(opts, Option{Name: "limit", Values: []string{"3/hour"}})
		}
		opts = slices.DeleteFunc(opts, func(o Option) bool {
			return o.Name == "limit-burst" && !o.Neg && slices.Equal(o.Values, []string{"5"})
		})
	}
	return opts
}

// keyTargetDefaults applies the defaults of target ext to its options.
func keyTargetDefaults(ext string, opts []Option, ipv6 bool) []Option {
	switch ext {
	case "REJECT":
		if !hasOption(opts, "reject-with") {
			with := "icmp-port-unreachable"
			if ipv6 {
				with = "icmp6-port-unreachable"
			}
			opts = append(opts, Option{Name: "reject-with", Values: []string{with}})
		}
	case "LOG":
		opts = slices.DeleteFunc(opts, func(o Option) bool { return o.Name == "log-level" && slices.Equal(o.Values, []string{"4"}) })
	}
	return opts
}

// keyValue spells one option value as iptables-save prints it.
func keyValue(ext, opt, v string, ipv6 bool) string {
	switch {
	case ext == "icmp" && opt == "icmp-type":
		return icmpTypeNumber(icmpTypes, v)
	case ext == "icmp6" && opt == "icmpv6-type":
		return icmpTypeNumber(icmp6Types, v)
	case ext == "limit" && opt == "limit":
		return limitRate(v)
	case (ext == "conntrack" && opt == "ctstate") || (ext == "state" && opt == "state"):
		return ctStates(v)
	case (ext == "mark" || ext == "connmark") && opt == "mark", ext == "MARK" && opt == "set-xmark":
		return markValue(v, ext == "MARK")
	case ext == "REJECT" && opt == "reject-with":
		aliases := rejectAliases
		if ipv6 {
			aliases = reject6Aliases
		}
		if c, ok := aliases[v]; ok {
			return c
		}
	case ext == "LOG" && opt == "log-level":
		if n, ok := logLevels[strings.ToLower(v)]; ok {
			return n
		}
	case ext == "recent" && opt == "mask":
		return keyAddr(v)
	}
	return v
}

// icmpType is an ICMP type name of iptables (libxt_icmp.c, libip6t_icmp6.c)
// with the type and, for a name of one code, the code.
type icmpType struct {
	typ  int
	code int // -1 for every code
}

var icmpTypes = map[string]icmpType{
	"any": {-1, -1}, "echo-reply": {0, -1}, "pong": {0, -1}, "destination-unreachable": {3, -1},
	"network-unreachable": {3, 0}, "host-unreachable": {3, 1}, "protocol-unreachable": {3, 2},
	"port-unreachable": {3, 3}, "fragmentation-needed": {3, 4}, "source-route-failed": {3, 5},
	"network-unknown": {3, 6}, "host-unknown": {3, 7}, "network-prohibited": {3, 9}, "host-prohibited": {3, 10},
	"tos-network-unreachable": {3, 11}, "tos-host-unreachable": {3, 12}, "communication-prohibited": {3, 13},
	"host-precedence-violation": {3, 14}, "precedence-cutoff": {3, 15}, "source-quench": {4, -1},
	"redirect": {5, -1}, "network-redirect": {5, 0}, "host-redirect": {5, 1}, "tos-network-redirect": {5, 2},
	"tos-host-redirect": {5, 3}, "echo-request": {8, -1}, "ping": {8, -1}, "router-advertisement": {9, -1},
	"router-solicitation": {10, -1}, "time-exceeded": {11, -1}, "ttl-exceeded": {11, -1},
	"ttl-zero-during-transit": {11, 0}, "ttl-zero-during-reassembly": {11, 1}, "parameter-problem": {12, -1},
	"ip-header-bad": {12, 0}, "required-option-missing": {12, 1}, "timestamp-request": {13, -1},
	"timestamp-reply": {14, -1}, "address-mask-request": {17, -1}, "address-mask-reply": {18, -1},
}

var icmp6Types = map[string]icmpType{
	"destination-unreachable": {1, -1}, "no-route": {1, 0}, "communication-prohibited": {1, 1},
	"beyond-scope": {1, 2}, "address-unreachable": {1, 3}, "port-unreachable": {1, 4},
	"failed-policy": {1, 5}, "reject-route": {1, 6}, "packet-too-big": {2, -1}, "time-exceeded": {3, -1},
	"ttl-exceeded": {3, -1}, "ttl-zero-during-transit": {3, 0}, "ttl-zero-during-reassembly": {3, 1},
	"parameter-problem": {4, -1}, "bad-header": {4, 0}, "unknown-header-type": {4, 1}, "unknown-option": {4, 2},
	"echo-request": {128, -1}, "ping": {128, -1}, "echo-reply": {129, -1}, "pong": {129, -1},
	"router-solicitation": {133, -1}, "router-advertisement": {134, -1}, "neighbour-solicitation": {135, -1},
	"neighbor-solicitation": {135, -1}, "neighbour-advertisement": {136, -1}, "neighbor-advertisement": {136, -1},
	"redirect": {137, -1},
}

// icmpTypeNumber is an ICMP type as iptables-save prints it: "any", the type
// number, or type/code for a single code. An unknown spelling is kept.
func icmpTypeNumber(names map[string]icmpType, v string) string {
	t, ok := names[strings.ToLower(v)]
	switch {
	case !ok:
		return v
	case t.typ < 0:
		return "any"
	case t.code < 0:
		return strconv.Itoa(t.typ)
	}
	return fmt.Sprintf("%d/%d", t.typ, t.code)
}

// limitUnits are the units of a limit rate, in the order iptables tries
// them, with the spelling iptables-save prints.
var limitUnits = []struct{ word, short string }{{"second", "sec"}, {"minute", "min"}, {"hour", "hour"}, {"day", "day"}}

// limitRate is a rate such as 3/minute as iptables-save prints it, 3/min.
// Like iptables (libxt_limit.c, parse_rate), it takes any prefix of a unit
// word and a rate without a unit as per second.
func limitRate(v string) string {
	n, unit, ok := strings.Cut(v, "/")
	if !ok {
		return n + "/sec"
	}
	for _, u := range limitUnits {
		if unit != "" && len(unit) <= len(u.word) && strings.EqualFold(unit, u.word[:len(unit)]) {
			return n + "/" + u.short
		}
	}
	return v
}

// ctStateOrder is the order iptables-save prints conntrack states in.
var ctStateOrder = []string{"INVALID", "NEW", "RELATED", "ESTABLISHED", "UNTRACKED", "SNAT", "DNAT"}

// ctStates sorts a comma list of states into ctStateOrder; an unknown state
// sorts last, in its own order.
func ctStates(v string) string {
	states := strings.Split(strings.ToUpper(v), ",")
	slices.SortStableFunc(states, func(a, b string) int {
		ia, ib := slices.Index(ctStateOrder, a), slices.Index(ctStateOrder, b)
		if ia < 0 {
			ia = len(ctStateOrder)
		}
		if ib < 0 {
			ib = len(ctStateOrder)
		}
		return ia - ib
	})
	return strings.Join(states, ",")
}

// markValue is a mark value[/mask] in hexadecimal as iptables-save prints
// it: the mask is omitted when it is all ones, except for MARK
// --set-xmark, which always prints it. A value that is not a number is
// kept.
func markValue(v string, alwaysMask bool) string {
	val, mask, hasMask := strings.Cut(v, "/")
	n, err := strconv.ParseUint(val, 0, 32)
	if err != nil {
		return v
	}
	m := uint64(0xffffffff)
	if hasMask {
		if m, err = strconv.ParseUint(mask, 0, 32); err != nil {
			return v
		}
	}
	if m == 0xffffffff && !alwaysMask {
		return fmt.Sprintf("0x%x", n)
	}
	return fmt.Sprintf("0x%x/0x%x", n, m)
}

// keyAddr is an address in its canonical spelling; a value that is not an
// address is kept.
func keyAddr(v string) string {
	if pfx, ok := parseAddr(v, true); ok {
		return pfx.Addr().String()
	}
	return v
}

// rejectAliases map the --reject-with spellings iptables accepts to the one
// iptables-save prints (libipt_REJECT.c); reject6Aliases do the same for
// ip6tables (libip6t_REJECT.c).
var (
	rejectAliases = map[string]string{
		"net-unreach": "icmp-net-unreachable", "host-unreach": "icmp-host-unreachable",
		"proto-unreach": "icmp-proto-unreachable", "port-unreach": "icmp-port-unreachable",
		"net-prohib": "icmp-net-prohibited", "host-prohib": "icmp-host-prohibited",
		"admin-prohib": "icmp-admin-prohibited", "tcp-rst": "tcp-reset",
	}
	reject6Aliases = map[string]string{
		"no-route": "icmp6-no-route", "adm-prohibited": "icmp6-adm-prohibited",
		"addr-unreach": "icmp6-addr-unreachable", "port-unreach": "icmp6-port-unreachable",
		"tcp-rst": "tcp-reset", "policy-fail": "icmp6-policy-fail", "reject-route": "icmp6-reject-route",
	}
)

// logLevels map syslog level names to the numbers iptables-save prints.
var logLevels = map[string]string{
	"emerg": "0", "alert": "1", "crit": "2", "err": "3", "error": "3", "warning": "4", "warn": "4",
	"notice": "5", "info": "6", "debug": "7",
}
