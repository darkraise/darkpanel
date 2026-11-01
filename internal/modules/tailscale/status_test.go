package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

func stBool(b bool) *bool    { return &b }
func stStr(s string) *string { return &s }

// stRoutes is a pointer to a non-nil list, the way a decoded "advertiseRoutes": [...] reads.
func stRoutes(r ...string) *[]string {
	if r == nil {
		r = []string{}
	}
	return &r
}

// stWant builds the seven preferences fields the status carries.
func stWant(running, accept, shields, ssh, advertiseExit bool, exit string, routes ...string) PrefsFields {
	return PrefsFields{
		WantRunning: stBool(running), AdvertiseRoutes: stRoutes(routes...), ExitNode: stStr(exit),
		AdvertiseExitNode: stBool(advertiseExit), AcceptRoutes: stBool(accept), ShieldsUp: stBool(shields), RunSSH: stBool(ssh),
	}
}

func stAddrs(s ...string) []netip.Addr {
	out := make([]netip.Addr, 0, len(s))
	for _, a := range s {
		out = append(out, netip.MustParseAddr(a))
	}
	return out
}

func stPrefixes(s ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(s))
	for _, p := range s {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}

// stRead reads tailscale.status on the host and decodes the answer; raw is the JSON as the resource encoded it.
func stRead(t *testing.T, h *tsHost) (got Status, raw string, err error) {
	t.Helper()
	b, err := h.ts().statusResource().Read(context.Background(), h.env(""))
	if err != nil {
		return Status{}, "", err
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("the answer %s does not decode as a Status: %v", b, err)
	}
	return got, string(b), nil
}

func TestStResourceDefinition(t *testing.T) {
	def := newTSHost(t).ts().statusResource()
	if def.Name != "tailscale.status" || def.OutputType != reflect.TypeFor[Status]() {
		t.Errorf("resource %s with output %v, want tailscale.status with Status", def.Name, def.OutputType)
	}
}

// TestStLoggedInNode: the default host is a logged-in node with no peers; every list is an empty array and
// every preference is set, and the read touches nothing on the host.
func TestStLoggedInNode(t *testing.T) {
	h := newTSHost(t)
	got, raw, err := stRead(t, h)
	if err != nil {
		t.Fatal(err)
	}
	want := Status{
		Version: "1.104.1", BackendState: "Running",
		Self: &Node{ID: "n1", HostName: thHostname, DNSName: thHostname + ".example.ts.net", OS: "linux",
			Addresses: []string{"100.64.0.7"}, Tags: []string{}},
		Tailnet: "example.com", MagicDNSSuffix: "example.ts.net", PreferredDERP: "", RunSSH: false,
		Prefs: stWant(true, false, false, false, false, ""),
		Peers: []Peer{}, Health: []string{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("status\n got %+v\nwant %+v", got, want)
	}
	for _, member := range []string{`"peers":[]`, `"health":[]`, `"tags":[]`, `"advertiseRoutes":[]`} {
		if !strings.Contains(raw, member) {
			t.Errorf("answer %s lacks %s", raw, member)
		}
	}
	if len(h.ran) != 0 || len(h.files) != 0 || len(h.local.Edits) != 0 || len(h.local.Starts) != 0 || h.local.Logouts != 0 ||
		len(h.journal.effects()) != 0 || h.local.CertCalls != nil {
		t.Errorf("the read changed the host: ran %v, files %d, edits %d, journal %v", h.ran, len(h.files), len(h.local.Edits), h.journal.effects())
	}
}

// TestStSelfPreferencesAndHealth: the home DERP region is the node's Relay, RunSSH comes from the preferences,
// addresses list IPv4 first, and the preferences are all seven fields.
func TestStSelfPreferencesAndHealth(t *testing.T) {
	h := newTSHost(t)
	self := h.local.Status.Self
	self.Relay = "fra"
	self.Tags = []string{"tag:web", "tag:db"}
	self.TailscaleIPs = stAddrs("fd7a:115c:a1e0::7", "100.64.0.7")
	h.local.Status.Health = []string{"Tailscale could not reach the DERP relay"}
	h.local.Prefs = &tslocal.Prefs{
		WantRunning: true, RouteAll: true, ShieldsUp: true, RunSSH: true, ExitNodeID: "nExit1",
		AdvertiseRoutes: stPrefixes("10.0.0.0/8", "0.0.0.0/0", "::/0"),
	}
	got, _, err := stRead(t, h)
	if err != nil {
		t.Fatal(err)
	}
	wantSelf := &Node{ID: "n1", HostName: thHostname, DNSName: thHostname + ".example.ts.net", OS: "linux",
		Addresses: []string{"100.64.0.7", "fd7a:115c:a1e0::7"}, Tags: []string{"tag:web", "tag:db"}}
	if !reflect.DeepEqual(got.Self, wantSelf) {
		t.Errorf("self = %+v, want %+v", got.Self, wantSelf)
	}
	if got.PreferredDERP != "fra" || !got.RunSSH {
		t.Errorf("preferredDerp %q runSSH %v, want fra and true", got.PreferredDERP, got.RunSSH)
	}
	if want := stWant(true, true, true, true, true, "nExit1", "10.0.0.0/8"); !reflect.DeepEqual(got.Prefs, want) {
		t.Errorf("prefs %+v, want %+v", got.Prefs, want)
	}
	if !reflect.DeepEqual(got.Health, []string{"Tailscale could not reach the DERP relay"}) {
		t.Errorf("health = %v", got.Health)
	}
}

// TestStLoggedOut: a node that needs a login has no self (a stale record of its last identity is not shown) and
// no tailnet.
func TestStLoggedOut(t *testing.T) {
	h := newTSHost(t)
	h.local.Status = &tslocal.Status{
		Version: "1.104.1", BackendState: "NeedsLogin", AuthURL: "https://login.tailscale.example/a/abc",
		Self: &tslocal.PeerStatus{ID: "stale", HostName: "old-name", DNSName: "old-name.example.ts.net."},
	}
	h.local.Prefs = &tslocal.Prefs{LoggedOut: true}
	got, raw, err := stRead(t, h)
	if err != nil {
		t.Fatal(err)
	}
	if got.BackendState != "NeedsLogin" || got.Self != nil || got.Tailnet != "" || got.MagicDNSSuffix != "" || got.PreferredDERP != "" {
		t.Errorf("status %+v, want NeedsLogin with no self, tailnet, suffix or region", got)
	}
	if !strings.Contains(raw, `"self":null`) {
		t.Errorf("answer %s lacks \"self\":null", raw)
	}
	if strings.Contains(raw, "login.tailscale.example") {
		t.Errorf("answer %s carries the login URL", raw)
	}
	if want := stWant(false, false, false, false, false, ""); !reflect.DeepEqual(got.Prefs, want) {
		t.Errorf("prefs %+v, want %+v", got.Prefs, want)
	}
}

func TestStSelfByBackendState(t *testing.T) {
	for _, tt := range []struct {
		state   string
		hasSelf bool
		want    bool
	}{
		{"NeedsLogin", true, false},
		{"NoState", true, false},
		{"Stopped", true, true},
		{"Starting", true, true},
		{"NeedsMachineAuth", true, true},
		{"Running", true, true},
		{"Running", false, false},
	} {
		h := newTSHost(t)
		h.local.Status.BackendState = tt.state
		if !tt.hasSelf {
			h.local.Status.Self = nil
		}
		got, _, err := stRead(t, h)
		if err != nil {
			t.Fatal(err)
		}
		if (got.Self != nil) != tt.want {
			t.Errorf("state %s with self %v: self = %+v, want present %v", tt.state, tt.hasSelf, got.Self, tt.want)
		}
	}
}

// TestStFallbacks: a status that leaves out the top-level suffix or the self addresses is completed from the
// current tailnet and the node's own address list.
func TestStFallbacks(t *testing.T) {
	h := newTSHost(t)
	h.local.Status.MagicDNSSuffix = ""
	h.local.Status.Self.TailscaleIPs = nil
	got, _, err := stRead(t, h)
	if err != nil {
		t.Fatal(err)
	}
	if got.MagicDNSSuffix != "example.ts.net" {
		t.Errorf("magicDNSSuffix = %q, want the current tailnet's example.ts.net", got.MagicDNSSuffix)
	}
	if got.Self == nil || !reflect.DeepEqual(got.Self.Addresses, []string{"100.64.0.7"}) {
		t.Errorf("self = %+v, want the status's address list", got.Self)
	}
}

// TestStPeers: peers are sorted by DNS name then ID; the connection is direct when an endpoint is in use, else a
// peer relay, else DERP, else none; a never-handshaking peer has no handshake time and a handshake time is UTC.
func TestStPeers(t *testing.T) {
	h := newTSHost(t)
	hs := time.Date(2026, 10, 31, 11, 59, 0, 0, time.FixedZone("plus1", 3600))
	h.local.Status.Peer = map[string]*tslocal.PeerStatus{
		"nodekey:9": {ID: "n9", HostName: "dup", DNSName: "e-dup.example.ts.net.", OS: "linux",
			TailscaleIPs: stAddrs("100.64.0.99"), Online: true},
		"nodekey:5": {ID: "n5", HostName: "dup", DNSName: "e-dup.example.ts.net.", OS: "linux",
			TailscaleIPs: stAddrs("100.64.0.55"), Online: true},
		"nodekey:d": {ID: "n4", HostName: "d-none", DNSName: "d-none.example.ts.net.", OS: "windows",
			TailscaleIPs: stAddrs("100.64.0.4"), Tags: []string{"tag:server"}},
		"nodekey:c": {ID: "n3", HostName: "c-relay", DNSName: "c-relay.example.ts.net.", OS: "macOS",
			TailscaleIPs: stAddrs("100.64.0.3"), PeerRelay: "198.51.100.9:3478:vni:7", Online: true},
		"nodekey:b": {ID: "n2", HostName: "b-direct", DNSName: "b-direct.example.ts.net.", OS: "linux",
			TailscaleIPs: stAddrs("fd7a:115c:a1e0::2", "100.64.0.2"), CurAddr: "203.0.113.5:41641", Relay: "nyc",
			Online: true, LastHandshake: hs},
		"nodekey:a": {ID: "n1x", HostName: "a-derp", DNSName: "a-derp.example.ts.net.", OS: "linux",
			TailscaleIPs: stAddrs("100.64.0.1"), Relay: "fra", Online: true, ExitNode: true, ExitNodeOption: true},
	}
	got, raw, err := stRead(t, h)
	if err != nil {
		t.Fatal(err)
	}
	want := []Peer{
		{Node: Node{ID: "n1x", HostName: "a-derp", DNSName: "a-derp.example.ts.net", OS: "linux", Addresses: []string{"100.64.0.1"}, Tags: []string{}},
			Online: true, Connection: "derp", Region: "fra", ExitNode: true, ExitNodeOption: true},
		{Node: Node{ID: "n2", HostName: "b-direct", DNSName: "b-direct.example.ts.net", OS: "linux", Addresses: []string{"100.64.0.2", "fd7a:115c:a1e0::2"}, Tags: []string{}},
			Online: true, Connection: "direct", Endpoint: "203.0.113.5:41641"},
		{Node: Node{ID: "n3", HostName: "c-relay", DNSName: "c-relay.example.ts.net", OS: "macOS", Addresses: []string{"100.64.0.3"}, Tags: []string{}},
			Online: true, Connection: "peer-relay"},
		{Node: Node{ID: "n4", HostName: "d-none", DNSName: "d-none.example.ts.net", OS: "windows", Addresses: []string{"100.64.0.4"}, Tags: []string{"tag:server"}},
			Connection: "none"},
		{Node: Node{ID: "n5", HostName: "dup", DNSName: "e-dup.example.ts.net", OS: "linux", Addresses: []string{"100.64.0.55"}, Tags: []string{}},
			Online: true, Connection: "none"},
		{Node: Node{ID: "n9", HostName: "dup", DNSName: "e-dup.example.ts.net", OS: "linux", Addresses: []string{"100.64.0.99"}, Tags: []string{}},
			Online: true, Connection: "none"},
	}
	if len(got.Peers) != len(want) {
		t.Fatalf("%d peers, want %d: %+v", len(got.Peers), len(want), got.Peers)
	}
	handshake := got.Peers[1].LastHandshake
	if handshake == nil || !handshake.Equal(hs) {
		t.Errorf("direct peer's handshake = %v, want %v", handshake, hs)
	}
	got.Peers[1].LastHandshake = nil
	for i := range want {
		if !reflect.DeepEqual(got.Peers[i], want[i]) {
			t.Errorf("peer %d\n got %+v\nwant %+v", i, got.Peers[i], want[i])
		}
	}
	if !strings.Contains(raw, `"lastHandshake":"2026-10-31T10:59:00Z"`) {
		t.Errorf("answer %s lacks the handshake as UTC", raw)
	}
	if strings.Count(raw, `"lastHandshake":null`) != len(want)-1 {
		t.Errorf("answer %s: want a null handshake for each of the %d peers that never handshook", raw, len(want)-1)
	}
}

func TestStPrefsFieldsOf(t *testing.T) {
	tests := []struct {
		name string
		in   tslocal.Prefs
		want PrefsFields
	}{
		{"zero", tslocal.Prefs{}, stWant(false, false, false, false, false, "")},
		{"flags", tslocal.Prefs{WantRunning: true, RouteAll: true, ShieldsUp: true, RunSSH: true},
			stWant(true, true, true, true, false, "")},
		{"exit node by id", tslocal.Prefs{ExitNodeID: "nExit1"}, stWant(false, false, false, false, false, "nExit1")},
		{"exit node by address", tslocal.Prefs{ExitNodeIP: netip.MustParseAddr("100.64.0.9")},
			stWant(false, false, false, false, false, "100.64.0.9")},
		{"the id wins over the address", tslocal.Prefs{ExitNodeID: "nExit1", ExitNodeIP: netip.MustParseAddr("100.64.0.9")},
			stWant(false, false, false, false, false, "nExit1")},
		{"routes are sorted, unique and without the default routes",
			tslocal.Prefs{AdvertiseRoutes: stPrefixes("10.2.0.0/16", "0.0.0.0/0", "10.1.0.0/16", "10.2.0.0/16", "::/0")},
			stWant(false, false, false, false, true, "", "10.1.0.0/16", "10.2.0.0/16")},
		{"one default route is not an exit node", tslocal.Prefs{AdvertiseRoutes: stPrefixes("0.0.0.0/0")},
			stWant(false, false, false, false, false, "")},
		{"the other default route alone either", tslocal.Prefs{AdvertiseRoutes: stPrefixes("::/0", "192.168.0.0/24")},
			stWant(false, false, false, false, false, "", "192.168.0.0/24")},
		{"both default routes and nothing else", tslocal.Prefs{AdvertiseRoutes: stPrefixes("::/0", "0.0.0.0/0")},
			stWant(false, false, false, false, true, "")},
		{"IPv4 sorts before IPv6", tslocal.Prefs{AdvertiseRoutes: stPrefixes("fd00::/8", "192.168.0.0/24", "10.0.0.0/8")},
			stWant(false, false, false, false, false, "", "10.0.0.0/8", "192.168.0.0/24", "fd00::/8")},
	}
	for _, tt := range tests {
		in := tt.in
		got := prefsFieldsOf(&in)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", tt.name, got, tt.want)
		}
		if got.AdvertiseRoutes == nil || *got.AdvertiseRoutes == nil {
			t.Errorf("%s: advertiseRoutes is nil, want a non-nil list", tt.name)
		}
	}
}

// TestStUnavailable: a tailscaled that does not answer, on either call, is tailscale.unavailable with the
// cause kept; a read cut off by its own context is that context's error.
func TestStUnavailable(t *testing.T) {
	for _, method := range []string{"Status", "Prefs"} {
		h := newTSHost(t)
		cause := tslocal.ErrUnavailable
		if method == "Prefs" {
			cause = &tslocal.APIError{StatusCode: 500, Message: "boom"}
		}
		h.local.Errs = map[string]error{method: cause}
		_, _, err := stRead(t, h)
		coded := errcode.From(err)
		if err == nil || coded.Code != errcode.TailscaleUnavailable || coded.Detail != "tailscaled does not answer on its socket" {
			t.Errorf("%s failing: err = %#v, want tailscale.unavailable with the reason", method, err)
			continue
		}
		if !errors.Is(err, cause) {
			t.Errorf("%s failing: the cause is not kept in the chain", method)
		}
	}

	h := newTSHost(t)
	h.local.Errs = map[string]error{"Status": context.Canceled}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := h.ts().statusResource().Read(ctx, h.env(""))
	if !errors.Is(err, context.Canceled) || errcode.From(err).Code == errcode.TailscaleUnavailable {
		t.Errorf("cancelled read = %v, want context.Canceled and not tailscale.unavailable", err)
	}
}
