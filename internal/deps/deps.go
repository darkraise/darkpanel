//go:build deps

// Package deps keeps modules in go.mod until code imports them; go mod tidy
// considers every build tag, so this file is enough. Delete it once each
// module has an importer.
package deps

import (
	_ "aead.dev/minisign"
	_ "github.com/godbus/dbus/v5"
	_ "github.com/hashicorp/yamux"
)
