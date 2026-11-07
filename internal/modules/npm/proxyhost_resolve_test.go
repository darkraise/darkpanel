package npm

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// v1r09Resolver answers every name with answers.
func v1r09Resolver(answers ...string) func(context.Context, string) ([]netip.Addr, error) {
	out := make([]netip.Addr, len(answers))
	for i, a := range answers {
		out[i] = netip.MustParseAddr(a)
	}
	return func(context.Context, string) ([]netip.Addr, error) { return out, nil }
}

// v1r09Field asserts that err names the path among its invalid fields.
func v1r09Field(t *testing.T, err error, path string) {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || !slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == path }) {
		t.Errorf("error %v, want an invalid field at %s", err, path)
	}
}

func TestV1r09AddRefusesANameThatResolvesToAReservedAddress(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		answers []string
		port    int
		parent  string
		refuse  bool
	}{
		{"the loopback", []string{"127.0.0.1"}, 81, "", true},
		{"the loopback on another port", []string{"127.0.0.1"}, 8080, "", true},
		{"an IPv4-mapped loopback", []string{"::ffff:127.0.0.1"}, 81, "", true},
		{"the IPv6 loopback", []string{"::1"}, 81, "", true},
		{"NPM's address on its API port", []string{"172.18.0.2"}, 3000, "", true},
		{"the panel gateway", []string{"172.31.255.1"}, 9443, "", true},
		{"a reserved second answer", []string{"203.0.113.7", "127.0.0.1"}, 8080, "", true},
		{"a public answer", []string{"203.0.113.7"}, 8080, "", false},
		{"the loopback from the admin exposure", []string{"127.0.0.1"}, 81, paAdmin, false},
		{"the panel gateway from the panel exposure", []string{"172.31.255.1"}, 9443, paPanel, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, n := paHost(t)
			n.d.resolve = v1r09Resolver(tc.answers...)
			in := paInput("x.example.com")
			in.ForwardHost, in.ForwardPort = "web.example.net", tc.port

			_, planErr := n.proxyhostAdd().Handler.Plan(ctx, paEnv(h, "", tc.parent), paJSON(t, in))
			_, applyErr := paApply(t, h, n, "c1", tc.parent, in)
			if !tc.refuse {
				if planErr != nil || applyErr != nil {
					t.Fatalf("plan error %v, apply error %v; want both to pass", planErr, applyErr)
				}
				return
			}
			_ = paRefused(t, planErr, errcode.NPMReserved)
			_ = paRefused(t, applyErr, errcode.NPMReserved)
			if len(h.npm.ProxyHosts()) != 0 || len(h.journal.effects()) != 0 {
				t.Error("a refused apply created a proxy host or journaled an effect")
			}
		})
	}
}

func TestV1r09AddRefusesANameWithNoAnswer(t *testing.T) {
	ctx := context.Background()
	for name, resolve := range map[string]func(context.Context, string) ([]netip.Addr, error){
		"a lookup error": func(context.Context, string) ([]netip.Addr, error) {
			return nil, errors.New("lookup web.example.net: no such host")
		},
		"an empty answer": func(context.Context, string) ([]netip.Addr, error) { return []netip.Addr{}, nil },
	} {
		t.Run(name, func(t *testing.T) {
			h, n := paHost(t)
			n.d.resolve = resolve
			in := paInput("x.example.com")
			in.ForwardHost = "web.example.net"

			_, err := n.proxyhostAdd().Handler.Plan(ctx, h.env(""), paJSON(t, in))
			_ = paRefused(t, err, errcode.InvalidInput)
			v1r09Field(t, err, "/forwardHost")
			_, err = paApply(t, h, n, "c1", "", in)
			_ = paRefused(t, err, errcode.InvalidInput)
			v1r09Field(t, err, "/forwardHost")
			if len(h.npm.ProxyHosts()) != 0 || len(h.journal.effects()) != 0 {
				t.Error("a refused apply created a proxy host or journaled an effect")
			}
		})
	}
}

func TestV1r09AddResolvesAgainAtApply(t *testing.T) {
	ctx := context.Background()
	h, n := paHost(t)
	calls := 0
	answers := v1r09Resolver("203.0.113.7")
	n.d.resolve = func(ctx context.Context, host string) ([]netip.Addr, error) {
		calls++
		return answers(ctx, host)
	}
	in := paInput("x.example.com")
	in.ForwardHost = "web.example.net"

	if _, err := n.proxyhostAdd().Handler.Plan(ctx, h.env(""), paJSON(t, in)); err != nil {
		t.Fatal(err)
	}
	planned := calls
	if planned == 0 {
		t.Fatal("the plan did not resolve the forward host")
	}
	answers = v1r09Resolver("127.0.0.1")
	_, err := paApply(t, h, n, "c1", "", in)
	_ = paRefused(t, err, errcode.NPMReserved)
	if calls == planned {
		t.Error("the apply did not resolve the forward host again")
	}
	if len(h.npm.ProxyHosts()) != 0 || len(h.journal.effects()) != 0 {
		t.Error("an apply of a name re-pointed at the loopback created a proxy host or journaled an effect")
	}
}

func TestV1r09AddPlanHashCoversTheSortedAnswerSet(t *testing.T) {
	ctx := context.Background()
	h, n := paHost(t)
	hash := func(host string, answers ...string) string {
		n.d.resolve = v1r09Resolver(answers...)
		in := paInput("x.example.com")
		in.ForwardHost = host
		out, err := n.proxyhostAdd().Handler.Plan(ctx, h.env(""), paJSON(t, in))
		if err != nil {
			t.Fatal(err)
		}
		return out.StateHash
	}
	first := hash("web.example.net", "203.0.113.7")
	if again := hash("web.example.net", "203.0.113.7"); again != first {
		t.Errorf("the same answers hash %q, want %q", again, first)
	}
	if other := hash("web.example.net", "203.0.113.8"); other == first {
		t.Error("another answer left the plan hash unchanged")
	}
	both := hash("web.example.net", "203.0.113.7", "203.0.113.8")
	if both == first {
		t.Error("an extra answer left the plan hash unchanged")
	}
	if reversed := hash("web.example.net", "203.0.113.8", "203.0.113.7"); reversed != both {
		t.Errorf("the answers in another order hash %q, want %q", reversed, both)
	}
	if doubled := hash("web.example.net", "203.0.113.7", "203.0.113.7"); doubled != first {
		t.Errorf("a repeated answer hashes %q, want %q", doubled, first)
	}
}

func TestV1r09AddNeverResolvesAnAddressLiteral(t *testing.T) {
	ctx := context.Background()
	h, n := paHost(t)
	n.d.resolve = func(_ context.Context, host string) ([]netip.Addr, error) {
		t.Errorf("resolved %q, which nginx reads as an address", host)
		return nil, errors.New("unexpected lookup")
	}
	for _, host := range []string{"10.0.0.5", "127.1"} {
		in := paInput("x.example.com")
		in.ForwardHost, in.ForwardPort = host, 8080
		if _, err := n.proxyhostAdd().Handler.Plan(ctx, paEnv(h, "", paAdmin), paJSON(t, in)); err != nil {
			t.Errorf("plan with literal host %q: %v", host, err)
		}
	}
}

func TestV1r09UpdateResolvesForwardHosts(t *testing.T) {
	ctx := context.Background()
	input := func(host string) ProxyHostInput {
		return puInput(func(in *ProxyHostInput) { in.ForwardHost = host })
	}

	t.Run("a reserved answer is refused at plan and at apply", func(t *testing.T) {
		h, host := puSeed(t, puInput())
		h.addChange("c1", "npm.proxyhost.update")
		n := h.nm()
		n.d.resolve = v1r09Resolver("127.0.0.1")
		raw := puJSON(t, ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: input("web.example.net")})
		before := puHosts(t, h)

		_, err := n.proxyhostUpdate().Handler.Plan(ctx, h.env(""), raw)
		puCoded(t, err, errcode.NPMReserved)
		_, err = n.proxyhostUpdate().Handler.Apply(ctx, h.env("c1"), raw)
		puCoded(t, err, errcode.NPMReserved)
		if !core.IsDefinite(err) {
			t.Errorf("apply error %v is not definite, though nothing was committed", err)
		}
		if got := puHosts(t, h); !reflect.DeepEqual(got, before) || len(puEffects(t, h)) != 0 {
			t.Errorf("a refusal changed NPM to %+v or journaled %+v", got, puEffects(t, h))
		}
	})

	t.Run("a name with no answer is invalid input on forwardHost", func(t *testing.T) {
		h, host := puSeed(t, puInput())
		h.addChange("c1", "npm.proxyhost.update")
		n := h.nm()
		n.d.resolve = func(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("no such host") }
		raw := puJSON(t, ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: input("web.example.net")})

		_, err := n.proxyhostUpdate().Handler.Plan(ctx, h.env(""), raw)
		puCoded(t, err, errcode.InvalidInput)
		v1r09Field(t, err, "/forwardHost")
		_, err = n.proxyhostUpdate().Handler.Apply(ctx, h.env("c1"), raw)
		puCoded(t, err, errcode.InvalidInput)
		v1r09Field(t, err, "/forwardHost")
	})

	t.Run("the plan hash covers the answer set", func(t *testing.T) {
		h, host := puSeed(t, puInput())
		n := h.nm()
		raw := puJSON(t, ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: input("web.example.net")})
		hash := func(answers ...string) string {
			n.d.resolve = v1r09Resolver(answers...)
			out, err := n.proxyhostUpdate().Handler.Plan(ctx, h.env(""), raw)
			if err != nil {
				t.Fatal(err)
			}
			return out.StateHash
		}
		first := hash("203.0.113.7")
		if again := hash("203.0.113.7"); again != first {
			t.Errorf("the same answers hash %q, want %q", again, first)
		}
		if other := hash("203.0.113.8"); other == first {
			t.Error("another answer left the plan hash unchanged")
		}
		if both, reversed := hash("203.0.113.7", "203.0.113.8"), hash("203.0.113.8", "203.0.113.7"); both != reversed {
			t.Errorf("the answers in another order hash %q, want %q", reversed, both)
		}
	})
}

// v1r09Container puts a running container on NPM's network with the address and the aliases.
func v1r09Container(h *thHost, name, addr string, aliases ...string) {
	h.docker.putContainer(thContainer(name, thRunning, thEndpoint(networkName, addr), func(c *dockerapi.ContainerJSON) {
		c.NetworkSettings.Networks[networkName].Aliases = aliases
	}))
}

// v1r09NoLookup fails the test when the host's resolver is asked.
func v1r09NoLookup(t *testing.T) func(context.Context, string) ([]netip.Addr, error) {
	return func(_ context.Context, host string) ([]netip.Addr, error) {
		t.Errorf("resolved %q with the host's resolver, though Docker answers it on NPM's network", host)
		return nil, errors.New("unexpected lookup")
	}
}

func TestV1r09DockerAnswersNamesOnNPMNetworks(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, host string
		port       int
		refuse     bool
	}{
		{"a container name", "shop", 8080, false},
		{"a container name in capitals with a trailing dot", "SHOP.", 8080, false},
		{"a network alias", "shop-alias", 8080, false},
		{"a container whose address is in the panel network", "panelweb", 9443, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, n := paHost(t)
			v1r09Container(h, "shop", "172.18.0.5", "shop-alias")
			v1r09Container(h, "panelweb", "172.31.255.2")
			n.d.resolve = v1r09NoLookup(t)
			in := paInput("x.example.com")
			in.ForwardHost, in.ForwardPort = tc.host, tc.port

			_, planErr := n.proxyhostAdd().Handler.Plan(ctx, h.env(""), paJSON(t, in))
			_, applyErr := paApply(t, h, n, "c1", "", in)
			if tc.refuse {
				_ = paRefused(t, planErr, errcode.NPMReserved)
				_ = paRefused(t, applyErr, errcode.NPMReserved)
				if len(h.npm.ProxyHosts()) != 0 {
					t.Error("a refused apply created a proxy host")
				}
				return
			}
			if planErr != nil || applyErr != nil {
				t.Fatalf("plan error %v, apply error %v; want both to pass", planErr, applyErr)
			}
		})
	}

	t.Run("the plan hash covers the container's address", func(t *testing.T) {
		h, n := paHost(t)
		n.d.resolve = v1r09NoLookup(t)
		in := paInput("x.example.com")
		in.ForwardHost = "shop"
		hash := func(addr string) string {
			v1r09Container(h, "shop", addr)
			out, err := n.proxyhostAdd().Handler.Plan(ctx, h.env(""), paJSON(t, in))
			if err != nil {
				t.Fatal(err)
			}
			return out.StateHash
		}
		if first, moved := hash("172.18.0.5"), hash("172.18.0.6"); first == moved {
			t.Error("a container that moved to another address left the plan hash unchanged")
		}
	})

	t.Run("the short ID and a DNS name of the endpoint", func(t *testing.T) {
		h, n := paHost(t)
		shop := thContainer("shop", thRunning, thEndpoint(networkName, "172.18.0.5"), func(c *dockerapi.ContainerJSON) {
			c.NetworkSettings.Networks[networkName].DNSNames = []string{"shop", "shop-dns"}
		})
		h.docker.putContainer(shop)
		n.d.resolve = v1r09NoLookup(t)
		for _, host := range []string{shop.ID[:12], "shop-dns"} {
			in := paInput("x.example.com")
			in.ForwardHost = host
			if _, err := n.proxyhostAdd().Handler.Plan(ctx, h.env(""), paJSON(t, in)); err != nil {
				t.Errorf("plan with forward host %q: %v", host, err)
			}
		}
	})

	t.Run("a container on the default bridge goes to the host's resolver", func(t *testing.T) {
		h, n := paHost(t)
		h.docker.putNetwork(thNetwork("bridge"))
		npmC, ok := h.docker.container(containerName)
		if !ok {
			t.Fatal("paHost left no NPM container")
		}
		npmC.NetworkSettings.Networks["bridge"] = &dockerapi.EndpointSettings{IPAddress: netip.MustParseAddr("172.17.0.2")}
		h.docker.putContainer(npmC)
		h.docker.putContainer(thContainer("bridged", thRunning, thEndpoint("bridge", "172.17.0.9")))
		asked := ""
		n.d.resolve = func(_ context.Context, host string) ([]netip.Addr, error) {
			asked = host
			return []netip.Addr{netip.MustParseAddr("203.0.113.7")}, nil
		}
		in := paInput("x.example.com")
		in.ForwardHost = "bridged"
		if _, err := n.proxyhostAdd().Handler.Plan(ctx, h.env(""), paJSON(t, in)); err != nil {
			t.Fatal(err)
		}
		if asked != "bridged" {
			t.Errorf("the host's resolver was asked %q, want bridged: Docker's DNS does not serve the default bridge", asked)
		}
	})

	t.Run("a container off NPM's networks goes to the host's resolver", func(t *testing.T) {
		h, n := paHost(t)
		h.docker.putContainer(thContainer("lonely", thRunning, thEndpoint("elsewhere", "10.9.0.5")))
		asked := ""
		n.d.resolve = func(_ context.Context, host string) ([]netip.Addr, error) {
			asked = host
			return []netip.Addr{netip.MustParseAddr("203.0.113.7")}, nil
		}
		in := paInput("x.example.com")
		in.ForwardHost = "lonely"
		if _, err := n.proxyhostAdd().Handler.Plan(ctx, h.env(""), paJSON(t, in)); err != nil {
			t.Fatal(err)
		}
		if asked != "lonely" {
			t.Errorf("the host's resolver was asked %q, want lonely", asked)
		}
	})
}

func TestV1r09UndoesCheckTheForwardHostTheyRestore(t *testing.T) {
	ctx := context.Background()
	const name = "web.example.net"
	for _, tc := range []struct {
		label   string
		resolve func(context.Context, string) ([]netip.Addr, error)
		code    errcode.Code // "" when the undo goes through
	}{
		{"a public answer", v1r09Resolver("203.0.113.7"), ""},
		{"a name re-pointed at the loopback", v1r09Resolver("127.0.0.1"), errcode.NPMReserved},
		{"a name with no answer", func(context.Context, string) ([]netip.Addr, error) {
			return nil, errors.New("no such host")
		}, errcode.InvalidInput},
	} {
		t.Run("remove: "+tc.label, func(t *testing.T) {
			h, host := puSeed(t, puInput(func(in *ProxyHostInput) { in.ForwardHost = name }))
			h.addChange("c1", "npm.proxyhost.remove")
			n := h.nm()
			if _, err := n.proxyhostRemove().Handler.Apply(ctx, h.env("c1"), puJSON(t, ProxyHostRemoveInput{ID: host.ID})); err != nil {
				t.Fatal(err)
			}
			e := puEffects(t, h)[0]
			e.UndoTo = &core.StateDoc{State: e.Before.State}
			n.d.resolve = tc.resolve

			_, err := n.proxyhostRemove().Handler.Inverse(ctx, h.env("c1"), e)
			hosts := puHosts(t, h)
			if tc.code == "" {
				if err != nil || len(hosts) != 1 || hosts[0].ForwardHost != name {
					t.Fatalf("inverse error %v, NPM holds %+v; want the host back with forward host %s", err, hosts, name)
				}
				return
			}
			puCoded(t, err, tc.code)
			if !core.IsDefinite(err) || len(hosts) != 0 {
				t.Errorf("error %v (definite %v), hosts %+v; want a definite refusal that created nothing", err, core.IsDefinite(err), hosts)
			}
		})
		t.Run("update: "+tc.label, func(t *testing.T) {
			h, host := puSeed(t, puInput(func(in *ProxyHostInput) { in.ForwardHost = name }))
			h.addChange("c1", "npm.proxyhost.update")
			n := h.nm()
			next := puInput(func(in *ProxyHostInput) { in.ForwardHost = "other" })
			raw := puJSON(t, ProxyHostUpdateInput{ID: host.ID, ProxyHostInput: next})
			if _, err := n.proxyhostUpdate().Handler.Apply(ctx, h.env("c1"), raw); err != nil {
				t.Fatal(err)
			}
			e := puEffects(t, h)[0]
			e.UndoTo = &core.StateDoc{State: e.Before.State}
			n.d.resolve = tc.resolve

			_, err := n.proxyhostUpdate().Handler.Inverse(ctx, h.env("c1"), e)
			hosts := puHosts(t, h)
			if tc.code == "" {
				if err != nil || len(hosts) != 1 || hosts[0].ForwardHost != name {
					t.Fatalf("inverse error %v, NPM holds %+v; want the host back at forward host %s", err, hosts, name)
				}
				return
			}
			puCoded(t, err, tc.code)
			if !core.IsDefinite(err) || len(hosts) != 1 || hosts[0].ForwardHost != "other" {
				t.Errorf("error %v (definite %v), hosts %+v; want a definite refusal that left forward host other", err, core.IsDefinite(err), hosts)
			}
		})
	}
}

func TestV1r09ProductionResolverIsAbsentInATestBinary(t *testing.T) {
	got := reflect.ValueOf(productionNM().d.resolve)
	if !got.IsValid() || got.IsNil() {
		t.Fatal("productionNM wires no resolver")
	}
	if want := reflect.ValueOf(absentResolve); got.Pointer() != want.Pointer() {
		t.Error("a test binary's production module would resolve names through the host's resolver")
	}
}
