package cloudflare

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// The resource nouns of the network claim effects: a claim is "cloudflare.claim:<tunnel>|<network>|<claimant>" and the
// connector's endpoint on a network "cloudflare.endpoint:<tunnel>|<network>".
const (
	nounClaim    = "cloudflare.claim"
	nounEndpoint = "cloudflare.endpoint"
)

// attachUnsupportedReason is the detail of the refusal of a network or target the connector cannot reach.
const attachUnsupportedReason = "connect the container to a user-defined network"

// networkAttach is cloudflare.network.attach: it joins the tunnel's connector to a user-defined bridge network, so the
// connector can reach a container there by name, and records the claimant that needs the attachment.
func (c cf) networkAttach() core.ActionDef {
	return core.DefineMutating[NetworkAttachInput, NetworkAttachment](core.ActionSpec{
		Name:     ID + ".network.attach",
		Summary:  "Join the tunnel's connector to a Docker network and record the claimant that needs it.",
		Version:  1,
		Class:    core.ClassCompensable,
		Side:     core.SideAgent,
		LockKeys: []string{"cloudflare"},
	}, networkAttachHandler{networkClaims{c}})
}

// networkClaims holds what the attach and detach handlers share: the observation and the undo of their effects.
type networkClaims struct{ c cf }

// networkAttachHandler is the handler of cloudflare.network.attach.
type networkAttachHandler struct{ networkClaims }

// claimDoc is the journaled state of a claim.
type claimDoc struct {
	Tunnel      string `json:"tunnel"`
	Network     string `json:"network"`
	Claimant    string `json:"claimant"`
	Preexisting bool   `json:"preexisting"`
	ChangeID    string `json:"changeId"`
}

// claimDocument renders a claim as its journaled state.
func claimDocument(c claimRow) (json.RawMessage, error) {
	doc, err := json.Marshal(claimDoc{Tunnel: c.TunnelID, Network: c.Network, Claimant: c.Claimant, Preexisting: c.Preexisting, ChangeID: c.ChangeID})
	if err != nil {
		return nil, fmt.Errorf("cloudflare: render the claim of %s on %s for tunnel %s: %w", c.Claimant, c.Network, c.TunnelID, err)
	}
	return doc, nil
}

// claimFind returns the claim of claimant on the network for the tunnel; ok is false when there is none.
func claimFind(ctx context.Context, q queryer, tunnel, network, claimant string) (claimRow, bool, error) {
	all, err := connectorClaims(ctx, q, tunnel, network)
	if err != nil {
		return claimRow{}, false, err
	}
	for _, c := range all {
		if c.Claimant == claimant {
			return c, true, nil
		}
	}
	return claimRow{}, false, nil
}

// claimIdentity splits a claim identity "<tunnel>|<network>|<claimant>". The claimant is the rest after the second
// "|", so it may hold "|" itself.
func claimIdentity(identity string) (tunnel, network, claimant string, err error) {
	parts := strings.SplitN(identity, "|", 3)
	if len(parts) != 3 || tunnelIDProblem(parts[0]) != "" || !networkNamePattern.MatchString(parts[1]) || parts[2] == "" {
		return "", "", "", fmt.Errorf("cloudflare: %q is not a claim identity", identity)
	}
	return parts[0], parts[1], parts[2], nil
}

// attachEndpointIdentity splits an endpoint identity "<tunnel>|<network>".
func attachEndpointIdentity(identity string) (tunnel, network string, err error) {
	tunnel, network, ok := strings.Cut(identity, "|")
	if !ok || tunnelIDProblem(tunnel) != "" || !networkNamePattern.MatchString(network) {
		return "", "", fmt.Errorf("cloudflare: %q is not an endpoint identity", identity)
	}
	return tunnel, network, nil
}

// attachInvalid is the Definite invalid_input refusal of the fields.
func attachInvalid(fields []errcode.FieldError) error {
	msgs := make([]string, len(fields))
	for i, f := range fields {
		msgs[i] = f.Message
	}
	return core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: strings.Join(msgs, "; "), Fields: fields})
}

// attachEngine marks a failed Engine read Definite, as the Engine changed nothing, with an unreachable Engine coded
// docker.engine_unavailable.
func attachEngine(what string, err error) error {
	return core.Definite(fmt.Errorf("cloudflare: %s: %w", what, engineCoded(err)))
}

// attachSupported is the supported-target rule: the network must be a user-defined bridge and a target, when there is
// one, must be attached to it. Without a target the rule is judged on the network alone.
func attachSupported(network dockerapi.Network, target *dockerapi.ContainerJSON) error {
	userDefined := network.Name != "bridge" && network.Name != "host" && network.Name != "none"
	if network.Driver != "bridge" || !userDefined || (target != nil && target.NetworkSettings.Networks[network.Name] == nil) {
		return core.Definite(errcode.New(errcode.CloudflareUnsupportedTarget, attachUnsupportedReason))
	}
	return nil
}

// attachAddrsOf is every address of the endpoint, assigned by the Engine or fixed, without duplicates.
func attachAddrsOf(ep *dockerapi.EndpointSettings) []netip.Addr {
	candidates := []netip.Addr{ep.IPAddress, ep.GlobalIPv6Address}
	if ep.IPAMConfig != nil {
		candidates = append(candidates, ep.IPAMConfig.IPv4Address, ep.IPAMConfig.IPv6Address)
	}
	var addrs []netip.Addr
	for _, a := range candidates {
		if a.IsValid() && !slices.Contains(addrs, a) {
			addrs = append(addrs, a)
		}
	}
	return addrs
}

// attachHashClaim is a claim as a plan's state hash covers it.
type attachHashClaim struct {
	Claimant    string `json:"claimant"`
	ChangeID    string `json:"changeId"`
	Preexisting bool   `json:"preexisting"`
}

// attachHashDoc is what a plan's state hash covers of one network.
type attachHashDoc struct {
	NetworkID  string            `json:"networkId"`
	EndpointID string            `json:"endpointId"`
	TargetID   string            `json:"targetId"`
	Tunnel     string            `json:"tunnel"`
	Claims     []attachHashClaim `json:"claims"`
}

// attachStateHash is the hex SHA-256 of the JSON form of the network, the connector's endpoint on it (nil when
// absent), the target's id, the tunnel and the network's claims ordered by claimant.
func attachStateHash(networkID string, ep *dockerapi.EndpointSettings, targetID, tunnel string, all []claimRow) (string, error) {
	doc := attachHashDoc{NetworkID: networkID, TargetID: targetID, Tunnel: tunnel, Claims: make([]attachHashClaim, len(all))}
	if ep != nil {
		doc.EndpointID = ep.EndpointID
	}
	for i, c := range all {
		doc.Claims[i] = attachHashClaim{Claimant: c.Claimant, ChangeID: c.ChangeID, Preexisting: c.Preexisting}
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("cloudflare: render the plan state: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// networkAttachCheck is what an attach found and decided.
type networkAttachCheck struct {
	network     dockerapi.Network
	container   dockerapi.ContainerJSON
	endpoint    *dockerapi.EndpointSettings // the connector's endpoint on the network; nil when not attached
	targetID    string
	claims      []claimRow // the network's claims for the tunnel, ordered by claimant
	connect     bool
	claim       bool
	preexisting bool
}

// check decides everything an attach does and changes nothing, so every failure is Definite: a refusal, a failed
// read of the database and a failed Engine read alike.
func (h networkAttachHandler) check(ctx context.Context, env core.Env, in NetworkAttachInput) (networkAttachCheck, error) {
	var fields []errcode.FieldError
	if p := tunnelIDProblem(in.Tunnel); p != "" {
		fields = append(fields, errcode.FieldError{Path: "/tunnel", Message: p})
	}
	if !networkNamePattern.MatchString(in.Network) {
		fields = append(fields, errcode.FieldError{Path: "/network", Message: "network must be a Docker network name"})
	}
	if p := claimantProblem(in.Claimant); p != "" {
		fields = append(fields, errcode.FieldError{Path: "/claimant", Message: p})
	}
	if in.Target != "" && !networkNamePattern.MatchString(in.Target) {
		fields = append(fields, errcode.FieldError{Path: "/target", Message: "target must be a Docker container name"})
	}
	if len(fields) > 0 {
		return networkAttachCheck{}, attachInvalid(fields)
	}

	db := env.DB.Read()
	name := connectorName(in.Tunnel)
	if _, ok, err := loadConnector(ctx, db, in.Tunnel); err != nil {
		return networkAttachCheck{}, core.Definite(err)
	} else if !ok {
		return networkAttachCheck{}, core.Definite(errcode.New(errcode.CloudflareConnectorMissing, "no connector of tunnel "+in.Tunnel+" is deployed on this host"))
	}
	docker := h.c.a.docker
	c, err := docker.InspectContainer(ctx, name)
	if dockerapi.IsNotFound(err) {
		return networkAttachCheck{}, core.Definite(errcode.New(errcode.CloudflareConnectorMissing, "the connector container "+name+" does not exist"))
	}
	if err != nil {
		return networkAttachCheck{}, attachEngine("inspect container "+name, err)
	}
	if err := foreignConnector(c, name); err != nil {
		return networkAttachCheck{}, err
	}
	network, err := docker.InspectNetwork(ctx, in.Network)
	if dockerapi.IsNotFound(err) {
		return networkAttachCheck{}, core.Definite(errcode.New(errcode.NotFound, "network "+in.Network+" does not exist"))
	}
	if err != nil {
		return networkAttachCheck{}, attachEngine("inspect network "+in.Network, err)
	}
	var target *dockerapi.ContainerJSON
	if in.Target != "" {
		t, err := docker.InspectContainer(ctx, in.Target)
		switch {
		case dockerapi.IsNotFound(err):
		case err != nil:
			return networkAttachCheck{}, attachEngine("inspect container "+in.Target, err)
		default:
			target = &t
		}
	}
	// A target that does not exist is judged after the network, so a network the connector can never use is refused
	// as such whatever the target.
	if err := attachSupported(network, target); err != nil {
		return networkAttachCheck{}, err
	}
	var targetID string
	if in.Target != "" {
		if target == nil {
			return networkAttachCheck{}, core.Definite(errcode.New(errcode.NotFound, "container "+in.Target+" does not exist"))
		}
		targetID = target.ID
	}
	ep := c.NetworkSettings.Networks[network.Name]
	if in.Address.IsValid() && ep != nil {
		if addrs := attachAddrsOf(ep); len(addrs) > 0 && !slices.Contains(addrs, in.Address) {
			shown := make([]string, len(addrs))
			for i, a := range addrs {
				shown[i] = a.String()
			}
			msg := fmt.Sprintf("%s is already on network %s at %s, not %s", name, network.Name, strings.Join(shown, ", "), in.Address)
			return networkAttachCheck{}, attachInvalid([]errcode.FieldError{{Path: "/address", Message: msg}})
		}
	}
	all, err := connectorClaims(ctx, db, in.Tunnel, network.Name)
	if err != nil {
		return networkAttachCheck{}, core.Definite(err)
	}
	ck := networkAttachCheck{network: network, container: c, endpoint: ep, targetID: targetID, claims: all, connect: ep == nil, claim: true}
	if len(all) == 0 {
		ck.preexisting = ep != nil
	} else {
		ck.preexisting = all[0].Preexisting
	}
	for _, existing := range all {
		if existing.Claimant == in.Claimant {
			ck.claim, ck.preexisting = false, existing.Preexisting
		}
	}
	return ck, nil
}

// Plan shows the join and the claim an attach adds. The hash covers the network, the connector's endpoint on it, the
// target, the tunnel and the network's claims.
func (h networkAttachHandler) Plan(ctx context.Context, env core.Env, in NetworkAttachInput) (core.PlanOutcome, error) {
	ck, err := h.check(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	name, network := connectorName(in.Tunnel), ck.network.Name
	var diff []core.DiffLine
	if ck.connect {
		diff = append(diff, core.DiffLine{Op: "+", Text: "container " + name + " joins network " + network})
		switch {
		case in.Address.Is4():
			diff = append(diff, core.DiffLine{Op: "+", Text: "ipv4 " + in.Address.String()})
		case in.Address.IsValid():
			diff = append(diff, core.DiffLine{Op: "+", Text: "ipv6 " + in.Address.String()})
		}
	}
	if ck.claim {
		diff = append(diff, core.DiffLine{Op: "+", Text: "claim of " + in.Claimant + " on network " + network})
		if ck.endpoint != nil && len(ck.claims) == 0 {
			diff = append(diff, core.DiffLine{Op: " ", Text: name + " is already on network " + network + "; detaching leaves it there"})
		}
	}
	hash, err := attachStateHash(ck.network.ID, ck.endpoint, ck.targetID, in.Tunnel, ck.claims)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{}, StateHash: hash}, nil
}

// Apply connects the connector to the network when it is not there and records the claim when the claimant has none.
func (h networkAttachHandler) Apply(ctx context.Context, env core.Env, in NetworkAttachInput) (NetworkAttachment, error) {
	ck, err := h.check(ctx, env, in)
	if err != nil {
		return NetworkAttachment{}, err
	}
	name, network := connectorName(in.Tunnel), ck.network.Name
	if ck.connect {
		resource := resourceOf(nounEndpoint, in.Tunnel+"|"+network)
		obs, err := h.Observe(ctx, env, core.Effect{Kind: core.EffectModify, Resource: resource, State: core.EffectIntended})
		if err != nil {
			return NetworkAttachment{}, core.Definite(fmt.Errorf("cloudflare: observe %s: %w", resource, err))
		}
		var settings *dockerapi.EndpointSettings
		switch {
		case in.Address.Is4():
			settings = &dockerapi.EndpointSettings{IPAMConfig: &dockerapi.EndpointIPAMConfig{IPv4Address: in.Address}}
		case in.Address.IsValid():
			settings = &dockerapi.EndpointSettings{IPAMConfig: &dockerapi.EndpointIPAMConfig{IPv6Address: in.Address}}
		}
		e := connectorEffect(core.EffectModify, resource, obs.Current.State, engineCommitWait)
		err = h.c.journal(ctx, env, e, h.Observe, func(ctx context.Context) (string, error) {
			if err := h.c.a.docker.ConnectNetwork(ctx, ck.network.ID, ck.container.ID, settings); err != nil {
				return "", fmt.Errorf("cloudflare: connect %s to network %s: %w", name, network, engineCoded(err))
			}
			return resource, nil
		})
		if err != nil {
			return NetworkAttachment{}, err
		}
	}
	if ck.claim {
		row := claimRow{TunnelID: in.Tunnel, Network: network, Claimant: in.Claimant, ChangeID: env.ChangeID, Preexisting: ck.preexisting}
		doc, err := claimDocument(row)
		if err != nil {
			return NetworkAttachment{}, err
		}
		identity := in.Tunnel + "|" + network + "|" + in.Claimant
		e := connectorEffect(core.EffectCreate, resourceOf(nounClaim, "?"+identity), nil, 0)
		e.Local, e.InverseLocal = true, true
		e.After = &core.StateDoc{State: doc}
		err = h.c.journal(ctx, env, e, h.Observe, func(ctx context.Context) (string, error) {
			return resourceOf(nounClaim, identity), env.DB.Write(ctx, func(tx *sql.Tx) error { return insertClaim(ctx, tx, row) })
		})
		if err != nil {
			return NetworkAttachment{}, err
		}
	}
	return NetworkAttachment{Tunnel: in.Tunnel, Network: network, Claimant: in.Claimant, Preexisting: ck.preexisting, Attached: true}, nil
}

// Verify checks from the host alone that the network exists, the connector is on it and the claimant holds a claim
// on it.
func (h networkAttachHandler) Verify(ctx context.Context, env core.Env, in NetworkAttachInput, _ NetworkAttachment) error {
	docker := h.c.a.docker
	network, err := docker.InspectNetwork(ctx, in.Network)
	if dockerapi.IsNotFound(err) {
		return fmt.Errorf("cloudflare: network %s does not exist", in.Network)
	}
	if err != nil {
		return fmt.Errorf("cloudflare: inspect network %s: %w", in.Network, engineCoded(err))
	}
	name := connectorName(in.Tunnel)
	var missing []string
	c, err := docker.InspectContainer(ctx, name)
	switch {
	case dockerapi.IsNotFound(err):
		missing = append(missing, "the container "+name)
	case err != nil:
		return fmt.Errorf("cloudflare: inspect container %s: %w", name, engineCoded(err))
	case c.NetworkSettings.Networks[network.Name] == nil:
		missing = append(missing, "the endpoint of "+name+" on network "+network.Name)
	}
	if _, ok, err := claimFind(ctx, env.DB.Read(), in.Tunnel, network.Name, in.Claimant); err != nil {
		return err
	} else if !ok {
		missing = append(missing, "the claim of "+in.Claimant+" on network "+network.Name)
	}
	if len(missing) > 0 {
		return fmt.Errorf("cloudflare: the attach left no %s", strings.Join(missing, ", no "))
	}
	return nil
}

// Observe reads an endpoint from the Engine and a claim from the module's table, where no row is the null state.
func (n networkClaims) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	noun, identity, creation, err := effCreationOf(e.Resource)
	if err != nil {
		return core.Observation{}, err
	}
	switch noun {
	case nounEndpoint:
		return n.attachObserveEndpoint(ctx, identity, creation, effPhaseOf(e))
	case nounClaim:
	default:
		return core.Observation{}, fmt.Errorf("cloudflare: %q is not an effect of a network claim", e.Resource)
	}
	tunnel, network, claimant, err := claimIdentity(identity)
	if err != nil {
		return core.Observation{}, err
	}
	row, ok, err := claimFind(ctx, env.DB.Read(), tunnel, network, claimant)
	if err != nil {
		return core.Observation{}, err
	}
	if !ok {
		return effObservation(nil, "", creation), nil
	}
	doc, err := claimDocument(row)
	if err != nil {
		return core.Observation{}, err
	}
	return effObservation(doc, resourceOf(nounClaim, identity), creation), nil
}

// attachObserveEndpoint reads the connector's attachment to the network of that name; Docker keys a container's
// endpoints by network name. A missing container or attachment is the null state.
func (n networkClaims) attachObserveEndpoint(ctx context.Context, identity string, creation bool, phase dstate.Phase) (core.Observation, error) {
	tunnel, network, err := attachEndpointIdentity(identity)
	if err != nil {
		return core.Observation{}, err
	}
	name := connectorName(tunnel)
	c, err := n.c.a.docker.InspectContainer(ctx, name)
	var ep *dockerapi.EndpointSettings
	switch {
	case dockerapi.IsNotFound(err):
	case err != nil:
		return core.Observation{}, fmt.Errorf("cloudflare: inspect container %s: %w", name, engineCoded(err))
	default:
		ep = c.NetworkSettings.Networks[network]
	}
	if ep == nil {
		return effObservation(nil, "", creation), nil
	}
	doc, err := dstate.Endpoint(name, network, ep, phase)
	if err != nil {
		return core.Observation{}, fmt.Errorf("cloudflare: render endpoint %s@%s: %w", name, network, err)
	}
	return effObservation(doc, resourceOf(nounEndpoint, identity), creation), nil
}

// Inverse undoes one effect of an attach or a detach: it drops a claim this change added, restores a claim the change
// removed, disconnects the connector from a network it joined, or reconnects it with a dynamic address to one it left.
func (n networkClaims) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	noun, identity, _, err := effCreationOf(e.Resource)
	if err != nil {
		return core.InverseResult{}, err
	}
	switch {
	case noun == nounClaim && e.Kind == core.EffectCreate:
		return core.InverseResult{}, n.claimUndoAdd(ctx, env, e, identity)
	case noun == nounClaim && e.Kind == core.EffectDelete:
		return core.InverseResult{}, n.claimRestore(ctx, env, e, identity)
	case noun == nounEndpoint && e.Kind == core.EffectModify && e.InverseRecovery == nil:
		return core.InverseResult{}, n.attachUndoConnect(ctx, env, e, identity)
	case noun == nounEndpoint && e.Kind == core.EffectModify:
		return n.attachUndoDisconnect(ctx, identity)
	default:
		return core.InverseResult{}, fmt.Errorf("cloudflare: %s %s is not an effect of a network claim", e.Kind, e.Resource)
	}
}

// claimUndoAdd drops the claim e added. A claim no longer there is done; one another change holds is refused.
func (n networkClaims) claimUndoAdd(ctx context.Context, env core.Env, e core.Effect, identity string) error {
	tunnel, network, claimant, err := claimIdentity(identity)
	if err != nil {
		return err
	}
	return env.DB.Write(ctx, func(tx *sql.Tx) error {
		row, ok, err := claimFind(ctx, tx, tunnel, network, claimant)
		if err != nil || !ok {
			return err
		}
		if row.ChangeID != e.ChangeID {
			return core.Definite(errcode.New(errcode.InvalidInput, fmt.Sprintf(
				"the claim of %s on network %s for tunnel %s belongs to change %s, not %s; it is kept", claimant, network, tunnel, row.ChangeID, e.ChangeID)))
		}
		return deleteClaim(ctx, tx, tunnel, network, claimant)
	})
}

// claimRestore adds back the claim e removed, with its original change and preexisting mark. The same claim already
// there is done; a different one is refused.
func (n networkClaims) claimRestore(ctx context.Context, env core.Env, e core.Effect, identity string) error {
	tunnel, network, claimant, err := claimIdentity(identity)
	if err != nil {
		return err
	}
	var doc claimDoc
	if err := json.Unmarshal(e.Before.State, &doc); err != nil || doc.Tunnel != tunnel || doc.Network != network || doc.Claimant != claimant || doc.ChangeID == "" {
		return core.Definite(errcode.New(errcode.InvalidInput, fmt.Sprintf(
			"the journaled claim does not describe the claim of %s on network %s for tunnel %s", claimant, network, tunnel)))
	}
	want := claimRow{TunnelID: doc.Tunnel, Network: doc.Network, Claimant: doc.Claimant, ChangeID: doc.ChangeID, Preexisting: doc.Preexisting}
	return env.DB.Write(ctx, func(tx *sql.Tx) error {
		row, ok, err := claimFind(ctx, tx, tunnel, network, claimant)
		switch {
		case err != nil:
			return err
		case ok && row == want:
			return nil
		case ok:
			return core.Definite(errcode.New(errcode.InvalidInput, fmt.Sprintf(
				"network %s holds another claim of %s for tunnel %s, of change %s", network, claimant, tunnel, row.ChangeID)))
		}
		return insertClaim(ctx, tx, want)
	})
}

// attachUndoConnect disconnects the connector from the network e joined, refused while a claim of another change
// needs it. A connector or endpoint no longer there is done.
func (n networkClaims) attachUndoConnect(ctx context.Context, env core.Env, e core.Effect, identity string) error {
	tunnel, network, err := attachEndpointIdentity(identity)
	if err != nil {
		return err
	}
	name := connectorName(tunnel)
	all, err := connectorClaims(ctx, env.DB.Read(), tunnel, network)
	if err != nil {
		return err
	}
	for _, other := range all {
		if other.ChangeID != e.ChangeID {
			return core.Definite(errcode.New(errcode.InvalidInput, fmt.Sprintf(
				"network %s is still claimed by %s of change %s; %s stays on it", network, other.Claimant, other.ChangeID, name)))
		}
	}
	docker := n.c.a.docker
	// The Engine refuses to disconnect a container that is not on the network, so an undo run again checks first.
	c, err := docker.InspectContainer(ctx, name)
	switch {
	case dockerapi.IsNotFound(err):
		return nil
	case err != nil:
		return attachUndoError("inspect container "+name, err)
	case c.NetworkSettings.Networks[network] == nil:
		return nil
	}
	err = docker.DisconnectNetwork(ctx, network, name, false)
	if err == nil || dockerapi.IsNotFound(err) {
		return nil
	}
	return attachUndoError("disconnect "+name+" from network "+network, err)
}

// attachUndoDisconnect reconnects the connector to the network e left. The Engine assigns the address; a fixed address
// the endpoint had is not restored.
func (n networkClaims) attachUndoDisconnect(ctx context.Context, identity string) (core.InverseResult, error) {
	tunnel, network, err := attachEndpointIdentity(identity)
	if err != nil {
		return core.InverseResult{}, err
	}
	name := connectorName(tunnel)
	if err := n.c.a.docker.ConnectNetwork(ctx, network, name, nil); err != nil {
		return core.InverseResult{}, attachUndoError("connect "+name+" to network "+network, err)
	}
	return core.InverseResult{Resource: resourceOf(nounEndpoint, identity)}, nil
}

// attachUndoError is the error of a failed Engine call of an undo: a 4xx answer is a Definite refusal, as the Engine
// changed nothing, and an unreachable Engine is coded docker.engine_unavailable.
func attachUndoError(what string, err error) error {
	if refused(err) {
		return core.Definite(fmt.Errorf("cloudflare: %s: %w", what, err))
	}
	return fmt.Errorf("cloudflare: %s: %w", what, engineCoded(err))
}
