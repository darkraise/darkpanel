package cli

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testCert is a certificate made in the test with its key.
type testCert struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func (c testCert) fingerprint() string {
	sum := sha256.Sum256(c.cert.Raw)
	return hex.EncodeToString(sum[:])
}

// newCert makes a certificate signed by parent, or a self-signed one when
// parent is nil.
func newCert(t *testing.T, tmpl *x509.Certificate, parent *testCert) testCert {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, signerCert := key, tmpl
	if parent != nil {
		signer, signerCert = parent.key, parent.cert
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signerCert, &key.PublicKey, signer)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return testCert{cert: cert, key: key}
}

func newTestCA(t *testing.T) testCert {
	return newCert(t, &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "darkpanel web root test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, MaxPathLenZero: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}, nil)
}

func newTestLeaf(t *testing.T, ca testCert, ip string) testCert {
	return newCert(t, &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "darkpanel"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP(ip)},
	}, &ca)
}

// serveTLS starts an HTTPS server on 127.0.0.1 presenting [leaf, ca].
func serveTLS(t *testing.T, leaf, ca testCert) *url.URL {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0) // refused handshakes are expected
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{
		Certificate: [][]byte{leaf.cert.Raw, ca.cert.Raw}, PrivateKey: leaf.key,
	}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func get(t *testing.T, u *url.URL, fingerprint string) error {
	t.Helper()
	c, err := newHTTPClient(u, fingerprint)
	if err != nil {
		t.Fatalf("newHTTPClient: %v", err)
	}
	resp, err := c.Get(u.String())
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	return nil
}

func TestPinnedCA(t *testing.T) {
	ca := newTestCA(t)
	u := serveTLS(t, newTestLeaf(t, ca, "127.0.0.1"), ca)
	if err := get(t, u, ca.fingerprint()); err != nil {
		t.Errorf("right fingerprint: %v", err)
	}
	if err := get(t, u, strings.ToUpper(ca.fingerprint())); err != nil {
		t.Errorf("upper-case fingerprint: %v", err)
	}
}

func TestPinnedCARefusals(t *testing.T) {
	ca := newTestCA(t)
	leaf := newTestLeaf(t, ca, "127.0.0.1")
	other := newTestCA(t)
	cases := []struct {
		name        string
		u           *url.URL
		fingerprint string
		want        string
	}{
		{"wrong fingerprint", serveTLS(t, leaf, ca), other.fingerprint(), "matches caFingerprint"},
		{"fingerprint of the leaf", serveTLS(t, leaf, ca), leaf.fingerprint(), "is not a CA"},
		{"leaf for another address", serveTLS(t, newTestLeaf(t, ca, "192.0.2.1"), ca), ca.fingerprint(), "192.0.2.1"},
		{"no fingerprint and a private root", serveTLS(t, leaf, ca), "", "certificate"},
	}
	for _, c := range cases {
		err := get(t, c.u, c.fingerprint)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want one containing %q", c.name, err, c.want)
		}
	}
}

func TestNewHTTPClientRefusesBadFingerprint(t *testing.T) {
	u, _ := url.Parse("https://127.0.0.1:8443")
	for _, fp := range []string{"abc", strings.Repeat("g", 64), strings.Repeat("ab:", 32)} {
		if _, err := newHTTPClient(u, fp); err == nil || !strings.Contains(err.Error(), "64 hexadecimal digits") {
			t.Errorf("fingerprint %q: error %v", fp, err)
		}
	}
	plain, _ := url.Parse("http://127.0.0.1:8080")
	if _, err := newHTTPClient(plain, strings.Repeat("ab", 32)); err != nil {
		t.Errorf("plain HTTP with a fingerprint: %v", err)
	}
}

func TestClientsRefuseRedirects(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(target.Close)
	redirect := httptest.NewServer(http.RedirectHandler(target.URL+"/api/v1/x", http.StatusFound))
	t.Cleanup(redirect.Close)
	plain, _ := url.Parse("http://127.0.0.1:8080")
	pinned, _ := url.Parse("https://127.0.0.1:8443")
	for _, c := range []struct {
		name        string
		base        *url.URL
		fingerprint string
	}{
		{"plain", plain, ""},
		{"pinned", pinned, strings.Repeat("ab", 32)},
	} {
		hc, err := newHTTPClient(c.base, c.fingerprint)
		if err != nil {
			t.Fatalf("%s: newHTTPClient: %v", c.name, err)
		}
		req, _ := http.NewRequest(http.MethodGet, redirect.URL, nil)
		req.Header.Set("Authorization", "Bearer test")
		resp, err := hc.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		if err == nil || !strings.Contains(err.Error(), "does not follow redirects") {
			t.Errorf("%s: error %v, want a refused redirect", c.name, err)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the redirect target got %d requests, want 0", n)
	}
}
