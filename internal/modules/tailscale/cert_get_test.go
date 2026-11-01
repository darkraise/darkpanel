package tailscale

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

const ceName = thHostname + ".example.ts.net"

var ceNotAfter = time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)

// ceMakePair returns a self-signed certificate for names, valid until notAfter, and its EC private key, both PEM.
func ceMakePair(t *testing.T, notAfter time.Time, names ...string) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: notAfter.Add(-90 * 24 * time.Hour), NotAfter: notAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

// ceHost is a host whose tailscaled offers ceName as its certificate domain and holds a valid pair for it.
func ceHost(t *testing.T) (h *tsHost, certPEM, keyPEM []byte) {
	t.Helper()
	h = newTSHost(t)
	certPEM, keyPEM = ceMakePair(t, ceNotAfter, ceName)
	h.local.Status.CertDomains = []string{ceName}
	h.local.Cert = [2][]byte{certPEM, keyPEM}
	return h, certPEM, keyPEM
}

// ceReveal runs the action's reveal handler the way the agent's Engine.Reveal does and decodes its value.
func ceReveal(t *testing.T, h *tsHost) (identity string, out CertPair, raw json.RawMessage, err error) {
	t.Helper()
	identity, raw, err = h.ts().certGet().Handler.Reveal(context.Background(), h.env(""), json.RawMessage(`{}`))
	if err != nil {
		return identity, CertPair{}, nil, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("the value %s does not decode as a CertPair: %v", raw, err)
	}
	return identity, out, raw, nil
}

func TestCeCertGetDeclaration(t *testing.T) {
	def := newTSHost(t).ts().certGet()
	s := def.Spec
	if s.Name != "tailscale.cert.get" || s.Version != 1 || s.Class != core.ClassRead || s.Side != core.SideAgent ||
		!s.Internal || s.Reveal || s.MinScope != "" || len(s.LockKeys) != 0 || len(s.Guards) != 0 || s.Available != nil || s.Summary == "" {
		t.Errorf("spec %+v, want the internal read tailscale.cert.get v1 on the agent with no lock, guard, minimum scope or availability check", s)
	}
	if def.InputType != reflect.TypeFor[CertGetInput]() || def.OutputType != reflect.TypeFor[CertPair]() {
		t.Errorf("types %v and %v, want CertGetInput and CertPair", def.InputType, def.OutputType)
	}
	if def.Handler.Reveal == nil || def.Handler.Read != nil || def.Handler.Plan != nil || def.Handler.Apply != nil {
		t.Errorf("handler %+v, want the reveal handler only", def.Handler)
	}
}

// TestCeCertGetReturnsThePair: the answer is the node's name, both PEM texts as tailscaled gave them (the key
// not redacted: the reveal path applies none) and the certificate's expiry; the identity names the certificate
// and the read changes nothing on the host.
func TestCeCertGetReturnsThePair(t *testing.T) {
	h, certPEM, keyPEM := ceHost(t)
	identity, out, raw, err := ceReveal(t, h)
	if err != nil {
		t.Fatal(err)
	}
	if identity != "tailscale certificate for "+ceName {
		t.Errorf("identity = %q", identity)
	}
	if out.Name != ceName || out.CertPEM != string(certPEM) || out.KeyPEM != string(keyPEM) || !out.NotAfter.Equal(ceNotAfter) {
		t.Errorf("pair name %q notAfter %v (cert match %v, key match %v), want %s until %v with the PEM texts unchanged",
			out.Name, out.NotAfter, out.CertPEM == string(certPEM), out.KeyPEM == string(keyPEM), ceName, ceNotAfter)
	}
	if !strings.Contains(string(raw), `"notAfter":"2026-12-01T00:00:00Z"`) {
		t.Errorf("value %s lacks the expiry as UTC", raw)
	}
	if !reflect.DeepEqual(h.local.CertCalls, []string{ceName}) {
		t.Errorf("tailscaled was asked for %v, want only %s", h.local.CertCalls, ceName)
	}
	if len(h.ran) != 0 || len(h.files) != 0 || len(h.local.Edits) != 0 || len(h.local.Starts) != 0 || len(h.journal.effects()) != 0 {
		t.Errorf("the read changed the host: ran %v, files %d, journal %v", h.ran, len(h.files), h.journal.effects())
	}
}

// TestCeCertGetName: the first certificate domain is the node's name, written without a trailing dot, and only
// that one is requested.
func TestCeCertGetName(t *testing.T) {
	h, _, _ := ceHost(t)
	h.local.Status.CertDomains = []string{ceName + ".", "alias.example.ts.net"}
	_, out, _, err := ceReveal(t, h)
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != ceName || !reflect.DeepEqual(h.local.CertCalls, []string{ceName}) {
		t.Errorf("name %q, asked for %v, want %s once", out.Name, h.local.CertCalls, ceName)
	}
}

// TestCeCertGetChain: the expiry is the leaf's, the first certificate of the chain, and the chain is returned
// whole.
func TestCeCertGetChain(t *testing.T) {
	h, leaf, key := ceHost(t)
	issuer, _ := ceMakePair(t, ceNotAfter.Add(365*24*time.Hour), "issuer.example.net")
	chain := append(append([]byte{}, leaf...), issuer...)
	h.local.Cert = [2][]byte{chain, key}
	_, out, _, err := ceReveal(t, h)
	if err != nil {
		t.Fatal(err)
	}
	if !out.NotAfter.Equal(ceNotAfter) || out.CertPEM != string(chain) {
		t.Errorf("notAfter %v, chain kept %v, want the leaf's expiry %v and the whole chain", out.NotAfter, out.CertPEM == string(chain), ceNotAfter)
	}
}

// TestCeCertGetRefusals: every refusal is tailscale.unavailable with a fixed client-safe detail, and none of the
// error text carries the private key.
func TestCeCertGetRefusals(t *testing.T) {
	const (
		noSocket = "tailscaled does not answer on its socket"
		noDomain = "tailscaled offers no certificate domain; enable HTTPS certificates for the tailnet and log the node in"
		noPair   = "tailscaled could not provide a certificate for the node's name"
		badPair  = "tailscaled returned a certificate pair that is not usable"
	)
	otherCert, otherKey := ceMakePair(t, ceNotAfter, "other.example.ts.net")
	tests := []struct {
		name   string
		setup  func(h *tsHost, certPEM, keyPEM []byte)
		detail string
		cause  error
		asked  int
	}{
		{"tailscaled does not answer", func(h *tsHost, _, _ []byte) {
			h.local.Errs = map[string]error{"Status": tslocal.ErrUnavailable}
		}, noSocket, tslocal.ErrUnavailable, 0},
		{"no certificate domain", func(h *tsHost, _, _ []byte) { h.local.Status.CertDomains = nil }, noDomain, nil, 0},
		{"an empty domain", func(h *tsHost, _, _ []byte) { h.local.Status.CertDomains = []string{"."} }, noDomain, nil, 0},
		{"tailscaled cannot obtain the pair", func(h *tsHost, _, _ []byte) {
			h.local.Errs = map[string]error{"CertPair": &tslocal.APIError{StatusCode: 500, Message: "HTTPS is not enabled"}}
		}, noPair, &tslocal.APIError{StatusCode: 500, Message: "HTTPS is not enabled"}, 1},
		{"the certificate is not PEM", func(h *tsHost, _, keyPEM []byte) {
			h.local.Cert = [2][]byte{[]byte("not a certificate"), keyPEM}
		}, badPair, nil, 1},
		{"the key is not the certificate's", func(h *tsHost, certPEM, _ []byte) {
			h.local.Cert = [2][]byte{certPEM, otherKey}
		}, badPair, nil, 1},
		{"the certificate is for another name", func(h *tsHost, _, _ []byte) {
			h.local.Cert = [2][]byte{otherCert, otherKey}
		}, badPair, nil, 1},
	}
	for _, tt := range tests {
		h, certPEM, keyPEM := ceHost(t)
		tt.setup(h, certPEM, keyPEM)
		_, _, _, err := ceReveal(t, h)
		coded := errcode.From(err)
		if err == nil || coded.Code != errcode.TailscaleUnavailable || coded.Detail != tt.detail {
			t.Errorf("%s: err = %#v, want tailscale.unavailable with %q", tt.name, err, tt.detail)
			continue
		}
		if tt.cause != nil && !errors.Is(err, tt.cause) && !reflect.DeepEqual(errors.Unwrap(err), tt.cause) {
			t.Errorf("%s: the cause %v is not kept in the chain of %v", tt.name, tt.cause, err)
		}
		if len(h.local.CertCalls) != tt.asked {
			t.Errorf("%s: tailscaled was asked for %d pairs, want %d", tt.name, len(h.local.CertCalls), tt.asked)
		}
		for _, secret := range [][]byte{keyPEM, otherKey} {
			body := strings.Split(string(secret), "\n")[1]
			if strings.Contains(err.Error(), body) || strings.Contains(coded.Detail, body) {
				t.Errorf("%s: the error text carries private key material: %v", tt.name, err)
			}
		}
	}
}

// TestCeCertGetCancelled: a read cut off by its own context is that context's error, not an unavailable
// tailscaled.
func TestCeCertGetCancelled(t *testing.T) {
	h, _, _ := ceHost(t)
	h.local.Errs = map[string]error{"Status": context.Canceled}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := h.ts().certGet().Handler.Reveal(ctx, h.env(""), json.RawMessage(`{}`))
	if !errors.Is(err, context.Canceled) || errcode.From(err).Code == errcode.TailscaleUnavailable {
		t.Errorf("err = %v, want context.Canceled and not tailscale.unavailable", err)
	}
}
