package backup

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestV1r22BackupRefusesACopySwappedForALink replaces the temporary copy with a
// link to a read-only file just after it is made. The backup must refuse and
// leave the file the link names as it was; a chmod by path would make it 0600.
func TestV1r22BackupRefusesACopySwappedForALink(t *testing.T) {
	e := bsNewEnv(t)
	decoy := filepath.Join(t.TempDir(), "decoy.db")
	if err := os.WriteFile(decoy, []byte("placeholder, not the database"), 0o400); err != nil {
		t.Fatal(err)
	}
	e.b.afterCopy = func(path string) {
		if err := os.Remove(path); err != nil {
			t.Errorf("remove the copy: %v", err)
			return
		}
		if err := os.Symlink(decoy, path); err != nil {
			t.Errorf("replace the copy with a link: %v", err)
		}
	}

	rec := e.post(bsBody(t, "placeholder-secret", &TestParams))

	st, err := os.Stat(decoy)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o400 {
		t.Errorf("the file the link names has mode %v, want 0400: the mode was set by path, through the link", st.Mode().Perm())
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500: a copy that is a link must be refused, not streamed (body %.120s)", rec.Code, rec.Body.String())
	}
	if left := e.copies(t); len(left) != 0 {
		t.Errorf("after the refusal, %v is left beside the database", left)
	}
}

// TestV1r22BackupRefusesACopySwappedForAFileThatIsNotRegular replaces the
// temporary copy with a FIFO or a directory, which the open does not refuse.
// The backup must refuse to stream it and remove it.
func TestV1r22BackupRefusesACopySwappedForAFileThatIsNotRegular(t *testing.T) {
	for _, tt := range []struct {
		name string
		make func(path string) error
	}{
		{"a FIFO", func(path string) error { return syscall.Mkfifo(path, 0o600) }},
		{"a directory", func(path string) error { return os.Mkdir(path, 0o700) }},
		{"a hard link", func(path string) error {
			other := path + ".held"
			if err := os.WriteFile(other, []byte("held"), 0o600); err != nil {
				return err
			}
			return os.Link(other, path)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := bsNewEnv(t)
			e.b.afterCopy = func(path string) {
				if err := os.Remove(path); err != nil {
					t.Errorf("remove the copy: %v", err)
					return
				}
				if err := tt.make(path); err != nil {
					t.Errorf("replace the copy with %s: %v", tt.name, err)
				}
			}

			rec := e.post(bsBody(t, "placeholder-secret", &TestParams))

			if rec.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500: a copy that is %s must be refused, not streamed (body %.120s)", rec.Code, tt.name, rec.Body.String())
			}
			if left := e.copies(t); len(left) != 0 {
				t.Errorf("after the refusal, %v is left beside the database", left)
			}
		})
	}
}

// TestV1r22BackupCopyIsPrivateWhenTheStreamStarts checks that the copy is a
// regular file of mode 0600, whatever the process umask made it, before the
// first byte is streamed. It reads the mode by path, so it cannot tell a chmod
// through the descriptor from one by path; the link test above does.
func TestV1r22BackupCopyIsPrivateWhenTheStreamStarts(t *testing.T) {
	e := bsNewEnv(t)
	var (
		seen    os.FileMode
		lookErr error
	)
	spy := &w10bcSpy{look: func() {
		names, err := filepath.Glob(filepath.Join(e.dir, "backup-*", "backup.db"))
		if err != nil {
			lookErr = fmt.Errorf("look for the copy: %w", err)
			return
		}
		if len(names) != 1 {
			lookErr = fmt.Errorf("found %v", names)
			return
		}
		st, err := os.Lstat(names[0])
		if err != nil {
			lookErr = err
			return
		}
		seen = st.Mode()
	}}

	rec := httptest.NewRecorder()
	e.b.streamServe(w10bcDeps(e), spy).ServeHTTP(rec, bsRequest(bsBody(t, "placeholder-secret", &TestParams)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %.300s)", rec.Code, rec.Body.String())
	}
	if lookErr != nil {
		t.Fatal(lookErr)
	}
	if !seen.IsRegular() || seen.Perm() != 0o600 {
		t.Errorf("when the stream started, the copy had mode %v, want a regular file of 0600", seen)
	}
}
