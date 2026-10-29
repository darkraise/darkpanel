package firewall

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// DockerEnableInput is the input of firewall.docker.enable (v1).
type DockerEnableInput struct{ core.Guarded }

// DockerDisableInput is the input of firewall.docker.disable (v1).
type DockerDisableInput struct{ core.Guarded }

// dockerEnable is firewall.docker.enable: turn the Docker integration on,
// writing the block and the stored rows' container rules in one change; a
// no-op when it is on.
func (f fw) dockerEnable() core.ActionDef {
	return defineMutation(core.ActionSpec{Name: "firewall.docker.enable", Summary: "Enable the Docker integration."}, mutation[DockerEnableInput]{
		f:       f,
		guarded: func(in DockerEnableInput) core.Guarded { return in.Guarded },
		build: func(ctx context.Context, env core.Env, _ DockerEnableInput) buildFunc {
			return func(cfg fwstate.Config, cur fwstate.Loaded) (change, error) {
				st, err := f.dockerState(ctx, env, cur)
				if err != nil {
					return change{}, err
				}
				return dockerEnableChange(env, cfg, cur, st)
			}
		},
		check: func(ctx context.Context, env core.Env, _ DockerEnableInput, cfg fwstate.Config) error {
			return dockerFlagCheck(ctx, env, cfg, true)
		},
	})
}

// dockerDisable is firewall.docker.disable: turn the Docker integration off,
// removing the block, the container rules and the owned chains while keeping
// the stored rows and settings; a no-op when it is off.
func (f fw) dockerDisable() core.ActionDef {
	return defineMutation(core.ActionSpec{Name: "firewall.docker.disable", Summary: "Disable the Docker integration."}, mutation[DockerDisableInput]{
		f:       f,
		guarded: func(in DockerDisableInput) core.Guarded { return in.Guarded },
		build: func(ctx context.Context, env core.Env, _ DockerDisableInput) buildFunc {
			return func(cfg fwstate.Config, _ fwstate.Loaded) (change, error) {
				// Removing the block needs no facts, and turning the integration
				// off must not depend on the daemon answering.
				dc, err := readDockerConfig(ctx, env.DB)
				if err != nil {
					return change{}, err
				}
				return dockerDisableChange(env, cfg, dockerState{cfg: dc})
			}
		},
		check: func(ctx context.Context, env core.Env, _ DockerDisableInput, cfg fwstate.Config) error {
			return dockerFlagCheck(ctx, env, cfg, false)
		},
	})
}

// dockerEnableChange is the change that turns the integration on, or a
// refusal (firewall.unsupported_integration) when the host cannot carry it
// (spec 2.6).
func dockerEnableChange(env core.Env, cfg fwstate.Config, cur fwstate.Loaded, st dockerState) (change, error) {
	if st.cfg.Enabled {
		return change{}, nil
	}
	if !st.available {
		return change{}, errcode.New(errcode.FirewallUnsupportedIntegration, "the Docker integration needs Docker: "+st.reason)
	}
	if reasons := dockerUnsupported(st); len(reasons) > 0 {
		return change{}, errcode.New(errcode.FirewallUnsupportedIntegration,
			"the Docker integration is not supported on this host: "+strings.Join(reasons, "; "))
	}
	if err := refuseOpenIncoming(true, cfg); err != nil {
		return change{}, err
	}
	if conflicts := dockerfw.ExemptionConflicts(st.cfg.Settings.ForwardExemptions, st.facts.Networks); len(conflicts) > 0 {
		return change{}, errcode.New(errcode.FirewallUnsupportedIntegration,
			"a forward exemption points into a Docker network: "+strings.Join(conflicts, "; "))
	}
	uncovered, err := denUncoveredForwards(cur, st.cfg.Settings.ForwardExemptions)
	if err != nil {
		return change{}, err
	}
	if len(uncovered) > 0 {
		return change{}, errcode.New(errcode.FirewallUnsupportedIntegration,
			"foreign port forwards need an entry in forwardExemptions before the integration is enabled: "+strings.Join(uncovered, "; "))
	}

	files, err := blockFiles(cfg, st, true)
	if err != nil {
		return change{}, err
	}
	ops, err := containerRuleOps(cfg, st.realized.Rules, newULID(env))
	if err != nil {
		return change{}, err
	}
	var notes []core.DiffLine
	manual := statusManualRoutes(cfg, st.facts)
	for _, r := range model.Merge(cfg.Rules4, cfg.Rules6) {
		if text := ruleText(r); slices.Contains(manual, text) {
			notes = append(notes, core.DiffLine{Op: " ", Text: "manual route rule " + text + ": no effect without a Darkpanel binding"})
		}
	}
	on := true
	return change{flag: &on, files: files, reload: true, ops: ops, notes: notes}, nil
}

// denUncoveredForwards describes each foreign nat PREROUTING DNAT rule of
// both families that no exemption covers, with the exemption proposed for
// it.
func denUncoveredForwards(cur fwstate.Loaded, ex []dockerfw.Exemption) ([]string, error) {
	var out []string
	for _, fam := range []struct {
		family fwstate.Family
		rs     *netfilter.Ruleset
	}{{fwstate.V4, cur.V4}, {fwstate.V6, cur.V6}} {
		if fam.rs == nil {
			continue
		}
		nat := fam.rs.Table("nat")
		if nat == nil {
			continue
		}
		for _, r := range dockerfw.ForeignDNAT(nat) {
			if dockerfw.Covered(r, ex) {
				continue
			}
			proposed, ok := dockerfw.Propose(r, fam.family)
			if !ok {
				out = append(out, r.Text+" -> no exemption can name it")
				continue
			}
			b, err := json.Marshal(proposed)
			if err != nil {
				return nil, fmt.Errorf("firewall: propose an exemption: %w", err)
			}
			out = append(out, r.Text+" -> propose "+string(b))
		}
	}
	return out, nil
}

// dockerDisableChange is the change that turns the integration off. It reads
// none of Docker's facts, so it works with the daemon unreachable, and it
// keeps the stored rows and settings, so a later enable restores the same
// policy. The owned-chain cleanup reloads UFW itself when it must.
func dockerDisableChange(env core.Env, cfg fwstate.Config, st dockerState) (change, error) {
	if !st.cfg.Enabled {
		return change{}, nil
	}
	files, err := blockFiles(cfg, st, false)
	if err != nil {
		return change{}, err
	}
	ops, err := containerRuleOps(cfg, nil, newULID(env))
	if err != nil {
		return change{}, err
	}
	off := false
	return change{flag: &off, files: files, cleanup: true, ops: ops}, nil
}

// dockerFlagCheck is the verify of firewall.docker.enable (on) and
// firewall.docker.disable: the stored flag is on, and both after files hold
// the Docker block, exactly when the change turned the integration on.
func dockerFlagCheck(ctx context.Context, env core.Env, cfg fwstate.Config, on bool) error {
	dc, err := readDockerConfig(ctx, env.DB)
	if err != nil {
		return fmt.Errorf("firewall: verify: %w", err)
	}
	if dc.Enabled != on {
		return mismatch("the stored Docker integration flag is %t after the change", dc.Enabled)
	}
	for _, p := range []string{"/etc/ufw/after.rules", "/etc/ufw/after6.rules"} {
		_, ok, err := dockerfw.BlockOf(cfg.Files[p].Data)
		if err != nil {
			return fmt.Errorf("firewall: verify: %s: %w", p, err)
		}
		switch {
		case ok && !on:
			return mismatch("%s holds the Docker block after the change", p)
		case !ok && on:
			return mismatch("%s holds no Docker block after the change", p)
		}
	}
	return nil
}
