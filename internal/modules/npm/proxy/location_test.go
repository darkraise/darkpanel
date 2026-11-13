package proxy

import (
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

const pxClientLine = "proxy_set_header X-Darkpanel-Client $realip_remote_addr;"

func TestPxLocationAddsOnlyTheClientHeader(t *testing.T) {
	got := Location("http", "web", 8080, false)
	want := npmapi.Location{Path: "/", ForwardScheme: "http", ForwardHost: "web", ForwardPort: 8080, AdvancedConfig: pxClientLine}
	if got != want {
		t.Errorf("Location = %+v, want %+v", got, want)
	}
}

func TestPxLocationForThePanelAddsTheReadTimeoutOnASecondLine(t *testing.T) {
	got := Location("https", "172.31.255.1", 9443, true)
	want := npmapi.Location{
		Path: "/", ForwardScheme: "https", ForwardHost: "172.31.255.1", ForwardPort: 9443,
		AdvancedConfig: pxClientLine + "\nproxy_read_timeout 3600s;",
	}
	if got != want {
		t.Errorf("Location = %+v, want %+v", got, want)
	}
}

// The NPM template of a custom location already sets Host, X-Forwarded-*, X-Real-IP, Upgrade and Connection;
// repeating one would send two of them upstream.
func TestPxLocationNeverEmitsAHeaderTheTemplateSets(t *testing.T) {
	for _, panel := range []bool{false, true} {
		cfg := Location("http", "web", 80, panel).AdvancedConfig
		if strings.HasSuffix(cfg, "\n") {
			t.Errorf("panel=%t: %q ends with a newline", panel, cfg)
		}
		lines := strings.Split(cfg, "\n")
		if len(lines) != map[bool]int{false: 1, true: 2}[panel] {
			t.Errorf("panel=%t: %d lines, want %d", panel, len(lines), map[bool]int{false: 1, true: 2}[panel])
		}
		for _, line := range lines {
			f := strings.Fields(line)
			client := len(f) == 3 && f[0] == "proxy_set_header" && f[1] == "X-Darkpanel-Client"
			timeout := len(f) == 2 && f[0] == "proxy_read_timeout"
			if !client && !timeout {
				t.Errorf("panel=%t: line %q is neither the client header nor the read timeout", panel, line)
			}
		}
	}
}

func TestW12aOperatorConfig(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"the panel's read timeout is the operator's on an updated host", clientHeaderLine + "\n" + panelTimeoutLine, panelTimeoutLine},
		{"an indented module line goes and blank lines go", clientHeaderLine + "\n  " + clientHeaderLine + "\n\n \nallow 10.0.0.0/8;", "allow 10.0.0.0/8;"},
		{"only the module's line", clientHeaderLine, ""},
		{"an operator line in another case", "Proxy_Set_Header X-DARKPANEL-CLIENT $remote_addr;\nallow 10.0.0.0/8;", "allow 10.0.0.0/8;"},
		{"a tab and repeated spaces", "proxy_set_header\tX-Darkpanel-Client   $remote_addr;", ""},
		{"a quoted header name", `proxy_set_header "X-Darkpanel-Client" $remote_addr; # set by hand`, ""},
		{"a line holding more than the directive is kept", "proxy_set_header X-Darkpanel-Client $x; }", "proxy_set_header X-Darkpanel-Client $x; }"},
		{"another header is kept", "proxy_set_header X-Darkpanel-Clients $x;", "proxy_set_header X-Darkpanel-Clients $x;"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := OperatorConfig(c.in); got != c.want {
				t.Errorf("OperatorConfig(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
