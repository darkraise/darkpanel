package core

import (
	"context"
	"net/netip"
	"testing"
)

func TestClientPeerContext(t *testing.T) {
	p := Peer{Addr: netip.MustParseAddr("192.0.2.7"), Port: 51234}
	if got, ok := ClientPeer(WithClientPeer(context.Background(), p)); !ok || got != p {
		t.Errorf("ClientPeer = %+v, %v; want %+v", got, ok, p)
	}
	if got, ok := ClientPeer(context.Background()); ok {
		t.Errorf("ClientPeer of a bare context = %+v, want none", got)
	}
}

func TestProxiedContext(t *testing.T) {
	revoked := make(chan struct{})
	p := Proxied{
		ListenerID: "01JLISTENER", Client: netip.MustParseAddr("203.0.113.9"),
		Transport: netip.MustParseAddrPort("172.31.255.2:40112"), PublicHost: "panel.example.com",
		Path: ProxyPath{ExposureID: "01JCHANGE", Kind: "npm", ProxyAddr: netip.MustParseAddr("172.31.255.2"),
			ListenerAddr: netip.MustParseAddrPort("172.31.255.1:9443")},
		Revoked: revoked,
	}
	got, ok := ProxiedFrom(WithProxied(context.Background(), p))
	if !ok || got.ListenerID != p.ListenerID || got.Client != p.Client || got.Transport != p.Transport ||
		got.PublicHost != p.PublicHost || got.Path.ExposureID != p.Path.ExposureID || got.Revoked != p.Revoked {
		t.Errorf("ProxiedFrom = %+v, %v; want %+v", got, ok, p)
	}
	if got, ok := ProxiedFrom(context.Background()); ok {
		t.Errorf("ProxiedFrom of a bare context = %+v, want none", got)
	}
	// A proxied request still has its own client peer, which the API sets
	// separately; one key never answers for the other.
	if _, ok := ClientPeer(WithProxied(context.Background(), p)); ok {
		t.Error("ClientPeer found a peer in a context that carries only Proxied")
	}
}
