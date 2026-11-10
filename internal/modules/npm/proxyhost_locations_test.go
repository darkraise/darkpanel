package npm

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

func TestW11c01UndoKeepsLocationsItCannotCheck(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, host string
		port       int
		want       errcode.Code
	}{
		{"an nginx variable", "$upstream", 9000, ""},
		{"an empty host", "", 9000, ""},
		{"a name with no answer", "only-the-container", 9000, ""},
		{"a bracketed IPv6 literal", "[2001:db8::1]", 9000, ""},
		{"a reserved upstream", "127.0.0.1", 81, errcode.NPMReserved},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTHHost(t)
			h.deployed()
			h.addChange("c0", "npm.admin.expose")
			body := proxyHostBody(puInput(), "")
			body.Locations = append(body.Locations, npmapi.Location{Path: "/api", ForwardScheme: "http", ForwardHost: tc.host, ForwardPort: tc.port})
			host := h.npm.SeedProxyHost(puHostOf(body))
			h.addChange("c1", "npm.proxyhost.update")
			n := h.nm()
			resolve := n.d.resolve
			// Only the host's own forward name resolves, so a location's check gets no answer.
			n.d.resolve = func(ctx context.Context, name string) ([]netip.Addr, error) {
				if name != "app" {
					return nil, nil
				}
				return resolve(ctx, name)
			}
			def := n.proxyhostUpdate()
			next := puInput(func(in *ProxyHostInput) { in.ForwardPort = 9090 })
			if _, err := def.Handler.Apply(ctx, h.env("c1"), puJSON(t, ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: next})); err != nil {
				t.Fatalf("the update refused a kept location: %v", err)
			}
			effects := h.journal.effects()
			_, err := def.Handler.Inverse(ctx, h.env("c1"), effects[len(effects)-1])
			if tc.want != "" {
				if errcode.From(err).Code != tc.want || !core.IsDefinite(err) {
					t.Fatalf("inverse = %v, want a definite %s", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("inverse = %v, want the location restored", err)
			}
			hosts := puHosts(t, h)
			if len(hosts) != 1 || hosts[0].Locations[len(hosts[0].Locations)-1].ForwardHost != tc.host {
				t.Errorf("NPM holds %+v, want the location forwarding to %q", hosts, tc.host)
			}
		})
	}
}

func TestW11c01RemoveUndoKeepsALocationItCannotCheck(t *testing.T) {
	ctx := context.Background()
	h := newTHHost(t)
	h.deployed()
	h.addChange("c0", "npm.admin.expose")
	body := proxyHostBody(puInput(), "")
	body.Locations = append(body.Locations, npmapi.Location{Path: "/api", ForwardScheme: "http", ForwardHost: "$upstream", ForwardPort: 9000})
	host := h.npm.SeedProxyHost(puHostOf(body))
	h.addChange("c1", "npm.proxyhost.remove")
	n := h.nm()
	resolve := n.d.resolve
	n.d.resolve = func(ctx context.Context, name string) ([]netip.Addr, error) {
		if name != "app" {
			return nil, nil
		}
		return resolve(ctx, name)
	}
	def := n.proxyhostRemove()
	if _, err := def.Handler.Apply(ctx, h.env("c1"), puJSON(t, ProxyHostRemoveInput{ID: host.ID})); err != nil {
		t.Fatal(err)
	}
	e := puEffects(t, h)[0]
	e.UndoTo = &core.StateDoc{State: e.Before.State}
	if _, err := def.Handler.Inverse(ctx, h.env("c1"), e); err != nil {
		t.Fatalf("inverse = %v, want the location restored", err)
	}
	hosts := puHosts(t, h)
	if len(hosts) != 1 || hosts[0].Locations[len(hosts[0].Locations)-1].ForwardHost != "$upstream" {
		t.Errorf("NPM holds %+v, want the location forwarding to $upstream", hosts)
	}
}

func TestW11c01ContextEndingDuringALocationCheckStopsTheUndo(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := newTHHost(t)
	h.deployed()
	h.addChange("c0", "npm.admin.expose")
	body := proxyHostBody(puInput(), "")
	body.Locations = append(body.Locations, npmapi.Location{Path: "/api", ForwardScheme: "http", ForwardHost: "ends-the-context", ForwardPort: 9000})
	host := h.npm.SeedProxyHost(puHostOf(body))
	h.addChange("c1", "npm.proxyhost.update")
	n := h.nm()
	resolve := n.d.resolve
	armed := false
	n.d.resolve = func(ctx context.Context, name string) ([]netip.Addr, error) {
		if name == "ends-the-context" {
			if armed {
				cancel()
			}
			return nil, errors.New("no answer in time")
		}
		return resolve(ctx, name)
	}
	def := n.proxyhostUpdate()
	next := puInput(func(in *ProxyHostInput) { in.ForwardPort = 9090 })
	if _, err := def.Handler.Apply(ctx, h.env("c1"), puJSON(t, ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: next})); err != nil {
		t.Fatal(err)
	}
	effects := h.journal.effects()
	armed = true
	_, err := def.Handler.Inverse(ctx, h.env("c1"), effects[len(effects)-1])
	if !errors.Is(err, context.Canceled) || core.IsDefinite(err) {
		t.Fatalf("inverse = %v, want the cancellation and no definite mark", err)
	}
}

func TestW11c01EngineFailureDuringALocationCheckStopsTheUndo(t *testing.T) {
	ctx := context.Background()
	h := newTHHost(t)
	h.deployed()
	h.addChange("c0", "npm.admin.expose")
	body := proxyHostBody(puInput(), "")
	body.Locations = append(body.Locations, npmapi.Location{Path: "/api", ForwardScheme: "http", ForwardHost: "backend", ForwardPort: 9000})
	host := h.npm.SeedProxyHost(puHostOf(body))
	h.addChange("c1", "npm.proxyhost.update")
	n := h.nm()
	resolve := n.d.resolve
	armed := false
	n.d.resolve = func(ctx context.Context, name string) ([]netip.Addr, error) {
		if armed && name == "app" {
			armed = false
			h.docker.Fail("InspectContainer", dockerapi.ErrUnavailable)
		}
		return resolve(ctx, name)
	}
	def := n.proxyhostUpdate()
	next := puInput(func(in *ProxyHostInput) { in.ForwardPort = 9090 })
	if _, err := def.Handler.Apply(ctx, h.env("c1"), puJSON(t, ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: next})); err != nil {
		t.Fatal(err)
	}
	before := puHosts(t, h)
	effects := h.journal.effects()
	armed = true
	_, err := def.Handler.Inverse(ctx, h.env("c1"), effects[len(effects)-1])
	if err == nil || core.IsDefinite(err) {
		t.Fatalf("inverse = %v, want the Engine failure and no definite mark", err)
	}
	if armed {
		t.Error("the host's forward name was never resolved, so the fault did not reach the location's check")
	}
	if got := puHosts(t, h); !reflect.DeepEqual(got, before) {
		t.Errorf("a stopped undo changed NPM to %+v", got)
	}
}
