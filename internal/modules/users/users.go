// Package users is the users and SSH module (M11 spec section 3): local accounts, SSH keys and the SSH daemon.
package users

import "github.com/darkraise/darkpanel/internal/core"

// ID is the module id.
const ID = "users"

type module struct{}

// Module returns the users module.
func Module() core.Module { return module{} }

// Manifest declares the module.
func (module) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: true}
}
