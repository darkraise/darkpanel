package core

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
