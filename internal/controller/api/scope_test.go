package api

import (
	"errors"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestRequiredScope(t *testing.T) {
	const unknownClass, unknownSide = core.Class("bogus"), core.Side("bogus")
	want := map[core.Class]map[core.Side]core.Scope{
		core.ClassRead: {
			core.SideAgent: core.ScopeRead, core.SideController: core.ScopeRead, unknownSide: core.ScopeRead,
		},
		core.ClassRevertible: {
			core.SideAgent: core.ScopeOperate, core.SideController: core.ScopeAdmin, unknownSide: core.ScopeAdmin,
		},
		core.ClassCompensable: {
			core.SideAgent: core.ScopeOperate, core.SideController: core.ScopeAdmin, unknownSide: core.ScopeAdmin,
		},
		core.ClassIrreversible: {
			core.SideAgent: core.ScopeOperate, core.SideController: core.ScopeAdmin, unknownSide: core.ScopeAdmin,
		},
		unknownClass: {
			core.SideAgent: core.ScopeAdmin, core.SideController: core.ScopeAdmin, unknownSide: core.ScopeAdmin,
		},
	}
	// The needs each principal scope satisfies, written out rather than
	// derived, so the test does not share the code's ordering.
	satisfies := map[core.Scope]map[core.Scope]bool{
		core.ScopeRead:    {core.ScopeRead: true},
		core.ScopeOperate: {core.ScopeRead: true, core.ScopeOperate: true},
		core.ScopeAdmin:   {core.ScopeRead: true, core.ScopeOperate: true, core.ScopeAdmin: true},
		"":                {},
		"superuser":       {},
	}
	for class, sides := range want {
		for side, need := range sides {
			if got := RequiredScope(class, side); got != need {
				t.Errorf("RequiredScope(%s, %s) = %s, want %s", class, side, got, need)
			}
			for scope, ok := range satisfies {
				err := authorize(core.Principal{Actor: "token:t1", Scope: scope}, RequiredScope(class, side))
				if ok[need] != (err == nil) {
					t.Errorf("scope %q, class %s, side %s: authorize = %v, want allowed %v", scope, class, side, err, ok[need])
				}
				var e *errcode.Error
				if err != nil && (!errors.As(err, &e) || e.Code != errcode.Forbidden || e.Detail != "this request needs the "+string(need)+" scope") {
					t.Errorf("scope %q, need %s: error = %v, want auth.forbidden naming the scope", scope, need, err)
				}
			}
		}
	}
}
