package npm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/core/store"
)

// loadUpgradeFailedFrom returns the phase the rollback of a change's upgrade failed in; it is empty for an upgrade
// that never failed and for one that failed before the phase was recorded. An unknown change id is an error.
func loadUpgradeFailedFrom(ctx context.Context, q queryer, changeID string) (string, error) {
	var from string
	err := q.QueryRowContext(ctx, "SELECT failed_from FROM npm_upgrades WHERE change_id = ?", changeID).Scan(&from)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("npm: read upgrade %s: no such upgrade", changeID)
	}
	if err != nil {
		return "", fmt.Errorf("npm: read upgrade %s: %w", changeID, err)
	}
	return from, nil
}

// setUpgradeFailed moves an upgrade to phase failed with result, recording from, the phase its rollback failed in.
// The stored archives are kept. An unknown change id is an error.
func setUpgradeFailed(ctx context.Context, db *store.DB, changeID, from, result string, at time.Time) error {
	err := db.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "UPDATE npm_upgrades SET phase = 'failed', failed_from = ?, result = ?, updated_at = ? WHERE change_id = ?",
			from, result, storeTime(at), changeID)
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

// upgradeArchivesInUse returns the archive files of every transaction that is neither done nor rolled back, as a
// set. A failed transaction still needs its archives.
func upgradeArchivesInUse(ctx context.Context, q queryer) (map[string]bool, error) {
	rows, err := q.QueryContext(ctx, "SELECT change_id, archives FROM npm_upgrades WHERE phase NOT IN ('done', 'rolled_back')")
	if err != nil {
		return nil, fmt.Errorf("npm: read the unfinished upgrades: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var (
			changeID, text string
			archives       []archiveRef
		)
		if err := rows.Scan(&changeID, &text); err != nil {
			return nil, fmt.Errorf("npm: read the unfinished upgrades: %w", err)
		}
		if err := json.Unmarshal([]byte(text), &archives); err != nil {
			return nil, fmt.Errorf("npm: decode the archives of upgrade %s: %w", changeID, err)
		}
		for _, a := range archives {
			out[a.File] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("npm: read the unfinished upgrades: %w", err)
	}
	return out, nil
}
