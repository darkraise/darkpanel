package fleet

import (
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"net/netip"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// tailnetReader is the principal the registry reads a connecting host's
// tailscale.status as.
var tailnetReader = core.Principal{Actor: "system:fleet", Scope: core.ScopeAdmin}

// tailnetReadTimeout bounds the tailscale.status read at connect.
const tailnetReadTimeout = 30 * time.Second

// maxTailnetAddrs bounds the addresses one status answer may add.
const maxTailnetAddrs = 16

// Tailscale's address ranges: the CGNAT block it assigns IPv4 addresses
// from and its IPv6 ULA prefix.
var (
	tailnetV4 = netip.MustParsePrefix("100.64.0.0/10")
	tailnetV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// tailnetStatus is the part of tailscale.status the registry records.
type tailnetStatus struct {
	Self *struct {
		ID        string   `json:"id"`
		Addresses []string `json:"addresses"`
	} `json:"self"`
}

// recordTailnet upserts hostID's tailnet addresses in core_host_tailnet: the
// addresses and stable id of its tailscale.status, and the session's remote
// address when it is a tailnet address. Rows are never removed here: a
// failed read, a disabled module or an offline host leaves them, so sign-in
// keeps refusing every address a host has held (spec section 3).
func (r *Registry) recordTailnet(hostID string, remote net.Addr) {
	defer r.wg.Done()
	ctx, cancel := context.WithTimeout(r.ctx, tailnetReadTimeout)
	defer cancel()
	var stableID string
	var addrs []netip.Addr
	if rr := r.cfg.Resources(); rr != nil {
		raw, err := rr.Resource(ctx, tailnetReader, hostID, "tailscale.status")
		var st tailnetStatus
		switch {
		case err != nil:
			r.cfg.Log.Info("fleet: the host's tailnet addresses were not read; earlier ones stay recorded", "host", hostID, "err", err)
		case json.Unmarshal(raw, &st) != nil:
			r.cfg.Log.Warn("fleet: the host's tailscale.status does not decode", "host", hostID)
		case st.Self != nil:
			stableID = st.Self.ID
			for _, s := range st.Self.Addresses {
				if a, err := netip.ParseAddr(s); err == nil && len(addrs) < maxTailnetAddrs {
					addrs = append(addrs, a.Unmap())
				}
			}
		}
	}
	if tcp, ok := remote.(*net.TCPAddr); ok {
		if a := tcp.AddrPort().Addr().Unmap(); tailnetV4.Contains(a) || tailnetV6.Contains(a) {
			addrs = append(addrs, a)
		}
	}
	if len(addrs) == 0 {
		return
	}
	now := formatTime(r.cfg.Clock.Now())
	err := r.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
		for _, a := range addrs {
			// A row keeps its stable id when this connect learned none.
			if _, err := tx.ExecContext(ctx, `INSERT INTO core_host_tailnet (host_id, addr, stable_id, seen_at) VALUES (?, ?, ?, ?)
				ON CONFLICT (host_id, addr) DO UPDATE SET seen_at = excluded.seen_at,
				stable_id = CASE WHEN excluded.stable_id = '' THEN core_host_tailnet.stable_id ELSE excluded.stable_id END`,
				hostID, a.String(), stableID, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		r.cfg.Log.Warn("fleet: cannot record the host's tailnet addresses", "host", hostID, "err", err)
	}
}
