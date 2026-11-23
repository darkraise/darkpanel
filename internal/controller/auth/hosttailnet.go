package auth

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/controller/listen"
)

// IsManagedTailnetNode reports whether a tailnet node is a Darkpanel host:
// whether any of addrs, or stableID, is recorded in core_host_tailnet, or
// any of addrs is one of the controller's own Tailscale addresses. A host
// counts whether it is online or not (M10 spec section 3). An error means
// the answer is unknown, and the caller refuses.
func (s *Service) IsManagedTailnetNode(ctx context.Context, addrs []netip.Addr, stableID string) (bool, error) {
	own, err := listen.LoadTailscaleAddrs(ctx, s.db)
	if err != nil {
		return false, fmt.Errorf("auth: the controller's tailnet addresses: %w", err)
	}
	args := make([]any, 0, len(addrs)+1)
	for _, a := range addrs {
		a = a.Unmap()
		if slices.Contains(own, a) {
			return true, nil
		}
		args = append(args, a.String())
	}
	var where []string
	if len(args) > 0 {
		where = append(where, "addr IN (?"+strings.Repeat(", ?", len(args)-1)+")")
	}
	if stableID != "" {
		where = append(where, "stable_id = ?")
		args = append(args, stableID)
	}
	if len(where) == 0 {
		return false, nil
	}
	var n int
	if err := s.db.Read().QueryRowContext(ctx,
		`SELECT count(*) FROM core_host_tailnet WHERE `+strings.Join(where, " OR "), args...).Scan(&n); err != nil {
		return false, fmt.Errorf("auth: look up the host tailnet addresses: %w", err)
	}
	return n > 0, nil
}
