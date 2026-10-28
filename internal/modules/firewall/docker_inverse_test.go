package firewall

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
)

// diTestForward runs op forward and returns its made effect.
func diTestForward(t *testing.T, h *fakeHost, d deps, op dockerOp) core.Effect {
	t.Helper()
	h.journal.effects = nil
	if err := runDockerEffect(context.Background(), h.env(), d, op); err != nil {
		t.Fatal(err)
	}
	e := efTestOnly(t, h)
	if e.State != core.EffectMade {
		t.Fatalf("forward effect is %s", e.State)
	}
	return e
}

func diTestUndo(h *fakeHost, d deps, e core.Effect) error {
	return inverse(context.Background(), h.env(), d, snapshot{}, e)
}

func diTestConfig(t *testing.T, h *fakeHost) dockerConfig {
	t.Helper()
	c, err := readDockerConfig(context.Background(), h.db)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDiInverseSettings(t *testing.T) {
	h := newFakeHost(t)
	d := h.deps()
	sources := dockerfw.Settings{PrivateSources: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}

	e := diTestForward(t, h, d, sourcesOp(sources))
	if c := diTestConfig(t, h); !slices.Equal(c.Settings.PrivateSources, sources.PrivateSources) {
		t.Fatalf("forward sources = %+v", c.Settings)
	}
	flag := diTestForward(t, h, d, flagOp(true))
	if err := diTestUndo(h, d, flag); err != nil {
		t.Fatal(err)
	}
	if c := diTestConfig(t, h); c.Enabled || !slices.Equal(c.Settings.PrivateSources, sources.PrivateSources) {
		t.Errorf("config after undoing the flag = %+v, want it off and the sources kept", c)
	}

	if err := diTestUndo(h, d, e); err != nil {
		t.Fatal(err)
	}
	if c := diTestConfig(t, h); !slices.Equal(c.Settings.PrivateSources, dockerfw.DefaultSettings().PrivateSources) || len(c.Settings.ForwardExemptions) != 0 {
		t.Errorf("settings after undoing the sources = %+v, want the defaults", c.Settings)
	}

	diTestForward(t, h, d, flagOp(true))
	off := diTestForward(t, h, d, flagOp(false))
	if err := diTestUndo(h, d, off); err != nil {
		t.Fatal(err)
	}
	if c := diTestConfig(t, h); !c.Enabled {
		t.Error("undoing the flag's switch off left it off")
	}
}

func TestDiInverseRows(t *testing.T) {
	h := newFakeHost(t)
	d := h.deps()
	allow, deny := deTestAllow(), deTestDeny()
	diTestForward(t, h, d, rowCreateOp(allow))

	created := diTestForward(t, h, d, rowCreateOp(deny))
	noPost := created
	noPost.Post = nil
	if err := diTestUndo(h, d, noPost); err == nil || !slices.Equal(deTestRules(t, h), []dockerfw.ContainerRule{allow, deny}) {
		t.Errorf("undoing a creation with no post state = %v, rows %+v", err, deTestRules(t, h))
	}
	if err := diTestUndo(h, d, created); err != nil {
		t.Fatal(err)
	}
	if got := deTestRules(t, h); !slices.Equal(got, []dockerfw.ContainerRule{allow}) {
		t.Errorf("rows after undoing the creation = %+v, want only the allow", got)
	}

	diTestForward(t, h, d, rowCreateOp(deny))
	deleted := diTestForward(t, h, d, rowDeleteOp(deny))
	if got := deTestRules(t, h); !slices.Equal(got, []dockerfw.ContainerRule{allow}) {
		t.Fatalf("rows after the deletion = %+v", got)
	}
	if err := diTestUndo(h, d, deleted); err != nil {
		t.Fatal(err)
	}
	if got := deTestRules(t, h); !slices.Equal(got, []dockerfw.ContainerRule{allow, deny}) {
		t.Errorf("rows after undoing the deletion = %+v, want the allow and the deny as they were", got)
	}
}

func TestDiInverseChains(t *testing.T) {
	boom := errors.New("boom")
	// loadsChains is the forward call that loads the chains, as a ufw
	// reload with the block does; cleansChains is the one that removes them.
	loadsChains := func(h *fakeHost) func(context.Context, core.Env) error {
		return func(context.Context, core.Env) error { deTestHold(h.t, h, deTestSaveDeny, deTestSavePlain); return nil }
	}
	cleansChains := func(h *fakeHost) func(context.Context, core.Env) error {
		return func(context.Context, core.Env) error {
			deTestHold(h.t, h, deTestSavePlain, deTestSavePlain)
			return nil
		}
	}

	t.Run("cleaned chains come back with a ufw reload", func(t *testing.T) {
		h := newFakeHost(t)
		deTestHold(t, h, deTestSaveDeny, deTestSavePlain)
		d := h.deps()
		e := diTestForward(t, h, d, chainsOp("absent", cleansChains(h)))
		h.ufw["reload"] = func(*fhState) int { deTestHold(t, h, deTestSaveDeny, deTestSavePlain); return 0 }
		if err := diTestUndo(h, d, e); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(h.calls, "run /usr/sbin/ufw reload") {
			t.Errorf("calls %v lack the reload", h.calls)
		}
	})
	t.Run("no reload while UFW is inactive", func(t *testing.T) {
		h := newFakeHost(t)
		deTestHold(t, h, deTestSaveDeny, deTestSavePlain)
		d := h.deps()
		e := diTestForward(t, h, d, chainsOp("absent", cleansChains(h)))
		s := h.state()
		s.Enabled = false
		h.setState(s)
		h.ufw["reload"] = func(*fhState) int { deTestHold(t, h, deTestSaveDeny, deTestSavePlain); return 0 }
		if err := diTestUndo(h, d, e); err == nil {
			t.Error("the undo succeeded with the chains still absent")
		}
		if slices.Contains(h.calls, "run /usr/sbin/ufw reload") {
			t.Errorf("calls %v hold a reload on an inactive UFW", h.calls)
		}
	})
	t.Run("a failing reload fails the undo", func(t *testing.T) {
		h := newFakeHost(t)
		deTestHold(t, h, deTestSaveDeny, deTestSavePlain)
		d := h.deps()
		e := diTestForward(t, h, d, chainsOp("absent", cleansChains(h)))
		h.ufw["reload"] = func(*fhState) int { return 1 }
		if err := diTestUndo(h, d, e); err == nil {
			t.Error("the undo succeeded after a failed reload")
		}
	})
	t.Run("loaded chains are cleaned under the change id", func(t *testing.T) {
		h := newFakeHost(t)
		deTestHold(t, h, deTestSavePlain, deTestSavePlain)
		d := h.deps()
		e := diTestForward(t, h, d, chainsOp("loaded", loadsChains(h)))
		var owners []string
		d.cleanChains = func(ctx context.Context, env core.Env, owner string) error {
			owners = append(owners, owner)
			return cleansChains(h)(ctx, env)
		}
		if err := diTestUndo(h, d, e); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(owners, []string{"c1"}) {
			t.Errorf("cleanup owners = %v, want [c1]", owners)
		}
		if slices.Contains(h.calls, "run /usr/sbin/ufw reload") {
			t.Errorf("calls %v hold a reload", h.calls)
		}
	})
	t.Run("a missing cleanup is an error", func(t *testing.T) {
		h := newFakeHost(t)
		deTestHold(t, h, deTestSavePlain, deTestSavePlain)
		d := h.deps()
		e := diTestForward(t, h, d, chainsOp("loaded", loadsChains(h)))
		err := diTestUndo(h, d, e)
		if err == nil || !strings.Contains(err.Error(), "cleanChains") {
			t.Errorf("undo with no cleanChains = %v, want an error naming cleanChains", err)
		}
		if obs := obTestObserve(t, h, core.EffectModify, "firewall.chains:docker"); string(obs.Current.State) != `"loaded"` {
			t.Errorf("chains = %s after the refused undo, want them loaded still", obs.Current.State)
		}
	})
	t.Run("a failing cleanup fails the undo", func(t *testing.T) {
		h := newFakeHost(t)
		deTestHold(t, h, deTestSavePlain, deTestSavePlain)
		d := h.deps()
		e := diTestForward(t, h, d, chainsOp("loaded", loadsChains(h)))
		d.cleanChains = func(context.Context, core.Env, string) error { return boom }
		if err := diTestUndo(h, d, e); !errors.Is(err, boom) {
			t.Errorf("undo = %v, want the cleanup's error", err)
		}
	})
}

func TestDiInverseRefusesDamagedEffects(t *testing.T) {
	h := newFakeHost(t)
	d := h.deps()
	diTestForward(t, h, d, flagOp(true))
	diTestForward(t, h, d, rowCreateOp(deTestAllow()))
	null := core.StateDoc{State: []byte("null")}
	for name, e := range map[string]core.Effect{
		"a flag with no before":      {Kind: core.EffectModify, Resource: "firewall.setting:docker", Before: null},
		"sources with no before":     {Kind: core.EffectModify, Resource: "firewall.setting:sources", Before: null},
		"sources that are not JSON":  {Kind: core.EffectModify, Resource: "firewall.setting:sources", Before: core.StateDoc{State: []byte(`"x"`)}},
		"an unknown setting":         {Kind: core.EffectModify, Resource: "firewall.setting:other", Before: core.StateDoc{State: []byte(`true`)}},
		"a deleted row with no row":  {Kind: core.EffectDelete, Resource: "firewall.container:" + deTestID2, Before: null},
		"chains in an unknown state": {Kind: core.EffectModify, Resource: "firewall.chains:docker", Before: core.StateDoc{State: []byte(`"sideways"`)}},
		"chains with no before":      {Kind: core.EffectModify, Resource: "firewall.chains:docker", Before: null},
	} {
		t.Run(name, func(t *testing.T) {
			if err := diTestUndo(h, d, e); err == nil {
				t.Error("the undo succeeded")
			}
			if c := diTestConfig(t, h); !c.Enabled {
				t.Error("the flag changed")
			}
			if got := deTestRules(t, h); !slices.Equal(got, []dockerfw.ContainerRule{deTestAllow()}) {
				t.Errorf("rows = %+v", got)
			}
		})
	}
}
