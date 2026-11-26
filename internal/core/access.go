package core

import (
	"fmt"
	"sort"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Access is what a principal may do on each host, resolved per request.
type Access struct {
	Admin bool             // every scope on every host and on the controller
	Fleet bool             // may run controller-side reads of fleet data
	All   Scope            // the scope on every host, from a "*" grant
	Hosts map[string]Scope // the scope on single hosts, from group grants
	// Ceil caps the scope on single hosts whatever the grants say: a
	// member's scope on the controller's own host is read at most, which
	// All alone cannot express under a "*" grant.
	Ceil map[string]Scope
}

// On returns the scope on hostID, or "" when the host is outside the grants.
func (a Access) On(hostID string) Scope {
	if a.Admin {
		return ScopeAdmin
	}
	s := a.All
	if h := a.Hosts[hostID]; h.rank() > s.rank() {
		s = h
	}
	if c, ok := a.Ceil[hostID]; ok && s.rank() > c.rank() {
		s = c
	}
	return known(s)
}

// known is s, or "" for a scope no check knows, so it fails closed.
func known(s Scope) Scope {
	if s.rank() == 0 {
		return ""
	}
	return s
}

// AllHosts reports whether need is allowed on every host, capped ones included.
func (a Access) AllHosts(need Scope) bool {
	if a.Admin {
		return true
	}
	if !a.All.Allows(need) {
		return false
	}
	for _, c := range a.Ceil {
		if !c.Allows(need) {
			return false
		}
	}
	return true
}

// Sees reports whether a row tied to hostID may be shown: the host is inside
// the grants, or, for a row tied to no host (""), the principal has fleet
// access.
func (a Access) Sees(hostID string) bool {
	if hostID == "" {
		return a.Fleet || a.Admin
	}
	return a.On(hostID) != ""
}

// HostAccess is the access a check applies to p. System, local break-glass
// and admin principals carry ScopeAdmin without resolved grants; they act as
// admins everywhere.
func (p Principal) HostAccess() Access {
	if p.Scope == ScopeAdmin {
		return Access{Admin: true, Fleet: true}
	}
	return p.Access
}

// Everywhere is the scope a holds on every host: admin for an admin, else
// All lowered to its lowest Ceil entry, "" without a "*" grant.
func (a Access) Everywhere() Scope {
	if a.Admin {
		return ScopeAdmin
	}
	s := a.All
	for _, c := range a.Ceil {
		if s.rank() > c.rank() {
			s = c
		}
	}
	return known(s)
}

// RowScope limits a list to the rows a principal without fleet access may
// see: rows on HostIDs, on every host when HostIDs is nil, and, in a list
// that keeps any, host-less rows whose actor is one of Actors.
type RowScope struct {
	HostIDs []string
	Actors  []string
}

// RowScope is the scope of a's lists, nil when a sees every row. A "*" grant
// sees every host, so its HostIDs is nil; otherwise HostIDs holds the hosts
// of a's group grants, empty when there are none.
func (a Access) RowScope(actors ...string) *RowScope {
	if a.Admin || a.Fleet {
		return nil
	}
	r := &RowScope{Actors: actors}
	if known(a.All) == "" {
		r.HostIDs = []string{}
		for id := range a.Hosts {
			if a.On(id) != "" {
				r.HostIDs = append(r.HostIDs, id)
			}
		}
		sort.Strings(r.HostIDs)
	}
	return r
}

// Shows reports whether r keeps a row on hostID; a nil r keeps every row.
func (r *RowScope) Shows(hostID string) bool {
	if r == nil {
		return true
	}
	if hostID == "" {
		return false
	}
	if r.HostIDs == nil {
		return true
	}
	i := sort.SearchStrings(r.HostIDs, hostID)
	return i < len(r.HostIDs) && r.HostIDs[i] == hostID
}

// RequireScope refuses env's principal with auth.forbidden when its scope on
// env.HostID is below need. A handler calls it in Plan when one input, such
// as a unit another module manages, needs more than the action's own scope.
func RequireScope(env Env, need Scope) error {
	if env.Principal.HostAccess().On(env.HostID).Allows(need) {
		return nil
	}
	return errcode.New(errcode.Forbidden, fmt.Sprintf("this request needs the %s scope", need))
}
