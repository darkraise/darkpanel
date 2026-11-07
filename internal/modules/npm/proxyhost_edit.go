package npm

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

// proxyHostEditRemoval maps each owning composite to the composite whose children may remove what it reserved.
var proxyHostEditRemoval = map[string]string{
	proxyhostAddAdminOwner: "npm.admin.unexpose",
	proxyhostAddPanelOwner: "panel.exposure.remove",
}

// proxyhostUpdate is npm.proxyhost.update: it replaces the settings of a proxy host through NPM's API.
func (n nm) proxyhostUpdate() core.ActionDef {
	return core.DefineMutating[ProxyHostUpdateInput, ProxyHostView](core.ActionSpec{
		Name:     "npm.proxyhost.update",
		Summary:  "Change the settings of a proxy host.",
		Version:  1,
		Class:    core.ClassCompensable,
		Side:     core.SideAgent,
		LockKeys: []string{lockKey},
	}, proxyHostUpdateHandler{proxyHostEdit{n: n}})
}

// proxyhostRemove is npm.proxyhost.remove: it deletes a proxy host named by its id or one of its domains.
func (n nm) proxyhostRemove() core.ActionDef {
	return core.DefineMutating[ProxyHostRemoveInput, ProxyHostView](core.ActionSpec{
		Name:      "npm.proxyhost.remove",
		Summary:   "Delete a proxy host.",
		Version:   1,
		Class:     core.ClassCompensable,
		Side:      core.SideAgent,
		LockKeys:  []string{lockKey},
		Converges: true,
	}, proxyHostRemoveHandler{proxyHostEdit{n: n}})
}

// proxyHostEdit holds what npm.proxyhost.update and npm.proxyhost.remove share.
type proxyHostEdit struct{ n nm }

// proxyHostUpdateHandler is the handler of npm.proxyhost.update.
type proxyHostUpdateHandler struct{ proxyHostEdit }

// proxyHostRemoveHandler is the handler of npm.proxyhost.remove.
type proxyHostRemoveHandler struct{ proxyHostEdit }

// proxyHostEditBody copies the body members of a proxy host.
func proxyHostEditBody(h npmapi.ProxyHost) npmapi.ProxyHostBody {
	return npmapi.ProxyHostBody{
		DomainNames: slices.Clone(h.DomainNames), ForwardScheme: h.ForwardScheme, ForwardHost: h.ForwardHost, ForwardPort: h.ForwardPort,
		CertificateID: h.CertificateID, AccessListID: h.AccessListID, SSLForced: h.SSLForced, HSTSEnabled: h.HSTSEnabled,
		AllowWebsocketUpgrade: h.AllowWebsocketUpgrade, Locations: slices.Clone(h.Locations), AdvancedConfig: h.AdvancedConfig,
	}
}

// proxyHostEditState is the effect document of a body: its JSON with the domains sorted and every advanced
// configuration, the host's and each location's, replaced by its fingerprint. It is compared, never sent: the body an
// undo restores is sealed with the effect.
func proxyHostEditState(env core.Env, b npmapi.ProxyHostBody) json.RawMessage {
	b.DomainNames = proxyhostAddSorted(b.DomainNames)
	b.AdvancedConfig = npmFingerprint(env, b.AdvancedConfig)
	b.Locations = slices.Clone(b.Locations)
	for i := range b.Locations {
		b.Locations[i].AdvancedConfig = npmFingerprint(env, b.Locations[i].AdvancedConfig)
	}
	// Marshal cannot fail on strings, numbers and booleans.
	s, _ := json.Marshal(b)
	return s
}

// proxyHostEditDoc is the effect document of a proxy host, with its times as the version.
func proxyHostEditDoc(env core.Env, h npmapi.ProxyHost) core.StateDoc {
	version, _ := json.Marshal(struct {
		CreatedOn  string `json:"createdOn"`
		ModifiedOn string `json:"modifiedOn"`
	}{h.CreatedOn, h.ModifiedOn})
	return core.StateDoc{State: proxyHostEditState(env, proxyHostEditBody(h)), Version: version}
}

// proxyHostEditKeep is body with what the operator wrote in NPM kept from host: the host's advanced configuration, and
// every location whose path is not "/", in NPM's order and unchanged, after the module's own "/" location.
func proxyHostEditKeep(body npmapi.ProxyHostBody, host npmapi.ProxyHost) npmapi.ProxyHostBody {
	body.AdvancedConfig = host.AdvancedConfig
	body.Locations = slices.Clone(body.Locations)
	for _, l := range host.Locations {
		if l.Path != "/" {
			body.Locations = append(body.Locations, l)
		}
	}
	return body
}

// proxyHostEditKey is the sorted domains joined by ",", the identity a recreated proxy host is found by.
func proxyHostEditKey(domains []string) string {
	return strings.Join(proxyhostAddSorted(domains), ",")
}

// proxyHostEditView is a proxy host as the actions return it; reserved is the owning action or "".
func proxyHostEditView(h npmapi.ProxyHost, reserved string) ProxyHostView {
	return ProxyHostView{
		ID: h.ID, Domains: append([]string{}, h.DomainNames...), ForwardScheme: h.ForwardScheme, ForwardHost: h.ForwardHost,
		ForwardPort: h.ForwardPort, CertificateID: h.CertificateID, AccessListID: h.AccessListID, SSLForced: h.SSLForced,
		HSTS: h.HSTSEnabled, Websockets: h.AllowWebsocketUpgrade, Reserved: reserved,
	}
}

// proxyHostEditUnhealthy is the Definite refusal of a failed read: a read commits nothing.
func proxyHostEditUnhealthy(err error) error {
	return core.Definite(errcode.Wrap(errcode.NPMUnhealthy, err, "Nginx Proxy Manager did not answer"))
}

// proxyHostEditSum is the hex SHA-256 of parts in order.
func proxyHostEditSum(parts ...[]byte) string {
	sum := sha256.New()
	for _, p := range parts {
		sum.Write(p)
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// proxyHostEditID returns the id a resource of a made proxy host names. Neither action creates through its own
// effect, so a creation is refused.
func proxyHostEditID(resource string) (int, error) {
	noun, name, creation, err := creationOf(resource)
	if err != nil {
		return 0, err
	}
	if noun != nounProxyHost {
		return 0, fmt.Errorf("npm: %q is not a proxy host", resource)
	}
	if creation {
		return 0, fmt.Errorf("npm: %q is a creation, and the proxy host update and remove actions create nothing", resource)
	}
	id, err := strconv.Atoi(name)
	if err != nil {
		return 0, fmt.Errorf("npm: %q does not name a proxy host by its id", resource)
	}
	return id, nil
}

// client returns the NPM client of the host's instance; nothing was sent when it fails.
func (p proxyHostEdit) client(ctx context.Context, env core.Env) (npmAPI, error) {
	api, err := p.n.d.api(ctx, env)
	if err != nil {
		return nil, core.Definite(err)
	}
	return api, nil
}

// fetch reads one proxy host; ok is false when NPM does not hold it.
func (p proxyHostEdit) fetch(ctx context.Context, api npmAPI, id int) (npmapi.ProxyHost, bool, error) {
	host, err := api.ProxyHost(ctx, id)
	if npmapi.IsNotFound(err) {
		return npmapi.ProxyHost{}, false, nil
	}
	if err != nil {
		return npmapi.ProxyHost{}, false, proxyHostEditUnhealthy(err)
	}
	return host, true, nil
}

// refuseReserved refuses with a Definite npm.reserved a change to a reserved proxy host outside the removal of its
// owner. reserved is true, with the reservation, when the host is reserved and the caller is that removal.
func (p proxyHostEdit) refuseReserved(ctx context.Context, env core.Env, id int) (reservedRow, bool, error) {
	row, ok, err := reservedOf(ctx, env.DB.Read(), "proxy_host", id)
	if err != nil {
		return reservedRow{}, false, core.Definite(err)
	}
	if !ok {
		return reservedRow{}, false, nil
	}
	if removal, known := proxyHostEditRemoval[row.Owner]; !known || env.Call.ParentAction != removal {
		return reservedRow{}, false, core.Definite(errcode.New(errcode.NPMReserved, fmt.Sprintf("proxy host %d belongs to the exposure %s", id, row.Exposure)))
	}
	return row, true, nil
}

// undoBody is the body an inverse sends: the body the host had, sealed with the effect. The effect's documents hold
// only fingerprints of the advanced configurations, so an inverse never restores from them.
func (p proxyHostEdit) undoBody(ctx context.Context, env core.Env, e core.Effect) (npmapi.ProxyHostBody, error) {
	secret, err := env.Effects.Secret(ctx, e.Seq)
	if err != nil {
		return npmapi.ProxyHostBody{}, core.Definite(fmt.Errorf("npm: read the sealed proxy host of effect %d of change %s: %w", e.Seq, e.ChangeID, err))
	}
	var b *npmapi.ProxyHostBody
	// The decode error is dropped: its text can quote the sealed body.
	if len(secret) == 0 || json.Unmarshal(secret, &b) != nil || b == nil {
		return npmapi.ProxyHostBody{}, core.Definite(fmt.Errorf("npm: effect %d of change %s holds no sealed proxy host to restore", e.Seq, e.ChangeID))
	}
	return *b, nil
}

// checkRestored runs the forward host check on every forward host body writes back: the host's and each custom
// location's. It returns the first error as checkForward returns it.
func (p proxyHostEdit) checkRestored(ctx context.Context, env core.Env, body npmapi.ProxyHostBody) error {
	targets := []ProxyHostInput{{ForwardHost: body.ForwardHost, ForwardPort: body.ForwardPort}}
	for _, l := range body.Locations {
		targets = append(targets, ProxyHostInput{ForwardHost: l.ForwardHost, ForwardPort: l.ForwardPort})
	}
	for _, target := range targets {
		if _, err := p.n.checkForward(ctx, env, target); err != nil {
			return err
		}
	}
	return nil
}

// Observe reads the proxy host of the effect. The undo of a removal is found by the id of the host the inverse
// recreated or, when the inverse was cut off before it returned, by the removed host's domain set.
func (p proxyHostEdit) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	id, err := proxyHostEditID(e.Resource)
	if err != nil {
		return core.Observation{}, err
	}
	api, err := p.client(ctx, env)
	if err != nil {
		return core.Observation{}, err
	}
	if e.Kind == core.EffectDelete && (e.State == core.EffectUndoing || e.State == core.EffectUndone) {
		var candidates []core.Candidate
		switch {
		case e.UndoResource != "":
			undoID, err := proxyHostEditID(e.UndoResource)
			if err != nil {
				return core.Observation{}, err
			}
			host, ok, err := p.fetch(ctx, api, undoID)
			if err != nil {
				return core.Observation{}, err
			}
			if ok {
				candidates = append(candidates, core.Candidate{Resource: resourceOf(nounProxyHost, strconv.Itoa(host.ID)), State: proxyHostEditDoc(env, host)})
			}
		case e.InverseRecovery != nil:
			hosts, err := api.ProxyHosts(ctx)
			if err != nil {
				return core.Observation{}, proxyHostEditUnhealthy(err)
			}
			for _, host := range hosts {
				if proxyHostEditKey(host.DomainNames) == e.InverseRecovery.Value {
					candidates = append(candidates, core.Candidate{Resource: resourceOf(nounProxyHost, strconv.Itoa(host.ID)), State: proxyHostEditDoc(env, host)})
				}
			}
		default:
			return core.Observation{}, fmt.Errorf("npm: effect %d of change %s has no identity to find the recreated proxy host by", e.Seq, e.ChangeID)
		}
		// NPM keeps proxy-host domains unique, so the domain set tells a recreated host apart from every other.
		obs := found(candidates)
		obs.Exclusive = true
		return obs, nil
	}
	host, ok, err := p.fetch(ctx, api, id)
	if err != nil {
		return core.Observation{}, err
	}
	if !ok {
		return named(nullState().State), nil
	}
	return core.Observation{Current: proxyHostEditDoc(env, host), Resource: e.Resource, Matches: 1, Exclusive: true}, nil
}

// check decides whether the update may run and changes nothing; every refusal is Definite. It returns the client, the
// host as NPM holds it, the body the update sends and the sorted addresses the forward host resolves to.
func (h proxyHostUpdateHandler) check(ctx context.Context, env core.Env, in ProxyHostUpdateInput) (npmAPI, npmapi.ProxyHost, npmapi.ProxyHostBody, []string, error) {
	if in.ID <= 0 {
		return nil, npmapi.ProxyHost{}, npmapi.ProxyHostBody{}, nil, proxyhostAddInvalid([]errcode.FieldError{{Path: "/id", Message: "id must be a proxy host id above 0"}})
	}
	if err := validateProxyHostInput(in.ProxyHostInput); err != nil {
		return nil, npmapi.ProxyHost{}, npmapi.ProxyHostBody{}, nil, err
	}
	api, err := h.client(ctx, env)
	if err != nil {
		return nil, npmapi.ProxyHost{}, npmapi.ProxyHostBody{}, nil, err
	}
	host, ok, err := h.fetch(ctx, api, in.ID)
	if err != nil {
		return nil, npmapi.ProxyHost{}, npmapi.ProxyHostBody{}, nil, err
	}
	if !ok {
		return nil, npmapi.ProxyHost{}, npmapi.ProxyHostBody{}, nil, core.Definite(errcode.New(errcode.NotFound, fmt.Sprintf("proxy host %d does not exist", in.ID)))
	}
	if _, _, err := h.refuseReserved(ctx, env, in.ID); err != nil {
		return nil, npmapi.ProxyHost{}, npmapi.ProxyHostBody{}, nil, err
	}
	resolved, err := h.n.checkForward(ctx, env, in.ProxyHostInput)
	if err != nil {
		return nil, npmapi.ProxyHost{}, npmapi.ProxyHostBody{}, nil, err
	}
	hosts, err := api.ProxyHosts(ctx)
	if err != nil {
		return nil, npmapi.ProxyHost{}, npmapi.ProxyHostBody{}, nil, proxyHostEditUnhealthy(err)
	}
	var fields []errcode.FieldError
	for _, d := range in.Domains {
		for _, other := range hosts {
			if other.ID != in.ID && slices.ContainsFunc(other.DomainNames, func(held string) bool { return strings.EqualFold(held, d) }) {
				fields = append(fields, errcode.FieldError{Path: "/domains", Message: fmt.Sprintf("domain %s is already served by proxy host %d", d, other.ID)})
				break
			}
		}
	}
	if len(fields) > 0 {
		return nil, npmapi.ProxyHost{}, npmapi.ProxyHostBody{}, nil, proxyhostAddInvalid(fields)
	}
	return api, host, proxyHostEditKeep(proxyHostBody(in.ProxyHostInput, env.Call.ParentAction), host), resolved, nil
}

// proxyHostEditDiff is a "-" and a "+" line for each shown member that differs between from and to.
func proxyHostEditDiff(from, to npmapi.ProxyHostBody) []core.DiffLine {
	diff := []core.DiffLine{}
	line := func(name, old, next string) {
		if old != next {
			diff = append(diff, core.DiffLine{Op: "-", Text: name + ": " + old}, core.DiffLine{Op: "+", Text: name + ": " + next})
		}
	}
	forward := func(b npmapi.ProxyHostBody) string {
		return fmt.Sprintf("%s://%s:%d", b.ForwardScheme, b.ForwardHost, b.ForwardPort)
	}
	ref := func(id int) string {
		if id == 0 {
			return "none"
		}
		return strconv.Itoa(id)
	}
	onOff := func(set bool) string {
		if set {
			return "on"
		}
		return "off"
	}
	line("domains", strings.Join(proxyhostAddSorted(from.DomainNames), ", "), strings.Join(proxyhostAddSorted(to.DomainNames), ", "))
	line("forward", forward(from), forward(to))
	line("certificate", ref(from.CertificateID), ref(to.CertificateID))
	line("access list", ref(from.AccessListID), ref(to.AccessListID))
	line("force SSL", onOff(from.SSLForced), onOff(to.SSLForced))
	line("HSTS", onOff(from.HSTSEnabled), onOff(to.HSTSEnabled))
	line("websockets", onOff(from.AllowWebsocketUpgrade), onOff(to.AllowWebsocketUpgrade))
	return diff
}

// Plan shows what the update changes; the hash covers the host's current state, the state the update sends and the
// addresses its forward host resolves to.
func (h proxyHostUpdateHandler) Plan(ctx context.Context, env core.Env, in ProxyHostUpdateInput) (core.PlanOutcome, error) {
	_, host, body, resolved, err := h.check(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	current := proxyHostEditBody(host)
	return core.PlanOutcome{
		Diff:      proxyHostEditDiff(current, body),
		Guards:    []core.Guard{},
		StateHash: proxyHostEditSum(proxyHostEditState(env, current), proxyHostEditState(env, body), []byte("\n"+strings.Join(resolved, ","))),
	}, nil
}

// Apply replaces the host's settings with the input's.
func (h proxyHostUpdateHandler) Apply(ctx context.Context, env core.Env, in ProxyHostUpdateInput) (ProxyHostView, error) {
	api, host, body, _, err := h.check(ctx, env, in)
	if err != nil {
		return ProxyHostView{}, err
	}
	e := npmEffect(core.EffectModify, resourceOf(nounProxyHost, strconv.Itoa(host.ID)), proxyHostEditState(env, proxyHostEditBody(host)), commitWait)
	e.After = &core.StateDoc{State: proxyHostEditState(env, body)}
	secret, _ := json.Marshal(proxyHostEditBody(host))
	var updated npmapi.ProxyHost
	err = h.n.run(ctx, withEffectSecret(env, secret), e, h.Observe, func(ctx context.Context) (string, error) {
		u, err := api.UpdateProxyHost(ctx, host.ID, body)
		if err != nil {
			return "", proxyhostAddAnswer(err)
		}
		updated = u
		return e.Resource, nil
	})
	if err != nil {
		return ProxyHostView{}, err
	}
	return proxyHostEditView(updated, ""), nil
}

// Verify checks from in alone that the host holds the input's settings.
func (h proxyHostUpdateHandler) Verify(ctx context.Context, env core.Env, in ProxyHostUpdateInput, _ ProxyHostView) error {
	api, err := h.client(ctx, env)
	if err != nil {
		return err
	}
	host, ok, err := h.fetch(ctx, api, in.ID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("npm: proxy host %d does not exist", in.ID)
	}
	domainSet := func(domains []string) []string { return slices.Compact(proxyhostAddSorted(domains)) }
	if !slices.Equal(domainSet(host.DomainNames), domainSet(in.Domains)) || host.ForwardScheme != in.ForwardScheme ||
		host.ForwardHost != in.ForwardHost || host.ForwardPort != in.ForwardPort || host.CertificateID != in.CertificateID ||
		host.AccessListID != in.AccessListID || host.SSLForced != in.SSLForced || host.HSTSEnabled != in.HSTS ||
		host.AllowWebsocketUpgrade != in.Websockets {
		return fmt.Errorf("npm: proxy host %d does not hold the requested settings", in.ID)
	}
	return nil
}

// Inverse sends the host the body it had before the update. It checks the forward hosts it restores as the removal's
// undo does.
func (h proxyHostUpdateHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	id, err := proxyHostEditID(e.Resource)
	if err != nil {
		return core.InverseResult{}, core.Definite(err)
	}
	body, err := h.undoBody(ctx, env, e)
	if err != nil {
		return core.InverseResult{}, err
	}
	if err := h.checkRestored(ctx, env, body); err != nil {
		return core.InverseResult{}, err
	}
	api, err := h.client(ctx, env)
	if err != nil {
		return core.InverseResult{}, err
	}
	_, err = api.UpdateProxyHost(ctx, id, body)
	switch {
	case err == nil:
		return core.InverseResult{}, nil
	case npmapi.IsNotFound(err):
		return core.InverseResult{}, core.Definite(errcode.New(errcode.NotFound, fmt.Sprintf("proxy host %d no longer exists", id)))
	case npmapi.IsClientError(err):
		return core.InverseResult{}, core.Definite(proxyhostAddAnswer(err))
	default:
		return core.InverseResult{}, err
	}
}

// proxyHostRemoveRule refuses an input that does not name the host by exactly one of a positive id and a domain.
func proxyHostRemoveRule(in ProxyHostRemoveInput) error {
	if in.ID < 0 {
		return proxyhostAddInvalid([]errcode.FieldError{{Path: "/id", Message: "id must not be negative"}})
	}
	if (in.ID > 0) == (in.Domain != "") {
		const msg = "exactly one of id and domain is required"
		return proxyhostAddInvalid([]errcode.FieldError{{Path: "/id", Message: msg}, {Path: "/domain", Message: msg}})
	}
	return nil
}

// proxyHostRemoveFind returns the host that lists domain among its domains.
func proxyHostRemoveFind(hosts []npmapi.ProxyHost, domain string) (npmapi.ProxyHost, bool) {
	for _, host := range hosts {
		if slices.ContainsFunc(host.DomainNames, func(held string) bool { return strings.EqualFold(held, domain) }) {
			return host, true
		}
	}
	return npmapi.ProxyHost{}, false
}

// proxyHostRemoveTarget is the host a removal names, as NPM holds it.
type proxyHostRemoveTarget struct {
	api      npmAPI
	host     npmapi.ProxyHost
	exists   bool
	row      reservedRow
	reserved bool // the host is reserved and the caller is its owner's removal
}

// target finds the host the removal names and decides whether it may be removed; every refusal is Definite.
func (h proxyHostRemoveHandler) target(ctx context.Context, env core.Env, in ProxyHostRemoveInput) (proxyHostRemoveTarget, error) {
	if err := proxyHostRemoveRule(in); err != nil {
		return proxyHostRemoveTarget{}, err
	}
	api, err := h.client(ctx, env)
	if err != nil {
		return proxyHostRemoveTarget{}, err
	}
	t := proxyHostRemoveTarget{api: api}
	if in.ID > 0 {
		t.host, t.exists, err = h.fetch(ctx, api, in.ID)
		if err != nil {
			return proxyHostRemoveTarget{}, err
		}
	} else {
		hosts, err := api.ProxyHosts(ctx)
		if err != nil {
			return proxyHostRemoveTarget{}, proxyHostEditUnhealthy(err)
		}
		t.host, t.exists = proxyHostRemoveFind(hosts, in.Domain)
	}
	if !t.exists {
		if in.ID > 0 {
			if t.row, _, err = reservedOf(ctx, env.DB.Read(), "proxy_host", in.ID); err != nil {
				return proxyHostRemoveTarget{}, core.Definite(err)
			}
		}
		return t, nil
	}
	t.row, t.reserved, err = h.refuseReserved(ctx, env, t.host.ID)
	if err != nil {
		return proxyHostRemoveTarget{}, err
	}
	return t, nil
}

// Plan shows the host the removal deletes, or nothing when it is already gone.
func (h proxyHostRemoveHandler) Plan(ctx context.Context, env core.Env, in ProxyHostRemoveInput) (core.PlanOutcome, error) {
	t, err := h.target(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if !t.exists {
		b, err := json.Marshal(in)
		if err != nil {
			return core.PlanOutcome{}, fmt.Errorf("npm: render the plan state: %w", err)
		}
		return core.PlanOutcome{Diff: []core.DiffLine{}, Guards: []core.Guard{}, StateHash: proxyHostEditSum(b)}, nil
	}
	text := fmt.Sprintf("proxy host %s -> %s://%s:%d", strings.Join(proxyhostAddSorted(t.host.DomainNames), ", "),
		t.host.ForwardScheme, t.host.ForwardHost, t.host.ForwardPort)
	return core.PlanOutcome{
		Diff:      []core.DiffLine{{Op: "-", Text: text}},
		Guards:    []core.Guard{},
		StateHash: proxyHostEditSum([]byte(strconv.Itoa(t.host.ID)+"\n"), proxyHostEditState(env, proxyHostEditBody(t.host))),
	}, nil
}

// Apply deletes the host and, under its owner's removal, releases that owner's records of the exposure. A host already
// gone changes nothing in NPM, but under an exposure's removal by domain the removing owner's records are still
// released, as are the records of a host's reservation under its owner's removal by id, so a removal cut off before
// its bookkeeping is completed by running it again.
func (h proxyHostRemoveHandler) Apply(ctx context.Context, env core.Env, in ProxyHostRemoveInput) (ProxyHostView, error) {
	t, err := h.target(ctx, env, in)
	if err != nil {
		return ProxyHostView{}, err
	}
	if !t.exists {
		view := ProxyHostView{ID: in.ID, Domains: []string{}}
		if in.Domain == "" {
			// A zero row has no owner, so no removal matches it.
			if removal, known := proxyHostEditRemoval[t.row.Owner]; known && env.Call.ParentAction == removal {
				if err := env.DB.Write(ctx, func(tx *sql.Tx) error { return proxyHostEditRelease(ctx, tx, t.row.Exposure, t.row.Owner) }); err != nil {
					return ProxyHostView{}, err
				}
			}
			return view, nil
		}
		view.Domains = []string{in.Domain}
		for owner, removal := range proxyHostEditRemoval {
			if env.Call.ParentAction == removal {
				if err := env.DB.Write(ctx, func(tx *sql.Tx) error { return proxyHostEditRelease(ctx, tx, in.Domain, owner) }); err != nil {
					return ProxyHostView{}, err
				}
				break
			}
		}
		return view, nil
	}
	resource := resourceOf(nounProxyHost, strconv.Itoa(t.host.ID))
	e := npmEffect(core.EffectDelete, resource, proxyHostEditState(env, proxyHostEditBody(t.host)), commitWait)
	after := nullState()
	e.After = &after
	e.InverseRecovery = &core.RecoveryID{Kind: "name", Value: proxyHostEditKey(t.host.DomainNames)}
	secret, _ := json.Marshal(proxyHostEditBody(t.host))
	err = h.n.run(ctx, withEffectSecret(env, secret), e, h.Observe, func(ctx context.Context) (string, error) {
		err := t.api.DeleteProxyHost(ctx, t.host.ID)
		switch {
		case err == nil, npmapi.IsNotFound(err):
			return resource, nil
		default:
			return "", proxyhostAddAnswer(err)
		}
	})
	if err != nil {
		return ProxyHostView{}, err
	}
	if t.reserved {
		if err := env.DB.Write(ctx, func(tx *sql.Tx) error {
			return proxyHostEditRelease(ctx, tx, t.row.Exposure, t.row.Owner)
		}); err != nil {
			return ProxyHostView{}, err
		}
	}
	return proxyHostEditView(t.host, t.row.Owner), nil
}

// proxyHostEditRelease drops the reservation records owner holds under exposure. Another owner's records under the same
// hostname are kept: only the owning exposure's removal may release them.
func proxyHostEditRelease(ctx context.Context, tx *sql.Tx, exposure, owner string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM npm_reserved WHERE exposure = ? AND owner = ?", exposure, owner); err != nil {
		return fmt.Errorf("npm: release the objects of exposure %s: %w", exposure, err)
	}
	return nil
}

// Verify checks from in alone that no host has the id or lists the domain.
func (h proxyHostRemoveHandler) Verify(ctx context.Context, env core.Env, in ProxyHostRemoveInput, _ ProxyHostView) error {
	api, err := h.client(ctx, env)
	if err != nil {
		return err
	}
	if in.ID > 0 {
		_, ok, err := h.fetch(ctx, api, in.ID)
		if err != nil {
			return err
		}
		if ok {
			return fmt.Errorf("npm: proxy host %d still exists", in.ID)
		}
		return nil
	}
	hosts, err := api.ProxyHosts(ctx)
	if err != nil {
		return proxyHostEditUnhealthy(err)
	}
	if host, ok := proxyHostRemoveFind(hosts, in.Domain); ok {
		return fmt.Errorf("npm: proxy host %d still serves %s", host.ID, in.Domain)
	}
	return nil
}

// Inverse recreates the removed host from the body sealed with the effect; NPM gives it a new id. It does not restore the
// exposure's reservation records. A composite's step in needs_repair is undone directly with the step's stored call,
// under which the forward host check refuses every reserved upstream and resolves each name again, so no undo recreates
// a reserved-upstream host that no reservation tracks.
func (h proxyHostRemoveHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	body, err := h.undoBody(ctx, env, e)
	if err != nil {
		return core.InverseResult{}, err
	}
	if err := h.checkRestored(ctx, env, body); err != nil {
		return core.InverseResult{}, err
	}
	api, err := h.client(ctx, env)
	if err != nil {
		return core.InverseResult{}, err
	}
	created, err := api.CreateProxyHost(ctx, body)
	if npmapi.IsClientError(err) {
		return core.InverseResult{}, core.Definite(proxyhostAddAnswer(err))
	}
	if err != nil {
		return core.InverseResult{}, err
	}
	return core.InverseResult{Resource: resourceOf(nounProxyHost, strconv.Itoa(created.ID))}, nil
}
