// Package cloudflare is the Cloudflare Tunnel module (spec section 7): the
// account, tunnels, connectors and public hostnames.
package cloudflare

import "github.com/darkraise/darkpanel/internal/core"

// ID is the module id.
const ID = "cloudflare"

type module struct{}

// Module returns the Cloudflare Tunnel module.
func Module() core.Module { return module{} }

// Manifest declares the module. Every module is enabled by default;
// preflight decides whether it is available on a host (R9). The manifest
// declares no action, resource or check yet.
func (module) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: true}
}
