package npm

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

const pwRepair = "panel.websockets.repair"

// pwAPI records the body of every proxy host update the module sends.
type pwAPI struct {
	npmAPI
	puts *[]npmapi.ProxyHostBody
}

func (a pwAPI) UpdateProxyHost(ctx context.Context, id int, b npmapi.ProxyHostBody) (npmapi.ProxyHost, error) {
	*a.puts = append(*a.puts, b)
	return a.npmAPI.UpdateProxyHost(ctx, id, b)
}

// pwModule is the module over h whose NPM client records its updates in puts.
func pwModule(h *thHost, puts *[]npmapi.ProxyHostBody) nm {
	n := h.nm()
	inner := n.d.api
	n.d.api = func(ctx context.Context, env core.Env) (npmAPI, error) {
		a, err := inner(ctx, env)
		if err != nil {
			return nil, err
		}
		return pwAPI{npmAPI: a, puts: puts}, nil
	}
	return n
}

// pwPanelInput is the proxy host panel.exposure.add makes for puDomain, without WebSocket upgrades.
func pwPanelInput() ProxyHostInput {
	return ProxyHostInput{Domains: []string{puDomain}, ForwardScheme: "https", ForwardHost: "172.30.0.1", ForwardPort: 9443, CertificateID: 3, SSLForced: true, HSTS: true}
}

// pwSeed is a host with a deployed instance and one proxy host made from in under parent, reserved by owner when owner
// is not "".
func pwSeed(t *testing.T, in ProxyHostInput, parent, owner string) (*thHost, npmapi.ProxyHost) {
	t.Helper()
	h := newTHHost(t)
	h.deployed()
	h.addChange("c0", "npm.admin.expose")
	host := h.npm.SeedProxyHost(puHostOf(proxyHostBody(in, parent)))
	if owner != "" {
		puReserve(t, h, "proxy_host", host.ID, owner, puDomain)
	}
	h.addChange("c1", "npm.proxyhost.websockets")
	return h, host
}

func TestProxyHostWebsockets(t *testing.T) {
	ctx := context.Background()
	on := puJSON(t, ProxyHostWebsocketsInput{Domain: puDomain, Websockets: true})

	t.Run("the panel's host under its repair", func(t *testing.T) {
		h, host := pwSeed(t, pwPanelInput(), proxyhostAddPanelOwner, proxyhostAddPanelOwner)
		var puts []npmapi.ProxyHostBody
		def := pwModule(h, &puts).proxyhostWebsockets()

		plan, err := def.Handler.Plan(ctx, h.child("", pwRepair), on)
		if err != nil {
			t.Fatal(err)
		}
		wantDiff := []core.DiffLine{{Op: "-", Text: "websockets: off"}, {Op: "+", Text: "websockets: on"}}
		if !slices.Equal(plan.Diff, wantDiff) || len(plan.StateHash) != 64 || len(plan.Guards) != 0 {
			t.Errorf("plan %+v, want only the diff %+v and a 64-digit hash", plan, wantDiff)
		}
		if len(puts) != 0 {
			t.Fatalf("plan sent %d updates", len(puts))
		}

		out, err := def.Handler.Apply(ctx, h.child("c1", pwRepair), on)
		if err != nil {
			t.Fatal(err)
		}
		want := proxyHostEditBody(host)
		want.AllowWebsocketUpgrade = true
		if len(puts) != 1 || !reflect.DeepEqual(puts[0], want) {
			t.Fatalf("updates %+v, want one with the host's own body and the flag on: %+v", puts, want)
		}
		root := puts[0].Locations[0].AdvancedConfig
		if root != host.Locations[0].AdvancedConfig || strings.Count(root, "proxy_read_timeout 3600s;") != 1 {
			t.Errorf("location / advanced configuration %q, want %q with the panel's timeout once", root, host.Locations[0].AdvancedConfig)
		}
		var view ProxyHostView
		if err := json.Unmarshal(out, &view); err != nil {
			t.Fatal(err)
		}
		if !view.Websockets || view.Reserved != proxyhostAddPanelOwner || view.ID != host.ID {
			t.Errorf("view %+v, want host %d with websockets, reserved by %s", view, host.ID, proxyhostAddPanelOwner)
		}
		if err := def.Handler.Verify(ctx, h.env("c1"), on, out); err != nil {
			t.Errorf("verify: %v", err)
		}

		effects := puEffects(t, h)
		if len(effects) != 1 || effects[0].Kind != core.EffectModify || effects[0].State != core.EffectMade || effects[0].Resource != resourceOf(nounProxyHost, strconv.Itoa(host.ID)) {
			t.Fatalf("journal %+v, want one made modify of the host", effects)
		}
		e := effects[0]
		if got := puStateBody(t, e.Before.State); got.AllowWebsocketUpgrade || got.ForwardHost != host.ForwardHost {
			t.Errorf("before %+v, want the host's body with the flag off", got)
		}

		// The undo outside the repair is refused like the change, and under it writes the body back.
		e.UndoTo = &core.StateDoc{State: e.Before.State}
		if _, err := def.Handler.Inverse(ctx, h.env("c1"), e); err == nil || !core.IsDefinite(err) || errcode.From(err).Code != errcode.NPMReserved {
			t.Errorf("inverse with no parent = %v, want a definite npm.reserved", err)
		}
		if _, err := def.Handler.Inverse(ctx, h.child("c1", pwRepair), e); err != nil {
			t.Fatal(err)
		}
		hosts := puHosts(t, h)
		if len(hosts) != 1 || !reflect.DeepEqual(puBodyOf(hosts[0]), puBodyOf(host)) {
			t.Errorf("NPM holds %+v after the undo, want the host back at %+v", hosts, puBodyOf(host))
		}
		if len(puts) != 2 || puts[1].AllowWebsocketUpgrade {
			t.Errorf("updates %+v, want the undo to send the flag off", puts)
		}
	})

	refusals := []struct {
		name, owner, parent string
	}{
		{"the panel's host with no parent", proxyhostAddPanelOwner, ""},
		{"the panel's host under its add", proxyhostAddPanelOwner, proxyhostAddPanelOwner},
		{"the panel's host under its removal", proxyhostAddPanelOwner, "panel.exposure.remove"},
		{"the admin exposure's host with no parent", proxyhostAddAdminOwner, ""},
		{"the admin exposure's host under the panel's repair", proxyhostAddAdminOwner, pwRepair},
		{"the admin exposure's host under its removal", proxyhostAddAdminOwner, "npm.admin.unexpose"},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := pwSeed(t, pwPanelInput(), proxyhostAddPanelOwner, tc.owner)
			var puts []npmapi.ProxyHostBody
			def := pwModule(h, &puts).proxyhostWebsockets()
			before := puHosts(t, h)
			for _, run := range []func() error{
				func() error { _, err := def.Handler.Plan(ctx, h.child("", tc.parent), on); return err },
				func() error { _, err := def.Handler.Apply(ctx, h.child("c1", tc.parent), on); return err },
			} {
				err := run()
				puCoded(t, err, errcode.NPMReserved)
				if !core.IsDefinite(err) {
					t.Errorf("error %v is not definite, though nothing was sent", err)
				}
			}
			if len(puts) != 0 || len(puEffects(t, h)) != 0 || !reflect.DeepEqual(puHosts(t, h), before) {
				t.Errorf("a refusal sent %+v or journaled %+v", puts, puEffects(t, h))
			}
		})
	}

	t.Run("an unreserved host for an admin", func(t *testing.T) {
		h, host := pwSeed(t, puInput(), "", "")
		var puts []npmapi.ProxyHostBody
		def := pwModule(h, &puts).proxyhostWebsockets()
		if _, err := def.Handler.Apply(ctx, h.env("c1"), on); err != nil {
			t.Fatal(err)
		}
		want := proxyHostEditBody(host)
		want.AllowWebsocketUpgrade = true
		if len(puts) != 1 || !reflect.DeepEqual(puts[0], want) || !puHosts(t, h)[0].AllowWebsocketUpgrade {
			t.Errorf("updates %+v, want one turning the flag on", puts)
		}
	})

	t.Run("a host that already has the flag", func(t *testing.T) {
		in := pwPanelInput()
		in.Websockets = true
		h, host := pwSeed(t, in, proxyhostAddPanelOwner, proxyhostAddPanelOwner)
		var puts []npmapi.ProxyHostBody
		def := pwModule(h, &puts).proxyhostWebsockets()
		plan, err := def.Handler.Plan(ctx, h.child("", pwRepair), on)
		if err != nil || len(plan.Diff) != 0 {
			t.Errorf("plan %+v (%v), want an empty diff", plan, err)
		}
		out, err := def.Handler.Apply(ctx, h.child("c1", pwRepair), on)
		if err != nil {
			t.Fatal(err)
		}
		var view ProxyHostView
		if err := json.Unmarshal(out, &view); err != nil || view.ID != host.ID || !view.Websockets {
			t.Errorf("view %+v (%v), want host %d with websockets", view, err, host.ID)
		}
		if len(puts) != 0 || len(puEffects(t, h)) != 0 {
			t.Errorf("a converged apply sent %+v and journaled %+v", puts, puEffects(t, h))
		}
	})

	t.Run("a domain no host serves", func(t *testing.T) {
		h, _ := pwSeed(t, puInput(), "", "")
		var puts []npmapi.ProxyHostBody
		def := pwModule(h, &puts).proxyhostWebsockets()
		other := puJSON(t, ProxyHostWebsocketsInput{Domain: "other.example.com", Websockets: true})
		_, err := def.Handler.Plan(ctx, h.env(""), other)
		puCoded(t, err, errcode.NotFound)
		_, err = def.Handler.Apply(ctx, h.env("c1"), other)
		puCoded(t, err, errcode.NotFound)
		if !core.IsDefinite(err) || len(puts) != 0 {
			t.Errorf("apply error %v (definite %v), updates %+v; want a definite refusal that sent nothing", err, core.IsDefinite(err), puts)
		}
		_, err = def.Handler.Apply(ctx, h.env("c1"), puJSON(t, ProxyHostWebsocketsInput{Websockets: true}))
		puCoded(t, err, errcode.InvalidInput)
	})
}
