package cloudflare

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const (
	naTunnel   = "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	naClaimant = "expose:app.example.com"
	naReason   = "connect the container to a user-defined network"
)

// newNA is an agent host with a running connector for naTunnel on the default bridge only, the networks web, other,
// bridge (the default one) and lan (a macvlan), and the container app on web.
func newNA(t *testing.T) *agHost {
	t.Helper()
	h := newAGHost(t)
	for _, n := range []dockerapi.Network{agNetwork("web", "bridge"), agNetwork("other", "bridge"), agNetwork("bridge", "bridge"), agNetwork("lan", "macvlan")} {
		h.docker.putNetwork(n)
	}
	conn := agContainer(connectorName(naTunnel), func(c *dockerapi.ContainerJSON) { c.State.Status, c.State.Running = "running", true })
	h.docker.putContainer(conn)
	h.docker.putContainer(agContainer("app", agOnNetwork("web", "172.20.0.5")))
	ctx := context.Background()
	env := h.env("")
	sealed, err := env.Sealer.Seal(connectorSealAD(naTunnel), []byte(agToken(naTunnel)))
	if err != nil {
		t.Fatal(err)
	}
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		return insertConnector(ctx, tx, connectorRow{TunnelID: naTunnel, ContainerID: conn.ID, TokenCT: sealed, ChangeID: "c0", CreatedAt: env.Clock.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// naOnNetwork puts the connector container on the network with a fixed address.
func naOnNetwork(t *testing.T, h *agHost, network, ip string) {
	t.Helper()
	c, err := h.docker.InspectContainer(context.Background(), connectorName(naTunnel))
	if err != nil {
		t.Fatal(err)
	}
	agOnNetwork(network, ip)(&c)
	h.docker.putContainer(c)
}

func naInput(mutate ...func(*NetworkAttachInput)) json.RawMessage {
	in := NetworkAttachInput{Tunnel: naTunnel, Network: "web", Claimant: naClaimant, Target: "app"}
	for _, m := range mutate {
		if m != nil {
			m(&in)
		}
	}
	raw, err := json.Marshal(in)
	if err != nil {
		panic(err)
	}
	return raw
}

func naApply(t *testing.T, h *agHost, changeID string, mutate ...func(*NetworkAttachInput)) (NetworkAttachment, error) {
	t.Helper()
	raw, err := h.cf().networkAttach().Handler.Apply(context.Background(), h.env(changeID), naInput(mutate...))
	if err != nil {
		return NetworkAttachment{}, err
	}
	var out NetworkAttachment
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("output %s: %v", raw, err)
	}
	return out, nil
}

func naClaimant2(in *NetworkAttachInput) { in.Claimant = "expose:b.example.com" }

func naClaims(t *testing.T, h *agHost) []claimRow {
	t.Helper()
	rows, err := connectorClaims(context.Background(), h.env("").DB.Read(), naTunnel, "")
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func naOnWeb(t *testing.T, h *agHost) bool {
	t.Helper()
	c, err := h.docker.InspectContainer(context.Background(), connectorName(naTunnel))
	if err != nil {
		t.Fatal(err)
	}
	return c.NetworkSettings.Networks["web"] != nil
}

var naMutations = []string{"ConnectNetwork", "DisconnectNetwork", "CreateContainer", "StartContainer", "StopContainer", "RemoveContainer", "PullImage"}

func naMutated(h *agHost, mark int) []string {
	var got []string
	for _, c := range h.docker.calls()[mark:] {
		if slices.Contains(naMutations, c) {
			got = append(got, c)
		}
	}
	return got
}

// naEffects are the journaled effects of one change.
func naEffects(t *testing.T, h *agHost, changeID string) []core.Effect {
	t.Helper()
	all, err := h.env(changeID).Effects.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var effs []core.Effect
	for _, e := range all {
		if e.ChangeID == changeID {
			effs = append(effs, e)
		}
	}
	return effs
}

func naRefused(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	var coded *errcode.Error
	if err == nil || !core.IsDefinite(err) || !errors.As(err, &coded) || coded.Code != code {
		t.Fatalf("error %v, want a Definite %s", err, code)
	}
	return coded
}

// naUndo undoes the made effects of the change as the engine does: in reverse order, each checked against its
// journaled post-state before and against its undo target after.
func naUndo(t *testing.T, h *agHost, changeID string) {
	t.Helper()
	ctx := context.Background()
	def := h.cf().networkAttach()
	env := h.env(changeID)
	effs := naEffects(t, h, changeID)
	for i := len(effs) - 1; i >= 0; i-- {
		e := effs[i]
		if e.State != core.EffectMade {
			continue
		}
		obs, err := def.Handler.Observe(ctx, env, e)
		if err != nil {
			t.Fatalf("observe %s: %v", e.Resource, err)
		}
		if string(obs.Current.State) != string(e.Post.State) {
			t.Errorf("%s: observed %s before its undo, journaled %s", e.Resource, obs.Current.State, e.Post.State)
		}
		e.State = core.EffectUndoing
		if _, err := def.Handler.Inverse(ctx, env, e); err != nil {
			t.Fatalf("inverse of %s: %v", e.Resource, err)
		}
		obs, err = def.Handler.Observe(ctx, env, e)
		if err != nil {
			t.Fatalf("observe %s after its undo: %v", e.Resource, err)
		}
		want := "null"
		if e.Kind != core.EffectCreate {
			want = string(e.Before.State)
		}
		if string(obs.Current.State) != want {
			t.Errorf("%s: observed %s after its undo, want %s", e.Resource, obs.Current.State, want)
		}
	}
}

func TestNaSpec(t *testing.T) {
	def := newNA(t).cf().networkAttach()
	s := def.Spec
	if s.Name != "cloudflare.network.attach" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"cloudflare"}) || s.Summary == "" {
		t.Errorf("spec %+v", s)
	}
	h := def.Handler
	if h.Plan == nil || h.Apply == nil || h.Verify == nil || h.Observe == nil || h.Inverse == nil {
		t.Errorf("handler lacks a stage: plan %t apply %t verify %t observe %t inverse %t",
			h.Plan != nil, h.Apply != nil, h.Verify != nil, h.Observe != nil, h.Inverse != nil)
	}
	if def.InputType != reflect.TypeFor[NetworkAttachInput]() || def.OutputType != reflect.TypeFor[NetworkAttachment]() {
		t.Errorf("types %v and %v", def.InputType, def.OutputType)
	}
}

func TestNaClaimDocument(t *testing.T) {
	doc, err := claimDocument(claimRow{TunnelID: naTunnel, Network: "web", Claimant: naClaimant, ChangeID: "c1", Preexisting: true})
	want := `{"tunnel":"` + naTunnel + `","network":"web","claimant":"` + naClaimant + `","preexisting":true,"changeId":"c1"}`
	if err != nil || string(doc) != want {
		t.Errorf("claimDocument = %s, %v; want %s", doc, err, want)
	}
}

func TestNaRefuses(t *testing.T) {
	ctx := context.Background()
	removeConnectorRow := func(t *testing.T, h *agHost) {
		env := h.env("")
		if err := env.DB.Write(ctx, func(tx *sql.Tx) error { return deleteConnector(ctx, tx, naTunnel) }); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*NetworkAttachInput)
		seed   func(t *testing.T, h *agHost)
		code   errcode.Code
		fields []string // the paths of the field errors, sorted
		detail string   // the exact detail, "" for any
	}{
		{name: "a bad tunnel id", mutate: func(in *NetworkAttachInput) { in.Tunnel = "x" }, code: errcode.InvalidInput, fields: []string{"/tunnel"}},
		{name: "a bad network name", mutate: func(in *NetworkAttachInput) { in.Network = "bad name" }, code: errcode.InvalidInput, fields: []string{"/network"}},
		{name: "a bad claimant", mutate: func(in *NetworkAttachInput) { in.Claimant = "a b" }, code: errcode.InvalidInput, fields: []string{"/claimant"}},
		{name: "a bad target name", mutate: func(in *NetworkAttachInput) { in.Target = "bad name" }, code: errcode.InvalidInput, fields: []string{"/target"}},
		{name: "every bad field", mutate: func(in *NetworkAttachInput) {
			in.Tunnel, in.Network, in.Claimant, in.Target = "x", "bad name", "a b", "bad name"
		},
			code: errcode.InvalidInput, fields: []string{"/claimant", "/network", "/target", "/tunnel"}},
		{name: "no connector row", seed: removeConnectorRow, code: errcode.CloudflareConnectorMissing},
		{name: "no connector container", code: errcode.CloudflareConnectorMissing,
			seed: func(t *testing.T, h *agHost) {
				if err := h.docker.RemoveContainer(ctx, connectorName(naTunnel), dockerapi.RemoveContainerOptions{Force: true}); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "an unknown network", mutate: func(in *NetworkAttachInput) { in.Network = "nosuch" }, code: errcode.NotFound},
		{name: "the default bridge", mutate: func(in *NetworkAttachInput) { in.Network, in.Target = "bridge", "legacy" }, code: errcode.CloudflareUnsupportedTarget, detail: naReason,
			seed: func(_ *testing.T, h *agHost) {
				h.docker.putContainer(agContainer("legacy", agOnNetwork("bridge", "172.17.0.2")))
			}},
		{name: "a macvlan network without a target", mutate: func(in *NetworkAttachInput) { in.Network, in.Target = "lan", "" }, code: errcode.CloudflareUnsupportedTarget, detail: naReason},
		{name: "an unknown target", mutate: func(in *NetworkAttachInput) { in.Target = "ghost" }, code: errcode.NotFound},
		{name: "a target on another network only", code: errcode.CloudflareUnsupportedTarget, detail: naReason,
			mutate: func(in *NetworkAttachInput) { in.Target = "elsewhere" },
			seed: func(_ *testing.T, h *agHost) {
				h.docker.putContainer(agContainer("elsewhere", agOnNetwork("other", "172.21.0.3")))
			}},
		{name: "a target on the default bridge only", code: errcode.CloudflareUnsupportedTarget, detail: naReason,
			mutate: func(in *NetworkAttachInput) { in.Target = "legacy" },
			seed: func(_ *testing.T, h *agHost) {
				h.docker.putContainer(agContainer("legacy", agOnNetwork("bridge", "172.17.0.2")))
			}},
		{name: "another fixed address", code: errcode.InvalidInput, fields: []string{"/address"},
			mutate: func(in *NetworkAttachInput) { in.Address = netip.MustParseAddr("172.20.0.10") },
			seed:   func(t *testing.T, h *agHost) { naOnNetwork(t, h, "web", "172.20.0.9") }},
	} {
		for _, stage := range []string{"plan", "apply"} {
			t.Run(tc.name+" "+stage, func(t *testing.T) {
				h := newNA(t)
				if tc.seed != nil {
					tc.seed(t, h)
				}
				mark := len(h.docker.calls())
				def := h.cf().networkAttach()
				var err error
				if stage == "plan" {
					_, err = def.Handler.Plan(ctx, h.env(""), naInput(tc.mutate))
				} else {
					_, err = def.Handler.Apply(ctx, h.env("c1"), naInput(tc.mutate))
				}
				coded := naRefused(t, err, tc.code)
				var paths []string
				for _, f := range coded.Fields {
					paths = append(paths, f.Path)
				}
				slices.Sort(paths)
				if !slices.Equal(paths, tc.fields) {
					t.Errorf("field paths %v, want %v", paths, tc.fields)
				}
				if tc.detail != "" && coded.Detail != tc.detail {
					t.Errorf("detail %q, want %q", coded.Detail, tc.detail)
				}
				if got := naMutated(h, mark); len(got) != 0 || len(naClaims(t, h)) != 0 {
					t.Errorf("the refusal changed the host: calls %v, claims %+v", got, naClaims(t, h))
				}
			})
		}
	}
	t.Run("the Engine down", func(t *testing.T) {
		h := newNA(t)
		h.docker.Fail("InspectContainer", dockerapi.ErrUnavailable)
		_, err := h.cf().networkAttach().Handler.Plan(ctx, h.env(""), naInput())
		if err == nil || errcode.From(err).Code != errcode.DockerEngineUnavailable {
			t.Errorf("error %v, want docker.engine_unavailable", err)
		}
	})
}

func TestNaPlanShowsTheJoinAndTheClaim(t *testing.T) {
	ctx := context.Background()
	plan := func(h *agHost, mutate ...func(*NetworkAttachInput)) core.PlanOutcome {
		out, err := h.cf().networkAttach().Handler.Plan(ctx, h.env(""), naInput(mutate...))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	join := core.DiffLine{Op: "+", Text: "container " + connectorName(naTunnel) + " joins network web"}
	claim := core.DiffLine{Op: "+", Text: "claim of " + naClaimant + " on network web"}

	h := newNA(t)
	out := plan(h)
	if want := []core.DiffLine{join, claim}; !slices.Equal(out.Diff, want) {
		t.Errorf("diff %+v, want %+v", out.Diff, want)
	}
	if len(out.StateHash) != 64 || out.StateHash != plan(h).StateHash || out.Guards == nil {
		t.Errorf("hash %q (stable %t), guards %v: want a 64-digit stable hash and an empty guard list", out.StateHash, out.StateHash == plan(h).StateHash, out.Guards)
	}
	if got := plan(h, func(in *NetworkAttachInput) { in.Address = netip.MustParseAddr("172.20.0.10") }).Diff; !slices.Equal(got, []core.DiffLine{join, {Op: "+", Text: "ipv4 172.20.0.10"}, claim}) {
		t.Errorf("diff with an IPv4 address %+v", got)
	}
	if got := plan(h, func(in *NetworkAttachInput) { in.Address = netip.MustParseAddr("fd00::10") }).Diff; !slices.Equal(got, []core.DiffLine{join, {Op: "+", Text: "ipv6 fd00::10"}, claim}) {
		t.Errorf("diff with an IPv6 address %+v", got)
	}
	if got := naMutated(h, 0); len(got) != 0 {
		t.Errorf("the plan changed the Engine: %v", got)
	}

	t.Run("the connector is already on the network", func(t *testing.T) {
		h := newNA(t)
		naOnNetwork(t, h, "web", "172.20.0.9")
		note := core.DiffLine{Op: " ", Text: connectorName(naTunnel) + " is already on network web; detaching leaves it there"}
		if got, want := plan(h).Diff, []core.DiffLine{claim, note}; !slices.Equal(got, want) {
			t.Errorf("diff %+v, want %+v", got, want)
		}
	})
	t.Run("a changed network changes the hash", func(t *testing.T) {
		h := newNA(t)
		before := plan(h).StateHash
		if _, err := naApply(t, h, "c1"); err != nil {
			t.Fatal(err)
		}
		if after := plan(h, naClaimant2).StateHash; after == before {
			t.Error("the hash did not change after a claim and a join")
		}
	})
	t.Run("a claimant that already holds the claim", func(t *testing.T) {
		h := newNA(t)
		if _, err := naApply(t, h, "c1"); err != nil {
			t.Fatal(err)
		}
		if got := plan(h).Diff; len(got) != 0 {
			t.Errorf("diff %+v, want none", got)
		}
	})
}

func TestNaApplyJoinsAndClaims(t *testing.T) {
	ctx := context.Background()
	h := newNA(t)
	out, err := naApply(t, h, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if want := (NetworkAttachment{Tunnel: naTunnel, Network: "web", Claimant: naClaimant, Preexisting: false, Attached: true}); out != want {
		t.Errorf("output %+v, want %+v", out, want)
	}
	if !naOnWeb(t, h) {
		t.Error("the connector did not join web")
	}
	wantClaim := claimRow{TunnelID: naTunnel, Network: "web", Claimant: naClaimant, ChangeID: "c1", Preexisting: false}
	if got := naClaims(t, h); len(got) != 1 || got[0] != wantClaim {
		t.Errorf("claims %+v, want %+v", got, wantClaim)
	}
	if got := naMutated(h, 0); !slices.Equal(got, []string{"ConnectNetwork"}) {
		t.Errorf("Engine calls %v, want one ConnectNetwork", got)
	}

	effs := naEffects(t, h, "c1")
	if len(effs) != 2 {
		t.Fatalf("%d effects, want 2: %+v", len(effs), effs)
	}
	join, claim := effs[0], effs[1]
	if join.Kind != core.EffectModify || join.Resource != "cloudflare.endpoint:"+naTunnel+"|web" || join.State != core.EffectMade || join.Local ||
		join.CommitWait != 2*time.Minute || string(join.Before.State) != "null" || join.Recovery != nil || join.InverseRecovery != nil || join.Post == nil {
		t.Errorf("join effect %+v", join)
	}
	id := naTunnel + "|web|" + naClaimant
	if claim.Kind != core.EffectCreate || claim.Resource != "cloudflare.claim:"+id || claim.State != core.EffectMade || !claim.Local || !claim.InverseLocal ||
		claim.Recovery == nil || claim.Recovery.Kind != "name" || claim.Recovery.Value != id || string(claim.Before.State) != "null" ||
		claim.After == nil || claim.Post == nil || string(claim.After.State) != string(claim.Post.State) {
		t.Errorf("claim effect %+v", claim)
	}
	if want, _ := claimDocument(wantClaim); string(claim.Post.State) != string(want) {
		t.Errorf("claim post-state %s, want %s", claim.Post.State, want)
	}

	if err := h.cf().networkAttach().Handler.Verify(ctx, h.env("c1"), naInput(), naJSON(t, out)); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func naJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestNaApplyWithAFixedAddress(t *testing.T) {
	for _, tc := range []struct {
		addr string
		got  func(*dockerapi.EndpointIPAMConfig) netip.Addr
	}{
		{"172.20.0.10", func(c *dockerapi.EndpointIPAMConfig) netip.Addr { return c.IPv4Address }},
		{"fd00::10", func(c *dockerapi.EndpointIPAMConfig) netip.Addr { return c.IPv6Address }},
	} {
		t.Run(tc.addr, func(t *testing.T) {
			h := newNA(t)
			want := netip.MustParseAddr(tc.addr)
			if _, err := naApply(t, h, "c1", func(in *NetworkAttachInput) { in.Address = want }); err != nil {
				t.Fatal(err)
			}
			c, err := h.docker.InspectContainer(context.Background(), connectorName(naTunnel))
			if err != nil {
				t.Fatal(err)
			}
			ep := c.NetworkSettings.Networks["web"]
			if ep == nil || ep.IPAMConfig == nil || tc.got(ep.IPAMConfig) != want {
				t.Errorf("endpoint %+v, want the fixed address %s", ep, want)
			}
		})
	}
}

func TestNaApplyClaimsThatShareANetwork(t *testing.T) {
	t.Run("the connector was on the network before", func(t *testing.T) {
		h := newNA(t)
		naOnNetwork(t, h, "web", "172.20.0.9")
		out, err := naApply(t, h, "c1")
		if err != nil {
			t.Fatal(err)
		}
		if !out.Preexisting || !out.Attached {
			t.Errorf("output %+v, want preexisting and attached", out)
		}
		if got := naMutated(h, 0); len(got) != 0 {
			t.Errorf("Engine calls %v, want none", got)
		}
		if got := naClaims(t, h); len(got) != 1 || !got[0].Preexisting {
			t.Errorf("claims %+v, want one preexisting claim", got)
		}
		if effs := naEffects(t, h, "c1"); len(effs) != 1 || effs[0].Resource != "cloudflare.claim:"+naTunnel+"|web|"+naClaimant {
			t.Errorf("effects %+v, want the claim only", effs)
		}
	})
	for _, preexisting := range []bool{false, true} {
		t.Run("a second claimant inherits the mark", func(t *testing.T) {
			h := newNA(t)
			if preexisting {
				naOnNetwork(t, h, "web", "172.20.0.9")
			}
			if _, err := naApply(t, h, "c1"); err != nil {
				t.Fatal(err)
			}
			out, err := naApply(t, h, "c2", naClaimant2)
			if err != nil {
				t.Fatal(err)
			}
			if out.Preexisting != preexisting {
				t.Errorf("preexisting %t, want %t", out.Preexisting, preexisting)
			}
			if got := naClaims(t, h); len(got) != 2 || got[0].Preexisting != preexisting || got[1].Preexisting != preexisting {
				t.Errorf("claims %+v", got)
			}
			if effs := naEffects(t, h, "c2"); len(effs) != 1 {
				t.Errorf("second change journaled %d effects, want only its claim", len(effs))
			}
		})
	}
	t.Run("the same claimant again", func(t *testing.T) {
		h := newNA(t)
		if _, err := naApply(t, h, "c1"); err != nil {
			t.Fatal(err)
		}
		calls := len(h.docker.calls())
		out, err := naApply(t, h, "c2")
		if err != nil || !out.Attached {
			t.Fatalf("output %+v, %v", out, err)
		}
		if got := naClaims(t, h); len(got) != 1 || got[0].ChangeID != "c1" {
			t.Errorf("claims %+v, want the first change's claim only", got)
		}
		if effs := naEffects(t, h, "c2"); len(effs) != 0 {
			t.Errorf("effects %+v, want none", effs)
		}
		for _, c := range h.docker.calls()[calls:] {
			if slices.Contains(naMutations, c) {
				t.Errorf("the repeated attach called %s", c)
			}
		}
	})
}

func TestNaApplyFailures(t *testing.T) {
	t.Run("a refused connect is Definite and leaves no claim", func(t *testing.T) {
		h := newNA(t)
		h.docker.Fail("ConnectNetwork", &dockerapi.APIError{StatusCode: http.StatusForbidden, Message: "no"})
		_, err := naApply(t, h, "c1")
		if err == nil || !core.IsDefinite(err) {
			t.Errorf("error %v, want a Definite refusal", err)
		}
		effs := naEffects(t, h, "c1")
		if len(effs) != 1 || effs[0].State != core.EffectSkipped || len(naClaims(t, h)) != 0 {
			t.Errorf("effects %+v, claims %+v; want one skipped effect and no claim", effs, naClaims(t, h))
		}
	})
	t.Run("an unknown connect failure stays intended", func(t *testing.T) {
		h := newNA(t)
		h.docker.Fail("ConnectNetwork", errors.New("timeout"))
		_, err := naApply(t, h, "c1")
		effs := naEffects(t, h, "c1")
		if err == nil || core.IsDefinite(err) || len(effs) != 1 || effs[0].State != core.EffectIntended {
			t.Errorf("error %v, effects %+v; want a failure that is not Definite and one intended effect", err, effs)
		}
	})
}

func TestNaVerify(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		damage func(t *testing.T, h *agHost)
		fails  bool
	}{
		{"as attached", func(*testing.T, *agHost) {}, false},
		{"the connector left the network", func(t *testing.T, h *agHost) {
			if err := h.docker.DisconnectNetwork(ctx, "web", connectorName(naTunnel), false); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"the claim is gone", func(t *testing.T, h *agHost) {
			env := h.env("")
			err := env.DB.Write(ctx, func(tx *sql.Tx) error { return deleteClaim(ctx, tx, naTunnel, "web", naClaimant) })
			if err != nil {
				t.Fatal(err)
			}
		}, true},
		{"the network is gone", func(_ *testing.T, h *agHost) {
			h.docker.Fail("InspectNetwork", &dockerapi.APIError{StatusCode: http.StatusNotFound, Message: "no such network"})
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newNA(t)
			out, err := naApply(t, h, "c1")
			if err != nil {
				t.Fatal(err)
			}
			tc.damage(t, h)
			err = h.cf().networkAttach().Handler.Verify(ctx, h.env("c1"), naInput(), naJSON(t, out))
			if tc.fails != (err != nil) {
				t.Errorf("Verify error %v, want failure %t", err, tc.fails)
			}
		})
	}
}

func TestNaUndo(t *testing.T) {
	ctx := context.Background()
	t.Run("a join and a claim", func(t *testing.T) {
		h := newNA(t)
		if _, err := naApply(t, h, "c1"); err != nil {
			t.Fatal(err)
		}
		naUndo(t, h, "c1")
		if naOnWeb(t, h) || len(naClaims(t, h)) != 0 {
			t.Errorf("after the undo the connector is on web: %t, claims %+v", naOnWeb(t, h), naClaims(t, h))
		}
	})
	t.Run("a claim on a network the connector was already on", func(t *testing.T) {
		h := newNA(t)
		naOnNetwork(t, h, "web", "172.20.0.9")
		if _, err := naApply(t, h, "c1"); err != nil {
			t.Fatal(err)
		}
		naUndo(t, h, "c1")
		if !naOnWeb(t, h) || len(naClaims(t, h)) != 0 {
			t.Errorf("after the undo the connector is on web: %t, claims %+v; want it kept on web with no claim", naOnWeb(t, h), naClaims(t, h))
		}
	})
	t.Run("a claim of another change keeps the connector on the network", func(t *testing.T) {
		h := newNA(t)
		if _, err := naApply(t, h, "c1"); err != nil {
			t.Fatal(err)
		}
		if _, err := naApply(t, h, "c2", naClaimant2); err != nil {
			t.Fatal(err)
		}
		def := h.cf().networkAttach()
		env := h.env("c1")
		effs := naEffects(t, h, "c1")
		claim, join := effs[1], effs[0]
		claim.State = core.EffectUndoing
		if _, err := def.Handler.Inverse(ctx, env, claim); err != nil {
			t.Fatalf("inverse of the claim: %v", err)
		}
		join.State = core.EffectUndoing
		_, err := def.Handler.Inverse(ctx, env, join)
		_ = naRefused(t, err, errcode.InvalidInput)
		if !naOnWeb(t, h) {
			t.Error("the connector left a network another change still claims")
		}
		if got := naClaims(t, h); len(got) != 1 || got[0].ChangeID != "c2" {
			t.Errorf("claims %+v, want only the second change's", got)
		}
	})
	t.Run("a claim of another change is not dropped", func(t *testing.T) {
		h := newNA(t)
		if _, err := naApply(t, h, "c1"); err != nil {
			t.Fatal(err)
		}
		claim := naEffects(t, h, "c1")[1]
		claim.ChangeID, claim.State = "c9", core.EffectUndoing
		_, err := h.cf().networkAttach().Handler.Inverse(ctx, h.env("c1"), claim)
		_ = naRefused(t, err, errcode.InvalidInput)
		if len(naClaims(t, h)) != 1 {
			t.Error("the claim of another change was dropped")
		}
	})
	t.Run("an undo run again", func(t *testing.T) {
		h := newNA(t)
		if _, err := naApply(t, h, "c1"); err != nil {
			t.Fatal(err)
		}
		naUndo(t, h, "c1")
		effs := naEffects(t, h, "c1")
		for i := len(effs) - 1; i >= 0; i-- {
			e := effs[i]
			e.State = core.EffectUndoing
			if _, err := h.cf().networkAttach().Handler.Inverse(ctx, h.env("c1"), e); err != nil {
				t.Errorf("inverse of %s again: %v", e.Resource, err)
			}
		}
	})
}

// TestNaInverseOfWhatADetachJournals: the detach action of a later task embeds networkClaims, so its undo of a
// removed claim and of a disconnect is pinned here with effects built the way that action journals them.
func TestNaInverseOfWhatADetachJournals(t *testing.T) {
	ctx := context.Background()
	claimID := naTunnel + "|web|" + naClaimant
	row := claimRow{TunnelID: naTunnel, Network: "web", Claimant: naClaimant, ChangeID: "c1", Preexisting: true}

	t.Run("a removed claim comes back", func(t *testing.T) {
		h := newNA(t)
		doc, err := claimDocument(row)
		if err != nil {
			t.Fatal(err)
		}
		e := core.Effect{ChangeID: "c9", Kind: core.EffectDelete, Resource: "cloudflare.claim:" + claimID, State: core.EffectUndoing, Before: core.StateDoc{State: doc}}
		def := h.cf().networkAttach()
		if _, err := def.Handler.Inverse(ctx, h.env("c9"), e); err != nil {
			t.Fatal(err)
		}
		if got := naClaims(t, h); len(got) != 1 || got[0] != row {
			t.Errorf("claims %+v, want %+v", got, row)
		}
		if _, err := def.Handler.Inverse(ctx, h.env("c9"), e); err != nil {
			t.Errorf("restoring the same claim again: %v", err)
		}
		other := row
		other.ChangeID = "c2"
		otherDoc, _ := claimDocument(other)
		e.Before = core.StateDoc{State: otherDoc}
		_, err = def.Handler.Inverse(ctx, h.env("c9"), e)
		_ = naRefused(t, err, errcode.InvalidInput)
		e.Before = core.StateDoc{State: json.RawMessage(`{"tunnel":"x"}`)}
		_, err = def.Handler.Inverse(ctx, h.env("c9"), e)
		_ = naRefused(t, err, errcode.InvalidInput)
	})

	t.Run("a left network is joined again", func(t *testing.T) {
		h := newNA(t)
		e := core.Effect{ChangeID: "c9", Kind: core.EffectModify, Resource: "cloudflare.endpoint:" + naTunnel + "|web", State: core.EffectUndoing,
			InverseRecovery: &core.RecoveryID{Kind: "name", Value: connectorName(naTunnel) + "@web"}}
		res, err := h.cf().networkAttach().Handler.Inverse(ctx, h.env("c9"), e)
		if err != nil {
			t.Fatal(err)
		}
		if res.Resource != e.Resource || !naOnWeb(t, h) {
			t.Errorf("result %+v, on web %t; want the endpoint resource and a joined network", res, naOnWeb(t, h))
		}
		_, err = h.cf().networkAttach().Handler.Inverse(ctx, h.env("c9"), e)
		if err == nil || !core.IsDefinite(err) {
			t.Errorf("joining a network the connector is on: %v, want a Definite refusal", err)
		}
	})

	t.Run("an effect of another module is refused", func(t *testing.T) {
		h := newNA(t)
		_, err := h.cf().networkAttach().Handler.Inverse(ctx, h.env("c9"), core.Effect{Kind: core.EffectCreate, Resource: "cloudflare.volume:x"})
		if err == nil {
			t.Error("an unknown resource was undone")
		}
	})
}

func TestNaObserveCreations(t *testing.T) {
	ctx := context.Background()
	h := newNA(t)
	def := h.cf().networkAttach()
	id := naTunnel + "|web|" + naClaimant
	claim := core.Effect{Kind: core.EffectCreate, Resource: "cloudflare.claim:?" + id}
	obs, err := def.Handler.Observe(ctx, h.env("c1"), claim)
	if err != nil || obs.Matches != 0 || string(obs.Current.State) != "null" {
		t.Errorf("before the attach: matches %d state %s (%v), want none and null", obs.Matches, obs.Current.State, err)
	}
	if _, err := naApply(t, h, "c1"); err != nil {
		t.Fatal(err)
	}
	obs, err = def.Handler.Observe(ctx, h.env("c1"), claim)
	want, _ := claimDocument(claimRow{TunnelID: naTunnel, Network: "web", Claimant: naClaimant, ChangeID: "c1"})
	if err != nil || obs.Matches != 1 || obs.Resource != "cloudflare.claim:"+id || string(obs.Current.State) != string(want) || !strings.Contains(string(obs.Current.State), `"changeId":"c1"`) {
		t.Errorf("after the attach: matches %d resource %q state %s (%v)", obs.Matches, obs.Resource, obs.Current.State, err)
	}
}
