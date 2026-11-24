package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/quarantine"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/notify/send"
)

// sendCall is one attempt the fake sender saw.
type sendCall struct {
	Channel, Event, Kind, Key string
	At                        time.Time
}

// fakeSender records every attempt. answer gives the code and error of an
// attempt (nil: "200"); with gate set, an attempt waits for a value on it,
// or for its context to end.
type fakeSender struct {
	clock         *coretest.FakeClock
	mu            sync.Mutex
	calls         []sendCall
	answer        func(ch send.Channel, ev send.Event) (string, error)
	gate          chan struct{}
	started       chan struct{}
	running, peak int
}

func (f *fakeSender) Send(ctx context.Context, ch send.Channel, ev send.Event) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, sendCall{Channel: ch.ID, Event: ev.ID, Kind: ev.Kind, At: f.clock.Now()})
	f.running++
	f.peak = max(f.peak, f.running)
	answer, gate, started := f.answer, f.gate, f.started
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.running--
		f.mu.Unlock()
	}()
	if started != nil {
		started <- struct{}{}
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return send.CodeNetwork, ctx.Err()
		}
	}
	if answer == nil {
		return "200", nil
	}
	return answer(ch, ev)
}

func (f *fakeSender) setAnswer(a func(ch send.Channel, ev send.Event) (string, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answer = a
}

// callsOf is the attempts on channel (every channel when "") of the events
// whose id is in events (every event when empty).
func (f *fakeSender) callsOf(channel string, events ...string) []sendCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []sendCall
	for _, c := range f.calls {
		if (channel == "" || c.Channel == channel) && (len(events) == 0 || slices.Contains(events, c.Event)) {
			out = append(out, c)
		}
	}
	return out
}

// answerBy answers by channel id: the code of the channel, "200" for any
// other; a numeric code below 300 is a delivery.
func answerBy(codes map[string]string) func(ch send.Channel, ev send.Event) (string, error) {
	return func(ch send.Channel, _ send.Event) (string, error) {
		code, ok := codes[ch.ID]
		if !ok {
			return "200", nil
		}
		if n, err := strconv.Atoi(code); err == nil && n < 300 {
			return code, nil
		}
		return code, errors.New("send: refused " + code)
	}
}

// outboxEnv is an env with a notifier and an outbox over a fake sender.
type outboxEnv struct {
	*env
	n    *notifier
	fake *fakeSender
	o    *outbox
	log  *slog.Logger
}

func newOutboxEnv(t *testing.T) *outboxEnv {
	t.Helper()
	e := newEnv(t, nil)
	x := &outboxEnv{env: e, n: newNotifier(e.clock), fake: &fakeSender{clock: e.clock},
		log: slog.New(slog.NewTextHandler(e.logs, nil))}
	x.o = x.newOutbox(e.db)
	return x
}

func (x *outboxEnv) newOutbox(db *store.DB) *outbox {
	return newOutbox(outboxConfig{DB: db, Sealer: coretest.Sealer{}, Clock: x.clock, Log: x.log, Send: x.fake, Notifier: x.n})
}

// emit emits ev in its own transaction and returns the id of the event it
// stored, or "" when it stored none.
func (x *outboxEnv) emit(ev core.NotifyEvent) string {
	x.t.Helper()
	ctx := context.Background()
	var before int
	if err := x.db.Read().QueryRow(`SELECT count(*) FROM notify_events`).Scan(&before); err != nil {
		x.t.Fatal(err)
	}
	if err := x.db.Write(ctx, func(tx *sql.Tx) error { return x.n.Emit(ctx, tx, ev) }); err != nil {
		x.t.Fatalf("emit %+v: %v", ev, err)
	}
	var id string
	err := x.db.Read().QueryRow(`SELECT id FROM notify_events ORDER BY rowid DESC LIMIT 1 OFFSET 0`).Scan(&id)
	if err != nil || x.count("notify_events") == before {
		return ""
	}
	return id
}

// drain waits for the running sends and runs passes until one starts no
// send, waiting for each pass's sends.
func (x *outboxEnv) drain(o *outbox) {
	x.t.Helper()
	for range 100 {
		o.wait()
		if o.pass(context.Background()) == 0 {
			o.wait()
			return
		}
	}
	x.t.Fatal("the outbox kept starting sends")
}

// nextDue is the earliest next_at after now of a pending delivery, or
// false. A delivery due already that a drain left pending waits for another.
func (x *outboxEnv) nextDue() (time.Time, bool) {
	x.t.Helper()
	var at sql.NullString
	if err := x.db.Read().QueryRow(`SELECT min(next_at) FROM notify_deliveries WHERE state = 'pending' AND next_at > ?`,
		x.clock.Now().UTC().Format(timeFormat)).Scan(&at); err != nil {
		x.t.Fatal(err)
	}
	if !at.Valid {
		return time.Time{}, false
	}
	ts, err := time.Parse(timeFormat, at.String)
	if err != nil {
		x.t.Fatal(err)
	}
	return ts, true
}

// runUntilSettled drains, then advances the clock to each next due delivery
// and drains again, until nothing is pending or the clock passes end.
func (x *outboxEnv) runUntilSettled(o *outbox, end time.Time) {
	x.t.Helper()
	x.drain(o)
	for {
		at, ok := x.nextDue()
		if !ok || at.After(end) {
			return
		}
		x.clock.Advance(at.Sub(x.clock.Now()))
		x.drain(o)
	}
}

type deliveryRow struct {
	State, LastError string
	Attempts         int
}

// delivery is the delivery of event on channel; ok is false when there is
// none.
func (x *outboxEnv) delivery(event, channel string) (deliveryRow, bool) {
	x.t.Helper()
	var d deliveryRow
	err := x.db.Read().QueryRow(`SELECT state, last_error, attempts FROM notify_deliveries WHERE event_id = ? AND channel_id = ?`,
		event, channel).Scan(&d.State, &d.LastError, &d.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return d, false
	}
	if err != nil {
		x.t.Fatal(err)
	}
	return d, true
}

// recipients is the channel ids event has a delivery to, sorted.
func (x *outboxEnv) recipients(event string) []string {
	x.t.Helper()
	rows, err := x.db.Read().Query(`SELECT channel_id FROM notify_deliveries WHERE event_id = ? ORDER BY channel_id`, event)
	if err != nil {
		x.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			x.t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

func (x *outboxEnv) webhook(name string, mut func(in *ChannelInput)) string {
	x.t.Helper()
	in := ChannelInput{Kind: "webhook", Name: name, MinSeverity: "info", Webhook: &WebhookInput{URL: "https://hooks.example.com/" + name}}
	if mut != nil {
		mut(&in)
	}
	var c Channel
	x.run("notify.channel.create", in, &c)
	return c.ID
}

func (x *outboxEnv) disable(id, name string) {
	x.t.Helper()
	x.run("notify.channel.update", ChannelUpdateInput{ID: id, Name: name, MinSeverity: "info", Enabled: false, Webhook: &WebhookInput{}}, nil)
}

func (x *outboxEnv) exec(query string, args ...any) {
	x.t.Helper()
	if _, err := x.db.Writer().Exec(query, args...); err != nil {
		x.t.Fatal(err)
	}
}

func rolloutEvent(key string) core.NotifyEvent {
	return core.NotifyEvent{Kind: core.NotifyRolloutFailed, Severity: core.SeverityCritical, Title: "Rollout failed",
		Body: "A host failed its update.", DedupeKey: key}
}

func minutes(calls []sendCall) []int {
	out := make([]int, len(calls))
	for i, c := range calls {
		out[i] = int(c.At.Sub(t0) / time.Minute)
	}
	return out
}

func TestRetrySchedule(t *testing.T) {
	x := newOutboxEnv(t)
	five, network, smtp4 := x.webhook("five", nil), x.webhook("network", nil), ""
	refused, smtp5 := x.webhook("refused", nil), ""
	for _, c := range []struct {
		name string
		to   *string
	}{{"smtp-busy", &smtp4}, {"smtp-refused", &smtp5}} {
		var ch Channel
		x.run("notify.channel.create", ChannelInput{Kind: "email", Name: c.name, MinSeverity: "info", Email: &EmailInput{
			Host: "127.0.0.1", Port: 2525, Security: "none", From: "p@example.com", Recipients: []string{"a@example.com"}}}, &ch)
		*c.to = ch.ID
	}
	x.fake.setAnswer(answerBy(map[string]string{five: "503", network: send.CodeNetwork, smtp4: "421", refused: "404", smtp5: "550"}))

	ev := x.emit(rolloutEvent("rollout.failed:r1:h1"))
	x.runUntilSettled(x.o, t0.Add(48*time.Hour))

	want := []int{0, 1, 5, 15, 60}
	for h := 2; h <= 24; h++ {
		want = append(want, h*60)
	}
	for _, c := range []struct{ name, id, code string }{{"503", five, "503"}, {"network", network, send.CodeNetwork}, {"SMTP 421", smtp4, "421"}} {
		if got := minutes(x.fake.callsOf(c.id, ev)); !slices.Equal(got, want) {
			t.Errorf("%s: attempts at minutes %v, want %v", c.name, got, want)
		}
		if d, _ := x.delivery(ev, c.id); d != (deliveryRow{State: "failed", LastError: c.code, Attempts: len(want)}) {
			t.Errorf("%s: delivery %+v, want failed after %d attempts", c.name, d, len(want))
		}
	}
	for _, c := range []struct{ name, id, code string }{{"HTTP 404", refused, "404"}, {"SMTP 550", smtp5, "550"}} {
		if got := minutes(x.fake.callsOf(c.id, ev)); !slices.Equal(got, []int{0}) {
			t.Errorf("%s: attempts at minutes %v, want one at 0", c.name, got)
		}
		if d, _ := x.delivery(ev, c.id); d != (deliveryRow{State: "failed", LastError: c.code, Attempts: 1}) {
			t.Errorf("%s: delivery %+v, want failed after one attempt", c.name, d)
		}
	}

	t.Run("at most 4 at once", func(t *testing.T) {
		x := newOutboxEnv(t)
		hook := x.webhook("hook", nil)
		x.fake.gate, x.fake.started = make(chan struct{}), make(chan struct{}, 10)
		var events []string
		for i := range 6 {
			events = append(events, x.emit(rolloutEvent("rollout.failed:r"+strconv.Itoa(i))))
		}
		if n := x.o.pass(context.Background()); n != 4 {
			t.Errorf("the first pass started %d sends, want 4", n)
		}
		for range 4 {
			<-x.fake.started
		}
		if n := x.o.pass(context.Background()); n != 0 {
			t.Errorf("a pass with 4 sends running started %d more", n)
		}
		x.fake.gate <- struct{}{}
		deadline := time.Now().Add(5 * time.Second)
		for len(x.fake.callsOf(hook, events...)) < 5 && time.Now().Before(deadline) {
			x.o.pass(context.Background())
			time.Sleep(time.Millisecond)
		}
		close(x.fake.gate)
		x.drain(x.o)
		if got := len(x.fake.callsOf(hook, events...)); got != 6 || x.fake.peak != 4 {
			t.Errorf("%d sends, at most %d at once; want 6, at most 4", got, x.fake.peak)
		}
		for _, ev := range events {
			if d, _ := x.delivery(ev, hook); d.State != "sent" || d.Attempts != 1 {
				t.Errorf("delivery of %s = %+v, want sent once", ev, d)
			}
		}
	})
}

func TestFailedRecordHoldsTheDelivery(t *testing.T) {
	x := newOutboxEnv(t)
	hook := x.webhook("hook", nil)
	ev := x.emit(rolloutEvent("rollout.failed:r"))
	x.exec(`CREATE TRIGGER notify_test_fail BEFORE UPDATE ON notify_deliveries BEGIN SELECT RAISE(ABORT, 'disk full'); END`)
	select {
	case <-x.n.wake:
	default:
	}
	x.drain(x.o)
	if got := len(x.fake.callsOf(hook, ev)); got != 1 {
		t.Fatalf("%d sends before the record failed, want 1", got)
	}
	if len(x.n.wake) != 0 {
		t.Error("a failed record woke the sender")
	}
	x.clock.Advance(30 * time.Second)
	x.drain(x.o)
	if got := len(x.fake.callsOf(hook, ev)); got != 1 {
		t.Errorf("%d sends while the record keeps failing, want 1 until the next retry offset", got)
	}
	x.exec(`DROP TRIGGER notify_test_fail`)
	x.clock.Advance(30 * time.Second)
	x.drain(x.o)
	if got := minutes(x.fake.callsOf(hook, ev)); !slices.Equal(got, []int{0, 1}) {
		t.Errorf("sends at minutes %v, want 0 and 1", got)
	}
	if d, _ := x.delivery(ev, hook); d.State != "sent" || d.Attempts != 1 {
		t.Errorf("delivery %+v, want sent with the recorded attempt", d)
	}
}

func TestDeliveriesSurviveRestart(t *testing.T) {
	x := newOutboxEnv(t)
	hook := x.webhook("hook", nil)
	x.fake.gate, x.fake.started = make(chan struct{}), make(chan struct{}, 10)

	// The first service stops while its send of the event is in flight.
	ev := x.emit(rolloutEvent("rollout.failed:r1"))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- x.o.Run(ctx) }()
	select {
	case <-x.fake.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the first service did not start the send")
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
	if d, _ := x.delivery(ev, hook); d != (deliveryRow{State: "pending"}) {
		t.Fatalf("after the stop the delivery is %+v, want pending with no attempt counted", d)
	}
	// An event emitted while no service runs.
	ev2 := x.emit(rolloutEvent("rollout.failed:r2"))

	// A new service on the same database file.
	if err := x.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), x.db.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	x.db = db
	x.fake.gate, x.fake.started = nil, nil
	o := x.newOutbox(db)
	before := len(x.fake.callsOf(hook, ev))
	x.drain(o)
	x.clock.Advance(2 * time.Hour)
	x.drain(o)
	if got := len(x.fake.callsOf(hook, ev)) - before; got != 1 {
		t.Errorf("the new service sent the first event %d times, want once", got)
	}
	if got := len(x.fake.callsOf(hook, ev2)); got != 1 {
		t.Errorf("the new service sent the second event %d times, want once", got)
	}
	for _, id := range []string{ev, ev2} {
		if d, _ := x.delivery(id, hook); d.State != "sent" || d.Attempts != 1 {
			t.Errorf("delivery of %s = %+v, want sent after one attempt", id, d)
		}
	}
}

func TestRoutingAndDedupe(t *testing.T) {
	x := newOutboxEnv(t)
	ts := t0.Format(timeFormat)
	x.exec(`INSERT INTO core_hosts (id, name, created_at) VALUES ('h1', 'web', ?), ('h2', 'db', ?)`, ts, ts)
	x.exec(`INSERT INTO core_host_groups (id, name, created_at) VALUES ('g1', 'prod', ?), ('g2', 'lab', ?)`, ts, ts)
	x.exec(`INSERT INTO core_host_group_members (group_id, host_id) VALUES ('g1', 'h1')`)

	all := x.webhook("all", nil)
	crit := x.webhook("crit", func(in *ChannelInput) { in.MinSeverity = "critical" })
	warn := x.webhook("warn", func(in *ChannelInput) { in.MinSeverity = "warning" })
	kinds := x.webhook("kinds", func(in *ChannelInput) { in.Kinds = []string{core.NotifyRolloutFailed} })
	grp := x.webhook("grp", func(in *ChannelInput) { in.GroupIDs = []string{"g2", "g1"} })
	x.webhook("lab", func(in *ChannelInput) { in.GroupIDs = []string{"g2"} })
	off := x.webhook("off", nil)
	x.disable(off, "off")

	sorted := func(ids ...string) []string { slices.Sort(ids); return ids }
	for _, c := range []struct {
		name string
		ev   core.NotifyEvent
		want []string
	}{
		{"a warning on a grouped host", core.NotifyEvent{Kind: core.NotifyRolloutFailed, Severity: core.SeverityWarning, HostID: "h1",
			Title: "t", Body: "b", DedupeKey: "rollout.failed:a"}, sorted(all, warn, kinds, grp)},
		{"a critical update on an ungrouped host", core.NotifyEvent{Kind: core.NotifyUpdateRolledBack, Severity: core.SeverityCritical, HostID: "h2",
			Title: "t", Body: "b", DedupeKey: "update.rolled_back:b"}, sorted(all, crit, warn)},
		{"a host-less info event", core.NotifyEvent{Kind: core.NotifyChangeFailed, Severity: core.SeverityInfo,
			Title: "t", Body: "b", DedupeKey: "change.failed:c"}, sorted(all)},
	} {
		ev := x.emit(c.ev)
		if got := x.recipients(ev); !slices.Equal(got, c.want) {
			t.Errorf("%s reached %v, want %v", c.name, got, c.want)
		}
	}

	// A change's key is emitted once: the repeat stores nothing.
	if id := x.emit(core.NotifyEvent{Kind: core.NotifyChangeFailed, Severity: core.SeverityInfo, Title: "t", Body: "b",
		DedupeKey: "change.failed:c"}); id != "" {
		t.Errorf("a repeated change key stored event %s", id)
	}

	// Dedupe: a repeat within 10 minutes of a sent or pending delivery is
	// stored and not delivered; one after 10 minutes is delivered.
	x.drain(x.o)
	x.clock.Advance(5 * time.Minute)
	repeat := x.emit(rolloutEvent("rollout.failed:d"))
	if got := x.recipients(repeat); len(got) != 4 {
		t.Fatalf("the first rollout.failed:d reached %v", got)
	}
	x.drain(x.o)
	x.clock.Advance(9 * time.Minute)
	if id := x.emit(rolloutEvent("rollout.failed:d")); id == "" || len(x.recipients(id)) != 0 {
		t.Errorf("a repeat after 9 minutes: event %q reached %v; want stored and not delivered", id, x.recipients(id))
	}
	x.clock.Advance(2 * time.Minute)
	if id := x.emit(rolloutEvent("rollout.failed:d")); len(x.recipients(id)) != 4 {
		t.Errorf("a repeat 11 minutes after the delivered one reached %v; want 4 channels", x.recipients(id))
	}
	x.fake.setAnswer(answerBy(map[string]string{all: "503"}))
	pend := x.emit(core.NotifyEvent{Kind: core.NotifyUpdateRolledBack, Severity: core.SeverityInfo, Title: "t", Body: "b", DedupeKey: "update.rolled_back:e"})
	x.drain(x.o)
	if d, _ := x.delivery(pend, all); d.State != "pending" || d.Attempts != 1 {
		t.Fatalf("the failing delivery is %+v, want pending after one attempt", d)
	}
	x.clock.Advance(3 * time.Minute)
	if id := x.emit(core.NotifyEvent{Kind: core.NotifyUpdateRolledBack, Severity: core.SeverityInfo, Title: "t", Body: "b",
		DedupeKey: "update.rolled_back:e"}); len(x.recipients(id)) != 0 {
		t.Errorf("a repeat of a pending event reached %v", x.recipients(id))
	}
	x.fake.setAnswer(nil)
	x.runUntilSettled(x.o, x.clock.Now().Add(time.Hour))

	// A failed delivery holds nothing back.
	x.fake.setAnswer(answerBy(map[string]string{all: "404"}))
	failed := x.emit(core.NotifyEvent{Kind: core.NotifyUpdateRolledBack, Severity: core.SeverityInfo, Title: "t", Body: "b", DedupeKey: "update.rolled_back:f"})
	x.drain(x.o)
	if d, _ := x.delivery(failed, all); d.State != "failed" {
		t.Fatalf("the refused delivery is %+v, want failed", d)
	}
	x.fake.setAnswer(nil)
	x.clock.Advance(time.Minute)
	if id := x.emit(core.NotifyEvent{Kind: core.NotifyUpdateRolledBack, Severity: core.SeverityInfo, Title: "t", Body: "b",
		DedupeKey: "update.rolled_back:f"}); !slices.Equal(x.recipients(id), []string{all}) {
		t.Errorf("a repeat of a failed event reached %v, want %v", x.recipients(id), []string{all})
	}

	t.Run("one alert in order", func(t *testing.T) {
		x := newOutboxEnv(t)
		x.exec(`INSERT INTO core_hosts (id, name, created_at) VALUES ('h2', 'db', ?)`, ts)
		hook := x.webhook("hook", nil)
		alert := func(kind, key string) core.NotifyEvent {
			return core.NotifyEvent{Kind: kind, Severity: core.SeverityWarning, HostID: "h2", Title: "t", Body: "b", DedupeKey: key}
		}
		fire1 := x.emit(alert(core.NotifyAlertFiring, "alert.firing:r1:h2:/var:1795000000"))
		x.clock.Advance(time.Minute)
		resolve := x.emit(alert(core.NotifyAlertResolved, "alert.resolved:r1:h2:/var:1795000000"))
		other := x.emit(alert(core.NotifyAlertFiring, "alert.firing:r2:h2:/var:1795000060"))
		x.clock.Advance(time.Minute)
		fire2 := x.emit(alert(core.NotifyAlertFiring, "alert.firing:r1:h2:/var:1795000120"))
		x.fake.setAnswer(func(_ send.Channel, ev send.Event) (string, error) {
			if ev.ID == fire1 && len(x.fake.callsOf(hook, fire1)) == 1 {
				return "503", errors.New("send: refused")
			}
			return "200", nil
		})
		x.drain(x.o)
		var order []string
		for _, c := range x.fake.callsOf(hook) {
			order = append(order, c.Event)
		}
		slices.Sort(order)
		if want := x.sortedIDs(fire1, other); !slices.Equal(order, want) {
			t.Errorf("while the first firing waits for its retry, sent %v; want it and the other alert %v", order, want)
		}
		x.runUntilSettled(x.o, x.clock.Now().Add(time.Hour))
		order = nil
		for _, c := range x.fake.callsOf(hook) {
			order = append(order, c.Event)
		}
		if want := []string{fire1, resolve, fire2}; len(order) != 5 || !slices.Equal(order[2:], want) {
			t.Errorf("sent %v, want %v", order, want)
		}
		for _, ev := range []string{fire1, resolve, fire2, other} {
			if d, _ := x.delivery(ev, hook); d.State != "sent" {
				t.Errorf("delivery of %s is %+v, want sent", ev, d)
			}
		}
	})

	t.Run("a channel disabled before the send", func(t *testing.T) {
		x := newOutboxEnv(t)
		hook, other := x.webhook("hook", nil), x.webhook("other", nil)
		ev := x.emit(rolloutEvent("rollout.failed:off"))
		x.disable(hook, "hook")
		x.drain(x.o)
		if d, _ := x.delivery(ev, hook); d != (deliveryRow{State: "failed", LastError: "disabled"}) {
			t.Errorf("the delivery to the disabled channel is %+v, want failed, disabled, no attempt", d)
		}
		if got := len(x.fake.callsOf(hook)); got != 0 {
			t.Errorf("%d sends on the disabled channel", got)
		}
		if d, _ := x.delivery(ev, other); d.State != "sent" || len(x.noticesFor(hook)) != 0 {
			t.Errorf("the other delivery is %+v and the disabled channel has %d notices; want sent and none", d, len(x.noticesFor(hook)))
		}
	})

	t.Run("refuses an unknown kind or severity", func(t *testing.T) {
		for _, ev := range []core.NotifyEvent{
			{Kind: "alert.nope", Severity: core.SeverityInfo, Title: "t", Body: "b", DedupeKey: "k"},
			{Kind: core.NotifyTest, Severity: "loud", Title: "t", Body: "b", DedupeKey: "k"},
			{Kind: core.NotifyTest, Severity: core.SeverityInfo, Title: "t", Body: "b"},
		} {
			err := x.db.Write(context.Background(), func(tx *sql.Tx) error { return x.n.Emit(context.Background(), tx, ev) })
			if err == nil {
				t.Errorf("Emit(%+v) = nil, want an error", ev)
			}
		}
	})
}

// marker sets the restore-quarantine flag with transaction tx.
func (x *outboxEnv) marker(tx string) {
	x.t.Helper()
	raw, err := json.Marshal(quarantine.Marker{Tx: tx, RestoredAt: x.clock.Now()})
	if err != nil {
		x.t.Fatal(err)
	}
	x.exec(`INSERT INTO core_meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, quarantine.MetaKey, string(raw))
}

// contribute makes the module's controller contribution, as a controller
// start does, and returns its services.
func (x *outboxEnv) contribute() []ext.Service {
	x.t.Helper()
	_, services, err := module{}.Controller(ext.Deps{DB: x.db, Sealer: coretest.Sealer{}, Clock: x.clock, Log: x.log, Audit: x.audit, Notifier: x.n})
	if err != nil {
		x.t.Fatal(err)
	}
	return services
}

func (x *outboxEnv) noticesFor(channel string) []string {
	x.t.Helper()
	rows, err := x.db.Read().Query(`SELECT id FROM notify_events WHERE kind = ? AND dedupe_key = ? ORDER BY rowid`,
		core.NotifyDeliveryFailed, "notify.delivery_failed:"+channel)
	if err != nil {
		x.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			x.t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

func TestFailureNotices(t *testing.T) {
	x := newOutboxEnv(t)
	broken, b, c := x.webhook("broken", nil), x.webhook("b", nil), x.webhook("c", nil)
	off := x.webhook("off", nil)
	x.disable(off, "off")
	x.webhook("crit", func(in *ChannelInput) { in.MinSeverity = "critical" })
	x.fake.setAnswer(answerBy(map[string]string{broken: "404"}))

	// No notice for a failed test or failure notice.
	for _, kind := range []string{core.NotifyTest, core.NotifyDeliveryFailed} {
		ev := x.emit(core.NotifyEvent{Kind: kind, Severity: core.SeverityCritical, Title: "t", Body: "b", DedupeKey: kind + ":x"})
		x.drain(x.o)
		if d, _ := x.delivery(ev, broken); d.State != "failed" {
			t.Fatalf("the %s delivery is %+v, want failed", kind, d)
		}
	}
	if n := x.noticesFor(broken); len(n) != 0 {
		t.Errorf("failed test and notice deliveries emitted %d notices", len(n))
	}

	// The first failure of a streak notifies every other enabled channel the
	// routing matches, once; a second failure in the streak adds nothing.
	x.emit(rolloutEvent("rollout.failed:1"))
	x.drain(x.o)
	notices := x.noticesFor(broken)
	if len(notices) != 1 {
		t.Fatalf("%d notices after the first failure, want 1", len(notices))
	}
	if got, want := x.recipients(notices[0]), x.sortedIDs(b, c); !slices.Equal(got, want) {
		t.Errorf("the notice reached %v, want %v (not the failed, the disabled or the critical-only channel)", got, want)
	}
	var title, body, sev, host string
	if err := x.db.Read().QueryRow(`SELECT title, body, severity, host_id FROM notify_events WHERE id = ?`, notices[0]).
		Scan(&title, &body, &sev, &host); err != nil {
		t.Fatal(err)
	}
	if title != "Notification channel broken is failing" || sev != core.SeverityWarning || host != "" ||
		body != "A notification on channel broken (webhook) was not delivered. Last result: 404." {
		t.Errorf("notice %q / %q / %s / %q", title, body, sev, host)
	}
	if got := len(x.fake.callsOf(b, notices[0])); got != 1 {
		t.Errorf("the notice was sent %d times on b", got)
	}
	x.clock.Advance(time.Hour)
	x.emit(rolloutEvent("rollout.failed:2"))
	x.drain(x.o)
	if n := x.noticesFor(broken); len(n) != 1 {
		t.Errorf("%d notices after a second failure in the streak, want 1", len(n))
	}

	// A delivery ends the streak; the next failure notifies again.
	x.fake.setAnswer(nil)
	x.emit(rolloutEvent("rollout.failed:3"))
	x.drain(x.o)
	x.fake.setAnswer(answerBy(map[string]string{broken: "404"}))
	x.clock.Advance(time.Hour)
	x.emit(rolloutEvent("rollout.failed:4"))
	x.drain(x.o)
	if n := x.noticesFor(broken); len(n) != 2 {
		t.Errorf("%d notices after a new streak, want 2", len(n))
	}

	t.Run("restore", func(t *testing.T) {
		x := newOutboxEnv(t)
		hook, other := x.webhook("hook", nil), x.webhook("other", nil)
		x.fake.setAnswer(answerBy(map[string]string{hook: "503", other: "503"}))
		ev := x.emit(rolloutEvent("rollout.failed:r"))
		x.drain(x.o)
		sent := len(x.fake.callsOf(""))
		events := x.count("notify_events")

		x.marker("tx-1")
		services := x.contribute()
		var names []string
		for _, svc := range services {
			names = append(names, svc.Name)
		}
		if !slices.Equal(names, []string{"notify.sender", "notify.changes"}) {
			t.Fatalf("the contribution's services are %v, want notify.sender and notify.changes", names)
		}
		for _, ch := range []string{hook, other} {
			if d, _ := x.delivery(ev, ch); d.State != "failed" || d.LastError != "restored" {
				t.Errorf("after the restore the delivery to %s is %+v, want failed, restored", ch, d)
			}
		}
		var restored sql.NullString
		if err := x.db.Read().QueryRow(`SELECT restored_tx FROM notify_state WHERE id = 1`).Scan(&restored); err != nil || restored.String != "tx-1" {
			t.Errorf("notify_state.restored_tx = %v, %v; want tx-1", restored, err)
		}
		if n := x.count("notify_events"); n != events {
			t.Errorf("the restore emitted %d events", n-events)
		}

		// The same marker again changes nothing; while it is set nothing is
		// sent.
		later := x.emit(rolloutEvent("rollout.failed:later"))
		x.contribute()
		if err := x.o.restore(context.Background()); err != nil {
			t.Fatal(err)
		}
		if d, _ := x.delivery(later, hook); d.State != "pending" {
			t.Errorf("a second start with the same marker changed a delivery to %+v", d)
		}
		x.clock.Advance(time.Hour)
		x.drain(x.o)
		if got := len(x.fake.callsOf("")); got != sent {
			t.Errorf("%d sends while in restore quarantine", got-sent)
		}
		x.exec(`DELETE FROM core_meta WHERE key = ?`, quarantine.MetaKey)
		x.fake.setAnswer(nil)
		x.drain(x.o)
		if d, _ := x.delivery(later, hook); d.State != "sent" {
			t.Errorf("after the release the delivery is %+v, want sent", d)
		}

		// A new restore marks again.
		x.fake.setAnswer(answerBy(map[string]string{hook: "503"}))
		again := x.emit(rolloutEvent("rollout.failed:again"))
		x.drain(x.o)
		x.marker("tx-2")
		x.contribute()
		if d, _ := x.delivery(again, hook); d.State != "failed" || d.LastError != "restored" {
			t.Errorf("after a second restore the delivery is %+v, want failed, restored", d)
		}
		if n := x.noticesFor(hook); len(n) != 0 {
			t.Errorf("restored deliveries emitted %d notices", len(n))
		}
	})

	t.Run("purge", func(t *testing.T) {
		x := newOutboxEnv(t)
		hook := x.webhook("hook", nil)
		old := x.emit(rolloutEvent("rollout.failed:old"))
		x.drain(x.o)
		x.clock.Advance(24 * time.Hour)
		young := x.emit(rolloutEvent("rollout.failed:young"))
		x.drain(x.o)
		x.clock.Advance(29*24*time.Hour + time.Minute)
		o := x.newOutbox(x.db)
		x.drain(o)
		if _, ok := x.delivery(old, hook); ok || x.recipients(old) != nil {
			t.Error("the delivery of the event older than 30 days was kept")
		}
		var n int
		if err := x.db.Read().QueryRow(`SELECT count(*) FROM notify_events WHERE id = ?`, old).Scan(&n); err != nil || n != 0 {
			t.Errorf("the event older than 30 days: %d rows, %v", n, err)
		}
		if d, ok := x.delivery(young, hook); !ok || d.State != "sent" {
			t.Errorf("the 29-day-old delivery is %+v, %v; want kept", d, ok)
		}
	})
}

func (x *outboxEnv) sortedIDs(ids ...string) []string {
	out := slices.Clone(ids)
	slices.Sort(out)
	return out
}

// TestStaleReadSendsNoDeliveryTwice: a delivery in flight when a pass reads
// the due list, and settled before the pass starts its sends, is not sent
// again from that list.
func TestStaleReadSendsNoDeliveryTwice(t *testing.T) {
	x := newOutboxEnv(t)
	hook := x.webhook("hook", nil)
	ev := x.emit(rolloutEvent("rollout.failed:r"))
	gate, started := make(chan struct{}), make(chan struct{}, 1)
	x.fake.mu.Lock()
	x.fake.gate, x.fake.started = gate, started
	x.fake.mu.Unlock()
	ctx := context.Background()
	if n := x.o.pass(ctx); n != 1 {
		t.Fatalf("first pass started %d, want 1", n)
	}
	<-started
	snap, ok := x.o.due(ctx)
	if !ok || len(snap.list) != 1 {
		t.Fatalf("read %d due deliveries (%v), want the one in flight", len(snap.list), ok)
	}
	close(gate)
	x.o.wait()
	if d, _ := x.delivery(ev, hook); d.State != "sent" {
		t.Fatalf("delivery %+v, want sent", d)
	}
	x.o.start(ctx, snap)
	x.o.wait()
	if got := len(x.fake.callsOf(hook, ev)); got != 1 {
		t.Errorf("%d sends, want 1: a stale read sent a settled delivery again", got)
	}
}
