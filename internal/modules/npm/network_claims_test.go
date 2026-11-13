package npm

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

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/npm/fake"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const (
	nwNet      = "appnet"
	nwNet2     = "appnet2"
	nwClaimant = "expose:app.example.com"
	nwOther    = "expose:other.example.com"
)

// nwModule registers the actions, so the registry's own checks run on them.
type nwModule struct{ defs []core.ActionDef }

func (m nwModule) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, Actions: m.defs}
}

type nwSetupFunc func(t *testing.T, h *thHost, user dockerapi.Network)

// nwRefused asserts err is a definite refusal with the given code and returns it.
func nwRefused(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != code {
		t.Fatalf("error %v, want code %s", err, code)
	}
	if !core.IsDefinite(err) {
		t.Errorf("error %v is not marked definite", err)
	}
	return coded
}

func nwJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// nwBridge is a user-defined bridge network.
func nwBridge(name string) dockerapi.Network {
	return thNetwork(name, func(n *dockerapi.Network) { n.Driver = "bridge" })
}

// nwSetup deploys the instance and adds the user-defined network appnet with a
// container web on it.
func nwSetup(t *testing.T) (*thHost, dockerapi.Network) {
	t.Helper()
	h := newTHHost(t)
	h.deployed()
	user := nwBridge(nwNet)
	h.docker.putNetwork(user)
	h.docker.putContainer(thContainer("web", func(c *dockerapi.ContainerJSON) {
		c.NetworkSettings.Networks = map[string]*dockerapi.EndpointSettings{nwNet: {NetworkID: user.ID}}
	}))
	return h, user
}

// nwConnect puts the NPM container on network, as if it had been there.
func nwConnect(t *testing.T, h *thHost, network dockerapi.Network, ep *dockerapi.EndpointSettings) {
	t.Helper()
	if err := h.docker.ConnectNetwork(context.Background(), network.ID, containerName, ep); err != nil {
		t.Fatal(err)
	}
}

func nwStatic(addr string) *dockerapi.EndpointSettings {
	return &dockerapi.EndpointSettings{IPAMConfig: &dockerapi.EndpointIPAMConfig{IPv4Address: netip.MustParseAddr(addr)}}
}

// nwEndpoint is the NPM container's endpoint on the network with the id, nil when it is not attached.
func nwEndpoint(t *testing.T, h *thHost, networkID string) *dockerapi.EndpointSettings {
	t.Helper()
	c, ok := h.docker.container(containerName)
	if !ok {
		t.Fatal("the NPM container is missing")
	}
	for _, ep := range c.NetworkSettings.Networks {
		if ep != nil && ep.NetworkID == networkID {
			return ep
		}
	}
	return nil
}

func nwClaim(t *testing.T, h *thHost, network, claimant, change string, preexisting bool) {
	t.Helper()
	ctx := context.Background()
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		return addClaim(ctx, tx, claimRow{Network: network, Claimant: claimant, ChangeID: change, Preexisting: preexisting})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func nwClaims(t *testing.T, h *thHost, network string) []claimRow {
	t.Helper()
	got, err := claims(context.Background(), h.db.Read(), network)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func nwCalls(h *thHost, method string) int {
	n := 0
	for _, c := range h.docker.calls() {
		if c == method {
			n++
		}
	}
	return n
}

func nwApplyAttach(t *testing.T, h *thHost, change string, in AttachInput) (Attachment, error) {
	t.Helper()
	h.addChange(change, "npm.network.attach")
	raw, err := h.nm().networkAttach().Handler.Apply(context.Background(), h.env(change), nwJSON(t, in))
	if err != nil {
		return Attachment{}, err
	}
	var out Attachment
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

func nwApplyDetach(t *testing.T, h *thHost, change string, in DetachInput) (Attachment, error) {
	t.Helper()
	h.addChange(change, "npm.network.detach")
	raw, err := h.nm().networkDetach().Handler.Apply(context.Background(), h.env(change), nwJSON(t, in))
	if err != nil {
		return Attachment{}, err
	}
	var out Attachment
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

func nwBridgeOnly(_ *testing.T, h *thHost, _ dockerapi.Network) {
	br := thNetwork("bridge", func(n *dockerapi.Network) { n.Driver = "bridge" })
	h.docker.putNetwork(br)
	h.docker.putContainer(thContainer("legacy", func(c *dockerapi.ContainerJSON) {
		c.NetworkSettings.Networks = map[string]*dockerapi.EndpointSettings{"bridge": {NetworkID: br.ID}}
	}))
}

func nwOverlay(_ *testing.T, h *thHost, _ dockerapi.Network) {
	ov := thNetwork("overlaynet", func(n *dockerapi.Network) { n.Driver = "overlay" })
	h.docker.putNetwork(ov)
	h.docker.putContainer(thContainer("ov", func(c *dockerapi.ContainerJSON) {
		c.NetworkSettings.Networks = map[string]*dockerapi.EndpointSettings{"overlaynet": {NetworkID: ov.ID}}
	}))
}

func nwOtherNet(_ *testing.T, h *thHost, _ dockerapi.Network) {
	h.docker.putNetwork(nwBridge("other"))
}

func TestNwAttachPlan(t *testing.T) {
	ctx := context.Background()
	base := AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}
	join := core.DiffLine{Op: "+", Text: "container darkpanel-npm joins network appnet"}
	claim := core.DiffLine{Op: "+", Text: "claim of expose:app.example.com on network appnet"}
	tests := []struct {
		name  string
		setup nwSetupFunc
		mod   func(in *AttachInput)
		diff  []core.DiffLine
	}{
		{name: "first claim connects", diff: []core.DiffLine{join, claim}},
		{name: "without a target", mod: func(in *AttachInput) { in.Target = "" }, diff: []core.DiffLine{join, claim}},
		{name: "static address", mod: func(in *AttachInput) { in.Address = netip.MustParseAddr("10.9.0.2") },
			diff: []core.DiffLine{join, {Op: "+", Text: "ipv4 10.9.0.2"}, claim}},
		{name: "attachment already there", setup: func(t *testing.T, h *thHost, user dockerapi.Network) {
			nwConnect(t, h, user, nil)
		}, diff: []core.DiffLine{claim, {Op: " ", Text: "darkpanel-npm is already on network appnet; detaching leaves it there"}}},
		{name: "second claimant", setup: func(t *testing.T, h *thHost, user dockerapi.Network) {
			nwConnect(t, h, user, nil)
			nwClaim(t, h, nwNet, nwOther, "c0", false)
		}, diff: []core.DiffLine{claim}},
		{name: "same claimant again", setup: func(t *testing.T, h *thHost, user dockerapi.Network) {
			nwConnect(t, h, user, nil)
			nwClaim(t, h, nwNet, nwClaimant, "c0", false)
		}, diff: nil},
		{name: "claim whose attachment is gone", setup: func(t *testing.T, h *thHost, _ dockerapi.Network) {
			nwClaim(t, h, nwNet, nwClaimant, "c0", false)
		}, diff: []core.DiffLine{join}},
		{name: "a forwarded address another container holds", setup: func(t *testing.T, h *thHost, user dockerapi.Network) {
			w12aSubnet(t, h, user)
			h.docker.putContainer(thContainer("db", thEndpoint(nwNet, "10.9.0.7")))
			w12aSeed(h, "10.9.0.7", 3000)
		}, diff: []core.DiffLine{join, claim}},
		{name: "a forwarded gateway", setup: w12aForwardsTo("10.9.0.1", 8080), diff: []core.DiffLine{join, claim}},
		{name: "a forwarded address outside the subnet", setup: w12aForwardsTo("10.10.0.7", 3000), diff: []core.DiffLine{join, claim}},
		{name: "a forwarded address outside the IP range", setup: func(_ *testing.T, h *thHost, _ dockerapi.Network) {
			h.docker.putNetwork(thNetwork(nwNet, func(n *dockerapi.Network) {
				n.IPAM.Config = []dockerapi.IPAMConfig{{Subnet: netip.MustParsePrefix("10.9.0.0/24"),
					IPRange: netip.MustParsePrefix("10.9.0.128/25"), Gateway: netip.MustParseAddr("10.9.0.1")}}
			}))
			w12aSeed(h, "10.9.0.7", 3000)
		}, diff: []core.DiffLine{join, claim}},
		{name: "a static address no proxy host forwards to", setup: w12aForwardsTo("10.9.0.7", 3000),
			mod:  func(in *AttachInput) { in.Address = netip.MustParseAddr("10.9.0.2") },
			diff: []core.DiffLine{join, {Op: "+", Text: "ipv4 10.9.0.2"}, claim}},
		{name: "a forwarded IPv6 address on a network without IPv6", setup: w12aForwardsTo("fd00:9::7", 3000),
			diff: []core.DiffLine{join, claim}},
		{name: "a forwarded address while NPM is already on the network", setup: func(t *testing.T, h *thHost, user dockerapi.Network) {
			w12aSubnet(t, h, user)
			nwConnect(t, h, user, nwStatic("10.9.0.5"))
			nwClaim(t, h, nwNet, nwOther, "c0", false)
			w12aSeed(h, "10.9.0.7", 3000)
		}, diff: []core.DiffLine{claim}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, user := nwSetup(t)
			if tt.setup != nil {
				tt.setup(t, h, user)
			}
			in := base
			if tt.mod != nil {
				tt.mod(&in)
			}
			connects := nwCalls(h, "ConnectNetwork")
			out, err := h.nm().networkAttach().Handler.Plan(ctx, h.env(""), nwJSON(t, in))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(out.Diff, tt.diff) {
				t.Errorf("diff %v, want %v", out.Diff, tt.diff)
			}
			if out.StateHash == "" {
				t.Error("empty state hash")
			}
			if nwCalls(h, "ConnectNetwork") != connects {
				t.Error("a plan connected a network")
			}
		})
	}
}

func TestNwAttachPlanStateHash(t *testing.T) {
	ctx := context.Background()
	h, user := nwSetup(t)
	def := h.nm().networkAttach()
	in := nwJSON(t, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})
	plan := func() string {
		t.Helper()
		out, err := def.Handler.Plan(ctx, h.env(""), in)
		if err != nil || out.StateHash == "" {
			t.Fatalf("plan: hash %q, error %v", out.StateHash, err)
		}
		return out.StateHash
	}
	first := plan()
	if again := plan(); again != first {
		t.Errorf("a second plan hashes %s, want %s", again, first)
	}
	nwConnect(t, h, user, nil)
	if attached := plan(); attached == first {
		t.Error("the hash did not change when NPM joined the network")
	}
}

func TestNwAttachPlanRefusals(t *testing.T) {
	ctx := context.Background()
	base := AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}
	tests := []struct {
		name  string
		setup nwSetupFunc
		mod   func(in *AttachInput)
		code  errcode.Code
		field string
		has   string
	}{
		{name: "unknown network", mod: func(in *AttachInput) { in.Network = "ghost" }, code: errcode.NotFound},
		{name: "unknown target", mod: func(in *AttachInput) { in.Target = "ghost" }, code: errcode.NotFound},
		{name: "target only on the default bridge", setup: nwBridgeOnly,
			mod:  func(in *AttachInput) { in.Network, in.Target = "bridge", "legacy" },
			code: errcode.NPMUnsupportedTarget, has: "connect the container to a user-defined network"},
		{name: "target on a non-bridge driver", setup: nwOverlay,
			mod:  func(in *AttachInput) { in.Network, in.Target = "overlaynet", "ov" },
			code: errcode.NPMUnsupportedTarget, has: "connect the container to a user-defined network"},
		{name: "target not on the network", setup: nwOtherNet,
			mod:  func(in *AttachInput) { in.Network = "other" },
			code: errcode.NPMUnsupportedTarget, has: "connect the container to a user-defined network"},
		{name: "the default bridge without a target", setup: nwBridgeOnly,
			mod:  func(in *AttachInput) { in.Network, in.Target = "bridge", "" },
			code: errcode.NPMUnsupportedTarget},
		{name: "empty claimant", mod: func(in *AttachInput) { in.Claimant = "" }, code: errcode.InvalidInput, field: "/claimant"},
		{name: "claimant with a space", mod: func(in *AttachInput) { in.Claimant = "expose: x" }, code: errcode.InvalidInput, field: "/claimant"},
		{name: "network name with a space", mod: func(in *AttachInput) { in.Network = "a b" }, code: errcode.InvalidInput, field: "/network"},
		{name: "ipv6 address", mod: func(in *AttachInput) { in.Address = netip.MustParseAddr("fd00::2") },
			code: errcode.InvalidInput, field: "/address"},
		{name: "address differs from the existing endpoint", setup: func(t *testing.T, h *thHost, user dockerapi.Network) {
			nwConnect(t, h, user, nwStatic("10.9.0.5"))
		}, mod: func(in *AttachInput) { in.Address = netip.MustParseAddr("10.9.0.2") }, code: errcode.InvalidInput, field: "/address"},
		{name: "an address a proxy host forwards to", setup: w12aForwardsTo("10.9.0.7", 3000),
			mod:  func(in *AttachInput) { in.Address = netip.MustParseAddr("10.9.0.7") },
			code: errcode.NPMReserved, has: "proxy host 1 forwards to 10.9.0.7, an address darkpanel-npm could take on network appnet"},
		{name: "a free address the Engine could assign", setup: w12aForwardsTo("10.9.0.7", 81),
			code: errcode.NPMReserved, has: "proxy host 1 forwards to 10.9.0.7, an address darkpanel-npm could take on network appnet"},
		{name: "a forward host in nginx's address form", setup: w12aForwardsTo("10.9.0.07", 3000),
			code: errcode.NPMReserved, has: "proxy host 1 forwards to 10.9.0.7, an address darkpanel-npm could take on network appnet"},
		{name: "an address a custom location forwards to", setup: func(t *testing.T, h *thHost, user dockerapi.Network) {
			w12aSubnet(t, h, user)
			w12aSeed(h, "web", 8080, npmapi.Location{Path: "/api", ForwardScheme: "http", ForwardHost: "10.9.0.7", ForwardPort: 3000})
		}, code: errcode.NPMReserved, has: "location /api of proxy host 1 forwards to 10.9.0.7, an address darkpanel-npm could take on network appnet"},
		{name: "an IPv6 address the Engine assigns beside a static IPv4 address", setup: func(_ *testing.T, h *thHost, _ dockerapi.Network) {
			h.docker.putNetwork(thNetwork(nwNet, thSubnet("10.9.0.0/24", "10.9.0.1"), thSubnet("fd00:9::/64", "fd00:9::1"),
				func(n *dockerapi.Network) { n.EnableIPv6 = true }))
			w12aSeed(h, "fd00:9::7", 3000)
		}, mod: func(in *AttachInput) { in.Address = netip.MustParseAddr("10.9.0.2") },
			code: errcode.NPMReserved, has: "proxy host 1 forwards to fd00:9::7, an address darkpanel-npm could take on network appnet"},
		{name: "a forwarded address on a network that reports no subnet", setup: func(_ *testing.T, h *thHost, _ dockerapi.Network) {
			w12aSeed(h, "10.9.0.7", 3000)
		}, code: errcode.NPMReserved, has: "proxy host 1 forwards to 10.9.0.7, an address darkpanel-npm could take on network appnet"},
		{name: "an NPM API that cannot list the proxy hosts", setup: func(t *testing.T, h *thHost, user dockerapi.Network) {
			w12aSubnet(t, h, user)
			h.npm.Fail(http.MethodGet, "/api/nginx/proxy-hosts", fake.Fault{Status: http.StatusInternalServerError})
		}, code: errcode.NPMUnhealthy, has: "Nginx Proxy Manager did not answer"},
		{name: "NPM container missing", setup: func(t *testing.T, h *thHost, _ dockerapi.Network) {
			err := h.docker.RemoveContainer(ctx, containerName, dockerapi.RemoveContainerOptions{Force: true})
			if err != nil {
				t.Fatal(err)
			}
		}, code: errcode.NPMUnhealthy},
		{name: "engine unavailable", setup: func(_ *testing.T, h *thHost, _ dockerapi.Network) {
			h.docker.Fail("InspectContainer", dockerapi.ErrUnavailable)
		}, code: errcode.DockerEngineUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, user := nwSetup(t)
			if tt.setup != nil {
				tt.setup(t, h, user)
			}
			in := base
			if tt.mod != nil {
				tt.mod(&in)
			}
			connects := nwCalls(h, "ConnectNetwork")
			_, err := h.nm().networkAttach().Handler.Plan(ctx, h.env(""), nwJSON(t, in))
			coded := nwRefused(t, err, tt.code)
			if tt.field != "" && !slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == tt.field }) {
				t.Errorf("fields %v, want one at %s", coded.Fields, tt.field)
			}
			if !strings.Contains(coded.Detail, tt.has) {
				t.Errorf("detail %q does not hold %q", coded.Detail, tt.has)
			}
			if nwCalls(h, "ConnectNetwork") != connects {
				t.Error("a refused plan connected a network")
			}
		})
	}
	t.Run("no instance", func(t *testing.T) {
		h := newTHHost(t)
		_, err := h.nm().networkAttach().Handler.Plan(ctx, h.env(""), nwJSON(t, base))
		_ = nwRefused(t, err, errcode.NotFound)
	})
}

func TestNwAttachApply(t *testing.T) {
	in := AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}

	t.Run("connects and claims", func(t *testing.T) {
		h, user := nwSetup(t)
		out, err := nwApplyAttach(t, h, "c1", in)
		if err != nil {
			t.Fatal(err)
		}
		if want := (Attachment{Network: nwNet, Claimant: nwClaimant, Attached: true}); out != want {
			t.Errorf("output %+v, want %+v", out, want)
		}
		if nwEndpoint(t, h, user.ID) == nil {
			t.Error("NPM is not on the network")
		}
		want := []claimRow{{Network: nwNet, Claimant: nwClaimant, ChangeID: "c1"}}
		if got := nwClaims(t, h, nwNet); !slices.Equal(got, want) {
			t.Errorf("claims %+v, want %+v", got, want)
		}
		effects := h.journal.effects()
		if len(effects) != 2 {
			t.Fatalf("%d effects, want 2: %+v", len(effects), effects)
		}
		ep, cl := effects[0], effects[1]
		if ep.Kind != core.EffectModify || ep.Resource != "npm.endpoint:appnet" || ep.State != core.EffectMade || ep.Local ||
			ep.CommitWait != commitWait || ep.InverseRecovery != nil {
			t.Errorf("endpoint effect %+v, want a made non-local modify of npm.endpoint:appnet with commit wait %s", ep, commitWait)
		}
		if cl.Kind != core.EffectCreate || cl.Resource != "npm.claim:appnet|expose:app.example.com" || cl.State != core.EffectMade || !cl.Local {
			t.Errorf("claim effect %+v, want a made local create of the claim", cl)
		}
		var doc struct {
			Network     string `json:"network"`
			Claimant    string `json:"claimant"`
			Preexisting bool   `json:"preexisting"`
			ChangeID    string `json:"changeId"`
		}
		if cl.Post == nil || json.Unmarshal(cl.Post.State, &doc) != nil || doc.Network != nwNet || doc.Claimant != nwClaimant || doc.Preexisting || doc.ChangeID != "c1" {
			t.Errorf("claim post %+v (%+v), want the claim of change c1", cl.Post, doc)
		}
	})

	t.Run("static address", func(t *testing.T) {
		h, user := nwSetup(t)
		withAddr := in
		withAddr.Address = netip.MustParseAddr("10.9.0.2")
		if _, err := nwApplyAttach(t, h, "c1", withAddr); err != nil {
			t.Fatal(err)
		}
		ep := nwEndpoint(t, h, user.ID)
		if ep == nil || ep.IPAMConfig == nil || ep.IPAMConfig.IPv4Address != withAddr.Address {
			t.Errorf("endpoint %+v, want the static address %s", ep, withAddr.Address)
		}
	})

	t.Run("second claimant adds only a claim", func(t *testing.T) {
		h, user := nwSetup(t)
		nwConnect(t, h, user, nil)
		nwClaim(t, h, nwNet, nwOther, "c0", false)
		connects := nwCalls(h, "ConnectNetwork")
		out, err := nwApplyAttach(t, h, "c1", in)
		if err != nil {
			t.Fatal(err)
		}
		if nwCalls(h, "ConnectNetwork") != connects {
			t.Error("apply connected a network that NPM is on")
		}
		if out.Preexisting || !out.Attached {
			t.Errorf("output %+v, want a claim that is not preexisting and attached", out)
		}
		if effects := h.journal.effects(); len(effects) != 1 || effects[0].Kind != core.EffectCreate {
			t.Errorf("effects %+v, want only the claim creation", effects)
		}
		if got := nwClaims(t, h, nwNet); len(got) != 2 {
			t.Errorf("claims %+v, want two", got)
		}
	})

	t.Run("attachment that was there before", func(t *testing.T) {
		h, user := nwSetup(t)
		nwConnect(t, h, user, nil)
		out, err := nwApplyAttach(t, h, "c1", in)
		if err != nil {
			t.Fatal(err)
		}
		if !out.Preexisting || !out.Attached {
			t.Errorf("output %+v, want a preexisting attachment", out)
		}
		want := []claimRow{{Network: nwNet, Claimant: nwClaimant, ChangeID: "c1", Preexisting: true}}
		if got := nwClaims(t, h, nwNet); !slices.Equal(got, want) {
			t.Errorf("claims %+v, want %+v", got, want)
		}
	})

	t.Run("a later claim copies preexisting", func(t *testing.T) {
		h, user := nwSetup(t)
		nwConnect(t, h, user, nil)
		nwClaim(t, h, nwNet, nwOther, "c0", true)
		out, err := nwApplyAttach(t, h, "c1", in)
		if err != nil {
			t.Fatal(err)
		}
		if !out.Preexisting {
			t.Errorf("output %+v, want preexisting copied from the existing claim", out)
		}
	})

	t.Run("same claimant again changes nothing", func(t *testing.T) {
		h, user := nwSetup(t)
		nwConnect(t, h, user, nil)
		nwClaim(t, h, nwNet, nwClaimant, "c0", false)
		out, err := nwApplyAttach(t, h, "c1", in)
		if err != nil {
			t.Fatal(err)
		}
		if !out.Attached || len(h.journal.effects()) != 0 {
			t.Errorf("output %+v with %d effects, want attached and none", out, len(h.journal.effects()))
		}
	})

	t.Run("claim whose attachment is gone reconnects only", func(t *testing.T) {
		h, user := nwSetup(t)
		nwClaim(t, h, nwNet, nwClaimant, "c0", false)
		if _, err := nwApplyAttach(t, h, "c1", in); err != nil {
			t.Fatal(err)
		}
		if nwEndpoint(t, h, user.ID) == nil {
			t.Error("NPM is not on the network")
		}
		if effects := h.journal.effects(); len(effects) != 1 || effects[0].Kind != core.EffectModify {
			t.Errorf("effects %+v, want only the endpoint modify", effects)
		}
		if got := nwClaims(t, h, nwNet); len(got) != 1 || got[0].ChangeID != "c0" {
			t.Errorf("claims %+v, want the original claim untouched", got)
		}
	})

	t.Run("the daemon refuses the connect", func(t *testing.T) {
		h, _ := nwSetup(t)
		h.docker.Fail("ConnectNetwork", &dockerapi.APIError{StatusCode: 403, Message: "forbidden"})
		_, err := nwApplyAttach(t, h, "c1", in)
		if err == nil || !core.IsDefinite(err) {
			t.Fatalf("error %v, want a definite refusal", err)
		}
		if effects := h.journal.effects(); len(effects) != 1 || effects[0].State != core.EffectSkipped {
			t.Errorf("effects %+v, want one skipped", effects)
		}
		if got := nwClaims(t, h, nwNet); len(got) != 0 {
			t.Errorf("claims %+v, want none", got)
		}
	})

	t.Run("the daemon fails", func(t *testing.T) {
		h, _ := nwSetup(t)
		h.docker.Fail("ConnectNetwork", &dockerapi.APIError{StatusCode: 500, Message: "boom"})
		_, err := nwApplyAttach(t, h, "c1", in)
		if err == nil || core.IsDefinite(err) {
			t.Fatalf("error %v, want an uncertain failure", err)
		}
		if effects := h.journal.effects(); len(effects) != 1 || effects[0].State != core.EffectIntended {
			t.Errorf("effects %+v, want one left intended", effects)
		}
		if got := nwClaims(t, h, nwNet); len(got) != 0 {
			t.Errorf("claims %+v, want none", got)
		}
	})
}

func TestNwAttachVerify(t *testing.T) {
	ctx := context.Background()
	h, _ := nwSetup(t)
	def := h.nm().networkAttach()
	raw := nwJSON(t, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})
	if def.Handler.Verify(ctx, h.env("c1"), raw, json.RawMessage(`{}`)) == nil {
		t.Error("verify passed before the attach")
	}
	if _, err := nwApplyAttach(t, h, "c1", AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}); err != nil {
		t.Fatal(err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), raw, json.RawMessage(`{}`)); err != nil {
		t.Errorf("verify after the attach: %v", err)
	}
}

// nwSeedAttached puts NPM on the network with a claim, as an earlier attach left it.
func nwSeedAttached(t *testing.T, h *thHost, network dockerapi.Network, claimant string, preexisting bool) {
	t.Helper()
	nwConnect(t, h, network, nil)
	nwClaim(t, h, network.Name, claimant, "c0", preexisting)
}

func TestNwDetachPlan(t *testing.T) {
	ctx := context.Background()
	del := func(network string) core.DiffLine {
		return core.DiffLine{Op: "-", Text: "claim of expose:app.example.com on network " + network}
	}
	leave := func(network string) core.DiffLine {
		return core.DiffLine{Op: "-", Text: "container darkpanel-npm leaves network " + network}
	}
	tests := []struct {
		name  string
		setup nwSetupFunc
		in    DetachInput
		diff  []core.DiffLine
	}{
		{name: "last claim leaves the network", setup: func(t *testing.T, h *thHost, user dockerapi.Network) {
			nwSeedAttached(t, h, user, nwClaimant, false)
		}, in: DetachInput{Network: nwNet, Claimant: nwClaimant}, diff: []core.DiffLine{del(nwNet), leave(nwNet)}},
		{name: "another claim keeps NPM there", setup: func(t *testing.T, h *thHost, user dockerapi.Network) {
			nwSeedAttached(t, h, user, nwClaimant, false)
			nwClaim(t, h, nwNet, nwOther, "c0", false)
		}, in: DetachInput{Network: nwNet, Claimant: nwClaimant}, diff: []core.DiffLine{del(nwNet),
			{Op: " ", Text: "darkpanel-npm stays on network appnet: claimed by expose:other.example.com"}}},
		{name: "attachment that was there before", setup: func(t *testing.T, h *thHost, user dockerapi.Network) {
			nwSeedAttached(t, h, user, nwClaimant, true)
		}, in: DetachInput{Network: nwNet, Claimant: nwClaimant}, diff: []core.DiffLine{del(nwNet),
			{Op: " ", Text: "darkpanel-npm stays on network appnet: it was there before"}}},
		{name: "no claim", setup: func(t *testing.T, h *thHost, user dockerapi.Network) {
			nwConnect(t, h, user, nil)
		}, in: DetachInput{Network: nwNet, Claimant: nwClaimant}, diff: nil},
		{name: "no claim on any network", in: DetachInput{Claimant: nwClaimant}, diff: nil},
		{name: "every network of the claimant", setup: func(t *testing.T, h *thHost, user dockerapi.Network) {
			second := nwBridge(nwNet2)
			h.docker.putNetwork(second)
			nwSeedAttached(t, h, user, nwClaimant, false)
			nwSeedAttached(t, h, second, nwClaimant, false)
		}, in: DetachInput{Claimant: nwClaimant}, diff: []core.DiffLine{del(nwNet), leave(nwNet), del(nwNet2), leave(nwNet2)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, user := nwSetup(t)
			if tt.setup != nil {
				tt.setup(t, h, user)
			}
			disconnects := nwCalls(h, "DisconnectNetwork")
			out, err := h.nm().networkDetach().Handler.Plan(ctx, h.env(""), nwJSON(t, tt.in))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(out.Diff, tt.diff) {
				t.Errorf("diff %v, want %v", out.Diff, tt.diff)
			}
			if nwCalls(h, "DisconnectNetwork") != disconnects {
				t.Error("a plan disconnected a network")
			}
		})
	}
	t.Run("claimant is required", func(t *testing.T) {
		h, _ := nwSetup(t)
		_, err := h.nm().networkDetach().Handler.Plan(ctx, h.env(""), nwJSON(t, DetachInput{Network: nwNet}))
		_ = nwRefused(t, err, errcode.InvalidInput)
	})
}

func TestNwDetachApply(t *testing.T) {
	t.Run("last claim disconnects", func(t *testing.T) {
		h, user := nwSetup(t)
		nwSeedAttached(t, h, user, nwClaimant, false)
		out, err := nwApplyDetach(t, h, "c2", DetachInput{Network: nwNet, Claimant: nwClaimant})
		if err != nil {
			t.Fatal(err)
		}
		if want := (Attachment{Network: nwNet, Claimant: nwClaimant}); out != want {
			t.Errorf("output %+v, want %+v", out, want)
		}
		if nwEndpoint(t, h, user.ID) != nil {
			t.Error("NPM is still on the network")
		}
		if got := nwClaims(t, h, nwNet); len(got) != 0 {
			t.Errorf("claims %+v, want none", got)
		}
		effects := h.journal.effects()
		if len(effects) != 2 {
			t.Fatalf("%d effects, want 2: %+v", len(effects), effects)
		}
		ep, cl := effects[0], effects[1]
		if ep.Kind != core.EffectModify || ep.Resource != "npm.endpoint:appnet" || ep.State != core.EffectMade || ep.Local ||
			ep.CommitWait != commitWait || ep.InverseRecovery == nil || ep.InverseRecovery.Value != "darkpanel-npm@appnet" {
			t.Errorf("endpoint effect %+v, want a made non-local modify declaring the inverse recovery darkpanel-npm@appnet", ep)
		}
		if cl.Kind != core.EffectDelete || cl.Resource != "npm.claim:appnet|expose:app.example.com" || cl.State != core.EffectMade ||
			!cl.Local || cl.Post == nil || string(cl.Post.State) != "null" {
			t.Errorf("claim effect %+v, want a made local delete with the null post-state", cl)
		}
	})

	t.Run("another claim keeps NPM there", func(t *testing.T) {
		h, user := nwSetup(t)
		nwSeedAttached(t, h, user, nwClaimant, false)
		nwClaim(t, h, nwNet, nwOther, "c0", false)
		out, err := nwApplyDetach(t, h, "c2", DetachInput{Network: nwNet, Claimant: nwClaimant})
		if err != nil {
			t.Fatal(err)
		}
		if !out.Attached {
			t.Errorf("output %+v, want NPM still attached", out)
		}
		if nwCalls(h, "DisconnectNetwork") != 0 || nwEndpoint(t, h, user.ID) == nil {
			t.Error("NPM left a network another claim needs")
		}
		if got := nwClaims(t, h, nwNet); len(got) != 1 || got[0].Claimant != nwOther {
			t.Errorf("claims %+v, want only %s", got, nwOther)
		}
		if effects := h.journal.effects(); len(effects) != 1 || effects[0].Kind != core.EffectDelete {
			t.Errorf("effects %+v, want only the claim deletion", effects)
		}
	})

	t.Run("attachment that was there before stays", func(t *testing.T) {
		h, user := nwSetup(t)
		nwSeedAttached(t, h, user, nwClaimant, true)
		out, err := nwApplyDetach(t, h, "c2", DetachInput{Network: nwNet, Claimant: nwClaimant})
		if err != nil {
			t.Fatal(err)
		}
		if !out.Preexisting || !out.Attached || nwEndpoint(t, h, user.ID) == nil {
			t.Errorf("output %+v, want a preexisting attachment left in place", out)
		}
	})

	t.Run("every network of the claimant", func(t *testing.T) {
		h, user := nwSetup(t)
		second := nwBridge(nwNet2)
		h.docker.putNetwork(second)
		nwSeedAttached(t, h, user, nwClaimant, false)
		nwSeedAttached(t, h, second, nwClaimant, false)
		nwClaim(t, h, nwNet, nwOther, "c0", false)
		if _, err := nwApplyDetach(t, h, "c2", DetachInput{Claimant: nwClaimant}); err != nil {
			t.Fatal(err)
		}
		if nwEndpoint(t, h, user.ID) == nil {
			t.Error("NPM left appnet, which the other claimant still needs")
		}
		if nwEndpoint(t, h, second.ID) != nil {
			t.Error("NPM is still on appnet2")
		}
		if got := nwClaims(t, h, nwNet2); len(got) != 0 {
			t.Errorf("appnet2 claims %+v, want none", got)
		}
		if got := nwClaims(t, h, nwNet); len(got) != 1 || got[0].Claimant != nwOther {
			t.Errorf("appnet claims %+v, want only %s", got, nwOther)
		}
	})

	t.Run("no claim changes nothing", func(t *testing.T) {
		h, user := nwSetup(t)
		nwConnect(t, h, user, nil)
		if _, err := nwApplyDetach(t, h, "c2", DetachInput{Network: nwNet, Claimant: nwClaimant}); err != nil {
			t.Fatal(err)
		}
		if len(h.journal.effects()) != 0 || nwCalls(h, "DisconnectNetwork") != 0 {
			t.Error("a detach without a claim journaled an effect or disconnected")
		}
	})

	t.Run("NPM container missing", func(t *testing.T) {
		h, _ := nwSetup(t)
		nwClaim(t, h, nwNet, nwClaimant, "c0", false)
		if err := h.docker.RemoveContainer(context.Background(), containerName, dockerapi.RemoveContainerOptions{Force: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := nwApplyDetach(t, h, "c2", DetachInput{Network: nwNet, Claimant: nwClaimant}); err != nil {
			t.Fatal(err)
		}
		if nwCalls(h, "DisconnectNetwork") != 0 || len(nwClaims(t, h, nwNet)) != 0 {
			t.Error("detach disconnected a missing container or kept the claim")
		}
	})

	t.Run("the daemon refuses the disconnect", func(t *testing.T) {
		h, user := nwSetup(t)
		nwSeedAttached(t, h, user, nwClaimant, false)
		h.docker.Fail("DisconnectNetwork", &dockerapi.APIError{StatusCode: 403, Message: "forbidden"})
		_, err := nwApplyDetach(t, h, "c2", DetachInput{Network: nwNet, Claimant: nwClaimant})
		if err == nil || !core.IsDefinite(err) {
			t.Fatalf("error %v, want a definite refusal", err)
		}
		if got := nwClaims(t, h, nwNet); len(got) != 1 {
			t.Errorf("claims %+v, want the claim kept so a repeat can finish", got)
		}
		if effects := h.journal.effects(); len(effects) != 1 || effects[0].State != core.EffectSkipped {
			t.Errorf("effects %+v, want one skipped", effects)
		}
	})
}

func TestNwUndo(t *testing.T) {
	ctx := context.Background()

	t.Run("attach", func(t *testing.T) {
		h, user := nwSetup(t)
		if _, err := nwApplyAttach(t, h, "c1", AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}); err != nil {
			t.Fatal(err)
		}
		def := h.nm().networkAttach()
		effects := h.journal.effects()
		for i := len(effects) - 1; i >= 0; i-- {
			if _, err := def.Handler.Inverse(ctx, h.env("u1"), effects[i]); err != nil {
				t.Fatalf("inverse of effect %d: %v", i, err)
			}
		}
		if nwEndpoint(t, h, user.ID) != nil {
			t.Error("NPM is still on the network after the undo")
		}
		if got := nwClaims(t, h, nwNet); len(got) != 0 {
			t.Errorf("claims %+v, want none after the undo", got)
		}
	})

	t.Run("detach", func(t *testing.T) {
		h, user := nwSetup(t)
		nwSeedAttached(t, h, user, nwClaimant, false)
		if _, err := nwApplyDetach(t, h, "c2", DetachInput{Network: nwNet, Claimant: nwClaimant}); err != nil {
			t.Fatal(err)
		}
		def := h.nm().networkDetach()
		effects := h.journal.effects()
		for i := len(effects) - 1; i >= 0; i-- {
			res, err := def.Handler.Inverse(ctx, h.env("u1"), effects[i])
			if err != nil {
				t.Fatalf("inverse of effect %d: %v", i, err)
			}
			if effects[i].Kind == core.EffectModify && res.Resource != "npm.endpoint:appnet" {
				t.Errorf("inverse of the endpoint names %q, want npm.endpoint:appnet", res.Resource)
			}
		}
		if nwEndpoint(t, h, user.ID) == nil {
			t.Error("NPM was not reconnected")
		}
		want := []claimRow{{Network: nwNet, Claimant: nwClaimant, ChangeID: "c0"}}
		if got := nwClaims(t, h, nwNet); !slices.Equal(got, want) {
			t.Errorf("claims %+v, want the original claim %+v back", got, want)
		}
	})

	t.Run("a claim of another change is not dropped", func(t *testing.T) {
		h, _ := nwSetup(t)
		nwClaim(t, h, nwNet, nwClaimant, "c9", false)
		e := core.Effect{ChangeID: "c1", Kind: core.EffectCreate, Resource: "npm.claim:appnet|expose:app.example.com", Local: true}
		_, err := h.nm().networkAttach().Handler.Inverse(ctx, h.env("u1"), e)
		_ = nwRefused(t, err, errcode.InvalidInput)
		if got := nwClaims(t, h, nwNet); len(got) != 1 {
			t.Errorf("claims %+v, want the other change's claim kept", got)
		}
	})

	t.Run("NPM stays while another change's claim remains", func(t *testing.T) {
		h, user := nwSetup(t)
		if _, err := nwApplyAttach(t, h, "c1", AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}); err != nil {
			t.Fatal(err)
		}
		nwClaim(t, h, nwNet, nwOther, "c2", false)
		endpointEffect := h.journal.effects()[0]
		_, err := h.nm().networkAttach().Handler.Inverse(ctx, h.env("u1"), endpointEffect)
		_ = nwRefused(t, err, errcode.InvalidInput)
		if nwCalls(h, "DisconnectNetwork") != 0 || nwEndpoint(t, h, user.ID) == nil {
			t.Error("the undo disconnected a network another claim needs")
		}
	})
}

func TestNwObserve(t *testing.T) {
	ctx := context.Background()
	h, user := nwSetup(t)
	def := h.nm().networkAttach()
	const res = "npm.claim:appnet|expose:app.example.com"
	creation := core.Effect{ChangeID: "c1", Kind: core.EffectCreate, Resource: "npm.claim:?appnet|expose:app.example.com",
		Recovery: &core.RecoveryID{Kind: "name", Value: "appnet|expose:app.example.com"}, Local: true}

	obs, err := def.Handler.Observe(ctx, h.env("c1"), creation)
	if err != nil || obs.Matches != 0 || string(obs.Current.State) != "null" {
		t.Errorf("creation without a row: %+v (%v), want no match and the null state", obs, err)
	}
	nwClaim(t, h, nwNet, nwClaimant, "c1", true)
	obs, err = def.Handler.Observe(ctx, h.env("c1"), creation)
	if err != nil || obs.Matches != 1 || obs.Resource != res || !obs.Exclusive {
		t.Fatalf("creation with a row: %+v (%v), want one exclusive match of %s", obs, err, res)
	}
	var doc struct {
		Network     string `json:"network"`
		Claimant    string `json:"claimant"`
		Preexisting bool   `json:"preexisting"`
		ChangeID    string `json:"changeId"`
	}
	if err := json.Unmarshal(obs.Current.State, &doc); err != nil || doc.Network != nwNet || doc.Claimant != nwClaimant || !doc.Preexisting || doc.ChangeID != "c1" {
		t.Errorf("claim document %s (%v)", obs.Current.State, err)
	}
	present := core.Effect{ChangeID: "c1", Kind: core.EffectDelete, Resource: res, Local: true}
	again, err := def.Handler.Observe(ctx, h.env("c1"), present)
	if err != nil || again.Matches != 1 || string(again.Current.State) != string(obs.Current.State) {
		t.Errorf("existing claim: %+v (%v), want the same document", again, err)
	}
	if err := h.db.Write(ctx, func(tx *sql.Tx) error { return dropClaim(ctx, tx, nwNet, nwClaimant) }); err != nil {
		t.Fatal(err)
	}
	gone, err := def.Handler.Observe(ctx, h.env("c1"), present)
	if err != nil || gone.Matches != 1 || string(gone.Current.State) != "null" {
		t.Errorf("dropped claim: %+v (%v), want the null state", gone, err)
	}

	endpoint := core.Effect{ChangeID: "c1", Kind: core.EffectModify, Resource: "npm.endpoint:appnet"}
	nwConnect(t, h, user, nil)
	got, err := def.Handler.Observe(ctx, h.env("c1"), endpoint)
	want, wantErr := h.nm().observeEngine(ctx, h.env("c1"), endpoint)
	if err != nil || wantErr != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("endpoint observation %+v (%v), want the Engine observation %+v (%v)", got, err, want, wantErr)
	}
}

func TestNwDefinition(t *testing.T) {
	h := newTHHost(t)
	n := h.nm()
	attach, detach := n.networkAttach(), n.networkDetach()
	for _, tt := range []struct {
		def       core.ActionDef
		name      string
		converges bool
	}{{attach, "npm.network.attach", false}, {detach, "npm.network.detach", true}} {
		s := tt.def.Spec
		if s.Name != tt.name || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
			!slices.Equal(s.LockKeys, []string{"npm"}) || s.Converges != tt.converges {
			t.Errorf("%s spec %+v", tt.name, s)
		}
		if tt.def.Handler.Inverse == nil || tt.def.Handler.Observe == nil {
			t.Errorf("%s: inverse %v observe %v, want both", tt.name, tt.def.Handler.Inverse != nil, tt.def.Handler.Observe != nil)
		}
	}
	if _, err := core.NewRegistry(nwModule{defs: []core.ActionDef{attach, detach}}); err != nil {
		t.Errorf("the registry refuses the actions: %v", err)
	}
	v := schema.NewValidator()
	for _, tt := range []struct {
		def   core.ActionDef
		input string
		field string
	}{
		{attach, `{"network":"appnet","claimant":"c","target":"web","address":"10.9.0.2"}`, ""},
		{attach, `{"network":"appnet","claimant":"c","x":1}`, "/x"},
		{detach, `{"network":"","claimant":"c"}`, ""},
		{detach, `{"claimant":"c","x":1}`, "/x"},
	} {
		err := v.ValidateInput(tt.def, json.RawMessage(tt.input))
		var coded *errcode.Error
		switch {
		case tt.field == "" && err != nil:
			t.Errorf("input %s: %v, want it accepted", tt.input, err)
		case tt.field != "" && (!errors.As(err, &coded) || !slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == tt.field })):
			t.Errorf("input %s: %v, want a refusal naming %s", tt.input, err, tt.field)
		}
	}
}

// TestW10NvUndoRepeatedAfterALostResponse: the undo of a connection or a disconnection that an earlier run already made
// is done, not refused; a refusal while the endpoint is still as it was stays a refusal.
func TestW10NvUndoRepeatedAfterALostResponse(t *testing.T) {
	ctx := context.Background()

	t.Run("a reconnect repeated is done", func(t *testing.T) {
		h, user := nwSetup(t)
		nwSeedAttached(t, h, user, nwClaimant, false)
		if _, err := nwApplyDetach(t, h, "c2", DetachInput{Network: nwNet, Claimant: nwClaimant}); err != nil {
			t.Fatal(err)
		}
		var endpoint core.Effect
		for _, e := range h.journal.effects() {
			if e.Kind == core.EffectModify {
				endpoint = e
			}
		}
		for i := 1; i <= 2; i++ {
			res, err := h.nm().networkDetach().Handler.Inverse(ctx, h.env("u1"), endpoint)
			if err != nil {
				t.Fatalf("inverse %d: %v", i, err)
			}
			if res.Resource != "npm.endpoint:appnet" {
				t.Errorf("inverse %d names %q, want npm.endpoint:appnet", i, res.Resource)
			}
		}
		if nwEndpoint(t, h, user.ID) == nil {
			t.Error("NPM is not on the network")
		}
	})

	t.Run("a disconnect repeated is done", func(t *testing.T) {
		h, user := nwSetup(t)
		if _, err := nwApplyAttach(t, h, "c1", AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}); err != nil {
			t.Fatal(err)
		}
		endpoint := h.journal.effects()[0]
		for i := 1; i <= 2; i++ {
			if _, err := h.nm().networkAttach().Handler.Inverse(ctx, h.env("u1"), endpoint); err != nil {
				t.Fatalf("inverse %d: %v", i, err)
			}
		}
		if nwEndpoint(t, h, user.ID) != nil {
			t.Error("NPM is still on the network")
		}
	})

	t.Run("a refused reconnect stays refused", func(t *testing.T) {
		h, user := nwSetup(t)
		nwSeedAttached(t, h, user, nwClaimant, false)
		if _, err := nwApplyDetach(t, h, "c2", DetachInput{Network: nwNet, Claimant: nwClaimant}); err != nil {
			t.Fatal(err)
		}
		var endpoint core.Effect
		for _, e := range h.journal.effects() {
			if e.Kind == core.EffectModify {
				endpoint = e
			}
		}
		h.docker.Fail("ConnectNetwork", thAPIError(403, "forbidden"))
		_, err := h.nm().networkDetach().Handler.Inverse(ctx, h.env("u1"), endpoint)
		if err == nil || !core.IsDefinite(err) {
			t.Errorf("error %v, want a Definite refusal while NPM is not on the network", err)
		}
		if nwEndpoint(t, h, user.ID) != nil {
			t.Error("NPM joined the network despite the refusal")
		}
	})

	t.Run("a refused disconnect stays refused", func(t *testing.T) {
		h, user := nwSetup(t)
		if _, err := nwApplyAttach(t, h, "c1", AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}); err != nil {
			t.Fatal(err)
		}
		h.docker.Fail("DisconnectNetwork", thAPIError(403, "forbidden"))
		_, err := h.nm().networkAttach().Handler.Inverse(ctx, h.env("u1"), h.journal.effects()[0])
		if err == nil || !core.IsDefinite(err) {
			t.Errorf("error %v, want a Definite refusal while NPM is on the network", err)
		}
		if nwEndpoint(t, h, user.ID) == nil {
			t.Error("NPM left the network despite the refusal")
		}
	})
}

// TestW10NvAttachFollowsEveryClaimsPreexistingMark: when the claims on a network disagree on whether NPM was there
// before the first claim, a new claim takes the safe answer, that it was, so a later detach never removes NPM from a
// network it may have been on.
func TestW10NvAttachFollowsEveryClaimsPreexistingMark(t *testing.T) {
	h, user := nwSetup(t)
	nwConnect(t, h, user, nil)
	nwClaim(t, h, nwNet, "a-claimant", "c0", false)
	nwClaim(t, h, nwNet, "b-claimant", "c0", true)

	out, err := nwApplyAttach(t, h, "c1", AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Preexisting {
		t.Errorf("attachment %+v, want Preexisting, as the claim of b-claimant says", out)
	}
	for _, c := range nwClaims(t, h, nwNet) {
		if c.Claimant == nwClaimant && !c.Preexisting {
			t.Errorf("the new claim %+v was recorded as not preexisting", c)
		}
	}
}

// w12aSubnet gives appnet the subnet 10.9.0.0/24 with the gateway 10.9.0.1, as the Engine reports a bridge network.
func w12aSubnet(_ *testing.T, h *thHost, _ dockerapi.Network) {
	h.docker.putNetwork(thNetwork(nwNet, thSubnet("10.9.0.0/24", "10.9.0.1")))
}

// w12aSeed adds to NPM a proxy host that forwards to host:port, with the custom locations.
func w12aSeed(h *thHost, host string, port int, locations ...npmapi.Location) {
	h.npm.SeedProxyHost(npmapi.ProxyHost{DomainNames: []string{"x.example.com"}, ForwardScheme: "http", ForwardHost: host,
		ForwardPort: port, Locations: locations})
}

// w12aForwardsTo gives appnet its subnet and adds a proxy host that forwards to host:port.
func w12aForwardsTo(host string, port int) nwSetupFunc {
	return func(t *testing.T, h *thHost, user dockerapi.Network) {
		w12aSubnet(t, h, user)
		w12aSeed(h, host, port)
	}
}

// TestW12aAttachChecksTheProxyHostsAgainAtApply: a proxy host added after the plan, forwarding to the address the attach
// asks for, refuses the apply before NPM joins the network.
func TestW12aAttachChecksTheProxyHostsAgainAtApply(t *testing.T) {
	ctx := context.Background()
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	in := AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web", Address: netip.MustParseAddr("10.9.0.7")}
	if _, err := h.nm().networkAttach().Handler.Plan(ctx, h.env(""), nwJSON(t, in)); err != nil {
		t.Fatal(err)
	}
	w12aSeed(h, "10.9.0.7", 3000)
	_, err := nwApplyAttach(t, h, "c1", in)
	coded := nwRefused(t, err, errcode.NPMReserved)
	if want := "proxy host 1 forwards to 10.9.0.7, an address darkpanel-npm could take on network appnet"; coded.Detail != want {
		t.Errorf("detail %q, want %q", coded.Detail, want)
	}
	if nwCalls(h, "ConnectNetwork") != 0 || nwEndpoint(t, h, user.ID) != nil || len(nwClaims(t, h, nwNet)) != 0 || len(h.journal.effects()) != 0 {
		t.Error("a refused apply connected NPM, recorded a claim or journaled an effect")
	}
}

// TestW12aAttachResolvesForwardHostNames: a forward host that is a name counts with each address it resolves to; a
// name that does not resolve adds nothing.
func TestW12aAttachResolvesForwardHostNames(t *testing.T) {
	ctx := context.Background()
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	w12aSeed(h, "app.example.net", 3000)
	in := nwJSON(t, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})
	n := h.nm()
	n.d.resolve = v1r09Resolver("203.0.113.9", "10.9.0.7")
	_, err := n.networkAttach().Handler.Plan(ctx, h.env(""), in)
	coded := nwRefused(t, err, errcode.NPMReserved)
	if want := "proxy host 1 forwards to app.example.net, which resolves to 10.9.0.7, an address darkpanel-npm could take on network appnet"; coded.Detail != want {
		t.Errorf("detail %q, want %q", coded.Detail, want)
	}
	n.d.resolve = func(context.Context, string) ([]netip.Addr, error) {
		return nil, errors.New("lookup app.example.net: no such host")
	}
	if _, err := n.networkAttach().Handler.Plan(ctx, h.env(""), in); err != nil {
		t.Errorf("plan with a name that does not resolve: %v, want it to pass", err)
	}
}

// TestW12aUndoOfADetachRefusesAForwardedAddress: the undo of a detach reconnects NPM with an address the Engine picks,
// so it is refused while a proxy host forwards to a free address of the network.
func TestW12aUndoOfADetachRefusesAForwardedAddress(t *testing.T) {
	ctx := context.Background()
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	nwSeedAttached(t, h, user, nwClaimant, false)
	if _, err := nwApplyDetach(t, h, "c2", DetachInput{Network: nwNet, Claimant: nwClaimant}); err != nil {
		t.Fatal(err)
	}
	var endpoint core.Effect
	for _, e := range h.journal.effects() {
		if e.Kind == core.EffectModify {
			endpoint = e
		}
	}
	w12aSeed(h, "10.9.0.7", 3000)
	_, err := h.nm().networkDetach().Handler.Inverse(ctx, h.env("u1"), endpoint)
	coded := nwRefused(t, err, errcode.NPMReserved)
	if want := "proxy host 1 forwards to 10.9.0.7, an address darkpanel-npm could take on network appnet"; coded.Detail != want {
		t.Errorf("detail %q, want %q", coded.Detail, want)
	}
	if nwEndpoint(t, h, user.ID) != nil {
		t.Error("the refused undo reconnected NPM")
	}
	// A repeat after a reconnect whose answer was lost finds NPM on the network and is done, whatever proxy hosts
	// forward to.
	if err := h.docker.ConnectNetwork(ctx, nwNet, containerName, nil); err != nil {
		t.Fatal(err)
	}
	res, err := h.nm().networkDetach().Handler.Inverse(ctx, h.env("u1"), endpoint)
	if err != nil {
		t.Fatalf("the repeated undo with NPM on the network: %v, want done", err)
	}
	if want := resourceOf(nounEndpoint, nwNet); res.Resource != want {
		t.Errorf("the repeated undo returned resource %q, want %q", res.Resource, want)
	}
}
