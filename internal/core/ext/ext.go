// Package ext is how modules and the controller's backup package contribute
// controller routes and services (spec 1.12). It imports only internal/core,
// internal/core/store and the standard library, so module packages and
// internal/controller/server can both import it without a cycle: the server
// imports the modules, the modules import ext, and no module imports the
// server.
package ext

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// Surface is where a contributed route is served.
type Surface int

// The surfaces a contributed route can be served on.
const (
	SurfaceWeb    Surface = iota // session or token, CSRF on unsafe methods, the 60 s write timeout
	SurfaceSocket                // the controller socket and its peer-uid check (api/socket.go)
)

// Route is one contributed HTTP route.
type Route struct {
	Surface Surface
	Method  string     // http.MethodGet, ...
	Path    string     // "/api/v1/controller/backup"; a Go ServeMux path pattern
	Summary string     // for the OpenAPI document
	Scope   core.Scope // SurfaceWeb only: the minimum scope; ignored on the socket
	Stream  bool       // a 30 s deadline per write instead of the 60 s total
	Handler http.Handler
}

// Service is one contributed controller service.
type Service struct {
	Name string                          // "tailnet.keysweep"; its audit actor is system:<Name>
	Run  func(ctx context.Context) error // returns when ctx is done; an error restarts it with backoff 1 s to 5 min
}

// Deps is what the assembly gives controller contributions.
type Deps struct {
	DB          *store.DB // controller.db
	Sealer      core.Sealer
	Clock       core.Clock
	Log         *slog.Logger
	Hosts       core.HostDirectory
	Dispatch    Dispatcher
	Events      Publisher
	Listeners   core.Listeners
	Audit       core.Audit // services write their audit rows here (actor system:<Service.Name>)
	LocalHostID string
}

// Dispatcher is the controller's dispatcher (internal/controller/dispatch);
// hostID is empty for controller-side actions.
type Dispatcher interface {
	Read(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (json.RawMessage, error)
	ReadInternal(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (json.RawMessage, error) // 1.7: p is audited
	Plan(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage, opts core.PlanOptions) (core.PlanView, error)
	Apply(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, planID string, opts core.ApplyOptions) (core.ChangeView, error)
}

// Publisher is the event bus as contributions see it (1.6): the bus
// encodes data as JSON and assigns the id. events.Bus implements it
// directly; there is no adapter.
type Publisher interface {
	Publish(eventType string, data any)
}

// Controller is a module's optional controller-side contribution, found
// on a core.Module by type assertion.
type Controller interface {
	Controller(deps Deps) ([]Route, []Service, error)
}
