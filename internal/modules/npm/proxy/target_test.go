package proxy

import (
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func pxEndpoints() map[string]Endpoint {
	return map[string]Endpoint{
		"bridge":   {Driver: "bridge", UserDefined: false},
		"app":      {Driver: "bridge", UserDefined: true},
		"host":     {Driver: "host", UserDefined: false},
		"overlay1": {Driver: "overlay", UserDefined: true},
		"lan":      {Driver: "macvlan", UserDefined: true},
	}
}

func TestPxTarget(t *testing.T) {
	for _, tc := range []struct {
		name      string
		endpoints map[string]Endpoint
		network   string
		refuse    bool
	}{
		{"a user-defined bridge the container is on", pxEndpoints(), "app", false},
		{"the default bridge", pxEndpoints(), "bridge", true},
		{"the host network", pxEndpoints(), "host", true},
		{"a user-defined overlay", pxEndpoints(), "overlay1", true},
		{"a user-defined macvlan", pxEndpoints(), "lan", true},
		{"a network the container is not on", pxEndpoints(), "other", true},
		{"an empty network name", pxEndpoints(), "", true},
		{"a container on no network", nil, "app", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Target(tc.endpoints, tc.network)
			if !tc.refuse {
				if err != nil {
					t.Fatalf("Target(%q) = %v, want nil", tc.network, err)
				}
				return
			}
			e := errcode.From(err)
			if e.Code != errcode.NPMUnsupportedTarget || e.Detail != "connect the container to a user-defined network" {
				t.Fatalf("Target(%q) = %v, want npm.unsupported_target with the reason \"connect the container to a user-defined network\"", tc.network, err)
			}
		})
	}
}

func TestPxFirstTargetNetwork(t *testing.T) {
	for _, tc := range []struct {
		name      string
		endpoints map[string]Endpoint
		want      string
		ok        bool
	}{
		{"the first supported network in name order", map[string]Endpoint{
			"zeta": {Driver: "bridge", UserDefined: true}, "alpha": {Driver: "bridge", UserDefined: true}, "bridge": {Driver: "bridge"},
		}, "alpha", true},
		{"unsupported networks sort first but are skipped", map[string]Endpoint{
			"0-overlay": {Driver: "overlay", UserDefined: true}, "1-host": {Driver: "host"}, "b-net": {Driver: "bridge", UserDefined: true}, "a-bridge": {Driver: "bridge"},
		}, "b-net", true},
		{"only the default bridge", map[string]Endpoint{"bridge": {Driver: "bridge"}}, "", false},
		{"no endpoints", map[string]Endpoint{}, "", false},
		{"a nil map", nil, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := FirstTargetNetwork(tc.endpoints)
			if got != tc.want || ok != tc.ok {
				t.Errorf("FirstTargetNetwork = %q, %t; want %q, %t", got, ok, tc.want, tc.ok)
			}
			if ok && Target(tc.endpoints, got) != nil {
				t.Errorf("the network %q it picked is refused by Target", got)
			}
		})
	}
}
