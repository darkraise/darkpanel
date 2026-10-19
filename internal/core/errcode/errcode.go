// Package errcode is the registry of stable error codes (shared contracts in
// docs/adr/README.md) and the HTTP status each one maps to.
package errcode

import (
	"errors"
	"fmt"
	"net/http"
)

// Code is a stable, machine-readable error code such as "plan.expired".
type Code string

// Codes returned as RFC 9457 problem details before a change exists.
const (
	InvalidJSON            Code = "request.invalid_json"
	InvalidInput           Code = "request.invalid_input"
	TooLarge               Code = "request.too_large"
	UnsupportedMediaType   Code = "request.unsupported_media_type"
	MethodNotAllowed       Code = "method_not_allowed"
	AuthRequired           Code = "auth.required"
	InvalidCredentials     Code = "auth.invalid_credentials" //nolint:gosec // G101: an error code, not a credential
	RateLimited            Code = "auth.rate_limited"
	CSRF                   Code = "auth.csrf"
	Forbidden              Code = "auth.forbidden"
	NotFound               Code = "not_found"
	ActionUnknown          Code = "action.unknown"
	ModuleUnavailable      Code = "module.unavailable"
	PlanNotFound           Code = "plan.not_found"
	PlanExpired            Code = "plan.expired"
	PlanStateChanged       Code = "plan.state_changed"
	PlanPrincipalMismatch  Code = "plan.principal_mismatch"
	PlanConfirmationNeeded Code = "plan.confirmation_required"
	PlanGuardFailed        Code = "plan.guard_failed"
	ChangeNotFound         Code = "change.not_found"
	ChangeNotPending       Code = "change.not_pending"
	LockHeld               Code = "lock.held"
	Internal               Code = "internal"
)

// Action-level codes: they appear in a change's error or in the audit log,
// never as an HTTP status of their own.
const (
	ChangeRevertFailed Code = "change.revert_failed"
	ExecOutputTooLarge Code = "exec.output_too_large"
	FilesMarkersBad    Code = "files.markers_invalid"
	JournalOverflow    Code = "journal.overflow"
)

// Problem codes of the fleet, enrollment, update and rollout.
const (
	HostOffline              Code = "host.offline"
	HostVersionUnsupported   Code = "host.version_unsupported"
	AgentNotEnrolled         Code = "agent.not_enrolled"
	EnrollTokenInvalid       Code = "enroll.token_invalid" //nolint:gosec // G101: an error code, not a credential
	EnrollRateLimited        Code = "enroll.rate_limited"
	UpdateInProgress         Code = "update.in_progress"
	UpdateUnconfirmedChanges Code = "update.unconfirmed_changes"
	UpdateDowngradeRefused   Code = "update.downgrade_refused"
	UpdateNotFound           Code = "update.not_found"
	UpdateNothingToRollBack  Code = "update.nothing_to_roll_back"
	RolloutInProgress        Code = "rollout.in_progress"
)

// Action-level codes of an update: they appear in an update record's error.
const (
	UpdateSignatureInvalid Code = "update.signature_invalid"
	UpdatePrevMismatch     Code = "update.prev_mismatch"
	UpdateInterrupted      Code = "update.interrupted"
)

type info struct {
	status int
	title  string
}

var registry = map[Code]info{
	InvalidJSON:            {http.StatusBadRequest, "Invalid JSON"},
	InvalidInput:           {http.StatusUnprocessableEntity, "Invalid input"},
	TooLarge:               {http.StatusRequestEntityTooLarge, "Request too large"},
	UnsupportedMediaType:   {http.StatusUnsupportedMediaType, "Unsupported media type"},
	MethodNotAllowed:       {http.StatusMethodNotAllowed, "Method not allowed"},
	AuthRequired:           {http.StatusUnauthorized, "Authentication required"},
	InvalidCredentials:     {http.StatusUnauthorized, "Invalid credentials"},
	RateLimited:            {http.StatusTooManyRequests, "Too many attempts"},
	CSRF:                   {http.StatusForbidden, "CSRF token missing or invalid"},
	Forbidden:              {http.StatusForbidden, "Forbidden"},
	NotFound:               {http.StatusNotFound, "Not found"},
	ActionUnknown:          {http.StatusNotFound, "Unknown action"},
	ModuleUnavailable:      {http.StatusConflict, "Module unavailable"},
	PlanNotFound:           {http.StatusNotFound, "Plan not found"},
	PlanExpired:            {http.StatusGone, "Plan expired"},
	PlanStateChanged:       {http.StatusConflict, "State changed since the plan"},
	PlanPrincipalMismatch:  {http.StatusForbidden, "Plan belongs to another principal"},
	PlanConfirmationNeeded: {http.StatusUnprocessableEntity, "Confirmation required"},
	PlanGuardFailed:        {http.StatusConflict, "Guard failed"},
	ChangeNotFound:         {http.StatusNotFound, "Change not found"},
	ChangeNotPending:       {http.StatusConflict, "Change is not pending"},
	LockHeld:               {http.StatusLocked, "Resource locked by another change"},
	Internal:               {http.StatusInternalServerError, "Internal error"},
	ChangeRevertFailed:     {http.StatusInternalServerError, "Revert failed"},
	ExecOutputTooLarge:     {http.StatusInternalServerError, "Command output too large"},
	FilesMarkersBad:        {http.StatusInternalServerError, "Invalid DARKPANEL markers"},
	JournalOverflow:        {http.StatusInternalServerError, "Journal overflow"},

	// A host whose agent has no live session may answer later unchanged.
	HostOffline:              {http.StatusServiceUnavailable, "Host offline"},
	HostVersionUnsupported:   {http.StatusConflict, "Agent version unsupported"},
	AgentNotEnrolled:         {http.StatusConflict, "Agent not enrolled"},
	EnrollTokenInvalid:       {http.StatusForbidden, "Invalid enrollment token"},
	EnrollRateLimited:        {http.StatusTooManyRequests, "Too many enrollment attempts"},
	UpdateInProgress:         {http.StatusConflict, "Update in progress"},
	UpdateUnconfirmedChanges: {http.StatusConflict, "Unconfirmed changes"},
	UpdateDowngradeRefused:   {http.StatusConflict, "Downgrade refused"},
	UpdateNotFound:           {http.StatusNotFound, "Release not found"},
	UpdateNothingToRollBack:  {http.StatusConflict, "Nothing to roll back"},
	RolloutInProgress:        {http.StatusConflict, "Rollout in progress"},
	UpdateSignatureInvalid:   {http.StatusInternalServerError, "Release signature invalid"},
	UpdatePrevMismatch:       {http.StatusInternalServerError, "Previous binary does not match"},
	UpdateInterrupted:        {http.StatusInternalServerError, "Update interrupted"},
}

// Known reports whether c is in the registry.
func Known(c Code) bool {
	_, ok := registry[c]
	return ok
}

// Status is the HTTP status for c; unknown codes map to 500.
func (c Code) Status() int {
	if i, ok := registry[c]; ok {
		return i.status
	}
	return http.StatusInternalServerError
}

// Title is the short human title used as a problem's "title".
func (c Code) Title() string {
	if i, ok := registry[c]; ok {
		return i.title
	}
	return registry[Internal].title
}

// FieldError is one validation failure, reported in a problem's "errors".
type FieldError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Error carries a code to the API boundary. Err is the cause; it is logged but
// never sent to a client.
type Error struct {
	Code   Code
	Detail string
	Fields []FieldError
	Err    error
}

// New returns an error with code and a client-safe detail.
func New(code Code, detail string) *Error {
	return &Error{Code: code, Detail: detail}
}

// Wrap returns an error with code, a client-safe detail and a hidden cause.
func Wrap(code Code, err error, detail string) *Error {
	return &Error{Code: code, Detail: detail, Err: err}
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Detail, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Detail)
}

func (e *Error) Unwrap() error { return e.Err }

// From returns the *Error in err's chain, or an Internal error wrapping err.
func From(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return Wrap(Internal, err, "")
}
