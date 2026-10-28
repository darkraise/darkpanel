package firewall

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// cleanupMode says what the owned-chain cleanup does with DOCKER-USER rules
// Darkpanel did not write.
type cleanupMode int

const (
	// cleanupRefuse refuses the cleanup while DOCKER-USER holds a foreign
	// rule.
	cleanupRefuse cleanupMode = iota
	// cleanupPreserve keeps the foreign rules in DOCKER-USER, around the
	// -j RETURN the cleanup leaves.
	cleanupPreserve
)

// cleanupFiles are the files that hold the Docker block, IPv4 first.
var cleanupFiles = []string{"/etc/ufw/after.rules", "/etc/ufw/after6.rules"}

// cleanupUserChain is the loaded DOCKER-USER chain of family in cur, nil
// when it is absent.
func cleanupUserChain(cur fwstate.Loaded, family fwstate.Family) *netfilter.Chain {
	rs := cur.V4
	if family == fwstate.V6 {
		rs = cur.V6
	}
	return chainOf(rs, "filter", dockerfw.ChainDockerUser)
}

// cleanupOwned lists, in chain order, the filter chains of rs that belong to
// the Docker block: the darkpanel-* and dp-b-* chains, not DOCKER-USER.
func cleanupOwned(rs *netfilter.Ruleset) []string {
	if rs == nil {
		return nil
	}
	t := rs.Table("filter")
	if t == nil {
		return nil
	}
	var out []string
	for _, c := range t.Chains {
		if c.Name != dockerfw.ChainDockerUser && dockerfw.Owned(c.Name) {
			out = append(out, c.Name)
		}
	}
	return out
}

// cleanChainsPreserving is the cleanup the inverse of the chains effect
// runs: it never refuses for foreign rules, it keeps them.
func (f fw) cleanChainsPreserving(ctx context.Context, env core.Env, owner string) error {
	return f.cleanupChains(ctx, env, owner, cleanupPreserve)
}

// cleanupChains removes the Docker block from the files and its chains from
// the kernel, leaving DOCKER-USER with -j RETURN, for owner (a change id or
// "uninstall"). Its intent is recorded until the cleanup is verified, so
// that finishCleanups repeats one that failed or was cut off.
func (f fw) cleanupChains(ctx context.Context, env core.Env, owner string, mode cleanupMode) error {
	return f.runCleanup(ctx, env, owner, mode, false)
}

// finishCleanups repeats every cleanup whose intent is still recorded. An
// automatic repeat never refuses, so it keeps foreign rules.
func (f fw) finishCleanups(ctx context.Context, env core.Env) error {
	owners, err := pendingCleanups(ctx, env.DB)
	if err != nil {
		return fmt.Errorf("firewall: read the pending cleanups: %w", err)
	}
	var errs []error
	for _, owner := range owners {
		if err := f.runCleanup(ctx, env, owner, cleanupPreserve, true); err != nil {
			errs = append(errs, fmt.Errorf("firewall: finish the cleanup of %s: %w", owner, err))
		}
	}
	return errors.Join(errs...)
}

// runCleanup runs owner's cleanup in mode; repeat says that its intent is
// recorded already.
func (f fw) runCleanup(ctx context.Context, env core.Env, owner string, mode cleanupMode, repeat bool) error {
	cfg, err := f.d.readConfig(f.d.root)
	if err != nil {
		return fmt.Errorf("firewall: cleanup: %w", err)
	}
	cur, err := f.d.readLoaded(ctx, env)
	if err != nil {
		return fmt.Errorf("firewall: cleanup: %w", err)
	}
	var known [][]byte
	for _, p := range cleanupFiles {
		if e := cfg.Files[p]; e.Present {
			known = append(known, e.Data)
		}
	}
	rules, err := blockUserRules(known)
	if err != nil {
		return fmt.Errorf("firewall: cleanup: %w", err)
	}
	rec, held, err := foreignOf(ctx, env.DB, owner)
	if err != nil {
		return err
	}
	if held {
		if rules, err = blockUserRules(slices.Concat(known, rec.Known)); err != nil {
			return fmt.Errorf("firewall: cleanup: %w", err)
		}
	}
	// Refusing before anything is recorded or written means a refused
	// cleanup changes nothing.
	if mode == cleanupRefuse && !held {
		var found []string
		for _, fam := range preserveFamilies {
			for _, r := range dockerfw.Foreign(cleanupUserChain(cur, fam), rules, fam) {
				found = append(found, string(fam)+" "+r.Text)
			}
		}
		if len(found) > 0 {
			return errcode.New(errcode.FirewallDockerUserInUse,
				"DOCKER-USER holds rules Darkpanel did not write: "+strings.Join(found, "; "))
		}
	}
	if !repeat {
		if err := beginCleanup(ctx, env.DB, owner, env.Clock.Now()); err != nil {
			return fmt.Errorf("firewall: record the cleanup of %s: %w", owner, err)
		}
	}
	steps := func() error { return f.cleanupSteps(ctx, env, cfg, cur) }
	// A held record means an earlier attempt was cut off after it read
	// DOCKER-USER, and the files may no longer hold the block. keepForeign
	// judges the chain against the files that attempt recorded too, so the
	// block lines still loaded are not taken for foreign rules, while rules
	// added since then are kept after the recorded ones. The interleaved
	// result is dropped: with no block line left, every foreign rule sits in
	// its original order ahead of -j RETURN.
	if held || len(cleanupOwned(cur.V4)) > 0 || len(cleanupOwned(cur.V6)) > 0 {
		if _, err := f.keepForeign(ctx, env, owner, known, nil, steps); err != nil {
			return err
		}
	} else if err := steps(); err != nil {
		return err
	}
	if err := f.verifyCleanup(ctx, env, rules); err != nil {
		return err
	}
	if err := endCleanup(ctx, env.DB, owner); err != nil {
		return fmt.Errorf("firewall: clear the cleanup of %s: %w", owner, err)
	}
	return nil
}

// cleanupSteps writes the files of cfg that hold the block without it,
// removes the block's chains from each family that has them and, outside a
// change, reloads UFW when it was active (before) and something changed.
// Inside a change the files and the chains are journaled effects, and the
// change's own transition reloads UFW.
func (f fw) cleanupSteps(ctx context.Context, env core.Env, cfg fwstate.Config, before fwstate.Loaded) error {
	changed := false
	for _, p := range cleanupFiles {
		entry := cfg.Files[p]
		if !entry.Present {
			continue
		}
		_, ok, err := dockerfw.BlockOf(entry.Data)
		if err != nil {
			return fmt.Errorf("firewall: cleanup: %s: %w", p, err)
		}
		if !ok {
			continue
		}
		if entry.Data, err = dockerfw.WithoutBlock(entry.Data); err != nil {
			return fmt.Errorf("firewall: cleanup: %s: %w", p, err)
		}
		if env.Effects != nil {
			if err := writeFileEffect(ctx, env, f.d, entry); err != nil {
				return err
			}
		} else if err := env.Files.Restore(ctx, ID, entry); err != nil {
			return fmt.Errorf("firewall: cleanup: %s: %w", p, err)
		}
		changed = true
	}
	now, err := f.d.readLoaded(ctx, env)
	if err != nil {
		return fmt.Errorf("firewall: cleanup: %w", err)
	}
	rulesets := map[fwstate.Family]*netfilter.Ruleset{fwstate.V4: now.V4, fwstate.V6: now.V6}
	var families []fwstate.Family
	for _, fam := range preserveFamilies {
		if len(cleanupOwned(rulesets[fam])) > 0 {
			families = append(families, fam)
		}
	}
	if len(families) > 0 {
		load := func(ctx context.Context, env core.Env) error {
			for _, fam := range families {
				cmd := core.Cmd{Path: restoreProgram(fam, now.Variant), Args: []string{"-n"}, Stdin: dockerfw.CleanupInput(rulesets[fam])}
				res, err := env.Exec.Run(ctx, cmd)
				if err != nil || res.ExitCode != 0 {
					return fmt.Errorf("firewall: remove the Docker chains (%s): %w", fam, runFailure(cmd, res, err))
				}
			}
			return nil
		}
		if env.Effects != nil {
			err = runDockerEffect(ctx, env, f.d, chainsOp("absent", load))
		} else {
			err = load(ctx, env)
		}
		if err != nil {
			return err
		}
		changed = true
	}
	// UFW's chains stay loaded after its package is removed, until a reboot,
	// so the loaded state alone does not show that its program exists.
	if changed && env.Effects == nil && ufwRunning(before) && f.regular(ufwPath) == nil {
		return ufw(ctx, env, reloadArgs)
	}
	return nil
}

// verifyCleanup checks the loaded state after a cleanup: no block chain is
// loaded in either family, and DOCKER-USER holds no rule of blockRules
// other than -j RETURN. Foreign rules the cleanup kept are not in
// blockRules.
func (f fw) verifyCleanup(ctx context.Context, env core.Env, blockRules []netfilter.Rule) error {
	now, err := f.d.readLoaded(ctx, env)
	if err != nil {
		return fmt.Errorf("firewall: verify the cleanup: %w", err)
	}
	for _, fam := range preserveFamilies {
		rs := now.V4
		if fam == fwstate.V6 {
			rs = now.V6
		}
		if owned := cleanupOwned(rs); len(owned) > 0 {
			return fmt.Errorf("firewall: verify the cleanup: %s chains %s are still loaded", fam, strings.Join(owned, ", "))
		}
		c := cleanupUserChain(now, fam)
		if c == nil {
			continue
		}
		v6 := fam == fwstate.V6
		for _, r := range c.Rules {
			key := netfilter.Key(r, v6)
			if key == "-j RETURN" {
				continue
			}
			for _, b := range blockRules {
				if netfilter.Key(b, v6) == key {
					return fmt.Errorf("firewall: verify the cleanup: DOCKER-USER (%s) still holds the block's rule %q", fam, r.Text)
				}
			}
		}
	}
	return nil
}
