package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/netip"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// TestIsManagedTailnetNodeAddressesOnly: a node is a Darkpanel host only by
// an address the controller observed, read through the caller's querier.
func TestIsManagedTailnetNodeAddressesOnly(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newTestService(t)
	for _, q := range []string{
		`INSERT INTO core_hosts (id, name, created_at) VALUES ('h1', 'web1', '2026-11-25T00:00:00Z')`,
		`INSERT INTO core_host_tailnet (host_id, addr, stable_id, seen_at) VALUES ('h1', '100.64.0.7', 'nHost', '2026-11-25T00:00:00Z')`,
	} {
		if _, err := s.db.Writer().ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := listen.SaveTailscaleAddrs(ctx, s.db, []netip.Addr{netip.MustParseAddr("100.64.0.1")}); err != nil {
		t.Fatal(err)
	}
	a := netip.MustParseAddr
	for _, tc := range []struct {
		name  string
		addrs []netip.Addr
		want  bool
	}{
		{"a recorded address", []netip.Addr{a("100.64.0.7")}, true},
		{"a recorded address, mapped", []netip.Addr{a("::ffff:100.64.0.7")}, true},
		{"another address of the node whose row names stable id nHost", []netip.Addr{a("100.64.0.9")}, false},
		{"the controller's own address", []netip.Addr{a("100.64.0.1")}, true},
		{"nothing to look up", nil, false},
	} {
		got, err := s.IsManagedTailnetNode(ctx, s.db.Read(), tc.addrs)
		if err != nil || got != tc.want {
			t.Errorf("%s: IsManagedTailnetNode = %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}

	// A row written inside an open write transaction is seen through that
	// transaction's querier.
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO core_host_tailnet (host_id, addr, stable_id, seen_at) VALUES ('h1', '100.64.0.20', '', '2026-11-25T00:00:00Z')`); err != nil {
			return err
		}
		got, err := s.IsManagedTailnetNode(ctx, tx, []netip.Addr{a("100.64.0.20")})
		if err != nil || !got {
			t.Errorf("a row of the open transaction: IsManagedTailnetNode = %v, %v; want true", got, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// tsEnv is a service with Tailscale sign-in on and helpers for its users.
type tsEnv struct {
	t       *testing.T
	s       *Service
	clock   *coretest.FakeClock
	audit   *coretest.MemAudit
	secrets map[string]string // username -> TOTP secret
}

func newTSEnv(t *testing.T, requireTOTP bool) *tsEnv {
	t.Helper()
	s, clock, audit := newTestService(t)
	e := &tsEnv{t: t, s: s, clock: clock, audit: audit, secrets: map[string]string{}}
	e.requireTOTP(requireTOTP)
	return e
}

func (e *tsEnv) requireTOTP(on bool) {
	e.t.Helper()
	exec(e.t, e.s, `INSERT INTO auth_settings (id, tailscale_enabled, tailscale_require_totp, updated_at) VALUES (1, 1, ?, '2026-11-25T00:00:00Z')
		ON CONFLICT (id) DO UPDATE SET tailscale_enabled = 1, tailscale_require_totp = excluded.tailscale_require_totp`, on)
}

// active adds an active user linked to login with totp_last_step step.
func (e *tsEnv) active(username, login string, step int64) User {
	e.t.Helper()
	u, secret := createActive(e.t, e.s, e.clock, username)
	e.secrets[username] = secret
	exec(e.t, e.s, `UPDATE core_users SET tailnet_login = ?, totp_last_step = ? WHERE id = ?`, login, step, u.ID)
	return u
}

// pending adds an invited user linked to login.
func (e *tsEnv) pending(username, login string) User {
	e.t.Helper()
	inv, err := e.s.Invite(context.Background(), InviteRequest{Username: username, Role: RoleMember, TailnetLogin: login})
	if err != nil {
		e.t.Fatal(err)
	}
	return inv.User
}

// remember inserts a device row for username's node stableID.
func (e *tsEnv) remember(username, stableID, nodeName string, at time.Time) {
	e.t.Helper()
	exec(e.t, e.s, `INSERT INTO core_user_tailnet_devices (user_id, stable_id, node_name, remembered_at, last_used_at)
		SELECT id, ?, ?, ?, ? FROM core_users WHERE username = ?`, stableID, nodeName, formatTime(at), formatTime(at), username)
}

// device reads username's row for stableID as "node_name remembered_at last_used_at", or "" when none.
func (e *tsEnv) device(username, stableID string) string {
	e.t.Helper()
	var out string
	err := e.s.db.Read().QueryRow(`SELECT d.node_name || ' ' || d.remembered_at || ' ' || d.last_used_at
		FROM core_user_tailnet_devices d JOIN core_users u ON u.id = d.user_id WHERE u.username = ? AND d.stable_id = ?`,
		username, stableID).Scan(&out)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *tsEnv) count(query string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.s.db.Read().QueryRow(query, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *tsEnv) sessions() int { return e.count(`SELECT count(*) FROM core_sessions`) }

// lastAudit is the input fields of the newest tailscale sign-in entry, with
// its result and code.
func (e *tsEnv) lastAudit() map[string]string {
	e.t.Helper()
	entries, _, err := e.audit.List(context.Background(), core.AuditQuery{Limit: 1000})
	if err != nil {
		e.t.Fatal(err)
	}
	for _, a := range entries {
		var in map[string]string
		if a.Action != AuditLoginAction || json.Unmarshal(a.Input, &in) != nil || in["method"] != MethodTailscale {
			continue
		}
		in["result"], in["code"] = a.Result, a.ErrorCode
		return in
	}
	e.t.Fatal("no tailscale sign-in was audited")
	return nil
}

func (e *tsEnv) ticket(sealed string) signInTicket {
	e.t.Helper()
	var t signInTicket
	if !e.s.openJSON(signInTicketAD, sealed, &t) {
		e.t.Fatalf("the ticket %q does not open", sealed)
	}
	return t
}

// miaWhois is the whois answer of mia's laptop.
func miaWhois() TailnetWhois {
	return TailnetWhois{LoginName: "mia@github", UserID: 42, NodeName: "laptop.tail1234.ts.net", StableID: "nL", Addresses: []string{"100.64.0.5"}}
}

func tsReq(w TailnetWhois) TailscaleRequest {
	return TailscaleRequest{Remote: netip.MustParseAddr("100.64.0.5"), Whois: w, Source: "100.64.0.5", UserAgent: "test"}
}

func TestContinueAsCodeReadyAsksTheCode(t *testing.T) {
	e := newTSEnv(t, false)
	mia := e.active("mia", "mia@github", 7)
	res, err := e.s.SignInTailscale(context.Background(), tsReq(miaWhois()))
	if err != nil || res.Ticket == "" || !res.Remember {
		t.Fatalf("SignInTailscale = ticket %q, remember %v, %v; want a ticket with remember", res.Ticket, res.Remember, err)
	}
	if n := e.sessions(); n != 0 {
		t.Errorf("%d sessions, want none before the code", n)
	}
	got := e.ticket(res.Ticket)
	want := signInTicket{ID: got.ID, UserID: mia.ID, Method: MethodTailscale, TailnetUserID: "42", Expires: got.Expires,
		StableID: "nL", NodeName: "laptop.tail1234.ts.net", Addrs: []string{"100.64.0.5"}, Login: "mia@github", WhoisUserID: "42"}
	if !slices.Equal(got.Addrs, want.Addrs) || got.StableID != want.StableID || got.NodeName != want.NodeName ||
		got.Login != want.Login || got.WhoisUserID != want.WhoisUserID || got.UserID != want.UserID || got.TailnetUserID != want.TailnetUserID {
		t.Errorf("ticket = %+v, want %+v", got, want)
	}
	if a := e.lastAudit(); a["refused"] != "" || a["step"] != "ticket" || a["result"] != "ok" {
		t.Errorf("audit = %v, want an ok ticket step with no refused cause", a)
	}
}

func TestContinueAsTicketAddresses(t *testing.T) {
	e := newTSEnv(t, false)
	e.active("mia", "mia@github", 7)
	w := miaWhois()
	w.Addresses = []string{"fd7a:115c:a1e0::5", "100.64.0.5", "::ffff:100.64.0.5"}
	r := tsReq(w)
	r.Remote = netip.MustParseAddr("::ffff:100.64.0.5")
	res, err := e.s.SignInTailscale(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := e.ticket(res.Ticket).Addrs, []string{"100.64.0.5", "fd7a:115c:a1e0::5"}; !slices.Equal(got, want) {
		t.Errorf("ticket addrs = %v, want %v: the unmapped remote first, then the whois addresses, once each", got, want)
	}
}

func TestContinueAsRememberedSignsIn(t *testing.T) {
	e := newTSEnv(t, false)
	e.active("mia", "mia@github", 7)
	t0 := e.clock.Now()
	e.remember("mia", "nL", "old-name", t0)
	e.clock.Advance(time.Hour)
	res, err := e.s.SignInTailscale(context.Background(), tsReq(miaWhois()))
	if err != nil || res.Ticket != "" || res.SessionID == "" {
		t.Fatalf("SignInTailscale = ticket %q, token %q, %v; want a session", res.Ticket, res.SessionID, err)
	}
	if got, want := e.device("mia", "nL"), "laptop.tail1234.ts.net "+formatTime(t0)+" "+formatTime(e.clock.Now()); got != want {
		t.Errorf("device row = %q, want %q", got, want)
	}
	if a := e.lastAudit(); a["device"] != "remembered" || a["result"] != "ok" || a["refused"] != "" {
		t.Errorf("audit = %v, want an ok sign-in with device remembered", a)
	}
}

func TestContinueAsNotCodeReadyRefused(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		setup  func(e *tsEnv)
		ticket bool
	}{
		{"an active user who never typed a code", func(e *tsEnv) { e.active("mia", "mia@github", 0) }, false},
		{"an invite OIDC activated", func(e *tsEnv) {
			u := e.pending("mia", "mia@github")
			exec(e.t, e.s, `UPDATE core_users SET state = 'active' WHERE id = ?`, u.ID)
		}, false},
		{"a TOTP reset, then a right code", func(e *tsEnv) {
			e.active("mia", "mia@github", 7)
			_, secret, err := e.s.ResetTOTP(ctx, "mia")
			if err != nil {
				e.t.Fatal(err)
			}
			e.clock.Advance(time.Minute)
			code, err := TOTPCode(secret, e.clock.Now())
			if err != nil {
				e.t.Fatal(err)
			}
			if _, err := e.s.ActivateUser(ctx, "mia", code); err != nil {
				e.t.Fatal(err)
			}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTSEnv(t, false)
			tc.setup(e)
			res, err := e.s.SignInTailscale(ctx, tsReq(miaWhois()))
			if tc.ticket {
				if err != nil || res.Ticket == "" {
					t.Fatalf("SignInTailscale = %+v, %v; want a ticket", res, err)
				}
				return
			}
			if errcode.From(err).Code != errcode.InvalidCredentials {
				t.Fatalf("SignInTailscale = %+v, %v; want auth.invalid_credentials", res, err)
			}
			if n := e.sessions(); n != 0 || e.device("mia", "nL") != "" {
				t.Errorf("%d sessions, device %q; want none", n, e.device("mia", "nL"))
			}
			if a := e.lastAudit(); a["refused"] != "device" || a["code"] != string(errcode.InvalidCredentials) {
				t.Errorf("audit = %v, want refused device", a)
			}
		})
	}
}

func TestPendingUserNeverSignsInByTailscale(t *testing.T) {
	ctx := context.Background()
	for _, requireTOTP := range []bool{false, true} {
		e := newTSEnv(t, requireTOTP)
		e.pending("mia", "mia@github")
		res, err := e.s.SignInTailscale(ctx, tsReq(miaWhois()))
		if errcode.From(err).Code != errcode.InvalidCredentials {
			t.Errorf("requireTotp %v: SignInTailscale = %+v, %v; want auth.invalid_credentials", requireTOTP, res, err)
		}
		if a := e.lastAudit(); a["refused"] != "device" {
			t.Errorf("requireTotp %v: audit = %v, want refused device", requireTOTP, a)
		}
		if n := e.count(`SELECT count(*) FROM core_users WHERE username = 'mia' AND state = 'pending'`); n != 1 || e.sessions() != 0 {
			t.Errorf("requireTotp %v: mia pending %d, %d sessions; want still pending and no session", requireTOTP, n, e.sessions())
		}
		m, err := e.s.TailscaleMatchFor(ctx, tsReq(miaWhois()))
		if err != nil || m.Username != "mia" || m.CodeReady || m.Remembered {
			t.Errorf("requireTotp %v: TailscaleMatchFor = %+v, %v; want mia, not code-ready, not remembered", requireTOTP, m, err)
		}
	}
}

func TestTailscaleMatchAnswersDeviceState(t *testing.T) {
	ctx := context.Background()
	e := newTSEnv(t, false)
	e.active("mia", "mia@github", 0)
	if m, err := e.s.TailscaleMatchFor(ctx, tsReq(miaWhois())); err != nil || m.CodeReady || m.Remembered {
		t.Errorf("never typed a code: %+v, %v; want neither", m, err)
	}
	exec(t, e.s, `UPDATE core_users SET totp_last_step = 3 WHERE username = 'mia'`)
	if m, err := e.s.TailscaleMatchFor(ctx, tsReq(miaWhois())); err != nil || !m.CodeReady || m.Remembered {
		t.Errorf("code-ready: %+v, %v; want code-ready, not remembered", m, err)
	}
	e.remember("mia", "nL", "laptop", e.clock.Now())
	if m, err := e.s.TailscaleMatchFor(ctx, tsReq(miaWhois())); err != nil || !m.CodeReady || !m.Remembered {
		t.Errorf("remembered: %+v, %v; want both", m, err)
	}
}

func TestRememberIsPerUser(t *testing.T) {
	e := newTSEnv(t, false)
	e.active("mia", "mia@github", 7)
	e.active("ben", "ben@github", 7)
	e.remember("mia", "nL", "laptop", e.clock.Now())
	w := miaWhois()
	w.LoginName, w.UserID = "ben@github", 43
	res, err := e.s.SignInTailscale(context.Background(), tsReq(w))
	if err != nil || res.Ticket == "" || res.SessionID != "" {
		t.Fatalf("ben on mia's remembered node: %+v, %v; want a ticket, no session", res, err)
	}
}

func TestRequireTotpAlwaysAsks(t *testing.T) {
	e := newTSEnv(t, true)
	e.active("mia", "mia@github", 7)
	e.remember("mia", "nL", "laptop", e.clock.Now())
	res, err := e.s.SignInTailscale(context.Background(), tsReq(miaWhois()))
	if err != nil || res.Ticket == "" || res.Remember || e.sessions() != 0 {
		t.Fatalf("SignInTailscale = %+v, %v; want a ticket without remember and no session", res, err)
	}
}

func TestRememberedSignInRacingForget(t *testing.T) {
	ctx := context.Background()
	e := newTSEnv(t, false)
	e.active("mia", "mia@github", 7)
	for i := range 50 {
		e.remember("mia", "nL", "laptop", e.clock.Now())
		var wg sync.WaitGroup
		start := make(chan struct{})
		var res SignInResult
		var signErr, forgetErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			res, signErr = e.s.SignInTailscale(ctx, tsReq(miaWhois()))
		}()
		go func() {
			defer wg.Done()
			<-start
			forgetErr = e.s.db.Write(ctx, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `DELETE FROM core_user_tailnet_devices WHERE stable_id = 'nL'`)
				return err
			})
		}()
		close(start)
		wg.Wait()
		if signErr != nil || forgetErr != nil || (res.SessionID == "") == (res.Ticket == "") {
			t.Fatalf("iteration %d: sign-in %+v, %v; forget %v; want a session or a ticket", i, res, signErr, forgetErr)
		}
		if got := e.device("mia", "nL"); got != "" {
			t.Fatalf("iteration %d: the forgotten row is back: %q", i, got)
		}
	}
}

func TestRefusalCauses(t *testing.T) {
	ctx := context.Background()
	host := func(e *tsEnv, addr string) {
		exec(e.t, e.s, `INSERT OR IGNORE INTO core_hosts (id, name, created_at) VALUES ('h1', 'web1', '2026-11-25T00:00:00Z')`)
		exec(e.t, e.s, `INSERT INTO core_host_tailnet (host_id, addr, stable_id, seen_at) VALUES ('h1', ?, '', '2026-11-25T00:00:00Z')`, addr)
	}
	for _, tc := range []struct {
		name  string
		setup func(e *tsEnv, w *TailnetWhois)
		code  errcode.Code
		cause string
	}{
		{"a host address", func(e *tsEnv, _ *TailnetWhois) { host(e, "100.64.0.5") }, errcode.InvalidCredentials, "host"},
		{"a remembered node a host session was then recorded from", func(e *tsEnv, _ *TailnetWhois) {
			e.remember("mia", "nL", "laptop", e.clock.Now())
			host(e, "100.64.0.5")
		}, errcode.InvalidCredentials, "host"},
		{"a tagged node", func(_ *tsEnv, w *TailnetWhois) { w.Tags = []string{"tag:server"} }, errcode.InvalidCredentials, "node"},
		{"no linked user", func(e *tsEnv, _ *TailnetWhois) { exec(e.t, e.s, `UPDATE core_users SET tailnet_login = NULL`) },
			errcode.NoLinkedUser, "link"},
		{"a pin mismatch", func(e *tsEnv, _ *TailnetWhois) { exec(e.t, e.s, `UPDATE core_users SET tailnet_user_id = '99'`) },
			errcode.InvalidCredentials, "link"},
		{"a disabled user", func(e *tsEnv, _ *TailnetWhois) {
			exec(e.t, e.s, `UPDATE core_users SET disabled_at = '2026-11-25T00:00:00Z' WHERE username = 'mia'`)
		}, errcode.InvalidCredentials, "user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTSEnv(t, false)
			e.active("mia", "mia@github", 7)
			w := miaWhois()
			tc.setup(e, &w)
			if _, err := e.s.TailscaleMatchFor(ctx, tsReq(w)); errcode.From(err).Code != tc.code {
				t.Errorf("TailscaleMatchFor: %v, want %s", err, tc.code)
			}
			res, err := e.s.SignInTailscale(ctx, tsReq(w))
			if errcode.From(err).Code != tc.code || res.SessionID != "" || res.Ticket != "" {
				t.Errorf("SignInTailscale = %+v, %v; want %s", res, err, tc.code)
			}
			if a := e.lastAudit(); a["refused"] != tc.cause || a["code"] != string(tc.code) {
				t.Errorf("audit = %v, want refused %s with %s", a, tc.cause, tc.code)
			}
		})
	}
}

// code is username's right TOTP code now.
func (e *tsEnv) code(username string) string {
	e.t.Helper()
	c, err := TOTPCode(e.secrets[username], e.clock.Now())
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

// budgetLeft asserts mia has n failed attempts left from her laptop's
// address before the limiter locks her out: n wrong passwords are each
// judged, and the next attempt is rate limited. The code step and the
// password login count against the same budget. It leaves mia locked, so it
// runs last.
func (e *tsEnv) budgetLeft(n int) {
	e.t.Helper()
	try := func() errcode.Code {
		_, err := e.s.Login(context.Background(), LoginRequest{Username: "mia", Password: "not her password", Source: "100.64.0.5", UserAgent: "test"})
		return errcode.From(err).Code
	}
	for i := range n {
		if c := try(); c != errcode.InvalidCredentials {
			e.t.Errorf("wrong password %d of %d = %s, want auth.invalid_credentials: the budget was spent before", i+1, n, c)
			return
		}
	}
	if c := try(); c != errcode.RateLimited {
		e.t.Errorf("attempt %d = %s, want auth.rate_limited: more than %d attempts were left", n+1, c, n)
	}
}

// miaTicket is a Continue-as ticket for code-ready mia on her laptop.
func (e *tsEnv) miaTicket() string {
	e.t.Helper()
	res, err := e.s.SignInTailscale(context.Background(), tsReq(miaWhois()))
	if err != nil || res.Ticket == "" {
		e.t.Fatalf("SignInTailscale = %+v, %v; want a ticket", res, err)
	}
	return res.Ticket
}

func stepReq(ticket, code, remote string) TOTPStepRequest {
	r := TOTPStepRequest{Ticket: ticket, Code: code, Source: "100.64.0.5", UserAgent: "test"}
	if remote != "" {
		r.Remote = netip.MustParseAddr(remote)
	}
	return r
}

func (e *tsEnv) pinned(username string) string {
	e.t.Helper()
	var v sql.NullString
	if err := e.s.db.Read().QueryRow(`SELECT tailnet_user_id FROM core_users WHERE username = ?`, username).Scan(&v); err != nil {
		e.t.Fatal(err)
	}
	return v.String
}

func TestCodeStepRemembersTheNode(t *testing.T) {
	ctx := context.Background()
	e := newTSEnv(t, false)
	e.active("mia", "mia@github", 7)
	t1 := e.clock.Now()
	res, err := e.s.FinishTOTPStep(ctx, stepReq(e.miaTicket(), e.code("mia"), "100.64.0.5"))
	if err != nil || res.SessionID == "" {
		t.Fatalf("FinishTOTPStep = %+v, %v; want a session", res, err)
	}
	if got, want := e.device("mia", "nL"), "laptop.tail1234.ts.net "+formatTime(t1)+" "+formatTime(t1); got != want {
		t.Errorf("device row = %q, want %q", got, want)
	}
	if got := e.pinned("mia"); got != "42" {
		t.Errorf("tailnet_user_id = %q, want 42", got)
	}
	if a := e.lastAudit(); a["device"] != "new" || a["result"] != "ok" {
		t.Errorf("audit = %v, want an ok code step with device new", a)
	}

	// requireTotp on: each Continue-as asks the code, and a right code
	// refreshes the row.
	e.requireTOTP(true)
	e.clock.Advance(time.Minute)
	w := miaWhois()
	w.NodeName = "renamed.tail1234.ts.net"
	got, err := e.s.SignInTailscale(ctx, tsReq(w))
	if err != nil || got.Ticket == "" {
		t.Fatalf("SignInTailscale = %+v, %v; want a ticket", got, err)
	}
	if _, err := e.s.FinishTOTPStep(ctx, stepReq(got.Ticket, e.code("mia"), "100.64.0.5")); err != nil {
		t.Fatal(err)
	}
	if got, want := e.device("mia", "nL"), "renamed.tail1234.ts.net "+formatTime(t1)+" "+formatTime(e.clock.Now()); got != want {
		t.Errorf("device row after a second code = %q, want %q", got, want)
	}
	if a := e.lastAudit(); a["device"] != "remembered" {
		t.Errorf("audit = %v, want device remembered", a)
	}
}

func TestCodeStepFailuresRememberNothing(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		step func(e *tsEnv, ticket string)
	}{
		{"a wrong code", func(e *tsEnv, ticket string) {
			if _, err := e.s.FinishTOTPStep(ctx, stepReq(ticket, "000000", "100.64.0.5")); err == nil {
				e.t.Error("a wrong code signed in")
			}
		}},
		{"an expired ticket", func(e *tsEnv, ticket string) {
			e.clock.Advance(SignInTicketTTL + time.Second)
			if _, err := e.s.FinishTOTPStep(ctx, stepReq(ticket, e.code("mia"), "100.64.0.5")); err == nil {
				e.t.Error("an expired ticket signed in")
			}
		}},
		{"Back, no step at all", func(*tsEnv, string) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTSEnv(t, false)
			e.active("mia", "mia@github", 7)
			tc.step(e, e.miaTicket())
			if got := e.device("mia", "nL"); got != "" {
				t.Errorf("device row %q, want none", got)
			}
			res, err := e.s.SignInTailscale(ctx, tsReq(miaWhois()))
			if err != nil || res.Ticket == "" {
				t.Errorf("the next Continue-as = %+v, %v; want a ticket again", res, err)
			}
		})
	}
}

// checkRefusedStep asserts a right code on ticket from remote is refused as
// cause, with nothing pinned or remembered; the caller checks the budget
// last, with budgetLeft.
func (e *tsEnv) checkRefusedStep(ticket, remote, cause string) {
	e.t.Helper()
	res, err := e.s.FinishTOTPStep(context.Background(), stepReq(ticket, e.code("mia"), remote))
	if errcode.From(err).Code != errcode.InvalidCredentials || res.SessionID != "" {
		e.t.Errorf("FinishTOTPStep from %q = %+v, %v; want auth.invalid_credentials", remote, res, err)
	}
	if a := e.lastAudit(); a["refused"] != cause {
		e.t.Errorf("audit = %v, want refused %s", a, cause)
	}
	if e.sessions() != 0 || e.pinned("mia") != "" || e.device("mia", "nL") != "" {
		e.t.Errorf("after the refusal: %d sessions, pin %q, device %q; want none", e.sessions(), e.pinned("mia"), e.device("mia", "nL"))
	}
}

func TestCodeStepFromAnotherNode(t *testing.T) {
	for _, remote := range []string{"100.64.0.6", ""} {
		t.Run("remote "+remote, func(t *testing.T) {
			e := newTSEnv(t, false)
			e.active("mia", "mia@github", 7)
			ticket := e.miaTicket()
			e.checkRefusedStep(ticket, remote, "node")
			if res, err := e.s.FinishTOTPStep(context.Background(), stepReq(ticket, e.code("mia"), "100.64.0.5")); err != nil || res.SessionID == "" {
				t.Errorf("the same ticket from its node = %+v, %v; want a session", res, err)
			}
			e.budgetLeft(UsernameFailureLimit)
		})
	}
}

func TestCodeStepAfterLinkChange(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(e *tsEnv)
		pin    string // the pin the change leaves, which the refusal keeps
	}{
		{"the tailnet login changed", func(e *tsEnv) {
			if _, err := e.s.SetIdentity(context.Background(), "mia", "", "other@github"); err != nil {
				e.t.Fatal(err)
			}
		}, ""},
		{"the pinned tailnet user id is another", func(e *tsEnv) {
			exec(e.t, e.s, `UPDATE core_users SET tailnet_user_id = '43' WHERE username = 'mia'`)
		}, "43"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTSEnv(t, false)
			e.active("mia", "mia@github", 7)
			ticket := e.miaTicket()
			tc.change(e)
			res, err := e.s.FinishTOTPStep(context.Background(), stepReq(ticket, e.code("mia"), "100.64.0.5"))
			if errcode.From(err).Code != errcode.InvalidCredentials || res.SessionID != "" {
				t.Errorf("FinishTOTPStep = %+v, %v; want auth.invalid_credentials", res, err)
			}
			if a := e.lastAudit(); a["refused"] != "link" {
				t.Errorf("audit = %v, want refused link", a)
			}
			if pin := e.pinned("mia"); e.device("mia", "nL") != "" || pin != tc.pin {
				t.Errorf("device %q, pin %q; want nothing remembered and pin %q", e.device("mia", "nL"), pin, tc.pin)
			}
			e.budgetLeft(UsernameFailureLimit)
		})
	}
}

// TestTwoFirstTicketsBothSignIn: an unpinned user starts Continue-as on two
// nodes before finishing either; the second right code finds the pin the
// first one set and still signs in.
func TestTwoFirstTicketsBothSignIn(t *testing.T) {
	ctx := context.Background()
	e := newTSEnv(t, false)
	e.active("mia", "mia@github", 7)
	laptop := e.miaTicket()
	desk := miaWhois()
	desk.NodeName, desk.StableID, desk.Addresses = "desk.tail1234.ts.net", "nD", []string{"100.64.0.9"}
	req := tsReq(desk)
	req.Remote = netip.MustParseAddr("100.64.0.9")
	res, err := e.s.SignInTailscale(ctx, req)
	if err != nil || res.Ticket == "" {
		t.Fatalf("SignInTailscale from the desk = %+v, %v; want a ticket", res, err)
	}
	if got, err := e.s.FinishTOTPStep(ctx, stepReq(laptop, e.code("mia"), "100.64.0.5")); err != nil || got.SessionID == "" {
		t.Fatalf("the laptop's code step = %+v, %v; want a session", got, err)
	}
	e.clock.Advance(30 * time.Second)
	if got, err := e.s.FinishTOTPStep(ctx, stepReq(res.Ticket, e.code("mia"), "100.64.0.9")); err != nil || got.SessionID == "" {
		t.Fatalf("the desk's code step after the laptop pinned mia = %+v, %v; want a session", got, err)
	}
	if e.pinned("mia") != "42" || e.device("mia", "nL") == "" || e.device("mia", "nD") == "" {
		t.Errorf("pin %q, laptop %q, desk %q; want 42 and both nodes remembered", e.pinned("mia"), e.device("mia", "nL"), e.device("mia", "nD"))
	}
}

func TestCodeStepAfterHostRecorded(t *testing.T) {
	ctx := context.Background()
	e := newTSEnv(t, false)
	e.active("mia", "mia@github", 7)
	ticket := e.miaTicket()
	exec(t, e.s, `INSERT INTO core_hosts (id, name, created_at) VALUES ('h1', 'web1', '2026-11-25T00:00:00Z')`)
	exec(t, e.s, `INSERT INTO core_host_tailnet (host_id, addr, stable_id, seen_at) VALUES ('h1', '100.64.0.5', '', '2026-11-25T00:00:00Z')`)
	e.checkRefusedStep(ticket, "100.64.0.5", "host")

	exec(t, e.s, `DELETE FROM core_host_tailnet`)
	if _, err := e.s.FinishTOTPStep(ctx, stepReq(e.miaTicket(), "000000", "100.64.0.5")); errcode.From(err).Code != errcode.InvalidCredentials {
		t.Fatalf("a wrong code: %v, want auth.invalid_credentials", err)
	}
	e.budgetLeft(UsernameFailureLimit - 1)
}

func TestListTailnetDevicesOrder(t *testing.T) {
	e := newTSEnv(t, false)
	e.active("mia", "mia@github", 7)
	e.active("ben", "ben@github", 7)
	t2 := e.clock.Now().Add(2 * time.Hour)
	t3 := t2.Add(time.Hour)
	e.remember("mia", "nB", "b-node", t2)
	e.remember("mia", "nA", "a-node", t3)
	e.remember("mia", "nA2", "a2-node", t2)
	e.remember("ben", "nX", "ben-node", t3.Add(time.Hour))
	got, err := e.s.ListTailnetDevices(context.Background(), "mia")
	want := []TailnetDevice{
		{StableID: "nA", NodeName: "a-node", RememberedAt: t3, LastUsedAt: t3},
		{StableID: "nA2", NodeName: "a2-node", RememberedAt: t2, LastUsedAt: t2},
		{StableID: "nB", NodeName: "b-node", RememberedAt: t2, LastUsedAt: t2},
	}
	if err != nil || len(got) != len(want) {
		t.Fatalf("ListTailnetDevices(mia) = %+v, %v; want %+v", got, err, want)
	}
	for i := range want {
		if got[i].StableID != want[i].StableID || got[i].NodeName != want[i].NodeName ||
			!got[i].RememberedAt.Equal(want[i].RememberedAt) || !got[i].LastUsedAt.Equal(want[i].LastUsedAt) {
			t.Errorf("device %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	e.active("ann", "ann@github", 7)
	if none, err := e.s.ListTailnetDevices(context.Background(), "ann"); err != nil || none == nil || len(none) != 0 {
		t.Errorf("ListTailnetDevices(ann) = %#v, %v; want an empty, non-nil list", none, err)
	}
}

func TestForgetTailnetDevice(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		stableID string
		wantErr  errcode.Code
		check    func(e *tsEnv, d TailnetDevice)
	}{
		{name: "a row with an empty node name", stableID: "nE", check: func(e *tsEnv, d TailnetDevice) {
			if d.StableID != "nE" || d.NodeName != "" || !d.LastUsedAt.Equal(e.clock.Now().UTC()) {
				e.t.Errorf("forgot %+v, want nE with an empty node name", d)
			}
			if ok, err := e.s.TailnetDeviceExists(ctx, "mia", "nE"); err != nil || ok || e.device("mia", "nE") != "" {
				e.t.Errorf("TailnetDeviceExists(mia, nE) after forgetting = %v, %v; want the row gone", ok, err)
			}
		}},
		{name: "a missing stable id", stableID: "nZ", wantErr: errcode.NotFound, check: func(e *tsEnv, _ TailnetDevice) {
			for _, id := range []string{"nE", "nL"} {
				if ok, err := e.s.TailnetDeviceExists(ctx, "mia", id); err != nil || !ok {
					e.t.Errorf("TailnetDeviceExists(mia, %s) = %v, %v; want mia's row kept", id, ok, err)
				}
			}
		}},
		{name: "another user's device", stableID: "nB", wantErr: errcode.NotFound, check: func(e *tsEnv, _ TailnetDevice) {
			if ok, err := e.s.TailnetDeviceExists(ctx, "ben", "nB"); err != nil || !ok {
				e.t.Errorf("TailnetDeviceExists(ben, nB) = %v, %v; want ben's row kept", ok, err)
			}
		}},
		{name: "the next Continue-as asks the code", stableID: "nL", check: func(e *tsEnv, _ TailnetDevice) {
			res, err := e.s.SignInTailscale(ctx, tsReq(miaWhois()))
			if err != nil || res.Ticket == "" || !res.Remember || e.sessions() != 0 {
				e.t.Errorf("SignInTailscale after forgetting = ticket %q, remember %v, %v, %d sessions; want a ticket with remember",
					res.Ticket, res.Remember, err, e.sessions())
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTSEnv(t, false)
			e.active("mia", "mia@github", 1)
			e.active("ben", "ben@github", 1)
			e.remember("mia", "nE", "", e.clock.Now())
			e.remember("mia", "nL", "laptop.tail1234.ts.net", e.clock.Now())
			e.remember("ben", "nB", "ben-node", e.clock.Now())
			d, err := e.s.ForgetTailnetDevice(ctx, "mia", tc.stableID)
			if (tc.wantErr == "" && err != nil) || (tc.wantErr != "" && errcode.From(err).Code != tc.wantErr) {
				t.Fatalf("ForgetTailnetDevice(mia, %s) = %v, want %q", tc.stableID, err, tc.wantErr)
			}
			if tc.check != nil {
				tc.check(e, d)
			}
		})
	}
}

func TestForgetEvents(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		kept bool
		// act forgets, or keeps, mia's devices; invited mia holds an
		// enrollment link instead of an active account.
		act     func(e *tsEnv, invite string) error
		invited bool
	}{
		{name: "reset TOTP", act: func(e *tsEnv, _ string) error {
			_, _, err := e.s.ResetTOTP(ctx, "mia")
			return err
		}},
		{name: "begin an enrollment", invited: true, act: func(e *tsEnv, token string) error {
			_, err := e.s.BeginEnrollment(ctx, token, "192.0.2.8", false)
			return err
		}},
		{name: "change the tailnet login", act: func(e *tsEnv, _ string) error {
			_, err := e.s.SetIdentity(ctx, "mia", "", "mia2@github")
			return err
		}},
		{name: "the same login in another case and an email", kept: true, act: func(e *tsEnv, _ string) error {
			_, err := e.s.SetIdentity(ctx, "mia", "mia@example.com", "MIA@github")
			return err
		}},
		{name: "delete the user", act: func(e *tsEnv, _ string) error {
			return e.s.DeleteUser(ctx, "mia")
		}},
		{name: "disable then enable", kept: true, act: func(e *tsEnv, _ string) error {
			if _, err := e.s.SetDisabled(ctx, "mia", true); err != nil {
				return err
			}
			_, err := e.s.SetDisabled(ctx, "mia", false)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTSEnv(t, false)
			e.active("ben", "ben@github", 1)
			var token string
			if tc.invited {
				inv, err := e.s.Invite(ctx, InviteRequest{Username: "mia", Role: RoleMember, TailnetLogin: "mia@github"})
				if err != nil {
					t.Fatal(err)
				}
				token = inv.Token
			} else {
				e.active("mia", "mia@github", 1)
			}
			e.remember("mia", "nL", "laptop", e.clock.Now())
			e.remember("mia", "nM", "phone", e.clock.Now())
			e.remember("ben", "nB", "ben-node", e.clock.Now())
			if err := tc.act(e, token); err != nil {
				t.Fatal(err)
			}
			want := 0
			if tc.kept {
				want = 2
			}
			if n := e.count(`SELECT count(*) FROM core_user_tailnet_devices WHERE stable_id IN ('nL', 'nM')`); n != want {
				t.Errorf("%d of mia's devices left, want %d", n, want)
			}
			if e.device("ben", "nB") == "" {
				t.Error("ben's device was forgotten")
			}
		})
	}
}

func TestForgetEndsNoSession(t *testing.T) {
	ctx := context.Background()
	e := newTSEnv(t, false)
	e.active("mia", "mia@github", 1)
	e.remember("mia", "nL", "laptop", e.clock.Now())
	res, err := e.s.SignInTailscale(ctx, tsReq(miaWhois()))
	if err != nil || res.SessionID == "" {
		t.Fatalf("SignInTailscale = %+v, %v; want a session", res, err)
	}
	if _, err := e.s.ForgetTailnetDevice(ctx, "mia", "nL"); err != nil {
		t.Fatal(err)
	}
	if id, err := e.s.Authenticate(ctx, Credentials{Session: res.SessionID, Source: "100.64.0.5"}); err != nil || id.User.Username != "mia" {
		t.Errorf("Authenticate after forgetting = %+v, %v; want mia's session still valid", id.User, err)
	}
}
