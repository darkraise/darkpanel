package backup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

// TestRoContributeRoutes: the contribution is exactly the five routes of the
// spec, all on the socket surface, and only the backup stream is a Stream
// route. Contribute reads nothing from its deps while building them.
func TestRoContributeRoutes(t *testing.T) {
	routes, services, err := New(Config{}).Contribute(ext.Deps{})
	if err != nil {
		t.Fatalf("Contribute: %v", err)
	}
	if len(services) != 0 {
		t.Errorf("Contribute returned %d services, want none", len(services))
	}
	want := []struct {
		method, path string
		stream       bool
	}{
		{http.MethodPost, "/api/v1/controller/backup", true},
		{http.MethodGet, "/api/v1/controller/quarantine", false},
		{http.MethodPost, "/api/v1/controller/quarantine/{record}/resolve", false},
		{http.MethodPost, "/api/v1/controller/quarantine/enroll-token", false},
		{http.MethodPost, "/api/v1/controller/quarantine/release", false},
	}
	if len(routes) != len(want) {
		t.Fatalf("Contribute returned %d routes, want %d", len(routes), len(want))
	}
	mux := http.NewServeMux()
	for i, w := range want {
		rt := routes[i]
		if rt.Surface != ext.SurfaceSocket || rt.Method != w.method || rt.Path != w.path || rt.Stream != w.stream {
			t.Errorf("route %d = surface %d %s %s stream %t, want a socket %s %s with stream %t",
				i, rt.Surface, rt.Method, rt.Path, rt.Stream, w.method, w.path, w.stream)
		}
		if rt.Handler == nil || rt.Summary == "" {
			t.Errorf("route %d (%s %s) has handler %v and summary %q, want both set", i, rt.Method, rt.Path, rt.Handler, rt.Summary)
			continue
		}
		// ServeMux panics on a pattern that conflicts with an earlier one,
		// which fails startup for a contributed route.
		mux.Handle(rt.Method+" "+rt.Path, rt.Handler)
	}
}

const (
	roToken   = "dpt-ro-enrollment-token" //nolint:gosec // G101: a test value, not a credential
	roAddress = "agents.example.test:7443"
	roTx      = "01JRESTORETX00000000000001"
	roPass    = "ro-backup-passphrase" //nolint:gosec // G101: a test value, not a credential
)

// roIssuer is a TokenIssuer that records what the route asked and the state
// of the host it saw inside the transaction it was given.
type roIssuer struct {
	hostID, address, stateSeen string
}

func (i *roIssuer) IssueToken(ctx context.Context, tx *sql.Tx, hostID, address string) (string, time.Time, error) {
	i.hostID, i.address = hostID, address
	if err := tx.QueryRowContext(ctx, `SELECT state FROM core_hosts WHERE id = ?`, hostID).Scan(&i.stateSeen); err != nil {
		return "", time.Time{}, err
	}
	return roToken, fxNow.Add(time.Hour), nil
}

// roEnv is a quarantined controller database behind the five routes, served
// over HTTP on 127.0.0.1.
type roEnv struct {
	dir     string
	db      *store.DB
	keyPath string
	issuer  *roIssuer
	audit   *audit.Store
	srv     *httptest.Server
}

// newRoEnv marks a database holding a composite and a single change, both
// non-terminal, the single change with a lock, and a completed change.
func newRoEnv(t *testing.T) *roEnv {
	t.Helper()
	dir := t.TempDir()
	db := fxDB(t, dir)
	keyPath, _, sealer := fxKey(t, t.TempDir())
	fxInsertHost(t, db, "h-local", "enrolled", true)
	fxInsertHost(t, db, "h-agent", "enrolled", false)
	fxInsertComposite(t, db, "c-comp", core.StateApplying, "forward", []fxStep{
		{ID: "s1", HostID: "h-agent", Action: "demo.alpha/v1", State: "applied"},
		{ID: "s2", HostID: "h-agent", Action: "demo.beta/v1", State: "pending"},
	})
	fxInsertChange(t, db, fxChange{ID: "c-single", HostID: "h-agent", Action: "firewall.rule.add", State: core.StatePending})
	fxInsertLock(t, db, "firewall.rule:8191", "c-single")
	fxInsertChange(t, db, fxChange{ID: "c-done", HostID: "h-agent", Action: "firewall.rule.add", State: core.StateCompleted})
	marker := quarantine.Marker{Tx: roTx, RestoredAt: fxNow, BackupCreatedAt: fxNow.Add(-2 * time.Hour)}
	if err := Mark(context.Background(), db, marker); err != nil {
		t.Fatalf("Mark: %v", err)
	}

	issuer := &roIssuer{}
	auditLog := audit.New(db, coretest.NewFakeClock(fxNow))
	deps := ext.Deps{
		DB: db, Sealer: sealer, Clock: coretest.NewFakeClock(fxNow),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Audit: auditLog, LocalHostID: "h-local",
	}
	routes, _, err := New(Config{KeyFile: keyPath, Tokens: issuer, AgentAdvertise: roAddress}).Contribute(deps)
	if err != nil {
		t.Fatalf("Contribute: %v", err)
	}
	mux := http.NewServeMux()
	for _, rt := range routes {
		mux.Handle(rt.Method+" "+rt.Path, rt.Handler)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &roEnv{dir: dir, db: db, keyPath: keyPath, issuer: issuer, audit: auditLog, srv: srv}
}

// call sends one request to the mounted routes and returns the status, the
// headers and the whole body.
func (e *roEnv) call(t *testing.T, method, path, body string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: read the answer: %v", method, path, err)
	}
	return resp.StatusCode, resp.Header, data
}

// query returns the single text column of a query on the database.
func (e *roEnv) query(t *testing.T, query string, args ...any) string {
	t.Helper()
	var s string
	if err := e.db.Read().QueryRowContext(context.Background(), query, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return s
}

func roDecode[T any](t *testing.T, data []byte) T {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var v T
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
	return v
}

// roProblemCode returns the code of an application/problem+json answer.
func roProblemCode(t *testing.T, h http.Header, data []byte) string {
	t.Helper()
	if ct := h.Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Fatalf("Content-Type %q, want application/problem+json; body %q", ct, data)
	}
	var p struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("decode the problem %q: %v", data, err)
	}
	return p.Code
}

// TestRoRoutesEndToEnd drives the mounted routes through a restored
// controller's recovery: list, resolve a composite to repair, issue an
// enrollment token, a release refused while a record is unresolved, a release
// that keeps it, and then a backup of the database, read back with Read.
func TestRoRoutesEndToEnd(t *testing.T) {
	e := newRoEnv(t)
	const quarantinePath = "/api/v1/controller/quarantine"

	code, _, data := e.call(t, http.MethodGet, quarantinePath, "")
	if code != http.StatusOK {
		t.Fatalf("GET quarantine = %d %s", code, data)
	}
	st := roDecode[Status](t, data)
	if !st.Active || st.Tx != roTx || st.RestoredAt == nil || !st.RestoredAt.Equal(fxNow) || len(st.Records) != 2 {
		t.Fatalf("GET quarantine = %+v, want active in %s since %s with two records", st, roTx, fxNow)
	}
	comp, single := st.Records[0], st.Records[1]
	if comp.ID != "c-comp" || comp.Kind != "composite" || comp.QuarantinedFrom != core.StateApplying {
		t.Errorf("first record = %+v, want composite c-comp quarantined from %s", comp, core.StateApplying)
	}
	if single.ID != "c-single" || single.Kind != "change" || single.Action != "firewall.rule.add" ||
		single.HostID != "h-agent" || single.QuarantinedFrom != core.StatePending ||
		len(single.Locks) != 1 || single.Locks[0] != "firewall.rule:8191" {
		t.Errorf("second record = %+v, want change c-single of firewall.rule.add on h-agent quarantined from %s holding firewall.rule:8191",
			single, core.StatePending)
	}
	var rawList struct {
		Records []struct {
			Locks json.RawMessage `json:"locks"`
		} `json:"records"`
	}
	if err := json.Unmarshal(data, &rawList); err != nil || string(rawList.Records[0].Locks) != "[]" {
		t.Errorf("the lock list of a record without locks is %s (%v), want [] and not null", rawList.Records[0].Locks, err)
	}

	code, _, data = e.call(t, http.MethodPost, quarantinePath+"/c-comp/resolve", `{"outcome":"repair"}`)
	if code != http.StatusOK {
		t.Fatalf("resolve c-comp repair = %d %s", code, data)
	}
	if rec := roDecode[Record](t, data); rec.ID != "c-comp" || rec.Kind != "composite" || rec.QuarantinedFrom != core.StateApplying {
		t.Errorf("resolve answered %+v, want the composite with its quarantinedFrom kept", rec)
	}
	if got := e.query(t, `SELECT state || '|' || error_code FROM core_changes WHERE id = 'c-comp'`); got != "needs_repair|core.needs_repair" {
		t.Errorf("c-comp is %q after resolve repair, want needs_repair|core.needs_repair", got)
	}
	code, h, data := e.call(t, http.MethodPost, quarantinePath+"/c-comp/resolve", `{"outcome":"repair"}`)
	if got := roProblemCode(t, h, data); code != http.StatusConflict || got != string(errcode.PlanStateChanged) {
		t.Errorf("resolving c-comp twice = %d %s, want 409 %s", code, got, errcode.PlanStateChanged)
	}

	code, _, data = e.call(t, http.MethodPost, quarantinePath+"/enroll-token", `{"hostId":"h-agent"}`)
	if code != http.StatusOK {
		t.Fatalf("enroll-token = %d %s", code, data)
	}
	if res := roDecode[EnrollTokenResult](t, data); res.Token != roToken || !res.ExpiresAt.Equal(fxNow.Add(time.Hour)) {
		t.Errorf("enroll-token answered %+v, want the issuer's token expiring at %s", res, fxNow.Add(time.Hour))
	}
	if e.issuer.hostID != "h-agent" || e.issuer.address != roAddress || e.issuer.stateSeen != "pending" {
		t.Errorf("the issuer saw host %q, address %q, state %q; want h-agent, %s and pending inside the route's transaction",
			e.issuer.hostID, e.issuer.address, e.issuer.stateSeen, roAddress)
	}

	code, h, data = e.call(t, http.MethodPost, quarantinePath+"/release", `{"keepUnresolved":false}`)
	if got := roProblemCode(t, h, data); code != http.StatusConflict || got != string(errcode.Quarantined) {
		t.Errorf("release with c-single unresolved = %d %s, want 409 %s", code, got, errcode.Quarantined)
	}
	if active, err := quarantine.Active(context.Background(), e.db); err != nil || !active {
		t.Errorf("the quarantine is active %t (%v) after a refused release, want true", active, err)
	}

	code, _, data = e.call(t, http.MethodPost, quarantinePath+"/release", `{"keepUnresolved":true}`)
	if code != http.StatusOK {
		t.Fatalf("release --keep-unresolved = %d %s", code, data)
	}
	if res := roDecode[ReleaseResult](t, data); len(res.HeldLocks) != 1 || res.HeldLocks[0] != (HeldLock{Key: "firewall.rule:8191", Record: "c-single"}) {
		t.Errorf("release answered %+v, want firewall.rule:8191 held by c-single", res)
	}
	if active, err := quarantine.Active(context.Background(), e.db); err != nil || active {
		t.Errorf("the quarantine is active %t (%v) after the release, want false", active, err)
	}
	if got := e.query(t, `SELECT state FROM core_changes WHERE id = 'c-single'`); got != core.StateQuarantined {
		t.Errorf("c-single is %q after the release, want it left %s", got, core.StateQuarantined)
	}

	code, _, data = e.call(t, http.MethodGet, quarantinePath, "")
	if st = roDecode[Status](t, data); code != http.StatusOK || st.Active || len(st.Records) != 1 || st.Records[0].ID != "c-single" {
		t.Errorf("GET quarantine after the release = %d %+v, want inactive with only c-single", code, st)
	}

	e.checkBackup(t)
	e.checkAudit(t)
}

// checkBackup streams a backup, reads it back with Read and checks that the
// database member passes the restore's own validation with the key member.
func (e *roEnv) checkBackup(t *testing.T) {
	t.Helper()
	body := `{"passphrase":"` + roPass + `","argon2":{"t":1,"m":65536,"p":1}}`
	code, h, data := e.call(t, http.MethodPost, "/api/v1/controller/backup", body)
	if code != http.StatusOK {
		t.Fatalf("backup = %d %s", code, data)
	}
	if ct := h.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("backup Content-Type = %q, want application/octet-stream", ct)
	}
	disposition := regexp.MustCompile(`^attachment; filename="darkpanel-controller-\d{8}T\d{6}Z\.dpbk"$`)
	if cd := h.Get("Content-Disposition"); !disposition.MatchString(cd) {
		t.Errorf("backup Content-Disposition = %q, want attachment; filename=\"darkpanel-controller-<UTC time>.dpbk\"", cd)
	}

	var names []string
	members := map[string][]byte{}
	_, err := Read(bytes.NewReader(data), []byte(roPass), func(name string, size int64, body io.Reader) error {
		b, err := io.ReadAll(body)
		if err != nil {
			return err
		}
		if int64(len(b)) != size {
			t.Errorf("member %s: %d bytes read, size %d announced", name, len(b), size)
		}
		names = append(names, name)
		members[name] = b
		return nil
	})
	if err != nil {
		t.Fatalf("Read of the streamed backup: %v", err)
	}
	if len(names) != 2 || names[0] != MemberDB || names[1] != MemberKey {
		t.Fatalf("the backup holds members %v, want [%s %s]", names, MemberDB, MemberKey)
	}
	wantKey, err := os.ReadFile(e.keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(members[MemberKey], wantKey) {
		t.Error("the key member differs from the controller key file")
	}
	staged := filepath.Join(t.TempDir(), "controller.db")
	if err := os.WriteFile(staged, members[MemberDB], 0o600); err != nil {
		t.Fatal(err)
	}
	createdAt, err := ValidateStaged(context.Background(), staged, members[MemberKey])
	if err != nil {
		t.Fatalf("ValidateStaged of the backed-up database: %v", err)
	}
	if !createdAt.Equal(fxNow) {
		t.Errorf("the backup canary says it was created at %s, want %s", createdAt, fxNow)
	}
	if left, _ := filepath.Glob(filepath.Join(e.dir, "backup-*.db")); len(left) != 0 {
		t.Errorf("the backup left %v beside the database", left)
	}
}

// checkAudit requires one audit action per kind of route used, all by the
// local root actor, and no secret in any row.
func (e *roEnv) checkAudit(t *testing.T) {
	t.Helper()
	entries, _, err := e.audit.List(context.Background(), core.AuditQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]core.AuditEntry{}
	for _, en := range entries {
		seen[en.Action] = en
		if en.Actor != "local:root@h-local" {
			t.Errorf("audit row %s has actor %q, want local:root@h-local", en.Action, en.Actor)
		}
		b, _ := json.Marshal(en)
		for _, secret := range []string{roPass, roToken} {
			if strings.Contains(string(b), secret) {
				t.Errorf("audit row %s holds a secret: %s", en.Action, b)
			}
		}
	}
	for _, action := range []string{"controller.backup", "controller.quarantine.resolve", "controller.quarantine.enroll-token", "controller.quarantine.release"} {
		if _, ok := seen[action]; !ok {
			t.Errorf("no audit row for %s", action)
		}
	}
	if en := seen["controller.quarantine.resolve"]; en.ChangeID != "c-comp" {
		t.Errorf("the resolve audit row names change %q, want c-comp", en.ChangeID)
	}
	if en := seen["controller.quarantine.enroll-token"]; en.HostID != "h-agent" {
		t.Errorf("the enroll-token audit row names host %q, want h-agent", en.HostID)
	}
}
