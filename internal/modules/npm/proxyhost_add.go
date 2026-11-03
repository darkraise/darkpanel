package npm

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
	"github.com/darkraise/darkpanel/internal/modules/npm/proxy"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const (
	// proxyhostAddAdminOwner and proxyhostAddPanelOwner are the composites that own what they create through
	// npm.proxyhost.add; npm_reserved records it.
	proxyhostAddAdminOwner = "npm.admin.expose"
	proxyhostAddPanelOwner = "panel.exposure.add"
)

var (
	// proxyhostAddLabel is one label of a proxy host's domain.
	proxyhostAddLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	// proxyhostAddHostName is a forward host that is a name. The charset keeps the host from carrying anything into
	// the nginx configuration NPM writes from it.
	proxyhostAddHostName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,252}$`)
)

// proxyhostAdd is npm.proxyhost.add: it creates a proxy host through NPM's API, refusing the reserved upstreams outside
// the composites that own them.
func (n nm) proxyhostAdd() core.ActionDef {
	return core.DefineMutating[ProxyHostInput, ProxyHostView](core.ActionSpec{
		Name:     "npm.proxyhost.add",
		Summary:  "Add a proxy host.",
		Version:  1,
		Class:    core.ClassCompensable,
		Side:     core.SideAgent,
		LockKeys: []string{lockKey},
	}, proxyhostAddHandler{n: n})
}

// proxyhostAddHandler is the handler of npm.proxyhost.add.
type proxyhostAddHandler struct{ n nm }

// proxyhostAddInvalid is the Definite invalid_input refusal of the fields; its detail is the first problem.
func proxyhostAddInvalid(fields []errcode.FieldError) error {
	return core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: fields[0].Message, Fields: fields})
}

// proxyhostAddDomainProblem says what is wrong with a proxy host's domain, or "" when it is acceptable.
func proxyhostAddDomainProblem(d string) string {
	if d != strings.ToLower(d) {
		return fmt.Sprintf("domain %q must be lowercase", d)
	}
	if len(d) > 253 {
		return "a domain must be at most 253 bytes"
	}
	labels := strings.Split(strings.TrimPrefix(d, "*."), ".")
	if len(labels) < 2 {
		return fmt.Sprintf("domain %q must have at least two labels", d)
	}
	for _, l := range labels {
		if !proxyhostAddLabel.MatchString(l) {
			return fmt.Sprintf("domain %q must be dot-separated labels of letters, digits and hyphens, optionally after \"*.\"", d)
		}
	}
	if c := labels[len(labels)-1][0]; c < 'a' || c > 'z' {
		return fmt.Sprintf("the last label of domain %q must start with a letter", d)
	}
	return ""
}

// proxyhostAddHostProblem says what is wrong with a forward host, or "" when it is acceptable. An address with a zone
// is refused: netip accepts any characters in a zone.
func proxyhostAddHostProblem(host string) string {
	if a, err := netip.ParseAddr(host); err == nil {
		if a.Zone() != "" {
			return "forwardHost must not carry an IPv6 zone"
		}
		return ""
	}
	if !proxyhostAddHostName.MatchString(host) {
		return "forwardHost must be an IP address or a name of letters, digits, \"_\", \".\" and \"-\" starting with a letter or digit, at most 253 bytes"
	}
	return ""
}

// validateProxyHostInput refuses a proxy host input of the wrong shape with a Definite invalid_input naming every bad
// field.
func validateProxyHostInput(in ProxyHostInput) error {
	var fields []errcode.FieldError
	add := func(path, msg string) { fields = append(fields, errcode.FieldError{Path: path, Message: msg}) }
	if len(in.Domains) == 0 {
		add("/domains", "at least one domain is required")
	}
	seen := make(map[string]bool, len(in.Domains))
	for i, d := range in.Domains {
		path := "/domains/" + strconv.Itoa(i)
		if msg := proxyhostAddDomainProblem(d); msg != "" {
			add(path, msg)
			continue
		}
		if seen[d] {
			add(path, fmt.Sprintf("domain %q is listed twice", d))
		}
		seen[d] = true
	}
	if in.ForwardScheme != "http" && in.ForwardScheme != "https" {
		add("/forwardScheme", "forwardScheme must be http or https")
	}
	if msg := proxyhostAddHostProblem(in.ForwardHost); msg != "" {
		add("/forwardHost", msg)
	}
	if in.ForwardPort < 1 || in.ForwardPort > 65535 {
		add("/forwardPort", "forwardPort must be 1 to 65535")
	}
	if in.CertificateID < 0 {
		add("/certificateId", "certificateId must not be negative")
	}
	if in.AccessListID < 0 {
		add("/accessListId", "accessListId must not be negative")
	}
	if in.SSLForced && in.CertificateID <= 0 {
		add("/sslForced", "sslForced needs a certificateId")
	}
	if in.HSTS && !in.SSLForced {
		add("/hsts", "hsts needs sslForced")
	}
	if len(fields) > 0 {
		return proxyhostAddInvalid(fields)
	}
	return nil
}

// proxyhostAddSorted returns a sorted copy of domains, empty rather than nil.
func proxyhostAddSorted(domains []string) []string {
	out := make([]string, len(domains))
	copy(out, domains)
	slices.Sort(out)
	return out
}

// proxyHostBody is the body NPM is sent for the input: the domains sorted, no host-level advanced configuration, and
// the one custom location that carries the module's headers, with the panel's read timeout under panel.exposure.add.
func proxyHostBody(in ProxyHostInput, parent string) npmapi.ProxyHostBody {
	return npmapi.ProxyHostBody{
		DomainNames:           proxyhostAddSorted(in.Domains),
		ForwardScheme:         in.ForwardScheme,
		ForwardHost:           in.ForwardHost,
		ForwardPort:           in.ForwardPort,
		CertificateID:         in.CertificateID,
		AccessListID:          in.AccessListID,
		SSLForced:             in.SSLForced,
		HSTSEnabled:           in.HSTS,
		AllowWebsocketUpgrade: in.Websockets,
		Locations:             []npmapi.Location{proxy.Location(in.ForwardScheme, in.ForwardHost, in.ForwardPort, parent == proxyhostAddPanelOwner)},
	}
}

// proxyhostAddLocation is a custom location as a proxy host's state holds it.
type proxyhostAddLocation struct {
	Path           string `json:"path"`
	ForwardScheme  string `json:"forwardScheme"`
	ForwardHost    string `json:"forwardHost"`
	ForwardPort    int    `json:"forwardPort"`
	AdvancedConfig string `json:"advancedConfig"`
}

// proxyhostAddState is the state document of a proxy host.
type proxyhostAddState struct {
	Domains        []string               `json:"domains"`
	ForwardScheme  string                 `json:"forwardScheme"`
	ForwardHost    string                 `json:"forwardHost"`
	ForwardPort    int                    `json:"forwardPort"`
	CertificateID  int                    `json:"certificateId"`
	AccessListID   int                    `json:"accessListId"`
	SSLForced      bool                   `json:"sslForced"`
	HSTS           bool                   `json:"hsts"`
	Websockets     bool                   `json:"websockets"`
	Locations      []proxyhostAddLocation `json:"locations"`
	AdvancedConfig string                 `json:"advancedConfig"`
}

// proxyHostState is the state document of a proxy host: what an update sets and an undo restores, without its id and
// times.
func proxyHostState(h npmapi.ProxyHost) json.RawMessage {
	doc := proxyhostAddState{
		Domains:        proxyhostAddSorted(h.DomainNames),
		ForwardScheme:  h.ForwardScheme,
		ForwardHost:    h.ForwardHost,
		ForwardPort:    h.ForwardPort,
		CertificateID:  h.CertificateID,
		AccessListID:   h.AccessListID,
		SSLForced:      h.SSLForced,
		HSTS:           h.HSTSEnabled,
		Websockets:     h.AllowWebsocketUpgrade,
		Locations:      make([]proxyhostAddLocation, len(h.Locations)),
		AdvancedConfig: h.AdvancedConfig,
	}
	for i, l := range h.Locations {
		doc.Locations[i] = proxyhostAddLocation{Path: l.Path, ForwardScheme: l.ForwardScheme, ForwardHost: l.ForwardHost,
			ForwardPort: l.ForwardPort, AdvancedConfig: l.AdvancedConfig}
	}
	// Marshal cannot fail on strings, numbers and booleans.
	b, _ := json.Marshal(doc)
	return b
}

// proxyhostAddDoc is the state document of a proxy host with its times as the version.
func proxyhostAddDoc(h npmapi.ProxyHost) core.StateDoc {
	version, _ := json.Marshal(struct {
		CreatedOn  string `json:"createdOn"`
		ModifiedOn string `json:"modifiedOn"`
	}{h.CreatedOn, h.ModifiedOn})
	return core.StateDoc{State: proxyHostState(h), Version: version}
}

// proxyHostCreatedOn parses NPM's created_on: RFC 3339, or "2006-01-02 15:04:05" read as UTC.
func proxyHostCreatedOn(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	t, err := time.Parse(time.DateTime, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("npm: created_on %q is neither RFC 3339 nor %q", s, time.DateTime)
	}
	return t, nil
}

// proxyhostAddAnswer maps a failed NPM call: a 4xx is invalid_input with NPM's status and message, anything else
// npm.unhealthy. err stays the cause, so refused still sees NPM's answer.
func proxyhostAddAnswer(err error) error {
	var answer *npmapi.APIError
	if errors.As(err, &answer) && npmapi.IsClientError(err) {
		return errcode.Wrap(errcode.InvalidInput, err, fmt.Sprintf("Nginx Proxy Manager refused the request (HTTP %d): %s", answer.Status, answer.Message))
	}
	return errcode.Wrap(errcode.NPMUnhealthy, err, "Nginx Proxy Manager did not answer")
}

// checkUpstream refuses with a Definite npm.reserved an upstream the input may not forward to under its parent. It
// adds to the reserved set every address of the NPM container and every name Docker resolves to it. A failed Engine
// read is not a refusal and is returned as it is.
func (n nm) checkUpstream(ctx context.Context, env core.Env, in ProxyHostInput) error {
	r, err := n.d.panel(ctx, env)
	if err != nil {
		return err
	}
	c, err := n.d.docker.InspectContainer(ctx, containerName)
	switch {
	case dockerapi.IsNotFound(err):
	case err != nil:
		return fmt.Errorf("npm: inspect container %s: %w", containerName, engineCoded(err))
	default:
		r.NPMAddrs, r.NPMNames = slices.Clone(r.NPMAddrs), slices.Clone(r.NPMNames)
		names := []string{c.ID, c.Config.Hostname}
		if len(c.ID) > 12 {
			names = append(names, c.ID[:12])
		}
		for _, ep := range c.NetworkSettings.Networks {
			if ep == nil {
				continue
			}
			addrs := []netip.Addr{ep.IPAddress, ep.GlobalIPv6Address}
			if ep.IPAMConfig != nil {
				addrs = append(addrs, ep.IPAMConfig.IPv4Address, ep.IPAMConfig.IPv6Address)
			}
			for _, a := range addrs {
				if a.IsValid() {
					r.NPMAddrs = append(r.NPMAddrs, a)
				}
			}
			names = append(names, ep.Aliases...)
			names = append(names, ep.DNSNames...)
		}
		for _, name := range names {
			if name != "" {
				r.NPMNames = append(r.NPMNames, name)
			}
		}
	}
	if err := proxy.CheckUpstream(r, in.ForwardHost, in.ForwardPort, env.Call.ParentAction); err != nil {
		return core.Definite(err)
	}
	return nil
}

// proxyhostAddRef is an object a proxy host refers to, as a plan's state hash covers it.
type proxyhostAddRef struct {
	ID     int  `json:"id"`
	Exists bool `json:"exists"`
}

// proxyhostAddFound is what an add found: the plan's state hash covers all of it.
type proxyhostAddFound struct {
	Domains       []string        `json:"domains"`
	ForwardScheme string          `json:"forwardScheme"`
	ForwardHost   string          `json:"forwardHost"`
	ForwardPort   int             `json:"forwardPort"`
	Certificate   proxyhostAddRef `json:"certificate"`
	AccessList    proxyhostAddRef `json:"accessList"`
	Parent        string          `json:"parent"`
}

// check decides whether the add may run and changes nothing; every failure is Definite. It returns what it found and
// the NPM client it used.
func (h proxyhostAddHandler) check(ctx context.Context, env core.Env, in ProxyHostInput) (proxyhostAddFound, npmAPI, error) {
	if err := validateProxyHostInput(in); err != nil {
		return proxyhostAddFound{}, nil, err
	}
	if err := h.n.checkUpstream(ctx, env, in); err != nil {
		return proxyhostAddFound{}, nil, core.Definite(err)
	}
	api, err := h.n.d.api(ctx, env)
	if err != nil {
		return proxyhostAddFound{}, nil, core.Definite(err)
	}
	hosts, err := api.ProxyHosts(ctx)
	if err != nil {
		return proxyhostAddFound{}, nil, core.Definite(proxyhostAddAnswer(err))
	}
	var fields []errcode.FieldError
	for i, d := range in.Domains {
		for _, host := range hosts {
			if slices.ContainsFunc(host.DomainNames, func(held string) bool { return strings.EqualFold(held, d) }) {
				fields = append(fields, errcode.FieldError{Path: "/domains/" + strconv.Itoa(i), Message: fmt.Sprintf("domain %q already has proxy host %d", d, host.ID)})
				break
			}
		}
	}
	if len(fields) > 0 {
		return proxyhostAddFound{}, nil, proxyhostAddInvalid(fields)
	}
	found := proxyhostAddFound{
		Domains: proxyhostAddSorted(in.Domains), ForwardScheme: in.ForwardScheme, ForwardHost: in.ForwardHost, ForwardPort: in.ForwardPort,
		Certificate: proxyhostAddRef{ID: in.CertificateID}, AccessList: proxyhostAddRef{ID: in.AccessListID}, Parent: env.Call.ParentAction,
	}
	if in.CertificateID != 0 {
		_, err := api.Certificate(ctx, in.CertificateID)
		switch {
		case npmapi.IsNotFound(err):
			return proxyhostAddFound{}, nil, proxyhostAddInvalid([]errcode.FieldError{{Path: "/certificateId", Message: fmt.Sprintf("certificate %d does not exist", in.CertificateID)}})
		case err != nil:
			return proxyhostAddFound{}, nil, core.Definite(proxyhostAddAnswer(err))
		}
		found.Certificate.Exists = true
	}
	if in.AccessListID != 0 {
		_, err := api.AccessList(ctx, in.AccessListID)
		switch {
		case npmapi.IsNotFound(err):
			return proxyhostAddFound{}, nil, proxyhostAddInvalid([]errcode.FieldError{{Path: "/accessListId", Message: fmt.Sprintf("access list %d does not exist", in.AccessListID)}})
		case err != nil:
			return proxyhostAddFound{}, nil, core.Definite(proxyhostAddAnswer(err))
		}
		found.AccessList.Exists = true
	}
	return found, api, nil
}

// Plan shows the proxy host the add creates. The hash covers the domains, the forward target, the certificate and
// access list it refers to and the parent action.
func (h proxyhostAddHandler) Plan(ctx context.Context, env core.Env, in ProxyHostInput) (core.PlanOutcome, error) {
	found, _, err := h.check(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{
		{Op: "+", Text: "proxy host " + strings.Join(found.Domains, ", ")},
		{Op: " ", Text: fmt.Sprintf("forward %s://%s:%d", in.ForwardScheme, in.ForwardHost, in.ForwardPort)},
	}
	if in.CertificateID != 0 {
		diff = append(diff, core.DiffLine{Op: " ", Text: "certificate " + strconv.Itoa(in.CertificateID)})
	}
	if in.AccessListID != 0 {
		diff = append(diff, core.DiffLine{Op: " ", Text: "access list " + strconv.Itoa(in.AccessListID)})
	}
	for _, flag := range []struct {
		set  bool
		text string
	}{{in.SSLForced, "force SSL"}, {in.HSTS, "HSTS"}, {in.Websockets, "websockets"}} {
		if flag.set {
			diff = append(diff, core.DiffLine{Op: " ", Text: flag.text})
		}
	}
	b, err := json.Marshal(found)
	if err != nil {
		return core.PlanOutcome{}, fmt.Errorf("npm: render the plan state: %w", err)
	}
	sum := sha256.Sum256(b)
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{}, StateHash: hex.EncodeToString(sum[:])}, nil
}

// Apply creates the proxy host and, under an owning composite, records it and its certificate as that composite's.
func (h proxyhostAddHandler) Apply(ctx context.Context, env core.Env, in ProxyHostInput) (ProxyHostView, error) {
	found, api, err := h.check(ctx, env, in)
	if err != nil {
		return ProxyHostView{}, err
	}
	parent := env.Call.ParentAction
	var created npmapi.ProxyHost
	e := npmEffect(core.EffectCreate, resourceOf(nounProxyHost, "?"+strings.Join(found.Domains, ",")), nullState().State, commitWait)
	err = h.n.run(ctx, env, e, h.Observe, func(ctx context.Context) (string, error) {
		host, err := api.CreateProxyHost(ctx, proxyHostBody(in, parent))
		if err != nil {
			return "", proxyhostAddAnswer(err)
		}
		created = host
		return resourceOf(nounProxyHost, strconv.Itoa(host.ID)), nil
	})
	if err != nil {
		return ProxyHostView{}, err
	}
	reserved := ""
	if parent == proxyhostAddAdminOwner || parent == proxyhostAddPanelOwner {
		reserved = parent
		err := env.DB.Write(ctx, func(tx *sql.Tx) error {
			row := reservedRow{Kind: "proxy_host", ObjectID: created.ID, Owner: parent, Exposure: in.Domains[0], ChangeID: env.ChangeID}
			if err := addReserved(ctx, tx, row); err != nil {
				return err
			}
			if in.CertificateID == 0 {
				return nil
			}
			if _, ok, err := reservedOf(ctx, tx, "certificate", in.CertificateID); err != nil || ok {
				return err
			}
			row.Kind, row.ObjectID = "certificate", in.CertificateID
			return addReserved(ctx, tx, row)
		})
		if err != nil {
			return ProxyHostView{}, err
		}
	}
	return ProxyHostView{
		ID: created.ID, Domains: proxyhostAddSorted(created.DomainNames), ForwardScheme: created.ForwardScheme, ForwardHost: created.ForwardHost,
		ForwardPort: created.ForwardPort, CertificateID: created.CertificateID, AccessListID: created.AccessListID, SSLForced: created.SSLForced,
		HSTS: created.HSTSEnabled, Websockets: created.AllowWebsocketUpgrade, Reserved: reserved,
	}, nil
}

// Verify checks from in alone that a proxy host with the input's domain set forwards to the input's target; when out
// names a host, it must be that one.
func (h proxyhostAddHandler) Verify(ctx context.Context, env core.Env, in ProxyHostInput, out ProxyHostView) error {
	api, err := h.n.d.api(ctx, env)
	if err != nil {
		return err
	}
	hosts, err := api.ProxyHosts(ctx)
	if err != nil {
		return proxyhostAddAnswer(err)
	}
	want := strings.Join(proxyhostAddSorted(in.Domains), ",")
	for _, host := range hosts {
		if out.ID != 0 && host.ID != out.ID {
			continue
		}
		if strings.Join(proxyhostAddSorted(host.DomainNames), ",") == want && host.ForwardScheme == in.ForwardScheme &&
			host.ForwardHost == in.ForwardHost && host.ForwardPort == in.ForwardPort {
			return nil
		}
	}
	return fmt.Errorf("npm: no proxy host for %s forwards to %s://%s:%d", want, in.ForwardScheme, in.ForwardHost, in.ForwardPort)
}

// Observe reads a made proxy host by its id, or finds a creation's proxy host by its exact domain set and a created_on
// not before the intent. NPM's created_on has whole seconds, so the intent is truncated to the second.
func (h proxyhostAddHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	noun, name, creation, err := creationOf(e.Resource)
	if err != nil {
		return core.Observation{}, err
	}
	if noun != nounProxyHost {
		return core.Observation{}, fmt.Errorf("npm: %q is not a proxy host", e.Resource)
	}
	api, err := h.n.d.api(ctx, env)
	if err != nil {
		return core.Observation{}, err
	}
	if !creation {
		id, err := strconv.Atoi(name)
		if err != nil {
			return core.Observation{}, fmt.Errorf("npm: %q does not name a proxy host by its id", e.Resource)
		}
		host, err := api.ProxyHost(ctx, id)
		if npmapi.IsNotFound(err) {
			return named(nullState().State), nil
		}
		if err != nil {
			return core.Observation{}, proxyhostAddAnswer(err)
		}
		return core.Observation{Current: proxyhostAddDoc(host), Matches: 1, Exclusive: true}, nil
	}
	hosts, err := api.ProxyHosts(ctx)
	if err != nil {
		return core.Observation{}, proxyhostAddAnswer(err)
	}
	intent := e.CommitBy.Add(-e.CommitWait).Truncate(time.Second)
	var candidates []core.Candidate
	for _, host := range hosts {
		if strings.Join(proxyhostAddSorted(host.DomainNames), ",") != name {
			continue
		}
		createdOn, err := proxyHostCreatedOn(host.CreatedOn)
		if err != nil {
			return core.Observation{}, fmt.Errorf("npm: proxy host %d: %w", host.ID, err)
		}
		if createdOn.Before(intent) {
			continue
		}
		candidates = append(candidates, core.Candidate{Resource: resourceOf(nounProxyHost, strconv.Itoa(host.ID)), State: proxyhostAddDoc(host)})
	}
	// The domain set is unique among NPM's proxy hosts, so even several matches are told apart from every other host.
	obs := found(candidates)
	obs.Exclusive = true
	return obs, nil
}

// Inverse deletes the proxy host the add made and drops every reservation of its change.
func (h proxyhostAddHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	noun, name, creation, err := creationOf(e.Resource)
	if err != nil {
		return core.InverseResult{}, err
	}
	id, err := strconv.Atoi(name)
	if noun != nounProxyHost || creation || err != nil {
		return core.InverseResult{}, fmt.Errorf("npm: %q is not a made proxy host", e.Resource)
	}
	api, err := h.n.d.api(ctx, env)
	if err != nil {
		return core.InverseResult{}, err
	}
	err = api.DeleteProxyHost(ctx, id)
	switch {
	case err == nil, npmapi.IsNotFound(err):
	case npmapi.IsClientError(err):
		return core.InverseResult{}, core.Definite(proxyhostAddAnswer(err))
	default:
		return core.InverseResult{}, err
	}
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM npm_reserved WHERE change_id = ?", e.ChangeID); err != nil {
			return fmt.Errorf("npm: release the objects reserved by change %s: %w", e.ChangeID, err)
		}
		return nil
	})
	return core.InverseResult{}, err
}

// Resolved watches the domain set of a creation resolved skipped, so the list reads mark a proxy host that holds it
// unclaimed.
func (h proxyhostAddHandler) Resolved(ctx context.Context, tx *sql.Tx, e core.Effect, _ string) error {
	if e.State != core.EffectSkipped || e.Recovery == nil {
		return nil
	}
	return addWatch(ctx, tx, "proxy_host", e.Recovery.Value, e.ChangeID, e.Seq)
}
