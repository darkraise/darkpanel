package backup

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/iotest"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core/ext"
)

// w10bcSpy is the random source of a backup stream. The stream asks for its
// salt after the temporary copy of the database exists and before the first
// response byte, so look runs while the copy is on disk. It runs once.
type w10bcSpy struct {
	once sync.Once
	look func()
}

func (s *w10bcSpy) Read(p []byte) (int, error) {
	s.once.Do(s.look)
	return rand.Read(p)
}

type w10bcSeen struct {
	name string
	dir  bool
	perm os.FileMode
}

func w10bcDeps(e *bsEnv) ext.Deps {
	return ext.Deps{
		DB: e.db, Sealer: e.sealer, Clock: e.clock, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Audit: e.audit, LocalHostID: "host-local",
	}
}

func w10bcLeftovers(t *testing.T, dir string) []string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(dir, "backup-*"))
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func TestW10BcBackupCopyLivesInAPrivateDirectory(t *testing.T) {
	e := bsNewEnv(t)
	var (
		seen    []w10bcSeen
		lookErr error
	)
	spy := &w10bcSpy{look: func() {
		names, err := filepath.Glob(filepath.Join(e.dir, "backup-*"))
		if err != nil {
			lookErr = err
			return
		}
		for _, name := range names {
			st, err := os.Lstat(name)
			if err != nil {
				lookErr = err
				return
			}
			seen = append(seen, w10bcSeen{name: filepath.Base(name), dir: st.IsDir(), perm: st.Mode().Perm()})
		}
	}}

	rec := httptest.NewRecorder()
	e.b.streamServe(w10bcDeps(e), spy).ServeHTTP(rec, bsRequest(bsBody(t, "p", &TestParams)))

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 (body %.300s)", rec.Code, rec.Body.String())
	}
	if lookErr != nil {
		t.Fatal(lookErr)
	}
	if len(seen) != 1 || !seen[0].dir || seen[0].perm != 0o700 {
		t.Errorf("while the stream ran, the database directory held %+v; want one directory of mode 0700 that holds the plaintext copy, so that no other user can read it before the copy is chmodded", seen)
	}
	if left := w10bcLeftovers(t, e.dir); len(left) != 0 {
		t.Errorf("after the stream, %v is left beside the database", left)
	}
}

func TestW10BcContributeSweepsTheCopiesACrashLeft(t *testing.T) {
	e := bsNewEnv(t)
	crashed := filepath.Join(e.dir, "backup-"+ulid.Make().String()+".tmp")
	if err := os.Mkdir(crashed, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(e.dir, "backup-"+ulid.Make().String()+".db")
	notACopy := []string{
		filepath.Join(e.dir, "backup-notes.txt"),
		filepath.Join(e.dir, "backup-"+ulid.Make().String()+".db.keep"),
		filepath.Join(e.dir, "mybackup-"+ulid.Make().String()+".db"),
	}
	for _, f := range append([]string{filepath.Join(crashed, "backup.db"), legacy}, notACopy...) {
		if err := os.WriteFile(f, []byte("plaintext database"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if _, _, err := e.b.Contribute(w10bcDeps(e)); err != nil {
		t.Fatalf("Contribute: %v", err)
	}

	for _, gone := range []string{crashed, legacy} {
		if _, err := os.Lstat(gone); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: error %v, want it swept at startup", filepath.Base(gone), err)
		}
	}
	for _, kept := range append(notACopy, e.db.Path()) {
		if _, err := os.Lstat(kept); err != nil {
			t.Errorf("%s: %v, want it kept: it is not a copy of a crashed backup", filepath.Base(kept), err)
		}
	}
}

func TestW10BcKeyReadRefusesAFileThatGrew(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controller.key")
	key := bytes.Repeat([]byte("k"), MaxKeyMember)

	got, err := streamReadBounded(bytes.NewReader(key), path)
	if err != nil || !bytes.Equal(got, key) {
		t.Fatalf("streamReadBounded of exactly %d bytes = %d bytes, %v; want them all", MaxKeyMember, len(got), err)
	}

	got, err = streamReadBounded(io.MultiReader(bytes.NewReader(key), strings.NewReader("k")), path)
	if err == nil || got != nil {
		t.Fatalf("streamReadBounded of %d bytes = %d bytes, %v; want no key and an error: a key that grew past the limit must not be cut off", MaxKeyMember+1, len(got), err)
	}
	if msg := err.Error(); !strings.Contains(msg, path) || !strings.Contains(msg, "larger than") || strings.Contains(msg, "kkkk") {
		t.Errorf("error %q, want one naming the file and the limit and carrying no key bytes", msg)
	}

	got, err = streamReadBounded(iotest.ErrReader(errors.New("disk failure")), path)
	if err == nil || got != nil || !strings.Contains(err.Error(), "disk failure") {
		t.Errorf("streamReadBounded of a failing reader = %d bytes, %v; want no key and the read error", len(got), err)
	}
}
