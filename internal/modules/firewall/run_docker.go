package firewall

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/darkraise/darkpanel/internal/core"
)

// runLogical stores a change's Docker integration state, each step a
// journaled effect: the flag, the settings, the container rows it deletes
// and then the ones it inserts, in the change's order.
func (f fw) runLogical(ctx context.Context, env core.Env, c change) error {
	if c.flag != nil {
		if err := runDockerEffect(ctx, env, f.d, flagOp(*c.flag)); err != nil {
			return err
		}
	}
	if c.sources != nil {
		if err := runDockerEffect(ctx, env, f.d, sourcesOp(*c.sources)); err != nil {
			return err
		}
	}
	for _, id := range c.rowsDel {
		row, ok, err := containerRule(ctx, env.DB, id)
		if err != nil {
			return fmt.Errorf("firewall: read container rule %s: %w", id, err)
		}
		if !ok {
			return fmt.Errorf("firewall: container rule %s does not exist", id)
		}
		if err := runDockerEffect(ctx, env, f.d, rowDeleteOp(row)); err != nil {
			return err
		}
	}
	for _, r := range c.rowsAdd {
		if err := runDockerEffect(ctx, env, f.d, rowCreateOp(r)); err != nil {
			return err
		}
	}
	return nil
}

// checkBlockFiles refuses a change that would write a file other than the
// two after files.
func checkBlockFiles(files map[string][]byte) error {
	for p := range files {
		if !slices.Contains(cleanupFiles, p) {
			return fmt.Errorf("firewall: a change writes %s, which is not a block file", p)
		}
	}
	return nil
}

// writeBlockFiles writes the after files of a change whole, each that
// differs from the host's as one effect. A file that exists keeps its mode
// and owner.
func (f fw) writeBlockFiles(ctx context.Context, env core.Env, files map[string][]byte) error {
	for _, p := range slices.Sorted(maps.Keys(files)) {
		cur, err := f.d.readConfig(f.d.root)
		if err != nil {
			return fmt.Errorf("firewall: write %s: %w", p, err)
		}
		live := cur.Files[p]
		want := live
		if !live.Present {
			want = core.RestoreEntry{Mode: 0o640, UID: 0, GID: 0}
		}
		want.Path, want.Data, want.Present = p, files[p], true
		if sameFile(live, want) {
			continue
		}
		if err := writeFileEffect(ctx, env, f.d, want); err != nil {
			return err
		}
	}
	return nil
}
