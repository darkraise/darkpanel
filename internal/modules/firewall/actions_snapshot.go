package firewall

import (
	"context"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

// SnapshotRestoreInput is the input of firewall.snapshot.restore (v1).
type SnapshotRestoreInput struct {
	core.Guarded
	ID     string `json:"id" jsonschema:"pattern=^[0-9A-HJKMNP-TV-Z]{26}$" jsonschema_description:"The snapshot's id, as firewall.snapshots lists it."`
	Manual bool   `json:"manual,omitempty" jsonschema_description:"Confirms the manual rules the restore adds or removes (spec 2.5)."`
}

// loadSnapshot reads snapshot id; an unknown id is invalid input at path.
func loadSnapshot(ctx context.Context, env core.Env, id, path string) (snapshot, error) {
	s, err := getSnapshot(ctx, env.DB, id)
	if errors.Is(err, errSnapshotNotFound) {
		return snapshot{}, &errcode.Error{Code: errcode.InvalidInput, Detail: fmt.Sprintf("no snapshot has the id %s", id),
			Fields: []errcode.FieldError{{Path: path, Message: "no snapshot has this id"}}}
	}
	return s, err
}

// snapshotRestore is firewall.snapshot.restore: bring a snapshot's files,
// UFW state and sysctl values back (spec 2.5), as journaled effects, so the
// restore itself is revertible from its own snapshot.
func (f fw) snapshotRestore() core.ActionDef {
	return defineMutation(core.ActionSpec{Name: "firewall.snapshot.restore", Summary: "Restore a firewall snapshot."}, mutation[SnapshotRestoreInput]{
		f:       f,
		guarded: func(in SnapshotRestoreInput) core.Guarded { return in.Guarded },
		manual:  func(in SnapshotRestoreInput) bool { return in.Manual },
		keep:    func(in SnapshotRestoreInput) string { return in.ID },
		build: func(ctx context.Context, env core.Env, in SnapshotRestoreInput) buildFunc {
			return func(fwstate.Config, fwstate.Loaded) (change, error) {
				s, err := loadSnapshot(ctx, env, in.ID, "/id")
				if err != nil {
					return change{}, err
				}
				return f.dockerRestoreChange(ctx, env, change{restore: &s}, s)
			}
		},
		check: func(ctx context.Context, env core.Env, in SnapshotRestoreInput, cfg fwstate.Config) error {
			s, err := getSnapshot(ctx, env.DB, in.ID)
			if err != nil {
				return err
			}
			if cfg.Enabled != s.State.Enabled {
				return mismatch("UFW's ENABLED is %s after the restore, the snapshot's is %s", yesNo(cfg.Enabled), yesNo(s.State.Enabled))
			}
			return nil
		},
	})
}
