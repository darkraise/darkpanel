package api

import (
	"net/http"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

type healthStatus struct {
	Status string `json:"status"`
}

type healthDetail struct {
	Status        string `json:"status"`
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	SchemaVersion int    `json:"schemaVersion"`
}

// handleHealthz answers the status alone, or the full detail for a caller
// with valid credentials (spec section 12). The authentication middleware
// drops invalid credentials on this route instead of refusing them.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	c, ok := callerFrom(r.Context())
	if !ok || authorize(c.principal, core.ScopeRead) != nil {
		writeJSON(w, s.log, http.StatusOK, healthStatus{Status: "ok"})
		return
	}
	s.writeHealthDetail(w, r)
}

// writeHealthDetail answers the version, the commit and the controller
// database's schema version.
func (s *Server) writeHealthDetail(w http.ResponseWriter, r *http.Request) {
	n, err := s.cfg.SchemaVersion(r.Context())
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, healthDetail{Status: "ok", Version: buildinfo.Version, Commit: buildinfo.Commit, SchemaVersion: n})
}

// handleOpenAPI answers the OpenAPI document (spec section 7), built from
// the registry and Routes on each request; it is rarely fetched.
func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	c, _ := callerFrom(r.Context())
	if err := authorize(c.principal, core.ScopeRead); err != nil {
		writeProblem(w, s.log, err)
		return
	}
	doc, err := schema.OpenAPI(s.cfg.Registry, Routes())
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(doc)
}

// handleListeners answers the listener state for the UI footer (spec section
// 13) from the source SetListenerState set.
func (s *Server) handleListeners(w http.ResponseWriter, r *http.Request) {
	c, _ := callerFrom(r.Context())
	if err := authorize(c.principal, core.ScopeRead); err != nil {
		writeProblem(w, s.log, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, s.listenerState())
}
