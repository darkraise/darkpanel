package cloudflare

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
)

//go:embed migrations/controller/*.sql
var storeMigrationsFS embed.FS

// controllerMigrations returns the module's controller-side migrations, the
// value of Manifest.Migrations[core.SideController].
func controllerMigrations() fs.FS {
	sub, err := fs.Sub(storeMigrationsFS, "migrations/controller")
	if err != nil {
		// fs.Sub fails only for an invalid path, and this one is a constant.
		panic(err)
	}
	return sub
}

// storeTimeFormat is the fixed-width form of every stored time, so times
// sort and compare as text.
const storeTimeFormat = "2006-01-02T15:04:05.000000000Z"

func storeFormatTime(t time.Time) string { return t.UTC().Format(storeTimeFormat) }

// storeNullTime is the value of a nullable time column: NULL for the zero
// time.
func storeNullTime(t time.Time) sql.NullString {
	if t.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: storeFormatTime(t), Valid: true}
}

func storeParseTime(s string) (time.Time, error) {
	t, err := time.Parse(storeTimeFormat, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("cloudflare: parse stored time: %w", err)
	}
	return t, nil
}

// storeParseNullTime reads a nullable time column: NULL is the zero time.
func storeParseNullTime(s sql.NullString) (time.Time, error) {
	if !s.Valid {
		return time.Time{}, nil
	}
	return storeParseTime(s.String)
}

// queryer is a *sql.DB or a *sql.Tx.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// account is the cloudflare_accounts row.
type account struct {
	ID, AccountID string
	TokenCT       []byte // cloud.SealCredential(s, "cloudflare_accounts", "token_ct", ID, token)
	Capabilities  []cloud.Capability
	ConnectedAt   time.Time
}

// tunnelRow is one cloudflare_tunnels row.
type tunnelRow struct {
	ID, Name, ChangeID, ConfigHash string
	Drift                          bool
	CreatedAt, CheckedAt           time.Time
}

// hostnameRow is one cloudflare_hostnames row.
type hostnameRow struct {
	Hostname, TunnelID, Service, ZoneID, RecordID, Phase, ChangeID, Health string
	NoTLSVerify                                                            bool
	CheckedAt, UpdatedAt                                                   time.Time
}

// loadAccount loads the account; ok is false when there is none.
func loadAccount(ctx context.Context, q queryer) (account, bool, error) {
	var (
		a                 account
		caps, connectedAt string
	)
	err := q.QueryRowContext(ctx, `SELECT id, account_id, token_ct, capabilities_json, connected_at
		FROM cloudflare_accounts`).Scan(&a.ID, &a.AccountID, &a.TokenCT, &caps, &connectedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return account{}, false, nil
	}
	if err != nil {
		return account{}, false, fmt.Errorf("cloudflare: load account: %w", err)
	}
	if err := json.Unmarshal([]byte(caps), &a.Capabilities); err != nil {
		return account{}, false, fmt.Errorf("cloudflare: decode account capabilities: %w", err)
	}
	if a.Capabilities == nil {
		a.Capabilities = []cloud.Capability{}
	}
	if a.ConnectedAt, err = storeParseTime(connectedAt); err != nil {
		return account{}, false, err
	}
	return a, true, nil
}

// insertAccount inserts a. It fails when an account row exists already.
func insertAccount(ctx context.Context, tx *sql.Tx, a account) error {
	caps := a.Capabilities
	if caps == nil {
		caps = []cloud.Capability{}
	}
	capsJSON, err := json.Marshal(caps)
	if err != nil {
		return fmt.Errorf("cloudflare: encode account capabilities: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cloudflare_accounts (id, account_id, token_ct, capabilities_json, connected_at)
		VALUES (?, ?, ?, ?, ?)`, a.ID, a.AccountID, a.TokenCT, string(capsJSON), storeFormatTime(a.ConnectedAt)); err != nil {
		return fmt.Errorf("cloudflare: insert account: %w", err)
	}
	return nil
}

// deleteAccount deletes the account with that id. No such row is not an
// error, so an inverse that runs twice succeeds.
func deleteAccount(ctx context.Context, tx *sql.Tx, id string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM cloudflare_accounts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("cloudflare: delete account: %w", err)
	}
	return nil
}

// storeUpdate runs an UPDATE through tx and fails, naming what, when it
// changed no row.
func storeUpdate(ctx context.Context, tx *sql.Tx, what, query string, args ...any) error {
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("cloudflare: %s: %w", what, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("cloudflare: %s: %w", what, err)
	}
	if n == 0 {
		return fmt.Errorf("cloudflare: %s: no such row", what)
	}
	return nil
}

// insertTunnel inserts t.
func insertTunnel(ctx context.Context, tx *sql.Tx, t tunnelRow) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO cloudflare_tunnels (id, name, change_id, config_hash, drift, created_at, checked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, t.ID, t.Name, t.ChangeID, t.ConfigHash, t.Drift, storeFormatTime(t.CreatedAt),
		storeNullTime(t.CheckedAt)); err != nil {
		return fmt.Errorf("cloudflare: insert tunnel %s: %w", t.ID, err)
	}
	return nil
}

const storeTunnelColumns = `id, name, change_id, config_hash, drift, created_at, checked_at`

// storeScanTunnel decodes one cloudflare_tunnels row, its columns selected as
// storeTunnelColumns, through scan, a *sql.Row's or a *sql.Rows' Scan method.
func storeScanTunnel(scan func(dest ...any) error) (tunnelRow, error) {
	var (
		t       tunnelRow
		created string
		checked sql.NullString
	)
	if err := scan(&t.ID, &t.Name, &t.ChangeID, &t.ConfigHash, &t.Drift, &created, &checked); err != nil {
		return tunnelRow{}, fmt.Errorf("cloudflare: scan tunnel: %w", err)
	}
	var err error
	if t.CreatedAt, err = storeParseTime(created); err != nil {
		return tunnelRow{}, err
	}
	if t.CheckedAt, err = storeParseNullTime(checked); err != nil {
		return tunnelRow{}, err
	}
	return t, nil
}

// loadTunnel loads the tunnel whose id or name is idOrName, preferring an id
// match; ok is false when there is none.
func loadTunnel(ctx context.Context, q queryer, idOrName string) (tunnelRow, bool, error) {
	t, err := storeScanTunnel(q.QueryRowContext(ctx, `SELECT `+storeTunnelColumns+` FROM cloudflare_tunnels
		WHERE id = ?1 OR name = ?1 ORDER BY id = ?1 DESC LIMIT 1`, idOrName).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return tunnelRow{}, false, nil
	}
	if err != nil {
		return tunnelRow{}, false, err
	}
	return t, true, nil
}

// listTunnels lists every tunnel by created_at, then id.
func listTunnels(ctx context.Context, q queryer) ([]tunnelRow, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+storeTunnelColumns+` FROM cloudflare_tunnels ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("cloudflare: list tunnels: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []tunnelRow
	for rows.Next() {
		t, err := storeScanTunnel(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cloudflare: list tunnels: %w", err)
	}
	return out, nil
}

// deleteTunnel deletes the tunnel with that id. No such row is not an error;
// a hostname row that still references the tunnel is.
func deleteTunnel(ctx context.Context, tx *sql.Tx, id string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM cloudflare_tunnels WHERE id = ?`, id); err != nil {
		return fmt.Errorf("cloudflare: delete tunnel %s: %w", id, err)
	}
	return nil
}

// setTunnelHash sets the config_hash of the tunnel with that id.
func setTunnelHash(ctx context.Context, tx *sql.Tx, id, hash string) error {
	return storeUpdate(ctx, tx, "set tunnel "+id+" hash",
		`UPDATE cloudflare_tunnels SET config_hash = ? WHERE id = ?`, hash, id)
}

// setTunnelCheck records a check of the tunnel with that id: its drift and
// when it ran.
func setTunnelCheck(ctx context.Context, tx *sql.Tx, id string, drift bool, at time.Time) error {
	return storeUpdate(ctx, tx, "set tunnel "+id+" check",
		`UPDATE cloudflare_tunnels SET drift = ?, checked_at = ? WHERE id = ?`, drift, storeNullTime(at), id)
}

// insertHostname inserts h, an empty Health as unknown.
func insertHostname(ctx context.Context, tx *sql.Tx, h hostnameRow) error {
	health := h.Health
	if health == "" {
		health = "unknown"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cloudflare_hostnames (hostname, tunnel_id, service, no_tls_verify, zone_id,
		record_id, phase, change_id, health, checked_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.Hostname, h.TunnelID, h.Service, h.NoTLSVerify, h.ZoneID, h.RecordID, h.Phase, h.ChangeID, health,
		storeNullTime(h.CheckedAt), storeFormatTime(h.UpdatedAt)); err != nil {
		return fmt.Errorf("cloudflare: insert hostname %s: %w", h.Hostname, err)
	}
	return nil
}

const storeHostnameColumns = `hostname, tunnel_id, service, no_tls_verify, zone_id, record_id, phase, change_id, health,
	checked_at, updated_at`

// storeScanHostname decodes one cloudflare_hostnames row, its columns
// selected as storeHostnameColumns, through scan, a *sql.Row's or a
// *sql.Rows' Scan method.
func storeScanHostname(scan func(dest ...any) error) (hostnameRow, error) {
	var (
		h       hostnameRow
		checked sql.NullString
		updated string
	)
	if err := scan(&h.Hostname, &h.TunnelID, &h.Service, &h.NoTLSVerify, &h.ZoneID, &h.RecordID, &h.Phase, &h.ChangeID,
		&h.Health, &checked, &updated); err != nil {
		return hostnameRow{}, fmt.Errorf("cloudflare: scan hostname: %w", err)
	}
	var err error
	if h.CheckedAt, err = storeParseNullTime(checked); err != nil {
		return hostnameRow{}, err
	}
	if h.UpdatedAt, err = storeParseTime(updated); err != nil {
		return hostnameRow{}, err
	}
	return h, nil
}

// loadHostname loads the hostname row; ok is false when there is none.
func loadHostname(ctx context.Context, q queryer, hostname string) (hostnameRow, bool, error) {
	h, err := storeScanHostname(q.QueryRowContext(ctx, `SELECT `+storeHostnameColumns+` FROM cloudflare_hostnames
		WHERE hostname = ?`, hostname).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return hostnameRow{}, false, nil
	}
	if err != nil {
		return hostnameRow{}, false, err
	}
	return h, true, nil
}

// listHostnames lists every hostname row by hostname.
func listHostnames(ctx context.Context, q queryer) ([]hostnameRow, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+storeHostnameColumns+` FROM cloudflare_hostnames ORDER BY hostname`)
	if err != nil {
		return nil, fmt.Errorf("cloudflare: list hostnames: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []hostnameRow
	for rows.Next() {
		h, err := storeScanHostname(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cloudflare: list hostnames: %w", err)
	}
	return out, nil
}

// setHostnamePhase sets the phase and updated_at of the hostname row, and its
// zone_id and record_id each when the argument is not "". The table's CHECK
// refuses an unknown phase.
func setHostnamePhase(ctx context.Context, tx *sql.Tx, hostname, phase, zoneID, recordID string, at time.Time) error {
	return storeUpdate(ctx, tx, "set hostname "+hostname+" phase", `UPDATE cloudflare_hostnames
		SET phase = ?, updated_at = ?,
			zone_id = CASE WHEN ?3 = '' THEN zone_id ELSE ?3 END,
			record_id = CASE WHEN ?4 = '' THEN record_id ELSE ?4 END
		WHERE hostname = ?5`, phase, storeFormatTime(at), zoneID, recordID, hostname)
}

// setHostnameHealth records a check of the hostname row: its health and when
// it ran. The table's CHECK refuses an unknown health.
func setHostnameHealth(ctx context.Context, tx *sql.Tx, hostname, health string, at time.Time) error {
	return storeUpdate(ctx, tx, "set hostname "+hostname+" health",
		`UPDATE cloudflare_hostnames SET health = ?, checked_at = ? WHERE hostname = ?`, health, storeNullTime(at), hostname)
}

// deleteHostname deletes the hostname row. No such row is not an error.
func deleteHostname(ctx context.Context, tx *sql.Tx, hostname string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM cloudflare_hostnames WHERE hostname = ?`, hostname); err != nil {
		return fmt.Errorf("cloudflare: delete hostname %s: %w", hostname, err)
	}
	return nil
}

// cleanupPending names what blocks deleting the account row (spec 1.9): each
// hostname row not in phase active, and each unresolved effect whose
// resource starts with "cloudflare.". An intended effect does not count, as
// the disconnecting change holds one while it checks. It reads through tx,
// so it sees the transaction's own writes.
func cleanupPending(ctx context.Context, tx *sql.Tx) ([]string, error) {
	var out []string
	hosts, err := tx.QueryContext(ctx, `SELECT hostname, phase FROM cloudflare_hostnames
		WHERE phase <> 'active' ORDER BY hostname`)
	if err != nil {
		return nil, fmt.Errorf("cloudflare: cleanup pending hostnames: %w", err)
	}
	defer func() { _ = hosts.Close() }()
	for hosts.Next() {
		var hostname, phase string
		if err := hosts.Scan(&hostname, &phase); err != nil {
			return nil, fmt.Errorf("cloudflare: cleanup pending hostnames: %w", err)
		}
		out = append(out, fmt.Sprintf("hostname %s is in phase %s", hostname, phase))
	}
	if err := hosts.Err(); err != nil {
		return nil, fmt.Errorf("cloudflare: cleanup pending hostnames: %w", err)
	}
	// Both queries share the transaction's one connection, so the hostname
	// rows are closed before the effects are queried.
	if err := hosts.Close(); err != nil {
		return nil, fmt.Errorf("cloudflare: cleanup pending hostnames: %w", err)
	}

	// substr, not LIKE: LIKE treats "_" and "%" as wildcards.
	effects, err := tx.QueryContext(ctx, `SELECT change_id, seq, resource FROM core_effects
		WHERE state = 'unresolved' AND substr(resource, 1, 11) = 'cloudflare.' ORDER BY change_id, seq`)
	if err != nil {
		return nil, fmt.Errorf("cloudflare: cleanup pending effects: %w", err)
	}
	defer func() { _ = effects.Close() }()
	for effects.Next() {
		var (
			changeID, resource string
			seq                int
		)
		if err := effects.Scan(&changeID, &seq, &resource); err != nil {
			return nil, fmt.Errorf("cloudflare: cleanup pending effects: %w", err)
		}
		out = append(out, fmt.Sprintf("unresolved effect %s#%d on %s", changeID, seq, resource))
	}
	if err := effects.Err(); err != nil {
		return nil, fmt.Errorf("cloudflare: cleanup pending effects: %w", err)
	}
	return out, nil
}
