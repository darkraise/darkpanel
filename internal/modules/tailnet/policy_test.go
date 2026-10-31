package tailnet

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
	"github.com/darkraise/darkpanel/internal/controller/cloud/cloudfake"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailnet/fake"
	"github.com/darkraise/darkpanel/internal/modules/tailnet/policy"
)

const (
	poChange = "01J9X4K8Q2M5N7P0R3S6T8V2P1"

	poBefore = `{
	// who may reach the web tier
	"acls": [
		{"action": "accept", "src": ["*"], "dst": ["*:*"]},
	],
}
`
	poAfter = `{
	// who may reach the web tier
	"acls": [
		{"action": "accept", "src": ["*"], "dst": ["*:*"]},
	],
	"tagOwners": {"tag:ci": ["autogroup:admin"]},
}
`
)

var (
	poACLPath      = "/api/v2/tailnet/" + fake.Tailnet + "/acl"
	poValidatePath = poACLPath + "/validate"
	poResource     = "tailnet.policy:" + fake.Tailnet
)

type poEnv struct {
	h    *tnHost
	srv  *fake.Server
	t    tn
	etag string // the provider's ETag before any write
}

func poSetup(t *testing.T) poEnv {
	t.Helper()
	h := newTNHost(t)
	srv := fake.New(t)
	acConnect(t, h, srv)
	return poEnv{h: h, srv: srv, t: tnWith(srv), etag: srv.SetPolicy(poBefore)}
}

func poJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func poSet(t *testing.T, e poEnv, text string) (PolicyWritten, error) {
	t.Helper()
	out, err := e.t.policySet().Handler.Apply(context.Background(), e.h.env(poChange), poJSON(t, PolicySetInput{Text: text}))
	var w PolicyWritten
	if err == nil {
		if err := json.Unmarshal(out, &w); err != nil {
			t.Fatal(err)
		}
	}
	return w, err
}

func poOnly(t *testing.T, e poEnv) core.Effect {
	t.Helper()
	effs := e.h.journal.effects()
	if len(effs) != 1 {
		t.Fatalf("journal holds %d effects, want 1", len(effs))
	}
	return effs[0]
}

func poCode(t *testing.T, err error) errcode.Code {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	return errcode.From(err).Code
}

func poCanonState(t *testing.T, text string) json.RawMessage {
	t.Helper()
	canon, err := policy.Canonical([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return json.RawMessage(fmt.Sprintf(`{"policy":%s}`, canon))
}

func poSame(t *testing.T, got, want json.RawMessage) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("decode %s: %v", got, err)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("decode %s: %v", want, err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("got %s, want %s", got, want)
	}
}

func poRequests(e poEnv, method, path string) []cloudfake.Request {
	var out []cloudfake.Request
	for _, r := range e.srv.Cloud().Requests() {
		if r.Method == method && r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

func TestPoGet(t *testing.T) {
	e := poSetup(t)
	raw, err := e.t.policyGet().Handler.Read(context.Background(), e.h.env(""), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var got Policy
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Text != poBefore || got.ETag != e.etag {
		t.Errorf("got %+v, want the stored text with its comments and ETag %s", got, e.etag)
	}
}

func TestPoSetPlan(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		fault     *cloudfake.Fault // on the validate request
		noAccount bool
		code      errcode.Code
		validated bool
	}{
		{name: "valid", text: poAfter, validated: true},
		{name: "not HuJSON", text: `{"acls": [`, code: errcode.TailnetPolicyInvalid},
		{name: "empty", text: "  ", code: errcode.TailnetPolicyInvalid},
		{name: "provider validation fails", text: poAfter, fault: &cloudfake.Fault{Status: http.StatusForbidden}, code: errcode.CloudPermissionMissing, validated: true},
		{name: "no account", text: poAfter, noAccount: true, code: errcode.CloudNotConnected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := poSetup(t)
			if tc.noAccount {
				e.h = newTNHost(t)
			}
			if tc.fault != nil {
				e.srv.Cloud().Fail(http.MethodPost, poValidatePath, *tc.fault)
			}
			out, err := e.t.policySet().Handler.Plan(context.Background(), e.h.env(""), poJSON(t, PolicySetInput{Text: tc.text}))
			if validated := len(poRequests(e, http.MethodPost, poValidatePath)) > 0; validated != tc.validated {
				t.Errorf("validate request sent %v, want %v", validated, tc.validated)
			}
			if tc.code != "" {
				if got := poCode(t, err); got != tc.code {
					t.Fatalf("code %s, want %s (%v)", got, tc.code, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var added, removed int
			for _, l := range out.Diff {
				switch {
				case l.Op == "+" && strings.Contains(l.Text, "tagOwners"):
					added++
				case l.Op == "-":
					removed++
				}
			}
			if added != 1 || removed != 0 {
				t.Errorf("diff %v: want one added tagOwners line and no removed line", out.Diff)
			}
			if out.StateHash == "" {
				t.Error("empty state hash")
			}
		})
	}
}

func TestPoSetPlanStateHashFollowsThePolicy(t *testing.T) {
	e := poSetup(t)
	plan := func() string {
		out, err := e.t.policySet().Handler.Plan(context.Background(), e.h.env(""), poJSON(t, PolicySetInput{Text: poAfter}))
		if err != nil {
			t.Fatal(err)
		}
		return out.StateHash
	}
	first := plan()
	if again := plan(); again != first {
		t.Errorf("an unchanged policy hashed %s then %s", first, again)
	}
	e.srv.SetPolicy(poBefore + "// edited elsewhere\n")
	if changed := plan(); changed == first {
		t.Error("the hash ignores a policy changed since the plan")
	}
}

func TestPoSetApply(t *testing.T) {
	ctx := context.Background()
	e := poSetup(t)
	w, err := poSet(t, e, poAfter)
	if err != nil {
		t.Fatal(err)
	}
	text, etag := e.srv.Policy()
	if text != poAfter || w.ETag != etag || etag == e.etag {
		t.Fatalf("provider holds %q with ETag %s; apply answered %+v (ETag before %s)", text, etag, w, e.etag)
	}
	posts := poRequests(e, http.MethodPost, poACLPath)
	if len(posts) != 1 || posts[0].Header.Get("If-Match") != e.etag {
		t.Fatalf("policy writes %+v, want one with If-Match %s", posts, e.etag)
	}

	eff := poOnly(t, e)
	if eff.Kind != core.EffectModify || eff.State != core.EffectMade || eff.Resource != poResource || eff.Local || eff.CommitWait != cloud.CommitWait {
		t.Errorf("effect %+v", eff)
	}
	poSame(t, eff.Before.State, poCanonState(t, poBefore))
	if string(eff.Before.Version) != strconv.Quote(e.etag) {
		t.Errorf("before version %s, want the ETag read", eff.Before.Version)
	}
	if eff.Post == nil {
		t.Fatal("no post state")
	}
	poSame(t, eff.Post.State, poCanonState(t, poAfter))
	if string(eff.Post.Version) != strconv.Quote(w.ETag) {
		t.Errorf("post version %s, want the ETag written", eff.Post.Version)
	}
	secret, err := e.h.journal.Secret(ctx, eff.Seq)
	if err != nil || string(secret) != poBefore {
		t.Errorf("sealed before-text %q (%v), want the policy with its comments", secret, err)
	}

	in := poJSON(t, PolicySetInput{Text: poAfter})
	if err := e.t.policySet().Handler.Verify(ctx, e.h.env(poChange), in, poJSON(t, w)); err != nil {
		t.Errorf("verify after the write: %v", err)
	}
	e.srv.SetPolicy(`{"acls": []}`)
	if err := e.t.policySet().Handler.Verify(ctx, e.h.env(poChange), in, poJSON(t, w)); err == nil {
		t.Error("verify accepted a policy that is not the one written")
	}
}

func TestPoSetApplySameTextWritesNothing(t *testing.T) {
	e := poSetup(t)
	w, err := poSet(t, e, poBefore)
	if err != nil {
		t.Fatal(err)
	}
	if w.ETag != e.etag || len(poRequests(e, http.MethodPost, poACLPath)) != 0 || len(e.h.journal.effects()) != 0 {
		t.Errorf("ETag %s; %d writes; %d effects: want the current ETag and nothing written",
			w.ETag, len(poRequests(e, http.MethodPost, poACLPath)), len(e.h.journal.effects()))
	}
}

func TestPoSetApplyStaleETagIsDefinite(t *testing.T) {
	e := poSetup(t)
	e.srv.Cloud().Fail(http.MethodPost, poACLPath, cloudfake.Fault{Status: http.StatusPreconditionFailed})
	_, err := poSet(t, e, poAfter)
	if got := poCode(t, err); got != errcode.TailnetPolicyConflict || !core.IsDefinite(err) {
		t.Fatalf("error %v (definite %v), want a definite %s", err, core.IsDefinite(err), errcode.TailnetPolicyConflict)
	}
	if eff := poOnly(t, e); eff.State != core.EffectSkipped {
		t.Errorf("effect %s, want skipped", eff.State)
	}
	if text, _ := e.srv.Policy(); text != poBefore {
		t.Error("the policy changed")
	}
}

func TestPoSetApplyLostAnswerLeavesTheEffectOpen(t *testing.T) {
	e := poSetup(t)
	e.srv.Cloud().Fail(http.MethodPost, poACLPath, cloudfake.Fault{LoseResponse: true})
	_, err := poSet(t, e, poAfter)
	if got := poCode(t, err); got != errcode.CloudUncertainOutcome || core.IsDefinite(err) {
		t.Fatalf("error %v (definite %v), want an uncertain outcome", err, core.IsDefinite(err))
	}
	eff := poOnly(t, e)
	if eff.State != core.EffectIntended {
		t.Errorf("effect %s, want intended for the engine to settle", eff.State)
	}
	obs, err := e.t.policySet().Handler.Observe(context.Background(), e.h.env(poChange), eff)
	if err != nil {
		t.Fatal(err)
	}
	poSame(t, obs.Current.State, poCanonState(t, poAfter))
}

func TestPoObserveAndInverse(t *testing.T) {
	ctx := context.Background()
	e := poSetup(t)
	w, err := poSet(t, e, poAfter)
	if err != nil {
		t.Fatal(err)
	}
	eff := poOnly(t, e)
	def := e.t.policySet()
	env := e.h.env(poChange)

	obs, err := def.Handler.Observe(ctx, env, eff)
	if err != nil {
		t.Fatal(err)
	}
	if obs.Matches != 1 || obs.Resource != poResource {
		t.Errorf("observation %+v", obs)
	}
	poSame(t, obs.Current.State, eff.Post.State)

	if _, err := def.Handler.Inverse(ctx, env, eff); err != nil {
		t.Fatal(err)
	}
	if text, _ := e.srv.Policy(); text != poBefore {
		t.Errorf("the policy after the undo is %q, want the before-text with its comments", text)
	}
	if posts := poRequests(e, http.MethodPost, poACLPath); len(posts) != 2 || posts[1].Header.Get("If-Match") != w.ETag {
		t.Errorf("policy writes %+v: want the undo to send If-Match %s, the ETag the change wrote", posts, w.ETag)
	}
	obs, err = def.Handler.Observe(ctx, env, eff)
	if err != nil {
		t.Fatal(err)
	}
	poSame(t, obs.Current.State, eff.Before.State)
}

func TestPoInverseKeepsLaterEdits(t *testing.T) {
	ctx := context.Background()
	e := poSetup(t)
	if _, err := poSet(t, e, poAfter); err != nil {
		t.Fatal(err)
	}
	eff := poOnly(t, e)
	def := e.t.policySet()
	env := e.h.env(poChange)

	t.Run("comment-only edit", func(t *testing.T) {
		later := poAfter + "// added by hand\n"
		e.srv.SetPolicy(later)
		_, err := def.Handler.Inverse(ctx, env, eff)
		if got := poCode(t, err); got != errcode.TailnetPolicyConflict || !core.IsDefinite(err) {
			t.Fatalf("error %v (definite %v), want a definite %s", err, core.IsDefinite(err), errcode.TailnetPolicyConflict)
		}
		if text, _ := e.srv.Policy(); text != later {
			t.Error("the undo erased a later edit")
		}
	})

	t.Run("content edit", func(t *testing.T) {
		e.srv.SetPolicy(`{"acls": []}`)
		obs, err := def.Handler.Observe(ctx, env, eff)
		if err != nil {
			t.Fatal(err)
		}
		var got, want any
		_ = json.Unmarshal(obs.Current.State, &got)
		_ = json.Unmarshal(eff.Post.State, &want)
		if reflect.DeepEqual(got, want) {
			t.Error("the observation does not show the later edit")
		}
	})
}

func TestPoInverseWithoutTheSealedBeforeText(t *testing.T) {
	e := poSetup(t)
	if _, err := poSet(t, e, poAfter); err != nil {
		t.Fatal(err)
	}
	eff := poOnly(t, e)
	eff.Seq += 100
	writes := len(poRequests(e, http.MethodPost, poACLPath))
	_, err := e.t.policySet().Handler.Inverse(context.Background(), e.h.env(poChange), eff)
	if err == nil || !core.IsDefinite(err) {
		t.Fatalf("error %v, want a definite refusal", err)
	}
	if n := len(poRequests(e, http.MethodPost, poACLPath)); n != writes {
		t.Error("the inverse wrote without the before-text")
	}
}

type poModule struct{ t tn }

func (m poModule) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, Actions: []core.ActionDef{m.t.policyGet(), m.t.policySet()}}
}

func TestPoActionsRegister(t *testing.T) {
	m := poModule{t: tnWith(fake.New(t))}
	if _, err := core.NewRegistry(m); err != nil {
		t.Fatal(err)
	}
	get, set := m.t.policyGet(), m.t.policySet()
	if get.Spec.Name != "tailnet.policy.get" || get.Spec.Class != core.ClassRead || len(get.Spec.LockKeys) != 0 {
		t.Errorf("get spec %+v", get.Spec)
	}
	if set.Spec.Name != "tailnet.policy.set" || set.Spec.Class != core.ClassCompensable || set.Spec.Side != core.SideController ||
		!reflect.DeepEqual(set.Spec.LockKeys, []string{"tailnet"}) {
		t.Errorf("set spec %+v", set.Spec)
	}
	if set.Handler.Observe == nil || set.Handler.Inverse == nil {
		t.Error("set lacks Observe or Inverse")
	}
}
