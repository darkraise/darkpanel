package fleet

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/transport"
)

// testStart is the fake clock's start; whole seconds, as X.509 times are.
var testStart = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// newSealer returns a production sealer over a random key.
func newSealer(t *testing.T) *secret.Sealer {
	t.Helper()
	key := make([]byte, secret.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	s, err := secret.NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// openControllerDB returns a migrated controller database in a temporary
// directory.
func openControllerDB(t *testing.T) *store.DB {
	t.Helper()
	return openControllerDBAt(t, filepath.Join(t.TempDir(), "controller.db"))
}

// openControllerDBAt opens the controller database at path, applying the
// core migrations when they are pending.
func openControllerDBAt(t *testing.T, path string) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	opts := store.MigrateOptions{
		Side: store.SideController, BinaryVersion: "test",
		BackupRoot: filepath.Join(filepath.Dir(path), "backups"), Keep: 5, Now: time.Now(),
	}
	if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
		t.Fatal(err)
	}
	return db
}

// newAuthority returns an authority over a fresh controller database and its
// fake clock.
func newAuthority(t *testing.T) (*Authority, *store.DB, *coretest.FakeClock) {
	t.Helper()
	db := openControllerDB(t)
	clock := coretest.NewFakeClock(testStart)
	a, err := LoadOrCreateAuthority(context.Background(), db, newSealer(t), clock)
	if err != nil {
		t.Fatal(err)
	}
	return a, db, clock
}

func TestAuthorityCreatesRoot(t *testing.T) {
	ctx := context.Background()
	db := openControllerDB(t)
	sealer := newSealer(t)
	a, err := LoadOrCreateAuthority(ctx, db, sealer, coretest.NewFakeClock(testStart))
	if err != nil {
		t.Fatal(err)
	}
	cert := a.Root()
	if _, ok := cert.PublicKey.(ed25519.PublicKey); !ok {
		t.Fatalf("root key is %T, want Ed25519", cert.PublicKey)
	}
	if !cert.IsCA || !cert.BasicConstraintsValid || cert.MaxPathLen != 0 || !cert.MaxPathLenZero {
		t.Fatalf("root constraints: IsCA %v, valid %v, MaxPathLen %d, zero %v",
			cert.IsCA, cert.BasicConstraintsValid, cert.MaxPathLen, cert.MaxPathLenZero)
	}
	if cert.KeyUsage != x509.KeyUsageCertSign|x509.KeyUsageCRLSign {
		t.Fatalf("root key usage = %v", cert.KeyUsage)
	}
	if !cert.NotAfter.Equal(testStart.AddDate(10, 0, 0)) || !cert.NotBefore.Equal(testStart.Add(-time.Hour)) {
		t.Fatalf("root validity %v to %v", cert.NotBefore, cert.NotAfter)
	}
	if err := cert.CheckSignatureFrom(cert); err != nil {
		t.Fatalf("root is not self-signed: %v", err)
	}
	if a.Fingerprint() != transport.Fingerprint(cert) {
		t.Fatalf("Fingerprint = %s, want %s", a.Fingerprint(), transport.Fingerprint(cert))
	}
	block, rest := pem.Decode(a.RootPEM())
	if block == nil || block.Type != "CERTIFICATE" || !bytes.Equal(block.Bytes, cert.Raw) || len(bytes.TrimSpace(rest)) != 0 {
		t.Fatalf("RootPEM is not the root as one CERTIFICATE block: %q", a.RootPEM())
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: a.Pool()}); err != nil {
		t.Fatalf("the root does not verify against Pool: %v", err)
	}

	if _, err := ulid.ParseStrict(a.ControllerID()); err != nil {
		t.Fatalf("ControllerID %q is not a ULID: %v", a.ControllerID(), err)
	}
	if a.ControllerSAN() != "darkpanel://controller/"+a.ControllerID() {
		t.Fatalf("ControllerSAN = %q", a.ControllerSAN())
	}
	var storedID string
	if err := db.Read().QueryRowContext(ctx, `SELECT value FROM core_meta WHERE key = 'controller_id'`).Scan(&storedID); err != nil || storedID != a.ControllerID() {
		t.Fatalf("core_meta controller_id = %q, %v; want %q", storedID, err, a.ControllerID())
	}

	var id, purpose, certPEM, createdAt string
	var keyCT []byte
	if err := db.Read().QueryRowContext(ctx,
		`SELECT id, purpose, cert_pem, key_ct, created_at FROM core_ca`).Scan(&id, &purpose, &certPEM, &keyCT, &createdAt); err != nil {
		t.Fatal(err)
	}
	if purpose != PurposeAgent || certPEM != string(a.RootPEM()) || createdAt != "2026-10-19T12:00:00.000000000Z" {
		t.Fatalf("core_ca row: purpose %q, created_at %q, cert_pem matches %v", purpose, createdAt, certPEM == string(a.RootPEM()))
	}
	der, err := sealer.Open("core_ca.key_ct."+id, keyCT)
	if err != nil {
		t.Fatalf("key_ct does not open with its row's associated data: %v", err)
	}
	if bytes.Contains(keyCT, der) {
		t.Fatal("key_ct holds the key in clear")
	}
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		t.Fatal(err)
	}
	if k, ok := key.(ed25519.PrivateKey); !ok || !k.Public().(ed25519.PublicKey).Equal(cert.PublicKey) {
		t.Fatal("the sealed key is not the root's key")
	}
}

func TestAuthorityLoadsTheSameRoot(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "controller.db")
	sealer := newSealer(t)
	clock := coretest.NewFakeClock(testStart)
	first, err := LoadOrCreateAuthority(ctx, openControllerDBAt(t, path), sealer, clock)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(24 * time.Hour)
	db := openControllerDBAt(t, path)
	second, err := LoadOrCreateAuthority(ctx, db, sealer, clock)
	if err != nil {
		t.Fatal(err)
	}
	if second.Fingerprint() != first.Fingerprint() || !second.key.Equal(first.key) || second.ControllerID() != first.ControllerID() {
		t.Fatal("a second start created a new root or controller id")
	}
	var n int
	if err := db.Read().QueryRowContext(ctx, `SELECT count(*) FROM core_ca`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("core_ca rows = %d, %v; want 1", n, err)
	}
}

func TestAuthorityConcurrentStartsAgree(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "controller.db")
	sealer := newSealer(t)
	clock := coretest.NewFakeClock(testStart)
	const starts = 4
	dbs := make([]*store.DB, starts)
	for i := range dbs {
		dbs[i] = openControllerDBAt(t, path)
	}
	prints := make([]string, starts)
	ids := make([]string, starts)
	errs := make([]error, starts)
	var ready, wg sync.WaitGroup
	gate := make(chan struct{})
	for i := range starts {
		ready.Add(1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			ready.Done()
			<-gate
			a, err := LoadOrCreateAuthority(ctx, dbs[i], sealer, clock)
			if err != nil {
				errs[i] = err
				return
			}
			prints[i], ids[i] = a.Fingerprint(), a.ControllerID()
		}()
	}
	ready.Wait()
	close(gate)
	wg.Wait()
	for i := range starts {
		if errs[i] != nil {
			t.Fatalf("start %d: %v", i, errs[i])
		}
		if prints[i] != prints[0] || ids[i] != ids[0] {
			t.Fatalf("start %d has root %s and id %s, start 0 has %s and %s", i, prints[i], ids[i], prints[0], ids[0])
		}
	}
	var roots, metas int
	if err := dbs[0].Read().QueryRowContext(ctx, `SELECT count(*) FROM core_ca`).Scan(&roots); err != nil || roots != 1 {
		t.Fatalf("core_ca rows = %d, %v; want 1", roots, err)
	}
	if err := dbs[0].Read().QueryRowContext(ctx, `SELECT count(*) FROM core_meta WHERE key = 'controller_id'`).Scan(&metas); err != nil || metas != 1 {
		t.Fatalf("controller_id rows = %d, %v; want 1", metas, err)
	}
}

func TestAuthorityIgnoresOtherPurposes(t *testing.T) {
	ctx := context.Background()
	db := openControllerDB(t)
	if _, err := db.Writer().ExecContext(ctx,
		`INSERT INTO core_ca (id, purpose, cert_pem, key_ct, created_at) VALUES ('web1', 'web', 'not a certificate', x'00', '2026-10-17T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	a, err := LoadOrCreateAuthority(ctx, db, newSealer(t), coretest.NewFakeClock(testStart))
	if err != nil {
		t.Fatalf("the web root row stopped the agent root: %v", err)
	}
	var n int
	if err := db.Read().QueryRowContext(ctx, `SELECT count(*) FROM core_ca WHERE purpose = ?`, PurposeAgent).Scan(&n); err != nil || n != 1 {
		t.Fatalf("agent roots = %d, %v; want 1", n, err)
	}
	if a.Root().Subject.CommonName == "" {
		t.Fatal("agent root has no common name")
	}
}

func TestAuthorityRefusesAnotherKeyOrMovedRow(t *testing.T) {
	ctx := context.Background()
	db := openControllerDB(t)
	clock := coretest.NewFakeClock(testStart)
	if _, err := LoadOrCreateAuthority(ctx, db, newSealer(t), clock); err != nil {
		t.Fatal(err)
	}
	_, err := LoadOrCreateAuthority(ctx, db, newSealer(t), clock)
	if err == nil || !strings.Contains(err.Error(), "fleet: agent authority") {
		t.Fatalf("load with another controller key: error = %v", err)
	}

	db = openControllerDB(t)
	sealer := newSealer(t)
	if _, err := LoadOrCreateAuthority(ctx, db, sealer, clock); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer().ExecContext(ctx, `UPDATE core_ca SET id = 'moved'`); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateAuthority(ctx, db, sealer, clock); err == nil {
		t.Fatal("a root whose row id changed opened; the associated data must bind the key to its row")
	}
}

// leafOf returns the listener certificate's leaf, checking the chain is
// [leaf, root].
func leafOf(t *testing.T, a *Authority, c *tls.Certificate) *x509.Certificate {
	t.Helper()
	if len(c.Certificate) != 2 || !bytes.Equal(c.Certificate[1], a.Root().Raw) {
		t.Fatalf("chain has %d certificates; want [leaf, root]", len(c.Certificate))
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if c.Leaf == nil || !bytes.Equal(c.Leaf.Raw, leaf.Raw) {
		t.Fatal("tls.Certificate.Leaf is not the chain's leaf")
	}
	return leaf
}

func TestControllerCertificate(t *testing.T) {
	a, _, _ := newAuthority(t)
	c, err := a.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	leaf := leafOf(t, a, c)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: a.Pool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Fatalf("the leaf does not verify against the agent root for serverAuth: %v", err)
	}
	if len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth || len(leaf.UnknownExtKeyUsage) != 0 {
		t.Fatalf("leaf EKU = %v, want serverAuth only", leaf.ExtKeyUsage)
	}
	if len(leaf.URIs) != 1 || leaf.URIs[0].String() != a.ControllerSAN() {
		t.Fatalf("leaf URIs = %v, want [%s]", leaf.URIs, a.ControllerSAN())
	}
	if len(leaf.DNSNames) != 0 || len(leaf.IPAddresses) != 0 || len(leaf.EmailAddresses) != 0 {
		t.Fatalf("leaf has DNS %v, IP %v or e-mail %v SANs", leaf.DNSNames, leaf.IPAddresses, leaf.EmailAddresses)
	}
	if leaf.IsCA {
		t.Fatal("the leaf is a CA")
	}
	pub, ok := leaf.PublicKey.(ed25519.PublicKey)
	if !ok {
		t.Fatalf("leaf key is %T, want Ed25519", leaf.PublicKey)
	}
	if priv, ok := c.PrivateKey.(ed25519.PrivateKey); !ok || !priv.Public().(ed25519.PublicKey).Equal(pub) {
		t.Fatal("the private key does not match the leaf")
	}
	if !leaf.NotBefore.Equal(testStart.Add(-time.Hour)) || !leaf.NotAfter.Equal(testStart.Add(CertValidity)) {
		t.Fatalf("leaf validity %v to %v", leaf.NotBefore, leaf.NotAfter)
	}
}

func TestControllerCertificateRenewal(t *testing.T) {
	a, _, clock := newAuthority(t)
	first, err := a.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(CertRenewAfter - time.Second)
	same, err := a.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if same != first {
		t.Fatal("the leaf was reissued before two thirds of its life")
	}
	clock.Advance(time.Second)
	renewed, err := a.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if renewed == first || leafOf(t, a, renewed).SerialNumber.Cmp(leafOf(t, a, first).SerialNumber) == 0 {
		t.Fatal("the leaf was not reissued after two thirds of its life")
	}
	if !leafOf(t, a, renewed).NotBefore.Equal(clock.Now().Add(-time.Hour)) {
		t.Fatal("the renewed leaf does not start from the renewal time")
	}
}

// TestControllerCertificateHandshake proves Go's TLS 1.3 accepts the Ed25519
// chain: the client checks the chain against the agent root alone and the
// controller SAN itself, as the agent does.
func TestControllerCertificateHandshake(t *testing.T) {
	a, _, _ := newAuthority(t)
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close(); _ = clientConn.Close() })
	server := tls.Server(serverConn, &tls.Config{MinVersion: tls.VersionTLS13, GetCertificate: a.GetCertificate})
	client := tls.Client(clientConn, &tls.Config{
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, //nolint:gosec // G402: VerifyConnection below does the whole check.
		VerifyConnection: func(cs tls.ConnectionState) error {
			leaf := cs.PeerCertificates[0]
			if _, err := leaf.Verify(x509.VerifyOptions{Roots: a.Pool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
				return err
			}
			if len(leaf.URIs) != 1 || leaf.URIs[0].String() != a.ControllerSAN() {
				t.Errorf("handshake leaf URIs = %v", leaf.URIs)
			}
			return nil
		},
	})
	errs := make(chan error, 1)
	go func() { errs <- server.Handshake() }()
	if err := client.Handshake(); err != nil {
		t.Fatalf("client handshake: %v", err)
	}
	if err := <-errs; err != nil {
		t.Fatalf("server handshake: %v", err)
	}
	if v := client.ConnectionState().Version; v != tls.VersionTLS13 {
		t.Fatalf("TLS version %x, want 1.3", v)
	}
}
