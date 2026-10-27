package firewall

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// planRuleID is the id a new rule carries in a plan, which has no change
// yet; Apply gives the rule its change's id (a ULID).
const planRuleID = "00000000000000000000000000"

// logical is the logical state a diff compares: UFW's configured state and
// the supported sysctl values.
type logical struct {
	cfg    fwstate.Config
	sysctl map[string]string
}

// stateDiff lists every change from a to b (R39), each as a "-" line for
// the old value and a "+" line for the new one: UFW's state; every rule
// removed (in a's order) and added (in b's order), a manual rule marked,
// and a reordering of the rules both hold; each default policy; the
// logging level; IPV6; and each sysctl value. Equal states give no line.
func stateDiff(a, b logical) []core.DiffLine {
	var out []core.DiffLine
	pair := func(old, now string) {
		if old != now {
			out = append(out, core.DiffLine{Op: "-", Text: old}, core.DiffLine{Op: "+", Text: now})
		}
	}
	pair(enabledText(a.cfg.Enabled), enabledText(b.cfg.Enabled))

	ra, rb := model.Merge(a.cfg.Rules4, a.cfg.Rules6), model.Merge(b.cfg.Rules4, b.cfg.Rules6)
	keptA, removed := unmatched(ra, rb)
	keptB, added := unmatched(rb, ra)
	for _, r := range removed {
		out = append(out, core.DiffLine{Op: "-", Text: ruleText(r)})
	}
	for _, r := range added {
		out = append(out, core.DiffLine{Op: "+", Text: ruleText(r)})
	}
	if !slices.Equal(keptA, keptB) {
		out = append(out, core.DiffLine{Op: " ", Text: "rules reordered: " + strings.Join(keptB, ", ")})
	}

	pair("default incoming "+a.cfg.DefaultInput, "default incoming "+b.cfg.DefaultInput)
	pair("default outgoing "+a.cfg.DefaultOutput, "default outgoing "+b.cfg.DefaultOutput)
	pair("default routed "+a.cfg.DefaultForward, "default routed "+b.cfg.DefaultForward)
	pair("logging "+a.cfg.LogLevel, "logging "+b.cfg.LogLevel)
	pair("IPV6 "+yesNo(a.cfg.IPv6), "IPV6 "+yesNo(b.cfg.IPv6))

	keys := slices.Collect(maps.Keys(a.sysctl))
	for k := range b.sysctl {
		if _, ok := a.sysctl[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		va, inA := a.sysctl[k]
		vb, inB := b.sysctl[k]
		if inA && inB && va == vb {
			continue
		}
		if inA {
			out = append(out, core.DiffLine{Op: "-", Text: "sysctl " + k + "=" + va})
		}
		if inB {
			out = append(out, core.DiffLine{Op: "+", Text: "sysctl " + k + "=" + vb})
		}
	}
	return out
}

// manualChanges returns the ids of the manual rules a change from a to b
// removes or adds (spec 2.2: they need manual: true), in stateDiff's order.
// An id appears once for each copy of the rule removed or added.
func manualChanges(a, b logical) []string {
	ra, rb := model.Merge(a.cfg.Rules4, a.cfg.Rules6), model.Merge(b.cfg.Rules4, b.cfg.Rules6)
	_, removed := unmatched(ra, rb)
	_, added := unmatched(rb, ra)
	var out []string
	for _, r := range slices.Concat(removed, added) {
		if r.Origin == model.OriginManual {
			out = append(out, r.ID)
		}
	}
	return out
}

// unmatched splits rules into the ids of those other also holds and the
// rules it does not. Rules match by their whole text one copy to one copy:
// identical manual rules share an id, so only a count shows that a change
// adds or removes a copy, which changes which copy filters first.
func unmatched(rules, other []model.Rule) (kept []string, rest []model.Rule) {
	left := make(map[string]int, len(other))
	for _, r := range other {
		left[ruleText(r)]++
	}
	for _, r := range rules {
		if t := ruleText(r); left[t] > 0 {
			left[t]--
			kept = append(kept, r.ID)
		} else {
			rest = append(rest, r)
		}
	}
	return kept, rest
}

// ruleTexts is the set of the rules' texts; it does not count copies.
func ruleTexts(rules []model.Rule) map[string]bool {
	out := make(map[string]bool, len(rules))
	for _, r := range rules {
		out[ruleText(r)] = true
	}
	return out
}

// ruleText describes a rule for a diff: its id, a manual rule marked, and
// the ufw rule without the comment argument followed by its label or
// comment; a manual rule the model cannot express shows its raw tuple.
func ruleText(r model.Rule) string {
	id := r.ID
	if id == planRuleID {
		id = "new"
	}
	if r.Raw != "" {
		return "rule " + id + " (manual, read-only): " + r.Raw
	}
	args := model.AddArgs(r)
	if n := len(args); n >= 2 && args[n-2] == "comment" {
		args = args[:n-2]
	}
	var b strings.Builder
	b.WriteString("rule " + id)
	if r.Origin == model.OriginManual {
		b.WriteString(" (manual)")
	}
	b.WriteString(": " + strings.Join(args, " "))
	if r.Comment != "" {
		b.WriteString(" comment " + strconv.Quote(r.Comment))
	}
	return b.String()
}

func enabledText(enabled bool) string {
	if enabled {
		return "UFW enabled"
	}
	return "UFW disabled"
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
