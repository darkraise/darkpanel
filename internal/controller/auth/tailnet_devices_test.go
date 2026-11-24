package auth

import (
	"context"
	"database/sql"
	"net/netip"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/listen"
)

// TestIsManagedTailnetNodeAddressesOnly: a node is a Darkpanel host only by
// an address the controller observed, read through the caller's querier.
func TestIsManagedTailnetNodeAddressesOnly(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newTestService(t)
	for _, q := range []string{
		`INSERT INTO core_hosts (id, name, created_at) VALUES ('h1', 'web1', '2026-11-25T00:00:00Z')`,
		`INSERT INTO core_host_tailnet (host_id, addr, stable_id, seen_at) VALUES ('h1', '100.64.0.7', 'nHost', '2026-11-25T00:00:00Z')`,
	} {
		if _, err := s.db.Writer().ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := listen.SaveTailscaleAddrs(ctx, s.db, []netip.Addr{netip.MustParseAddr("100.64.0.1")}); err != nil {
		t.Fatal(err)
	}
	a := netip.MustParseAddr
	for _, tc := range []struct {
		name  string
		addrs []netip.Addr
		want  bool
	}{
		{"a recorded address", []netip.Addr{a("100.64.0.7")}, true},
		{"a recorded address, mapped", []netip.Addr{a("::ffff:100.64.0.7")}, true},
		{"another address of the node whose row names stable id nHost", []netip.Addr{a("100.64.0.9")}, false},
		{"the controller's own address", []netip.Addr{a("100.64.0.1")}, true},
		{"nothing to look up", nil, false},
	} {
		got, err := s.IsManagedTailnetNode(ctx, s.db.Read(), tc.addrs)
		if err != nil || got != tc.want {
			t.Errorf("%s: IsManagedTailnetNode = %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}

	// A row written inside an open write transaction is seen through that
	// transaction's querier.
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO core_host_tailnet (host_id, addr, stable_id, seen_at) VALUES ('h1', '100.64.0.20', '', '2026-11-25T00:00:00Z')`); err != nil {
			return err
		}
		got, err := s.IsManagedTailnetNode(ctx, tx, []netip.Addr{a("100.64.0.20")})
		if err != nil || !got {
			t.Errorf("a row of the open transaction: IsManagedTailnetNode = %v, %v; want true", got, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
