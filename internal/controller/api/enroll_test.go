package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const enrollPassword = "a member's own passphrase" //nolint:gosec // G101: a test value, not a credential

// invite makes username a pending member through the identity service and
// returns its enrollment token.
func (h *harness) invite(username string) string {
	h.t.Helper()
	inv, err := h.auth.Invite(context.Background(), auth.InviteRequest{Username: username, Role: auth.RoleMember})
	if err != nil {
		h.t.Fatal(err)
	}
	return inv.Token
}

func enrollBody(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// begin posts token to the begin route.
func (h *harness) begin(token string) *httptest.ResponseRecorder {
	return h.serve(request(http.MethodPost, enrollBeginPath, enrollBody(map[string]string{"token": token})))
}

// beginSecret begins token's enrollment and returns the TOTP secret.
func (h *harness) beginSecret(token string) string {
	h.t.Helper()
	rec := h.begin(token)
	if rec.Code != http.StatusOK {
		h.t.Fatalf("begin: %d %s", rec.Code, rec.Body)
	}
	var out auth.EnrollmentStart
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		h.t.Fatal(err)
	}
	return out.Secret
}

// finish posts token with enrollPassword and a code from secret ("" sends
// 000000).
func (h *harness) finish(token, secret string) *httptest.ResponseRecorder {
	h.t.Helper()
	code := "000000"
	if secret != "" {
		var err error
		if code, err = auth.TOTPCode(secret, h.clock.Now()); err != nil {
			h.t.Fatal(err)
		}
	}
	return h.serve(request(http.MethodPost, enrollFinishPath,
		enrollBody(map[string]string{"token": token, "password": enrollPassword, "code": code})))
}

func TestEnrollmentLink(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	token := h.invite("mia")

	rec := h.begin(token)
	if rec.Code != http.StatusOK {
		t.Fatalf("begin: %d %s", rec.Code, rec.Body)
	}
	var start auth.EnrollmentStart
	if err := json.Unmarshal(rec.Body.Bytes(), &start); err != nil {
		t.Fatal(err)
	}
	if start.Username != "mia" || start.Secret == "" ||
		start.URI != "otpauth://totp/Darkpanel:mia?secret="+start.Secret+"&issuer=Darkpanel&algorithm=SHA1&digits=6&period=30" ||
		!start.ExpiresAt.Equal(testStart.Add(auth.TOTPPeriod).Add(auth.EnrollmentTTL)) {
		t.Fatalf("begin = %+v; want mia's otpauth URI and the link's expiry", start)
	}

	bad := h.finish(token, "")
	decodeProblem(t, bad, http.StatusUnprocessableEntity, errcode.InvalidInput)
	if u, _ := h.auth.GetUser(ctx, "mia"); u.State != auth.StatePending {
		t.Fatalf("after a wrong code mia is %s, want pending", u.State)
	}

	rec = h.finish(token, start.Secret)
	if rec.Code != http.StatusOK {
		t.Fatalf("finish: %d %s", rec.Code, rec.Body)
	}
	var session *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookiePlain {
			session = c
		}
	}
	if session == nil || session.Value == "" {
		t.Fatal("finish set no session cookie")
	}
	if !strings.Contains(rec.Body.String(), `"username":"mia"`) || !strings.Contains(rec.Body.String(), `"state":"active"`) {
		t.Errorf("finish body %s; want mia, active", rec.Body)
	}
	me := h.serve(withSession(request(http.MethodGet, "/api/v1/auth/session", ""), session.Value, ""))
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"username":"mia"`) {
		t.Fatalf("session after enrollment: %d %s", me.Code, me.Body)
	}
	h.clock.Advance(auth.TOTPPeriod)
	if _, err := h.auth.Login(ctx, auth.LoginRequest{Username: "mia", Password: enrollPassword,
		Code: mustCode(t, start.Secret, h.clock.Now()), Source: "192.0.2.10"}); err != nil {
		t.Errorf("password login after enrollment: %v", err)
	}

	h.clock.Advance(auth.TOTPPeriod)
	decodeProblem(t, h.finish(token, start.Secret), http.StatusGone, errcode.EnrollmentExpired)
	decodeProblem(t, h.begin(token), http.StatusGone, errcode.EnrollmentExpired)
	if _, _, err := h.auth.ResetTOTP(ctx, "mia"); err != nil {
		t.Fatal(err)
	}
	decodeProblem(t, h.begin(token), http.StatusGone, errcode.EnrollmentExpired)

	late := h.invite("nia")
	lateSecret := h.beginSecret(late)
	h.clock.Advance(auth.EnrollmentTTL)
	decodeProblem(t, h.finish(late, lateSecret), http.StatusGone, errcode.EnrollmentExpired)

	tokens := map[string]string{}
	for name, invalidate := range map[string]func(username string){
		"renew": func(u string) {
			if _, err := h.auth.RenewInvite(ctx, u); err != nil {
				t.Fatal(err)
			}
		},
		"cancel": func(u string) {
			if _, err := h.auth.CancelInvite(ctx, u); err != nil {
				t.Fatal(err)
			}
		},
		"disable": func(u string) {
			if _, err := h.auth.SetDisabled(ctx, u, true); err != nil {
				t.Fatal(err)
			}
		},
	} {
		username := "old-" + name
		old := h.invite(username)
		secret := h.beginSecret(old)
		invalidate(username)
		decodeProblem(t, h.finish(old, secret), http.StatusGone, errcode.EnrollmentExpired)
		decodeProblem(t, h.begin(old), http.StatusGone, errcode.EnrollmentExpired)
		tokens[username] = old
	}
	if _, err := h.auth.SetDisabled(ctx, "old-disable", false); err != nil {
		t.Fatal(err)
	}
	decodeProblem(t, h.begin(tokens["old-disable"]), http.StatusGone, errcode.EnrollmentExpired)
	renewed, err := h.auth.RenewInvite(ctx, "old-disable")
	if err != nil {
		t.Fatal(err)
	}
	if rec := h.begin(renewed.Token); rec.Code != http.StatusOK {
		t.Errorf("begin with a link renewed after enable: %d %s", rec.Code, rec.Body)
	}

	var ok, failed int
	entries, _, err := h.audit.List(ctx, core.AuditQuery{Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Action != auth.AuditLoginAction || !strings.Contains(string(e.Input), `"method":"enrollment"`) {
			continue
		}
		if e.Source != "192.0.2.10" {
			t.Errorf("enrollment audit source %q, want the client address", e.Source)
		}
		switch {
		case e.Result == "ok" && string(e.Input) == `{"method":"enrollment","username":"mia"}` && strings.HasPrefix(e.Actor, "user:"):
			ok++
		case e.Result == "error":
			failed++
		}
	}
	if ok != 1 || failed < 5 {
		t.Errorf("enrollment audits: %d ok, %d failed; want mia's one success and every refused finish", ok, failed)
	}
	noCredentials(t, h, []*httptest.ResponseRecorder{rec}, map[string]string{"password": enrollPassword})
}

// TestEnrollmentSharesTheSourceBudget: refused links count against the
// login's per-source budget, so a source that guesses tokens is locked out.
func TestEnrollmentSharesTheSourceBudget(t *testing.T) {
	h := newHarness(t)
	token := h.invite("mia")
	for i := range auth.SourceFailureLimit {
		guess := strings.Repeat("A", 42) + string(rune('A'+i%26))
		decodeProblem(t, h.begin(guess), http.StatusGone, errcode.EnrollmentExpired)
	}
	decodeProblem(t, h.begin(token), http.StatusTooManyRequests, errcode.RateLimited)
	decodeProblem(t, h.finish(token, ""), http.StatusTooManyRequests, errcode.RateLimited)
	h.clock.Advance(auth.LockDuration)
	if rec := h.begin(token); rec.Code != http.StatusOK {
		t.Errorf("begin after the lock: %d %s", rec.Code, rec.Body)
	}
}

func mustCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := auth.TOTPCode(secret, at)
	if err != nil {
		t.Fatal(err)
	}
	return code
}
