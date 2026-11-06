package npm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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

// alAPI is the module's NPM client with CreateAccessList replaced.
type alAPI struct {
	npmAPI
	create func(ctx context.Context, b npmapi.AccessListBody) (npmapi.AccessList, error)
}

func (a alAPI) CreateAccessList(ctx context.Context, b npmapi.AccessListBody) (npmapi.AccessList, error) {
	return a.create(ctx, b)
}

// alReplaceCreate returns n with its NPM client answering CreateAccessList
// through create, which also receives the live client.
func alReplaceCreate(n nm, create func(ctx context.Context, live npmAPI, b npmapi.AccessListBody) (npmapi.AccessList, error)) nm {
	inner := n.d.api
	n.d.api = func(ctx context.Context, env core.Env) (npmAPI, error) {
		live, err := inner(ctx, env)
		if err != nil {
			return nil, err
		}
		return alAPI{npmAPI: live, create: func(ctx context.Context, b npmapi.AccessListBody) (npmapi.AccessList, error) {
			return create(ctx, live, b)
		}}, nil
	}
	return n
}

func alSetup(t *testing.T) (*thHost, nm) {
	t.Helper()
	h := newTHHost(t)
	h.deployed()
	return h, h.nm()
}

func alInput() AccessListInput {
	return AccessListInput{Clients: []AccessClientInput{
		{Address: "10.0.0.0/8", Directive: "allow"},
		{Address: "10.9.0.5", Directive: "deny"},
	}}
}

func alErr(t *testing.T, err error) *errcode.Error {
	t.Helper()
	var ec *errcode.Error
	if !errors.As(err, &ec) {
		t.Fatalf("error = %v, want an *errcode.Error", err)
	}
	return ec
}

func alJSON(t *testing.T, raw []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return v
}

func alLists(t *testing.T, n nm, env core.Env) []npmapi.AccessList {
	t.Helper()
	api, err := n.d.api(context.Background(), env)
	if err != nil {
		t.Fatalf("open the NPM client: %v", err)
	}
	lists, err := api.AccessLists(context.Background())
	if err != nil {
		t.Fatalf("list access lists: %v", err)
	}
	return lists
}

func alReserve(t *testing.T, h *thHost, id int, owner string) {
	t.Helper()
	ctx := context.Background()
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		return addReserved(ctx, tx, reservedRow{Kind: "access_list", ObjectID: id, Owner: owner, Exposure: "npm.example.com", ChangeID: "c0"})
	})
	if err != nil {
		t.Fatalf("reserve access list %d: %v", id, err)
	}
}

func TestAlSpecs(t *testing.T) {
	n := newTHHost(t).nm()
	for _, tc := range []struct {
		def      core.ActionDef
		name     string
		resolver bool
	}{
		{n.accesslistAdd(), "npm.accesslist.add", true},
		{n.accesslistRemove(), "npm.accesslist.remove", false},
	} {
		s := tc.def.Spec
		if s.Name != tc.name || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent {
			t.Errorf("%s: spec = %+v", tc.name, s)
		}
		if !slices.Equal(s.LockKeys, []string{"npm"}) {
			t.Errorf("%s: LockKeys = %v, want [npm]", tc.name, s.LockKeys)
		}
		if core.RequiredScope(s) != core.ScopeOperate {
			t.Errorf("%s: scope = %s, want operate", tc.name, core.RequiredScope(s))
		}
		h := tc.def.Handler
		if h.Plan == nil || h.Apply == nil || h.Verify == nil || h.Observe == nil || h.Inverse == nil {
			t.Errorf("%s: a handler member is missing: %+v", tc.name, h)
		}
		if (h.Resolved != nil) != tc.resolver {
			t.Errorf("%s: Resolved set = %v, want %v", tc.name, h.Resolved != nil, tc.resolver)
		}
	}
}

func TestAlAddPlan(t *testing.T) {
	ctx := context.Background()
	h, n := alSetup(t)
	handler := accessListAddHandler{n: n}
	out, err := handler.Plan(ctx, h.env(""), alInput())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wantDiff := []core.DiffLine{
		{Op: "+", Text: "access list (named when applied)"},
		{Op: " ", Text: "satisfy all"},
		{Op: "+", Text: "allow 10.0.0.0/8"},
		{Op: "+", Text: "deny 10.9.0.5"},
	}
	if !reflect.DeepEqual(out.Diff, wantDiff) {
		t.Errorf("diff = %+v, want %+v", out.Diff, wantDiff)
	}
	again, err := handler.Plan(ctx, h.env(""), alInput())
	if err != nil {
		t.Fatalf("Plan again: %v", err)
	}
	if out.StateHash == "" || again.StateHash != out.StateHash {
		t.Errorf("state hashes %q and %q, want equal and non-empty", out.StateHash, again.StateHash)
	}
	loose := alInput()
	loose.SatisfyAny = true
	other, err := handler.Plan(ctx, h.env(""), loose)
	if err != nil {
		t.Fatalf("Plan satisfy any: %v", err)
	}
	if other.StateHash == out.StateHash || other.Diff[1].Text != "satisfy any" {
		t.Errorf("satisfy any plan = %+v, want another hash and the line %q", other, "satisfy any")
	}
}

func TestAlAddPlanRefusesInvalidInput(t *testing.T) {
	h, n := alSetup(t)
	handler := accessListAddHandler{n: n}
	for _, tc := range []struct {
		name string
		in   AccessListInput
		path string
	}{
		{"no clients", AccessListInput{}, "/clients"},
		{"not an address", AccessListInput{Clients: []AccessClientInput{{Address: "ten", Directive: "allow"}}}, "/clients/0/address"},
		{"an address with an IPv6 zone", AccessListInput{Clients: []AccessClientInput{{Address: "fe80::1%eth0", Directive: "allow"}}}, "/clients/0/address"},
		{"unmasked prefix", AccessListInput{Clients: []AccessClientInput{{Address: "10.0.0.5/8", Directive: "allow"}}}, "/clients/0/address"},
		{"unknown directive", AccessListInput{Clients: []AccessClientInput{{Address: "10.0.0.1", Directive: "allow"}, {Address: "10.0.0.2", Directive: "permit"}}}, "/clients/1/directive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := handler.Plan(context.Background(), h.env(""), tc.in)
			if !core.IsDefinite(err) {
				t.Fatalf("error = %v, want a Definite refusal", err)
			}
			ec := alErr(t, err)
			if ec.Code != errcode.InvalidInput || len(ec.Fields) == 0 || ec.Fields[0].Path != tc.path {
				t.Errorf("error = %+v, want request.invalid_input at %s", ec, tc.path)
			}
		})
	}
}

func TestAlAddNeedsAnAnsweringInstance(t *testing.T) {
	ctx := context.Background()
	bare := newTHHost(t)
	bare.addChange("c1", "npm.accesslist.add")
	handler := accessListAddHandler{n: bare.nm()}
	for name, run := range map[string]func() error{
		"plan":  func() error { _, err := handler.Plan(ctx, bare.env(""), alInput()); return err },
		"apply": func() error { _, err := handler.Apply(ctx, bare.env("c1"), alInput()); return err },
	} {
		err := run()
		if !core.IsDefinite(err) || alErr(t, err).Code != errcode.NotFound {
			t.Errorf("%s without an instance: error = %v, want a Definite not_found", name, err)
		}
	}
	if got := bare.journal.effects(); len(got) != 0 {
		t.Errorf("journaled %d effects without an instance, want none", len(got))
	}

	h, n := alSetup(t)
	n.d.api = func(context.Context, core.Env) (npmAPI, error) { return nil, errors.New("npm: connection refused") }
	_, err := accessListAddHandler{n: n}.Plan(ctx, h.env(""), alInput())
	if !core.IsDefinite(err) || alErr(t, err).Code != errcode.NPMUnhealthy {
		t.Errorf("plan with NPM down: error = %v, want a Definite npm.unhealthy", err)
	}
}

func TestAlAddApplyJournalsTheNamedIdentity(t *testing.T) {
	ctx := context.Background()
	h, n := alSetup(t)
	h.addChange("c1", "npm.accesslist.add")
	env := h.env("c1")
	handler := accessListAddHandler{n: n}
	inputs := []AccessListInput{
		alInput(),
		{SatisfyAny: true, Clients: []AccessClientInput{{Address: "2001:db8::/32", Directive: "allow"}}},
	}
	var views []AccessListView
	for i, in := range inputs {
		v, err := handler.Apply(ctx, env, in)
		if err != nil {
			t.Fatalf("Apply %d: %v", i+1, err)
		}
		views = append(views, v)
	}
	effects := h.journal.effects()
	if len(effects) != 2 {
		t.Fatalf("journaled %d effects, want 2", len(effects))
	}
	for i, e := range effects {
		name := fmt.Sprintf("darkpanel-c1-%d", i+1)
		v := views[i]
		if v.Name != name || v.SatisfyAny != inputs[i].SatisfyAny || !reflect.DeepEqual(v.Clients, inputs[i].Clients) || v.Reserved != "" || v.Unclaimed {
			t.Errorf("view %d = %+v, want name %s and the input's rules", i+1, v, name)
		}
		if e.Seq != i+1 || e.Kind != core.EffectCreate || e.State != core.EffectMade || e.Local || e.CommitWait != 2*time.Minute ||
			e.Resource != "npm.accesslist:"+strconv.Itoa(v.ID) || !reflect.DeepEqual(e.Recovery, &core.RecoveryID{Kind: "name", Value: name}) || e.Post == nil {
			t.Errorf("effect %d = %+v", i+1, e)
		}
	}
	wantState := alJSON(t, []byte(`{"name":"darkpanel-c1-1","satisfyAny":false,"clients":[{"address":"10.0.0.0/8","directive":"allow"},{"address":"10.9.0.5","directive":"deny"}]}`))
	if got := alJSON(t, effects[0].Post.State); !reflect.DeepEqual(got, wantState) {
		t.Errorf("post state = %v, want %v", got, wantState)
	}
	version, _ := alJSON(t, effects[0].Post.Version).(map[string]any)
	if version["id"] != float64(views[0].ID) {
		t.Errorf("post version = %v, want the list id %d", version, views[0].ID)
	}
	if lists := alLists(t, n, env); len(lists) != 2 {
		t.Fatalf("NPM holds %d access lists, want 2", len(lists))
	}
	if err := handler.Verify(ctx, env, inputs[0], views[0]); err != nil {
		t.Errorf("Verify: %v", err)
	}
	api, err := n.d.api(ctx, env)
	if err != nil {
		t.Fatal(err)
	}
	if err := api.DeleteAccessList(ctx, views[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := handler.Verify(ctx, env, inputs[1], views[1]); err == nil {
		t.Error("Verify passed for a list NPM no longer holds")
	}
}

func TestAlAddVerifyFindsTheListFromTheChangeAlone(t *testing.T) {
	ctx := context.Background()
	h, n := alSetup(t)
	h.addChange("c1", "npm.accesslist.add")
	env := h.env("c1")
	handler := accessListAddHandler{n: n}
	if _, err := handler.Apply(ctx, env, alInput()); err != nil {
		t.Fatal(err)
	}
	if err := handler.Verify(ctx, env, alInput(), AccessListView{}); err != nil {
		t.Errorf("Verify with the zero output: %v", err)
	}
	h.npm.SeedAccessList(npmapi.AccessList{Name: "darkpanel-c1-9", Clients: []npmapi.AccessClient{{Address: "10.1.1.1", Directive: "allow"}}})
	if err := handler.Verify(ctx, env, alInput(), AccessListView{}); err != nil {
		t.Errorf("Verify with the zero output failed although one of the change's two lists holds the rules: %v", err)
	}
}

func TestAlAddRefusedByNPMIsDefiniteAndSkipped(t *testing.T) {
	ctx := context.Background()
	h, n := alSetup(t)
	h.addChange("c1", "npm.accesslist.add")
	n = alReplaceCreate(n, func(context.Context, npmAPI, npmapi.AccessListBody) (npmapi.AccessList, error) {
		return npmapi.AccessList{}, &npmapi.APIError{Status: 400, Message: "name is invalid"}
	})
	_, err := accessListAddHandler{n: n}.Apply(ctx, h.env("c1"), alInput())
	if !core.IsDefinite(err) {
		t.Fatalf("error = %v, want a Definite refusal", err)
	}
	effects := h.journal.effects()
	if len(effects) != 1 || effects[0].State != core.EffectSkipped {
		t.Errorf("effects = %+v, want one skipped effect", effects)
	}
	if lists := alLists(t, n, h.env("c1")); len(lists) != 0 {
		t.Errorf("NPM holds %d access lists, want none", len(lists))
	}
}

func TestAlAddLostResponseLeavesTheEffectObservable(t *testing.T) {
	ctx := context.Background()
	h, n := alSetup(t)
	h.addChange("c1", "npm.accesslist.add")
	env := h.env("c1")
	n = alReplaceCreate(n, func(ctx context.Context, live npmAPI, b npmapi.AccessListBody) (npmapi.AccessList, error) {
		if _, err := live.CreateAccessList(ctx, b); err != nil {
			return npmapi.AccessList{}, err
		}
		return npmapi.AccessList{}, errors.New("npm: connection reset by peer")
	})
	handler := accessListAddHandler{n: n}
	_, err := handler.Apply(ctx, env, alInput())
	if err == nil || core.IsDefinite(err) {
		t.Fatalf("error = %v, want a failure that is not Definite", err)
	}
	effects := h.journal.effects()
	if len(effects) != 1 || effects[0].State != core.EffectIntended || effects[0].Resource != "npm.accesslist:?darkpanel-c1-1" {
		t.Fatalf("effects = %+v, want one intended creation of npm.accesslist:?darkpanel-c1-1", effects)
	}
	lists := alLists(t, n, env)
	if len(lists) != 1 {
		t.Fatalf("NPM holds %d access lists, want the one the lost response created", len(lists))
	}
	obs, err := handler.Observe(ctx, env, effects[0])
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.Matches != 1 || !obs.Exclusive || obs.Resource != "npm.accesslist:"+strconv.Itoa(lists[0].ID) {
		t.Errorf("observation = %+v, want one exclusive match of list %d", obs, lists[0].ID)
	}
	if got, _ := alJSON(t, obs.Current.State).(map[string]any); got["name"] != "darkpanel-c1-1" {
		t.Errorf("observed state = %s, want the list's state", obs.Current.State)
	}
}

func TestAlObserveCreationCountsMatchesByName(t *testing.T) {
	ctx := context.Background()
	h, n := alSetup(t)
	handler := accessListAddHandler{n: n}
	e := core.Effect{
		ChangeID: "c1", Seq: 1, Kind: core.EffectCreate, Resource: "npm.accesslist:?darkpanel-c1-1",
		Recovery: &core.RecoveryID{Kind: "name", Value: "darkpanel-c1-1"}, State: core.EffectIntended,
	}
	none, err := handler.Observe(ctx, h.env("c1"), e)
	if err != nil {
		t.Fatal(err)
	}
	if none.Matches != 0 || string(none.Current.State) != "null" {
		t.Errorf("no list: observation = %+v, want Matches 0 and state null", none)
	}
	for range 2 {
		h.npm.SeedAccessList(npmapi.AccessList{Name: "darkpanel-c1-1", Clients: []npmapi.AccessClient{{Address: "10.1.1.1", Directive: "allow"}}})
	}
	h.npm.SeedAccessList(npmapi.AccessList{Name: "darkpanel-c1-12"})
	two, err := handler.Observe(ctx, h.env("c1"), e)
	if err != nil {
		t.Fatal(err)
	}
	if two.Matches != 2 || len(two.Candidates) != 2 || string(two.Current.State) != "null" {
		t.Fatalf("two lists: observation = %+v, want Matches 2, two candidates and state null", two)
	}
	if !slices.IsSortedFunc(two.Candidates, func(a, b core.Candidate) int { return strings.Compare(a.Resource, b.Resource) }) {
		t.Errorf("candidates %+v are not sorted by resource", two.Candidates)
	}
}

func TestAlAddUndoDeletesTheListAndRefusesOneInUse(t *testing.T) {
	ctx := context.Background()
	h, n := alSetup(t)
	h.addChange("c1", "npm.accesslist.add")
	env := h.env("c1")
	handler := accessListAddHandler{n: n}
	view, err := handler.Apply(ctx, env, alInput())
	if err != nil {
		t.Fatal(err)
	}
	made := h.journal.effects()[0]

	h.npm.SeedProxyHost(npmapi.ProxyHost{DomainNames: []string{"app.example.com"}, ForwardScheme: "http", ForwardHost: "app", ForwardPort: 8080, AccessListID: view.ID})
	_, err = handler.Inverse(ctx, env, made)
	if !core.IsDefinite(err) || alErr(t, err).Code != errcode.InvalidInput || !strings.Contains(alErr(t, err).Detail, "app.example.com") {
		t.Fatalf("undo of a list in use: error = %v, want a Definite invalid_input naming the proxy host", err)
	}
	if len(alLists(t, n, env)) != 1 {
		t.Fatal("the list in use was deleted")
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
	res, err := handler.Inverse(ctx, env, made)
	if err != nil || res.Resource != "" {
		t.Fatalf("Inverse = %+v, %v, want an empty result", res, err)
	}
	if len(alLists(t, n, env)) != 0 {
		t.Error("the list is still there after the undo")
	}
	if _, err := handler.Inverse(ctx, env, made); err != nil {
		t.Errorf("undoing a list that is already gone: %v", err)
	}
	obs, err := handler.Observe(ctx, env, made)
	if err != nil || string(obs.Current.State) != "null" {
		t.Errorf("Observe after the undo = %+v, %v, want state null", obs, err)
	}
}

func TestAlResolvedWatchesOnlyASkippedCreation(t *testing.T) {
	ctx := context.Background()
	h, n := alSetup(t)
	handler := accessListAddHandler{n: n}
	created := func(seq int, state core.EffectState) core.Effect {
		name := fmt.Sprintf("darkpanel-c1-%d", seq)
		return core.Effect{
			ChangeID: "c1", Seq: seq, Kind: core.EffectCreate, Resource: "npm.accesslist:?" + name,
			Recovery: &core.RecoveryID{Kind: "name", Value: name}, State: state,
		}
	}
	for _, e := range []core.Effect{created(3, core.EffectSkipped), created(4, core.EffectAccepted)} {
		err := h.db.Write(ctx, func(tx *sql.Tx) error { return handler.Resolved(ctx, tx, e, "reconcile") })
		if err != nil {
			t.Fatalf("Resolved(%s): %v", e.State, err)
		}
	}
	watching, err := watched(ctx, h.db.Read(), "access_list")
	if err != nil {
		t.Fatal(err)
	}
	if !watching["darkpanel-c1-3"] || watching["darkpanel-c1-4"] || len(watching) != 1 {
		t.Errorf("watched access lists = %v, want only darkpanel-c1-3", watching)
	}
}

func TestAlRemovePlanApplyAndVerify(t *testing.T) {
	ctx := context.Background()
	h, n := alSetup(t)
	seeded := h.npm.SeedAccessList(npmapi.AccessList{
		Name: "office", SatisfyAny: true,
		Clients: []npmapi.AccessClient{{Address: "203.0.113.0/24", Directive: "allow"}},
	})
	handler := accessListRemoveHandler{n: n}
	in := AccessListRemoveInput{ID: seeded.ID}
	plan, err := handler.Plan(ctx, h.env(""), in)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wantDiff := []core.DiffLine{
		{Op: "-", Text: fmt.Sprintf("access list office (id %d)", seeded.ID)},
		{Op: " ", Text: "satisfy any"},
		{Op: "-", Text: "allow 203.0.113.0/24"},
	}
	if !reflect.DeepEqual(plan.Diff, wantDiff) || plan.StateHash == "" || plan.Confirmation != "" {
		t.Errorf("plan = %+v, want diff %+v, a state hash and no confirmation", plan, wantDiff)
	}
	if err := handler.Verify(ctx, h.env(""), in, AccessListView{}); err == nil {
		t.Error("Verify passed while the list still exists")
	}

	h.addChange("c2", "npm.accesslist.remove")
	env := h.env("c2")
	view, err := handler.Apply(ctx, env, in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	wantView := AccessListView{ID: seeded.ID, Name: "office", SatisfyAny: true, Clients: []AccessClientInput{{Address: "203.0.113.0/24", Directive: "allow"}}}
	if !reflect.DeepEqual(view, wantView) {
		t.Errorf("view = %+v, want %+v", view, wantView)
	}
	if len(alLists(t, n, env)) != 0 {
		t.Error("the list is still in NPM")
	}
	effects := h.journal.effects()
	if len(effects) != 1 {
		t.Fatalf("journaled %d effects, want 1", len(effects))
	}
	e := effects[0]
	if e.Kind != core.EffectDelete || e.State != core.EffectMade || e.Local || e.CommitWait != 2*time.Minute ||
		e.Resource != "npm.accesslist:"+strconv.Itoa(seeded.ID) || e.Recovery != nil ||
		!reflect.DeepEqual(e.InverseRecovery, &core.RecoveryID{Kind: "name", Value: "office"}) {
		t.Errorf("effect = %+v", e)
	}
	wantBefore := alJSON(t, []byte(`{"name":"office","satisfyAny":true,"clients":[{"address":"203.0.113.0/24","directive":"allow"}]}`))
	if got := alJSON(t, e.Before.State); !reflect.DeepEqual(got, wantBefore) {
		t.Errorf("before state = %v, want %v", got, wantBefore)
	}
	if err := handler.Verify(ctx, env, in, view); err != nil {
		t.Errorf("Verify after the removal: %v", err)
	}
}

func TestAlRemoveRefusals(t *testing.T) {
	ctx := context.Background()
	h, n := alSetup(t)
	handler := accessListRemoveHandler{n: n}
	used := h.npm.SeedAccessList(npmapi.AccessList{Name: "used", Clients: []npmapi.AccessClient{{Address: "10.1.1.1", Directive: "allow"}}})
	h.npm.SeedProxyHost(npmapi.ProxyHost{DomainNames: []string{"app.example.com"}, ForwardScheme: "http", ForwardHost: "app", ForwardPort: 8080, AccessListID: used.ID})
	for _, tc := range []struct {
		name   string
		id     int
		code   errcode.Code
		detail string
	}{
		{"zero id", 0, errcode.InvalidInput, ""},
		{"unknown list", 4242, errcode.NotFound, ""},
		{"list in use", used.ID, errcode.InvalidInput, "app.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := handler.Plan(ctx, h.env(""), AccessListRemoveInput{ID: tc.id})
			if !core.IsDefinite(err) {
				t.Fatalf("error = %v, want a Definite refusal", err)
			}
			if ec := alErr(t, err); ec.Code != tc.code || !strings.Contains(ec.Detail, tc.detail) {
				t.Errorf("error = %+v, want %s containing %q", ec, tc.code, tc.detail)
			}
		})
	}
	if len(alLists(t, n, h.env(""))) != 1 {
		t.Error("a refused removal changed NPM")
	}
}

func TestAlRemoveOfAReservedListNeedsTheOwnersRemoval(t *testing.T) {
	ctx := context.Background()
	h, n := alSetup(t)
	handler := accessListRemoveHandler{n: n}
	list := h.npm.SeedAccessList(npmapi.AccessList{Name: "admin-only", Clients: []npmapi.AccessClient{{Address: "203.0.113.7", Directive: "allow"}}})
	alReserve(t, h, list.ID, "npm.admin.expose")
	in := AccessListRemoveInput{ID: list.ID}
	h.addChange("c3", "npm.accesslist.remove")
	for _, tc := range []struct {
		name   string
		env    core.Env
		refuse bool
	}{
		{"operator", h.env("c3"), true},
		{"the owner's own composite", h.child("c3", "npm.admin.expose"), true},
		{"another owner's removal", h.child("c3", "panel.exposure.remove"), true},
		{"the owner's removal", h.child("c3", "npm.admin.unexpose"), false},
	} {
		_, err := handler.Plan(ctx, tc.env, in)
		if tc.refuse {
			if !core.IsDefinite(err) || alErr(t, err).Code != errcode.NPMReserved {
				t.Errorf("%s: error = %v, want a Definite npm.reserved", tc.name, err)
			}
		} else if err != nil {
			t.Errorf("%s: Plan: %v", tc.name, err)
		}
	}
	if _, err := handler.Apply(ctx, h.env("c3"), in); alErr(t, err).Code != errcode.NPMReserved {
		t.Errorf("Apply as the operator: error = %v, want npm.reserved", err)
	}
	if len(alLists(t, n, h.env(""))) != 1 {
		t.Fatal("a refused removal deleted the list")
	}
	if _, err := handler.Apply(ctx, h.child("c3", "npm.admin.unexpose"), in); err != nil {
		t.Fatalf("Apply as the owner's removal: %v", err)
	}
	if len(alLists(t, n, h.env(""))) != 0 {
		t.Error("the owner's removal left the list in NPM")
	}
	if _, found, err := reservedOf(ctx, h.db.Read(), "access_list", list.ID); err != nil || found {
		t.Errorf("reservedOf after the removal = %v, %v, want no row", found, err)
	}
}

func TestAlRemoveUndoRecreatesTheList(t *testing.T) {
	ctx := context.Background()
	h, n := alSetup(t)
	seeded := h.npm.SeedAccessList(npmapi.AccessList{
		Name: "office", SatisfyAny: true,
		Clients: []npmapi.AccessClient{{Address: "203.0.113.0/24", Directive: "allow"}, {Address: "203.0.113.9", Directive: "deny"}},
	})
	h.addChange("c2", "npm.accesslist.remove")
	env := h.env("c2")
	handler := accessListRemoveHandler{n: n}
	if _, err := handler.Apply(ctx, env, AccessListRemoveInput{ID: seeded.ID}); err != nil {
		t.Fatal(err)
	}
	made := h.journal.effects()[0]
	res, err := handler.Inverse(ctx, env, made)
	if err != nil {
		t.Fatalf("Inverse: %v", err)
	}
	lists := alLists(t, n, env)
	if len(lists) != 1 || res.Resource != "npm.accesslist:"+strconv.Itoa(lists[0].ID) {
		t.Fatalf("Inverse result %+v with lists %+v, want the recreated list's resource", res, lists)
	}
	if got := lists[0]; got.Name != "office" || !got.SatisfyAny || !reflect.DeepEqual(got.Clients, seeded.Clients) {
		t.Errorf("recreated list = %+v, want the original's name, mode and rules", got)
	}

	undone := made
	undone.State = core.EffectUndone
	undone.UndoResource = res.Resource
	obs, err := handler.Observe(ctx, env, undone)
	if err != nil {
		t.Fatalf("Observe after the undo: %v", err)
	}
	if obs.Matches != 1 || !reflect.DeepEqual(alJSON(t, obs.Current.State), alJSON(t, made.Before.State)) {
		t.Errorf("observation = %+v, want one match State-equal to the original", obs)
	}

	cut := made
	cut.State = core.EffectUndoing
	obs, err = handler.Observe(ctx, env, cut)
	if err != nil {
		t.Fatalf("Observe of an inverse cut off before it returned: %v", err)
	}
	if obs.Matches != 1 || obs.Resource != res.Resource || obs.Exclusive {
		t.Errorf("observation = %+v, want the recreated list found by name, not exclusive", obs)
	}
}

// rrReserve reserves an NPM object under the exposure hostname the reservation fixtures share.
func rrReserve(t *testing.T, h *thHost, kind string, id int, owner string) {
	t.Helper()
	ctx := context.Background()
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		return addReserved(ctx, tx, reservedRow{Kind: kind, ObjectID: id, Owner: owner, Exposure: "npm.example.com", ChangeID: "c0"})
	})
	if err != nil {
		t.Fatalf("reserve %s %d: %v", kind, id, err)
	}
}

// rrWantReserved checks which of the objects are still reserved, and by whom; an owner "" means released.
func rrWantReserved(t *testing.T, h *thHost, want []reservedRow) {
	t.Helper()
	for _, w := range want {
		got, held, err := reservedOf(context.Background(), h.db.Read(), w.Kind, w.ObjectID)
		if err != nil {
			t.Fatalf("reservedOf(%s, %d): %v", w.Kind, w.ObjectID, err)
		}
		if w.Owner == "" && held {
			t.Errorf("%s %d is still reserved by %s, want it released", w.Kind, w.ObjectID, got.Owner)
		}
		if w.Owner != "" && (!held || got.Owner != w.Owner) {
			t.Errorf("%s %d: reserved = %v by %q, want it kept for %s", w.Kind, w.ObjectID, held, got.Owner, w.Owner)
		}
	}
}

// TestRrAccessListRemoveReleasesOnlyItsOwnersReservations: removing a reserved list as its owner's removal releases the
// owner's records under the exposure and keeps another owner's records under the same hostname.
func TestRrAccessListRemoveReleasesOnlyItsOwnersReservations(t *testing.T) {
	ctx := context.Background()
	h, n := alSetup(t)
	list := h.npm.SeedAccessList(npmapi.AccessList{Name: "admin-only", Clients: []npmapi.AccessClient{{Address: "203.0.113.7", Directive: "allow"}}})
	alReserve(t, h, list.ID, "npm.admin.expose")
	rrReserve(t, h, "proxy_host", 41, "npm.admin.expose")
	rrReserve(t, h, "proxy_host", 42, "panel.exposure.add")
	rrReserve(t, h, "certificate", 43, "panel.exposure.add")
	h.addChange("c3", "npm.accesslist.remove")
	view, err := (accessListRemoveHandler{n: n}).Apply(ctx, h.child("c3", "npm.admin.unexpose"), AccessListRemoveInput{ID: list.ID})
	if err != nil {
		t.Fatalf("Apply as the owner's removal: %v", err)
	}
	if view.Reserved != "npm.admin.expose" {
		t.Errorf("view reserved = %q, want the owner npm.admin.expose", view.Reserved)
	}
	rrWantReserved(t, h, []reservedRow{
		{Kind: "access_list", ObjectID: list.ID},
		{Kind: "proxy_host", ObjectID: 41},
		{Kind: "proxy_host", ObjectID: 42, Owner: "panel.exposure.add"},
		{Kind: "certificate", ObjectID: 43, Owner: "panel.exposure.add"},
	})
}

// TestW10NvAddVerifyReadsOnlyTheChangesLists: with no id in the output, Verify looks at the lists named
// darkpanel-<change id>-<sequence number>, no others, and wants one of them to hold the requested rules.
func TestW10NvAddVerifyReadsOnlyTheChangesLists(t *testing.T) {
	ctx := context.Background()
	rules := []npmapi.AccessClient{{Address: "10.0.0.0/8", Directive: "allow"}, {Address: "10.9.0.5", Directive: "deny"}}

	t.Run("no list of the change holds the rules", func(t *testing.T) {
		h, n := alSetup(t)
		h.addChange("c1", "npm.accesslist.add")
		env := h.env("c1")
		handler := accessListAddHandler{n: n}
		if _, err := handler.Apply(ctx, env, alInput()); err != nil {
			t.Fatal(err)
		}
		h.npm.SeedAccessList(npmapi.AccessList{Name: "darkpanel-c1-9", Clients: []npmapi.AccessClient{{Address: "10.1.1.1", Directive: "allow"}}})
		other := AccessListInput{Clients: []AccessClientInput{{Address: "192.0.2.1", Directive: "allow"}}}
		if err := handler.Verify(ctx, env, other, AccessListView{}); err == nil {
			t.Error("Verify passed for rules none of the change's lists holds")
		}
	})

	t.Run("a list of another change whose id extends this one", func(t *testing.T) {
		h, n := alSetup(t)
		h.npm.SeedAccessList(npmapi.AccessList{Name: "darkpanel-c1-2-1", Clients: rules})
		handler := accessListAddHandler{n: n}
		if err := handler.Verify(ctx, h.env("c1"), alInput(), AccessListView{}); err == nil {
			t.Error("Verify of change c1 passed on the list darkpanel-c1-2-1, which belongs to change c1-2")
		}
	})
}
