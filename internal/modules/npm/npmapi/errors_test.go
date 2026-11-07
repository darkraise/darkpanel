package npmapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestV1r11AnswersKeepOnlyTheStatusAndAnAllowlistedCode(t *testing.T) {
	const secret = "placeholder-secret"
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		code     int
		text     string
		notFound bool
		client   bool
	}{
		{"a refused duplicate", 400, `{"error":{"code":400,"message":"placeholder-secret is already in use"}}`, 400, "npmapi: NPM answered 400", false, true},
		{"a missing object", 404, `{"error":{"code":404,"message":"placeholder-secret"}}`, 404, "npmapi: NPM answered 404", true, true},
		{"an unauthorized call", 401, `{"error":{"code":401,"message":"placeholder-secret"}}`, 401, "npmapi: NPM answered 401", false, true},
		{"a server failure", 500, `{"error":{"code":500,"message":"placeholder-secret"}}`, 500, "npmapi: NPM answered 500", false, false},
		{"a code that differs from the status", 403, `{"error":{"code":400,"message":"placeholder-secret"}}`, 400, "npmapi: NPM answered 403 (error code 400)", false, true},
		{"a code outside the allowlist", 400, `{"error":{"code":12345,"message":"placeholder-secret"}}`, 0, "npmapi: NPM answered 400", false, true},
		{"a code below the allowlist", 400, `{"error":{"code":200,"message":"placeholder-secret"}}`, 0, "npmapi: NPM answered 400", false, true},
		{"a code that is text", 400, `{"error":{"code":"placeholder-secret","message":"x"}}`, 0, "npmapi: NPM answered 400", false, true},
		{"an HTML body from a proxy", 502, `<html>token=placeholder-secret</html>`, 0, "npmapi: NPM answered 502", false, false},
		{"an empty body", 503, ``, 0, "npmapi: NPM answered 503", false, false},
		{"JSON without an error member", 400, `{"password":"placeholder-secret"}`, 0, "npmapi: NPM answered 400", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := apStart(t, tc.status, tc.body)
			err := s.client().DeleteProxyHost(t.Context(), 3)
			var ae *APIError
			if !errors.As(err, &ae) || ae.Status != tc.status || ae.Code != tc.code {
				t.Fatalf("error %#v, want an *APIError with status %d and code %d", err, tc.status, tc.code)
			}
			if got := err.Error(); got != tc.text {
				t.Errorf("error text %q, want %q", got, tc.text)
			}
			for _, shown := range []string{err.Error(), fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
				if strings.Contains(shown, secret) {
					t.Errorf("%q carries the answer text", shown)
				}
			}
			if IsNotFound(err) != tc.notFound || IsClientError(err) != tc.client {
				t.Errorf("IsNotFound %t and IsClientError %t, want %t and %t", IsNotFound(err), IsClientError(err), tc.notFound, tc.client)
			}
		})
	}
}

func TestV1r11APIErrorText(t *testing.T) {
	for _, tc := range []struct {
		err  APIError
		want string
	}{
		{APIError{Status: 400}, "npmapi: NPM answered 400"},
		{APIError{Status: 500, Code: 500}, "npmapi: NPM answered 500"},
		{APIError{Status: 403, Code: 400}, "npmapi: NPM answered 403 (error code 400)"},
	} {
		if got := tc.err.Error(); got != tc.want {
			t.Errorf("%+v: Error() = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestApPredicatesSeeThroughWrappingAndIgnoreOtherErrors(t *testing.T) {
	wrapped := fmt.Errorf("npm: remove the proxy host: %w", &APIError{Status: 404})
	if !IsNotFound(wrapped) || !IsClientError(wrapped) {
		t.Error("a wrapped 404 is not recognised")
	}
	for _, err := range []error{nil, errors.New("boom"), &APIError{Status: 399}, &APIError{Status: 500}} {
		if IsNotFound(err) || IsClientError(err) {
			t.Errorf("%v was taken for a client error", err)
		}
	}
	if !IsClientError(&APIError{Status: 400}) || !IsClientError(&APIError{Status: 499}) {
		t.Error("the client-error range is not 400 to 499")
	}
}

func TestApTransportAndDecodeFailuresAreNotAPIErrors(t *testing.T) {
	closed := apStart(t, 200, `{}`)
	c := closed.client()
	closed.srv.Close()
	_, err := c.ProxyHosts(t.Context())
	var ae *APIError
	if err == nil || errors.As(err, &ae) || IsClientError(err) || IsNotFound(err) || !strings.HasPrefix(err.Error(), "npmapi: ") {
		t.Errorf("a closed server gave %v, want an npmapi: transport error that proves nothing about NPM", err)
	}

	garbled := apStart(t, 200, `{"id":`)
	if _, err := garbled.client().ProxyHost(t.Context(), 1); err == nil || errors.As(err, &ae) || IsClientError(err) {
		t.Errorf("a truncated answer gave %v, want a decode error that is not an API error", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	live := apStart(t, 200, `[]`)
	if _, err := live.client().ProxyHosts(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled context gave %v, want context.Canceled", err)
	}
}
