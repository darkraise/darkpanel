package panel

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

const (
	// networkLockKey serialises the panel's actions on a host.
	networkLockKey = "panel"
	// networkNoun is the resource noun of the panel network; a resource is "panel.network:<network id>", a creation
	// not yet made "panel.network:?darkpanel-panel".
	networkNoun = "panel.network"
	// networkCommitWait is how long the Engine may still commit a network call the agent lost track of.
	networkCommitWait = 2 * time.Minute
	// networkBlockBits is the prefix length of the panel network's subnet.
	networkBlockBits = 29
	// networkRouteMinBits is the shortest host route the subnet choice avoids. A shorter route stands in for a default
	// route (OpenVPN's def1 splits 0.0.0.0/0 into two /1s) and names no network of hosts a /29 could shadow.
	networkRouteMinBits = 8
)

// networkResource is the effect resource of the panel network that identity names.
func networkResource(identity string) string { return networkNoun + ":" + identity }

// networkNull is the state of a network that does not exist.
func networkNull() core.StateDoc { return core.StateDoc{State: json.RawMessage("null")} }

// networkClient builds a client over the Engine socket for one call.
func networkClient(d agentDeps) (panelDocker, error) {
	docker, err := d.docker()
	if err != nil {
		return nil, core.Definite(fmt.Errorf("panel: connect to the Docker Engine: %w", err))
	}
	return docker, nil
}

// networkEngineCoded gives an unreachable Engine its code, so the caller sees docker.engine_unavailable.
func networkEngineCoded(err error) error {
	if errors.Is(err, dockerapi.ErrUnavailable) {
		return errcode.Wrap(errcode.DockerEngineUnavailable, err, "the Docker Engine is not reachable")
	}
	return err
}

// networkReadFailed is a failed Engine read made before anything changed.
func networkReadFailed(what string, err error) error {
	return core.Definite(fmt.Errorf("panel: %s: %w", what, networkEngineCoded(err)))
}

// networkRefused reports whether err is an Engine answer that proves the call committed nothing: a 4xx.
func networkRefused(err error) bool {
	return dockerapi.IsClientError(err)
}

// networkHash is the hex SHA-256 of the JSON document of v.
func networkHash(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("panel: hash the plan state: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// networkFailed settles the intended effect seq after its call failed with err: a refusal skips it and is Definite;
// any other failure leaves it intended for the engine to settle through Observe.
func networkFailed(ctx context.Context, env core.Env, seq int, resource string, err error) error {
	if !networkRefused(err) {
		return err
	}
	if skipErr := env.Effects.Skipped(ctx, seq); skipErr != nil {
		return errors.Join(err, fmt.Errorf("panel: journal %s: %w", resource, skipErr))
	}
	return core.Definite(err)
}

// networkDoc renders a network as InspectNetwork reports it, because only the inspect answer lists the containers
// attached to it; ok is false when the network does not exist.
func networkDoc(ctx context.Context, docker panelDocker, id string) (doc json.RawMessage, ok bool, err error) {
	n, err := docker.InspectNetwork(ctx, id)
	if dockerapi.IsNotFound(err) {
		return networkNull().State, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("panel: inspect network %s: %w", id, networkEngineCoded(err))
	}
	doc, err = dstate.NetworkCreated(&n)
	if err != nil {
		return nil, false, fmt.Errorf("panel: render network %s: %w", id, err)
	}
	return doc, true, nil
}

// networkObserve reads the panel network of one effect: a made network by its id, a creation's network by its exact
// name.
func networkObserve(ctx context.Context, d agentDeps, e core.Effect) (core.Observation, error) {
	identity, ok := strings.CutPrefix(e.Resource, networkNoun+":")
	name, creation := strings.CutPrefix(identity, "?")
	if !ok || name == "" {
		return core.Observation{}, fmt.Errorf("panel: %q is not a panel network resource", e.Resource)
	}
	docker, err := networkClient(d)
	if err != nil {
		return core.Observation{}, err
	}
	if !creation {
		doc, _, err := networkDoc(ctx, docker, name)
		if err != nil {
			return core.Observation{}, err
		}
		return core.Observation{Current: core.StateDoc{State: doc}, Matches: 1, Exclusive: true}, nil
	}
	list, err := docker.ListNetworks(ctx, dockerapi.Filters{"name": {name}})
	if err != nil {
		return core.Observation{}, fmt.Errorf("panel: list networks named %s: %w", name, networkEngineCoded(err))
	}
	var candidates []core.Candidate
	for _, n := range list {
		// The Engine's name filter also matches networks whose name only contains name.
		if n.Name != name {
			continue
		}
		doc, ok, err := networkDoc(ctx, docker, n.ID)
		if err != nil {
			return core.Observation{}, err
		}
		if ok {
			candidates = append(candidates, core.Candidate{Resource: networkResource(n.ID), State: core.StateDoc{State: doc}})
		}
	}
	slices.SortFunc(candidates, func(a, b core.Candidate) int { return strings.Compare(a.Resource, b.Resource) })
	switch len(candidates) {
	case 0:
		return core.Observation{Current: networkNull()}, nil
	case 1:
		return core.Observation{Current: candidates[0].State, Resource: candidates[0].Resource, Matches: 1, Exclusive: true}, nil
	default:
		return core.Observation{Current: networkNull(), Matches: len(candidates), Candidates: candidates}, nil
	}
}

// networkCreateAction declares panel.network.create: it creates the bridge network between the proxy and the panel's
// listener on a free /29 of PanelRange.
func networkCreateAction(d agentDeps) core.ActionDef {
	return core.DefineMutating[NetworkCreateInput, NetworkView](core.ActionSpec{
		Name:     ActionNetCreate,
		Summary:  "Create the panel network between the proxy and the panel's listener on a free /29 of 172.31.255.0/24.",
		Version:  1,
		Class:    core.ClassCompensable,
		Side:     core.SideAgent,
		LockKeys: []string{networkLockKey},
		MinScope: core.ScopeAdmin,
	}, networkCreateHandler{d: d})
}

// networkCreateHandler is the handler of panel.network.create.
type networkCreateHandler struct{ d agentDeps }

// networkChoice is the subnet the panel network takes and its two addresses.
type networkChoice struct {
	subnet         netip.Prefix
	gateway, proxy netip.Addr
}

// networkHostTaken returns the IPv4 prefixes the host already uses: the destination of every route of /8 or longer in
// any table and of any type (a Tailscale subnet route lives in table 52, a blackhole route holds its range too),
// followed by the address of every interface, up or down.
func networkHostTaken(d agentDeps) ([]netip.Prefix, error) {
	routes, err := d.routes(netstat.IPv4)
	if err != nil {
		return nil, core.Definite(fmt.Errorf("panel: read the host's IPv4 routes: %w", err))
	}
	ifaces, err := d.interfaces()
	if err != nil {
		return nil, core.Definite(fmt.Errorf("panel: read the host's interface addresses: %w", err))
	}
	var taken []netip.Prefix
	for _, r := range routes {
		if r.Dst.IsValid() && r.Dst.Addr().Is4() && r.Dst.Bits() >= networkRouteMinBits {
			taken = append(taken, r.Dst)
		}
	}
	for _, ifc := range ifaces {
		for _, a := range ifc.Addrs {
			if a.IsValid() && a.Addr().Is4() {
				taken = append(taken, a)
			}
		}
	}
	return taken, nil
}

// networkChoose refuses an existing panel network and picks the highest /29 of PanelRange that overlaps no IPv4 subnet
// of any network on the host, no IPv4 route of the host of /8 or longer and no IPv4 address of its interfaces.
func networkChoose(ctx context.Context, d agentDeps, docker panelDocker) (networkChoice, error) {
	list, err := docker.ListNetworks(ctx, nil)
	if err != nil {
		return networkChoice{}, networkReadFailed("list networks", err)
	}
	var taken []netip.Prefix
	for _, n := range list {
		if n.Name == NetworkName {
			return networkChoice{}, core.Definite(errcode.New(errcode.PanelAlreadyExposed,
				fmt.Sprintf("the network %s already exists on this host", NetworkName)))
		}
		for _, c := range n.IPAM.Config {
			if c.Subnet.IsValid() && c.Subnet.Addr().Is4() {
				taken = append(taken, c.Subnet)
			}
		}
	}
	host, err := networkHostTaken(d)
	if err != nil {
		return networkChoice{}, err
	}
	taken = append(taken, host...)
	base := PanelRange.Masked().Addr().As4()
	first := binary.BigEndian.Uint32(base[:])
	size := uint32(1) << (32 - networkBlockBits)
	for i := (uint32(1) << (networkBlockBits - PanelRange.Bits())); i > 0; i-- {
		var a [4]byte
		binary.BigEndian.PutUint32(a[:], first+(i-1)*size)
		block := netip.PrefixFrom(netip.AddrFrom4(a), networkBlockBits)
		if slices.ContainsFunc(taken, block.Overlaps) {
			continue
		}
		gateway := block.Addr().Next()
		return networkChoice{subnet: block, gateway: gateway, proxy: gateway.Next()}, nil
	}
	var inRange []netip.Prefix
	for _, p := range taken {
		if p.Overlaps(PanelRange) {
			inRange = append(inRange, p.Masked())
		}
	}
	slices.SortFunc(inRange, func(a, b netip.Prefix) int {
		if c := a.Addr().Compare(b.Addr()); c != 0 {
			return c
		}
		return a.Bits() - b.Bits()
	})
	inRange = slices.Compact(inRange)
	names := make([]string, len(inRange))
	for i, p := range inRange {
		names[i] = p.String()
	}
	return networkChoice{}, core.Definite(fmt.Errorf(
		"panel: no free /%d is left in %s: every one overlaps a Docker network's subnet or a host route or address (%s)",
		networkBlockBits, PanelRange, strings.Join(names, ", ")))
}

// Plan reports the subnet the network would take; the hash covers only that subnet.
func (h networkCreateHandler) Plan(ctx context.Context, _ core.Env, _ NetworkCreateInput) (core.PlanOutcome, error) {
	docker, err := networkClient(h.d)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	c, err := networkChoose(ctx, h.d, docker)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	hash, err := networkHash(map[string]string{"subnet": c.subnet.String()})
	if err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{
		{Op: "+", Text: "network " + NetworkName + " " + c.subnet.String()},
		{Op: "+", Text: "gateway " + c.gateway.String() + ", proxy " + c.proxy.String()},
	}
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{}, StateHash: hash}, nil
}

// Apply journals the creation, creates the network and records it made with its state.
func (h networkCreateHandler) Apply(ctx context.Context, env core.Env, _ NetworkCreateInput) (NetworkView, error) {
	docker, err := networkClient(h.d)
	if err != nil {
		return NetworkView{}, err
	}
	c, err := networkChoose(ctx, h.d, docker)
	if err != nil {
		return NetworkView{}, err
	}
	e := core.Effect{
		Kind:       core.EffectCreate,
		Resource:   networkResource("?" + NetworkName),
		Recovery:   &core.RecoveryID{Kind: "name", Value: NetworkName},
		CommitWait: networkCommitWait,
		Before:     networkNull(),
	}
	seq, err := env.Effects.Intend(ctx, e, nil)
	if err != nil {
		return NetworkView{}, fmt.Errorf("panel: journal %s: %w", e.Resource, err)
	}
	ipv6 := false
	id, err := docker.CreateNetwork(ctx, dockerapi.NetworkCreate{
		Name:       NetworkName,
		Driver:     "bridge",
		EnableIPv6: &ipv6,
		IPAM:       &dockerapi.IPAM{Config: []dockerapi.IPAMConfig{{Subnet: c.subnet, Gateway: c.gateway}}},
		Options:    map[string]string{BridgeOption: BridgeName},
		Labels:     map[string]string{core.LabelModule: ID},
	})
	if err != nil {
		return NetworkView{}, networkFailed(ctx, env, seq, e.Resource,
			fmt.Errorf("panel: create network %s: %w", NetworkName, networkEngineCoded(err)))
	}
	n, err := docker.InspectNetwork(ctx, id)
	if err != nil {
		return NetworkView{}, fmt.Errorf("panel: network %s is unsettled: inspect: %w", id, networkEngineCoded(err))
	}
	doc, err := dstate.NetworkCreated(&n)
	if err != nil {
		return NetworkView{}, fmt.Errorf("panel: render network %s: %w", id, err)
	}
	resource := networkResource(id)
	if err := env.Effects.Made(ctx, seq, resource, core.StateDoc{State: doc}); err != nil {
		return NetworkView{}, fmt.Errorf("panel: journal %s: %w", resource, err)
	}
	return networkViewOf(n)
}

// networkViewOf is the view of a network: its first IPv4 configuration, whose gateway is the subnet's first host
// address when the Engine reports none.
func networkViewOf(n dockerapi.Network) (NetworkView, error) {
	for _, c := range n.IPAM.Config {
		if !c.Subnet.IsValid() || !c.Subnet.Addr().Is4() {
			continue
		}
		subnet := c.Subnet.Masked()
		gateway := c.Gateway
		if !gateway.IsValid() {
			gateway = subnet.Addr().Next()
		}
		return NetworkView{Network: n.Name, ID: n.ID, Subnet: subnet, Gateway: gateway, Proxy: gateway.Next()}, nil
	}
	return NetworkView{}, fmt.Errorf("panel: network %s has no IPv4 subnet", n.Name)
}

// Verify reads the network from the Engine alone, because startup recovery passes no output.
func (h networkCreateHandler) Verify(ctx context.Context, _ core.Env, _ NetworkCreateInput, _ NetworkView) error {
	docker, err := networkClient(h.d)
	if err != nil {
		return err
	}
	n, err := docker.InspectNetwork(ctx, NetworkName)
	if dockerapi.IsNotFound(err) {
		return fmt.Errorf("panel: network %s does not exist", NetworkName)
	}
	if err != nil {
		return fmt.Errorf("panel: inspect network %s: %w", NetworkName, networkEngineCoded(err))
	}
	var problems []string
	if n.Labels[core.LabelModule] != ID {
		problems = append(problems, fmt.Sprintf("it lacks the label %s=%s", core.LabelModule, ID))
	}
	if n.Options[BridgeOption] != BridgeName {
		problems = append(problems, "its bridge is not "+BridgeName)
	}
	if n.EnableIPv6 {
		problems = append(problems, "IPv6 is on")
	}
	if len(n.IPAM.Config) != 1 {
		problems = append(problems, fmt.Sprintf("it has %d address configurations, not 1", len(n.IPAM.Config)))
	} else if s := n.IPAM.Config[0].Subnet; !s.IsValid() || !s.Addr().Is4() || s.Bits() != networkBlockBits || !PanelRange.Contains(s.Addr()) {
		problems = append(problems, fmt.Sprintf("its subnet is not a /%d of %s", networkBlockBits, PanelRange))
	}
	if len(problems) > 0 {
		return fmt.Errorf("panel: network %s is not as created: %s", NetworkName, strings.Join(problems, "; "))
	}
	return nil
}

// Observe reads the network of one effect.
func (h networkCreateHandler) Observe(ctx context.Context, _ core.Env, e core.Effect) (core.Observation, error) {
	return networkObserve(ctx, h.d, e)
}

// Inverse removes the network a creation made, by its id, while no container is attached to it.
func (h networkCreateHandler) Inverse(ctx context.Context, _ core.Env, e core.Effect) (core.InverseResult, error) {
	id, ok := strings.CutPrefix(e.Resource, networkNoun+":")
	if !ok || id == "" || strings.HasPrefix(id, "?") || e.Kind != core.EffectCreate {
		return core.InverseResult{}, fmt.Errorf("panel: %s %s is not a made creation of the panel network", e.Kind, e.Resource)
	}
	docker, err := networkClient(h.d)
	if err != nil {
		return core.InverseResult{}, err
	}
	n, err := docker.InspectNetwork(ctx, id)
	if dockerapi.IsNotFound(err) {
		return core.InverseResult{}, nil
	}
	if err != nil {
		return core.InverseResult{}, fmt.Errorf("panel: inspect network %s: %w", id, networkEngineCoded(err))
	}
	if len(n.Containers) > 0 {
		names := make([]string, 0, len(n.Containers))
		for _, c := range n.Containers {
			names = append(names, c.Name)
		}
		slices.Sort(names)
		return core.InverseResult{}, core.Definite(fmt.Errorf("panel: network %s still has %d attached container(s): %s",
			n.Name, len(names), strings.Join(names, ", ")))
	}
	err = docker.RemoveNetwork(ctx, id)
	switch {
	case err == nil, dockerapi.IsNotFound(err):
		return core.InverseResult{}, nil
	case networkRefused(err):
		return core.InverseResult{}, core.Definite(fmt.Errorf("panel: remove network %s: %w", id, err))
	default:
		return core.InverseResult{}, fmt.Errorf("panel: remove network %s: %w", id, networkEngineCoded(err))
	}
}
