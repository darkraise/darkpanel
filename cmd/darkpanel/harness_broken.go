//go:build harnessbroken

package main

// A harnessbroken build passes selftest but reports a degraded controller and
// an agent that never reaches it, so an update to it fails its health check
// and rolls back.
func init() {
	healthStatus = "degraded"
	neverConnected = true
}
