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
