package firewall

import (
	"context"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/conf"
	"github.com/darkraise/darkpanel/internal/modules/firewall/eval"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/guard"
	"github.com/darkraise/darkpanel/internal/modules/firewall/loaded"
	"github.com/darkraise/darkpanel/internal/modules/firewall/predict"
)

// checkFunc runs the lockout guard on st: guard.Check, or a test's stand-in.
type checkFunc func(ctx context.Context, env core.Env, st fwstate.Loaded, in guard.Input) (core.Guard, []guard.FlowResult, error)

// fw is everything the firewall's handlers use: the host-facing functions
// of deps and, for plans, the IPV6=no refusal and the lockout guard.
type fw struct {
	d           deps
	ipv6Refusal func(cfg fwstate.Config, cur fwstate.Loaded, tr fwstate.Transition) error
	check       checkFunc
}

// productionFW is the firewall over this host.
func productionFW() fw {
	return fw{
		d: deps{
			root:       "/",
			readConfig: conf.Read,
			readLoaded: loaded.Reader{}.Read,
			predict:    predict.Predict,
			edit:       predict.Edit,
			files:      predict.Files,
			compare:    loaded.Compare,
			sysctl:     loaded.PredictSysctl,
			lockout:    lockoutVia(guard.Check),
		},
		ipv6Refusal: predict.IPv6Refusal,
		check:       guard.Check,
	}
}

// lockoutVia is the lockout guard of a revert's connectivity exception
// (deps.lockout): check evaluates, with the real evaluator, the flows of
// the snapshot's guard context and of the host's live facts. A revert has
// no apply-time refresh and no retired exposure.
func lockoutVia(check checkFunc) func(ctx context.Context, env core.Env, st fwstate.Loaded, gc *core.GuardContext) (bool, string, error) {
	return func(ctx context.Context, env core.Env, st fwstate.Loaded, gc *core.GuardContext) (bool, string, error) {
		g, _, err := check(ctx, env, st, guard.Input{Plan: gc, Eval: eval.Evaluate})
		if err != nil {
			return false, "", err
		}
		return g.OK, g.Message, nil
	}
}
