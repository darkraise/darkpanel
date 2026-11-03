package proxy

import (
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const (
	npmContainerName = "darkpanel-npm"
	npmAdminPort     = 81
	adminOwner       = "npm.admin.expose"
	panelOwner       = "panel.exposure.add"
)

// Reserved holds the addresses whose upstreams only an exposure's own composite may create.
// A zero PanelNetwork or PanelGateway matches nothing.
type Reserved struct {
	NPMAddrs     []netip.Addr
	PanelNetwork netip.Prefix
	PanelGateway netip.Addr
}

// CheckUpstream refuses with npm.reserved an upstream that points at the local host, or at NPM's own
// admin port, unless parent is npm.admin.expose, and one inside the panel network or at the panel
// gateway unless parent is panel.exposure.add. A host name other than localhost and the NPM container's
// name is allowed: it resolves nothing.
func CheckUpstream(r Reserved, host string, port int, parent string) error {
	name := normaliseHost(host)
	addr, err := netip.ParseAddr(name)
	isAddr := err == nil
	if isAddr {
		addr = addr.Unmap()
	}

	if parent != adminOwner {
		// Linux connects the unspecified address to the local host, so it reaches the loopback too.
		if name == "localhost" || isAddr && (addr.IsLoopback() || addr.IsUnspecified()) {
			return reserved(host, port, "admin")
		}
		if port == npmAdminPort && (name == npmContainerName || isAddr && isNPMAddr(r.NPMAddrs, addr)) {
			return reserved(host, port, "admin")
		}
	}
	if parent != panelOwner && isAddr {
		inNetwork := r.PanelNetwork.IsValid() && r.PanelNetwork.Contains(addr)
		isGateway := r.PanelGateway.IsValid() && r.PanelGateway.Unmap() == addr
		if inNetwork || isGateway {
			return reserved(host, port, "panel")
		}
	}
	return nil
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

func isNPMAddr(addrs []netip.Addr, addr netip.Addr) bool {
	for _, a := range addrs {
		if a.Unmap() == addr {
			return true
		}
	}
	return false
}

func reserved(host string, port int, exposure string) error {
	upstream := net.JoinHostPort(trimBrackets(host), strconv.Itoa(port))
	return errcode.New(errcode.NPMReserved, "upstream "+upstream+" is reserved for the "+exposure+" exposure")
}
