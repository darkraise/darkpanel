package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// TailnetDevice is a Tailscale node its user typed a right code from; the
// user's next Continue-as from it asks no code.
type TailnetDevice struct {
	StableID     string    `json:"stableId" jsonschema_description:"Tailscale stable node id of the device."`
	NodeName     string    `json:"nodeName" jsonschema_description:"The node's MagicDNS name when it last signed in; empty when it has none."`
	RememberedAt time.Time `json:"rememberedAt" jsonschema_description:"When a right code first remembered the device."`
	LastUsedAt   time.Time `json:"lastUsedAt" jsonschema_description:"When the device last signed its user in."`
}

const deviceColumns = `stable_id, node_name, remembered_at, last_used_at`

type scanner interface{ Scan(dest ...any) error }

func scanDevice(sc scanner) (TailnetDevice, error) {
	var d TailnetDevice
	var remembered, used string
	if err := sc.Scan(&d.StableID, &d.NodeName, &remembered, &used); err != nil {
		return TailnetDevice{}, err
	}
	var err error
	if d.RememberedAt, err = parseTime(remembered); err != nil {
		return TailnetDevice{}, err
	}
	if d.LastUsedAt, err = parseTime(used); err != nil {
		return TailnetDevice{}, err
	}
	return d, nil
}

func errDeviceNotFound() *errcode.Error { return errcode.New(errcode.NotFound, "device not found") }

// ListTailnetDevices returns username's remembered devices, the most recently
// used first; none is an empty, non-nil slice.
func (s *Service) ListTailnetDevices(ctx context.Context, username string) ([]TailnetDevice, error) {
	r, err := loadUser(ctx, s.db.Read(), username)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Read().QueryContext(ctx, `SELECT `+deviceColumns+` FROM core_user_tailnet_devices
		WHERE user_id = ? ORDER BY last_used_at DESC, stable_id`, r.ID)
	if err != nil {
		return nil, fmt.Errorf("auth: list tailnet devices: %w", err)
	}
	defer func() { _ = rows.Close() }()
	devices := []TailnetDevice{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, fmt.Errorf("auth: list tailnet devices: %w", err)
		}
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: list tailnet devices: %w", err)
	}
	return devices, nil
}

// ForgetTailnetDevice deletes username's device stableID and returns it as it
// was; the next Continue-as from that node asks the code again. Sessions it
// opened stay. No such device of username's is not_found.
func (s *Service) ForgetTailnetDevice(ctx context.Context, username, stableID string) (TailnetDevice, error) {
	var d TailnetDevice
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		r, err := loadUser(ctx, tx, username)
		if err != nil {
			return err
		}
		d, err = scanDevice(tx.QueryRowContext(ctx, `SELECT `+deviceColumns+` FROM core_user_tailnet_devices
			WHERE user_id = ? AND stable_id = ?`, r.ID, stableID))
		if errors.Is(err, sql.ErrNoRows) {
			return errDeviceNotFound()
		}
		if err != nil {
			return fmt.Errorf("auth: forget tailnet device: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM core_user_tailnet_devices WHERE user_id = ? AND stable_id = ?`,
			r.ID, stableID); err != nil {
			return fmt.Errorf("auth: forget tailnet device: %w", err)
		}
		return nil
	})
	if err != nil {
		return TailnetDevice{}, err
	}
	return d, nil
}

// TailnetDeviceExists reports whether username still has device stableID.
func (s *Service) TailnetDeviceExists(ctx context.Context, username, stableID string) (bool, error) {
	r, err := loadUser(ctx, s.db.Read(), username)
	if err != nil {
		return false, err
	}
	var n int
	if err := s.db.Read().QueryRowContext(ctx, `SELECT count(*) FROM core_user_tailnet_devices
		WHERE user_id = ? AND stable_id = ?`, r.ID, stableID).Scan(&n); err != nil {
		return false, fmt.Errorf("auth: tailnet device exists: %w", err)
	}
	return n > 0, nil
}

// forgetTailnetDevices deletes all of userID's devices, in the transaction
// that changes what a right code or a tailnet login proves.
func forgetTailnetDevices(ctx context.Context, tx *sql.Tx, userID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM core_user_tailnet_devices WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("auth: forget tailnet devices: %w", err)
	}
	return nil
}
