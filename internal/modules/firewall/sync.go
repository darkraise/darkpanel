package firewall

import (
	"fmt"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// ChainDifference is one chain whose loaded rules differ from what UFW's
// configuration predicts (spec 2.3, in sync): the JSON form of a
// fwstate.ChainDiff, with the prediction on one side and the loaded state on
// the other. Rules are netfilter keys.
type ChainDifference struct {
	Family          string   `json:"family"` // v4 or v6
	Table           string   `json:"table"`
	Chain           string   `json:"chain"`
	PolicyPredicted string   `json:"policyPredicted,omitempty"` // set only when the built-in policies differ
	PolicyLoaded    string   `json:"policyLoaded,omitempty"`
	OnlyPredicted   []string `json:"onlyPredicted,omitempty"`
	OnlyLoaded      []string `json:"onlyLoaded,omitempty"`
	OrderDiffers    bool     `json:"orderDiffers,omitempty"`
}

// chainDifferences is the JSON form of diffs; never nil.
func chainDifferences(diffs []fwstate.ChainDiff) []ChainDifference {
	out := make([]ChainDifference, 0, len(diffs))
	for _, d := range diffs {
		out = append(out, ChainDifference{
			Family: string(d.Family), Table: d.Table, Chain: d.Chain, PolicyPredicted: d.PolicyA, PolicyLoaded: d.PolicyB,
			OnlyPredicted: d.OnlyA, OnlyLoaded: d.OnlyB, OrderDiffers: d.OrderDiffers,
		})
	}
	return out
}

// chainDiffText describes one difference on one line, for plans and
// errors.
func chainDiffText(d fwstate.ChainDiff) string {
	var parts []string
	if d.PolicyA != "" || d.PolicyB != "" {
		parts = append(parts, fmt.Sprintf("policy %s predicted, %s loaded", d.PolicyA, d.PolicyB))
	}
	switch {
	case len(d.OnlyA) > 0 && len(d.OnlyB) > 0:
		parts = append(parts, fmt.Sprintf("%s only predicted, %d only loaded", plural(len(d.OnlyA), "rule"), len(d.OnlyB)))
	case len(d.OnlyA) > 0:
		parts = append(parts, plural(len(d.OnlyA), "rule")+" only predicted")
	case len(d.OnlyB) > 0:
		parts = append(parts, plural(len(d.OnlyB), "rule")+" only loaded")
	}
	if d.OrderDiffers {
		parts = append(parts, "order differs")
	}
	if len(parts) == 0 {
		parts = append(parts, "present on one side only")
	}
	return fmt.Sprintf("%s %s/%s: %s", d.Family, d.Table, d.Chain, strings.Join(parts, ", "))
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// ufwChain selects UFW's own chains.
func ufwChain(_ fwstate.Family, _, chain string) bool {
	return strings.HasPrefix(chain, "ufw-") || strings.HasPrefix(chain, "ufw6-")
}

// syncDiffs compares the loaded state cur with what the configuration cfg
// predicts it to be (spec 2.3, in sync). An enabled UFW is compared with
// the result of a reload, over UFW's and Darkpanel's chains and the filter
// table's built-in chains, where UFW hooks in (rules a before.rules block
// adds to another table's built-in chains repeat on every start and are
// not compared). A disabled UFW is compared with the result of a stop, over
// UFW's chains only, so a built-in policy set while UFW is off does not
// count. A difference marks the host out of sync; after an apply, it fails
// verify.
func (f fw) syncDiffs(cfg fwstate.Config, cur fwstate.Loaded) ([]fwstate.ChainDiff, error) {
	tr, keep := fwstate.TransReload, restoreKeep
	if !cfg.Enabled {
		tr, keep = fwstate.TransDisable, ufwChain
	}
	pred, err := f.d.predict(cfg, cur, tr)
	if err != nil {
		return nil, fmt.Errorf("firewall: predict the loaded state of the configuration: %w", err)
	}
	return f.d.compare(pred, cur, keep), nil
}

// builtinNames are the names of the built-in chains of the five tables.
var builtinNames = []string{"PREROUTING", "INPUT", "FORWARD", "OUTPUT", "POSTROUTING"}

// verifyKeep selects the chains verify compares after a start: UFW's and
// Darkpanel's chains and the built-in chains of every table, which
// before.rules and after.rules may write to. A foreign user chain is left
// out: no start writes it, so the prediction copies it from the loaded
// state.
func verifyKeep(_ fwstate.Family, _, chain string) bool {
	return firewallChain(chain) || slices.Contains(builtinNames, chain)
}

// verifyDiffs is syncDiffs for the state after a change (spec 2.3, verify).
// An enabled UFW is compared over verifyKeep. A rule a before.rules or
// after.rules block appends to a built-in chain of a table other than
// filter is appended again by every start, which the prediction models, so
// the prediction from the state after the start holds it once more than
// that state does: such a difference is reappended and does not count while
// the loaded chain holds every rule the start appends. A disabled UFW is
// compared as in syncDiffs.
func (f fw) verifyDiffs(cfg fwstate.Config, cur fwstate.Loaded) ([]fwstate.ChainDiff, error) {
	if !cfg.Enabled {
		return f.syncDiffs(cfg, cur)
	}
	pred, err := f.d.predict(cfg, cur, fwstate.TransReload)
	if err != nil {
		return nil, fmt.Errorf("firewall: predict the loaded state of the configuration: %w", err)
	}
	var out []fwstate.ChainDiff
	for _, d := range f.d.compare(pred, cur, verifyKeep) {
		if !reappended(d, cur) {
			out = append(out, d)
		}
	}
	return out, nil
}

// reappended reports whether d is only the rules a start appends to a
// built-in chain of a table other than filter, all of which the loaded
// chain already holds: the same policy, no rule only loaded, the same order.
func reappended(d fwstate.ChainDiff, cur fwstate.Loaded) bool {
	if d.Table == "filter" || !slices.Contains(builtinNames, d.Chain) ||
		d.PolicyA != "" || d.PolicyB != "" || len(d.OnlyB) > 0 || d.OrderDiffers {
		return false
	}
	have := map[string]int{}
	if c := chainOf(familyRules(cur, d.Family), d.Table, d.Chain); c != nil {
		for _, r := range c.Rules {
			have[netfilter.Key(r, d.Family == fwstate.V6)]++
		}
	}
	for _, k := range d.OnlyA {
		if have[k]--; have[k] < 0 {
			return false
		}
	}
	return true
}

// confined reports whether every difference between the prediction pred
// and the loaded state cur lies in UFW's and Darkpanel's own chains, which
// a repair may bring back (spec 2.3): a UFW or Darkpanel chain, or a
// built-in chain of the filter table whose rules other than bare jumps to such
// chains are the same, in order, on both sides (its policy and those jumps
// are UFW's). Any other difference is a foreign chain's.
func confined(diffs []fwstate.ChainDiff, pred, cur fwstate.Loaded) bool {
	for _, d := range diffs {
		switch {
		case firewallChain(d.Chain):
		case d.Table == "filter" && slices.Contains(builtinChains, d.Chain):
			a, b := familyRules(pred, d.Family), familyRules(cur, d.Family)
			if !slices.Equal(foreignKeys(a, d.Chain, d.Family), foreignKeys(b, d.Chain, d.Family)) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func familyRules(st fwstate.Loaded, fam fwstate.Family) *netfilter.Ruleset {
	if fam == fwstate.V6 {
		return st.V6
	}
	return st.V4
}

// foreignKeys are the keys of a filter chain's rules that are not bare jumps
// to a UFW or Darkpanel chain.
func foreignKeys(rs *netfilter.Ruleset, chain string, fam fwstate.Family) []string {
	var out []string
	if c := chainOf(rs, "filter", chain); c != nil {
		for _, r := range c.Rules {
			k := netfilter.Key(r, fam == fwstate.V6)
			if !firewallChain(r.Target.Name) || k != "-j "+r.Target.Name {
				out = append(out, k)
			}
		}
	}
	return out
}
