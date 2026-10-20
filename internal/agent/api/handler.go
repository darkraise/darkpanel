// Package api is the agent API (ADR 0012): the HTTP handler the agent serves
// on transport streams and on its socket, and the core.AgentClient that
// calls it over either.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"mime"
	"net/http"
	"slices"
	"strconv"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// prefix is the path every agent API route starts with.
const prefix = "/agent/v1/"

const problemContentType = "application/problem+json"

// The operations of the agent API, one per route.
const (
	opHello     = "hello"
	opRead      = "read"
	opResource  = "resource"
	opPlan      = "plan"
	opApply     = "apply"
	opConfirm   = "confirm"
	opRevert    = "revert"
	opChange    = "change"
	opChanges   = "changes"
	opPreflight = "preflight"
	opJournal   = "journal"
	opAck       = "ack"
)

// ControllerHello is the controller's half of a hello, sent in the headers
// of GET hello.
type ControllerHello struct {
	Protocol     int
	Version      string
	ControllerID string
}

// Config is what the handler serves.
type Config struct {
	Agent core.AgentClient // the agent runtime
	Log   *slog.Logger
	// OnHello receives the controller's half of each hello on a transport
	// stream; nil ignores it.
	OnHello func(ControllerHello)
}

// handler serves the agent API. Its three functions are what differs
// between a transport stream, whose peer is the authenticated controller,
// and the agent socket.
type handler struct {
	cfg Config
	// principal returns the caller of an operation that acts for one.
	principal func(r *http.Request) (core.Principal, error)
	// allow refuses an operation before it reaches the agent; name is the
	// action or resource name, empty for the other operations.
	allow func(ctx context.Context, op, name string) error
	// hello checks the controller's half of a hello.
	hello func(r *http.Request) error
}

// NewHandler serves the agent API on transport streams: the caller is the
// principal the controller names in the Darkpanel-Principal header.
func NewHandler(cfg Config) http.Handler {
	h := &handler{cfg: cfg}
	h.principal = headerPrincipal
	h.allow = func(context.Context, string, string) error { return nil }
	h.hello = h.controllerHello
	return h.routes(true)
}

// routes maps every route; the journal routes only when journal is set.
func (h *handler) routes(journal bool) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+prefix+"hello", h.handleHello)
	mux.HandleFunc("POST "+prefix+"actions/{name}/{version}/read", h.handleRead)
	mux.HandleFunc("GET "+prefix+"resources/{module}/{resource}", h.handleResource)
	mux.HandleFunc("POST "+prefix+"actions/{name}/{version}/plan", h.handlePlan)
	mux.HandleFunc("POST "+prefix+"plans/{id}/apply", h.handleApply)
	mux.HandleFunc("POST "+prefix+"changes/{id}/confirm", h.handleChangeOp(opConfirm))
	mux.HandleFunc("POST "+prefix+"changes/{id}/revert", h.handleChangeOp(opRevert))
	mux.HandleFunc("GET "+prefix+"changes/{id}", h.handleChange)
	mux.HandleFunc("GET "+prefix+"changes", h.handleChanges)
	mux.HandleFunc("GET "+prefix+"modules/{id}/preflight", h.handlePreflight)
	if journal {
		mux.HandleFunc("GET "+prefix+"journal", h.handleJournal)
		mux.HandleFunc("POST "+prefix+"journal/ack", h.handleAck)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		h.writeProblem(w, errcode.New(errcode.NotFound, fmt.Sprintf("no agent API route %s %s", r.Method, r.URL.Path)))
	})
	return mux
}

// headerPrincipal reads the principal the controller sends.
func headerPrincipal(r *http.Request) (core.Principal, error) {
	raw := r.Header.Get(wire.HeaderPrincipal)
	if raw == "" {
		return core.Principal{}, errcode.New(errcode.InvalidInput, "the "+wire.HeaderPrincipal+" header is required")
	}
	var p wire.Principal
	if err := json.Unmarshal([]byte(raw), &p); err != nil || p.Actor == "" {
		return core.Principal{}, errcode.New(errcode.InvalidInput, "the "+wire.HeaderPrincipal+" header is not a principal")
	}
	return p.Core(), nil
}

// controllerHello reads the controller's hello headers and hands them to
// OnHello.
func (h *handler) controllerHello(r *http.Request) error {
	protocol, err := strconv.Atoi(r.Header.Get(wire.HeaderProtocol))
	c := ControllerHello{Protocol: protocol, Version: r.Header.Get(wire.HeaderVersion), ControllerID: r.Header.Get(wire.HeaderControllerID)}
	if err != nil || c.Protocol < 1 || c.Version == "" || c.ControllerID == "" {
		return errcode.New(errcode.InvalidInput, fmt.Sprintf("hello needs the headers %s (a positive integer), %s and %s",
			wire.HeaderProtocol, wire.HeaderVersion, wire.HeaderControllerID))
	}
	if h.cfg.OnHello != nil {
		h.cfg.OnHello(c)
	}
	return nil
}

// actionRef reads an action route's {name} and {version}.
func actionRef(r *http.Request) (core.ActionRef, error) {
	name, version := r.PathValue("name"), r.PathValue("version")
	v, err := core.ParseVersion(version)
	if err != nil {
		return core.ActionRef{}, errcode.New(errcode.ActionUnknown, fmt.Sprintf("no action %s/%s", name, version))
	}
	return core.ActionRef{Name: name, Version: v}, nil
}

// actor runs the checks of an operation that acts for a principal: the
// principal, then allow.
func (h *handler) actor(r *http.Request, op, name string) (core.Principal, error) {
	p, err := h.principal(r)
	if err != nil {
		return core.Principal{}, err
	}
	return p, h.allow(r.Context(), op, name)
}

func (h *handler) handleHello(w http.ResponseWriter, r *http.Request) {
	if err := h.hello(r); err != nil {
		h.writeProblem(w, err)
		return
	}
	hello, err := h.cfg.Agent.Hello(r.Context())
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, wire.FromHello(hello))
}

func (h *handler) handleRead(w http.ResponseWriter, r *http.Request) {
	ref, err := actionRef(r)
	var p core.Principal
	if err == nil {
		p, err = h.actor(r, opRead, ref.Name)
	}
	var input json.RawMessage
	if err == nil {
		input, err = readJSON(w, r)
	}
	var out json.RawMessage
	if err == nil {
		out, err = h.cfg.Agent.Read(r.Context(), p, ref, input)
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	h.writeRaw(w, out)
}

func (h *handler) handleResource(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("module") + "." + r.PathValue("resource")
	p, err := h.actor(r, opResource, name)
	var out json.RawMessage
	if err == nil {
		out, err = h.cfg.Agent.Resource(r.Context(), p, name)
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	h.writeRaw(w, out)
}

func (h *handler) handlePlan(w http.ResponseWriter, r *http.Request) {
	ref, err := actionRef(r)
	var p core.Principal
	if err == nil {
		p, err = h.actor(r, opPlan, ref.Name)
	}
	var input json.RawMessage
	if err == nil {
		input, err = readJSON(w, r)
	}
	var v core.PlanView
	if err == nil {
		v, err = h.cfg.Agent.Plan(r.Context(), p, ref, input)
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, wire.FromPlanView(v))
}

func (h *handler) handleApply(w http.ResponseWriter, r *http.Request) {
	p, err := h.actor(r, opApply, "")
	var opts wire.ApplyOptions
	if err == nil {
		err = decodeJSON(w, r, &opts)
	}
	var v core.ChangeView
	if err == nil {
		v, err = h.cfg.Agent.Apply(r.Context(), p, r.PathValue("id"), opts.Core())
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, wire.FromChangeView(v))
}

// handleChangeOp serves confirm and revert, which take no body.
func (h *handler) handleChangeOp(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := h.actor(r, op, "")
		var v core.ChangeView
		if err == nil {
			if op == opConfirm {
				v, err = h.cfg.Agent.Confirm(r.Context(), p, r.PathValue("id"))
			} else {
				v, err = h.cfg.Agent.Revert(r.Context(), p, r.PathValue("id"))
			}
		}
		if err != nil {
			h.writeProblem(w, err)
			return
		}
		h.writeJSON(w, http.StatusOK, wire.FromChangeView(v))
	}
}

func (h *handler) handleChange(w http.ResponseWriter, r *http.Request) {
	err := h.allow(r.Context(), opChange, "")
	var v core.ChangeView
	if err == nil {
		v, err = h.cfg.Agent.Change(r.Context(), r.PathValue("id"))
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, wire.FromChangeView(v))
}

func (h *handler) handleChanges(w http.ResponseWriter, r *http.Request) {
	err := h.allow(r.Context(), opChanges, "")
	var f core.ChangeFilter
	if err == nil {
		f, err = wire.ParseChangeFilter(r.URL.Query())
	}
	var views []core.ChangeView
	if err == nil {
		views, err = h.cfg.Agent.Changes(r.Context(), f)
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	out := make([]wire.ChangeView, len(views))
	for i, v := range views {
		out[i] = wire.FromChangeView(v)
	}
	h.writeJSON(w, http.StatusOK, out)
}

func (h *handler) handlePreflight(w http.ResponseWriter, r *http.Request) {
	err := h.allow(r.Context(), opPreflight, "")
	var st core.ModuleStatus
	if err == nil {
		st, err = h.cfg.Agent.Preflight(r.Context(), r.PathValue("id"))
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, wire.FromModuleStatus(st))
}

// journalQuery reads after (a sequence, 0 when absent) and limit (a
// positive count, 0 when absent: the agent's default) of GET journal.
func journalQuery(r *http.Request) (after int64, limit int, err error) {
	q := r.URL.Query()
	for _, k := range slices.Sorted(maps.Keys(q)) {
		if (k != "after" && k != "limit") || len(q[k]) > 1 {
			return 0, 0, invalidQuery(k, "unknown or repeated parameter")
		}
	}
	if s := q.Get("after"); s != "" {
		if after, err = strconv.ParseInt(s, 10, 64); err != nil || after < 0 {
			return 0, 0, invalidQuery("after", "must be a sequence number, 0 or more")
		}
	}
	if s := q.Get("limit"); s != "" {
		if limit, err = strconv.Atoi(s); err != nil || limit < 1 {
			return 0, 0, invalidQuery("limit", "must be a positive integer")
		}
	}
	return after, limit, nil
}

func invalidQuery(param, msg string) error {
	return &errcode.Error{Code: errcode.InvalidInput, Detail: fmt.Sprintf("query parameter %s: %s", param, msg),
		Fields: []errcode.FieldError{{Path: "/" + param, Message: msg}}}
}

func (h *handler) handleJournal(w http.ResponseWriter, r *http.Request) {
	after, limit, err := journalQuery(r)
	if err == nil {
		err = h.allow(r.Context(), opJournal, "")
	}
	var entries []core.JournalEntry
	if err == nil {
		entries, err = h.cfg.Agent.Journal(r.Context(), after, limit)
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	out := make([]wire.JournalEntry, len(entries))
	for i, e := range entries {
		out[i] = wire.FromJournalEntry(e)
	}
	h.writeJSON(w, http.StatusOK, out)
}

func (h *handler) handleAck(w http.ResponseWriter, r *http.Request) {
	err := h.allow(r.Context(), opAck, "")
	var ack wire.Ack
	if err == nil {
		err = decodeJSON(w, r, &ack)
	}
	if err == nil && ack.Seq < 0 {
		err = &errcode.Error{Code: errcode.InvalidInput, Detail: "seq must be 0 or more",
			Fields: []errcode.FieldError{{Path: "/seq", Message: "must be 0 or more"}}}
	}
	if err == nil {
		err = h.cfg.Agent.Ack(r.Context(), ack.Seq)
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// readJSON reads a request body of one JSON value, sent as application/json
// and at most wire.MaxBody bytes.
func readJSON(w http.ResponseWriter, r *http.Request) (json.RawMessage, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return nil, errcode.New(errcode.UnsupportedMediaType, "send the request body with Content-Type: application/json")
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, wire.MaxBody))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		return nil, errcode.New(errcode.TooLarge, fmt.Sprintf("the request body exceeds %d bytes", wire.MaxBody))
	case err != nil:
		return nil, errcode.Wrap(errcode.InvalidJSON, err, "the request body could not be read")
	case len(body) == 0:
		return nil, errcode.New(errcode.InvalidJSON, "the request body is empty")
	case !json.Valid(body):
		return nil, errcode.New(errcode.InvalidJSON, "the request body is not one JSON value")
	}
	return body, nil
}

// decodeJSON reads a body into dst. Unknown members are ignored, so a
// controller one minor newer may send members this agent does not know.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	body, err := readJSON(w, r)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return errcode.New(errcode.InvalidJSON, "the request body does not have the expected members and types")
	}
	return nil
}

// problem is an RFC 9457 problem details body, the shape the controller's
// API answers.
type problem struct {
	Type   string               `json:"type"`
	Title  string               `json:"title"`
	Status int                  `json:"status"`
	Detail string               `json:"detail"`
	Code   errcode.Code         `json:"code"`
	Errors []errcode.FieldError `json:"errors,omitempty"`
}

// writeProblem answers err as problem details. A code outside the registry
// becomes internal, and a 5xx answer carries only its title: the cause is
// logged on the agent, never sent.
func (h *handler) writeProblem(w http.ResponseWriter, err error) {
	e := errcode.From(err)
	code := e.Code
	if !errcode.Known(code) {
		code = errcode.Internal
	}
	status := code.Status()
	p := problem{Type: "about:blank", Title: code.Title(), Status: status, Detail: e.Detail, Code: code, Errors: e.Fields}
	if status >= http.StatusInternalServerError {
		p.Detail, p.Errors = code.Title(), nil
		h.cfg.Log.Error("api: request failed", "code", e.Code, "err", err)
	}
	if p.Detail == "" {
		p.Detail = code.Title()
	}
	b, merr := json.Marshal(p)
	if merr != nil {
		panic(fmt.Sprintf("api: marshal problem: %v", merr)) // strings, ints and field errors always marshal
	}
	w.Header().Set("Content-Type", problemContentType)
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func (h *handler) writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		h.writeProblem(w, fmt.Errorf("api: marshal response: %w", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// writeRaw answers an action's or resource's output as it is; no output is
// JSON null.
func (h *handler) writeRaw(w http.ResponseWriter, out json.RawMessage) {
	if len(out) == 0 {
		out = json.RawMessage("null")
	}
	if !json.Valid(out) {
		h.writeProblem(w, errors.New("api: the agent answered output that is not JSON"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out) //nolint:gosec // G705: checked JSON sent as application/json, never rendered as HTML
}
