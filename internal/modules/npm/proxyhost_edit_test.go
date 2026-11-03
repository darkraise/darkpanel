package npm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

const puDomain = "app.example.com"

// puInput is a valid proxy host input for puDomain; each change edits it.
func puInput(change ...func(*ProxyHostInput)) ProxyHostInput {
	in := ProxyHostInput{Domains: []string{puDomain}, ForwardScheme: "http", ForwardHost: "app", ForwardPort: 8080}
	for _, c := range change {
		c(&in)
	}
	return in
}

// puHostOf is the proxy host NPM holds for body.
func puHostOf(b npmapi.ProxyHostBody) npmapi.ProxyHost {
	return npmapi.ProxyHost{
		DomainNames: b.DomainNames, ForwardScheme: b.ForwardScheme, ForwardHost: b.ForwardHost, ForwardPort: b.ForwardPort,
		CertificateID: b.CertificateID, AccessListID: b.AccessListID, SSLForced: b.SSLForced, HSTSEnabled: b.HSTSEnabled,
		AllowWebsocketUpgrade: b.AllowWebsocketUpgrade, Locations: b.Locations, AdvancedConfig: b.AdvancedConfig,
	}
}

// puBodyOf is the body members of h.
func puBodyOf(h npmapi.ProxyHost) npmapi.ProxyHostBody {
	return npmapi.ProxyHostBody{
		DomainNames: h.DomainNames, ForwardScheme: h.ForwardScheme, ForwardHost: h.ForwardHost, ForwardPort: h.ForwardPort,
		CertificateID: h.CertificateID, AccessListID: h.AccessListID, SSLForced: h.SSLForced, HSTSEnabled: h.HSTSEnabled,
		AllowWebsocketUpgrade: h.AllowWebsocketUpgrade, Locations: h.Locations, AdvancedConfig: h.AdvancedConfig,
	}
}

// puSeed is a host with a deployed instance and one proxy host made from in.
func puSeed(t *testing.T, in ProxyHostInput) (*thHost, npmapi.ProxyHost) {
	t.Helper()
	h := newTHHost(t)
	h.deployed()
	h.addChange("c0", "npm.admin.expose")
	return h, h.npm.SeedProxyHost(puHostOf(proxyHostBody(in, "")))
}

// puHosts lists the proxy hosts NPM holds, read the way the module reads them.
func puHosts(t *testing.T, h *thHost) []npmapi.ProxyHost {
	t.Helper()
	api, err := h.nm().d.api(context.Background(), h.env(""))
	if err != nil {
		t.Fatal(err)
	}
	hosts, err := api.ProxyHosts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return hosts
}

func puJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func puCoded(t *testing.T, err error, code errcode.Code) {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != code {
		t.Fatalf("error %v, want code %s", err, code)
	}
}

// puStateBody decodes the State of a proxy host effect document.
func puStateBody(t *testing.T, state json.RawMessage) npmapi.ProxyHostBody {
	t.Helper()
	var b npmapi.ProxyHostBody
	if err := json.Unmarshal(state, &b); err != nil {
		t.Fatalf("state %s is not a proxy host body: %v", state, err)
	}
	return b
}

func puSameJSON(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func puEffects(t *testing.T, h *thHost) []core.Effect {
	t.Helper()
	effects, err := h.journal.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return effects
}

// puReserve records that owner reserved the object of kind and id for the exposure of hostname.
func puReserve(t *testing.T, h *thHost, kind string, id int, owner, hostname string) {
	t.Helper()
	ctx := context.Background()
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		return addReserved(ctx, tx, reservedRow{Kind: kind, ObjectID: id, Owner: owner, Exposure: hostname, ChangeID: "c0"})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func puReserved(t *testing.T, h *thHost, kind string, id int) bool {
	t.Helper()
	_, ok, err := reservedOf(context.Background(), h.db.Read(), kind, id)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// puFault is the failure a fault-injecting NPM client returns; commit makes the call take effect first, as a lost
// response does.
type puFault struct {
	err    error
	commit bool
}

type puFaults struct{ update, create, remove puFault }

// puAPI wraps the module's NPM client with the faults of f.
type puAPI struct {
	npmAPI
	f *puFaults
}

func (a puAPI) UpdateProxyHost(ctx context.Context, id int, b npmapi.ProxyHostBody) (npmapi.ProxyHost, error) {
	return puCall(a.f.update, func() (npmapi.ProxyHost, error) { return a.npmAPI.UpdateProxyHost(ctx, id, b) })
}

func (a puAPI) CreateProxyHost(ctx context.Context, b npmapi.ProxyHostBody) (npmapi.ProxyHost, error) {
	return puCall(a.f.create, func() (npmapi.ProxyHost, error) { return a.npmAPI.CreateProxyHost(ctx, b) })
}

func (a puAPI) DeleteProxyHost(ctx context.Context, id int) error {
	_, err := puCall(a.f.remove, func() (npmapi.ProxyHost, error) { return npmapi.ProxyHost{}, a.npmAPI.DeleteProxyHost(ctx, id) })
	return err
}

func puCall(f puFault, call func() (npmapi.ProxyHost, error)) (npmapi.ProxyHost, error) {
	if f.err == nil {
		return call()
	}
	if f.commit {
		if _, err := call(); err != nil {
			return npmapi.ProxyHost{}, err
		}
	}
	return npmapi.ProxyHost{}, f.err
}

// puFaulty is the module over h whose NPM client fails as f says.
func puFaulty(h *thHost, f *puFaults) nm {
	n := h.nm()
	inner := n.d.api
	n.d.api = func(ctx context.Context, env core.Env) (npmAPI, error) {
		a, err := inner(ctx, env)
		if err != nil {
			return nil, err
		}
		return puAPI{npmAPI: a, f: f}, nil
	}
	return n
}

func TestPuDeclarations(t *testing.T) {
	n := newTHHost(t).nm()
	tests := []struct {
		def       core.ActionDef
		name      string
		converges bool
		in        reflect.Type
	}{
		{n.proxyhostUpdate(), "npm.proxyhost.update", false, reflect.TypeFor[ProxyHostUpdateInput]()},
		{n.proxyhostRemove(), "npm.proxyhost.remove", true, reflect.TypeFor[ProxyHostRemoveInput]()},
	}
	for _, tc := range tests {
		s, hd := tc.def.Spec, tc.def.Handler
		if s.Name != tc.name || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
			!slices.Equal(s.LockKeys, []string{"npm"}) || s.Converges != tc.converges || s.LongRunning || s.Task || s.Internal ||
			s.Reveal || s.MinScope != "" || s.Available != nil || len(s.Guards) != 0 || s.CompensatedBy != "" || s.ForwardOnly ||
			s.ApplyTimeout != 0 || s.Summary == "" {
			t.Errorf("%s: spec %+v, want a compensable agent action locking npm with the default timeout (converges %v)", tc.name, s, tc.converges)
		}
		if tc.def.InputType != tc.in || tc.def.OutputType != reflect.TypeFor[ProxyHostView]() {
			t.Errorf("%s: input %v and output %v", tc.name, tc.def.InputType, tc.def.OutputType)
		}
		if hd.Plan == nil || hd.Apply == nil || hd.Verify == nil || hd.Observe == nil || hd.Inverse == nil || hd.Read != nil || hd.Task != nil {
			t.Errorf("%s: handler has Plan %v Apply %v Verify %v Observe %v Inverse %v Read %v Task %v", tc.name,
				hd.Plan != nil, hd.Apply != nil, hd.Verify != nil, hd.Observe != nil, hd.Inverse != nil, hd.Read != nil, hd.Task != nil)
		}
	}
}

func TestPuUpdateAppliesTheInput(t *testing.T) {
	ctx := context.Background()
	h, host := puSeed(t, puInput())
	h.addChange("c1", "npm.proxyhost.update")
	next := puInput(func(in *ProxyHostInput) {
		in.ForwardPort = 9090
		in.Websockets = true
	})
	def := h.nm().proxyhostUpdate()
	raw := puJSON(t, ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: next})

	plan, err := def.Handler.Plan(ctx, h.env(""), raw)
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := []core.DiffLine{
		{Op: "-", Text: "forward: http://app:8080"}, {Op: "+", Text: "forward: http://app:9090"},
		{Op: "-", Text: "websockets: off"}, {Op: "+", Text: "websockets: on"},
	}
	if !slices.Equal(plan.Diff, wantDiff) || len(plan.StateHash) != 64 || plan.Confirmation != "" || len(plan.Guards) != 0 {
		t.Errorf("plan %+v, want diff %+v, a 64-digit hash, no confirmation and no guard", plan, wantDiff)
	}
	if got := puHosts(t, h); len(got) != 1 || got[0].ForwardPort != 8080 || len(puEffects(t, h)) != 0 {
		t.Errorf("plan changed the host: %+v, effects %+v", got, puEffects(t, h))
	}
	again, err := def.Handler.Plan(ctx, h.env(""), raw)
	if err != nil || again.StateHash != plan.StateHash {
		t.Errorf("a second plan hash %q (error %v), want %q", again.StateHash, err, plan.StateHash)
	}

	out, err := def.Handler.Apply(ctx, h.env("c1"), raw)
	if err != nil {
		t.Fatal(err)
	}
	var view ProxyHostView
	if err := json.Unmarshal(out, &view); err != nil {
		t.Fatal(err)
	}
	wantView := ProxyHostView{ID: host.ID, Domains: []string{puDomain}, ForwardScheme: "http", ForwardHost: "app", ForwardPort: 9090, Websockets: true}
	if !reflect.DeepEqual(view, wantView) {
		t.Errorf("view %+v, want %+v", view, wantView)
	}
	hosts := puHosts(t, h)
	wantBody := proxyHostBody(next, "")
	if len(hosts) != 1 || hosts[0].ID != host.ID || !reflect.DeepEqual(puBodyOf(hosts[0]), wantBody) {
		t.Errorf("NPM holds %+v, want one proxy host %d with the body %+v", hosts, host.ID, wantBody)
	}

	effects := puEffects(t, h)
	if len(effects) != 1 {
		t.Fatalf("journal %+v, want one effect", effects)
	}
	e := effects[0]
	if e.Kind != core.EffectModify || e.Resource != "npm.proxyhost:"+strconv.Itoa(host.ID) || e.State != core.EffectMade || e.Local ||
		e.CommitWait != 2*time.Minute || e.Recovery != nil || e.After == nil {
		t.Errorf("effect %+v, want a made non-local modify of npm.proxyhost:%d with a 2 minute commit wait", e, host.ID)
	}
	if got := puStateBody(t, e.Before.State); !reflect.DeepEqual(got, puBodyOf(host)) {
		t.Errorf("before %+v, want the body the host had: %+v", got, puBodyOf(host))
	}
	if got := puStateBody(t, e.After.State); !reflect.DeepEqual(got, wantBody) {
		t.Errorf("after %+v, want the requested body %+v", got, wantBody)
	}
}

func TestPuUpdateRefusals(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name  string
		setup func(t *testing.T, h *thHost, host npmapi.ProxyHost) (parent string, in ProxyHostUpdateInput)
		code  errcode.Code
	}{
		{"an id NPM does not hold", func(_ *testing.T, _ *thHost, host npmapi.ProxyHost) (string, ProxyHostUpdateInput) {
			return "", ProxyHostUpdateInput{ID: host.ID + 100, ProxyHostInput: puInput()}
		}, errcode.NotFound},
		{"no id", func(_ *testing.T, _ *thHost, _ npmapi.ProxyHost) (string, ProxyHostUpdateInput) {
			return "", ProxyHostUpdateInput{ProxyHostInput: puInput()}
		}, errcode.InvalidInput},
		{"an input without a domain", func(_ *testing.T, _ *thHost, host npmapi.ProxyHost) (string, ProxyHostUpdateInput) {
			return "", ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: puInput(func(in *ProxyHostInput) { in.Domains = nil })}
		}, errcode.InvalidInput},
		{"a host reserved by an exposure", func(t *testing.T, h *thHost, host npmapi.ProxyHost) (string, ProxyHostUpdateInput) {
			puReserve(t, h, "proxy_host", host.ID, "npm.admin.expose", puDomain)
			return "", ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: puInput()}
		}, errcode.NPMReserved},
		{"a host reserved by an exposure, under another exposure's removal", func(t *testing.T, h *thHost, host npmapi.ProxyHost) (string, ProxyHostUpdateInput) {
			puReserve(t, h, "proxy_host", host.ID, "npm.admin.expose", puDomain)
			return "panel.exposure.remove", ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: puInput()}
		}, errcode.NPMReserved},
		{"the admin port of NPM as upstream", func(_ *testing.T, _ *thHost, host npmapi.ProxyHost) (string, ProxyHostUpdateInput) {
			in := puInput(func(in *ProxyHostInput) {
				in.ForwardHost = "127.0.0.1"
				in.ForwardPort = 81
			})
			return "", ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: in}
		}, errcode.NPMReserved},
		{"a domain another proxy host serves", func(_ *testing.T, h *thHost, host npmapi.ProxyHost) (string, ProxyHostUpdateInput) {
			other := puInput(func(in *ProxyHostInput) { in.Domains = []string{"other.example.com"} })
			h.npm.SeedProxyHost(puHostOf(proxyHostBody(other, "")))
			in := puInput(func(in *ProxyHostInput) { in.Domains = []string{puDomain, "other.example.com"} })
			return "", ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: in}
		}, errcode.InvalidInput},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, host := puSeed(t, puInput())
			h.addChange("c1", "npm.proxyhost.update")
			parent, in := tc.setup(t, h, host)
			raw := puJSON(t, in)
			def := h.nm().proxyhostUpdate()
			before := puHosts(t, h)

			_, err := def.Handler.Plan(ctx, h.child("", parent), raw)
			puCoded(t, err, tc.code)
			_, err = def.Handler.Apply(ctx, h.child("c1", parent), raw)
			puCoded(t, err, tc.code)
			if !core.IsDefinite(err) {
				t.Errorf("apply error %v is not definite, though nothing was committed", err)
			}
			if got := puHosts(t, h); !reflect.DeepEqual(got, before) || len(puEffects(t, h)) != 0 {
				t.Errorf("a refusal changed NPM to %+v or journaled %+v", got, puEffects(t, h))
			}
		})
	}
}

func TestPuUpdateTheOutcomeOfAFailedCall(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		fault    puFault
		state    core.EffectState
		definite bool
		wantPort int
	}{
		{"refused by NPM", puFault{err: &npmapi.APIError{Status: 400, Message: "forward_port must be valid"}}, core.EffectSkipped, true, 8080},
		{"the response is lost after NPM committed", puFault{err: errors.New("npmapi: connection reset"), commit: true}, core.EffectIntended, false, 9090},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, host := puSeed(t, puInput())
			h.addChange("c1", "npm.proxyhost.update")
			n := puFaulty(h, &puFaults{update: tc.fault})
			next := puInput(func(in *ProxyHostInput) { in.ForwardPort = 9090 })
			_, err := n.proxyhostUpdate().Handler.Apply(ctx, h.env("c1"), puJSON(t, ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: next}))
			if err == nil || core.IsDefinite(err) != tc.definite {
				t.Fatalf("error %v (definite %v), want an error with definite %v", err, core.IsDefinite(err), tc.definite)
			}
			effects := puEffects(t, h)
			if len(effects) != 1 || effects[0].State != tc.state {
				t.Errorf("journal %+v, want one effect in state %s", effects, tc.state)
			}
			if got := puHosts(t, h); len(got) != 1 || got[0].ForwardPort != tc.wantPort {
				t.Errorf("NPM holds %+v, want the forward port %d", got, tc.wantPort)
			}
		})
	}
}

func TestPuUpdateInverseRestoresTheBody(t *testing.T) {
	ctx := context.Background()
	h, host := puSeed(t, puInput(func(in *ProxyHostInput) { in.Domains = []string{puDomain, "www.example.com"} }))
	h.addChange("c1", "npm.proxyhost.update")
	def := h.nm().proxyhostUpdate()
	next := puInput(func(in *ProxyHostInput) {
		in.ForwardHost = "other"
		in.ForwardPort = 9090
	})
	if _, err := def.Handler.Apply(ctx, h.env("c1"), puJSON(t, ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: next})); err != nil {
		t.Fatal(err)
	}
	e := puEffects(t, h)[0]
	e.UndoTo = &core.StateDoc{State: e.Before.State}

	res, err := def.Handler.Inverse(ctx, h.env("c1"), e)
	if err != nil || res.Resource != "" {
		t.Fatalf("inverse = %+v, %v; want no resource and no error", res, err)
	}
	hosts := puHosts(t, h)
	if len(hosts) != 1 || hosts[0].ID != host.ID || !reflect.DeepEqual(puBodyOf(hosts[0]), puBodyOf(host)) {
		t.Errorf("NPM holds %+v, want proxy host %d back at %+v", hosts, host.ID, puBodyOf(host))
	}
	obs, err := def.Handler.Observe(ctx, h.env("c1"), e)
	if err != nil || obs.Matches != 1 || !obs.Exclusive || obs.Resource != e.Resource || !puSameJSON(obs.Current.State, e.UndoTo.State) {
		t.Errorf("observation %+v (%v), want the restored state of %s", obs, err, e.Resource)
	}

	// A host that NPM no longer holds cannot be restored, and the inverse says it changed nothing.
	api, err := h.nm().d.api(ctx, h.env(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := api.DeleteProxyHost(ctx, host.ID); err != nil {
		t.Fatal(err)
	}
	_, err = def.Handler.Inverse(ctx, h.env("c1"), e)
	puCoded(t, err, errcode.NotFound)
	if !core.IsDefinite(err) || len(puHosts(t, h)) != 0 {
		t.Errorf("inverse of a vanished host: error %v (definite %v), hosts %+v; want a definite refusal that created nothing",
			err, core.IsDefinite(err), puHosts(t, h))
	}
}

func TestPuObserveReadsTheHostById(t *testing.T) {
	ctx := context.Background()
	h, host := puSeed(t, puInput())
	def := h.nm().proxyhostUpdate()
	e := core.Effect{ChangeID: "c1", Seq: 1, Kind: core.EffectModify, Resource: "npm.proxyhost:" + strconv.Itoa(host.ID), State: core.EffectMade}

	obs, err := def.Handler.Observe(ctx, h.env(""), e)
	if err != nil || obs.Matches != 1 || !obs.Exclusive || obs.Resource != e.Resource || !reflect.DeepEqual(puStateBody(t, obs.Current.State), puBodyOf(host)) {
		t.Errorf("observation %+v (%v), want the body of host %d", obs, err, host.ID)
	}
	e.Resource = "npm.proxyhost:" + strconv.Itoa(host.ID+50)
	obs, err = def.Handler.Observe(ctx, h.env(""), e)
	if err != nil || obs.Matches != 1 || string(obs.Current.State) != "null" {
		t.Errorf("observation of a missing host %+v (%v), want one match whose state is null", obs, err)
	}
	e.Resource = "npm.proxyhost:?" + puDomain
	if _, err := def.Handler.Observe(ctx, h.env(""), e); err == nil {
		t.Error("observing a creation succeeded, want an error: this action creates nothing")
	}
}

func TestPuRemoveDeletesTheHost(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name  string
		input func(host npmapi.ProxyHost) ProxyHostRemoveInput
	}{
		{"by id", func(host npmapi.ProxyHost) ProxyHostRemoveInput { return ProxyHostRemoveInput{ID: host.ID} }},
		{"by one of its domains", func(npmapi.ProxyHost) ProxyHostRemoveInput { return ProxyHostRemoveInput{Domain: "www.example.com"} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := puInput(func(in *ProxyHostInput) { in.Domains = []string{puDomain, "www.example.com"} })
			h, host := puSeed(t, in)
			other := puInput(func(in *ProxyHostInput) { in.Domains = []string{"keep.example.com"} })
			keep := h.npm.SeedProxyHost(puHostOf(proxyHostBody(other, "")))
			h.addChange("c1", "npm.proxyhost.remove")
			def := h.nm().proxyhostRemove()
			raw := puJSON(t, tc.input(host))

			plan, err := def.Handler.Plan(ctx, h.env(""), raw)
			if err != nil {
				t.Fatal(err)
			}
			wantDiff := []core.DiffLine{{Op: "-", Text: "proxy host app.example.com, www.example.com -> http://app:8080"}}
			if !slices.Equal(plan.Diff, wantDiff) || len(plan.StateHash) != 64 || len(puHosts(t, h)) != 2 {
				t.Errorf("plan %+v, want diff %+v, a 64-digit hash and both hosts still held", plan, wantDiff)
			}

			out, err := def.Handler.Apply(ctx, h.env("c1"), raw)
			if err != nil {
				t.Fatal(err)
			}
			var view ProxyHostView
			if err := json.Unmarshal(out, &view); err != nil {
				t.Fatal(err)
			}
			if view.ID != host.ID || !slices.Equal(view.Domains, []string{puDomain, "www.example.com"}) || view.ForwardHost != "app" || view.ForwardPort != 8080 {
				t.Errorf("view %+v, want the host as removed", view)
			}
			if got := puHosts(t, h); len(got) != 1 || got[0].ID != keep.ID {
				t.Errorf("NPM holds %+v, want only proxy host %d", got, keep.ID)
			}
			effects := puEffects(t, h)
			if len(effects) != 1 {
				t.Fatalf("journal %+v, want one effect", effects)
			}
			e := effects[0]
			wantRecovery := &core.RecoveryID{Kind: "name", Value: "app.example.com,www.example.com"}
			if e.Kind != core.EffectDelete || e.Resource != "npm.proxyhost:"+strconv.Itoa(host.ID) || e.State != core.EffectMade || e.Local ||
				e.CommitWait != 2*time.Minute || e.Recovery != nil || !reflect.DeepEqual(e.InverseRecovery, wantRecovery) ||
				e.After == nil || string(e.After.State) != "null" {
				t.Errorf("effect %+v, want a made non-local delete of the host, ending null, whose inverse is found by %+v", e, wantRecovery)
			}
			if got := puStateBody(t, e.Before.State); !reflect.DeepEqual(got, puBodyOf(host)) {
				t.Errorf("before %+v, want the body the host had: %+v", got, puBodyOf(host))
			}
			if err := def.Handler.Verify(ctx, h.env("c1"), raw, out); err != nil {
				t.Errorf("verify after the removal: %v", err)
			}
		})
	}
}

func TestPuRemoveOfAnAbsentHostConverges(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		in   ProxyHostRemoveInput
		view ProxyHostView
	}{
		{"by id", ProxyHostRemoveInput{ID: 4242}, ProxyHostView{ID: 4242, Domains: []string{}}},
		{"by domain", ProxyHostRemoveInput{Domain: "gone.example.com"}, ProxyHostView{Domains: []string{"gone.example.com"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, host := puSeed(t, puInput())
			h.addChange("c1", "npm.proxyhost.remove")
			def := h.nm().proxyhostRemove()
			raw := puJSON(t, tc.in)

			plan, err := def.Handler.Plan(ctx, h.env(""), raw)
			if err != nil || len(plan.Diff) != 0 {
				t.Fatalf("plan %+v (%v), want an empty diff for an absent host", plan, err)
			}
			out, err := def.Handler.Apply(ctx, h.env("c1"), raw)
			if err != nil {
				t.Fatal(err)
			}
			var view ProxyHostView
			if err := json.Unmarshal(out, &view); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(view, tc.view) {
				t.Errorf("view %+v, want %+v", view, tc.view)
			}
			if got := puHosts(t, h); len(got) != 1 || got[0].ID != host.ID || len(puEffects(t, h)) != 0 {
				t.Errorf("NPM holds %+v and the journal %+v, want the other host untouched and no effect", got, puEffects(t, h))
			}
			if err := def.Handler.Verify(ctx, h.env("c1"), raw, out); err != nil {
				t.Errorf("verify of an absent host: %v", err)
			}
		})
	}
}

func TestPuRemoveRefusesAnAmbiguousTarget(t *testing.T) {
	ctx := context.Background()
	h, host := puSeed(t, puInput())
	h.addChange("c1", "npm.proxyhost.remove")
	def := h.nm().proxyhostRemove()
	for name, in := range map[string]ProxyHostRemoveInput{
		"an id and a domain": {ID: host.ID, Domain: puDomain},
		"neither":            {},
		"a negative id":      {ID: -1},
	} {
		raw := puJSON(t, in)
		_, err := def.Handler.Plan(ctx, h.env(""), raw)
		puCoded(t, err, errcode.InvalidInput)
		_, err = def.Handler.Apply(ctx, h.env("c1"), raw)
		puCoded(t, err, errcode.InvalidInput)
		if !core.IsDefinite(err) || len(puHosts(t, h)) != 1 || len(puEffects(t, h)) != 0 {
			t.Errorf("%s: error %v (definite %v), hosts %+v, effects %+v", name, err, core.IsDefinite(err), puHosts(t, h), puEffects(t, h))
		}
	}
}

func TestPuRemoveOfAReservedHost(t *testing.T) {
	ctx := context.Background()
	const admin = "admin.example.com"
	seed := func(t *testing.T) (*thHost, npmapi.ProxyHost) {
		h, host := puSeed(t, puInput(func(in *ProxyHostInput) { in.Domains = []string{admin} }))
		h.addChange("c1", "npm.proxyhost.remove")
		puReserve(t, h, "proxy_host", host.ID, "npm.admin.expose", admin)
		puReserve(t, h, "certificate", 77, "npm.admin.expose", admin)
		puReserve(t, h, "certificate", 88, "npm.admin.expose", "other.example.com")
		return h, host
	}

	for _, parent := range []string{"", "npm.admin.expose", "panel.exposure.remove"} {
		t.Run("refused under "+parent, func(t *testing.T) {
			h, host := seed(t)
			raw := puJSON(t, ProxyHostRemoveInput{ID: host.ID})
			def := h.nm().proxyhostRemove()
			_, err := def.Handler.Plan(ctx, h.child("", parent), raw)
			puCoded(t, err, errcode.NPMReserved)
			_, err = def.Handler.Apply(ctx, h.child("c1", parent), raw)
			puCoded(t, err, errcode.NPMReserved)
			if !core.IsDefinite(err) || len(puHosts(t, h)) != 1 || len(puEffects(t, h)) != 0 || !puReserved(t, h, "proxy_host", host.ID) {
				t.Errorf("a refusal left error %v (definite %v), hosts %+v, effects %+v", err, core.IsDefinite(err), puHosts(t, h), puEffects(t, h))
			}
		})
	}

	t.Run("removed by the owning exposure's removal", func(t *testing.T) {
		h, host := seed(t)
		raw := puJSON(t, ProxyHostRemoveInput{Domain: admin})
		def := h.nm().proxyhostRemove()
		out, err := def.Handler.Apply(ctx, h.child("c1", "npm.admin.unexpose"), raw)
		if err != nil {
			t.Fatal(err)
		}
		var view ProxyHostView
		if err := json.Unmarshal(out, &view); err != nil {
			t.Fatal(err)
		}
		if view.ID != host.ID || view.Reserved != "npm.admin.expose" || len(puHosts(t, h)) != 0 {
			t.Errorf("view %+v, hosts %+v; want the reserved host removed and reported as reserved by npm.admin.expose", view, puHosts(t, h))
		}
		if puReserved(t, h, "proxy_host", host.ID) || puReserved(t, h, "certificate", 77) {
			t.Error("the exposure's reservation records are still held, want the proxy host's and the certificate's dropped")
		}
		if !puReserved(t, h, "certificate", 88) {
			t.Error("the reservation record of another exposure was dropped")
		}
	})

	t.Run("a repeated removal drops what a cut-off one left", func(t *testing.T) {
		h, _ := seed(t)
		api, err := h.nm().d.api(ctx, h.env(""))
		if err != nil {
			t.Fatal(err)
		}
		hosts := puHosts(t, h)
		if err := api.DeleteProxyHost(ctx, hosts[0].ID); err != nil {
			t.Fatal(err)
		}
		raw := puJSON(t, ProxyHostRemoveInput{Domain: admin})
		def := h.nm().proxyhostRemove()

		if _, err := def.Handler.Apply(ctx, h.env("c1"), raw); err != nil || !puReserved(t, h, "certificate", 77) {
			t.Fatalf("a removal outside an exposure's removal: error %v, certificate record kept %v; want it left alone", err, puReserved(t, h, "certificate", 77))
		}
		if _, err := def.Handler.Apply(ctx, h.child("c1", "npm.admin.unexpose"), raw); err != nil {
			t.Fatal(err)
		}
		if puReserved(t, h, "certificate", 77) || !puReserved(t, h, "certificate", 88) {
			t.Error("the repeated removal did not drop exactly the records of the exposure")
		}
	})
}

func TestPuRemoveInverseRecreatesTheHost(t *testing.T) {
	ctx := context.Background()
	in := puInput(func(in *ProxyHostInput) { in.Domains = []string{puDomain, "www.example.com"} })
	h, host := puSeed(t, in)
	h.addChange("c1", "npm.proxyhost.remove")
	def := h.nm().proxyhostRemove()
	if _, err := def.Handler.Apply(ctx, h.env("c1"), puJSON(t, ProxyHostRemoveInput{ID: host.ID})); err != nil {
		t.Fatal(err)
	}
	e := puEffects(t, h)[0]
	e.UndoTo = &core.StateDoc{State: e.Before.State}

	made, err := def.Handler.Observe(ctx, h.env("c1"), e)
	if err != nil || made.Matches != 1 || string(made.Current.State) != "null" {
		t.Fatalf("observation of the removed host %+v (%v), want one match whose state is null", made, err)
	}
	res, err := def.Handler.Inverse(ctx, h.env("c1"), e)
	if err != nil {
		t.Fatal(err)
	}
	hosts := puHosts(t, h)
	if len(hosts) != 1 || !reflect.DeepEqual(puBodyOf(hosts[0]), puBodyOf(host)) || res.Resource != "npm.proxyhost:"+strconv.Itoa(hosts[0].ID) {
		t.Fatalf("inverse = %+v; NPM holds %+v, want one host with the removed body whose resource is returned", res, hosts)
	}

	e.State = core.EffectUndoing
	for name, undoResource := range map[string]string{"by the object the inverse returned": res.Resource, "by the inverse's recovery identity": ""} {
		e.UndoResource = undoResource
		obs, err := def.Handler.Observe(ctx, h.env("c1"), e)
		if err != nil || obs.Matches != 1 || !obs.Exclusive || obs.Resource != res.Resource || !puSameJSON(obs.Current.State, e.UndoTo.State) {
			t.Errorf("%s: observation %+v (%v), want one match, %s, in the restored state", name, obs, err, res.Resource)
		}
	}
}

func TestPuRemoveTheOutcomeOfAFailedCall(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		fault    puFault
		state    core.EffectState
		definite bool
		held     int
	}{
		{"refused by NPM", puFault{err: &npmapi.APIError{Status: 400, Message: "in use"}}, core.EffectSkipped, true, 1},
		{"the response is lost after NPM committed", puFault{err: errors.New("npmapi: connection reset"), commit: true}, core.EffectIntended, false, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, host := puSeed(t, puInput())
			h.addChange("c1", "npm.proxyhost.remove")
			n := puFaulty(h, &puFaults{remove: tc.fault})
			_, err := n.proxyhostRemove().Handler.Apply(ctx, h.env("c1"), puJSON(t, ProxyHostRemoveInput{ID: host.ID}))
			if err == nil || core.IsDefinite(err) != tc.definite {
				t.Fatalf("error %v (definite %v), want an error with definite %v", err, core.IsDefinite(err), tc.definite)
			}
			effects := puEffects(t, h)
			if len(effects) != 1 || effects[0].State != tc.state || len(puHosts(t, h)) != tc.held {
				t.Errorf("journal %+v, hosts %+v; want one effect in state %s and %d host(s) held", effects, puHosts(t, h), tc.state, tc.held)
			}
		})
	}
}

func TestPuVerifyChecksTheUpdatedHost(t *testing.T) {
	ctx := context.Background()
	h, host := puSeed(t, puInput())
	h.addChange("c1", "npm.proxyhost.update")
	def := h.nm().proxyhostUpdate()
	next := puInput(func(in *ProxyHostInput) { in.ForwardPort = 9090 })
	raw := puJSON(t, ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: next})

	if err := def.Handler.Verify(ctx, h.env("c1"), raw, nil); err == nil {
		t.Error("verify before the update succeeded, want an error: the host still forwards to port 8080")
	}
	out, err := def.Handler.Apply(ctx, h.env("c1"), raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), raw, out); err != nil {
		t.Errorf("verify after the update: %v", err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), raw, nil); err != nil {
		t.Errorf("verify without an output, as recovery calls it: %v", err)
	}
	api, err := h.nm().d.api(ctx, h.env(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := api.DeleteProxyHost(ctx, host.ID); err != nil {
		t.Fatal(err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), raw, out); err == nil {
		t.Error("verify of a vanished host succeeded, want an error")
	}
}
