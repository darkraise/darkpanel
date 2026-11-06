package backup

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/quarantine"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// TestW10BcEnrollTokenReadsTheFlagInItsTransaction releases the quarantine in
// a transaction that commits only after the enroll-token request has passed
// its first check and is waiting for the database writer. The token must not
// be issued, because the quarantine is over when the request's transaction
// runs.
func TestW10BcEnrollTokenReadsTheFlagInItsTransaction(t *testing.T) {
	e := qlNew(t)
	qlActivate(t, e.db)
	ctx := context.Background()
	hostsBefore := e.hostStates(t)

	entered, release := make(chan struct{}), make(chan struct{})
	released := make(chan error, 1)
	go func() {
		released <- e.db.Write(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `DELETE FROM core_meta WHERE key = ?`, quarantine.MetaKey); err != nil {
				return err
			}
			close(entered)
			<-release
			return nil
		})
	}()
	select {
	case <-entered:
	case err := <-released:
		t.Fatalf("the releasing transaction ended before it held the writer: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the releasing transaction never began")
	}

	// A request that waits for the writer is counted by the pool, so the
	// test knows the request is past every check made outside a transaction.
	waiting := e.db.Writer().Stats().WaitCount
	answered := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		answered <- qlCall(t, e.b.enrollTokenHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/enroll-token", `{"hostId":"host-enr"}`)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for e.db.Writer().Stats().WaitCount <= waiting {
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("the enroll-token request never waited for the database writer")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	if err := <-released; err != nil {
		t.Fatalf("release the quarantine: %v", err)
	}

	var rec *httptest.ResponseRecorder
	select {
	case rec = <-answered:
	case <-time.After(10 * time.Second):
		t.Fatal("the enroll-token request never answered")
	}
	qlWantProblem(t, rec, http.StatusUnprocessableEntity, errcode.InvalidInput,
		"the controller is not in restore quarantine; use darkpanel host token")
	if n := len(e.issuer.issued()); n != 0 {
		t.Errorf("the issuer was called %d times after the quarantine ended", n)
	}
	if got := e.hostStates(t); got != hostsBefore {
		t.Errorf("host states changed to %s, were %s", got, hostsBefore)
	}
	if rows := e.auditRows(t); len(rows) != 0 {
		t.Errorf("audit rows = %+v, want none for a refusal", rows)
	}
}
