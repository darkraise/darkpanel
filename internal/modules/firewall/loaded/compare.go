package loaded

import (
	"slices"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// policyAccept is the policy of a built-in chain whose table is not loaded:
// a table that is not there hooks nothing, so every packet goes on.
const policyAccept = "ACCEPT"

// Compare returns the chains whose rules or policy differ between a and b,
// for every family, table and chain keep selects (nil keep selects all),
// ordered by family (v4, v6), table (netfilter.Tables order, then by name)
// and chain name. Rules are compared by netfilter.Key in the chain's
// family, so formatting differences never count. A built-in chain missing
// on one side compares as an empty chain with policy ACCEPT, which is what
// an absent table does; a user chain declared on one side only always
// differs, even when it is empty, because jumps can reach it.
func Compare(a, b fwstate.Loaded, keep func(f fwstate.Family, table, chain string) bool) []fwstate.ChainDiff {
	var out []fwstate.ChainDiff
	out = append(out, compareFamily(fwstate.V4, a.V4, b.V4, keep)...)
	return append(out, compareFamily(fwstate.V6, a.V6, b.V6, keep)...)
}

func compareFamily(f fwstate.Family, a, b *netfilter.Ruleset, keep func(fwstate.Family, string, string) bool) []fwstate.ChainDiff {
	var out []fwstate.ChainDiff
	for _, table := range compareTables(a, b) {
		ta, tb := compareTable(a, table), compareTable(b, table)
		for _, chain := range compareChains(ta, tb) {
			if keep != nil && !keep(f, table, chain) {
				continue
			}
			if d, ok := compareChain(f, table, chain, compareChainOf(ta, chain), compareChainOf(tb, chain)); ok {
				out = append(out, d)
			}
		}
	}
	return out
}

// compareChain compares one chain; ca or cb is nil when the chain is
// missing on that side.
func compareChain(f fwstate.Family, table, chain string, ca, cb *netfilter.Chain) (fwstate.ChainDiff, bool) {
	d := fwstate.ChainDiff{Family: f, Table: table, Chain: chain}
	builtin := ca != nil && ca.Builtin || cb != nil && cb.Builtin
	if pa, pb := comparePolicy(ca, builtin), comparePolicy(cb, builtin); pa != pb {
		d.PolicyA, d.PolicyB = pa, pb
	}
	ka, kb := compareKeys(f, ca), compareKeys(f, cb)
	d.OnlyA, d.OnlyB = compareOnly(ka, kb), compareOnly(kb, ka)
	d.OrderDiffers = !slices.Equal(compareCommon(ka, kb), compareCommon(kb, ka))
	oneSided := !builtin && (ca == nil) != (cb == nil)
	differs := d.PolicyA != "" || d.PolicyB != "" || len(d.OnlyA) > 0 || len(d.OnlyB) > 0 || d.OrderDiffers
	return d, differs || oneSided
}

// comparePolicy is a chain's policy, ACCEPT for a missing built-in chain
// and "" for a user chain.
func comparePolicy(c *netfilter.Chain, builtin bool) string {
	switch {
	case !builtin:
		return ""
	case c == nil:
		return policyAccept
	}
	return c.Policy
}

func compareKeys(f fwstate.Family, c *netfilter.Chain) []string {
	if c == nil {
		return nil
	}
	keys := make([]string, len(c.Rules))
	for i, r := range c.Rules {
		keys[i] = netfilter.Key(r, f == fwstate.V6)
	}
	return keys
}

// compareOnly returns the keys of x that y does not hold, counting
// duplicates, in x's order.
func compareOnly(x, y []string) []string {
	left := compareCount(y)
	var out []string
	for _, k := range x {
		if left[k] > 0 {
			left[k]--
			continue
		}
		out = append(out, k)
	}
	return out
}

// compareCommon returns the keys of x that y also holds, counting
// duplicates, in x's order.
func compareCommon(x, y []string) []string {
	left := compareCount(y)
	var out []string
	for _, k := range x {
		if left[k] > 0 {
			left[k]--
			out = append(out, k)
		}
	}
	return out
}

func compareCount(keys []string) map[string]int {
	n := make(map[string]int, len(keys))
	for _, k := range keys {
		n[k]++
	}
	return n
}

// compareTables lists the tables of a and b: the known tables in
// netfilter.Tables order, then any other name sorted.
func compareTables(a, b *netfilter.Ruleset) []string {
	var extra []string
	seen := map[string]bool{}
	for _, rs := range []*netfilter.Ruleset{a, b} {
		if rs == nil {
			continue
		}
		for _, t := range rs.Tables {
			if !seen[t.Name] && !slices.Contains(netfilter.Tables(), t.Name) {
				extra = append(extra, t.Name)
			}
			seen[t.Name] = true
		}
	}
	var out []string
	for _, name := range netfilter.Tables() {
		if seen[name] {
			out = append(out, name)
		}
	}
	slices.Sort(extra)
	return append(out, extra...)
}

func compareTable(rs *netfilter.Ruleset, name string) *netfilter.Table {
	if rs == nil {
		return nil
	}
	return rs.Table(name)
}

// compareChains lists the chain names of a and b, sorted.
func compareChains(a, b *netfilter.Table) []string {
	var out []string
	for _, t := range []*netfilter.Table{a, b} {
		if t == nil {
			continue
		}
		for _, c := range t.Chains {
			if !slices.Contains(out, c.Name) {
				out = append(out, c.Name)
			}
		}
	}
	slices.Sort(out)
	return out
}

func compareChainOf(t *netfilter.Table, name string) *netfilter.Chain {
	if t == nil {
		return nil
	}
	return t.Chain(name)
}
