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

// confined reports whether every difference between the prediction pred
// and the loaded state cur lies in UFW's and Darkpanel's own chains, which
// a repair may bring back (spec 2.3): a UFW or Darkpanel chain, or a
// built-in chain of the filter table whose rules other than jumps to such
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

// foreignKeys are the keys of a filter chain's rules that are not jumps to
// a UFW or Darkpanel chain.
func foreignKeys(rs *netfilter.Ruleset, chain string, fam fwstate.Family) []string {
	var out []string
	if c := chainOf(rs, "filter", chain); c != nil {
		for _, r := range c.Rules {
			if !firewallChain(r.Target.Name) {
				out = append(out, netfilter.Key(r, fam == fwstate.V6))
			}
		}
	}
	return out
}
