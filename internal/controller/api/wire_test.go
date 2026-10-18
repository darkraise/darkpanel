package api

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// testLog returns a logger writing text lines into the returned buffer.
func testLog() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

// decodeProblem checks that rec holds a problem with status and code and
// returns it.
func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code errcode.Code) problem {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}
	var p problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("problem is not JSON: %v; body %s", err, rec.Body)
	}
	if p.Code != code || p.Status != status || p.Type != "about:blank" || p.Title != code.Title() {
		t.Fatalf("problem = %+v, want code %s status %d", p, code, status)
	}
	return p
}

func TestWriteProblemStatuses(t *testing.T) {
	for code, status := range map[errcode.Code]int{
		errcode.InvalidJSON:            400,
		errcode.InvalidInput:           422,
		errcode.TooLarge:               413,
		errcode.UnsupportedMediaType:   415,
		errcode.MethodNotAllowed:       405,
		errcode.AuthRequired:           401,
		errcode.InvalidCredentials:     401,
		errcode.RateLimited:            429,
		errcode.CSRF:                   403,
		errcode.Forbidden:              403,
		errcode.NotFound:               404,
		errcode.ActionUnknown:          404,
		errcode.ModuleUnavailable:      409,
		errcode.PlanNotFound:           404,
		errcode.PlanExpired:            410,
		errcode.PlanStateChanged:       409,
		errcode.PlanPrincipalMismatch:  403,
		errcode.PlanConfirmationNeeded: 422,
		errcode.PlanGuardFailed:        409,
		errcode.ChangeNotFound:         404,
		errcode.ChangeNotPending:       409,
		errcode.LockHeld:               423,
		errcode.Internal:               500,
	} {
		log, _ := testLog()
		rec := httptest.NewRecorder()
		writeProblem(rec, log, errcode.New(code, "the detail"))
		p := decodeProblem(t, rec, status, code)
		want := "the detail"
		if status == 500 {
			want = code.Title()
		}
		if p.Detail != want {
			t.Errorf("%s: detail = %q, want %q", code, p.Detail, want)
		}
	}
}

func TestWriteProblemHidesInternalText(t *testing.T) {
	cause := errors.New("open /var/lib/darkpanel/controller.db: hunter2")
	for name, err := range map[string]error{
		"plain error":   cause,
		"wrapped":       errcode.Wrap(errcode.Internal, cause, "detail with hunter2"),
		"unknown code":  errcode.Wrap("made.up", cause, "hunter2"),
		"action code":   errcode.Wrap(errcode.ChangeRevertFailed, cause, "hunter2"),
		"with a field":  &errcode.Error{Code: errcode.Internal, Fields: []errcode.FieldError{{Path: "/x", Message: "hunter2"}}, Err: cause},
		"empty detail ": errcode.New(errcode.Internal, ""),
	} {
		log, logged := testLog()
		rec := httptest.NewRecorder()
		writeProblem(rec, log, err)
		if rec.Code != 500 {
			t.Fatalf("%s: status = %d, want 500", name, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "hunter2") {
			t.Errorf("%s: body leaks the cause: %s", name, rec.Body)
		}
		var p problem
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		if p.Detail == "" || p.Errors != nil {
			t.Errorf("%s: problem = %+v, want a title detail and no errors", name, p)
		}
		if name != "empty detail " && !strings.Contains(logged.String(), "hunter2") {
			t.Errorf("%s: the cause was not logged: %s", name, logged)
		}
	}
}

func TestWriteProblemFields(t *testing.T) {
	log, _ := testLog()
	rec := httptest.NewRecorder()
	writeProblem(rec, log, &errcode.Error{Code: errcode.InvalidInput, Detail: "bad",
		Fields: []errcode.FieldError{{Path: "/name", Message: "required"}}})
	p := decodeProblem(t, rec, 422, errcode.InvalidInput)
	if len(p.Errors) != 1 || p.Errors[0].Path != "/name" || p.Errors[0].Message != "required" {
		t.Errorf("errors = %+v", p.Errors)
	}

	rec = httptest.NewRecorder()
	writeProblem(rec, log, errcode.New(errcode.NotFound, "no"))
	if strings.Contains(rec.Body.String(), `"errors"`) {
		t.Errorf("a problem without field errors has an errors member: %s", rec.Body)
	}
}

func TestWriteProblemRetryAfter(t *testing.T) {
	log, _ := testLog()
	rec := httptest.NewRecorder()
	err := errcode.Wrap(errcode.RateLimited, &auth.RetryAfterError{After: 90*time.Second + time.Millisecond}, "slow down")
	writeProblem(rec, log, err)
	decodeProblem(t, rec, 429, errcode.RateLimited)
	if got := rec.Header().Get("Retry-After"); got != "91" {
		t.Errorf("Retry-After = %q, want 91", got)
	}

	rec = httptest.NewRecorder()
	writeProblem(rec, log, errcode.New(errcode.NotFound, "no"))
	if got := rec.Header().Get("Retry-After"); got != "" {
		t.Errorf("Retry-After on not_found = %q", got)
	}
}

func TestWriteJSON(t *testing.T) {
	log, _ := testLog()
	rec := httptest.NewRecorder()
	writeJSON(rec, log, http.StatusCreated, map[string]int{"n": 1})
	if rec.Code != 201 || rec.Header().Get("Content-Type") != "application/json" || rec.Body.String() != `{"n":1}` {
		t.Errorf("got %d %q %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}

	rec = httptest.NewRecorder()
	writeJSON(rec, log, http.StatusOK, struct {
		Output json.RawMessage `json:"output"`
	}{json.RawMessage(`{not json`)})
	decodeProblem(t, rec, 500, errcode.Internal)
}

type decodeTarget struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestDecodeBody(t *testing.T) {
	big := `{"name":"` + strings.Repeat("a", MaxBodyBytes) + `"}`
	for _, tc := range []struct {
		name, contentType, body string
		code                    errcode.Code // "" means success
		detail                  string
	}{
		{"valid", "application/json", `{"name":"x","count":2}`, "", ""},
		{"charset parameter", "application/json; charset=utf-8", `{"name":"x"}`, "", ""},
		{"trailing space", "application/json", "{\"name\":\"x\"}\n ", "", ""},
		{"no content type", "", `{"name":"x"}`, errcode.UnsupportedMediaType, "application/json"},
		{"text", "text/plain", `{"name":"x"}`, errcode.UnsupportedMediaType, "application/json"},
		{"too large", "application/json", big, errcode.TooLarge, "1048576"},
		{"empty", "application/json", "", errcode.InvalidJSON, "empty"},
		{"truncated", "application/json", `{"name":`, errcode.InvalidJSON, "ends early"},
		{"malformed", "application/json", `{"name" "x"}`, errcode.InvalidJSON, "byte"},
		{"unknown field", "application/json", `{"name":"x","extra":1}`, errcode.InvalidJSON, `unknown field "extra"`},
		{"wrong type", "application/json", `{"count":"two"}`, errcode.InvalidJSON, `"count"`},
		{"not an object", "application/json", `[1]`, errcode.InvalidJSON, "JSON object"},
		{"two values", "application/json", `{"name":"x"}{"name":"y"}`, errcode.InvalidJSON, "more than one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/x", strings.NewReader(tc.body))
			if tc.contentType != "" {
				r.Header.Set("Content-Type", tc.contentType)
			}
			var dst decodeTarget
			err := decodeBody(httptest.NewRecorder(), r, &dst)
			if tc.code == "" {
				if err != nil || dst.Name != "x" {
					t.Fatalf("err = %v, dst = %+v", err, dst)
				}
				return
			}
			var e *errcode.Error
			if !errors.As(err, &e) || e.Code != tc.code {
				t.Fatalf("err = %v, want code %s", err, tc.code)
			}
			if !strings.Contains(e.Detail, tc.detail) {
				t.Errorf("detail = %q, want it to contain %q", e.Detail, tc.detail)
			}
		})
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, tc := range []struct {
		path    string
		tls     bool
		noStore bool
	}{
		{"/api/v1/changes", false, true},
		{"/api", true, true},
		{"/healthz", false, true},
		{"/assets/app.js", true, false},
		{"/", false, false},
	} {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		if tc.tls {
			r.TLS = &tls.ConnectionState{}
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		got := rec.Header()
		want := map[string]string{
			"Content-Security-Policy":    "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
			"X-Content-Type-Options":     "nosniff",
			"Referrer-Policy":            "no-referrer",
			"Cross-Origin-Opener-Policy": "same-origin",
		}
		for k, v := range want {
			if got.Get(k) != v {
				t.Errorf("%s: %s = %q, want %q", tc.path, k, got.Get(k), v)
			}
		}
		if hsts := got.Get("Strict-Transport-Security"); (hsts == "max-age=31536000") != tc.tls || (!tc.tls && hsts != "") {
			t.Errorf("%s (tls %v): Strict-Transport-Security = %q", tc.path, tc.tls, hsts)
		}
		if cc := got.Get("Cache-Control"); (cc == "no-store") != tc.noStore {
			t.Errorf("%s: Cache-Control = %q, want no-store %v", tc.path, cc, tc.noStore)
		}
	}
}

func TestRecoverPanics(t *testing.T) {
	log, logged := testLog()
	h := recoverPanics(log, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom in a handler")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/actions/demo.item.remove/v1/apply", nil))
	p := decodeProblem(t, rec, 500, errcode.Internal)
	if strings.Contains(rec.Body.String(), "boom") || p.Detail != errcode.Internal.Title() {
		t.Errorf("the panic value reached the client: %s", rec.Body)
	}
	for _, want := range []string{"boom in a handler", "goroutine", "/api/v1/actions/demo.item.remove/v1/apply"} {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("log lacks %q: %s", want, logged)
		}
	}
}

func TestRecoverPanicsAfterWriteAborts(t *testing.T) {
	log, logged := testLog()
	h := recoverPanics(log, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		panic("late boom")
	}))
	defer func() {
		v := recover()
		if err, ok := v.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
			t.Errorf("recovered %v, want http.ErrAbortHandler", v)
		}
		if !strings.Contains(logged.String(), "late boom") {
			t.Errorf("log lacks the panic: %s", logged)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/changes", nil))
}

func TestRecoverPanicsPassesAbort(t *testing.T) {
	log, logged := testLog()
	h := recoverPanics(log, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		v := recover()
		if err, ok := v.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
			t.Errorf("recovered %v, want http.ErrAbortHandler", v)
		}
		if logged.Len() != 0 {
			t.Errorf("an abort was logged: %s", logged)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}
