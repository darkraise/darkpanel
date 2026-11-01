package tailnet

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// enrollKeyGrace is how long after its expiry an enroll key's row may still
// wait for its host before the key is retired.
const enrollKeyGrace = 15 * time.Minute

// enrollKeyFacts is one enroll key's row with what the controller database
// knows of its host: hostState is not valid when the host row is gone, and
// tokenExpires is not valid when the host has no enrollment token.
type enrollKeyFacts struct {
	description   string
	requestedAt   time.Time
	expirySeconds int
	hostState     sql.NullString
	tokenExpires  sql.NullString
	tokenConsumed bool
}

// settleEnrollKeys moves each enroll key's row that is live or uncertain to
// used when its host has enrolled, and to retired when the host is gone, its
// enrollment token expired unconsumed, or the key is past its expiry plus
// enrollKeyGrace. The fleet code knows nothing of the tailnet, so the sweep
// reads the hosts and their tokens itself.
func settleEnrollKeys(ctx context.Context, db *store.DB, now time.Time) error {
	return db.Write(ctx, func(tx *sql.Tx) error {
		facts, err := enrollKeysToSettle(ctx, tx)
		if err != nil {
			return err
		}
		for _, f := range facts {
			state := enrollKeyState(f, now)
			if state == "" {
				continue
			}
			if err := setKeyState(ctx, tx, f.description, state, "", now); err != nil {
				return err
			}
		}
		return nil
	})
}

// enrollKeysToSettle reads every live or uncertain enroll key's row with its
// host and token. It closes its rows before returning, since the caller's
// writes share the transaction's one connection.
func enrollKeysToSettle(ctx context.Context, tx *sql.Tx) ([]enrollKeyFacts, error) {
	rows, err := tx.QueryContext(ctx, `SELECT k.description, k.requested_at, k.expiry_seconds, h.state, t.expires_at, t.consumed_at IS NOT NULL
		FROM tailnet_keys k
		LEFT JOIN core_hosts h ON h.id = k.owner_id
		LEFT JOIN core_enroll_tokens t ON t.host_id = k.owner_id
		WHERE k.kind = 'enroll' AND k.state IN ('live', 'uncertain')
		ORDER BY k.requested_at, k.description`)
	if err != nil {
		return nil, fmt.Errorf("tailnet: read enroll keys: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []enrollKeyFacts
	for rows.Next() {
		var (
			f         enrollKeyFacts
			requested string
			consumed  sql.NullBool
		)
		if err := rows.Scan(&f.description, &requested, &f.expirySeconds, &f.hostState, &f.tokenExpires, &consumed); err != nil {
			return nil, fmt.Errorf("tailnet: read enroll keys: %w", err)
		}
		if f.requestedAt, err = parseStoreTime(requested); err != nil {
			return nil, err
		}
		f.tokenConsumed = consumed.Bool
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tailnet: read enroll keys: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("tailnet: read enroll keys: %w", err)
	}
	return out, nil
}

// enrollKeyState is the state an enroll key's row moves to, or "" when it
// stays as it is. An enrolled host wins over every reason to retire.
func enrollKeyState(f enrollKeyFacts, now time.Time) string {
	switch {
	case f.hostState.Valid && f.hostState.String == core.HostEnrolled:
		return "used"
	case !f.hostState.Valid:
		return "retired"
	// Stored times share one fixed-width layout, so they compare as text.
	case f.tokenExpires.Valid && !f.tokenConsumed && f.tokenExpires.String <= formatStoreTime(now):
		return "retired"
	case now.After(f.requestedAt.Add(time.Duration(f.expirySeconds)*time.Second + enrollKeyGrace)):
		return "retired"
	}
	return ""
}
