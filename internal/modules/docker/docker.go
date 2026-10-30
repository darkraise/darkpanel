// Package docker is the Docker Engine module (spec section 3): containers,
// images, networks, volumes and Compose projects.
package docker

import "github.com/darkraise/darkpanel/internal/core"

// ID is the module id.
const ID = "docker"

type module struct{ k dk }

// Module returns the Docker module.
func Module() core.Module { return module{k: productionDK()} }

// Manifest declares the module. Every module is enabled by default;
// preflight decides whether it is available on a host (R9): the Docker
// daemon must answer with a supported API version.
func (m module) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: true, Preflight: m.k.preflight()}
}
