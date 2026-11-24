package api

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// authorize refuses p with auth.forbidden unless its scope includes need.
// Only the controller socket, whose principals are break-glass admins, still
// checks a bare scope; network routes check access (authorizeHost,
// authorizeController, authorizeAdmin).
func authorize(p core.Principal, need core.Scope) error {
	if !p.Scope.Allows(need) {
		return errcode.New(errcode.Forbidden, fmt.Sprintf("this request needs the %s scope", need))
	}
	return nil
}

// authorizeAdmin refuses p unless it has admin access.
func authorizeAdmin(p core.Principal) error {
	if !p.HostAccess().Admin {
		return errcode.New(errcode.Forbidden, "this request needs the admin scope")
	}
	return nil
}

// authorizeAction refuses p def on its route: an agent-side action needs its
// scope on hostID, a controller-side one the rules of authorizeController.
func authorizeAction(p core.Principal, def core.ActionDef, hostID string) error {
	if def.Spec.Side == core.SideAgent {
		return authorizeHost(p, hostID, core.RequiredScope(def.Spec))
	}
	return authorizeController(p, def)
}

// authorizeHost refuses p on hostID: not_found when the host is
// outside p's access, so host ids do not leak, and auth.forbidden when p's
// scope there is below need.
func authorizeHost(p core.Principal, hostID string, need core.Scope) error {
	have := p.HostAccess().On(hostID)
	if have == "" {
		return errcode.New(errcode.NotFound, fmt.Sprintf("no host %s", hostID))
	}
	if !have.Allows(need) {
		return errcode.New(errcode.Forbidden, fmt.Sprintf("this request needs the %s scope", need))
	}
	return nil
}

// authorizeController refuses p a controller-side action. A mutating one
// needs fleet access and its scope on every host, whatever hosts its input
// names, so members are refused, except for their own tokens
// (ownTokenActions). A read is served by its HostData class (ADR 0018); an
// input read's hosts are checked once its input is decoded
// (authorizeInputHosts).
func authorizeController(p core.Principal, def core.ActionDef) error {
	need := core.RequiredScope(def.Spec)
	access := p.HostAccess()
	if ownTokenActions[def.Ref()] && !access.Fleet {
		if p.UserID == "" || p.TokenID != "" {
			return errcode.New(errcode.Forbidden, "managing tokens needs a signed-in session")
		}
		return nil
	}
	if def.Spec.Class != core.ClassRead {
		if access.Fleet && access.AllHosts(need) {
			return nil
		}
		return errcode.New(errcode.Forbidden, fmt.Sprintf("this request needs the %s scope", need))
	}
	// A MinScope read needs that scope everywhere: the class rules alone would
	// serve it to an admin's read token.
	if !core.ScopeRead.Allows(need) && !access.AllHosts(need) {
		return errcode.New(errcode.Forbidden, fmt.Sprintf("this request needs the %s scope", need))
	}
	switch def.Spec.HostData {
	case core.HostDataNone, core.HostDataInput, core.HostDataFiltered:
		if access.Fleet || readsAnyHost(access) {
			return nil
		}
	default:
		if access.Fleet {
			return nil
		}
	}
	return errcode.New(errcode.Forbidden, "this request needs access to the whole fleet")
}

// ownTokenActions act only on the calling user's own tokens, so a principal
// without fleet access may run them from a session (A47). A token is refused,
// so a member's token cannot mint or revoke another; the handlers refuse a
// member the admin scope.
var ownTokenActions = map[core.ActionRef]bool{
	{Name: "core.token.create", Version: 1}: true,
	{Name: "core.token.revoke", Version: 1}: true,
}

// readsAnyHost reports whether a grants read on at least one host.
func readsAnyHost(a core.Access) bool {
	if a.Admin || a.All.Allows(core.ScopeRead) {
		return true
	}
	for _, s := range a.Hosts {
		if s.Allows(core.ScopeRead) {
			return true
		}
	}
	return false
}

// authorizeInputHosts checks read on every host an input read's
// HostPointers name. A pointer to an array names each of its elements. A
// pointer the input leaves out names no host; one holding anything but a
// string or an array of strings refuses a principal without fleet access
// as not found.
func authorizeInputHosts(p core.Principal, def core.ActionDef, input json.RawMessage) error {
	if def.Spec.HostData != core.HostDataInput {
		return nil
	}
	var doc any
	if err := json.Unmarshal(input, &doc); err != nil {
		return invalidField("/input", "must be a JSON value")
	}
	for _, ptr := range def.Spec.HostPointers {
		v, present := jsonPointer(doc, ptr)
		if !present {
			continue
		}
		var hosts []string
		named := true
		switch v := v.(type) {
		case string:
			hosts = []string{v}
		case []any:
			for _, e := range v {
				s, isString := e.(string)
				if !isString {
					named = false
					continue
				}
				hosts = append(hosts, s)
			}
		default:
			named = false
		}
		if !named && !p.HostAccess().Fleet {
			return errcode.New(errcode.NotFound, "no host named by "+ptr)
		}
		for _, h := range hosts {
			if err := authorizeHost(p, h, core.ScopeRead); err != nil {
				return err
			}
		}
	}
	return nil
}

// jsonPointer resolves an RFC 6901 pointer of object members in doc.
func jsonPointer(doc any, ptr string) (any, bool) {
	if ptr == "" {
		return doc, true
	}
	for _, tok := range strings.Split(strings.TrimPrefix(ptr, "/"), "/") {
		obj, ok := doc.(map[string]any)
		if !ok {
			return nil, false
		}
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		if doc, ok = obj[tok]; !ok {
			return nil, false
		}
	}
	return doc, true
}

// changeVisible reports whether p may see change v: the network shows it,
// and its host is visible to p. A host-less change is visible to a fleet
// principal, and to any other when it is p's own or a composite whose every
// step is on a host p sees; core.ChangeFilter.Rows is the same rule as a
// query.
func (s *Server) changeVisible(ctx context.Context, p core.Principal, v core.ChangeView) bool {
	if !s.networkVisible(v) {
		return false
	}
	a := p.HostAccess()
	if v.HostID != "" || a.Sees("") {
		return a.Sees(v.HostID)
	}
	if v.Class != core.ClassComposite {
		return slices.Contains(ownActors(p), v.Actor)
	}
	steps, err := s.cfg.Dispatcher.Steps(ctx, p, v.ID)
	if err != nil {
		if errcode.From(err).Code != errcode.NotFound && ctx.Err() == nil {
			s.log.Warn("api: read a composite change's steps", "change", v.ID, "err", err)
		}
		return false
	}
	if len(steps) == 0 {
		return false
	}
	for _, st := range steps {
		if st.Host == "" || !a.Sees(st.Host) {
			return false
		}
	}
	return true
}

// ownActors are the actors of p's own changes: its own and its user's.
func ownActors(p core.Principal) []string {
	out := []string{p.Actor}
	if p.UserID != "" && p.Actor != "user:"+p.UserID {
		out = append(out, "user:"+p.UserID)
	}
	return out
}

// changeRows is the row scope of p's change lists.
func changeRows(p core.Principal) *core.RowScope {
	return p.HostAccess().RowScope(ownActors(p)...)
}
