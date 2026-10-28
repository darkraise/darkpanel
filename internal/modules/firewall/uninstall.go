package firewall

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// The values of the uninstall option rules (spec 2.9).
const (
	rulesRemove   = "remove"
	rulesKeep     = "keep"
	rulesBaseline = "baseline"
)

// UninstallOptions declares the option rules (spec 1.11, 2.9).
func (module) UninstallOptions() []core.UninstallOption {
	return []core.UninstallOption{{
		Key: "rules", Values: []string{rulesRemove, rulesKeep, rulesBaseline}, Default: rulesRemove,
		Help: "remove deletes Darkpanel's rules and keeps manual ones; keep leaves every rule; baseline restores the firewall as it was before Darkpanel's first change",
	}}
}

// Uninstall applies the option rules (spec 2.9), which leave the rules of a
// host without UFW as they are, and then, for every value of rules and with
// or without UFW, finishes the Docker cleanups an earlier change left and
// removes what the Docker integration left: the block in the after files and
// its chains. Each step is attempted whatever the earlier ones returned. It
// succeeds again on a host where it already ran: remove finds no Darkpanel
// rule left, baseline restores the same snapshot again, and the Docker
// integration has left nothing. keepData does not change it: rules are the
// host's firewall, not Darkpanel's data.
func (m module) Uninstall(ctx context.Context, env core.Env, _ bool, opts map[string]string) error {
	v := opts["rules"]
	if v != rulesRemove && v != rulesKeep && v != rulesBaseline {
		return fmt.Errorf("firewall: uninstall option rules=%q is not remove, keep or baseline", v)
	}
	hasUFW := true
	if err := m.f.regular(ufwPath); err != nil {
		var missing missingFile
		if !errors.As(err, &missing) {
			return fmt.Errorf("firewall: uninstall: %w", err)
		}
		hasUFW = false
	}
	return errors.Join(
		m.f.finishCleanups(ctx, env),
		m.f.uninstallRules(ctx, env, v, hasUFW),
		m.f.uninstallDocker(ctx, env, hasUFW),
	)
}

// uninstallRules applies the option rules with value v.
func (f fw) uninstallRules(ctx context.Context, env core.Env, v string, hasUFW bool) error {
	if v == rulesKeep {
		return nil
	}
	if !hasUFW {
		env.Log.Info("firewall: UFW is not installed; the uninstall leaves the firewall as it is")
		return nil
	}
	if v == rulesBaseline {
		return f.restoreBaseline(ctx, env)
	}
	return f.removeRules(ctx, env)
}

// uninstallDocker runs the owned-chain cleanup when the Docker integration
// left something on the host. It runs after the rules work, so a refusal for
// a foreign DOCKER-USER rule fails the uninstall only once the rules are
// done; the operator removes that rule and runs the uninstall again.
func (f fw) uninstallDocker(ctx context.Context, env core.Env, hasUFW bool) error {
	left, err := f.dockerLeft(ctx, env, hasUFW)
	if err != nil {
		return fmt.Errorf("firewall: uninstall: %w", err)
	}
	if !left {
		return nil
	}
	return f.cleanupChains(ctx, env, "uninstall", cleanupRefuse)
}

// dockerLeft reports whether the Docker integration left its block in an
// after file or a darkpanel-* or dp-b-* chain loaded. Foreign DOCKER-USER
// rules alone do not count: the cleanup would refuse them on a host that
// never enabled the integration.
func (f fw) dockerLeft(ctx context.Context, env core.Env, hasUFW bool) (bool, error) {
	cfg, err := f.d.readConfig(f.d.root)
	switch {
	case err != nil && hasUFW:
		return false, err
	case err == nil:
		for _, p := range cleanupFiles {
			e := cfg.Files[p]
			if !e.Present {
				continue
			}
			// A malformed block counts as a block, so the cleanup reports it.
			if _, ok, err := dockerfw.BlockOf(e.Data); ok || err != nil {
				return true, nil
			}
		}
	}
	if err := f.regular("/usr/sbin/iptables"); err != nil {
		var missing missingFile
		if errors.As(err, &missing) {
			return false, nil
		}
		return false, err
	}
	cur, err := f.d.readLoaded(ctx, env)
	if err != nil {
		return false, fmt.Errorf("read the loaded state: %w", err)
	}
	return len(cleanupOwned(cur.V4)) > 0 || len(cleanupOwned(cur.V6)) > 0, nil
}

// removeRules deletes every Darkpanel rule, then reloads an active UFW. A
// tagged rule the model cannot express cannot be deleted by its arguments:
// it is left, and the uninstall fails naming it once the rest is done.
func (f fw) removeRules(ctx context.Context, env core.Env) error {
	cfg, err := f.d.readConfig(f.d.root)
	if err != nil {
		return fmt.Errorf("firewall: uninstall: %w", err)
	}
	deleted := 0
	var left []string
	for _, r := range model.Merge(cfg.Rules4, cfg.Rules6) {
		switch {
		case r.Origin != model.OriginDarkpanel:
			continue
		case r.Raw != "":
			env.Log.Warn("firewall: the uninstall leaves a Darkpanel rule it cannot express", "rule", r.ID, "tuple", r.Raw)
			left = append(left, r.ID)
			continue
		}
		if err := ufw(ctx, env, model.DeleteArgs(r)); err != nil {
			return fmt.Errorf("firewall: uninstall: delete rule %s: %w", r.ID, err)
		}
		env.Log.Info("firewall: the uninstall deleted a Darkpanel rule", "rule", ruleText(r))
		deleted++
	}
	if deleted > 0 && cfg.Enabled {
		if err := ufw(ctx, env, reloadArgs); err != nil {
			return err
		}
	}
	if len(left) > 0 {
		return fmt.Errorf("firewall: uninstall: Darkpanel rules %s cannot be written as ufw arguments, so they stay; delete them by number with ufw",
			strings.Join(left, ", "))
	}
	return nil
}

// restoreBaseline restores the baseline snapshot, the host's first, taken
// before Darkpanel's first change, after logging every manual rule the
// restore adds or removes. Without a baseline there is nothing to restore.
func (f fw) restoreBaseline(ctx context.Context, env core.Env) error {
	list, err := listSnapshots(ctx, env.DB, snapshotFilter{})
	if err != nil {
		return fmt.Errorf("firewall: uninstall: %w", err)
	}
	var id string
	for _, s := range list {
		if s.Baseline {
			id = s.ID
		}
	}
	if id == "" {
		env.Log.Info("firewall: no baseline snapshot; the uninstall leaves the firewall as it is")
		return nil
	}
	snap, err := getSnapshot(ctx, env.DB, id)
	if err != nil {
		return fmt.Errorf("firewall: uninstall: %w", err)
	}
	cfgB, err := f.snapshotConfig(snap)
	if err != nil {
		return fmt.Errorf("firewall: uninstall: %w", err)
	}
	cur, err := f.d.readConfig(f.d.root)
	if err != nil {
		return fmt.Errorf("firewall: uninstall: %w", err)
	}
	from, to := model.Merge(cur.Rules4, cur.Rules6), model.Merge(cfgB.Rules4, cfgB.Rules6)
	inFrom, inTo := ruleTexts(from), ruleTexts(to)
	for _, r := range from {
		if r.Origin == model.OriginManual && !inTo[ruleText(r)] {
			env.Log.Info("firewall: restoring the baseline removes the manual rule", "rule", ruleText(r))
		}
	}
	for _, r := range to {
		if r.Origin == model.OriginManual && !inFrom[ruleText(r)] {
			env.Log.Info("firewall: restoring the baseline adds the manual rule", "rule", ruleText(r))
		}
	}
	rep, err := restore(ctx, env, f.d, snap)
	if err != nil {
		return fmt.Errorf("firewall: uninstall: restore the baseline snapshot %s: %w", id, err)
	}
	logVanished(env, rep.Vanished)
	return nil
}
