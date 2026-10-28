package firewall

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
)

// restoreDockerState writes snapshot snap's Docker flag, settings and
// container rules on tx, replacing every stored row; restored rows are
// created at at. A snapshot taken before the integration existed holds no
// settings and no rows, and restores the integration off with the default
// settings. The rows are policy by binding, so they are restored as they
// were and reconcile replays them against the containers running now; no
// container address is ever restored.
func restoreDockerState(ctx context.Context, tx *sql.Tx, snap snapshot, at time.Time) error {
	cfg := dockerConfig{Enabled: snap.State.Docker, Settings: dockerfw.DefaultSettings()}
	if s := snap.State.DockerSettings; s != nil {
		cfg.Settings = *s
	}
	if err := writeDockerConfig(ctx, tx, cfg); err != nil {
		return err
	}
	return replaceContainerRules(ctx, tx, snap.State.ContainerRules, at)
}

// writeDockerState is restoreDockerState in a transaction of its own, for a
// restore whose record already exists: the record may predate the rows, so
// a retry writes them again, which changes nothing when they are there.
func writeDockerState(ctx context.Context, db *store.DB, snap snapshot, at time.Time) error {
	err := db.Write(ctx, func(tx *sql.Tx) error {
		return restoreDockerState(ctx, tx, snap, at)
	})
	if err != nil {
		return fmt.Errorf("firewall: restore the Docker state of snapshot %s: %w", snap.ID, err)
	}
	return nil
}
