// Package quarantine reads the controller's restore-quarantine flag (spec
// 10): a restored controller database starts with the flag set, and every
// automatic mutation source and every action apply stay suspended until
// the operator releases it. Restore sets the flag and release clears it;
// this package only reads it.
package quarantine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// MetaKey is the flag's core_meta key in controller.db.
const MetaKey = "restore_quarantine"

// Marker is the flag's value.
type Marker struct {
	Tx              string    `json:"tx"`
	RestoredAt      time.Time `json:"restoredAt"`
	BackupCreatedAt time.Time `json:"backupCreatedAt"`
}

// Read returns the marker and whether the flag is set. A set flag whose
// value does not decode is an error, never an unset flag.
func Read(ctx context.Context, db *store.DB) (Marker, bool, error) {
	raw, ok, err := value(ctx, db)
	if err != nil || !ok {
		return Marker{}, ok, err
	}
	var m Marker
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return Marker{}, true, fmt.Errorf("quarantine: decode core_meta %s: %w", MetaKey, err)
	}
	return m, true, nil
}

// Active reports whether the flag is set, whatever its value holds.
func Active(ctx context.Context, db *store.DB) (bool, error) {
	_, ok, err := value(ctx, db)
	return ok, err
}

// Check returns errcode.Quarantined while the flag is set, and the read's
// error when the flag cannot be read: a gate must not open on a failed read.
func Check(ctx context.Context, db *store.DB) error {
	ok, err := Active(ctx, db)
	switch {
	case err != nil:
		return err
	case ok:
		return errcode.New(errcode.Quarantined,
			"the controller runs in restore quarantine; resolve the quarantined records and release the quarantine with darkpanel controller quarantine")
	}
	return nil
}

func value(ctx context.Context, db *store.DB) (string, bool, error) {
	var raw string
	err := db.Read().QueryRowContext(ctx, `SELECT value FROM core_meta WHERE key = ?`, MetaKey).Scan(&raw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("quarantine: read core_meta %s: %w", MetaKey, err)
	}
	return raw, true, nil
}
