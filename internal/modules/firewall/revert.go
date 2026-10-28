package firewall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

// overrideDetail is the emergency restore record of the connectivity
// exception (spec 2.5): RevertOutcome.Detail, which the engine stores as
// core_changes.override_json and shows the operator.
type overrideDetail struct {
	At       time.Time         `json:"at"`
	Snapshot string            `json:"snapshot"`
	Guard    string            `json:"guard"`                    // why the lockout guard failed
	Files    []fileOverwrite   `json:"files,omitempty"`          // state files the restore rewrote
	Chains   []chainOverwrite  `json:"chains,omitempty"`         // loaded chains the restore changed
	Effects  []effectOverwrite `json:"effects,omitempty"`        // effects left unresolved, with what the restore overwrote
	Vanished []string          `json:"vanishedSysctl,omitempty"` // sysctl keys of interfaces that have gone, not restored
}

type fileOverwrite struct {
	Path string `json:"path"`
	Was  string `json:"was"` // fileHash before the restore
	Now  string `json:"now"` // the snapshot's
}

type chainOverwrite struct {
	Family    fwstate.Family `json:"family"`
	Table     string         `json:"table"`
	Chain     string         `json:"chain"`
	PolicyWas string         `json:"policyWas,omitempty"`
	PolicyNow string         `json:"policyNow,omitempty"`
	Removed   []string       `json:"removed,omitempty"` // rule keys the restore removed
	Added     []string       `json:"added,omitempty"`   // rule keys the restore added
	Reordered bool           `json:"reordered,omitempty"`
}

type effectOverwrite struct {
	Seq      int             `json:"seq"`
	Resource string          `json:"resource"`
	Was      json.RawMessage `json:"was,omitempty"` // the observed State the restore overwrote; absent when it could not be observed
}

// revert reverts a firewall change from its snapshot (spec 2.5, revert of
// a change; spec 1.2, one precedence rule), deciding before it writes:
//
//  1. Check: every made effect is observed against its Post; one that
//     differs is a conflict, left in place. An effect already unresolved
//     (or still intended) blocks every write.
//  2. Whole restore, when nothing is unresolved, nothing conflicts and
//     every state file's hash equals the post-apply hash (after a mid-apply
//     crash: the hash expected after the last settled effect; with neither
//     recorded, never).
//  3. Otherwise the inverses of the made effects, newest first, skipping
//     conflicts and stopping at the first failure, then "ufw reload" when
//     a rule, default or logging inverse ran on an active UFW.
//  4. Whenever that did not restore the prior state, the lockout guard on
//     the loaded state now, with the snapshot's guard context. A failing
//     (or unevaluable) guard restores the snapshot whole over everything
//     and returns Override with the emergency restore record; a passing one
//     leaves the host as it is and returns an error when an effect was
//     unresolved or a write failed, else the conflicts.
//  5. Every ending other than reverted pins the snapshot until a repair
//     unpins it (unpinChangeSnapshot).
//
// A restore of this snapshot that a crash cut off is finished first,
// whatever the checks would say: the files are partly the snapshot's.
func revert(ctx context.Context, env core.Env, d deps, snapBytes []byte) (core.RevertOutcome, error) {
	snap, err := getSnapshot(ctx, env.DB, string(snapBytes))
	if err != nil {
		return core.RevertOutcome{}, fmt.Errorf("firewall: revert: %w", err)
	}
	if env.ChangeID != "" {
		if err := attachSnapshot(ctx, env.DB, snap.ID, env.ChangeID); err != nil {
			return core.RevertOutcome{}, fmt.Errorf("firewall: revert: %w", err)
		}
	}
	effects, err := env.Effects.List(ctx)
	if err != nil {
		return core.RevertOutcome{}, fmt.Errorf("firewall: revert: read the effects: %w", err)
	}
	var blocked, made []core.Effect
	for _, e := range effects {
		switch e.State {
		case core.EffectUnresolved, core.EffectIntended:
			blocked = append(blocked, e)
		case core.EffectMade:
			made = append(made, e)
		}
	}
	if rec, pending, err := restoreRecordOf(ctx, env.DB, snap.ID); err != nil {
		return core.RevertOutcome{}, err
	} else if pending {
		return finishRestore(ctx, env, d, snap, rec, blocked, made)
	}

	conflicts := map[int]core.EffectOutcome{}
	for _, e := range made {
		obs, err := observe(ctx, env, d, e)
		switch {
		case err != nil:
			env.Log.Warn("firewall: revert cannot observe an effect", "change", env.ChangeID, "effect", e.Seq, "error", err)
			conflicts[e.Seq] = core.EffectOutcome{Seq: e.Seq, State: core.EffectUnresolved, Reason: core.ReasonUncertain}
		case e.Post == nil || !stateEqual(obs.Current, *e.Post):
			cur := obs.Current
			conflicts[e.Seq] = core.EffectOutcome{Seq: e.Seq, State: core.EffectUnresolved, Reason: core.ReasonConflict, Current: &cur}
		}
	}

	var writeErr error
	if len(blocked) == 0 {
		if len(conflicts) == 0 && hashesAsApplied(d, snap, made) {
			rep, err := restore(ctx, env, d, snap)
			if err == nil {
				logVanished(env, rep.Vanished)
				return core.RevertOutcome{Effects: undoneOutcomes(made, nil)}, nil
			}
			writeErr = err
		} else {
			writeErr = undoNewestFirst(ctx, env, d, snap, made, conflicts)
		}
	}
	if len(blocked) == 0 && len(conflicts) == 0 && writeErr == nil {
		return core.RevertOutcome{Effects: undoneOutcomes(made, nil)}, nil
	}

	pin(ctx, env, snap.ID)
	st, err := d.readLoaded(ctx, env)
	ok, why := false, ""
	if err != nil {
		why = "the loaded state cannot be read: " + err.Error()
	} else if ok, why, err = d.lockout(ctx, env, st, snap.State.Guard); err != nil {
		ok, why = false, "the lockout guard cannot be evaluated: "+err.Error()
	}
	if !ok {
		return emergencyRestore(ctx, env, d, snap, why, blocked, made, conflicts, writeErr)
	}
	if len(blocked) > 0 {
		return core.RevertOutcome{}, fmt.Errorf("firewall: the revert left the host as it is: effects %s are unresolved; resolve them, then repair the change",
			seqList(blocked))
	}
	if writeErr != nil {
		return core.RevertOutcome{}, fmt.Errorf("firewall: the revert stopped: %w", writeErr)
	}
	out := undoneOutcomes(made, conflicts)
	for _, e := range made {
		if c, ok := conflicts[e.Seq]; ok {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b core.EffectOutcome) int { return a.Seq - b.Seq })
	return core.RevertOutcome{Effects: out}, nil
}

// undoneOutcomes reports every made effect not in skip as undone.
func undoneOutcomes(made []core.Effect, skip map[int]core.EffectOutcome) []core.EffectOutcome {
	var out []core.EffectOutcome
	for _, e := range made {
		if _, ok := skip[e.Seq]; !ok {
			out = append(out, core.EffectOutcome{Seq: e.Seq, State: core.EffectUndone})
		}
	}
	return out
}

func seqList(effects []core.Effect) string {
	s := make([]string, len(effects))
	for i, e := range effects {
		s[i] = fmt.Sprintf("%d (%s)", e.Seq, e.Resource)
	}
	return strings.Join(s, ", ")
}

// hashesAsApplied reports whether every state file's hash equals the hash
// recorded right after the change applied, or, without that record (a
// mid-apply crash or a failed apply), the hash expected after the last
// settled effect: the capture hash overlaid, effect by effect, with each
// made effect's post-call hashes. With neither, it is false.
func hashesAsApplied(d deps, snap snapshot, made []core.Effect) bool {
	expected := snap.PostHashes
	if expected == nil {
		expected = expectedHashes(snap, made)
	}
	if expected == nil {
		return false
	}
	cfg, err := d.readConfig(d.root)
	if err != nil {
		return false
	}
	for _, e := range snap.Entries {
		want, ok := expected[e.Path]
		if !ok || fileHash(cfg.Files[e.Path]) != want {
			return false
		}
	}
	return true
}

// expectedHashes returns the state files' hashes after the made effects,
// or nil when no made effect records a file's hash.
func expectedHashes(snap snapshot, made []core.Effect) map[string]string {
	out := make(map[string]string, len(snap.Hashes))
	for p, h := range snap.Hashes {
		out[p] = h
	}
	recorded := false
	for _, e := range made {
		if e.Post == nil {
			continue
		}
		noun, ident, err := splitResource(e.Resource)
		if err != nil {
			continue
		}
		if noun == nounFile {
			var h string
			if json.Unmarshal(e.Post.State, &h) == nil {
				out[ident], recorded = h, true
			}
			continue
		}
		var v ufwVersion
		if len(e.Post.Version) > 0 && json.Unmarshal(e.Post.Version, &v) == nil && len(v.Files) > 0 {
			for p, h := range v.Files {
				out[p] = h
			}
			recorded = true
		}
	}
	if !recorded {
		return nil
	}
	return out
}

// undoNewestFirst runs the inverses of the made effects not in conflicts,
// newest first, stopping at the first failure, then reloads an active UFW
// when a ufw inverse ran.
func undoNewestFirst(ctx context.Context, env core.Env, d deps, snap snapshot, made []core.Effect, conflicts map[int]core.EffectOutcome) error {
	reload := false
	for i := len(made) - 1; i >= 0; i-- {
		e := made[i]
		if _, ok := conflicts[e.Seq]; ok {
			continue
		}
		if err := inverse(ctx, env, d, snap, e); err != nil {
			return err
		}
		reload = reload || ufwEffect(e)
	}
	if reload {
		return reloadIfActive(ctx, env, d)
	}
	return nil
}

// ufwEffect reports whether undoing e runs ufw for a rule, a default or the
// logging level, which a running UFW loads completely only on a reload.
func ufwEffect(e core.Effect) bool {
	noun, _, err := splitResource(e.Resource)
	return err == nil && (noun == nounRule || noun == nounDefault || noun == nounLogging)
}

// reloadIfActive runs "ufw reload" when the configuration says UFW is
// active; on an inactive UFW it would only print that it skips.
func reloadIfActive(ctx context.Context, env core.Env, d deps) error {
	cfg, err := d.readConfig(d.root)
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return nil
	}
	return ufw(ctx, env, transitionArgs(fwstate.TransReload))
}

// emergencyRecord is what an emergency restore keeps in its restore
// record before it writes anything: the override detail as far as it is
// known then (the loaded chains it changed are not), and the outcome of
// every effect, so that finishing a restore a crash cut off returns the
// same override.
type emergencyRecord struct {
	Detail  overrideDetail  `json:"detail"`
	Effects []storedOutcome `json:"effects"`
}

// storedOutcome is a core.EffectOutcome in the restore record.
type storedOutcome struct {
	Seq     int              `json:"seq"`
	State   core.EffectState `json:"state"`
	Reason  string           `json:"reason,omitempty"`
	Current *core.StateDoc   `json:"current,omitempty"`
}

func storeOutcomes(out []core.EffectOutcome) []storedOutcome {
	s := make([]storedOutcome, len(out))
	for i, o := range out {
		s[i] = storedOutcome{Seq: o.Seq, State: o.State, Reason: o.Reason, Current: o.Current}
	}
	return s
}

func (r emergencyRecord) outcomes() []core.EffectOutcome {
	out := make([]core.EffectOutcome, len(r.Effects))
	for i, o := range r.Effects {
		out[i] = core.EffectOutcome{Seq: o.Seq, State: o.State, Reason: o.Reason, Current: o.Current}
	}
	return out
}

// finishRestore finishes a restore of snap that a crash cut off, with the
// transition its record holds. An emergency restore (the record says so)
// ends as the override it was, with the effects and the detail it recorded
// before it wrote, except for the loaded chains it changed: the state
// before the cut-off run is gone. A restore recorded before the record had
// a kind, over an effect that is unresolved, is taken for the connectivity
// exception too, because only that restores whole over one.
func finishRestore(ctx context.Context, env core.Env, d deps, snap snapshot, rec restoreRecord, blocked, made []core.Effect) (core.RevertOutcome, error) {
	rep, err := restore(ctx, env, d, snap)
	if err != nil {
		pin(ctx, env, snap.ID)
		return core.RevertOutcome{}, fmt.Errorf("firewall: finish the interrupted restore of snapshot %s: %w", snap.ID, err)
	}
	logVanished(env, rep.Vanished)
	if rec.Emergency != nil {
		var er emergencyRecord
		readErr := json.Unmarshal(rec.Emergency, &er)
		if readErr == nil {
			pin(ctx, env, snap.ID)
			er.Detail.Vanished = rep.Vanished
			return core.RevertOutcome{Effects: er.outcomes(), Override: true, Detail: mustJSON(er.Detail)}, nil
		}
		env.Log.Warn("firewall: the emergency restore record cannot be read", "snapshot", snap.ID, "error", readErr)
	}
	out := undoneOutcomes(made, nil)
	if len(blocked) == 0 {
		return core.RevertOutcome{Effects: out}, nil
	}
	pin(ctx, env, snap.ID)
	detail := overrideDetail{At: env.Clock.Now().UTC(), Snapshot: snap.ID, Guard: "an interrupted emergency restore was finished", Vanished: rep.Vanished}
	for _, e := range blocked {
		out = append(out, core.EffectOutcome{Seq: e.Seq, State: core.EffectUnresolved, Reason: e.Reason})
		detail.Effects = append(detail.Effects, effectOverwrite{Seq: e.Seq, Resource: e.Resource})
	}
	slices.SortFunc(out, func(a, b core.EffectOutcome) int { return a.Seq - b.Seq })
	return core.RevertOutcome{Effects: out, Override: true, Detail: mustJSON(detail)}, nil
}

// emergencyRestore is the connectivity exception (spec 2.5 step 4): the
// lockout guard failed on the state the revert left, so the snapshot is
// restored whole over unresolved effects, conflicts and manual edits. It
// settles nothing: each unresolved effect and conflict is reported
// unresolved with what the restore overwrote; the other made effects are
// undone.
func emergencyRestore(ctx context.Context, env core.Env, d deps, snap snapshot, why string, blocked, made []core.Effect,
	conflicts map[int]core.EffectOutcome, writeErr error) (core.RevertOutcome, error) {
	if why == "" {
		why = "the lockout guard failed"
	}
	detail := overrideDetail{At: env.Clock.Now().UTC(), Snapshot: snap.ID, Guard: why}
	var out []core.EffectOutcome
	for _, e := range blocked {
		o := core.EffectOutcome{Seq: e.Seq, State: core.EffectUnresolved, Reason: e.Reason}
		ow := effectOverwrite{Seq: e.Seq, Resource: e.Resource}
		if obs, err := observe(ctx, env, d, e); err == nil {
			cur := obs.Current
			o.Current, ow.Was = &cur, cur.State
		}
		out = append(out, o)
		detail.Effects = append(detail.Effects, ow)
	}
	for _, e := range made {
		c, ok := conflicts[e.Seq]
		if !ok {
			out = append(out, core.EffectOutcome{Seq: e.Seq, State: core.EffectUndone})
			continue
		}
		out = append(out, c)
		ow := effectOverwrite{Seq: e.Seq, Resource: e.Resource}
		if c.Current != nil {
			ow.Was = c.Current.State
		}
		detail.Effects = append(detail.Effects, ow)
	}
	slices.SortFunc(out, func(a, b core.EffectOutcome) int { return a.Seq - b.Seq })
	slices.SortFunc(detail.Effects, func(a, b effectOverwrite) int { return a.Seq - b.Seq })

	beforeFiles := map[string]string{}
	if cfg, err := d.readConfig(d.root); err == nil {
		beforeFiles = fileHashes(cfg.Files)
	}
	beforeLoaded, beforeErr := d.readLoaded(ctx, env)
	for _, e := range snap.Entries {
		if was, now := beforeFiles[e.Path], fileHash(e); was != now {
			detail.Files = append(detail.Files, fileOverwrite{Path: e.Path, Was: was, Now: now})
		}
	}
	rep, err := restoreOver(ctx, env, d, snap, mustJSON(emergencyRecord{Detail: detail, Effects: storeOutcomes(out)}))
	if err != nil {
		return core.RevertOutcome{}, fmt.Errorf("firewall: the lockout guard failed (%s) and the emergency restore of snapshot %s failed: %w",
			why, snap.ID, errors.Join(err, writeErr))
	}
	detail.Vanished = rep.Vanished
	logVanished(env, rep.Vanished)
	if afterLoaded, err := d.readLoaded(ctx, env); err == nil && beforeErr == nil {
		for _, df := range d.compare(beforeLoaded, afterLoaded, restoreKeep) {
			detail.Chains = append(detail.Chains, chainOverwrite{Family: df.Family, Table: df.Table, Chain: df.Chain,
				PolicyWas: df.PolicyA, PolicyNow: df.PolicyB, Removed: df.OnlyA, Added: df.OnlyB, Reordered: df.OrderDiffers})
		}
	}
	return core.RevertOutcome{Effects: out, Override: true, Detail: mustJSON(detail)}, nil
}

// pin pins a snapshot whose change will need repair. A failure is only
// logged: retention also keeps the snapshot of every change in
// needs_repair.
func pin(ctx context.Context, env core.Env, id string) {
	if err := setPinned(ctx, env.DB, id, true); err != nil {
		env.Log.Warn("firewall: cannot pin the snapshot of a change that needs repair", "snapshot", id, "error", err)
	}
}

func logVanished(env core.Env, keys []string) {
	if len(keys) > 0 {
		env.Log.Warn("firewall: the restore skipped the sysctl keys of interfaces that have gone", "change", env.ChangeID, "keys", keys)
	}
}

// unpinChangeSnapshot unpins the snapshot of changeID; the repair that
// ends that change's needs_repair calls it when it completes.
func unpinChangeSnapshot(ctx context.Context, env core.Env, changeID string) error {
	id, err := snapshotOfChange(ctx, env.DB, changeID)
	if err != nil {
		return err
	}
	return setPinned(ctx, env.DB, id, false)
}
