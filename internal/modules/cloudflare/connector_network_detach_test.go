package cloudflare

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const (
	ndTunnel      = "5e6f7a8b-9c0d-4e1f-8a2b-3c4d5e6f7a8b"
	ndOtherTunnel = "6f7a8b9c-0d1e-4f2a-9b3c-4d5e6f7a8b9c"
	ndClaimant    = "expose:app.example.com"
	ndClaimant2   = "expose:b.example.com"
)

// ndModule registers the actions, so the registry's own checks run on them.
type ndModule struct{ defs []core.ActionDef }

func (m ndModule) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, Actions: m.defs}
}

// newND is an agent host with a running connector for ndTunnel that is on no user-defined network, and the
// user-defined bridge networks web and web2.
func newND(t *testing.T) *agHost {
	t.Helper()
	h := newAGHost(t)
	h.docker.putNetwork(agNetwork("web", "bridge"))
	h.docker.putNetwork(agNetwork("web2", "bridge"))
	conn := agContainer(connectorName(ndTunnel), func(c *dockerapi.ContainerJSON) { c.State.Status, c.State.Running = "running", true })
	h.docker.putContainer(conn)
	ctx := context.Background()
	env := h.env("")
	sealed, err := env.Sealer.Seal(connectorSealAD(ndTunnel), []byte(agToken(ndTunnel)))
	if err != nil {
		t.Fatal(err)
	}
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		return insertConnector(ctx, tx, connectorRow{TunnelID: ndTunnel, ContainerID: conn.ID, TokenCT: sealed, ChangeID: "c0", CreatedAt: env.Clock.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// ndJoin puts the connector on the network before any claim exists.
func ndJoin(t *testing.T, h *agHost, network, ip string) {
	t.Helper()
	c, err := h.docker.InspectContainer(context.Background(), connectorName(ndTunnel))
	if err != nil {
		t.Fatal(err)
	}
	agOnNetwork(network, ip)(&c)
	h.docker.putContainer(c)
}

func ndJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// ndAttach applies cloudflare.network.attach for the claimant, with no target, as change.
func ndAttach(t *testing.T, h *agHost, change, network, claimant string) {
	t.Helper()
	raw := ndJSON(t, NetworkAttachInput{Tunnel: ndTunnel, Network: network, Claimant: claimant})
	if _, err := h.cf().networkAttach().Handler.Apply(context.Background(), h.env(change), raw); err != nil {
		t.Fatalf("attach %s on %s: %v", claimant, network, err)
	}
}

// ndAddClaim inserts a claim row without touching the Engine.
func ndAddClaim(t *testing.T, h *agHost, c claimRow) {
	t.Helper()
	ctx := context.Background()
	if err := h.env("").DB.Write(ctx, func(tx *sql.Tx) error { return insertClaim(ctx, tx, c) }); err != nil {
		t.Fatal(err)
	}
}

func ndClaims(t *testing.T, h *agHost, tunnel string) []claimRow {
	t.Helper()
	rows, err := connectorClaims(context.Background(), h.env("").DB.Read(), tunnel, "")
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func ndInput(mutate ...func(*NetworkDetachInput)) json.RawMessage {
	in := NetworkDetachInput{Tunnel: ndTunnel, Network: "web", Claimant: ndClaimant}
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

func ndAllNetworks(in *NetworkDetachInput) { in.Network = "" }

func ndApply(t *testing.T, h *agHost, change string, mutate ...func(*NetworkDetachInput)) (NetworkAttachment, error) {
	t.Helper()
	raw, err := h.cf().networkDetach().Handler.Apply(context.Background(), h.env(change), ndInput(mutate...))
	if err != nil {
		return NetworkAttachment{}, err
	}
	var out NetworkAttachment
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("output %s: %v", raw, err)
	}
	return out, nil
}

func ndOn(t *testing.T, h *agHost, network string) bool {
	t.Helper()
	c, err := h.docker.InspectContainer(context.Background(), connectorName(ndTunnel))
	if err != nil {
		t.Fatal(err)
	}
	return c.NetworkSettings.Networks[network] != nil
}

var ndMutations = []string{"ConnectNetwork", "DisconnectNetwork", "CreateContainer", "StartContainer", "StopContainer", "RemoveContainer", "PullImage"}

// ndMutatedAfter lists the Engine calls that change something, made after the first mark calls.
func ndMutatedAfter(h *agHost, mark int) []string {
	var got []string
	for _, c := range h.docker.calls()[mark:] {
		if slices.Contains(ndMutations, c) {
			got = append(got, c)
		}
	}
	return got
}

// ndEffects are the journaled effects of one change, in journal order.
func ndEffects(t *testing.T, h *agHost, change string) []core.Effect {
	t.Helper()
	all, err := h.env(change).Effects.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var effs []core.Effect
	for _, e := range all {
		if e.ChangeID == change {
			effs = append(effs, e)
		}
	}
	return effs
}

func ndRefused(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	var coded *errcode.Error
	if err == nil || !core.IsDefinite(err) || !errors.As(err, &coded) || coded.Code != code {
		t.Fatalf("error %v, want a Definite %s", err, code)
	}
	return coded
}

// ndObserved is the state the detach handler observes for the effect now.
func ndObserved(t *testing.T, h *agHost, change string, e core.Effect) string {
	t.Helper()
	obs, err := h.cf().networkDetach().Handler.Observe(context.Background(), h.env(change), e)
	if err != nil {
		t.Fatalf("observe %s: %v", e.Resource, err)
	}
	return string(obs.Current.State)
}

// ndUndo undoes the made effects of the change in reverse order, as the engine does, and returns each result by
// resource.
func ndUndo(t *testing.T, h *agHost, change string) map[string]core.InverseResult {
	t.Helper()
	def := h.cf().networkDetach()
	env := h.env(change)
	effs := ndEffects(t, h, change)
	results := map[string]core.InverseResult{}
	for i := len(effs) - 1; i >= 0; i-- {
		e := effs[i]
		if e.State != core.EffectMade {
			continue
		}
		e.State = core.EffectUndoing
		res, err := def.Handler.Inverse(context.Background(), env, e)
		if err != nil {
			t.Fatalf("inverse of %s: %v", e.Resource, err)
		}
		results[e.Resource] = res
	}
	return results
}

func TestNdSpec(t *testing.T) {
	h := newND(t)
	def := h.cf().networkDetach()
	s := def.Spec
	if s.Name != "cloudflare.network.detach" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"cloudflare"}) || !s.Converges || s.Summary == "" {
		t.Errorf("spec %+v", s)
	}
	handler := def.Handler
	if handler.Plan == nil || handler.Apply == nil || handler.Verify == nil || handler.Observe == nil || handler.Inverse == nil {
		t.Errorf("handler lacks a stage: plan %t apply %t verify %t observe %t inverse %t",
			handler.Plan != nil, handler.Apply != nil, handler.Verify != nil, handler.Observe != nil, handler.Inverse != nil)
	}
	if def.InputType != reflect.TypeFor[NetworkDetachInput]() || def.OutputType != reflect.TypeFor[NetworkAttachment]() {
		t.Errorf("types %v and %v", def.InputType, def.OutputType)
	}
	if _, err := core.NewRegistry(ndModule{defs: []core.ActionDef{h.cf().networkAttach(), def}}); err != nil {
		t.Errorf("the registry refuses the actions: %v", err)
	}
	v := schema.NewValidator()
	for _, tc := range []struct{ input, field string }{
		{`{"tunnel":"` + ndTunnel + `","network":"","claimant":"c"}`, ""},
		{`{"tunnel":"` + ndTunnel + `","network":"web","claimant":"c","x":1}`, "/x"},
	} {
		err := v.ValidateInput(def, json.RawMessage(tc.input))
		var coded *errcode.Error
		switch {
		case tc.field == "" && err != nil:
			t.Errorf("input %s: %v, want it accepted", tc.input, err)
		case tc.field != "" && (!errors.As(err, &coded) || !slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == tc.field })):
			t.Errorf("input %s: %v, want a refusal naming %s", tc.input, err, tc.field)
		}
	}
}

func TestNdRefuses(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		mutate func(*NetworkDetachInput)
		fields []string // the paths of the field errors, sorted
	}{
		{"a bad tunnel id", func(in *NetworkDetachInput) { in.Tunnel = "x" }, []string{"/tunnel"}},
		{"a bad network name", func(in *NetworkDetachInput) { in.Network = "bad name" }, []string{"/network"}},
		{"an empty claimant", func(in *NetworkDetachInput) { in.Claimant = "" }, []string{"/claimant"}},
		{"a claimant with white space", func(in *NetworkDetachInput) { in.Claimant = "a b" }, []string{"/claimant"}},
		{"every bad field", func(in *NetworkDetachInput) { in.Tunnel, in.Network, in.Claimant = "x", "bad name", "a b" },
			[]string{"/claimant", "/network", "/tunnel"}},
	} {
		for _, stage := range []string{"plan", "apply"} {
			t.Run(tc.name+" "+stage, func(t *testing.T) {
				h := newND(t)
				ndAddClaim(t, h, claimRow{TunnelID: ndTunnel, Network: "web", Claimant: ndClaimant, ChangeID: "c0"})
				def := h.cf().networkDetach()
				var err error
				if stage == "plan" {
					_, err = def.Handler.Plan(ctx, h.env(""), ndInput(tc.mutate))
				} else {
					_, err = def.Handler.Apply(ctx, h.env("c1"), ndInput(tc.mutate))
				}
				coded := ndRefused(t, err, errcode.InvalidInput)
				var paths []string
				for _, f := range coded.Fields {
					paths = append(paths, f.Path)
				}
				slices.Sort(paths)
				if !slices.Equal(paths, tc.fields) {
					t.Errorf("field paths %v, want %v", paths, tc.fields)
				}
				if got := h.docker.calls(); len(got) != 0 {
					t.Errorf("a refused input reached the Engine: %v", got)
				}
				if got := ndClaims(t, h, ndTunnel); len(got) != 1 {
					t.Errorf("claims %+v, want the claim untouched", got)
				}
			})
		}
	}
}

func TestNdPlan(t *testing.T) {
	ctx := context.Background()
	name := connectorName(ndTunnel)
	del := func(claimant, network string) core.DiffLine {
		return core.DiffLine{Op: "-", Text: "claim of " + claimant + " on network " + network}
	}
	leave := func(network string) core.DiffLine {
		return core.DiffLine{Op: "-", Text: "container " + name + " leaves network " + network}
	}
	for _, tc := range []struct {
		name   string
		seed   func(t *testing.T, h *agHost)
		mutate func(*NetworkDetachInput)
		diff   []core.DiffLine
	}{
		{name: "the last claim takes the connector off the network",
			seed: func(t *testing.T, h *agHost) { ndAttach(t, h, "c1", "web", ndClaimant) },
			diff: []core.DiffLine{del(ndClaimant, "web"), leave("web")}},
		{name: "another claim keeps the connector there",
			seed: func(t *testing.T, h *agHost) {
				ndAttach(t, h, "c1", "web", ndClaimant)
				ndAttach(t, h, "c2", "web", ndClaimant2)
			},
			diff: []core.DiffLine{del(ndClaimant, "web"), {Op: " ", Text: name + " stays on network web: claimed by " + ndClaimant2}}},
		{name: "an attachment that was there before",
			seed: func(t *testing.T, h *agHost) {
				ndJoin(t, h, "web", "172.20.0.9")
				ndAttach(t, h, "c1", "web", ndClaimant)
			},
			diff: []core.DiffLine{del(ndClaimant, "web"), {Op: " ", Text: name + " stays on network web: it was there before"}}},
		{name: "every network of the claimant",
			seed: func(t *testing.T, h *agHost) {
				ndAttach(t, h, "c1", "web", ndClaimant)
				ndAttach(t, h, "c2", "web2", ndClaimant)
			},
			mutate: ndAllNetworks,
			diff:   []core.DiffLine{del(ndClaimant, "web"), leave("web"), del(ndClaimant, "web2"), leave("web2")}},
		{name: "no claim of the claimant",
			seed: func(t *testing.T, h *agHost) { ndAttach(t, h, "c1", "web", ndClaimant2) }},
		{name: "no claim on any network", mutate: ndAllNetworks},
		{name: "a claim of another tunnel",
			seed: func(t *testing.T, h *agHost) {
				ndAddClaim(t, h, claimRow{TunnelID: ndOtherTunnel, Network: "web", Claimant: ndClaimant, ChangeID: "c0"})
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newND(t)
			if tc.seed != nil {
				tc.seed(t, h)
			}
			mark := len(h.docker.calls())
			out, err := h.cf().networkDetach().Handler.Plan(ctx, h.env(""), ndInput(tc.mutate))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(out.Diff, tc.diff) {
				t.Errorf("diff %+v, want %+v", out.Diff, tc.diff)
			}
			if got := ndMutatedAfter(h, mark); len(got) != 0 {
				t.Errorf("the plan changed the Engine: %v", got)
			}
		})
	}

	t.Run("the state hash follows the claims", func(t *testing.T) {
		h := newND(t)
		ndAttach(t, h, "c1", "web", ndClaimant)
		plan := func() core.PlanOutcome {
			out, err := h.cf().networkDetach().Handler.Plan(ctx, h.env(""), ndInput())
			if err != nil {
				t.Fatal(err)
			}
			return out
		}
		first := plan()
		if len(first.StateHash) != 64 || first.StateHash != plan().StateHash || first.Guards == nil || len(first.Guards) != 0 {
			t.Errorf("hash %q (stable %t), guards %v: want a 64-digit stable hash and an empty guard list", first.StateHash, first.StateHash == plan().StateHash, first.Guards)
		}
		ndAttach(t, h, "c2", "web", ndClaimant2)
		if plan().StateHash == first.StateHash {
			t.Error("the hash did not change when another claimant joined the network")
		}
	})
	t.Run("the Engine down", func(t *testing.T) {
		h := newND(t)
		ndAddClaim(t, h, claimRow{TunnelID: ndTunnel, Network: "web", Claimant: ndClaimant, ChangeID: "c0"})
		h.docker.Fail("InspectContainer", dockerapi.ErrUnavailable)
		_, err := h.cf().networkDetach().Handler.Plan(ctx, h.env(""), ndInput())
		if err == nil || errcode.From(err).Code != errcode.DockerEngineUnavailable {
			t.Errorf("error %v, want docker.engine_unavailable", err)
		}
	})
	t.Run("no claim needs no Engine", func(t *testing.T) {
		h := newND(t)
		h.docker.Fail("InspectContainer", dockerapi.ErrUnavailable)
		out, err := h.cf().networkDetach().Handler.Plan(ctx, h.env(""), ndInput())
		if err != nil || len(out.Diff) != 0 || len(h.docker.calls()) != 0 {
			t.Errorf("diff %+v, error %v, Engine calls %v; want an empty diff and no call", out.Diff, err, h.docker.calls())
		}
	})
}

func TestNdApplyLeavesTheNetworkAndReleasesTheClaim(t *testing.T) {
	ctx := context.Background()
	h := newND(t)
	ndAttach(t, h, "c1", "web", ndClaimant)
	mark := len(h.docker.calls())
	out, err := ndApply(t, h, "c2")
	if err != nil {
		t.Fatal(err)
	}
	if want := (NetworkAttachment{Tunnel: ndTunnel, Network: "web", Claimant: ndClaimant}); out != want {
		t.Errorf("output %+v, want %+v", out, want)
	}
	if ndOn(t, h, "web") {
		t.Error("the connector is still on web")
	}
	if got := ndClaims(t, h, ndTunnel); len(got) != 0 {
		t.Errorf("claims %+v, want none", got)
	}
	if got := ndMutatedAfter(h, mark); !slices.Equal(got, []string{"DisconnectNetwork"}) {
		t.Errorf("Engine calls %v, want one DisconnectNetwork", got)
	}

	effs := ndEffects(t, h, "c2")
	if len(effs) != 2 {
		t.Fatalf("%d effects, want 2: %+v", len(effs), effs)
	}
	ep, claim := effs[0], effs[1]
	if ep.Kind != core.EffectModify || ep.Resource != "cloudflare.endpoint:"+ndTunnel+"|web" || ep.State != core.EffectMade || ep.Local ||
		ep.CommitWait != engineCommitWait || ep.Recovery != nil || ep.Post == nil || string(ep.Post.State) != "null" ||
		string(ep.Before.State) == "null" || ep.InverseRecovery == nil || ep.InverseRecovery.Kind != "name" ||
		ep.InverseRecovery.Value != connectorName(ndTunnel)+"@web" {
		t.Errorf("endpoint effect %+v, want a made non-local modify with the inverse recovery %s@web", ep, connectorName(ndTunnel))
	}
	wantBefore, err := claimDocument(claimRow{TunnelID: ndTunnel, Network: "web", Claimant: ndClaimant, ChangeID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if claim.Kind != core.EffectDelete || claim.Resource != "cloudflare.claim:"+ndTunnel+"|web|"+ndClaimant || claim.State != core.EffectMade ||
		!claim.Local || !claim.InverseLocal || string(claim.Before.State) != string(wantBefore) || claim.After == nil ||
		string(claim.After.State) != "null" || claim.Post == nil || string(claim.Post.State) != "null" {
		t.Errorf("claim effect %+v, want a made local delete of the claim with a null post-state", claim)
	}
	if err := h.cf().networkDetach().Handler.Verify(ctx, h.env("c2"), ndInput(), ndJSON(t, out)); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestNdApplyKeepsWhatIsStillNeeded(t *testing.T) {
	t.Run("another claim keeps the connector there", func(t *testing.T) {
		h := newND(t)
		ndAttach(t, h, "c1", "web", ndClaimant)
		ndAttach(t, h, "c2", "web", ndClaimant2)
		mark := len(h.docker.calls())
		out, err := ndApply(t, h, "c3")
		if err != nil {
			t.Fatal(err)
		}
		if !out.Attached || out.Preexisting {
			t.Errorf("output %+v, want the connector still attached", out)
		}
		if got := ndMutatedAfter(h, mark); len(got) != 0 || !ndOn(t, h, "web") {
			t.Errorf("Engine calls %v, on web %t; want the connector left on web", got, ndOn(t, h, "web"))
		}
		if got := ndClaims(t, h, ndTunnel); len(got) != 1 || got[0].Claimant != ndClaimant2 {
			t.Errorf("claims %+v, want only %s", got, ndClaimant2)
		}
		if effs := ndEffects(t, h, "c3"); len(effs) != 1 || effs[0].Kind != core.EffectDelete {
			t.Errorf("effects %+v, want only the claim deletion", effs)
		}
	})
	t.Run("an attachment that was there before stays", func(t *testing.T) {
		h := newND(t)
		ndJoin(t, h, "web", "172.20.0.9")
		ndAttach(t, h, "c1", "web", ndClaimant)
		mark := len(h.docker.calls())
		out, err := ndApply(t, h, "c2")
		if err != nil {
			t.Fatal(err)
		}
		if !out.Preexisting || !out.Attached {
			t.Errorf("output %+v, want a preexisting attachment left in place", out)
		}
		if got := ndMutatedAfter(h, mark); len(got) != 0 || !ndOn(t, h, "web") {
			t.Errorf("Engine calls %v, on web %t; want the connector left on web", got, ndOn(t, h, "web"))
		}
		if got := ndClaims(t, h, ndTunnel); len(got) != 0 {
			t.Errorf("claims %+v, want none", got)
		}
	})
	t.Run("a claim of another tunnel is not touched", func(t *testing.T) {
		h := newND(t)
		ndAttach(t, h, "c1", "web", ndClaimant)
		other := claimRow{TunnelID: ndOtherTunnel, Network: "web", Claimant: ndClaimant, ChangeID: "c0", Preexisting: true}
		ndAddClaim(t, h, other)
		out, err := ndApply(t, h, "c2")
		if err != nil {
			t.Fatal(err)
		}
		if ndOn(t, h, "web") || out.Attached {
			t.Errorf("the other tunnel's claim kept this connector on web: output %+v", out)
		}
		if got := ndClaims(t, h, ndOtherTunnel); len(got) != 1 || got[0] != other {
			t.Errorf("the other tunnel's claims %+v, want %+v", got, other)
		}
	})
}

func TestNdApplyEveryNetworkOfTheClaimant(t *testing.T) {
	h := newND(t)
	ndAttach(t, h, "c1", "web", ndClaimant)
	ndAttach(t, h, "c2", "web", ndClaimant2)
	ndAttach(t, h, "c3", "web2", ndClaimant)
	out, err := ndApply(t, h, "c4", ndAllNetworks)
	if err != nil {
		t.Fatal(err)
	}
	if want := (NetworkAttachment{Tunnel: ndTunnel, Network: "", Claimant: ndClaimant, Attached: true}); out != want {
		t.Errorf("output %+v, want %+v (still on web, which the other claimant needs)", out, want)
	}
	if !ndOn(t, h, "web") || ndOn(t, h, "web2") {
		t.Errorf("on web %t, on web2 %t; want web kept and web2 left", ndOn(t, h, "web"), ndOn(t, h, "web2"))
	}
	if got := ndClaims(t, h, ndTunnel); len(got) != 1 || got[0].Claimant != ndClaimant2 || got[0].Network != "web" {
		t.Errorf("claims %+v, want only %s on web", got, ndClaimant2)
	}
	var got []string
	for _, e := range ndEffects(t, h, "c4") {
		got = append(got, e.Resource)
	}
	want := []string{
		"cloudflare.claim:" + ndTunnel + "|web|" + ndClaimant,
		"cloudflare.endpoint:" + ndTunnel + "|web2",
		"cloudflare.claim:" + ndTunnel + "|web2|" + ndClaimant,
	}
	if !slices.Equal(got, want) {
		t.Errorf("effects %v, want %v (each network's disconnect before its claim)", got, want)
	}
}

func TestNdApplyConverges(t *testing.T) {
	ctx := context.Background()
	t.Run("a second detach changes nothing", func(t *testing.T) {
		h := newND(t)
		ndAttach(t, h, "c1", "web", ndClaimant)
		if _, err := ndApply(t, h, "c2"); err != nil {
			t.Fatal(err)
		}
		mark := len(h.docker.calls())
		out, err := ndApply(t, h, "c3")
		if err != nil {
			t.Fatal(err)
		}
		if want := (NetworkAttachment{Tunnel: ndTunnel, Network: "web", Claimant: ndClaimant}); out != want {
			t.Errorf("output %+v, want %+v", out, want)
		}
		if got := h.docker.calls()[mark:]; len(got) != 0 {
			t.Errorf("a converged detach called the Engine: %v", got)
		}
		if effs := ndEffects(t, h, "c3"); len(effs) != 0 {
			t.Errorf("effects %+v, want none", effs)
		}
		plan, err := h.cf().networkDetach().Handler.Plan(ctx, h.env(""), ndInput())
		if err != nil || len(plan.Diff) != 0 {
			t.Errorf("plan after the detach: diff %+v, error %v; want an empty diff", plan.Diff, err)
		}
	})
	t.Run("the connector container is gone", func(t *testing.T) {
		h := newND(t)
		ndAddClaim(t, h, claimRow{TunnelID: ndTunnel, Network: "web", Claimant: ndClaimant, ChangeID: "c0"})
		if err := h.docker.RemoveContainer(ctx, connectorName(ndTunnel), dockerapi.RemoveContainerOptions{Force: true}); err != nil {
			t.Fatal(err)
		}
		mark := len(h.docker.calls())
		out, err := ndApply(t, h, "c2")
		if err != nil {
			t.Fatal(err)
		}
		if out.Attached {
			t.Errorf("output %+v, want not attached", out)
		}
		if got := ndMutatedAfter(h, mark); len(got) != 0 {
			t.Errorf("Engine calls %v, want none", got)
		}
		if got := ndClaims(t, h, ndTunnel); len(got) != 0 {
			t.Errorf("claims %+v, want none", got)
		}
		if effs := ndEffects(t, h, "c2"); len(effs) != 1 || effs[0].Kind != core.EffectDelete {
			t.Errorf("effects %+v, want only the claim deletion", effs)
		}
	})
	t.Run("the claimed network is gone", func(t *testing.T) {
		h := newND(t)
		ndAddClaim(t, h, claimRow{TunnelID: ndTunnel, Network: "gone", Claimant: ndClaimant, ChangeID: "c0"})
		mark := len(h.docker.calls())
		if _, err := ndApply(t, h, "c2", func(in *NetworkDetachInput) { in.Network = "gone" }); err != nil {
			t.Fatal(err)
		}
		if got := ndMutatedAfter(h, mark); len(got) != 0 {
			t.Errorf("Engine calls %v, want none", got)
		}
		if got := ndClaims(t, h, ndTunnel); len(got) != 0 {
			t.Errorf("claims %+v, want none", got)
		}
	})
}

func TestNdApplyFailures(t *testing.T) {
	t.Run("a refused disconnect is Definite, keeps the claim and a repeat finishes", func(t *testing.T) {
		h := newND(t)
		ndAttach(t, h, "c1", "web", ndClaimant)
		h.docker.Fail("DisconnectNetwork", &dockerapi.APIError{StatusCode: http.StatusForbidden, Message: "no"})
		_, err := ndApply(t, h, "c2")
		if err == nil || !core.IsDefinite(err) {
			t.Fatalf("error %v, want a Definite refusal", err)
		}
		effs := ndEffects(t, h, "c2")
		if len(effs) != 1 || effs[0].State != core.EffectSkipped || len(ndClaims(t, h, ndTunnel)) != 1 || !ndOn(t, h, "web") {
			t.Fatalf("effects %+v, claims %+v; want one skipped effect, the claim kept and the connector on web", effs, ndClaims(t, h, ndTunnel))
		}
		if _, err := ndApply(t, h, "c3"); err != nil {
			t.Fatal(err)
		}
		if ndOn(t, h, "web") || len(ndClaims(t, h, ndTunnel)) != 0 {
			t.Errorf("after the repeat: on web %t, claims %+v; want it off web with no claim", ndOn(t, h, "web"), ndClaims(t, h, ndTunnel))
		}
	})
	t.Run("an unknown disconnect failure stays intended", func(t *testing.T) {
		h := newND(t)
		ndAttach(t, h, "c1", "web", ndClaimant)
		h.docker.Fail("DisconnectNetwork", errors.New("timeout"))
		_, err := ndApply(t, h, "c2")
		effs := ndEffects(t, h, "c2")
		if err == nil || core.IsDefinite(err) || len(effs) != 1 || effs[0].State != core.EffectIntended || len(ndClaims(t, h, ndTunnel)) != 1 {
			t.Errorf("error %v, effects %+v; want a failure that is not Definite, one intended effect and the claim kept", err, effs)
		}
	})
	t.Run("the Engine down", func(t *testing.T) {
		h := newND(t)
		ndAddClaim(t, h, claimRow{TunnelID: ndTunnel, Network: "web", Claimant: ndClaimant, ChangeID: "c0"})
		h.docker.Fail("InspectContainer", dockerapi.ErrUnavailable)
		_, err := ndApply(t, h, "c2")
		if err == nil || errcode.From(err).Code != errcode.DockerEngineUnavailable {
			t.Errorf("error %v, want docker.engine_unavailable", err)
		}
		if got := ndClaims(t, h, ndTunnel); len(got) != 1 || len(ndEffects(t, h, "c2")) != 0 {
			t.Errorf("claims %+v, effects %+v; want the claim kept and nothing journaled", got, ndEffects(t, h, "c2"))
		}
	})
}

func TestNdVerify(t *testing.T) {
	ctx := context.Background()
	h := newND(t)
	ndAttach(t, h, "c1", "web", ndClaimant)
	ndAttach(t, h, "c1b", "web2", ndClaimant)
	verify := func(mutate ...func(*NetworkDetachInput)) error {
		return h.cf().networkDetach().Handler.Verify(ctx, h.env("c2"), ndInput(mutate...), ndJSON(t, NetworkAttachment{}))
	}
	if verify() == nil || verify(ndAllNetworks) == nil {
		t.Error("Verify passed before the detach")
	}
	if _, err := ndApply(t, h, "c2"); err != nil {
		t.Fatal(err)
	}
	if err := verify(); err != nil {
		t.Errorf("Verify of web after its detach: %v", err)
	}
	if verify(ndAllNetworks) == nil {
		t.Error("Verify of every network passed while the claim on web2 is left")
	}
	ndAttach(t, h, "c4", "web", ndClaimant2)
	if err := verify(); err != nil {
		t.Errorf("Verify failed on a claim of another claimant: %v", err)
	}
	if _, err := ndApply(t, h, "c5", ndAllNetworks); err != nil {
		t.Fatal(err)
	}
	if err := verify(ndAllNetworks); err != nil {
		t.Errorf("Verify of every network after its detach: %v", err)
	}
}

func TestNdUndo(t *testing.T) {
	t.Run("a disconnect and a claim come back", func(t *testing.T) {
		h := newND(t)
		ndAttach(t, h, "c1", "web", ndClaimant)
		original := ndClaims(t, h, ndTunnel)
		if _, err := ndApply(t, h, "c2"); err != nil {
			t.Fatal(err)
		}
		effs := ndEffects(t, h, "c2")
		for _, e := range effs {
			if got := ndObserved(t, h, "c2", e); got != string(e.Post.State) {
				t.Errorf("%s: observed %s before the undo, journaled %s", e.Resource, got, e.Post.State)
			}
		}
		mark := len(h.docker.calls())
		results := ndUndo(t, h, "c2")
		endpoint := "cloudflare.endpoint:" + ndTunnel + "|web"
		if got := results[endpoint]; got.Resource != endpoint {
			t.Errorf("inverse of the endpoint names %q, want %s", got.Resource, endpoint)
		}
		if got := ndMutatedAfter(h, mark); !slices.Equal(got, []string{"ConnectNetwork"}) || !ndOn(t, h, "web") {
			t.Errorf("Engine calls %v, on web %t; want one ConnectNetwork and the connector back on web", got, ndOn(t, h, "web"))
		}
		if got := ndClaims(t, h, ndTunnel); !slices.Equal(got, original) {
			t.Errorf("claims %+v, want the original %+v back", got, original)
		}
		for _, e := range effs {
			if e.Kind != core.EffectDelete {
				continue
			}
			e.State = core.EffectUndone
			if got := ndObserved(t, h, "c2", e); got != string(e.Before.State) {
				t.Errorf("%s: observed %s after the undo, want %s", e.Resource, got, e.Before.State)
			}
		}
	})
	t.Run("a preexisting claim comes back without an Engine call", func(t *testing.T) {
		h := newND(t)
		ndJoin(t, h, "web", "172.20.0.9")
		ndAttach(t, h, "c1", "web", ndClaimant)
		original := ndClaims(t, h, ndTunnel)
		if len(original) != 1 || !original[0].Preexisting {
			t.Fatalf("claims %+v, want one preexisting claim", original)
		}
		if _, err := ndApply(t, h, "c2"); err != nil {
			t.Fatal(err)
		}
		mark := len(h.docker.calls())
		ndUndo(t, h, "c2")
		if got := ndMutatedAfter(h, mark); len(got) != 0 || !ndOn(t, h, "web") {
			t.Errorf("Engine calls %v, on web %t; want none and the connector still on web", got, ndOn(t, h, "web"))
		}
		if got := ndClaims(t, h, ndTunnel); !slices.Equal(got, original) {
			t.Errorf("claims %+v, want the original %+v back", got, original)
		}
	})
}
