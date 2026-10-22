package api

import (
	"net/http"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// handleAvailability answers which actions of a module are available on a
// host, from the dispatcher's cached answer (spec 1.7), so the UI can grey
// out the others. Internal actions are left out: no public route names them.
func (s *Server) handleAvailability(w http.ResponseWriter, r *http.Request) {
	c, _ := callerFrom(r.Context())
	err := authorize(c.principal, core.ScopeRead)
	if err == nil && s.cfg.Availability == nil {
		err = errcode.New(errcode.NotFound, "module availability is not served by this controller")
	}
	var m core.ModuleAvailability
	if err == nil {
		m, err = s.cfg.Availability(r.Context(), r.PathValue("host"), r.PathValue("module"))
	}
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	actions := make([]core.ActionAvailability, 0, len(m.Actions))
	for _, a := range m.Actions {
		if !s.internal(a.Name) {
			actions = append(actions, a)
		}
	}
	m.Actions = actions
	writeJSON(w, s.log, http.StatusOK, wire.FromModuleAvailability(m))
}

// internal reports whether any version of the action name is Internal.
func (s *Server) internal(name string) bool {
	for _, def := range s.cfg.Registry.Actions() {
		if def.Spec.Name == name && def.Spec.Internal {
			return true
		}
	}
	return false
}
