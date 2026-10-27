package predict

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// The chain lists of ufw-init-functions, without the "ufw" or "ufw6"
// prefix: chains_command flushes the first, delete_chains deletes the
// second (the secondary chains).
var (
	flushedChains = []string{
		"logging-deny", "logging-allow", "not-local", "user-logging-input", "user-limit-accept", "user-limit",
		"skip-to-policy-input", "reject-input", "after-logging-input", "after-input", "user-input", "before-input",
		"before-logging-input", "skip-to-policy-forward", "reject-forward", "after-logging-forward", "after-forward",
		"user-logging-forward", "user-forward", "before-forward", "before-logging-forward", "track-forward",
		"track-output", "track-input", "skip-to-policy-output", "reject-output", "after-logging-output",
		"after-output", "user-logging-output", "user-output", "before-output", "before-logging-output",
	}
	secondaryChains = []string{
		"logging-deny", "logging-allow", "not-local", "user-logging-input", "user-logging-output",
		"user-logging-forward", "user-limit-accept", "user-limit", "user-input", "user-forward", "user-output",
		"skip-to-policy-input", "skip-to-policy-output", "skip-to-policy-forward",
	}
	filterBuiltins = []string{"INPUT", "FORWARD", "OUTPUT"}
)

// Predict returns the loaded state UFW leaves after transition tr,
// starting from the loaded state cur (spec 2.3). cfg is the configured
// state UFW starts from: the settings once the action's rule, default and
// logging commands have run, with Files holding the bytes on disk when
// ufw-init runs, which is Edit's result for the commands before the
// transition's own "ufw --force enable" or "ufw reload" (that command
// rewrites user.rules and user6.rules only after UFW has loaded them; it
// matters on a first enable, which loads the package's templates). Every
// chain UFW does not manage keeps its rules and position:
//
//   - TransEnable starts UFW as start_firewall does: its chains are rebuilt
//     from the translation of cfg and the logging chains then refreshed
//     live for cfg's log level and policies, the jumps from INPUT, OUTPUT and FORWARD to
//     its primary chains are appended only when those chains do not exist
//     yet (otherwise the existing jumps keep their positions), a table
//     before.rules or after.rules writes to receives their rules as
//     iptables-restore --noflush loads them (a built-in chain's rules are
//     appended again on every start), and the built-in policies come from
//     cfg. With IPv6 off the IPv6 filter table becomes exactly UFW's plain
//     table: policies DROP and only loopback accepted. Starting UFW while
//     its user chains are loaded is an error: ufw_start would skip.
//   - TransReload is a stop followed by a start, and nothing when cfg is
//     not enabled, since ufw reload then skips.
//   - TransDisable stops UFW as ufw_stop does: its chains are flushed, its
//     secondary chains deleted, its empty primary chains and their jumps
//     kept, and the built-in filter policies set to ACCEPT, in each family
//     ip6tables serves.
//   - TransNone returns cur.
//
// Sysctl and Unsupported are copied from cur. An unsupported
// configuration, a missing IPv4 ruleset and an unknown transition are
// errors. cur is not modified.
func Predict(cfg fwstate.Config, cur fwstate.Loaded, tr fwstate.Transition) (fwstate.Loaded, error) {
	if cur.V4 == nil {
		return fwstate.Loaded{}, fmt.Errorf("predict: the loaded state has no IPv4 ruleset")
	}
	out := fwstate.Loaded{
		Variant: cur.Variant, V4: cloneRuleset(cur.V4), V6: cloneRuleset(cur.V6),
		Sysctl: maps.Clone(cur.Sysctl), Unsupported: slices.Clone(cur.Unsupported),
	}
	start := false
	switch tr {
	case fwstate.TransNone:
		return out, nil
	case fwstate.TransDisable:
		stop(out)
		return out, nil
	case fwstate.TransReload:
		if !cfg.Enabled {
			return out, nil
		}
		stop(out)
		start = true
	case fwstate.TransEnable:
		if f := out.V4.Table("filter"); f != nil && f.Chain("ufw-user-input") != nil {
			return fwstate.Loaded{}, fmt.Errorf("predict: UFW is already running, so a start would change nothing; reload it instead")
		}
		start = true
	default:
		return fwstate.Loaded{}, fmt.Errorf("predict: unknown transition %q", tr)
	}
	if start {
		if len(cfg.Unsupported) > 0 {
			return fwstate.Loaded{}, fmt.Errorf("predict: the configuration is unsupported: %s", strings.Join(cfg.Unsupported, "; "))
		}
		if err := startFamily(cfg, out.V4, false); err != nil {
			return fwstate.Loaded{}, err
		}
		if out.V6 != nil {
			if err := startFamily(cfg, out.V6, true); err != nil {
				return fwstate.Loaded{}, err
			}
		}
	}
	return out, nil
}

// stop is ufw_stop for both families.
func stop(st fwstate.Loaded) {
	stopFamily(st.V4, chainPrefix(false))
	if st.V6 != nil {
		stopFamily(st.V6, chainPrefix(true))
	}
}

// stopFamily is ufw_stop for one family: delete_chains, then INPUT,
// OUTPUT and FORWARD set to ACCEPT.
func stopFamily(rs *netfilter.Ruleset, u string) {
	f := filterTable(rs)
	deleteChains(f, u)
	for _, name := range filterBuiltins {
		builtinChain(f, name).Policy = "ACCEPT"
	}
}

// deleteChains is delete_chains: the chains of chains_command flushed and
// the secondary chains deleted unless a rule still jumps to them (iptables
// -X then fails, which UFW ignores).
func deleteChains(f *netfilter.Table, u string) {
	for _, name := range flushedChains {
		if c := f.Chain(u + "-" + name); c != nil {
			c.Rules = nil
		}
	}
	for _, name := range secondaryChains {
		name = u + "-" + name
		if f.Chain(name) != nil && !referenced(f, name) {
			f.Chains = slices.DeleteFunc(f.Chains, func(c netfilter.Chain) bool { return c.Name == name })
		}
	}
}

// startFamily is start_firewall for one family: ufw_start, then the live
// part of update_logging.
func startFamily(cfg fwstate.Config, rs *netfilter.Ruleset, v6 bool) error {
	u := chainPrefix(v6)
	f := filterTable(rs)
	if v6 && !cfg.IPv6 {
		*f = netfilter.Table{Name: "filter", Chains: plainV6Filter()}
		return nil
	}
	tr, err := translate(cfg, v6)
	if err != nil {
		return err
	}
	primary := f.Chain(u+"-before-logging-input") != nil
	if primary {
		deleteChains(f, u)
	}
	for _, tt := range tr.Tables {
		dst := rs.Table(tt.Name)
		if dst == nil {
			rs.Tables = append(rs.Tables, netfilter.Table{Name: tt.Name})
			dst = &rs.Tables[len(rs.Tables)-1]
		}
		for _, c := range tt.Chains {
			if tt.Name == "filter" && c.Builtin && primary {
				builtinChain(dst, c.Name).Policy = c.Policy
				continue
			}
			loadChain(dst, c)
		}
	}
	return startLogging(cfg, rs.Table("filter"), v6)
}

// startLogging is the live part of update_logging, which start_firewall
// runs after ufw_start has loaded the files: the before, after and misc
// logging chains are flushed and refilled with the rules of the log level,
// the RETURN rule at the top of each user logging chain is removed (or,
// with logging off, put back), and the rate-limit LOG rule is moved to the
// top of the limit chain (or removed with logging off). It matters when
// the files UFW loaded differ from what it then writes, as the package's
// template user6.rules does.
func startLogging(cfg fwstate.Config, f *netfilter.Table, v6 bool) error {
	u := chainPrefix(v6)
	chain := func(name string) (*netfilter.Chain, error) {
		if c := f.Chain(name); c != nil {
			return c, nil
		}
		return nil, fmt.Errorf("predict: UFW's chain %s is missing after its start", name)
	}
	for _, name := range []string{"before-logging-input", "before-logging-output", "before-logging-forward",
		"after-logging-input", "after-logging-output", "after-logging-forward", "logging-deny", "logging-allow"} {
		c, err := chain(u + "-" + name)
		if err != nil {
			return err
		}
		c.Rules = nil
	}
	off := cfg.LogLevel == "off"
	for _, args := range loggingRules(cfg, u) {
		c, err := chain(args[1])
		if err != nil {
			return err
		}
		r, err := logRule(args[1:])
		if err != nil {
			return err
		}
		switch {
		case args[0] == "-A":
			c.Rules = append(c.Rules, r)
		case args[0] == "-I" && off:
			deleteRule(c, r, v6)
			c.Rules = slices.Insert(c.Rules, 0, r)
		case args[0] == "-I":
			c.Rules = slices.Insert(c.Rules, 0, r)
		default:
			deleteRule(c, r, v6)
		}
	}
	limit, err := chain(u + "-user-limit")
	if err != nil {
		return err
	}
	r, err := logRule([]string{u + "-user-limit", "-m", "limit", "--limit", "3/minute", "-j", "LOG", "--log-prefix", "[UFW LIMIT BLOCK] "})
	if err != nil {
		return err
	}
	deleteRule(limit, r, v6)
	if !off {
		limit.Rules = slices.Insert(limit.Rules, 0, r)
	}
	return nil
}

// logRule parses one rule UFW passes to iptables as an argument list: the
// chain, then the options, a log prefix being one argument.
func logRule(args []string) (netfilter.Rule, error) {
	line := strings.Join(append([]string{"-A"}, args...), " ")
	line = strings.ReplaceAll(strings.ReplaceAll(line, "[", `"[`), "] ", `] "`)
	rs, err := netfilter.ParseRestore(strings.NewReader("*filter\n:" + args[0] + " - [0:0]\n" + line + "\nCOMMIT\n"))
	if err != nil {
		return netfilter.Rule{}, fmt.Errorf("predict: UFW's logging rule %q does not parse: %w", line, err)
	}
	return rs.Tables[0].Chains[0].Rules[0], nil
}

// deleteRule is iptables -D with a rule specification: the first rule
// equal to r goes, and nothing happens when none is.
func deleteRule(c *netfilter.Chain, r netfilter.Rule, v6 bool) {
	key := netfilter.Key(r, v6)
	if i := slices.IndexFunc(c.Rules, func(x netfilter.Rule) bool { return netfilter.Key(x, v6) == key }); i >= 0 {
		c.Rules = slices.Delete(c.Rules, i, i+1)
	}
}

// loadChain loads one chain of a restore input into a table as
// iptables-restore --noflush does for the input UFW loads: a user chain
// takes the input's rules, a built-in chain gets them appended and takes
// the input's policy when the input declares one.
func loadChain(t *netfilter.Table, c netfilter.Chain) {
	dst := t.Chain(c.Name)
	if dst == nil {
		t.Chains = append(t.Chains, netfilter.Chain{Name: c.Name, Builtin: c.Builtin})
		dst = &t.Chains[len(t.Chains)-1]
	}
	if !c.Builtin {
		dst.Rules = slices.Clone(c.Rules)
		return
	}
	dst.Rules = append(dst.Rules, c.Rules...)
	if c.Policy != "" {
		dst.Policy = c.Policy
	}
}

// plainV6Filter is the IPv6 filter table ufw_start loads, without
// --noflush, when IPv6 is off.
func plainV6Filter() []netfilter.Chain {
	rs, err := netfilter.ParseRestore(strings.NewReader("*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT DROP [0:0]\n" +
		"-A INPUT -i lo -j ACCEPT\n-A OUTPUT -o lo -j ACCEPT\nCOMMIT\n"))
	if err != nil {
		panic("predict: the plain IPv6 table does not parse: " + err.Error())
	}
	return rs.Tables[0].Chains
}

// filterTable returns the ruleset's filter table, adding it when absent.
func filterTable(rs *netfilter.Ruleset) *netfilter.Table {
	if t := rs.Table("filter"); t != nil {
		return t
	}
	rs.Tables = append(rs.Tables, netfilter.Table{Name: "filter"})
	return &rs.Tables[len(rs.Tables)-1]
}

// builtinChain returns a table's built-in chain, adding it with policy
// ACCEPT when absent.
func builtinChain(t *netfilter.Table, name string) *netfilter.Chain {
	if c := t.Chain(name); c != nil {
		return c
	}
	t.Chains = append(t.Chains, netfilter.Chain{Name: name, Builtin: true, Policy: "ACCEPT"})
	return &t.Chains[len(t.Chains)-1]
}

// referenced reports whether any rule of the table jumps to chain.
func referenced(t *netfilter.Table, chain string) bool {
	for _, c := range t.Chains {
		for _, r := range c.Rules {
			if r.Target.Name == chain {
				return true
			}
		}
	}
	return false
}

// cloneRuleset copies a ruleset deeply enough that editing its tables,
// chains and rule lists leaves the original alone.
func cloneRuleset(rs *netfilter.Ruleset) *netfilter.Ruleset {
	if rs == nil {
		return nil
	}
	out := &netfilter.Ruleset{LegacyPresent: rs.LegacyPresent, Tables: slices.Clone(rs.Tables)}
	for i := range out.Tables {
		out.Tables[i].Chains = slices.Clone(out.Tables[i].Chains)
		for j := range out.Tables[i].Chains {
			out.Tables[i].Chains[j].Rules = slices.Clone(out.Tables[i].Chains[j].Rules)
		}
	}
	return out
}
