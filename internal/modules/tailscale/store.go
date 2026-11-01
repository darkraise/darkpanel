package tailscale

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/darkraise/darkpanel/internal/core/store"
)

//go:embed migrations/agent/*.sql
var migrationsFS embed.FS

// agentMigrations returns the module's agent-side migrations, the value of
// Manifest.Migrations[core.SideAgent].
func agentMigrations() fs.FS {
	sub, err := fs.Sub(migrationsFS, "migrations/agent")
	if err != nil {
		// fs.Sub fails only for an invalid path, and this one is a constant.
		panic(err)
	}
	return sub
}

// storeTimeFormat is the fixed-width UTC form of every stored time, so times
// compare as text.
const storeTimeFormat = "2006-01-02T15:04:05.000000000Z"

func storeTime(t time.Time) string { return t.UTC().Format(storeTimeFormat) }

// queryer is what a read needs: a database's read handle or a transaction.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// pinRow is one tailscale_pins row.
type pinRow struct {
	ChangeID, Version, Arch, PriorVersion string
	PriorHeld                             bool
	PriorBackend                          string
	LinkTailnet                           bool
	Phase, StagedDeb, StagedSHA256        string
	UnpinnedBy                            string
	UpdatedAt                             time.Time
}

const pinColumns = "change_id, version, arch, prior_version, prior_held, prior_backend, link_tailnet, phase, staged_deb, staged_sha256, unpinned_by, updated_at"

const (
	insertPinSQL = "INSERT INTO tailscale_pins (" + pinColumns + ") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
	loadPinSQL   = "SELECT " + pinColumns + " FROM tailscale_pins WHERE change_id = ?"
	activePinSQL = "SELECT " + pinColumns + " FROM tailscale_pins WHERE phase = 'done' AND unpinned_by = '' ORDER BY updated_at DESC, change_id DESC LIMIT 1"

	setPinPhaseSQL    = "UPDATE tailscale_pins SET phase = ?, updated_at = ? WHERE change_id = ?"
	setPinStagedSQL   = "UPDATE tailscale_pins SET staged_deb = ?, staged_sha256 = ?, updated_at = ? WHERE change_id = ?"
	setPinUnpinnedSQL = "UPDATE tailscale_pins SET unpinned_by = ?, updated_at = ? WHERE change_id = ?"
	addPinStepSQL     = "INSERT INTO tailscale_pin_steps (change_id, seq, step, result, at) " +
		"VALUES (?, (SELECT COALESCE(MAX(seq), 0) + 1 FROM tailscale_pin_steps WHERE change_id = ?), ?, ?, ?)"

	insertInstallSQL     = "INSERT INTO tailscale_installs (change_id, version, installed_at) VALUES (?, ?, ?)"
	liveInstallSQL       = "SELECT change_id, version FROM tailscale_installs WHERE removed_at = '' ORDER BY installed_at DESC, change_id DESC LIMIT 1"
	setInstallRemovedSQL = "UPDATE tailscale_installs SET removed_at = ? WHERE change_id = ?"
)

// insertPin records a new pin.
func insertPin(ctx context.Context, tx *sql.Tx, p pinRow) error {
	if _, err := tx.ExecContext(ctx, insertPinSQL, p.ChangeID, p.Version, p.Arch, p.PriorVersion, p.PriorHeld, p.PriorBackend,
		p.LinkTailnet, p.Phase, p.StagedDeb, p.StagedSHA256, p.UnpinnedBy, storeTime(p.UpdatedAt)); err != nil {
		return fmt.Errorf("tailscale: record pin %s: %w", p.ChangeID, err)
	}
	return nil
}

// loadPin returns the pin of a change; ok is false when there is none.
func loadPin(ctx context.Context, q queryer, changeID string) (pinRow, bool, error) {
	p, err := pinScan(q.QueryRowContext(ctx, loadPinSQL, changeID))
	if errors.Is(err, sql.ErrNoRows) {
		return pinRow{}, false, nil
	}
	if err != nil {
		return pinRow{}, false, fmt.Errorf("tailscale: read pin %s: %w", changeID, err)
	}
	return p, true, nil
}

// activePin returns the newest pin in phase done that no unpin has reversed;
// ok is false when there is none.
func activePin(ctx context.Context, q queryer) (pinRow, bool, error) {
	p, err := pinScan(q.QueryRowContext(ctx, activePinSQL))
	if errors.Is(err, sql.ErrNoRows) {
		return pinRow{}, false, nil
	}
	if err != nil {
		return pinRow{}, false, fmt.Errorf("tailscale: read the active pin: %w", err)
	}
	return p, true, nil
}

func pinScan(row interface{ Scan(dest ...any) error }) (pinRow, error) {
	var (
		p       pinRow
		updated string
	)
	if err := row.Scan(&p.ChangeID, &p.Version, &p.Arch, &p.PriorVersion, &p.PriorHeld, &p.PriorBackend, &p.LinkTailnet,
		&p.Phase, &p.StagedDeb, &p.StagedSHA256, &p.UnpinnedBy, &updated); err != nil {
		return pinRow{}, err
	}
	at, err := time.Parse(storeTimeFormat, updated)
	if err != nil {
		return pinRow{}, fmt.Errorf("updated at: %w", err)
	}
	p.UpdatedAt = at
	return p, nil
}

// setPinPhase moves a pin to phase.
func setPinPhase(ctx context.Context, db *store.DB, changeID, phase string, at time.Time) error {
	return storeUpdate(ctx, db, "set the phase of pin "+changeID, setPinPhaseSQL, phase, storeTime(at), changeID)
}

// setPinStaged records the staged prior package of a pin; its phase stays as it was.
func setPinStaged(ctx context.Context, db *store.DB, changeID, deb, sha string, at time.Time) error {
	return storeUpdate(ctx, db, "record the staged package of pin "+changeID, setPinStagedSQL, deb, sha, storeTime(at), changeID)
}

// setPinUnpinned records the unpin change that reversed a pin.
func setPinUnpinned(ctx context.Context, db *store.DB, changeID, by string, at time.Time) error {
	return storeUpdate(ctx, db, "record the unpin of pin "+changeID, setPinUnpinnedSQL, by, storeTime(at), changeID)
}

// addPinStep appends a step to a pin's log, numbered one past the pin's last step.
func addPinStep(ctx context.Context, db *store.DB, changeID, step, result string, at time.Time) error {
	err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, addPinStepSQL, changeID, changeID, step, result, storeTime(at))
		return err
	})
	if err != nil {
		return fmt.Errorf("tailscale: record a step of pin %s: %w", changeID, err)
	}
	return nil
}

// insertInstall records a package install the module made.
func insertInstall(ctx context.Context, tx *sql.Tx, changeID, version string, at time.Time) error {
	if _, err := tx.ExecContext(ctx, insertInstallSQL, changeID, version, storeTime(at)); err != nil {
		return fmt.Errorf("tailscale: record install %s: %w", changeID, err)
	}
	return nil
}

// liveInstall returns the newest install not yet removed; ok is false when there is none.
func liveInstall(ctx context.Context, q queryer) (changeID, version string, ok bool, err error) {
	err = q.QueryRowContext(ctx, liveInstallSQL).Scan(&changeID, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("tailscale: read the live install: %w", err)
	}
	return changeID, version, true, nil
}

// setInstallRemoved records that an install's package was removed.
func setInstallRemoved(ctx context.Context, db *store.DB, changeID string, at time.Time) error {
	return storeUpdate(ctx, db, "record the removal of install "+changeID, setInstallRemovedSQL, storeTime(at), changeID)
}

// storeUpdate runs one UPDATE in a write transaction and fails when it matched no row.
func storeUpdate(ctx context.Context, db *store.DB, what, query string, args ...any) error {
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
			return errors.New("no such row")
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("tailscale: %s: %w", what, err)
	}
	return nil
}
