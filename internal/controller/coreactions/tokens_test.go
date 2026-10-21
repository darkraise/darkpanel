package coreactions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// testPassword is a valid password; tests scan outputs and errors for it.
const testPassword = "correct horse battery staple"

// noSecret fails when any of values, rendered with %+v, with Error() or as
// JSON, contains one of secrets.
func noSecret(t *testing.T, secrets []string, values ...any) {
	t.Helper()
	if msg := secretLeak(secrets, values...); msg != "" {
		t.Fatal(msg)
	}
}

// secretLeak returns "" when no rendering of values contains one of secrets,
// else a message naming the first leaking value, its rendering and the
// secret by position. It never quotes the text, so a failing run does not
// write the secret into the test log.
func secretLeak(secrets []string, values ...any) string {
	for i, v := range values {
		type rendering struct{ name, text string }
		texts := []rendering{{"%+v", fmt.Sprintf("%+v", v)}}
		js := v
		if err, ok := v.(error); ok {
			texts = append(texts, rendering{"Error()", err.Error()})
			var e *errcode.Error
			if errors.As(err, &e) {
				js = e
			}
		}
		if b, err := json.Marshal(js); err == nil {
			texts = append(texts, rendering{"JSON", string(b)})
		}
		for _, r := range texts {
			for j, s := range secrets {
				if s != "" && strings.Contains(r.text, s) {
					return fmt.Sprintf("value %d (%T) rendered as %s contains secret %d", i, v, r.name, j)
				}
			}
		}
	}
	return ""
}

// activeUser creates and activates username through the identity service
// and returns the user and its principal on a network listener. It then
// advances the clock by one TOTP period, so the next code is a fresh step.
func (e *testEnv) activeUser(username string) (auth.User, core.Principal) {
	e.t.Helper()
	ctx := context.Background()
	_, totp, err := e.auth.CreateUser(ctx, username, testPassword)
	if err != nil {
		e.t.Fatal(err)
	}
	code, err := auth.TOTPCode(totp, e.clock.Now())
	if err != nil {
		e.t.Fatal(err)
	}
	u, err := e.auth.ActivateUser(ctx, username, code)
	if err != nil {
		e.t.Fatal(err)
	}
	e.clock.Advance(auth.TOTPPeriod)
	return u, core.Principal{Actor: "user:" + u.ID, UserID: u.ID, Scope: core.ScopeAdmin, Source: "127.0.0.1"}
}

func TestTokenCreate(t *testing.T) {
	e := newTestEnv(t)
	_, alice := e.activeUser("alice")
	in := TokenCreateInput{Name: "ci", Scope: core.ScopeOperate, ExpiresInDays: 30}

	pv, err := e.plan(alice, "core.token.create", in)
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := []core.DiffLine{
		{Op: "+", Text: `token "ci" with scope operate for user alice, expiring in 30 days`},
		{Op: " ", Text: "compensated by core.token.revoke with the new token's id"},
	}
	if !reflect.DeepEqual(pv.Diff, wantDiff) || len(pv.Guards) != 0 || pv.Confirmation != "" {
		t.Errorf("plan = %+v, want diff %+v", pv, wantDiff)
	}
	v, err := e.apply(alice, pv.PlanID, "")
	if err != nil || v.State != core.StateCompleted {
		t.Fatalf("apply = %+v, %v", v, err)
	}
	if v.CompensatedBy != "core.token.revoke" {
		t.Errorf("compensated by %q, want core.token.revoke", v.CompensatedBy)
	}
	var out NewToken
	if err := json.Unmarshal(v.Output, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.Token, auth.TokenPrefix+out.ID+"_") || out.Name != "ci" || out.Scope != core.ScopeOperate {
		t.Errorf("output = %+v, want the new token of scope operate", out)
	}
	if want := out.CreatedAt.Add(30 * 24 * time.Hour); !out.ExpiresAt.Equal(want) {
		t.Errorf("expiresAt = %v, want %v", out.ExpiresAt, want)
	}

	stored, err := e.engine.Change(context.Background(), v.ID)
	if err != nil {
		t.Fatal(err)
	}
	var redacted map[string]any
	if err := json.Unmarshal(stored.Output, &redacted); err != nil {
		t.Fatal(err)
	}
	if redacted["token"] != "[redacted]" || redacted["id"] != out.ID {
		t.Errorf("stored output = %s, want the token redacted and the id kept", stored.Output)
	}
	entries, _, err := e.audit.List(context.Background(), core.AuditQuery{})
	if err != nil || len(entries) != 1 || entries[0].Action != "core.token.create" {
		t.Fatalf("audit = %+v, %v; want one core.token.create row", entries, err)
	}
	noSecret(t, []string{out.Token, strings.TrimPrefix(out.Token, auth.TokenPrefix+out.ID+"_")}, pv, stored, entries)

	var list TokenList
	if err := e.read(alice, "core.token.list", NoInput{}, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Tokens) != 1 || !reflect.DeepEqual(list.Tokens[0], out.TokenInfo) {
		t.Errorf("tokens = %+v, want [%+v]", list.Tokens, out.TokenInfo)
	}
}

func TestTokenCreateDefaultsAndRefusals(t *testing.T) {
	e := newTestEnv(t)
	_, alice := e.activeUser("alice")
	pv, err := e.plan(alice, "core.token.create", TokenCreateInput{Name: "ci", Scope: core.ScopeRead})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(pv.Diff[0].Text, "expiring in 90 days") {
		t.Errorf("diff = %+v, want the 90-day default", pv.Diff)
	}
	for _, raw := range []string{
		`{"name":"ci","scope":"root"}`,
		`{"name":"","scope":"read"}`,
		`{"name":"ci","scope":"read","expiresInDays":366}`,
		`{"name":"ci","scope":"read","expiresInDays":0}`,
		`{"name":"ci"}`,
	} {
		_, err := e.engine.Plan(context.Background(), alice, ref("core.token.create"), json.RawMessage(raw), core.PlanOptions{})
		var ce *errcode.Error
		if !errors.As(err, &ce) || ce.Code != errcode.InvalidInput {
			t.Errorf("plan %s: err = %v, want request.invalid_input", raw, err)
		}
	}

	pending, _, err := e.auth.CreateUser(context.Background(), "bob", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	bob := core.Principal{Actor: "user:" + pending.ID, UserID: pending.ID, Scope: core.ScopeAdmin}
	_, err = e.plan(bob, "core.token.create", TokenCreateInput{Name: "ci", Scope: core.ScopeRead})
	wantCode(t, err, errcode.NotFound)
}

func TestTokenRevoke(t *testing.T) {
	e := newTestEnv(t)
	_, alice := e.activeUser("alice")
	var tok NewToken
	e.run(alice, "core.token.create", TokenCreateInput{Name: "ci", Scope: core.ScopeRead}, &tok)

	pv, err := e.plan(alice, "core.token.revoke", TokenRevokeInput{ID: tok.ID})
	if err != nil {
		t.Fatal(err)
	}
	subject := fmt.Sprintf(`token %s ("ci", scope read)`, tok.ID)
	wantDiff := []core.DiffLine{
		{Op: "-", Text: subject + ": active"},
		{Op: "+", Text: subject + ": revoked; a request that sends it gets auth.required"},
	}
	if !reflect.DeepEqual(pv.Diff, wantDiff) || pv.Confirmation != tok.ID {
		t.Errorf("plan = %+v, want diff %+v and the token id as the phrase", pv, wantDiff)
	}
	_, err = e.apply(alice, pv.PlanID, "")
	wantCode(t, err, errcode.PlanConfirmationNeeded)
	v, err := e.apply(alice, pv.PlanID, tok.ID)
	if err != nil || v.State != core.StateCompleted {
		t.Fatalf("apply = %+v, %v", v, err)
	}
	var out auth.TokenInfo
	if err := json.Unmarshal(v.Output, &out); err != nil {
		t.Fatal(err)
	}
	if out.RevokedAt == nil || !out.RevokedAt.Equal(e.clock.Now()) {
		t.Errorf("revokedAt = %v, want %v", out.RevokedAt, e.clock.Now())
	}

	again, err := e.plan(alice, "core.token.revoke", TokenRevokeInput{ID: tok.ID})
	if err != nil {
		t.Fatal(err)
	}
	want := []core.DiffLine{{Op: " ", Text: subject + ": already revoked at " +
		e.clock.Now().UTC().Format(time.RFC3339) + "; nothing changes"}}
	if !reflect.DeepEqual(again.Diff, want) {
		t.Errorf("diff = %+v, want %+v", again.Diff, want)
	}
}

func TestTokenRevokeStateChanged(t *testing.T) {
	e := newTestEnv(t)
	u, alice := e.activeUser("alice")
	var tok NewToken
	e.run(alice, "core.token.create", TokenCreateInput{Name: "ci", Scope: core.ScopeRead}, &tok)
	pv, err := e.plan(alice, "core.token.revoke", TokenRevokeInput{ID: tok.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.auth.RevokeToken(context.Background(), u.ID, tok.ID); err != nil {
		t.Fatal(err)
	}
	_, err = e.apply(alice, pv.PlanID, tok.ID)
	wantCode(t, err, errcode.PlanStateChanged)
}

func TestTokensBelongToTheirUser(t *testing.T) {
	e := newTestEnv(t)
	_, alice := e.activeUser("alice")
	_, bob := e.activeUser("bob")
	var tok NewToken
	e.run(alice, "core.token.create", TokenCreateInput{Name: "ci", Scope: core.ScopeRead}, &tok)

	var list TokenList
	if err := e.read(bob, "core.token.list", NoInput{}, &list); err != nil || len(list.Tokens) != 0 {
		t.Errorf("bob's tokens = %+v, %v; want none", list.Tokens, err)
	}
	_, err := e.plan(bob, "core.token.revoke", TokenRevokeInput{ID: tok.ID})
	wantCode(t, err, errcode.NotFound)
}

func TestTokenActionsNeedAUser(t *testing.T) {
	e := newTestEnv(t)
	var list TokenList
	wantCode(t, e.read(root, "core.token.list", NoInput{}, &list), errcode.Forbidden)
	_, err := e.plan(root, "core.token.create", TokenCreateInput{Name: "ci", Scope: core.ScopeRead})
	wantCode(t, err, errcode.Forbidden)
	_, err = e.plan(root, "core.token.revoke", TokenRevokeInput{ID: "01JZ"})
	wantCode(t, err, errcode.Forbidden)

	// Recovery verifies as system:recovery, which has no user: an interrupted
	// token change is reported unverified rather than guessed.
	wantCode(t, e.verify("core.token.create", TokenCreateInput{Name: "ci", Scope: core.ScopeRead}), errcode.Forbidden)
	wantCode(t, e.verify("core.token.revoke", TokenRevokeInput{ID: "01JZ"}), errcode.Forbidden)
}

// hidden prints as "hidden" but marshals its value, so only the JSON
// rendering shows it.
type hidden struct{ Value string }

func (hidden) String() string { return "hidden" }

func TestSecretLeakNamesPositionsOnly(t *testing.T) {
	const secret = "s3cret-token-value"
	type holder struct{ Value string }
	for _, tc := range []struct {
		name    string
		secrets []string
		values  []any
		want    string
	}{
		{"no leak", []string{"other", secret}, []any{"clean", holder{"clean"}}, ""},
		{"empty secret ignored", []string{""}, []any{"anything"}, ""},
		{"struct", []string{"other", secret}, []any{"clean", holder{"x" + secret}},
			"value 1 (coreactions.holder) rendered as %+v contains secret 1"},
		{"JSON only", []string{secret}, []any{hidden{secret}},
			"value 0 (coreactions.hidden) rendered as JSON contains secret 0"},
		{"error", []string{secret}, []any{errcode.New(errcode.NotFound, "no token "+secret)},
			"value 0 (*errcode.Error) rendered as %+v contains secret 0"},
	} {
		got := secretLeak(tc.secrets, tc.values...)
		if got != tc.want {
			t.Errorf("%s: secretLeak = %q, want %q", tc.name, got, tc.want)
		}
		if strings.Contains(got, secret) {
			t.Errorf("%s: the message carries the secret", tc.name)
		}
	}
}
