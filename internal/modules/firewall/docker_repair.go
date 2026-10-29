package firewall

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// DockerRepairInput is the input of firewall.docker.repair (v1).
type DockerRepairInput struct {
	core.Guarded
	DiscardForeign bool `json:"discardForeign,omitempty" jsonschema_description:"Discards the foreign rules DOCKER-USER holds; the plan lists each. Needs manual: true."`
	Manual         bool `json:"manual,omitempty" jsonschema_description:"Confirms discardForeign."`
}

// dockerRepair is firewall.docker.repair: rewrite the Docker block into both
// after files, realize the stored rows and reload UFW, as a repair of UFW's
// loaded state. Its plan lists each foreign DOCKER-USER rule it discards.
func (f fw) dockerRepair() core.ActionDef {
	def := defineMutation(core.ActionSpec{Name: "firewall.docker.repair", Summary: "Rewrite and reload the Docker integration's block."}, mutation[DockerRepairInput]{
		f:       f,
		guarded: func(in DockerRepairInput) core.Guarded { return in.Guarded },
		manual:  func(in DockerRepairInput) bool { return in.Manual },
		build: func(ctx context.Context, env core.Env, in DockerRepairInput) buildFunc {
			return func(cfg fwstate.Config, cur fwstate.Loaded) (change, error) {
				st, err := f.dockerState(ctx, env, cur)
				if err != nil {
					return change{}, err
				}
				return dockerRepairChange(cfg, in, st, newULID(env))
			}
		},
		check: func(ctx context.Context, env core.Env, _ DockerRepairInput, cfg fwstate.Config) error {
			dc, err := readDockerConfig(ctx, env.DB)
			if err != nil {
				return fmt.Errorf("firewall: verify: %w", err)
			}
			if !dc.Enabled {
				return nil
			}
			for _, p := range []string{"/etc/ufw/after.rules", "/etc/ufw/after6.rules"} {
				if _, ok, err := dockerfw.BlockOf(cfg.Files[p].Data); err != nil || !ok {
					return mismatch("%s holds no Docker block after the repair", p)
				}
			}
			return nil
		},
	})
	plan := def.Handler.Plan
	def.Handler.Plan = func(ctx context.Context, env core.Env, raw json.RawMessage) (core.PlanOutcome, error) {
		out, err := plan(ctx, env, raw)
		if err != nil {
			return out, err
		}
		var in DockerRepairInput
		if json.Unmarshal(raw, &in) != nil || !in.DiscardForeign || !in.Manual {
			return out, nil
		}
		cfg, err := f.d.readConfig(f.d.root)
		if err != nil {
			return core.PlanOutcome{}, fmt.Errorf("firewall: list the foreign rules: %w", err)
		}
		cur, err := f.d.readLoaded(ctx, env)
		if err != nil {
			return core.PlanOutcome{}, fmt.Errorf("firewall: list the foreign rules: %w", err)
		}
		lines, err := repairDiscards(in, cfg, cur)
		if err != nil {
			return core.PlanOutcome{}, err
		}
		out.Diff = append(out.Diff, lines...)
		return out, nil
	}
	return def
}

// dockerRepairChange is the change that repairs the Docker integration: the
// realized block written into both after files, the stored rows' container
// rules and a reload, from the actual loaded state (a repair). It refuses
// discarding foreign rules without manual (firewall.manual_required), a
// repair while the integration is off (request.invalid_input) and a host that
// cannot carry the integration (firewall.unsupported_integration). It does
// not refuse an ACCEPT incoming policy: a repair cannot change it.
func dockerRepairChange(cfg fwstate.Config, in DockerRepairInput, st dockerState, newID func() string) (change, error) {
	if in.DiscardForeign && !in.Manual {
		return change{}, errcode.New(errcode.FirewallManualRequired, "discarding foreign DOCKER-USER rules needs manual: true")
	}
	if !st.cfg.Enabled {
		return change{}, errcode.New(errcode.InvalidInput, "the Docker integration is off, so there is nothing to repair")
	}
	if !st.available {
		return change{}, errcode.New(errcode.FirewallUnsupportedIntegration, "Docker is unavailable: "+st.reason)
	}
	if reasons := dockerfw.Unsupported(st.det); len(reasons) > 0 {
		return change{}, errcode.New(errcode.FirewallUnsupportedIntegration, strings.Join(reasons, "; "))
	}
	files, err := blockFiles(cfg, st, true)
	if err != nil {
		return change{}, fmt.Errorf("firewall: repair the Docker block: %w", err)
	}
	ops, err := containerRuleOps(cfg, st.realized.Rules, newID)
	if err != nil {
		return change{}, fmt.Errorf("firewall: repair the container rules: %w", err)
	}
	return change{repair: true, reload: true, files: files, ops: ops, discardForeign: in.DiscardForeign && in.Manual}, nil
}

// repairDiscards lists, for a repair that discards foreign rules (both
// DiscardForeign and Manual), each rule the loaded DOCKER-USER chain holds
// beyond the block's own in the family's after file, IPv4 first and each
// family in chain order; any other repair lists none.
func repairDiscards(in DockerRepairInput, cfg fwstate.Config, cur fwstate.Loaded) ([]core.DiffLine, error) {
	if !in.DiscardForeign || !in.Manual {
		return nil, nil
	}
	var out []core.DiffLine
	for _, fam := range []struct {
		family fwstate.Family
		loaded *netfilter.Ruleset
		path   string
	}{
		{fwstate.V4, cur.V4, "/etc/ufw/after.rules"},
		{fwstate.V6, cur.V6, "/etc/ufw/after6.rules"},
	} {
		if fam.loaded == nil {
			continue
		}
		filter := fam.loaded.Table("filter")
		if filter == nil {
			continue
		}
		chain := filter.Chain(dockerfw.ChainDockerUser)
		if chain == nil {
			continue
		}
		known, err := drefKnownRules(cfg.Files[fam.path])
		if err != nil {
			return nil, fmt.Errorf("firewall: %s: %w", fam.path, err)
		}
		for _, r := range dockerfw.Foreign(chain, known, fam.family) {
			out = append(out, core.DiffLine{Op: "-", Text: "foreign DOCKER-USER rule (" + string(fam.family) + "): " + r.Text})
		}
	}
	return out, nil
}
