package npm

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

// accesslistAdd is npm.accesslist.add: it creates an IP access list named after the change and the effect that makes it.
func (n nm) accesslistAdd() core.ActionDef {
	return core.DefineMutating[AccessListInput, AccessListView](core.ActionSpec{
		Name:     ID + ".accesslist.add",
		Summary:  "Create an IP access list in Nginx Proxy Manager.",
		Version:  1,
		Class:    core.ClassCompensable,
		Side:     core.SideAgent,
		LockKeys: []string{lockKey},
	}, accessListAddHandler{n: n})
}

// accesslistRemove is npm.accesslist.remove: it deletes an access list no proxy host uses.
func (n nm) accesslistRemove() core.ActionDef {
	return core.DefineMutating[AccessListRemoveInput, AccessListView](core.ActionSpec{
		Name:     ID + ".accesslist.remove",
		Summary:  "Delete an access list from Nginx Proxy Manager.",
		Version:  1,
		Class:    core.ClassCompensable,
		Side:     core.SideAgent,
		LockKeys: []string{lockKey},
	}, accessListRemoveHandler{n: n})
}

// accessListAddHandler is the handler of npm.accesslist.add.
type accessListAddHandler struct{ n nm }

// accessListRemoveHandler is the handler of npm.accesslist.remove.
type accessListRemoveHandler struct{ n nm }

// accessListDocument is the state document of an access list. The clients keep their order: nginx applies the first
// rule that matches.
type accessListDocument struct {
	Name       string                `json:"name"`
	SatisfyAny bool                  `json:"satisfyAny"`
	Clients    []npmapi.AccessClient `json:"clients"`
}

// accessListClient is the NPM client of the host's instance. It only reads, so every error is Definite.
func (n nm) accessListClient(ctx context.Context, env core.Env) (npmAPI, error) {
	_, ok, err := loadInstance(ctx, env.DB.Read())
	if err != nil {
		return nil, core.Definite(err)
	}
	if !ok {
		return nil, core.Definite(errcode.New(errcode.NotFound, "no Nginx Proxy Manager instance is deployed on this host"))
	}
	api, err := n.d.api(ctx, env)
	if err != nil {
		return nil, core.Definite(errcode.Wrap(errcode.NPMUnhealthy, err, "the Nginx Proxy Manager API is not available"))
	}
	return api, nil
}

// accessListInvalid is the Definite invalid_input refusal of the field at path.
func accessListInvalid(path, msg string) error {
	return core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: msg, Fields: []errcode.FieldError{{Path: path, Message: msg}}})
}

// accessListRules returns the input's client rules in their order, refusing an empty list, an address that is not an
// IP address or a masked CIDR prefix, and a directive other than allow or deny. A zone is refused because netip
// accepts any characters in it, and NPM writes the address into the nginx configuration.
func accessListRules(in AccessListInput) ([]npmapi.AccessClient, error) {
	if len(in.Clients) == 0 {
		return nil, accessListInvalid("/clients", "at least one client rule is required")
	}
	rules := make([]npmapi.AccessClient, len(in.Clients))
	for i, c := range in.Clients {
		path := "/clients/" + strconv.Itoa(i)
		if a, err := netip.ParseAddr(c.Address); err == nil {
			if a.Zone() != "" {
				return nil, accessListInvalid(path+"/address", fmt.Sprintf("address %q must not carry an IPv6 zone", c.Address))
			}
		} else if p, err := netip.ParsePrefix(c.Address); err != nil {
			return nil, accessListInvalid(path+"/address", fmt.Sprintf("address %q must be an IP address or a CIDR prefix", c.Address))
		} else if p != p.Masked() {
			return nil, accessListInvalid(path+"/address", fmt.Sprintf("prefix %q has host bits set; use %s", c.Address, p.Masked()))
		}
		if c.Directive != "allow" && c.Directive != "deny" {
			return nil, accessListInvalid(path+"/directive", "directive must be allow or deny")
		}
		rules[i] = npmapi.AccessClient{Address: c.Address, Directive: c.Directive}
	}
	return rules, nil
}

// accessListState is the state document of an access list with its id and creation time as the version. It holds what
// an undo recreates and nothing NPM assigns, so a recreated list is State-equal to the original.
func accessListState(a npmapi.AccessList) (core.StateDoc, error) {
	doc := accessListDocument{Name: a.Name, SatisfyAny: a.SatisfyAny, Clients: make([]npmapi.AccessClient, len(a.Clients))}
	copy(doc.Clients, a.Clients)
	state, err := json.Marshal(doc)
	if err != nil {
		return core.StateDoc{}, fmt.Errorf("npm: render access list %d: %w", a.ID, err)
	}
	version, err := json.Marshal(struct {
		ID        int    `json:"id"`
		CreatedOn string `json:"createdOn"`
	}{a.ID, a.CreatedOn})
	if err != nil {
		return core.StateDoc{}, fmt.Errorf("npm: render access list %d: %w", a.ID, err)
	}
	return core.StateDoc{State: state, Version: version}, nil
}

// accessListView is an access list as the actions return it.
func accessListView(a npmapi.AccessList) AccessListView {
	v := AccessListView{ID: a.ID, Name: a.Name, SatisfyAny: a.SatisfyAny, Clients: make([]AccessClientInput, len(a.Clients))}
	for i, c := range a.Clients {
		v.Clients[i] = AccessClientInput{Address: c.Address, Directive: c.Directive}
	}
	return v
}

// accessListName is the name of the list the effect seq of the change creates.
func accessListName(changeID string, seq int) string {
	return fmt.Sprintf("darkpanel-%s-%d", changeID, seq)
}

// accessListNextSeq predicts the sequence number the journal gives the next effect of the change, as Intend assigns
// it, because the list's name must be journaled with the intent.
func accessListNextSeq(ctx context.Context, env core.Env) (int, error) {
	effects, err := env.Effects.List(ctx)
	if err != nil {
		return 0, fmt.Errorf("npm: read the journal of change %s: %w", env.ChangeID, err)
	}
	highest := 0
	for _, e := range effects {
		highest = max(highest, e.Seq)
	}
	return highest + 1, nil
}

// accessListUsers returns the first domain of every proxy host that uses the access list id, sorted.
func accessListUsers(ctx context.Context, api npmAPI, id int) ([]string, error) {
	hosts, err := api.ProxyHosts(ctx)
	if err != nil {
		return nil, fmt.Errorf("npm: list the proxy hosts: %w", err)
	}
	var users []string
	for _, h := range hosts {
		if h.AccessListID != id {
			continue
		}
		domain := strconv.Itoa(h.ID)
		if len(h.DomainNames) > 0 {
			domain = h.DomainNames[0]
		}
		users = append(users, domain)
	}
	slices.Sort(users)
	return users, nil
}

// accessListInUse refuses deleting a list that proxy hosts use: it would silently lift their protection.
func accessListInUse(id int, users []string) error {
	if len(users) == 0 {
		return nil
	}
	return accessListInvalid("/id", fmt.Sprintf("access list %d is used by proxy hosts: %s", id, strings.Join(users, ", ")))
}

// accessListRemovalOf is the composite that may remove what owner reserved, or "" when there is none.
func accessListRemovalOf(owner string) string {
	switch owner {
	case "npm.admin.expose":
		return "npm.admin.unexpose"
	case "panel.exposure.add":
		return "panel.exposure.remove"
	default:
		return ""
	}
}

// accessListReservedBy refuses with a Definite npm.reserved the removal of a reserved list outside its owner's
// removal; found is true when the list is reserved and the removal is that owner's.
func accessListReservedBy(ctx context.Context, env core.Env, id int) (reservedRow, bool, error) {
	row, found, err := reservedOf(ctx, env.DB.Read(), "access_list", id)
	if err != nil {
		return reservedRow{}, false, core.Definite(err)
	}
	if !found {
		return reservedRow{}, false, nil
	}
	if env.Call.ParentAction != accessListRemovalOf(row.Owner) {
		return reservedRow{}, false, core.Definite(errcode.New(errcode.NPMReserved, fmt.Sprintf("access list %d belongs to the exposure %s", id, row.Exposure)))
	}
	return row, true, nil
}

// accessListID returns the id a resource of an access list names.
func accessListID(resource string) (int, error) {
	noun, name, creation, err := creationOf(resource)
	if err != nil {
		return 0, err
	}
	id, err := strconv.Atoi(name)
	if noun != nounAccessList || creation || err != nil {
		return 0, fmt.Errorf("npm: %q does not name an access list by its id", resource)
	}
	return id, nil
}

// accessListSum is the hex SHA-256 of v's JSON.
func accessListSum(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("npm: render the plan state: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// accessListSatisfyLine is the diff line of a list's satisfy mode.
func accessListSatisfyLine(satisfyAny bool) core.DiffLine {
	if satisfyAny {
		return core.DiffLine{Op: " ", Text: "satisfy any"}
	}
	return core.DiffLine{Op: " ", Text: "satisfy all"}
}

// Plan shows the list the add creates; it proves the instance answers. Its name is known only when applied, because
// it carries the sequence number of the effect.
func (h accessListAddHandler) Plan(ctx context.Context, env core.Env, in AccessListInput) (core.PlanOutcome, error) {
	rules, err := accessListRules(in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if _, err := h.n.accessListClient(ctx, env); err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{{Op: "+", Text: "access list (named when applied)"}, accessListSatisfyLine(in.SatisfyAny)}
	for _, r := range rules {
		diff = append(diff, core.DiffLine{Op: "+", Text: r.Directive + " " + r.Address})
	}
	hash, err := accessListSum(in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{}, StateHash: hash}, nil
}

// Apply creates the list under the name the journal's next sequence number gives it.
func (h accessListAddHandler) Apply(ctx context.Context, env core.Env, in AccessListInput) (AccessListView, error) {
	rules, err := accessListRules(in)
	if err != nil {
		return AccessListView{}, err
	}
	api, err := h.n.accessListClient(ctx, env)
	if err != nil {
		return AccessListView{}, err
	}
	seq, err := accessListNextSeq(ctx, env)
	if err != nil {
		return AccessListView{}, err
	}
	body := npmapi.AccessListBody{Name: accessListName(env.ChangeID, seq), SatisfyAny: in.SatisfyAny, PassAuth: false, Clients: rules}
	var created npmapi.AccessList
	e := npmEffect(core.EffectCreate, resourceOf(nounAccessList, "?"+body.Name), json.RawMessage("null"), commitWait)
	err = h.n.run(ctx, env, e, h.n.observeAccessList, func(ctx context.Context) (string, error) {
		list, err := api.CreateAccessList(ctx, body)
		if err != nil {
			return "", err
		}
		created = list
		return resourceOf(nounAccessList, strconv.Itoa(list.ID)), nil
	})
	if err != nil {
		return AccessListView{}, err
	}
	return accessListView(created), nil
}

// Verify checks that the list holds the input's mode and rules. Without an id in out it reads the change's lists, those
// named darkpanel-<change id>-<seq> with seq all digits, and one of them must hold them.
func (h accessListAddHandler) Verify(ctx context.Context, env core.Env, in AccessListInput, out AccessListView) error {
	rules, err := accessListRules(in)
	if err != nil {
		return err
	}
	api, err := h.n.accessListClient(ctx, env)
	if err != nil {
		return err
	}
	holds := func(l npmapi.AccessList) bool { return l.SatisfyAny == in.SatisfyAny && slices.Equal(l.Clients, rules) }
	if out.ID != 0 {
		list, err := api.AccessList(ctx, out.ID)
		if err != nil {
			return fmt.Errorf("npm: read access list %d: %w", out.ID, err)
		}
		if !holds(list) {
			return fmt.Errorf("npm: access list %d does not hold the requested mode and rules", list.ID)
		}
		return nil
	}
	lists, err := api.AccessLists(ctx)
	if err != nil {
		return fmt.Errorf("npm: list the access lists: %w", err)
	}
	prefix := "darkpanel-" + env.ChangeID + "-"
	read := 0
	for _, l := range lists {
		seq, ok := strings.CutPrefix(l.Name, prefix)
		if !ok || seq == "" || strings.Trim(seq, "0123456789") != "" {
			continue
		}
		read++
		if holds(l) {
			return nil
		}
	}
	return fmt.Errorf("npm: none of the %d access lists named %s<seq> holds the requested mode and rules", read, prefix)
}

// Observe reads the list of the effect.
func (h accessListAddHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	return h.n.observeAccessList(ctx, env, e)
}

// Inverse deletes the list the add made, unless a proxy host now uses it.
func (h accessListAddHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	api, err := h.n.accessListClient(ctx, env)
	if err != nil {
		return core.InverseResult{}, err
	}
	id, err := strconv.Atoi(identityOf(e.Resource))
	if err != nil {
		return core.InverseResult{}, fmt.Errorf("npm: %q is not a made access list", e.Resource)
	}
	if _, err := api.AccessList(ctx, id); npmapi.IsNotFound(err) {
		return core.InverseResult{}, nil
	} else if err != nil {
		return core.InverseResult{}, fmt.Errorf("npm: read access list %d: %w", id, err)
	}
	users, err := accessListUsers(ctx, api, id)
	if err != nil {
		return core.InverseResult{}, err
	}
	if err := accessListInUse(id, users); err != nil {
		return core.InverseResult{}, err
	}
	err = api.DeleteAccessList(ctx, id)
	switch {
	case err == nil, npmapi.IsNotFound(err):
		return core.InverseResult{}, nil
	case npmapi.IsClientError(err):
		return core.InverseResult{}, core.Definite(err)
	default:
		return core.InverseResult{}, err
	}
}

// Resolved watches the name of a creation resolved skipped, so the list read marks a list that carries it unclaimed.
func (h accessListAddHandler) Resolved(ctx context.Context, tx *sql.Tx, e core.Effect, _ string) error {
	if e.State != core.EffectSkipped || e.Recovery == nil {
		return nil
	}
	return addWatch(ctx, tx, "access_list", e.Recovery.Value, e.ChangeID, e.Seq)
}

// target reads the list the removal deletes and decides whether it may; every failure is Definite. The checks run in
// this order: the id, the instance, the list, its reservation, its users. found is true when the list is reserved and
// the removal is its owner's.
func (h accessListRemoveHandler) target(ctx context.Context, env core.Env, in AccessListRemoveInput) (npmAPI, npmapi.AccessList, reservedRow, bool, error) {
	if in.ID <= 0 {
		return nil, npmapi.AccessList{}, reservedRow{}, false, accessListInvalid("/id", "an access list id is required")
	}
	api, err := h.n.accessListClient(ctx, env)
	if err != nil {
		return nil, npmapi.AccessList{}, reservedRow{}, false, err
	}
	list, err := api.AccessList(ctx, in.ID)
	if npmapi.IsNotFound(err) {
		return nil, npmapi.AccessList{}, reservedRow{}, false, core.Definite(errcode.New(errcode.NotFound, fmt.Sprintf("access list %d not found", in.ID)))
	}
	if err != nil {
		return nil, npmapi.AccessList{}, reservedRow{}, false, core.Definite(fmt.Errorf("npm: read access list %d: %w", in.ID, err))
	}
	row, found, err := accessListReservedBy(ctx, env, in.ID)
	if err != nil {
		return nil, npmapi.AccessList{}, reservedRow{}, false, err
	}
	users, err := accessListUsers(ctx, api, in.ID)
	if err != nil {
		return nil, npmapi.AccessList{}, reservedRow{}, false, core.Definite(err)
	}
	if err := accessListInUse(in.ID, users); err != nil {
		return nil, npmapi.AccessList{}, reservedRow{}, false, err
	}
	return api, list, row, found, nil
}

// Plan shows the list the removal deletes; the hash covers its state document.
func (h accessListRemoveHandler) Plan(ctx context.Context, env core.Env, in AccessListRemoveInput) (core.PlanOutcome, error) {
	_, list, _, _, err := h.target(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	doc, err := accessListState(list)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{{Op: "-", Text: fmt.Sprintf("access list %s (id %d)", list.Name, list.ID)}, accessListSatisfyLine(list.SatisfyAny)}
	for _, c := range list.Clients {
		diff = append(diff, core.DiffLine{Op: "-", Text: c.Directive + " " + c.Address})
	}
	hash, err := accessListSum(doc)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{}, StateHash: hash}, nil
}

// Apply deletes the list and, under its owner's removal, releases the objects its owner reserved under the exposure.
func (h accessListRemoveHandler) Apply(ctx context.Context, env core.Env, in AccessListRemoveInput) (AccessListView, error) {
	api, list, row, reserved, err := h.target(ctx, env, in)
	if err != nil {
		return AccessListView{}, err
	}
	doc, err := accessListState(list)
	if err != nil {
		return AccessListView{}, err
	}
	resource := resourceOf(nounAccessList, strconv.Itoa(list.ID))
	e := npmEffect(core.EffectDelete, resource, doc.State, commitWait)
	e.Before.Version = doc.Version
	e.InverseRecovery = &core.RecoveryID{Kind: "name", Value: list.Name}
	err = h.n.run(ctx, env, e, h.n.observeAccessList, func(ctx context.Context) (string, error) {
		if err := api.DeleteAccessList(ctx, list.ID); err != nil {
			return "", err
		}
		return resource, nil
	})
	if err != nil {
		return AccessListView{}, err
	}
	view := accessListView(list)
	if reserved {
		err := env.DB.Write(ctx, func(tx *sql.Tx) error { return proxyHostEditRelease(ctx, tx, row.Exposure, row.Owner) })
		if err != nil {
			return AccessListView{}, err
		}
		view.Reserved = row.Owner
	}
	return view, nil
}

// Verify checks that the list is gone.
func (h accessListRemoveHandler) Verify(ctx context.Context, env core.Env, in AccessListRemoveInput, out AccessListView) error {
	id := out.ID
	if id == 0 {
		id = in.ID
	}
	api, err := h.n.accessListClient(ctx, env)
	if err != nil {
		return err
	}
	_, err = api.AccessList(ctx, id)
	switch {
	case npmapi.IsNotFound(err):
		return nil
	case err != nil:
		return fmt.Errorf("npm: read access list %d: %w", id, err)
	default:
		return fmt.Errorf("npm: access list %d still exists", id)
	}
}

// Observe reads the list of the effect, or the list its undo recreated.
func (h accessListRemoveHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	return h.n.observeAccessList(ctx, env, e)
}

// Inverse recreates the removed list from the state the journal holds; NPM gives it a new id.
func (h accessListRemoveHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	api, err := h.n.accessListClient(ctx, env)
	if err != nil {
		return core.InverseResult{}, err
	}
	if e.Kind != core.EffectDelete {
		return core.InverseResult{}, fmt.Errorf("npm: effect %d of change %s is a %s, not the deletion of an access list", e.Seq, e.ChangeID, e.Kind)
	}
	var doc *accessListDocument
	if err := json.Unmarshal(e.Before.State, &doc); err != nil || doc == nil {
		return core.InverseResult{}, fmt.Errorf("npm: effect %d of change %s holds no access list to recreate", e.Seq, e.ChangeID)
	}
	created, err := api.CreateAccessList(ctx, npmapi.AccessListBody{Name: doc.Name, SatisfyAny: doc.SatisfyAny, PassAuth: false, Clients: doc.Clients})
	if npmapi.IsClientError(err) {
		return core.InverseResult{}, core.Definite(err)
	}
	if err != nil {
		return core.InverseResult{}, err
	}
	return core.InverseResult{Resource: resourceOf(nounAccessList, strconv.Itoa(created.ID))}, nil
}

// observeAccessList is the Observer of access lists; it only reads. A creation is found by its name, the undo of a
// removal by the recreated list's id or, when the inverse was cut off before it returned, by the removed list's name;
// anything else is read by its id.
func (n nm) observeAccessList(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	noun, name, creation, err := creationOf(e.Resource)
	if err != nil {
		return core.Observation{}, err
	}
	if noun != nounAccessList {
		return core.Observation{}, fmt.Errorf("npm: %q is not an access list", e.Resource)
	}
	api, err := n.d.api(ctx, env)
	if err != nil {
		return core.Observation{}, err
	}
	undoing := e.State == core.EffectUndoing || e.State == core.EffectUndone
	switch {
	case creation:
		return accessListFind(ctx, api, name, true)
	case e.Kind == core.EffectDelete && undoing && e.UndoResource == "":
		if e.InverseRecovery == nil {
			return core.Observation{}, fmt.Errorf("npm: effect %d of change %s has no identity to find the recreated access list by", e.Seq, e.ChangeID)
		}
		return accessListFind(ctx, api, e.InverseRecovery.Value, false)
	}
	resource := e.Resource
	if e.Kind == core.EffectDelete && undoing {
		resource = e.UndoResource
	}
	id, err := accessListID(resource)
	if err != nil {
		return core.Observation{}, err
	}
	list, err := api.AccessList(ctx, id)
	if npmapi.IsNotFound(err) {
		return named(nullState().State), nil
	}
	if err != nil {
		return core.Observation{}, fmt.Errorf("npm: read access list %d: %w", id, err)
	}
	doc, err := accessListState(list)
	if err != nil {
		return core.Observation{}, err
	}
	obs := named(doc.State)
	obs.Current = doc
	return obs, nil
}

// accessListFind finds the lists named name. exclusive says whether the name tells its list apart from every other:
// true for the change's own creation name, false for a name an operator chose.
func accessListFind(ctx context.Context, api npmAPI, name string, exclusive bool) (core.Observation, error) {
	lists, err := api.AccessLists(ctx)
	if err != nil {
		return core.Observation{}, fmt.Errorf("npm: list the access lists: %w", err)
	}
	var candidates []core.Candidate
	for _, l := range lists {
		if l.Name != name {
			continue
		}
		doc, err := accessListState(l)
		if err != nil {
			return core.Observation{}, err
		}
		candidates = append(candidates, core.Candidate{Resource: resourceOf(nounAccessList, strconv.Itoa(l.ID)), State: doc})
	}
	obs := found(candidates)
	if obs.Matches == 1 {
		obs.Exclusive = exclusive
	}
	return obs, nil
}
