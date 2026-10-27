package firewall

import (
	"context"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// RuleAddInput is the input of firewall.rule.add (v1): one rule without an
// id; the action tags it with its change's id.
type RuleAddInput struct {
	core.Guarded
	Rule model.Rule `json:"rule"`
}

// RuleDeleteInput is the input of firewall.rule.delete (v1).
type RuleDeleteInput struct {
	core.Guarded
	ID     string `json:"id" jsonschema:"pattern=^([0-9A-HJKMNP-TV-Z]{26}|manual-[0-9a-f]{16})$" jsonschema_description:"The rule's id, as firewall.status lists it."`
	Manual bool   `json:"manual,omitempty" jsonschema_description:"Confirms deleting a manual rule (spec 2.2)."`
}

// changeRuleID is the id rule.add gives its rule: the change's id, a ULID
// unique to the change; a plan, which has no change yet, uses planRuleID.
func changeRuleID(env core.Env) string {
	if env.ChangeID != "" {
		return env.ChangeID
	}
	return planRuleID
}

// fieldErrors prefixes the field paths of an invalid-input error.
func fieldErrors(err error, prefix string) error {
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != errcode.InvalidInput {
		return err
	}
	out := &errcode.Error{Code: e.Code, Detail: e.Detail}
	for _, fe := range e.Fields {
		out.Fields = append(out.Fields, errcode.FieldError{Path: prefix + fe.Path, Message: fe.Message})
	}
	return out
}

// newRule validates a rule a caller asks Darkpanel to add (spec 2.2): it
// carries no id, which the caller supplies, and is Darkpanel's; field
// paths start with path.
func newRule(r model.Rule, id, path string) (model.Rule, error) {
	var fields []errcode.FieldError
	if r.ID != "" {
		fields = append(fields, errcode.FieldError{Path: path + "/id", Message: "Darkpanel assigns the id"})
	}
	if r.Origin == model.OriginManual {
		fields = append(fields, errcode.FieldError{Path: path + "/origin", Message: "a rule Darkpanel adds is Darkpanel's"})
	}
	if len(fields) > 0 {
		return model.Rule{}, &errcode.Error{Code: errcode.InvalidInput, Detail: "the rule is not a new Darkpanel rule", Fields: fields}
	}
	r = r.Normalize()
	r.ID, r.Origin = id, model.OriginDarkpanel
	if err := r.Validate(); err != nil {
		return model.Rule{}, fieldErrors(err, path)
	}
	return r, nil
}

// addOp is the effect that adds r to cfg, with the configuration it
// leads to. UFW skips a rule it already holds under another comment, so a
// rule the prediction does not add is refused.
func (f fw) addOp(cfg fwstate.Config, r model.Rule, path string) (ufwOp, fwstate.Config, error) {
	op, err := addRuleOp(r)
	if err != nil {
		return ufwOp{}, fwstate.Config{}, err
	}
	next, err := f.d.edit(cfg, op.Args)
	if err != nil {
		return ufwOp{}, fwstate.Config{}, &errcode.Error{Code: errcode.InvalidInput, Detail: "UFW cannot add the rule: " + err.Error(),
			Fields: []errcode.FieldError{{Path: path, Message: err.Error()}}}
	}
	if len(findRules(next, r.ID)) == 0 {
		return ufwOp{}, fwstate.Config{}, &errcode.Error{Code: errcode.InvalidInput, Detail: "UFW already holds this rule",
			Fields: []errcode.FieldError{{Path: path, Message: "UFW already holds this rule under another comment"}}}
	}
	return op, next, nil
}

// ruleAdd is firewall.rule.add.
func (f fw) ruleAdd() core.ActionDef {
	return defineMutation(core.ActionSpec{Name: "firewall.rule.add", Summary: "Add a firewall rule."}, mutation[RuleAddInput]{
		f:       f,
		guarded: func(in RuleAddInput) core.Guarded { return in.Guarded },
		build: func(_ context.Context, env core.Env, in RuleAddInput) buildFunc {
			return func(cfg fwstate.Config, _ fwstate.Loaded) (change, error) {
				r, err := newRule(in.Rule, changeRuleID(env), "/rule")
				if err != nil {
					return change{}, err
				}
				op, _, err := f.addOp(cfg, r, "/rule")
				if err != nil {
					return change{}, err
				}
				return change{ops: []ufwOp{op}}, nil
			}
		},
		check: func(_ context.Context, env core.Env, _ RuleAddInput, cfg fwstate.Config) error {
			if n := len(findRules(cfg, env.ChangeID)); n != 1 {
				return mismatch("%d rules carry the tag %s after the change, not 1", n, env.ChangeID)
			}
			return nil
		},
		finish: func(_ context.Context, env core.Env, _ RuleAddInput, out *ChangeOutput) error {
			out.Rules = []string{env.ChangeID}
			return nil
		},
	})
}

// deleteOp is the effect that deletes the rule with id from cfg. A manual
// rule outside the model is kept by every operation (spec 2.2).
func deleteOp(cfg fwstate.Config, id, path string) (ufwOp, error) {
	refuse := func(msg string) error {
		return &errcode.Error{Code: errcode.InvalidInput, Detail: msg, Fields: []errcode.FieldError{{Path: path, Message: msg}}}
	}
	groups := findRules(cfg, id)
	switch {
	case len(groups) == 0:
		return ufwOp{}, refuse(fmt.Sprintf("no rule has the id %s", id))
	case len(groups) > 1:
		return ufwOp{}, refuse(fmt.Sprintf("%d rules carry the id %s", len(groups), id))
	case groups[0].Rule.Raw != "":
		return ufwOp{}, refuse(fmt.Sprintf("rule %s is outside the rules Darkpanel can write, so Darkpanel keeps it", id))
	}
	return deleteRuleOp(groups[0].Rule), nil
}

// ruleDelete is firewall.rule.delete.
func (f fw) ruleDelete() core.ActionDef {
	return defineMutation(core.ActionSpec{Name: "firewall.rule.delete", Summary: "Delete a firewall rule."}, mutation[RuleDeleteInput]{
		f:       f,
		guarded: func(in RuleDeleteInput) core.Guarded { return in.Guarded },
		manual:  func(in RuleDeleteInput) bool { return in.Manual },
		build: func(_ context.Context, _ core.Env, in RuleDeleteInput) buildFunc {
			return func(cfg fwstate.Config, _ fwstate.Loaded) (change, error) {
				op, err := deleteOp(cfg, in.ID, "/id")
				if err != nil {
					return change{}, err
				}
				return change{ops: []ufwOp{op}}, nil
			}
		},
		check: func(_ context.Context, _ core.Env, in RuleDeleteInput, cfg fwstate.Config) error {
			if len(findRules(cfg, in.ID)) > 0 {
				return mismatch("rule %s is still there after the change", in.ID)
			}
			return nil
		},
	})
}
