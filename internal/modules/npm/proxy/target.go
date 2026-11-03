package proxy

import (
	"maps"
	"slices"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const unsupportedTargetReason = "connect the container to a user-defined network"

// Endpoint is one network a container is connected to, keyed by the network's name.
type Endpoint struct {
	Driver      string
	UserDefined bool
}

// Target refuses with npm.unsupported_target unless the container is on network and network is a
// user-defined bridge.
func Target(endpoints map[string]Endpoint, network string) error {
	if e, ok := endpoints[network]; ok && e.Driver == "bridge" && e.UserDefined {
		return nil
	}
	return errcode.New(errcode.NPMUnsupportedTarget, unsupportedTargetReason)
}

// FirstTargetNetwork returns the first network, in ascending name order, that Target accepts,
// or "", false when there is none.
func FirstTargetNetwork(endpoints map[string]Endpoint) (string, bool) {
	for _, name := range slices.Sorted(maps.Keys(endpoints)) {
		if Target(endpoints, name) == nil {
			return name, true
		}
	}
	return "", false
}
