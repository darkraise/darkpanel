package cloudflare

import (
	"context"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
)

// hostnameList declares cloudflare.hostname.list, which lists the stored
// public hostnames in every phase with their last checked health.
func (c cf) hostnameList() core.ActionDef {
	return core.DefineRead[HostnameListInput, HostnameList](core.ActionSpec{
		Name: ID + ".hostname.list", Summary: "List the public hostnames of the managed Cloudflare tunnels.", Version: 1,
		Side: core.SideController,
	}, hostnameReader{})
}

// hostnameReader lists the stored hostname rows; it calls no provider, so it
// needs no connected account.
type hostnameReader struct{}

// Read answers every hostname row sorted by hostname.
func (hostnameReader) Read(ctx context.Context, env core.Env, _ HostnameListInput) (HostnameList, error) {
	rows, err := listHostnames(ctx, env.DB.Read())
	if err != nil {
		return HostnameList{}, err
	}
	slices.SortFunc(rows, func(x, y hostnameRow) int { return strings.Compare(x.Hostname, y.Hostname) })
	views := make([]HostnameView, 0, len(rows))
	for _, r := range rows {
		views = append(views, hostnameViewOf(r))
	}
	return HostnameList{Hostnames: views}, nil
}
