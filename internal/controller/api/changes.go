package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/darkraise/darkpanel/internal/core"
)

// maxReleaseReason is the longest release reason, in characters (spec 1.3).
const maxReleaseReason = 500

// repairRequest is the body of POST /api/v1/changes/{id}/repair: input for
// a RepairedBy plan, or a composite step decision, or neither.
type repairRequest struct {
	Input json.RawMessage `json:"input"`
	Step  string          `json:"step"`
	As    string          `json:"as"`
}

// repairResponse holds the change a repair started or the plan it made.
type repairResponse struct {
	Change *changeResponse `json:"change,omitempty"`
	Plan   *planResponse   `json:"plan,omitempty"`
}

type releaseRequest struct {
	Reason string `json:"reason"`
}

type releaseResponse struct {
	Change    changeResponse    `json:"change"`
	Abandoned []core.EffectView `json:"abandoned"`
}

type effectsResponse struct {
	Effects []core.EffectView `json:"effects"`
}

type stepsResponse struct {
	Steps []core.StepView `json:"steps"`
}

type resolveRequest struct {
	Resolution string `json:"resolution"`
	Resource   string `json:"resource"`
}

// request checks a repair body. An input must be an object, and a step
// decision names the step and reverted or applied, never with an input.
func (b repairRequest) request() (core.RepairRequest, error) {
	input := bytes.TrimSpace(b.Input)
	if bytes.Equal(input, []byte("null")) {
		input = nil
	}
	switch {
	case len(input) > 0 && input[0] != '{':
		return core.RepairRequest{}, invalidField("/input", "must be an object")
	case len(input) > 0 && b.Step != "":
		return core.RepairRequest{}, invalidField("/input", "not allowed with step")
	case b.Step == "" && b.As != "":
		return core.RepairRequest{}, invalidField("/step", "required with as")
	case b.Step != "" && b.As != "reverted" && b.As != "applied":
		return core.RepairRequest{}, invalidField("/as", "must be reverted or applied")
	}
	return core.RepairRequest{Input: input, Step: b.Step, As: b.As}, nil
}

// visibleChange authorizes need, then reads the route's change as the
// caller; a change the network hides is answered as missing.
func (s *Server) visibleChange(r *http.Request, need core.Scope) (core.Principal, core.ChangeView, error) {
	c, _ := callerFrom(r.Context())
	if err := authorize(c.principal, need); err != nil {
		return c.principal, core.ChangeView{}, err
	}
	id := r.PathValue("id")
	v, err := s.cfg.Dispatcher.Change(r.Context(), c.principal, id)
	if err == nil && !s.networkVisible(v) {
		err = errHiddenChange(id)
	}
	return c.principal, v, err
}

// effectSeq reads an effect route's {seq}, a positive integer.
func effectSeq(r *http.Request) (int, error) {
	seq, err := strconv.Atoi(r.PathValue("seq"))
	if err != nil || seq < 1 {
		return 0, invalidField("seq", "must be a positive integer")
	}
	return seq, nil
}

// handleUndo needs the scope of the change's own action, as confirm and
// revert do. It answers 202 with the change while it is undoing, and 200
// with the change otherwise, as when it was already reverted.
func (s *Server) handleUndo(w http.ResponseWriter, r *http.Request) {
	p, v, err := s.visibleChange(r, core.ScopeRead)
	if err == nil {
		err = s.authorizeChange(p, v)
	}
	if err == nil {
		v, err = s.cfg.Dispatcher.Undo(r.Context(), p, r.PathValue("id"))
	}
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	status := http.StatusOK
	if v.State == core.StateUndoing {
		status = http.StatusAccepted
	}
	writeJSON(w, s.log, status, toChange(v))
}

// handleRepair needs the scope of the change's own action. It answers 202
// with the change a repair started, or 200 with the plan it made, which the
// client applies through the ordinary apply route.
func (s *Server) handleRepair(w http.ResponseWriter, r *http.Request) {
	p, v, err := s.visibleChange(r, core.ScopeRead)
	if err == nil {
		err = s.authorizeChange(p, v)
	}
	var body repairRequest
	if err == nil {
		err = decodeBody(w, r, &body)
	}
	var req core.RepairRequest
	if err == nil {
		req, err = body.request()
	}
	var res core.RepairResult
	if err == nil {
		res, err = s.cfg.Dispatcher.Repair(r.Context(), p, r.PathValue("id"), req)
	}
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	switch {
	case res.Change != nil:
		ch := toChange(*res.Change)
		writeJSON(w, s.log, http.StatusAccepted, repairResponse{Change: &ch})
	case res.Plan != nil:
		pl := toPlan(*res.Plan)
		writeJSON(w, s.log, http.StatusOK, repairResponse{Plan: &pl})
	default:
		writeProblem(w, s.log, fmt.Errorf("api: the repair of change %s answered neither a change nor a plan", r.PathValue("id")))
	}
}

func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	p, _, err := s.visibleChange(r, core.ScopeAdmin)
	var body releaseRequest
	if err == nil {
		err = decodeBody(w, r, &body)
	}
	if n := utf8.RuneCountInString(body.Reason); err == nil && (n < 1 || n > maxReleaseReason) {
		err = invalidField("/reason", fmt.Sprintf("must be 1 to %d characters", maxReleaseReason))
	}
	var res core.ReleaseResult
	if err == nil {
		res, err = s.cfg.Dispatcher.Release(r.Context(), p, r.PathValue("id"), body.Reason)
	}
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	abandoned := res.Abandoned
	if abandoned == nil {
		abandoned = []core.EffectView{}
	}
	writeJSON(w, s.log, http.StatusOK, releaseResponse{Change: toChange(res.Change), Abandoned: abandoned})
}

func (s *Server) handleEffects(w http.ResponseWriter, r *http.Request) {
	p, _, err := s.visibleChange(r, core.ScopeRead)
	var effects []core.EffectView
	if err == nil {
		effects, err = s.cfg.Dispatcher.Effects(r.Context(), p, r.PathValue("id"))
	}
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	if effects == nil {
		effects = []core.EffectView{}
	}
	writeJSON(w, s.log, http.StatusOK, effectsResponse{Effects: effects})
}

func (s *Server) handleObserve(w http.ResponseWriter, r *http.Request) {
	p, _, err := s.visibleChange(r, core.ScopeAdmin)
	var seq int
	if err == nil {
		seq, err = effectSeq(r)
	}
	var res core.ObserveResult
	if err == nil {
		res, err = s.cfg.Dispatcher.Observe(r.Context(), p, r.PathValue("id"), seq)
	}
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, res)
}

func (s *Server) handleResolve(w http.ResponseWriter, r *http.Request) {
	p, _, err := s.visibleChange(r, core.ScopeAdmin)
	var seq int
	if err == nil {
		seq, err = effectSeq(r)
	}
	var body resolveRequest
	if err == nil {
		err = decodeBody(w, r, &body)
	}
	if err == nil && body.Resolution != core.ResolutionAccept && body.Resolution != core.ResolutionReconcile {
		err = invalidField("/resolution", "must be accept or reconcile")
	}
	var v core.EffectView
	if err == nil {
		v, err = s.cfg.Dispatcher.Resolve(r.Context(), p, r.PathValue("id"), seq,
			core.ResolveRequest{Resolution: body.Resolution, Resource: body.Resource})
	}
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, v)
}

func (s *Server) handleSteps(w http.ResponseWriter, r *http.Request) {
	p, _, err := s.visibleChange(r, core.ScopeRead)
	var steps []core.StepView
	if err == nil {
		steps, err = s.cfg.Dispatcher.Steps(r.Context(), p, r.PathValue("id"))
	}
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	if steps == nil {
		steps = []core.StepView{}
	}
	writeJSON(w, s.log, http.StatusOK, stepsResponse{Steps: steps})
}
