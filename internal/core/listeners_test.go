package core

import "testing"

// The listener states are stored in core_listeners and the client headers
// are what NPM's proxy host and Cloudflare set, so their values are fixed.
func TestListenerConstantValues(t *testing.T) {
	for got, want := range map[string]string{
		ListenerOpen:       "open",
		ListenerRevoked:    "revoked",
		ListenerClosed:     "closed",
		ClientHeaderNPM:    "X-Darkpanel-Client",
		ClientHeaderTunnel: "Cf-Connecting-IP",
	} {
		if got != want {
			t.Errorf("constant = %q, want %q", got, want)
		}
	}
}
