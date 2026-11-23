//go:build harness

package harness

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

// whoisWaitDefault is how long TestWhoisAnswerShapes waits for a peer's
// request when DARKPANEL_TAILNET_WAIT does not say.
const whoisWaitDefault = 3 * time.Minute

// TestWhoisAnswerShapes checks, against a live logged-in tailscaled, the
// facts Tailscale identity sign-in rests on and that the unit tests take
// from recorded fixtures (internal/modules/tailscale/testdata/whois-*.json):
// a user-owned node has no tags, sharer 0 and a user id; a tagged node has
// tags; a shared-in node has a sharer; and a request from a peer to a
// listener on this node's tailnet address arrives from the peer's tailnet
// address. The owner runs it on a node of a tailnet holding all three kinds
// of peer, with DARKPANEL_TAILNET_SOCKET naming tailscaled's socket, then
// sends one HTTP request from a peer to the URL it logs; it never runs in CI.
func TestWhoisAnswerShapes(t *testing.T) {
	sock := os.Getenv("DARKPANEL_TAILNET_SOCKET")
	if sock == "" {
		t.Skip("DARKPANEL_TAILNET_SOCKET does not name a logged-in tailscaled socket")
	}
	wait := whoisWaitDefault
	if v := os.Getenv("DARKPANEL_TAILNET_WAIT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatalf("DARKPANEL_TAILNET_WAIT: %v", err)
		}
		wait = d
	}
	ctx, cancel := context.WithTimeout(context.Background(), wait+time.Minute)
	defer cancel()
	c := tslocal.New(sock)
	defer c.Close()
	st, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.BackendState != "Running" || st.Self == nil || len(st.TailscaleIPs) == 0 {
		t.Fatalf("tailscaled is %s with no self node or address: log it in first", st.BackendState)
	}

	seen := map[string]bool{}
	for _, p := range st.Peer {
		if p == nil || len(p.TailscaleIPs) == 0 {
			continue
		}
		w, err := c.WhoIs(ctx, p.TailscaleIPs[0])
		if err != nil {
			t.Errorf("whois %s (%s): %v", p.TailscaleIPs[0], p.DNSName, err)
			continue
		}
		if w.Node == nil || w.UserProfile == nil {
			t.Errorf("whois %s: node %v, profile %v; want both", p.TailscaleIPs[0], w.Node, w.UserProfile)
			continue
		}
		n := w.Node
		if n.StableID != p.ID {
			t.Errorf("whois %s: stable id %q, status says %q", p.TailscaleIPs[0], n.StableID, p.ID)
		}
		switch {
		case len(n.Tags) > 0:
			seen["tagged"] = true
		case n.Sharer != 0:
			seen["shared-in"] = true
			if w.UserProfile.ID == 0 {
				t.Errorf("shared-in node %s: no owner id", n.Name)
			}
		default:
			seen["user-owned"] = true
			if w.UserProfile.ID == 0 || w.UserProfile.LoginName == "" {
				t.Errorf("user-owned node %s: profile %+v, want an id and a login name", n.Name, w.UserProfile)
			}
		}
		t.Logf("whois %s: tags %v, sharer %d, user %d %q", n.Name, n.Tags, n.Sharer, w.UserProfile.ID, w.UserProfile.LoginName)
	}
	for _, kind := range []string{"user-owned", "tagged", "shared-in"} {
		if !seen[kind] {
			t.Errorf("the tailnet has no %s peer, so its whois shape is not checked: add one and run again", kind)
		}
	}

	whoisRemoteAddr(ctx, t, c, st, wait)
}

// whoisRemoteAddr listens on this node's first tailnet address and checks
// that the first request a peer sends arrives from that peer's tailnet
// address, which whois resolves to a node other than this one.
func whoisRemoteAddr(ctx context.Context, t *testing.T, c *tslocal.Client, st *tslocal.Status, wait time.Duration) {
	t.Helper()
	l, err := net.Listen("tcp", netip.AddrPortFrom(st.TailscaleIPs[0], 0).String())
	if err != nil {
		t.Fatal(err)
	}
	remotes := make(chan string, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case remotes <- r.RemoteAddr:
		default:
		}
		_, _ = w.Write([]byte("ok\n"))
	})}
	go func() { _ = srv.Serve(l) }()
	defer func() { _ = srv.Close() }()
	t.Logf("from a peer of this tailnet, run: curl http://%s/ (waiting %s)", l.Addr(), wait)

	var remote string
	select {
	case remote = <-remotes:
	case <-time.After(wait):
		t.Fatal("no peer request arrived on the tailnet listener")
	}
	ap, err := netip.ParseAddrPort(remote)
	if err != nil {
		t.Fatalf("remote address %q: %v", remote, err)
	}
	addr := ap.Addr().Unmap()
	if !netip.MustParsePrefix("100.64.0.0/10").Contains(addr) && !netip.MustParsePrefix("fd7a:115c:a1e0::/48").Contains(addr) {
		t.Fatalf("the request arrived from %s, not a tailnet address", addr)
	}
	w, err := c.WhoIs(ctx, addr)
	if errors.Is(err, tslocal.ErrPeerNotFound) {
		t.Fatalf("whois %s: no node holds the request's address", addr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if w.Node == nil || w.Node.StableID == st.Self.ID {
		t.Fatalf("whois %s: node %+v, want a peer", addr, w.Node)
	}
	t.Logf("the request came from %s, node %s", addr, w.Node.Name)
}
