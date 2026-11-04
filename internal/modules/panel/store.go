package panel

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/base32"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"time"
)

//go:embed migrations/controller/*.sql
var storeMigrationsFS embed.FS

// controllerMigrations returns the module's controller-side migrations, the value of
// Manifest.Migrations[core.SideController].
func controllerMigrations() fs.FS {
	sub, err := fs.Sub(storeMigrationsFS, "migrations/controller")
	if err != nil {
		// fs.Sub fails only for an invalid path, and this one is a constant.
		panic(err)
	}
	return sub
}

// storeTimeFormat is the fixed-width form of every stored time, so times sort and compare as text.
const storeTimeFormat = "2006-01-02T15:04:05.000000000Z"

// storeQueryer is a *sql.DB or a *sql.Tx.
type storeQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// GoneSQL is the predicate of a gone exposure over a panel_exposures row aliased e: it is revoked and the change that
// removed it completed. Every other row is live. It serves queries in this module and in internal/controller/backup.
const GoneSQL = `e.state = 'revoked' AND e.removal_change_id <> '' AND EXISTS (SELECT 1 FROM core_changes c WHERE c.id = e.removal_change_id AND c.state = 'completed')`

// exposureRow is one panel_exposures row.
type exposureRow struct {
	ID, Via, Hostname, HostID, Tunnel string
	Subnet                            netip.Prefix
	Gateway, Proxy                    netip.Addr
	Port                              int
	ListenerID, RuleID, State         string
	RemovalChangeID                   string
	CreatedAt, UpdatedAt              time.Time
}

// liveExposure returns the live exposure, false when there is none.
func liveExposure(ctx context.Context, q storeQueryer) (exposureRow, bool, error) {
	var (
		r                      exposureRow
		subnet, gateway, proxy string
		created, updated       string
	)
	err := q.QueryRowContext(ctx, `SELECT e.id, e.via, e.hostname, e.host_id, e.tunnel, e.subnet, e.gateway, e.proxy, e.port,
		e.listener_id, e.rule_id, e.state, e.removal_change_id, e.created_at, e.updated_at
		FROM panel_exposures e WHERE NOT (`+GoneSQL+`) ORDER BY e.created_at, e.id LIMIT 1`).Scan(
		&r.ID, &r.Via, &r.Hostname, &r.HostID, &r.Tunnel, &subnet, &gateway, &proxy, &r.Port,
		&r.ListenerID, &r.RuleID, &r.State, &r.RemovalChangeID, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return exposureRow{}, false, nil
	}
	if err != nil {
		return exposureRow{}, false, fmt.Errorf("panel: read the live exposure: %w", err)
	}
	if r.Subnet, err = netip.ParsePrefix(subnet); err != nil {
		return exposureRow{}, false, fmt.Errorf("panel: exposure %s subnet: %w", r.ID, err)
	}
	if r.Gateway, err = netip.ParseAddr(gateway); err != nil {
		return exposureRow{}, false, fmt.Errorf("panel: exposure %s gateway: %w", r.ID, err)
	}
	if r.Proxy, err = netip.ParseAddr(proxy); err != nil {
		return exposureRow{}, false, fmt.Errorf("panel: exposure %s proxy: %w", r.ID, err)
	}
	if r.CreatedAt, err = time.Parse(storeTimeFormat, created); err != nil {
		return exposureRow{}, false, fmt.Errorf("panel: exposure %s created_at: %w", r.ID, err)
	}
	if r.UpdatedAt, err = time.Parse(storeTimeFormat, updated); err != nil {
		return exposureRow{}, false, fmt.Errorf("panel: exposure %s updated_at: %w", r.ID, err)
	}
	return r, true, nil
}

// insertExposure stores r.
func insertExposure(ctx context.Context, tx *sql.Tx, r exposureRow) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO panel_exposures (id, via, hostname, host_id, tunnel, subnet, gateway, proxy, port,
		listener_id, rule_id, state, removal_change_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Via, r.Hostname, r.HostID, r.Tunnel, r.Subnet.String(), r.Gateway.String(), r.Proxy.String(), r.Port,
		r.ListenerID, r.RuleID, r.State, r.RemovalChangeID,
		r.CreatedAt.UTC().Format(storeTimeFormat), r.UpdatedAt.UTC().Format(storeTimeFormat)); err != nil {
		return fmt.Errorf("panel: insert exposure %s: %w", r.ID, err)
	}
	return nil
}

// setExposureState sets the state and the update time of exposure id, and its removal change id unless
// removalChangeID is empty.
func setExposureState(ctx context.Context, tx *sql.Tx, id, state, removalChangeID string, at time.Time) error {
	res, err := tx.ExecContext(ctx, `UPDATE panel_exposures SET state = ?, updated_at = ?,
		removal_change_id = CASE WHEN ? = '' THEN removal_change_id ELSE ? END WHERE id = ?`,
		state, at.UTC().Format(storeTimeFormat), removalChangeID, removalChangeID, id)
	if err != nil {
		return fmt.Errorf("panel: set exposure %s state: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("panel: set exposure %s state: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("panel: exposure %s does not exist", id)
	}
	return nil
}

// deleteExposure deletes exposure id. There being none is not an error: the undo of a listener open may run twice.
func deleteExposure(ctx context.Context, tx *sql.Tx, id string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM panel_exposures WHERE id = ?`, id); err != nil {
		return fmt.Errorf("panel: delete exposure %s: %w", id, err)
	}
	return nil
}

// view is the exposure as the API shows it.
func (r exposureRow) view() ExposureView {
	return ExposureView{
		ID: r.ID, Via: r.Via, Hostname: r.Hostname, HostID: r.HostID, Tunnel: r.Tunnel, Subnet: r.Subnet,
		Gateway: r.Gateway, Proxy: r.Proxy, Port: r.Port, State: r.State, ListenerID: r.ListenerID, CreatedAt: r.CreatedAt,
	}
}

var storeCrockford = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// RuleID is the firewall rule id of exposure id: "0" + the first 25 Crockford base-32 characters of
// sha256("darkpanel/panel/" + id), the form of derp/server.RuleID.
func RuleID(exposureID string) string {
	sum := sha256.Sum256([]byte("darkpanel/panel/" + exposureID))
	return "0" + storeCrockford.EncodeToString(sum[:])[:25]
}
