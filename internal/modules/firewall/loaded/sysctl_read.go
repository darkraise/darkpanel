package loaded

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

const (
	// module is the firewall's module id, which every env.Files call names.
	module       = "firewall"
	keyIPForward = "net/ipv4/ip_forward"
)

// sysctlIface is an interface name the agent's Files accepts as the
// <iface> segment of a sysctl key.
var sysctlIface = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,15}$`)

// sysctlParams are the supported per-interface keys (spec 2.1), the
// interface written as %s.
var sysctlParams = []string{
	"net/ipv6/conf/%s/forwarding",
	"net/ipv6/conf/%s/accept_ra",
	"net/ipv4/conf/%s/rp_filter",
	"net/ipv4/conf/%s/accept_source_route",
}

// sysctlKeys returns the supported keys to read: net/ipv4/ip_forward, then
// the per-interface keys of all, default and each non-loopback interface
// in the order given. An interface whose name cannot be a key segment, or
// that would read as all or default, is not read and gets a reason
// instead: its reverse-path filter would be unknown.
func sysctlKeys(ifaces []netstat.Interface) (keys, reasons []string) {
	names := []string{"all", "default"}
	for _, i := range ifaces {
		switch {
		case i.Loopback:
		case i.Name == "all" || i.Name == "default" || !sysctlIface.MatchString(i.Name):
			reasons = append(reasons, fmt.Sprintf("interface %q: Darkpanel cannot read its sysctl values", truncate(i.Name)))
		default:
			names = append(names, i.Name)
		}
	}
	keys = []string{keyIPForward}
	for _, n := range names {
		for _, p := range sysctlParams {
			keys = append(keys, fmt.Sprintf(p, n))
		}
	}
	return keys, reasons
}

// readSysctl reads every key through files. A key the kernel lacks (IPv6
// disabled, an interface that went away) is left out; any other failure is
// an error.
func readSysctl(ctx context.Context, files core.FileWriter, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		v, err := files.ReadSysctl(ctx, module, k)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("loaded: %w", err)
		}
		out[k] = v
	}
	return out, nil
}
