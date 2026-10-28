package dockerfw

import (
	"fmt"
	"slices"

	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// RuleOp is one step that brings UFW's container rules to the desired ones:
// the deletion of a rule, or the addition of one.
type RuleOp struct {
	Delete string      // a rule id to delete; empty for an add
	Add    *model.Rule // a rule to add, with its new ID
	Insert int         // 1-based position for Add in UFW's numbering; 0 appends
}

// roEntry is a rule in the numbering list of one family: des is the index of
// the desired rule it keeps, or -1 for a rule that is not a desired one.
type roEntry struct{ des int }

// RuleOps returns the operations that bring the container rules in current,
// the configured rules of both families, to desired. A desired rule equal to
// a current container rule in every field but its ID is kept; every other
// current container rule is deleted and every missing desired rule is added
// at the position that makes UFW's order match desired's. Other rules are
// never deleted or moved. It is pure; newID mints the ID of each added rule.
func RuleOps(current []model.Rule, desired []model.Rule, newID func() string) ([]RuleOp, error) {
	des, err := roDesired(current, desired)
	if err != nil {
		return nil, err
	}
	for _, c := range current {
		if roIsContainer(c) && c.Raw != "" {
			return nil, fmt.Errorf("dockerfw: container rule %s is outside the model and cannot be changed", c.ID)
		}
	}

	keeps := make([]int, len(current))
	for j := range keeps {
		keeps[j] = -1
	}
	kept := make([]bool, len(des))
	last := map[model.Family]int{}
	for i, d := range des {
		for j, c := range current {
			if keeps[j] >= 0 || !roIsContainer(c) || roKey(c) != roKey(d) {
				continue
			}
			if j+1 > last[d.Family] {
				keeps[j], kept[i] = i, true
				last[d.Family] = j + 1
			}
			break
		}
	}

	var ops []RuleOp
	for j, c := range current {
		if roIsContainer(c) && keeps[j] < 0 {
			ops = append(ops, RuleOp{Delete: c.ID})
		}
	}

	var v4, v6 []roEntry
	for j, c := range current {
		if roIsContainer(c) && keeps[j] < 0 {
			continue
		}
		e := roEntry{des: keeps[j]}
		if c.Family == model.FamilyV4 || c.Family == model.FamilyBoth {
			v4 = append(v4, e)
		}
		if c.Family == model.FamilyV6 || c.Family == model.FamilyBoth {
			v6 = append(v6, e)
		}
	}

	for i, d := range des {
		if kept[i] {
			continue
		}
		d.ID = newID()
		if err := d.Validate(); err != nil {
			return nil, fmt.Errorf("dockerfw: rule for %q: %v", d.Comment, err) //nolint:errorlint // wrapping would let the field errors reach a client as the request's own
		}
		list := &v4
		if d.Family == model.FamilyV6 {
			list = &v6
		}
		p := roPosition(*list, des, i)
		insert := 0
		if p < len(*list) {
			insert = p + 1
			if d.Family == model.FamilyV6 {
				insert += len(v4)
			}
		}
		*list = slices.Insert(*list, p, roEntry{des: i})
		rule := d
		ops = append(ops, RuleOp{Add: &rule, Insert: insert})
	}
	return ops, nil
}

// roDesired normalizes and checks the desired rules and returns the ones to
// realize: one per tuple, in order.
func roDesired(current, desired []model.Rule) ([]model.Rule, error) {
	var des []model.Rule
	for _, r := range desired {
		r = r.Normalize()
		if _, ok := RuleOf(r.Comment); !ok || r.Origin != model.OriginDarkpanel {
			return nil, fmt.Errorf("dockerfw: desired rule %q has no container label", r.Comment)
		}
		if r.Family != model.FamilyV4 && r.Family != model.FamilyV6 {
			return nil, fmt.Errorf("dockerfw: desired rule %q is for family %q, not one family", r.Comment, r.Family)
		}
		if slices.ContainsFunc(des, func(e model.Rule) bool { return e.Family == r.Family && roTuple(e) == roTuple(r) }) {
			continue
		}
		for _, c := range current {
			if !roIsContainer(c) && roSameTuple(c, r) {
				return nil, fmt.Errorf("dockerfw: container rule %q would take over the rule %s: ufw keeps one rule per tuple", r.Comment, c.ID)
			}
		}
		des = append(des, r)
	}
	return des, nil
}

// roIsContainer reports whether r is one of the rules the container rules
// own: a Darkpanel rule whose label names a container rule.
func roIsContainer(r model.Rule) bool {
	_, ok := RuleOf(r.Comment)
	return ok && (r.Origin == "" || r.Origin == model.OriginDarkpanel)
}

// roTuple is what UFW matches a rule on.
func roTuple(r model.Rule) model.Rule {
	r = r.Normalize()
	r.ID, r.Comment, r.Raw, r.Origin = "", "", "", ""
	return r
}

// roSameTuple reports whether a and b have one tuple, family both being
// compatible with each single family.
func roSameTuple(a, b model.Rule) bool {
	ta, tb := roTuple(a), roTuple(b)
	if ta.Family != tb.Family && ta.Family != model.FamilyBoth && tb.Family != model.FamilyBoth {
		return false
	}
	ta.Family, tb.Family = "", ""
	return ta == tb
}

// roKey is a rule as the keep pass compares it: every field but its ID and
// Raw.
func roKey(r model.Rule) model.Rule {
	r = r.Normalize()
	r.ID, r.Raw = "", ""
	return r
}

// roPosition returns the 0-based index at which desired rule i enters list:
// right after the nearest earlier desired rule of its family the list
// holds, else at the nearest later one, else at the end.
func roPosition(list []roEntry, des []model.Rule, i int) int {
	for k := i - 1; k >= 0; k-- {
		if des[k].Family != des[i].Family {
			continue
		}
		if at := slices.Index(list, roEntry{des: k}); at >= 0 {
			return at + 1
		}
	}
	for k := i + 1; k < len(des); k++ {
		if des[k].Family != des[i].Family {
			continue
		}
		if at := slices.Index(list, roEntry{des: k}); at >= 0 {
			return at
		}
	}
	return len(list)
}
