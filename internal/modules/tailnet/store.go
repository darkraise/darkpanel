package tailnet

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
var migrationsFS embed.FS

// controllerMigrations returns the module's controller-side migrations, the
// value of Manifest.Migrations[core.SideController].
func controllerMigrations() fs.FS {
	sub, err := fs.Sub(migrationsFS, "migrations/controller")
	if err != nil {
		// fs.Sub fails only for an invalid path, and this one is a constant.
		panic(err)
	}
	return sub
}

// storeTimeLayout is the fixed-width form of every stored time, so times
// sort and compare as text.
const storeTimeLayout = "2006-01-02T15:04:05.000000000Z"

func formatStoreTime(t time.Time) string { return t.UTC().Format(storeTimeLayout) }

func parseStoreTime(s string) (time.Time, error) {
	t, err := time.Parse(storeTimeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("tailnet: parse stored time: %w", err)
	}
	return t, nil
}

// queryer is a *sql.DB or a *sql.Tx.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// account is the tailnet_accounts row.
type account struct {
	ID, Tailnet, ClientID string
	SecretCT              []byte // cloud.SealCredential(s, "tailnet_accounts", "client_secret_ct", ID, secret)
	TagsAllowed           []string
	Capabilities          []cloud.Capability
	ConnectedAt           time.Time
}

// keyRow is one tailnet_keys row.
type keyRow struct {
	Description, Kind, OwnerID, KeyID, State string
	RequestedAt, UpdatedAt                   time.Time
	ExpirySeconds                            int
	Tags                                     []string
}

// loadAccount loads the account; ok is false when there is none.
func loadAccount(ctx context.Context, q queryer) (account, bool, error) {
	var (
		a                       account
		tags, caps, connectedAt string
	)
	err := q.QueryRowContext(ctx, `SELECT id, tailnet, client_id, client_secret_ct, tags_allowed_json, capabilities_json, connected_at
		FROM tailnet_accounts`).Scan(&a.ID, &a.Tailnet, &a.ClientID, &a.SecretCT, &tags, &caps, &connectedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return account{}, false, nil
	}
	if err != nil {
		return account{}, false, fmt.Errorf("tailnet: load account: %w", err)
	}
	if err := json.Unmarshal([]byte(tags), &a.TagsAllowed); err != nil {
		return account{}, false, fmt.Errorf("tailnet: decode account tags: %w", err)
	}
	if err := json.Unmarshal([]byte(caps), &a.Capabilities); err != nil {
		return account{}, false, fmt.Errorf("tailnet: decode account capabilities: %w", err)
	}
	if a.TagsAllowed == nil {
		a.TagsAllowed = []string{}
	}
	if a.Capabilities == nil {
		a.Capabilities = []cloud.Capability{}
	}
	if a.ConnectedAt, err = parseStoreTime(connectedAt); err != nil {
		return account{}, false, err
	}
	return a, true, nil
}

// insertAccount inserts a. It fails when an account row exists already.
func insertAccount(ctx context.Context, tx *sql.Tx, a account) error {
	tags := a.TagsAllowed
	if tags == nil {
		tags = []string{}
	}
	caps := a.Capabilities
	if caps == nil {
		caps = []cloud.Capability{}
	}
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return fmt.Errorf("tailnet: encode account tags: %w", err)
	}
	capsJSON, err := json.Marshal(caps)
	if err != nil {
		return fmt.Errorf("tailnet: encode account capabilities: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO tailnet_accounts
		(id, tailnet, client_id, client_secret_ct, tags_allowed_json, capabilities_json, connected_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Tailnet, a.ClientID, a.SecretCT, string(tagsJSON), string(capsJSON), formatStoreTime(a.ConnectedAt)); err != nil {
		return fmt.Errorf("tailnet: insert account: %w", err)
	}
	return nil
}

// deleteAccount deletes the account with that id. No such row is not an
// error, so an inverse that runs twice succeeds.
func deleteAccount(ctx context.Context, tx *sql.Tx, id string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM tailnet_accounts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("tailnet: delete account: %w", err)
	}
	return nil
}

// insertKey inserts k.
func insertKey(ctx context.Context, tx *sql.Tx, k keyRow) error {
	tags := k.Tags
	if tags == nil {
		tags = []string{}
	}
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return fmt.Errorf("tailnet: encode key tags: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO tailnet_keys
		(description, kind, owner_id, requested_at, expiry_seconds, tags_json, key_id, state, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		k.Description, k.Kind, k.OwnerID, formatStoreTime(k.RequestedAt), k.ExpirySeconds, string(tagsJSON),
		k.KeyID, k.State, formatStoreTime(k.UpdatedAt)); err != nil {
		return fmt.Errorf("tailnet: insert key %s: %w", k.Description, err)
	}
	return nil
}

// scanKey decodes one tailnet_keys row, its columns selected in table order,
// through scan, a *sql.Row's or a *sql.Rows' Scan method.
func scanKey(scan func(dest ...any) error) (keyRow, error) {
	var k keyRow
	var requested, updated, tagsRaw string
	if err := scan(&k.Description, &k.Kind, &k.OwnerID, &requested, &k.ExpirySeconds, &tagsRaw, &k.KeyID, &k.State, &updated); err != nil {
		return keyRow{}, fmt.Errorf("tailnet: scan key: %w", err)
	}
	var err error
	if k.RequestedAt, err = parseStoreTime(requested); err != nil {
		return keyRow{}, err
	}
	if k.UpdatedAt, err = parseStoreTime(updated); err != nil {
		return keyRow{}, err
	}
	if err := json.Unmarshal([]byte(tagsRaw), &k.Tags); err != nil {
		return keyRow{}, fmt.Errorf("tailnet: decode key %s tags: %w", k.Description, err)
	}
	if k.Tags == nil {
		k.Tags = []string{}
	}
	return k, nil
}

// loadKey loads the key with that description; ok is false when there is
// none.
func loadKey(ctx context.Context, q queryer, description string) (keyRow, bool, error) {
	k, err := scanKey(q.QueryRowContext(ctx, `SELECT description, kind, owner_id, requested_at, expiry_seconds, tags_json, key_id, state, updated_at
		FROM tailnet_keys WHERE description = ?`, description).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return keyRow{}, false, nil
	}
	if err != nil {
		return keyRow{}, false, err
	}
	return k, true, nil
}

// listKeys lists every key by requested_at, then description.
func listKeys(ctx context.Context, q queryer) ([]keyRow, error) {
	rows, err := q.QueryContext(ctx, `SELECT description, kind, owner_id, requested_at, expiry_seconds, tags_json, key_id, state, updated_at
		FROM tailnet_keys ORDER BY requested_at, description`)
	if err != nil {
		return nil, fmt.Errorf("tailnet: list keys: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []keyRow
	for rows.Next() {
		k, err := scanKey(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tailnet: list keys: %w", err)
	}
	return out, nil
}

// setKeyState sets the state and updated_at of the key with that
// description, and its key_id when keyID is not "". It leaves every other
// column alone.
func setKeyState(ctx context.Context, tx *sql.Tx, description, state, keyID string, at time.Time) error {
	switch state {
	case "requested", "live", "uncertain", "used", "retired", "deleted":
	default:
		return fmt.Errorf("tailnet: set key %s state: unknown state %q", description, state)
	}
	res, err := tx.ExecContext(ctx, `UPDATE tailnet_keys
		SET state = ?, updated_at = ?, key_id = CASE WHEN ? = '' THEN key_id ELSE ? END
		WHERE description = ?`, state, formatStoreTime(at), keyID, keyID, description)
	if err != nil {
		return fmt.Errorf("tailnet: set key %s state: %w", description, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("tailnet: set key %s state: %w", description, err)
	}
	if n == 0 {
		return fmt.Errorf("tailnet: set key %s state: no such key", description)
	}
	return nil
}

// cleanupPending names what blocks deleting the account row (spec 1.9):
// each tailnet_keys row in requested, uncertain or retired, and each
// unresolved effect whose resource starts with "tailnet.". It reads through
// tx, so it sees the transaction's own writes.
func cleanupPending(ctx context.Context, tx *sql.Tx) ([]string, error) {
	out := []string{}
	keys, err := tx.QueryContext(ctx, `SELECT description, state FROM tailnet_keys
		WHERE state IN ('requested', 'uncertain', 'retired') ORDER BY requested_at, description`)
	if err != nil {
		return nil, fmt.Errorf("tailnet: cleanup pending keys: %w", err)
	}
	defer func() { _ = keys.Close() }()
	for keys.Next() {
		var description, state string
		if err := keys.Scan(&description, &state); err != nil {
			return nil, fmt.Errorf("tailnet: cleanup pending keys: %w", err)
		}
		out = append(out, fmt.Sprintf("key %s is %s", description, state))
	}
	if err := keys.Err(); err != nil {
		return nil, fmt.Errorf("tailnet: cleanup pending keys: %w", err)
	}
	// Both queries share the transaction's one connection, so the key rows
	// are closed before the effects are queried.
	if err := keys.Close(); err != nil {
		return nil, fmt.Errorf("tailnet: cleanup pending keys: %w", err)
	}

	// substr, not LIKE: LIKE treats "_" and "%" as wildcards.
	effects, err := tx.QueryContext(ctx, `SELECT change_id, seq, resource FROM core_effects
		WHERE state = 'unresolved' AND substr(resource, 1, 8) = 'tailnet.' ORDER BY change_id, seq`)
	if err != nil {
		return nil, fmt.Errorf("tailnet: cleanup pending effects: %w", err)
	}
	defer func() { _ = effects.Close() }()
	for effects.Next() {
		var (
			changeID, resource string
			seq                int
		)
		if err := effects.Scan(&changeID, &seq, &resource); err != nil {
			return nil, fmt.Errorf("tailnet: cleanup pending effects: %w", err)
		}
		out = append(out, fmt.Sprintf("effect %s#%d %s is unresolved", changeID, seq, resource))
	}
	if err := effects.Err(); err != nil {
		return nil, fmt.Errorf("tailnet: cleanup pending effects: %w", err)
	}
	return out, nil
}
