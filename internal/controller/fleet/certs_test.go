package fleet

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"errors"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/transport"
)

// addHost inserts a core_hosts row in state.
func addHost(t *testing.T, db *store.DB, id, state string) {
	t.Helper()
	if _, err := db.Writer().ExecContext(context.Background(),
		`INSERT INTO core_hosts (id, name, created_at, state) VALUES (?, ?, '2026-10-19T00:00:00.000000000Z', ?)`,
		id, "host-"+id, state); err != nil {
		t.Fatal(err)
	}
}

// csrPEM returns tmpl signed by key as one PEM CERTIFICATE REQUEST block.
func csrPEM(t *testing.T, key crypto.Signer, tmpl *x509.CertificateRequest) []byte {
	t.Helper()
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}

// newCSR returns a CSR for a fresh Ed25519 key, as an agent makes it.
func newCSR(t *testing.T) ([]byte, ed25519.PrivateKey) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return csrPEM(t, key, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "agent"}}), key
}

// parseCert parses one PEM CERTIFICATE block.
func parseCert(t *testing.T, data []byte) *x509.Certificate {
	t.Helper()
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		t.Fatalf("not one CERTIFICATE block: %q", data)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

// issue issues hostID a certificate for a fresh key in a write transaction.
func issue(t *testing.T, a *Authority, db *store.DB, hostID string) *x509.Certificate {
	t.Helper()
	csr, _ := newCSR(t)
	var out []byte
	err := db.Write(context.Background(), func(tx *sql.Tx) error {
		var err error
		out, err = a.IssueAgentCert(context.Background(), tx, hostID, csr)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return parseCert(t, out)
}

func wantCode(t *testing.T, err error, code errcode.Code, what string) {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != code {
		t.Fatalf("%s: error = %v, want %s", what, err, code)
	}
}

func countCerts(t *testing.T, db *store.DB) int {
	t.Helper()
	var n int
	if err := db.Read().QueryRow(`SELECT count(*) FROM core_agent_certs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestIssueAgentCert(t *testing.T) {
	a, db, _ := newAuthority(t)
	addHost(t, db, "H1", "enrolled")
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := url.Parse(transport.HostURI("H2"))
	// Every identity field names something else; the certificate must name H1 alone.
	csr := csrPEM(t, key, &x509.CertificateRequest{
		Subject:        pkix.Name{CommonName: "H2", Organization: []string{"evil"}},
		DNSNames:       []string{"controller.example"},
		IPAddresses:    []net.IP{net.ParseIP("192.0.2.1")},
		EmailAddresses: []string{"root@example.com"},
		URIs:           []*url.URL{other},
	})
	var out []byte
	if err := db.Write(context.Background(), func(tx *sql.Tx) error {
		out, err = a.IssueAgentCert(context.Background(), tx, "H1", csr)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	cert := parseCert(t, out)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: a.Pool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("the certificate does not verify for clientAuth against the agent root: %v", err)
	}
	if id, err := transport.HostIDOf(cert); err != nil || id != "H1" {
		t.Fatalf("HostIDOf = %q, %v; want H1", id, err)
	}
	if len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth || len(cert.UnknownExtKeyUsage) != 0 {
		t.Fatalf("EKU = %v, want clientAuth only", cert.ExtKeyUsage)
	}
	if len(cert.DNSNames)+len(cert.IPAddresses)+len(cert.EmailAddresses) != 0 || len(cert.Subject.Organization) != 0 {
		t.Fatalf("CSR identity fields reached the certificate: %v %v %v %v", cert.DNSNames, cert.IPAddresses, cert.EmailAddresses, cert.Subject)
	}
	if !cert.PublicKey.(ed25519.PublicKey).Equal(key.Public()) {
		t.Fatal("the certificate does not carry the CSR's key")
	}
	if cert.IsCA || cert.SerialNumber.BitLen() > 128 || cert.SerialNumber.Sign() <= 0 {
		t.Fatalf("IsCA %v, serial %s", cert.IsCA, cert.SerialNumber)
	}
	if !cert.NotBefore.Equal(testStart.Add(-time.Hour)) || !cert.NotAfter.Equal(testStart.Add(CertValidity)) {
		t.Fatalf("validity %v to %v", cert.NotBefore, cert.NotAfter)
	}
	var host, notBefore, notAfter, createdAt string
	var revoked *string
	if err := db.Read().QueryRow(`SELECT host_id, not_before, not_after, revoked_at, created_at FROM core_agent_certs WHERE serial = ?`,
		transport.SerialText(cert.SerialNumber)).Scan(&host, &notBefore, &notAfter, &revoked, &createdAt); err != nil {
		t.Fatalf("no core_agent_certs row for the serial: %v", err)
	}
	if host != "H1" || revoked != nil || notBefore != "2026-10-19T11:00:00.000000000Z" ||
		notAfter != "2027-10-19T12:00:00.000000000Z" || createdAt != "2026-10-19T12:00:00.000000000Z" {
		t.Fatalf("row: host %s, not_before %s, not_after %s, revoked %v, created %s", host, notBefore, notAfter, revoked, createdAt)
	}
}

func TestIssueAgentCertRefusesBadCSRs(t *testing.T) {
	a, db, _ := newAuthority(t)
	addHost(t, db, "H1", "enrolled")
	good, _ := newCSR(t)
	block, _ := pem.Decode(good)
	tampered := bytes.Clone(block.Bytes)
	tampered[len(tampered)-1] ^= 0xff
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		csr  []byte
	}{
		{"not PEM", []byte("not a request")},
		{"empty", nil},
		{"wrong block type", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: block.Bytes})},
		{"two blocks", append(bytes.Clone(good), good...)},
		{"garbage DER", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: []byte{0x30, 0x03, 0x02, 0x01, 0x01}})},
		{"bad signature", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: tampered})},
		{"ECDSA key", csrPEM(t, ecKey, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "agent"}})},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := db.Write(context.Background(), func(tx *sql.Tx) error {
				_, err := a.IssueAgentCert(context.Background(), tx, "H1", tc.csr)
				return err
			})
			wantCode(t, err, errcode.InvalidInput, tc.name)
			if n := countCerts(t, db); n != 0 {
				t.Fatalf("certificate rows = %d after a refused CSR", n)
			}
		})
	}
}

func TestIssueAgentCertRefusesUnreadableHostIDs(t *testing.T) {
	a, db, _ := newAuthority(t)
	for _, id := range []string{strings.Repeat("a", 65), "a/b"} {
		t.Run(id, func(t *testing.T) {
			// The host row exists, so only the read-back can stop the insert.
			addHost(t, db, id, "enrolled")
			csr, _ := newCSR(t)
			err := db.Write(context.Background(), func(tx *sql.Tx) error {
				_, err := a.IssueAgentCert(context.Background(), tx, id, csr)
				return err
			})
			if err == nil {
				t.Fatalf("host id %q was certified", id)
			}
			if errors.As(err, new(*errcode.Error)) {
				t.Fatalf("error = %v, want a plain error", err)
			}
			if n := countCerts(t, db); n != 0 {
				t.Fatalf("certificate rows = %d after a refused host id", n)
			}
		})
	}
}

func TestCheckPeer(t *testing.T) {
	a, db, clock := newAuthority(t)
	ctx := context.Background()
	addHost(t, db, "H1", "enrolled")
	addHost(t, db, "H2", "enrolled")
	good := transport.SerialText(issue(t, a, db, "H1").SerialNumber)
	otherHost := transport.SerialText(issue(t, a, db, "H2").SerialNumber)
	revoked := transport.SerialText(issue(t, a, db, "H1").SerialNumber)
	if _, err := db.Writer().ExecContext(ctx, `UPDATE core_agent_certs SET revoked_at = ? WHERE serial = ?`, formatTime(clock.Now()), revoked); err != nil {
		t.Fatal(err)
	}
	if err := a.CheckPeer(ctx, "H1", good); err != nil {
		t.Fatalf("valid certificate refused: %v", err)
	}
	refused := []struct{ name, host, serial string }{
		{"unknown serial", "H1", "abc"},
		{"serial of another host", "H1", otherHost},
		{"revoked serial", "H1", revoked},
		{"unknown host", "H9", good},
	}
	for _, tc := range refused {
		wantCode(t, a.CheckPeer(ctx, tc.host, tc.serial), errcode.Forbidden, tc.name)
	}
	for _, state := range []string{"pending", "revoked"} {
		if _, err := db.Writer().ExecContext(ctx, `UPDATE core_hosts SET state = ? WHERE id = 'H1'`, state); err != nil {
			t.Fatal(err)
		}
		wantCode(t, a.CheckPeer(ctx, "H1", good), errcode.Forbidden, "host "+state)
	}
	if _, err := db.Writer().ExecContext(ctx, `UPDATE core_hosts SET state = 'enrolled' WHERE id = 'H1'`); err != nil {
		t.Fatal(err)
	}
	clock.Advance(CertValidity - time.Second)
	if err := a.CheckPeer(ctx, "H1", good); err != nil {
		t.Fatalf("certificate refused a second before it expires: %v", err)
	}
	clock.Advance(time.Second)
	wantCode(t, a.CheckPeer(ctx, "H1", good), errcode.Forbidden, "expired serial")
}

func TestRenewKeepsBothSerialsUntilTheHandshake(t *testing.T) {
	a, db, clock := newAuthority(t)
	ctx := context.Background()
	addHost(t, db, "H1", "enrolled")
	old := transport.SerialText(issue(t, a, db, "H1").SerialNumber)
	clock.Advance(CertRenewAfter)
	csr, key := newCSR(t)
	out, err := a.Renew(ctx, "H1", csr)
	if err != nil {
		t.Fatal(err)
	}
	renewed := parseCert(t, out)
	if !renewed.PublicKey.(ed25519.PublicKey).Equal(key.Public()) {
		t.Fatal("the renewed certificate does not carry the new key")
	}
	if id, err := transport.HostIDOf(renewed); err != nil || id != "H1" {
		t.Fatalf("renewed HostIDOf = %q, %v", id, err)
	}
	fresh := transport.SerialText(renewed.SerialNumber)
	for _, s := range []string{old, fresh} {
		if err := a.CheckPeer(ctx, "H1", s); err != nil {
			t.Fatalf("serial %s refused after renewal: %v", s, err)
		}
	}
	// An agent whose renewal was interrupted before it saved the new
	// certificate keeps connecting with the old one, which must stay valid.
	if err := a.Handshake(ctx, "H1", old); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{old, fresh} {
		if err := a.CheckPeer(ctx, "H1", s); err != nil {
			t.Fatalf("serial %s refused after a handshake with the old one: %v", s, err)
		}
	}
	if err := a.Handshake(ctx, "H1", fresh); err != nil {
		t.Fatal(err)
	}
	wantCode(t, a.CheckPeer(ctx, "H1", old), errcode.Forbidden, "old serial after the new one's handshake")
	if err := a.CheckPeer(ctx, "H1", fresh); err != nil {
		t.Fatalf("new serial refused: %v", err)
	}
}

func TestRenewRefusesHostsThatAreNotEnrolled(t *testing.T) {
	a, db, _ := newAuthority(t)
	addHost(t, db, "HP", "pending")
	addHost(t, db, "HR", "revoked")
	for _, host := range []string{"HP", "HR", "H9"} {
		csr, _ := newCSR(t)
		_, err := a.Renew(context.Background(), host, csr)
		wantCode(t, err, errcode.Forbidden, "renew "+host)
	}
	addHost(t, db, "H1", "enrolled")
	_, err := a.Renew(context.Background(), "H1", []byte("not a request"))
	wantCode(t, err, errcode.InvalidInput, "renew with a bad CSR")
	if n := countCerts(t, db); n != 0 {
		t.Fatalf("certificate rows = %d after refused renewals", n)
	}
}

func TestHandshakeRevokesOnlyEarlierSerials(t *testing.T) {
	a, db, clock := newAuthority(t)
	ctx := context.Background()
	addHost(t, db, "H1", "enrolled")
	addHost(t, db, "H2", "enrolled")
	var serials []string
	for range 3 {
		serials = append(serials, transport.SerialText(issue(t, a, db, "H1").SerialNumber))
		clock.Advance(time.Minute)
	}
	other := transport.SerialText(issue(t, a, db, "H2").SerialNumber)
	if err := a.Handshake(ctx, "H1", serials[1]); err != nil {
		t.Fatal(err)
	}
	wantCode(t, a.CheckPeer(ctx, "H1", serials[0]), errcode.Forbidden, "earlier serial")
	for _, s := range serials[1:] {
		if err := a.CheckPeer(ctx, "H1", s); err != nil {
			t.Fatalf("serial %s refused: %v", s, err)
		}
	}
	if err := a.CheckPeer(ctx, "H2", other); err != nil {
		t.Fatalf("another host's serial was revoked: %v", err)
	}
	if err := a.Handshake(ctx, "H1", "unknown"); err != nil {
		t.Fatalf("handshake with an unknown serial: %v", err)
	}
	if err := a.CheckPeer(ctx, "H1", serials[2]); err != nil {
		t.Fatalf("an unknown serial's handshake revoked a valid one: %v", err)
	}
}

func TestRevokeHost(t *testing.T) {
	a, db, clock := newAuthority(t)
	ctx := context.Background()
	addHost(t, db, "H1", "enrolled")
	addHost(t, db, "H2", "enrolled")
	first := transport.SerialText(issue(t, a, db, "H1").SerialNumber)
	second := transport.SerialText(issue(t, a, db, "H1").SerialNumber)
	other := transport.SerialText(issue(t, a, db, "H2").SerialNumber)
	for _, host := range []string{"H1", "H2"} {
		if _, err := db.Writer().ExecContext(ctx,
			`INSERT INTO core_enroll_tokens (host_id, hash, expires_at, created_at) VALUES (?, x'00', '2026-10-19T13:00:00.000000000Z', '2026-10-19T12:00:00.000000000Z')`,
			host); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(time.Minute)
	if err := db.Write(ctx, func(tx *sql.Tx) error { return RevokeHost(ctx, tx, "H1", clock.Now()) }); err != nil {
		t.Fatal(err)
	}
	var tokens int
	if err := db.Read().QueryRow(`SELECT count(*) FROM core_enroll_tokens WHERE host_id = 'H1'`).Scan(&tokens); err != nil || tokens != 0 {
		t.Fatalf("revoked host's tokens = %d, %v; want 0", tokens, err)
	}
	if err := db.Read().QueryRow(`SELECT count(*) FROM core_enroll_tokens WHERE host_id = 'H2'`).Scan(&tokens); err != nil || tokens != 1 {
		t.Fatalf("another host's tokens = %d, %v; want 1", tokens, err)
	}
	var state string
	if err := db.Read().QueryRow(`SELECT state FROM core_hosts WHERE id = 'H1'`).Scan(&state); err != nil || state != "revoked" {
		t.Fatalf("state = %q, %v; want revoked", state, err)
	}
	var revokedAt string
	if err := db.Read().QueryRow(`SELECT revoked_at FROM core_agent_certs WHERE serial = ?`, first).Scan(&revokedAt); err != nil ||
		revokedAt != "2026-10-19T12:01:00.000000000Z" {
		t.Fatalf("revoked_at = %q, %v", revokedAt, err)
	}
	if _, err := db.Writer().ExecContext(ctx, `UPDATE core_hosts SET state = 'enrolled' WHERE id = 'H1'`); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{first, second} {
		wantCode(t, a.CheckPeer(ctx, "H1", s), errcode.Forbidden, "revoked host's serial")
	}
	if err := a.CheckPeer(ctx, "H2", other); err != nil {
		t.Fatalf("another host's serial was revoked: %v", err)
	}
	err := db.Write(ctx, func(tx *sql.Tx) error { return RevokeHost(ctx, tx, "H9", clock.Now()) })
	wantCode(t, err, errcode.NotFound, "revoke an unknown host")
}
