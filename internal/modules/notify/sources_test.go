package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/audit"
	"github.com/darkraise/darkpanel/internal/controller/rollout"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
)

const canary = "canary-7f3a91"

type eventRow struct{ Kind, Key, Host, Severity, Title, Body string }

// events is every stored event, oldest first.
func (e *env) events() []eventRow {
	e.t.Helper()
	rows, err := e.db.Read().Query(`SELECT kind, dedupe_key, host_id, severity, title, body FROM notify_events ORDER BY rowid`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []eventRow
	for rows.Next() {
		var r eventRow
		if err := rows.Scan(&r.Kind, &r.Key, &r.Host, &r.Severity, &r.Title, &r.Body); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func keys(events []eventRow) []string {
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = ev.Key
	}
	return out
}

func sqlExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

// changeRow stores a controller-side change; code and cause may be "".
func changeRow(t *testing.T, db *sql.DB, id, host, action, state, code, cause string, at time.Time) {
	t.Helper()
	ts := at.UTC().Format(timeFormat)
	sqlExec(t, db, `INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash, diff_json, guards_json,
		confirmation, created_at, expires_at) VALUES (?, ?, ?, 1, 'user:u1', 'h', 's', '[]', '[]', '', ?, ?)`, "p-"+id, host, action, ts, ts)
	sqlExec(t, db, `INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state, input_json,
		error_code, error_message, error_cause, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, 'revertible', '', 'user:u1', ?, '{}', nullif(?, ''), nullif(?, ''), nullif(?, ''), ?, ?)`,
		id, "p-"+id, host, action, state, code, cause, cause, ts, ts)
}

// journalRow stores a controller journal entry of a change.
func journalRow(t *testing.T, db *sql.DB, change, phase, actor string, at time.Time) {
	t.Helper()
	sqlExec(t, db, `INSERT INTO core_audit (id, at, actor, host_id, action, version, phase, change_id, result)
		VALUES (?, ?, ?, '', 'files.write', 1, ?, ?, 'ok')`, "a-"+change+"-"+phase, at.UTC().Format(timeFormat), actor, phase, change)
}

func (x *outboxEnv) cursor() *changeCursor {
	return newChangeCursor(x.db, x.clock, x.log, x.n)
}

func (x *outboxEnv) cursorPass(c *changeCursor) {
	x.t.Helper()
	if err := c.pass(context.Background()); err != nil {
		x.t.Fatalf("cursor pass: %v", err)
	}
}

// statusDispatcher answers core.update.status with the transactions of
// each operation.
type statusDispatcher struct {
	rollout.Dispatcher
	ops map[string][]core.UpdateTransaction
}

func (d statusDispatcher) Read(_ context.Context, _ core.Principal, _ string, _ core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
	var in core.UpdateStatusInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	return json.Marshal(core.UpdateStatus{Transactions: d.ops[in.OperationID]})
}

// updatingRollout stores a running rollout whose one host follows op.
func updatingRollout(t *testing.T, db *sql.DB, id, host, op string, at time.Time) {
	t.Helper()
	ts := at.UTC().Format(timeFormat)
	sqlExec(t, db, `INSERT INTO core_rollouts (id, version, channel, state, created_at, updated_at) VALUES (?, '1.4.0', 'stable', 'running', ?, ?)`, id, ts, ts)
	sqlExec(t, db, `INSERT INTO core_rollout_hosts (rollout_id, host_id, position, state, plan_id, apply_sent_at, operation_id, followed_at)
		VALUES (?, ?, 0, 'updating', 'plan-x', ?, ?, ?)`, id, host, ts, op, ts)
}

func TestEachSourceEmits(t *testing.T) {
	x := newOutboxEnv(t)
	ts := t0.Format(timeFormat)
	x.exec(`INSERT INTO core_hosts (id, name, created_at) VALUES ('h1', 'web', ?)`, ts)
	x.webhook("all", nil)
	ctx := context.Background()

	// Agent changes, from the ingested journal entries alone.
	entries := []core.JournalEntry{
		{Seq: 1, At: t0, Actor: "user:u1", Action: "files.write", Version: 1, Phase: "apply", ChangeID: "ca1", Result: "error", ErrorCode: "change.apply_failed"},
		{Seq: 2, At: t0, Actor: lifecycle.ActorRevertTimer, Action: "files.write", Version: 1, Phase: "revert", ChangeID: "ca2", Result: "ok"},
		{Seq: 3, At: t0, Actor: lifecycle.ActorRevertTimer, Action: "files.write", Version: 1, Phase: lifecycle.PhaseRevertWatchdog, ChangeID: "ca3", Result: "ok"},
		{Seq: 4, At: t0, Actor: "user:u1", Action: "files.write", Version: 1, Phase: "revert", ChangeID: "ca4", Result: "ok"},
		{Seq: 5, At: t0, Actor: "user:u1", Action: "files.write", Version: 1, Phase: "apply", ChangeID: "ca5", Result: "ok"},
		{Seq: 6, At: t0, Actor: "user:u1", Action: "files.write", Version: 1, Phase: "confirm", ChangeID: "ca1", Result: "error", ErrorCode: "x"},
		{Seq: 7, At: t0, Actor: lifecycle.ActorRevertTimer, Action: "files.write", Version: 1, Phase: "revert", ChangeID: "ca7", Result: "error", ErrorCode: "change.needs_repair"},
		{Seq: 8, At: t0, Actor: "user:u1", Action: "files.write", Version: 1, Phase: "revert", ChangeID: "ca8", Result: "error", ErrorCode: "change.needs_repair"},
		{Seq: 9, At: t0, Actor: lifecycle.ActorRevertTimer, Action: "files.write", Version: 1, Phase: "revert", ChangeID: "ca9", Result: "override", ErrorCode: "change.needs_repair"},
	}
	agent := &coretest.FakeAgent{
		JournalFunc: func(_ context.Context, after int64, _ int) ([]core.JournalEntry, error) {
			var out []core.JournalEntry
			for _, e := range entries {
				if e.Seq > after {
					out = append(out, e)
				}
			}
			return out, nil
		},
		AckFunc: func(context.Context, int64) error { return nil },
	}
	ingest := func() {
		in := audit.NewIngester(audit.IngesterConfig{Store: audit.New(x.db, x.clock), Agents: coretest.MapHosts{"h1": agent},
			Clock: x.clock, Log: x.log, Notifier: x.n})
		if err := in.IngestAll(ctx); err != nil {
			t.Fatal(err)
		}
	}
	ingest()
	if got, want := keys(x.events()), []string{"change.failed:h1:ca1", "change.auto_reverted:h1:ca2", "change.auto_reverted:h1:ca3",
		"change.failed:h1:ca7", "change.failed:h1:ca8", "change.failed:h1:ca9"}; !equal(got, want) {
		t.Fatalf("agent entries emitted %v, want %v", got, want)
	}
	for _, ev := range x.events() {
		if ev.Host != "h1" {
			t.Errorf("event %s names host %q, want h1", ev.Key, ev.Host)
		}
	}
	// A journal read again (a new epoch) emits nothing twice.
	sqlExec(t, x.db.Writer(), `DELETE FROM core_journal_cursor`)
	sqlExec(t, x.db.Writer(), `DELETE FROM core_audit WHERE host_id = 'h1'`)
	ingest()
	if n := len(x.events()); n != 6 {
		t.Errorf("a second ingestion of the entries left %d events, want 6", n)
	}

	// Controller-side changes, through the cursor over core_changes.
	db := x.db.Writer()
	at := t0.Add(time.Minute)
	changeRow(t, db, "cc1", "", "notify.channel.create", core.StateFailed, "change.verify_failed", "", at)
	changeRow(t, db, "cc2", "", "files.write", core.StateReverted, "", "", at)
	journalRow(t, db, "cc2", "revert", lifecycle.ActorRevertTimer, at)
	changeRow(t, db, "cc3", "", "files.write", core.StateReverted, "", "", at)
	journalRow(t, db, "cc3", "revert", "user:u1", at)
	changeRow(t, db, "cc4", "", "files.write", core.StateCompleted, "", "", at)
	changeRow(t, db, "cc5", "", "files.write", core.StateReverted, "change.apply_failed", "", at)
	changeRow(t, db, "cc6", "", "files.write", core.StateReverted, "", "", at)
	journalRow(t, db, "cc6", lifecycle.PhaseRevertWatchdog, lifecycle.ActorRevertTimer, at)
	changeRow(t, db, "cc7", "", "files.write", core.StateNeedsRepair, "change.needs_repair", "", at)
	changeRow(t, db, "cc8", "", "files.write", core.StateReverted, "", "", at)
	sqlExec(t, db, `INSERT INTO core_audit (id, at, actor, host_id, action, version, phase, change_id, result)
		VALUES ('a-cc8', ?, ?, '', 'files.write', 1, 'revert', 'cc8', 'error')`, at.UTC().Format(timeFormat), lifecycle.ActorRevertTimer)
	x.cursorPass(x.cursor())
	got := keys(x.events())[6:]
	if want := []string{"change.failed:cc1", "change.auto_reverted:cc2", "change.failed:cc5", "change.auto_reverted:cc6", "change.failed:cc7"}; !equal(got, want) {
		t.Errorf("controller changes emitted %v, want %v", got, want)
	}

	// Rollouts: a rolled-back update fails its host; a failed one only fails it.
	hosts := x.count("notify_events")
	disp := statusDispatcher{ops: map[string][]core.UpdateTransaction{
		"op1": {{ID: "t1", OperationID: "op1", Kind: "update", State: core.UpdateRolledBack,
			Error: &core.ErrorDetail{Code: "update.health_failed", Message: canary}}},
		"op2": {{ID: "t2", OperationID: "op2", Kind: "update", State: core.UpdateFailed,
			Error: &core.ErrorDetail{Code: "update.download_failed", Message: canary}}},
	}}
	drive := func() {
		d, err := rollout.NewDriver(rollout.DriverConfig{DB: x.db, Dispatcher: disp, Clock: x.clock, Log: x.log, Notifier: x.n})
		if err != nil {
			t.Fatal(err)
		}
		d.Start()
		x.clock.Advance(rollout.PollInterval)
		d.Stop()
	}
	updatingRollout(t, db, "r1", "h1", "op1", x.clock.Now())
	drive()
	updatingRollout(t, db, "r2", "h1", "op2", x.clock.Now())
	drive()
	got = keys(x.events())[hosts:]
	if want := []string{"rollout.failed:r1:h1", "update.rolled_back:r1:h1", "rollout.failed:r2:h1"}; !equal(got, want) {
		t.Errorf("rollouts emitted %v, want %v", got, want)
	}
	for _, ev := range x.events() {
		if ev.Title == "" || ev.Body == "" || ev.Severity == "" {
			t.Errorf("event %s has no text: %+v", ev.Key, ev)
		}
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCursorAcrossRestart(t *testing.T) {
	// A failed change from before the module's migration is history.
	x := &outboxEnv{}
	x.env = newEnv(t, func(e *env) {
		changeRow(t, e.db.Writer(), "old", "", "files.write", core.StateFailed, "change.apply_failed", "", t0.Add(-time.Hour))
	})
	x.n = newNotifier(x.clock)
	x.log = slog.New(slog.NewTextHandler(x.logs, nil))
	db := x.db.Writer()
	var mark string
	if err := x.db.Read().QueryRow(`SELECT updated_at FROM notify_cursor`).Scan(&mark); err != nil || mark != t0.Add(-time.Hour+cursorOverlap).Format(timeFormat) {
		t.Fatalf("the migrated cursor is %q, %v; want the overlap after the newest existing change", mark, err)
	}

	// The controller stops before its cursor runs.
	at := t0.Add(time.Minute)
	changeRow(t, db, "cf", "", "files.write", core.StateFailed, "change.apply_failed", "", at)
	changeRow(t, db, "cr", "", "files.write", core.StateReverted, "", "", at)
	journalRow(t, db, "cr", "revert", lifecycle.ActorRevertTimer, at)
	_ = x.cursor()

	x.cursorPass(x.cursor())
	if got, want := keys(x.events()), []string{"change.failed:cf", "change.auto_reverted:cr"}; !equal(got, want) {
		t.Fatalf("after the restart the cursor emitted %v, want %v", got, want)
	}
	if err := x.db.Read().QueryRow(`SELECT updated_at FROM notify_cursor`).Scan(&mark); err != nil || mark != at.Format(timeFormat) {
		t.Errorf("the cursor is %q, %v; want %s", mark, err, at.Format(timeFormat))
	}
	x.cursorPass(x.cursor())
	x.cursorPass(x.cursor())
	if n := len(x.events()); n != 2 {
		t.Errorf("two more restarts left %d events, want 2", n)
	}

	// A change stamped before the mark but committed after the pass is read
	// within the overlap; one stamped earlier than the overlap is not.
	changeRow(t, db, "late", "", "files.write", core.StateFailed, "", "", at.Add(-2*time.Second))
	changeRow(t, db, "stale", "", "files.write", core.StateFailed, "", "", at.Add(-time.Minute))
	x.cursorPass(x.cursor())
	if got := keys(x.events()); !equal(got, []string{"change.failed:cf", "change.auto_reverted:cr", "change.failed:late"}) {
		t.Errorf("after a late commit the cursor emitted %v", got)
	}
}

func TestBodiesAreTemplates(t *testing.T) {
	x := newOutboxEnv(t)
	ts := t0.Format(timeFormat)
	x.exec(`INSERT INTO core_hosts (id, name, created_at) VALUES ('h1', 'web', ?)`, ts)
	db := x.db.Writer()
	at := t0.Add(time.Minute)
	changeRow(t, db, "c1", "h1", "files.write", core.StateFailed, "change.apply_failed", canary, at)
	changeRow(t, db, "c2", "h1", "files.write", core.StateReverted, "", canary, at)
	journalRow(t, db, "c2", "revert", lifecycle.ActorRevertTimer, at)
	changeRow(t, db, "c3", "", "core.group.create", core.StateFailed, "", canary, at)
	changeRow(t, db, "c4", "h1", "files.write", core.StateNeedsRepair, "change.needs_repair", canary, at)
	x.exec(`INSERT INTO core_hosts (id, name, created_at) VALUES ('h2', ?, ?)`, "db\n@everyone", ts)
	changeRow(t, db, "c5", "h2", "files.write\n<https://evil.example>", core.StateFailed, "code with spaces", canary, at)
	x.cursorPass(x.cursor())
	disp := statusDispatcher{ops: map[string][]core.UpdateTransaction{"op1": {{ID: "t1", OperationID: "op1", Kind: "update",
		State: core.UpdateRolledBack, Error: &core.ErrorDetail{Code: "update.health_failed", Message: canary}}}}}
	updatingRollout(t, db, "r1", "h1", "op1", x.clock.Now())
	d, err := rollout.NewDriver(rollout.DriverConfig{DB: x.db, Dispatcher: disp, Clock: x.clock, Log: x.log, Notifier: x.n})
	if err != nil {
		t.Fatal(err)
	}
	d.Start()
	x.clock.Advance(rollout.PollInterval)
	d.Stop()

	want := map[string]eventRow{
		"change.failed:c1": {Severity: core.SeverityWarning, Title: "Change failed on web",
			Body: "Change c1 (files.write) on web ended failed with error change.apply_failed."},
		"change.auto_reverted:c2": {Severity: core.SeverityWarning, Title: "Change reverted automatically on web",
			Body: "Change c2 (files.write) on web was reverted automatically and ended reverted."},
		"change.failed:c3": {Severity: core.SeverityWarning, Title: "Change failed on the controller",
			Body: "Change c3 (core.group.create) on the controller ended failed."},
		"change.failed:c4": {Severity: core.SeverityWarning, Title: "Change failed on web",
			Body: "Change c4 (files.write) on web ended needs_repair with error change.needs_repair."},
		"change.failed:c5": {Severity: core.SeverityWarning, Title: "Change failed on db @everyone",
			Body: "Change c5 (unknown) on db @everyone ended failed with error unknown."},
		"rollout.failed:r1:h1": {Severity: core.SeverityCritical, Title: "Update rollout failed on web",
			Body: "Rollout r1 of version 1.4.0 failed on web with error update.health_failed."},
		"update.rolled_back:r1:h1": {Severity: core.SeverityCritical, Title: "Update rolled back on web",
			Body: "The update of web to version 1.4.0 in rollout r1 was rolled back."},
	}
	got := x.events()
	if len(got) != len(want) {
		t.Errorf("%d events, want %d: %v", len(got), len(want), keys(got))
	}
	for _, ev := range got {
		w, ok := want[ev.Key]
		if !ok {
			t.Errorf("unexpected event %s", ev.Key)
			continue
		}
		if ev.Severity != w.Severity || ev.Title != w.Title || ev.Body != w.Body {
			t.Errorf("%s: %s / %q / %q; want %s / %q / %q", ev.Key, ev.Severity, ev.Title, ev.Body, w.Severity, w.Title, w.Body)
		}
	}
	var leaks int
	if err := x.db.Read().QueryRow(`SELECT count(*) FROM notify_events WHERE instr(title || body || dedupe_key, ?) > 0`, canary).Scan(&leaks); err != nil || leaks != 0 {
		t.Errorf("%d events carry the canary (%v)", leaks, err)
	}
	if strings.Contains(x.logs.String(), canary) {
		t.Error("the log carries the canary")
	}
}
