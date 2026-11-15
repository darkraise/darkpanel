package npm

import (
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
	"github.com/darkraise/darkpanel/internal/modules/npm/proxy"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// networkNamePattern is the form of a Docker network or container name the claims accept. It keeps "|" out of a
// network name, so a claim identity "<network>|<claimant>" splits at its first "|".
var networkNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// networkAttach is npm.network.attach: it joins the NPM container to a network so it can reach a target by name, and
// records the claimant that needs the attachment.
func (n nm) networkAttach() core.ActionDef {
	return core.DefineMutating[AttachInput, Attachment](core.ActionSpec{
		Name:         "npm.network.attach",
		Summary:      "Attach Nginx Proxy Manager to a network.",
		Version:      1,
		Class:        core.ClassCompensable,
		Side:         core.SideAgent,
		LockKeys:     []string{lockKey},
		ApplyTimeout: 45 * time.Second,
	}, networkAttachHandler{networkClaims{n: n}})
}

// networkDetach is npm.network.detach: it drops a claim and leaves the network only when no claim is left and NPM was
// not there before the first claim.
func (n nm) networkDetach() core.ActionDef {
	return core.DefineMutating[DetachInput, Attachment](core.ActionSpec{
		Name:      "npm.network.detach",
		Summary:   "Release an attachment claim.",
		Version:   1,
		Class:     core.ClassCompensable,
		Side:      core.SideAgent,
		LockKeys:  []string{lockKey},
		Converges: true,
		// The undo of a detach runs the attach's join: a forward guard, two reloads, a second listing and a disconnect.
		ApplyTimeout: 45 * time.Second,
	}, networkDetachHandler{networkClaims{n: n}})
}

// networkClaims holds what the attach and detach handlers share: the observation and the undo of their effects.
type networkClaims struct{ n nm }

// networkAttachHandler is the handler of npm.network.attach.
type networkAttachHandler struct{ networkClaims }

// networkDetachHandler is the handler of npm.network.detach.
type networkDetachHandler struct{ networkClaims }

// claimDoc is the journaled state of a claim.
type claimDoc struct {
	Network     string `json:"network"`
	Claimant    string `json:"claimant"`
	Preexisting bool   `json:"preexisting"`
	ChangeID    string `json:"changeId"`
}

// claimDocument renders a claim as its journaled state.
func claimDocument(c claimRow) (json.RawMessage, error) {
	doc, err := json.Marshal(claimDoc{Network: c.Network, Claimant: c.Claimant, Preexisting: c.Preexisting, ChangeID: c.ChangeID})
	if err != nil {
		return nil, fmt.Errorf("npm: render the claim of %s on %s: %w", c.Claimant, c.Network, err)
	}
	return doc, nil
}

// claimFind returns the claim of claimant on network; ok is false when there is none.
func claimFind(ctx context.Context, q queryer, network, claimant string) (claimRow, bool, error) {
	all, err := claims(ctx, q, network)
	if err != nil {
		return claimRow{}, false, err
	}
	for _, c := range all {
		if c.Network == network && c.Claimant == claimant {
			return c, true, nil
		}
	}
	return claimRow{}, false, nil
}

// claimsOfClaimant returns every claim of claimant ordered by network.
func claimsOfClaimant(ctx context.Context, q queryer, claimant string) ([]claimRow, error) {
	rows, err := q.QueryContext(ctx, "SELECT network, claimant, change_id, preexisting FROM npm_attachments WHERE claimant = ? ORDER BY network", claimant)
	if err != nil {
		return nil, fmt.Errorf("npm: read the claims of %s: %w", claimant, err)
	}
	defer func() { _ = rows.Close() }()
	out := []claimRow{}
	for rows.Next() {
		var c claimRow
		if err := rows.Scan(&c.Network, &c.Claimant, &c.ChangeID, &c.Preexisting); err != nil {
			return nil, fmt.Errorf("npm: read the claims of %s: %w", claimant, err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("npm: read the claims of %s: %w", claimant, err)
	}
	return out, nil
}

// claimantProblem says what is wrong with a claimant, or "" when it is acceptable.
func claimantProblem(claimant string) string {
	if claimant == "" || len(claimant) > 255 {
		return "claimant must be 1 to 255 bytes"
	}
	if strings.IndexFunc(claimant, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return "claimant must not contain whitespace or control characters"
	}
	return ""
}

// networkInvalid is the Definite invalid_input refusal of the fields.
func networkInvalid(fields []errcode.FieldError) error {
	msgs := make([]string, len(fields))
	for i, f := range fields {
		msgs[i] = f.Message
	}
	return core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: strings.Join(msgs, "; "), Fields: fields})
}

// networkEngine marks a failed Engine read Definite, as the Engine changed nothing, with an unreachable Engine coded
// docker.engine_unavailable.
func networkEngine(what string, err error) error {
	return core.Definite(fmt.Errorf("npm: %s: %w", what, engineCoded(err)))
}

// networkUserDefined reports whether a network name is not one of the Engine's predefined networks.
func networkUserDefined(name string) bool {
	return name != "bridge" && name != "host" && name != "none"
}

// networkEndpointOf returns the container's endpoint on the network with the id, or nil when it is not attached.
func networkEndpointOf(c dockerapi.ContainerJSON, networkID string) *dockerapi.EndpointSettings {
	for _, ep := range c.NetworkSettings.Networks {
		if ep != nil && ep.NetworkID == networkID {
			return ep
		}
	}
	return nil
}

// networkHashClaim is a claim as a plan's state hash covers it.
type networkHashClaim struct {
	Claimant    string `json:"claimant"`
	ChangeID    string `json:"changeId"`
	Preexisting bool   `json:"preexisting"`
}

// networkHashDoc is what a plan's state hash covers of one network.
type networkHashDoc struct {
	NetworkID  string             `json:"networkId"`
	EndpointID string             `json:"endpointId"`
	TargetID   string             `json:"targetId"`
	Claims     []networkHashClaim `json:"claims"`
}

// networkHashDocOf is the hashed form of a network, NPM's endpoint on it (nil when absent), the target's id and the
// network's claims ordered by claimant.
func networkHashDocOf(networkID string, ep *dockerapi.EndpointSettings, targetID string, all []claimRow) networkHashDoc {
	doc := networkHashDoc{NetworkID: networkID, TargetID: targetID, Claims: make([]networkHashClaim, len(all))}
	if ep != nil {
		doc.EndpointID = ep.EndpointID
	}
	for i, c := range all {
		doc.Claims[i] = networkHashClaim{Claimant: c.Claimant, ChangeID: c.ChangeID, Preexisting: c.Preexisting}
	}
	return doc
}

// networkStateHash is the hex SHA-256 of v's JSON form.
func networkStateHash(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("npm: render the plan state: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// networkAttachCheck is what an attach found and decided.
type networkAttachCheck struct {
	network     dockerapi.Network
	container   dockerapi.ContainerJSON
	endpoint    *dockerapi.EndpointSettings // NPM's endpoint on the network; nil when not attached
	targetID    string
	claims      []claimRow // the network's claims, ordered by claimant
	connect     bool
	claim       bool
	preexisting bool
}

// validate refuses an attach input of the wrong shape, naming every bad field.
func (h networkAttachHandler) validate(in AttachInput) error {
	var fields []errcode.FieldError
	if !networkNamePattern.MatchString(in.Network) {
		fields = append(fields, errcode.FieldError{Path: "/network", Message: "network must be a Docker network name"})
	}
	if msg := claimantProblem(in.Claimant); msg != "" {
		fields = append(fields, errcode.FieldError{Path: "/claimant", Message: msg})
	}
	if in.Target != "" && !networkNamePattern.MatchString(in.Target) {
		fields = append(fields, errcode.FieldError{Path: "/target", Message: "target must be a Docker container name"})
	}
	if in.Address.IsValid() && !in.Address.Is4() {
		fields = append(fields, errcode.FieldError{Path: "/address", Message: "address must be an IPv4 address"})
	}
	if len(fields) > 0 {
		return networkInvalid(fields)
	}
	return nil
}

// targetEndpoints builds the endpoints the supported-target rule reads: the target's networks, or without a target
// only the network itself.
func (h networkAttachHandler) targetEndpoints(ctx context.Context, in AttachInput, network dockerapi.Network) (map[string]proxy.Endpoint, string, error) {
	if in.Target == "" {
		return map[string]proxy.Endpoint{network.Name: {Driver: network.Driver, UserDefined: networkUserDefined(network.Name)}}, "", nil
	}
	docker := h.n.d.docker
	target, err := docker.InspectContainer(ctx, in.Target)
	if dockerapi.IsNotFound(err) {
		return nil, "", core.Definite(errcode.New(errcode.NotFound, "container "+in.Target+" does not exist"))
	}
	if err != nil {
		return nil, "", networkEngine("inspect container "+in.Target, err)
	}
	listed, err := docker.ListNetworks(ctx, nil)
	if err != nil {
		return nil, "", networkEngine("list networks", err)
	}
	drivers := make(map[string]string, len(listed))
	for _, l := range listed {
		drivers[l.ID] = l.Driver
	}
	endpoints := make(map[string]proxy.Endpoint, len(target.NetworkSettings.Networks))
	for name, ep := range target.NetworkSettings.Networks {
		driver := ""
		if ep != nil {
			driver = drivers[ep.NetworkID]
		}
		endpoints[name] = proxy.Endpoint{Driver: driver, UserDefined: networkUserDefined(name)}
	}
	return endpoints, target.ID, nil
}

// check decides everything an attach does and changes nothing; every failure is Definite. guard runs the forward
// guard of a join; Apply runs its own after the reload before the join instead.
func (h networkAttachHandler) check(ctx context.Context, env core.Env, in AttachInput, guard bool) (networkAttachCheck, error) {
	if err := h.validate(in); err != nil {
		return networkAttachCheck{}, err
	}
	if _, ok, err := loadInstance(ctx, env.DB.Read()); err != nil {
		return networkAttachCheck{}, core.Definite(err)
	} else if !ok {
		return networkAttachCheck{}, core.Definite(errcode.New(errcode.NotFound, "no Nginx Proxy Manager instance is deployed on this host"))
	}
	docker := h.n.d.docker
	c, err := docker.InspectContainer(ctx, containerName)
	if dockerapi.IsNotFound(err) {
		return networkAttachCheck{}, core.Definite(errcode.New(errcode.NPMUnhealthy, "the NPM container "+containerName+" does not exist"))
	}
	if err != nil {
		return networkAttachCheck{}, networkEngine("inspect container "+containerName, err)
	}
	network, err := docker.InspectNetwork(ctx, in.Network)
	if dockerapi.IsNotFound(err) {
		return networkAttachCheck{}, core.Definite(errcode.New(errcode.NotFound, "network "+in.Network+" does not exist"))
	}
	if err != nil {
		return networkAttachCheck{}, networkEngine("inspect network "+in.Network, err)
	}
	endpoints, targetID, err := h.targetEndpoints(ctx, in, network)
	if err != nil {
		return networkAttachCheck{}, err
	}
	if err := proxy.Target(endpoints, network.Name); err != nil {
		return networkAttachCheck{}, core.Definite(err)
	}
	ep := networkEndpointOf(c, network.ID)
	if in.Address.IsValid() && ep != nil {
		has := ep.IPAddress
		if ep.IPAMConfig != nil && ep.IPAMConfig.IPv4Address.IsValid() {
			has = ep.IPAMConfig.IPv4Address
		}
		if has != in.Address {
			msg := fmt.Sprintf("%s is already on network %s with address %s, not %s", containerName, network.Name, has, in.Address)
			return networkAttachCheck{}, networkInvalid([]errcode.FieldError{{Path: "/address", Message: msg}})
		}
	}
	all, err := claims(ctx, env.DB.Read(), network.Name)
	if err != nil {
		return networkAttachCheck{}, core.Definite(err)
	}
	ck := networkAttachCheck{network: network, container: c, endpoint: ep, targetID: targetID, claims: all, connect: ep == nil, claim: true}
	// Only a join can hand NPM a new address: the addresses it holds are already reserved against proxy hosts.
	if ck.connect && guard {
		if err := h.n.forwardGuard(ctx, env, network, in.Address); err != nil {
			return networkAttachCheck{}, err
		}
	}
	// Claims that disagree on the mark take the answer under which a detach never removes NPM from a network it may
	// have been on before the first claim.
	ck.preexisting = len(all) == 0 && ep != nil
	for _, existing := range all {
		ck.preexisting = ck.preexisting || existing.Preexisting
	}
	for _, existing := range all {
		if existing.Claimant == in.Claimant {
			ck.claim, ck.preexisting = false, existing.Preexisting
		}
	}
	return ck, nil
}

// Plan shows the join and the claim an attach adds. The hash covers the network, NPM's endpoint on it, the target and
// the network's claims.
func (h networkAttachHandler) Plan(ctx context.Context, env core.Env, in AttachInput) (core.PlanOutcome, error) {
	ck, err := h.check(ctx, env, in, true)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	name := ck.network.Name
	var diff []core.DiffLine
	if ck.connect {
		diff = append(diff, core.DiffLine{Op: "+", Text: "container " + containerName + " joins network " + name})
		if in.Address.IsValid() {
			diff = append(diff, core.DiffLine{Op: "+", Text: "ipv4 " + in.Address.String()})
		}
	}
	if ck.claim {
		diff = append(diff, core.DiffLine{Op: "+", Text: "claim of " + in.Claimant + " on network " + name})
		if ck.preexisting && len(ck.claims) == 0 {
			diff = append(diff, core.DiffLine{Op: " ", Text: containerName + " is already on network " + name + "; detaching leaves it there"})
		}
	}
	hash, err := networkStateHash(networkHashDocOf(ck.network.ID, ck.endpoint, ck.targetID, ck.claims))
	if err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{}, StateHash: hash}, nil
}

// Apply connects NPM to the network when it is not there and records the claim when the claimant has none. A join
// reloads nginx, reads the network and the forward targets again and only then connects. NPM already on a network that
// has no claim, which an apply cut off after its connect leaves, is checked as after a fresh join.
func (h networkAttachHandler) Apply(ctx context.Context, env core.Env, in AttachInput) (Attachment, error) {
	ck, err := h.check(ctx, env, in, false)
	if err != nil {
		return Attachment{}, err
	}
	name := ck.network.Name
	if ck.connect {
		resource := resourceOf(nounEndpoint, name)
		obs, err := h.n.observeEngine(ctx, env, core.Effect{Kind: core.EffectModify, Resource: resource, State: core.EffectIntended})
		if err != nil {
			return Attachment{}, err
		}
		var settings *dockerapi.EndpointSettings
		if in.Address.IsValid() {
			settings = &dockerapi.EndpointSettings{IPAMConfig: &dockerapi.EndpointIPAMConfig{IPv4Address: in.Address}}
		}
		if err := h.reloadBeforeJoin(ctx, env, name); err != nil {
			return Attachment{}, err
		}
		targets, err := h.joinGuard(ctx, env, ck.network.ID, in.Address)
		if err != nil {
			return Attachment{}, err
		}
		e := npmEffect(core.EffectModify, resource, obs.Current.State, commitWait)
		err = h.n.run(ctx, env, e, h.n.observeEngine, func(ctx context.Context) (string, error) {
			if err := h.n.d.docker.ConnectNetwork(ctx, ck.network.ID, ck.container.ID, settings); err != nil {
				connectErr := fmt.Errorf("npm: connect %s to network %s: %w", containerName, name, engineCoded(err))
				if !refused(err) {
					return "", h.lostJoin(ctx, env, name, targets, connectErr)
				}
				// A refusal with NPM on the network is a join made in between, checked as this one would be.
				if on, inspectErr := h.npmOnNetwork(ctx, name); inspectErr != nil || !on {
					return "", connectErr
				}
			}
			if err := h.afterJoin(ctx, env, name, targets); err != nil {
				return "", err
			}
			return resource, nil
		})
		if err != nil {
			return Attachment{}, err
		}
	} else if len(ck.claims) == 0 {
		targets, err := h.n.forwardTargets(ctx, env)
		if err != nil {
			return Attachment{}, core.Definite(err)
		}
		if err := h.afterJoin(ctx, env, name, targets); err != nil {
			return Attachment{}, err
		}
	}
	if ck.claim {
		row := claimRow{Network: name, Claimant: in.Claimant, ChangeID: env.ChangeID, Preexisting: ck.preexisting}
		doc, err := claimDocument(row)
		if err != nil {
			return Attachment{}, err
		}
		e := npmEffect(core.EffectCreate, resourceOf(nounClaim, "?"+name+"|"+in.Claimant), nullState().State, 0)
		e.Local, e.InverseLocal = true, true
		e.After = &core.StateDoc{State: doc}
		err = h.n.run(ctx, env, e, h.Observe, func(ctx context.Context) (string, error) {
			return resourceOf(nounClaim, name+"|"+in.Claimant), env.DB.Write(ctx, func(tx *sql.Tx) error { return addClaim(ctx, tx, row) })
		})
		if err != nil {
			return Attachment{}, err
		}
	}
	return Attachment{Network: name, Claimant: in.Claimant, Preexisting: ck.preexisting, Attached: true}, nil
}

// Verify checks from the host alone that NPM is on the network and the claimant holds a claim on it.
func (h networkAttachHandler) Verify(ctx context.Context, env core.Env, in AttachInput, _ Attachment) error {
	docker := h.n.d.docker
	network, err := docker.InspectNetwork(ctx, in.Network)
	if dockerapi.IsNotFound(err) {
		return fmt.Errorf("npm: network %s does not exist", in.Network)
	}
	if err != nil {
		return fmt.Errorf("npm: inspect network %s: %w", in.Network, engineCoded(err))
	}
	var missing []string
	c, err := docker.InspectContainer(ctx, containerName)
	switch {
	case dockerapi.IsNotFound(err):
		missing = append(missing, "the container "+containerName)
	case err != nil:
		return fmt.Errorf("npm: inspect container %s: %w", containerName, engineCoded(err))
	case networkEndpointOf(c, network.ID) == nil:
		missing = append(missing, "the endpoint of "+containerName+" on network "+network.Name)
	}
	if _, ok, err := claimFind(ctx, env.DB.Read(), network.Name, in.Claimant); err != nil {
		return err
	} else if !ok {
		missing = append(missing, "the claim of "+in.Claimant+" on network "+network.Name)
	}
	if len(missing) > 0 {
		return fmt.Errorf("npm: the attach left no %s", strings.Join(missing, ", no "))
	}
	return nil
}

// networkDetachStep is what a detach does with one claim it removes.
type networkDetachStep struct {
	claim      claimRow
	others     []string // the other claimants of the network, ordered
	networkID  string   // "" when the network no longer exists
	attached   bool     // NPM is on the network now
	disconnect bool
	hash       networkHashDoc
}

// networkDetachCheck is what a detach found and decided, its steps in network-name order.
type networkDetachCheck struct {
	containerID string // "" when the NPM container does not exist
	steps       []networkDetachStep
}

// check decides everything a detach does and changes nothing; every failure is Definite. A missing NPM container or
// network counts as NPM not being on the network.
func (h networkDetachHandler) check(ctx context.Context, env core.Env, in DetachInput) (networkDetachCheck, error) {
	var fields []errcode.FieldError
	if in.Network != "" && !networkNamePattern.MatchString(in.Network) {
		fields = append(fields, errcode.FieldError{Path: "/network", Message: "network must be a Docker network name"})
	}
	if msg := claimantProblem(in.Claimant); msg != "" {
		fields = append(fields, errcode.FieldError{Path: "/claimant", Message: msg})
	}
	if len(fields) > 0 {
		return networkDetachCheck{}, networkInvalid(fields)
	}
	db := env.DB.Read()
	var removed []claimRow
	if in.Network != "" {
		c, ok, err := claimFind(ctx, db, in.Network, in.Claimant)
		if err != nil {
			return networkDetachCheck{}, core.Definite(err)
		}
		if ok {
			removed = append(removed, c)
		}
	} else {
		var err error
		if removed, err = claimsOfClaimant(ctx, db, in.Claimant); err != nil {
			return networkDetachCheck{}, core.Definite(err)
		}
	}
	if len(removed) == 0 {
		return networkDetachCheck{}, nil
	}
	docker := h.n.d.docker
	var ck networkDetachCheck
	c, err := docker.InspectContainer(ctx, containerName)
	switch {
	case dockerapi.IsNotFound(err):
	case err != nil:
		return networkDetachCheck{}, networkEngine("inspect container "+containerName, err)
	default:
		ck.containerID = c.ID
	}
	for _, claim := range removed {
		all, err := claims(ctx, db, claim.Network)
		if err != nil {
			return networkDetachCheck{}, core.Definite(err)
		}
		s := networkDetachStep{claim: claim}
		for _, other := range all {
			if other.Claimant != claim.Claimant {
				s.others = append(s.others, other.Claimant)
			}
		}
		network, err := docker.InspectNetwork(ctx, claim.Network)
		switch {
		case dockerapi.IsNotFound(err):
		case err != nil:
			return networkDetachCheck{}, networkEngine("inspect network "+claim.Network, err)
		default:
			s.networkID = network.ID
		}
		var ep *dockerapi.EndpointSettings
		if ck.containerID != "" && s.networkID != "" {
			ep = networkEndpointOf(c, s.networkID)
		}
		s.attached = ep != nil
		s.disconnect = len(s.others) == 0 && !claim.Preexisting && s.attached
		s.hash = networkHashDocOf(s.networkID, ep, "", all)
		ck.steps = append(ck.steps, s)
	}
	return ck, nil
}

// Plan shows each claim the detach removes and whether NPM leaves its network. The hash covers every network
// involved as an attach's plan covers its network.
func (h networkDetachHandler) Plan(ctx context.Context, env core.Env, in DetachInput) (core.PlanOutcome, error) {
	ck, err := h.check(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	var diff []core.DiffLine
	docs := make([]networkHashDoc, 0, len(ck.steps))
	for _, s := range ck.steps {
		name := s.claim.Network
		diff = append(diff, core.DiffLine{Op: "-", Text: "claim of " + s.claim.Claimant + " on network " + name})
		switch {
		case s.disconnect:
			diff = append(diff, core.DiffLine{Op: "-", Text: "container " + containerName + " leaves network " + name})
		case len(s.others) > 0:
			diff = append(diff, core.DiffLine{Op: " ", Text: containerName + " stays on network " + name + ": claimed by " + strings.Join(s.others, ", ")})
		case s.claim.Preexisting:
			diff = append(diff, core.DiffLine{Op: " ", Text: containerName + " stays on network " + name + ": it was there before"})
		}
		docs = append(docs, s.hash)
	}
	hash, err := networkStateHash(docs)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{}, StateHash: hash}, nil
}

// Apply handles the claims in network-name order, each disconnect before its claim removal, so a failed disconnect
// keeps the claim and applying again finishes the work.
func (h networkDetachHandler) Apply(ctx context.Context, env core.Env, in DetachInput) (Attachment, error) {
	ck, err := h.check(ctx, env, in)
	if err != nil {
		return Attachment{}, err
	}
	out := Attachment{Network: in.Network, Claimant: in.Claimant}
	for _, s := range ck.steps {
		name := s.claim.Network
		if s.disconnect {
			resource := resourceOf(nounEndpoint, name)
			obs, err := h.n.observeEngine(ctx, env, core.Effect{Kind: core.EffectModify, Resource: resource, State: core.EffectIntended})
			if err != nil {
				return Attachment{}, err
			}
			e := npmEffect(core.EffectModify, resource, obs.Current.State, commitWait)
			e.InverseRecovery = &core.RecoveryID{Kind: "name", Value: containerName + "@" + name}
			err = h.n.run(ctx, env, e, h.n.observeEngine, func(ctx context.Context) (string, error) {
				if err := h.n.d.docker.DisconnectNetwork(ctx, s.networkID, ck.containerID, false); err != nil {
					return "", fmt.Errorf("npm: disconnect %s from network %s: %w", containerName, name, engineCoded(err))
				}
				return resource, nil
			})
			if err != nil {
				return Attachment{}, err
			}
		}
		doc, err := claimDocument(s.claim)
		if err != nil {
			return Attachment{}, err
		}
		e := npmEffect(core.EffectDelete, resourceOf(nounClaim, name+"|"+s.claim.Claimant), doc, 0)
		e.Local, e.InverseLocal = true, true
		null := nullState()
		e.After = &null
		err = h.n.run(ctx, env, e, h.Observe, func(ctx context.Context) (string, error) {
			return "", env.DB.Write(ctx, func(tx *sql.Tx) error { return dropClaim(ctx, tx, name, s.claim.Claimant) })
		})
		if err != nil {
			return Attachment{}, err
		}
		out.Preexisting = out.Preexisting || s.claim.Preexisting
		out.Attached = out.Attached || (s.attached && !s.disconnect)
	}
	return out, nil
}

// Verify checks from the host alone that the claimant holds no claim on the network, or on any network when the
// input names none.
func (h networkDetachHandler) Verify(ctx context.Context, env core.Env, in DetachInput, _ Attachment) error {
	db := env.DB.Read()
	if in.Network != "" {
		if _, ok, err := claimFind(ctx, db, in.Network, in.Claimant); err != nil {
			return err
		} else if ok {
			return fmt.Errorf("npm: the claim of %s on network %s is still recorded", in.Claimant, in.Network)
		}
		return nil
	}
	left, err := claimsOfClaimant(ctx, db, in.Claimant)
	if err != nil {
		return err
	}
	if len(left) > 0 {
		return fmt.Errorf("npm: %d claims of %s are still recorded, the first on network %s", len(left), in.Claimant, left[0].Network)
	}
	return nil
}

// claimIdentity splits a claim identity "<network>|<claimant>".
func claimIdentity(identity string) (network, claimant string, err error) {
	network, claimant, ok := strings.Cut(identity, "|")
	if !ok || network == "" || claimant == "" {
		return "", "", fmt.Errorf("npm: %q is not a claim identity", identity)
	}
	return network, claimant, nil
}

// Observe reads an endpoint from the Engine and a claim from the module's table, where no row is the null state.
func (c networkClaims) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	noun, name, creation, err := creationOf(e.Resource)
	if err != nil {
		return core.Observation{}, err
	}
	switch noun {
	case nounEndpoint:
		return c.n.observeEngine(ctx, env, e)
	case nounClaim:
	default:
		return core.Observation{}, fmt.Errorf("npm: %q is not an effect of a network claim", e.Resource)
	}
	network, claimant, err := claimIdentity(name)
	if err != nil {
		return core.Observation{}, err
	}
	row, ok, err := claimFind(ctx, env.DB.Read(), network, claimant)
	if err != nil {
		return core.Observation{}, err
	}
	obs := named(nullState().State)
	if ok {
		if obs.Current.State, err = claimDocument(row); err != nil {
			return core.Observation{}, err
		}
	}
	if creation {
		obs.Matches = 0
		if ok {
			obs.Matches, obs.Resource = 1, resourceOf(nounClaim, name)
		}
	}
	return obs, nil
}

// Inverse undoes one effect of an attach or a detach: it drops a claim this change added, restores a claim the change
// removed, disconnects NPM from a network it joined, or reconnects it with a dynamic address to one it left.
func (c networkClaims) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	noun, name, _, err := creationOf(e.Resource)
	if err != nil {
		return core.InverseResult{}, err
	}
	switch {
	case noun == nounClaim && e.Kind == core.EffectCreate:
		return core.InverseResult{}, c.claimUndoAdd(ctx, env, e, name)
	case noun == nounClaim && e.Kind == core.EffectDelete:
		return core.InverseResult{}, c.claimRestore(ctx, env, e, name)
	case noun == nounEndpoint && e.InverseRecovery == nil:
		return core.InverseResult{}, c.networkUndoConnect(ctx, env, e, name)
	case noun == nounEndpoint:
		return c.networkUndoDisconnect(ctx, env, name)
	default:
		return core.InverseResult{}, fmt.Errorf("npm: %s %s is not an effect of a network claim", e.Kind, e.Resource)
	}
}

// claimUndoAdd drops the claim e added. A claim no longer there is done; one another change holds is refused.
func (c networkClaims) claimUndoAdd(ctx context.Context, env core.Env, e core.Effect, identity string) error {
	network, claimant, err := claimIdentity(identity)
	if err != nil {
		return err
	}
	return env.DB.Write(ctx, func(tx *sql.Tx) error {
		row, ok, err := claimFind(ctx, tx, network, claimant)
		if err != nil || !ok {
			return err
		}
		if row.ChangeID != e.ChangeID {
			return core.Definite(errcode.New(errcode.InvalidInput, fmt.Sprintf(
				"the claim of %s on network %s belongs to change %s, not %s; it is kept", claimant, network, row.ChangeID, e.ChangeID)))
		}
		return dropClaim(ctx, tx, network, claimant)
	})
}

// claimRestore adds back the claim e removed, with its original change and preexisting mark. The same claim already
// there is done; a different one is refused.
func (c networkClaims) claimRestore(ctx context.Context, env core.Env, e core.Effect, identity string) error {
	network, claimant, err := claimIdentity(identity)
	if err != nil {
		return err
	}
	var doc claimDoc
	if err := json.Unmarshal(e.Before.State, &doc); err != nil || doc.Network != network || doc.Claimant != claimant || doc.ChangeID == "" {
		return core.Definite(errcode.New(errcode.InvalidInput, "the journaled claim does not describe the claim of "+claimant+" on network "+network))
	}
	want := claimRow{Network: doc.Network, Claimant: doc.Claimant, ChangeID: doc.ChangeID, Preexisting: doc.Preexisting}
	return env.DB.Write(ctx, func(tx *sql.Tx) error {
		row, ok, err := claimFind(ctx, tx, network, claimant)
		switch {
		case err != nil:
			return err
		case ok && row == want:
			return nil
		case ok:
			return core.Definite(errcode.New(errcode.InvalidInput, fmt.Sprintf(
				"network %s holds another claim of %s, of change %s", network, claimant, row.ChangeID)))
		}
		return addClaim(ctx, tx, want)
	})
}

// networkUndoConnect disconnects NPM from the network e joined, refused while a claim of another change needs it. A
// refused disconnect while NPM is not on the network is done: an earlier run whose answer was lost already made it.
func (c networkClaims) networkUndoConnect(ctx context.Context, env core.Env, e core.Effect, network string) error {
	all, err := claims(ctx, env.DB.Read(), network)
	if err != nil {
		return err
	}
	for _, other := range all {
		if other.ChangeID != e.ChangeID {
			return core.Definite(errcode.New(errcode.InvalidInput, fmt.Sprintf(
				"network %s is still claimed by %s of change %s; %s stays on it", network, other.Claimant, other.ChangeID, containerName)))
		}
	}
	err = c.n.d.docker.DisconnectNetwork(ctx, network, containerName, false)
	switch {
	case err == nil, dockerapi.IsNotFound(err):
		return nil
	case refused(err):
		if on, inspectErr := c.npmOnNetwork(ctx, network); inspectErr == nil && !on {
			return nil
		}
		return core.Definite(fmt.Errorf("npm: disconnect %s from network %s: %w", containerName, network, err))
	default:
		return fmt.Errorf("npm: disconnect %s from network %s: %w", containerName, network, engineCoded(err))
	}
}

// networkUndoDisconnect reconnects NPM to the network e left. The Engine assigns the address; a static address the
// endpoint had is not restored. It joins as the attach does: nginx is reloaded, the network and the forward targets are
// read again and the reconnect is refused while a proxy host forwards to an address the Engine could pick there; after
// the connect the address NPM took is checked before any lookup or reload. NPM already on the network, found first or
// after a refused reconnect, is an earlier run whose answer was lost: with no claim on the network the address it holds
// is checked as after a join before the undo answers done; a network with a claim is left as it is.
func (c networkClaims) networkUndoDisconnect(ctx context.Context, env core.Env, network string) (core.InverseResult, error) {
	done := core.InverseResult{Resource: resourceOf(nounEndpoint, network)}
	on, err := c.npmOnNetwork(ctx, network)
	if err != nil {
		return core.InverseResult{}, fmt.Errorf("npm: inspect container %s: %w", containerName, engineCoded(err))
	}
	if on {
		all, err := claims(ctx, env.DB.Read(), network)
		if err != nil {
			return core.InverseResult{}, err
		}
		if len(all) > 0 {
			return done, nil
		}
		targets, err := c.n.forwardTargets(ctx, env)
		if err != nil {
			return core.InverseResult{}, core.Definite(err)
		}
		if err := c.afterJoin(ctx, env, network, targets); err != nil {
			return core.InverseResult{}, err
		}
		return done, nil
	}
	if err := c.reloadBeforeJoin(ctx, env, network); err != nil {
		return core.InverseResult{}, err
	}
	targets, err := c.joinGuard(ctx, env, network, netip.Addr{})
	if err != nil {
		return core.InverseResult{}, err
	}
	err = c.n.d.docker.ConnectNetwork(ctx, network, containerName, nil)
	switch {
	case err == nil:
	case refused(err):
		if on, inspectErr := c.npmOnNetwork(ctx, network); inspectErr != nil || !on {
			return core.InverseResult{}, core.Definite(fmt.Errorf("npm: connect %s to network %s: %w", containerName, network, err))
		}
	default:
		return core.InverseResult{}, c.lostJoin(ctx, env, network, targets,
			fmt.Errorf("npm: connect %s to network %s: %w", containerName, network, engineCoded(err)))
	}
	if err := c.afterJoin(ctx, env, network, targets); err != nil {
		return core.InverseResult{}, err
	}
	return done, nil
}

// npmOnNetwork reports whether the NPM container is on the network; a missing container is not on it.
func (c networkClaims) npmOnNetwork(ctx context.Context, network string) (bool, error) {
	ct, err := c.n.d.docker.InspectContainer(ctx, containerName)
	switch {
	case dockerapi.IsNotFound(err):
		return false, nil
	case err != nil:
		return false, err
	}
	return ct.NetworkSettings.Networks[network] != nil, nil
}

// nginxDisconnectWait bounds the disconnect that takes back a join nginx or the forward hosts did not allow.
const nginxDisconnectWait = 10 * time.Second

// lostJoinWait bounds the check of a join whose connect answer was lost: an inspection, a reload, a lookup pass and a
// disconnect. It runs under a deadline of its own, since the caller's may be what ended.
const lostJoinWait = nginxReloadWait + resolveTimeout + time.Second + nginxDisconnectWait

const (
	reloadBeforeJoinWhere = "before " + containerName + " joins network "
	reloadAfterJoinWhere  = "after " + containerName + " joined network "
)

// reloadBeforeJoin reloads nginx before NPM joins network, so a name nginx resolved when it last loaded its
// configuration is resolved again before NPM can take the address it held. A reload that is not confirmed refuses the
// join, Definite: nothing has changed yet.
func (c networkClaims) reloadBeforeJoin(ctx context.Context, env core.Env, network string) error {
	if err := c.n.reloadNginx(ctx); err != nil {
		return core.Definite(c.n.reloadRefusal(ctx, env, err, reloadBeforeJoinWhere+network))
	}
	return nil
}

// joinGuard reads the forward targets and then network again, after the reload before a join, so no listing or lookup
// sits between the network's read and the connect, and refuses a network that is not a user-defined bridge and a
// target NPM could take there (forwardTaken). It returns the targets for afterJoin. Every failure is Definite.
func (c networkClaims) joinGuard(ctx context.Context, env core.Env, network string, static netip.Addr) ([]forwardTarget, error) {
	targets, err := c.n.forwardTargets(ctx, env)
	if err != nil {
		return nil, core.Definite(err)
	}
	nw, err := c.n.d.docker.InspectNetwork(ctx, network)
	if err != nil {
		return nil, networkEngine("inspect network "+network, err)
	}
	endpoint := map[string]proxy.Endpoint{nw.Name: {Driver: nw.Driver, UserDefined: networkUserDefined(nw.Name)}}
	if err := proxy.Target(endpoint, nw.Name); err != nil {
		return nil, core.Definite(err)
	}
	var statics []netip.Addr
	if static.IsValid() {
		statics = []netip.Addr{static}
	}
	if err := forwardTaken(targets, nw, statics); err != nil {
		return nil, err
	}
	return targets, nil
}

// afterJoin checks the address NPM holds on network, first against targets, read before the join, with no listing or
// lookup in between, so nothing stretches the time NPM holds a forwarded address; then it reloads nginx, so nginx
// resolves its names again with NPM on the network, and checks the address against the targets read afresh. A
// forwarded address, a failed reload or a check that could not be made takes NPM off the network again (leave).
func (c networkClaims) afterJoin(ctx context.Context, env core.Env, network string, targets []forwardTarget) error {
	ct, err := c.n.d.docker.InspectContainer(ctx, containerName)
	if err != nil {
		return c.leave(ctx, network, uncheckedJoin(fmt.Errorf("inspect container %s: %w", containerName, engineCoded(err))))
	}
	ep := ct.NetworkSettings.Networks[network]
	if ep == nil {
		return c.leave(ctx, network, uncheckedJoin(fmt.Errorf("%s has no endpoint on network %s after the join", containerName, network)))
	}
	held := heldAddresses(ct)[network]
	if hit := heldTarget(held, targets, network); hit != "" {
		return c.leave(ctx, network, reservedJoin(hit))
	}
	if err := c.n.reloadNginx(ctx); err != nil {
		where := reloadAfterJoinWhere + network
		return c.leave(ctx, network, joinProblem{
			err: fmt.Errorf("nginx could not be reloaded %s: %w", where, err),
			refuse: func(suffix string) error {
				coded := errcode.From(c.n.reloadRefusal(ctx, env, err, where))
				return core.Definite(errcode.New(coded.Code, coded.Detail+suffix))
			},
		})
	}
	fresh, err := c.n.forwardTargets(ctx, env)
	if err != nil {
		return c.leave(ctx, network, uncheckedJoin(fmt.Errorf("read the forward hosts of NPM's proxy hosts: %w", err)))
	}
	if hit := heldTarget(held, fresh, network); hit != "" {
		return c.leave(ctx, network, reservedJoin(hit))
	}
	return nil
}

// lostJoin checks a join that connectErr, a connect's lost answer, may hide: NPM found on network is checked as afterJoin
// checks it. It returns afterJoin's error when that error is not definite, and otherwise connectErr, with afterJoin's
// definite refusal appended. The refusal is added as text only, so the error keeps connectErr's classification.
func (c networkClaims) lostJoin(ctx context.Context, env core.Env, network string, targets []forwardTarget, connectErr error) error {
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lostJoinWait)
	defer cancel()
	if on, err := c.npmOnNetwork(wctx, network); err != nil || !on {
		return connectErr
	}
	err := c.afterJoin(wctx, env, network, targets)
	switch {
	case err == nil:
		return connectErr
	case core.IsDefinite(err):
		return fmt.Errorf("%w; %s", connectErr, err.Error())
	}
	return err
}

// heldTarget describes the first target at an address NPM holds on network, "" when there is none.
func heldTarget(held []netip.Addr, targets []forwardTarget, network string) string {
	for _, t := range targets {
		if !slices.Contains(held, t.addr.Unmap()) {
			continue
		}
		if t.name {
			return fmt.Sprintf("%s forwards to %s, which resolves to %s, the address %s took on network %s",
				t.where, t.host, t.addr, containerName, network)
		}
		return fmt.Sprintf("%s forwards to %s, the address %s took on network %s", t.where, t.addr, containerName, network)
	}
	return ""
}

// joinProblem is why NPM may not stay on a network it joined. err names it when the disconnect fails; refuse builds
// the refusal once NPM is off the network, suffix saying what became of the join.
type joinProblem struct {
	err    error
	refuse func(suffix string) error
}

func reservedJoin(detail string) joinProblem {
	return joinProblem{err: errors.New(detail), refuse: func(suffix string) error {
		return core.Definite(errcode.New(errcode.NPMReserved, detail+suffix))
	}}
}

// uncheckedJoin is a check that could not be made; the refusal wraps its cause.
func uncheckedJoin(cause error) joinProblem {
	return joinProblem{err: cause, refuse: func(suffix string) error {
		return core.Definite(fmt.Errorf("npm: %w%s", cause, suffix))
	}}
}

// leave disconnects NPM from network and then refuses. The refusal is Definite once NPM is off the network; when the
// disconnect fails, NPM stays joined, so the error carries neither the Definite mark nor an errcode cause. NPM never
// leaves its own network: there the refusal comes with no disconnect.
func (c networkClaims) leave(ctx context.Context, network string, p joinProblem) error {
	if network == networkName {
		return p.refuse("")
	}
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), nginxDisconnectWait)
	defer cancel()
	if err := c.disconnectAgain(dctx, network); err != nil {
		return fmt.Errorf("npm: %s stays on network %s: %w", containerName, network,
			errors.New(p.err.Error()+"; disconnecting it again failed: "+err.Error()))
	}
	return p.refuse("; it was disconnected again")
}

// disconnectAgain takes NPM off network; NPM already off it counts as done.
func (c networkClaims) disconnectAgain(ctx context.Context, network string) error {
	err := c.n.d.docker.DisconnectNetwork(ctx, network, containerName, false)
	switch {
	case err == nil, dockerapi.IsNotFound(err):
		return nil
	case refused(err):
		if on, inspectErr := c.npmOnNetwork(ctx, network); inspectErr == nil && !on {
			return nil
		}
	}
	return err
}

// forwardTarget is one address a proxy host or custom location forwards to. where names the forward: "proxy host <id>"
// or "location <path> of proxy host <id>". host is the forward host as NPM holds it; name is true when it is a name and
// addr one of its answers.
type forwardTarget struct {
	where string
	host  string
	name  bool
	addr  netip.Addr
}

// forwardRef is one forward of an enabled proxy host or of one of its custom locations, its host read up to its first
// "/", as nginx reads a proxy_pass host. literal is set when the host is an address.
type forwardRef struct {
	where, host string
	literal     netip.Addr
	location    bool
}

// resolvable is true for a forward host that is a name: a host holding an nginx variable is resolved by nginx per
// request, so no address can be read from it.
func (f forwardRef) resolvable() bool {
	return !f.literal.IsValid() && f.host != "" && !strings.Contains(f.host, "$")
}

// forwardRefs lists the forwards of NPM's proxy hosts in proxy host order. A disabled host is skipped: NPM renders no
// server block for it, so nginx neither forwards to its hosts nor resolves their names.
func (n nm) forwardRefs(ctx context.Context, env core.Env) ([]forwardRef, error) {
	api, err := n.d.api(ctx, env)
	if err != nil {
		return nil, err
	}
	hosts, err := api.ProxyHosts(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, npmListingAnswer(err)
	}
	slices.SortFunc(hosts, func(a, b npmapi.ProxyHost) int { return cmp.Compare(a.ID, b.ID) })
	var refs []forwardRef
	for _, ph := range hosts {
		if !bool(ph.Enabled) {
			continue
		}
		id := strconv.Itoa(ph.ID)
		refs = append(refs, forwardRef{where: "proxy host " + id, host: ph.ForwardHost})
		for _, l := range ph.Locations {
			refs = append(refs, forwardRef{where: "location " + l.Path + " of proxy host " + id, host: l.ForwardHost, location: true})
		}
	}
	for i := range refs {
		host, _, _ := strings.Cut(refs[i].host, "/")
		refs[i].host = host
		if a, ok := proxy.LiteralAddr(host); ok {
			refs[i].literal = a
		}
	}
	return refs, nil
}

func refNames(refs []forwardRef) []string {
	var names []string
	for _, r := range refs {
		if r.resolvable() && !slices.Contains(names, r.host) {
			names = append(names, r.host)
		}
	}
	return names
}

// unresolvedName is forwardAnswers' failure for a name whose lookup gave neither an answer nor a definite not-found.
type unresolvedName struct{ name string }

func (u *unresolvedName) Error() string {
	return "npm: forward host " + u.name + " could not be resolved: the resolver did not answer"
}

// unresolvedRefusal names the first forward that uses the name forwardAnswers could not resolve; any other error is
// returned as it is.
func unresolvedRefusal(refs []forwardRef, err error) error {
	var u *unresolvedName
	if !errors.As(err, &u) {
		return err
	}
	for _, r := range refs {
		if r.resolvable() && r.host == u.name {
			return core.Definite(errcode.New(errcode.NPMForwardUnresolved, r.where+" forwards to "+r.host+
				", which could not be resolved: the resolver did not answer; a stopped or removed container's name is a usual cause"))
		}
	}
	return err
}

// forwardTargets reads every address NPM's enabled proxy hosts and their custom locations forward to, in proxy host
// order. A name is resolved as checkForward resolves it; a name with no answer adds nothing here, because every join
// of NPM runs nginx -t and nginx -s reload in the container, confirmed by a new worker, before the connect and again
// after the first check of the address NPM took. A DNS name re-pointed between the first reload and this read leaves
// nginx with the older answer until the second reload: the accepted DNS risk (spec D10).
func (n nm) forwardTargets(ctx context.Context, env core.Env) ([]forwardTarget, error) {
	targets, _, err := n.forwardsAndUnanswered(ctx, env)
	return targets, err
}

// forwardsAndUnanswered is forwardTargets together with the unanswered static forwards, read from the same listing and
// lookups, so that a check needing both lists the proxy hosts and resolves each name once.
func (n nm) forwardsAndUnanswered(ctx context.Context, env core.Env) ([]forwardTarget, []unansweredForward, error) {
	refs, err := n.forwardRefs(ctx, env)
	if err != nil {
		return nil, nil, err
	}
	answers, err := n.forwardAnswers(ctx, refNames(refs))
	if err != nil {
		return nil, nil, unresolvedRefusal(refs, err)
	}
	out, us := forwardsOf(refs, answers)
	return out, us, nil
}

// forwardsOf builds the targets of refs from answers, the addresses of each name, and lists the custom locations whose
// static name has none.
func forwardsOf(refs []forwardRef, answers map[string][]netip.Addr) ([]forwardTarget, []unansweredForward) {
	var out []forwardTarget
	var us []unansweredForward
	for _, f := range refs {
		if f.literal.IsValid() {
			out = append(out, forwardTarget{where: f.where, host: f.host, addr: f.literal})
			continue
		}
		if f.location && f.resolvable() && len(answers[f.host]) == 0 {
			us = append(us, unansweredForward{Where: f.where, Host: f.host})
		}
		for _, a := range answers[f.host] {
			out = append(out, forwardTarget{where: f.where, host: f.host, name: true, addr: a})
		}
	}
	return out, us
}

// unansweredForward is a custom location whose static forward host has no answer: nginx resolves such a name when it
// loads its configuration, so nginx -t fails while it does not answer.
type unansweredForward struct{ Where, Host string }

// unansweredForwards lists, in proxy host order, the custom locations of enabled proxy hosts whose forward host is a
// name that gets a definite not-found. A proxy host's own forward host is not listed: nginx resolves it per request.
func (n nm) unansweredForwards(ctx context.Context, env core.Env) ([]unansweredForward, error) {
	refs, err := n.forwardRefs(ctx, env)
	if err != nil {
		return nil, err
	}
	var locs []forwardRef
	for _, r := range refs {
		if r.location && r.resolvable() {
			locs = append(locs, r)
		}
	}
	answers, err := n.forwardAnswers(ctx, refNames(locs))
	if err != nil {
		return nil, unresolvedRefusal(locs, err)
	}
	_, us := forwardsOf(locs, answers)
	return us, nil
}

func unansweredDetail(us []unansweredForward) string {
	parts := make([]string, 0, len(us))
	for _, u := range us {
		parts = append(parts, u.Where+" forwards to "+u.Host+", which has no answer")
	}
	return strings.Join(parts, "; ")
}

const forwardResolvers = 8

// forwardAnswers returns the sorted answers of each name, none for a name that does not exist. Docker's names are read
// in one pass; the rest go to the host's resolver concurrently, and both run under one deadline, so a listing of many
// names that do not answer costs one resolveTimeout, not one per name. Only an answer that the name does not exist
// skips it: any other failure leaves the name's addresses unknown, and the guard must not pass on that.
func (n nm) forwardAnswers(ctx context.Context, names []string) (map[string][]netip.Addr, error) {
	out := make(map[string][]netip.Addr, len(names))
	if len(names) == 0 {
		return out, nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, resolveTimeout+time.Second)
	defer cancel()
	members, err := n.dockerMembers(lookupCtx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if lookupCtx.Err() != nil {
			return nil, core.Definite(errcode.New(errcode.NPMForwardUnresolved,
				"Docker's names of the forward hosts could not be read in time; retry"))
		}
		return nil, fmt.Errorf("npm: read Docker's names of the forward hosts: %w", err)
	}
	var lookups []string
	for _, name := range names {
		if answers := members.answers(name); len(answers) > 0 {
			out[name] = plainAddrs(answers)
		} else {
			lookups = append(lookups, name)
		}
	}
	type result struct {
		answers []netip.Addr
		err     error
	}
	var (
		mu      sync.Mutex
		results = make(map[string]result, len(lookups))
		wg      sync.WaitGroup
		slots   = make(chan struct{}, forwardResolvers)
	)
	for _, name := range lookups {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-lookupCtx.Done():
				return
			}
			defer func() { <-slots }()
			answers, err := n.d.resolve(lookupCtx, name)
			mu.Lock()
			results[name] = result{answers, err}
			mu.Unlock()
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-lookupCtx.Done():
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	mu.Lock()
	defer mu.Unlock()
	for _, name := range lookups {
		r, ok := results[name]
		var dnsErr *net.DNSError
		switch {
		case ok && r.err == nil:
			out[name] = plainAddrs(r.answers)
		case ok && errors.As(r.err, &dnsErr) && dnsErr.IsNotFound:
		default:
			return nil, &unresolvedName{name: name}
		}
	}
	return out, nil
}

func plainAddrs(answers []netip.Addr) []netip.Addr {
	plain := make([]netip.Addr, 0, len(answers))
	for _, a := range answers {
		plain = append(plain, a.Unmap().WithZone(""))
	}
	slices.SortFunc(plain, netip.Addr.Compare)
	return slices.Compact(plain)
}

// npmListingAnswer maps a failed listing of NPM's proxy hosts to npm.unhealthy: a guard that cannot read the forwards
// must not pass, and no answer of NPM's, a 4xx included, is the caller's input to correct.
func npmListingAnswer(err error) error {
	var answer *npmapi.APIError
	switch {
	case errors.As(err, &answer):
		return errcode.Wrap(errcode.NPMUnhealthy, err, fmt.Sprintf("Nginx Proxy Manager answered HTTP %d", answer.Status))
	case errors.Is(err, npmapi.ErrUndecodable):
		return errcode.Wrap(errcode.NPMUnhealthy, err, "Nginx Proxy Manager gave an answer that did not decode")
	default:
		return errcode.Wrap(errcode.NPMUnhealthy, err, "Nginx Proxy Manager did not answer")
	}
}

// networkAssignable reports whether the Engine could give NPM the address addr when NPM joins network. staticV4 means
// NPM asks for a static IPv4 address, so no other IPv4 address is handed out; an IPv6 address is still assigned
// dynamically, because the attach input takes IPv4 only. A network that reports no subnet of addr's family counts every
// unheld address, so missing data is never trusted.
func networkAssignable(network dockerapi.Network, addr netip.Addr, staticV4 bool) bool {
	if addr.Is4() && (staticV4 || (network.EnableIPv4 != nil && !*network.EnableIPv4)) {
		return false
	}
	if addr.Is6() && !network.EnableIPv6 {
		return false
	}
	for _, held := range network.Containers {
		if held.IPv4Address.Addr() == addr || held.IPv6Address.Addr() == addr {
			return false
		}
	}
	subnets := false
	for _, c := range network.IPAM.Config {
		if !c.Subnet.IsValid() || c.Subnet.Addr().Is4() != addr.Is4() {
			continue
		}
		subnets = true
		pool := c.Subnet
		if c.IPRange.IsValid() {
			pool = c.IPRange
		}
		if pool.Contains(addr) && addr != c.Gateway && !engineReserved(c.Subnet, addr) {
			return true
		}
	}
	return !subnets
}

// engineReserved reports whether the Engine's IPAM keeps addr of subnet out of every allocation: the network address
// of any subnet but an IPv4 /31 or /32, and the broadcast address of an IPv4 subnet shorter than /31 (moby
// defaultipam newPoolData).
func engineReserved(subnet netip.Prefix, addr netip.Addr) bool {
	subnet = subnet.Masked()
	if subnet.Addr().Is4() && subnet.Bits() >= 31 {
		return false
	}
	if addr == subnet.Addr() {
		return true
	}
	if !subnet.Addr().Is4() {
		return false
	}
	b := subnet.Addr().As4()
	last := binary.BigEndian.Uint32(b[:]) | (1<<(32-subnet.Bits()) - 1)
	binary.BigEndian.PutUint32(b[:], last)
	return addr == netip.AddrFrom4(b)
}

// forwardGuard refuses with npm.reserved when a proxy host or custom location forwards to an address NPM could take on
// joining network: static, the IPv4 address NPM asks for, or an address the Engine could assign. Every failure is
// Definite, since the guard changes nothing.
func (n nm) forwardGuard(ctx context.Context, env core.Env, network dockerapi.Network, static netip.Addr) error {
	targets, err := n.forwardTargets(ctx, env)
	if err != nil {
		return core.Definite(err)
	}
	var statics []netip.Addr
	if static.IsValid() {
		statics = []netip.Addr{static}
	}
	return forwardTaken(targets, network, statics)
}

// forwardTaken refuses with a Definite npm.reserved the first target NPM could take on joining nw: one of static, the
// addresses NPM asks for, or an address the Engine could assign. An IPv4 address in static means the Engine assigns
// NPM no other IPv4 address.
func forwardTaken(targets []forwardTarget, nw dockerapi.Network, static []netip.Addr) error {
	staticV4 := slices.ContainsFunc(static, netip.Addr.Is4)
	for _, t := range targets {
		if !slices.Contains(static, t.addr) && !networkAssignable(nw, t.addr, staticV4) {
			continue
		}
		detail := fmt.Sprintf("%s forwards to %s, an address %s could take on network %s", t.where, t.addr, containerName, nw.Name)
		if t.name {
			detail = fmt.Sprintf("%s forwards to %s, which resolves to %s, an address %s could take on network %s",
				t.where, t.host, t.addr, containerName, nw.Name)
		}
		return core.Definite(errcode.New(errcode.NPMReserved, detail))
	}
	return nil
}
