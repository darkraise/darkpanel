package firewall

import (
	"context"
	"fmt"
	"regexp"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// RulesApplyInput is the input of firewall.rules.apply (v1): Darkpanel
// rules to add, with ids the caller chooses (ULIDs) and the batch's label
// as their comment, and rule ids to delete. A rule already present with
// the same definition, and an id already absent, are skipped, so run again
// with the same input it plans an empty diff (Converges).
type RulesApplyInput struct {
	core.Guarded
	Label  string       `json:"label,omitempty" jsonschema:"pattern=^[A-Za-z0-9 :._/-]{0\\,64}$" jsonschema_description:"The comment of every added rule, after the Darkpanel tag."`
	Add    []model.Rule `json:"add,omitempty" jsonschema_description:"Rules to add, each with its id and without a comment."`
	Delete []string     `json:"delete,omitempty" jsonschema_description:"Ids of rules to delete."`
	Manual bool         `json:"manual,omitempty" jsonschema_description:"Confirms deleting manual rules (spec 2.2)."`
}

var (
	ruleIDRe = regexp.MustCompile(`^([0-9A-HJKMNP-TV-Z]{26}|manual-[0-9a-f]{16})$`)
	ulidIDRe = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)
)

// batch validates a rules.apply input: each rule to add carries a ULID of
// its own, no comment (the label is its comment) and is Darkpanel's; ids
// are unique and none is both added and deleted; every id to delete is a
// rule id.
func batch(in RulesApplyInput) ([]model.Rule, error) {
	var fields []errcode.FieldError
	bad := func(path, msg string) { fields = append(fields, errcode.FieldError{Path: path, Message: msg}) }
	seen := map[string]bool{}
	var rules []model.Rule
	for i, r := range in.Add {
		path := fmt.Sprintf("/add/%d", i)
		switch {
		case !ulidIDRe.MatchString(r.ID):
			bad(path+"/id", "a rule to add carries its own ULID")
		case seen[r.ID]:
			bad(path+"/id", "the id is listed twice")
		}
		seen[r.ID] = true
		if r.Comment != "" {
			bad(path+"/comment", "the batch's label is every added rule's comment")
		}
		if len(fields) > 0 {
			continue
		}
		id := r.ID
		r.ID = ""
		nr, err := newRule(r, id, path)
		if err != nil {
			return nil, err
		}
		nr.Comment = in.Label
		if err := nr.Validate(); err != nil {
			return nil, fieldErrors(err, path)
		}
		rules = append(rules, nr)
	}
	for i, id := range in.Delete {
		path := fmt.Sprintf("/delete/%d", i)
		switch {
		case !ruleIDRe.MatchString(id):
			bad(path, "not a rule id")
		case seen[id]:
			bad(path, "the id is both added and deleted")
		}
	}
	if len(fields) > 0 {
		return nil, &errcode.Error{Code: errcode.InvalidInput, Detail: "the batch is invalid", Fields: fields}
	}
	return rules, nil
}

// rulesApply is firewall.rules.apply.
func (f fw) rulesApply() core.ActionDef {
	return defineMutation(core.ActionSpec{Name: "firewall.rules.apply", Summary: "Add and delete a batch of firewall rules.", Converges: true},
		mutation[RulesApplyInput]{
			f:       f,
			guarded: func(in RulesApplyInput) core.Guarded { return in.Guarded },
			manual:  func(in RulesApplyInput) bool { return in.Manual },
			build: func(_ context.Context, _ core.Env, in RulesApplyInput) buildFunc {
				return func(cfg fwstate.Config, _ fwstate.Loaded) (change, error) { return f.batchChange(cfg, in) }
			},
			check: func(_ context.Context, _ core.Env, in RulesApplyInput, cfg fwstate.Config) error {
				for _, r := range in.Add {
					if n := len(findRules(cfg, r.ID)); n != 1 {
						return mismatch("%d rules carry the id %s after the change, not 1", n, r.ID)
					}
				}
				for _, id := range in.Delete {
					if len(findRules(cfg, id)) > 0 {
						return mismatch("rule %s is still there after the change", id)
					}
				}
				return nil
			},
			finish: func(_ context.Context, _ core.Env, in RulesApplyInput, out *ChangeOutput) error {
				for _, r := range in.Add {
					out.Rules = append(out.Rules, r.ID)
				}
				return nil
			},
		})
}

// batchChange adds, in order, each rule not present yet, then deletes each
// rule still present. A rule present under its id with another definition
// is refused.
func (f fw) batchChange(cfg fwstate.Config, in RulesApplyInput) (change, error) {
	rules, err := batch(in)
	if err != nil {
		return change{}, err
	}
	var c change
	for i, r := range rules {
		path := fmt.Sprintf("/add/%d", i)
		switch groups := findRules(cfg, r.ID); {
		case len(groups) == 1 && ruleText(groups[0].Rule) == ruleText(r):
			continue
		case len(groups) > 0:
			return change{}, &errcode.Error{Code: errcode.InvalidInput, Detail: fmt.Sprintf("rule %s exists with another definition", r.ID),
				Fields: []errcode.FieldError{{Path: path, Message: "a rule with this id exists with another definition"}}}
		}
		op, next, err := f.addOp(cfg, r, path)
		if err != nil {
			return change{}, err
		}
		c.ops, cfg = append(c.ops, op), next
	}
	for i, id := range in.Delete {
		if len(findRules(cfg, id)) == 0 {
			continue
		}
		op, err := deleteOp(cfg, id, fmt.Sprintf("/delete/%d", i))
		if err != nil {
			return change{}, err
		}
		next, err := f.d.edit(cfg, op.Args)
		if err != nil {
			return change{}, errcode.New(errcode.InvalidInput, fmt.Sprintf("Darkpanel cannot predict deleting rule %s: %v", id, err))
		}
		c.ops, cfg = append(c.ops, op), next
	}
	return c, nil
}
