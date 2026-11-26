package console

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform"
)

// spyFS is the platform file manager with a hook run before each write.
type spyFS struct {
	core.ConsoleFiles
	beforeWrite func()
}

func (s *spyFS) WriteText(ctx context.Context, path string, data []byte, expectSHA string) (string, error) {
	if s.beforeWrite != nil {
		s.beforeWrite()
	}
	return s.ConsoleFiles.WriteText(ctx, path, data, expectSHA)
}

func (s *spyFS) Upload(ctx context.Context, path string, r io.Reader, size int64) (string, error) {
	if s.beforeWrite != nil {
		s.beforeWrite()
	}
	return s.ConsoleFiles.Upload(ctx, path, r, size)
}

// filesRig is an agent runtime with the console on, its file manager over
// a temporary root, and the agent API in front of it.
type filesRig struct {
	t    *testing.T
	root string
	dir  string
	db   *store.DB
	rt   *runtime.Runtime
	h    http.Handler
	free atomic.Int64
	spy  *spyFS
}

func newFilesRig(t *testing.T) *filesRig {
	t.Helper()
	r := &filesRig{t: t, root: t.TempDir(), dir: t.TempDir()}
	r.free.Store(1 << 40)
	for _, d := range []string{"srv", "etc/darkpanel"} {
		if err := os.MkdirAll(filepath.Join(r.root, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	db, err := store.Open(context.Background(), filepath.Join(r.dir, "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	r.db = db
	r.spy = &spyFS{ConsoleFiles: platform.NewConsoleFiles(platform.ConsoleFilesConfig{
		Root: r.root, Binary: "/usr/local/bin/darkpanel", FreeSpace: func(int) (int64, error) { return r.free.Load(), nil },
	})}
	r.open()
	if err := r.rt.SetModuleEnabled(context.Background(), ID, true); err != nil {
		t.Fatal(err)
	}
	return r
}

// open starts a runtime, as an agent start does, with a new module value.
func (r *filesRig) open() {
	r.t.Helper()
	reg, err := core.NewRegistry(Module())
	if err != nil {
		r.t.Fatal(err)
	}
	rt, err := runtime.Open(context.Background(), runtime.Config{
		DB: r.db, Sealer: coretest.Sealer{}, Clock: coretest.NewFakeClock(t0), Log: slog.New(slog.DiscardHandler),
		Registry: reg, HostID: "h1", BinaryVersion: "test", BackupDir: filepath.Join(r.dir, "backups"),
		Exec: platform.NewRunner(nil), FilesTestMode: true, ConsoleFS: r.spy,
	})
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(rt.Close)
	r.rt = rt
	r.h = api.NewHandler(api.Config{Agent: rt, Log: slog.New(slog.DiscardHandler)})
}

func (r *filesRig) path(p string) string { return filepath.Join(r.root, p) }

func (r *filesRig) write(p, data string) {
	r.t.Helper()
	if err := os.WriteFile(r.path(p), []byte(data), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

func principalHeader(t *testing.T, p core.Principal) string {
	t.Helper()
	b, err := json.Marshal(wire.FromPrincipal(p))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (r *filesRig) request(method, op string, q url.Values, body io.Reader, ctype string) *http.Request {
	req := httptest.NewRequest(method, "/agent/v1/files/"+op+"?"+q.Encode(), body)
	req.Header.Set(wire.HeaderPrincipal, principalHeader(r.t, adminP))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	return req
}

func (r *filesRig) serve(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	return rec
}

func (r *filesRig) get(op string, q url.Values) *httptest.ResponseRecorder {
	return r.serve(r.request(http.MethodGet, op, q, nil, ""))
}

func (r *filesRig) raw(op string, q url.Values, body []byte) *httptest.ResponseRecorder {
	return r.serve(r.request(http.MethodPost, op, q, bytes.NewReader(body), "application/octet-stream"))
}

func (r *filesRig) change(op string, body any) *httptest.ResponseRecorder {
	b, err := json.Marshal(body)
	if err != nil {
		r.t.Fatal(err)
	}
	return r.serve(r.request(http.MethodPost, op, nil, bytes.NewReader(b), "application/json"))
}

func problemCode(rec *httptest.ResponseRecorder) errcode.Code {
	var p struct {
		Code errcode.Code `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	return p.Code
}

func wantProblem(t *testing.T, what string, rec *httptest.ResponseRecorder, status int, code errcode.Code) {
	t.Helper()
	if rec.Code != status || problemCode(rec) != code {
		t.Errorf("%s: %d %s, want %d %s", what, rec.Code, rec.Body.String(), status, code)
	}
}

func q(kv ...string) url.Values {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return v
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestListPaging(t *testing.T) {
	r := newFilesRig(t)
	big := r.path("srv/big")
	if err := os.Mkdir(big, 0o750); err != nil {
		t.Fatal(err)
	}
	var want []string
	for i := range 100 {
		name := fmt.Sprintf("d%03d", i)
		if err := os.Mkdir(filepath.Join(big, name), 0o750); err != nil {
			t.Fatal(err)
		}
		want = append(want, name)
	}
	var files []string
	for i := range 2399 {
		files = append(files, fmt.Sprintf("f%04d", 2398-i))
	}
	bad := "bad\xff\xfename"
	files = append(files, bad)
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(big, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(files)
	want = append(want, files...)

	var got []core.FileInfo
	cursor := ""
	for page := 0; ; page++ {
		rec := r.get("list", q("path", "/srv/big", "cursor", cursor))
		if rec.Code != http.StatusOK {
			t.Fatalf("page %d: %d %s", page, rec.Code, rec.Body.String())
		}
		var p core.DirPage
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		if wantLen := []int{2000, 500}[page]; len(p.Entries) != wantLen {
			t.Fatalf("page %d has %d entries, want %d", page, len(p.Entries), wantLen)
		}
		got = append(got, p.Entries...)
		if p.Next == "" {
			if page != 1 {
				t.Fatalf("the listing ended after page %d", page)
			}
			break
		}
		cursor = p.Next
	}
	for i, e := range got {
		name := e.Name
		if e.Escaped {
			raw, err := base64.StdEncoding.DecodeString(e.Raw)
			if err != nil {
				t.Fatal(err)
			}
			name = string(raw)
		}
		if name != want[i] {
			t.Fatalf("entry %d is %q, want %q", i, name, want[i])
		}
		if wantType := core.FileTypeDir; i >= 100 && e.Type == wantType || i < 100 && e.Type != wantType {
			t.Fatalf("entry %d (%s) has type %s", i, e.Name, e.Type)
		}
		if name == bad && (!e.Escaped || e.Name != `bad\xff\xfename`) {
			t.Errorf("the non-UTF-8 name is %q (escaped %v), want bad\\xff\\xfename", e.Name, e.Escaped)
		}
	}
	wantProblem(t, "a foreign cursor", r.get("list", q("path", "/srv/big", "cursor", "nope!")), 422, errcode.InvalidInput)
}

func TestTextAndConflict(t *testing.T) {
	r := newFilesRig(t)
	text := bytes.Repeat([]byte("é"), platform.MaxTextFile/2)
	r.write("srv/max.txt", string(text))
	r.write("srv/over.txt", string(text)+"a")
	r.write("srv/latin1.txt", "caf\xe9")

	rec := r.get("read", q("path", "/srv/max.txt"))
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), text) || rec.Header().Get(wire.HeaderSHA256) != hashOf(text) {
		t.Fatalf("read of 1 MiB of UTF-8: %d, %d bytes, hash %q", rec.Code, rec.Body.Len(), rec.Header().Get(wire.HeaderSHA256))
	}
	wantProblem(t, "1 MiB + 1 byte", r.get("read", q("path", "/srv/over.txt")), 422, errcode.FilesNotText)
	wantProblem(t, "invalid UTF-8", r.get("read", q("path", "/srv/latin1.txt")), 422, errcode.FilesNotText)

	opened := rec.Header().Get(wire.HeaderSHA256)
	edited := []byte("edited\n")
	rec = r.raw("write", q("path", "/srv/max.txt", "sha256", opened), edited)
	var written wire.FileWritten
	_ = json.Unmarshal(rec.Body.Bytes(), &written)
	if rec.Code != http.StatusOK || written.SHA256 != hashOf(edited) {
		t.Fatalf("write with the opened hash: %d %s", rec.Code, rec.Body.String())
	}
	wantProblem(t, "write with a stale hash", r.raw("write", q("path", "/srv/max.txt", "sha256", opened), []byte("lost")), 409, errcode.FilesConflict)
	if b, _ := os.ReadFile(r.path("srv/max.txt")); !bytes.Equal(b, edited) {
		t.Errorf("after the conflict the file holds %q", b)
	}
	wantProblem(t, "a new file over an existing one", r.raw("write", q("path", "/srv/max.txt"), []byte("x")), 409, errcode.FilesConflict)
	wantProblem(t, "a write over 1 MiB", r.raw("write", q("path", "/srv/new.txt"), make([]byte, platform.MaxTextFile+1)), 413, errcode.TooLarge)
	if rec := r.raw("write", q("path", "/srv/new.txt"), []byte("hello")); rec.Code != http.StatusOK {
		t.Errorf("a new file: %d %s", rec.Code, rec.Body.String())
	}
}

// failReader fails the test that reads it.
type failReader struct{ t *testing.T }

func (f failReader) Read([]byte) (int, error) {
	f.t.Error("the body was read")
	return 0, io.EOF
}

func TestUploadLimits(t *testing.T) {
	r := newFilesRig(t)
	req := r.request(http.MethodPost, "upload", q("path", "/srv/a"), failReader{t}, "application/octet-stream")
	req.ContentLength = -1
	rec := r.serve(req)
	wantProblem(t, "no Content-Length", rec, 422, errcode.InvalidInput)
	if !strings.Contains(rec.Body.String(), "Content-Length") {
		t.Errorf("the refusal %s does not name the missing Content-Length", rec.Body.String())
	}

	req = r.request(http.MethodPost, "upload", q("path", "/srv/a"), failReader{t}, "application/octet-stream")
	req.ContentLength = platform.MaxUpload + 1
	wantProblem(t, "above 512 MiB", r.serve(req), 413, errcode.TooLarge)
	if e := r.fileJournal("console.file.upload"); len(e) != 0 {
		t.Errorf("uploads refused for their Content-Length were journaled: %+v", e)
	}

	r.free.Store(100 << 20)
	req = r.request(http.MethodPost, "upload", q("path", "/srv/a"), failReader{t}, "application/octet-stream")
	req.ContentLength = 40 << 20
	wantProblem(t, "40 MiB with 100 MiB free", r.serve(req), 422, errcode.InvalidInput)
	entries, err := os.ReadDir(r.path("srv"))
	if err != nil || len(entries) != 0 {
		t.Errorf("/srv after the refusals: %v, %v", entries, err)
	}

	data := bytes.Repeat([]byte{0, 1, 2, 3}, 256<<10)
	rec = r.raw("upload", q("path", "/srv/a"), data)
	var written wire.FileWritten
	_ = json.Unmarshal(rec.Body.Bytes(), &written)
	if rec.Code != http.StatusOK || written.SHA256 != hashOf(data) || written.Size != int64(len(data)) {
		t.Fatalf("1 MiB with 100 MiB free: %d %s", rec.Code, rec.Body.String())
	}
	if b, _ := os.ReadFile(r.path("srv/a")); !bytes.Equal(b, data) {
		t.Error("the uploaded file differs")
	}
	rec = r.get("download", q("path", "/srv/a"))
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), data) {
		t.Errorf("download: %d, %d bytes", rec.Code, rec.Body.Len())
	}
}

func TestDeleteConfirm(t *testing.T) {
	r := newFilesRig(t)
	if err := os.MkdirAll(r.path("srv/tree/a/b"), 0o750); err != nil {
		t.Fatal(err)
	}
	r.write("srv/tree/a/b/f", "f")
	r.write("srv/tree/g", "g")
	r.write("srv/single", "s")
	wantProblem(t, "no confirm", r.change("delete", wire.FileChange{Path: "/srv/tree"}), 422, errcode.InvalidInput)
	wantProblem(t, "another path as confirm", r.change("delete", wire.FileChange{Path: "/srv/tree", Confirm: "/srv/tree/a"}), 422, errcode.InvalidInput)
	if _, err := os.Stat(r.path("srv/tree/a/b/f")); err != nil {
		t.Fatalf("the tree after the refusals: %v", err)
	}
	if rec := r.change("delete", wire.FileChange{Path: "/srv/tree", Confirm: "/srv/tree"}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete with confirm: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(r.path("srv/tree")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the tree after its delete: %v", err)
	}
	if rec := r.change("delete", wire.FileChange{Path: "/srv/single"}); rec.Code != http.StatusNoContent {
		t.Errorf("delete a file: %d %s", rec.Code, rec.Body.String())
	}
}

type journalRow struct {
	Seq       int64
	Actor     string
	Phase     string
	Result    string
	ErrorCode string
	Source    string
	Input     fileRecord
}

// fileJournal returns the journal entries of action in order.
func (r *filesRig) fileJournal(action string) []journalRow {
	r.t.Helper()
	rows, err := r.db.Read().QueryContext(context.Background(),
		"SELECT seq, actor, phase, result, COALESCE(error_code, ''), COALESCE(source, ''), input_json FROM core_journal WHERE action = ? ORDER BY seq", action)
	if err != nil {
		r.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []journalRow
	for rows.Next() {
		var j journalRow
		var input string
		if err := rows.Scan(&j.Seq, &j.Actor, &j.Phase, &j.Result, &j.ErrorCode, &j.Source, &input); err != nil {
			r.t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(input), &j.Input); err != nil {
			r.t.Fatal(err)
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		r.t.Fatal(err)
	}
	return out
}

func (r *filesRig) opRows() int {
	r.t.Helper()
	var n int
	if err := r.db.Read().QueryRow("SELECT count(*) FROM console_file_ops").Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

func TestJournalIntentResult(t *testing.T) {
	r := newFilesRig(t)
	data := []byte("uploaded bytes")
	var intentsAtWrite []journalRow
	r.spy.beforeWrite = func() { intentsAtWrite = r.fileJournal("console.file.upload") }
	if rec := r.raw("upload", q("path", "/srv/up"), data); rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	r.spy.beforeWrite = nil
	if len(intentsAtWrite) != 1 || intentsAtWrite[0].Phase != PhaseIntent {
		t.Fatalf("before the first byte the journal held %+v, want the intent alone", intentsAtWrite)
	}
	entries := r.fileJournal("console.file.upload")
	if len(entries) != 2 {
		t.Fatalf("upload entries %+v, want an intent and a result", entries)
	}
	intent, result := entries[0], entries[1]
	size := int64(len(data))
	if intent.Actor != adminP.Actor || intent.Source != adminP.Source || intent.Input.Path != "/srv/up" ||
		intent.Input.Size == nil || *intent.Input.Size != size || intent.Input.SHA256 != "" {
		t.Errorf("intent %+v", intent)
	}
	if result.Phase != PhaseResult || result.Result != "ok" || result.Input.SHA256 != hashOf(data) ||
		result.Input.Size == nil || *result.Input.Size != size || result.Input.Source != adminP.Source {
		t.Errorf("result %+v", result)
	}

	r.write("srv/conflict", "v1")
	wantProblem(t, "stale write", r.raw("write", q("path", "/srv/conflict", "sha256", hashOf([]byte("v0"))), []byte("v2")), 409, errcode.FilesConflict)
	entries = r.fileJournal("console.file.write")
	if len(entries) != 2 || entries[1].Result != "error" || entries[1].ErrorCode != string(errcode.FilesConflict) ||
		entries[1].Input.Error == "" || entries[1].Input.SHA256 != "" {
		t.Errorf("a refused write's entries %+v, want an intent and an error result", entries)
	}

	// A flag change sweeps while a write runs: the running write is no
	// crash's.
	r.write("srv/swept", "v1")
	r.spy.beforeWrite = func() {
		if err := r.rt.SetModuleEnabled(context.Background(), ID, true); err != nil {
			t.Error(err)
		}
	}
	if rec := r.raw("write", q("path", "/srv/swept", "sha256", hashOf([]byte("v1"))), []byte("v2")); rec.Code != http.StatusOK {
		t.Fatalf("a write during a sweep: %d %s", rec.Code, rec.Body.String())
	}
	r.spy.beforeWrite = nil
	for _, e := range r.fileJournal("console.file.write") {
		if e.Input.Error == ResultInterrupted {
			t.Errorf("the sweep took the running write for an interrupted one: %+v", e)
		}
	}

	if err := os.Mkdir(r.path("srv/d"), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		op   string
		body wire.FileChange
	}{
		{"mkdir", wire.FileChange{Path: "/srv/new"}},
		{"chmod", wire.FileChange{Path: "/srv/up", Mode: "0600"}},
		{"chown", wire.FileChange{Path: "/srv/up", UID: new(os.Getuid()), GID: new(os.Getgid())}},
		{"rename", wire.FileChange{Path: "/srv/up", To: "/srv/d/up"}},
		{"delete", wire.FileChange{Path: "/srv/d", Confirm: "/srv/d"}},
	} {
		if rec := r.change(c.op, c.body); rec.Code != http.StatusNoContent {
			t.Fatalf("%s: %d %s", c.op, rec.Code, rec.Body.String())
		}
		e := r.fileJournal("console.file." + c.op)
		if len(e) != 2 || e[0].Phase != PhaseIntent || e[1].Phase != PhaseResult || e[1].Result != "ok" || e[0].Input.Path != c.body.Path {
			t.Errorf("%s entries %+v", c.op, e)
		}
	}
	if e := r.fileJournal("console.file.rename"); len(e) == 2 && e[0].Input.To != "/srv/d/up" {
		t.Errorf("the rename's intent names %q as its target", e[0].Input.To)
	}

	t.Run("a crash's intent is interrupted at start", func(t *testing.T) {
		r.rt.Close()
		stale := "/srv/" + platform.TempPrefix + "0123456789abcdef"
		r.write(stale[1:], "half an upload")
		err := r.db.Write(context.Background(), func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO console_file_ops (id, op, path, tmp_path, actor, source, size, started_at)
				VALUES ('crashed', 'upload', '/srv/big', ?, 'user:admin', '192.0.2.7', 100, '2026-11-26T11:00:00Z')`, stale)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		before := len(r.fileJournal("console.file.upload"))
		r.open()
		entries := r.fileJournal("console.file.upload")
		if len(entries) != before+1 {
			t.Fatalf("after the start: %d upload entries, want %d", len(entries), before+1)
		}
		e := entries[len(entries)-1]
		if e.Phase != PhaseResult || e.Result != "error" || e.Input.Error != ResultInterrupted || e.Input.Path != "/srv/big" ||
			e.Actor != "user:admin" || e.Source != "192.0.2.7" {
			t.Errorf("the recovered result %+v", e)
		}
		if _, err := os.Stat(r.path(stale)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the stale temporary file: %v", err)
		}
		if n := r.opRows(); n != 0 {
			t.Errorf("%d operation rows left after the start, want none", n)
		}
	})
}

// slowBody gives first, then blocks until release closes, then fails.
type slowBody struct {
	first   []byte
	release chan struct{}
	sent    bool
}

func (b *slowBody) Read(p []byte) (int, error) {
	if !b.sent {
		b.sent = true
		return copy(p, b.first), nil
	}
	<-b.release
	return 0, io.ErrUnexpectedEOF
}

func TestTransferNotGuarded(t *testing.T) {
	r := newFilesRig(t)
	srv := httptest.NewServer(r.h)
	t.Cleanup(srv.Close)
	release := make(chan struct{})
	releaseBodies := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseBodies)

	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	upload := func(name string) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := &slowBody{first: bytes.Repeat([]byte("a"), 64<<10), release: release}
			req, err := http.NewRequest(http.MethodPost, srv.URL+"/agent/v1/files/upload?"+q("path", "/srv/"+name).Encode(), body)
			if err != nil {
				t.Error(err)
				return
			}
			req.ContentLength = 1 << 20
			req.Header.Set("Content-Type", "application/octet-stream")
			req.Header.Set(wire.HeaderPrincipal, principalHeader(t, adminP))
			resp, err := srv.Client().Do(req)
			if err != nil {
				statuses <- 0
				return
			}
			_ = resp.Body.Close()
			statuses <- resp.StatusCode
		}()
	}
	waitTemps := func(n int) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			entries, _ := os.ReadDir(r.path("srv"))
			count := 0
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), platform.TempPrefix) {
					count++
				}
			}
			if count == n {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%d temporary files, want %d", count, n)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	upload("one")
	upload("two")
	waitTemps(2)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req := r.request(http.MethodGet, "stat", q("path", "/srv"), nil, "").WithContext(ctx)
	if rec := r.serve(req); rec.Code != http.StatusOK {
		t.Errorf("a stat during two uploads: %d %s", rec.Code, rec.Body.String())
	}
	r.write("srv/f", "f")
	wantProblem(t, "a third transfer", r.get("download", q("path", "/srv/f")), 429, errcode.ConsoleLimit)

	closed := make(chan struct{})
	go func() {
		r.rt.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("Runtime.Close did not end the uploads")
	}
	// The clients still send their bodies; let them end.
	releaseBodies()
	wg.Wait()
	for range 2 {
		if s := <-statuses; s == http.StatusOK {
			t.Error("an upload the runtime's close ended answered 200")
		}
	}
	waitTemps(0)
	for _, name := range []string{"one", "two"} {
		if _, err := os.Stat(r.path("srv/" + name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("/srv/%s exists after its upload was ended: %v", name, err)
		}
	}
	results := 0
	for _, e := range r.fileJournal("console.file.upload") {
		if e.Phase == PhaseResult && e.Result == "error" {
			results++
		}
	}
	if results != 2 {
		t.Errorf("%d error results journaled, want one per upload", results)
	}

	t.Run("turning the module off ends a transfer", func(t *testing.T) {
		r.open()
		srv := httptest.NewServer(r.h)
		t.Cleanup(srv.Close)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		pr, pw := io.Pipe()
		t.Cleanup(func() { _ = pw.Close() })
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/agent/v1/files/upload?"+q("path", "/srv/three").Encode(), pr)
		if err != nil {
			t.Fatal(err)
		}
		req.ContentLength = 1 << 20
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set(wire.HeaderPrincipal, principalHeader(t, adminP))
		done := make(chan error, 1)
		go func() {
			// A refused answer or a dropped request both end the upload.
			resp, err := srv.Client().Do(req)
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					done <- errors.New("the ended upload answered 200")
					return
				}
			}
			done <- nil
		}()
		go func() { _, _ = pw.Write(make([]byte, 64<<10)) }()
		waitTemps(1)
		if err := r.rt.SetModuleEnabled(ctx, ID, false); err != nil {
			t.Fatal(err)
		}
		waitTemps(0)
		_ = pw.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-ctx.Done():
			t.Fatal("the upload's request did not end")
		}
		if e := r.fileJournal("console.file.upload"); len(e) == 0 || e[len(e)-1].Input.Path != "/srv/three" || e[len(e)-1].Result != "error" {
			t.Errorf("the ended upload's last entry: %+v", e[len(e)-1:])
		}
	})
}
