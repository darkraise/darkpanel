package listen

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"

	"github.com/darkraise/darkpanel/internal/core/store"
)

// TailscaleInterface is the interface tailscaled creates.
const TailscaleInterface = "tailscale0"

var (
	tailscale4 = netip.MustParsePrefix("100.64.0.0/10")
	tailscale6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// TailscaleAddrs returns the addresses inside 100.64.0.0/10 or
// fd7a:115c:a1e0::/48, sorted and without repeats, or nil when there are none.
func TailscaleAddrs(list []netip.Addr) []netip.Addr {
	var out []netip.Addr
	for _, a := range list {
		a = a.Unmap()
		if (tailscale4.Contains(a) || tailscale6.Contains(a)) && !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, netip.Addr.Compare)
	return out
}

// InterfaceAddrs lists the addresses of the named interface; present is
// false when no interface has that name.
type InterfaceAddrs func(name string) (addrs []netip.Addr, present bool, err error)

// SystemInterfaceAddrs is the production InterfaceAddrs.
func SystemInterfaceAddrs(name string) ([]netip.Addr, bool, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, false, fmt.Errorf("listen: list interfaces: %w", err)
	}
	i := slices.IndexFunc(ifs, func(ifi net.Interface) bool { return ifi.Name == name })
	if i < 0 {
		return nil, false, nil
	}
	list, err := ifs[i].Addrs()
	if err != nil {
		return nil, true, fmt.Errorf("listen: addresses of %s: %w", name, err)
	}
	var out []netip.Addr
	for _, a := range list {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if addr, ok := netip.AddrFromSlice(ipnet.IP); ok {
			out = append(out, addr.Unmap())
		}
	}
	return out, true, nil
}

const metaTailscaleAddrs = "tailscale_addrs"

// LoadTailscaleAddrs returns the Tailscale addresses saved in core_meta, or
// nil when none are saved.
func LoadTailscaleAddrs(ctx context.Context, db *store.DB) ([]netip.Addr, error) {
	var raw string
	err := db.Read().QueryRowContext(ctx, `SELECT value FROM core_meta WHERE key = ?`, metaTailscaleAddrs).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("listen: read core_meta %s: %w", metaTailscaleAddrs, err)
	}
	var list []netip.Addr
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, fmt.Errorf("listen: core_meta %s: %w", metaTailscaleAddrs, err)
	}
	return TailscaleAddrs(list), nil
}

// SaveTailscaleAddrs saves addrs in core_meta as a JSON array of address
// strings, such as ["100.64.0.1","fd7a:115c:a1e0::1"].
func SaveTailscaleAddrs(ctx context.Context, db *store.DB, addrs []netip.Addr) error {
	if addrs == nil {
		addrs = []netip.Addr{}
	}
	raw, err := json.Marshal(addrs)
	if err != nil {
		return fmt.Errorf("listen: encode %s: %w", metaTailscaleAddrs, err)
	}
	return db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO core_meta (key, value) VALUES (?, ?)
			 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, metaTailscaleAddrs, string(raw))
		if err != nil {
			return fmt.Errorf("listen: save core_meta %s: %w", metaTailscaleAddrs, err)
		}
		return nil
	})
}
