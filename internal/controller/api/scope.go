package api

import (
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// RequiredScope is the default scope of an action of class on side,
// core.DefaultScope, before any MinScope raises it. No route authorizes
// with it: a registered action needs core.RequiredScope of its spec (spec
// 1.7), and a change whose action is unregistered needs admin.
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
