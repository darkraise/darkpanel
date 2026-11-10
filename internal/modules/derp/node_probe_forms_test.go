package derp

import (
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestW11c04ProbeRefusesInternalAddressForms(t *testing.T) {
	for _, s := range []string{
		"0.1.2.3", "192.0.0.1", "198.18.0.1", "198.19.255.254", "240.0.0.1", "255.255.255.255",
		"::1.2.3.4", "64:ff9b::7f00:1", "64:ff9b:1::1", "2001:0:1::1", "2002:7f00:1::1",
		"::ffff:0:7f00:1", "fec0::1",
		"::ffff:0.1.2.3", "::ffff:192.0.0.1", "::ffff:198.18.0.1", "::ffff:240.0.0.1",
		"0.255.255.255", "192.0.0.255", "198.19.255.255",
		"::ffff:ffff", "64:ff9b::ffff:ffff", "64:ff9b:1:ffff:ffff:ffff:ffff:ffff",
		"2001:0:ffff:ffff:ffff:ffff:ffff:ffff", "2002:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
		"::ffff:0:ffff:ffff", "feff:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
	} {
		if nodeProbeAllowedAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s is allowed, want it refused", s)
		}
	}
	for _, s := range []string{
		"93.184.216.34", "203.0.113.9", "2606:4700::1",
		"1.0.0.1", "192.0.1.1", "198.20.0.1", "223.255.255.255", "::ffff:93.184.216.34",
		"64:ff9b:2::1", "2001:1::1", "2003::1",
	} {
		if !nodeProbeAllowedAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s is refused, want it allowed", s)
		}
	}
}

func TestW11c04RefusalsNameTheSpecialPurposeForms(t *testing.T) {
	for _, in := range []NodeProbeInput{
		{HostName: "198.18.0.1"},
		{HostName: "derp.example.com", Address: "2002:7f00:1::1"},
	} {
		err := nodeProbeCheck(in)
		fields := errcode.From(err).Fields
		if len(fields) != 1 || !strings.Contains(fields[0].Message, "special-purpose") {
			t.Errorf("%+v: %v (fields %+v), want a refusal that names the special-purpose forms", in, err, fields)
		}
	}
	var d dp
	d.d.resolve = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("64:ff9b::7f00:1")}, nil
	}
	if _, err := d.nodeProbeResolve(context.Background(), "derp.example.com"); err == nil || !strings.Contains(err.Error(), "special-purpose") {
		t.Errorf("resolve = %v, want a refusal that names the special-purpose forms", err)
	}
}
