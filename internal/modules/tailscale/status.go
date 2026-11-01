package tailscale

import (
	"context"
	"net/netip"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

// statusResource is tailscale.status: the node's state, preferences and peers, read from tailscaled's local API.
func (t ts) statusResource() core.ResourceDef {
	return core.DefineResource(ID+".status", t.statusRead)
}

// statusRead reads the status and the preferences, in that order, and nothing else.
func (t ts) statusRead(ctx context.Context, _ core.Env) (Status, error) {
	local := t.d.local()
	s, err := local.Status(ctx)
	if err != nil {
		return Status{}, statusLocalErr(ctx, err)
	}
	prefs, err := local.Prefs(ctx)
	if err != nil {
		return Status{}, statusLocalErr(ctx, err)
	}

	out := Status{
		Version:        s.Version,
		BackendState:   s.BackendState,
		MagicDNSSuffix: s.MagicDNSSuffix,
		RunSSH:         prefs.RunSSH,
		Prefs:          prefsFieldsOf(prefs),
		Peers:          make([]Peer, 0, len(s.Peer)),
		Health:         slices.Clone(s.Health),
	}
	if out.Health == nil {
		out.Health = []string{}
	}
	if s.CurrentTailnet != nil {
		out.Tailnet = s.CurrentTailnet.Name
		if out.MagicDNSSuffix == "" {
			out.MagicDNSSuffix = s.CurrentTailnet.MagicDNSSuffix
		}
	}
	// A logged-out node keeps a stale record of the identity it last held; it is not shown.
	if s.Self != nil && s.BackendState != "NeedsLogin" && s.BackendState != "NoState" {
		self := statusNode(s.Self, s.TailscaleIPs)
		out.Self = &self
		out.PreferredDERP = s.Self.Relay
	}
	for _, p := range s.Peer {
		if p != nil {
			out.Peers = append(out.Peers, statusPeer(p))
		}
	}
	slices.SortFunc(out.Peers, func(a, b Peer) int {
		if c := strings.Compare(a.DNSName, b.DNSName); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

// statusLocalErr is the error of a failed local API call: the context's own error when the read was cut off,
// else tailscale.unavailable with the cause kept.
func statusLocalErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errcode.Wrap(errcode.TailscaleUnavailable, err, "tailscaled does not answer on its socket")
}

// statusNode is p as a Node; fallback is the address list used when p carries none.
func statusNode(p *tslocal.PeerStatus, fallback []netip.Addr) Node {
	ips := p.TailscaleIPs
	if len(ips) == 0 {
		ips = fallback
	}
	addrs := make([]string, 0, len(ips))
	for _, a := range ips {
		if a.Is4() {
			addrs = append(addrs, a.String())
		}
	}
	for _, a := range ips {
		if !a.Is4() {
			addrs = append(addrs, a.String())
		}
	}
	tags := slices.Clone(p.Tags)
	if tags == nil {
		tags = []string{}
	}
	return Node{
		ID: p.ID, HostName: p.HostName, DNSName: strings.TrimSuffix(p.DNSName, "."), OS: p.OS,
		Addresses: addrs, Tags: tags,
	}
}

// statusPeer is p as a Peer.
func statusPeer(p *tslocal.PeerStatus) Peer {
	peer := Peer{
		Node: statusNode(p, nil), Online: p.Online, ExitNode: p.ExitNode, ExitNodeOption: p.ExitNodeOption,
	}
	switch {
	case p.CurAddr != "":
		peer.Connection, peer.Endpoint = "direct", p.CurAddr
	case p.PeerRelay != "":
		peer.Connection = "peer-relay"
	case p.Relay != "":
		peer.Connection, peer.Region = "derp", p.Relay
	default:
		peer.Connection = "none"
	}
	if !p.LastHandshake.IsZero() {
		hs := p.LastHandshake.UTC()
		peer.LastHandshake = &hs
	}
	return peer
}

// prefsFieldsOf returns all seven fields of p set (no nil pointer): wantRunning from WantRunning; acceptRoutes
// from RouteAll; shieldsUp; runSSH; exitNode from ExitNodeID when non-empty, else ExitNodeIP when the address is
// valid, else ""; advertiseRoutes from the AdvertiseRoutes entries other than 0.0.0.0/0 and ::/0, sorted and
// without duplicates, a non-nil empty list when there are none; advertiseExitNode true only when both default
// prefixes are advertised. It does not modify p.
func prefsFieldsOf(p *tslocal.Prefs) PrefsFields {
	exit := p.ExitNodeID
	if exit == "" && p.ExitNodeIP.IsValid() {
		exit = p.ExitNodeIP.String()
	}
	var v4Default, v6Default bool
	prefixes := make([]netip.Prefix, 0, len(p.AdvertiseRoutes))
	for _, r := range p.AdvertiseRoutes {
		switch r {
		case netip.MustParsePrefix("0.0.0.0/0"):
			v4Default = true
		case netip.MustParsePrefix("::/0"):
			v6Default = true
		default:
			prefixes = append(prefixes, r)
		}
	}
	slices.SortFunc(prefixes, netip.Prefix.Compare)
	prefixes = slices.Compact(prefixes)
	routes := make([]string, 0, len(prefixes))
	for _, r := range prefixes {
		routes = append(routes, r.String())
	}
	running, advertiseExit, accept, shields, ssh := p.WantRunning, v4Default && v6Default, p.RouteAll, p.ShieldsUp, p.RunSSH
	return PrefsFields{
		WantRunning: &running, AdvertiseRoutes: &routes, ExitNode: &exit,
		AdvertiseExitNode: &advertiseExit, AcceptRoutes: &accept, ShieldsUp: &shields, RunSSH: &ssh,
	}
}
