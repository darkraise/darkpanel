package api

import (
	"encoding/json"
	"net/http"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// revealResponse is the answer of a reveal route (spec 1.14).
type revealResponse struct {
	Value json.RawMessage `json:"value"`
}

// authorizeReveal refuses p unless it has admin, which every reveal route
// needs (spec 1.14), and the scope the action itself requires. The registry
// already gives every reveal action MinScope admin; checking admin here
// keeps the route closed should that rule ever change.
func authorizeReveal(p core.Principal, s core.ActionSpec) error {
	if err := authorize(p, core.ScopeAdmin); err != nil {
		return err
	}
	return authorize(p, core.RequiredScope(s))
}

// revealHandler serves the reveal route of side's actions: a reveal
// action's value, once, to a principal with the action's scope, which is
// admin. The executing side audits the reveal before it answers, and the
// value is never kept here.
func (s *Server) revealHandler(side core.Side) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// securityHeaders sets this for every API path; a revealed secret
		// must stay out of every cache even if that rule changes.
		w.Header().Set("Cache-Control", "no-store")
		c, _ := callerFrom(r.Context())
		def, err := s.lookupAction(r.PathValue("name"), r.PathValue("version"), side, opReveal)
		if err == nil && def.Spec.BreakGlass {
			err = errcode.New(errcode.Forbidden, "break-glass actions are served only on the controller socket")
		}
		if err == nil {
			err = authorizeReveal(c.principal, def.Spec)
		}
		var in inputRequest
		if err == nil {
			err = decodeBody(w, r, &in)
		}
		if err == nil && len(in.Input) == 0 {
			err = invalidField("/input", "required")
		}
		var out json.RawMessage
		if err == nil {
			out, err = s.cfg.Dispatcher.Reveal(r.Context(), c.principal, r.PathValue("host"), def.Ref(), in.Input)
		}
		if err != nil {
			writeProblem(w, s.log, err)
			return
		}
		writeJSON(w, s.log, http.StatusOK, revealResponse{Value: orNull(out)})
	}
}
