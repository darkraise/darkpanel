package server

import (
	"fmt"

	"github.com/darkraise/darkpanel/internal/controller/audit"
	"github.com/darkraise/darkpanel/internal/controller/backup"
	"github.com/darkraise/darkpanel/internal/controller/dispatch"
	"github.com/darkraise/darkpanel/internal/controller/events"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/modules"
)

// The controller's own types are what the extension package's interfaces
// describe, so contributions get them without adapters.
var (
	_ ext.Publisher      = (*events.Bus)(nil)
	_ ext.Dispatcher     = (*dispatch.Dispatcher)(nil)
	_ core.HostDirectory = (*dispatch.Hosts)(nil)
	_ core.Audit         = (*audit.Store)(nil)
)

// controllerFunc is a contribution given as a function, such as
// backup.Contribute.
type controllerFunc func(deps ext.Deps) ([]ext.Route, []ext.Service, error)

func (f controllerFunc) Controller(deps ext.Deps) ([]ext.Route, []ext.Service, error) { return f(deps) }

// contributor is one controller contribution and the name its errors carry.
type contributor struct {
	name string
	c    ext.Controller
}

// extDeps is what the assembly gives controller contributions (spec 1.12).
func (s *Server) extDeps() ext.Deps {
	return ext.Deps{
		DB: s.controllerDB, Sealer: s.controllerSealer, Clock: s.clock, Log: s.log, Hosts: s.hosts,
		Dispatch: s.dispatcher, Events: s.events, Audit: s.auditLog, LocalHostID: s.localHostID,
	}
}

// contributions calls Controller on every compiled-in module that has a
// controller-side contribution, on Options.Contributors, and on
// backup.Contribute, in that order, and returns their routes and services.
// A contribution's error fails startup.
func (s *Server) contributions() ([]ext.Route, []ext.Service, error) {
	var all []contributor
	for _, m := range modules.All() {
		if c, ok := m.(ext.Controller); ok {
			all = append(all, contributor{name: "module " + m.Manifest().ID, c: c})
		}
	}
	for i, c := range s.opts.Contributors {
		all = append(all, contributor{name: fmt.Sprintf("contributor %d", i), c: c})
	}
	all = append(all, contributor{name: "backup", c: controllerFunc(backup.Contribute)})
	deps := s.extDeps()
	var routes []ext.Route
	var services []ext.Service
	for _, c := range all {
		r, svc, err := c.c.Controller(deps)
		if err != nil {
			return nil, nil, fmt.Errorf("server: the controller contribution of %s: %w", c.name, err)
		}
		routes = append(routes, r...)
		services = append(services, svc...)
	}
	return routes, services, nil
}
