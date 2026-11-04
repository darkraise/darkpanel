package backup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/quarantine"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules"
)

// CanaryKey is the core_meta key of the backup canary.
const CanaryKey = "backup_canary"

// CanaryAD is the associated data the canary is sealed with.
const CanaryAD = "core_meta.value.backup_canary"

// Canary is the sealed canary's plaintext, JSON.
type Canary struct {
	CreatedAt time.Time `json:"createdAt"`
}

// ErrNotQuarantined is the error of an installed controller database that
// does not hold this restore's quarantine.
var ErrNotQuarantined = errors.New("backup: the installed controller database is not in this restore's quarantine")

// validateMigrationNumber matches the NNNN_ prefix of a migration file name.
var validateMigrationNumber = regexp.MustCompile(`^(\d{4})_`)

// ValidateStaged opens dbPath, checks PRAGMA integrity_check answers exactly "ok", that no applied
// core or module controller migration is newer than this binary embeds, and that a sealer over
// keyFile opens the canary; it returns the canary's CreatedAt. The database is closed before return.
func ValidateStaged(ctx context.Context, dbPath string, keyFile []byte) (createdAt time.Time, err error) {
	sealer, err := validateKeySealer(keyFile)
	if err != nil {
		return time.Time{}, err
	}

	info, err := os.Stat(dbPath)
	if err != nil {
		return time.Time{}, fmt.Errorf("backup: staged database: open: %w", err)
	}
	if !info.Mode().IsRegular() {
		return time.Time{}, errors.New("backup: staged database: open: not a regular file")
	}
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		return time.Time{}, fmt.Errorf("backup: staged database: open: %w", err)
	}
	// Closing the last connection also removes the -wal and -shm files that
	// store.Open leaves beside the database while it is open.
	defer func() {
		if errClose := db.Close(); errClose != nil && err == nil {
			createdAt, err = time.Time{}, fmt.Errorf("backup: staged database: close: %w", errClose)
		}
	}()

	if err := validateIntegrity(ctx, db); err != nil {
		return time.Time{}, err
	}
	if err := validateSchema(ctx, db); err != nil {
		return time.Time{}, err
	}
	return validateCanary(ctx, db, sealer)
}

// validateKeySealer builds a sealer from a key member, which must be exactly
// a key file: one line of secret.KeyPrefix and secret.KeySize bytes in
// standard base64. No error quotes the bytes.
func validateKeySealer(keyFile []byte) (*secret.Sealer, error) {
	if len(keyFile) > MaxKeyMember {
		return nil, fmt.Errorf("backup: the key member is over %d bytes", MaxKeyMember)
	}
	line, ok := bytes.CutSuffix(keyFile, []byte("\n"))
	encoded, hasPrefix := bytes.CutPrefix(line, []byte(secret.KeyPrefix))
	if !ok || !hasPrefix || bytes.ContainsAny(encoded, "\r\n") {
		return nil, fmt.Errorf("backup: the key member is not a key file: want one line starting with %s", secret.KeyPrefix)
	}
	key, err := base64.StdEncoding.DecodeString(string(encoded))
	defer clear(key)
	if err != nil || len(key) != secret.KeySize {
		// The decoder's error would quote key material, so it is not reported.
		return nil, fmt.Errorf("backup: the key member is not a key file: want %d bytes in standard base64", secret.KeySize)
	}
	sealer, err := secret.NewSealer(key)
	if err != nil {
		return nil, fmt.Errorf("backup: the key member: %w", err)
	}
	return sealer, nil
}

func validateIntegrity(ctx context.Context, db *store.DB) error {
	rows, err := db.Read().QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return fmt.Errorf("backup: staged database: integrity check: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var answers []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return fmt.Errorf("backup: staged database: integrity check: %w", err)
		}
		answers = append(answers, s)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("backup: staged database: integrity check: %w", err)
	}
	if len(answers) != 1 || answers[0] != "ok" {
		return fmt.Errorf("backup: staged database: integrity check failed (%d problems reported)", len(answers))
	}
	return nil
}

func validateSchema(ctx context.Context, db *store.DB) error {
	embedded, err := validateEmbedded()
	if err != nil {
		return err
	}
	rows, err := db.Read().QueryContext(ctx,
		`SELECT module, max(number) FROM core_migrations WHERE side = 'controller' GROUP BY module`)
	if err != nil {
		return fmt.Errorf("backup: staged database: schema: read core_migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var module string
		var applied int
		if err := rows.Scan(&module, &applied); err != nil {
			return fmt.Errorf("backup: staged database: schema: read core_migrations: %w", err)
		}
		if has := embedded[module]; applied > has {
			return fmt.Errorf("backup: staged database: schema is newer than this binary: module %s has migration %d, the binary has %d",
				module, applied, has)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("backup: staged database: schema: read core_migrations: %w", err)
	}
	return nil
}

// validateEmbedded returns the highest controller migration number this
// binary embeds, by module; the core's migrations are under "core".
func validateEmbedded() (map[string]int, error) {
	coreSet, err := store.CoreMigrations(store.SideController)
	if err != nil {
		return nil, fmt.Errorf("backup: staged database: schema: %w", err)
	}
	sets := map[string]fs.FS{coreSet.Module: coreSet.FS}
	for _, m := range modules.All() {
		man := m.Manifest()
		if fsys := man.Migrations[core.SideController]; fsys != nil {
			sets[man.ID] = fsys
		}
	}
	out := make(map[string]int, len(sets))
	for module, fsys := range sets {
		entries, err := fs.ReadDir(fsys, ".")
		if err != nil {
			return nil, fmt.Errorf("backup: staged database: schema: list migrations of %s: %w", module, err)
		}
		for _, e := range entries {
			m := validateMigrationNumber.FindStringSubmatch(e.Name())
			if e.IsDir() || m == nil {
				continue
			}
			n, _ := strconv.Atoi(m[1])
			out[module] = max(out[module], n)
		}
	}
	return out, nil
}

func validateCanary(ctx context.Context, db *store.DB, sealer *secret.Sealer) (time.Time, error) {
	var value string
	err := db.Read().QueryRowContext(ctx, `SELECT value FROM core_meta WHERE key = ?`, CanaryKey).Scan(&value)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return time.Time{}, fmt.Errorf("backup: staged database: canary: core_meta has no %s row", CanaryKey)
	case err != nil:
		return time.Time{}, fmt.Errorf("backup: staged database: canary: read core_meta %s: %w", CanaryKey, err)
	}
	sealed, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return time.Time{}, errors.New("backup: staged database: canary: the value is not standard base64")
	}
	plain, err := sealer.Open(CanaryAD, sealed)
	if err != nil {
		return time.Time{}, fmt.Errorf("backup: staged database: canary: the seal does not open with the backup's key: %w", err)
	}
	var c Canary
	if err := json.Unmarshal(plain, &c); err != nil {
		// The decoder's error would quote the payload, so it is not reported.
		return time.Time{}, errors.New("backup: staged database: canary: the payload does not decode")
	}
	if c.CreatedAt.IsZero() {
		return time.Time{}, errors.New("backup: staged database: canary: the payload has no creation time")
	}
	return c.CreatedAt, nil
}

// VerifyInstalled opens dbPath read-only ("file:<path>?mode=ro" plus query_only) and checks spec 10
// phase 7: core_meta.restore_quarantine decodes with Tx == tx, no core_changes row is in a
// NonTerminal state, no core_rollouts row is running or draining, and every core_locks owner is a
// core_changes row in state quarantined.
func VerifyInstalled(ctx context.Context, dbPath, tx string) (err error) {
	if strings.ContainsAny(dbPath, "?#%") {
		return fmt.Errorf("backup: installed database path %q must not contain ?, # or %%", dbPath)
	}
	// store.Open is not used: it switches to WAL, creates -wal and -shm and
	// chmods, and phase 7 must leave the installed database untouched.
	u := url.URL{Scheme: "file", Path: dbPath, RawQuery: "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return fmt.Errorf("backup: installed database: open: %w", err)
	}
	defer func() {
		if errClose := db.Close(); errClose != nil && err == nil {
			err = fmt.Errorf("backup: installed database: close: %w", errClose)
		}
	}()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("backup: installed database: open %s: %w", dbPath, err)
	}

	var raw string
	err = db.QueryRowContext(ctx, `SELECT value FROM core_meta WHERE key = ?`, quarantine.MetaKey).Scan(&raw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%w: core_meta %s is missing", ErrNotQuarantined, quarantine.MetaKey)
	case err != nil:
		return fmt.Errorf("backup: installed database: read core_meta %s: %w", quarantine.MetaKey, err)
	}
	var m quarantine.Marker
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return fmt.Errorf("%w: core_meta %s does not decode", ErrNotQuarantined, quarantine.MetaKey)
	}
	if m.Tx != tx {
		return fmt.Errorf("%w: core_meta %s carries tx %s, this restore is %s", ErrNotQuarantined, quarantine.MetaKey, m.Tx, tx)
	}

	args := make([]any, 0, len(NonTerminal))
	for _, s := range NonTerminal {
		args = append(args, s)
	}
	in := strings.TrimSuffix(strings.Repeat("?, ", len(NonTerminal)), ", ")
	var id, state string
	err = db.QueryRowContext(ctx,
		`SELECT id, state FROM core_changes WHERE state IN (`+in+`) ORDER BY id LIMIT 1`, //nolint:gosec // G202: in holds only ? placeholders; the states are bound
		args...).Scan(&id, &state)
	switch {
	case err == nil:
		return fmt.Errorf("%w: change %s is still %s", ErrNotQuarantined, id, state)
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("backup: installed database: read core_changes: %w", err)
	}

	err = db.QueryRowContext(ctx,
		`SELECT id, state FROM core_rollouts WHERE state IN (?, ?) ORDER BY id LIMIT 1`,
		core.RolloutRunning, core.RolloutDraining).Scan(&id, &state)
	switch {
	case err == nil:
		return fmt.Errorf("%w: rollout %s is still %s", ErrNotQuarantined, id, state)
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("backup: installed database: read core_rollouts: %w", err)
	}

	var key, owner string
	err = db.QueryRowContext(ctx,
		`SELECT key, owner FROM core_locks WHERE owner NOT IN (SELECT id FROM core_changes WHERE state = ?) ORDER BY key LIMIT 1`,
		core.StateQuarantined).Scan(&key, &owner)
	switch {
	case err == nil:
		return fmt.Errorf("%w: lock %s is owned by %s, which is not a quarantined change", ErrNotQuarantined, key, owner)
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("backup: installed database: read core_locks: %w", err)
	}
	return nil
}
