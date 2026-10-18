// Package listen opens the controller's web and API listeners (spec section
// 13, ADR 0006): plain HTTP on loopback, HTTPS on the Tailscale addresses and
// on explicit binds, with a certificate from the controller's web authority
// (ADR 0010).
package listen

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// Default ports of the loopback and tailscale listeners.
const (
	LoopbackPort  = 8080
	TailscalePort = 8443
)

// Kind is the form of one --listen value.
type Kind uint8

// The --listen forms.
const (
	KindLoopback  Kind = iota + 1 // plain HTTP on 127.0.0.1:8080 and [::1]:8080
	KindTailscale                 // HTTPS on port 8443 of each Tailscale address
	KindHTTPS                     // https://<ip>:<port>
	KindHTTP                      // http://<loopback ip>:<port>
)

// Spec is one parsed --listen value. Addr is set for KindHTTPS and KindHTTP.
type Spec struct {
	Kind Kind
	Addr netip.AddrPort
}

// String returns the value as it is written on the command line.
func (s Spec) String() string {
	switch s.Kind {
	case KindLoopback:
		return "loopback"
	case KindTailscale:
		return "tailscale"
	case KindHTTPS:
		return "https://" + s.Addr.String()
	case KindHTTP:
		return "http://" + s.Addr.String()
	}
	return fmt.Sprintf("Kind(%d)", s.Kind)
}

// ParseSpecs parses the --listen values; none means the defaults, loopback
// and tailscale. Repeated values count once. It refuses plain HTTP on an
// address that is not loopback, and one address listed as both HTTP and
// HTTPS.
func ParseSpecs(values []string) ([]Spec, error) {
	if len(values) == 0 {
		return []Spec{{Kind: KindLoopback}, {Kind: KindTailscale}}, nil
	}
	var out []Spec
	for _, v := range values {
		s, err := parseSpec(v)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	scheme := map[netip.AddrPort]bool{}
	for _, s := range out {
		for _, e := range expand(s, nil) {
			if tls, seen := scheme[e.Addr]; seen && tls != e.TLS {
				return nil, fmt.Errorf("listen: %s is listed as both HTTP and HTTPS", e.Addr)
			}
			scheme[e.Addr] = e.TLS
		}
	}
	return out, nil
}

func parseSpec(v string) (Spec, error) {
	switch v {
	case "loopback":
		return Spec{Kind: KindLoopback}, nil
	case "tailscale":
		return Spec{Kind: KindTailscale}, nil
	}
	kind := KindHTTPS
	rest, ok := strings.CutPrefix(v, "https://")
	if !ok {
		kind = KindHTTP
		if rest, ok = strings.CutPrefix(v, "http://"); !ok {
			return Spec{}, badValue(v)
		}
	}
	ap, err := netip.ParseAddrPort(rest)
	if err != nil {
		return Spec{}, badValue(v)
	}
	addr := ap.Addr()
	switch {
	case addr.Zone() != "":
		return Spec{}, fmt.Errorf("listen: %q: the address must not carry a zone", v)
	case ap.Port() == 0:
		return Spec{}, fmt.Errorf("listen: %q needs a port between 1 and 65535", v)
	}
	addr = addr.Unmap()
	if addr.IsUnspecified() || addr.IsMulticast() {
		return Spec{}, fmt.Errorf("listen: %q: the address must be a specific address of this host", v)
	}
	if kind == KindHTTP && !addr.IsLoopback() {
		return Spec{}, fmt.Errorf("listen: %q: plain HTTP is allowed only on a loopback address; use https://%s", v, ap)
	}
	return Spec{Kind: kind, Addr: netip.AddrPortFrom(addr, ap.Port())}, nil
}

func badValue(v string) error {
	return fmt.Errorf("listen: %q is not loopback, tailscale, https://<ip>:<port> or http://<loopback ip>:<port>", v)
}

// HasTailscale reports whether specs include the tailscale listener.
func HasTailscale(specs []Spec) bool {
	return slices.ContainsFunc(specs, func(s Spec) bool { return s.Kind == KindTailscale })
}

// Endpoint is one address the controller listens on.
type Endpoint struct {
	Addr netip.AddrPort
	TLS  bool
	// FreeBind binds with IP_FREEBIND or IPV6_FREEBIND, so a Tailscale address
	// can be bound before tailscaled has configured it.
	FreeBind bool
	// Optional marks the IPv6 loopback endpoint: a host without that address
	// skips it with one log line instead of failing.
	Optional bool
}

// URL returns the endpoint as a base URL, such as https://100.64.0.1:8443.
func (e Endpoint) URL() string {
	if e.TLS {
		return "https://" + e.Addr.String()
	}
	return "http://" + e.Addr.String()
}

var (
	loopback4 = netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), LoopbackPort)
	loopback6 = netip.AddrPortFrom(netip.IPv6Loopback(), LoopbackPort)
)

// Endpoints expands specs into endpoints, with tailscale as the current
// Tailscale addresses. One address listed twice is one endpoint; it keeps
// FreeBind if either listing has it. The result is sorted by address.
func Endpoints(specs []Spec, tailscale []netip.Addr) []Endpoint {
	byAddr := map[netip.AddrPort]Endpoint{}
	add := func(e Endpoint) {
		if prev, ok := byAddr[e.Addr]; ok {
			e.FreeBind = e.FreeBind || prev.FreeBind
			e.Optional = e.Optional && prev.Optional
		}
		byAddr[e.Addr] = e
	}
	for _, s := range specs {
		for _, e := range expand(s, tailscale) {
			add(e)
		}
	}
	out := make([]Endpoint, 0, len(byAddr))
	for _, e := range byAddr {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b Endpoint) int { return a.Addr.Compare(b.Addr) })
	return out
}

func expand(s Spec, tailscale []netip.Addr) []Endpoint {
	switch s.Kind {
	case KindLoopback:
		return []Endpoint{{Addr: loopback4}, {Addr: loopback6, Optional: true}}
	case KindTailscale:
		out := make([]Endpoint, 0, len(tailscale))
		for _, a := range tailscale {
			out = append(out, Endpoint{Addr: netip.AddrPortFrom(a, TailscalePort), TLS: true, FreeBind: true})
		}
		return out
	case KindHTTPS:
		return []Endpoint{{Addr: s.Addr, TLS: true}}
	case KindHTTP:
		return []Endpoint{{Addr: s.Addr}}
	}
	return nil
}
