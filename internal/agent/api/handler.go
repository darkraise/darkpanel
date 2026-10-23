// Package api is the agent API (ADR 0012): the HTTP handler the agent serves
// on transport streams and on its socket, and the core.AgentClient that
// calls it over either.
package api

import (
	"bytes"
	"context"
	"encoding/base64"
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
	opHello        = "hello"
	opRead         = "read"
	opResource     = "resource"
	opPlan         = "plan"
	opApply        = "apply"
	opConfirm      = "confirm"
	opRevert       = "revert"
	opChange       = "change"
	opChanges      = "changes"
	opPreflight    = "preflight"
	opJournal      = "journal"
	opAck          = "ack"
	opUndo         = "undo"
	opRepair       = "repair"
	opRelease      = "release"
	opEffects      = "effects"
	opObserve      = "observe"
	opResolve      = "resolve"
	opLookup       = "lookup"
	opRetire       = "retire"
	opAckPrivate   = "ack-private"
	opEnabled      = "enabled"
	opAvailability = "availability"
	opReveal       = "reveal"
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

// handler serves the agent API. Its functions are what differs between a
// transport stream, whose peer is the authenticated controller, and the
// agent socket.
type handler struct {
	cfg Config
	// principal returns the caller of an operation that acts for one.
	principal func(r *http.Request) (core.Principal, error)
	// call returns the call context the controller sends with a plan, apply,
	// undo or release; nil when the request carries none.
	call func(r *http.Request) (*core.CallContext, error)
	// allow refuses an operation before it reaches the agent; name is the
	// action or resource name, empty for the other operations.
	allow func(ctx context.Context, op, name string) error
	// hello checks the controller's half of a hello.
	hello func(r *http.Request) error
	// planInput refuses a plan's input before it reaches the agent.
	planInput func(input json.RawMessage) error
}

// NewHandler serves the agent API on transport streams: the caller is the
// principal the controller names in the Darkpanel-Principal header.
func NewHandler(cfg Config) http.Handler {
	h := &handler{cfg: cfg}
	h.principal = headerPrincipal
	h.call = headerCall
	h.allow = func(context.Context, string, string) error { return nil }
	h.hello = h.controllerHello
	h.planInput = func(json.RawMessage) error { return nil }
	return h.routes(true)
}

// routes maps every route. The journal routes and the change operations of
// spec 1.3 only serve the controller's session: the agent socket serves none
// of them, so every undo, repair, release and resolution goes through the
// controller's admission checks.
func (h *handler) routes(controller bool) *http.ServeMux {
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
	if controller {
		mux.HandleFunc("GET "+prefix+"journal", h.handleJournal)
		mux.HandleFunc("POST "+prefix+"journal/ack", h.handleAck)
		mux.HandleFunc("POST "+prefix+"changes/{id}/undo", h.handleUndo)
		mux.HandleFunc("POST "+prefix+"changes/{id}/repair", h.handleRepair)
		mux.HandleFunc("POST "+prefix+"changes/{id}/release", h.handleRelease)
		mux.HandleFunc("GET "+prefix+"changes/{id}/effects", h.handleEffects)
		mux.HandleFunc("GET "+prefix+"changes/{id}/effects/{seq}/observe", h.handleObserve)
		mux.HandleFunc("GET "+prefix+"modules/{id}/availability", h.handleAvailability)
		mux.HandleFunc("POST "+prefix+"changes/{id}/effects/{seq}/resolve", h.handleResolve)
		mux.HandleFunc("POST "+prefix+"changes/{id}/private-output/ack", h.handleAckPrivate)
		mux.HandleFunc("GET "+prefix+"plans/{id}", h.handleLookup)
		mux.HandleFunc("POST "+prefix+"plans/{id}/retire", h.handleRetire)
		mux.HandleFunc("PUT "+prefix+"modules/{id}/enabled", h.handleEnabled)
		mux.HandleFunc("POST "+prefix+"reveal/{name}/{version}", h.handleReveal)
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

// headerCall reads the call context the controller sends, base64url without
// padding over its JSON; a request without the header has none. Unknown
// members are refused, so a repairOf, which the agent derives itself, is
// request.invalid_input.
func headerCall(r *http.Request) (*core.CallContext, error) {
	if len(r.Header.Values(wire.HeaderCall)) == 0 {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(r.Header.Get(wire.HeaderCall))
	if err != nil {
		return nil, errcode.New(errcode.InvalidInput, fmt.Sprintf("the %s header is not base64url without padding: %v", wire.HeaderCall, err))
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var c wire.CallContext
	err = dec.Decode(&c)
	if err == nil {
		if _, tail := dec.Token(); !errors.Is(tail, io.EOF) {
			err = errors.New("more than one JSON value")
		}
	}
	if err != nil {
		return nil, errcode.New(errcode.InvalidInput, fmt.Sprintf("the %s header is not a call context: %v", wire.HeaderCall, err))
	}
	call := c.Core()
	return &call, nil
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

// handleReveal runs a reveal action, or with Darkpanel-Internal: 1 an
// Internal read, and answers the value as it is (spec 1.7 and 1.14). Only
// the controller's session serves the route, so every reveal is authorized
// and audited by the controller; the agent socket has no form of it.
func (h *handler) handleReveal(w http.ResponseWriter, r *http.Request) {
	ref, err := actionRef(r)
	var p core.Principal
	if err == nil {
		p, err = h.actor(r, opReveal, ref.Name)
	}
	var internal bool
	if err == nil {
		internal, err = internalHeader(r)
	}
	var input json.RawMessage
	if err == nil {
		input, err = readJSON(w, r)
	}
	var out json.RawMessage
	if err == nil {
		out, err = h.cfg.Agent.Reveal(r.Context(), p, ref, input, internal)
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.writeRaw(w, out)
}

// internalHeader reads Darkpanel-Internal: absent asks for a reveal, "1"
// for an Internal read. Any other value is refused, so that a malformed
// header never selects the other kind of call.
func internalHeader(r *http.Request) (bool, error) {
	switch vs := r.Header.Values(wire.HeaderInternal); {
	case len(vs) == 0:
		return false, nil
	case len(vs) == 1 && vs[0] == "1":
		return true, nil
	}
	return false, errcode.New(errcode.InvalidInput, fmt.Sprintf("the %s header accepts only the value 1", wire.HeaderInternal))
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
	var call *core.CallContext
	if err == nil {
		call, err = h.call(r)
	}
	var input json.RawMessage
	if err == nil {
		input, err = readJSON(w, r)
	}
	if err == nil {
		err = h.planInput(input)
	}
	var v core.PlanView
	if err == nil {
		v, err = h.cfg.Agent.Plan(r.Context(), p, ref, input, core.PlanOptions{Call: call})
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, wire.FromPlanView(v))
}

// handleApply answers 202 while the change is still applying (spec 1.3
// Asynchronous apply), 200 otherwise.
func (h *handler) handleApply(w http.ResponseWriter, r *http.Request) {
	p, err := h.actor(r, opApply, "")
	var call *core.CallContext
	if err == nil {
		call, err = h.call(r)
	}
	var opts wire.ApplyOptions
	if err == nil {
		err = decodeJSON(w, r, &opts)
	}
	var v core.ChangeView
	if err == nil {
		o := opts.Core()
		o.Call = call
		v, err = h.cfg.Agent.Apply(r.Context(), p, r.PathValue("id"), o)
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	status := http.StatusOK
	if v.State == core.StateApplying {
		status = http.StatusAccepted
	}
	h.writeJSON(w, status, wire.FromChangeView(v))
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

// handleAvailability answers which actions of a module are available (spec
// 1.7); it serves only the controller's session.
func (h *handler) handleAvailability(w http.ResponseWriter, r *http.Request) {
	err := h.allow(r.Context(), opAvailability, "")
	var m core.ModuleAvailability
	if err == nil {
		m, err = h.cfg.Agent.Availability(r.Context(), r.PathValue("id"))
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, wire.FromModuleAvailability(m))
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

// handleUndo answers 202 with the change undoing, or 200 when it was
// already reverted.
func (h *handler) handleUndo(w http.ResponseWriter, r *http.Request) {
	p, err := h.actor(r, opUndo, "")
	var call *core.CallContext
	if err == nil {
		call, err = h.call(r)
	}
	var v core.ChangeView
	if err == nil {
		v, err = h.cfg.Agent.Undo(r.Context(), p, r.PathValue("id"), call)
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	status := http.StatusAccepted
	if v.State == core.StateReverted {
		status = http.StatusOK
	}
	h.writeJSON(w, status, wire.FromChangeView(v))
}

// handleRepair answers 202 with the change a repair started, or 200 with the
// plan it made.
func (h *handler) handleRepair(w http.ResponseWriter, r *http.Request) {
	p, err := h.actor(r, opRepair, "")
	var req core.RepairRequest
	if err == nil {
		err = decodeJSON(w, r, &req)
	}
	var res core.RepairResult
	if err == nil {
		res, err = h.cfg.Agent.Repair(r.Context(), p, r.PathValue("id"), req)
	}
	if err == nil && (res.Change == nil) == (res.Plan == nil) {
		err = errors.New("api: a repair answered neither a change nor a plan, or both")
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	status := http.StatusOK
	if res.Change != nil {
		status = http.StatusAccepted
	}
	h.writeJSON(w, status, wire.FromRepairResult(res))
}

// handleRelease passes the reason on unchecked: the engine validates it.
func (h *handler) handleRelease(w http.ResponseWriter, r *http.Request) {
	p, err := h.actor(r, opRelease, "")
	var call *core.CallContext
	if err == nil {
		call, err = h.call(r)
	}
	var body wire.Release
	if err == nil {
		err = decodeJSON(w, r, &body)
	}
	var res core.ReleaseResult
	if err == nil {
		res, err = h.cfg.Agent.Release(r.Context(), p, r.PathValue("id"), body.Reason, call)
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, wire.FromReleaseResult(res))
}

func (h *handler) handleEffects(w http.ResponseWriter, r *http.Request) {
	p, err := h.actor(r, opEffects, "")
	var effects []core.EffectView
	if err == nil {
		effects, err = h.cfg.Agent.Effects(r.Context(), p, r.PathValue("id"))
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	if effects == nil {
		effects = []core.EffectView{}
	}
	h.writeJSON(w, http.StatusOK, wire.Effects{Effects: effects})
}

// effectSeq reads an effect route's {seq}, a positive integer.
func effectSeq(r *http.Request) (int, error) {
	seq, err := strconv.Atoi(r.PathValue("seq"))
	if err != nil || seq < 1 {
		return 0, &errcode.Error{Code: errcode.InvalidInput, Detail: "seq must be a positive integer",
			Fields: []errcode.FieldError{{Path: "/seq", Message: "must be a positive integer"}}}
	}
	return seq, nil
}

func (h *handler) handleObserve(w http.ResponseWriter, r *http.Request) {
	p, err := h.actor(r, opObserve, "")
	var seq int
	if err == nil {
		seq, err = effectSeq(r)
	}
	var res core.ObserveResult
	if err == nil {
		res, err = h.cfg.Agent.Observe(r.Context(), p, r.PathValue("id"), seq)
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, res)
}

func (h *handler) handleResolve(w http.ResponseWriter, r *http.Request) {
	p, err := h.actor(r, opResolve, "")
	var seq int
	if err == nil {
		seq, err = effectSeq(r)
	}
	var req core.ResolveRequest
	if err == nil {
		err = decodeJSON(w, r, &req)
	}
	var v core.EffectView
	if err == nil {
		v, err = h.cfg.Agent.Resolve(r.Context(), p, r.PathValue("id"), seq, req)
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, v)
}

// lookupPrivate reads GET plans/{id}'s one parameter: private=1 asks for a
// child change's unredacted output.
func lookupPrivate(r *http.Request) (bool, error) {
	q := r.URL.Query()
	for _, k := range slices.Sorted(maps.Keys(q)) {
		if k != "private" || len(q[k]) > 1 {
			return false, invalidQuery(k, "unknown or repeated parameter")
		}
	}
	if !q.Has("private") {
		return false, nil
	}
	if q.Get("private") != "1" {
		return false, invalidQuery("private", "must be 1")
	}
	return true, nil
}

func (h *handler) handleLookup(w http.ResponseWriter, r *http.Request) {
	private, err := lookupPrivate(r)
	if err == nil {
		err = h.allow(r.Context(), opLookup, "")
	}
	var lk core.PlanLookup
	if err == nil {
		lk, err = h.cfg.Agent.Lookup(r.Context(), r.PathValue("id"), private)
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, lk)
}

func (h *handler) handleRetire(w http.ResponseWriter, r *http.Request) {
	err := h.allow(r.Context(), opRetire, "")
	var lk core.PlanLookup
	if err == nil {
		lk, err = h.cfg.Agent.Retire(r.Context(), r.PathValue("id"))
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, lk)
}

// handleAckPrivate reads an empty body as an acknowledgement that is not
// final.
func (h *handler) handleAckPrivate(w http.ResponseWriter, r *http.Request) {
	err := h.allow(r.Context(), opAckPrivate, "")
	var ack wire.AckPrivate
	if err == nil && r.ContentLength != 0 {
		err = decodeJSON(w, r, &ack)
	}
	if err == nil {
		err = h.cfg.Agent.AckPrivate(r.Context(), r.PathValue("id"), ack.Final)
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleEnabled stores the controller's enabled flag of a module (spec 1.4).
// A body without enabled is refused rather than read as a disable.
func (h *handler) handleEnabled(w http.ResponseWriter, r *http.Request) {
	err := h.allow(r.Context(), opEnabled, "")
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err == nil {
		err = decodeJSON(w, r, &body)
	}
	if err == nil && body.Enabled == nil {
		err = &errcode.Error{Code: errcode.InvalidInput, Detail: "enabled is required",
			Fields: []errcode.FieldError{{Path: "/enabled", Message: "required, true or false"}}}
	}
	if err == nil {
		err = h.cfg.Agent.SetModuleEnabled(r.Context(), r.PathValue("id"), *body.Enabled)
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
