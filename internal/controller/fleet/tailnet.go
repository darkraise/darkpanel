package fleet

import (
	"context"
	"database/sql"
	"net"
	"net/netip"
	"time"

	"github.com/darkraise/darkpanel/internal/transport"
)

// tailnetWriteTimeout bounds the core_host_tailnet write at connect.
const tailnetWriteTimeout = 30 * time.Second

// Tailscale's address ranges: the CGNAT block it assigns IPv4 addresses
// from and its IPv6 ULA prefix.
var (
	tailnetV4 = netip.MustParsePrefix("100.64.0.0/10")
	tailnetV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// isTailnet reports whether a lies in one of Tailscale's address ranges.
// Only such addresses are recorded: sign-in sees no other.
func isTailnet(a netip.Addr) bool { return tailnetV4.Contains(a) || tailnetV6.Contains(a) }

// recordTailnet upserts the address hostID's session came from in
// core_host_tailnet when it is a tailnet address. Only an address the
// controller observed is recorded: what a host reports about itself would let
// one host make any tailnet node count as managed (spec section 1). Rows are
// never removed here: an offline host, or one that connects over the LAN,
// leaves them, so sign-in keeps refusing every address a host has held.
//
// A failed write closes the session: an unrecorded host's node would pass
// the sign-in host check, so the agent reconnects and the write runs again.
func (r *Registry) recordTailnet(hostID string, s *transport.Session) {
	defer r.wg.Done()
	tcp, ok := s.RemoteAddr().(*net.TCPAddr)
	if !ok {
		r.cfg.Log.Warn("fleet: the host's agent session has no TCP address; it is not recorded", "host", hostID, "addr", s.RemoteAddr().String())
		return
	}
	a := tcp.AddrPort().Addr().Unmap()
	if !isTailnet(a) {
		if !a.IsLoopback() {
			r.cfg.Log.Info("fleet: the host's agent session is not from a tailnet address; it is not recorded", "host", hostID, "addr", a.String())
		}
		return
	}
	ctx, cancel := context.WithTimeout(r.ctx, tailnetWriteTimeout)
	defer cancel()
	now := formatTime(r.cfg.Clock.Now())
	err := r.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO core_host_tailnet (host_id, addr, stable_id, seen_at) VALUES (?, ?, '', ?)
			ON CONFLICT (host_id, addr) DO UPDATE SET seen_at = excluded.seen_at`, hostID, a.String(), now)
		return err
	})
	if err != nil {
		r.cfg.Log.Error("fleet: cannot record the host's tailnet address; its session is closed so the agent reconnects", "host", hostID, "err", err)
		_ = s.Close()
	}
}
