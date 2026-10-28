package firewall

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

// snapshotEntries returns the files a snapshot holds (spec 2.5): every
// state file in fwstate.StateFiles order, then UFW's sysctl file when the
// configuration records it.
func snapshotEntries(cfg fwstate.Config) ([]core.RestoreEntry, error) {
	var out []core.RestoreEntry
	for _, p := range fwstate.StateFiles {
		e, ok := cfg.Files[p]
		if !ok {
			return nil, fmt.Errorf("firewall: the configuration does not record %s", p)
		}
		out = append(out, e)
	}
	for _, p := range slices.Sorted(maps.Keys(cfg.Files)) {
		if slices.Contains(fwstate.StateFiles, p) {
			continue
		}
		if !strings.HasPrefix(p, "/etc/ufw/") {
			return nil, fmt.Errorf("firewall: the configuration records %s, which is not a UFW file", p)
		}
		out = append(out, cfg.Files[p])
	}
	return out, nil
}

// fileHashes returns fileHash of every file, by path.
func fileHashes(files map[string]core.RestoreEntry) map[string]string {
	out := make(map[string]string, len(files))
	for p, e := range files {
		out[p] = fileHash(e)
	}
	return out
}

// systemActor reports whether actor is one of the firewall's background
// tasks (actor system:<task id>, task ids firewall.<name>), whose changes
// count against the system snapshots' retention.
func systemActor(actor string) bool { return strings.HasPrefix(actor, "system:"+ID+".") }

// capture takes the firewall's logical snapshot before a change (spec 2.5)
// and returns the snapshot bytes the engine stores: the snapshot's id. The
// engine calls it before the change exists, so the snapshot is attached
// later (attach). gc is the guard context of the change's plan, kept for
// the connectivity exception of a revert; withImage also takes a repair's
// loaded-state before-image (spec 2.3). The host's first snapshot becomes
// its baseline, and retention prunes in the same transaction.
func capture(ctx context.Context, env core.Env, d deps, gc *core.GuardContext, withImage bool) ([]byte, error) {
	cfg, err := d.readConfig(d.root)
	if err != nil {
		return nil, fmt.Errorf("firewall: snapshot: %w", err)
	}
	entries, err := snapshotEntries(cfg)
	if err != nil {
		return nil, fmt.Errorf("firewall: snapshot: %w", err)
	}
	st, err := d.readLoaded(ctx, env)
	if err != nil {
		return nil, fmt.Errorf("firewall: snapshot: %w", err)
	}
	// Without the Docker state nothing is saved: a snapshot missing the rows
	// would delete them when restored.
	dc, err := readDockerConfig(ctx, env.DB)
	if err != nil {
		return nil, fmt.Errorf("firewall: snapshot: read the Docker settings: %w", err)
	}
	rows, err := listContainerRules(ctx, env.DB)
	if err != nil {
		return nil, fmt.Errorf("firewall: snapshot: read the container rules: %w", err)
	}
	state := snapshotState{
		Enabled:        cfg.Enabled,
		Sysctl:         maps.Clone(st.Sysctl),
		Guard:          gc,
		Docker:         dc.Enabled,
		DockerSettings: &dc.Settings,
		ContainerRules: rows,
	}
	if state.Sysctl == nil {
		state.Sysctl = map[string]string{}
	}
	if withImage {
		state.Image = takeImage(st)
	}
	now := env.Clock.Now()
	s := snapshot{
		ID:        ulid.MustNew(ulid.Timestamp(now), rand.Reader).String(),
		CreatedAt: now,
		Actor:     env.Principal.Actor,
		System:    systemActor(env.Principal.Actor),
		Entries:   entries,
		State:     state,
		Hashes:    fileHashes(cfg.Files),
	}
	if _, err := saveSnapshot(ctx, env.DB, s); err != nil {
		return nil, err
	}
	return []byte(s.ID), nil
}

// attach records env.ChangeID on the snapshot captured for it and returns
// the snapshot's id; Apply calls it first. It is idempotent.
func attach(ctx context.Context, env core.Env) (string, error) {
	if env.ChangeID == "" {
		return "", errors.New("firewall: attach needs the change id")
	}
	return attachNewest(ctx, env.DB, env.ChangeID)
}

// recordPostHashes records on snapshot id the hash of every UFW file as it
// stands right after the change applied (spec 2.5); Apply calls it last.
func recordPostHashes(ctx context.Context, env core.Env, d deps, id string) error {
	cfg, err := d.readConfig(d.root)
	if err != nil {
		return fmt.Errorf("firewall: post-apply hashes: %w", err)
	}
	return setPostHashes(ctx, env.DB, id, fileHashes(cfg.Files))
}
