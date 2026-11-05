package install

import (
	"net"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

func fz10Head(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

// FuzzCheckAgentAddress checks that CheckAgentAddress accepts only host:port with a port from 1 to 65535 and
// no whitespace, quote, percent sign or dollar sign, so the value can sit unquoted in a unit file.
func FuzzCheckAgentAddress(f *testing.F) {
	for _, s := range []string{
		"10.77.0.10:7443", "[2001:db8::1]:7443", "host.example:1", "host.example:65535", "a:65536", "a:0", ":7443",
		"host:7443\n", "host :7443", `ho"st:1`, "ho%st:1", "ho$st:1", "ho'st:1", "host:+80", "host:0443", "[::1]", "", "[]:80",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if CheckAgentAddress(s) != nil {
			return
		}
		if strings.ContainsAny(s, "\"'`%$") || strings.ContainsFunc(s, unicode.IsSpace) {
			t.Fatalf("CheckAgentAddress accepted %q, which holds a character a unit file would read", fz10Head(s))
		}
		host, port, err := net.SplitHostPort(s)
		if n, perr := strconv.Atoi(port); err != nil || host == "" || perr != nil || n < 1 || n > 65535 {
			t.Fatalf("CheckAgentAddress accepted %q, which is not host:port with a port from 1 to 65535", fz10Head(s))
		}
	})
}
