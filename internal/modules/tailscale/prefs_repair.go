package tailscale

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailscale/link"
)

// prefsRepair is tailscale.prefs.repair: it settles a tailscale.prefs.set whose revert failed, or a repair of one,
// by restoring the preferences the change found or keeping the ones it set. It never edits a field the change did
// not set.
func (t ts) prefsRepair() core.ActionDef {
	return core.DefineRevertible[PrefsRepairInput, PrefsRepaired](core.ActionSpec{
		Name: ID + ".prefs.repair", Summary: "Repair a change to the node's Tailscale preferences.",
		Version: 1, Side: core.SideAgent, LockKeys: []string{lockKey}, RepairedBy: ID + ".prefs.repair",
		Guards:    []core.GuardSpec{{ID: link.GuardID, Overridable: true}},
		Available: t.available,
	}, prefsRepairHandler{t: t})
}

// prefsRepairHandler is prefs.repair's handler. It keeps prefs.set's snapshot and effect shapes, so an unconfirmed
// repair reverts and is observed exactly as a prefs.set is.
type prefsRepairHandler struct{ t ts }

func (h prefsRepairHandler) set() prefsSetHandler { return prefsSetHandler(h) }

func (h prefsRepairHandler) Plan(ctx context.Context, env core.Env, in PrefsRepairInput) (core.PlanOutcome, error) {
	_, target, err := prefsRepairResolve(ctx, env, in.Outcome)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if target, err = prefsSetResolve(ctx, h.t.d.local(), target); err != nil {
		return core.PlanOutcome{}, err
	}
	lp, raw, err := h.t.currentLinkPrefs(ctx)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	all := prefsFieldsOf(raw)
	cur := prefsSetPick(all, target)
	g, err := h.t.linkGuard(ctx, env, in.Guarded, lp, link.Change{Kind: "prefs", After: prefsSetLinkAfter(lp, all, target)})
	if err != nil {
		return core.PlanOutcome{}, err
	}
	sum := sha256.Sum256(prefsSetState(cur).State)
	return core.PlanOutcome{Diff: prefsRepairLines(cur, target), Guards: []core.Guard{g}, StateHash: hex.EncodeToString(sum[:])}, nil
}

// Snapshot uses the target only as a field mask, so it does not resolve an exit node address.
func (h prefsRepairHandler) Snapshot(ctx context.Context, env core.Env, in PrefsRepairInput) ([]byte, error) {
	_, target, err := prefsRepairResolve(ctx, env, in.Outcome)
	if err != nil {
		return nil, err
	}
	p, err := h.set().read(ctx)
	if err != nil {
		return nil, err
	}
	return prefsSetState(prefsSetPick(prefsFieldsOf(p), target)).State, nil
}

// Apply journals the effect before the edit, so a lost answer leaves an intended effect for the engine to settle.
func (h prefsRepairHandler) Apply(ctx context.Context, env core.Env, in PrefsRepairInput) (PrefsRepaired, error) {
	kept, target, err := prefsRepairResolve(ctx, env, in.Outcome)
	if err != nil {
		return PrefsRepaired{}, err
	}
	if target, err = prefsSetResolve(ctx, h.t.d.local(), target); err != nil {
		return PrefsRepaired{}, core.Definite(err)
	}
	p, err := h.set().read(ctx)
	if err != nil {
		return PrefsRepaired{}, err
	}
	patch, err := prefsSetPatch(p, target)
	if err != nil {
		return PrefsRepaired{}, err
	}
	after := prefsSetState(target)
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectModify, Resource: prefsEffectResource, Local: false, CommitWait: prefsCommitWait,
		Before: prefsSetState(prefsSetPick(prefsFieldsOf(p), target)), After: &after,
	}, nil)
	if err != nil {
		return PrefsRepaired{}, err
	}
	returned, err := h.t.d.local().EditPrefs(ctx, patch)
	if err != nil {
		refusal := prefsRepairRefusal(err)
		if !core.IsDefinite(refusal) {
			return PrefsRepaired{}, refusal
		}
		if err := env.Effects.Skipped(ctx, seq); err != nil {
			return PrefsRepaired{}, err
		}
		return PrefsRepaired{}, refusal
	}
	got := prefsSetPick(prefsFieldsOf(returned), target)
	if err := env.Effects.Made(ctx, seq, prefsEffectResource, prefsSetState(got)); err != nil {
		return PrefsRepaired{}, err
	}
	if msg := prefsRepairMismatch(got, target); msg != "" {
		return PrefsRepaired{}, errors.New(msg)
	}
	outcome := "restored"
	if kept {
		outcome = "kept"
	}
	return PrefsRepaired{RepairOutcome: outcome, Prefs: got}, nil
}

// Verify checks the host from the input and the repaired change alone, as recovery calls it without an output.
func (h prefsRepairHandler) Verify(ctx context.Context, env core.Env, in PrefsRepairInput, _ PrefsRepaired) error {
	_, target, err := prefsRepairResolve(ctx, env, in.Outcome)
	if err != nil {
		return err
	}
	if target, err = prefsSetResolve(ctx, h.t.d.local(), target); err != nil {
		return err
	}
	p, err := h.set().read(ctx)
	if err != nil {
		return err
	}
	if msg := prefsRepairMismatch(prefsSetPick(prefsFieldsOf(p), target), target); msg != "" {
		return errors.New(msg)
	}
	return nil
}

func (h prefsRepairHandler) Revert(ctx context.Context, env core.Env, snapshot []byte) (core.RevertOutcome, error) {
	return h.set().Revert(ctx, env, snapshot)
}

func (h prefsRepairHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	return h.set().Observe(ctx, env, e)
}

// prefsRepairTarget reads change env.Call.RepairOf from core_changes on env.DB.Read()
// (SELECT action, input_json, snapshot_ct FROM core_changes WHERE id = ?) and opens the snapshot with
// env.Sealer.Open("core_changes.snapshot_ct."+id, ct), the associated data the engine seals it with
// (internal/core/lifecycle/apply.go snapshotAD). prior is the decoded snapshot (PrefsFields). For a
// tailscale.prefs.set change, set is prefsSetNormalize of its input's PrefsFields; for a tailscale.prefs.repair
// change, set is the zero PrefsFields (a repair's input names no preferences).
func prefsRepairTarget(ctx context.Context, env core.Env) (action string, prior, set PrefsFields, err error) {
	id := env.Call.RepairOf
	if id == "" {
		return "", PrefsFields{}, PrefsFields{}, prefsSetInvalid("", ID+".prefs.repair runs only as the repair of a change")
	}
	var input string
	var ct []byte
	err = env.DB.Read().QueryRowContext(ctx, `SELECT action, input_json, snapshot_ct FROM core_changes WHERE id = ?`, id).
		Scan(&action, &input, &ct)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", PrefsFields{}, PrefsFields{}, core.Definite(&errcode.Error{Code: errcode.NotFound, Detail: "change " + id + " not found"})
	case err != nil:
		return "", PrefsFields{}, PrefsFields{}, fmt.Errorf("tailscale: read change %s: %w", id, err)
	}
	// The action is checked before the snapshot is opened: another action's snapshot need not be preferences.
	if action != ID+".prefs.set" && action != ID+".prefs.repair" {
		return "", PrefsFields{}, PrefsFields{}, prefsSetInvalid("", "change "+id+" is a "+action+" change, not a preferences change")
	}
	snapshot, err := env.Sealer.Open("core_changes.snapshot_ct."+id, ct)
	if err != nil {
		return "", PrefsFields{}, PrefsFields{}, fmt.Errorf("tailscale: open the snapshot of change %s: %w", id, err)
	}
	if err := json.Unmarshal(snapshot, &prior); err != nil {
		return "", PrefsFields{}, PrefsFields{}, fmt.Errorf("tailscale: decode the snapshot of change %s: %w", id, err)
	}
	if action == ID+".prefs.set" {
		var in PrefsSetInput
		if err := json.Unmarshal([]byte(input), &in); err != nil {
			return "", PrefsFields{}, PrefsFields{}, fmt.Errorf("tailscale: decode the input of change %s: %w", id, err)
		}
		if set, err = prefsSetNormalize(in.PrefsFields); err != nil {
			return "", PrefsFields{}, PrefsFields{}, fmt.Errorf("tailscale: the input of change %s: %w", id, err)
		}
	}
	return action, prior, set, nil
}

// prefsRepairResolve checks the outcome and the repaired change and returns the preferences the repair sets: the
// change's prior values for restore (the default), the values it set for keep.
func prefsRepairResolve(ctx context.Context, env core.Env, outcome string) (kept bool, target PrefsFields, err error) {
	if env.Call.RepairOf == "" {
		_, _, _, err := prefsRepairTarget(ctx, env)
		return false, PrefsFields{}, err
	}
	switch outcome {
	case "", "restore":
	case "keep":
		kept = true
	default:
		return false, PrefsFields{}, prefsSetInvalid("/outcome", fmt.Sprintf("the outcome %q is neither restore nor keep", outcome))
	}
	action, prior, set, err := prefsRepairTarget(ctx, env)
	if err != nil {
		return false, PrefsFields{}, err
	}
	if !kept {
		return false, prior, nil
	}
	if action == ID+".prefs.repair" {
		return false, PrefsFields{}, prefsSetInvalid("/outcome", "change "+env.Call.RepairOf+" is a repair; repair it with outcome restore")
	}
	return true, set, nil
}

// prefsRepairLines is the diff from cur to target, one entry per field target sets, in the order of the wire shape.
func prefsRepairLines(cur, target PrefsFields) []core.DiffLine {
	have := map[string]prefsSetField{}
	for _, f := range prefsSetFields(cur) {
		have[f.name] = f
	}
	var out []core.DiffLine
	for _, w := range prefsSetFields(target) {
		if c := have[w.name]; c.key == w.key && c.name != "" {
			out = append(out, core.DiffLine{Op: " ", Text: w.name + ": " + w.text + " (unchanged)"})
		} else {
			out = append(out, core.DiffLine{Op: "-", Text: w.name + ": " + c.text}, core.DiffLine{Op: "+", Text: w.name + ": " + w.text})
		}
	}
	return out
}

// prefsRepairMismatch names the first field of want, in the order of the wire shape, that got does not hold, or is
// "" when got holds them all.
func prefsRepairMismatch(got, want PrefsFields) string {
	have := map[string]prefsSetField{}
	for _, f := range prefsSetFields(got) {
		have[f.name] = f
	}
	for _, w := range prefsSetFields(want) {
		if g := have[w.name]; g.key != w.key || g.name == "" {
			return fmt.Sprintf("tailscale: %s is %s after the repair, want %s", w.name, g.text, w.text)
		}
	}
	return ""
}

// prefsRepairRefusal classifies a failed edit as prefs.set does: definite only when tailscaled committed nothing.
func prefsRepairRefusal(err error) error { return prefsSetRefusal(err) }
