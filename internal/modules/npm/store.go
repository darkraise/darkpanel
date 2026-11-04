package npm

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

//go:embed migrations/agent/*.sql
var agentMigrationsFS embed.FS

// agentMigrations returns the module's agent-side migrations, the value of
// Manifest.Migrations[core.SideAgent].
func agentMigrations() fs.FS {
	sub, err := fs.Sub(agentMigrationsFS, "migrations/agent")
	if err != nil {
		// fs.Sub fails only for an invalid path, and this one is a constant.
		panic(err)
	}
	return sub
}

// storeTimeFormat is the fixed-width UTC form of every stored time, so times
// compare as text.
const storeTimeFormat = "2006-01-02T15:04:05.000000000Z"

// storeTime formats t in UTC for a time column; the zero time is "".
func storeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(storeTimeFormat)
}

// parseStoreTime reads a time column written by storeTime; "" is the zero time.
func parseStoreTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(storeTimeFormat, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("npm: stored time %q: %w", s, err)
	}
	return t, nil
}

// queryer is what a read needs: a database's read handle or a transaction.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// instanceRow is the npm_instances row: the one NPM instance of the host.
// PasswordCT is the admin password sealed with associated data
// npm_instances.password_ct.1.
type instanceRow struct {
	ContainerID, Image, AdminEmail string
	PasswordCT                     []byte
	Public                         string // "no" | "confirmed"
	CreatedAt, UpdatedAt           time.Time
}

// instanceColumns are the npm_instances columns an instanceRow holds, in its field order.
const instanceColumns = "container_id, image, admin_email, password_ct, public, created_at, updated_at"

// loadInstance returns the instance row; ok is false when none is stored.
func loadInstance(ctx context.Context, q queryer) (instanceRow, bool, error) {
	var (
		r                instanceRow
		created, updated string
	)
	err := q.QueryRowContext(ctx, "SELECT "+instanceColumns+" FROM npm_instances WHERE id = 1").
		Scan(&r.ContainerID, &r.Image, &r.AdminEmail, &r.PasswordCT, &r.Public, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return instanceRow{}, false, nil
	}
	if err != nil {
		return instanceRow{}, false, fmt.Errorf("npm: read the instance: %w", err)
	}
	if r.CreatedAt, err = parseStoreTime(created); err != nil {
		return instanceRow{}, false, err
	}
	if r.UpdatedAt, err = parseStoreTime(updated); err != nil {
		return instanceRow{}, false, err
	}
	return r, true, nil
}

// saveInstance writes the instance row, inserting it or replacing every column of the stored one.
func saveInstance(ctx context.Context, tx *sql.Tx, r instanceRow) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO npm_instances (id, "+instanceColumns+") VALUES (1, ?, ?, ?, ?, ?, ?, ?) "+
		"ON CONFLICT (id) DO UPDATE SET container_id = excluded.container_id, image = excluded.image, admin_email = excluded.admin_email, "+
		"password_ct = excluded.password_ct, public = excluded.public, created_at = excluded.created_at, updated_at = excluded.updated_at",
		r.ContainerID, r.Image, r.AdminEmail, r.PasswordCT, r.Public, storeTime(r.CreatedAt), storeTime(r.UpdatedAt))
	if err != nil {
		return fmt.Errorf("npm: record the instance: %w", err)
	}
	return nil
}

// deleteInstance removes the instance row; there being none is not an error.
func deleteInstance(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM npm_instances"); err != nil {
		return fmt.Errorf("npm: delete the instance: %w", err)
	}
	return nil
}

// reservedRow is one npm_reserved row: an NPM object an exposure owns.
type reservedRow struct {
	Kind     string // "proxy_host" | "access_list" | "certificate"
	ObjectID int
	Owner    string // the owning action: "npm.admin.expose" | "panel.exposure.add"
	Exposure string // the exposure's host name
	ChangeID string
}

// reservedOf returns the reservation of an object; ok is false when it is not reserved.
func reservedOf(ctx context.Context, q queryer, kind string, id int) (reservedRow, bool, error) {
	var r reservedRow
	err := q.QueryRowContext(ctx, "SELECT kind, object_id, owner, exposure, change_id FROM npm_reserved WHERE kind = ? AND object_id = ?", kind, id).
		Scan(&r.Kind, &r.ObjectID, &r.Owner, &r.Exposure, &r.ChangeID)
	if errors.Is(err, sql.ErrNoRows) {
		return reservedRow{}, false, nil
	}
	if err != nil {
		return reservedRow{}, false, fmt.Errorf("npm: read the reservation of %s %d: %w", kind, id, err)
	}
	return r, true, nil
}

// addReserved records a reservation; an object already reserved is an error.
func addReserved(ctx context.Context, tx *sql.Tx, r reservedRow) error {
	if _, err := tx.ExecContext(ctx, "INSERT INTO npm_reserved (kind, object_id, owner, exposure, change_id) VALUES (?, ?, ?, ?, ?)",
		r.Kind, r.ObjectID, r.Owner, r.Exposure, r.ChangeID); err != nil {
		return fmt.Errorf("npm: reserve %s %d: %w", r.Kind, r.ObjectID, err)
	}
	return nil
}

// claimRow is one npm_attachments row: a claimant that needs the NPM container on a network. Preexisting is true when
// the container was already attached before the first claim.
type claimRow struct {
	Network, Claimant, ChangeID string
	Preexisting                 bool
}

// claims returns the claims on network ordered by claimant, or with network "" every claim ordered by network and
// then claimant. The slice is never nil.
func claims(ctx context.Context, q queryer, network string) ([]claimRow, error) {
	query, args := "SELECT network, claimant, change_id, preexisting FROM npm_attachments ORDER BY network, claimant", []any(nil)
	if network != "" {
		query = "SELECT network, claimant, change_id, preexisting FROM npm_attachments WHERE network = ? ORDER BY claimant"
		args = []any{network}
	}
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("npm: read the network claims: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []claimRow{}
	for rows.Next() {
		var c claimRow
		if err := rows.Scan(&c.Network, &c.Claimant, &c.ChangeID, &c.Preexisting); err != nil {
			return nil, fmt.Errorf("npm: read the network claims: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("npm: read the network claims: %w", err)
	}
	return out, nil
}

// addClaim records a claim; a claimant claiming the same network twice is an error.
func addClaim(ctx context.Context, tx *sql.Tx, c claimRow) error {
	if _, err := tx.ExecContext(ctx, "INSERT INTO npm_attachments (network, claimant, change_id, preexisting) VALUES (?, ?, ?, ?)",
		c.Network, c.Claimant, c.ChangeID, c.Preexisting); err != nil {
		return fmt.Errorf("npm: record the claim of %s on %s: %w", c.Claimant, c.Network, err)
	}
	return nil
}

// dropClaim removes a claim; there being none is not an error.
func dropClaim(ctx context.Context, tx *sql.Tx, network, claimant string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM npm_attachments WHERE network = ? AND claimant = ?", network, claimant); err != nil {
		return fmt.Errorf("npm: drop the claim of %s on %s: %w", claimant, network, err)
	}
	return nil
}

// containerConfig is the create configuration of the container an upgrade replaces. Name is kept beside Create
// because ContainerCreate.Name is not part of its JSON; Networks holds every endpoint beyond darkpanel-npm.
type containerConfig struct {
	Create   dockerapi.ContainerCreate
	Name     string
	Networks map[string]*dockerapi.EndpointSettings
}

// archiveRef is one verified volume archive of an upgrade; File is a bare managed-file name inside backupDir.
type archiveRef struct {
	Volume string `json:"volume"`
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}

// upgradeRow is one npm_upgrades row: the agent-local transaction of an npm.instance.upgrade change.
type upgradeRow struct {
	ChangeID, FromImage, ToImage string
	Config                       containerConfig
	Archives                     []archiveRef
	Phase, Result                string
	UpdatedAt                    time.Time
}

// upgradeColumns are the npm_upgrades columns an upgradeRow holds, in its field order.
const upgradeColumns = "change_id, from_image, to_image, config, archives, phase, result, updated_at"

// insertUpgrade records a new upgrade in phase recorded with no archives; a second upgrade of one change is an error.
func insertUpgrade(ctx context.Context, db *store.DB, u upgradeRow) error {
	config, err := json.Marshal(u.Config)
	if err != nil {
		return fmt.Errorf("npm: encode the container configuration of upgrade %s: %w", u.ChangeID, err)
	}
	archives, err := encodeArchives(nil)
	if err != nil {
		return err
	}
	err = db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO npm_upgrades ("+upgradeColumns+") VALUES (?, ?, ?, ?, ?, 'recorded', '', ?)",
			u.ChangeID, u.FromImage, u.ToImage, string(config), archives, storeTime(u.UpdatedAt))
		return err
	})
	if err != nil {
		return fmt.Errorf("npm: record upgrade %s: %w", u.ChangeID, err)
	}
	return nil
}

// encodeArchives is the archives column of a list; nil encodes as [].
func encodeArchives(a []archiveRef) (string, error) {
	if a == nil {
		a = []archiveRef{}
	}
	b, err := json.Marshal(a)
	if err != nil {
		return "", fmt.Errorf("npm: encode the archives: %w", err)
	}
	return string(b), nil
}

// loadUpgrade returns the upgrade of a change; ok is false when there is none.
func loadUpgrade(ctx context.Context, q queryer, changeID string) (upgradeRow, bool, error) {
	var (
		u                         upgradeRow
		config, archives, updated string
	)
	err := q.QueryRowContext(ctx, "SELECT "+upgradeColumns+" FROM npm_upgrades WHERE change_id = ?", changeID).
		Scan(&u.ChangeID, &u.FromImage, &u.ToImage, &config, &archives, &u.Phase, &u.Result, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return upgradeRow{}, false, nil
	}
	if err != nil {
		return upgradeRow{}, false, fmt.Errorf("npm: read upgrade %s: %w", changeID, err)
	}
	if err := json.Unmarshal([]byte(config), &u.Config); err != nil {
		return upgradeRow{}, false, fmt.Errorf("npm: decode the container configuration of upgrade %s: %w", changeID, err)
	}
	if err := json.Unmarshal([]byte(archives), &u.Archives); err != nil {
		return upgradeRow{}, false, fmt.Errorf("npm: decode the archives of upgrade %s: %w", changeID, err)
	}
	if u.UpdatedAt, err = parseStoreTime(updated); err != nil {
		return upgradeRow{}, false, err
	}
	return u, true, nil
}

// setUpgradePhase moves an upgrade to phase with result. A nil archives keeps the stored list and a non-nil one
// replaces it. An unknown change id is an error.
func setUpgradePhase(ctx context.Context, db *store.DB, changeID, phase, result string, archives []archiveRef, at time.Time) error {
	query := "UPDATE npm_upgrades SET phase = ?, result = ?, updated_at = ? WHERE change_id = ?"
	args := []any{phase, result, storeTime(at), changeID}
	if archives != nil {
		text, err := encodeArchives(archives)
		if err != nil {
			return err
		}
		query = "UPDATE npm_upgrades SET phase = ?, result = ?, updated_at = ?, archives = ? WHERE change_id = ?"
		args = []any{phase, result, storeTime(at), text, changeID}
	}
	err := db.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return errors.New("no such upgrade")
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("npm: set the phase of upgrade %s: %w", changeID, err)
	}
	return nil
}

// addWatch records that the effect seq of a change, the creation of the object kind identity names, was resolved
// skipped; a watch already recorded for the identity is kept.
func addWatch(ctx context.Context, tx *sql.Tx, kind, identity, changeID string, seq int) error {
	if _, err := tx.ExecContext(ctx, "INSERT INTO npm_watch (kind, identity, change_id, seq) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING",
		kind, identity, changeID, seq); err != nil {
		return fmt.Errorf("npm: watch %s %s: %w", kind, identity, err)
	}
	return nil
}

// watched returns the watched identities of kind as a set; the map is never nil.
func watched(ctx context.Context, q queryer, kind string) (map[string]bool, error) {
	rows, err := q.QueryContext(ctx, "SELECT identity FROM npm_watch WHERE kind = ?", kind)
	if err != nil {
		return nil, fmt.Errorf("npm: read the watched %s identities: %w", kind, err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var identity string
		if err := rows.Scan(&identity); err != nil {
			return nil, fmt.Errorf("npm: read the watched %s identities: %w", kind, err)
		}
		out[identity] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("npm: read the watched %s identities: %w", kind, err)
	}
	return out, nil
}
