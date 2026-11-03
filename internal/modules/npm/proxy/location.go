// Package proxy holds the NPM exposure rules that need no I/O: the custom location Darkpanel sends,
// the upstreams reserved for the admin and panel exposures, and the networks a proxy target may use.
package proxy

import "github.com/darkraise/darkpanel/internal/modules/npm/npmapi"

const (
	clientHeaderLine = "proxy_set_header X-Darkpanel-Client $realip_remote_addr;"
	panelTimeoutLine = "proxy_read_timeout 3600s;"
)

// Location returns the custom location at "/" that forwards to forwardScheme://forwardHost:forwardPort.
// Its advanced configuration sets the X-Darkpanel-Client header and, when panel is true, a one-hour
// read timeout.
func Location(forwardScheme, forwardHost string, forwardPort int, panel bool) npmapi.Location {
	// NPM's custom-location template already sets Host, X-Forwarded-*, X-Real-IP and the websocket
	// headers, so any of those repeated here would reach the upstream twice.
	cfg := clientHeaderLine
	if panel {
		cfg += "\n" + panelTimeoutLine
	}
	return npmapi.Location{
		Path:           "/",
		ForwardScheme:  forwardScheme,
		ForwardHost:    forwardHost,
		ForwardPort:    forwardPort,
		AdvancedConfig: cfg,
	}
}
