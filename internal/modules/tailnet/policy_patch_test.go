package tailnet

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
	"github.com/darkraise/darkpanel/internal/controller/cloud/cloudfake"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailnet/fake"
)

const ppPolicy = `// Darkpanel tailnet policy.
{
	// who may reach what
	"grants": [
		{"src": ["*"], "dst": ["*"], "ip": ["*"]}, // everyone, everywhere
	],
	"tagOwners": {"tag:darkpanel": ["autogroup:admin"]},
}
`

const (
	ppAddGrant = `{"op":"addItem","path":"/grants","value":{"src":["tag:patched"],"dst":["*"],"ip":["*"]}}`
	ppSetPort  = `{"op":"set","path":"/randomizeClientPort","value":true}`
)

var (
	ppACL      = "/api/v2/tailnet/" + fake.Tailnet + "/acl"
	ppValidate = ppACL + "/validate"
)

func ppInput(edits ...string) json.RawMessage {
	return json.RawMessage(`{"edits":[` + strings.Join(edits, ",") + `]}`)
}

// ppWorld is a connected account over a fake tailnet that holds text, and
// the action under test.
type ppWorld struct {
	h    *tnHost
	srv  *fake.Server
	def  core.ActionDef
	etag string // the ETag of the policy as it was set
}

func ppNew(t *testing.T, text string) *ppWorld {
	t.Helper()
	srv := fake.New(t)
	etag := srv.SetPolicy(text)
	h := newTNHost(t)
	acConnect(t, h, srv)
	return &ppWorld{h: h, srv: srv, def: tnWith(srv).policyPatch(), etag: etag}
}

func (w *ppWorld) requests(method, path string) []cloudfake.Request {
	var out []cloudfake.Request
	for _, r := range w.srv.Cloud().Requests() {
		if r.Method == method && r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

func (w *ppWorld) apply(t *testing.T, change string, in json.RawMessage) (json.RawMessage, error) {
	t.Helper()
	return w.def.Handler.Apply(context.Background(), w.h.env(change), in)
}

func ppSame(a, b json.RawMessage) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

// TestPPSpec: the action is compensable with an undo handler, controller
// side, locked on tailnet, and converges.
func TestPPSpec(t *testing.T) {
	w := ppNew(t, ppPolicy)
	s := w.def.Spec
	if s.Name != "tailnet.policy.patch" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideController ||
		!slices.Equal(s.LockKeys, []string{"tailnet"}) || !s.Converges || s.ForwardOnly || s.CompensatedBy != "" {
		t.Errorf("spec %+v", s)
	}
	if w.def.Handler.Observe == nil || w.def.Handler.Inverse == nil {
		t.Errorf("observe %v inverse %v, want both", w.def.Handler.Observe != nil, w.def.Handler.Inverse != nil)
	}
}

// TestPPPlanShowsTheEdits: the plan validates the patched text with the
// provider, shows the added lines, writes nothing and hashes the state.
func TestPPPlanShowsTheEdits(t *testing.T) {
	w := ppNew(t, ppPolicy)
	ctx := context.Background()
	in := ppInput(ppAddGrant, ppSetPort)
	out, err := w.def.Handler.Plan(ctx, w.h.env(""), in)
	if err != nil {
		t.Fatal(err)
	}
	var added []string
	for _, l := range out.Diff {
		if l.Op == "+" {
			added = append(added, l.Text)
		}
	}
	joined := strings.Join(added, "\n")
	if !strings.Contains(joined, "tag:patched") || !strings.Contains(joined, "randomizeClientPort") {
		t.Errorf("added lines %q, want the new grant and the port setting", added)
	}
	if out.StateHash == "" || out.Confirmation != "" {
		t.Errorf("state hash %q confirmation %q, want a hash and no confirmation", out.StateHash, out.Confirmation)
	}
	if n := len(w.requests(http.MethodPost, ppValidate)); n != 1 {
		t.Errorf("%d validate requests, want 1", n)
	}
	if n := len(w.requests(http.MethodPost, ppACL)); n != 0 {
		t.Errorf("%d policy writes during the plan, want 0", n)
	}
	again, err := w.def.Handler.Plan(ctx, w.h.env(""), in)
	if err != nil || again.StateHash != out.StateHash {
		t.Errorf("second plan hash %q err %v, want %q", again.StateHash, err, out.StateHash)
	}
}

// TestPPPlanHashTracksThePolicy: a policy that changed after the plan
// changes the hash, so the engine refuses the stale plan.
func TestPPPlanHashTracksThePolicy(t *testing.T) {
	w := ppNew(t, ppPolicy)
	ctx := context.Background()
	in := ppInput(ppAddGrant)
	before, err := w.def.Handler.Plan(ctx, w.h.env(""), in)
	if err != nil {
		t.Fatal(err)
	}
	w.srv.SetPolicy(strings.Replace(ppPolicy, `"tagOwners"`, `"autoApprovers": {}, "tagOwners"`, 1))
	after, err := w.def.Handler.Plan(ctx, w.h.env(""), in)
	if err != nil {
		t.Fatal(err)
	}
	if after.StateHash == before.StateHash {
		t.Error("the plan hash did not change when the policy did")
	}
}

// TestPPApplyWritesAndJournals: apply posts the patched text with If-Match
// on the ETag it read, keeps the comments and unrelated content, journals
// one modify effect of the policy, and verifies.
func TestPPApplyWritesAndJournals(t *testing.T) {
	w := ppNew(t, ppPolicy)
	ctx := context.Background()
	in := ppInput(ppAddGrant, ppSetPort)
	env := w.h.env("01J00000000000000000000001")
	raw, err := w.def.Handler.Apply(ctx, env, in)
	if err != nil {
		t.Fatal(err)
	}
	var out PolicyWritten
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	text, etag := w.srv.Policy()
	if out.ETag != etag || etag == w.etag {
		t.Errorf("output etag %q, fake etag %q, original %q; want the new etag", out.ETag, etag, w.etag)
	}
	for _, want := range []string{"// Darkpanel tailnet policy.", "// who may reach what", "// everyone, everywhere", `"tag:darkpanel"`, "tag:patched", "randomizeClientPort"} {
		if !strings.Contains(text, want) {
			t.Errorf("patched policy lacks %q:\n%s", want, text)
		}
	}
	posts := w.requests(http.MethodPost, ppACL)
	if len(posts) != 1 || posts[0].Header.Get("If-Match") != w.etag {
		t.Fatalf("%d policy writes, want 1 with If-Match %q", len(posts), w.etag)
	}
	effs := w.h.journal.effects()
	if len(effs) != 1 {
		t.Fatalf("%d effects, want 1", len(effs))
	}
	e := effs[0]
	if e.Kind != core.EffectModify || e.Resource != "tailnet.policy:"+fake.Tailnet || e.State != core.EffectMade ||
		e.Local || e.CommitWait != cloud.CommitWait || e.Post == nil {
		t.Errorf("effect %+v, want a made, non-local modify of tailnet.policy:%s with a post state", e, fake.Tailnet)
	}
	obs, err := w.def.Handler.Observe(ctx, env, e)
	if err != nil || !ppSame(obs.Current.State, e.Post.State) {
		t.Errorf("observation %s err %v, want the effect's post state %s", obs.Current.State, err, e.Post.State)
	}
	if err := w.def.Handler.Verify(ctx, env, in, raw); err != nil {
		t.Errorf("verify after apply: %v", err)
	}
}

// TestPPVerifyFailsWhenTheEditsAreGone: verify reads the live policy and
// refuses one that no longer holds the edits.
func TestPPVerifyFailsWhenTheEditsAreGone(t *testing.T) {
	w := ppNew(t, ppPolicy)
	in := ppInput(ppAddGrant)
	raw, err := w.apply(t, "01J00000000000000000000001", in)
	if err != nil {
		t.Fatal(err)
	}
	w.srv.SetPolicy(ppPolicy)
	if err := w.def.Handler.Verify(context.Background(), w.h.env("01J00000000000000000000001"), in, raw); err == nil {
		t.Error("verify passed although the policy no longer holds the grant")
	}
}

// TestPPConverges: the same edits run again plan an empty diff, send no
// validate or write request, journal nothing and answer the live ETag.
func TestPPConverges(t *testing.T) {
	w := ppNew(t, ppPolicy)
	ctx := context.Background()
	in := ppInput(ppAddGrant, ppSetPort)
	if _, err := w.apply(t, "01J00000000000000000000001", in); err != nil {
		t.Fatal(err)
	}
	validates, posts := len(w.requests(http.MethodPost, ppValidate)), len(w.requests(http.MethodPost, ppACL))

	out, err := w.def.Handler.Plan(ctx, w.h.env(""), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Diff) != 0 {
		t.Errorf("second plan diff %+v, want it empty", out.Diff)
	}
	raw, err := w.apply(t, "01J00000000000000000000002", in)
	if err != nil {
		t.Fatal(err)
	}
	var written PolicyWritten
	if err := json.Unmarshal(raw, &written); err != nil {
		t.Fatal(err)
	}
	if _, etag := w.srv.Policy(); written.ETag != etag {
		t.Errorf("second apply answered etag %q, want the live %q", written.ETag, etag)
	}
	if n := len(w.requests(http.MethodPost, ppValidate)); n != validates {
		t.Errorf("%d validate requests after the converged runs, want %d", n, validates)
	}
	if n := len(w.requests(http.MethodPost, ppACL)); n != posts {
		t.Errorf("%d policy writes after the converged runs, want %d", n, posts)
	}
	if n := len(w.h.journal.effects()); n != 1 {
		t.Errorf("%d effects after the converged run, want 1", n)
	}
}

// TestPPApplyKeepsAConcurrentEdit: apply patches the policy as it is at
// apply, so an edit made elsewhere after the plan is not erased.
func TestPPApplyKeepsAConcurrentEdit(t *testing.T) {
	w := ppNew(t, ppPolicy)
	in := ppInput(ppAddGrant)
	w.srv.SetPolicy(strings.Replace(ppPolicy, `"tagOwners"`, `"autoApprovers": {}, "tagOwners"`, 1))
	if _, err := w.apply(t, "01J00000000000000000000001", in); err != nil {
		t.Fatal(err)
	}
	text, _ := w.srv.Policy()
	if !strings.Contains(text, "autoApprovers") || !strings.Contains(text, "tag:patched") {
		t.Errorf("policy after apply lacks the concurrent edit or the patch:\n%s", text)
	}
}

// TestPPUndoRestoresTheOriginal: the inverse posts the text, comments
// included, that the patch replaced.
func TestPPUndoRestoresTheOriginal(t *testing.T) {
	w := ppNew(t, ppPolicy)
	env := w.h.env("01J00000000000000000000001")
	if _, err := w.def.Handler.Apply(context.Background(), env, ppInput(ppAddGrant, ppSetPort)); err != nil {
		t.Fatal(err)
	}
	e := w.h.journal.effects()[0]
	e.State = core.EffectUndoing
	if _, err := w.def.Handler.Inverse(context.Background(), env, e); err != nil {
		t.Fatal(err)
	}
	if text, _ := w.srv.Policy(); text != ppPolicy {
		t.Errorf("policy after undo:\n%s\nwant the original:\n%s", text, ppPolicy)
	}
}

// TestPPUndoRefusesAfterAnotherEdit: an edit made after the patch makes the
// undo fail with tailnet.policy_conflict and leaves that edit in place.
func TestPPUndoRefusesAfterAnotherEdit(t *testing.T) {
	w := ppNew(t, ppPolicy)
	env := w.h.env("01J00000000000000000000001")
	if _, err := w.def.Handler.Apply(context.Background(), env, ppInput(ppAddGrant)); err != nil {
		t.Fatal(err)
	}
	other := ppPolicy + "// edited elsewhere\n"
	w.srv.SetPolicy(other)
	e := w.h.journal.effects()[0]
	e.State = core.EffectUndoing
	_, err := w.def.Handler.Inverse(context.Background(), env, e)
	if got := errcode.From(err).Code; got != errcode.TailnetPolicyConflict {
		t.Errorf("undo error %v, want %s", err, errcode.TailnetPolicyConflict)
	}
	if text, _ := w.srv.Policy(); text != other {
		t.Errorf("policy after the refused undo:\n%s\nwant the other edit kept", text)
	}
}

// TestPPRefusesBadEdits: an edit the policy package refuses fails the plan
// with request.invalid_input naming the edit, before any validate or write.
func TestPPRefusesBadEdits(t *testing.T) {
	tests := []struct {
		name  string
		edits []string
		field string
	}{
		{"array index", []string{`{"op":"set","path":"/grants/0","value":1}`}, "/edits/0"},
		{"unknown op", []string{ppSetPort, `{"op":"move","path":"/grants"}`}, "/edits/1"},
		{"missing parent", []string{`{"op":"set","path":"/acls/extra","value":1}`}, "/edits/0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := ppNew(t, ppPolicy)
			_, err := w.def.Handler.Plan(context.Background(), w.h.env(""), ppInput(tt.edits...))
			coded := errcode.From(err)
			if coded.Code != errcode.InvalidInput || !slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == tt.field }) {
				t.Errorf("plan error %v, want %s naming %s", err, errcode.InvalidInput, tt.field)
			}
			if n := len(w.requests(http.MethodPost, ppValidate)) + len(w.requests(http.MethodPost, ppACL)); n != 0 {
				t.Errorf("%d validate or write requests, want none", n)
			}
		})
	}
}

// TestPPProviderRefusals: the provider's validation answer fails the plan
// with tailnet.policy_invalid, and a stale ETag at apply fails with
// tailnet.policy_conflict, definitely, with the effect skipped.
func TestPPProviderRefusals(t *testing.T) {
	t.Run("invalid", func(t *testing.T) {
		w := ppNew(t, ppPolicy)
		w.srv.Cloud().Fail(http.MethodPost, ppValidate, cloudfake.Fault{Status: http.StatusBadRequest, Body: `{"message":"grant 1 is invalid","data":[]}`})
		_, err := w.def.Handler.Plan(context.Background(), w.h.env(""), ppInput(ppAddGrant))
		if got := errcode.From(err).Code; got != errcode.TailnetPolicyInvalid {
			t.Errorf("plan error %v, want %s", err, errcode.TailnetPolicyInvalid)
		}
	})
	t.Run("conflict", func(t *testing.T) {
		w := ppNew(t, ppPolicy)
		w.srv.Cloud().Fail(http.MethodPost, ppACL, cloudfake.Fault{Status: http.StatusPreconditionFailed})
		_, err := w.apply(t, "01J00000000000000000000001", ppInput(ppAddGrant))
		if got := errcode.From(err).Code; got != errcode.TailnetPolicyConflict || !core.IsDefinite(err) {
			t.Errorf("apply error %v (definite %v), want a definite %s", err, core.IsDefinite(err), errcode.TailnetPolicyConflict)
		}
		effs := w.h.journal.effects()
		if len(effs) != 1 || effs[0].State != core.EffectSkipped {
			t.Errorf("effects %+v, want one skipped", effs)
		}
	})
}

// TestPPNotConnected: without an account the plan fails with
// cloud.not_connected.
func TestPPNotConnected(t *testing.T) {
	srv := fake.New(t)
	h := newTNHost(t)
	_, err := tnWith(srv).policyPatch().Handler.Plan(context.Background(), h.env(""), ppInput(ppAddGrant))
	if got := errcode.From(err).Code; got != errcode.CloudNotConnected {
		t.Errorf("plan error %v, want %s", err, errcode.CloudNotConnected)
	}
}
