package npm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

// ceAPI is the module's NPM client with CreateCertificate replaced.
type ceAPI struct {
	npmAPI
	create func(ctx context.Context, b npmapi.CertificateBody) (npmapi.Certificate, error)
}

func (a ceAPI) CreateCertificate(ctx context.Context, b npmapi.CertificateBody) (npmapi.Certificate, error) {
	return a.create(ctx, b)
}

// ceReplaceCreate returns n with its NPM client answering CreateCertificate
// through create, which also receives the live client.
func ceReplaceCreate(n nm, create func(ctx context.Context, live npmAPI, b npmapi.CertificateBody) (npmapi.Certificate, error)) nm {
	inner := n.d.api
	n.d.api = func(ctx context.Context, env core.Env) (npmAPI, error) {
		live, err := inner(ctx, env)
		if err != nil {
			return nil, err
		}
		return ceAPI{npmAPI: live, create: func(ctx context.Context, b npmapi.CertificateBody) (npmapi.Certificate, error) {
			return create(ctx, live, b)
		}}, nil
	}
	return n
}

// ceRecordBodies returns n with every CreateCertificate body appended to
// bodies before the live client handles it.
func ceRecordBodies(n nm, bodies *[]npmapi.CertificateBody) nm {
	return ceReplaceCreate(n, func(ctx context.Context, live npmAPI, b npmapi.CertificateBody) (npmapi.Certificate, error) {
		*bodies = append(*bodies, b)
		return live.CreateCertificate(ctx, b)
	})
}

func ceSetup(t *testing.T) (*thHost, nm, thInstance) {
	t.Helper()
	h := newTHHost(t)
	inst := h.deployed()
	return h, h.nm(), inst
}

func ceErr(t *testing.T, err error) *errcode.Error {
	t.Helper()
	var ec *errcode.Error
	if !errors.As(err, &ec) {
		t.Fatalf("error = %v, want an *errcode.Error", err)
	}
	return ec
}

func ceJSON(t *testing.T, raw []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return v
}

func ceCerts(t *testing.T, n nm, env core.Env) []npmapi.Certificate {
	t.Helper()
	api, err := n.d.api(context.Background(), env)
	if err != nil {
		t.Fatalf("open the NPM client: %v", err)
	}
	certs, err := api.Certificates(context.Background())
	if err != nil {
		t.Fatalf("list certificates: %v", err)
	}
	return certs
}

func ceReserve(t *testing.T, h *thHost, id int, owner string) {
	t.Helper()
	ctx := context.Background()
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		return addReserved(ctx, tx, reservedRow{Kind: "certificate", ObjectID: id, Owner: owner, Exposure: "npm.example.com", ChangeID: "c0"})
	})
	if err != nil {
		t.Fatalf("reserve certificate %d: %v", id, err)
	}
}

func TestCeSpecs(t *testing.T) {
	n := newTHHost(t).nm()
	req, rem := n.certificateRequest().Spec, n.certificateRemove().Spec
	if req.Name != "npm.certificate.request" || req.Version != 1 || req.Class != core.ClassCompensable || req.Side != core.SideAgent ||
		!req.LongRunning || req.ApplyTimeout != 10*time.Minute || !slices.Equal(req.LockKeys, []string{"npm"}) {
		t.Errorf("request spec = %+v", req)
	}
	if rem.Name != "npm.certificate.remove" || rem.Version != 1 || rem.Class != core.ClassIrreversible || rem.Side != core.SideAgent ||
		rem.LongRunning || !slices.Equal(rem.LockKeys, []string{"npm"}) {
		t.Errorf("remove spec = %+v", rem)
	}
	for _, s := range []core.ActionSpec{req, rem} {
		if core.RequiredScope(s) != core.ScopeOperate {
			t.Errorf("%s: scope = %s, want operate", s.Name, core.RequiredScope(s))
		}
	}
	rh, mh := n.certificateRequest().Handler, n.certificateRemove().Handler
	if rh.Observe == nil || rh.Inverse == nil || rh.Resolved == nil {
		t.Errorf("request handler misses Observe, Inverse or Resolved: %+v", rh)
	}
	if mh.Observe == nil || mh.Inverse != nil {
		t.Errorf("remove handler: Observe set = %v, Inverse set = %v, want true and false", mh.Observe != nil, mh.Inverse != nil)
	}
}

func TestCeRequestPlan(t *testing.T) {
	ctx := context.Background()
	h, n, inst := ceSetup(t)
	handler := certificateRequestHandler{n: n}
	out, err := handler.Plan(ctx, h.env(""), CertificateInput{Domains: []string{"B.example.com", "a.example.com"}})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wantDiff := []core.DiffLine{
		{Op: "+", Text: "certificate for a.example.com, b.example.com"},
		{Op: " ", Text: "issued by Let's Encrypt through NPM, validated over HTTP on port 80"},
		{Op: " ", Text: "contact " + inst.Email},
		{Op: " ", Text: "the request waits up to 10 minutes for the ACME order"},
	}
	if !reflect.DeepEqual(out.Diff, wantDiff) || out.StateHash == "" {
		t.Errorf("plan = %+v, want diff %+v and a state hash", out, wantDiff)
	}
	same, err := handler.Plan(ctx, h.env(""), CertificateInput{Domains: []string{"a.example.com", "b.example.com"}})
	if err != nil || same.StateHash != out.StateHash {
		t.Errorf("the same names in another order: hash %q, error %v, want %q", same.StateHash, err, out.StateHash)
	}
	other, err := handler.Plan(ctx, h.env(""), CertificateInput{Domains: []string{"a.example.com"}})
	if err != nil || other.StateHash == out.StateHash {
		t.Errorf("other names: hash %q, error %v, want a different hash", other.StateHash, err)
	}
}

func TestCeRequestPlanRefusesInvalidDomains(t *testing.T) {
	h, n, _ := ceSetup(t)
	handler := certificateRequestHandler{n: n}
	for _, tc := range []struct {
		name    string
		domains []string
		path    string
	}{
		{"none", nil, "/domains"},
		{"wildcard", []string{"*.example.com"}, "/domains/0"},
		{"single label", []string{"localhost"}, "/domains/0"},
		{"ip address", []string{"192.0.2.1"}, "/domains/0"},
		{"underscore", []string{"ok.example.com", "my_host.example.com"}, "/domains/1"},
		{"leading hyphen", []string{"-a.example.com"}, "/domains/0"},
		{"duplicate after lowercasing", []string{"a.example.com", "A.example.com"}, "/domains/1"},
		{"label too long", []string{strings.Repeat("a", 64) + ".example.com"}, "/domains/0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := handler.Plan(context.Background(), h.env(""), CertificateInput{Domains: tc.domains})
			if !core.IsDefinite(err) {
				t.Fatalf("error = %v, want a Definite refusal", err)
			}
			ec := ceErr(t, err)
			if ec.Code != errcode.InvalidInput || len(ec.Fields) == 0 || ec.Fields[0].Path != tc.path {
				t.Errorf("error = %+v, want request.invalid_input at %s", ec, tc.path)
			}
		})
	}
}

func TestCeRequestNeedsAnAnsweringInstance(t *testing.T) {
	ctx := context.Background()
	bare := newTHHost(t)
	bare.addChange("c1", "npm.certificate.request")
	handler := certificateRequestHandler{n: bare.nm()}
	in := CertificateInput{Domains: []string{"a.example.com"}}
	for name, run := range map[string]func() error{
		"plan":  func() error { _, err := handler.Plan(ctx, bare.env(""), in); return err },
		"apply": func() error { _, err := handler.Apply(ctx, bare.env("c1"), in); return err },
	} {
		err := run()
		if !core.IsDefinite(err) || ceErr(t, err).Code != errcode.NotFound {
			t.Errorf("%s without an instance: error = %v, want a Definite not_found", name, err)
		}
	}
	if got := bare.journal.effects(); len(got) != 0 {
		t.Errorf("journaled %d effects without an instance, want none", len(got))
	}

	h, n, _ := ceSetup(t)
	n.d.api = func(context.Context, core.Env) (npmAPI, error) { return nil, errors.New("npm: connection refused") }
	_, err := certificateRequestHandler{n: n}.Plan(ctx, h.env(""), in)
	if !core.IsDefinite(err) || ceErr(t, err).Code != errcode.NPMUnhealthy {
		t.Errorf("plan with NPM down: error = %v, want a Definite npm.unhealthy", err)
	}
}

func TestCeRequestApplySendsTheLetsEncryptBodyAndJournalsTheDomainSet(t *testing.T) {
	ctx := context.Background()
	h, n, _ := ceSetup(t)
	h.addChange("c1", "npm.certificate.request")
	env := h.env("c1")
	var bodies []npmapi.CertificateBody
	handler := certificateRequestHandler{n: ceRecordBodies(n, &bodies)}
	inputs := []CertificateInput{
		{Domains: []string{"B.example.com", "a.example.com"}},
		{Domains: []string{"app.example.org"}},
	}
	var views []CertificateView
	for i, in := range inputs {
		v, err := handler.Apply(ctx, env, in)
		if err != nil {
			t.Fatalf("Apply %d: %v", i+1, err)
		}
		views = append(views, v)
	}
	wantDomains := [][]string{{"a.example.com", "b.example.com"}, {"app.example.org"}}
	effects := h.journal.effects()
	if len(bodies) != 2 || len(effects) != 2 {
		t.Fatalf("%d bodies and %d effects, want 2 each", len(bodies), len(effects))
	}
	for i, e := range effects {
		identity := strings.Join(wantDomains[i], ",")
		wantBody := npmapi.CertificateBody{
			Provider: "letsencrypt", DomainNames: wantDomains[i],
		}
		if !reflect.DeepEqual(bodies[i], wantBody) {
			t.Errorf("body %d = %+v, want %+v and no nice_name", i+1, bodies[i], wantBody)
		}
		v := views[i]
		if v.Name != strings.Join(wantDomains[i], ", ") || !reflect.DeepEqual(v.Domains, wantDomains[i]) || v.Reserved != "" || v.Unclaimed {
			t.Errorf("view %d = %+v, want the name NPM gave (the domains joined by \", \") and domains %v", i+1, v, wantDomains[i])
		}
		if e.Seq != i+1 || e.Kind != core.EffectCreate || e.State != core.EffectMade || e.Local || e.CommitWait != 10*time.Minute ||
			e.Resource != "npm.certificate:"+strconv.Itoa(v.ID) || !reflect.DeepEqual(e.Recovery, &core.RecoveryID{Kind: "name", Value: identity}) || e.Post == nil {
			t.Errorf("effect %d = %+v, want a made creation recovered by the name %q", i+1, e, identity)
		}
	}
	posts := 0
	for _, r := range h.npm.Requests() {
		if r.Method == http.MethodPost && r.Path == "/api/nginx/certificates" {
			posts++
			if strings.Contains(string(r.Body), "nice_name") {
				t.Errorf("the request body %s carries a nice_name", r.Body)
			}
		}
	}
	if posts != 2 {
		t.Errorf("NPM received %d certificate creations, want 2", posts)
	}
	wantState := ceJSON(t, []byte(`{"name":"a.example.com, b.example.com","provider":"letsencrypt","domains":["a.example.com","b.example.com"]}`))
	if got := ceJSON(t, effects[0].Post.State); !reflect.DeepEqual(got, wantState) {
		t.Errorf("post state = %v, want %v", got, wantState)
	}
	if err := handler.Verify(ctx, env, inputs[0], views[0]); err != nil {
		t.Errorf("Verify: %v", err)
	}
	api, err := n.d.api(ctx, env)
	if err != nil {
		t.Fatal(err)
	}
	if err := api.DeleteCertificate(ctx, views[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := handler.Verify(ctx, env, inputs[1], views[1]); err == nil {
		t.Error("Verify passed for a certificate NPM no longer holds")
	}
}

func TestCeRequestVerifyFindsTheCertificateFromTheChangeAlone(t *testing.T) {
	ctx := context.Background()
	h, n, _ := ceSetup(t)
	h.addChange("c1", "npm.certificate.request")
	env := h.env("c1")
	handler := certificateRequestHandler{n: n}
	in := CertificateInput{Domains: []string{"a.example.com"}}
	if _, err := handler.Apply(ctx, env, in); err != nil {
		t.Fatal(err)
	}
	if err := handler.Verify(ctx, env, in, CertificateView{}); err != nil {
		t.Errorf("Verify with the zero output: %v", err)
	}
	if err := handler.Verify(ctx, env, CertificateInput{Domains: []string{"other.example.com"}}, CertificateView{}); err == nil {
		t.Error("Verify with the zero output passed for other domains")
	}
}

func TestCeRequestRefusedByNPMIsDefiniteAndSkipped(t *testing.T) {
	ctx := context.Background()
	h, n, _ := ceSetup(t)
	h.addChange("c1", "npm.certificate.request")
	n = ceReplaceCreate(n, func(context.Context, npmAPI, npmapi.CertificateBody) (npmapi.Certificate, error) {
		return npmapi.Certificate{}, &npmapi.APIError{Status: 400}
	})
	_, err := certificateRequestHandler{n: n}.Apply(ctx, h.env("c1"), CertificateInput{Domains: []string{"a.example.com"}})
	if !core.IsDefinite(err) {
		t.Fatalf("error = %v, want a Definite refusal", err)
	}
	effects := h.journal.effects()
	if len(effects) != 1 || effects[0].State != core.EffectSkipped {
		t.Errorf("effects = %+v, want one skipped effect", effects)
	}
}

func TestCeRequestLostResponseLeavesTheEffectObservable(t *testing.T) {
	ctx := context.Background()
	h, n, _ := ceSetup(t)
	h.addChange("c1", "npm.certificate.request")
	env := h.env("c1")
	n = ceReplaceCreate(n, func(ctx context.Context, live npmAPI, b npmapi.CertificateBody) (npmapi.Certificate, error) {
		if _, err := live.CreateCertificate(ctx, b); err != nil {
			return npmapi.Certificate{}, err
		}
		return npmapi.Certificate{}, errors.New("npm: connection reset by peer")
	})
	handler := certificateRequestHandler{n: n}
	_, err := handler.Apply(ctx, env, CertificateInput{Domains: []string{"a.example.com"}})
	if err == nil || core.IsDefinite(err) {
		t.Fatalf("error = %v, want a failure that is not Definite", err)
	}
	effects := h.journal.effects()
	if len(effects) != 1 || effects[0].State != core.EffectIntended || effects[0].Resource != "npm.certificate:?a.example.com" {
		t.Fatalf("effects = %+v, want one intended creation of npm.certificate:?a.example.com", effects)
	}
	certs := ceCerts(t, n, env)
	if len(certs) != 1 {
		t.Fatalf("NPM holds %d certificates, want the one the lost response created", len(certs))
	}
	created, err := certificateCreatedOn(certs[0].CreatedOn)
	if err != nil {
		t.Fatal(err)
	}
	e := effects[0]
	e.CommitBy = created.Add(-time.Second).Add(e.CommitWait)
	obs, err := handler.Observe(ctx, env, e)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.Matches != 1 || obs.Exclusive || obs.Resource != "npm.certificate:"+strconv.Itoa(certs[0].ID) {
		t.Errorf("observation = %+v, want one match of certificate %d that is not exclusive", obs, certs[0].ID)
	}
}

// ceCreation is an intended creation of the certificate for domains whose
// intent time is intent.
func ceCreation(domains []string, intent time.Time) core.Effect {
	identity := certificateIdentity(domains)
	return core.Effect{
		ChangeID: "c1", Seq: 1, Kind: core.EffectCreate, Resource: "npm.certificate:?" + identity,
		Recovery: &core.RecoveryID{Kind: "name", Value: identity}, State: core.EffectIntended,
		CommitWait: certCommitWait, CommitBy: intent.Add(certCommitWait),
	}
}

func TestCeCreatedOn(t *testing.T) {
	want := time.Date(2026, 11, 2, 10, 0, 1, 0, time.UTC)
	for _, s := range []string{"2026-11-02T10:00:01Z", "2026-11-02T10:00:01.000Z", "2026-11-02 10:00:01"} {
		if got, err := certificateCreatedOn(s); err != nil || !got.Equal(want) {
			t.Errorf("%q parses to %v (%v), want %v", s, got, err, want)
		}
	}
	if _, err := certificateCreatedOn("yesterday"); err == nil {
		t.Error("an unparseable time was accepted")
	}
}

func TestCeIdentityIsTheSortedDomainSet(t *testing.T) {
	in := []string{"b.example.com", "a.example.com"}
	if got := certificateIdentity(in); got != "a.example.com,b.example.com" {
		t.Errorf("identity = %q, want the sorted names joined by a comma", got)
	}
	if in[0] != "b.example.com" {
		t.Errorf("the identity sorted its argument in place: %v", in)
	}
}

func TestCeObserveCreationMatchesTheExactDomainSetNotBeforeTheIntent(t *testing.T) {
	ctx := context.Background()
	h, n, _ := ceSetup(t)
	handler := certificateRequestHandler{n: n}
	const before, after = "2026-11-02T09:00:00.000Z", "2026-11-02T10:00:05.000Z"
	seed := func(createdOn string, domains ...string) npmapi.Certificate {
		return h.npm.SeedCertificate(npmapi.Certificate{
			CreatedOn: createdOn, NiceName: strings.Join(domains, ", "), Provider: "letsencrypt", DomainNames: domains,
		})
	}
	wanted := []string{"b.example.com", "a.example.com"}
	e := ceCreation(wanted, time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC))

	none, err := handler.Observe(ctx, h.env("c1"), e)
	if err != nil {
		t.Fatal(err)
	}
	if none.Matches != 0 || string(none.Current.State) != "null" || none.Exclusive || len(none.Candidates) != 0 {
		t.Errorf("no certificate: observation = %+v, want Matches 0, state null and not exclusive", none)
	}

	seed(before, "a.example.com", "b.example.com")
	seed(after, "a.example.com")
	seed(after, "a.example.com", "b.example.com", "c.example.com")
	seed(after, "a.example.com", "x.example.com")
	none, err = handler.Observe(ctx, h.env("c1"), e)
	if err != nil {
		t.Fatal(err)
	}
	if none.Matches != 0 || string(none.Current.State) != "null" || none.Exclusive {
		t.Errorf("same domains created before the intent, a subset, a superset and another set: observation = %+v, want Matches 0 and state null", none)
	}

	one := seed(after, "b.example.com", "a.example.com")
	wantState, err := certificateState(one)
	if err != nil {
		t.Fatal(err)
	}
	for name, intent := range map[string]time.Time{
		"an intent just before the creation":          time.Date(2026, 11, 2, 10, 0, 4, 0, time.UTC),
		"an intent within the second of the creation": time.Date(2026, 11, 2, 10, 0, 5, 700_000_000, time.UTC),
		"an intent long before the creation":          time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC),
	} {
		obs, err := handler.Observe(ctx, h.env("c1"), ceCreation(wanted, intent))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if obs.Matches != 1 || obs.Exclusive || obs.Resource != "npm.certificate:"+strconv.Itoa(one.ID) || string(obs.Current.State) != string(wantState.State) {
			t.Errorf("%s: observation = %+v, want one match of certificate %d, not exclusive, in state %s", name, obs, one.ID, wantState.State)
		}
	}
	late, err := handler.Observe(ctx, h.env("c1"), ceCreation(wanted, time.Date(2026, 11, 2, 10, 0, 6, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	if late.Matches != 0 || string(late.Current.State) != "null" {
		t.Errorf("an intent after the creation: observation = %+v, want Matches 0 and state null", late)
	}

	seed(after, "a.example.com", "b.example.com")
	two, err := handler.Observe(ctx, h.env("c1"), e)
	if err != nil {
		t.Fatal(err)
	}
	if two.Matches != 2 || len(two.Candidates) != 2 || string(two.Current.State) != "null" || two.Exclusive {
		t.Fatalf("two certificates: observation = %+v, want Matches 2, two candidates, state null and not exclusive", two)
	}
	if !slices.IsSortedFunc(two.Candidates, func(a, b core.Candidate) int { return strings.Compare(a.Resource, b.Resource) }) {
		t.Errorf("candidates %+v are not sorted by resource", two.Candidates)
	}
}

func TestCeRequestUndoDeletesTheCertificateAndRefusesOneInUse(t *testing.T) {
	ctx := context.Background()
	h, n, _ := ceSetup(t)
	h.addChange("c1", "npm.certificate.request")
	env := h.env("c1")
	handler := certificateRequestHandler{n: n}
	view, err := handler.Apply(ctx, env, CertificateInput{Domains: []string{"app.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	made := h.journal.effects()[0]

	h.npm.SeedProxyHost(npmapi.ProxyHost{DomainNames: []string{"app.example.com"}, ForwardScheme: "http", ForwardHost: "app", ForwardPort: 8080, CertificateID: view.ID})
	_, err = handler.Inverse(ctx, env, made)
	if !core.IsDefinite(err) || ceErr(t, err).Code != errcode.InvalidInput || !strings.Contains(ceErr(t, err).Detail, "app.example.com") {
		t.Fatalf("undo of a certificate in use: error = %v, want a Definite invalid_input naming the proxy host", err)
	}
	if len(ceCerts(t, n, env)) != 1 {
		t.Fatal("the certificate in use was deleted")
	}

	api, err := n.d.api(ctx, env)
	if err != nil {
		t.Fatal(err)
	}
	hosts, err := api.ProxyHosts(ctx)
	if err != nil || len(hosts) != 1 {
		t.Fatalf("proxy hosts = %v, %v", hosts, err)
	}
	if err := api.DeleteProxyHost(ctx, hosts[0].ID); err != nil {
		t.Fatal(err)
	}
	if res, err := handler.Inverse(ctx, env, made); err != nil || res.Resource != "" {
		t.Fatalf("Inverse = %+v, %v, want an empty result", res, err)
	}
	if len(ceCerts(t, n, env)) != 0 {
		t.Error("the certificate is still there after the undo")
	}
	if _, err := handler.Inverse(ctx, env, made); err != nil {
		t.Errorf("undoing a certificate that is already gone: %v", err)
	}
	if obs, err := handler.Observe(ctx, env, made); err != nil || string(obs.Current.State) != "null" {
		t.Errorf("Observe after the undo = %+v, %v, want state null", obs, err)
	}
}

func TestCeResolvedWatchesOnlyASkippedCreation(t *testing.T) {
	ctx := context.Background()
	h, n, _ := ceSetup(t)
	handler := certificateRequestHandler{n: n}
	created := func(seq int, domains []string, state core.EffectState) core.Effect {
		identity := certificateIdentity(domains)
		return core.Effect{
			ChangeID: "c1", Seq: seq, Kind: core.EffectCreate, Resource: "npm.certificate:?" + identity,
			Recovery: &core.RecoveryID{Kind: "name", Value: identity}, State: state,
		}
	}
	skipped := created(3, []string{"b.example.com", "a.example.com"}, core.EffectSkipped)
	accepted := created(4, []string{"c.example.com"}, core.EffectAccepted)
	for _, e := range []core.Effect{skipped, accepted} {
		err := h.db.Write(ctx, func(tx *sql.Tx) error { return handler.Resolved(ctx, tx, e, "reconcile") })
		if err != nil {
			t.Fatalf("Resolved(%s): %v", e.State, err)
		}
	}
	watching, err := watched(ctx, h.db.Read(), "certificate")
	if err != nil {
		t.Fatal(err)
	}
	if !watching["a.example.com,b.example.com"] || watching["c.example.com"] || len(watching) != 1 {
		t.Errorf("watched certificates = %v, want only the sorted domain set a.example.com,b.example.com", watching)
	}
}

func TestCeRemovePlanApplyAndVerify(t *testing.T) {
	ctx := context.Background()
	h, n, _ := ceSetup(t)
	seeded := h.npm.SeedCertificate(npmapi.Certificate{
		NiceName: "old.example.com", Provider: "letsencrypt",
		DomainNames: []string{"old.example.com", "www.old.example.com"}, ExpiresOn: "2027-03-04 05:06:07",
	})
	handler := certificateRemoveHandler{n: n}
	in := CertificateRemoveInput{ID: seeded.ID}
	plan, err := handler.Plan(ctx, h.env(""), in)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wantDiff := []core.DiffLine{
		{Op: "-", Text: fmt.Sprintf("certificate old.example.com (id %d)", seeded.ID)},
		{Op: " ", Text: "domains old.example.com, www.old.example.com"},
		{Op: " ", Text: "expires 2027-03-04 05:06:07"},
		{Op: " ", Text: "the certificate cannot be recovered"},
	}
	if !reflect.DeepEqual(plan.Diff, wantDiff) || plan.StateHash == "" || plan.Confirmation != "old.example.com" {
		t.Errorf("plan = %+v, want diff %+v, a state hash and the confirmation old.example.com", plan, wantDiff)
	}
	if err := handler.Verify(ctx, h.env(""), in, CertificateView{}); err == nil {
		t.Error("Verify passed while the certificate still exists")
	}

	h.addChange("c2", "npm.certificate.remove")
	env := h.env("c2")
	view, err := handler.Apply(ctx, env, in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	wantView := CertificateView{ID: seeded.ID, Name: "old.example.com", Domains: []string{"old.example.com", "www.old.example.com"}, ExpiresOn: "2027-03-04 05:06:07"}
	if !reflect.DeepEqual(view, wantView) {
		t.Errorf("view = %+v, want %+v", view, wantView)
	}
	if len(ceCerts(t, n, env)) != 0 {
		t.Error("the certificate is still in NPM")
	}
	effects := h.journal.effects()
	if len(effects) != 1 {
		t.Fatalf("journaled %d effects, want 1", len(effects))
	}
	e := effects[0]
	if e.Kind != core.EffectDelete || e.State != core.EffectMade || e.Local || e.CommitWait != 10*time.Minute ||
		e.Resource != "npm.certificate:"+strconv.Itoa(seeded.ID) || e.Recovery != nil {
		t.Errorf("effect = %+v", e)
	}
	wantBefore := ceJSON(t, []byte(`{"name":"old.example.com","provider":"letsencrypt","domains":["old.example.com","www.old.example.com"]}`))
	if got := ceJSON(t, e.Before.State); !reflect.DeepEqual(got, wantBefore) {
		t.Errorf("before state = %v, want %v", got, wantBefore)
	}
	if err := handler.Verify(ctx, env, in, view); err != nil {
		t.Errorf("Verify after the removal: %v", err)
	}
}

func TestCeRemoveRefusals(t *testing.T) {
	ctx := context.Background()
	h, n, _ := ceSetup(t)
	handler := certificateRemoveHandler{n: n}
	used := h.npm.SeedCertificate(npmapi.Certificate{NiceName: "app.example.com", Provider: "letsencrypt", DomainNames: []string{"app.example.com"}})
	h.npm.SeedProxyHost(npmapi.ProxyHost{DomainNames: []string{"app.example.com"}, ForwardScheme: "http", ForwardHost: "app", ForwardPort: 8080, CertificateID: used.ID})
	for _, tc := range []struct {
		name   string
		id     int
		code   errcode.Code
		detail string
	}{
		{"zero id", 0, errcode.InvalidInput, ""},
		{"unknown certificate", 4242, errcode.NotFound, ""},
		{"certificate in use", used.ID, errcode.InvalidInput, "app.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := handler.Plan(ctx, h.env(""), CertificateRemoveInput{ID: tc.id})
			if !core.IsDefinite(err) {
				t.Fatalf("error = %v, want a Definite refusal", err)
			}
			if ec := ceErr(t, err); ec.Code != tc.code || !strings.Contains(ec.Detail, tc.detail) {
				t.Errorf("error = %+v, want %s containing %q", ec, tc.code, tc.detail)
			}
		})
	}
	if len(ceCerts(t, n, h.env(""))) != 1 {
		t.Error("a refused removal changed NPM")
	}
}

func TestCeRemoveOfAReservedCertificateNeedsTheOwnersRemoval(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		owner  string
		parent string
		refuse bool
	}{
		{"operator", "npm.admin.expose", "", true},
		{"the owner's own composite", "npm.admin.expose", "npm.admin.expose", true},
		{"another owner's removal", "npm.admin.expose", "panel.exposure.remove", true},
		{"the admin exposure's removal", "npm.admin.expose", "npm.admin.unexpose", false},
		{"the panel exposure's removal", "panel.exposure.add", "panel.exposure.remove", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, n, _ := ceSetup(t)
			handler := certificateRemoveHandler{n: n}
			cert := h.npm.SeedCertificate(npmapi.Certificate{NiceName: "admin.example.com", Provider: "letsencrypt", DomainNames: []string{"admin.example.com"}})
			ceReserve(t, h, cert.ID, tc.owner)
			in := CertificateRemoveInput{ID: cert.ID}
			h.addChange("c3", "npm.certificate.remove")
			env := h.env("c3")
			if tc.parent != "" {
				env = h.child("c3", tc.parent)
			}
			_, err := handler.Plan(ctx, env, in)
			if tc.refuse {
				if !core.IsDefinite(err) || ceErr(t, err).Code != errcode.NPMReserved {
					t.Fatalf("Plan: error = %v, want a Definite npm.reserved", err)
				}
				if _, err := handler.Apply(ctx, env, in); err == nil || ceErr(t, err).Code != errcode.NPMReserved {
					t.Errorf("Apply: error = %v, want npm.reserved", err)
				}
				if len(ceCerts(t, n, h.env(""))) != 1 {
					t.Error("a refused removal deleted the certificate")
				}
				return
			}
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if _, err := handler.Apply(ctx, env, in); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if len(ceCerts(t, n, h.env(""))) != 0 {
				t.Error("the owner's removal left the certificate in NPM")
			}
			if _, found, err := reservedOf(ctx, h.db.Read(), "certificate", cert.ID); err != nil || found {
				t.Errorf("reservedOf after the removal = %v, %v, want no row", found, err)
			}
		})
	}
}

// TestRrCertificateRemoveReleasesOnlyItsOwnersReservations: removing a reserved certificate as its owner's removal
// releases the owner's records under the exposure and keeps another owner's records under the same hostname.
func TestRrCertificateRemoveReleasesOnlyItsOwnersReservations(t *testing.T) {
	ctx := context.Background()
	h, n, _ := ceSetup(t)
	cert := h.npm.SeedCertificate(npmapi.Certificate{NiceName: "panel.example.com", Provider: "letsencrypt", DomainNames: []string{"panel.example.com"}})
	ceReserve(t, h, cert.ID, "panel.exposure.add")
	rrReserve(t, h, "proxy_host", 41, "panel.exposure.add")
	rrReserve(t, h, "proxy_host", 42, "npm.admin.expose")
	rrReserve(t, h, "access_list", 43, "npm.admin.expose")
	h.addChange("c3", "npm.certificate.remove")
	view, err := (certificateRemoveHandler{n: n}).Apply(ctx, h.child("c3", "panel.exposure.remove"), CertificateRemoveInput{ID: cert.ID})
	if err != nil {
		t.Fatalf("Apply as the owner's removal: %v", err)
	}
	if view.Reserved != "panel.exposure.add" {
		t.Errorf("view reserved = %q, want the owner panel.exposure.add", view.Reserved)
	}
	rrWantReserved(t, h, []reservedRow{
		{Kind: "certificate", ObjectID: cert.ID},
		{Kind: "proxy_host", ObjectID: 41},
		{Kind: "proxy_host", ObjectID: 42, Owner: "npm.admin.expose"},
		{Kind: "access_list", ObjectID: 43, Owner: "npm.admin.expose"},
	})
}
