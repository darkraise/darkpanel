// Package proxy holds the NPM exposure rules that need no I/O: the custom location Darkpanel sends,
// the upstreams reserved for the admin and panel exposures, and the networks a proxy target may use.
package proxy

import (
	"strings"

	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

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

// OperatorConfig returns the lines of a "/" location's advanced configuration other than the X-Darkpanel-Client line
// that Location writes for every host, in order and joined by "\n", or "" when none remain. The panel's read timeout
// is kept: npm.proxyhost.update never writes the panel's host, so on any host it updates that line is the operator's.
// Empty and whitespace-only lines are dropped.
func OperatorConfig(advancedConfig string) string {
	var kept []string
	for _, l := range strings.Split(advancedConfig, "\n") {
		// Whole lines are compared untrimmed: an indented copy of a module line inside an operator's block is the
		// operator's.
		if l == clientHeaderLine || strings.TrimSpace(l) == "" {
			continue
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n")
}
