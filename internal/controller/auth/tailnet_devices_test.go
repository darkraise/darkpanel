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
	t     *testing.T
	s     *Service
	clock *coretest.FakeClock
	audit *coretest.MemAudit
}

func newTSEnv(t *testing.T, requireTOTP bool) *tsEnv {
	t.Helper()
	s, clock, audit := newTestService(t)
	e := &tsEnv{t: t, s: s, clock: clock, audit: audit}
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
	u, _ := createActive(e.t, e.s, e.clock, username)
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
		{"an invite OIDC activated, then a password reset", func(e *tsEnv) {
			u := e.pending("mia", "mia@github")
			exec(e.t, e.s, `UPDATE core_users SET state = 'active' WHERE id = ?`, u.ID)
			if _, err := e.s.ResetPassword(ctx, "mia", "another horse battery"); err != nil {
				e.t.Fatal(err)
			}
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
		match bool // TailscaleMatchFor is refused with the same code
	}{
		{"a host address", func(e *tsEnv, _ *TailnetWhois) { host(e, "100.64.0.5") }, errcode.InvalidCredentials, "host", true},
		{"a remembered node a host session was then recorded from", func(e *tsEnv, _ *TailnetWhois) {
			e.remember("mia", "nL", "laptop", e.clock.Now())
			host(e, "100.64.0.5")
		}, errcode.InvalidCredentials, "host", true},
		{"a tagged node", func(_ *tsEnv, w *TailnetWhois) { w.Tags = []string{"tag:server"} }, errcode.InvalidCredentials, "node", true},
		{"no linked user", func(e *tsEnv, _ *TailnetWhois) { exec(e.t, e.s, `UPDATE core_users SET tailnet_login = NULL`) },
			errcode.NoLinkedUser, "link", true},
		{"a pin mismatch", func(e *tsEnv, _ *TailnetWhois) { exec(e.t, e.s, `UPDATE core_users SET tailnet_user_id = '99'`) },
			errcode.InvalidCredentials, "link", true},
		{"a disabled user", func(e *tsEnv, _ *TailnetWhois) {
			exec(e.t, e.s, `UPDATE core_users SET disabled_at = '2026-11-25T00:00:00Z' WHERE username = 'mia'`)
		}, errcode.InvalidCredentials, "user", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTSEnv(t, false)
			e.active("mia", "mia@github", 7)
			w := miaWhois()
			tc.setup(e, &w)
			if _, err := e.s.TailscaleMatchFor(ctx, tsReq(w)); tc.match && errcode.From(err).Code != tc.code {
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
