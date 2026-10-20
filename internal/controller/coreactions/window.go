package coreactions

import "github.com/darkraise/darkpanel/internal/update"

// ProtocolVersion is the agent protocol this controller speaks (ADR 0003).
const ProtocolVersion = 1

// Supported reports whether an agent running agentVersion over protocol is
// inside the support window of a controller running controllerVersion (spec
// section 5): protocol 1, the controller's major version, and its minor
// version or the one before. An agent outside the window, older or newer,
// accepts only the core.update actions. A version that does not parse, such
// as a development build's "dev", is equal only to the same string.
func Supported(controllerVersion, agentVersion string, protocol int) bool {
	if protocol != ProtocolVersion {
		return false
	}
	if agentVersion == controllerVersion {
		return true
	}
	c, err := update.ParseVersion(controllerVersion)
	if err != nil {
		return false
	}
	a, err := update.ParseVersion(agentVersion)
	if err != nil {
		return false
	}
	return a.Major == c.Major && (a.Minor == c.Minor || a.Minor+1 == c.Minor)
}
