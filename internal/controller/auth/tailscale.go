package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// MethodTailscale is the sign-in method of Tailscale identity (M10 spec
// section 3).
const MethodTailscale = "tailscale"

// taggedDevices is the login name of the pseudo-user that owns tagged nodes.
const taggedDevices = "tagged-devices"

// TailnetWhois is the whois answer for a request's tailnet address: the
// output of tailscale.whois.get.
type TailnetWhois struct {
	LoginName string   `json:"loginName"`
	UserID    int64    `json:"userId"`
	NodeName  string   `json:"nodeName"`
	StableID  string   `json:"stableId"`
	Tags      []string `json:"tags"`
	Sharer    int64    `json:"sharer"`
	Addresses []string `json:"addresses"`
}

// TailscaleRequest is a Tailscale identity sign-in: the request's address
// and the whois answer for it.
type TailscaleRequest struct {
	Remote            netip.Addr
	Whois             TailnetWhois
	Source, UserAgent string
	Trusted           bool
}

// TailscaleMatch is the user a Tailscale node may continue as.
type TailscaleMatch struct {
	Username  string `json:"username"`
	LoginName string `json:"loginName"`
	NodeName  string `json:"nodeName"`
	// Remembered reports that the user has a device row for this node; it
	// signs in without a code only while requireTotp is off.
	Remembered bool `json:"remembered"`
	// CodeReady reports that the user has typed an authenticator code
	// before, so the code step can remember the node.
	CodeReady bool `json:"codeReady"`
}

// Refusal causes: the audit row's refused field says which check refused a
// Tailscale sign-in; the caller sees only the error's code (spec section 3).
const (
	refusedHost   = "host"
	refusedNode   = "node"
	refusedDevice = "device"
	refusedLink   = "link"
	refusedUser   = "user"
)

// refusal is a sign-in refusal and the check that made it.
type refusal struct {
	cause string
	err   error
}

func (r *refusal) Error() string { return r.err.Error() }
func (r *refusal) Unwrap() error { return r.err }

func refuse(cause string, err error) error { return &refusal{cause: cause, err: err} }

// refusedCause is the cause err carries, or "" for any other error.
func refusedCause(err error) string {
	var r *refusal
	if errors.As(err, &r) {
		return r.cause
	}
	return ""
}

// checkTailnetNode refuses every whois answer but a node owned by a user
// of this tailnet, and any node that is a Darkpanel host or the controller.
func (s *Service) checkTailnetNode(ctx context.Context, q querier, remote netip.Addr, w TailnetWhois) error {
	// A whois answer with no node has no stable id.
	if w.StableID == "" || len(w.Tags) > 0 || w.Sharer != 0 || w.UserID == 0 || w.LoginName == "" || strings.EqualFold(w.LoginName, taggedDevices) {
		return refuse(refusedNode, errSignInRefused())
	}
	addrs, err := nodeAddrs(remote, w)
	if err != nil {
		return err
	}
	managed, err := s.IsManagedTailnetNode(ctx, q, addrs)
	if err != nil {
		return err
	}
	if managed {
		return refuse(refusedHost, errSignInRefused())
	}
	return nil
}

// nodeAddrs is the node's unmapped addresses: the request's first, then the
// whois answer's, each once. An address that does not parse refuses.
func nodeAddrs(remote netip.Addr, w TailnetWhois) ([]netip.Addr, error) {
	addrs := []netip.Addr{remote.Unmap()}
	for _, a := range w.Addresses {
		addr, err := netip.ParseAddr(a)
		if err != nil {
			return nil, refuse(refusedNode, errSignInRefused())
		}
		if addr = addr.Unmap(); !slices.Contains(addrs, addr) {
			addrs = append(addrs, addr)
		}
	}
	return addrs, nil
}

// tailnetUser is the active user linked to w's login name; pin reports
// that its tailnet user id is not stored yet, and codeReady that the user
// has typed an authenticator code before (totp_last_step > 0). Every other
// state is refused; a pending user's refusal still carries its record.
func tailnetUser(ctx context.Context, q querier, w TailnetWhois) (rec userRecord, pin, codeReady bool, err error) {
	rec, err = loadUserBy(ctx, q, `tailnet_login = ? COLLATE NOCASE`, w.LoginName)
	if errcode.From(err).Code == errcode.NotFound {
		return rec, false, false, refuse(refusedLink, errNoLinkedUser())
	}
	if err != nil {
		return rec, false, false, err
	}
	var pinned sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT tailnet_user_id FROM core_users WHERE id = ?`, rec.ID).Scan(&pinned); err != nil {
		return rec, false, false, fmt.Errorf("auth: read the tailnet link: %w", err)
	}
	switch {
	case pinned.Valid && pinned.String != strconv.FormatInt(w.UserID, 10):
		return rec, false, false, refuse(refusedLink, errSignInRefused())
	case rec.Disabled:
		return rec, false, false, refuse(refusedUser, errSignInRefused())
	case rec.State == StatePending:
		// Tailscale identity never activates an invite (spec section 2).
		return rec, false, false, refuse(refusedDevice, errSignInRefused())
	case rec.State != StateActive:
		return rec, false, false, refuse(refusedUser, errSignInRefused())
	}
	return rec, !pinned.Valid, rec.lastStep > 0, nil
}

// checkTicketNode refuses a Tailscale ticket's code step unless it comes
// from one of the ticket's node addresses (node), the user is still linked
// to the ticket's login and tailnet user (link), and none of the node's
// addresses has become a Darkpanel host's (host).
func (s *Service) checkTicketNode(ctx context.Context, tx *sql.Tx, t signInTicket, remote netip.Addr) error {
	var addrs []netip.Addr
	for _, a := range t.Addrs {
		addr, err := netip.ParseAddr(a)
		if err != nil {
			return refuse(refusedNode, errSignInRefused())
		}
		addrs = append(addrs, addr)
	}
	if !remote.IsValid() || !slices.Contains(addrs, remote.Unmap()) {
		return refuse(refusedNode, errSignInRefused())
	}
	var login, pinned sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT tailnet_login, tailnet_user_id FROM core_users WHERE id = ?`, t.UserID).
		Scan(&login, &pinned); err != nil {
		return fmt.Errorf("auth: read the tailnet link: %w", err)
	}
	if !login.Valid || !strings.EqualFold(login.String, t.Login) || pinned.Valid && pinned.String != t.WhoisUserID {
		return refuse(refusedLink, errSignInRefused())
	}
	managed, err := s.IsManagedTailnetNode(ctx, tx, addrs)
	if err != nil {
		return err
	}
	if managed {
		return refuse(refusedHost, errSignInRefused())
	}
	return nil
}

// remembered reports whether userID's device row for stableID exists.
func remembered(ctx context.Context, q querier, userID, stableID string) (bool, error) {
	var n int
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM core_user_tailnet_devices WHERE user_id = ? AND stable_id = ?`,
		userID, stableID).Scan(&n); err != nil {
		return false, fmt.Errorf("auth: read the remembered devices: %w", err)
	}
	return n > 0, nil
}

// tailscaleOn loads the method's settings and refuses while it is off.
func tailscaleOn(ctx context.Context, q querier) (TailscaleSettings, error) {
	st, err := loadSettings(ctx, q)
	if err != nil {
		return TailscaleSettings{}, err
	}
	if !st.Tailscale.Enabled {
		return TailscaleSettings{}, errMethodOff()
	}
	return st.Tailscale, nil
}

// TailscaleMatchFor answers which user the node of r may continue as, and
// whether that sign-in needs no code (Remembered) or can ask one
// (CodeReady). A pending user is answered with neither.
func (s *Service) TailscaleMatchFor(ctx context.Context, r TailscaleRequest) (TailscaleMatch, error) {
	q := s.db.Read()
	if _, err := tailscaleOn(ctx, q); err != nil {
		return TailscaleMatch{}, err
	}
	if err := s.checkTailnetNode(ctx, q, r.Remote, r.Whois); err != nil {
		return TailscaleMatch{}, err
	}
	rec, _, codeReady, err := tailnetUser(ctx, q, r.Whois)
	m := TailscaleMatch{Username: rec.Username, LoginName: r.Whois.LoginName, NodeName: r.Whois.NodeName}
	if err != nil {
		if rec.State == StatePending && refusedCause(err) == refusedDevice {
			return m, nil
		}
		return TailscaleMatch{}, err
	}
	if m.Remembered, err = remembered(ctx, q, rec.ID, r.Whois.StableID); err != nil {
		return TailscaleMatch{}, err
	}
	m.CodeReady = codeReady
	return m, nil
}

// SignInTailscale signs the node of r in as its linked active user. With
// the code step off, a node remembered for the user opens a session at
// once; otherwise a code-ready user gets a ticket for FinishTOTPStep, which
// pins the tailnet user id and, with Remember, remembers the node. A user
// who has never typed a code is refused, and so is an invited one: Tailscale
// identity never activates an invite. The node checks and the device read
// share one write transaction, so a forget either lands before the sign-in
// reads the row or after its session is open. Every outcome is audited with
// method tailscale.
func (s *Service) SignInTailscale(ctx context.Context, r TailscaleRequest) (SignInResult, error) {
	var res SignInResult
	var rec userRecord
	extra := map[string]string{"login": r.Whois.LoginName, "node": r.Whois.NodeName}
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		set, err := tailscaleOn(ctx, tx)
		if err != nil {
			return err
		}
		if err := s.checkTailnetNode(ctx, tx, r.Remote, r.Whois); err != nil {
			return err
		}
		var pin, codeReady bool
		if rec, pin, codeReady, err = tailnetUser(ctx, tx, r.Whois); err != nil {
			return err
		}
		if !set.RequireTOTP {
			used, err := tx.ExecContext(ctx, `UPDATE core_user_tailnet_devices SET last_used_at = ?, node_name = ?
				WHERE user_id = ? AND stable_id = ?`, formatTime(s.now()), r.Whois.NodeName, rec.ID, r.Whois.StableID)
			if err != nil {
				return fmt.Errorf("auth: use the remembered device: %w", err)
			}
			n, err := used.RowsAffected()
			if err != nil {
				return fmt.Errorf("auth: use the remembered device: %w", err)
			}
			if n == 1 {
				extra["device"] = "remembered"
				res.LoginResult, err = s.openSession(ctx, tx, rec.User, r.Source, r.UserAgent)
				return err
			}
		}
		if !codeReady {
			return refuse(refusedDevice, errSignInRefused())
		}
		addrs, err := nodeAddrs(r.Remote, r.Whois)
		if err != nil {
			return err
		}
		t := signInTicket{ID: randomID(), UserID: rec.ID, Method: MethodTailscale, Expires: s.now().Add(SignInTicketTTL),
			StableID: r.Whois.StableID, NodeName: r.Whois.NodeName, Login: r.Whois.LoginName,
			WhoisUserID: strconv.FormatInt(r.Whois.UserID, 10)}
		for _, a := range addrs {
			t.Addrs = append(t.Addrs, a.String())
		}
		if pin {
			t.TailnetUserID = t.WhoisUserID
		}
		res.Remember = !set.RequireTOTP
		res.Ticket, err = s.sealJSON(signInTicketAD, t)
		res.TicketExpires = t.Expires
		return err
	})
	code := errcode.Code("")
	if err != nil {
		code = errcode.From(err).Code
		if cause := refusedCause(err); cause != "" {
			extra["refused"] = cause
		}
	}
	if res.Ticket != "" {
		extra["step"] = "ticket"
	}
	if aerr := s.recordSignInWith(ctx, MethodTailscale, rec.Username, r.Source, rec.ID, code, extra); aerr != nil {
		return SignInResult{}, aerr
	}
	if err != nil {
		return SignInResult{}, err
	}
	return res, nil
}

// RefuseTailscale audits a Continue-as sign-in refused because the whois
// failed or its answer did not decode, and returns err; an audit failure
// is returned instead.
func (s *Service) RefuseTailscale(ctx context.Context, source string, err error) error {
	if aerr := s.recordSignInWith(ctx, MethodTailscale, "", source, "", errcode.From(err).Code, map[string]string{"step": "whois"}); aerr != nil {
		return aerr
	}
	return err
}
