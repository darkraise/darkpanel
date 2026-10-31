package tailnet

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
	"github.com/darkraise/darkpanel/internal/controller/cloud/cloudfake"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailnet/fake"
)

const (
	akChange  = "01J9X4K8Q2M5N7P0R3S6T8V1W2"
	akChange2 = "01J9X4K8Q2M5N7P0R3S6T8V1W3"
	akHost    = "01J9X4K8Q2M5N7P0R3S6T8V1H1"
)

var akKeysPath = "/api/v2/tailnet/" + fake.Tailnet + "/keys"

type akEnv struct {
	h   *tnHost
	srv *fake.Server
	t   tn
}

// akSetup connects an account whose OAuth client may assign tag:ci and tag:web.
func akSetup(t *testing.T) akEnv {
	t.Helper()
	h := newTNHost(t)
	srv := fake.New(t)
	acct := acConnect(t, h, srv)
	err := h.db.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(),
			`UPDATE tailnet_accounts SET tags_allowed_json = ? WHERE id = ?`, `["tag:ci","tag:web"]`, acct.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return akEnv{h: h, srv: srv, t: tnWith(srv)}
}

func akJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func akSame(t *testing.T, got, want json.RawMessage) {
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

func akApply(t *testing.T, e akEnv, change string, in AuthKeyInput) (CreatedKey, error) {
	t.Helper()
	out, err := e.t.authkeyCreate().Handler.Apply(context.Background(), e.h.env(change), akJSON(t, in))
	var ck CreatedKey
	if err == nil {
		if err := json.Unmarshal(out, &ck); err != nil {
			t.Fatal(err)
		}
	}
	return ck, err
}

func akOnly(t *testing.T, e akEnv) core.Effect {
	t.Helper()
	effs := e.h.journal.effects()
	if len(effs) != 1 {
		t.Fatalf("journal holds %d effects, want 1", len(effs))
	}
	return effs[0]
}

func akRow(t *testing.T, e akEnv, description string) keyRow {
	t.Helper()
	r, ok, err := loadKey(context.Background(), e.h.db.Read(), description)
	if err != nil || !ok {
		t.Fatalf("tailnet_keys row %s: ok %v, err %v", description, ok, err)
	}
	return r
}

func akCode(t *testing.T, err error) errcode.Code {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	return errcode.From(err).Code
}

// akUncertain applies a create whose answer is lost: committed says whether the
// provider kept the key.
func akUncertain(t *testing.T, committed bool) (akEnv, core.Effect) {
	t.Helper()
	e := akSetup(t)
	fault := cloudfake.Fault{Status: http.StatusInternalServerError}
	if committed {
		fault = cloudfake.Fault{LoseResponse: true}
	}
	e.srv.Cloud().Fail(http.MethodPost, akKeysPath, fault)
	_, err := akApply(t, e, akChange, AuthKeyInput{Tags: []string{"tag:ci"}})
	if got := akCode(t, err); got != errcode.CloudUncertainOutcome || core.IsDefinite(err) {
		t.Fatalf("apply error %v (definite %v), want an uncertain outcome", err, core.IsDefinite(err))
	}
	return e, akOnly(t, e)
}

func TestAkPlan(t *testing.T) {
	cases := []struct {
		name      string
		in        AuthKeyInput
		noAccount bool
		code      errcode.Code
		field     string
		diff      string
	}{
		{name: "default expiry", in: AuthKeyInput{Tags: []string{"tag:ci"}, Preauthorized: true}, diff: "24h0m0s"},
		{name: "given expiry", in: AuthKeyInput{Tags: []string{"tag:ci", "tag:web"}, ExpirySeconds: 3600}, diff: "1h0m0s"},
		{name: "no tags", in: AuthKeyInput{}, code: errcode.InvalidInput, field: "/tags"},
		{name: "tag outside tagsAllowed", in: AuthKeyInput{Tags: []string{"tag:db"}}, code: errcode.InvalidInput, field: "/tags"},
		{name: "malformed tag", in: AuthKeyInput{Tags: []string{"ci"}}, code: errcode.InvalidInput, field: "/tags"},
		{name: "expiry above 90 days", in: AuthKeyInput{Tags: []string{"tag:ci"}, ExpirySeconds: 7776001}, code: errcode.InvalidInput, field: "/expirySeconds"},
		{name: "negative expiry", in: AuthKeyInput{Tags: []string{"tag:ci"}, ExpirySeconds: -1}, code: errcode.InvalidInput, field: "/expirySeconds"},
		{name: "no account", in: AuthKeyInput{Tags: []string{"tag:ci"}}, noAccount: true, code: errcode.CloudNotConnected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := akSetup(t)
			if tc.noAccount {
				e = akEnv{h: newTNHost(t), srv: e.srv, t: e.t}
			}
			out, err := e.t.authkeyCreate().Handler.Plan(context.Background(), e.h.env(""), akJSON(t, tc.in))
			if tc.code != "" {
				if got := akCode(t, err); got != tc.code {
					t.Fatalf("code %s, want %s (%v)", got, tc.code, err)
				}
				fields := errcode.From(err).Fields
				if tc.field != "" && !slices.ContainsFunc(fields, func(f errcode.FieldError) bool { return f.Path == tc.field }) {
					t.Errorf("fields %v lack %s", fields, tc.field)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if out.StateHash == "" {
				t.Error("empty state hash")
			}
			if !slices.ContainsFunc(out.Diff, func(l core.DiffLine) bool { return l.Op == "+" && strings.Contains(l.Text, tc.diff) }) {
				t.Errorf("no added line mentions %s: %v", tc.diff, out.Diff)
			}
		})
	}
}

func TestAkPlanStateHash(t *testing.T) {
	e := akSetup(t)
	hash := func(tags ...string) string {
		out, err := e.t.authkeyCreate().Handler.Plan(context.Background(), e.h.env(""), akJSON(t, AuthKeyInput{Tags: tags}))
		if err != nil {
			t.Fatal(err)
		}
		return out.StateHash
	}
	if a, b := hash("tag:ci", "tag:web"), hash("tag:web", "tag:ci", "tag:ci"); a != b {
		t.Errorf("the same tags in another order hash differently: %s, %s", a, b)
	}
	if a, b := hash("tag:ci"), hash("tag:web"); a == b {
		t.Error("different tags hash alike")
	}
}

func TestAkApplyCreatesAndJournals(t *testing.T) {
	e := akSetup(t)
	ck, err := akApply(t, e, akChange, AuthKeyInput{Tags: []string{"tag:web", "tag:ci"}, Reusable: true})
	if err != nil {
		t.Fatal(err)
	}
	want := "darkpanel-" + akChange
	if ck.ID == "" || ck.Key == "" || ck.Description != want {
		t.Fatalf("created key %+v, want an id, a key and description %s", ck, want)
	}
	if n := len(e.srv.Keys()); n != 1 {
		t.Fatalf("provider holds %d keys, want 1", n)
	}

	row := akRow(t, e, want)
	if row.State != "live" || row.KeyID != ck.ID || row.Kind != "change" || row.OwnerID != akChange || row.ExpirySeconds != 86400 ||
		!reflect.DeepEqual(row.Tags, []string{"tag:ci", "tag:web"}) || !row.RequestedAt.Equal(e.h.clock.Now()) {
		t.Errorf("row %+v", row)
	}

	eff := akOnly(t, e)
	if eff.Kind != core.EffectCreate || eff.State != core.EffectMade || eff.Resource != "tailnet.key:"+ck.ID ||
		eff.Local || eff.CommitWait != cloud.CommitWait {
		t.Errorf("effect %+v", eff)
	}
	if !reflect.DeepEqual(eff.Recovery, &core.RecoveryID{Kind: "marker", Value: want}) {
		t.Errorf("recovery %+v", eff.Recovery)
	}
	akSame(t, eff.Before.State, json.RawMessage(`null`))
	if eff.Post == nil {
		t.Fatal("no post state")
	}
	akSame(t, eff.Post.State, akJSON(t, map[string]string{"id": ck.ID, "description": want}))
	if blob, _ := json.Marshal(eff); strings.Contains(string(blob), ck.Key) {
		t.Error("the journal holds the key secret")
	}

	var sent string
	for _, r := range e.srv.Cloud().Requests() {
		if r.Method == http.MethodPost && r.Path == akKeysPath {
			sent = string(r.Body)
		}
	}
	for _, part := range []string{want, "tag:ci", "tag:web", "86400"} {
		if !strings.Contains(sent, part) {
			t.Errorf("create request %q lacks %s", sent, part)
		}
	}
}

func TestAkApplyRefusedWritesNothing(t *testing.T) {
	e := akSetup(t)
	e.srv.SetScopes("devices:core")
	_, err := akApply(t, e, akChange, AuthKeyInput{Tags: []string{"tag:ci"}})
	if got := akCode(t, err); got != errcode.CloudPermissionMissing || !core.IsDefinite(err) {
		t.Fatalf("error %v (definite %v), want a definite %s", err, core.IsDefinite(err), errcode.CloudPermissionMissing)
	}
	if eff := akOnly(t, e); eff.State != core.EffectSkipped {
		t.Errorf("effect %s, want skipped", eff.State)
	}
	if r := akRow(t, e, "darkpanel-"+akChange); r.State != "deleted" {
		t.Errorf("row %s, want deleted", r.State)
	}
	if n := len(e.srv.Keys()); n != 0 {
		t.Errorf("provider holds %d keys", n)
	}
}

func TestAkApplyOutsideTagsAllowedIsRefusedBeforeAnyRecord(t *testing.T) {
	e := akSetup(t)
	_, err := akApply(t, e, akChange, AuthKeyInput{Tags: []string{"tag:db"}})
	if got := akCode(t, err); got != errcode.InvalidInput || !core.IsDefinite(err) {
		t.Fatalf("error %v, want a definite %s", err, errcode.InvalidInput)
	}
	if n := len(e.h.journal.effects()); n != 0 {
		t.Errorf("%d effects journaled", n)
	}
	if _, ok, _ := loadKey(context.Background(), e.h.db.Read(), "darkpanel-"+akChange); ok {
		t.Error("a tailnet_keys row was written")
	}
}

func TestAkApplyUncertainLeavesTheEffectOpen(t *testing.T) {
	for _, committed := range []bool{true, false} {
		e, eff := akUncertain(t, committed)
		if eff.State != core.EffectIntended {
			t.Errorf("committed %v: effect %s, want intended for the engine to settle", committed, eff.State)
		}
		if r := akRow(t, e, "darkpanel-"+akChange); r.State != "uncertain" || r.KeyID != "" {
			t.Errorf("committed %v: row %+v", committed, r)
		}
		want := 0
		if committed {
			want = 1
		}
		if n := len(e.srv.Keys()); n != want {
			t.Errorf("committed %v: provider holds %d keys, want %d", committed, n, want)
		}
	}
}

func TestAkApplyAnswerWithoutKeyIDIsUncertain(t *testing.T) {
	e := akSetup(t)
	e.srv.Cloud().Fail(http.MethodPost, akKeysPath, cloudfake.Fault{
		Status: http.StatusOK,
		Body:   `{"key":"tskey-auth-noid","expires":"2026-11-01T00:00:00Z","description":"darkpanel-` + akChange + `"}`,
	})
	out, err := akApply(t, e, akChange, AuthKeyInput{Tags: []string{"tag:ci"}})
	if got := akCode(t, err); got != errcode.CloudUncertainOutcome || core.IsDefinite(err) {
		t.Fatalf("apply error %v (definite %v), want an uncertain outcome", err, core.IsDefinite(err))
	}
	if out.Key != "" {
		t.Error("the secret was returned for a key without an id")
	}
	eff := akOnly(t, e)
	if eff.State != core.EffectIntended || eff.Resource != "tailnet.key:?darkpanel-"+akChange {
		t.Errorf("effect %s on %s, want intended on the marker resource", eff.State, eff.Resource)
	}
	if r := akRow(t, e, "darkpanel-"+akChange); r.State != "uncertain" || r.KeyID != "" {
		t.Errorf("row %+v, want uncertain with no key id", r)
	}
}

func TestAkObserve(t *testing.T) {
	ctx := context.Background()
	desc := "darkpanel-" + akChange

	t.Run("made key", func(t *testing.T) {
		e := akSetup(t)
		ck, err := akApply(t, e, akChange, AuthKeyInput{Tags: []string{"tag:ci"}})
		if err != nil {
			t.Fatal(err)
		}
		eff := akOnly(t, e)
		obs, err := e.t.authkeyCreate().Handler.Observe(ctx, e.h.env(akChange), eff)
		if err != nil {
			t.Fatal(err)
		}
		if obs.Matches != 1 || obs.Resource != "tailnet.key:"+ck.ID {
			t.Errorf("observation %+v", obs)
		}
		akSame(t, obs.Current.State, eff.Post.State)
	})

	t.Run("committed but unanswered create", func(t *testing.T) {
		e, eff := akUncertain(t, true)
		a, _, err := e.t.connected(ctx, e.h.db, coretest.Sealer{}, e.h.clock)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.CreateKey(ctx, KeyRequest{Description: "darkpanel-" + akChange2, Tags: []string{"tag:ci"}, ExpirySeconds: 3600}); err != nil {
			t.Fatal(err)
		}
		obs, err := e.t.authkeyCreate().Handler.Observe(ctx, e.h.env(akChange), eff)
		if err != nil {
			t.Fatal(err)
		}
		if obs.Matches != 1 || !obs.Exclusive || !strings.HasPrefix(obs.Resource, "tailnet.key:") || strings.Contains(obs.Resource, "?") {
			t.Fatalf("observation %+v: want the one key with description %s", obs, desc)
		}
		id := strings.TrimPrefix(obs.Resource, "tailnet.key:")
		akSame(t, obs.Current.State, akJSON(t, map[string]string{"id": id, "description": desc}))
	})

	t.Run("create that never committed", func(t *testing.T) {
		e, eff := akUncertain(t, false)
		obs, err := e.t.authkeyCreate().Handler.Observe(ctx, e.h.env(akChange), eff)
		if err != nil {
			t.Fatal(err)
		}
		if obs.Matches != 0 {
			t.Errorf("matches %d, want 0", obs.Matches)
		}
		akSame(t, obs.Current.State, json.RawMessage(`null`))
	})
}

func TestAkInverseDeletesTheKey(t *testing.T) {
	ctx := context.Background()
	e := akSetup(t)
	if _, err := akApply(t, e, akChange, AuthKeyInput{Tags: []string{"tag:ci"}}); err != nil {
		t.Fatal(err)
	}
	eff := akOnly(t, e)
	def := e.t.authkeyCreate()
	if _, err := def.Handler.Inverse(ctx, e.h.env(akChange), eff); err != nil {
		t.Fatal(err)
	}
	obs, err := def.Handler.Observe(ctx, e.h.env(akChange), eff)
	if err != nil {
		t.Fatal(err)
	}
	akSame(t, obs.Current.State, json.RawMessage(`null`))
	if r := akRow(t, e, "darkpanel-"+akChange); r.State != "deleted" {
		t.Errorf("row %s, want deleted", r.State)
	}
}

func TestAkInverseOfAKeyAlreadyGone(t *testing.T) {
	ctx := context.Background()
	e := akSetup(t)
	ck, err := akApply(t, e, akChange, AuthKeyInput{Tags: []string{"tag:ci"}})
	if err != nil {
		t.Fatal(err)
	}
	eff := akOnly(t, e)
	e.srv.Cloud().Fail(http.MethodDelete, akKeysPath+"/"+ck.ID, cloudfake.Fault{LoseResponse: true})
	if _, err := e.t.authkeyCreate().Handler.Inverse(ctx, e.h.env(akChange), eff); err != nil {
		t.Fatalf("undo of a key whose delete committed unanswered: %v", err)
	}
	if n := len(e.srv.Keys()); n != 0 {
		t.Errorf("provider holds %d keys, want 0", n)
	}
	if r := akRow(t, e, "darkpanel-"+akChange); r.State != "deleted" || r.KeyID != ck.ID {
		t.Errorf("row %+v, want deleted with key id %s", r, ck.ID)
	}
}

func TestAkResolved(t *testing.T) {
	cases := []struct {
		name       string
		committed  bool
		resolution string
		want       string
		withID     bool
	}{
		{"accept an observed key", true, core.ResolutionAccept, "live", true},
		{"reconcile an observed key", true, core.ResolutionReconcile, "live", true},
		{"accept no key", false, core.ResolutionAccept, "retired", false},
		{"reconcile no key", false, core.ResolutionReconcile, "retired", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			e, eff := akUncertain(t, tc.committed)
			def := e.t.authkeyCreate()
			obs, err := def.Handler.Observe(ctx, e.h.env(akChange), eff)
			if err != nil {
				t.Fatal(err)
			}
			e.h.clock.Advance(time.Hour)
			res := eff
			res.ResolvedAt = e.h.clock.Now()
			switch {
			case tc.resolution == core.ResolutionAccept:
				cur := obs.Current
				res.State, res.Current = core.EffectAccepted, &cur
			case obs.Matches == 0:
				res.State = core.EffectSkipped
			default:
				post := obs.Current
				res.State, res.Resource, res.Post = core.EffectMade, obs.Resource, &post
			}
			err = e.h.db.Write(ctx, func(tx *sql.Tx) error { return def.Handler.Resolved(ctx, tx, res, tc.resolution) })
			if err != nil {
				t.Fatal(err)
			}
			row := akRow(t, e, "darkpanel-"+akChange)
			if row.State != tc.want || !row.UpdatedAt.Equal(res.ResolvedAt) {
				t.Errorf("row %+v, want state %s updated at %v", row, tc.want, res.ResolvedAt)
			}
			if tc.withID != (row.KeyID != "" && row.KeyID == strings.TrimPrefix(obs.Resource, "tailnet.key:")) {
				t.Errorf("key id %q, observed resource %q", row.KeyID, obs.Resource)
			}
		})
	}
}

func TestAkList(t *testing.T) {
	ctx := context.Background()
	e := akSetup(t)
	now := e.h.clock.Now()
	rows := []keyRow{
		{Description: "darkpanel-enroll-" + akHost, Kind: "enroll", OwnerID: akHost, State: "retired", RequestedAt: now.Add(-time.Hour),
			UpdatedAt: now, ExpirySeconds: 3600, Tags: []string{"tag:web"}},
		{Description: "darkpanel-" + akChange, Kind: "change", OwnerID: akChange, KeyID: "k1", State: "live", RequestedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now, ExpirySeconds: 86400, Tags: []string{"tag:ci"}},
	}
	err := e.h.db.Write(ctx, func(tx *sql.Tx) error {
		for _, r := range rows {
			if err := insertKey(ctx, tx, r); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	before := len(e.srv.Cloud().Requests())
	raw, err := e.t.authkeyList().Handler.Read(ctx, e.h.env(""), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(e.srv.Cloud().Requests()); n != before {
		t.Errorf("list made %d provider requests", n-before)
	}
	if strings.Contains(string(raw), `"key"`) {
		t.Errorf("list output carries a key member: %s", raw)
	}
	var list AuthKeyList
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Keys) != 2 || list.Keys[0].Description != rows[1].Description || list.Keys[1].Description != rows[0].Description {
		t.Fatalf("keys %+v, want the two rows by request time", list.Keys)
	}
	first := list.Keys[0]
	if first.KeyID != "k1" || first.State != "live" || first.Kind != "change" || first.OwnerID != akChange ||
		!first.RequestedAt.Equal(rows[1].RequestedAt) || !first.Expires.Equal(rows[1].RequestedAt.Add(24*time.Hour)) {
		t.Errorf("first row %+v", first)
	}
}

type akModule struct{ t tn }

func (m akModule) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, Actions: []core.ActionDef{m.t.authkeyCreate(), m.t.authkeyList()}}
}

func TestAkActionsRegister(t *testing.T) {
	m := akModule{t: tnWith(fake.New(t))}
	if _, err := core.NewRegistry(m); err != nil {
		t.Fatal(err)
	}
	create, list := m.t.authkeyCreate(), m.t.authkeyList()
	if create.Spec.Name != "tailnet.authkey.create" || create.Spec.Class != core.ClassCompensable || create.Spec.Side != core.SideController ||
		!reflect.DeepEqual(create.Spec.LockKeys, []string{"tailnet"}) {
		t.Errorf("create spec %+v", create.Spec)
	}
	if create.Handler.Observe == nil || create.Handler.Inverse == nil || create.Handler.Resolved == nil {
		t.Error("create lacks Observe, Inverse or Resolved")
	}
	if list.Spec.Name != "tailnet.authkey.list" || list.Spec.Class != core.ClassRead || len(list.Spec.LockKeys) != 0 {
		t.Errorf("list spec %+v", list.Spec)
	}
}
