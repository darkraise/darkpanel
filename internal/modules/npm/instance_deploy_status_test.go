package npm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

type w11c02Refuser struct {
	npmAPI
	status int
}

func (r w11c02Refuser) CreateAdmin(context.Context, string, string) error {
	return &npmapi.APIError{Status: r.status}
}

func TestW11c02OnlyAnExistingAdminIsAConflict(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		removes bool
	}{
		{"a rejected payload", 400, false},
		{"permission denied", 403, true},
		{"a conflict", 409, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTHHost(t)
			n := h.nm()
			anon := n.d.anon
			n.d.anon = func() npmAPI { return w11c02Refuser{npmAPI: anon(), status: tc.status} }
			_, err := dpApply(h, n, "c1")
			if errcode.From(err).Code != errcode.NPMSetupFailed {
				t.Fatalf("error %v, want npm.setup_failed", err)
			}
			if removed := len(v1r12Removals(h)) > 0; removed != tc.removes {
				t.Errorf("removed the instance: %v, want %v", removed, tc.removes)
			}
			if n := strings.Count(err.Error(), "did not create the administrator"); n != 1 {
				t.Errorf("the error states its detail %d times, want once: %v", n, err)
			}
		})
	}
}

func TestW11c02ExistingAdministratorIsStatedOnce(t *testing.T) {
	h := newTHHost(t)
	h.npm.SeedAdmin(v1r12Intruder, v1r12IntruderPassword)
	_, err := dpApply(h, h.nm(), "c1")
	if errcode.From(err).Code != errcode.NPMSetupFailed {
		t.Fatalf("error %v, want npm.setup_failed", err)
	}
	if n := strings.Count(err.Error(), "already has an administrator"); n != 1 {
		t.Errorf("the error states its detail %d times, want once: %v", n, err)
	}
}

func TestW11c02WithoutDefiniteMarkKeepsWrapperText(t *testing.T) {
	cause := errors.New("cause")
	wrapped := withoutDefiniteMark(fmt.Errorf("outer: %w", core.Definite(cause)))
	if core.IsDefinite(wrapped) || !errors.Is(wrapped, cause) || !strings.Contains(wrapped.Error(), "outer: ") {
		t.Errorf("a mark beneath a wrapper gave %v (definite %v)", wrapped, core.IsDefinite(wrapped))
	}
	joined := withoutDefiniteMark(errors.Join(core.Definite(cause), errors.New("and more")))
	if core.IsDefinite(joined) || !strings.Contains(joined.Error(), "and more") {
		t.Errorf("a joined mark gave %v (definite %v)", joined, core.IsDefinite(joined))
	}
}
