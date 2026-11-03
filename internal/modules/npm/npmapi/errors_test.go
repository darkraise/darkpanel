package npmapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestApErrorsCarryNPMsMessageAndNeverTheBody(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		message  string
		notFound bool
		client   bool
	}{
		{"a refused duplicate", 400, `{"error":{"code":400,"message":"app.example.com is already in use"}}`, "app.example.com is already in use", false, true},
		{"a missing object", 404, `{"error":{"code":404,"message":"Not Found"}}`, "Not Found", true, true},
		{"an unauthorized call", 401, `{"error":{"code":401,"message":"Unauthorized"}}`, "Unauthorized", false, true},
		{"a server failure with NPM's message", 500, `{"error":{"code":500,"message":"database is locked"}}`, "database is locked", false, false},
		{"an HTML body from a proxy", 502, `<html>token=leak-hunter2</html>`, "Bad Gateway", false, false},
		{"an empty body", 503, ``, "Service Unavailable", false, false},
		{"JSON without error.message", 400, `{"password":"leak-hunter2"}`, "Bad Request", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := apStart(t, tc.status, tc.body)
			err := s.client().DeleteProxyHost(t.Context(), 3)
			var ae *APIError
			if !errors.As(err, &ae) || ae.Status != tc.status || ae.Message != tc.message {
				t.Fatalf("error %v, want an *APIError with status %d and message %q", err, tc.status, tc.message)
			}
			if IsNotFound(err) != tc.notFound || IsClientError(err) != tc.client {
				t.Errorf("IsNotFound %t and IsClientError %t, want %t and %t", IsNotFound(err), IsClientError(err), tc.notFound, tc.client)
			}
			if strings.Contains(err.Error(), "leak") {
				t.Errorf("error text %q carries the response body", err.Error())
			}
		})
	}
}

func TestApAPIErrorText(t *testing.T) {
	err := &APIError{Status: 400, Message: "app.example.com is already in use"}
	if got, want := err.Error(), "npmapi: NPM answered 400: app.example.com is already in use"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestApPredicatesSeeThroughWrappingAndIgnoreOtherErrors(t *testing.T) {
	wrapped := fmt.Errorf("npm: remove the proxy host: %w", &APIError{Status: 404, Message: "Not Found"})
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
