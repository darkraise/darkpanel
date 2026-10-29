package firewall

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

// DockerSourcesInput is the input of firewall.docker.set-sources (v1). A list
// present replaces the stored one (the empty list clears it), an absent list
// is kept, and at least one is present.
type DockerSourcesInput struct {
	core.Guarded
	PrivateSources    *[]netip.Prefix       `json:"privateSources,omitempty" jsonschema_description:"The source ranges that reach every Docker network; omitted: kept."`
	ForwardExemptions *[]dockerfw.Exemption `json:"forwardExemptions,omitempty" jsonschema_description:"The foreign port forwards into non-Docker destinations that the block lets through; omitted: kept."`
}

// dockerSourcesSet is firewall.docker.set-sources: store the Docker
// integration's private sources and forward exemptions and, while the
// integration is on, rewrite the block with them and reload UFW.
func (f fw) dockerSourcesSet() core.ActionDef {
	return defineMutation(core.ActionSpec{Name: "firewall.docker.set-sources", Summary: "Set the Docker integration's private sources and forward exemptions."}, mutation[DockerSourcesInput]{
		f:       f,
		guarded: func(in DockerSourcesInput) core.Guarded { return in.Guarded },
		build: func(ctx context.Context, env core.Env, in DockerSourcesInput) buildFunc {
			return func(cfg fwstate.Config, cur fwstate.Loaded) (change, error) {
				st, err := f.dockerState(ctx, env, cur)
				if err != nil {
					return change{}, err
				}
				return dockerSourcesChange(cfg, in, st, newULID(env))
			}
		},
		check: func(ctx context.Context, env core.Env, in DockerSourcesInput, _ fwstate.Config) error {
			dc, err := readDockerConfig(ctx, env.DB)
			if err != nil {
				return fmt.Errorf("firewall: verify: %w", err)
			}
			if in.PrivateSources != nil && !slices.Equal(*in.PrivateSources, dc.Settings.PrivateSources) {
				return mismatch("the stored private sources are %v after the change", dc.Settings.PrivateSources)
			}
			if in.ForwardExemptions != nil && !slices.Equal(*in.ForwardExemptions, dc.Settings.ForwardExemptions) {
				return mismatch("the stored forward exemptions are %v after the change", dc.Settings.ForwardExemptions)
			}
			return nil
		},
	})
}

// dockerSourcesChange is the change that stores the input's lists and, while
// the integration is on, rewrites the block and the container rules with
// them. It refuses lists that fail validation, an exemption into a Docker
// network and a range that would shadow a stored deny, whatever the flag; with
// the flag on it also refuses a host that cannot carry the integration.
func dockerSourcesChange(cfg fwstate.Config, in DockerSourcesInput, st dockerState, newID func() string) (change, error) {
	if in.PrivateSources == nil && in.ForwardExemptions == nil {
		const msg = "set privateSources, forwardExemptions or both"
		return change{}, &errcode.Error{Code: errcode.InvalidInput, Detail: msg, Fields: []errcode.FieldError{{Path: "/privateSources", Message: msg}}}
	}
	s := dockerfw.Settings{
		PrivateSources:    slices.Clone(st.cfg.Settings.PrivateSources),
		ForwardExemptions: slices.Clone(st.cfg.Settings.ForwardExemptions),
	}
	if in.PrivateSources != nil {
		s.PrivateSources = slices.Clone(*in.PrivateSources)
	}
	if in.ForwardExemptions != nil {
		s.ForwardExemptions = slices.Clone(*in.ForwardExemptions)
	}
	if err := s.Validate(); err != nil {
		return change{}, err
	}
	if conflicts := dockerfw.ExemptionConflicts(s.ForwardExemptions, st.facts.Networks); len(conflicts) > 0 {
		return change{}, errcode.New(errcode.FirewallUnsupportedIntegration, strings.Join(conflicts, "; "))
	}
	if in.PrivateSources != nil {
		for _, r := range st.rows {
			if err := r.Validate(s); err != nil {
				msg := "container rule " + r.ID + " no longer fits the private sources: " + err.Error()
				return change{}, &errcode.Error{Code: errcode.InvalidInput, Detail: msg, Fields: []errcode.FieldError{{Path: "/privateSources", Message: msg}}}
			}
		}
	}
	if slices.Equal(s.PrivateSources, st.cfg.Settings.PrivateSources) && slices.Equal(s.ForwardExemptions, st.cfg.Settings.ForwardExemptions) {
		return change{}, nil
	}

	c := change{sources: &s}
	if !st.cfg.Enabled {
		return c, nil
	}
	if !st.available {
		return change{}, errcode.New(errcode.FirewallUnsupportedIntegration, "Docker is unavailable: "+st.reason)
	}
	if reasons := dockerfw.Unsupported(st.det); len(reasons) > 0 {
		return change{}, errcode.New(errcode.FirewallUnsupportedIntegration, strings.Join(reasons, "; "))
	}
	if err := refuseOpenIncoming(true, cfg); err != nil {
		return change{}, err
	}
	st.cfg.Settings = s
	st.realized = dockerfw.Realize(st.rows, st.facts, s)
	files, err := blockFiles(cfg, st, true)
	if err != nil {
		return change{}, fmt.Errorf("firewall: rewrite the Docker block: %w", err)
	}
	ops, err := containerRuleOps(cfg, st.realized.Rules, newID)
	if err != nil {
		return change{}, fmt.Errorf("firewall: realize the container rules: %w", err)
	}
	c.files, c.ops, c.reload = files, ops, true
	return c, nil
}
