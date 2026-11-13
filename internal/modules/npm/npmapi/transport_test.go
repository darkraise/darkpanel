package npmapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// w12aRaw starts an NPM that answers every request with raw, the bytes of a whole HTTP answer, so the answer can be
// one net/http itself would never write, and returns its URL.
func w12aRaw(t *testing.T, raw string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = buf.WriteString(raw)
		_ = buf.Flush()
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// w12aClient is a client of base that sees a redirect as an answer and never follows it, as the npm module's is.
func w12aClient(base string) *Client {
	return New(base, &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})
}

// TestW12aFailuresKeepNoAnswerText: a request that ends without a usable answer, or with a 2xx answer that does not
// decode, fails with an error that names the method, the path and a fixed reason, never text NPM sent.
func TestW12aFailuresKeepNoAnswerText(t *testing.T) {
	login := func(ctx context.Context, base string) error {
		_, err := w12aClient(base).Token(ctx, "admin@example.com", "placeholder-password")
		return err
	}
	read := func(ctx context.Context, base string) error {
		_, err := w12aClient(base).ProxyHost(ctx, 1)
		return err
	}
	closed := apStart(t, 200, `{}`)
	closedURL := closed.srv.URL
	closed.srv.Close()
	passed, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	for _, tc := range []struct {
		name   string
		ctx    context.Context
		base   string
		call   func(context.Context, string) error
		secret string // text of the answer the error must not carry; empty when the answer carries none
		want   string
		is     error // a sentinel the error must still match, or nil
	}{
		{"a redirect to a malformed Location", t.Context(),
			w12aRaw(t, "HTTP/1.1 302 Found\r\nLocation: http://[placeholder-secret\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"),
			login, "placeholder-secret", "npmapi: POST /api/tokens: no usable answer", nil},
		{"a malformed status line", t.Context(), w12aRaw(t, "HTTP/1.1 placeholder-secret\r\n\r\n"),
			login, "placeholder-secret", "npmapi: POST /api/tokens: no usable answer", nil},
		{"a header line without a colon", t.Context(), w12aRaw(t, "HTTP/1.1 200 OK\r\nplaceholder-secret\r\n\r\n"),
			login, "placeholder-secret", "npmapi: POST /api/tokens: no usable answer", nil},
		{"a refused connection", t.Context(), closedURL, login, "", "npmapi: POST /api/tokens: connection refused", nil},
		{"a deadline that has passed", passed, closedURL, login, "",
			"npmapi: POST /api/tokens: context deadline exceeded", context.DeadlineExceeded},
		{"a number NPM chose in a 2xx answer", t.Context(),
			w12aRaw(t, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nConnection: close\r\n\r\n{\"id\":31415926535897932384626433}"),
			read, "31415926535897932384626433", "npmapi: decode GET /api/nginx/proxy-hosts/1 answer: not the expected JSON", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call(tc.ctx, tc.base)
			if err == nil {
				t.Fatal("the call succeeded, want an error")
			}
			if got := err.Error(); got != tc.want {
				t.Errorf("error text %q, want %q", got, tc.want)
			}
			var ae *APIError
			if errors.As(err, &ae) {
				t.Errorf("error %v is an *APIError, want a failure that proves nothing about NPM", err)
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Errorf("error %v does not match %v", err, tc.is)
			}
			for e := err; e != nil && tc.secret != ""; e = errors.Unwrap(e) {
				if strings.Contains(e.Error(), tc.secret) {
					t.Errorf("error %q carries the answer text %q", e.Error(), tc.secret)
				}
			}
		})
	}
}
