package auth

import (
	"context"
	"database/sql"
	"fmt"
	"net/netip"
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
}

// checkTailnetNode refuses every whois answer but a node owned by a user
// of this tailnet, and any node that is a Darkpanel host or the controller.
func (s *Service) checkTailnetNode(ctx context.Context, remote netip.Addr, w TailnetWhois) error {
	// A whois answer with no node has no stable id.
	if w.StableID == "" || len(w.Tags) > 0 || w.Sharer != 0 || w.UserID == 0 || w.LoginName == "" || strings.EqualFold(w.LoginName, taggedDevices) {
		return errSignInRefused()
	}
	addrs := []netip.Addr{remote.Unmap()}
	for _, a := range w.Addresses {
		addr, err := netip.ParseAddr(a)
		if err != nil {
			return errSignInRefused()
		}
		addrs = append(addrs, addr)
	}
	managed, err := s.IsManagedTailnetNode(ctx, addrs, w.StableID)
	if err != nil {
		return err
	}
	if managed {
		return errSignInRefused()
	}
	return nil
}

// tailnetUser is the user linked to w's login name; pin reports that its
// tailnet user id is not stored yet. A pending user is refused when the
// code step is on, as OIDC does.
func tailnetUser(ctx context.Context, q querier, w TailnetWhois, requireTOTP bool) (rec userRecord, pin bool, err error) {
	rec, err = loadUserBy(ctx, q, `tailnet_login = ? COLLATE NOCASE`, w.LoginName)
	if errcode.From(err).Code == errcode.NotFound {
		return rec, false, errNoLinkedUser()
	}
	if err != nil {
		return rec, false, err
	}
	var pinned sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT tailnet_user_id FROM core_users WHERE id = ?`, rec.ID).Scan(&pinned); err != nil {
		return rec, false, fmt.Errorf("auth: read the tailnet link: %w", err)
	}
	if pinned.Valid && pinned.String != strconv.FormatInt(w.UserID, 10) || rec.Disabled {
		return rec, false, errSignInRefused()
	}
	switch {
	case rec.State == StateActive:
	case rec.State == StatePending && !requireTOTP:
	default:
		return rec, false, errSignInRefused()
	}
	return rec, !pinned.Valid, nil
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

// TailscaleMatchFor answers which user the node of r may continue as.
func (s *Service) TailscaleMatchFor(ctx context.Context, r TailscaleRequest) (TailscaleMatch, error) {
	set, err := tailscaleOn(ctx, s.db.Read())
	if err != nil {
		return TailscaleMatch{}, err
	}
	if err := s.checkTailnetNode(ctx, r.Remote, r.Whois); err != nil {
		return TailscaleMatch{}, err
	}
	rec, _, err := tailnetUser(ctx, s.db.Read(), r.Whois, set.RequireTOTP)
	if err != nil {
		return TailscaleMatch{}, err
	}
	return TailscaleMatch{Username: rec.Username, LoginName: r.Whois.LoginName, NodeName: r.Whois.NodeName}, nil
}

// SignInTailscale signs the node of r in as its linked user: it pins the
// tailnet user id on the first sign-in, activates an invited user, and
// with the code step on answers a ticket for FinishTOTPStep instead of a
// session. Every outcome is audited with method tailscale.
func (s *Service) SignInTailscale(ctx context.Context, r TailscaleRequest) (SignInResult, error) {
	var res SignInResult
	var rec userRecord
	err := s.checkTailnetNode(ctx, r.Remote, r.Whois)
	if err == nil {
		err = s.db.Write(ctx, func(tx *sql.Tx) error {
			set, err := tailscaleOn(ctx, tx)
			if err != nil {
				return err
			}
			var pin bool
			if rec, pin, err = tailnetUser(ctx, tx, r.Whois, set.RequireTOTP); err != nil {
				return err
			}
			userID := strconv.FormatInt(r.Whois.UserID, 10)
			if set.RequireTOTP {
				t := signInTicket{ID: randomID(), UserID: rec.ID, Method: MethodTailscale, Expires: s.now().Add(SignInTicketTTL)}
				if pin {
					t.TailnetUserID = userID
				}
				res.Ticket, err = s.sealJSON(signInTicketAD, t)
				res.TicketExpires = t.Expires
				return err
			}
			now := formatTime(s.now())
			if pin {
				if _, err := tx.ExecContext(ctx, `UPDATE core_users SET tailnet_user_id = ?, updated_at = ? WHERE id = ?`,
					userID, now, rec.ID); err != nil {
					return fmt.Errorf("auth: pin the tailnet user: %w", err)
				}
			}
			if rec.State == StatePending {
				// The enrollment link stays usable, as for OIDC (spec 1.5).
				if _, err := tx.ExecContext(ctx, `UPDATE core_users SET state = ?, updated_at = ? WHERE id = ?`,
					StateActive, now, rec.ID); err != nil {
					return fmt.Errorf("auth: activate on Tailscale sign-in: %w", err)
				}
				rec.State = StateActive
			}
			res.LoginResult, err = s.openSession(ctx, tx, rec.User, r.Source, r.UserAgent)
			return err
		})
	}
	code := errcode.Code("")
	if err != nil {
		code = errcode.From(err).Code
	}
	extra := map[string]string{"login": r.Whois.LoginName, "node": r.Whois.NodeName}
	if res.Ticket != "" {
		extra["step"] = "ticket"
	}
	if aerr := s.recordSignInWith(ctx, MethodTailscale, rec.Username, r.Source, rec.ID, code, extra); aerr != nil && err == nil {
		return SignInResult{}, aerr
	}
	if err != nil {
		return SignInResult{}, err
	}
	return res, nil
}
