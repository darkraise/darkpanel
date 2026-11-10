package derp

import (
	"net/netip"
	"testing"
)

func TestW11c04ProbeRefusesInternalAddressForms(t *testing.T) {
	for _, s := range []string{
		"0.1.2.3", "192.0.0.1", "198.18.0.1", "198.19.255.254", "240.0.0.1", "255.255.255.255",
		"::1.2.3.4", "64:ff9b::7f00:1", "64:ff9b:1::1", "2001:0:1::1", "2002:7f00:1::1",
	} {
		if nodeProbeAllowedAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s is allowed, want it refused", s)
		}
	}
	for _, s := range []string{"93.184.216.34", "203.0.113.9", "2606:4700::1"} {
		if !nodeProbeAllowedAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s is refused, want it allowed", s)
		}
	}
}
