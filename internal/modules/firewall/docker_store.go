package firewall

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
)

// dockerConfig is the Docker integration's stored configuration, the one
// firewall_settings row.
type dockerConfig struct {
	Enabled  bool              `json:"enabled"`
	Settings dockerfw.Settings `json:"settings"`
}

// readDockerConfig returns the stored Docker configuration; with no row it
// returns the integration off with dockerfw.DefaultSettings.
func readDockerConfig(ctx context.Context, db *store.DB) (dockerConfig, error) {
	var c dockerConfig
	var settings string
	err := db.Read().QueryRowContext(ctx, "SELECT docker, settings FROM firewall_settings WHERE id = 1").Scan(&c.Enabled, &settings)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return dockerConfig{Settings: dockerfw.DefaultSettings()}, nil
	case err != nil:
		return dockerConfig{}, fmt.Errorf("firewall: read the Docker settings: %w", err)
	}
	if err := json.Unmarshal([]byte(settings), &c.Settings); err != nil {
		return dockerConfig{}, fmt.Errorf("firewall: decode the Docker settings: %w", err)
	}
	return c, nil
}

// writeDockerConfig stores c as the one firewall_settings row, on tx.
func writeDockerConfig(ctx context.Context, tx *sql.Tx, c dockerConfig) error {
	settings, err := json.Marshal(c.Settings)
	if err != nil {
		return fmt.Errorf("firewall: encode the Docker settings: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO firewall_settings (id, docker, settings) VALUES (1, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET docker = excluded.docker, settings = excluded.settings`,
		c.Enabled, string(settings)); err != nil {
		return fmt.Errorf("firewall: write the Docker settings: %w", err)
	}
	return nil
}

const dsRuleColumns = "id, selector, host_addr, host_port, proto, kind, source"

// dsScanRule reads dsRuleColumns into a container rule; an empty host_addr
// or source is the zero address or prefix.
func dsScanRule(row rowScanner) (dockerfw.ContainerRule, error) {
	var r dockerfw.ContainerRule
	var host, kind, source string
	if err := row.Scan(&r.ID, &r.Binding.Selector, &host, &r.Binding.HostPort, &r.Binding.Proto, &kind, &source); err != nil {
		return dockerfw.ContainerRule{}, err
	}
	r.Kind = dockerfw.Kind(kind)
	if host != "" {
		a, err := netip.ParseAddr(host)
		if err != nil {
			return dockerfw.ContainerRule{}, fmt.Errorf("firewall: container rule %s: host address %q: %w", r.ID, host, err)
		}
		r.Binding.HostAddr = a
	}
	if source != "" {
		p, err := netip.ParsePrefix(source)
		if err != nil {
			return dockerfw.ContainerRule{}, fmt.Errorf("firewall: container rule %s: source %q: %w", r.ID, source, err)
		}
		r.From = p
	}
	return r, nil
}

// listContainerRules returns every stored container rule ordered by id.
func listContainerRules(ctx context.Context, db *store.DB) ([]dockerfw.ContainerRule, error) {
	rows, err := db.Read().QueryContext(ctx, "SELECT "+dsRuleColumns+" FROM firewall_container_rules ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("firewall: list container rules: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []dockerfw.ContainerRule
	for rows.Next() {
		r, err := dsScanRule(rows)
		if err != nil {
			return nil, fmt.Errorf("firewall: list container rules: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("firewall: list container rules: %w", err)
	}
	return out, nil
}

// containerRule returns the container rule id; ok is false when there is
// none.
func containerRule(ctx context.Context, db *store.DB, id string) (r dockerfw.ContainerRule, ok bool, err error) {
	r, err = dsScanRule(db.Read().QueryRowContext(ctx, "SELECT "+dsRuleColumns+" FROM firewall_container_rules WHERE id = ?", id))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return dockerfw.ContainerRule{}, false, nil
	case err != nil:
		return dockerfw.ContainerRule{}, false, fmt.Errorf("firewall: read container rule %s: %w", id, err)
	}
	return r, true, nil
}

// insertContainerRule stores r, created at at, on tx. It does not validate
// r; a duplicate id or rule is the table's constraint error.
func insertContainerRule(ctx context.Context, tx *sql.Tx, r dockerfw.ContainerRule, at time.Time) error {
	host, source := "", ""
	if r.Binding.HostAddr.IsValid() {
		host = r.Binding.HostAddr.String()
	}
	if r.From.IsValid() {
		source = r.From.String()
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO firewall_container_rules (id, selector, host_addr, host_port, proto, kind, source, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Binding.Selector, host, r.Binding.HostPort, r.Binding.Proto, string(r.Kind), source, formatSnapshotTime(at)); err != nil {
		return fmt.Errorf("firewall: insert container rule %s: %w", r.ID, err)
	}
	return nil
}

// deleteContainerRule deletes the container rule id on tx. An absent id is
// not an error, so a replayed inverse effect is harmless.
func deleteContainerRule(ctx context.Context, tx *sql.Tx, id string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM firewall_container_rules WHERE id = ?", id); err != nil {
		return fmt.Errorf("firewall: delete container rule %s: %w", id, err)
	}
	return nil
}

// replaceContainerRules replaces every stored container rule with rows, in
// their order and created at at, on tx. On an error the caller rolls tx
// back.
func replaceContainerRules(ctx context.Context, tx *sql.Tx, rows []dockerfw.ContainerRule, at time.Time) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM firewall_container_rules"); err != nil {
		return fmt.Errorf("firewall: clear container rules: %w", err)
	}
	for _, r := range rows {
		if err := insertContainerRule(ctx, tx, r, at); err != nil {
			return err
		}
	}
	return nil
}

// beginCleanup records owner's cleanup intent, started at at, and commits
// it before returning. An owner already recorded keeps its first start
// time.
func beginCleanup(ctx context.Context, db *store.DB, owner string, at time.Time) error {
	err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			"INSERT INTO firewall_docker_cleanups (owner, started_at) VALUES (?, ?) ON CONFLICT (owner) DO NOTHING",
			owner, formatSnapshotTime(at))
		return err
	})
	if err != nil {
		return fmt.Errorf("firewall: record the Docker cleanup of %s: %w", owner, err)
	}
	return nil
}

// endCleanup deletes owner's cleanup intent; an absent owner is not an
// error.
func endCleanup(ctx context.Context, db *store.DB, owner string) error {
	err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM firewall_docker_cleanups WHERE owner = ?", owner)
		return err
	})
	if err != nil {
		return fmt.Errorf("firewall: clear the Docker cleanup of %s: %w", owner, err)
	}
	return nil
}

// pendingCleanups returns the owners of every recorded cleanup intent, the
// oldest first.
func pendingCleanups(ctx context.Context, db *store.DB) ([]string, error) {
	rows, err := db.Read().QueryContext(ctx, "SELECT owner FROM firewall_docker_cleanups ORDER BY started_at, owner")
	if err != nil {
		return nil, fmt.Errorf("firewall: list Docker cleanups: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var owner string
		if err := rows.Scan(&owner); err != nil {
			return nil, fmt.Errorf("firewall: list Docker cleanups: %w", err)
		}
		out = append(out, owner)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("firewall: list Docker cleanups: %w", err)
	}
	return out, nil
}
