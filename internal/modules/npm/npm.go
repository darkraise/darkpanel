// Package npm is the Nginx Proxy Manager module (spec section 6): the
// Darkpanel-owned proxy container and its proxy hosts and certificates.
package npm

import "github.com/darkraise/darkpanel/internal/core"

// ID is the module id.
const ID = "npm"

type module struct{}

// Module returns the Nginx Proxy Manager module.
func Module() core.Module { return module{} }

// Manifest declares the module. Every module is enabled by default;
// preflight decides whether it is available on a host (R9). The manifest
// declares no action, resource or check yet.
func (module) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: true}
}
