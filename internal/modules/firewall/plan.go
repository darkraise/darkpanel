package firewall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/eval"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/guard"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// buildFunc computes an action's change from its input and the current
// configured and loaded state.
type buildFunc func(cfg fwstate.Config, cur fwstate.Loaded) (change, error)

// planReq is one mutating action's plan request.
type planReq struct {
	input   any          // the action's decoded input, which the state hash covers
	guarded core.Guarded // the input's guard context and overrides
	manual  bool         // the input's manual: true
	build   buildFunc
}

// prepared is a change computed from the state it starts from.
type prepared struct {
	cfg0 fwstate.Config
	cur  fwstate.Loaded
	c    change
	tg   target
}

// prepare reads the configured and loaded state, refuses an unsupported
// configuration (spec 2.1: a hard refusal, firewall.unsupported_configuration,
// mangle and security rules the evaluator cannot model included), builds the
// change and predicts its target, whose configuration and loaded state must
// be supported too. Between the build and the prediction it refuses the
// change (firewall.docker_user_in_use) while the Docker integration is on
// before or after it and DOCKER-USER holds a foreign rule, unless the change
// discards those rules on purpose. Plan and Apply both start here.
func (f fw) prepare(ctx context.Context, env core.Env, build buildFunc) (prepared, error) {
	cfg0, err := f.d.readConfig(f.d.root)
	if err != nil {
		return prepared{}, fmt.Errorf("firewall: read the configured state: %w", err)
	}
	cur, err := f.d.readLoaded(ctx, env)
	if err != nil {
		return prepared{}, fmt.Errorf("firewall: read the loaded state: %w", err)
	}
	if reasons := hostUnsupported(cfg0, cur); len(reasons) > 0 {
		return prepared{}, errcode.New(errcode.FirewallUnsupportedConfiguration,
			"the host is an unsupported configuration: "+strings.Join(reasons, "; "))
	}
	c, err := build(cfg0, cur)
	if err != nil {
		return prepared{}, err
	}
	// The stored flag is read only when DOCKER-USER holds something to
	// refuse, so a change on a host without the integration's state never
	// reads the Docker store.
	if !c.discardForeign {
		if refusal := dockerUserInUse(cur, cfg0, true); refusal != nil {
			stored, err := readDockerConfig(ctx, env.DB)
			if err != nil {
				return prepared{}, fmt.Errorf("firewall: read the Docker integration flag: %w", err)
			}
			if stored.Enabled || (c.flag != nil && *c.flag) {
				return prepared{}, refusal
			}
		}
	}
	tg, err := f.predictTarget(cfg0, cur, c)
	if err != nil {
		return prepared{}, err
	}
	if reasons := append(slices.Clone(tg.cfgN.Unsupported), unmodelledRules(tg.pred)...); len(reasons) > 0 {
		return prepared{}, errcode.New(errcode.FirewallUnsupportedConfiguration,
			"the configuration the change leads to is unsupported: "+strings.Join(reasons, "; "))
	}
	return prepared{cfg0: cfg0, cur: cur, c: c, tg: tg}, nil
}

// plan is the plan of every mutating firewall action (spec 2.1-2.3). After
// prepare it refuses a change of manual rules without manual: true
// (firewall.manual_required); lists every change of the logical state
// (R39) and, on a host out of sync with its configuration, each differing
// chain; reports the lockout guard; and hashes the input with the state it
// read. On an out-of-sync host the guard fails as a hard refusal, which
// overrides cannot pass, except for a repair whose difference lies in UFW's
// and Darkpanel's own chains: its guard runs on the target as usual.
// Otherwise the guard runs on the predicted loaded state with the plan's
// guard context, the apply's refresh, the retired exposures and the real
// evaluator, and a failing guard listed in overrides passes as overridden.
func (f fw) plan(ctx context.Context, env core.Env, req planReq) (core.PlanOutcome, error) {
	p, err := f.prepare(ctx, env, req.build)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	diffs, err := f.syncDiffs(p.cfg0, p.cur)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	from, to := logical{cfg: p.cfg0, sysctl: p.cur.Sysctl}, logical{cfg: p.tg.final, sysctl: p.tg.pred.Sysctl}
	if ids := manualChanges(from, to); len(ids) > 0 && !req.manual {
		return core.PlanOutcome{}, errcode.New(errcode.FirewallManualRequired,
			fmt.Sprintf("the change removes or adds manual rules %s; set manual: true to change them", strings.Join(ids, ", ")))
	}
	out := core.PlanOutcome{Diff: stateDiff(from, to)}
	docker, err := dockerLines(ctx, env, p.cfg0, p.c)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	out.Diff = append(out.Diff, docker...)
	for _, d := range diffs {
		out.Diff = append(out.Diff, core.DiffLine{Op: " ", Text: "out of sync: " + chainDiffText(d)})
	}
	g, err := f.planGuard(ctx, env, req.guarded, p, diffs)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	out.Guards = []core.Guard{g}
	if out.StateHash, err = stateHash(req.input, p.cfg0, p.cur, env.Call.RepairOf); err != nil {
		return core.PlanOutcome{}, err
	}
	return out, nil
}

// planGuard is the lockout guard of a plan.
func (f fw) planGuard(ctx context.Context, env core.Env, gd core.Guarded, p prepared, diffs []fwstate.ChainDiff) (core.Guard, error) {
	if len(diffs) > 0 {
		repairable := false
		if p.c.repair {
			var err error
			if repairable, err = f.confinedDiffs(p.cfg0, p.cur, diffs); err != nil {
				return core.Guard{}, err
			}
		}
		if !repairable {
			return core.Guard{ID: guard.GuardID, Message: outOfSyncMessage(diffs, p.c.repair)}, nil
		}
	}
	in := guard.Input{Plan: gd.Guard, Refresh: env.Call.GuardRefresh, Retired: env.Call.Retired, Eval: eval.Evaluate}
	g, _, err := f.check(ctx, env, p.tg.pred, in)
	if err != nil {
		return core.Guard{}, fmt.Errorf("firewall: the lockout guard: %w", err)
	}
	if !g.OK && slices.Contains(gd.Overrides, guard.GuardID) {
		g.OK, g.Overridden = true, true
	}
	return g, nil
}

// confinedDiffs reports whether the differences between the loaded state
// and the configuration's prediction lie in UFW's and Darkpanel's own
// chains.
func (f fw) confinedDiffs(cfg fwstate.Config, cur fwstate.Loaded, diffs []fwstate.ChainDiff) (bool, error) {
	tr := fwstate.TransReload
	if !cfg.Enabled {
		tr = fwstate.TransDisable
	}
	pred, err := f.d.predict(cfg, cur, tr)
	if err != nil {
		return false, fmt.Errorf("firewall: predict the loaded state of the configuration: %w", err)
	}
	return confined(diffs, pred, cur), nil
}

func outOfSyncMessage(diffs []fwstate.ChainDiff, repair bool) string {
	chains := make([]string, len(diffs))
	for i, d := range diffs {
		chains[i] = fmt.Sprintf("%s %s/%s", d.Family, d.Table, d.Chain)
	}
	if repair {
		return "the loaded state differs from UFW's configuration outside UFW's and Darkpanel's chains (" +
			strings.Join(chains, ", ") + "), which a repair does not change; every protected flow is unknown"
	}
	return "the host is out of sync with UFW's configuration (" + strings.Join(chains, ", ") +
		"); every protected flow is unknown until firewall.state.repair brings it back"
}

// stateHash hashes what a plan depends on: the input, every file of the
// configured state, every loaded chain with its policy and rule keys, the
// sysctl values and the change a repair repairs. Apply re-plans and refuses
// when it differs (plan.state_changed).
func stateHash(input any, cfg fwstate.Config, cur fwstate.Loaded, repairOf string) (string, error) {
	doc := struct {
		Input    any               `json:"input"`
		Files    map[string]string `json:"files"`
		Variant  string            `json:"variant"`
		Loaded   []string          `json:"loaded"`
		Sysctl   map[string]string `json:"sysctl"`
		RepairOf string            `json:"repairOf,omitempty"`
	}{input, fileHashes(cfg.Files), cur.Variant, loadedKeys(cur), cur.Sysctl, repairOf}
	b, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("firewall: state hash: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// loadedKeys lists every chain of both families with its policy, each
// followed by its rules' keys.
func loadedKeys(st fwstate.Loaded) []string {
	var out []string
	for _, f := range []struct {
		fam fwstate.Family
		rs  *netfilter.Ruleset
	}{{fwstate.V4, st.V4}, {fwstate.V6, st.V6}} {
		if f.rs == nil {
			continue
		}
		for _, t := range f.rs.Tables {
			for _, c := range t.Chains {
				out = append(out, fmt.Sprintf("%s %s %s %s", f.fam, t.Name, c.Name, c.Policy))
				for _, r := range c.Rules {
					out = append(out, "  "+netfilter.Key(r, f.fam == fwstate.V6))
				}
			}
		}
	}
	return out
}
