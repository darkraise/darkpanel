package core

// DefaultScope is the scope an action of class on side needs (spec section
// 10): read for read actions, operate for agent-side mutations, admin
// otherwise. An unknown class or side needs admin, so a mistake fails closed.
func DefaultScope(class Class, side Side) Scope {
	switch {
	case class == ClassRead:
		return ScopeRead
	case side == SideAgent && (class == ClassRevertible || class == ClassCompensable || class == ClassIrreversible):
		return ScopeOperate
	default:
		return ScopeAdmin
	}
}

// RequiredScope is the higher of DefaultScope(s.Class, s.Side) and
// s.MinScope. An unknown MinScope is returned as it is, which no principal's
// scope allows.
func RequiredScope(s ActionSpec) Scope {
	need := DefaultScope(s.Class, s.Side)
	if s.MinScope == "" || need.Allows(s.MinScope) {
		return need
	}
	return s.MinScope
}
