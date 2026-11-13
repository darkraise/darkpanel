package npm

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

const (
	v1r10HostSecret = "placeholder-secret-host"
	v1r10LocSecret  = "placeholder-secret-location"
)

func v1r10Env() core.Env { return core.Env{Sealer: coretest.Sealer{}} }

// v1r10Fingerprint computes the documented fingerprint without the module's helper.
func v1r10Fingerprint(value string) string {
	m := hmac.New(sha256.New, coretest.Sealer{}.Derive("npm.effect.fingerprint/v1"))
	m.Write([]byte(value))
	return "hmac-sha256:" + hex.EncodeToString(m.Sum(nil))
}

// v1r10Doc is b as an effect document holds it: every advanced configuration replaced by its fingerprint.
func v1r10Doc(b npmapi.ProxyHostBody) npmapi.ProxyHostBody {
	b.AdvancedConfig = v1r10Fingerprint(b.AdvancedConfig)
	b.Locations = append([]npmapi.Location(nil), b.Locations...)
	for i := range b.Locations {
		b.Locations[i].AdvancedConfig = v1r10Fingerprint(b.Locations[i].AdvancedConfig)
	}
	return b
}

// v1r10NoSecret fails when the JSON of v holds either placeholder secret.
func v1r10NoSecret(t *testing.T, what string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{v1r10HostSecret, v1r10LocSecret} {
		if strings.Contains(string(b), s) {
			t.Errorf("%s holds %q: %s", what, s, b)
		}
	}
}

// v1r10Seed is a host with a deployed instance and one proxy host whose NPM configuration carries a placeholder
// credential at the host level and in a custom location the operator wrote.
func v1r10Seed(t *testing.T) (*thHost, npmapi.ProxyHost) {
	t.Helper()
	h := newTHHost(t)
	h.deployed()
	h.addChange("c0", "npm.admin.expose")
	body := proxyHostBody(puInput(), "")
	body.AdvancedConfig = `proxy_set_header Authorization "Bearer ` + v1r10HostSecret + `";`
	body.Locations = append(body.Locations, npmapi.Location{
		Path: "/api", ForwardScheme: "http", ForwardHost: "app", ForwardPort: 9000,
		AdvancedConfig: "proxy_set_header X-Api-Key " + v1r10LocSecret + ";",
	})
	return h, h.npm.SeedProxyHost(puHostOf(body))
}

// v1r10Sealed decodes the body sealed with effect seq.
func v1r10Sealed(t *testing.T, h *thHost, seq int) npmapi.ProxyHostBody {
	t.Helper()
	raw, err := h.journal.Secret(context.Background(), seq)
	if err != nil || len(raw) == 0 {
		t.Fatalf("effect %d has no sealed body: %q, %v", seq, raw, err)
	}
	var b npmapi.ProxyHostBody
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("the sealed body of effect %d is not a proxy host body: %v", seq, err)
	}
	return b
}

type v1r10OtherKey struct{ coretest.Sealer }

func (v1r10OtherKey) Derive(label string) []byte { return []byte("another role key for " + label) }

func TestV1r10Fingerprint(t *testing.T) {
	env := v1r10Env()
	got := npmFingerprint(env, "placeholder-secret")
	if want := v1r10Fingerprint("placeholder-secret"); got != want {
		t.Errorf("npmFingerprint = %q, want %q", got, want)
	}
	if digits, ok := strings.CutPrefix(got, "hmac-sha256:"); !ok || len(digits) != 64 || strings.ToLower(digits) != digits {
		t.Errorf("fingerprint %q is not hmac-sha256: and 64 lower-case hex digits", got)
	}
	if other := npmFingerprint(env, "another-secret"); other == got {
		t.Error("two values share a fingerprint")
	}
	other := core.Env{Sealer: v1r10OtherKey{}}
	if npmFingerprint(other, "placeholder-secret") == got {
		t.Error("the fingerprint does not depend on the role key")
	}
}

func TestV1r10UpdateEffectsHoldFingerprintsAndTheSealedBody(t *testing.T) {
	ctx := context.Background()
	h, host := v1r10Seed(t)
	h.addChange("c1", "npm.proxyhost.update")
	def := h.nm().proxyhostUpdate()
	next := puInput(func(in *ProxyHostInput) { in.ForwardPort = 9090 })
	raw := puJSON(t, ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: next})

	plan, err := def.Handler.Plan(ctx, h.env(""), raw)
	if err != nil {
		t.Fatal(err)
	}
	v1r10NoSecret(t, "plan", plan)
	if _, err := def.Handler.Apply(ctx, h.env("c1"), raw); err != nil {
		t.Fatal(err)
	}
	effects := puEffects(t, h)
	if len(effects) != 1 {
		t.Fatalf("journal %+v, want one effect", effects)
	}
	e := effects[0]
	v1r10NoSecret(t, "the effect", e)
	if got := puStateBody(t, e.Before.State); !reflect.DeepEqual(got, v1r10Doc(puBodyOf(host))) {
		t.Errorf("before %+v, want the body the host had with every advanced configuration fingerprinted: %+v", got, v1r10Doc(puBodyOf(host)))
	}
	if got := v1r10Sealed(t, h, e.Seq); !reflect.DeepEqual(got, puBodyOf(host)) {
		t.Errorf("sealed body %+v, want the body the host had: %+v", got, puBodyOf(host))
	}

	e.UndoTo = &core.StateDoc{State: e.Before.State}
	if _, err := def.Handler.Inverse(ctx, h.env("c1"), e); err != nil {
		t.Fatal(err)
	}
	if hosts := puHosts(t, h); len(hosts) != 1 || !reflect.DeepEqual(puBodyOf(hosts[0]), puBodyOf(host)) {
		t.Errorf("NPM holds %+v after the undo, want the original body with its advanced configuration: %+v", hosts, puBodyOf(host))
	}
	obs, err := def.Handler.Observe(ctx, h.env("c1"), e)
	if err != nil {
		t.Fatal(err)
	}
	v1r10NoSecret(t, "the observation", obs)
	if !puSameJSON(obs.Current.State, e.UndoTo.State) {
		t.Errorf("observed %s after the undo, want the before document %s", obs.Current.State, e.UndoTo.State)
	}
}

func TestV1r10UpdateKeepsTheOperatorsAdvancedConfig(t *testing.T) {
	ctx := context.Background()
	h, host := v1r10Seed(t)
	// The operator replaced the module's "/" configuration in NPM with rules of its own.
	rootRules := "allow 10.0.0.0/8;\ndeny all;\nproxy_set_header X-Root-Key " + v1r10LocSecret + ";"
	api, err := h.nm().d.api(ctx, h.env(""))
	if err != nil {
		t.Fatal(err)
	}
	edited := puBodyOf(host)
	edited.Locations = append([]npmapi.Location(nil), host.Locations...)
	edited.Locations[0].AdvancedConfig = rootRules
	if host, err = api.UpdateProxyHost(ctx, host.ID, edited); err != nil {
		t.Fatal(err)
	}
	next := puInput(func(in *ProxyHostInput) { in.ForwardPort = 9090 })
	def := h.nm().proxyhostUpdate()
	raw := puJSON(t, ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: next})
	plan, err := def.Handler.Plan(ctx, h.env(""), raw)
	if err != nil {
		t.Fatal(err)
	}
	v1r10NoSecret(t, "plan", plan)
	kept := core.DiffLine{Op: " ", Text: "location /: keeps the advanced configuration written in NPM"}
	if !slices.Contains(plan.Diff, kept) {
		t.Errorf("plan diff %+v, want the line %+v", plan.Diff, kept)
	}
	want := proxyHostBody(next, "")
	want.AdvancedConfig = host.AdvancedConfig
	want.Locations[0].AdvancedConfig += "\n" + rootRules
	want.Locations = append(want.Locations, host.Locations[1])
	// A second update of the same input must not repeat the module's own line.
	for _, id := range []string{"c1", "c2"} {
		h.addChange(id, "npm.proxyhost.update")
		if _, err := def.Handler.Apply(ctx, h.env(id), raw); err != nil {
			t.Fatal(err)
		}
		hosts := puHosts(t, h)
		if len(hosts) != 1 || !reflect.DeepEqual(puBodyOf(hosts[0]), want) {
			t.Errorf("after change %s NPM holds %+v, want the module's / location for the new port followed by the operator's / rules, and the operator's advanced configuration and /api location kept: %+v", id, hosts, want)
		}
	}
}

func TestV1r10RemoveEffectsHoldFingerprintsAndTheSealedBody(t *testing.T) {
	ctx := context.Background()
	h, host := v1r10Seed(t)
	h.addChange("c1", "npm.proxyhost.remove")
	def := h.nm().proxyhostRemove()
	raw := puJSON(t, ProxyHostRemoveInput{ID: host.ID})

	plan, err := def.Handler.Plan(ctx, h.env(""), raw)
	if err != nil {
		t.Fatal(err)
	}
	v1r10NoSecret(t, "plan", plan)
	if _, err := def.Handler.Apply(ctx, h.env("c1"), raw); err != nil {
		t.Fatal(err)
	}
	e := puEffects(t, h)[0]
	v1r10NoSecret(t, "the effect", e)
	if got := puStateBody(t, e.Before.State); !reflect.DeepEqual(got, v1r10Doc(puBodyOf(host))) {
		t.Errorf("before %+v, want the body the host had with every advanced configuration fingerprinted: %+v", got, v1r10Doc(puBodyOf(host)))
	}
	if got := v1r10Sealed(t, h, e.Seq); !reflect.DeepEqual(got, puBodyOf(host)) {
		t.Errorf("sealed body %+v, want the body the host had: %+v", got, puBodyOf(host))
	}

	e.UndoTo = &core.StateDoc{State: e.Before.State}
	res, err := def.Handler.Inverse(ctx, h.env("c1"), e)
	if err != nil {
		t.Fatal(err)
	}
	hosts := puHosts(t, h)
	if len(hosts) != 1 || !reflect.DeepEqual(puBodyOf(hosts[0]), puBodyOf(host)) || res.Resource != "npm.proxyhost:"+strconv.Itoa(hosts[0].ID) {
		t.Fatalf("inverse = %+v; NPM holds %+v, want one host with the removed body, advanced configuration included", res, hosts)
	}
	e.State, e.UndoResource = core.EffectUndoing, res.Resource
	obs, err := def.Handler.Observe(ctx, h.env("c1"), e)
	if err != nil {
		t.Fatal(err)
	}
	v1r10NoSecret(t, "the observation", obs)
	if !puSameJSON(obs.Current.State, e.UndoTo.State) {
		t.Errorf("observed %s after the undo, want the before document %s", obs.Current.State, e.UndoTo.State)
	}
}

func TestV1r10InverseNeverRestoresFromTheDocument(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		kind core.EffectKind
		def  func(nm) core.ActionDef
	}{
		{"update", core.EffectModify, func(n nm) core.ActionDef { return n.proxyhostUpdate() }},
		{"remove", core.EffectDelete, func(n nm) core.ActionDef { return n.proxyhostRemove() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, host := v1r10Seed(t)
			h.env("c1")
			body := puBodyOf(host)
			body.ForwardPort = 7777
			doc := core.StateDoc{State: puJSON(t, body)}
			e := core.Effect{
				ChangeID: "c1", Seq: 1, Kind: tc.kind, Resource: "npm.proxyhost:" + strconv.Itoa(host.ID), State: core.EffectMade,
				Before: doc, UndoTo: &doc,
			}
			before := puHosts(t, h)

			if _, err := tc.def(h.nm()).Handler.Inverse(ctx, h.env("c1"), e); err == nil || !core.IsDefinite(err) || !strings.Contains(err.Error(), "read the sealed proxy host") {
				t.Fatalf("inverse of an effect whose secret cannot be read = %v, want a definite refusal", err)
			}
			if _, err := h.journal.Intend(ctx, e, nil); err != nil {
				t.Fatal(err)
			}
			_, err := tc.def(h.nm()).Handler.Inverse(ctx, h.env("c1"), e)
			if err == nil || !core.IsDefinite(err) || !strings.Contains(err.Error(), "holds no sealed proxy host") {
				t.Fatalf("inverse without a sealed body = %v, want a definite refusal for the missing sealed body", err)
			}
			if got := puHosts(t, h); !reflect.DeepEqual(got, before) {
				t.Errorf("a refused inverse changed NPM to %+v", got)
			}
		})
	}
}

func TestV1r10AddObservationHoldsFingerprints(t *testing.T) {
	ctx := context.Background()
	h, n := paHost(t)
	view, err := paApply(t, h, n, "c1", "", paInput("a.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	api, err := n.d.api(ctx, h.env(""))
	if err != nil {
		t.Fatal(err)
	}
	body := proxyHostBody(paInput("a.example.com"), "")
	body.AdvancedConfig = `proxy_set_header Authorization "Bearer ` + v1r10HostSecret + `";`
	if _, err := api.UpdateProxyHost(ctx, view.ID, body); err != nil {
		t.Fatal(err)
	}
	e := h.journal.effects()[0]
	obs, err := n.proxyhostAdd().Handler.Observe(ctx, h.env("c1"), e)
	if err != nil {
		t.Fatal(err)
	}
	v1r10NoSecret(t, "the observation", obs)
	var doc struct {
		AdvancedConfig string `json:"advancedConfig"`
	}
	if err := json.Unmarshal(obs.Current.State, &doc); err != nil || doc.AdvancedConfig != v1r10Fingerprint(body.AdvancedConfig) {
		t.Errorf("observed advanced configuration %q (%v), want the fingerprint of what NPM holds", doc.AdvancedConfig, err)
	}
}
