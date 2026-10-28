// Package dockerfw holds the pure logic of the firewall's Docker integration
// (spec 2.6): its settings, bindings and container rules, the marked block,
// realization against container facts, detection and health. It reads no host
// state.
package dockerfw

import (
	"net/netip"
	"regexp"
	"slices"
	"strconv"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

// MaxPrivateSources is the most private source ranges Settings may hold.
const MaxPrivateSources = 32

// exemptionIfaceRe is the shape of an exemption's inbound interface: a Linux
// interface name of at most 15 bytes, with "+" as the netfilter wildcard.
var exemptionIfaceRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.+-]{0,14}$`)

// Exemption lets a foreign DNAT forward to a Docker address pass the block: it
// names the original destination the DNAT matches and the address it
// translates to.
type Exemption struct {
	// InInterface is the interface the traffic arrives on; empty matches any.
	InInterface string `json:"inInterface,omitempty"`
	// OrigDestination is the address the client connected to.
	OrigDestination netip.Addr `json:"origDestination"`
	// OrigPort is the port the client connected to; 0 matches any.
	OrigPort uint16 `json:"origPort,omitempty"`
	// Destination is the address the DNAT translates to.
	Destination netip.Addr `json:"destination"`
	// Proto is the transport protocol: tcp or udp.
	Proto string `json:"proto"`
	// Port is the translated port; 0 matches any.
	Port uint16 `json:"port,omitempty"`
}

// Settings is the Docker integration's configuration.
type Settings struct {
	// PrivateSources are the ranges whose traffic to containers is treated as
	// private and left to the container rules.
	PrivateSources []netip.Prefix `json:"privateSources"`
	// ForwardExemptions are the foreign DNAT forwards the block lets through.
	ForwardExemptions []Exemption `json:"forwardExemptions"`
}

// DefaultSettings returns the default private ranges, in spec order, and no
// exemptions. Every call returns slices of its own.
func DefaultSettings() Settings {
	return Settings{
		PrivateSources: []netip.Prefix{
			netip.MustParsePrefix("10.0.0.0/8"),
			netip.MustParsePrefix("172.16.0.0/12"),
			netip.MustParsePrefix("192.168.0.0/16"),
			netip.MustParsePrefix("100.64.0.0/10"),
			netip.MustParsePrefix("fc00::/7"),
			netip.MustParsePrefix("fe80::/10"),
		},
		ForwardExemptions: []Exemption{},
	}
}

// Validate returns nil, or an *errcode.Error with code errcode.InvalidInput
// that holds one field error per problem, in the order the settings list them.
func (s Settings) Validate() error {
	var fields []errcode.FieldError
	add := func(path, msg string) {
		fields = append(fields, errcode.FieldError{Path: path, Message: msg})
	}

	if len(s.PrivateSources) > MaxPrivateSources {
		add("/privateSources", "at most 32 private source ranges are allowed")
	}
	for i, p := range s.PrivateSources {
		path := "/privateSources/" + strconv.Itoa(i)
		switch {
		case !p.IsValid():
			add(path, "must be a valid CIDR range")
		case p.Addr().Is4In6():
			add(path, "must not be an IPv4-mapped IPv6 range")
		case p != p.Masked():
			add(path, "has host bits set; use "+p.Masked().String())
		case p.Addr().Is4() && p.Bits() < 8:
			add(path, "an IPv4 range must not be shorter than /8")
		case p.Addr().Is6() && p.Bits() < 7:
			add(path, "an IPv6 range must not be shorter than /7")
		case slices.Contains(s.PrivateSources[:i], p):
			add(path, "duplicates an earlier range")
		}
	}

	for i, e := range s.ForwardExemptions {
		path := "/forwardExemptions/" + strconv.Itoa(i)
		if e.InInterface != "" && !exemptionIfaceRe.MatchString(e.InInterface) {
			add(path+"/inInterface", "must be an interface name of at most 15 characters")
		}
		origProblem := exemptionAddrProblem(e.OrigDestination)
		if origProblem != "" {
			add(path+"/origDestination", origProblem)
		}
		destProblem := exemptionAddrProblem(e.Destination)
		if destProblem != "" {
			add(path+"/destination", destProblem)
		}
		if origProblem == "" && destProblem == "" && e.OrigDestination.Is4() != e.Destination.Is4() {
			add(path+"/destination", "must be of the same family as origDestination")
		}
		if e.Proto != "tcp" && e.Proto != "udp" {
			add(path+"/proto", "must be tcp or udp")
		}
		if slices.Contains(s.ForwardExemptions[:i], e) {
			add(path, "duplicates an earlier exemption")
		}
	}

	if len(fields) == 0 {
		return nil
	}
	err := errcode.New(errcode.InvalidInput, "the Docker settings are invalid")
	err.Fields = fields
	return err
}

// exemptionAddrProblem names the first reason a is not usable as an
// exemption address, or returns "".
func exemptionAddrProblem(a netip.Addr) string {
	switch {
	case !a.IsValid():
		return "must be an IP address"
	case a.Zone() != "":
		return "must not carry a zone"
	case a.Is4In6():
		return "must not be an IPv4-mapped IPv6 address"
	case a.IsUnspecified():
		return "must not be the unspecified address"
	}
	return ""
}

// Private returns the first private source range that holds a.
func (s Settings) Private(a netip.Addr) (netip.Prefix, bool) {
	for _, p := range s.PrivateSources {
		if p.Contains(a) {
			return p, true
		}
	}
	return netip.Prefix{}, false
}

// Family is the IP family of the exemption's destination.
func (e Exemption) Family() fwstate.Family {
	if e.Destination.Is6() {
		return fwstate.V6
	}
	return fwstate.V4
}
