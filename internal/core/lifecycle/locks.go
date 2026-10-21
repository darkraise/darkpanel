package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// takeLocks gives every key to owner, or fails with lock.held when another
// owner has one or an update operation holds every lock. A key listed twice
// is taken once. With bump, which only a new change's intent sets, each
// key's epoch in core_lock_epochs goes up by one and the new epochs are
// returned, so the change can later tell whether a later change took one of
// its keys (spec 1.2, undo window). Without bump the result is nil.
func takeLocks(ctx context.Context, tx *sql.Tx, owner string, keys []string, bump bool) (map[string]int64, error) {
	if len(keys) > 0 {
		if err := notReserved(ctx, tx, owner, keys[0]); err != nil {
			return nil, err
		}
	}
	var epochs map[string]int64
	if bump {
		epochs = map[string]int64{}
	}
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO core_locks (key, owner) VALUES (?, ?) ON CONFLICT (key) DO NOTHING", key, owner); err != nil {
			return nil, err
		}
		var held string
		if err := tx.QueryRowContext(ctx, "SELECT owner FROM core_locks WHERE key = ?", key).Scan(&held); err != nil {
			return nil, err
		}
		if held != owner {
			return nil, errcode.New(errcode.LockHeld, fmt.Sprintf("lock %s is held by change %s", key, held))
		}
		if _, seen := epochs[key]; !bump || seen {
			continue
		}
		var epoch int64
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO core_lock_epochs (key, epoch) VALUES (?, 1)
			 ON CONFLICT (key) DO UPDATE SET epoch = epoch + 1 RETURNING epoch`, key).Scan(&epoch); err != nil {
			return nil, err
		}
		epochs[key] = epoch
	}
	return epochs, nil
}

// recordedEpochs returns the epochs a change's intent recorded. A change
// recorded before epochs existed has none.
func recordedEpochs(ctx context.Context, q queryer, changeID string) (map[string]int64, error) {
	var raw sql.NullString
	err := q.QueryRowContext(ctx, "SELECT lock_epochs FROM core_changes WHERE id = ?", changeID).Scan(&raw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, errcode.New(errcode.ChangeNotFound, fmt.Sprintf("no change %s", changeID))
	case err != nil:
		return nil, fmt.Errorf("lifecycle: read lock epochs of %s: %w", changeID, err)
	}
	epochs := map[string]int64{}
	if raw.Valid {
		if err := json.Unmarshal([]byte(raw.String), &epochs); err != nil {
			return nil, fmt.Errorf("lifecycle: read lock epochs of %s: %w", changeID, err)
		}
	}
	return epochs, nil
}

// giveBackEpochs sets each key of changeID whose epoch is still the one the
// change recorded back to that value minus one: the change left nothing
// behind, so the change before it on that key becomes undoable again. Call it
// in the transaction that makes the change reverted, or failed with nothing
// left (nothingLeft).
func giveBackEpochs(ctx context.Context, tx *sql.Tx, changeID string) error {
	epochs, err := recordedEpochs(ctx, tx, changeID)
	if err != nil {
		return err
	}
	for key, epoch := range epochs {
		if _, err := tx.ExecContext(ctx,
			"UPDATE core_lock_epochs SET epoch = epoch - 1 WHERE key = ? AND epoch = ?", key, epoch); err != nil {
			return fmt.Errorf("lifecycle: give back lock epoch %s: %w", key, err)
		}
	}
	return nil
}

// windowOpen returns the first key, in key order, whose epoch moved since
// changeID's intent recorded it, or "" while every key still has its epoch:
// the change's undo window is open.
func windowOpen(ctx context.Context, q queryer, changeID string) (string, error) {
	epochs, err := recordedEpochs(ctx, q, changeID)
	if err != nil {
		return "", err
	}
	keys := make([]string, 0, len(epochs))
	for key := range epochs {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		var cur int64
		err := q.QueryRowContext(ctx, "SELECT epoch FROM core_lock_epochs WHERE key = ?", key).Scan(&cur)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return key, nil
		case err != nil:
			return "", fmt.Errorf("lifecycle: read lock epoch %s: %w", key, err)
		case cur != epochs[key]:
			return key, nil
		}
	}
	return "", nil
}

// nothingLeft reports whether no effect is left that a change may still have
// made: none is intended, made, undoing or unresolved.
func nothingLeft(effects []core.Effect) bool {
	for _, eff := range effects {
		switch eff.State {
		case core.EffectIntended, core.EffectMade, core.EffectUndoing, core.EffectUnresolved:
			return false
		}
	}
	return true
}
