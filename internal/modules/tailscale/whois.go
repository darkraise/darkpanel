package tailscale

import (
	"context"
	"errors"
	"net/netip"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

// whois is tailscale.whois.get: the internal read that tells the controller which node and user hold a
// tailnet address, for Tailscale identity sign-in.
func (t ts) whois() core.ActionDef {
	return core.DefineInternalRead[WhoisInput, Whois](core.ActionSpec{
		Name: ID + ".whois.get", Summary: "Look up the node and user holding a tailnet address, for the controller's own use.",
		Version: 1, Side: core.SideAgent,
	}, t.whoisRead)
}

func (t ts) whoisRead(ctx context.Context, _ core.Env, in WhoisInput) (core.Revealed[Whois], error) {
	addr, err := netip.ParseAddr(in.Addr)
	if err != nil {
		msg := "addr must be an IP address"
		return core.Revealed[Whois]{}, core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: msg,
			Fields: []errcode.FieldError{{Path: "/addr", Message: msg}}})
	}
	addr = addr.Unmap()
	w, err := t.d.local().WhoIs(ctx, addr)
	switch {
	case errors.Is(err, tslocal.ErrPeerNotFound):
		w = nil
	case err != nil:
		return core.Revealed[Whois]{}, statusLocalErr(ctx, err)
	}
	return core.Revealed[Whois]{Identity: "tailscale whois for " + addr.String(), Value: whoisOf(w)}, nil
}

// whoisOf is w in the read's shape; nil, or a node with no profile, leaves the missing fields empty.
func whoisOf(w *tslocal.WhoIs) Whois {
	out := Whois{Tags: []string{}, Addresses: []string{}}
	if w == nil {
		return out
	}
	if n := w.Node; n != nil {
		out.NodeName = strings.TrimSuffix(n.Name, ".")
		out.StableID = n.StableID
		out.Sharer = n.Sharer
		out.Tags = append(out.Tags, n.Tags...)
		for _, p := range n.Addresses {
			out.Addresses = append(out.Addresses, p.Addr().String())
		}
	}
	if u := w.UserProfile; u != nil {
		out.LoginName = u.LoginName
		out.UserID = u.ID
	}
	return out
}
