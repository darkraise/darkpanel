package proxy

import (
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const (
	npmContainerName = "darkpanel-npm"
	adminOwner       = "npm.admin.expose"
	panelOwner       = "panel.exposure.add"
)

// loopbackNames are the names Docker writes for the loopback into the NPM container's /etc/hosts,
// which nginx's getaddrinfo reads.
var loopbackNames = []string{"localhost", "ip6-localhost", "ip6-loopback"}

// Reserved holds the addresses and names whose upstreams only an exposure's own composite may create.
// NPMNames are the NPM container's other names (id, short id, hostname, network aliases), which Docker
// resolves to the container. A zero PanelNetwork or PanelGateway matches nothing.
type Reserved struct {
	NPMAddrs     []netip.Addr
	NPMNames     []string
	PanelNetwork netip.Prefix
	PanelGateway netip.Addr
}

// CheckUpstream refuses with npm.reserved, unless parent is npm.admin.expose, an upstream that points at
// the local host or at any port of NPM's own names and addresses, and, unless parent is
// panel.exposure.add, one inside the panel network or at the panel gateway. host is read as nginx reads
// a proxy_pass host; a name other than the loopback names and the NPM container's names passes here,
// and the caller resolves it and checks each answer with CheckResolved.
func CheckUpstream(r Reserved, host string, port int, parent string) error {
	name := normaliseHost(host)
	addr, isAddr := upstreamAddr(host, name)

	if parent != adminOwner {
		// Linux connects the unspecified address to the local host, so it reaches the loopback too.
		if !isAddr && slices.Contains(loopbackNames, name) || isAddr && (addr.IsLoopback() || addr.IsUnspecified()) {
			return reserved(host, port, "admin")
		}
		// NPM's backend API listens on port 3000 on every interface of its container, so every port of NPM's
		// names and addresses is reachable, not only the admin port.
		if !isAddr && isNPMName(r.NPMNames, name) || isAddr && isNPMAddr(r.NPMAddrs, addr) {
			return reserved(host, port, "admin")
		}
	}
	if parent != panelOwner && isAddr {
		inNetwork := r.PanelNetwork.IsValid() && r.PanelNetwork.Contains(addr)
		isGateway := r.PanelGateway.IsValid() && plainAddr(r.PanelGateway) == addr
		if inNetwork || isGateway {
			return reserved(host, port, "panel")
		}
	}
	return nil
}

// LiteralAddr reports whether nginx reads host as an address, so that a lookup of it would be pointless, and
// returns that address, unmapped and without a zone.
func LiteralAddr(host string) (netip.Addr, bool) {
	return upstreamAddr(host, normaliseHost(host))
}

// CheckResolved runs CheckUpstream on each answer the forward host name host resolved to, in order, and
// returns the first refusal as npm.reserved naming the host and the offending answer. No answers pass.
func CheckResolved(r Reserved, host string, answers []netip.Addr, port int, parent string) error {
	for _, a := range answers {
		text := plainAddr(a).String()
		if err := CheckUpstream(r, text, port, parent); err != nil {
			return errcode.New(errcode.NPMReserved, "forward host "+host+" resolves to "+text+": "+errcode.From(err).Detail)
		}
	}
	return nil
}

// upstreamAddr reads host as an address the way nginx does. nginx resolves a custom location's static
// proxy_pass host itself: first with its own four-part decimal parser, then through getaddrinfo,
// whose glibc numeric form accepts shortened, octal and hex addresses such as 127.1 or 0x7f.1.
// A host neither accepts is a name.
func upstreamAddr(host, name string) (netip.Addr, bool) {
	if a, err := netip.ParseAddr(name); err == nil {
		return plainAddr(a), true
	}
	if a, ok := parseNginxIPv4(strings.ToLower(trimBrackets(host))); ok {
		return a, true
	}
	return parseInetAton(name)
}

// parseNginxIPv4 follows nginx's ngx_inet_addr: decimal digits and exactly three dots, each part at
// most 255, leading zeros read as decimal and an empty part read as zero.
func parseNginxIPv4(s string) (netip.Addr, bool) {
	var b [4]byte
	part, dots := 0, 0
	for i := range len(s) {
		switch c := s[i]; {
		case '0' <= c && c <= '9':
			part = part*10 + int(c-'0')
			if part > 255 {
				return netip.Addr{}, false
			}
		case c == '.':
			if dots == 3 {
				return netip.Addr{}, false
			}
			b[dots] = byte(part) //nolint:gosec // G115: part is at most 255, checked above
			part = 0
			dots++
		default:
			return netip.Addr{}, false
		}
	}
	if dots != 3 {
		return netip.Addr{}, false
	}
	b[3] = byte(part)
	return netip.AddrFrom4(b), true
}

// parseInetAton follows glibc's inet_aton: one to four non-empty parts, each decimal, octal after a
// leading 0 or hex after 0x; every part but the last is one byte and the last fills the low bytes left.
func parseInetAton(s string) (netip.Addr, bool) {
	parts := strings.Split(s, ".")
	if len(parts) > 4 {
		return netip.Addr{}, false
	}
	var v uint32
	for i, p := range parts {
		n, ok := parseInetAtonPart(p)
		if !ok {
			return netip.Addr{}, false
		}
		shift := 8 * (3 - i)
		if i == len(parts)-1 {
			shift = 0
			if n>>(8*(5-len(parts))) != 0 {
				return netip.Addr{}, false
			}
		} else if n > 255 {
			return netip.Addr{}, false
		}
		v |= uint32(n) << shift //nolint:gosec // G115: ParseUint bounds n to 32 bits
	}
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}), true
}

func parseInetAtonPart(p string) (uint64, bool) {
	base, digits := 10, p
	if rest, ok := strings.CutPrefix(p, "0x"); ok {
		base, digits = 16, rest
	} else if len(p) > 1 && p[0] == '0' {
		base, digits = 8, p[1:]
	}
	if digits == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(digits, base, 32)
	return n, err == nil
}

func normaliseHost(host string) string {
	h := trimBrackets(host)
	h = strings.TrimSuffix(h, ".")
	return strings.ToLower(h)
}

func trimBrackets(host string) string {
	if inner, ok := strings.CutPrefix(host, "["); ok {
		if inner, ok := strings.CutSuffix(inner, "]"); ok {
			return inner
		}
	}
	return host
}

func plainAddr(a netip.Addr) netip.Addr {
	return a.Unmap().WithZone("")
}

func isNPMAddr(addrs []netip.Addr, addr netip.Addr) bool {
	for _, a := range addrs {
		if plainAddr(a) == addr {
			return true
		}
	}
	return false
}

// isNPMName also matches the name cut at each of its dots, last dot first, because Docker's embedded DNS
// resolves a.b.c as a.b.c, as a.b in network c and as a in network b.c (moby libnetwork Sandbox.ResolveName).
// The network part is not compared with NPM's networks, since NPM may join another network after the check.
func isNPMName(names []string, name string) bool {
	for {
		if name == npmContainerName {
			return true
		}
		for _, n := range names {
			if normaliseHost(n) == name {
				return true
			}
		}
		i := strings.LastIndex(name, ".")
		if i < 0 {
			return false
		}
		name = name[:i]
	}
}

func reserved(host string, port int, exposure string) error {
	upstream := net.JoinHostPort(trimBrackets(host), strconv.Itoa(port))
	return errcode.New(errcode.NPMReserved, "upstream "+upstream+" is reserved for the "+exposure+" exposure")
}
