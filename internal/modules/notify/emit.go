package notify

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
)

// dedupeWindow is how long an event's delivery on a channel holds back a
// later event with the same key on that channel.
const dedupeWindow = 10 * time.Minute

var severityRank = map[string]int{core.SeverityInfo: 0, core.SeverityWarning: 1, core.SeverityCritical: 2}

// notifier is the module's core.Notifier: it writes the outbox, and wakes
// the outbox's sender after each event it routes.
type notifier struct {
	clock core.Clock
	wake  chan struct{}
}

var _ core.Notifier = (*notifier)(nil)

func newNotifier(clock core.Clock) *notifier {
	if clock == nil {
		clock = core.SystemClock{}
	}
	return &notifier{clock: clock, wake: make(chan struct{}, 1)}
}

// Emit stores e and one pending delivery per enabled channel whose routing
// matches it, in tx. A change's key (change.failed, change.auto_reverted) is
// stored once: a repeat stores nothing and returns nil. A channel with a
// sent or pending delivery of an event with the same key from the last 10
// minutes gets no delivery.
func (n *notifier) Emit(ctx context.Context, tx *sql.Tx, e core.NotifyEvent) error {
	return n.emit(ctx, tx, e, "")
}

// emit is Emit, leaving out the channel skip.
func (n *notifier) emit(ctx context.Context, tx *sql.Tx, e core.NotifyEvent, skip string) error {
	if !slices.Contains(core.NotifyKinds(), e.Kind) {
		return fmt.Errorf("notify: unknown event kind %q", e.Kind)
	}
	if _, ok := severityRank[e.Severity]; !ok {
		return fmt.Errorf("notify: unknown severity %q", e.Severity)
	}
	if e.DedupeKey == "" {
		return fmt.Errorf("notify: a %s event has no dedupe key", e.Kind)
	}
	now := n.clock.Now().UTC()
	id, err := ulid.New(ulid.Timestamp(now), rand.Reader)
	if err != nil {
		return fmt.Errorf("notify: event id: %w", err)
	}
	at := now.Format(timeFormat)
	res, err := tx.ExecContext(ctx, `INSERT INTO notify_events (id, kind, severity, host_id, title, body, dedupe_key, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (dedupe_key) WHERE kind IN ('change.failed', 'change.auto_reverted') DO NOTHING`,
		id.String(), e.Kind, e.Severity, e.HostID, e.Title, e.Body, e.DedupeKey, at)
	if err != nil {
		return fmt.Errorf("notify: store the %s event: %w", e.Kind, err)
	}
	stored, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("notify: store the %s event: %w", e.Kind, err)
	}
	if stored == 0 {
		return nil
	}
	channels, err := routes(ctx, tx, e, skip)
	if err != nil {
		return err
	}
	since := now.Add(-dedupeWindow).Format(timeFormat)
	routed := false
	for _, ch := range channels {
		var held int
		err := tx.QueryRowContext(ctx, `SELECT count(*) FROM notify_deliveries d JOIN notify_events e ON e.id = d.event_id
			WHERE d.channel_id = ? AND d.state IN ('pending', 'sent') AND e.dedupe_key = ? AND e.created_at >= ? AND e.id <> ?`,
			ch, e.DedupeKey, since, id.String()).Scan(&held)
		if err != nil {
			return fmt.Errorf("notify: dedupe the %s event: %w", e.Kind, err)
		}
		if held > 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO notify_deliveries (event_id, channel_id, state, next_at, updated_at)
			VALUES (?, ?, 'pending', ?, ?)`, id.String(), ch, at, at); err != nil {
			return fmt.Errorf("notify: store a delivery of the %s event: %w", e.Kind, err)
		}
		routed = true
	}
	if routed {
		n.signal()
	}
	return nil
}

// signal wakes the sender without waiting; one wake covers every event
// emitted before the sender reads it.
func (n *notifier) signal() {
	select {
	case n.wake <- struct{}{}:
	default:
	}
}

// routes is the ids of the enabled channels, other than skip, whose minimum
// severity, kinds and host groups match e. A host-less event matches only
// channels with no group restriction.
func routes(ctx context.Context, tx *sql.Tx, e core.NotifyEvent, skip string) ([]string, error) {
	groups := map[string]bool{}
	if e.HostID != "" {
		rows, err := tx.QueryContext(ctx, `SELECT group_id FROM core_host_group_members WHERE host_id = ?`, e.HostID)
		if err != nil {
			return nil, fmt.Errorf("notify: read the groups of host %s: %w", e.HostID, err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var g string
			if err := rows.Scan(&g); err != nil {
				return nil, fmt.Errorf("notify: read the groups of host %s: %w", e.HostID, err)
			}
			groups[g] = true
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("notify: read the groups of host %s: %w", e.HostID, err)
		}
	}
	channels, err := loadChannels(ctx, tx, "")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, ch := range channels {
		if !ch.Enabled || ch.ID == skip || severityRank[e.Severity] < severityRank[ch.MinSeverity] ||
			len(ch.Kinds) > 0 && !slices.Contains(ch.Kinds, e.Kind) {
			continue
		}
		if len(ch.GroupIDs) > 0 && !slices.ContainsFunc(ch.GroupIDs, func(g string) bool { return groups[g] }) {
			continue
		}
		out = append(out, ch.ID)
	}
	return out, nil
}

// alertSubject is the alert an alert event's key names: the key without its
// kind and its final segment, the transition time. Keys are
// "alert.firing:<rule>:<host>:<subject>:<since>" and
// "alert.resolved:<rule>:<host>:<subject>:<since>", where <since> holds no
// colon, so the events of one alert share it whatever the subject holds.
// It is "" for another kind.
func alertSubject(kind, key string) string {
	if kind != core.NotifyAlertFiring && kind != core.NotifyAlertResolved {
		return ""
	}
	rest, ok := strings.CutPrefix(key, kind+":")
	if !ok {
		return key
	}
	if i := strings.LastIndexByte(rest, ':'); i >= 0 {
		return rest[:i]
	}
	return rest
}
