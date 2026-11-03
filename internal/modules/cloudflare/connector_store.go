package cloudflare

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"time"
)

// agentMigrations returns the module's agent-side migrations, the value of
// Manifest.Migrations[core.SideAgent].
func agentMigrations() fs.FS {
	sub, err := fs.Sub(storeMigrationsFS, "migrations/agent")
	if err != nil {
		// fs.Sub fails only for an invalid path, and this one is a constant.
		panic(err)
	}
	return sub
}

// connectorSealAD is the associated data a connector's tunnel token is sealed
// with, which binds the ciphertext to its tunnel.
func connectorSealAD(tunnel string) string { return "cloudflare_connectors.token_ct." + tunnel }

// connectorRow is one cloudflare_connectors row.
type connectorRow struct {
	TunnelID, ContainerID, ChangeID string
	TokenCT                         []byte // the tunnel token sealed with connectorSealAD(TunnelID)
	CreatedAt                       time.Time
}

// claimRow is one cloudflare_attachments row: a claimant's need for the
// tunnel's connector to be on a network.
type claimRow struct {
	TunnelID, Network, Claimant, ChangeID string
	Preexisting                           bool // the connector was on the network before the first claim
}

const connectorColumns = `tunnel_id, container_id, token_ct, change_id, created_at`

// scanConnector decodes one cloudflare_connectors row, its columns selected
// as connectorColumns, through scan, a *sql.Row's or a *sql.Rows' Scan method.
func scanConnector(scan func(dest ...any) error) (connectorRow, error) {
	var (
		r       connectorRow
		created string
	)
	if err := scan(&r.TunnelID, &r.ContainerID, &r.TokenCT, &r.ChangeID, &created); err != nil {
		return connectorRow{}, err
	}
	var err error
	if r.CreatedAt, err = storeParseTime(created); err != nil {
		return connectorRow{}, err
	}
	return r, nil
}

// loadConnector loads the connector of the tunnel; ok is false when there is
// none.
func loadConnector(ctx context.Context, q queryer, tunnelID string) (connectorRow, bool, error) {
	r, err := scanConnector(q.QueryRowContext(ctx, `SELECT `+connectorColumns+` FROM cloudflare_connectors
		WHERE tunnel_id = ?`, tunnelID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return connectorRow{}, false, nil
	}
	if err != nil {
		return connectorRow{}, false, fmt.Errorf("cloudflare: read the connector of tunnel %s: %w", tunnelID, err)
	}
	return r, true, nil
}

// connectorByContainer loads the connector whose container has the id; ok is
// false when there is none.
func connectorByContainer(ctx context.Context, q queryer, containerID string) (connectorRow, bool, error) {
	r, err := scanConnector(q.QueryRowContext(ctx, `SELECT `+connectorColumns+` FROM cloudflare_connectors
		WHERE container_id = ?`, containerID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return connectorRow{}, false, nil
	}
	if err != nil {
		return connectorRow{}, false, fmt.Errorf("cloudflare: read the connector of container %s: %w", containerID, err)
	}
	return r, true, nil
}

// listConnectors returns every connector, ordered by tunnel id.
func listConnectors(ctx context.Context, q queryer) ([]connectorRow, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+connectorColumns+` FROM cloudflare_connectors ORDER BY tunnel_id`)
	if err != nil {
		return nil, fmt.Errorf("cloudflare: list connectors: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []connectorRow{}
	for rows.Next() {
		r, err := scanConnector(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("cloudflare: list connectors: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cloudflare: list connectors: %w", err)
	}
	return out, nil
}

// insertConnector inserts r. It fails when the tunnel has a connector row
// already.
func insertConnector(ctx context.Context, tx *sql.Tx, r connectorRow) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO cloudflare_connectors (`+connectorColumns+`) VALUES (?, ?, ?, ?, ?)`,
		r.TunnelID, r.ContainerID, r.TokenCT, r.ChangeID, storeFormatTime(r.CreatedAt)); err != nil {
		return fmt.Errorf("cloudflare: insert the connector of tunnel %s: %w", r.TunnelID, err)
	}
	return nil
}

// deleteConnector deletes the connector of the tunnel. No such row is not an
// error, so an inverse that runs twice succeeds.
func deleteConnector(ctx context.Context, tx *sql.Tx, tunnelID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM cloudflare_connectors WHERE tunnel_id = ?`, tunnelID); err != nil {
		return fmt.Errorf("cloudflare: delete the connector of tunnel %s: %w", tunnelID, err)
	}
	return nil
}

// connectorClaims returns the claims on the tunnel's connector, ordered by
// network then claimant; network "" selects the claims on every network.
func connectorClaims(ctx context.Context, q queryer, tunnelID, network string) ([]claimRow, error) {
	rows, err := q.QueryContext(ctx, `SELECT tunnel_id, network, claimant, change_id, preexisting FROM cloudflare_attachments
		WHERE tunnel_id = ?1 AND (?2 = '' OR network = ?2) ORDER BY network, claimant`, tunnelID, network)
	if err != nil {
		return nil, fmt.Errorf("cloudflare: list the claims of tunnel %s: %w", tunnelID, err)
	}
	defer func() { _ = rows.Close() }()
	out := []claimRow{}
	for rows.Next() {
		var c claimRow
		if err := rows.Scan(&c.TunnelID, &c.Network, &c.Claimant, &c.ChangeID, &c.Preexisting); err != nil {
			return nil, fmt.Errorf("cloudflare: list the claims of tunnel %s: %w", tunnelID, err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cloudflare: list the claims of tunnel %s: %w", tunnelID, err)
	}
	return out, nil
}

// insertClaim inserts c. It fails when the claimant holds a claim on the
// network for the tunnel already.
func insertClaim(ctx context.Context, tx *sql.Tx, c claimRow) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO cloudflare_attachments (tunnel_id, network, claimant, change_id, preexisting)
		VALUES (?, ?, ?, ?, ?)`, c.TunnelID, c.Network, c.Claimant, c.ChangeID, c.Preexisting); err != nil {
		return fmt.Errorf("cloudflare: claim of %s on %s for tunnel %s: %w", c.Claimant, c.Network, c.TunnelID, err)
	}
	return nil
}

// deleteClaim deletes the claimant's claim on the network for the tunnel. No
// such row is not an error, so an inverse that runs twice succeeds.
func deleteClaim(ctx context.Context, tx *sql.Tx, tunnelID, network, claimant string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM cloudflare_attachments WHERE tunnel_id = ? AND network = ? AND claimant = ?`,
		tunnelID, network, claimant); err != nil {
		return fmt.Errorf("cloudflare: claim of %s on %s for tunnel %s: %w", claimant, network, tunnelID, err)
	}
	return nil
}
