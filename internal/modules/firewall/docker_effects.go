package firewall

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// The resource nouns of the Docker integration's logical state: a stored
// setting ("docker" for the flag, "sources" for the settings), a stored
// container rule by its id, and Darkpanel's Docker chains ("docker").
const (
	nounSetting   = "firewall.setting"
	nounContainer = "firewall.container"
	nounChains    = "firewall.chains"
)

const (
	deResourceFlag    = nounSetting + ":docker"
	deResourceSources = nounSetting + ":sources"
	deResourceChains  = nounChains + ":docker"
)

// dockerOp is one change to the Docker integration's state that
// runDockerEffect journals as an effect. After is the resource's State once
// apply has run; nil is the JSON null.
type dockerOp struct {
	Kind     core.EffectKind
	Resource string // firewall.setting:docker, firewall.setting:sources, firewall.container:<rule id> or firewall.chains:docker
	After    any
	apply    func(ctx context.Context, env core.Env) error
}

// flagOp stores the integration flag, keeping the stored settings.
func flagOp(on bool) dockerOp {
	return dockerOp{Kind: core.EffectModify, Resource: deResourceFlag, After: on,
		apply: func(ctx context.Context, env core.Env) error {
			return deEditConfig(ctx, env, func(c *dockerConfig) { c.Enabled = on })
		}}
}

// sourcesOp stores the integration's settings, keeping the stored flag.
func sourcesOp(s dockerfw.Settings) dockerOp {
	s = deNormalized(s)
	return dockerOp{Kind: core.EffectModify, Resource: deResourceSources, After: s,
		apply: func(ctx context.Context, env core.Env) error {
			return deEditConfig(ctx, env, func(c *dockerConfig) { c.Settings = s })
		}}
}

// rowCreateOp inserts the container rule r.
func rowCreateOp(r dockerfw.ContainerRule) dockerOp {
	return dockerOp{Kind: core.EffectCreate, Resource: nounContainer + ":" + r.ID, After: r,
		apply: func(ctx context.Context, env core.Env) error {
			return env.DB.Write(ctx, func(tx *sql.Tx) error { return insertContainerRule(ctx, tx, r, env.Clock.Now()) })
		}}
}

// rowDeleteOp deletes the container rule r.
func rowDeleteOp(r dockerfw.ContainerRule) dockerOp {
	return dockerOp{Kind: core.EffectDelete, Resource: nounContainer + ":" + r.ID,
		apply: func(ctx context.Context, env core.Env) error {
			return env.DB.Write(ctx, func(tx *sql.Tx) error { return deleteContainerRule(ctx, tx, r.ID) })
		}}
}

// chainsOp leaves Darkpanel's Docker chains in state, "loaded" or "absent",
// by running apply.
func chainsOp(state string, apply func(ctx context.Context, env core.Env) error) dockerOp {
	return dockerOp{Kind: core.EffectModify, Resource: deResourceChains, After: state, apply: apply}
}

// deEditConfig rewrites the stored Docker configuration with edit applied,
// reading it inside the write transaction so no other write falls between.
func deEditConfig(ctx context.Context, env core.Env, edit func(c *dockerConfig)) error {
	return env.DB.Write(ctx, func(tx *sql.Tx) error {
		c, err := readDockerConfig(ctx, env.DB)
		if err != nil {
			return err
		}
		edit(&c)
		return writeDockerConfig(ctx, tx, c)
	})
}

// deNormalized returns s with nil lists made empty, so that settings stored
// as null and as [] are one State.
func deNormalized(s dockerfw.Settings) dockerfw.Settings {
	if s.PrivateSources == nil {
		s.PrivateSources = []netip.Prefix{}
	}
	if s.ForwardExemptions == nil {
		s.ForwardExemptions = []dockerfw.Exemption{}
	}
	return s
}

// dockerDoc returns the State of a Docker resource; these resources carry
// no Version. A container rule's identity may carry the "?" of a creation
// not yet made.
func dockerDoc(ctx context.Context, env core.Env, d deps, resource string) (json.RawMessage, error) {
	noun, ident, err := splitResource(resource)
	if err != nil {
		return nil, err
	}
	switch {
	case resource == deResourceFlag || resource == deResourceSources:
		c, err := readDockerConfig(ctx, env.DB)
		if err != nil {
			return nil, fmt.Errorf("firewall: %s: %w", resource, err)
		}
		if resource == deResourceFlag {
			return mustJSON(c.Enabled), nil
		}
		return mustJSON(deNormalized(c.Settings)), nil
	case noun == nounContainer:
		r, ok, err := containerRule(ctx, env.DB, ident)
		switch {
		case err != nil:
			return nil, fmt.Errorf("firewall: %s: %w", resource, err)
		case !ok:
			return json.RawMessage("null"), nil
		}
		return mustJSON(r), nil
	case resource == deResourceChains:
		st, err := d.readLoaded(ctx, env)
		if err != nil {
			return nil, fmt.Errorf("firewall: %s: %w", resource, err)
		}
		if deChainsLoaded(st.V4) || deChainsLoaded(st.V6) {
			return mustJSON("loaded"), nil
		}
		return mustJSON("absent"), nil
	}
	return nil, fmt.Errorf("firewall: effect resource %q is not a Docker resource", resource)
}

// deChainsLoaded reports whether rs declares a darkpanel-* or dp-b-* chain
// in any table; DOCKER-USER is Docker's own and does not count.
func deChainsLoaded(rs *netfilter.Ruleset) bool {
	if rs == nil {
		return false
	}
	for _, t := range rs.Tables {
		for _, c := range t.Chains {
			if c.Name != dockerfw.ChainDockerUser && dockerfw.Owned(c.Name) {
				return true
			}
		}
	}
	return false
}

// runDockerEffect makes op as a journaled effect (spec 1.2, 2.5), as runUfw
// does for a ufw vector: Before is the resource now, the effect is intended
// before apply runs and, from the observation after it, recorded made
// (State-equal to After), skipped (equal to Before, whatever apply
// returned) or left intended for the engine's settle with an error.
func runDockerEffect(ctx context.Context, env core.Env, d deps, op dockerOp) error {
	if op.apply == nil {
		return fmt.Errorf("firewall: %s: the op has nothing to apply", op.Resource)
	}
	before, err := dockerDoc(ctx, env, d, op.Resource)
	if err != nil {
		return err
	}
	absent := jsonEqual(before, json.RawMessage("null"))
	switch {
	case op.Kind == core.EffectCreate && !absent:
		return fmt.Errorf("firewall: %s exists already", op.Resource)
	case op.Kind == core.EffectDelete && absent:
		return fmt.Errorf("firewall: %s does not exist", op.Resource)
	}
	after := core.StateDoc{State: mustJSON(op.After)}
	e := core.Effect{Kind: op.Kind, Resource: op.Resource, Local: true, InverseLocal: true,
		Before: core.StateDoc{State: before}, After: &after}
	if op.Kind == core.EffectCreate {
		_, id, _ := splitResource(op.Resource)
		e.Resource, e.Recovery = nounContainer+":?"+id, &core.RecoveryID{Kind: "marker", Value: id}
	}
	if _, err := runJournaled(ctx, env, d, e, op.Resource, func() (core.CmdResult, error) {
		return core.CmdResult{}, op.apply(ctx, env)
	}); err != nil {
		return fmt.Errorf("firewall: %s: %w", op.Resource, err)
	}
	return nil
}

// deObserve is observe for a Docker resource: a container rule's creation
// matches the rule once it is stored, every other effect matches 1.
func deObserve(ctx context.Context, env core.Env, d deps, e core.Effect, noun, ident string) (core.Observation, error) {
	state, err := dockerDoc(ctx, env, d, e.Resource)
	if err != nil {
		return core.Observation{}, err
	}
	obs := core.Observation{Current: core.StateDoc{State: state}, Matches: 1, Exclusive: true}
	if noun == nounContainer && e.Kind == core.EffectCreate {
		if jsonEqual(state, json.RawMessage("null")) {
			obs.Matches = 0
		} else {
			obs.Resource = nounContainer + ":" + ident
		}
	}
	return obs, nil
}
