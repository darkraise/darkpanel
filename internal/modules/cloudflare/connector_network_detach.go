package cloudflare

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// networkDetach is cloudflare.network.detach: it releases a claimant's claims on the tunnel's connector and takes the
// connector off a network only when no claim is left there and the connector was not on it before the first claim.
func (c cf) networkDetach() core.ActionDef {
	return core.DefineMutating[NetworkDetachInput, NetworkAttachment](core.ActionSpec{
		Name:      ID + ".network.detach",
		Summary:   "Release a claimant's network claims on the tunnel's connector.",
		Version:   1,
		Class:     core.ClassCompensable,
		Side:      core.SideAgent,
		LockKeys:  []string{"cloudflare"},
		Converges: true,
	}, networkDetachHandler{networkClaims{c: c}})
}

// networkDetachHandler is the handler of cloudflare.network.detach. Its Observe and Inverse are those of networkClaims.
type networkDetachHandler struct{ networkClaims }

// detachHashDoc is what a plan's state hash covers of one network a claim is removed from.
type detachHashDoc struct {
	NetworkID  string            `json:"networkId"`
	EndpointID string            `json:"endpointId"`
	Tunnel     string            `json:"tunnel"`
	Claims     []attachHashClaim `json:"claims"`
}

// detachStep is what a detach does with one claim it removes.
type detachStep struct {
	claim      claimRow
	others     []string // the other claimants of the network, ordered
	networkID  string   // "" when the network no longer exists
	attached   bool     // the connector is on the network now
	disconnect bool
	hash       detachHashDoc
}

// detachDecision is what a detach found and decided, its steps in network-name order.
type detachDecision struct {
	containerID string // "" when the connector container does not exist
	steps       []detachStep
}

// detachClaimsOf returns the claims of claimant for the tunnel on the network, or on every network when it is "",
// ordered by network.
func detachClaimsOf(ctx context.Context, q queryer, tunnel, network, claimant string) ([]claimRow, error) {
	all, err := connectorClaims(ctx, q, tunnel, network)
	if err != nil {
		return nil, err
	}
	var mine []claimRow
	for _, c := range all {
		if c.Claimant == claimant {
			mine = append(mine, c)
		}
	}
	return mine, nil
}

// detachInvalid is the Definite invalid_input refusal of every bad field of in, nil when there is none.
func detachInvalid(in NetworkDetachInput) error {
	var fields []errcode.FieldError
	if p := tunnelIDProblem(in.Tunnel); p != "" {
		fields = append(fields, errcode.FieldError{Path: "/tunnel", Message: p})
	}
	if in.Network != "" && !networkNamePattern.MatchString(in.Network) {
		fields = append(fields, errcode.FieldError{Path: "/network", Message: "network must be a Docker network name"})
	}
	if p := claimantProblem(in.Claimant); p != "" {
		fields = append(fields, errcode.FieldError{Path: "/claimant", Message: p})
	}
	if len(fields) > 0 {
		return attachInvalid(fields)
	}
	return nil
}

// detachClaimsNamed returns the claimant's claims the input names: on the network, or on every network when it is "".
// An attach records a claim under the network's name, so a network named by id is resolved through the Engine; the
// Engine is asked only when the claimant holds a claim of the tunnel that the input's text does not name.
func (h networkDetachHandler) detachClaimsNamed(ctx context.Context, q queryer, in NetworkDetachInput) ([]claimRow, error) {
	mine, err := detachClaimsOf(ctx, q, in.Tunnel, in.Network, in.Claimant)
	if err != nil || len(mine) > 0 || in.Network == "" {
		return mine, err
	}
	held, err := detachClaimsOf(ctx, q, in.Tunnel, "", in.Claimant)
	if err != nil || len(held) == 0 {
		return nil, err
	}
	network, err := h.c.a.docker.InspectNetwork(ctx, in.Network)
	switch {
	case dockerapi.IsNotFound(err):
		return nil, nil
	case err != nil:
		return nil, attachEngine("inspect network "+in.Network, err)
	}
	for _, c := range held {
		if c.Network == network.Name {
			mine = append(mine, c)
		}
	}
	return mine, nil
}

// detachCheck decides everything a detach does and changes nothing; every failure is Definite. A missing connector
// container or network counts as the connector not being on the network.
func (h networkDetachHandler) detachCheck(ctx context.Context, env core.Env, in NetworkDetachInput) (detachDecision, error) {
	if err := detachInvalid(in); err != nil {
		return detachDecision{}, err
	}

	db := env.DB.Read()
	removed, err := h.detachClaimsNamed(ctx, db, in)
	if err != nil {
		return detachDecision{}, core.Definite(err)
	}
	if len(removed) == 0 {
		return detachDecision{}, nil
	}
	docker := h.c.a.docker
	name := connectorName(in.Tunnel)
	var d detachDecision
	c, err := docker.InspectContainer(ctx, name)
	switch {
	case dockerapi.IsNotFound(err):
	case err != nil:
		return detachDecision{}, attachEngine("inspect container "+name, err)
	default:
		d.containerID = c.ID
	}
	for _, claim := range removed {
		all, err := connectorClaims(ctx, db, in.Tunnel, claim.Network)
		if err != nil {
			return detachDecision{}, core.Definite(err)
		}
		s := detachStep{claim: claim}
		for _, other := range all {
			if other.Claimant != claim.Claimant {
				s.others = append(s.others, other.Claimant)
			}
		}
		network, err := docker.InspectNetwork(ctx, claim.Network)
		switch {
		case dockerapi.IsNotFound(err):
		case err != nil:
			return detachDecision{}, attachEngine("inspect network "+claim.Network, err)
		default:
			s.networkID = network.ID
		}
		var ep *dockerapi.EndpointSettings
		if d.containerID != "" && s.networkID != "" {
			ep = c.NetworkSettings.Networks[claim.Network]
		}
		s.attached = ep != nil
		s.disconnect = len(s.others) == 0 && !claim.Preexisting && s.attached
		s.hash = detachHashDoc{NetworkID: s.networkID, Tunnel: in.Tunnel, Claims: make([]attachHashClaim, len(all))}
		if ep != nil {
			s.hash.EndpointID = ep.EndpointID
		}
		for i, a := range all {
			s.hash.Claims[i] = attachHashClaim{Claimant: a.Claimant, ChangeID: a.ChangeID, Preexisting: a.Preexisting}
		}
		d.steps = append(d.steps, s)
	}
	return d, nil
}

// Plan shows each claim the detach removes and whether the connector leaves its network. The hash covers every
// network involved as an attach's plan covers its network.
func (h networkDetachHandler) Plan(ctx context.Context, env core.Env, in NetworkDetachInput) (core.PlanOutcome, error) {
	d, err := h.detachCheck(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	name := connectorName(in.Tunnel)
	var diff []core.DiffLine
	docs := make([]detachHashDoc, 0, len(d.steps))
	for _, s := range d.steps {
		network := s.claim.Network
		diff = append(diff, core.DiffLine{Op: "-", Text: "claim of " + s.claim.Claimant + " on network " + network})
		switch {
		case s.disconnect:
			diff = append(diff, core.DiffLine{Op: "-", Text: "container " + name + " leaves network " + network})
		case len(s.others) > 0:
			diff = append(diff, core.DiffLine{Op: " ", Text: name + " stays on network " + network + ": claimed by " + strings.Join(s.others, ", ")})
		case s.claim.Preexisting:
			diff = append(diff, core.DiffLine{Op: " ", Text: name + " stays on network " + network + ": it was there before"})
		}
		docs = append(docs, s.hash)
	}
	b, err := json.Marshal(docs)
	if err != nil {
		return core.PlanOutcome{}, fmt.Errorf("cloudflare: render the plan state: %w", err)
	}
	sum := sha256.Sum256(b)
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{}, StateHash: hex.EncodeToString(sum[:])}, nil
}

// Apply handles the claims in network-name order, each disconnect before its claim removal, so a failed disconnect
// keeps the claim and applying again finishes the work.
func (h networkDetachHandler) Apply(ctx context.Context, env core.Env, in NetworkDetachInput) (NetworkAttachment, error) {
	d, err := h.detachCheck(ctx, env, in)
	if err != nil {
		return NetworkAttachment{}, err
	}
	name := connectorName(in.Tunnel)
	out := NetworkAttachment{Tunnel: in.Tunnel, Network: in.Network, Claimant: in.Claimant}
	for _, s := range d.steps {
		network := s.claim.Network
		if s.disconnect {
			resource := resourceOf(nounEndpoint, in.Tunnel+"|"+network)
			obs, err := h.Observe(ctx, env, core.Effect{Kind: core.EffectModify, Resource: resource, State: core.EffectIntended})
			if err != nil {
				return NetworkAttachment{}, core.Definite(fmt.Errorf("cloudflare: observe %s: %w", resource, err))
			}
			e := connectorEffect(core.EffectModify, resource, obs.Current.State, engineCommitWait)
			e.InverseRecovery = &core.RecoveryID{Kind: "name", Value: name + "@" + network}
			err = h.c.journal(ctx, env, e, h.Observe, func(ctx context.Context) (string, error) {
				if err := h.c.a.docker.DisconnectNetwork(ctx, s.networkID, d.containerID, false); err != nil {
					return "", fmt.Errorf("cloudflare: disconnect %s from network %s: %w", name, network, engineCoded(err))
				}
				return resource, nil
			})
			if err != nil {
				return NetworkAttachment{}, err
			}
		}
		doc, err := claimDocument(s.claim)
		if err != nil {
			return NetworkAttachment{}, err
		}
		e := connectorEffect(core.EffectDelete, resourceOf(nounClaim, in.Tunnel+"|"+network+"|"+s.claim.Claimant), doc, 0)
		e.Local, e.InverseLocal = true, true
		null := nullState()
		e.After = &null
		err = h.c.journal(ctx, env, e, h.Observe, func(ctx context.Context) (string, error) {
			return "", env.DB.Write(ctx, func(tx *sql.Tx) error { return deleteClaim(ctx, tx, in.Tunnel, network, s.claim.Claimant) })
		})
		if err != nil {
			return NetworkAttachment{}, err
		}
		out.Preexisting = out.Preexisting || s.claim.Preexisting
		out.Attached = out.Attached || (s.attached && !s.disconnect)
	}
	return out, nil
}

// Verify checks from the host alone that the claimant holds no claim for the tunnel on the network, or on any network
// when the input names none.
func (h networkDetachHandler) Verify(ctx context.Context, env core.Env, in NetworkDetachInput, _ NetworkAttachment) error {
	if err := detachInvalid(in); err != nil {
		return err
	}
	left, err := h.detachClaimsNamed(ctx, env.DB.Read(), in)
	if err != nil {
		return err
	}
	switch {
	case len(left) == 1:
		return fmt.Errorf("cloudflare: 1 claim of %s for tunnel %s is still recorded, on network %s", in.Claimant, in.Tunnel, left[0].Network)
	case len(left) > 1:
		return fmt.Errorf("cloudflare: %d claims of %s for tunnel %s are still recorded, the first on network %s", len(left), in.Claimant, in.Tunnel, left[0].Network)
	}
	return nil
}
