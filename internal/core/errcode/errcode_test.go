package errcode

import (
	"errors"
	"fmt"
	"testing"
)

func TestStatus(t *testing.T) {
	tests := []struct {
		code Code
		want int
	}{
		{InvalidJSON, 400},
		{InvalidInput, 422},
		{TooLarge, 413},
		{UnsupportedMediaType, 415},
		{MethodNotAllowed, 405},
		{AuthRequired, 401},
		{InvalidCredentials, 401},
		{RateLimited, 429},
		{CSRF, 403},
		{Forbidden, 403},
		{NotFound, 404},
		{ActionUnknown, 404},
		{ModuleUnavailable, 409},
		{PlanNotFound, 404},
		{PlanExpired, 410},
		{PlanStateChanged, 409},
		{PlanPrincipalMismatch, 403},
		{PlanConfirmationNeeded, 422},
		{PlanGuardFailed, 409},
		{ChangeNotFound, 404},
		{ChangeNotPending, 409},
		{LockHeld, 423},
		{Internal, 500},
		{ChangeRevertFailed, 500},
		{Code("no.such_code"), 500},
	}
	for _, tt := range tests {
		if got := tt.code.Status(); got != tt.want {
			t.Errorf("%s.Status() = %d, want %d", tt.code, got, tt.want)
		}
	}
}

func TestEveryCodeHasATitle(t *testing.T) {
	for code, i := range registry {
		if i.title == "" {
			t.Errorf("%s has no title", code)
		}
	}
	if Code("no.such_code").Title() != "Internal error" {
		t.Errorf("unknown code title = %q", Code("no.such_code").Title())
	}
}

func TestFrom(t *testing.T) {
	coded := New(PlanExpired, "plan 01J expired")
	if got := From(fmt.Errorf("apply: %w", coded)); got != coded {
		t.Errorf("From(wrapped) = %v, want the original *Error", got)
	}
	cause := errors.New("disk full")
	got := From(cause)
	if got.Code != Internal || !errors.Is(got, cause) || got.Detail != "" {
		t.Errorf("From(plain) = %+v, want Internal wrapping the cause with no detail", got)
	}
}
