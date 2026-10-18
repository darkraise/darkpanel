// Package api serves the controller's HTTP API (spec section 12) on the
// network listeners and the break-glass routes on the controller socket
// (spec section 14).
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// MaxBodyBytes is the largest request body the API reads (spec section 12).
const MaxBodyBytes = 1 << 20

const problemContentType = "application/problem+json"

// problem is an RFC 9457 problem details body (spec section 12).
type problem struct {
	Type   string               `json:"type"`
	Title  string               `json:"title"`
	Status int                  `json:"status"`
	Detail string               `json:"detail"`
	Code   errcode.Code         `json:"code"`
	Errors []errcode.FieldError `json:"errors,omitempty"`
}

// writeProblem answers err as problem details. A code outside the registry
// becomes internal. A 5xx answer carries only the code's title and no field
// errors, because neither the detail nor the cause of an internal failure is
// client-safe; the cause is logged instead.
func writeProblem(w http.ResponseWriter, log *slog.Logger, err error) {
	e := errcode.From(err)
	code := e.Code
	if !errcode.Known(code) {
		code = errcode.Internal
	}
	status := code.Status()
	p := problem{Type: "about:blank", Title: code.Title(), Status: status, Detail: e.Detail, Code: code, Errors: e.Fields}
	if status >= http.StatusInternalServerError {
		p.Detail, p.Errors = code.Title(), nil
		log.Error("api: request failed", "code", e.Code, "err", err)
	}
	if p.Detail == "" {
		p.Detail = code.Title()
	}
	var ra *auth.RetryAfterError
	if code == errcode.RateLimited && errors.As(err, &ra) {
		w.Header().Set("Retry-After", strconv.Itoa(ra.Seconds()))
	}
	b, merr := json.Marshal(p)
	if merr != nil {
		panic(fmt.Sprintf("api: marshal problem: %v", merr)) // strings, ints and field errors always marshal
	}
	w.Header().Set("Content-Type", problemContentType)
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// writeJSON answers v as JSON with status. A value that does not marshal,
// such as invalid raw output, is answered as internal.
func writeJSON(w http.ResponseWriter, log *slog.Logger, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		writeProblem(w, log, fmt.Errorf("api: marshal response: %w", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// decodeBody reads one JSON object into dst. The body must be sent as
// application/json, hold at most MaxBodyBytes, name no field dst lacks and
// hold nothing after the object.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errcode.New(errcode.UnsupportedMediaType, "send the request body with Content-Type: application/json")
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return decodeError(err)
		}
		return errcode.New(errcode.InvalidJSON, "the request body holds more than one JSON value")
	}
	return nil
}

func decodeError(err error) error {
	var (
		tooLarge  *http.MaxBytesError
		syntax    *json.SyntaxError
		wrongType *json.UnmarshalTypeError
	)
	detail := "the request body is not valid JSON"
	switch {
	case errors.As(err, &tooLarge):
		return errcode.New(errcode.TooLarge, fmt.Sprintf("the request body exceeds %d bytes", MaxBodyBytes))
	case errors.Is(err, io.EOF):
		detail = "the request body is empty"
	case errors.Is(err, io.ErrUnexpectedEOF):
		detail = "the request body ends early"
	case errors.As(err, &syntax):
		detail = fmt.Sprintf("malformed JSON at byte %d", syntax.Offset)
	case errors.As(err, &wrongType) && wrongType.Field == "":
		detail = "the request body must be a JSON object"
	case errors.As(err, &wrongType):
		detail = fmt.Sprintf("field %q has the wrong type", wrongType.Field)
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		detail = strings.TrimPrefix(err.Error(), "json: ")
	}
	return errcode.New(errcode.InvalidJSON, detail)
}
