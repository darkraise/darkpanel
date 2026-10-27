package firewall

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

// SnapshotList is the output of resource firewall.snapshots (v1): every
// logical snapshot, newest first.
type SnapshotList struct {
	Snapshots []SnapshotInfo `json:"snapshots"`
}

// SnapshotInfo is one logical snapshot (spec 2.5).
type SnapshotInfo struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	ChangeID  string    `json:"changeId,omitempty"` // the change it was taken before; empty until that change applied
	Actor     string    `json:"actor"`
	System    bool      `json:"system"`   // taken for a background task's change
	Pinned    bool      `json:"pinned"`   // kept while its change needs repair
	Baseline  bool      `json:"baseline"` // the host's first snapshot, never pruned
	Enabled   bool      `json:"enabled"`  // UFW's ENABLED in the snapshot
}

// SnapshotCompareInput is the input of firewall.snapshot.compare (v1): two
// snapshots, or one against the host now when to is omitted.
type SnapshotCompareInput struct {
	From string `json:"from" jsonschema:"pattern=^[0-9A-HJKMNP-TV-Z]{26}$" jsonschema_description:"The older snapshot's id."`
	To   string `json:"to,omitempty" jsonschema:"pattern=^[0-9A-HJKMNP-TV-Z]{26}$" jsonschema_description:"The newer snapshot's id; omitted: the host now."`
}

// SnapshotComparison is the output of firewall.snapshot.compare (v1): the
// changes from one state to the other as a plan lists them, and the state
// files whose hashes differ.
type SnapshotComparison struct {
	From  string          `json:"from"`
	To    string          `json:"to"` // a snapshot id, or "now"
	Diff  []core.DiffLine `json:"diff"`
	Files []FileChange    `json:"files"`
}

// FileChange is one state file whose content differs.
type FileChange struct {
	Path string `json:"path"`
	From string `json:"from"` // "sha256:<hex>", or "absent"
	To   string `json:"to"`
}

// readFunc is a read action's handler.
type readFunc[In, Out any] func(ctx context.Context, env core.Env, in In) (Out, error)

// Read runs the handler.
func (r readFunc[In, Out]) Read(ctx context.Context, env core.Env, in In) (Out, error) {
	return r(ctx, env, in)
}

// snapshots reads resource firewall.snapshots.
func (f fw) snapshots(ctx context.Context, env core.Env) (SnapshotList, error) {
	list, err := listSnapshots(ctx, env.DB, snapshotFilter{})
	if err != nil {
		return SnapshotList{}, err
	}
	out := SnapshotList{Snapshots: make([]SnapshotInfo, 0, len(list))}
	for _, s := range list {
		out.Snapshots = append(out.Snapshots, SnapshotInfo{
			ID: s.ID, CreatedAt: s.CreatedAt, ChangeID: s.ChangeID, Actor: s.Actor,
			System: s.System, Pinned: s.Pinned, Baseline: s.Baseline, Enabled: s.State.Enabled,
		})
	}
	return out, nil
}

// snapshotCompare is firewall.snapshot.compare.
func (f fw) snapshotCompare() core.ActionDef {
	return core.DefineRead[SnapshotCompareInput, SnapshotComparison](core.ActionSpec{
		Name: "firewall.snapshot.compare", Summary: "Compare two firewall snapshots, or one with the host now.", Version: 1, Side: core.SideAgent,
	}, readFunc[SnapshotCompareInput, SnapshotComparison](f.compareSnapshots))
}

// compareSnapshots compares two snapshots, or one with the host now.
func (f fw) compareSnapshots(ctx context.Context, env core.Env, in SnapshotCompareInput) (SnapshotComparison, error) {
	from, err := loadSnapshot(ctx, env, in.From, "/from")
	if err != nil {
		return SnapshotComparison{}, err
	}
	a, err := f.snapshotConfig(from)
	if err != nil {
		return SnapshotComparison{}, err
	}
	out := SnapshotComparison{From: from.ID, To: "now"}
	var b fwstate.Config
	var sysctl map[string]string
	if in.To != "" {
		to, err := loadSnapshot(ctx, env, in.To, "/to")
		if err != nil {
			return SnapshotComparison{}, err
		}
		if b, err = f.snapshotConfig(to); err != nil {
			return SnapshotComparison{}, err
		}
		out.To, sysctl = to.ID, to.State.Sysctl
	} else {
		if b, err = f.d.readConfig(f.d.root); err != nil {
			return SnapshotComparison{}, fmt.Errorf("firewall: read the configured state: %w", err)
		}
		st, err := f.d.readLoaded(ctx, env)
		if err != nil {
			return SnapshotComparison{}, fmt.Errorf("firewall: read the loaded state: %w", err)
		}
		sysctl = st.Sysctl
	}
	out.Diff = stateDiff(logical{cfg: a, sysctl: from.State.Sysctl}, logical{cfg: b, sysctl: sysctl})
	if out.Diff == nil {
		out.Diff = []core.DiffLine{}
	}
	out.Files = fileChanges(a.Files, b.Files)
	return out, nil
}

// fileChanges lists the state files whose hashes differ: fwstate.StateFiles
// in order, then any other file either side records.
func fileChanges(a, b map[string]core.RestoreEntry) []FileChange {
	paths := slices.Clone(fwstate.StateFiles)
	for _, m := range []map[string]core.RestoreEntry{a, b} {
		for _, p := range slices.Sorted(maps.Keys(m)) {
			if !slices.Contains(paths, p) {
				paths = append(paths, p)
			}
		}
	}
	out := []FileChange{}
	for _, p := range paths {
		if ha, hb := fileHash(a[p]), fileHash(b[p]); ha != hb {
			out = append(out, FileChange{Path: p, From: ha, To: hb})
		}
	}
	return out
}
