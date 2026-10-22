package api

import (
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// RequiredScope is the scope a principal needs for a change of class on
// side whose action the registry no longer has: core.DefaultScope, because
// no MinScope is left to raise it. A registered action needs
// core.RequiredScope of its spec (spec 1.7).
func RequiredScope(class core.Class, side core.Side) core.Scope {
	return core.DefaultScope(class, side)
}

// authorize refuses p with auth.forbidden unless its scope includes need.
func authorize(p core.Principal, need core.Scope) error {
	if !p.Scope.Allows(need) {
		return errcode.New(errcode.Forbidden, fmt.Sprintf("this request needs the %s scope", need))
	}
	return nil
}
