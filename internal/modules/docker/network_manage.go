package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

var (
	manageNamePattern   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
	manageDriverPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
)

// manageInvalid is the refusal of one input field. Nothing has changed when
// an input is refused, so it is Definite.
func manageInvalid(path, msg string) error {
	return core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: msg,
		Fields: []errcode.FieldError{{Path: path, Message: msg}}})
}

// manageLabels refuses the module label: module-owned objects carry it, and
// the undo and remove refusals that protect them depend on it.
func manageLabels(labels map[string]string) error {
	if _, ok := labels[core.LabelModule]; ok {
		return manageInvalid("/labels", "label "+core.LabelModule+" is reserved for Darkpanel modules")
	}
	return nil
}

func manageLabelLines(labels map[string]string) []core.DiffLine {
	var lines []core.DiffLine
	for _, k := range slices.Sorted(maps.Keys(labels)) {
		lines = append(lines, core.DiffLine{Op: " ", Text: "label " + k + "=" + labels[k]})
	}
	return lines
}

func manageHash(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("docker: hash the planned state: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// manageNetworkUsers names the containers attached to a network as
// InspectNetwork reports them.
func manageNetworkUsers(n dockerapi.Network) []string {
	names := make([]string, 0, len(n.Containers))
	for _, c := range n.Containers {
		names = append(names, c.Name)
	}
	slices.Sort(names)
	return names
}

func (k dk) networkCreate() core.ActionDef {
	return core.DefineMutating[NetworkCreateInput, Created](core.ActionSpec{
		Name: ID + ".network.create", Summary: "Create a network.", Version: 1, Class: core.ClassCompensable,
		Side: core.SideAgent, LockKeys: []string{lockKey},
	}, networkCreateHandler{k: k})
}

// networkCreateHandler creates one network. Its undo removes the network
// while no container uses it.
type networkCreateHandler struct{ k dk }

// request builds the create request and its diff, or refuses the input.
// Nothing has changed when it fails, so every failure is Definite.
func (h networkCreateHandler) request(ctx context.Context, in NetworkCreateInput) (dockerapi.NetworkCreate, []core.DiffLine, error) {
	var none dockerapi.NetworkCreate
	if !manageNamePattern.MatchString(in.Name) {
		return none, nil, manageInvalid("/name", "network name must start with a letter or digit and hold at most 128 letters, digits, '_', '.' or '-'")
	}
	if err := manageLabels(in.Labels); err != nil {
		return none, nil, err
	}
	driver := in.Driver
	if driver == "" {
		driver = "bridge"
	}
	if !manageDriverPattern.MatchString(driver) {
		return none, nil, manageInvalid("/driver", "driver "+driver+" is not a valid driver name")
	}
	req := dockerapi.NetworkCreate{Name: in.Name, Driver: driver, Internal: in.Internal, Attachable: in.Attachable, Labels: in.Labels}
	diff := []core.DiffLine{{Op: "+", Text: "network " + in.Name}, {Op: " ", Text: "driver " + driver}}
	if in.Internal {
		diff = append(diff, core.DiffLine{Op: " ", Text: "internal"})
	}
	if in.Attachable {
		diff = append(diff, core.DiffLine{Op: " ", Text: "attachable"})
	}
	if in.IPv6 {
		enabled := true
		req.EnableIPv6 = &enabled
		diff = append(diff, core.DiffLine{Op: " ", Text: "IPv6 enabled"})
	}
	var subnet netip.Prefix
	if in.Subnet != "" {
		p, err := netip.ParsePrefix(in.Subnet)
		if err != nil || p != p.Masked() {
			return none, nil, manageInvalid("/subnet", "subnet "+in.Subnet+" is not a network prefix such as 10.9.0.0/24")
		}
		if p.Addr().Is6() && !in.IPv6 {
			return none, nil, manageInvalid("/subnet", "subnet "+in.Subnet+" is an IPv6 prefix; set ipv6 to use it")
		}
		subnet = p
		diff = append(diff, core.DiffLine{Op: " ", Text: "subnet " + p.String()})
	}
	var gateway netip.Addr
	if in.Gateway != "" {
		g, err := netip.ParseAddr(in.Gateway)
		switch {
		case !subnet.IsValid():
			return none, nil, manageInvalid("/gateway", "a gateway needs a subnet")
		case err != nil:
			return none, nil, manageInvalid("/gateway", "gateway "+in.Gateway+" is not an IP address")
		case !subnet.Contains(g):
			return none, nil, manageInvalid("/gateway", "gateway "+in.Gateway+" is outside subnet "+subnet.String())
		}
		gateway = g
		diff = append(diff, core.DiffLine{Op: " ", Text: "gateway " + g.String()})
	}
	if subnet.IsValid() {
		req.IPAM = &dockerapi.IPAM{Config: []dockerapi.IPAMConfig{{Subnet: subnet, Gateway: gateway}}}
	}
	diff = append(diff, manageLabelLines(in.Labels)...)
	list, err := h.k.d.docker.ListNetworks(ctx, dockerapi.Filters{"name": {in.Name}})
	if err != nil {
		return none, nil, core.Definite(engineError("network", in.Name, err))
	}
	// Docker's name filter also matches networks whose name only contains it.
	if slices.ContainsFunc(list, func(n dockerapi.Network) bool { return n.Name == in.Name }) {
		return none, nil, manageInvalid("/name", "network "+in.Name+" already exists")
	}
	return req, diff, nil
}

func (h networkCreateHandler) Plan(ctx context.Context, _ core.Env, in NetworkCreateInput) (core.PlanOutcome, error) {
	req, diff, err := h.request(ctx, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	hash, err := manageHash(req)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{Diff: diff, StateHash: hash}, nil
}

func (h networkCreateHandler) Apply(ctx context.Context, env core.Env, in NetworkCreateInput) (Created, error) {
	req, _, err := h.request(ctx, in)
	if err != nil {
		return Created{}, err
	}
	e := dockerEffect(core.EffectCreate, networkResource("?"+in.Name), json.RawMessage("null"))
	e.Recovery = &core.RecoveryID{Kind: "name", Value: in.Name}
	var id string
	err = h.k.run(ctx, env, e, func(ctx context.Context) (string, error) {
		var err error
		id, err = h.k.d.docker.CreateNetwork(ctx, req)
		if err != nil {
			return "", engineError("network", in.Name, err)
		}
		return networkResource(id), nil
	})
	if err != nil {
		return Created{}, err
	}
	return Created{ID: id, Name: in.Name}, nil
}

// Verify checks by the created id, or by name when recovery passes the zero
// output.
func (h networkCreateHandler) Verify(ctx context.Context, _ core.Env, in NetworkCreateInput, out Created) error {
	ref := out.ID
	if ref == "" {
		ref = in.Name
	}
	n, err := h.k.d.docker.InspectNetwork(ctx, ref)
	if err != nil {
		return engineError("network", ref, err)
	}
	if n.Name != in.Name {
		return fmt.Errorf("docker: network %s is named %s, want %s", ref, n.Name, in.Name)
	}
	return nil
}

func (h networkCreateHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	return h.k.observe(ctx, env, e)
}

// Inverse removes the created network. A network already gone is the state
// the undo wants; one a container joined since is left alone and refused.
func (h networkCreateHandler) Inverse(ctx context.Context, _ core.Env, e core.Effect) (core.InverseResult, error) {
	id, ok := strings.CutPrefix(e.Resource, networkResource(""))
	if !ok || id == "" || strings.HasPrefix(id, "?") {
		return core.InverseResult{}, fmt.Errorf("docker: %q is not a created network", e.Resource)
	}
	n, err := h.k.d.docker.InspectNetwork(ctx, id)
	if dockerapi.IsNotFound(err) {
		return core.InverseResult{}, nil
	}
	if err != nil {
		return core.InverseResult{}, core.Definite(engineError("network", id, err))
	}
	if users := manageNetworkUsers(n); len(users) > 0 {
		return core.InverseResult{}, core.Definite(errcode.New(errcode.DockerInUse,
			"network "+n.Name+" is used by containers: "+strings.Join(users, ", ")))
	}
	err = h.k.d.docker.RemoveNetwork(ctx, id)
	if err == nil || dockerapi.IsNotFound(err) {
		return core.InverseResult{}, nil
	}
	var refused *dockerapi.APIError
	if errors.As(err, &refused) && refused.StatusCode >= http.StatusBadRequest && refused.StatusCode < http.StatusInternalServerError {
		return core.InverseResult{}, core.Definite(engineError("network", n.Name, err))
	}
	return core.InverseResult{}, engineError("network", n.Name, err)
}

func (k dk) networkRemove() core.ActionDef {
	return core.DefineMutating[NetworkInput, Removed](core.ActionSpec{
		Name: ID + ".network.remove", Summary: "Remove a network.", Version: 1, Class: core.ClassIrreversible,
		Side: core.SideAgent, LockKeys: []string{lockKey},
	}, networkRemoveHandler{k: k})
}

// networkRemoveHandler deletes one network. Removal cannot be undone, so it
// has no Inverse.
type networkRemoveHandler struct{ k dk }

// target reads the network in names and refuses what removal must not touch.
// Nothing has changed when it fails, so every failure is Definite.
func (h networkRemoveHandler) target(ctx context.Context, in NetworkInput) (dockerapi.Network, error) {
	if in.Network == "" {
		return dockerapi.Network{}, manageInvalid("/network", "network is required")
	}
	n, err := h.k.d.docker.InspectNetwork(ctx, in.Network)
	if err != nil {
		return dockerapi.Network{}, core.Definite(engineError("network", in.Network, err))
	}
	switch n.Name {
	case "bridge", "host", "none":
		return dockerapi.Network{}, manageInvalid("/network", "network "+n.Name+" is predefined and cannot be removed")
	}
	if err := refuseOwned("network", n.Name, n.Labels); err != nil {
		return dockerapi.Network{}, err
	}
	if users := manageNetworkUsers(n); len(users) > 0 {
		return dockerapi.Network{}, core.Definite(errcode.New(errcode.DockerInUse,
			"network "+n.Name+" is used by containers: "+strings.Join(users, ", ")))
	}
	return n, nil
}

func (h networkRemoveHandler) Plan(ctx context.Context, _ core.Env, in NetworkInput) (core.PlanOutcome, error) {
	n, err := h.target(ctx, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	doc, err := dstate.NetworkCreated(&n)
	if err != nil {
		return core.PlanOutcome{}, fmt.Errorf("docker: render network %s: %w", n.ID, err)
	}
	hash, err := manageHash(doc)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{
		{Op: "-", Text: "network " + n.Name + " (" + n.ID[:min(len(n.ID), 12)] + ")"},
		{Op: " ", Text: "driver " + n.Driver},
	}
	return core.PlanOutcome{Diff: diff, StateHash: hash, Confirmation: n.Name}, nil
}

// Apply removes the network by the id it found, so a network created under
// the same name since the plan is left alone.
func (h networkRemoveHandler) Apply(ctx context.Context, env core.Env, in NetworkInput) (Removed, error) {
	n, err := h.target(ctx, in)
	if err != nil {
		return Removed{}, err
	}
	before, err := dstate.NetworkCreated(&n)
	if err != nil {
		return Removed{}, fmt.Errorf("docker: render network %s: %w", n.ID, err)
	}
	resource := networkResource(n.ID)
	err = h.k.run(ctx, env, dockerEffect(core.EffectDelete, resource, before), func(ctx context.Context) (string, error) {
		return resource, engineError("network", n.Name, h.k.d.docker.RemoveNetwork(ctx, n.ID))
	})
	if err != nil {
		return Removed{}, err
	}
	return Removed{ID: n.ID, Name: n.Name}, nil
}

// Verify checks by the removed id, or by in when recovery passes the zero
// output.
func (h networkRemoveHandler) Verify(ctx context.Context, _ core.Env, in NetworkInput, out Removed) error {
	ref := out.ID
	if ref == "" {
		ref = in.Network
	}
	_, err := h.k.d.docker.InspectNetwork(ctx, ref)
	switch {
	case err == nil:
		return fmt.Errorf("docker: network %s still exists", ref)
	case dockerapi.IsNotFound(err):
		return nil
	default:
		return engineError("network", ref, err)
	}
}

func (h networkRemoveHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	return h.k.observe(ctx, env, e)
}
