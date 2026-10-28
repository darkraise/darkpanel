package dockerfw

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

func setPrefixes(t *testing.T, cidrs ...string) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		out = append(out, netip.MustParsePrefix(c))
	}
	return out
}

// setUnmasked is a prefix whose host bits are set, which ParsePrefix keeps.
func setUnmasked(addr string, bits int) netip.Prefix {
	return netip.PrefixFrom(netip.MustParseAddr(addr), bits)
}

func setExemption(over func(*Exemption)) Exemption {
	e := Exemption{
		InInterface:     "eth0",
		OrigDestination: netip.MustParseAddr("203.0.113.5"),
		OrigPort:        2222,
		Destination:     netip.MustParseAddr("10.8.0.5"),
		Proto:           "tcp",
		Port:            22,
	}
	if over != nil {
		over(&e)
	}
	return e
}

func TestSetDefaultSettings(t *testing.T) {
	s := DefaultSettings()
	want := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "fc00::/7", "fe80::/10"}
	var got []string
	for _, p := range s.PrivateSources {
		got = append(got, p.String())
	}
	if !slices.Equal(got, want) {
		t.Fatalf("PrivateSources = %v, want %v", got, want)
	}
	if s.ForwardExemptions == nil || len(s.ForwardExemptions) != 0 {
		t.Fatalf("ForwardExemptions = %#v, want an empty non-nil list so the JSON is []", s.ForwardExemptions)
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("the defaults do not validate: %v", err)
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON := `{"privateSources":["10.0.0.0/8","172.16.0.0/12","192.168.0.0/16","100.64.0.0/10","fc00::/7","fe80::/10"],"forwardExemptions":[]}`
	if string(data) != wantJSON {
		t.Fatalf("JSON = %s, want %s", data, wantJSON)
	}
	s.PrivateSources[0] = netip.MustParsePrefix("192.0.2.0/24")
	if DefaultSettings().PrivateSources[0].String() != "10.0.0.0/8" {
		t.Fatal("DefaultSettings shares its slice between calls")
	}
}

func TestSetExemptionJSON(t *testing.T) {
	e := Exemption{
		OrigDestination: netip.MustParseAddr("203.0.113.5"),
		Destination:     netip.MustParseAddr("10.8.0.5"),
		Proto:           "udp",
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"origDestination":"203.0.113.5","destination":"10.8.0.5","proto":"udp"}`; string(data) != want {
		t.Fatalf("JSON = %s, want %s: the optional fields are omitted", data, want)
	}
	full := setExemption(nil)
	data, err = json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"inInterface":"eth0","origDestination":"203.0.113.5","origPort":2222,"destination":"10.8.0.5","proto":"tcp","port":22}`
	if string(data) != want {
		t.Fatalf("JSON = %s, want %s", data, want)
	}
	var back Exemption
	if err := json.Unmarshal(data, &back); err != nil || back != full {
		t.Fatalf("round trip = %+v, %v; want %+v", back, err, full)
	}
}

func TestSetValidate(t *testing.T) {
	many := func(n int) []netip.Prefix {
		out := make([]netip.Prefix, 0, n)
		for i := range n {
			out = append(out, netip.MustParsePrefix(fmt.Sprintf("10.%d.0.0/16", i)))
		}
		return out
	}
	cases := []struct {
		name  string
		edit  func(s *Settings)
		paths []string // nil: valid
	}{
		{"the defaults", func(_ *Settings) {}, nil},
		{"32 sources", func(s *Settings) { s.PrivateSources = many(32) }, nil},
		{"33 sources", func(s *Settings) { s.PrivateSources = many(33) }, []string{"/privateSources"}},
		{"no sources", func(s *Settings) { s.PrivateSources = nil }, nil},
		{"IPv4 /8 is the shortest", func(s *Settings) { s.PrivateSources = setPrefixes(t, "10.0.0.0/8") }, nil},
		{"IPv4 /7", func(s *Settings) { s.PrivateSources = setPrefixes(t, "10.0.0.0/8", "10.0.0.0/7") }, []string{"/privateSources/1"}},
		{"IPv4 default route", func(s *Settings) { s.PrivateSources = setPrefixes(t, "0.0.0.0/0") }, []string{"/privateSources/0"}},
		{"IPv6 /7 is the shortest", func(s *Settings) { s.PrivateSources = setPrefixes(t, "fc00::/7") }, nil},
		{"IPv6 /6", func(s *Settings) { s.PrivateSources = setPrefixes(t, "fc00::/6") }, []string{"/privateSources/0"}},
		{"IPv6 default route", func(s *Settings) { s.PrivateSources = setPrefixes(t, "10.0.0.0/8", "::/0") }, []string{"/privateSources/1"}},
		{"host bits set", func(s *Settings) { s.PrivateSources = []netip.Prefix{setUnmasked("10.1.2.3", 8)} }, []string{"/privateSources/0"}},
		{"invalid prefix", func(s *Settings) { s.PrivateSources = []netip.Prefix{{}} }, []string{"/privateSources/0"}},
		{"a host address", func(s *Settings) { s.PrivateSources = setPrefixes(t, "10.1.2.3/32") }, nil},
		{"IPv4-mapped IPv6", func(s *Settings) { s.PrivateSources = setPrefixes(t, "::ffff:10.0.0.0/104") }, []string{"/privateSources/0"}},
		{"duplicate source", func(s *Settings) {
			s.PrivateSources = setPrefixes(t, "10.0.0.0/8", "192.168.0.0/16", "10.0.0.0/8")
		}, []string{"/privateSources/2"}},
		{"overlapping sources are not duplicates", func(s *Settings) { s.PrivateSources = setPrefixes(t, "10.0.0.0/8", "10.1.0.0/16") }, nil},
		{"an exemption", func(s *Settings) { s.ForwardExemptions = []Exemption{setExemption(nil)} }, nil},
		{"an exemption with the required fields only", func(s *Settings) {
			s.ForwardExemptions = []Exemption{setExemption(func(e *Exemption) { e.InInterface, e.OrigPort, e.Port = "", 0, 0 })}
		}, nil},
		{"an IPv6 exemption", func(s *Settings) {
			s.ForwardExemptions = []Exemption{setExemption(func(e *Exemption) {
				e.OrigDestination, e.Destination, e.Proto = netip.MustParseAddr("2001:db8::5"), netip.MustParseAddr("fd00::5"), "udp"
			})}
		}, nil},
		{"interface with a wildcard", func(s *Settings) {
			s.ForwardExemptions = []Exemption{setExemption(func(e *Exemption) { e.InInterface = "wg+" })}
		}, nil},
		{"interface too long", func(s *Settings) {
			s.ForwardExemptions = []Exemption{setExemption(func(e *Exemption) { e.InInterface = "abcdefghijklmnop" })}
		}, []string{"/forwardExemptions/0/inInterface"}},
		{"interface leading dash", func(s *Settings) {
			s.ForwardExemptions = []Exemption{setExemption(func(e *Exemption) { e.InInterface = "-eth0" })}
		}, []string{"/forwardExemptions/0/inInterface"}},
		{"interface with a colon", func(s *Settings) {
			s.ForwardExemptions = []Exemption{setExemption(func(e *Exemption) { e.InInterface = "eth0:1" })}
		}, []string{"/forwardExemptions/0/inInterface"}},
		{"missing original destination", func(s *Settings) {
			s.ForwardExemptions = []Exemption{setExemption(func(e *Exemption) { e.OrigDestination = netip.Addr{} })}
		}, []string{"/forwardExemptions/0/origDestination"}},
		{"unspecified original destination", func(s *Settings) {
			s.ForwardExemptions = []Exemption{setExemption(func(e *Exemption) { e.OrigDestination = netip.IPv4Unspecified() })}
		}, []string{"/forwardExemptions/0/origDestination"}},
		{"missing destination", func(s *Settings) {
			s.ForwardExemptions = []Exemption{setExemption(func(e *Exemption) { e.Destination = netip.Addr{} })}
		}, []string{"/forwardExemptions/0/destination"}},
		{"unspecified destination", func(s *Settings) {
			s.ForwardExemptions = []Exemption{setExemption(func(e *Exemption) { e.Destination = netip.IPv6Unspecified() })}
		}, []string{"/forwardExemptions/0/destination"}},
		{"mixed families", func(s *Settings) {
			s.ForwardExemptions = []Exemption{setExemption(func(e *Exemption) { e.Destination = netip.MustParseAddr("fd00::5") })}
		}, []string{"/forwardExemptions/0/destination"}},
		{"IPv4-mapped destination", func(s *Settings) {
			s.ForwardExemptions = []Exemption{setExemption(func(e *Exemption) { e.Destination = netip.MustParseAddr("::ffff:10.8.0.5") })}
		}, []string{"/forwardExemptions/0/destination"}},
		{"zone on the original destination", func(s *Settings) {
			s.ForwardExemptions = []Exemption{setExemption(func(e *Exemption) {
				e.OrigDestination, e.Destination = netip.MustParseAddr("fe80::1%eth0"), netip.MustParseAddr("fd00::5")
			})}
		}, []string{"/forwardExemptions/0/origDestination"}},
		{"unknown protocol", func(s *Settings) {
			s.ForwardExemptions = []Exemption{setExemption(func(e *Exemption) { e.Proto = "icmp" })}
		}, []string{"/forwardExemptions/0/proto"}},
		{"missing protocol", func(s *Settings) {
			s.ForwardExemptions = []Exemption{setExemption(func(e *Exemption) { e.Proto = "" })}
		}, []string{"/forwardExemptions/0/proto"}},
		{"duplicate exemption", func(s *Settings) {
			s.ForwardExemptions = []Exemption{setExemption(nil), setExemption(func(e *Exemption) { e.Port = 23 }), setExemption(nil)}
		}, []string{"/forwardExemptions/2"}},
		{"several problems in order", func(s *Settings) {
			s.PrivateSources = setPrefixes(t, "10.0.0.0/7", "10.0.0.0/8")
			s.ForwardExemptions = []Exemption{
				setExemption(func(e *Exemption) { e.InInterface, e.Proto = "-x", "gre" }),
				setExemption(func(e *Exemption) { e.OrigDestination = netip.Addr{} }),
			}
		}, []string{"/privateSources/0", "/forwardExemptions/0/inInterface", "/forwardExemptions/0/proto", "/forwardExemptions/1/origDestination"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := DefaultSettings()
			c.edit(&s)
			err := s.Validate()
			if c.paths == nil {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			var e *errcode.Error
			if !errors.As(err, &e) || e.Code != errcode.InvalidInput {
				t.Fatalf("Validate() = %v, want %s", err, errcode.InvalidInput)
			}
			var got []string
			for _, f := range e.Fields {
				if f.Message == "" {
					t.Errorf("field %s has no message", f.Path)
				}
				got = append(got, f.Path)
			}
			if !slices.Equal(got, c.paths) {
				t.Fatalf("field paths = %q, want %q (%v)", got, c.paths, e.Fields)
			}
		})
	}
}

func TestSetPrivate(t *testing.T) {
	cases := []struct {
		addr string
		want string // "" for no range
	}{
		{"10.1.2.3", "10.0.0.0/8"},
		{"172.31.255.255", "172.16.0.0/12"},
		{"172.32.0.1", ""},
		{"192.168.1.1", "192.168.0.0/16"},
		{"100.64.0.1", "100.64.0.0/10"},
		{"100.128.0.1", ""},
		{"203.0.113.5", ""},
		{"fd7a:115c:a1e0::1", "fc00::/7"},
		{"fe80::1", "fe80::/10"},
		{"2001:db8::1", ""},
	}
	s := DefaultSettings()
	for _, c := range cases {
		t.Run(c.addr, func(t *testing.T) {
			p, ok := s.Private(netip.MustParseAddr(c.addr))
			if c.want == "" {
				if ok || p.IsValid() {
					t.Fatalf("Private(%s) = %v, %v; want no range", c.addr, p, ok)
				}
				return
			}
			if !ok || p.String() != c.want {
				t.Fatalf("Private(%s) = %v, %v; want %s", c.addr, p, ok, c.want)
			}
		})
	}
	nested := Settings{PrivateSources: setPrefixes(t, "10.0.0.0/8", "10.1.0.0/16")}
	if p, ok := nested.Private(netip.MustParseAddr("10.1.2.3")); !ok || p.String() != "10.0.0.0/8" {
		t.Errorf("Private with nested ranges = %v, %v; want the first, 10.0.0.0/8", p, ok)
	}
	if _, ok := (Settings{}).Private(netip.MustParseAddr("10.1.2.3")); ok {
		t.Error("Private with no ranges found one")
	}
}

func TestSetExemptionFamily(t *testing.T) {
	if got := setExemption(nil).Family(); got != fwstate.V4 {
		t.Errorf("IPv4 exemption family = %q", got)
	}
	v6 := setExemption(func(e *Exemption) {
		e.OrigDestination, e.Destination = netip.MustParseAddr("2001:db8::5"), netip.MustParseAddr("fd00::5")
	})
	if got := v6.Family(); got != fwstate.V6 {
		t.Errorf("IPv6 exemption family = %q", got)
	}
}

func TestSetBridge(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	cases := []struct {
		name string
		n    Network
		want string
	}{
		{"the default bridge", Network{ID: id, Name: "bridge", Bridge: ""}, "docker0"},
		{"the default bridge with its option", Network{ID: id, Name: "bridge", Bridge: "docker0"}, "docker0"},
		{"a user network", Network{ID: id, Name: "app"}, "br-0123456789ab"},
		{"a custom bridge name", Network{ID: id, Name: "custom", Bridge: "dpcustom"}, "dpcustom"},
		{"a custom name on the default network", Network{ID: id, Name: "bridge", Bridge: "mybr0"}, "mybr0"},
		{"a short id", Network{ID: "abc", Name: "app"}, "br-abc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Bridge(c.n); got != c.want {
				t.Fatalf("Bridge(%+v) = %q, want %q", c.n, got, c.want)
			}
		})
	}
	if got := Bridge(Network{ID: id, Name: "app"}); len(got) > 15 || !strings.HasPrefix(got, "br-") {
		t.Errorf("Bridge = %q: a Linux interface name holds at most 15 bytes", got)
	}
}
