// Package docker is the Docker Engine module (spec section 3): containers,
// images, networks, volumes and Compose projects.
package docker

import (
	"io/fs"

	"github.com/darkraise/darkpanel/internal/core"
)

// ID is the module id.
const ID = "docker"

type module struct {
	k     dk
	pulls *pullHub
}

// newModule builds the module over k. Nothing here calls the daemon:
// the registry is built on hosts without Docker and by the schema
// generator.
func newModule(k dk, clock core.Clock) module {
	return module{k: k, pulls: newPullHub(clock)}
}

// Module returns the Docker module.
func Module() core.Module { return newModule(productionDK(), core.SystemClock{}) }

// Manifest declares the module's actions, resources, owned directory and
// migrations. Every module is enabled by default; preflight decides
// whether it is available on a host (R9): the Docker daemon must answer
// with a supported API version.
func (m module) Manifest() core.Manifest {
	return core.Manifest{
		ID:             ID,
		Version:        1,
		DefaultEnabled: true,
		Actions:        m.actions(),
		Resources:      m.resources(),
		Preflight:      m.k.preflight(),
		OwnedPaths:     ownedPaths(),
		Migrations:     map[core.Side]fs.FS{core.SideAgent: agentMigrations()},
	}
}

func (m module) actions() []core.ActionDef {
	k := m.k
	return []core.ActionDef{
		k.containerStart(), k.containerStop(), k.containerRestart(), k.containerRemove(),
		k.containerInspect(), k.containerLogs(), k.containerRevealEnv(),
		k.imagePull(m.pulls), k.imageRemove(), k.imagePrune(),
		k.networkCreate(), k.networkRemove(), k.networkConnect(), k.networkDisconnect(),
		k.volumeCreate(), k.volumeRemove(),
		k.composeUp(), k.composeDown(), k.composePull(), k.composeRestart(),
	}
}

func (m module) resources() []core.ResourceDef {
	k := m.k
	return []core.ResourceDef{
		k.engineResource(), k.containersResource(), k.imagesResource(),
		k.networksResource(), k.volumesResource(), k.projectsResource(),
	}
}

// Streams are the container log and stats streams and the image pull
// stream. The pull stream reads the same hub docker.image.pull publishes
// to, so a module value has exactly one hub.
func (m module) Streams() []core.StreamDef {
	return []core.StreamDef{m.k.logsStream(), m.k.statsStream(), m.k.pullStreamDef(m.pulls)}
}
