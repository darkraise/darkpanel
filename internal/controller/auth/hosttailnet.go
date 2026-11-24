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
// whether any of addrs is recorded in core_host_tailnet, read through q, or
// is one of the controller's own Tailscale addresses. A host is recognised
// only by addresses the controller observed its sessions come from, never by
// what it reports about itself (spec section 2). A host counts whether it is
// online or not. An error means the answer is unknown, and the caller
// refuses.
func (s *Service) IsManagedTailnetNode(ctx context.Context, q querier, addrs []netip.Addr) (bool, error) {
	if len(addrs) == 0 {
		return false, nil
	}
	own, err := listen.LoadTailscaleAddrs(ctx, s.db)
	if err != nil {
		return false, fmt.Errorf("auth: the controller's tailnet addresses: %w", err)
	}
	args := make([]any, 0, len(addrs))
	for _, a := range addrs {
		a = a.Unmap()
		if slices.Contains(own, a) {
			return true, nil
		}
		args = append(args, a.String())
	}
	var n int
	if err := q.QueryRowContext(ctx,
		`SELECT count(*) FROM core_host_tailnet WHERE addr IN (?`+strings.Repeat(", ?", len(args)-1)+`)`, args...).Scan(&n); err != nil {
		return false, fmt.Errorf("auth: look up the host tailnet addresses: %w", err)
	}
	return n > 0, nil
}
