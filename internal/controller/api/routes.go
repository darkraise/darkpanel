package api

import (
	"net/http"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

// route is one route of the network mux. A public route must also be public
// in routeAccess, which the authentication middleware consults.
type route struct {
	method, path, summary string
	public                bool
	// perAction marks a route serving every registry action or resource of
	// one kind; schema.OpenAPI documents those from the registry, one
	// operation each, so Routes leaves them out.
	perAction bool
	handler   http.HandlerFunc
}

// networkRoutes lists the network mux's routes.
func (s *Server) networkRoutes() []route {
	const hostActions, controllerActions = "/api/v1/hosts/{host}/actions/{name}/{version}/", "/api/v1/actions/{name}/{version}/"
	return []route{
		{method: http.MethodPost, path: loginPath, summary: "Log in with username, password and TOTP code", public: true, handler: s.handleLogin},
		{method: http.MethodPost, path: "/api/v1/auth/logout", summary: "Log out and clear the session cookie", handler: s.handleLogout},
		{method: http.MethodGet, path: "/api/v1/auth/session", summary: "Describe the calling session or token", handler: s.handleSession},
		{method: http.MethodGet, path: "/api/v1/hosts/{host}/resources/{module}/{resource}", perAction: true, handler: s.handleResource},
		{method: http.MethodGet, path: "/api/v1/hosts/{host}/modules/{module}/availability",
			summary: "Which actions of a module are available on a host", handler: s.handleAvailability},
		{method: http.MethodPost, path: hostActions + opPlan, perAction: true, handler: s.actionHandler(core.SideAgent, opPlan)},
		{method: http.MethodPost, path: hostActions + opApply, perAction: true, handler: s.actionHandler(core.SideAgent, opApply)},
		{method: http.MethodPost, path: hostActions + opRead, perAction: true, handler: s.actionHandler(core.SideAgent, opRead)},
		{method: http.MethodPost, path: controllerActions + opPlan, perAction: true, handler: s.actionHandler(core.SideController, opPlan)},
		{method: http.MethodPost, path: controllerActions + opApply, perAction: true, handler: s.actionHandler(core.SideController, opApply)},
		{method: http.MethodPost, path: controllerActions + opRead, perAction: true, handler: s.actionHandler(core.SideController, opRead)},
		{method: http.MethodPost, path: hostActions + opReveal, perAction: true, handler: s.revealHandler(core.SideAgent)},
		{method: http.MethodPost, path: controllerActions + opReveal, perAction: true, handler: s.revealHandler(core.SideController)},
		{method: http.MethodGet, path: "/api/v1/changes", summary: "List changes, newest first", handler: s.handleChanges},
		{method: http.MethodGet, path: "/api/v1/changes/{id}", summary: "Show a change", handler: s.handleChange},
		{method: http.MethodPost, path: "/api/v1/changes/{id}/confirm", summary: "Confirm a pending change", handler: s.changeOp(true)},
		{method: http.MethodPost, path: "/api/v1/changes/{id}/revert", summary: "Revert a pending change", handler: s.changeOp(false)},
		{method: http.MethodPost, path: "/api/v1/changes/{id}/undo", summary: "Undo a change while no later change took its locks",
			handler: s.handleUndo},
		{method: http.MethodPost, path: "/api/v1/changes/{id}/repair", summary: "Repair a change that needs repair",
			handler: s.handleRepair},
		{method: http.MethodPost, path: "/api/v1/changes/{id}/release", summary: "Give up a change that needs repair",
			handler: s.handleRelease},
		{method: http.MethodGet, path: "/api/v1/changes/{id}/effects", summary: "List a change's effects",
			handler: s.handleEffects},
		{method: http.MethodGet, path: "/api/v1/changes/{id}/effects/{seq}/observe",
			summary: "Observe one effect's resource now, changing nothing", handler: s.handleObserve},
		{method: http.MethodPost, path: "/api/v1/changes/{id}/effects/{seq}/resolve", summary: "Resolve an unresolved effect",
			handler: s.handleResolve},
		{method: http.MethodGet, path: "/api/v1/changes/{id}/steps", summary: "List a composite change's steps",
			handler: s.handleSteps},
		{method: http.MethodGet, path: healthzPath, summary: "Health; with credentials also version, commit and schema version",
			public: true, handler: s.handleHealthz},
		{method: http.MethodGet, path: "/api/v1/openapi.json", summary: "This OpenAPI document", handler: s.handleOpenAPI},
		{method: http.MethodGet, path: "/api/v1/controller/listeners", summary: "Listener state for the UI footer: Tailscale bound, and a message",
			handler: s.handleListeners},
		{method: http.MethodGet, path: "/api/v1/events", summary: "Stream the controller's change, step, host and module events (server-sent events)",
			handler: s.handleEvents},
	}
}

// Routes returns the hand-written network routes that schema.OpenAPI
// documents next to the registry's actions and resources.
func Routes() []schema.Route {
	out := []schema.Route{}
	for _, rt := range (&Server{}).networkRoutes() {
		if !rt.perAction {
			out = append(out, schema.Route{Method: rt.method, Path: rt.path, Summary: rt.summary, Public: rt.public})
		}
	}
	return out
}
