package listen

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// testStart is the fake clock's start; whole seconds, as X.509 times are.
var testStart = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

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

// rootCert parses an authority's root certificate PEM.
func rootCert(t *testing.T, a *Authority) *x509.Certificate {
	t.Helper()
	block, rest := pem.Decode(a.RootPEM())
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		t.Fatalf("RootPEM is not one CERTIFICATE block: %q", a.RootPEM())
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestAuthorityCreatesRoot(t *testing.T) {
	ctx := context.Background()
	db := openControllerDB(t)
	sealer := newSealer(t)
	clock := coretest.NewFakeClock(testStart)
	a, err := LoadOrCreateAuthority(ctx, db, sealer, clock)
	if err != nil {
		t.Fatal(err)
	}
	cert := rootCert(t, a)
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		t.Fatalf("root key is %T, want ECDSA P-256", cert.PublicKey)
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
	sum := sha256.Sum256(cert.Raw)
	if a.Fingerprint() != hex.EncodeToString(sum[:]) {
		t.Fatalf("Fingerprint = %s, want %x", a.Fingerprint(), sum)
	}

	var id, purpose, certPEM, createdAt string
	var keyCT []byte
	if err := db.Read().QueryRowContext(ctx,
		`SELECT id, purpose, cert_pem, key_ct, created_at FROM core_ca`).Scan(&id, &purpose, &certPEM, &keyCT, &createdAt); err != nil {
		t.Fatal(err)
	}
	if purpose != PurposeWeb || certPEM != string(a.RootPEM()) || createdAt != testStart.Format(time.RFC3339) {
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
	if k, ok := key.(*ecdsa.PrivateKey); !ok || !k.PublicKey.Equal(cert.PublicKey) {
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
	if second.Fingerprint() != first.Fingerprint() || !second.key.Equal(first.key) {
		t.Fatal("a second start created a new root")
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
			prints[i] = a.Fingerprint()
		}()
	}
	ready.Wait()
	close(gate)
	wg.Wait()
	for i := range starts {
		if errs[i] != nil {
			t.Fatalf("start %d: %v", i, errs[i])
		}
		if prints[i] != prints[0] {
			t.Fatalf("start %d has root %s, start 0 has %s", i, prints[i], prints[0])
		}
	}
	var n int
	if err := dbs[0].Read().QueryRowContext(ctx, `SELECT count(*) FROM core_ca`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("core_ca rows = %d, %v; want 1", n, err)
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
	if err == nil || !strings.Contains(err.Error(), "listen: web authority") {
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
		t.Fatal("a key sealed for another row id was accepted")
	}
}
