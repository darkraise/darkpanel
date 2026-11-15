package npm

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

func w12gLoc(path, host string) npmapi.Location {
	return npmapi.Location{Path: path, ForwardScheme: "http", ForwardHost: host, ForwardPort: 80}
}

func TestW12gForwardTargetsEnabledHostsOnly(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	w12aSeed(h, "10.9.0.20", 80)
	w12aSeed(h, "10.9.0.21", 80, w12gLoc("/x", "10.9.0.22"))
	h.npm.DisableProxyHost(2)
	got, err := h.nm().forwardTargets(context.Background(), h.env(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].addr != netip.MustParseAddr("10.9.0.20") || got[0].where != "proxy host 1" {
		t.Errorf("targets %+v, want only proxy host 1's 10.9.0.20", got)
	}
}

func TestW12gUnansweredForwards(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	w12aSeed(h, "def.invalid", 80,
		w12gLoc("/a", "gone.invalid"), w12gLoc("/b", "10.9.0.5"), w12gLoc("/c", "$host"), w12gLoc("/d", "ok.invalid"), w12gLoc("/e", "gone2.invalid/x"))
	w12aSeed(h, "def2.invalid", 80, w12gLoc("/z", "off.invalid"))
	h.npm.DisableProxyHost(2)
	n := h.nm()
	n.d.resolve = func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "ok.invalid" {
			return []netip.Addr{netip.MustParseAddr("203.0.113.9")}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	got, err := n.unansweredForwards(context.Background(), h.env(""))
	if err != nil {
		t.Fatal(err)
	}
	want := []unansweredForward{{Where: "location /a of proxy host 1", Host: "gone.invalid"}, {Where: "location /e of proxy host 1", Host: "gone2.invalid"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unanswered %+v, want %+v (the default forward host, a literal, a variable, an answering name and a disabled host are not listed)", got, want)
	}
	wantText := "location /a of proxy host 1 forwards to gone.invalid, which has no answer; location /e of proxy host 1 forwards to gone2.invalid, which has no answer"
	if text := unansweredDetail(got); text != wantText {
		t.Errorf("detail %q, want %q", text, wantText)
	}
	targets, err := n.forwardTargets(context.Background(), h.env(""))
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range targets {
		if !tg.addr.IsValid() || tg.host == "gone.invalid" || tg.host == "gone2.invalid" {
			t.Errorf("target %+v: an unanswered location reached the join guard's targets", tg)
		}
	}
}

func TestW12gUnresolvedNameRefusal(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	w12aSeed(h, "web-old", 80)
	n := h.nm()
	n.d.resolve = func(_ context.Context, host string) ([]netip.Addr, error) {
		return nil, &net.DNSError{Err: "server misbehaving", Name: host, IsTemporary: true}
	}
	err := n.forwardGuard(context.Background(), h.env(""), user, netip.Addr{})
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.NPMForwardUnresolved || !core.IsDefinite(err) {
		t.Fatalf("error %v, want a definite %s", err, errcode.NPMForwardUnresolved)
	}
	want := "proxy host 1 forwards to web-old, which could not be resolved: the resolver did not answer; a stopped or removed container's name is a usual cause"
	if coded.Detail != want {
		t.Errorf("detail %q, want %q", coded.Detail, want)
	}
}
