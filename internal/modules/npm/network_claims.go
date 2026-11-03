package npm

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
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
		Name:     "npm.network.attach",
		Summary:  "Attach Nginx Proxy Manager to a network.",
		Version:  1,
		Class:    core.ClassCompensable,
		Side:     core.SideAgent,
		LockKeys: []string{lockKey},
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

// check decides everything an attach does and changes nothing; every failure is Definite.
func (h networkAttachHandler) check(ctx context.Context, env core.Env, in AttachInput) (networkAttachCheck, error) {
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
	switch {
	case len(all) == 0:
		ck.preexisting = ep != nil
	default:
		ck.preexisting = all[0].Preexisting
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
	ck, err := h.check(ctx, env, in)
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

// Apply connects NPM to the network when it is not there and records the claim when the claimant has none.
func (h networkAttachHandler) Apply(ctx context.Context, env core.Env, in AttachInput) (Attachment, error) {
	ck, err := h.check(ctx, env, in)
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
		e := npmEffect(core.EffectModify, resource, obs.Current.State, commitWait)
		err = h.n.run(ctx, env, e, h.n.observeEngine, func(ctx context.Context) (string, error) {
			if err := h.n.d.docker.ConnectNetwork(ctx, ck.network.ID, ck.container.ID, settings); err != nil {
				return "", fmt.Errorf("npm: connect %s to network %s: %w", containerName, name, engineCoded(err))
			}
			return resource, nil
		})
		if err != nil {
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
		return c.networkUndoDisconnect(ctx, name)
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

// networkUndoConnect disconnects NPM from the network e joined, refused while a claim of another change needs it.
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
		return core.Definite(fmt.Errorf("npm: disconnect %s from network %s: %w", containerName, network, err))
	default:
		return fmt.Errorf("npm: disconnect %s from network %s: %w", containerName, network, engineCoded(err))
	}
}

// networkUndoDisconnect reconnects NPM to the network e left. The Engine assigns the address; a static address the
// endpoint had is not restored.
func (c networkClaims) networkUndoDisconnect(ctx context.Context, network string) (core.InverseResult, error) {
	err := c.n.d.docker.ConnectNetwork(ctx, network, containerName, nil)
	switch {
	case err == nil:
		return core.InverseResult{Resource: resourceOf(nounEndpoint, network)}, nil
	case refused(err):
		return core.InverseResult{}, core.Definite(fmt.Errorf("npm: connect %s to network %s: %w", containerName, network, err))
	default:
		return core.InverseResult{}, fmt.Errorf("npm: connect %s to network %s: %w", containerName, network, engineCoded(err))
	}
}
