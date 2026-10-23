package listen

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// listenerTimeFormat is the fixed-width RFC 3339 UTC form of every stored
// time, so the text order of created_at is its time order.
const listenerTimeFormat = "2006-01-02T15:04:05.000000000Z"

const listenerColumns = `id, address, port, allowed_peer, client_header, public_host, change_id, state`

// insertListener records spec as listener id in state open.
func insertListener(ctx context.Context, tx *sql.Tx, id string, spec core.ListenerSpec, now time.Time) error {
	at := now.UTC().Format(listenerTimeFormat)
	_, err := tx.ExecContext(ctx, `INSERT INTO core_listeners (`+listenerColumns+`, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, spec.Addr.Addr().String(), int(spec.Addr.Port()), spec.AllowedPeer.String(), spec.ClientHeader,
		spec.PublicHost, spec.ChangeID, core.ListenerOpen, at, at)
	if err != nil {
		return fmt.Errorf("listen: record runtime listener %s: %w", id, err)
	}
	return nil
}

// setListenerState moves listener id to state when its state is one of
// from, and reports whether it did.
func setListenerState(ctx context.Context, tx *sql.Tx, id, state string, from []string, now time.Time) (bool, error) {
	if len(from) == 0 {
		return false, errors.New("listen: setListenerState needs at least one state to move from")
	}
	fromJSON, err := json.Marshal(from)
	if err != nil {
		return false, fmt.Errorf("listen: set runtime listener %s %s: %w", id, state, err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE core_listeners SET state = ?, updated_at = ?
		WHERE id = ? AND state IN (SELECT value FROM json_each(?))`,
		state, now.UTC().Format(listenerTimeFormat), id, string(fromJSON))
	if err != nil {
		return false, fmt.Errorf("listen: set runtime listener %s %s: %w", id, state, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("listen: set runtime listener %s %s: %w", id, state, err)
	}
	return n == 1, nil
}

// getListener reads listener id; errcode.NotFound when there is none.
func getListener(ctx context.Context, db *store.DB, id string) (core.ListenerInfo, error) {
	row := db.Read().QueryRowContext(ctx, `SELECT `+listenerColumns+` FROM core_listeners WHERE id = ?`, id)
	info, err := scanListener(row)
	if errors.Is(err, sql.ErrNoRows) {
		return core.ListenerInfo{}, errcode.New(errcode.NotFound, "no runtime listener "+id)
	}
	return info, err
}

// listListeners lists the listeners in state, oldest first.
func listListeners(ctx context.Context, db *store.DB, state string) ([]core.ListenerInfo, error) {
	rows, err := db.Read().QueryContext(ctx, `SELECT `+listenerColumns+` FROM core_listeners
		WHERE state = ? ORDER BY created_at, id`, state)
	if err != nil {
		return nil, fmt.Errorf("listen: list runtime listeners: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []core.ListenerInfo
	for rows.Next() {
		info, err := scanListener(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listen: list runtime listeners: %w", err)
	}
	return out, nil
}

type listenerScanner interface {
	Scan(dest ...any) error
}

func scanListener(row listenerScanner) (core.ListenerInfo, error) {
	var info core.ListenerInfo
	var addr, peer string
	var port int
	err := row.Scan(&info.ID, &addr, &port, &peer, &info.Spec.ClientHeader, &info.Spec.PublicHost,
		&info.Spec.ChangeID, &info.State)
	if errors.Is(err, sql.ErrNoRows) {
		return core.ListenerInfo{}, err
	}
	if err != nil {
		return core.ListenerInfo{}, fmt.Errorf("listen: read runtime listener: %w", err)
	}
	ip, err := netip.ParseAddr(addr)
	if err != nil || port < 1 || port > 65535 {
		return core.ListenerInfo{}, fmt.Errorf("listen: runtime listener %s has a bad address %q port %d", info.ID, addr, port)
	}
	info.Spec.Addr = netip.AddrPortFrom(ip, uint16(port)) //nolint:gosec // G115: checked to be within 1-65535
	if info.Spec.AllowedPeer, err = netip.ParseAddr(peer); err != nil {
		return core.ListenerInfo{}, fmt.Errorf("listen: runtime listener %s has a bad allowed peer %q", info.ID, peer)
	}
	return info, nil
}
