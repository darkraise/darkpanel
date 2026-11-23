package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

// whoisFixtureClient is a tslocal client of a fake tailscaled whose whois
// answers each address with the fixture of byAddr, and 404 for any other.
func whoisFixtureClient(t *testing.T, byAddr map[string]string) *tslocal.Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "ts.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, ok := byAddr[r.URL.Query().Get("addr")]
		if r.URL.Path != "/localapi/v0/whois" || !ok {
			http.Error(w, "no match for IP:port", http.StatusNotFound)
			return
		}
		b, err := os.ReadFile(filepath.Join("testdata", file)) //nolint:gosec // G304: a fixture name from the test's own table
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(b)
	}))
	_ = srv.Listener.Close()
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	c := tslocal.New(sock)
	t.Cleanup(c.Close)
	return c
}

func TestWhoisFixtures(t *testing.T) {
	c := whoisFixtureClient(t, map[string]string{
		"100.101.102.103": "whois-user.json", "100.90.80.70": "whois-tagged.json", "100.70.60.50": "whois-shared.json",
	})
	cases := []struct {
		addr string
		want Whois
	}{
		{"100.101.102.103", Whois{
			LoginName: "alice@example.com", UserID: 4242, NodeName: "laptop.tail1234.ts.net", StableID: "nUserLaptopCNTRL",
			Tags: []string{}, Sharer: 0, Addresses: []string{"100.101.102.103", "fd7a:115c:a1e0::6601:6667"},
		}},
		{"100.90.80.70", Whois{
			LoginName: "tagged-devices", UserID: 9001, NodeName: "build-server.tail1234.ts.net", StableID: "nTaggedSrvCNTRL",
			Tags: []string{"tag:server", "tag:ci"}, Addresses: []string{"100.90.80.70", "fd7a:115c:a1e0::5a01:5046"},
		}},
		{"100.70.60.50", Whois{
			LoginName: "bob@example.org", UserID: 5555, NodeName: "friends-pc.example.ts.net", StableID: "nSharedInCNTRL",
			Tags: []string{}, Sharer: 7777, Addresses: []string{"100.70.60.50", "fd7a:115c:a1e0::4601:3c32"},
		}},
	}
	for _, tc := range cases {
		w, err := c.WhoIs(t.Context(), netip.MustParseAddr(tc.addr))
		if err != nil {
			t.Fatalf("%s: %v", tc.addr, err)
		}
		if got := whoisOf(w); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", tc.addr, got, tc.want)
		}
	}
	user, _ := c.WhoIs(t.Context(), netip.MustParseAddr("100.101.102.103"))
	if u := whoisOf(user); len(u.Tags) != 0 || u.Sharer != 0 || u.UserID == 0 {
		t.Errorf("the user-owned node %+v must have no tags, sharer 0 and a user id", u)
	}
	tagged, _ := c.WhoIs(t.Context(), netip.MustParseAddr("100.90.80.70"))
	if len(whoisOf(tagged).Tags) == 0 {
		t.Error("the tagged node has no tags")
	}
	shared, _ := c.WhoIs(t.Context(), netip.MustParseAddr("100.70.60.50"))
	if whoisOf(shared).Sharer == 0 {
		t.Error("the shared-in node has no sharer")
	}
	if _, err := c.WhoIs(t.Context(), netip.MustParseAddr("100.64.0.99")); !errors.Is(err, tslocal.ErrPeerNotFound) {
		t.Errorf("an unknown address: err = %v, want ErrPeerNotFound", err)
	}
}

// whoisRead runs tailscale.whois.get the way the agent's Engine.Reveal does.
func whoisRead(t *testing.T, h *tsHost, input string) (string, Whois, error) {
	t.Helper()
	identity, raw, err := h.ts().whois().Handler.Reveal(context.Background(), h.env(""), json.RawMessage(input))
	if err != nil {
		return identity, Whois{}, err
	}
	var out Whois
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("the value %s does not decode as a Whois: %v", raw, err)
	}
	return identity, out, nil
}

func TestWhoisRead(t *testing.T) {
	def := newTSHost(t).ts().whois()
	if s := def.Spec; s.Name != "tailscale.whois.get" || s.Version != 1 || s.Class != core.ClassRead || s.Side != core.SideAgent ||
		!s.Internal || s.Reveal || s.Summary == "" {
		t.Fatalf("spec %+v, want the internal read tailscale.whois.get v1 on the agent", s)
	}

	h := newTSHost(t)
	h.local.Whois = map[netip.Addr]*tslocal.WhoIs{
		netip.MustParseAddr("100.101.102.103"): {
			Node: &tslocal.WhoIsNode{
				ID: 1, StableID: "nLaptop", Name: "laptop.tail1234.ts.net.", Sharer: 77,
				Addresses: []netip.Prefix{netip.MustParsePrefix("100.101.102.103/32"), netip.MustParsePrefix("fd7a:115c:a1e0::1/128")},
				Tags:      []string{"tag:x"},
			},
			UserProfile: &tslocal.UserProfile{ID: 42, LoginName: "alice@example.com", DisplayName: "Alice"},
		},
	}
	identity, got, err := whoisRead(t, h, `{"addr":"100.101.102.103"}`)
	if err != nil {
		t.Fatal(err)
	}
	want := Whois{LoginName: "alice@example.com", UserID: 42, NodeName: "laptop.tail1234.ts.net", StableID: "nLaptop",
		Tags: []string{"tag:x"}, Sharer: 77, Addresses: []string{"100.101.102.103", "fd7a:115c:a1e0::1"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if identity != "tailscale whois for 100.101.102.103" {
		t.Errorf("identity %q", identity)
	}
	if calls := h.local.WhoisCalls; len(calls) != 1 || calls[0] != netip.MustParseAddr("100.101.102.103") {
		t.Errorf("whois calls %v", calls)
	}

	// An IPv4-mapped address is looked up unmapped.
	if _, got, err = whoisRead(t, h, `{"addr":"::ffff:100.101.102.103"}`); err != nil || got.StableID != "nLaptop" {
		t.Errorf("mapped address: %+v, %v", got, err)
	}

	_, got, err = whoisRead(t, h, `{"addr":"100.64.0.99"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, Whois{Tags: []string{}, Addresses: []string{}}) {
		t.Errorf("an unknown address answered %+v, want the empty answer", got)
	}

	// A node with no user profile answers no login name or user id.
	h.local.Whois[netip.MustParseAddr("100.64.0.5")] = &tslocal.WhoIs{Node: &tslocal.WhoIsNode{StableID: "nBare"}}
	if _, got, err = whoisRead(t, h, `{"addr":"100.64.0.5"}`); err != nil || got.LoginName != "" || got.UserID != 0 || got.StableID != "nBare" {
		t.Errorf("node without a profile: %+v, %v", got, err)
	}

	for _, bad := range []string{`{"addr":""}`, `{"addr":"laptop"}`, `{"addr":"100.64.0.1:80"}`} {
		if _, _, err := whoisRead(t, h, bad); errcode.From(err).Code != errcode.InvalidInput || !core.IsDefinite(err) {
			t.Errorf("%s: err = %v, want a definite invalid_input", bad, err)
		}
	}

	h.local.Errs = map[string]error{"WhoIs": errors.New("dial unix: connection refused")}
	if _, _, err := whoisRead(t, h, `{"addr":"100.101.102.103"}`); errcode.From(err).Code != errcode.TailscaleUnavailable {
		t.Errorf("tailscaled down: err = %v, want tailscale.unavailable", err)
	}
}
