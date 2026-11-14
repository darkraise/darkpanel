package npm

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// w12eUpSetup is w12aUpSetup with NPM's address on app-net chosen by the Engine, and the container db holding
// 172.30.0.20 there, which a proxy host forwards to.
func w12eUpSetup(t *testing.T) *upSetup {
	t.Helper()
	s := w12aUpSetup(t)
	c, ok := s.h.docker.container(s.inst.ContainerID)
	if !ok {
		t.Fatal("the NPM container is missing")
	}
	c.NetworkSettings.Networks[upAppNet].IPAMConfig = nil
	s.h.docker.putContainer(c)
	s.orig = c
	s.h.docker.putContainer(thContainer("db", thEndpoint(upAppNet, "172.30.0.20")))
	w12aSeed(s.h, "172.30.0.20", 5432)
	return s
}

// w12eFreeAt removes db when the upgrade makes call, once, and pins its address for the next endpoints on app-net. For
// PullImage the pinned image is reported missing until the pull starts, so the upgrade pulls it.
func w12eFreeAt(t *testing.T, s *upSetup, call string) {
	t.Helper()
	if call == "PullImage" {
		s.dock.missing[Image] = true
	}
	done := false
	s.dock.trace = func(c string) {
		if c != call || done {
			return
		}
		done = true
		s.dock.missing[Image] = false
		if err := s.h.docker.RemoveContainer(context.Background(), "db", dockerapi.RemoveContainerOptions{Force: true}); err != nil {
			t.Error(err)
		}
		s.h.docker.pin(upAppNet, netip.MustParseAddr("172.30.0.20"))
	}
}

// TestW12eUpgradeLateForwardCheck: a forwarded address freed during the pull refuses the upgrade at the late check,
// before anything is journaled or stopped.
func TestW12eUpgradeLateForwardCheck(t *testing.T) {
	s := w12eUpSetup(t)
	w12eFreeAt(t, s, "PullImage")
	_, err := s.apply()
	coded := nwRefused(t, err, errcode.NPMReserved)
	if want := "proxy host 1 forwards to 172.30.0.20, an address darkpanel-npm could take on network app-net"; coded.Detail != want {
		t.Errorf("detail %q, want %q", coded.Detail, want)
	}
	if len(s.effects()) != 0 || s.phase() != "no row" || s.dock.counts["StopContainer"] != 0 {
		t.Error("the refused upgrade journaled an effect, recorded a phase or stopped NPM")
	}
}

// TestW12eStartVerifiesHeldAddresses: a forwarded address freed after the stop is taken by the new container at its
// start; the container leaves that network at once, the upgrade fails with npm.reserved and rolls back.
func TestW12eStartVerifiesHeldAddresses(t *testing.T) {
	t.Run("upgrade", func(t *testing.T) {
		s := w12eUpSetup(t)
		w12eFreeAt(t, s, "StopContainer")
		_, err := s.apply()
		if code := errcode.From(err).Code; code != errcode.NPMReserved {
			t.Fatalf("error %v with code %s, want %s", err, code, errcode.NPMReserved)
		}
		if !strings.Contains(err.Error(), "172.30.0.20") || !strings.Contains(err.Error(), "app-net") {
			t.Errorf("error %v does not name the address and the network", err)
		}
		if nwCalls(s.h, "DisconnectNetwork") == 0 {
			t.Error("the new container was not disconnected from app-net")
		}
		if got := s.phase(); got != "rolled_back" {
			t.Errorf("phase %q, want rolled_back", got)
		}
		u := s.upgrade()
		if u.Config.Targets == nil || len(*u.Config.Targets) == 0 {
			t.Errorf("the row recorded targets %v, want the late check's list", u.Config.Targets)
		}
	})
	t.Run("an upgrade whose new container holds no target", func(t *testing.T) {
		s := w12eUpSetup(t)
		if _, err := s.apply(); err != nil {
			t.Fatal(err)
		}
		if got := s.phase(); got != "done" || nwCalls(s.h, "DisconnectNetwork") != 0 {
			t.Errorf("phase %q after %d disconnects, want done and none", got, nwCalls(s.h, "DisconnectNetwork"))
		}
		if u := s.upgrade(); u.Config.Targets == nil {
			t.Error("the row recorded no target list; want an empty or full one, not none")
		}
	})
	t.Run("a hit on darkpanel-npm fails the upgrade", func(t *testing.T) {
		s := w12eUpSetup(t)
		s.h.docker.putContainer(thContainer("db2", thEndpoint(networkName, "172.20.0.30")))
		w12aSeed(s.h, "172.20.0.30", 5432)
		stopped := false
		s.dock.trace = func(call string) {
			switch {
			case call == "StopContainer" && !stopped:
				stopped = true
				if err := s.h.docker.RemoveContainer(context.Background(), "db2", dockerapi.RemoveContainerOptions{Force: true}); err != nil {
					t.Error(err)
				}
				s.h.docker.pin(networkName, netip.MustParseAddr("172.20.0.30"))
			case call == "StartContainer":
				// The new container was created with the pinned address; the restored original must not get it.
				s.h.docker.pin(networkName, netip.Addr{})
			}
		}
		_, err := s.apply()
		if err == nil || errcode.From(err).Code != errcode.NPMReserved || !strings.Contains(err.Error(), "it stays on darkpanel-npm") {
			t.Fatalf("error %v, want npm.reserved naming the hit on darkpanel-npm", err)
		}
		if nwCalls(s.h, "DisconnectNetwork") != 0 {
			t.Error("a network was disconnected; a hit on darkpanel-npm fails the step without a disconnect")
		}
		if got := s.phase(); got == "done" {
			t.Errorf("phase %q, want the upgrade failed and rolled back", got)
		}
	})
}

// TestW12eVerifyHeldFailures: the two hits that cannot be dropped fail the check for every caller. A hit on
// darkpanel-npm is not disconnected; a hit whose disconnect fails stops the container.
func TestW12eVerifyHeldFailures(t *testing.T) {
	ctx := context.Background()
	target := func(addr string) []forwardTarget {
		return []forwardTarget{{where: "proxy host 1", host: addr, addr: netip.MustParseAddr(addr)}}
	}
	t.Run("a hit on darkpanel-npm", func(t *testing.T) {
		s := w12aUpSetup(t)
		res, err := instanceUpgradeHandler{n: s.n}.verifyHeld(ctx, s.env, s.inst.ContainerID, target("172.20.0.2"))
		var coded *errcode.Error
		if !errors.As(err, &coded) || coded.Code != errcode.NPMReserved || core.IsDefinite(err) {
			t.Fatalf("error %v, want npm.reserved without the definite mark", err)
		}
		if want := "proxy host 1 forwards to 172.20.0.2, which darkpanel-npm took on network darkpanel-npm when it started; it stays on darkpanel-npm"; coded.Detail != want {
			t.Errorf("detail %q, want %q", coded.Detail, want)
		}
		if nwCalls(s.h, "DisconnectNetwork") != 0 || len(res.Dropped) != 0 {
			t.Errorf("%d disconnects, dropped %v; want darkpanel-npm kept", nwCalls(s.h, "DisconnectNetwork"), res.Dropped)
		}
	})
	t.Run("a disconnect that fails", func(t *testing.T) {
		s := w12aUpSetup(t)
		s.h.docker.Fail("DisconnectNetwork", errors.New("engine down"))
		_, err := instanceUpgradeHandler{n: s.n}.verifyHeld(ctx, s.env, s.inst.ContainerID, target("172.30.0.9"))
		if err == nil || !strings.Contains(err.Error(), "engine down") || core.IsDefinite(err) {
			t.Fatalf("error %v, want a failure that joins the disconnect error and is not definite", err)
		}
		if c, _ := s.h.docker.container(s.inst.ContainerID); c.State.Running {
			t.Error("the container still runs at a forwarded address it could not leave")
		}
	})
	t.Run("an ordinary hit", func(t *testing.T) {
		s := w12aUpSetup(t)
		res, err := instanceUpgradeHandler{n: s.n}.verifyHeld(ctx, s.env, s.inst.ContainerID, target("172.30.0.9"))
		if err != nil || res.Unverified != nil {
			t.Fatalf("error %v, unverified %v; want neither", err, res.Unverified)
		}
		if code := errcode.From(res.Hit).Code; code != errcode.NPMReserved || len(res.Dropped) != 1 || res.Dropped[0] != upAppNet {
			t.Errorf("hit %v (code %s), dropped %v; want npm.reserved and app-net", res.Hit, code, res.Dropped)
		}
	})
}
