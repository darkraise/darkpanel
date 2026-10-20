package fleet

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const problemContentType = "application/problem+json"

// problem is an RFC 9457 problem details body, the shape every Darkpanel
// API answers a refusal with.
type problem struct {
	Type   string               `json:"type"`
	Title  string               `json:"title"`
	Status int                  `json:"status"`
	Detail string               `json:"detail"`
	Code   errcode.Code         `json:"code"`
	Errors []errcode.FieldError `json:"errors,omitempty"`
}

// writeProblem answers err as problem details; every refusal of the agent
// listener goes through it. A client sees only the code's fixed detail,
// never err.Error(): a code outside the registry becomes internal, and a
// 5xx answer carries only the code's title because neither its detail nor
// its cause is client-safe. The cause is logged instead.
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
		log.Error("fleet: request failed", "code", e.Code, "err", err)
	}
	if p.Detail == "" {
		p.Detail = code.Title()
	}
	b, merr := json.Marshal(p)
	if merr != nil {
		panic(fmt.Sprintf("fleet: marshal problem: %v", merr)) // strings, ints and field errors always marshal
	}
	w.Header().Set("Content-Type", problemContentType)
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// writeJSON answers v as JSON with status.
func writeJSON(w http.ResponseWriter, log *slog.Logger, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		writeProblem(w, log, fmt.Errorf("fleet: marshal response: %w", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// decodeBody reads one JSON object of at most wire.MaxBody bytes, sent as
// application/json, into dst. Unknown members are ignored: an agent one
// minor newer may send members this controller does not know.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errcode.New(errcode.UnsupportedMediaType, "send the request body with Content-Type: application/json")
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, wire.MaxBody))
	err = dec.Decode(dst)
	if err == nil {
		if _, extra := dec.Token(); !errors.Is(extra, io.EOF) {
			err = errors.New("fleet: data after the JSON object")
		}
	}
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		return errcode.New(errcode.TooLarge, fmt.Sprintf("the request body exceeds %d bytes", wire.MaxBody))
	case err != nil:
		return errcode.Wrap(errcode.InvalidJSON, err, "the request body must be one JSON object")
	}
	return nil
}
