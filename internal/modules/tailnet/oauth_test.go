package tailnet

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud/cloudfake"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailnet/fake"
)

// apFetcher is a token source that counts its calls.
type apFetcher struct {
	calls atomic.Int32
	ttl   time.Duration
	err   error
}

func (f *apFetcher) fetch(context.Context) (string, time.Duration, error) {
	n := f.calls.Add(1)
	if f.err != nil {
		return "", 0, f.err
	}
	return fmt.Sprintf("tok%d", n), f.ttl, nil
}

func TestAPTokenCacheRenewsAtFiftyMinutes(t *testing.T) {
	base := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		ttl     time.Duration
		elapsed time.Duration
		want    string
	}{
		{"just under 50 minutes", time.Hour, 49*time.Minute + 59*time.Second, "tok1"},
		{"at 50 minutes", time.Hour, 50 * time.Minute, "tok2"},
		{"a token that lives 10 minutes is renewed at 10", 10 * time.Minute, 10 * time.Minute, "tok2"},
		{"a token that lives 10 minutes is kept at 9", 10 * time.Minute, 9 * time.Minute, "tok1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, f := newTokenCache(), &apFetcher{ttl: tc.ttl}
			if got, err := c.token(context.Background(), "client", f.fetch, base); err != nil || got != "tok1" {
				t.Fatalf("first token = %q, %v; want tok1", got, err)
			}
			got, err := c.token(context.Background(), "client", f.fetch, base.Add(tc.elapsed))
			if err != nil || got != tc.want {
				t.Errorf("token after %s = %q, %v; want %s", tc.elapsed, got, err, tc.want)
			}
		})
	}
}

func TestAPTokenCacheKeepsKeysApart(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	c, f := newTokenCache(), &apFetcher{ttl: time.Hour}
	a, _ := c.token(context.Background(), "a", f.fetch, now)
	b, _ := c.token(context.Background(), "b", f.fetch, now)
	a2, _ := c.token(context.Background(), "a", f.fetch, now)
	if a == b || a != a2 || f.calls.Load() != 2 {
		t.Errorf("tokens a=%q b=%q a again=%q after %d fetches, want two fetches and a reused", a, b, a2, f.calls.Load())
	}
}

func TestAPTokenCacheDoesNotKeepFailures(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	c, f := newTokenCache(), &apFetcher{ttl: time.Hour, err: errors.New("down")}
	if _, err := c.token(context.Background(), "a", f.fetch, now); err == nil {
		t.Fatal("token succeeded while the fetch failed")
	}
	f.err = nil
	if got, err := c.token(context.Background(), "a", f.fetch, now); err != nil || got != "tok2" {
		t.Errorf("token after the failure = %q, %v; want a new fetch, tok2", got, err)
	}
}

func TestAPTokenCacheFetchesOnceForConcurrentCallers(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	c, f := newTokenCache(), &apFetcher{ttl: time.Hour}
	var wg sync.WaitGroup
	got := make([]string, 8)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], _ = c.token(context.Background(), "a", f.fetch, now)
		}()
	}
	wg.Wait()
	if f.calls.Load() != 1 {
		t.Errorf("%d fetches, want 1", f.calls.Load())
	}
	for _, g := range got {
		if g != "tok1" {
			t.Errorf("a caller got %q, want tok1", g)
		}
	}
}

func TestAPTokenRequestShapeAndRenewal(t *testing.T) {
	srv := fake.New(t)
	clock := apClock()
	a := apAPI(t, tnWith(srv), fake.ClientSecret, clock)
	ctx := context.Background()
	if devices, err := a.Devices(ctx); err != nil || len(devices) != 0 {
		t.Fatalf("Devices = %v, %v; want none", devices, err)
	}

	tokenReqs := apRequests(srv, http.MethodPost, "/api/v2/oauth/token")
	if len(tokenReqs) != 1 {
		t.Fatalf("%d token requests, want 1", len(tokenReqs))
	}
	tr := tokenReqs[0]
	form, err := url.ParseQuery(string(tr.Body))
	if err != nil {
		t.Fatal(err)
	}
	wantForm := url.Values{"client_id": {fake.ClientID}, "client_secret": {fake.ClientSecret}, "grant_type": {"client_credentials"}}
	if !reflect.DeepEqual(form, wantForm) {
		t.Errorf("token form %v, want %v", form, wantForm)
	}
	if ct := tr.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
		t.Errorf("token Content-Type %q, want application/x-www-form-urlencoded", ct)
	}
	if tr.Header.Get("Authorization") != "" {
		t.Error("the token request carries an Authorization header")
	}
	dev := apRequests(srv, http.MethodGet, apTailnet("/devices"))
	if len(dev) != 1 || !strings.HasPrefix(dev[0].Header.Get("Authorization"), "Bearer ") || len(dev[0].Header.Get("Authorization")) <= len("Bearer ") {
		t.Errorf("device requests %+v, want one with a bearer token", dev)
	}

	if _, err := a.Devices(ctx); err != nil {
		t.Fatal(err)
	}
	if n := srv.Tokens(); n != 1 {
		t.Errorf("%d tokens issued after two calls, want 1", n)
	}
	clock.Advance(50 * time.Minute)
	if _, err := a.Devices(ctx); err != nil {
		t.Fatal(err)
	}
	if n := srv.Tokens(); n != 2 {
		t.Errorf("%d tokens issued after 50 minutes, want 2", n)
	}
}

func TestAPTokenIsKeyedByTheSecretToo(t *testing.T) {
	const wrong = "wrong-secret-value"
	srv := fake.New(t)
	shared := tnWith(srv)
	clock := apClock()
	ctx := context.Background()
	if _, err := apAPI(t, shared, fake.ClientSecret, clock).Devices(ctx); err != nil {
		t.Fatal(err)
	}

	srv.Cloud().Fail(http.MethodPost, "/api/v2/oauth/token", cloudfake.Fault{Status: http.StatusUnauthorized, Body: `{"error":"invalid_client"}`})
	_, err := apAPI(t, shared, wrong, clock).Devices(ctx)
	if got := apCode(t, err); got != errcode.CloudAuthFailed {
		t.Fatalf("code %s, want %s: a changed secret must not reuse the cached token", got, errcode.CloudAuthFailed)
	}
	if strings.Contains(err.Error(), wrong) || strings.Contains(errcode.From(err).Detail, wrong) {
		t.Errorf("the error %q carries the client secret", err)
	}
	if n := len(apRequests(srv, http.MethodGet, apTailnet("/devices"))); n != 1 {
		t.Errorf("%d device reads, want only the first call's", n)
	}
}

func TestAPTokenEndpointFailureIsUnavailableAndDefinite(t *testing.T) {
	srv, a := apFake(t)
	srv.Cloud().Fail(http.MethodPost, "/api/v2/oauth/token", cloudfake.Fault{Status: http.StatusInternalServerError})
	_, err := a.Devices(context.Background())
	if got := apCode(t, err); got != errcode.CloudUnavailable {
		t.Errorf("code %s, want %s", got, errcode.CloudUnavailable)
	}
	if !core.IsDefinite(err) {
		t.Error("a call that never reached the API is definite")
	}
	if n := len(apRequests(srv, http.MethodGet, apTailnet("/devices"))); n != 0 {
		t.Errorf("%d device reads sent without a token", n)
	}
}
