// Package proxy holds the NPM exposure rules that need no I/O: the custom location Darkpanel sends,
// the upstreams reserved for the admin and panel exposures, and the networks a proxy target may use.
package proxy

import (
	"strings"
	"unicode"

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

// OperatorConfig returns the lines of a "/" location's advanced configuration other than any X-Darkpanel-Client line,
// in order and joined by "\n", or "" when none remain. Location writes that header for every host, and nginx would
// send a second copy an operator wrote, in whatever case or spacing, beside it. The panel's read timeout is kept:
// npm.proxyhost.update never writes the panel's host, so on any host it updates that line is the operator's. Empty and
// whitespace-only lines are dropped.
func OperatorConfig(advancedConfig string) string {
	var kept []string
	for _, l := range strings.Split(advancedConfig, "\n") {
		if strings.TrimSpace(l) == "" || clientHeaderDirective(l) {
			continue
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n")
}

// clientHeaderDirective reports whether line is one proxy_set_header directive for X-Darkpanel-Client and nothing else
// but a trailing comment. A line holding more is kept, since dropping it would drop the operator's other text.
func clientHeaderDirective(line string) bool {
	directive, rest := cutToken(line)
	header, rest := cutToken(rest)
	if len(header) >= 2 && (header[0] == '"' || header[0] == '\'') && header[len(header)-1] == header[0] {
		header = header[1 : len(header)-1]
	}
	if !strings.EqualFold(directive, "proxy_set_header") || !strings.EqualFold(header, "x-darkpanel-client") {
		return false
	}
	value, after, ok := strings.Cut(rest, ";")
	value = strings.TrimSpace(value)
	if !ok || value == "" || strings.ContainsFunc(value, unicode.IsSpace) {
		return false
	}
	after = strings.TrimSpace(after)
	return after == "" || after[0] == '#'
}

func cutToken(s string) (token, rest string) {
	s = strings.TrimLeftFunc(s, unicode.IsSpace)
	if i := strings.IndexFunc(s, unicode.IsSpace); i >= 0 {
		return s[:i], s[i:]
	}
	return s, ""
}
