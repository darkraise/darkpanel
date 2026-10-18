package api

import (
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// RequiredScope is the scope a principal needs for an action of class on
// side (spec section 10). Read actions need read. Mutating agent-side
// actions, the module actions, need operate for plan and apply, as their
// changes' confirm and revert do. Mutating controller-side actions, the
// core's token and module-enablement actions, need admin. An unknown class
// or side needs admin, so a mistake fails closed.
func RequiredScope(class core.Class, side core.Side) core.Scope {
	switch {
	case class == core.ClassRead:
		return core.ScopeRead
	case side == core.SideAgent &&
		(class == core.ClassRevertible || class == core.ClassCompensable || class == core.ClassIrreversible):
		return core.ScopeOperate
	default:
		return core.ScopeAdmin
	}
}

// authorize refuses p with auth.forbidden unless its scope includes need.
func authorize(p core.Principal, need core.Scope) error {
	if !p.Scope.Allows(need) {
		return errcode.New(errcode.Forbidden, fmt.Sprintf("this request needs the %s scope", need))
	}
	return nil
}
