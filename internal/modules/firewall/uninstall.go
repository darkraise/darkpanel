package firewall

import (
	"context"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
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

// Uninstall applies the option rules (spec 2.9) and does nothing on a host
// without UFW. It succeeds again on a host where it already ran: remove
// finds no Darkpanel rule left, and baseline restores the same snapshot
// again. keepData does not change it: rules are the host's firewall, not
// Darkpanel's data.
func (m module) Uninstall(ctx context.Context, env core.Env, _ bool, opts map[string]string) error {
	v := opts["rules"]
	if v != rulesRemove && v != rulesKeep && v != rulesBaseline {
		return fmt.Errorf("firewall: uninstall option rules=%q is not remove, keep or baseline", v)
	}
	if v == rulesKeep {
		return nil
	}
	if err := m.f.regular(ufwPath); err != nil {
		var missing missingFile
		if errors.As(err, &missing) {
			env.Log.Info("firewall: UFW is not installed; the uninstall leaves the firewall as it is")
			return nil
		}
		return fmt.Errorf("firewall: uninstall: %w", err)
	}
	if v == rulesBaseline {
		return m.f.restoreBaseline(ctx, env)
	}
	return m.f.removeRules(ctx, env)
}

// removeRules deletes every Darkpanel rule, then reloads an active UFW. A
// tagged rule the model cannot express cannot be deleted by its arguments
// and is left, with a warning.
func (f fw) removeRules(ctx context.Context, env core.Env) error {
	cfg, err := f.d.readConfig(f.d.root)
	if err != nil {
		return fmt.Errorf("firewall: uninstall: %w", err)
	}
	deleted := 0
	for _, r := range model.Merge(cfg.Rules4, cfg.Rules6) {
		switch {
		case r.Origin != model.OriginDarkpanel:
			continue
		case r.Raw != "":
			env.Log.Warn("firewall: the uninstall leaves a Darkpanel rule it cannot express", "rule", r.ID, "tuple", r.Raw)
			continue
		}
		if err := ufw(ctx, env, model.DeleteArgs(r)); err != nil {
			return fmt.Errorf("firewall: uninstall: delete rule %s: %w", r.ID, err)
		}
		env.Log.Info("firewall: the uninstall deleted a Darkpanel rule", "rule", ruleText(r))
		deleted++
	}
	if deleted > 0 && cfg.Enabled {
		return ufw(ctx, env, reloadArgs)
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
