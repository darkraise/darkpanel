package schema

import (
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// addrGuardedIn is the input of an action with guards.
type addrGuardedIn struct {
	core.Guarded
	Name string `json:"name"`
}

func TestGuardedSchema(t *testing.T) {
	v := NewValidator()
	def := core.DefineRead(core.ActionSpec{Name: "demo.guarded.get", Version: 1, Side: core.SideAgent},
		fixtureHandler[addrGuardedIn, fixtureStatus]{})
	addr, addrPort := netip.MustParseAddr, netip.MustParseAddrPort
	contexts := map[string]core.GuardContext{
		"IPv4": {
			Client: &core.Peer{Addr: addr("192.0.2.10"), Port: 50000},
			Via: &core.ProxyPath{ExposureID: "e1", Kind: "npm", ProxyAddr: addr("172.18.0.2"),
				ListenerAddr: addrPort("172.18.0.1:8443"), Public: []netip.AddrPort{addrPort("203.0.113.5:443")}},
			PanelListen: []netip.AddrPort{addrPort("0.0.0.0:8443")},
			AgentListen: []netip.AddrPort{addrPort("192.0.2.1:7443")},
			AgentPeers:  []netip.Addr{addr("198.51.100.4")},
		},
		"IPv6": {
			Client: &core.Peer{Addr: addr("2001:db8::10"), Port: 50000},
			Via: &core.ProxyPath{ExposureID: "e1", Kind: "tunnel", ProxyAddr: addr("fd00::2"),
				ListenerAddr: addrPort("[fd00::1]:8443"), Public: []netip.AddrPort{addrPort("[2001:db8::1]:443")}},
			PanelListen: []netip.AddrPort{addrPort("[::]:8443")},
			AgentListen: []netip.AddrPort{addrPort("[2001:db8::1]:7443")},
			AgentPeers:  []netip.Addr{addr("2001:db8::4")},
		},
	}
	for name, g := range contexts {
		raw, err := json.Marshal(addrGuardedIn{Guarded: core.Guarded{Guard: &g, Overrides: []string{"demo.lockout"}}, Name: "x"})
		if err != nil {
			t.Fatal(err)
		}
		if err := v.ValidateInput(def, raw); err != nil {
			t.Errorf("%s: ValidateInput(%s) = %v, want nil", name, raw, err)
		}
	}
	if err := v.ValidateInput(def, json.RawMessage(`{"name":"x","overrides":["a"]}`)); err != nil {
		t.Errorf("overrides of strings: %v, want nil", err)
	}
	tests := []struct {
		name, raw     string
		path, message string
	}{
		{"unknown guard member", `{"name":"x","guard":{"client":{"addr":"192.0.2.10","port":1},"extra":1}}`, "/guard/extra", "unknown property"},
		{"override that is not a string", `{"name":"x","overrides":[1]}`, "/overrides/0", "got number, want string"},
		{"bad agent peer", `{"name":"x","guard":{"agentPeers":["192.0.2.300"]}}`, "/guard/agentPeers/0", `value fails the "format" constraint`},
	}
	for _, tt := range tests {
		err := v.ValidateInput(def, json.RawMessage(tt.raw))
		var coded *errcode.Error
		if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput {
			t.Errorf("%s: err = %v, want %s", tt.name, err, errcode.InvalidInput)
			continue
		}
		if want := (errcode.FieldError{Path: tt.path, Message: tt.message}); !slices.Contains(coded.Fields, want) {
			t.Errorf("%s: fields = %+v, want %+v", tt.name, coded.Fields, want)
		}
	}
}
