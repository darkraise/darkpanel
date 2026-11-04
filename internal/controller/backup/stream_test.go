package backup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/audit"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// bsEnv is a migrated controller database with a key file, a clock, the
// real audit log and the backup handler over them.
type bsEnv struct {
	db      *store.DB
	dir     string // beside controller.db, where the handler makes its temporary copy
	keyPath string
	key     []byte // the key file's bytes
	sealer  *secret.Sealer
	clock   *coretest.FakeClock
	audit   *audit.Store
	b       *Backup
	h       http.Handler
}

func bsNewEnv(t *testing.T) *bsEnv {
	t.Helper()
	dir := t.TempDir()
	db := fxDB(t, dir)
	fxInsertHost(t, db, "host-local", "enrolled", true)
	fxInsertChange(t, db, fxChange{ID: "01BSCHANGE00000000000000AA", HostID: "host-local", Action: "docker.container-stop", State: core.StateApplying})
	keyPath, key, sealer := fxKey(t, t.TempDir())
	clock := coretest.NewFakeClock(fxNow)
	log := audit.New(db, clock)
	deps := ext.Deps{
		DB: db, Sealer: sealer, Clock: clock, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Audit: log, LocalHostID: "host-local",
	}
	b := New(Config{KeyFile: keyPath})
	return &bsEnv{db: db, dir: dir, keyPath: keyPath, key: key, sealer: sealer, clock: clock, audit: log, b: b, h: b.backupHandler(deps)}
}

func bsBody(t *testing.T, passphrase string, p *Params) string {
	t.Helper()
	b, err := json.Marshal(BackupRequest{Passphrase: passphrase, Argon2: p})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func bsRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/controller/backup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func (e *bsEnv) post(body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, bsRequest(body))
	return rec
}

// copies lists the temporary database copies left beside controller.db.
func (e *bsEnv) copies(t *testing.T) []string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(e.dir, "backup-*.db"))
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// rows returns the audit rows of backups, newest first.
func (e *bsEnv) rows(t *testing.T) []core.AuditEntry {
	t.Helper()
	got, _, err := e.audit.List(context.Background(), core.AuditQuery{Action: "controller.backup"})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

type bsProblem struct {
	Status int    `json:"status"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func bsDecodeProblem(t *testing.T, rec *httptest.ResponseRecorder) bsProblem {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json (status %d)", ct, rec.Code)
	}
	var p bsProblem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode the problem: %v", err)
	}
	if p.Status != rec.Code {
		t.Errorf("problem status = %d, response status = %d", p.Status, rec.Code)
	}
	return p
}

// bsOpen decrypts a backup body with Read and returns its header, the members
// by name and their order.
func bsOpen(t *testing.T, body []byte, passphrase string) (Header, map[string][]byte, []string) {
	t.Helper()
	members := map[string][]byte{}
	var order []string
	hdr, err := Read(bytes.NewReader(body), []byte(passphrase), func(name string, size int64, r io.Reader) error {
		b, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		if int64(len(b)) != size {
			return fmt.Errorf("member %s: read %d bytes, size %d", name, len(b), size)
		}
		members[name] = b
		order = append(order, name)
		return nil
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return hdr, members, order
}

func bsQuery(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// bsCanary opens the sealed canary of db with sealer and returns its CreatedAt.
func bsCanary(t *testing.T, sealer *secret.Sealer, db *sql.DB) time.Time {
	t.Helper()
	got := bsQuery(t, db, `SELECT value FROM core_meta WHERE key = '`+CanaryKey+`'`)
	if len(got) != 1 {
		t.Fatalf("core_meta holds %d %s rows, want 1", len(got), CanaryKey)
	}
	sealed, err := base64.StdEncoding.DecodeString(got[0])
	if err != nil {
		t.Fatalf("the canary is not base64: %v", err)
	}
	plain, err := sealer.Open(CanaryAD, sealed)
	if err != nil {
		t.Fatalf("the key does not open the canary: %v", err)
	}
	var c Canary
	if err := json.Unmarshal(plain, &c); err != nil {
		t.Fatalf("decode the canary: %v", err)
	}
	return c.CreatedAt
}

func TestBsRoundTrip(t *testing.T) {
	e := bsNewEnv(t)
	passphrase := strings.Repeat("p", 1024)

	rec := e.post(bsBody(t, passphrase, &TestParams))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %.300s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", got)
	}
	disposition := regexp.MustCompile(`^attachment; filename="darkpanel-controller-20261104T120000Z\.dpbk"$`)
	if got := rec.Header().Get("Content-Disposition"); !disposition.MatchString(got) {
		t.Errorf("Content-Disposition = %q, want the UTC time of the clock in the file name", got)
	}

	hdr, members, order := bsOpen(t, rec.Body.Bytes(), passphrase)
	if hdr.Params != TestParams {
		t.Errorf("header params = %+v, want the requested %+v", hdr.Params, TestParams)
	}
	if len(order) != 2 || order[0] != MemberDB || order[1] != MemberKey {
		t.Fatalf("members = %v, want [%s %s]", order, MemberDB, MemberKey)
	}
	if !bytes.Equal(members[MemberKey], e.key) {
		t.Error("the key member differs from the key file")
	}

	restored := filepath.Join(t.TempDir(), "restored.db")
	if err := os.WriteFile(restored, members[MemberDB], 0o600); err != nil {
		t.Fatal(err)
	}
	rdb, err := store.Open(context.Background(), restored)
	if err != nil {
		t.Fatalf("open the database member: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	for _, q := range []string{
		`SELECT id || '|' || state FROM core_changes ORDER BY id`,
		`SELECT id FROM core_hosts ORDER BY id`,
		`SELECT module || '|' || number FROM core_migrations ORDER BY module, number`,
		`SELECT key || '=' || value FROM core_meta ORDER BY key`,
	} {
		live, got := bsQuery(t, e.db.Read(), q), bsQuery(t, rdb.Read(), q)
		if strings.Join(live, "\n") != strings.Join(got, "\n") {
			t.Errorf("%s\nlive:     %q\nrestored: %q", q, live, got)
		}
	}
	if got := bsCanary(t, e.sealer, rdb.Read()); !got.Equal(fxNow) {
		t.Errorf("canary createdAt = %s, want the clock's %s", got, fxNow)
	}
	if left := e.copies(t); len(left) != 0 {
		t.Errorf("temporary copies left after a backup: %v", left)
	}
}

func TestBsDefaultParams(t *testing.T) {
	e := bsNewEnv(t)
	rec := e.post(`{"passphrase":"p"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %.300s)", rec.Code, rec.Body.String())
	}
	h := rec.Body.Bytes()
	if len(h) < HeaderSize || string(h[:4]) != Magic || h[4] != FormatVersion {
		t.Fatalf("the body does not start with a backup header")
	}
	got := Params{T: binary.BigEndian.Uint32(h[5:9]), M: binary.BigEndian.Uint32(h[9:13]), P: h[13]}
	if got != DefaultParams {
		t.Errorf("header params = %+v, want the defaults %+v", got, DefaultParams)
	}
}

func TestBsRefusesBadRequests(t *testing.T) {
	e := bsNewEnv(t)
	cases := []struct{ name, body, path string }{
		{"empty body", ``, ""},
		{"not JSON", `passphrase`, ""},
		{"two JSON values", `{"passphrase":"x"}{"passphrase":"x"}`, ""},
		{"unknown field", `{"passphrase":"x","extra":1}`, ""},
		{"empty passphrase", `{"passphrase":""}`, ""},
		{"missing passphrase", `{}`, ""},
		{"passphrase of 1025 bytes", `{"passphrase":"` + strings.Repeat("a", 1025) + `"}`, ""},
		{"body over 64 KiB", `{"passphrase":"` + strings.Repeat("a", 70000) + `"}`, ""},
		{"argon2.t 0", `{"passphrase":"x","argon2":{"t":0,"m":65536,"p":1}}`, "argon2.t"},
		{"argon2.t 11", `{"passphrase":"x","argon2":{"t":11,"m":65536,"p":1}}`, "argon2.t"},
		{"argon2.m 65535", `{"passphrase":"x","argon2":{"t":1,"m":65535,"p":1}}`, "argon2.m"},
		{"argon2.m 1048577", `{"passphrase":"x","argon2":{"t":1,"m":1048577,"p":1}}`, "argon2.m"},
		{"argon2.p 0", `{"passphrase":"x","argon2":{"t":1,"m":65536,"p":0}}`, "argon2.p"},
		{"argon2.p 17", `{"passphrase":"x","argon2":{"t":1,"m":65536,"p":17}}`, "argon2.p"},
		{"argon2.p 300", `{"passphrase":"x","argon2":{"t":1,"m":65536,"p":300}}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := e.post(c.body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (body %.300s)", rec.Code, rec.Body.String())
			}
			if p := bsDecodeProblem(t, rec); p.Code != "request.invalid_input" {
				t.Errorf("code = %q, want request.invalid_input", p.Code)
			}
			if c.path != "" && !strings.Contains(rec.Body.String(), c.path) {
				t.Errorf("the problem does not name %s: %s", c.path, rec.Body.String())
			}
			if e.b.running.Load() {
				t.Error("a refused request left the backup slot taken")
			}
		})
	}
	if got := bsQuery(t, e.db.Read(), `SELECT key FROM core_meta WHERE key = '`+CanaryKey+`'`); len(got) != 0 {
		t.Error("a refused request wrote the canary")
	}
	if left := e.copies(t); len(left) != 0 {
		t.Errorf("temporary copies left after refused requests: %v", left)
	}
	if rows := e.rows(t); len(rows) != 0 {
		t.Errorf("refused requests wrote %d audit rows", len(rows))
	}
}

// bsGate is a response writer whose first body write waits until open is called.
type bsGate struct {
	header   http.Header
	status   int
	entered  chan struct{} // closed when the first body write begins
	release  chan struct{}
	enter    sync.Once
	openOnce sync.Once
}

func bsNewGate() *bsGate {
	return &bsGate{header: http.Header{}, entered: make(chan struct{}), release: make(chan struct{})}
}

func (g *bsGate) Header() http.Header  { return g.header }
func (g *bsGate) WriteHeader(code int) { g.status = code }
func (g *bsGate) Write(p []byte) (int, error) {
	g.enter.Do(func() { close(g.entered) })
	<-g.release
	return len(p), nil
}
func (g *bsGate) open() { g.openOnce.Do(func() { close(g.release) }) }

func TestBsRefusesASecondBackupWhileOneStreams(t *testing.T) {
	e := bsNewEnv(t)
	gate := bsNewGate()
	t.Cleanup(gate.open)
	first := bsRequest(bsBody(t, "first", &TestParams))
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.h.ServeHTTP(gate, first)
	}()
	select {
	case <-gate.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the first backup never began to stream")
	}

	rec := e.post(bsBody(t, "second", &TestParams))
	if rec.Code != http.StatusConflict {
		t.Fatalf("second backup status = %d, want 409 (body %.300s)", rec.Code, rec.Body.String())
	}
	p := bsDecodeProblem(t, rec)
	if p.Code != "core.change_busy" || !strings.Contains(p.Detail, "another backup is running") {
		t.Errorf("problem = %+v, want core.change_busy naming the running backup", p)
	}

	gate.open()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the first backup never finished")
	}
	if gate.status != http.StatusOK {
		t.Errorf("first backup status = %d, want 200", gate.status)
	}
	if rec := e.post(bsBody(t, "third", &TestParams)); rec.Code != http.StatusOK {
		t.Errorf("a backup after the first finished: status = %d, want 200 (body %.300s)", rec.Code, rec.Body.String())
	}
	if rows := e.rows(t); len(rows) != 2 {
		t.Errorf("audit rows = %d, want 2 (the refused request writes none)", len(rows))
	}
}

func TestBsKeyFileRules(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(t *testing.T, e *bsEnv)
		wantOK  bool
	}{
		{"symbolic link", func(t *testing.T, e *bsEnv) {
			target := filepath.Join(t.TempDir(), "real.key")
			if err := os.WriteFile(target, e.key, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(e.keyPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, e.keyPath); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"directory", func(t *testing.T, e *bsEnv) {
			if err := os.Remove(e.keyPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(e.keyPath, 0o700); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"missing", func(t *testing.T, e *bsEnv) {
			if err := os.Remove(e.keyPath); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"one byte over the limit", func(t *testing.T, e *bsEnv) {
			if err := os.WriteFile(e.keyPath, bytes.Repeat([]byte("k"), MaxKeyMember+1), 0o600); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"exactly the limit", func(t *testing.T, e *bsEnv) {
			if err := os.WriteFile(e.keyPath, bytes.Repeat([]byte("k"), MaxKeyMember), 0o600); err != nil {
				t.Fatal(err)
			}
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := bsNewEnv(t)
			c.arrange(t, e)
			rec := e.post(bsBody(t, "p", &TestParams))
			if c.wantOK {
				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200 (body %.300s)", rec.Code, rec.Body.String())
				}
				_, members, _ := bsOpen(t, rec.Body.Bytes(), "p")
				if len(members[MemberKey]) != MaxKeyMember {
					t.Errorf("key member is %d bytes, want %d", len(members[MemberKey]), MaxKeyMember)
				}
				return
			}
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500 (body %.300s)", rec.Code, rec.Body.String())
			}
			if p := bsDecodeProblem(t, rec); p.Code != "internal" {
				t.Errorf("code = %q, want internal", p.Code)
			}
			if strings.Contains(rec.Body.String(), secret.KeyPrefix) {
				t.Error("the problem carries key file bytes")
			}
			if left := e.copies(t); len(left) != 0 {
				t.Errorf("temporary copy left after a refused key file: %v", left)
			}
			if rows := e.rows(t); len(rows) != 0 {
				t.Errorf("a backup that never streamed wrote %d audit rows", len(rows))
			}
		})
	}
}

// bsBrokenWriter accepts its first body write and fails every later one, as
// a client that disconnects in the middle of the download.
type bsBrokenWriter struct {
	header http.Header
	status int
	writes int
}

func (w *bsBrokenWriter) Header() http.Header  { return w.header }
func (w *bsBrokenWriter) WriteHeader(code int) { w.status = code }
func (w *bsBrokenWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes > 1 {
		return 0, errors.New("broken pipe")
	}
	return len(p), nil
}

// bsServe runs h and returns what it panicked with, nil when it returned.
func bsServe(h http.Handler, w http.ResponseWriter, r *http.Request) (recovered any) {
	defer func() { recovered = recover() }()
	h.ServeHTTP(w, r)
	return nil
}

func TestBsAbortsTheResponseWhenAWriteFailsAfterTheFirstByte(t *testing.T) {
	e := bsNewEnv(t)
	w := &bsBrokenWriter{header: http.Header{}}

	recovered := bsServe(e.h, w, bsRequest(bsBody(t, "p", &TestParams)))
	if err, ok := recovered.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
		t.Fatalf("handler panicked with %v, want http.ErrAbortHandler", recovered)
	}
	if w.status != http.StatusOK || w.writes < 2 {
		t.Errorf("status = %d after %d body writes, want 200 and a failure on a later write", w.status, w.writes)
	}
	if left := e.copies(t); len(left) != 0 {
		t.Errorf("temporary copy left after an aborted response: %v", left)
	}
	if rec := e.post(bsBody(t, "p", &TestParams)); rec.Code != http.StatusOK {
		t.Errorf("a backup after the aborted one: status = %d, want 200 (the slot must be free)", rec.Code)
	}
	var ok, failed int
	for _, r := range e.rows(t) {
		switch {
		case r.Result == "ok":
			ok++
		case r.Result == "error" && r.ErrorCode == "internal":
			failed++
		}
	}
	if ok != 1 || failed != 1 {
		t.Errorf("audit rows: %d ok and %d failed, want one of each", ok, failed)
	}
}

func TestBsAuditsOneRowWithoutThePassphrase(t *testing.T) {
	e := bsNewEnv(t)
	const passphrase = "correct horse battery staple 7f3a91"

	if rec := e.post(bsBody(t, passphrase, &TestParams)); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %.300s)", rec.Code, rec.Body.String())
	}
	rows := e.rows(t)
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
	r := rows[0]
	if r.Actor != "local:root@host-local" || r.Action != "controller.backup" || r.Phase != "apply" || r.Result != "ok" {
		t.Errorf("row = %+v, want actor local:root@host-local, action controller.backup, phase apply, result ok", r)
	}
	if r.ErrorCode != "" || r.ChangeID != "" || r.HostID != "" || len(r.Input) != 0 {
		t.Errorf("row = %+v, want no error code, change, host or input", r)
	}
	if !r.At.Equal(fxNow) {
		t.Errorf("row time = %s, want the clock's %s", r.At, fxNow)
	}
	if strings.Contains(fmt.Sprintf("%+v", r), passphrase) {
		t.Error("the audit row carries the passphrase")
	}
}

func TestBsRefreshesTheCanaryBeforeEachCopy(t *testing.T) {
	e := bsNewEnv(t)
	for i := range 2 {
		want := fxNow.Add(time.Duration(i) * time.Hour)
		if i > 0 {
			e.clock.Advance(time.Hour)
		}
		if rec := e.post(bsBody(t, "p", &TestParams)); rec.Code != http.StatusOK {
			t.Fatalf("backup %d: status = %d, want 200 (body %.300s)", i, rec.Code, rec.Body.String())
		}
		if got := bsCanary(t, e.sealer, e.db.Read()); !got.Equal(want) {
			t.Errorf("after backup %d the canary createdAt = %s, want %s", i, got, want)
		}
	}
}

// bsFailingReader fails every read, as a random source that is unavailable.
type bsFailingReader struct{}

func (bsFailingReader) Read([]byte) (int, error) { return 0, errors.New("no randomness") }

func TestBsAuditsAFailureBeforeTheFirstByte(t *testing.T) {
	e := bsNewEnv(t)
	deps := ext.Deps{
		DB: e.db, Sealer: e.sealer, Clock: e.clock, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Audit: e.audit, LocalHostID: "host-local",
	}
	rec := httptest.NewRecorder()
	e.b.streamServe(deps, bsFailingReader{}).ServeHTTP(rec, bsRequest(bsBody(t, "p", &TestParams)))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body %.300s)", rec.Code, rec.Body.String())
	}
	if p := bsDecodeProblem(t, rec); p.Code != "internal" {
		t.Errorf("code = %q, want internal", p.Code)
	}
	rows := e.rows(t)
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
	if r := rows[0]; r.Result != "error" || r.ErrorCode != "internal" {
		t.Errorf("row = %+v, want result error with error code internal", r)
	}
	if left := e.copies(t); len(left) != 0 {
		t.Errorf("temporary copy left after a failed backup: %v", left)
	}
	if e.b.running.Load() {
		t.Error("a failed backup left the backup slot taken")
	}
}
