// Package console is the console module (M11 spec section 5): the web terminal and file manager. It is off until an admin turns it on.
package console

import (
	"embed"
	"io/fs"

	"github.com/darkraise/darkpanel/internal/core"
)

// ID is the module id.
const ID = "console"

//go:embed migrations/agent/*.sql
var migrationsFS embed.FS

type module struct {
	terms *terminals // the host's open terminals
}

// Module returns the console module.
func Module() core.Module { return &module{terms: newTerminals()} }

// Manifest declares the module.
func (m *module) Manifest() core.Manifest {
	agent, err := fs.Sub(migrationsFS, "migrations/agent")
	if err != nil {
		panic(err)
	}
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: false, Migrations: map[core.Side]fs.FS{core.SideAgent: agent}}
}
