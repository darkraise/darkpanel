package guard

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"regexp"
	"slices"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// forwardingKey matches the supported IPv6 forwarding keys of a loaded
// state's sysctl values.
var forwardingKey = regexp.MustCompile(`^net/ipv6/conf/[^/]+/forwarding$`)

// raFlows reports a change to IPv6 forwarding on a host whose default IPv6
// route was learned from router advertisements as one unknown flow: an
// interface with accept_ra 1 stops taking advertisements while it forwards,
// so the route expires and the IPv6 paths of the host go with it. The change
// is any forwarding value of st that differs from the live one; a key the
// kernel lacks (IPv6 is off, the interface is gone) is no change. The
// routes are read only when a value changes.
func raFlows(ctx context.Context, env core.Env, h Host, st fwstate.Loaded) ([]flow, error) {
	for _, key := range slices.Sorted(maps.Keys(st.Sysctl)) {
		if !forwardingKey.MatchString(key) {
			continue
		}
		live, err := env.Files.ReadSysctl(ctx, moduleID, key)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("firewall: read %s: %w", key, err)
		}
		if live == st.Sysctl[key] {
			continue
		}
		dumper, ok := h.(routeDumper)
		if !ok {
			return nil, errors.New("firewall: the host cannot list routes")
		}
		routes, err := dumper.Routes(netstat.IPv6)
		if err != nil {
			return nil, fmt.Errorf("firewall: read the IPv6 routes: %w", err)
		}
		for _, rt := range routes {
			if rt.Protocol == netstat.RouteProtoRA && rt.Dst.Bits() == 0 {
				return []flow{{label: "IPv6 router advertisements", unknown: fmt.Sprintf(
					"%s changes from %s to %s and the default IPv6 route was learned from router advertisements, which an interface stops accepting while it forwards",
					key, live, st.Sysctl[key])}}, nil
			}
		}
		return nil, nil
	}
	return nil, nil
}
