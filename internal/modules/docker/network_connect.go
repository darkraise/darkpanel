package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strconv"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

var endpointAliasPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)

func (k dk) networkConnect() core.ActionDef {
	return core.DefineMutating[ConnectInput, EndpointView](core.ActionSpec{
		Name: ID + ".network.connect", Summary: "Connect a container to a network.", Version: 1,
		Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: []string{lockKey},
	}, connectHandler{k: k})
}

func (k dk) networkDisconnect() core.ActionDef {
	return core.DefineMutating[DisconnectInput, EndpointView](core.ActionSpec{
		Name: ID + ".network.disconnect", Summary: "Disconnect a container from a network.", Version: 1,
		Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: []string{lockKey},
	}, disconnectHandler{k: k})
}

// endpointTarget is a container and a network as the Engine reports them, and
// the container's endpoint on that network, nil when it is not attached.
type endpointTarget struct {
	container dockerapi.ContainerJSON
	network   dockerapi.Network
	name      string
	ep        *dockerapi.EndpointSettings
}

// endpointResolve reads the container and network the input names and refuses
// objects another module owns. Engine errors are not marked Definite, because
// Apply also resolves after its call has changed the endpoint.
func (k dk) endpointResolve(ctx context.Context, container, network string) (endpointTarget, error) {
	if container == "" {
		return endpointTarget{}, manageInvalid("/container", "container is required")
	}
	if network == "" {
		return endpointTarget{}, manageInvalid("/network", "network is required")
	}
	c, err := k.d.docker.InspectContainer(ctx, container)
	if err != nil {
		return endpointTarget{}, engineError("container", container, err)
	}
	n, err := k.d.docker.InspectNetwork(ctx, network)
	if err != nil {
		return endpointTarget{}, engineError("network", network, err)
	}
	t := endpointTarget{container: c, network: n, name: containerName(c)}
	if err := refuseOwned("container", t.name, c.Config.Labels); err != nil {
		return endpointTarget{}, err
	}
	if err := refuseOwned("network", n.Name, n.Labels); err != nil {
		return endpointTarget{}, err
	}
	// Docker keys Networks by network name, which is not unique, so the
	// endpoint is found by the network id it carries.
	for _, ep := range c.NetworkSettings.Networks {
		if ep != nil && ep.NetworkID == n.ID {
			t.ep = ep
			break
		}
	}
	return t, nil
}

// endpointPlan is the outcome of planning a change to t's endpoint. Its hash
// covers the endpoint's identity, so a plan goes stale when the endpoint
// appears, disappears or is renewed before apply.
func endpointPlan(t endpointTarget, diff []core.DiffLine) (core.PlanOutcome, error) {
	doc, err := dstate.Endpoint(t.container.ID, t.network.ID, t.ep, dstate.Recorded)
	if err != nil {
		return core.PlanOutcome{}, fmt.Errorf("docker: render endpoint %s@%s: %w", t.container.ID, t.network.ID, err)
	}
	sum := sha256.Sum256([]byte(t.container.ID + "\n" + t.network.ID + "\n" + string(doc)))
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{}, StateHash: hex.EncodeToString(sum[:])}, nil
}

// endpointApply journals and makes the Engine call that changes t's endpoint.
// The effect's Before is the endpoint as it is now, which an undo restores.
func (k dk) endpointApply(ctx context.Context, env core.Env, t endpointTarget, call func(ctx context.Context) error) error {
	before, err := dstate.Endpoint(t.container.ID, t.network.ID, t.ep, dstate.Restored)
	if err != nil {
		return fmt.Errorf("docker: render endpoint %s@%s: %w", t.container.ID, t.network.ID, err)
	}
	resource := endpointResource(t.container.ID, t.network.ID)
	return k.run(ctx, env, dockerEffect(core.EffectModify, resource, before), func(ctx context.Context) (string, error) {
		return resource, call(ctx)
	})
}

// endpointViewOf renders ep, an endpoint of t's container on t's network.
func endpointViewOf(t endpointTarget, ep *dockerapi.EndpointSettings) EndpointView {
	v := EndpointView{Network: t.network.Name, NetworkID: t.network.ID, Container: t.name, EndpointID: ep.EndpointID}
	if len(ep.Aliases) > 0 {
		v.Aliases = slices.Sorted(slices.Values(ep.Aliases))
	}
	v4, v6 := ep.IPAddress, ep.GlobalIPv6Address
	if ep.IPAMConfig != nil {
		if ep.IPAMConfig.IPv4Address.IsValid() {
			v4 = ep.IPAMConfig.IPv4Address
		}
		if ep.IPAMConfig.IPv6Address.IsValid() {
			v6 = ep.IPAMConfig.IPv6Address
		}
	}
	if v4.IsValid() {
		v.IPv4 = v4.String()
	}
	if v6.IsValid() {
		v.IPv6 = v6.String()
	}
	return v
}

// endpointStaticLines lists the endpoint's static addresses for a diff.
func endpointStaticLines(op string, ipam *dockerapi.EndpointIPAMConfig) []core.DiffLine {
	if ipam == nil {
		return nil
	}
	var lines []core.DiffLine
	if ipam.IPv4Address.IsValid() {
		lines = append(lines, core.DiffLine{Op: op, Text: "ipv4 " + ipam.IPv4Address.String()})
	}
	if ipam.IPv6Address.IsValid() {
		lines = append(lines, core.DiffLine{Op: op, Text: "ipv6 " + ipam.IPv6Address.String()})
	}
	return lines
}

// endpointSettings is what a connect sends for the aliases and static
// addresses given, nil when there are none. ipam's zero addresses are unset.
func endpointSettings(aliases []string, ipam dockerapi.EndpointIPAMConfig) *dockerapi.EndpointSettings {
	static := ipam.IPv4Address.IsValid() || ipam.IPv6Address.IsValid()
	if len(aliases) == 0 && !static {
		return nil
	}
	s := &dockerapi.EndpointSettings{Aliases: aliases}
	if static {
		s.IPAMConfig = &ipam
	}
	return s
}

// endpointAddr parses a journaled address; "" is no address.
func endpointAddr(s string) (netip.Addr, error) {
	if s == "" {
		return netip.Addr{}, nil
	}
	return netip.ParseAddr(s)
}

// endpointInverseErr marks a 4xx answer Definite, because the daemon refused
// before changing anything; any other failure leaves the undo uncertain.
func endpointInverseErr(err error) error {
	var refused *dockerapi.APIError
	if errors.As(err, &refused) && refused.StatusCode >= http.StatusBadRequest && refused.StatusCode < http.StatusInternalServerError {
		return core.Definite(err)
	}
	return err
}

// endpointRestored is the Restored endpoint document an effect journaled as
// its Before.
type endpointRestored struct {
	Container  string   `json:"container"`
	Network    string   `json:"network"`
	Present    *bool    `json:"present"`
	Aliases    []string `json:"aliases"`
	IPAMConfig *struct {
		IPv4 string `json:"ipv4"`
		IPv6 string `json:"ipv6"`
	} `json:"ipamConfig"`
}

// endpointBefore decodes e's Before. A document an undo cannot act on is a
// Definite error, since nothing has been called yet.
func endpointBefore(e core.Effect) (endpointRestored, error) {
	var doc endpointRestored
	if err := json.Unmarshal(e.Before.State, &doc); err != nil {
		return endpointRestored{}, core.Definite(fmt.Errorf("docker: read the endpoint %s is restored to: %w", e.Resource, err))
	}
	if doc.Container == "" || doc.Network == "" {
		return endpointRestored{}, core.Definite(fmt.Errorf("docker: the endpoint %s is restored to names no container or network", e.Resource))
	}
	return doc, nil
}

// connectHandler connects a container to a network. Its undo disconnects it.
type connectHandler struct{ k dk }

// check refuses the input or a container already on the network and builds
// the endpoint settings to send. Nothing has changed when it fails, so every
// failure is Definite.
func (h connectHandler) check(ctx context.Context, in ConnectInput) (endpointTarget, *dockerapi.EndpointSettings, error) {
	for i, a := range in.Aliases {
		if !endpointAliasPattern.MatchString(a) {
			return endpointTarget{}, nil, manageInvalid("/aliases/"+strconv.Itoa(i),
				"alias "+strconv.Quote(a)+" must start with a letter or digit and hold at most 63 letters, digits, '_', '.' or '-'")
		}
	}
	var ipam dockerapi.EndpointIPAMConfig
	if in.IPv4 != "" {
		a, err := netip.ParseAddr(in.IPv4)
		if err != nil || !a.Is4() {
			return endpointTarget{}, nil, manageInvalid("/ipv4", "ipv4 "+in.IPv4+" is not an IPv4 address")
		}
		ipam.IPv4Address = a
	}
	if in.IPv6 != "" {
		a, err := netip.ParseAddr(in.IPv6)
		if err != nil || !a.Is6() || a.Is4In6() {
			return endpointTarget{}, nil, manageInvalid("/ipv6", "ipv6 "+in.IPv6+" is not an IPv6 address")
		}
		ipam.IPv6Address = a
	}
	t, err := h.k.endpointResolve(ctx, in.Container, in.Network)
	if err != nil {
		return endpointTarget{}, nil, core.Definite(err)
	}
	if t.ep != nil {
		return endpointTarget{}, nil, core.Definite(errcode.New(errcode.InvalidInput,
			"container "+t.name+" is already connected to network "+t.network.Name))
	}
	return t, endpointSettings(in.Aliases, ipam), nil
}

func (h connectHandler) Plan(ctx context.Context, _ core.Env, in ConnectInput) (core.PlanOutcome, error) {
	t, settings, err := h.check(ctx, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{{Op: "+", Text: "container " + t.name + " joins network " + t.network.Name}}
	if settings != nil {
		for _, a := range settings.Aliases {
			diff = append(diff, core.DiffLine{Op: "+", Text: "alias " + a})
		}
		diff = append(diff, endpointStaticLines("+", settings.IPAMConfig)...)
	}
	return endpointPlan(t, diff)
}

func (h connectHandler) Apply(ctx context.Context, env core.Env, in ConnectInput) (EndpointView, error) {
	t, settings, err := h.check(ctx, in)
	if err != nil {
		return EndpointView{}, err
	}
	err = h.k.endpointApply(ctx, env, t, func(ctx context.Context) error {
		return h.k.d.docker.ConnectNetwork(ctx, t.network.ID, t.container.ID, settings)
	})
	if err != nil {
		return EndpointView{}, err
	}
	after, err := h.k.endpointResolve(ctx, t.container.ID, t.network.ID)
	if err != nil {
		return EndpointView{}, err
	}
	if after.ep == nil {
		return EndpointView{}, fmt.Errorf("docker: container %s is not on network %s after the connect", t.name, t.network.Name)
	}
	return endpointViewOf(after, after.ep), nil
}

// Verify checks the endpoint from in alone, because recovery passes the zero
// output.
func (h connectHandler) Verify(ctx context.Context, _ core.Env, in ConnectInput, _ EndpointView) error {
	t, err := h.k.endpointResolve(ctx, in.Container, in.Network)
	if err != nil {
		return err
	}
	if t.ep == nil {
		return fmt.Errorf("docker: container %s is not connected to network %s", t.name, t.network.Name)
	}
	return nil
}

func (h connectHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	return h.k.observe(ctx, env, e)
}

// Inverse disconnects the container the journaled Before names.
func (h connectHandler) Inverse(ctx context.Context, _ core.Env, e core.Effect) (core.InverseResult, error) {
	doc, err := endpointBefore(e)
	if err != nil {
		return core.InverseResult{}, err
	}
	if err := h.k.d.docker.DisconnectNetwork(ctx, doc.Network, doc.Container, false); err != nil {
		return core.InverseResult{}, endpointInverseErr(err)
	}
	return core.InverseResult{}, nil
}

// disconnectHandler disconnects a container from a network. Its undo
// reconnects it with the aliases and static addresses it had.
type disconnectHandler struct{ k dk }

// check refuses the input or a container not on the network. Nothing has
// changed when it fails, so every failure is Definite.
func (h disconnectHandler) check(ctx context.Context, in DisconnectInput) (endpointTarget, error) {
	t, err := h.k.endpointResolve(ctx, in.Container, in.Network)
	if err != nil {
		return endpointTarget{}, core.Definite(err)
	}
	if t.ep == nil {
		return endpointTarget{}, core.Definite(errcode.New(errcode.InvalidInput,
			"container "+t.name+" is not connected to network "+t.network.Name))
	}
	return t, nil
}

func (h disconnectHandler) Plan(ctx context.Context, _ core.Env, in DisconnectInput) (core.PlanOutcome, error) {
	t, err := h.check(ctx, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{{Op: "-", Text: "container " + t.name + " leaves network " + t.network.Name}}
	for _, a := range slices.Sorted(slices.Values(t.ep.Aliases)) {
		diff = append(diff, core.DiffLine{Op: "-", Text: "alias " + a})
	}
	diff = append(diff, endpointStaticLines("-", t.ep.IPAMConfig)...)
	return endpointPlan(t, diff)
}

// Apply returns the endpoint as it was before the call, since a detached
// endpoint no longer exists to be read.
func (h disconnectHandler) Apply(ctx context.Context, env core.Env, in DisconnectInput) (EndpointView, error) {
	t, err := h.check(ctx, in)
	if err != nil {
		return EndpointView{}, err
	}
	err = h.k.endpointApply(ctx, env, t, func(ctx context.Context) error {
		return h.k.d.docker.DisconnectNetwork(ctx, t.network.ID, t.container.ID, in.Force)
	})
	if err != nil {
		return EndpointView{}, err
	}
	return endpointViewOf(t, t.ep), nil
}

// Verify checks the endpoint from in alone, because recovery passes the zero
// output.
func (h disconnectHandler) Verify(ctx context.Context, _ core.Env, in DisconnectInput, _ EndpointView) error {
	t, err := h.k.endpointResolve(ctx, in.Container, in.Network)
	if err != nil {
		return err
	}
	if t.ep != nil {
		return fmt.Errorf("docker: container %s is still connected to network %s", t.name, t.network.Name)
	}
	return nil
}

func (h disconnectHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	return h.k.observe(ctx, env, e)
}

// Inverse reconnects the container the journaled Before names, with the
// aliases and static addresses it journaled.
func (h disconnectHandler) Inverse(ctx context.Context, _ core.Env, e core.Effect) (core.InverseResult, error) {
	doc, err := endpointBefore(e)
	if err != nil {
		return core.InverseResult{}, err
	}
	if doc.Present == nil || !*doc.Present {
		return core.InverseResult{}, core.Definite(fmt.Errorf("docker: the endpoint %s is restored to is not present", e.Resource))
	}
	var ipam dockerapi.EndpointIPAMConfig
	if doc.IPAMConfig != nil {
		if ipam.IPv4Address, err = endpointAddr(doc.IPAMConfig.IPv4); err != nil {
			return core.InverseResult{}, core.Definite(fmt.Errorf("docker: the endpoint %s is restored to: %w", e.Resource, err))
		}
		if ipam.IPv6Address, err = endpointAddr(doc.IPAMConfig.IPv6); err != nil {
			return core.InverseResult{}, core.Definite(fmt.Errorf("docker: the endpoint %s is restored to: %w", e.Resource, err))
		}
	}
	if err := h.k.d.docker.ConnectNetwork(ctx, doc.Network, doc.Container, endpointSettings(doc.Aliases, ipam)); err != nil {
		return core.InverseResult{}, endpointInverseErr(err)
	}
	return core.InverseResult{}, nil
}
