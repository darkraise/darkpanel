package notify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/quarantine"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/notify/send"
)

const (
	// maxSending bounds the sends running at once, across channels.
	maxSending = 4
	// pollInterval is how often the sender looks for due deliveries when
	// nothing wakes it; a wake can come before its event commits.
	pollInterval = 5 * time.Second
)

// The last_error of a delivery the sender settled without sending.
const (
	reasonRestored = "restored"
	reasonDisabled = "disabled"
)

// retryOffsets are when, after its event was created, a failing delivery is
// tried again: 1, 5, 15 and 60 minutes, then every hour until 24 hours.
var retryOffsets = func() []time.Duration {
	out := []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour}
	for h := 2; h <= 24; h++ {
		out = append(out, time.Duration(h)*time.Hour)
	}
	return out
}()

// outboxConfig configures newOutbox.
type outboxConfig struct {
	DB        *store.DB
	Sealer    core.Sealer
	Clock     core.Clock
	Log       *slog.Logger
	Send      send.Sender
	Notifier  *notifier                               // wakes the sender; emits the failure notices
	Suspended func(ctx context.Context) (bool, error) // nil: restore quarantine
}

// outbox sends the due deliveries of the outbox.
type outbox struct {
	cfg       outboxConfig
	mu        sync.Mutex
	inFlight  map[deliveryKey]bool
	held      map[deliveryKey]time.Time // a delivery whose attempt could not be recorded, until its next retry offset
	wg        sync.WaitGroup
	lastPurge time.Time
}

type deliveryKey struct{ event, channel string }

// due is a pending delivery whose next attempt is due.
type due struct {
	key                                       deliveryKey
	kind, severity, hostID, title, body, dkey string
	created                                   time.Time
}

func newOutbox(cfg outboxConfig) *outbox {
	if cfg.Clock == nil {
		cfg.Clock = core.SystemClock{}
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.Notifier == nil {
		cfg.Notifier = newNotifier(cfg.Clock)
	}
	if cfg.Suspended == nil {
		db := cfg.DB
		cfg.Suspended = func(ctx context.Context) (bool, error) { return quarantine.Active(ctx, db) }
	}
	return &outbox{cfg: cfg, inFlight: map[deliveryKey]bool{}, held: map[deliveryKey]time.Time{}}
}

func (o *outbox) service() ext.Service {
	return ext.Service{Name: ID + ".sender", Run: o.Run}
}

// Run marks what a restore left pending, then sends due deliveries until ctx
// ends, after each wake and every pollInterval. It returns once its sends
// have.
func (o *outbox) Run(ctx context.Context) error {
	defer o.wait()
	if err := o.restore(ctx); err != nil {
		return err
	}
	for {
		o.pass(ctx)
		fired := make(chan struct{})
		t := o.cfg.Clock.AfterFunc(pollInterval, func() { close(fired) })
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-o.cfg.Notifier.wake:
		case <-fired:
		}
		t.Stop()
	}
}

// wait returns once every send started has finished.
func (o *outbox) wait() { o.wg.Wait() }

// pass starts the due deliveries that may go, up to maxSending running at
// once, and returns how many deliveries it started or settled. It does
// nothing while the outbox is suspended, or when it cannot tell.
func (o *outbox) pass(ctx context.Context) int {
	suspended, err := o.cfg.Suspended(ctx)
	if err != nil {
		if ctx.Err() == nil {
			o.cfg.Log.Warn("notify: cannot tell whether restore quarantine suspends the sender; sending nothing", "error", err)
		}
		return 0
	}
	if suspended {
		return 0
	}
	o.purgeIfDue(ctx)
	now := o.cfg.Clock.Now()
	o.mu.Lock()
	free := maxSending - len(o.inFlight)
	for k, until := range o.held {
		if !now.Before(until) {
			delete(o.held, k)
		}
	}
	o.mu.Unlock()
	if free <= 0 {
		return 0
	}
	list, heads, channels, err := o.dueDeliveries(ctx)
	if err != nil {
		if ctx.Err() == nil {
			o.cfg.Log.Warn("notify: cannot read the due deliveries", "error", err)
		}
		return 0
	}
	progress := 0
	for _, d := range list {
		if free == 0 {
			break
		}
		o.mu.Lock()
		running := o.inFlight[d.key]
		_, held := o.held[d.key]
		o.mu.Unlock()
		if running || held {
			continue
		}
		// One alert's events go out in order on a channel: only the oldest
		// pending one may be sent.
		if subject := alertSubject(d.kind, d.dkey); subject != "" && heads[alertHead{d.key.channel, subject}] != d.key.event {
			continue
		}
		ch, ok := channels[d.key.channel]
		if !ok {
			continue
		}
		if !ch.Enabled {
			if err := o.record(context.WithoutCancel(ctx), d, ch, reasonDisabled, false); err != nil {
				o.cfg.Log.Warn("notify: cannot settle a delivery to a disabled channel", "channel", ch.ID, "error", err)
				continue
			}
			progress++
			continue
		}
		o.mu.Lock()
		o.inFlight[d.key] = true
		o.mu.Unlock()
		o.wg.Add(1)
		go o.deliver(ctx, d, ch)
		free--
		progress++
	}
	return progress
}

type alertHead struct{ channel, subject string }

// dueDeliveries reads the due pending deliveries, oldest event first; the
// oldest pending event of each alert on each channel; and the channels.
// It reads every due delivery, since alert events held behind their alert's
// oldest one would otherwise fill a page and starve the rest; a delivery is
// pending for 24 hours at most.
func (o *outbox) dueDeliveries(ctx context.Context) ([]due, map[alertHead]string, map[string]channelRow, error) {
	db := o.cfg.DB.Read()
	now := o.cfg.Clock.Now().UTC().Format(timeFormat)
	rows, err := db.QueryContext(ctx, `SELECT d.event_id, d.channel_id, e.kind, e.severity, e.host_id, e.title, e.body, e.dedupe_key, e.created_at
		FROM notify_deliveries d JOIN notify_events e ON e.id = d.event_id
		WHERE d.state = 'pending' AND d.next_at <= ?
		ORDER BY e.created_at, e.rowid, d.channel_id`, now)
	if err != nil {
		return nil, nil, nil, err
	}
	var list []due
	for rows.Next() {
		var d due
		var created string
		if err := rows.Scan(&d.key.event, &d.key.channel, &d.kind, &d.severity, &d.hostID, &d.title, &d.body, &d.dkey, &created); err != nil {
			_ = rows.Close()
			return nil, nil, nil, err
		}
		if d.created, err = time.Parse(timeFormat, created); err != nil {
			_ = rows.Close()
			return nil, nil, nil, fmt.Errorf("event %s created_at: %w", d.key.event, err)
		}
		list = append(list, d)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil || len(list) == 0 {
		return nil, nil, nil, err
	}
	heads, err := alertHeads(ctx, db)
	if err != nil {
		return nil, nil, nil, err
	}
	all, err := loadChannels(ctx, db, "")
	if err != nil {
		return nil, nil, nil, err
	}
	channels := make(map[string]channelRow, len(all))
	for _, ch := range all {
		channels[ch.ID] = ch
	}
	return list, heads, channels, nil
}

// alertHeads is the oldest pending alert event of each alert on each
// channel, due or not.
func alertHeads(ctx context.Context, db *sql.DB) (map[alertHead]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT d.channel_id, e.id, e.kind, e.dedupe_key
		FROM notify_deliveries d JOIN notify_events e ON e.id = d.event_id
		WHERE d.state = 'pending' AND e.kind IN ('alert.firing', 'alert.resolved')
		ORDER BY e.created_at, e.rowid`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	heads := map[alertHead]string{}
	for rows.Next() {
		var channel, id, kind, key string
		if err := rows.Scan(&channel, &id, &kind, &key); err != nil {
			return nil, err
		}
		h := alertHead{channel, alertSubject(kind, key)}
		if _, ok := heads[h]; !ok {
			heads[h] = id
		}
	}
	return heads, rows.Err()
}

// deliver makes one attempt of d on ch and records it. An attempt cut short
// by the sender stopping is not recorded, so the delivery stays due. An
// attempt whose record fails is held until its next retry offset, and does
// not wake the sender, so a failing database does not resend it at once.
func (o *outbox) deliver(ctx context.Context, d due, ch channelRow) {
	hold := false
	defer func() {
		o.mu.Lock()
		delete(o.inFlight, d.key)
		if hold {
			now := o.cfg.Clock.Now()
			until, ok := nextAttempt(d.created, now)
			if !ok {
				until = now.Add(time.Hour)
			}
			o.held[d.key] = until
		}
		o.mu.Unlock()
		o.wg.Done()
		if !hold {
			o.cfg.Notifier.signal()
		}
	}()
	var code string
	var sendErr error
	secrets, err := openSecrets(core.Env{Sealer: o.cfg.Sealer}, ch)
	if err != nil {
		o.cfg.Log.Warn("notify: a channel's write-only values do not open; its delivery fails", "channel", ch.ID)
		code, sendErr = send.CodeInvalid, errors.New("notify: the channel's write-only values do not open")
	} else {
		code, sendErr = o.cfg.Send.Send(ctx, sendChannel(ch, secrets), send.Event{ID: d.key.event, Kind: d.kind,
			Severity: d.severity, HostID: d.hostID, Title: d.title, Body: d.body, CreatedAt: d.created})
	}
	if sendErr != nil && ctx.Err() != nil {
		return
	}
	if err := o.record(context.WithoutCancel(ctx), d, ch, code, sendErr == nil); err != nil {
		o.cfg.Log.Warn("notify: cannot record a delivery attempt; holding it until its next retry", "channel", ch.ID, "event", d.key.event, "error", err)
		hold = true
	}
}

// record stores the outcome of an attempt of d on ch, or of settling it
// without one (code reasonDisabled): sent, pending until its next retry, or
// failed. A delivery marked failed by an attempt notifies the other channels
// once per failure streak.
func (o *outbox) record(ctx context.Context, d due, ch channelRow, code string, delivered bool) error {
	now := o.cfg.Clock.Now().UTC()
	state, next, lastErr, attempt := "sent", "", "", 1
	switch {
	case code == reasonDisabled:
		state, lastErr, attempt = "failed", reasonDisabled, 0
	case !delivered:
		state, lastErr = "failed", code
		if at, ok := nextAttempt(d.created, now); ok && retryable(ch.Kind, code) {
			state, next = "pending", at.Format(timeFormat)
		}
		o.cfg.Log.Info("notify: a delivery attempt failed", "channel", ch.ID, "kind", ch.Kind, "code", code, "state", state)
	}
	return o.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE notify_deliveries SET state = ?, attempts = attempts + ?, next_at = ?, last_error = ?, updated_at = ?
			WHERE event_id = ? AND channel_id = ? AND state = 'pending'`,
			state, attempt, next, lastErr, now.Format(timeFormat), d.key.event, d.key.channel)
		if err != nil {
			return err
		}
		// Gone (a channel delete) or settled meanwhile (a restore).
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return err
		}
		if state != "failed" || lastErr == reasonDisabled {
			return nil
		}
		return o.notifyFailure(ctx, tx, d, ch, code)
	})
}

// notifyFailure emits notify.delivery_failed for ch to the other channels,
// unless d is itself a failure notice or a test, or a notice for ch was
// emitted since ch last delivered.
func (o *outbox) notifyFailure(ctx context.Context, tx *sql.Tx, d due, ch channelRow, code string) error {
	if d.kind == core.NotifyDeliveryFailed || d.kind == core.NotifyTest {
		return nil
	}
	key := core.NotifyDeliveryFailed + ":" + ch.ID
	var notified int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM notify_events WHERE kind = ? AND dedupe_key = ? AND created_at >
		coalesce((SELECT max(updated_at) FROM notify_deliveries WHERE channel_id = ? AND state = 'sent'), '')`,
		core.NotifyDeliveryFailed, key, ch.ID).Scan(&notified)
	if err != nil {
		return fmt.Errorf("notify: read the failure streak of channel %s: %w", ch.ID, err)
	}
	if notified > 0 {
		return nil
	}
	return o.cfg.Notifier.emit(ctx, tx, core.NotifyEvent{
		Kind: core.NotifyDeliveryFailed, Severity: core.SeverityWarning, DedupeKey: key,
		Title: "Notification channel " + ch.Name + " is failing",
		Body:  "A notification on channel " + ch.Name + " (" + ch.Kind + ") was not delivered. Last result: " + code + ".",
	}, ch.ID)
}

// nextAttempt is the first retry offset after now of an event created at
// created, or false when the 24 hours are over.
func nextAttempt(created, now time.Time) (time.Time, bool) {
	for _, off := range retryOffsets {
		if at := created.Add(off); at.After(now) {
			return at, true
		}
	}
	return time.Time{}, false
}

// retryable reports whether a failed attempt on a channel of kind may
// succeed later: a timeout, a network or TLS failure, an HTTP 5xx, 408 or
// 429, or an SMTP 4xx reply. A redirect, another HTTP 4xx, an SMTP 5xx
// reply or an invalid request fails at once.
func retryable(kind, code string) bool {
	switch code {
	case send.CodeTimeout, send.CodeNetwork, send.CodeTLS:
		return true
	}
	n, err := strconv.Atoi(code)
	if err != nil {
		return false
	}
	if kind == kindEmail {
		return n >= 400 && n < 500
	}
	return n >= 500 || n == 408 || n == 429
}
