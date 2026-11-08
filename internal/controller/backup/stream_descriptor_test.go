package backup

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// TestV1r22BackupCopyIsPrivateWhenTheStreamStarts guards the other half of the
// change: the descriptor the backup streams from belongs to a regular file of
// mode 0600, whatever the process umask made it.
func TestV1r22BackupCopyIsPrivateWhenTheStreamStarts(t *testing.T) {
	e := bsNewEnv(t)
	var (
		seen    os.FileMode
		lookErr error
	)
	spy := &w10bcSpy{look: func() {
		names, err := filepath.Glob(filepath.Join(e.dir, "backup-*", "backup.db"))
		if err != nil || len(names) != 1 {
			lookErr = fmt.Errorf("found %v, error %v", names, err) //nolint:errorlint // err is usually nil here, which %w would print as %!w(<nil>)
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
