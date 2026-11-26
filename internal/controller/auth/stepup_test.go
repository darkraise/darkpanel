package auth

import (
	"context"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// stepUpPrincipal is the principal of alice's browser session.
func stepUpPrincipal(u User) core.Principal {
	return core.Principal{Actor: "user:" + u.ID, UserID: u.ID, Scope: core.ScopeAdmin, Source: "10.0.0.1"}
}

func wantStepUpCode(t *testing.T, what string, err error, code errcode.Code) {
	t.Helper()
	if got := errcode.From(err).Code; err == nil || got != code {
		t.Fatalf("%s: err = %v (%s), want %s", what, err, got, code)
	}
}

func elevated(t *testing.T, s *Service, session string) bool {
	t.Helper()
	ok, err := s.Elevated(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestStepUpElevates15Minutes(t *testing.T) {
	s, clock, _ := newTestService(t)
	u, secret := createActive(t, s, clock, "alice")
	session, _ := newSession(t, s, u.ID)
	if elevated(t, s, session) {
		t.Fatal("a new session is elevated")
	}
	start := clock.Now()
	code, err := TOTPCode(secret, start)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.StepUp(context.Background(), stepUpPrincipal(u), session, code); err != nil {
		t.Fatalf("StepUp: %v", err)
	}
	clock.Advance(StepUpWindow - time.Second)
	if !elevated(t, s, session) {
		t.Error("not elevated 14m59s after step-up")
	}
	clock.Advance(2 * time.Second)
	if elevated(t, s, session) {
		t.Error("still elevated 15m01s after step-up")
	}
}

func TestStepUpRefusesReplay(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, secret := createActive(t, s, clock, "alice")
	signIn := func(code string) error {
		_, err := s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: code, Source: "10.0.0.1"})
		return err
	}
	stepS := clock.Now()
	codeS, _ := TOTPCode(secret, stepS)
	if err := signIn(codeS); err != nil {
		t.Fatalf("sign-in: %v", err)
	}
	wantStepUpCode(t, "a second sign-in with the same code", signIn(codeS), errcode.InvalidCredentials)
	session, _ := newSession(t, s, u.ID)
	p := stepUpPrincipal(u)
	codePrev, _ := TOTPCode(secret, stepS.Add(-TOTPPeriod))
	// More replays than the limiter allows wrong codes: none of them counts.
	for range UsernameFailureLimit + 1 {
		wantStepUpCode(t, "step-up with the sign-in step", s.StepUp(ctx, p, session, codeS), errcode.StepUpWait)
		wantStepUpCode(t, "step-up with the step before", s.StepUp(ctx, p, session, codePrev), errcode.StepUpWait)
	}
	if elevated(t, s, session) {
		t.Fatal("a replayed code elevated the session")
	}
	clock.Advance(TOTPPeriod)
	codeNext, _ := TOTPCode(secret, clock.Now())
	if err := s.StepUp(ctx, p, session, codeNext); err != nil {
		t.Fatalf("step-up with the next step: %v", err)
	}
	if !elevated(t, s, session) {
		t.Fatal("not elevated after step-up")
	}
	wantStepUpCode(t, "the step-up code presented again", s.StepUp(ctx, p, session, codeNext), errcode.StepUpWait)
}

func TestStepUpUnavailable(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	pending, _, err := s.CreateUser(ctx, "bob", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	session, _ := newSession(t, s, pending.ID)
	u, secret := createActive(t, s, clock, "alice")
	code, _ := TOTPCode(secret, clock.Now())
	token := core.Principal{Actor: "token:t1", UserID: u.ID, TokenID: "t1", Scope: core.ScopeAdmin, Source: "10.0.0.1"}
	for name, call := range map[string]func() error{
		"pending user": func() error { return s.StepUp(ctx, stepUpPrincipal(pending), session, "000000") },
		"API token":    func() error { return s.StepUp(ctx, token, "", code) },
	} {
		err := call()
		wantStepUpCode(t, name, err, errcode.StepUpUnavailable)
		if got := errcode.From(err).Detail; got != "Set up an authenticator to use the console and jobs" {
			t.Errorf("%s: message %q", name, got)
		}
	}
	// The token's refusal came before any code check: the code still works.
	alice, _ := newSession(t, s, u.ID)
	if err := s.StepUp(ctx, stepUpPrincipal(u), alice, code); err != nil {
		t.Errorf("the code the token presented no longer works: %v", err)
	}
}

func TestStepUpRateLimit(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, secret := createActive(t, s, clock, "alice")
	session, _ := newSession(t, s, u.ID)
	p := stepUpPrincipal(u)
	right, _ := TOTPCode(secret, clock.Now())
	wrong := "000000"
	if wrong == right {
		wrong = "111111"
	}
	for range UsernameFailureLimit {
		wantStepUpCode(t, "wrong code", s.StepUp(ctx, p, session, wrong), errcode.InvalidCredentials)
	}
	wantStepUpCode(t, "the sixth attempt", s.StepUp(ctx, p, session, right), errcode.RateLimited)
	// The limiter is the sign-in one: alice's login from the same source is locked too.
	_, err := s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: right, Source: "10.0.0.1"})
	wantStepUpCode(t, "sign-in after the wrong step-up codes", err, errcode.RateLimited)
}
