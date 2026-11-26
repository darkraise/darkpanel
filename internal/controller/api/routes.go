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
		{method: http.MethodPost, path: enrollBeginPath, summary: "Start an enrollment link's use: a new TOTP secret and its otpauth URI",
			public: true, handler: s.handleEnrollBegin},
		{method: http.MethodPost, path: enrollFinishPath, summary: "Finish an enrollment link: set the password, confirm a code and sign in",
			public: true, handler: s.handleEnrollFinish},
		{method: http.MethodGet, path: methodsPath, summary: "Which sign-in buttons the login page shows on this origin and listener",
			public: true, handler: s.handleMethods},
		{method: http.MethodGet, path: oidcStartPath, summary: "Start an OIDC sign-in: redirect to the provider with a state cookie",
			public: true, handler: s.handleOIDCStart},
		{method: http.MethodGet, path: oidcCallbackPath, summary: "Finish an OIDC sign-in: a page that navigates to /?signin=<outcome>",
			public: true, handler: s.handleOIDCCallback},
		{method: http.MethodGet, path: tailscalePath, summary: "Which user this Tailscale node may continue as, whether the node is remembered and the user code-ready",
			public: true, handler: s.handleTailscaleMatch},
		{method: http.MethodPost, path: tailscalePath, summary: "Continue as the whois match: a remembered node signs in, any other asks the code that remembers it",
			public: true, handler: s.handleTailscaleSignIn},
		{method: http.MethodPost, path: totpStepPath, summary: "Enter the authenticator code a sign-in method asks for and sign in; a Tailscale code must come from its node",
			public: true, handler: s.handleTOTPStep},
		{method: http.MethodPost, path: "/api/v1/auth/logout", summary: "Log out and clear the session cookie", handler: s.handleLogout},
		{method: http.MethodGet, path: "/api/v1/auth/session", summary: "Describe the calling session or token", handler: s.handleSession},
		{method: http.MethodPost, path: "/api/v1/auth/step-up", summary: "Enter a fresh authenticator code to elevate the session for 15 minutes",
			handler: s.handleStepUp},
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
		{method: http.MethodGet, path: "/api/v1/hosts/{host}/streams/{module}/{name}",
			summary: "Follow a module stream of a host as server-sent events", handler: s.handleStream},
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
