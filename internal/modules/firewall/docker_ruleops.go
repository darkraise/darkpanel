package firewall

import (
	"crypto/rand"
	"fmt"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// newULID returns the source of ids for new UFW rules and new container
// rule rows: each call mints a fresh ULID at the time env's clock reads
// then.
func newULID(env core.Env) func() string {
	return func() string {
		return ulid.MustNew(ulid.Timestamp(env.Clock.Now()), rand.Reader).String()
	}
}

// containerRuleOps returns the ufw operations that bring cfg's container
// rules to desired, in the order dockerfw.RuleOps gives them; newID mints
// the id of each added rule. It reads no host state.
func containerRuleOps(cfg fwstate.Config, desired []model.Rule, newID func() string) ([]ufwOp, error) {
	steps, err := dockerfw.RuleOps(model.Merge(cfg.Rules4, cfg.Rules6), desired, newID)
	if err != nil {
		return nil, fmt.Errorf("firewall: container rule operations: %w", err)
	}
	var ops []ufwOp
	for _, s := range steps {
		switch {
		case s.Delete != "":
			found := findRules(cfg, s.Delete)
			if len(found) != 1 {
				return nil, fmt.Errorf("firewall: container rule operations: %d rules carry the id %s", len(found), s.Delete)
			}
			ops = append(ops, deleteRuleOp(found[0].Rule))
		case s.Insert == 0:
			op, err := addRuleOp(*s.Add)
			if err != nil {
				return nil, err
			}
			ops = append(ops, op)
		default:
			// An insert is journaled as an add: the rule's tag, not its
			// position, is what recovery and the inverse find it by.
			ops = append(ops, ufwOp{Kind: core.EffectCreate, Resource: ruleResource(s.Add.ID), Args: model.InsertArgs(s.Insert, *s.Add)})
		}
	}
	return ops, nil
}
