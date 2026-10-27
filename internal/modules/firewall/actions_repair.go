package firewall

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

// The repair outcomes (spec 1.3, 2.5).
const (
	outcomeRestore = "restore"
	outcomeKeep    = "keep"
)

// RepairInput is the input of firewall.state.repair (v1). Repairing a
// change (darkpanel change repair), outcome restore, the default, returns
// the host to the change's snapshot, and keep brings UFW's loaded state to
// the files the change left. Run on its own, the repair takes no outcome
// and reloads UFW from its configuration.
type RepairInput struct {
	core.Guarded
	Outcome string `json:"outcome,omitempty" jsonschema:"enum=restore,enum=keep" jsonschema_description:"Repairing a change: restore (the default) or keep."`
}

// stateRepair is firewall.state.repair (spec 2.3, 2.5): from the actual
// loaded state to a target, allowed on a host out of sync in UFW's and
// Darkpanel's own chains, with a loaded-state before-image in its snapshot.
// It is every firewall action's RepairedBy, its own included (owner ruling
// R1).
func (f fw) stateRepair() core.ActionDef {
	return defineMutation(core.ActionSpec{Name: repairAction, Summary: "Repair the firewall: bring UFW's loaded state to its target."}, mutation[RepairInput]{
		f:       f,
		image:   true,
		guarded: func(in RepairInput) core.Guarded { return in.Guarded },
		build: func(ctx context.Context, env core.Env, in RepairInput) buildFunc {
			return func(cfg fwstate.Config, _ fwstate.Loaded) (change, error) { return repairChange(ctx, env, in, cfg) }
		},
		check: func(ctx context.Context, env core.Env, in RepairInput, cfg fwstate.Config) error {
			if env.Call.RepairOf == "" {
				return nil
			}
			s, err := repairedSnapshot(ctx, env)
			if err != nil {
				return err
			}
			if in.Outcome == outcomeKeep {
				if paths := changedFiles(cfg, s.PostHashes); len(paths) > 0 {
					return mismatch("%s differ from what change %s left", strings.Join(paths, ", "), env.Call.RepairOf)
				}
				return nil
			}
			if cfg.Enabled != s.State.Enabled {
				return mismatch("UFW's ENABLED is %s after the repair, the snapshot's is %s", yesNo(cfg.Enabled), yesNo(s.State.Enabled))
			}
			return nil
		},
		finish: func(ctx context.Context, env core.Env, in RepairInput, out *ChangeOutput) error {
			if env.Call.RepairOf == "" {
				return nil
			}
			out.RepairOutcome = "restored"
			if in.Outcome == outcomeKeep {
				out.RepairOutcome = "kept"
			}
			if err := unpinChangeSnapshot(ctx, env, env.Call.RepairOf); err != nil {
				env.Log.Warn("firewall: cannot unpin the repaired change's snapshot", "change", env.Call.RepairOf, "error", err)
			}
			return nil
		},
	})
}

// repairChange is the repair's change. Without a repaired change it reloads
// (or stops) UFW from its configuration. Repairing a change, restore brings
// the change's snapshot back, and keep reloads UFW from the files the
// change left, which must still hash as it recorded them right after it
// applied.
func repairChange(ctx context.Context, env core.Env, in RepairInput, cfg fwstate.Config) (change, error) {
	refuse := func(msg string) error {
		return &errcode.Error{Code: errcode.InvalidInput, Detail: msg, Fields: []errcode.FieldError{{Path: "/outcome", Message: msg}}}
	}
	if env.Call.RepairOf == "" {
		if in.Outcome != "" {
			return change{}, refuse("only the repair of a change takes an outcome")
		}
		return change{repair: true}, nil
	}
	s, err := repairedSnapshot(ctx, env)
	if err != nil {
		return change{}, err
	}
	if in.Outcome == "" || in.Outcome == outcomeRestore {
		return change{repair: true, restore: &s}, nil
	}
	if s.PostHashes == nil {
		return change{}, refuse(fmt.Sprintf("change %s recorded no post-apply state; repair it with outcome restore", env.Call.RepairOf))
	}
	if paths := changedFiles(cfg, s.PostHashes); len(paths) > 0 {
		return change{}, refuse(fmt.Sprintf("%s differ from what change %s left; repair it with outcome restore",
			strings.Join(paths, ", "), env.Call.RepairOf))
	}
	return change{repair: true}, nil
}

// repairedSnapshot is the snapshot of the change the repair repairs.
func repairedSnapshot(ctx context.Context, env core.Env) (snapshot, error) {
	id, err := snapshotOfChange(ctx, env.DB, env.Call.RepairOf)
	if err != nil {
		return snapshot{}, fmt.Errorf("firewall: the snapshot of change %s: %w", env.Call.RepairOf, err)
	}
	return getSnapshot(ctx, env.DB, id)
}

// changedFiles lists the files whose hashes differ from want, sorted.
func changedFiles(cfg fwstate.Config, want map[string]string) []string {
	var out []string
	for _, p := range slices.Sorted(maps.Keys(want)) {
		if fileHash(cfg.Files[p]) != want[p] {
			out = append(out, p)
		}
	}
	return out
}
