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
		{Unavailable, 503},
		{ChangeRevertFailed, 500},
		{HostOffline, 503},
		{HostVersionUnsupported, 409},
		{AgentNotEnrolled, 409},
		{EnrollTokenInvalid, 403},
		{EnrollRateLimited, 429},
		{UpdateInProgress, 409},
		{UpdateUnconfirmedChanges, 409},
		{UpdateDowngradeRefused, 409},
		{UpdateNotFound, 404},
		{UpdateNothingToRollBack, 409},
		{RolloutInProgress, 409},
		{UpdateSignatureInvalid, 500},
		{UpdatePrevMismatch, 500},
		{UpdateInterrupted, 500},
		{ChildFailed, 500},
		{NeedsRepair, 409},
		{Recovering, 409},
		{UndoConflict, 409},
		{ChangeBusy, 409},
		{DeadlineExceeded, 504},
		{Uninstalling, 409},
		{Code("no.such_code"), 500},
	}
	for _, tt := range tests {
		if got := tt.code.Status(); got != tt.want {
			t.Errorf("%s.Status() = %d, want %d", tt.code, got, tt.want)
		}
	}
}

// The values are shared contracts: agents, the CLI and the web UI match on
// them.
func TestFleetCodeValues(t *testing.T) {
	for code, want := range map[Code]string{
		Unavailable:              "unavailable",
		HostOffline:              "host.offline",
		HostVersionUnsupported:   "host.version_unsupported",
		AgentNotEnrolled:         "agent.not_enrolled",
		EnrollTokenInvalid:       "enroll.token_invalid",
		EnrollRateLimited:        "enroll.rate_limited",
		UpdateInProgress:         "update.in_progress",
		UpdateUnconfirmedChanges: "update.unconfirmed_changes",
		UpdateDowngradeRefused:   "update.downgrade_refused",
		UpdateNotFound:           "update.not_found",
		UpdateNothingToRollBack:  "update.nothing_to_roll_back",
		RolloutInProgress:        "rollout.in_progress",
		UpdateSignatureInvalid:   "update.signature_invalid",
		UpdatePrevMismatch:       "update.prev_mismatch",
		UpdateInterrupted:        "update.interrupted",
	} {
		if string(code) != want || !Known(code) {
			t.Errorf("code %q: want value %q registered", code, want)
		}
	}
}

// The lifecycle codes are shared contracts too; the titles are the ones
// problem details and change errors show.
func TestLifecycleCodes(t *testing.T) {
	tests := []struct {
		code         Code
		value, title string
	}{
		{ChildFailed, "core.child_failed", "A composite step failed"},
		{NeedsRepair, "core.needs_repair", "Change needs repair"},
		{Recovering, "core.recovering", "Recovery in progress"},
		{UndoConflict, "core.undo_conflict", "Undo window closed"},
		{ChangeBusy, "core.change_busy", "Change busy"},
		{DeadlineExceeded, "core.deadline_exceeded", "Action deadline exceeded"},
		{Uninstalling, "core.uninstalling", "Host is being uninstalled"},
	}
	for _, tt := range tests {
		if string(tt.code) != tt.value || !Known(tt.code) || tt.code.Title() != tt.title {
			t.Errorf("code %q: known %v, title %q; want value %q registered with title %q",
				tt.code, Known(tt.code), tt.code.Title(), tt.value, tt.title)
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
