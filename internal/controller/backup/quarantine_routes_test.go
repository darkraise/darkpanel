package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/audit"
	"github.com/darkraise/darkpanel/internal/controller/quarantine"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/core/store"
)

const (
	qlAdvertise = "controller.example:7443"
	qlTx        = "01JTXQUARANTINE0000000000"
)

var qlExpiry = fxNow.Add(time.Hour)

func qlToken(host string) string { return "qltoken-secret-for-" + host }

// qlIssued is one IssueToken call as the fake issuer saw it.
type qlIssued struct{ host, address, stateInTx string }

// qlIssuer is a TokenIssuer that writes a token row in the transaction it is
// given and records the host's state as that transaction sees it.
type qlIssuer struct {
	mu    sync.Mutex
	calls []qlIssued
	err   error
}

func (i *qlIssuer) IssueToken(ctx context.Context, tx *sql.Tx, hostID, address string) (string, time.Time, error) {
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM core_hosts WHERE id = ?`, hostID).Scan(&state); err != nil {
		return "", time.Time{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO core_enroll_tokens (host_id, hash, expires_at, consumed_at, created_at)
		VALUES (?, x'00', ?, NULL, ?)`, hostID, qlExpiry.Format("2006-01-02T15:04:05.000000000Z"), "2026-11-04T12:00:00.000000000Z"); err != nil {
		return "", time.Time{}, err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.calls = append(i.calls, qlIssued{hostID, address, state})
	if i.err != nil {
		return "", time.Time{}, i.err
	}
	return qlToken(hostID), qlExpiry, nil
}

func (i *qlIssuer) issued() []qlIssued {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]qlIssued(nil), i.calls...)
}

// qlFailingAudit is a core.Audit whose Record always fails.
type qlFailingAudit struct{ *coretest.MemAudit }

func (qlFailingAudit) Record(context.Context, core.AuditEntry) error {
	return errors.New("audit store is down")
}

type qlEnv struct {
	db     *store.DB
	audit  *coretest.MemAudit
	issuer *qlIssuer
	deps   ext.Deps
	b      *Backup
}

// qlNew returns a migrated controller database with four hosts (host-local,
// host-enr enrolled, host-pen pending, host-rev revoked) and a Backup whose
// issuer and advertise address are set; the quarantine flag is not.
func qlNew(t *testing.T) *qlEnv {
	t.Helper()
	db := fxDB(t, "")
	fxInsertHost(t, db, "host-local", "enrolled", true)
	fxInsertHost(t, db, "host-enr", "enrolled", false)
	fxInsertHost(t, db, "host-pen", "pending", false)
	fxInsertHost(t, db, "host-rev", "revoked", false)
	e := &qlEnv{db: db, audit: &coretest.MemAudit{}, issuer: &qlIssuer{}}
	e.deps = ext.Deps{
		DB: db, Clock: coretest.NewFakeClock(fxNow), Audit: e.audit, LocalHostID: "host-local",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	e.b = New(Config{Tokens: e.issuer, AgentAdvertise: qlAdvertise})
	return e
}

func qlExec(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Writer().ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func qlScalar(t *testing.T, db *store.DB, query string, args ...any) string {
	t.Helper()
	var s sql.NullString
	if err := db.Read().QueryRowContext(context.Background(), query, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return s.String
}

// qlDump renders every row of query as one string, columns joined by "|".
func qlDump(t *testing.T, db *store.DB, query string) []string {
	t.Helper()
	rows, err := db.Read().QueryContext(context.Background(), query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(cols))
		for i, v := range vals {
			parts[i] = fmt.Sprint(v)
		}
		out = append(out, strings.Join(parts, "|"))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func qlActivate(t *testing.T, db *store.DB) {
	t.Helper()
	raw, err := json.Marshal(quarantine.Marker{Tx: qlTx, RestoredAt: fxNow, BackupCreatedAt: fxNow.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	qlExec(t, db, `INSERT INTO core_meta (key, value) VALUES (?, ?)`, quarantine.MetaKey, string(raw))
}

// qlQuarantine inserts a change already in state quarantined, remembering from.
func qlQuarantine(t *testing.T, db *store.DB, c fxChange, from string) {
	t.Helper()
	c.State = "quarantined"
	fxInsertChange(t, db, c)
	qlExec(t, db, `UPDATE core_changes SET quarantined_from = ? WHERE id = ?`, from, c.ID)
}

func qlCall(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// qlWantProblem checks an RFC 9457 answer; an empty detail is not compared.
func qlWantProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code errcode.Code, detail string) {
	t.Helper()
	if rec.Code != status {
		t.Errorf("status = %d, want %d (body %s)", rec.Code, status, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	var p struct {
		Status int          `json:"status"`
		Code   errcode.Code `json:"code"`
		Detail string       `json:"detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("problem body %q: %v", rec.Body, err)
	}
	if p.Status != status || p.Code != code {
		t.Errorf("problem = %+v, want status %d code %s", p, status, code)
	}
	if detail != "" && p.Detail != detail {
		t.Errorf("detail = %q, want %q", p.Detail, detail)
	}
}

func qlDecode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("body %q: %v", rec.Body, err)
	}
	return v
}

func (e *qlEnv) auditRows(t *testing.T) []core.AuditEntry {
	t.Helper()
	rows, _, err := e.audit.List(context.Background(), core.AuditQuery{})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func (e *qlEnv) hostStates(t *testing.T) string {
	t.Helper()
	return strings.Join(qlDump(t, e.db, `SELECT id, state FROM core_hosts ORDER BY id`), ",")
}

// qlSeed inserts the records of the list test and returns what the list must answer.
func qlSeed(t *testing.T, db *store.DB) []Record {
	t.Helper()
	qlQuarantine(t, db, fxChange{ID: "a-change", HostID: "host-a", Action: "core.host.create"}, "applying")
	fxInsertLock(t, db, "net.b", "a-change")
	fxInsertLock(t, db, "net.a", "a-change")
	fxInsertRollout(t, db, "b-rollout", "quarantined", "host-a")
	qlExec(t, db, `UPDATE core_rollouts SET quarantined_from = 'running' WHERE id = 'b-rollout'`)
	fxInsertComposite(t, db, "c-composite", "quarantined", "forward", []fxStep{{ID: "s1", HostID: "host-a", Action: "fixture.one/v1", State: "applied"}})
	qlExec(t, db, `UPDATE core_changes SET quarantined_from = 'needs_repair' WHERE id = 'c-composite'`)
	qlQuarantine(t, db, fxChange{ID: "d-tailnet", Action: "tailnet.key.rotate"}, "recovering")
	qlQuarantine(t, db, fxChange{ID: "e-cloudflare", Action: "cloudflare.hostname.add"}, "applying")
	qlQuarantine(t, db, fxChange{ID: "f-core", Action: "core.host.revoke"}, "undoing")
	fxInsertLock(t, db, "dns.x", "f-core")
	// Not quarantined: none of these is listed, and their lock is nobody's record.
	fxInsertChange(t, db, fxChange{ID: "g-applying", Action: "core.host.create", State: "applying"})
	fxInsertLock(t, db, "other.key", "g-applying")
	fxInsertChange(t, db, fxChange{ID: "h-done", Action: "core.host.create", State: "completed"})
	fxInsertRollout(t, db, "i-running", "running", "host-a")
	fxInsertRollout(t, db, "j-done", "succeeded", "host-a")
	return []Record{
		{ID: "a-change", Kind: "change", Action: "core.host.create", HostID: "host-a", QuarantinedFrom: "applying", Locks: []string{"net.a", "net.b"}},
		{ID: "b-rollout", Kind: "rollout", Action: "core.update.rollout", HostID: "", QuarantinedFrom: "running", Locks: []string{}},
		{ID: "c-composite", Kind: "composite", Action: qlScalar(t, db, `SELECT action FROM core_changes WHERE id = 'c-composite'`),
			HostID: qlScalar(t, db, `SELECT host_id FROM core_changes WHERE id = 'c-composite'`), QuarantinedFrom: "needs_repair", Locks: []string{}},
		{ID: "d-tailnet", Kind: "cloud", Action: "tailnet.key.rotate", QuarantinedFrom: "recovering", Locks: []string{}},
		{ID: "e-cloudflare", Kind: "cloud", Action: "cloudflare.hostname.add", QuarantinedFrom: "applying", Locks: []string{}},
		{ID: "f-core", Kind: "change", Action: "core.host.revoke", QuarantinedFrom: "undoing", Locks: []string{"dns.x"}},
	}
}

func TestQlListRecordsKindsAndLocks(t *testing.T) {
	e := qlNew(t)
	want := qlSeed(t, e.db)
	ctx := context.Background()

	got, err := listRecords(ctx, e.db.Read())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("listRecords(*sql.DB) = %+v, %v\nwant %+v", got, err, want)
	}
	err = e.db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		got, err = listRecords(ctx, tx)
		return err
	})
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("listRecords(*sql.Tx) = %+v, %v\nwant %+v", got, err, want)
	}

	qlActivate(t, e.db)
	rec := qlCall(t, e.b.quarantineHandler(e.deps), http.MethodGet, "/api/v1/controller/quarantine", "")
	st := qlDecode[Status](t, rec)
	if !st.Active || st.Tx != qlTx || st.RestoredAt == nil || !st.RestoredAt.Equal(fxNow) {
		t.Errorf("status = %+v, want active with tx %s restored at %s", st, qlTx, fxNow)
	}
	if !reflect.DeepEqual(st.Records, want) {
		t.Errorf("records:\n got %+v\nwant %+v", st.Records, want)
	}
	if !strings.Contains(rec.Body.String(), `"locks":[]`) || strings.Contains(rec.Body.String(), `"locks":null`) {
		t.Errorf("a record without locks must answer \"locks\":[]: %s", rec.Body)
	}
}

func TestQlStatusWhenNotActive(t *testing.T) {
	e := qlNew(t)
	h := e.b.quarantineHandler(e.deps)

	rec := qlCall(t, h, http.MethodGet, "/api/v1/controller/quarantine", "")
	if got := rec.Body.String(); got != `{"active":false,"records":[]}` {
		t.Errorf("with nothing quarantined: %s, want {\"active\":false,\"records\":[]}", got)
	}

	qlQuarantine(t, e.db, fxChange{ID: "kept", Action: "core.host.create"}, "pending")
	fxInsertLock(t, e.db, "net.kept", "kept")
	rec = qlCall(t, h, http.MethodGet, "/api/v1/controller/quarantine", "")
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["tx"]; ok {
		t.Errorf("an inactive status carries tx: %s", rec.Body)
	}
	if _, ok := raw["restoredAt"]; ok {
		t.Errorf("an inactive status carries restoredAt: %s", rec.Body)
	}
	st := qlDecode[Status](t, rec)
	want := []Record{{ID: "kept", Kind: "change", Action: "core.host.create", QuarantinedFrom: "pending", Locks: []string{"net.kept"}}}
	if st.Active || !reflect.DeepEqual(st.Records, want) {
		t.Errorf("status = %+v, want inactive with the kept record %+v", st, want)
	}
}

func TestQlRecordByID(t *testing.T) {
	e := qlNew(t)
	want := qlSeed(t, e.db)
	ctx := context.Background()
	q := e.db.Read()

	for _, w := range want {
		got, ok, err := recordByID(ctx, q, w.ID)
		if err != nil || !ok || !reflect.DeepEqual(got, w) {
			t.Errorf("recordByID(%s) = %+v, %v, %v; want %+v", w.ID, got, ok, err, w)
		}
	}
	for _, id := range []string{"g-applying", "h-done", "i-running", "j-done", "no-such-record"} {
		if got, ok, err := recordByID(ctx, q, id); err != nil || ok {
			t.Errorf("recordByID(%s) = %+v, %v, %v; want not found (it was never quarantined)", id, got, ok, err)
		}
	}

	qlExec(t, e.db, `UPDATE core_changes SET state = 'needs_repair' WHERE id = 'a-change'`)
	got, ok, err := recordByID(ctx, q, "a-change")
	if err != nil || !ok || got.QuarantinedFrom != "applying" || got.Kind != "change" || !reflect.DeepEqual(got.Locks, []string{"net.a", "net.b"}) {
		t.Errorf("recordByID of a resolved record = %+v, %v, %v; want it found with quarantinedFrom kept", got, ok, err)
	}
	listed, err := listRecords(ctx, q)
	if err != nil || len(listed) != len(want)-1 {
		t.Errorf("listRecords after resolving one = %d records, %v; want %d", len(listed), err, len(want)-1)
	}
}

func TestQlEnrollTokenRefusals(t *testing.T) {
	long := strings.Repeat(" ", 64<<10)
	cases := []struct {
		name    string
		prepare func(e *qlEnv)
		body    string
		status  int
		code    errcode.Code
		detail  string
		active  bool
	}{
		{"not in quarantine", nil, `{"hostId":"host-enr"}`, 422, errcode.InvalidInput,
			"the controller is not in restore quarantine; use darkpanel host token", false},
		{"unknown host", nil, `{"hostId":"host-nope"}`, 404, errcode.NotFound, "", true},
		{"the local host", nil, `{"hostId":"host-local"}`, 422, errcode.InvalidInput, "", true},
		{"no token issuer", func(e *qlEnv) { e.b = New(Config{AgentAdvertise: qlAdvertise}) }, `{"hostId":"host-enr"}`, 422, errcode.InvalidInput,
			"start the controller with --agent-advertise to issue enrollment tokens", true},
		{"no advertise address", func(e *qlEnv) { e.b = New(Config{Tokens: e.issuer}) }, `{"hostId":"host-enr"}`, 422, errcode.InvalidInput,
			"start the controller with --agent-advertise to issue enrollment tokens", true},
		{"empty host id", nil, `{"hostId":""}`, 422, errcode.InvalidInput, "", true},
		{"no host id", nil, `{}`, 422, errcode.InvalidInput, "", true},
		{"unknown member", nil, `{"hostId":"host-enr","extra":1}`, 422, errcode.InvalidInput, "", true},
		{"two JSON values", nil, `{"hostId":"host-enr"} {}`, 422, errcode.InvalidInput, "", true},
		{"not JSON", nil, `host-enr`, 422, errcode.InvalidInput, "", true},
		{"empty body", nil, ``, 422, errcode.InvalidInput, "", true},
		{"body over 64 KiB", nil, `{"hostId":"host-enr"}` + long, 422, errcode.InvalidInput, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := qlNew(t)
			if c.active {
				qlActivate(t, e.db)
			}
			if c.prepare != nil {
				c.prepare(e)
			}
			hostsBefore := e.hostStates(t)
			rec := qlCall(t, e.b.enrollTokenHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/enroll-token", c.body)
			qlWantProblem(t, rec, c.status, c.code, c.detail)
			if n := len(e.issuer.issued()); n != 0 {
				t.Errorf("the issuer was called %d times", n)
			}
			if rows := e.auditRows(t); len(rows) != 0 {
				t.Errorf("audit rows = %+v, want none for a refusal", rows)
			}
			if got := e.hostStates(t); got != hostsBefore {
				t.Errorf("host states changed on a refusal: %s, was %s", got, hostsBefore)
			}
		})
	}
}

func TestQlEnrollTokenIssuesInTheHostsTransaction(t *testing.T) {
	cases := []struct{ host, stateInTx, stateAfter string }{
		{"host-enr", "pending", "pending"},
		{"host-pen", "pending", "pending"},
		{"host-rev", "revoked", "revoked"},
	}
	for _, c := range cases {
		t.Run(c.host, func(t *testing.T) {
			e := qlNew(t)
			qlActivate(t, e.db)
			others := map[string]string{"host-local": "enrolled", "host-enr": "enrolled", "host-pen": "pending", "host-rev": "revoked"}
			others[c.host] = c.stateAfter
			var want []string
			for _, id := range []string{"host-enr", "host-local", "host-pen", "host-rev"} {
				want = append(want, id+"|"+others[id])
			}

			rec := qlCall(t, e.b.enrollTokenHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/enroll-token", `{"hostId":"`+c.host+`"}`)
			res := qlDecode[EnrollTokenResult](t, rec)
			if res.Token != qlToken(c.host) || !res.ExpiresAt.Equal(qlExpiry) {
				t.Errorf("answer = %+v, want the issuer's token and expiry %s", res, qlExpiry)
			}
			if got := e.issuer.issued(); !reflect.DeepEqual(got, []qlIssued{{c.host, qlAdvertise, c.stateInTx}}) {
				t.Errorf("issuer calls = %+v, want one for %s at %s seeing the host %s", got, c.host, qlAdvertise, c.stateInTx)
			}
			if got := qlDump(t, e.db, `SELECT id, state FROM core_hosts ORDER BY id`); !reflect.DeepEqual(got, want) {
				t.Errorf("host states = %v, want %v", got, want)
			}
			if n := qlScalar(t, e.db, `SELECT count(*) FROM core_enroll_tokens WHERE host_id = ?`, c.host); n != "1" {
				t.Errorf("token rows of %s = %s, want 1", c.host, n)
			}

			rows := e.auditRows(t)
			if len(rows) != 1 {
				t.Fatalf("audit rows = %+v, want exactly one", rows)
			}
			r := rows[0]
			if r.Actor != "local:root@host-local" || r.Action != "controller.quarantine.enroll-token" || r.HostID != c.host ||
				r.Phase != "apply" || r.Result != "ok" || r.ChangeID != "" || len(r.Input) != 0 {
				t.Errorf("audit row = %+v", r)
			}
			if b, _ := json.Marshal(rows); strings.Contains(string(b), qlToken(c.host)) {
				t.Errorf("the audit row carries the token: %s", b)
			}
		})
	}
}

func TestQlEnrollTokenIssuerFailureRollsBack(t *testing.T) {
	e := qlNew(t)
	qlActivate(t, e.db)
	e.issuer.err = errors.New("issuer: secret backend exploded")
	hostsBefore := e.hostStates(t)

	rec := qlCall(t, e.b.enrollTokenHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/enroll-token", `{"hostId":"host-enr"}`)
	qlWantProblem(t, rec, http.StatusInternalServerError, errcode.Internal, "")
	if strings.Contains(rec.Body.String(), "exploded") {
		t.Errorf("the issuer's error reached the client: %s", rec.Body)
	}
	if got := e.issuer.issued(); len(got) != 1 || got[0].stateInTx != "pending" {
		t.Errorf("issuer calls = %+v, want one that saw the host pending", got)
	}
	if got := e.hostStates(t); got != hostsBefore {
		t.Errorf("host states after the issuer failed = %s, want %s (the pending change rolled back)", got, hostsBefore)
	}
	if n := qlScalar(t, e.db, `SELECT count(*) FROM core_enroll_tokens`); n != "0" {
		t.Errorf("token rows = %s, want 0 (the issuer's write rolled back)", n)
	}
	if rows := e.auditRows(t); len(rows) != 0 {
		t.Errorf("audit rows = %+v, want none", rows)
	}
}

func TestQlReleaseRefusesWhileRecordsAreQuarantined(t *testing.T) {
	for _, body := range []string{`{}`, `{"keepUnresolved":false}`} {
		t.Run(body, func(t *testing.T) {
			e := qlNew(t)
			qlActivate(t, e.db)
			qlQuarantine(t, e.db, fxChange{ID: "q1", Action: "core.host.create"}, "applying")
			fxInsertRollout(t, e.db, "q2", "quarantined")
			rec := qlCall(t, e.b.releaseHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/release", body)
			qlWantProblem(t, rec, http.StatusConflict, errcode.Quarantined,
				"2 records are still quarantined; resolve each one, or release with --keep-unresolved")
			if active, err := quarantine.Active(context.Background(), e.db); err != nil || !active {
				t.Errorf("quarantine active = %v, %v after a refused release; want still set", active, err)
			}
			if rows := e.auditRows(t); len(rows) != 0 {
				t.Errorf("audit rows = %+v, want none for a refusal", rows)
			}
		})
	}
}

func TestQlReleaseKeepUnresolvedNamesTheHeldLocks(t *testing.T) {
	e := qlNew(t)
	qlActivate(t, e.db)
	qlQuarantine(t, e.db, fxChange{ID: "q1", Action: "core.host.create"}, "applying")
	qlQuarantine(t, e.db, fxChange{ID: "q2", Action: "core.host.revoke"}, "needs_repair")
	fxInsertLock(t, e.db, "net.b", "q1")
	fxInsertLock(t, e.db, "net.a", "q1")
	fxInsertLock(t, e.db, "dns.x", "q2")

	rec := qlCall(t, e.b.releaseHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/release", `{"keepUnresolved":true}`)
	res := qlDecode[ReleaseResult](t, rec)
	want := []HeldLock{{Key: "dns.x", Record: "q2"}, {Key: "net.a", Record: "q1"}, {Key: "net.b", Record: "q1"}}
	if !reflect.DeepEqual(res.HeldLocks, want) {
		t.Errorf("held locks = %+v, want %+v", res.HeldLocks, want)
	}
	if active, err := quarantine.Active(context.Background(), e.db); err != nil || active {
		t.Errorf("quarantine active = %v, %v after release; want cleared", active, err)
	}
	if got := qlDump(t, e.db, `SELECT id, state FROM core_changes ORDER BY id`); !reflect.DeepEqual(got, []string{"q1|quarantined", "q2|quarantined"}) {
		t.Errorf("records after --keep-unresolved = %v, want both still quarantined", got)
	}
	if got := qlDump(t, e.db, `SELECT key FROM core_locks ORDER BY key`); len(got) != 3 {
		t.Errorf("locks after release = %v, want all three kept", got)
	}
	rows := e.auditRows(t)
	if len(rows) != 1 {
		t.Fatalf("audit rows = %+v, want exactly one", rows)
	}
	if r := rows[0]; r.Actor != "local:root@host-local" || r.Action != "controller.quarantine.release" || r.Phase != "release" ||
		r.Result != "ok" || r.HostID != "" || r.ChangeID != "" || len(r.Input) != 0 {
		t.Errorf("audit row = %+v", r)
	}
}

func TestQlReleaseWithNothingToResolve(t *testing.T) {
	e := qlNew(t)
	qlActivate(t, e.db)
	fxInsertChange(t, e.db, fxChange{ID: "done", Action: "core.host.create", State: "completed"})
	rec := qlCall(t, e.b.releaseHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/release", `{}`)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"heldLocks":[]}` {
		t.Errorf("release = %d %s, want 200 {\"heldLocks\":[]}", rec.Code, rec.Body)
	}
	if active, err := quarantine.Active(context.Background(), e.db); err != nil || active {
		t.Errorf("quarantine active = %v, %v after release; want cleared", active, err)
	}
	if rows := e.auditRows(t); len(rows) != 1 {
		t.Errorf("audit rows = %+v, want one", rows)
	}
}

func TestQlReleaseWhenNotActiveChangesNothing(t *testing.T) {
	e := qlNew(t)
	qlQuarantine(t, e.db, fxChange{ID: "q1", Action: "core.host.create"}, "applying")
	fxInsertLock(t, e.db, "net.a", "q1")
	for _, body := range []string{`{}`, `{"keepUnresolved":true}`} {
		rec := qlCall(t, e.b.releaseHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/release", body)
		res := qlDecode[ReleaseResult](t, rec)
		if !reflect.DeepEqual(res.HeldLocks, []HeldLock{{Key: "net.a", Record: "q1"}}) {
			t.Errorf("body %s: held locks = %+v, want the quarantined record's lock", body, res.HeldLocks)
		}
	}
	if got := qlDump(t, e.db, `SELECT id, state FROM core_changes`); !reflect.DeepEqual(got, []string{"q1|quarantined"}) {
		t.Errorf("records = %v, want unchanged", got)
	}
	if rows := e.auditRows(t); len(rows) != 0 {
		t.Errorf("audit rows = %+v, want none: nothing was released", rows)
	}
}

func TestQlReleaseRefusesBadBodies(t *testing.T) {
	for name, body := range map[string]string{
		"unknown member": `{"keepUnresolved":true,"force":true}`,
		"not JSON":       `release`,
		"two values":     `{} {}`,
		"empty":          ``,
		"over 64 KiB":    `{}` + strings.Repeat(" ", 64<<10),
	} {
		t.Run(name, func(t *testing.T) {
			e := qlNew(t)
			qlActivate(t, e.db)
			rec := qlCall(t, e.b.releaseHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/release", body)
			qlWantProblem(t, rec, http.StatusUnprocessableEntity, errcode.InvalidInput, "")
			if active, err := quarantine.Active(context.Background(), e.db); err != nil || !active {
				t.Errorf("quarantine active = %v, %v; want still set", active, err)
			}
		})
	}
}

func TestQlAnAuditFailureDoesNotHideTheResult(t *testing.T) {
	e := qlNew(t)
	e.deps.Audit = qlFailingAudit{e.audit}
	qlActivate(t, e.db)
	rec := qlCall(t, e.b.releaseHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/release", `{}`)
	if rec.Code != http.StatusOK {
		t.Errorf("release = %d %s, want 200: the release committed", rec.Code, rec.Body)
	}
	if active, err := quarantine.Active(context.Background(), e.db); err != nil || active {
		t.Errorf("quarantine active = %v, %v; want cleared", active, err)
	}
	qlActivate(t, e.db)
	rec = qlCall(t, e.b.enrollTokenHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/enroll-token", `{"hostId":"host-enr"}`)
	if res := qlDecode[EnrollTokenResult](t, rec); res.Token != qlToken("host-enr") {
		t.Errorf("enroll-token answer = %+v, want the token: it is shown once", res)
	}
}

// TestQlAuditRowsFitTheAuditTable writes the rows through the real audit
// store, whose table restricts phase and result.
func TestQlAuditRowsFitTheAuditTable(t *testing.T) {
	e := qlNew(t)
	log := audit.New(e.db, e.deps.Clock)
	e.deps.Audit = log
	qlActivate(t, e.db)
	if rec := qlCall(t, e.b.enrollTokenHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/enroll-token", `{"hostId":"host-pen"}`); rec.Code != http.StatusOK {
		t.Fatalf("enroll-token = %d %s", rec.Code, rec.Body)
	}
	if rec := qlCall(t, e.b.releaseHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/release", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("release = %d %s", rec.Code, rec.Body)
	}
	rows, _, err := log.List(context.Background(), core.AuditQuery{})
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, r := range rows {
		actions = append(actions, fmt.Sprintf("%s/%s/%s", r.Action, r.Phase, r.Result))
	}
	want := []string{"controller.quarantine.release/release/ok", "controller.quarantine.enroll-token/apply/ok"}
	if !reflect.DeepEqual(actions, want) {
		t.Errorf("stored audit rows = %v, want %v (newest first)", actions, want)
	}
}
