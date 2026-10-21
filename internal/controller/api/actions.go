package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
)

// The operations of an action route, its last path segment.
const (
	opPlan  = "plan"
	opApply = "apply"
	opRead  = "read"
)

// maxChangeLimit is the largest limit of GET /api/v1/changes; the engine
// lists no more. defaultChangeLimit is the limit when the query names none,
// the engine's default.
const (
	maxChangeLimit     = 500
	defaultChangeLimit = 100
)

// maxExtraChangeFetches bounds the further pages GET /api/v1/changes fetches
// to fill a page that hidden changes thinned out. Each asks for
// maxChangeLimit changes, so a page comes back short only after a run of
// more than 2,000 hidden changes.
const maxExtraChangeFetches = 4

var changeStates = []string{
	core.StateApplying, core.StatePending, core.StateConfirmed, core.StateReverted,
	core.StateCompleted, core.StateFailed, core.StateInterrupted,
}

// planResponse is the Plan shape of spec section 12.
type planResponse struct {
	PlanID       string          `json:"planId"`
	HostID       *string         `json:"hostId"`
	Action       string          `json:"action"`
	Version      int             `json:"version"`
	InputHash    string          `json:"inputHash"`
	Diff         []core.DiffLine `json:"diff"`
	Guards       []core.Guard    `json:"guards"`
	ExpiresAt    time.Time       `json:"expiresAt"`
	Confirmation *string         `json:"confirmation"`
}

type changeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// changeResponse is the Change shape of spec section 12.
type changeResponse struct {
	ID             string          `json:"id"`
	HostID         *string         `json:"hostId"`
	Action         string          `json:"action"`
	Version        int             `json:"version"`
	Class          core.Class      `json:"class"`
	State          string          `json:"state"`
	Actor          string          `json:"actor"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	RevertDeadline *time.Time      `json:"revertDeadline"`
	Output         json.RawMessage `json:"output"`
	Error          *changeError    `json:"error"`
	CompensatedBy  *string         `json:"compensatedBy"`
}

type changesResponse struct {
	Changes []changeResponse `json:"changes"`
}

type readResponse struct {
	Output json.RawMessage `json:"output"`
}

// inputRequest is the body of plan and read.
type inputRequest struct {
	Input json.RawMessage `json:"input"`
}

type applyRequest struct {
	PlanID                string `json:"planId"`
	ConfirmTimeoutSeconds int64  `json:"confirmTimeoutSeconds"`
	Confirmation          string `json:"confirmation"`
	ConfirmNow            bool   `json:"confirmNow"`
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func orNull(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return raw
}

// toPlan converts a plan; its diff and guards are [] rather than null.
func toPlan(v core.PlanView) planResponse {
	diff, guards := v.Diff, v.Guards
	if diff == nil {
		diff = []core.DiffLine{}
	}
	if guards == nil {
		guards = []core.Guard{}
	}
	return planResponse{PlanID: v.PlanID, HostID: nullable(v.HostID), Action: v.Action, Version: v.Version,
		InputHash: v.InputHash, Diff: diff, Guards: guards, ExpiresAt: v.ExpiresAt, Confirmation: nullable(v.Confirmation)}
}

func toChange(v core.ChangeView) changeResponse {
	c := changeResponse{ID: v.ID, HostID: nullable(v.HostID), Action: v.Action, Version: v.Version, Class: v.Class,
		State: v.State, Actor: v.Actor, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, RevertDeadline: v.RevertDeadline,
		Output: orNull(v.Output), CompensatedBy: nullable(v.CompensatedBy)}
	if v.Error != nil {
		c.Error = &changeError{Code: v.Error.Code, Message: v.Error.Message}
	}
	return c
}

func invalidField(path, message string) *errcode.Error {
	return &errcode.Error{Code: errcode.InvalidInput, Detail: "invalid request",
		Fields: []errcode.FieldError{{Path: path, Message: message}}}
}

// options checks an apply body. The confirm timeout is bounded here too, so
// a huge value cannot overflow the duration.
func (a applyRequest) options() (core.ApplyOptions, error) {
	if a.PlanID == "" {
		return core.ApplyOptions{}, invalidField("/planId", "required")
	}
	lo, hi := int64(lifecycle.MinConfirmTimeout/time.Second), int64(lifecycle.MaxConfirmTimeout/time.Second)
	if a.ConfirmTimeoutSeconds != 0 && (a.ConfirmTimeoutSeconds < lo || a.ConfirmTimeoutSeconds > hi) {
		return core.ApplyOptions{}, invalidField("/confirmTimeoutSeconds", fmt.Sprintf("must be %d to %d", lo, hi))
	}
	return core.ApplyOptions{ConfirmTimeout: time.Duration(a.ConfirmTimeoutSeconds) * time.Second,
		Confirmation: a.Confirmation, ConfirmNow: a.ConfirmNow}, nil
}

// lookupAction resolves an action route's {name} and {version} to an action
// that side serves with op: a read action has only read, a mutating action
// only plan and apply.
func (s *Server) lookupAction(name, version string, side core.Side, op string) (core.ActionDef, error) {
	unknown := errcode.New(errcode.ActionUnknown, fmt.Sprintf("no action %s/%s with a %s operation at this route", name, version, op))
	v, err := core.ParseVersion(version)
	if err != nil {
		return core.ActionDef{}, unknown
	}
	def, ok := s.cfg.Registry.Action(core.ActionRef{Name: name, Version: v})
	if !ok || def.Spec.Side != side || (op == opRead) != (def.Spec.Class == core.ClassRead) {
		return core.ActionDef{}, unknown
	}
	return def, nil
}

// actionHandler serves op of the network's action routes: agent-side
// actions under /api/v1/hosts/{host}, controller-side ones without a host.
// Break-glass actions are refused here; only the controller socket serves
// them (spec section 10).
func (s *Server) actionHandler(side core.Side, op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, _ := callerFrom(r.Context())
		def, err := s.lookupAction(r.PathValue("name"), r.PathValue("version"), side, op)
		if err == nil && def.Spec.BreakGlass {
			err = errcode.New(errcode.Forbidden, "break-glass actions are served only on the controller socket")
		}
		if err == nil {
			err = authorize(c.principal, RequiredScope(def.Spec.Class, def.Spec.Side))
		}
		if err != nil {
			writeProblem(w, s.log, err)
			return
		}
		s.runAction(w, r, c.principal, r.PathValue("host"), def, op)
	}
}

// runAction decodes op's body and dispatches it. Apply answers 200 with the
// change whatever its outcome (spec section 12).
func (s *Server) runAction(w http.ResponseWriter, r *http.Request, p core.Principal, hostID string, def core.ActionDef, op string) {
	ctx := r.Context()
	if op == opApply {
		var in applyRequest
		if err := decodeBody(w, r, &in); err != nil {
			writeProblem(w, s.log, err)
			return
		}
		opts, err := in.options()
		if err != nil {
			writeProblem(w, s.log, err)
			return
		}
		v, err := s.cfg.Dispatcher.Apply(ctx, p, hostID, def.Ref(), in.PlanID, opts)
		if err != nil {
			writeProblem(w, s.log, err)
			return
		}
		writeJSON(w, s.log, http.StatusOK, toChange(v))
		return
	}
	var in inputRequest
	if err := decodeBody(w, r, &in); err != nil {
		writeProblem(w, s.log, err)
		return
	}
	if len(in.Input) == 0 {
		writeProblem(w, s.log, invalidField("/input", "required"))
		return
	}
	if op == opRead {
		out, err := s.cfg.Dispatcher.Read(ctx, p, hostID, def.Ref(), in.Input)
		if err != nil {
			writeProblem(w, s.log, err)
			return
		}
		writeJSON(w, s.log, http.StatusOK, readResponse{Output: orNull(out)})
		return
	}
	v, err := s.cfg.Dispatcher.Plan(ctx, p, hostID, def.Ref(), in.Input, core.PlanOptions{})
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, toPlan(v))
}

// handleResource answers a resource's output as it is.
func (s *Server) handleResource(w http.ResponseWriter, r *http.Request) {
	c, _ := callerFrom(r.Context())
	name := r.PathValue("module") + "." + r.PathValue("resource")
	var err error
	if _, ok := s.cfg.Registry.Resource(name); !ok {
		err = errcode.New(errcode.NotFound, "no resource "+name)
	} else {
		err = authorize(c.principal, core.ScopeRead)
	}
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	out, err := s.cfg.Dispatcher.Resource(r.Context(), c.principal, r.PathValue("host"), name)
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, orNull(out))
}

// changeFilter reads the query of GET /api/v1/changes.
func changeFilter(q url.Values) (core.ChangeFilter, error) {
	f := core.ChangeFilter{State: q.Get("state"), HostID: q.Get("hostId"), Before: q.Get("before")}
	if f.State != "" && !slices.Contains(changeStates, f.State) {
		return f, invalidField("state", fmt.Sprintf("must be one of %v", changeStates))
	}
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > maxChangeLimit {
			return f, invalidField("limit", fmt.Sprintf("must be 1 to %d", maxChangeLimit))
		}
		f.Limit = n
	}
	return f, nil
}

func (s *Server) handleChanges(w http.ResponseWriter, r *http.Request) {
	c, _ := callerFrom(r.Context())
	if err := authorize(c.principal, core.ScopeRead); err != nil {
		writeProblem(w, s.log, err)
		return
	}
	f, err := changeFilter(r.URL.Query())
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	if f.Limit == 0 {
		f.Limit = defaultChangeLimit
	}
	want := f.Limit
	out := changesResponse{Changes: make([]changeResponse, 0, want)}
	for range 1 + maxExtraChangeFetches {
		views, err := s.cfg.Dispatcher.Changes(r.Context(), c.principal, f)
		if err != nil {
			writeProblem(w, s.log, err)
			return
		}
		for _, v := range views {
			if len(out.Changes) == want {
				break
			}
			if s.networkVisible(v) {
				out.Changes = append(out.Changes, toChange(v))
			}
		}
		if len(out.Changes) == want || len(views) < f.Limit {
			break
		}
		f.Before, f.Limit = views[len(views)-1].ID, maxChangeLimit
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

func (s *Server) handleChange(w http.ResponseWriter, r *http.Request) {
	c, _ := callerFrom(r.Context())
	if err := authorize(c.principal, core.ScopeRead); err != nil {
		writeProblem(w, s.log, err)
		return
	}
	id := r.PathValue("id")
	v, err := s.cfg.Dispatcher.Change(r.Context(), c.principal, id)
	if err == nil && !s.networkVisible(v) {
		err = errHiddenChange(id)
	}
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, toChange(v))
}

// networkVisible reports whether the network listeners show change v. They
// hide changes of break-glass actions, whose inputs and outputs are the
// controller socket's alone (owner decision), and controller-side changes
// whose action is no longer registered, because whether it was break-glass
// cannot be known. Show, confirm and revert answer a hidden change with
// errHiddenChange.
func (s *Server) networkVisible(v core.ChangeView) bool {
	def, ok := s.cfg.Registry.Action(core.ActionRef{Name: v.Action, Version: v.Version})
	if ok {
		return !def.Spec.BreakGlass
	}
	return v.HostID != ""
}

// errHiddenChange is the answer to a change the network hides: the lifecycle
// engine's own answer for a missing change, so the two cannot be told apart.
func errHiddenChange(id string) error {
	return errcode.New(errcode.ChangeNotFound, fmt.Sprintf("no change %s", id))
}

// authorizeChange applies to a confirm or revert the scope of the change's
// own action, never of the route: RequiredScope of its class and side. v
// must be networkVisible, so an unregistered action is agent-side and its
// stored class decides.
func (s *Server) authorizeChange(p core.Principal, v core.ChangeView) error {
	class, side := v.Class, core.SideAgent
	if def, ok := s.cfg.Registry.Action(core.ActionRef{Name: v.Action, Version: v.Version}); ok {
		class, side = def.Spec.Class, def.Spec.Side
	}
	return authorize(p, RequiredScope(class, side))
}

// changeOp serves confirm or revert. It reads the change first, as the
// caller, answers a change the network hides as missing, and authorizes
// against the change's action.
func (s *Server) changeOp(confirm bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, _ := callerFrom(r.Context())
		id := r.PathValue("id")
		err := authorize(c.principal, core.ScopeRead)
		var v core.ChangeView
		if err == nil {
			v, err = s.cfg.Dispatcher.Change(r.Context(), c.principal, id)
		}
		if err == nil && !s.networkVisible(v) {
			err = errHiddenChange(id)
		}
		if err == nil {
			err = s.authorizeChange(c.principal, v)
		}
		if err != nil {
			writeProblem(w, s.log, err)
			return
		}
		run := s.cfg.Dispatcher.Revert
		if confirm {
			run = s.cfg.Dispatcher.Confirm
		}
		v, err = run(r.Context(), c.principal, id)
		if err != nil {
			writeProblem(w, s.log, err)
			return
		}
		writeJSON(w, s.log, http.StatusOK, toChange(v))
	}
}
