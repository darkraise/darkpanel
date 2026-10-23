package cloud

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud/cloudfake"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// clSleeps records the delays a client asked to wait, without waiting.
type clSleeps struct {
	mu     sync.Mutex
	delays []time.Duration
}

func (s *clSleeps) sleep(_ context.Context, d time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delays = append(s.delays, d)
	return nil
}

func (s *clSleeps) got() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.delays...)
}

// clClient is a client of fake under the base path /api, with a bearer
// token and recorded sleeps.
func clClient(t *testing.T, fake *cloudfake.Server, mod func(*Config)) (*Client, *clSleeps) {
	t.Helper()
	sleeps := &clSleeps{}
	cfg := Config{
		Provider: "tailscale", BaseURL: fake.URL() + "/api", HTTP: fake.Client(), Sleep: sleeps.sleep,
		Auth: func(r *http.Request) error { r.Header.Set("Authorization", "Bearer tok"); return nil },
	}
	if mod != nil {
		mod(&cfg)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c, sleeps
}

func clOK(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `{"ok":true}`)
}

func TestDoJoinsPathQueryAndCredentials(t *testing.T) {
	fake := cloudfake.New(t)
	fake.Handle(http.MethodGet, "/api/v2/devices", clOK)
	c, _ := clClient(t, fake, nil)
	res, err := c.Do(t.Context(), Request{Method: http.MethodGet, Path: "/v2/devices", Query: url.Values{"fields": {"all"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != http.StatusOK || string(res.Body) != `{"ok":true}` {
		t.Fatalf("answer = %d %s", res.Status, res.Body)
	}
	reqs := fake.Requests()
	if len(reqs) != 1 || reqs[0].RawQuery != "fields=all" || reqs[0].Header.Get("Authorization") != "Bearer tok" {
		t.Fatalf("requests = %+v", reqs)
	}
}

func TestDoMapsFailedAnswers(t *testing.T) {
	cases := []struct {
		method   string
		status   int
		times    int
		code     errcode.Code
		definite bool
		requests int
	}{
		{http.MethodGet, 401, 1, errcode.CloudAuthFailed, false, 1},
		{http.MethodPost, 401, 1, errcode.CloudAuthFailed, true, 1},
		{http.MethodPost, 403, 1, errcode.CloudPermissionMissing, true, 1},
		{http.MethodPut, 409, 1, errcode.CloudConflict, true, 1},
		{http.MethodPatch, 412, 1, errcode.CloudConflict, true, 1},
		{http.MethodPost, 400, 1, errcode.InvalidInput, true, 1},
		{http.MethodGet, 404, 1, errcode.InvalidInput, false, 1},
		{http.MethodDelete, 404, 1, errcode.InvalidInput, false, 1},
		{http.MethodPost, 429, 1, errcode.CloudRateLimited, true, 1},
		{http.MethodGet, 429, 4, errcode.CloudRateLimited, false, 4},
		{http.MethodGet, 503, 4, errcode.CloudUnavailable, false, 4},
		{http.MethodDelete, 500, 4, errcode.CloudUnavailable, false, 4},
		{http.MethodPost, 500, 1, errcode.CloudUncertainOutcome, false, 1},
		{http.MethodPut, 502, 1, errcode.CloudUncertainOutcome, false, 1},
		{http.MethodGet, 302, 1, errcode.CloudUnavailable, false, 1},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+http.StatusText(tc.status), func(t *testing.T) {
			fake := cloudfake.New(t)
			fake.Handle(tc.method, "/api/x", clOK)
			fake.Fail(tc.method, "/api/x", cloudfake.Fault{Status: tc.status, Times: tc.times})
			c, _ := clClient(t, fake, nil)
			_, err := c.Do(t.Context(), Request{Method: tc.method, Path: "/x"})
			if got := errcode.From(err).Code; got != tc.code {
				t.Fatalf("code = %s, want %s (err %v)", got, tc.code, err)
			}
			if core.IsDefinite(err) != tc.definite {
				t.Fatalf("definite = %v, want %v", core.IsDefinite(err), tc.definite)
			}
			if Status(err) != tc.status {
				t.Fatalf("Status = %d, want %d", Status(err), tc.status)
			}
			if n := len(fake.Requests()); n != tc.requests {
				t.Fatalf("sent %d requests, want %d", n, tc.requests)
			}
		})
	}
}

func TestReadsRetryWithBackoff(t *testing.T) {
	fake := cloudfake.New(t)
	fake.Handle(http.MethodGet, "/api/x", clOK)
	fake.Fail(http.MethodGet, "/api/x", cloudfake.Fault{Status: 503, Times: 2})
	c, sleeps := clClient(t, fake, nil)
	if _, err := c.Do(t.Context(), Request{Method: http.MethodGet, Path: "/x"}); err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{time.Second, 2 * time.Second}
	if got := sleeps.got(); !clEqualDurations(got, want) {
		t.Fatalf("delays = %v, want %v", got, want)
	}
}

func TestExhaustedReadsWaitThreeTimes(t *testing.T) {
	fake := cloudfake.New(t)
	fake.Handle(http.MethodDelete, "/api/x", clOK)
	fake.Fail(http.MethodDelete, "/api/x", cloudfake.Fault{Status: 500, Times: 4})
	c, sleeps := clClient(t, fake, nil)
	if _, err := c.Do(t.Context(), Request{Method: http.MethodDelete, Path: "/x"}); err == nil {
		t.Fatal("Do succeeded, want cloud.unavailable")
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	if got := sleeps.got(); !clEqualDurations(got, want) {
		t.Fatalf("delays = %v, want %v", got, want)
	}
}

func TestRetryAfter(t *testing.T) {
	start := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		retryAfter string
		delays     []time.Duration
		requests   int
	}{
		{"seconds", "7", []time.Duration{7 * time.Second}, 2},
		{"http date", start.Add(30 * time.Second).Format(http.TimeFormat), []time.Duration{30 * time.Second}, 2},
		{"unreadable", "soon", []time.Duration{time.Second}, 2},
		{"beyond the cap", "3600", nil, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := cloudfake.New(t)
			fake.Handle(http.MethodGet, "/api/x", clOK)
			fake.Fail(http.MethodGet, "/api/x", cloudfake.Fault{Status: 429, RetryAfter: tc.retryAfter})
			c, sleeps := clClient(t, fake, func(cfg *Config) { cfg.Clock = coretest.NewFakeClock(start) })
			_, err := c.Do(t.Context(), Request{Method: http.MethodGet, Path: "/x"})
			if tc.delays == nil {
				if errcode.From(err).Code != errcode.CloudRateLimited {
					t.Fatalf("err = %v, want cloud.rate_limited without waiting", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if got := sleeps.got(); !clEqualDurations(got, tc.delays) {
				t.Fatalf("delays = %v, want %v", got, tc.delays)
			}
			if n := len(fake.Requests()); n != tc.requests {
				t.Fatalf("sent %d requests, want %d", n, tc.requests)
			}
		})
	}
}

func TestLostWriteAnswerIsUncertainAndNotResent(t *testing.T) {
	fake := cloudfake.New(t)
	var commits atomic.Int32
	fake.Handle(http.MethodPost, "/api/keys", func(w http.ResponseWriter, _ *http.Request) {
		commits.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	fake.Fail(http.MethodPost, "/api/keys", cloudfake.Fault{LoseResponse: true})
	c, sleeps := clClient(t, fake, nil)
	_, err := c.Do(t.Context(), Request{Method: http.MethodPost, Path: "/keys", Body: []byte(`{}`)})
	if errcode.From(err).Code != errcode.CloudUncertainOutcome || core.IsDefinite(err) {
		t.Fatalf("err = %v, want an uncertain, not definite outcome", err)
	}
	if commits.Load() != 1 || len(fake.Requests()) != 1 || len(sleeps.got()) != 0 {
		t.Fatalf("commits %d, requests %d, sleeps %d; want one send and no retry", commits.Load(), len(fake.Requests()), len(sleeps.got()))
	}
}

func TestLostReadAnswerIsRetried(t *testing.T) {
	fake := cloudfake.New(t)
	fake.Handle(http.MethodGet, "/api/x", clOK)
	fake.Fail(http.MethodGet, "/api/x", cloudfake.Fault{LoseResponse: true, Times: 2})
	c, _ := clClient(t, fake, nil)
	if _, err := c.Do(t.Context(), Request{Method: http.MethodGet, Path: "/x"}); err != nil {
		t.Fatal(err)
	}
	if n := len(fake.Requests()); n != 3 {
		t.Fatalf("sent %d requests, want 3", n)
	}
}

// clRoundTrip is an http.RoundTripper made of a function.
type clRoundTrip func(*http.Request) (*http.Response, error)

func (f clRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEveryAttemptCarriesTheTimeout(t *testing.T) {
	var deadlines []time.Duration
	hc := &http.Client{Transport: clRoundTrip(func(r *http.Request) (*http.Response, error) {
		d, ok := r.Context().Deadline()
		if !ok {
			t.Error("attempt without a deadline")
		}
		deadlines = append(deadlines, time.Until(d))
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})}
	c, err := New(Config{Provider: "npm", BaseURL: "http://npm.invalid", HTTP: hc, Sleep: (&clSleeps{}).sleep})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.Do(t.Context(), Request{Method: http.MethodGet, Path: "/x"})
	if len(deadlines) != 1+MaxRetries {
		t.Fatalf("%d attempts, want %d", len(deadlines), 1+MaxRetries)
	}
	for _, d := range deadlines {
		if d <= Timeout-5*time.Second || d > Timeout {
			t.Fatalf("attempt deadline in %v, want about %v", d, Timeout)
		}
	}
}

func TestDetailNamesStatusAndCodeButNotTheBody(t *testing.T) {
	fake := cloudfake.New(t)
	fake.Handle(http.MethodPost, "/api/x", clOK)
	fake.Fail(http.MethodPost, "/api/x", cloudfake.Fault{Status: 400, Body: `{"code":"invalid_key","message":"secret-value tskey-abc"}`})
	c, _ := clClient(t, fake, func(cfg *Config) {
		cfg.ErrorCode = func(body []byte) string {
			if bytes.Contains(body, []byte("invalid_key")) {
				return "invalid_key"
			}
			return ""
		}
	})
	_, err := c.Do(t.Context(), Request{Method: http.MethodPost, Path: "/x"})
	e := errcode.From(err)
	if e.Detail != "tailscale answered 400 (invalid_key)" {
		t.Fatalf("detail = %q", e.Detail)
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("the error carries the body: %v", err)
	}
}

func TestCredentialFailureSendsNothing(t *testing.T) {
	fake := cloudfake.New(t)
	fake.Handle(http.MethodPost, "/api/x", clOK)
	c, _ := clClient(t, fake, func(cfg *Config) {
		cfg.Auth = func(*http.Request) error { return errors.New("no token") }
	})
	_, err := c.Do(t.Context(), Request{Method: http.MethodPost, Path: "/x"})
	if err == nil || !core.IsDefinite(err) || len(fake.Requests()) != 0 {
		t.Fatalf("err = %v, requests %d; want a definite error and nothing sent", err, len(fake.Requests()))
	}
}

func TestCancelledWaitEndsTheRetries(t *testing.T) {
	fake := cloudfake.New(t)
	fake.Handle(http.MethodGet, "/api/x", clOK)
	fake.Fail(http.MethodGet, "/api/x", cloudfake.Fault{Status: 503, Times: 4})
	c, _ := clClient(t, fake, func(cfg *Config) {
		cfg.Sleep = func(context.Context, time.Duration) error { return context.Canceled }
	})
	_, err := c.Do(t.Context(), Request{Method: http.MethodGet, Path: "/x"})
	if !errors.Is(err, context.Canceled) || len(fake.Requests()) != 1 {
		t.Fatalf("err = %v after %d requests, want context.Canceled after 1", err, len(fake.Requests()))
	}
}

func TestDefaultSleepWaitsOnTheClock(t *testing.T) {
	fake := cloudfake.New(t)
	fake.Handle(http.MethodGet, "/api/x", clOK)
	fake.Fail(http.MethodGet, "/api/x", cloudfake.Fault{Status: 503})
	clock := coretest.NewFakeClock(time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC))
	c, err := New(Config{Provider: "cloudflare", BaseURL: fake.URL() + "/api", HTTP: fake.Client(), Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := c.Do(t.Context(), Request{Method: http.MethodGet, Path: "/x"})
		done <- err
	}()
	for clock.Pending() == 0 {
		select {
		case err := <-done:
			t.Fatalf("Do returned %v before waiting", err)
		case <-time.After(time.Millisecond):
		}
	}
	clock.Advance(time.Second)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestJSONEncodesAndDecodes(t *testing.T) {
	fake := cloudfake.New(t)
	fake.Handle(http.MethodPost, "/api/items", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusUnsupportedMediaType)
			return
		}
		b, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(bytes.Replace(b, []byte(`"name"`), []byte(`"id":"i1","name"`), 1))
	})
	fake.Handle(http.MethodDelete, "/api/items/i1", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	c, _ := clClient(t, fake, nil)
	var out struct{ ID, Name string }
	res, err := c.JSON(t.Context(), http.MethodPost, "/items", map[string]string{"name": "a"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != http.StatusCreated || out.ID != "i1" || out.Name != "a" {
		t.Fatalf("answer %d, out %+v", res.Status, out)
	}
	if _, err := c.JSON(t.Context(), http.MethodDelete, "/items/i1", nil, nil); err != nil {
		t.Fatal(err)
	}
	if body := fake.Requests()[1].Body; len(body) != 0 {
		t.Fatalf("DELETE sent a body %q", body)
	}
}

func TestNewRefusesABadConfig(t *testing.T) {
	for _, cfg := range []Config{
		{BaseURL: "https://api.example"},
		{Provider: "npm", BaseURL: "ftp://npm.example"},
		{Provider: "npm", BaseURL: "https://npm.example/"},
		{Provider: "npm", BaseURL: "https://npm.example/api?x=1"},
		{Provider: "npm", BaseURL: "npm.example"},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("New(%+v) succeeded", cfg)
		}
	}
}

func TestDefaultClientDoesNotFollowRedirects(t *testing.T) {
	fake := cloudfake.New(t)
	fake.Handle(http.MethodGet, "/api/x", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	})
	fake.Handle(http.MethodGet, "/elsewhere", clOK)
	c, err := New(Config{Provider: "npm", BaseURL: fake.URL() + "/api", Sleep: (&clSleeps{}).sleep})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Do(t.Context(), Request{Method: http.MethodGet, Path: "/x"})
	if Status(err) != http.StatusFound || len(fake.Requests()) != 1 {
		t.Fatalf("err = %v after %d requests, want the 302 itself", err, len(fake.Requests()))
	}
}

func TestStatusWithoutAProviderAnswer(t *testing.T) {
	if Status(nil) != 0 || Status(errors.New("x")) != 0 {
		t.Fatal("Status of an error without a provider answer is not 0")
	}
}

func clEqualDurations(a, b []time.Duration) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSuppliedClientDoesNotFollowRedirects(t *testing.T) {
	fake := cloudfake.New(t)
	fake.Handle(http.MethodGet, "/api/x", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	})
	fake.Handle(http.MethodGet, "/elsewhere", clOK)
	hc := fake.Client()
	c, _ := clClient(t, fake, func(cfg *Config) { cfg.HTTP = hc })
	_, err := c.Do(t.Context(), Request{Method: http.MethodGet, Path: "/x"})
	if Status(err) != http.StatusFound || errcode.From(err).Code != errcode.CloudUnavailable || len(fake.Requests()) != 1 {
		t.Fatalf("err = %v after %d requests, want the 302 itself as cloud.unavailable", err, len(fake.Requests()))
	}
	if hc.CheckRedirect != nil {
		t.Error("New changed the supplied client")
	}
}

func TestDoRefusesPathsThatLeaveTheirPath(t *testing.T) {
	fake := cloudfake.New(t)
	fake.Handle(http.MethodGet, "/api/items/{id}", clOK)
	c, _ := clClient(t, fake, nil)
	for _, path := range []string{"", "items", "/items?x=1", "/items#f", "//evil.example/items", "/items//a",
		"/items/../keys", "/items/./a", "/items/%2e%2e/keys", "/items/%zz"} {
		_, err := c.Do(t.Context(), Request{Method: http.MethodGet, Path: path})
		if err == nil || !core.IsDefinite(err) {
			t.Errorf("Do(%q) = %v, want a definite refusal", path, err)
		}
	}
	if n := len(fake.Requests()); n != 0 {
		t.Fatalf("%d requests sent for refused paths", n)
	}
	if _, err := c.Do(t.Context(), Request{Method: http.MethodGet, Path: "/items/a%2Fb"}); err != nil {
		t.Fatalf("an escaped id was refused: %v", err)
	}
}

func TestOversizedAnswer(t *testing.T) {
	fake := cloudfake.New(t)
	big := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, maxBody+1))
	}
	fake.Handle(http.MethodGet, "/api/x", big)
	fake.Handle(http.MethodPost, "/api/x", big)
	c, sleeps := clClient(t, fake, nil)
	_, err := c.Do(t.Context(), Request{Method: http.MethodGet, Path: "/x"})
	if errcode.From(err).Code != errcode.CloudUnavailable || len(fake.Requests()) != 1 || len(sleeps.got()) != 0 {
		t.Fatalf("GET: err = %v after %d requests and %d sleeps, want cloud.unavailable without a retry",
			err, len(fake.Requests()), len(sleeps.got()))
	}
	_, err = c.Do(t.Context(), Request{Method: http.MethodPost, Path: "/x", Body: []byte(`{}`)})
	if errcode.From(err).Code != errcode.CloudUncertainOutcome || core.IsDefinite(err) {
		t.Fatalf("POST: err = %v, want an uncertain outcome for an accepted write whose answer is too large", err)
	}
}

func TestJSONRefusesAnUnreadableBody(t *testing.T) {
	fake := cloudfake.New(t)
	fake.Handle(http.MethodGet, "/api/bad", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"id":`) })
	fake.Handle(http.MethodGet, "/api/empty", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	fake.Handle(http.MethodGet, "/api/none", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	c, _ := clClient(t, fake, nil)
	for _, path := range []string{"/bad", "/empty"} {
		var out struct{ ID string }
		if _, err := c.JSON(t.Context(), http.MethodGet, path, nil, &out); errcode.From(err).Code != errcode.CloudUnavailable {
			t.Errorf("JSON %s = %v, want cloud.unavailable", path, err)
		}
	}
	out := struct{ ID string }{ID: "kept"}
	if res, err := c.JSON(t.Context(), http.MethodGet, "/none", nil, &out); err != nil || res.Status != http.StatusNoContent || out.ID != "kept" {
		t.Errorf("JSON on 204 = %d, %v, out %+v; want success with out untouched", res.Status, err, out)
	}
}
