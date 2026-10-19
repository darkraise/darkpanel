package fleet

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/transport"
)

const testAddress = "198.18.0.10:7443"

// issueToken issues hostID a token in a write transaction of its own.
func issueToken(t *testing.T, a *Authority, db *store.DB, hostID string) (string, time.Time) {
	t.Helper()
	var token string
	var expires time.Time
	err := db.Write(context.Background(), func(tx *sql.Tx) error {
		var err error
		token, expires, err = a.IssueToken(context.Background(), tx, hostID, testAddress)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return token, expires
}

func hostState(t *testing.T, db *store.DB, hostID string) string {
	t.Helper()
	var state string
	if err := db.Read().QueryRow(`SELECT state FROM core_hosts WHERE id = ?`, hostID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestIssueTokenAndEnroll(t *testing.T) {
	a, db, clock := newAuthority(t)
	ctx := context.Background()
	addHost(t, db, "H1", "pending")
	token, expires := issueToken(t, a, db, "H1")
	if !expires.Equal(testStart.Add(TokenTTL)) {
		t.Fatalf("expiresAt = %v, want %v", expires, testStart.Add(TokenTTL))
	}
	parsed, err := transport.ParseToken(token)
	if err != nil {
		t.Fatalf("the issued token does not parse: %v", err)
	}
	if parsed.Address != testAddress || parsed.Fingerprint != a.Fingerprint() || parsed.ControllerSAN != a.ControllerSAN() || parsed.HostID != "H1" {
		t.Fatalf("token = %+v", parsed)
	}
	if secret, err := base64.RawURLEncoding.DecodeString(parsed.Secret); err != nil || len(secret) != 16 {
		t.Fatalf("secret = %q, %v; want 16 bytes", parsed.Secret, err)
	}
	want, err := parsed.SecretHash()
	if err != nil {
		t.Fatal(err)
	}
	var hash []byte
	var expiresAt, createdAt string
	var consumed *string
	if err := db.Read().QueryRow(`SELECT hash, expires_at, consumed_at, created_at FROM core_enroll_tokens WHERE host_id = 'H1'`).
		Scan(&hash, &expiresAt, &consumed, &createdAt); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(hash, want) || expiresAt != "2026-10-19T13:00:00.000000000Z" || consumed != nil || createdAt != "2026-10-19T12:00:00.000000000Z" {
		t.Fatalf("token row: hash ok %v, expires %s, consumed %v, created %s", bytes.Equal(hash, want), expiresAt, consumed, createdAt)
	}
	if bytes.Contains([]byte(token), hash) {
		t.Fatal("the row stores the token text")
	}

	clock.Advance(TokenTTL - time.Second)
	csr, key := newCSR(t)
	got, err := a.Enroll(ctx, token, csr)
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if got.HostID != "H1" || !bytes.Equal(got.CA, a.RootPEM()) {
		t.Fatalf("enrollment host %q, CA matches %v", got.HostID, bytes.Equal(got.CA, a.RootPEM()))
	}
	cert := parseCert(t, got.Certificate)
	if id, err := transport.HostIDOf(cert); err != nil || id != "H1" {
		t.Fatalf("HostIDOf = %q, %v", id, err)
	}
	if !cert.PublicKey.(ed25519.PublicKey).Equal(key.Public()) {
		t.Fatal("the certificate does not carry the CSR's key")
	}
	if err := a.CheckPeer(ctx, "H1", transport.SerialText(cert.SerialNumber)); err != nil {
		t.Fatalf("the enrolled certificate is refused: %v", err)
	}
	var enrolledAt string
	if err := db.Read().QueryRow(`SELECT enrolled_at FROM core_hosts WHERE id = 'H1'`).Scan(&enrolledAt); err != nil ||
		enrolledAt != "2026-10-19T12:59:59.000000000Z" || hostState(t, db, "H1") != "enrolled" {
		t.Fatalf("host after enrollment: state %s, enrolled_at %q, %v", hostState(t, db, "H1"), enrolledAt, err)
	}
	if err := db.Read().QueryRow(`SELECT consumed_at FROM core_enroll_tokens WHERE host_id = 'H1'`).Scan(&consumed); err != nil || consumed == nil {
		t.Fatalf("token not marked consumed: %v, %v", consumed, err)
	}
}

func TestIssueTokenReplacesTheEarlierToken(t *testing.T) {
	a, db, _ := newAuthority(t)
	ctx := context.Background()
	addHost(t, db, "H1", "pending")
	first, _ := issueToken(t, a, db, "H1")
	second, _ := issueToken(t, a, db, "H1")
	csr, _ := newCSR(t)
	_, err := a.Enroll(ctx, first, csr)
	wantCode(t, err, errcode.EnrollTokenInvalid, "replaced token")
	if _, err := a.Enroll(ctx, second, csr); err != nil {
		t.Fatalf("replacement token: %v", err)
	}
	var rows int
	if err := db.Read().QueryRow(`SELECT count(*) FROM core_enroll_tokens`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("token rows = %d, %v; want 1", rows, err)
	}
}

func TestIssueTokenRefusals(t *testing.T) {
	a, db, _ := newAuthority(t)
	addHost(t, db, "H1", "pending")
	err := db.Write(context.Background(), func(tx *sql.Tx) error {
		_, _, err := a.IssueToken(context.Background(), tx, "H9", testAddress)
		return err
	})
	wantCode(t, err, errcode.NotFound, "token for an unknown host")
	err = db.Write(context.Background(), func(tx *sql.Tx) error {
		_, _, err := a.IssueToken(context.Background(), tx, "H1", "")
		return err
	})
	wantCode(t, err, errcode.InvalidInput, "token without an address")
}

func TestEnrollRefusals(t *testing.T) {
	type setup struct {
		a     *Authority
		db    *store.DB
		clock *coretest.FakeClock
		token string
	}
	tamper := func(t *testing.T, token string, edit func(*transport.Token)) string {
		t.Helper()
		parsed, err := transport.ParseToken(token)
		if err != nil {
			t.Fatal(err)
		}
		edit(&parsed)
		return parsed.String()
	}
	tests := []struct {
		name string
		// prepare changes the state and returns the token and CSR to present.
		prepare func(t *testing.T, s setup) (string, []byte)
	}{
		{"reused token", func(t *testing.T, s setup) (string, []byte) {
			csr, _ := newCSR(t)
			if _, err := s.a.Enroll(context.Background(), s.token, csr); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Writer().Exec(`UPDATE core_hosts SET state = 'revoked' WHERE id = 'H1'`); err != nil {
				t.Fatal(err)
			}
			return s.token, csr
		}},
		{"expired token", func(t *testing.T, s setup) (string, []byte) {
			s.clock.Advance(TokenTTL)
			csr, _ := newCSR(t)
			return s.token, csr
		}},
		{"wrong secret", func(t *testing.T, s setup) (string, []byte) {
			csr, _ := newCSR(t)
			return tamper(t, s.token, func(tk *transport.Token) {
				tk.Secret = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 16))
			}), csr
		}},
		{"token moved to another host", func(t *testing.T, s setup) (string, []byte) {
			addHost(t, s.db, "H2", "pending")
			issueToken(t, s.a, s.db, "H2")
			csr, _ := newCSR(t)
			return tamper(t, s.token, func(tk *transport.Token) { tk.HostID = "H2" }), csr
		}},
		{"host without a token", func(t *testing.T, s setup) (string, []byte) {
			addHost(t, s.db, "H3", "pending")
			csr, _ := newCSR(t)
			return tamper(t, s.token, func(tk *transport.Token) { tk.HostID = "H3" }), csr
		}},
		{"another controller", func(t *testing.T, s setup) (string, []byte) {
			csr, _ := newCSR(t)
			return tamper(t, s.token, func(tk *transport.Token) { tk.ControllerSAN = transport.ControllerURI("01OTHER") }), csr
		}},
		{"another root", func(t *testing.T, s setup) (string, []byte) {
			csr, _ := newCSR(t)
			return tamper(t, s.token, func(tk *transport.Token) { tk.Fingerprint = string(bytes.Repeat([]byte{'a'}, 64)) }), csr
		}},
		{"malformed token", func(t *testing.T, _ setup) (string, []byte) {
			csr, _ := newCSR(t)
			return "dpe1.not-a-token", csr
		}},
		{"host already enrolled", func(t *testing.T, s setup) (string, []byte) {
			if _, err := s.db.Writer().Exec(`UPDATE core_hosts SET state = 'enrolled' WHERE id = 'H1'`); err != nil {
				t.Fatal(err)
			}
			csr, _ := newCSR(t)
			return s.token, csr
		}},
		{"bad CSR", func(_ *testing.T, s setup) (string, []byte) {
			return s.token, []byte("not a request")
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, db, clock := newAuthority(t)
			addHost(t, db, "H1", "pending")
			token, _ := issueToken(t, a, db, "H1")
			s := setup{a: a, db: db, clock: clock, token: token}
			presented, csr := tc.prepare(t, s)
			certsBefore := countCerts(t, db)
			stateBefore := hostState(t, db, "H1")
			_, err := a.Enroll(context.Background(), presented, csr)
			wantCode(t, err, errcode.EnrollTokenInvalid, tc.name)
			if n := countCerts(t, db); n != certsBefore {
				t.Fatalf("certificate rows %d -> %d after a refused enrollment", certsBefore, n)
			}
			if state := hostState(t, db, "H1"); state != stateBefore {
				t.Fatalf("host state %s -> %s after a refused enrollment", stateBefore, state)
			}
		})
	}
}

func TestEnrollBadCSRKeepsToken(t *testing.T) {
	a, db, _ := newAuthority(t)
	ctx := context.Background()
	addHost(t, db, "H1", "pending")
	token, _ := issueToken(t, a, db, "H1")
	_, err := a.Enroll(ctx, token, []byte("not a request"))
	wantCode(t, err, errcode.EnrollTokenInvalid, "bad CSR")
	var consumed sql.NullString
	if err := db.Read().QueryRow(`SELECT consumed_at FROM core_enroll_tokens WHERE host_id = 'H1'`).Scan(&consumed); err != nil || consumed.Valid {
		t.Fatalf("token consumed by a bad CSR: %v, %v", consumed, err)
	}
	csr, _ := newCSR(t)
	if _, err := a.Enroll(ctx, token, csr); err != nil {
		t.Fatalf("the token failed after a bad CSR: %v", err)
	}
	if state := hostState(t, db, "H1"); state != "enrolled" {
		t.Fatalf("state %s, want enrolled", state)
	}
}

func TestRevokedHostReenrolls(t *testing.T) {
	a, db, clock := newAuthority(t)
	ctx := context.Background()
	addHost(t, db, "H1", "pending")
	token, _ := issueToken(t, a, db, "H1")
	csr, _ := newCSR(t)
	first, err := a.Enroll(ctx, token, csr)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	if err := db.Write(ctx, func(tx *sql.Tx) error { return RevokeHost(ctx, tx, "H1", clock.Now()) }); err != nil {
		t.Fatal(err)
	}
	token, _ = issueToken(t, a, db, "H1")
	csr, _ = newCSR(t)
	second, err := a.Enroll(ctx, token, csr)
	if err != nil {
		t.Fatalf("re-enrollment of a revoked host: %v", err)
	}
	oldSerial := transport.SerialText(parseCert(t, first.Certificate).SerialNumber)
	wantCode(t, a.CheckPeer(ctx, "H1", oldSerial), errcode.Forbidden, "certificate from before the revocation")
	if err := a.CheckPeer(ctx, "H1", transport.SerialText(parseCert(t, second.Certificate).SerialNumber)); err != nil {
		t.Fatalf("re-enrolled certificate refused: %v", err)
	}
}

// orders are the ways an enrollment and a competing write can meet: one
// after the other in either order, and at once. Run at once, the competing
// write usually wins, because Enroll checks the CSR before it takes the
// writer, so the two sequential orders are what make both outcomes certain.
var orders = []struct {
	name   string
	rounds int
	run    func(enroll, other func())
}{
	{"enrollment first", 1, func(enroll, other func()) { enroll(); other() }},
	{"other write first", 1, func(enroll, other func()) { other(); enroll() }},
	{"at once", 20, race},
}

// race runs a and b at once, as near the same instant as goroutines allow.
func race(a, b func()) {
	var ready, done sync.WaitGroup
	gate := make(chan struct{})
	for _, f := range []func(){a, b} {
		ready.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			ready.Done()
			<-gate
			f()
		}()
	}
	ready.Wait()
	close(gate)
	done.Wait()
}

func TestTokenReplacementAgainstEnrollment(t *testing.T) {
	ctx := context.Background()
	for _, order := range orders {
		t.Run(order.name, func(t *testing.T) {
			for i := range order.rounds {
				a, db, _ := newAuthority(t)
				addHost(t, db, "H1", "pending")
				first, _ := issueToken(t, a, db, "H1")
				csr, _ := newCSR(t)
				var enrollErr, issueErr error
				var second string
				order.run(func() { _, enrollErr = a.Enroll(ctx, first, csr) }, func() {
					issueErr = db.Write(ctx, func(tx *sql.Tx) error {
						var err error
						second, _, err = a.IssueToken(ctx, tx, "H1", testAddress)
						return err
					})
				})
				if issueErr != nil {
					t.Fatalf("round %d: IssueToken: %v", i, issueErr)
				}
				_, secondErr := a.Enroll(ctx, second, csr)
				if enrollErr == nil {
					// The first token was consumed before it was replaced; the
					// host is enrolled, so its replacement cannot enroll it again.
					wantCode(t, secondErr, errcode.EnrollTokenInvalid, "replacement after enrollment")
				} else {
					wantCode(t, enrollErr, errcode.EnrollTokenInvalid, "replaced token")
					if secondErr != nil {
						t.Fatalf("round %d: the replacement token failed after the replaced one was refused: %v", i, secondErr)
					}
				}
				if n := countCerts(t, db); n != 1 || hostState(t, db, "H1") != "enrolled" {
					t.Fatalf("round %d: %d certificates, state %s; want 1, enrolled", i, n, hostState(t, db, "H1"))
				}
			}
		})
	}
}

func TestRevocationAgainstEnrollment(t *testing.T) {
	ctx := context.Background()
	for _, order := range orders {
		t.Run(order.name, func(t *testing.T) {
			for i := range order.rounds {
				a, db, clock := newAuthority(t)
				addHost(t, db, "H1", "pending")
				token, _ := issueToken(t, a, db, "H1")
				csr, _ := newCSR(t)
				var enrolled Enrollment
				var enrollErr, revokeErr error
				order.run(func() { enrolled, enrollErr = a.Enroll(ctx, token, csr) }, func() {
					revokeErr = db.Write(ctx, func(tx *sql.Tx) error { return RevokeHost(ctx, tx, "H1", clock.Now()) })
				})
				if revokeErr != nil {
					t.Fatalf("round %d: RevokeHost: %v", i, revokeErr)
				}
				if enrollErr == nil {
					serial := transport.SerialText(parseCert(t, enrolled.Certificate).SerialNumber)
					wantCode(t, a.CheckPeer(ctx, "H1", serial), errcode.Forbidden, "certificate enrolled before the revocation")
				} else {
					wantCode(t, enrollErr, errcode.EnrollTokenInvalid, "enrollment after the revocation")
				}
				if state := hostState(t, db, "H1"); state != "revoked" {
					t.Fatalf("round %d: state %s, want revoked", i, state)
				}
				_, err := a.Enroll(ctx, token, csr)
				wantCode(t, err, errcode.EnrollTokenInvalid, "token after the revocation")
			}
		})
	}
}
