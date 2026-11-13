package npm

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/fake"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

const (
	v1r12Intruder         = "intruder@example.com"
	v1r12IntruderPassword = "placeholder-secret-intruder"
)

// v1r12Racer is the unauthenticated NPM client of a deploy that loses the first-administrator race: another client
// creates an administrator after the deploy read NPM's status and before the deploy creates its own.
type v1r12Racer struct {
	npmAPI
	win func()
}

func (r v1r12Racer) CreateAdmin(ctx context.Context, email, password string) error {
	r.win()
	return r.npmAPI.CreateAdmin(ctx, email, password)
}

// v1r12Removals is the Engine changes that follow the start of the container.
func v1r12Removals(h *thHost) []string {
	got := dpMutated(h)
	return got[slices.Index(got, "StartContainer")+1:]
}

// w12aHalt is the unauthenticated NPM client of a deploy that is cancelled while it waits for NPM to answer.
type w12aHalt struct {
	npmAPI
	cancel context.CancelFunc
}

func (s w12aHalt) Health(ctx context.Context) (npmapi.Health, error) {
	s.cancel()
	return s.npmAPI.Health(ctx)
}

func TestV1r12BootstrapConflictRemovesTheInstance(t *testing.T) {
	cases := []struct {
		name      string
		arrange   func(h *thHost, n *nm, cancel context.CancelFunc)
		foreign   bool // the administrator in NPM is the intruder's
		removed   bool
		cancelled bool // the apply's context ends during the bootstrap
	}{
		{"an administrator exists before the deploy creates one", func(h *thHost, _ *nm, _ context.CancelFunc) {
			h.npm.SeedAdmin(v1r12Intruder, v1r12IntruderPassword)
		}, true, true, false},
		{"an administrator appears between the status read and the creation", func(h *thHost, n *nm, _ context.CancelFunc) {
			live := n.d.anon()
			n.d.anon = func() npmAPI {
				return v1r12Racer{npmAPI: live, win: func() { h.npm.SeedAdmin(v1r12Intruder, v1r12IntruderPassword) }}
			}
		}, true, true, false},
		{"NPM refuses to create the administrator", func(h *thHost, _ *nm, _ context.CancelFunc) {
			h.npm.Fail(http.MethodPost, "/api/users", fake.Fault{Status: http.StatusForbidden})
		}, false, true, false},
		{"NPM fails while it creates the administrator", func(h *thHost, _ *nm, _ context.CancelFunc) {
			h.npm.Fail(http.MethodPost, "/api/users", fake.Fault{Status: http.StatusInternalServerError})
		}, false, true, false},
		{"the answer to the creation is lost", func(h *thHost, _ *nm, _ context.CancelFunc) {
			h.npm.Fail(http.MethodPost, "/api/users", fake.Fault{AfterCommit: true})
		}, false, true, false},
		{"NPM does not answer within the wait", func(_ *thHost, n *nm, _ context.CancelFunc) {
			probe := &dpHealth{npmAPI: n.d.anon(), failing: -1}
			n.d.anon = func() npmAPI { return probe }
		}, false, true, false},
		{"the deploy is cancelled while it creates the administrator", func(_ *thHost, n *nm, cancel context.CancelFunc) {
			live := n.d.anon()
			n.d.anon = func() npmAPI { return v1r12Racer{npmAPI: live, win: cancel} }
		}, false, true, true},
		{"the deploy is cancelled while it waits for NPM", func(_ *thHost, n *nm, cancel context.CancelFunc) {
			live := n.d.anon()
			n.d.anon = func() npmAPI { return w12aHalt{npmAPI: live, cancel: cancel} }
		}, false, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newTHHost(t)
			n := h.nm()
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			c.arrange(h, &n, cancel)
			var err error
			dpDrive(t, h, deployPoll, func() {
				_, err = n.instanceDeploy().Handler.Apply(ctx, h.env("c1"), dpInput(dpEmail))
			})
			var coded *errcode.Error
			if err == nil || !errors.As(err, &coded) || coded.Code != errcode.NPMSetupFailed {
				t.Fatalf("error %v, want npm.setup_failed", err)
			}
			if core.IsDefinite(err) {
				t.Error("the failure is Definite: effects of the deploy were made")
			}
			if got := errors.Is(err, context.Canceled); got != c.cancelled {
				t.Errorf("error %v carries the cancellation: %t, want %t", err, got, c.cancelled)
			}
			for _, secret := range []string{dpPassword, v1r12IntruderPassword} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("the error carries %q", secret)
				}
			}
			_, container := h.docker.container(containerName)
			_, network := h.docker.network(networkName)
			_, data := h.docker.volume(dataVolume)
			_, certs := h.docker.volume(certVolume)
			for name, present := range map[string]bool{"container": container, "network": network, "data volume": data, "certificate volume": certs} {
				if present == c.removed {
					t.Errorf("the %s is present: %t, want %t", name, present, !c.removed)
				}
			}
			if removed := strings.Contains(coded.Detail, "the deploy removed what it created"); removed != c.removed {
				t.Errorf("detail %q says the deploy removed its objects: %t, want %t", coded.Detail, removed, c.removed)
			}
			want := []string{"StopContainer", "RemoveContainer", "RemoveVolume", "RemoveVolume", "RemoveNetwork"}
			if !c.removed {
				want = nil
			}
			if got := v1r12Removals(h); !slices.Equal(got, want) {
				t.Errorf("Engine changes after the start %v, want %v", got, want)
			}
			if c.foreign {
				if email, password, ok := h.npm.Admin(); !ok || email != v1r12Intruder || password != v1r12IntruderPassword {
					t.Errorf("the other administrator is now %q: the deploy must not touch NPM's users", email)
				}
			}
			if _, ok, _ := loadInstance(context.Background(), h.db.Read()); ok {
				t.Error("an instance row was written")
			}
		})
	}
}

func TestV1r12BootstrapConflictReportsAFailedRemoval(t *testing.T) {
	h := newTHHost(t)
	h.npm.SeedAdmin(v1r12Intruder, v1r12IntruderPassword)
	h.docker.Fail("RemoveContainer", thAPIError(http.StatusInternalServerError, "daemon error"))
	_, err := dpApply(h, h.nm(), "c1")
	var coded *errcode.Error
	if err == nil || !errors.As(err, &coded) || coded.Code != errcode.NPMSetupFailed {
		t.Fatalf("error %v, want npm.setup_failed", err)
	}
	if !strings.Contains(coded.Detail, "the deploy could not remove what it created") || strings.Contains(coded.Detail, "the deploy removed what it created") {
		t.Errorf("detail %q, want it to say the removal failed", coded.Detail)
	}
	if _, ok := h.docker.container(containerName); !ok {
		t.Error("the container is gone although its removal failed")
	}
	want := []string{"StopContainer", "RemoveContainer", "RemoveVolume", "RemoveVolume", "RemoveNetwork"}
	if got := v1r12Removals(h); !slices.Equal(got, want) {
		t.Errorf("Engine changes after the start %v, want every removal attempted: %v", got, want)
	}
	for _, secret := range []string{dpPassword, v1r12IntruderPassword} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("the error carries %q", secret)
		}
	}
}
