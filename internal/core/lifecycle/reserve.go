package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// ReserveAll is the lock key of an operation that excludes every keyed change:
// while a row with this key exists, no change of another owner can take any
// lock (ADR 0012). A change whose action declares no lock keys takes no lock
// and is not stopped, so an action that must not run during an update has to
// declare a key; core.NewRegistry enforces that for revertible agent actions,
// which could otherwise stay unconfirmed across an update's restart.
const ReserveAll = "*"

// ReservationOwner is the lock owner of an update operation. No change id
// starts with "op:", so a sweep of reservations never touches a change's lock.
func ReservationOwner(operationID string) string { return "op:" + operationID }

// rowQuerier is a *sql.DB or a *sql.Tx.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Reserve gives key to owner in a write transaction of its own, so it must
// not run inside another Write. It succeeds without change when owner already
// holds key and nothing else is locked. It fails with update.in_progress
// while another owner holds ReserveAll, and with update.unconfirmed_changes,
// naming the first held lock, its owner and that owner's state, while any
// other lock is held or a revertible change is applying or pending.
func Reserve(ctx context.Context, db *store.DB, key, owner string) error {
	err := db.Write(ctx, func(tx *sql.Tx) error {
		var holder string
		err := tx.QueryRowContext(ctx,
			"SELECT owner FROM core_locks WHERE key = ? AND owner <> ?", ReserveAll, owner).Scan(&holder)
		switch {
		case err == nil:
			return errcode.New(errcode.UpdateInProgress, fmt.Sprintf("update operation %s is in progress", holder))
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		var others, mine int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FILTER (WHERE NOT (key = ? AND owner = ?)), count(*) FILTER (WHERE key = ? AND owner = ?)
			 FROM core_locks`, key, owner, key, owner).Scan(&others, &mine); err != nil {
			return err
		}
		switch {
		case others > 0:
			detail, err := heldLock(ctx, tx, key, owner)
			if err != nil {
				return err
			}
			return errcode.New(errcode.UpdateUnconfirmedChanges, detail)
		case mine > 0:
			return nil
		}
		var unconfirmed int
		if err := tx.QueryRowContext(ctx,
			"SELECT count(*) FROM core_changes WHERE class = ? AND state IN (?, ?)",
			string(core.ClassRevertible), core.StateApplying, core.StatePending).Scan(&unconfirmed); err != nil {
			return err
		}
		if unconfirmed > 0 {
			return errcode.New(errcode.UpdateUnconfirmedChanges,
				"a revertible change is not confirmed yet; confirm or revert it first")
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO core_locks (key, owner) VALUES (?, ?)", key, owner)
		return err
	})
	var coded *errcode.Error
	switch {
	case errors.As(err, &coded):
		return err
	case err != nil:
		return fmt.Errorf("lifecycle: reserve %s for %s: %w", key, owner, err)
	}
	return nil
}

// heldLock names the first lock, by key, that another owner than owner of
// key holds, with its owning change and that change's state, for
// Reserve's refusal (1.3, locks across restarts and updates).
func heldLock(ctx context.Context, tx *sql.Tx, key, owner string) (string, error) {
	var held, holder, state string
	err := tx.QueryRowContext(ctx,
		`SELECT l.key, l.owner, coalesce(c.state, '') FROM core_locks l
		 LEFT JOIN core_changes c ON c.id = l.owner
		 WHERE NOT (l.key = ? AND l.owner = ?) ORDER BY l.key, l.owner LIMIT 1`, key, owner).
		Scan(&held, &holder, &state)
	if err != nil {
		return "", err
	}
	if state == "" {
		return fmt.Sprintf("lock %s is held by %s; wait for it to finish", held, holder), nil
	}
	return fmt.Sprintf("lock %s is held by change %s, which is %s; wait for it to finish", held, holder, state), nil
}

// Release deletes every lock row of owner.
func Release(ctx context.Context, db *store.DB, owner string) error {
	err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM core_locks WHERE owner = ?", owner)
		return err
	})
	if err != nil {
		return fmt.Errorf("lifecycle: release the locks of %s: %w", owner, err)
	}
	return nil
}

// notReserved fails with lock.held, naming key, when an owner other than
// owner holds ReserveAll.
func notReserved(ctx context.Context, q rowQuerier, owner, key string) error {
	var holder string
	err := q.QueryRowContext(ctx,
		"SELECT owner FROM core_locks WHERE key = ? AND owner <> ?", ReserveAll, owner).Scan(&holder)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("lifecycle: read lock %s: %w", ReserveAll, err)
	}
	return errcode.New(errcode.LockHeld,
		fmt.Sprintf("lock %s is unavailable while update operation %s holds every lock", key, holder))
}
