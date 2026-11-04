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
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

// certificateRequest is npm.certificate.request: NPM orders a Let's Encrypt certificate over HTTP-01 and answers when
// the ACME order has finished.
func (n nm) certificateRequest() core.ActionDef {
	return core.DefineMutating[CertificateInput, CertificateView](core.ActionSpec{
		Name:         ID + ".certificate.request",
		Summary:      "Request a Let's Encrypt certificate through Nginx Proxy Manager.",
		Version:      1,
		Class:        core.ClassCompensable,
		Side:         core.SideAgent,
		LockKeys:     []string{lockKey},
		LongRunning:  true,
		ApplyTimeout: 10 * time.Minute,
	}, certificateRequestHandler{n: n})
}

// certificateRemove is npm.certificate.remove: it deletes a certificate no proxy host uses. A deleted certificate
// cannot be brought back.
func (n nm) certificateRemove() core.ActionDef {
	return core.DefineMutating[CertificateRemoveInput, CertificateView](core.ActionSpec{
		Name:     ID + ".certificate.remove",
		Summary:  "Delete a certificate from Nginx Proxy Manager.",
		Version:  1,
		Class:    core.ClassIrreversible,
		Side:     core.SideAgent,
		LockKeys: []string{lockKey},
	}, certificateRemoveHandler{n: n})
}

// certificateRequestHandler is the handler of npm.certificate.request.
type certificateRequestHandler struct{ n nm }

// certificateRemoveHandler is the handler of npm.certificate.remove.
type certificateRemoveHandler struct{ n nm }

// certificateClient is the NPM client of the host's instance and the instance's row. It only reads, so every error is
// Definite.
func (n nm) certificateClient(ctx context.Context, env core.Env) (npmAPI, instanceRow, error) {
	row, ok, err := loadInstance(ctx, env.DB.Read())
	if err != nil {
		return nil, instanceRow{}, core.Definite(err)
	}
	if !ok {
		return nil, instanceRow{}, core.Definite(errcode.New(errcode.NotFound, "no Nginx Proxy Manager instance is deployed on this host"))
	}
	api, err := n.d.api(ctx, env)
	if err != nil {
		return nil, instanceRow{}, core.Definite(errcode.Wrap(errcode.NPMUnhealthy, err, "the Nginx Proxy Manager API is not available"))
	}
	return api, row, nil
}

// certificateInvalid is the Definite invalid_input refusal of the field at path.
func certificateInvalid(path, msg string) error {
	return core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: msg, Fields: []errcode.FieldError{{Path: path, Message: msg}}})
}

// certificateDomains returns the input's names lowercased and sorted, refusing what HTTP-01 cannot validate: a
// wildcard, an IP address, a single label, or anything else that is not a DNS name. A name that repeats an earlier
// one after lowercasing is refused at the later one's index.
func certificateDomains(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, certificateInvalid("/domains", "at least one domain is required")
	}
	if len(in) > 100 {
		return nil, certificateInvalid("/domains", "a certificate covers at most 100 domains")
	}
	out := make([]string, len(in))
	seen := make(map[string]bool, len(in))
	for i, raw := range in {
		path := "/domains/" + strconv.Itoa(i)
		d := strings.ToLower(raw)
		if strings.HasPrefix(d, "*.") {
			return nil, certificateInvalid(path, fmt.Sprintf("domain %q is a wildcard, which HTTP validation cannot prove", raw))
		}
		if len(d) > 253 {
			return nil, certificateInvalid(path, "a domain must be at most 253 characters")
		}
		labels := strings.Split(d, ".")
		if len(labels) < 2 {
			return nil, certificateInvalid(path, fmt.Sprintf("domain %q must have at least two labels", raw))
		}
		for _, l := range labels {
			if !proxyhostAddLabel.MatchString(l) {
				return nil, certificateInvalid(path, fmt.Sprintf("domain %q must be dot-separated labels of 1 to 63 letters, digits and inner hyphens", raw))
			}
		}
		if c := labels[len(labels)-1][0]; c < 'a' || c > 'z' {
			return nil, certificateInvalid(path, fmt.Sprintf("the last label of domain %q must start with a letter", raw))
		}
		if seen[d] {
			return nil, certificateInvalid(path, fmt.Sprintf("domain %q is listed twice", d))
		}
		seen[d] = true
		out[i] = d
	}
	slices.Sort(out)
	return out, nil
}

// certificateState is the state document of a certificate, with its id and times as the version.
func certificateState(c npmapi.Certificate) (core.StateDoc, error) {
	domains := make([]string, len(c.DomainNames))
	copy(domains, c.DomainNames)
	slices.Sort(domains)
	state, err := json.Marshal(struct {
		Name     string   `json:"name"`
		Provider string   `json:"provider"`
		Domains  []string `json:"domains"`
	}{c.NiceName, c.Provider, domains})
	if err != nil {
		return core.StateDoc{}, fmt.Errorf("npm: render certificate %d: %w", c.ID, err)
	}
	version, err := json.Marshal(struct {
		ID        int    `json:"id"`
		CreatedOn string `json:"createdOn"`
		ExpiresOn string `json:"expiresOn"`
	}{c.ID, c.CreatedOn, c.ExpiresOn})
	if err != nil {
		return core.StateDoc{}, fmt.Errorf("npm: render certificate %d: %w", c.ID, err)
	}
	return core.StateDoc{State: state, Version: version}, nil
}

// certificateView is a certificate as the actions return it.
func certificateView(c npmapi.Certificate) CertificateView {
	domains := make([]string, len(c.DomainNames))
	copy(domains, c.DomainNames)
	return CertificateView{ID: c.ID, Name: c.NiceName, Domains: domains, ExpiresOn: c.ExpiresOn}
}

// certificateLabel names a certificate to the operator: its name, or its id when it has none.
func certificateLabel(c npmapi.Certificate) string {
	if c.NiceName != "" {
		return c.NiceName
	}
	return strconv.Itoa(c.ID)
}

// certificateIdentity is the creation identity of the certificate for domains: the names sorted and joined by ",".
// NPM names a Let's Encrypt certificate after its domains whatever it is sent, so the set is all that tells one apart.
func certificateIdentity(domains []string) string {
	sorted := slices.Clone(domains)
	slices.Sort(sorted)
	return strings.Join(sorted, ",")
}

// certificateCreatedOn parses NPM's created_on of a certificate.
func certificateCreatedOn(s string) (time.Time, error) {
	return proxyHostCreatedOn(s)
}

// certificateUsers returns the first domain of every proxy host that uses the certificate id, sorted, and refuses
// with a Definite invalid_input when there is one: deleting the certificate would break their TLS.
func certificateUsers(ctx context.Context, api npmAPI, id int) ([]string, error) {
	hosts, err := api.ProxyHosts(ctx)
	if err != nil {
		return nil, fmt.Errorf("npm: list the proxy hosts: %w", err)
	}
	var users []string
	for _, h := range hosts {
		if h.CertificateID != id {
			continue
		}
		domain := strconv.Itoa(h.ID)
		if len(h.DomainNames) > 0 {
			domain = h.DomainNames[0]
		}
		users = append(users, domain)
	}
	slices.Sort(users)
	if len(users) > 0 {
		return users, certificateInvalid("/id", fmt.Sprintf("certificate %d is used by proxy hosts: %s", id, strings.Join(users, ", ")))
	}
	return users, nil
}

// certificateRemovalOf is the composite that may remove what owner reserved, or "" when there is none.
func certificateRemovalOf(owner string) string {
	switch owner {
	case "npm.admin.expose":
		return "npm.admin.unexpose"
	case "panel.exposure.add":
		return "panel.exposure.remove"
	default:
		return ""
	}
}

// certificateReservedBy refuses with a Definite npm.reserved the removal of a reserved certificate outside its owner's
// removal; found is true when the certificate is reserved and the removal is that owner's.
func certificateReservedBy(ctx context.Context, env core.Env, id int) (reservedRow, bool, error) {
	row, found, err := reservedOf(ctx, env.DB.Read(), "certificate", id)
	if err != nil {
		return reservedRow{}, false, core.Definite(err)
	}
	if !found {
		return reservedRow{}, false, nil
	}
	if env.Call.ParentAction != certificateRemovalOf(row.Owner) {
		return reservedRow{}, false, core.Definite(errcode.New(errcode.NPMReserved, fmt.Sprintf("certificate %d belongs to the exposure %s", id, row.Exposure)))
	}
	return row, true, nil
}

// certificateSum is the hex SHA-256 of b.
func certificateSum(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Plan shows the certificate the request orders; it proves the instance answers. The hash covers the names and the
// ACME contact, which NPM takes from its admin user.
func (h certificateRequestHandler) Plan(ctx context.Context, env core.Env, in CertificateInput) (core.PlanOutcome, error) {
	domains, err := certificateDomains(in.Domains)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	_, row, err := h.n.certificateClient(ctx, env)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{
		{Op: "+", Text: "certificate for " + strings.Join(domains, ", ")},
		{Op: " ", Text: "issued by Let's Encrypt through NPM, validated over HTTP on port 80"},
		{Op: " ", Text: "contact " + row.AdminEmail},
		{Op: " ", Text: "the request waits up to 10 minutes for the ACME order"},
	}
	hash := certificateSum([]byte(strings.Join(domains, ",") + "|" + row.AdminEmail))
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{}, StateHash: hash}, nil
}

// Apply orders the certificate. The body carries no nice_name, which NPM overwrites, and no meta: NPM refuses the
// letsencrypt members and takes the ACME contact from its admin user, whose email is the instance's admin email.
func (h certificateRequestHandler) Apply(ctx context.Context, env core.Env, in CertificateInput) (CertificateView, error) {
	domains, err := certificateDomains(in.Domains)
	if err != nil {
		return CertificateView{}, err
	}
	api, _, err := h.n.certificateClient(ctx, env)
	if err != nil {
		return CertificateView{}, err
	}
	var created npmapi.Certificate
	e := npmEffect(core.EffectCreate, resourceOf(nounCertificate, "?"+certificateIdentity(domains)), json.RawMessage("null"), certCommitWait)
	err = h.n.run(ctx, env, e, h.n.observeCertificate, func(ctx context.Context) (string, error) {
		c, err := api.CreateCertificate(ctx, npmapi.CertificateBody{Provider: "letsencrypt", DomainNames: domains})
		if err != nil {
			return "", err
		}
		created = c
		return resourceOf(nounCertificate, strconv.Itoa(c.ID)), nil
	})
	if err != nil {
		return CertificateView{}, err
	}
	return certificateView(created), nil
}

// Verify checks that NPM holds a certificate for exactly the input's names: the one out names, or, without an id in
// out, any certificate.
func (h certificateRequestHandler) Verify(ctx context.Context, env core.Env, in CertificateInput, out CertificateView) error {
	domains, err := certificateDomains(in.Domains)
	if err != nil {
		return err
	}
	api, _, err := h.n.certificateClient(ctx, env)
	if err != nil {
		return err
	}
	want := certificateIdentity(domains)
	if out.ID != 0 {
		c, err := api.Certificate(ctx, out.ID)
		if err != nil {
			return fmt.Errorf("npm: read certificate %d: %w", out.ID, err)
		}
		if got := certificateIdentity(c.DomainNames); got != want {
			return fmt.Errorf("npm: certificate %d covers %s, want %s", out.ID, got, want)
		}
		return nil
	}
	certs, err := api.Certificates(ctx)
	if err != nil {
		return fmt.Errorf("npm: list the certificates: %w", err)
	}
	if !slices.ContainsFunc(certs, func(c npmapi.Certificate) bool { return certificateIdentity(c.DomainNames) == want }) {
		return fmt.Errorf("npm: no certificate covers exactly %s", want)
	}
	return nil
}

// Observe reads the certificate of the effect.
func (h certificateRequestHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	return h.n.observeCertificate(ctx, env, e)
}

// Inverse deletes the certificate the request made, by the id its effect recorded, unless a proxy host now uses it.
// It does not check npm_reserved: a composite's own unwind undoes the certificate its child created.
func (h certificateRequestHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	api, _, err := h.n.certificateClient(ctx, env)
	if err != nil {
		return core.InverseResult{}, err
	}
	id, err := strconv.Atoi(identityOf(e.Resource))
	if err != nil {
		return core.InverseResult{}, fmt.Errorf("npm: %q is not a made certificate", e.Resource)
	}
	if _, err := api.Certificate(ctx, id); npmapi.IsNotFound(err) {
		return core.InverseResult{}, nil
	} else if err != nil {
		return core.InverseResult{}, fmt.Errorf("npm: read certificate %d: %w", id, err)
	}
	if _, err := certificateUsers(ctx, api, id); err != nil {
		return core.InverseResult{}, err
	}
	err = api.DeleteCertificate(ctx, id)
	switch {
	case err == nil, npmapi.IsNotFound(err):
		return core.InverseResult{}, nil
	case npmapi.IsClientError(err):
		return core.InverseResult{}, core.Definite(err)
	default:
		return core.InverseResult{}, err
	}
}

// Resolved watches the domain set of a creation resolved skipped, so the list read marks a certificate that covers it
// unclaimed.
func (h certificateRequestHandler) Resolved(ctx context.Context, tx *sql.Tx, e core.Effect, _ string) error {
	if e.State != core.EffectSkipped || e.Recovery == nil {
		return nil
	}
	return addWatch(ctx, tx, "certificate", e.Recovery.Value, e.ChangeID, e.Seq)
}

// target reads the certificate the removal deletes and decides whether it may; every failure is Definite. The checks
// run in this order: the id, the instance, the certificate, its reservation, its users. found is true when the
// certificate is reserved and the removal is its owner's.
func (h certificateRemoveHandler) target(ctx context.Context, env core.Env, in CertificateRemoveInput) (npmAPI, npmapi.Certificate, reservedRow, bool, error) {
	if in.ID <= 0 {
		return nil, npmapi.Certificate{}, reservedRow{}, false, certificateInvalid("/id", "a certificate id is required")
	}
	api, _, err := h.n.certificateClient(ctx, env)
	if err != nil {
		return nil, npmapi.Certificate{}, reservedRow{}, false, err
	}
	c, err := api.Certificate(ctx, in.ID)
	if npmapi.IsNotFound(err) {
		return nil, npmapi.Certificate{}, reservedRow{}, false, core.Definite(errcode.New(errcode.NotFound, fmt.Sprintf("certificate %d not found", in.ID)))
	}
	if err != nil {
		return nil, npmapi.Certificate{}, reservedRow{}, false, core.Definite(fmt.Errorf("npm: read certificate %d: %w", in.ID, err))
	}
	row, found, err := certificateReservedBy(ctx, env, in.ID)
	if err != nil {
		return nil, npmapi.Certificate{}, reservedRow{}, false, err
	}
	if _, err := certificateUsers(ctx, api, in.ID); err != nil {
		return nil, npmapi.Certificate{}, reservedRow{}, false, core.Definite(err)
	}
	return api, c, row, found, nil
}

// Plan shows the certificate the removal deletes; the hash covers its state document, and the operator confirms by
// typing its name.
func (h certificateRemoveHandler) Plan(ctx context.Context, env core.Env, in CertificateRemoveInput) (core.PlanOutcome, error) {
	_, c, _, _, err := h.target(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	doc, err := certificateState(c)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{
		{Op: "-", Text: fmt.Sprintf("certificate %s (id %d)", certificateLabel(c), c.ID)},
		{Op: " ", Text: "domains " + strings.Join(c.DomainNames, ", ")},
	}
	if c.ExpiresOn != "" {
		diff = append(diff, core.DiffLine{Op: " ", Text: "expires " + c.ExpiresOn})
	}
	diff = append(diff, core.DiffLine{Op: " ", Text: "the certificate cannot be recovered"})
	b, err := json.Marshal(doc)
	if err != nil {
		return core.PlanOutcome{}, fmt.Errorf("npm: render the plan state: %w", err)
	}
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{}, StateHash: certificateSum(b), Confirmation: certificateLabel(c)}, nil
}

// Apply deletes the certificate and, under its owner's removal, releases the objects its owner reserved under the exposure.
func (h certificateRemoveHandler) Apply(ctx context.Context, env core.Env, in CertificateRemoveInput) (CertificateView, error) {
	api, c, row, reserved, err := h.target(ctx, env, in)
	if err != nil {
		return CertificateView{}, err
	}
	doc, err := certificateState(c)
	if err != nil {
		return CertificateView{}, err
	}
	resource := resourceOf(nounCertificate, strconv.Itoa(c.ID))
	e := npmEffect(core.EffectDelete, resource, doc.State, certCommitWait)
	e.Before.Version = doc.Version
	err = h.n.run(ctx, env, e, h.n.observeCertificate, func(ctx context.Context) (string, error) {
		if err := api.DeleteCertificate(ctx, c.ID); err != nil {
			return "", err
		}
		return resource, nil
	})
	if err != nil {
		return CertificateView{}, err
	}
	view := certificateView(c)
	if reserved {
		err := env.DB.Write(ctx, func(tx *sql.Tx) error { return proxyHostEditRelease(ctx, tx, row.Exposure, row.Owner) })
		if err != nil {
			return CertificateView{}, err
		}
		view.Reserved = row.Owner
	}
	return view, nil
}

// Verify checks that the certificate is gone.
func (h certificateRemoveHandler) Verify(ctx context.Context, env core.Env, in CertificateRemoveInput, out CertificateView) error {
	id := out.ID
	if id == 0 {
		id = in.ID
	}
	api, _, err := h.n.certificateClient(ctx, env)
	if err != nil {
		return err
	}
	_, err = api.Certificate(ctx, id)
	switch {
	case npmapi.IsNotFound(err):
		return nil
	case err != nil:
		return fmt.Errorf("npm: read certificate %d: %w", id, err)
	default:
		return fmt.Errorf("npm: certificate %d still exists", id)
	}
}

// Observe reads the certificate of the effect.
func (h certificateRemoveHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	return h.n.observeCertificate(ctx, env, e)
}

// observeCertificate is the Observer of certificates; it only reads. A creation is found by its exact domain set and a
// created_on not before the intent, truncated to the second because NPM's created_on has whole seconds. The answer is
// never exclusive: a certificate the operator or an earlier request made for the same names looks the same, so an
// intended creation with a match is left for the operator's reconcile. Anything else is read by its id.
func (n nm) observeCertificate(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	noun, name, creation, err := creationOf(e.Resource)
	if err != nil {
		return core.Observation{}, err
	}
	if noun != nounCertificate {
		return core.Observation{}, fmt.Errorf("npm: %q is not a certificate", e.Resource)
	}
	api, err := n.d.api(ctx, env)
	if err != nil {
		return core.Observation{}, err
	}
	if !creation {
		id, err := strconv.Atoi(name)
		if err != nil {
			return core.Observation{}, fmt.Errorf("npm: %q does not name a certificate by its id", e.Resource)
		}
		c, err := api.Certificate(ctx, id)
		if npmapi.IsNotFound(err) {
			return named(nullState().State), nil
		}
		if err != nil {
			return core.Observation{}, fmt.Errorf("npm: read certificate %d: %w", id, err)
		}
		doc, err := certificateState(c)
		if err != nil {
			return core.Observation{}, err
		}
		return named(doc.State), nil
	}
	certs, err := api.Certificates(ctx)
	if err != nil {
		return core.Observation{}, fmt.Errorf("npm: list the certificates: %w", err)
	}
	intent := e.CommitBy.Add(-e.CommitWait).Truncate(time.Second)
	var candidates []core.Candidate
	for _, c := range certs {
		if certificateIdentity(c.DomainNames) != name {
			continue
		}
		createdOn, err := certificateCreatedOn(c.CreatedOn)
		if err != nil {
			return core.Observation{}, fmt.Errorf("npm: certificate %d: %w", c.ID, err)
		}
		if createdOn.Before(intent) {
			continue
		}
		doc, err := certificateState(c)
		if err != nil {
			return core.Observation{}, err
		}
		candidates = append(candidates, core.Candidate{Resource: resourceOf(nounCertificate, strconv.Itoa(c.ID)), State: doc})
	}
	obs := found(candidates)
	obs.Exclusive = false
	return obs, nil
}
