package firewall

import (
	"context"
	"fmt"
	"slices"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
)

// dockerRestoreSide is the Docker integration's logical state on one side of
// a restore: its flag and settings, and its container rules.
type dockerRestoreSide struct {
	cfg  dockerConfig
	rows []dockerfw.ContainerRule
}

// readDockerRestoreSide reads the stored Docker state, rows in id order.
func readDockerRestoreSide(ctx context.Context, db *store.DB) (dockerRestoreSide, error) {
	cfg, err := readDockerConfig(ctx, db)
	if err != nil {
		return dockerRestoreSide{}, fmt.Errorf("firewall: read the Docker state: %w", err)
	}
	rows, err := listContainerRules(ctx, db)
	if err != nil {
		return dockerRestoreSide{}, fmt.Errorf("firewall: read the Docker state: %w", err)
	}
	return dockerRestoreSide{cfg: cfg, rows: rows}, nil
}

// snapshotDockerSide is the Docker state snapshot s records. A snapshot
// taken before the integration existed holds no settings and restores the
// default ones.
func snapshotDockerSide(s snapshot) dockerRestoreSide {
	cfg := dockerConfig{Enabled: s.State.Docker, Settings: dockerfw.DefaultSettings()}
	if p := s.State.DockerSettings; p != nil {
		cfg.Settings = *p
	}
	return dockerRestoreSide{cfg: cfg, rows: s.State.ContainerRules}
}

// withDockerRestore returns c with the flag, settings and container-row
// changes that bring the Docker state from now to want, and every other
// field of c as it was. Rows are told apart by id alone, since a row is
// never edited. It writes no block file and asks for no reload: the
// snapshot's own files carry the block or lack it, and a restore of an
// active UFW reloads anyway.
func withDockerRestore(c change, now, want dockerRestoreSide) change {
	if now.cfg.Enabled != want.cfg.Enabled {
		flag := want.cfg.Enabled
		c.flag = &flag
	}
	if !slices.Equal(now.cfg.Settings.PrivateSources, want.cfg.Settings.PrivateSources) ||
		!slices.Equal(now.cfg.Settings.ForwardExemptions, want.cfg.Settings.ForwardExemptions) {
		s := want.cfg.Settings
		c.sources = &s
	}
	nowIDs := make(map[string]bool, len(now.rows))
	for _, r := range now.rows {
		nowIDs[r.ID] = true
	}
	wantIDs := make(map[string]bool, len(want.rows))
	for _, r := range want.rows {
		wantIDs[r.ID] = true
		if !nowIDs[r.ID] {
			c.rowsAdd = append(c.rowsAdd, r)
		}
	}
	for _, r := range now.rows {
		if !wantIDs[r.ID] {
			c.rowsDel = append(c.rowsDel, r.ID)
		}
	}
	// Only an integration that is on has chains loaded; cleaning up while it
	// stays off would refuse a host whose DOCKER-USER holds the operator's
	// own rules.
	c.cleanup = now.cfg.Enabled && !want.cfg.Enabled
	return c
}

// dockerRestoreChange returns c with the changes that bring the stored
// Docker state to snapshot s's. It refuses a restore after which the
// integration, which is then the snapshot's flag, is on with an incoming
// policy of ACCEPT.
func (f fw) dockerRestoreChange(ctx context.Context, env core.Env, c change, s snapshot) (change, error) {
	want := snapshotDockerSide(s)
	final, err := f.snapshotConfig(s)
	if err != nil {
		return change{}, err
	}
	if err := refuseOpenIncoming(want.cfg.Enabled, final); err != nil {
		return change{}, err
	}
	now, err := readDockerRestoreSide(ctx, env.DB)
	if err != nil {
		return change{}, err
	}
	return withDockerRestore(c, now, want), nil
}
