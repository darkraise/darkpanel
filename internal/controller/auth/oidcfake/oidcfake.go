// Package oidcfake is an OIDC provider for tests: discovery, a key set and
// a token endpoint over TLS, signing RS256 ID tokens with the claims each
// test registers for a code.
package oidcfake

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

// LeakMarker is in the body of every refused token request; no error, log
// or page may carry it.
const LeakMarker = "provider body that must not leak"

// Provider is a running fake provider.
type Provider struct {
	t   *testing.T
	srv *httptest.Server
	Key *rsa.PrivateKey
	// Discovery counts the discovery document's fetches.
	Discovery atomic.Int32

	mu        sync.Mutex
	codes     map[string]Code
	redirects []string
}

// Code is what the token endpoint answers for one authorization code: the
// ID token's claims signed with Key, if the PKCE verifier matches
// Challenge.
type Code struct {
	Claims    map[string]any
	Challenge string
	Key       *rsa.PrivateKey
}

// New starts a provider; t's cleanup stops it.
func New(t *testing.T) *Provider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &Provider{t: t, Key: key, codes: map[string]Code{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		p.Discovery.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": p.srv.URL, "authorization_endpoint": p.srv.URL + "/authorize", "token_endpoint": p.srv.URL + "/token",
			"jwks_uri": p.srv.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		e := big.NewInt(int64(key.E)).Bytes()
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(e)}}})
	})
	mux.HandleFunc("POST /token", p.token)
	p.srv = httptest.NewTLSServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

// URL is the provider's issuer.
func (p *Provider) URL() string { return p.srv.URL }

// Transport reaches the provider, trusting its certificate.
func (p *Provider) Transport() http.RoundTripper { return p.srv.Client().Transport }

// Register makes code answer c once.
func (p *Provider) Register(code string, c Code) {
	if c.Key == nil {
		c.Key = p.Key
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.codes[code] = c
}

// Redirects are the redirect_uri of each token request so far.
func (p *Provider) Redirects() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.redirects...)
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	c, ok := p.codes[r.PostForm.Get("code")]
	delete(p.codes, r.PostForm.Get("code"))
	p.redirects = append(p.redirects, r.PostForm.Get("redirect_uri"))
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != c.Challenge {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"` + LeakMarker + `"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": p.Sign(c.Key, c.Claims)})
}

// Sign returns claims as an RS256 JWT signed with key.
func (p *Provider) Sign(key *rsa.PrivateKey, claims map[string]any) string {
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			p.t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	head := enc(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"}) + "." + enc(claims)
	sum := sha256.Sum256([]byte(head))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		p.t.Fatal(err)
	}
	return head + "." + base64.RawURLEncoding.EncodeToString(sig)
}
