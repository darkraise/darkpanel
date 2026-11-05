package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

const (
	fz8NoBlock  = "server: no CERTIFICATE block in the PEM data"
	fz8BadBlock = "server: the CERTIFICATE block is not a valid X.509 certificate"
)

// fz8CertPEM is a self-signed certificate that expires on 2027-10-20.
func fz8CertPEM(tb testing.TB) []byte {
	tb.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		tb.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "derpkey"},
		NotBefore:    time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC),
		NotAfter:     time.Date(2027, 10, 20, 9, 0, 0, 0, time.UTC),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		tb.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// FuzzParseCert feeds arbitrary bytes to ParseCert: it never panics; an error
// is one of the two fixed texts, so it never echoes the input, and returns
// nothing else; a certificate it accepts parses again from its DER, whose
// NotAfter is the time it returns, in UTC (R74).
func FuzzParseCert(f *testing.F) {
	cert := fz8CertPEM(f)
	params := pem.EncodeToMemory(&pem.Block{Type: "EC PARAMETERS", Bytes: []byte{1, 2, 3}})
	f.Add(cert)
	f.Add(append(append([]byte(nil), params...), cert...))
	f.Add(append(append([]byte(nil), cert...), cert...))
	f.Add(params)
	f.Add(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("nope")}))
	f.Add(append(append([]byte(nil), cert...), "trailing text"...))
	f.Add([]byte("not a pem file"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, in []byte) {
		der, notAfter, err := ParseCert(in)
		if err != nil {
			if msg := err.Error(); (msg != fz8NoBlock && msg != fz8BadBlock) || der != nil || !notAfter.IsZero() {
				t.Fatalf("ParseCert(%q) = %q, %v, %q", in[:min(len(in), 64)], der[:min(len(der), 8)], notAfter, msg[:min(len(msg), 64)])
			}
			return
		}
		parsed, err := x509.ParseCertificate(der)
		if err != nil || !parsed.NotAfter.Equal(notAfter) || notAfter.Location() != time.UTC {
			t.Fatalf("ParseCert(%q) returned a DER that does not give back %v in UTC: %v", in[:min(len(in), 64)], notAfter, err)
		}
	})
}
