package auth

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func code(t *testing.T, secret string, clock *coretest.FakeClock) string {
	t.Helper()
	c, err := TOTPCode(secret, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// wrongCode returns a six-digit code that matches none of the steps
// matchTOTP accepts for secret at the clock's time.
func wrongCode(t *testing.T, secret string, clock *coretest.FakeClock) string {
	t.Helper()
	var valid []string
	for _, step := range []time.Duration{-1, 0, 1} {
		c, err := TOTPCode(secret, clock.Now().Add(step*TOTPPeriod))
		if err != nil {
			t.Fatal(err)
		}
		valid = append(valid, c)
	}
	for d := '0'; d <= '9'; d++ {
		if c := strings.Repeat(string(d), totpDigits); !slices.Contains(valid, c) {
			return c
		}
	}
	t.Fatal("no wrong code found")
	return ""
}

// countHashes makes s count the argon2id computations it runs.
func countHashes(s *Service) *atomic.Int32 {
	var n atomic.Int32
	inner := s.hasher.compute
	s.hasher.compute = func(password, salt []byte, p Params) []byte {
		n.Add(1)
		return inner(password, salt, p)
	}
	return &n
}

func TestLogin(t *testing.T) {
	s, clock, audit := newTestService(t)
	ctx := context.Background()
	u, secret := createActive(t, s, clock, "alice")
	c := code(t, secret, clock)
	res, err := s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: c, Source: "10.0.0.1", UserAgent: "ua"})
	if err != nil {
		t.Fatal(err)
	}
	if res.User.ID != u.ID || res.User.State != StateActive || !res.ExpiresAt.Equal(clock.Now().Add(SessionIdle)) {
		t.Errorf("result = %+v, want alice with a session ending %s", res, clock.Now().Add(SessionIdle))
	}
	if res.CSRFToken == "" || res.CSRFToken != s.CSRFToken(res.SessionID) {
		t.Errorf("CSRF token %q is not the session's", res.CSRFToken)
	}
	id, err := s.Authenticate(ctx, Credentials{Session: res.SessionID, Source: "10.0.0.1"})
	if err != nil || id.Principal.UserID != u.ID {
		t.Fatalf("Authenticate with the new session = %+v, %v", id, err)
	}
	if count(t, s, "core_users", "id = ? AND totp_last_step = ?", u.ID, totpStep(clock.Now())) != 1 {
		t.Error("login did not store the code's step")
	}
	entries, _, _ := audit.List(ctx, core.AuditQuery{})
	if len(entries) != 1 {
		t.Fatalf("%d audit entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Actor != "user:"+u.ID || e.Action != AuditLoginAction || e.Version != 1 || e.Phase != "login" ||
		e.Result != "ok" || e.ErrorCode != "" || e.Source != "10.0.0.1" || !e.At.Equal(clock.Now()) ||
		string(e.Input) != `{"username":"alice"}` || e.ID == "" {
		t.Errorf("audit entry = %+v", e)
	}
}

func TestLoginFailuresAreIdentical(t *testing.T) {
	s, clock, audit := newTestService(t)
	ctx := context.Background()
	alice, secret := createActive(t, s, clock, "alice")
	bob, bobSecret, err := s.CreateUser(ctx, "bob", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	right := code(t, secret, clock)
	hashes := countHashes(s)
	cases := []struct {
		name  string
		req   LoginRequest
		actor string
	}{
		{"unknown user", LoginRequest{Username: "nobody", Password: testPassword, Code: right}, ActorAnonymous},
		{"wrong password", LoginRequest{Username: "alice", Password: "wrong horse battery", Code: right}, "user:" + alice.ID},
		{"wrong code", LoginRequest{Username: "alice", Password: testPassword, Code: wrongCode(t, secret, clock)}, "user:" + alice.ID},
		{"pending user", LoginRequest{Username: "bob", Password: testPassword, Code: code(t, bobSecret, clock)}, "user:" + bob.ID},
	}
	var first *errcode.Error
	for _, tt := range cases {
		tt.req.Source = "10.0.0.1"
		hashes.Store(0)
		_, err := s.Login(ctx, tt.req)
		var e *errcode.Error
		if !errors.As(err, &e) || e.Code != errcode.InvalidCredentials {
			t.Fatalf("%s: error = %v, want %s", tt.name, err, errcode.InvalidCredentials)
		}
		if first == nil {
			first = e
		} else if e.Detail != first.Detail || e.Error() != first.Error() || len(e.Fields) != 0 {
			t.Errorf("%s: error %q differs from %q", tt.name, e.Error(), first.Error())
		}
		if n := hashes.Load(); n != 1 {
			t.Errorf("%s: ran %d argon2id hashes, want 1", tt.name, n)
		}
		entries, _, _ := audit.List(ctx, core.AuditQuery{Limit: 1})
		got := entries[0]
		if got.Actor != tt.actor || got.Result != "error" || got.ErrorCode != string(errcode.InvalidCredentials) ||
			got.Phase != "login" || string(got.Input) != `{"username":"`+tt.req.Username+`"}` {
			t.Errorf("%s: audit entry = %+v, want actor %s", tt.name, got, tt.actor)
		}
		for _, secretValue := range []string{tt.req.Password, tt.req.Code} {
			if strings.Contains(string(got.Input), secretValue) || strings.Contains(e.Error(), secretValue) {
				t.Errorf("%s: audit input or error carries a password or code", tt.name)
			}
		}
	}

	if _, err := s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: right, Source: "10.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	_, err = s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: right, Source: "10.0.0.1"})
	if e := errcode.From(err); e.Code != errcode.InvalidCredentials || e.Error() != first.Error() {
		t.Errorf("replayed code: error = %v, want the same %s", err, errcode.InvalidCredentials)
	}

	before, _, _ := audit.List(ctx, core.AuditQuery{})
	hashes.Store(0)
	_, err = s.Login(ctx, LoginRequest{Username: "Not A Name", Password: testPassword, Code: right, Source: "10.0.0.1"})
	if e := errcode.From(err); e.Code != errcode.InvalidCredentials || e.Error() != first.Error() {
		t.Errorf("invalid username: error = %v, want the same %s", err, errcode.InvalidCredentials)
	}
	after, _, _ := audit.List(ctx, core.AuditQuery{})
	if len(after) != len(before) || hashes.Load() != 0 {
		t.Errorf("an invalid username was evaluated: %d audit entries added, %d hashes", len(after)-len(before), hashes.Load())
	}
}

func TestLoginRateLimit(t *testing.T) {
	s, clock, audit := newTestService(t)
	ctx := context.Background()
	_, secret := createActive(t, s, clock, "alice")
	for range UsernameFailureLimit {
		_, err := s.Login(ctx, LoginRequest{Username: "alice", Password: "wrong horse battery", Code: "000000", Source: "10.0.0.1"})
		wantCode(t, err, errcode.InvalidCredentials)
	}
	before, _, _ := audit.List(ctx, core.AuditQuery{})
	hashes := countHashes(s)
	_, err := s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: code(t, secret, clock), Source: "10.0.0.2"})
	wantCode(t, err, errcode.RateLimited)
	var ra *RetryAfterError
	if !errors.As(err, &ra) || ra.Seconds() != int(LockDuration/time.Second) {
		t.Fatalf("error %v carries no Retry-After of %s", err, LockDuration)
	}
	after, _, _ := audit.List(ctx, core.AuditQuery{})
	if hashes.Load() != 0 || len(after) != len(before) {
		t.Errorf("a blocked attempt was evaluated: %d hashes, %d audit entries added", hashes.Load(), len(after)-len(before))
	}
	clock.Advance(LockDuration)
	if _, err := s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: code(t, secret, clock), Source: "10.0.0.2"}); err != nil {
		t.Errorf("login after the lock ended: %v", err)
	}
}

func TestConcurrentLoginsWithOneCode(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	_, secret := createActive(t, s, clock, "alice")
	for round := range 5 {
		c := code(t, secret, clock)
		start := make(chan struct{})
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i := range errs {
			wg.Go(func() {
				<-start
				_, errs[i] = s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: c, Source: "10.0.0.1"})
			})
		}
		close(start)
		wg.Wait()
		ok := 0
		for _, err := range errs {
			if err == nil {
				ok++
			} else {
				wantCode(t, err, errcode.InvalidCredentials)
			}
		}
		if ok != 1 {
			t.Fatalf("round %d: %d of two concurrent logins with one code succeeded, want exactly 1", round, ok)
		}
		clock.Advance(TOTPPeriod)
	}
}

func TestConcurrentLoginsRespectTheLimit(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	createActive(t, s, clock, "alice")
	wrong := LoginRequest{Username: "alice", Password: "wrong horse battery", Code: "000000", Source: "10.0.0.9"}
	for range UsernameFailureLimit - 1 {
		_, err := s.Login(ctx, wrong)
		wantCode(t, err, errcode.InvalidCredentials)
	}
	var hashes atomic.Int32
	entered := make(chan struct{}, 16)
	gate := make(chan struct{})
	inner := s.hasher.compute
	s.hasher.compute = func(password, salt []byte, p Params) []byte {
		hashes.Add(1)
		entered <- struct{}{}
		<-gate
		return inner(password, salt, p)
	}
	const attempts = MaxConcurrentHashes + 4
	errs := make([]error, attempts)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Go(func() { _, errs[i] = s.Login(ctx, wrong) })
	}
	for range MaxConcurrentHashes {
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("no login reached the hash")
		}
	}
	close(gate)
	wg.Wait()
	limited := 0
	for _, err := range errs {
		if errcode.From(err).Code == errcode.RateLimited {
			limited++
		} else {
			wantCode(t, err, errcode.InvalidCredentials)
		}
	}
	if hashes.Load() != MaxConcurrentHashes || limited != attempts-MaxConcurrentHashes {
		t.Fatalf("%d concurrent attempts ran %d hashes with %d rate limited; want %d hashes and the rest limited",
			attempts, hashes.Load(), limited, MaxConcurrentHashes)
	}
}

func TestLogout(t *testing.T) {
	s, clock, audit := newTestService(t)
	ctx := context.Background()
	u, secret := createActive(t, s, clock, "alice")
	res, err := s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: code(t, secret, clock), Source: "10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Logout(ctx, res.SessionID, "10.0.0.3"); err != nil {
		t.Fatal(err)
	}
	_, err = s.Authenticate(ctx, Credentials{Session: res.SessionID})
	wantCode(t, err, errcode.AuthRequired)
	entries, _, _ := audit.List(ctx, core.AuditQuery{})
	e := entries[0]
	if len(entries) != 2 || e.Actor != "user:"+u.ID || e.Action != AuditLogoutAction || e.Phase != "logout" ||
		e.Result != "ok" || e.Source != "10.0.0.3" || string(e.Input) != `{}` {
		t.Errorf("logout audit entry = %+v (of %d)", e, len(entries))
	}
	if err := s.Logout(ctx, res.SessionID, "10.0.0.3"); err != nil {
		t.Errorf("second logout = %v, want nil", err)
	}
	if again, _, _ := audit.List(ctx, core.AuditQuery{}); len(again) != 2 {
		t.Errorf("logging out an ended session was audited")
	}
}

func TestAuthenticateCredentials(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, secret := createActive(t, s, clock, "alice")
	res, err := s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: code(t, secret, clock), Source: "10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	info, token, err := s.CreateToken(ctx, u.ID, "ci", core.ScopeRead, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name    string
		c       Credentials
		session bool
		ok      bool
	}{
		{"session cookie", Credentials{Session: res.SessionID}, true, true},
		{"bearer token", Credentials{Authorization: []string{"Bearer " + token}}, false, true},
		{"lower-case scheme", Credentials{Authorization: []string{"bearer " + token}}, false, true},
		{"bearer wins over a cookie", Credentials{Authorization: []string{"Bearer " + token}, Session: res.SessionID}, false, true},
		{"invalid bearer never falls back to the cookie", Credentials{Authorization: []string{"Bearer dpt_x"}, Session: res.SessionID}, false, false},
		{"other scheme", Credentials{Authorization: []string{"Basic YWxpY2U6eA=="}, Session: res.SessionID}, false, false},
		{"empty header", Credentials{Authorization: []string{""}, Session: res.SessionID}, false, false},
		{"two headers", Credentials{Authorization: []string{"Bearer " + token, "Bearer " + token}}, false, false},
		{"present but no value", Credentials{Authorization: []string{}, Session: res.SessionID}, false, false},
		{"nothing", Credentials{}, false, false},
	} {
		tt.c.Source = "10.0.0.9"
		id, err := s.Authenticate(ctx, tt.c)
		if !tt.ok {
			if e := errcode.From(err); e.Code != errcode.AuthRequired {
				t.Errorf("%s: error = %v, want %s", tt.name, err, errcode.AuthRequired)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if id.FromSession != tt.session || id.Principal.Source != "10.0.0.9" {
			t.Errorf("%s: identity = %+v, want from session %v", tt.name, id, tt.session)
		}
		if !tt.session && id.Principal.TokenID != info.ID {
			t.Errorf("%s: principal %+v is not the token's", tt.name, id.Principal)
		}
	}
}

var (
	errSealerDown = errors.New("sealer is down")
	errAuditDown  = errors.New("audit log is down")
)

// openFails is a sealer whose Open always fails, so a login with the right
// password fails inside its write transaction.
type openFails struct{ coretest.Sealer }

func (openFails) Open(string, []byte) ([]byte, error) { return nil, errSealerDown }

// failingAudit fails its first fail Record calls, then records.
type failingAudit struct {
	*coretest.MemAudit
	fail int
}

func (a *failingAudit) Record(ctx context.Context, e core.AuditEntry) error {
	if a.fail > 0 {
		a.fail--
		return errAuditDown
	}
	return a.MemAudit.Record(ctx, e)
}

// failures is the number of failures the limiter holds for key.
func failures(s *Service, key string) int {
	s.limiter.mu.Lock()
	defer s.limiter.mu.Unlock()
	if c, ok := s.limiter.keys[key]; ok {
		return len(c.failures)
	}
	return 0
}

func TestLoginFaultAfterPasswordCounts(t *testing.T) {
	s, clock, audit := newTestService(t)
	ctx := context.Background()
	u, secret := createActive(t, s, clock, "alice")
	s.sealer = openFails{}
	req := LoginRequest{Username: "alice", Password: testPassword, Code: code(t, secret, clock), Source: "10.0.0.1"}
	for i := range UsernameFailureLimit {
		_, err := s.Login(ctx, req)
		if !errors.Is(err, errSealerDown) || errcode.From(err).Code != errcode.Internal {
			t.Fatalf("attempt %d: error = %v, want the sealer's error, answered internal", i, err)
		}
		// The last failure locks the username and clears its count.
		if n, want := failures(s, usernameKey("alice")), (i+1)%UsernameFailureLimit; n != want {
			t.Fatalf("attempt %d: %d failures counted for alice, want %d", i, n, want)
		}
		entries, _, _ := audit.List(ctx, core.AuditQuery{Limit: 1})
		e := entries[0]
		if len(entries) != 1 || e.Actor != "user:"+u.ID || e.Action != AuditLoginAction || e.Phase != "login" ||
			e.Result != "error" || e.ErrorCode != string(errcode.Internal) || e.Source != "10.0.0.1" ||
			string(e.Input) != `{"username":"alice"}` {
			t.Fatalf("attempt %d: audit entry = %+v, want a login error with code internal", i, e)
		}
	}
	_, err := s.Login(ctx, req)
	wantCode(t, err, errcode.RateLimited)

	_, err = s.Login(ctx, LoginRequest{Username: "bob", Password: testPassword, Code: "000000", Source: "10.0.0.2"})
	wantCode(t, err, errcode.InvalidCredentials)
	entries, _, _ := audit.List(ctx, core.AuditQuery{Limit: 1})
	if entries[0].ErrorCode != string(errcode.InvalidCredentials) {
		t.Errorf("an unknown user with a failing sealer was audited as %+v, want invalid credentials", entries[0])
	}
}

func TestLoginAuditFaultCounts(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, secret := createActive(t, s, clock, "alice")
	audit := &failingAudit{MemAudit: &coretest.MemAudit{}, fail: 1}
	s.audit = audit
	_, err := s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: code(t, secret, clock), Source: "10.0.0.1"})
	if !errors.Is(err, errAuditDown) || errcode.From(err).Code != errcode.Internal {
		t.Fatalf("error = %v, want the audit's error, answered internal", err)
	}
	if n := failures(s, usernameKey("alice")); n != 1 {
		t.Errorf("%d failures counted for alice, want 1", n)
	}
	entries, _, _ := audit.List(ctx, core.AuditQuery{})
	if len(entries) != 1 || entries[0].Actor != "user:"+u.ID || entries[0].Result != "error" ||
		entries[0].ErrorCode != string(errcode.Internal) {
		t.Errorf("audit = %+v, want one login error with code internal", entries)
	}

	clock.Advance(TOTPPeriod)
	s.sealer = openFails{}
	audit.fail = 1
	_, err = s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: code(t, secret, clock), Source: "10.0.0.1"})
	if !errors.Is(err, errSealerDown) || errors.Is(err, errAuditDown) {
		t.Errorf("error = %v, want the sealer's error alone when the audit row also fails", err)
	}
	if n := failures(s, usernameKey("alice")); n != 2 {
		t.Errorf("%d failures counted for alice, want 2", n)
	}
}
