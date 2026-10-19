// Package coreactions is the core module (spec section 10): the host, module,
// token, audit and user actions, under module id "core". Every action is
// controller-side, and the six user actions are break-glass.
package coreactions

import (
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
)

// Deps are the controller services the core actions call. registry.Build
// passes the zero value for callers that use only the registry's shape; a
// handler that needs a dependency it was not given fails with an internal
// error.
type Deps struct {
	// Auth is the identity service over the controller database.
	Auth *auth.Service
	// Audit is the controller's audit log, read by core.audit.list.
	Audit core.Audit
	// Modules are the compiled-in modules whose enablement the core.module
	// actions manage, in registration order; the server passes modules.All().
	Modules []core.Module
	// Extra are core actions defined outside this package, such as the update
	// and rollout actions; they join the one core manifest after the built-in
	// ones.
	Extra []core.ActionDef
	// OwnedPaths are the files, directories, units and user the core itself
	// installs, declared like a module's so that the registry's ownership
	// rule and uninstall cover them.
	OwnedPaths []core.OwnedPath
}

// Module returns the core module.
func Module(d Deps) core.Module {
	m := module{d: d}
	for _, mod := range d.Modules {
		man := mod.Manifest()
		m.catalog = append(m.catalog, moduleInfo{id: man.ID, version: man.Version})
	}
	return m
}

type moduleInfo struct {
	id      string
	version int
}

type module struct {
	d       Deps
	catalog []moduleInfo
}

// Manifest declares the core module.
func (m module) Manifest() core.Manifest {
	var actions []core.ActionDef
	for _, group := range [][]core.ActionDef{
		m.hostActions(),
		m.moduleActions(),
		m.tokenActions(),
		m.auditActions(),
		m.userActions(),
		m.enrollActions(),
	} {
		actions = append(actions, group...)
	}
	actions = append(actions, m.d.Extra...)
	return core.Manifest{ID: core.ModuleIDCore, Version: 1, Actions: actions, OwnedPaths: m.d.OwnedPaths, DefaultEnabled: true}
}

// NoInput is the input of an action that takes none.
type NoInput struct{}

// Empty is the output of an action that returns nothing.
type Empty struct{}
