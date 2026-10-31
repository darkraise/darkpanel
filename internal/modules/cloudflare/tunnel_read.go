package cloudflare

import (
	"context"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// tunnelList declares cloudflare.tunnel.list, which lists the managed
// tunnels with their status at Cloudflare.
func (c cf) tunnelList() core.ActionDef {
	return core.DefineRead[TunnelListInput, TunnelList](core.ActionSpec{
		Name: ID + ".tunnel.list", Summary: "List the managed Cloudflare tunnels.", Version: 1, Side: core.SideController,
	}, tunnelReader{c})
}

// tunnelReader lists the stored tunnel rows, so a tunnel Cloudflare no
// longer has still appears, with what Cloudflare reports of each.
type tunnelReader struct{ c cf }

// Read answers every stored tunnel sorted by name. Each provider read fails
// on its own and leaves its part of the view unknown; without an account
// every status is unknown.
func (r tunnelReader) Read(ctx context.Context, env core.Env, _ TunnelListInput) (TunnelList, error) {
	rows, err := listTunnels(ctx, env.DB.Read())
	if err != nil {
		return TunnelList{}, err
	}
	hosts, err := listHostnames(ctx, env.DB.Read())
	if err != nil {
		return TunnelList{}, err
	}
	hostnames := map[string]int{}
	for _, h := range hosts {
		hostnames[h.TunnelID]++
	}
	// Without an account a is nil and every status stays unknown.
	a, _, err := r.c.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil && errcode.From(err).Code != errcode.CloudNotConnected {
		return TunnelList{}, err
	}
	views := make([]TunnelView, 0, len(rows))
	for _, row := range rows {
		v := TunnelView{
			ID: row.ID, Name: row.Name, Status: "unknown", Connections: []ConnectionView{},
			Drift: row.Drift, Hostnames: hostnames[row.ID], CreatedAt: row.CreatedAt,
		}
		if a != nil {
			if t, err := a.Tunnel(ctx, row.ID); err == nil {
				v.Status = t.Status
			}
			if conns, err := a.Connections(ctx, row.ID); err == nil {
				for _, cn := range conns {
					v.Connections = append(v.Connections, ConnectionView{
						ID: cn.ID, Colo: cn.ColoName, OriginIP: cn.OriginIP, OpenedAt: cn.OpenedAt, Version: cn.ClientVersion,
					})
				}
			}
		}
		views = append(views, v)
	}
	slices.SortFunc(views, func(x, y TunnelView) int { return strings.Compare(x.Name, y.Name) })
	return TunnelList{Tunnels: views}, nil
}

// tunnelToken declares cloudflare.tunnel.token, an internal read of the
// token a connector runs a managed tunnel with.
func (c cf) tunnelToken() core.ActionDef {
	return core.DefineInternalRead[TunnelInput, TunnelToken](core.ActionSpec{
		Name: ID + ".tunnel.token", Summary: "Read the token of a managed Cloudflare tunnel.", Version: 1, Side: core.SideController,
	}, c.readTunnelToken)
}

// readTunnelToken reads the token of the managed tunnel the input names, by
// id or name. The token appears only in the revealed value, never in the
// identity or an error.
func (c cf) readTunnelToken(ctx context.Context, env core.Env, in TunnelInput) (core.Revealed[TunnelToken], error) {
	if in.Tunnel == "" {
		return core.Revealed[TunnelToken]{}, core.Definite(&errcode.Error{
			Code: errcode.InvalidInput, Detail: "the token input is invalid",
			Fields: []errcode.FieldError{{Path: "/tunnel", Message: "the tunnel id or name is required"}},
		})
	}
	row, found, err := loadTunnel(ctx, env.DB.Read(), in.Tunnel)
	if err != nil {
		return core.Revealed[TunnelToken]{}, err
	}
	if !found {
		return core.Revealed[TunnelToken]{}, core.Definite(errcode.New(errcode.NotFound, "no managed tunnel "+in.Tunnel))
	}
	a, _, err := c.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return core.Revealed[TunnelToken]{}, err
	}
	token, err := a.TunnelToken(ctx, row.ID)
	if err != nil {
		return core.Revealed[TunnelToken]{}, err
	}
	return core.Revealed[TunnelToken]{Identity: "Cloudflare tunnel token of " + row.Name, Value: TunnelToken{Token: token}}, nil
}
