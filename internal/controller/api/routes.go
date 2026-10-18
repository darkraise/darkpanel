package api

import (
	"net/http"

	"github.com/darkraise/darkpanel/internal/core/schema"
)

// route is one route of the network mux. A public route must also be public
// in routeAccess, which the authentication middleware consults.
type route struct {
	method, path, summary string
	public                bool
	handler               http.HandlerFunc
}

// networkRoutes lists the network mux's routes.
func (s *Server) networkRoutes() []route {
	return []route{}
}

// Routes returns the hand-written network routes that schema.OpenAPI
// documents next to the registry's actions and resources.
func Routes() []schema.Route {
	out := []schema.Route{}
	for _, rt := range (&Server{}).networkRoutes() {
		out = append(out, schema.Route{Method: rt.method, Path: rt.path, Summary: rt.summary, Public: rt.public})
	}
	return out
}
