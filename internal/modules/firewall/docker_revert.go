package firewall

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// kfwPlan is how one restore or undo treats the foreign DOCKER-USER rules
// loaded before it.
type kfwPlan struct {
	// block are, per family, the DOCKER-USER rules of the Docker block of
	// that family's after file, as it stands now and afterwards.
	block map[fwstate.Family][]netfilter.Rule
	// known are the after files now and afterwards, IPv4 first, for
	// keepForeign.
	known [][]byte
	// loads reports that the block's chains are loaded afterwards.
	loads bool
	// next are, per family, the DOCKER-USER lines of the block loaded
	// afterwards; a family without an entry loads -j RETURN only.
	next map[fwstate.Family][]string
	// keep reports that the transition flushes foreign rules that must be
	// loaded again.
	keep bool
	// cleanup reports that the block's chains are loaded and will not be
	// afterwards, so the owned-chain cleanup has to remove them.
	cleanup bool
	before  fwstate.Loaded
}

// kfwPlanOf plans a restore or undo that takes the after files from now to
// after (absent or missing entries are files without a block), leaves UFW
// enabled or not and IPv6 on or off, and, when flush, runs a transition that
// rewrites DOCKER-USER, over the loaded state before.
func kfwPlanOf(now, after map[string]core.RestoreEntry, enabled, ipv6, flush bool, before fwstate.Loaded) kfwPlan {
	p := kfwPlan{block: map[fwstate.Family][]netfilter.Rule{}, next: map[fwstate.Family][]string{}, before: before}
	afterBlock := map[fwstate.Family][]netfilter.Rule{}
	holds := map[fwstate.Family]bool{}
	for i, fam := range preserveFamilies {
		path := cleanupFiles[i]
		for _, file := range []core.RestoreEntry{now[path], after[path]} {
			if !file.Present {
				continue
			}
			rules, ok := kfwBlockRules(file.Data)
			if !ok {
				continue
			}
			p.known = append(p.known, file.Data)
			p.block[fam] = append(p.block[fam], rules...)
		}
		if file := after[path]; file.Present {
			afterBlock[fam], holds[fam] = kfwBlockRules(file.Data)
		}
	}
	p.loads = enabled && (holds[fwstate.V4] || (ipv6 && holds[fwstate.V6]))
	if p.loads {
		for _, fam := range preserveFamilies {
			if !holds[fam] || (fam == fwstate.V6 && before.V6 == nil) {
				continue
			}
			var lines []string
			for _, r := range afterBlock[fam] {
				lines = append(lines, r.Text)
			}
			p.next[fam] = lines
		}
	}
	for _, fam := range preserveFamilies {
		if flush && p.loads && len(dockerfw.Foreign(cleanupUserChain(before, fam), p.block[fam], fam)) > 0 {
			p.keep = true
		}
	}
	p.cleanup = !p.loads && (len(cleanupOwned(before.V4)) > 0 || len(cleanupOwned(before.V6)) > 0)
	return p
}

// kfwBlockRules returns the DOCKER-USER rules of file's Docker block; ok is
// false when file holds no block or one that does not parse.
func kfwBlockRules(file []byte) (rules []netfilter.Rule, ok bool) {
	block, found, err := dockerfw.BlockOf(file)
	if err != nil || !found {
		return nil, false
	}
	rs, err := dockerfw.BlockRules(block)
	if err != nil {
		return nil, false
	}
	if c := chainOf(rs, "filter", dockerfw.ChainDockerUser); c != nil {
		rules = c.Rules
	}
	return rules, true
}

// without returns st with the foreign rules of each family's DOCKER-USER
// removed; st's rulesets are left as they are.
func (p kfwPlan) without(st fwstate.Loaded) fwstate.Loaded {
	st.V4 = p.withoutIn(st.V4, fwstate.V4)
	st.V6 = p.withoutIn(st.V6, fwstate.V6)
	return st
}

func (p kfwPlan) withoutIn(rs *netfilter.Ruleset, fam fwstate.Family) *netfilter.Ruleset {
	foreign := dockerfw.Foreign(chainOf(rs, "filter", dockerfw.ChainDockerUser), p.block[fam], fam)
	if len(foreign) == 0 {
		return rs
	}
	lines := map[int]bool{}
	for _, r := range foreign {
		lines[r.Line] = true
	}
	out := *rs
	out.Tables = slices.Clone(rs.Tables)
	for i := range out.Tables {
		if out.Tables[i].Name != "filter" {
			continue
		}
		out.Tables[i].Chains = slices.Clone(out.Tables[i].Chains)
		for j := range out.Tables[i].Chains {
			if c := &out.Tables[i].Chains[j]; c.Name == dockerfw.ChainDockerUser {
				c.Rules = slices.DeleteFunc(slices.Clone(c.Rules), func(r netfilter.Rule) bool { return lines[r.Line] })
			}
		}
	}
	return &out
}

// withoutImage returns img with the foreign rules of each family's
// DOCKER-USER removed, as without does for a loaded state, so that an image
// that captured a foreign rule compares with a loaded state whose foreign
// rules are set aside. img is left as it is; nil stays nil.
func (p kfwPlan) withoutImage(img *beforeImage) *beforeImage {
	if img == nil {
		return nil
	}
	return &beforeImage{V4: p.withoutImageIn(img.V4, fwstate.V4), V6: p.withoutImageIn(img.V6, fwstate.V6)}
}

func (p kfwPlan) withoutImageIn(tables []imageTable, fam fwstate.Family) []imageTable {
	v6 := fam == fwstate.V6
	known := map[string]bool{"-j RETURN": true}
	for _, r := range p.block[fam] {
		known[netfilter.Key(r, v6)] = true
	}
	out := slices.Clone(tables)
	for i := range out {
		if out[i].Name != "filter" {
			continue
		}
		out[i].Chains = slices.Clone(out[i].Chains)
		for j := range out[i].Chains {
			if c := &out[i].Chains[j]; c.Name == dockerfw.ChainDockerUser {
				c.Rules = slices.DeleteFunc(slices.Clone(c.Rules), func(r imageRule) bool { return !known[r.Key] })
			}
		}
	}
	return out
}

// order returns the original order of the DOCKER-USER chain of each family
// in which a foreign rule sits between the block's lines.
func (p kfwPlan) order() []string {
	layouts := map[fwstate.Family]dockerfw.Layout{}
	for _, fam := range preserveFamilies {
		layouts[fam] = dockerfw.LayoutOf(cleanupUserChain(p.before, fam), p.block[fam], fam)
	}
	return kfwOrderOf(layouts)
}

// kfwOrderOf lists the Order entries of the interleaved layouts, each behind
// its family, IPv4 first.
func kfwOrderOf(layouts map[fwstate.Family]dockerfw.Layout) []string {
	var out []string
	for _, fam := range preserveFamilies {
		if l := layouts[fam]; l.Interleaved {
			for _, line := range l.Order {
				out = append(out, string(fam)+" "+line)
			}
		}
	}
	return out
}

// interleavedError reports foreign DOCKER-USER rules that were moved ahead
// of the block, with the order they had.
func interleavedError(order []string) error {
	return fmt.Errorf("firewall: foreign DOCKER-USER rules sat between the block's lines and were re-inserted before its first line; their original order was: %s",
		strings.Join(order, "; "))
}

// finishRecorded completes owner's reload of DOCKER-USER when a cut-off
// attempt left its record, and returns the original order of the
// interleaved chains the record held. A record that fails to load stays
// for the caller's own keepForeign, which merges it.
func (f fw) finishRecorded(ctx context.Context, env core.Env, owner string) ([]string, error) {
	rec, ok, err := foreignOf(ctx, env.DB, owner)
	if err != nil || !ok {
		return nil, err
	}
	if _, err := f.finishForeign(ctx, env, owner); err != nil {
		return kfwOrderOf(rec.Layouts), fmt.Errorf("firewall: the foreign DOCKER-USER rules recorded for %s are not loaded: %w", owner, err)
	}
	return kfwOrderOf(rec.Layouts), nil
}

// adoptRevertRecord moves the foreign DOCKER-USER rules a failed revert of
// env's change left recorded into owner's record, and reports whether owner
// holds a record afterwards, whose rules its keepForeign then loads.
func (f fw) adoptRevertRecord(ctx context.Context, env core.Env, owner string) (bool, error) {
	if env.ChangeID == "" {
		_, held, err := foreignOf(ctx, env.DB, owner)
		return held, err
	}
	return adoptForeign(ctx, env.DB, "revert:"+env.ChangeID, owner, env.Clock.Now())
}

// finishRecordedLogged is finishRecorded for a caller that goes on to its
// own keepForeign, which merges a record that failed to load: the failure
// is only logged.
func (f fw) finishRecordedLogged(ctx context.Context, env core.Env, owner string) []string {
	order, err := f.finishRecorded(ctx, env, owner)
	if err != nil {
		env.Log.Warn("firewall: cannot load the foreign DOCKER-USER rules left by an earlier attempt", "owner", owner, "error", err)
	}
	return order
}

// endChangeCleanup ends the cleanup intent of the change being reverted. The
// revert takes the host back to before the change, so finishCleanups
// repeating that cleanup would strip the block the revert puts back. The
// foreign DOCKER-USER rules a failed attempt of the cleanup flushed are
// loaded back first, where the revert keeps them. A cleanup the revert runs
// itself records its own intent again.
func (f fw) endChangeCleanup(ctx context.Context, env core.Env) error {
	if env.ChangeID == "" {
		return nil
	}
	if _, err := f.finishForeign(ctx, env, env.ChangeID); err != nil {
		return fmt.Errorf("load the foreign DOCKER-USER rules the change's cleanup flushed: %w", err)
	}
	return endCleanup(ctx, env.DB, env.ChangeID)
}

// kfwUndoOrder returns made with each Docker chains effect that found the
// chains loaded moved ahead of the after-file effects directly before it.
//
// A cleanup journals its file writes before its chains effect, so undone in
// plain reverse order the chains effect's reload runs while the files still
// lack the block, and the inverse's own check of the loaded chains fails. A
// chains effect that loaded the block keeps its place because its inverse,
// the cleanup, reads the block from the files.
func kfwUndoOrder(made []core.Effect) []core.Effect {
	out := make([]core.Effect, 0, len(made))
	run := -1
	for _, e := range made {
		var state string
		switch {
		case e.Resource == deResourceChains && beforeValue(e, &state) == nil && state == "loaded":
			if run >= 0 {
				out = slices.Insert(out, run, e)
			} else {
				out = append(out, e)
			}
			run = -1
		case slices.ContainsFunc(cleanupFiles, func(path string) bool { return e.Resource == nounFile+":"+path }):
			if run < 0 {
				run = len(out)
			}
			out = append(out, e)
		default:
			out = append(out, e)
			run = -1
		}
	}
	return out
}

// undoKeepingForeign runs the inverses of the made effects not in
// conflicts, newest first, and for a repair's snapshot then loads its
// before-image, keeping the foreign DOCKER-USER rules that a reload or the
// image flushes.
func (f fw) undoKeepingForeign(ctx context.Context, env core.Env, snap snapshot, made []core.Effect, conflicts map[int]core.EffectOutcome) error {
	var undone []core.Effect
	for _, e := range made {
		if _, ok := conflicts[e.Seq]; !ok {
			undone = append(undone, e)
		}
	}
	body := func() error {
		if err := undoNewestFirst(ctx, env, f.d, snap, kfwUndoOrder(made), conflicts); err != nil {
			return err
		}
		if snap.State.Image == nil {
			return nil
		}
		switch changed, err := imageFilesChanged(f.d, snap); {
		case err != nil:
			return fmt.Errorf("firewall: load the before-image: %w", err)
		case len(changed) > 0:
			return fmt.Errorf("firewall: the before-image was not loaded: %s changed since the snapshot, and loading it would drop their rules from the loaded chains",
				strings.Join(changed, ", "))
		}
		return loadVerifiedImage(ctx, env, f.d, snap)
	}

	// Without the configuration and the loaded state the foreign rules
	// cannot be told from the block's, and the inverses' reload would flush
	// them, so nothing is undone.
	cfg, err := f.d.readConfig(f.d.root)
	if err != nil {
		return fmt.Errorf("firewall: read the configuration to keep foreign DOCKER-USER rules: %w", err)
	}
	cur, err := f.d.readLoaded(ctx, env)
	if err != nil {
		return fmt.Errorf("firewall: read the loaded state to keep foreign DOCKER-USER rules: %w", err)
	}
	after := maps.Clone(cfg.Files)
	enabled, flush := cfg.Enabled, false
	for _, e := range undone {
		noun, ident, err := splitResource(e.Resource)
		if err != nil {
			continue
		}
		switch noun {
		case nounFile:
			if i := slices.IndexFunc(snap.Entries, func(s core.RestoreEntry) bool { return s.Path == ident }); i >= 0 && slices.Contains(cleanupFiles, ident) {
				after[ident] = snap.Entries[i]
			}
		case nounState:
			if e.Resource == resourceEnabled {
				enabled = snap.State.Enabled
			}
		}
		switch noun {
		case nounRule, nounDefault, nounLogging, nounState, nounChains:
			flush = true
		}
	}
	p := kfwPlanOf(cfg.Files, after, enabled, cfg.IPv6, flush, cur)
	owner := "revert:" + env.ChangeID
	// A record an earlier attempt left that failed to load holds rules no
	// longer in the chain, so only keepForeign's merge loads them again.
	_, held, err := foreignOf(ctx, env.DB, owner)
	if err != nil {
		return err
	}
	if !p.keep && !held {
		return body()
	}
	interleaved, err := f.keepForeign(ctx, env, owner, p.known, p.next, body)
	if err != nil {
		// A failed body leaves the flushed foreign rules in this owner's
		// record. A restore of the change's snapshot adopts the record; when
		// none follows, the error says that the rules are not loaded.
		if _, ferr := f.finishRecorded(ctx, env, owner); ferr != nil {
			return errors.Join(err, ferr)
		}
		return err
	}
	if interleaved {
		return interleavedError(p.order())
	}
	return nil
}
