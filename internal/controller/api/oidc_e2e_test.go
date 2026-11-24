package api

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
)

// TestServeOIDCE2E serves the real controller API and web UI over TLS on
// https://127.0.0.1:<p> with OIDC set to a stub provider on
// https://localhost:<q>, another site, for web/e2e/oidc-cross-origin.pw.ts.
// It prints "E2E_READY app=<url> idp=<url>" and serves until it is
// interrupted or terminated, or its parent go command exits: go test gives
// the test binary no stdin to watch.
func TestServeOIDCE2E(t *testing.T) {
	if os.Getenv("DARKPANEL_E2E_SERVE") != "1" {
		t.Skip("serves the OIDC end-to-end test's controller; set DARKPANEL_E2E_SERVE=1")
	}
	cert, roots := e2eCertificate(t)
	clock := core.SystemClock{}

	idpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	idp := newE2EProvider(t, "https://localhost:"+portOf(idpListener), clock)
	idpServer := &http.Server{Handler: idp.mux(), TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = idpServer.ServeTLS(idpListener, "", "") }()
	t.Cleanup(func() { _ = idpServer.Close() })

	appListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	app := "https://" + appListener.Addr().String()

	h := newHarness(t)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	h.auth = auth.New(h.db, coretest.Sealer{}, clock, h.audit, auth.Options{Params: testParams, OIDCTransport: transport})
	h.cfg.Auth = h.auth
	h.cfg.UI = os.DirFS("../../../web/dist")
	if _, err := os.Stat("../../../web/dist/index.html"); err != nil {
		t.Fatalf("build the web UI first (pnpm -C web build): %v", err)
	}
	srv, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := coretest.Sealer{}.Seal("auth_settings.oidc_client_secret_ct.1", []byte(e2eClientSecret))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO auth_settings (id, oidc_issuer, oidc_client_id, oidc_client_secret_ct, oidc_display_name,
			oidc_redirect_base, oidc_require_totp, updated_at) VALUES (1, ?, ?, ?, 'Stub', ?, 0, ?)`,
			[]any{idp.issuer, e2eClientID, ct, app, time.Now().UTC().Format(time.RFC3339)}},
		{`UPDATE core_users SET email = ? WHERE id = ?`, []any{e2eEmail, h.user.ID}},
	} {
		if _, err := h.db.Writer().Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}

	appServer := &http.Server{Handler: srv.Handler(), TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = appServer.ServeTLS(appListener, "", "") }()
	t.Cleanup(func() { _ = appServer.Close() })

	fmt.Printf("E2E_READY app=%s idp=%s\n", app, idp.issuer)
	waitForStop()
}

// waitForStop returns on SIGINT or SIGTERM, or once the parent process is
// gone, so a killed go command leaves no server behind.
func waitForStop() {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	parent := os.Getppid()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			if os.Getppid() != parent {
				return
			}
		}
	}
}

const (
	e2eClientID     = "darkpanel"
	e2eClientSecret = "e2e-client-secret" //nolint:gosec // G101: a test provider's client secret
	e2eEmail        = "admin@example.com"
)

func portOf(l net.Listener) string {
	_, port, _ := net.SplitHostPort(l.Addr().String())
	return port
}

// e2eCertificate is one self-signed certificate for 127.0.0.1 and
// localhost, and a pool that trusts it.
func e2eCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "darkpanel e2e"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true,
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

// e2eProvider is an OIDC provider whose authorize endpoint answers a page
// linking back with a code for e2eEmail, verified.
type e2eProvider struct {
	issuer string
	key    *rsa.PrivateKey
	clock  core.Clock

	mu    sync.Mutex
	codes map[string]e2eGrant
}

type e2eGrant struct {
	nonce, challenge, redirect string
}

func newE2EProvider(t *testing.T, issuer string, clock core.Clock) *e2eProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return &e2eProvider{issuer: issuer, key: key, clock: clock, codes: map[string]e2eGrant{}}
}

func (p *e2eProvider) mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeE2EJSON(w, http.StatusOK, map[string]any{
			"issuer": p.issuer, "authorization_endpoint": p.issuer + "/authorize", "token_endpoint": p.issuer + "/token",
			"jwks_uri": p.issuer + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		e := big.NewInt(int64(p.key.E)).Bytes()
		writeE2EJSON(w, http.StatusOK, map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(p.key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(e)}}})
	})
	mux.HandleFunc("GET /authorize", p.authorize)
	mux.HandleFunc("POST /token", p.token)
	return mux
}

func (p *e2eProvider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	back, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || q.Get("client_id") != e2eClientID || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" {
		writeE2EJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	code := rand.Text()
	p.mu.Lock()
	p.codes[code] = e2eGrant{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirect: back.String()}
	p.mu.Unlock()
	v := back.Query()
	v.Set("code", code)
	v.Set("state", q.Get("state"))
	back.RawQuery = v.Encode()
	// A link the user follows, not a redirect: Chromium treats a redirect
	// chain the app's page started as same-site, so a state cookie made
	// Strict would still reach the callback. A navigation the provider's page
	// starts is cross-site, as a real sign-in is.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Stub provider</title></head>`+
		`<body><a href="%s">Continue to Darkpanel</a></body></html>`, html.EscapeString(back.String()))
}

func (p *e2eProvider) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeE2EJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	id, secret, ok := r.BasicAuth()
	if !ok {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	p.mu.Lock()
	g, found := p.codes[r.PostForm.Get("code")]
	delete(p.codes, r.PostForm.Get("code"))
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !found || id != e2eClientID || secret != e2eClientSecret || r.PostForm.Get("redirect_uri") != g.redirect ||
		base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
		writeE2EJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	now := p.clock.Now()
	idToken, err := p.sign(map[string]any{"iss": p.issuer, "aud": e2eClientID, "sub": "e2e-subject", "email": e2eEmail,
		"email_verified": true, "nonce": g.nonce, "iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix()})
	if err != nil {
		writeE2EJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
		return
	}
	writeE2EJSON(w, http.StatusOK, map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": idToken})
}

// sign returns claims as an RS256 JWT.
func (p *e2eProvider) sign(claims map[string]any) (string, error) {
	enc := func(v any) (string, error) {
		b, err := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b), err
	}
	head, err := enc(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	body, err := enc(claims)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(head + "." + body))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", errors.Join(errors.New("sign the ID token"), err)
	}
	return head + "." + body + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func writeE2EJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
