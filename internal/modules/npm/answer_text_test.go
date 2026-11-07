package npm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

// v1r11Reflecting starts an NPM that answers every request with status and puts the request's body and bearer token
// into error.message, as a compromised NPM would to leak what it was sent.
func v1r11Reflecting(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
			"code": status, "message": string(b) + " " + r.Header.Get("Authorization"),
		}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// v1r11Chain fails the test when err, or any error it wraps, names one of the secrets.
func v1r11Chain(t *testing.T, err error, secrets ...string) {
	t.Helper()
	for e := err; e != nil; e = errors.Unwrap(e) {
		for _, s := range secrets {
			if strings.Contains(e.Error(), s) {
				t.Errorf("error %q carries %q", e.Error(), s)
			}
		}
	}
}

func TestV1r11LoginFailureCarriesNoNPMText(t *testing.T) {
	ctx := context.Background()
	for _, status := range []int{http.StatusUnauthorized, http.StatusInternalServerError} {
		h := newTHHost(t)
		inst := h.deployed()
		srv := v1r11Reflecting(t, status)
		n := h.nm()
		n.d.api = apiVia(func() *npmapi.Client { return npmapi.New(srv.URL, nil) })
		_, err := n.d.api(ctx, h.env(""))
		var coded *errcode.Error
		if !errors.As(err, &coded) || coded.Code != errcode.NPMUnhealthy {
			t.Fatalf("status %d: error %v, want %s", status, err, errcode.NPMUnhealthy)
		}
		v1r11Chain(t, err, inst.Password)
	}
}

func TestV1r11MutationFailureCarriesNoNPMText(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		status int
		code   errcode.Code
		detail string
	}{
		{http.StatusBadRequest, errcode.InvalidInput, "Nginx Proxy Manager refused the request (HTTP 400)"},
		{http.StatusInternalServerError, errcode.NPMUnhealthy, "Nginx Proxy Manager did not answer"},
	} {
		srv := v1r11Reflecting(t, tc.status)
		c := npmapi.New(srv.URL, nil).WithToken("placeholder-token")
		_, err := c.CreateProxyHost(ctx, npmapi.ProxyHostBody{
			DomainNames: []string{"app.example.com"}, ForwardHost: "203.0.113.10", ForwardPort: 8080,
			AdvancedConfig: "placeholder-secret",
		})
		answered := proxyhostAddAnswer(err)
		var coded *errcode.Error
		if !errors.As(answered, &coded) || coded.Code != tc.code {
			t.Fatalf("status %d: error %v, want %s", tc.status, answered, tc.code)
		}
		if coded.Detail != tc.detail {
			t.Errorf("status %d: detail %q, want %q", tc.status, coded.Detail, tc.detail)
		}
		v1r11Chain(t, answered, "placeholder-token", "placeholder-secret", "app.example.com")
	}
}
