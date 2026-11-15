package npm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
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
		c, ok := s.h.docker.container(containerName)
		if !ok || !c.State.Running || c.Config.Image != upFromImage || c.NetworkSettings.Networks[upAppNet] != nil {
			t.Errorf("container %+v (found %v), want the original running without app-net", c, ok)
		}
		if want := "the started container held a forwarded address and left network app-net"; u.Result != want {
			t.Errorf("result %q, want %q", u.Result, want)
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
	t.Run("resume after a failed archive", func(t *testing.T) {
		s := w12eUpSetup(t)
		started := false
		s.dock.trace = func(call string) {
			if call != "StartContainer" || started {
				return
			}
			// The archive failed before any new container, so this start is resume's restart of the original; the
			// Engine hands it the address db freed.
			started = true
			if err := s.h.docker.RemoveContainer(context.Background(), "db", dockerapi.RemoveContainerOptions{Force: true}); err != nil {
				t.Error(err)
			}
			c, _ := s.h.docker.container(s.inst.ContainerID)
			c.NetworkSettings.Networks[upAppNet].IPAddress = netip.MustParseAddr("172.30.0.20")
			s.h.docker.putContainer(c)
		}
		s.dock.fail = func(call string, _ int) error {
			if strings.HasPrefix(call, "CopyFromContainer") {
				return errors.New("the archive was cut off")
			}
			return nil
		}
		_, err := s.apply()
		if err == nil || !strings.Contains(err.Error(), "the original instance was restored without network app-net") {
			t.Fatalf("error %v, want the restored original named without app-net", err)
		}
		c, _ := s.h.docker.container(s.inst.ContainerID)
		if !started || !c.State.Running || c.NetworkSettings.Networks[upAppNet] != nil {
			t.Errorf("container %+v (restarted %v), want the original running without app-net", c, started)
		}
		if want := "the started container held a forwarded address and left network app-net"; s.upgrade().Result != want {
			t.Errorf("result %q, want %q", s.upgrade().Result, want)
		}
	})
	t.Run("undo of a finished upgrade", func(t *testing.T) {
		f := urSetup(t)
		f.replace(urTo)
		w12eDone(t, f, w12eAppNet(), &[]recordedTarget{{Where: "proxy host 1", Host: "172.30.0.20", Addr: netip.MustParseAddr("172.30.0.20")}})
		f.h.docker.pin(upAppNet, netip.MustParseAddr("172.30.0.20"))
		if err := f.inverse(urResource); err != nil {
			t.Fatal(err)
		}
		f.restored()
		c, _ := f.h.docker.container(containerName)
		if c.NetworkSettings.Networks[upAppNet] != nil {
			t.Error("the restored original stayed on app-net at a forwarded address")
		}
	})
	t.Run("recovery rollback from recreating", func(t *testing.T) {
		f := urSetup(t)
		f.replace(urTo)
		w12eDone(t, f, w12eAppNet(), &[]recordedTarget{{Where: "proxy host 1", Host: "172.30.0.20", Addr: netip.MustParseAddr("172.30.0.20")}})
		if err := setUpgradePhase(context.Background(), f.h.db, urChange, "recreating", "", f.archives(), f.h.clock.Now()); err != nil {
			t.Fatal(err)
		}
		f.h.docker.pin(upAppNet, netip.MustParseAddr("172.30.0.20"))
		out, err := f.recover()
		if err != nil || out.State != core.RecoveryRolledBack {
			t.Fatalf("recovery %+v, error %v; want rolled back", out, err)
		}
		c, _ := f.h.docker.container(containerName)
		if !c.State.Running || c.NetworkSettings.Networks[upAppNet] != nil {
			t.Errorf("container %+v, want the original running without app-net", c)
		}
	})
	t.Run("restart of the original from a recorded list", func(t *testing.T) {
		f := urSetup(t)
		w12eDone(t, f, w12eAppNet(), &[]recordedTarget{{Where: "proxy host 1", Host: "172.30.0.20", Addr: netip.MustParseAddr("172.30.0.20")}})
		if err := setUpgradePhase(context.Background(), f.h.db, urChange, "stopped", "", nil, f.h.clock.Now()); err != nil {
			t.Fatal(err)
		}
		if err := f.h.docker.ConnectNetwork(context.Background(), upAppNet, f.inst.ContainerID, nwStatic("172.30.0.20")); err != nil {
			t.Fatal(err)
		}
		f.setRunning(f.inst.ContainerID, false)
		out, err := f.recover()
		if err != nil || out.State != core.RecoveryRolledBack {
			t.Fatalf("recovery %+v, error %v; want rolled back", out, err)
		}
		c, _ := f.h.docker.container(f.inst.ContainerID)
		if !c.State.Running || c.NetworkSettings.Networks[upAppNet] != nil {
			t.Errorf("container %+v, want the original running without app-net", c)
		}
	})
	t.Run("restart of the original from a row written before w12e", func(t *testing.T) {
		f := urSetup(t)
		w12eDone(t, f, w12eAppNet(), nil)
		if err := setUpgradePhase(context.Background(), f.h.db, urChange, "stopped", "", nil, f.h.clock.Now()); err != nil {
			t.Fatal(err)
		}
		if err := f.h.docker.ConnectNetwork(context.Background(), upAppNet, f.inst.ContainerID, nwStatic("172.30.0.20")); err != nil {
			t.Fatal(err)
		}
		w12aSeed(f.h, "172.30.0.20", 3000)
		f.setRunning(f.inst.ContainerID, false)
		out, err := f.recover()
		if err != nil || out.State != core.RecoveryRolledBack {
			t.Fatalf("recovery %+v, error %v; want rolled back", out, err)
		}
		c, _ := f.h.docker.container(f.inst.ContainerID)
		if c.NetworkSettings.Networks[upAppNet] != nil {
			t.Error("the original stayed on app-net at an address NPM's API names as forwarded")
		}
	})
	t.Run("a row written before w12e whose API does not answer", func(t *testing.T) {
		f := urSetup(t)
		w12eDone(t, f, w12eAppNet(), nil)
		if err := setUpgradePhase(context.Background(), f.h.db, urChange, "stopped", "", nil, f.h.clock.Now()); err != nil {
			t.Fatal(err)
		}
		f.setRunning(f.inst.ContainerID, false)
		w12eListing(f, func(context.Context, npmAPI) ([]npmapi.ProxyHost, error) {
			return nil, errors.New("npmapi: connection refused")
		})
		var out core.RecoveryOutcome
		var err error
		upDrive(t, f.h, func() { out, err = f.recover() })
		if err != nil || out.State != core.RecoveryNeedsRepair || !strings.Contains(out.Reason, "unverified") {
			t.Fatalf("recovery %+v, error %v; want needs repair naming the unverified start", out, err)
		}
		if c, _ := f.h.docker.container(f.inst.ContainerID); !c.State.Running {
			t.Error("the original was not left running")
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

// w12eDone records change urChange as a finished upgrade whose original container joined networks with the targets,
// with the instance row naming the upgraded container urNewID as a finished upgrade leaves it, and puts app-net with its
// subnet on the Engine.
func w12eDone(t *testing.T, f *urFixture, networks map[string]*dockerapi.EndpointSettings, targets *[]recordedTarget) {
	t.Helper()
	ctx := context.Background()
	f.h.docker.putNetwork(thNetwork(upAppNet, thSubnet("172.30.0.0/24", "172.30.0.1")))
	err := insertUpgrade(ctx, f.h.db, upgradeRow{ChangeID: urChange, FromImage: urFrom, ToImage: urTo,
		Config:    containerConfig{Create: instanceCreate(urFrom), Name: containerName, Networks: networks, Targets: targets},
		UpdatedAt: f.h.clock.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := setUpgradePhase(ctx, f.h.db, urChange, "done", "", f.archives(), f.h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	err = f.h.db.Write(ctx, func(tx *sql.Tx) error {
		row, _, err := loadInstance(ctx, tx)
		if err != nil {
			return err
		}
		row.ContainerID, row.Image = urNewID, urTo
		return saveInstance(ctx, tx, row)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func w12eAppNet() map[string]*dockerapi.EndpointSettings {
	return map[string]*dockerapi.EndpointSettings{upAppNet: {}}
}

// w12eListing makes NPM's listing call list.
func w12eListing(f *urFixture, list func(ctx context.Context, live npmAPI) ([]npmapi.ProxyHost, error)) {
	inner := f.n.d.api
	f.n.d.api = func(ctx context.Context, env core.Env) (npmAPI, error) {
		live, err := inner(ctx, env)
		if err != nil {
			return nil, err
		}
		return w12dListingAPI{npmAPI: live, list: func(ctx context.Context) ([]npmapi.ProxyHost, error) { return list(ctx, live) }}, nil
	}
}

func TestW12eUpgradeUndoChecksForwards(t *testing.T) {
	t.Run("a free address of a network the upgraded container left", func(t *testing.T) {
		f := urSetup(t)
		f.replace(urTo)
		w12eDone(t, f, w12eAppNet(), &[]recordedTarget{})
		w12aSeed(f.h, "172.30.0.20", 3000)
		mark := f.mark()
		sawPull := false
		w12eListing(f, func(ctx context.Context, live npmAPI) ([]npmapi.ProxyHost, error) {
			// The original image is made available by an inspection of it, or a pull when it is missing; either comes
			// after the mark and before this listing.
			since := f.callsSince(mark)
			sawPull = urCalled(since, "InspectImage") || urCalled(since, "PullImage")
			return live.ProxyHosts(ctx)
		})
		coded := nwRefused(t, f.inverse(urResource), errcode.NPMReserved)
		if want := "proxy host 1 forwards to 172.30.0.20, an address darkpanel-npm could take on network app-net"; coded.Detail != want {
			t.Errorf("detail %q, want %q", coded.Detail, want)
		}
		if !sawPull {
			t.Error("the forwards were listed before the original image was made available")
		}
		if c, ok := f.h.docker.container(urNewID); !ok || !c.State.Running || f.phase() != "done" {
			t.Errorf("container %s found %v running %v in phase %q; want the upgraded one running and the change done", urNewID, ok, c.State.Running, f.phase())
		}
	})
	t.Run("a network of the original container no longer exists", func(t *testing.T) {
		f := urSetup(t)
		f.replace(urTo)
		w12eDone(t, f, map[string]*dockerapi.EndpointSettings{"gone-net": {}}, &[]recordedTarget{})
		mark := f.mark()
		err := f.inverse(urResource)
		coded := nwRefused(t, err, errcode.NotFound)
		if want := "network gone-net of the original container no longer exists"; coded.Detail != want {
			t.Errorf("detail %q, want %q", coded.Detail, want)
		}
		if urCalled(f.callsSince(mark), "RemoveContainer") || f.phase() != "done" {
			t.Errorf("the refused undo removed a container or changed phase done to %q", f.phase())
		}
	})
	refusedUnchanged := func(t *testing.T, f *urFixture, mark int) {
		t.Helper()
		if urCalled(f.callsSince(mark), "RemoveContainer") || f.phase() != "done" {
			t.Errorf("the refused undo removed a container or changed phase done to %q", f.phase())
		}
		if c, ok := f.h.docker.container(urNewID); !ok || !c.State.Running {
			t.Errorf("container %s found %v running %v; want the upgraded one running", urNewID, ok, c.State.Running)
		}
	}
	t.Run("the inspection of darkpanel-npm fails", func(t *testing.T) {
		f := urSetup(t)
		f.replace(urTo)
		w12eDone(t, f, w12eAppNet(), &[]recordedTarget{})
		mark := f.mark()
		f.h.docker.Fail("InspectContainer", dockerapi.ErrUnavailable)
		err := f.inverse(urResource)
		nwRefused(t, err, errcode.DockerEngineUnavailable)
		if !strings.Contains(err.Error(), "inspect container "+containerName) {
			t.Errorf("error %q does not name the inspection of %s", err, containerName)
		}
		refusedUnchanged(t, f, mark)
	})
	t.Run("the pull of the original image fails", func(t *testing.T) {
		f := urSetup(t)
		f.replace(urTo)
		w12eDone(t, f, w12eAppNet(), &[]recordedTarget{})
		mark := f.mark()
		f.h.docker.Fail("InspectImage", &dockerapi.APIError{StatusCode: 404, Message: "no such image: " + urFrom})
		f.h.docker.Fail("PullImage", dockerapi.ErrUnavailable)
		err := f.inverse(urResource)
		nwRefused(t, err, errcode.DockerEngineUnavailable)
		if !strings.Contains(err.Error(), "pull "+urFrom) {
			t.Errorf("error %q does not name the pull of %s", err, urFrom)
		}
		refusedUnchanged(t, f, mark)
	})
	t.Run("NPM's API does not answer", func(t *testing.T) {
		f := urSetup(t)
		f.replace(urTo)
		w12eDone(t, f, w12eAppNet(), &[]recordedTarget{})
		w12eListing(f, func(context.Context, npmAPI) ([]npmapi.ProxyHost, error) {
			return nil, errors.New("npmapi: connection refused")
		})
		coded := nwRefused(t, f.inverse(urResource), errcode.NPMUnhealthy)
		if !strings.HasSuffix(coded.Detail, "; start Nginx Proxy Manager and retry, or remove and redeploy the instance") {
			t.Errorf("detail %q does not say how to get out", coded.Detail)
		}
		if f.phase() != "done" {
			t.Errorf("phase %q, want done", f.phase())
		}
	})
	t.Run("a retry after the container was removed uses the recorded list", func(t *testing.T) {
		f := urSetup(t)
		f.replace("")
		w12eDone(t, f, w12eAppNet(), &[]recordedTarget{{Where: "proxy host 1", Host: "172.30.0.20", Addr: netip.MustParseAddr("172.30.0.20")}})
		w12eListing(f, func(context.Context, npmAPI) ([]npmapi.ProxyHost, error) {
			return nil, fmt.Errorf("npmapi: the API is down")
		})
		coded := nwRefused(t, f.inverse(urResource), errcode.NPMReserved)
		if want := "proxy host 1 forwards to 172.30.0.20, an address darkpanel-npm could take on network app-net"; coded.Detail != want {
			t.Errorf("detail %q, want %q", coded.Detail, want)
		}
	})
	t.Run("a retry with an empty recorded list restores the original", func(t *testing.T) {
		f := urSetup(t)
		f.replace("")
		w12eDone(t, f, w12eAppNet(), &[]recordedTarget{})
		w12eListing(f, func(context.Context, npmAPI) ([]npmapi.ProxyHost, error) {
			return nil, fmt.Errorf("npmapi: the API is down")
		})
		if err := f.inverse(urResource); err != nil {
			t.Fatal(err)
		}
		f.restored()
	})
}

// TestW12eVerifyHeldNames: forwarded names are resolved again after the start, and a failed inspection or lookup
// reports the check unverified while keeping the disconnects already made.
func TestW12eVerifyHeldNames(t *testing.T) {
	ctx := context.Background()
	// The name answered another address when the targets were read.
	name := forwardTarget{where: "proxy host 2", host: "up.invalid", name: true, addr: netip.MustParseAddr("203.0.113.9")}
	literal := forwardTarget{where: "proxy host 1", host: "172.30.0.9", addr: netip.MustParseAddr("172.30.0.9")}
	onAppNet := func(t *testing.T, s *upSetup) bool {
		t.Helper()
		c, _ := s.h.docker.container(s.inst.ContainerID)
		return c.NetworkSettings.Networks[upAppNet] != nil
	}
	t.Run("a name that now answers a held address", func(t *testing.T) {
		s := w12aUpSetup(t)
		s.n.d.resolve = w12eAnswer("172.30.0.9")
		res, err := instanceUpgradeHandler{n: s.n}.verifyHeld(ctx, s.env, s.inst.ContainerID, []forwardTarget{name})
		if err != nil || res.Unverified != nil {
			t.Fatalf("error %v, unverified %v; want neither", err, res.Unverified)
		}
		coded := errcode.From(res.Hit)
		if want := "proxy host 2 forwards to 172.30.0.9, which darkpanel-npm took on network app-net when it started; it was disconnected from app-net"; coded.Code != errcode.NPMReserved || coded.Detail != want {
			t.Errorf("hit %v, want npm.reserved with detail %q", res.Hit, want)
		}
		if len(res.Dropped) != 1 || res.Dropped[0] != upAppNet || onAppNet(t, s) {
			t.Errorf("dropped %v, still on app-net %v; want app-net left", res.Dropped, onAppNet(t, s))
		}
	})
	t.Run("a lookup that fails", func(t *testing.T) {
		s := w12aUpSetup(t)
		s.n.d.resolve = func(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("the resolver is down") }
		res, err := instanceUpgradeHandler{n: s.n}.verifyHeld(ctx, s.env, s.inst.ContainerID, []forwardTarget{literal, name})
		if err != nil || res.Unverified == nil {
			t.Fatalf("error %v, unverified %v; want no error and the check unverified", err, res.Unverified)
		}
		if errcode.From(res.Hit).Code != errcode.NPMReserved || len(res.Dropped) != 1 || res.Dropped[0] != upAppNet || onAppNet(t, s) {
			t.Errorf("hit %v, dropped %v; want the literal's hit kept and app-net left", res.Hit, res.Dropped)
		}
	})
	t.Run("an inspection that fails", func(t *testing.T) {
		s := w12aUpSetup(t)
		s.h.docker.Fail("InspectContainer", errors.New("engine down"))
		res, err := instanceUpgradeHandler{n: s.n}.verifyHeld(ctx, s.env, s.inst.ContainerID, []forwardTarget{literal})
		if err != nil || res.Unverified == nil {
			t.Fatalf("error %v, unverified %v; want no error and the check unverified", err, res.Unverified)
		}
		if res.Hit != nil || len(res.Dropped) != 0 || nwCalls(s.h, "DisconnectNetwork") != 0 {
			t.Errorf("hit %v, dropped %v after %d disconnects; want nothing compared", res.Hit, res.Dropped, nwCalls(s.h, "DisconnectNetwork"))
		}
	})
}

// w12eHalfRestored puts a created, never started original under the NPM container's name, holding addr on app-net, as
// an undo cut off between the restore's create and its start leaves the host; NPM's API answers only while the
// container of that name runs.
func w12eHalfRestored(f *urFixture, addr string) {
	f.h.docker.putContainer(thContainer(containerName, thEndpoint(upAppNet, addr), func(c *dockerapi.ContainerJSON) {
		c.ID, c.Image, c.Config.Image = "orig2", urFrom, urFrom
	}))
	w12eListing(f, func(ctx context.Context, live npmAPI) ([]npmapi.ProxyHost, error) {
		if c, ok := f.h.docker.container(containerName); !ok || !c.State.Running {
			return nil, errors.New("npmapi: connection refused")
		}
		return live.ProxyHosts(ctx)
	})
}

// TestW12eUndoRetryAfterTheUpgradedContainerLeft: a retry of a cut-off undo checks the recorded list whenever the
// container named darkpanel-npm is not the upgraded one, and a row written before w12e is not checked before the
// restore; neither is refused for NPM's API being down.
func TestW12eUndoRetryAfterTheUpgradedContainerLeft(t *testing.T) {
	held := &[]recordedTarget{{Where: "proxy host 1", Host: "172.30.0.20", Addr: netip.MustParseAddr("172.30.0.20")}}
	t.Run("a half-restored original and a recorded list", func(t *testing.T) {
		f := urSetup(t)
		f.replace("")
		w12eDone(t, f, w12eAppNet(), held)
		w12eHalfRestored(f, "172.30.0.20")
		// The address the half-restored original holds counts as free: the restore removes that container.
		coded := nwRefused(t, f.inverse(urResource), errcode.NPMReserved)
		if want := "proxy host 1 forwards to 172.30.0.20, an address darkpanel-npm could take on network app-net"; coded.Detail != want {
			t.Errorf("detail %q, want %q", coded.Detail, want)
		}
		if f.phase() != "done" {
			t.Errorf("phase %q, want done", f.phase())
		}
	})
	t.Run("a half-restored original and an empty recorded list", func(t *testing.T) {
		f := urSetup(t)
		f.replace("")
		w12eDone(t, f, w12eAppNet(), &[]recordedTarget{})
		w12eHalfRestored(f, "172.30.0.20")
		if err := f.inverse(urResource); err != nil {
			t.Fatal(err)
		}
		f.restored()
	})
	for _, tc := range []struct {
		name string
		half bool
	}{
		{"a row written before w12e with a half-restored original", true},
		{"a row written before w12e with no container", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := urSetup(t)
			f.replace("")
			w12eDone(t, f, w12eAppNet(), nil)
			w12eHalfRestored(f, "172.30.0.20")
			if !tc.half {
				if err := f.h.docker.RemoveContainer(context.Background(), "orig2", dockerapi.RemoveContainerOptions{Force: true}); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.inverse(urResource); err != nil {
				t.Fatal(err)
			}
			f.restored()
		})
	}
}

// TestW12eRestoreSavesTheRowBeforeVerifying: a restore whose verification fails with the original left running
// still records that container in the instance row.
func TestW12eRestoreSavesTheRowBeforeVerifying(t *testing.T) {
	names := func(t *testing.T, f *urFixture) {
		t.Helper()
		row, _, err := loadInstance(context.Background(), f.h.db.Read())
		if err != nil {
			t.Fatal(err)
		}
		c, ok := f.h.docker.container(containerName)
		if !ok || !c.State.Running || row.ContainerID != c.ID || row.Image != urFrom {
			t.Errorf("the row names %s running %s; want the running original %s (found %v, running %v)", row.ContainerID, row.Image, c.ID, ok, c.State.Running)
		}
	}
	recreating := func(t *testing.T, f *urFixture) {
		t.Helper()
		if err := setUpgradePhase(context.Background(), f.h.db, urChange, "recreating", "", f.archives(), f.h.clock.Now()); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("a hit on darkpanel-npm", func(t *testing.T) {
		f := urSetup(t)
		f.replace(urTo)
		w12eDone(t, f, w12eAppNet(), &[]recordedTarget{{Where: "proxy host 1", Host: "172.20.0.50", Addr: netip.MustParseAddr("172.20.0.50")}})
		recreating(t, f)
		f.h.docker.pin(networkName, netip.MustParseAddr("172.20.0.50"))
		out, err := f.recover()
		if err != nil || out.State != core.RecoveryNeedsRepair || !strings.Contains(out.Reason, "it stays on darkpanel-npm") {
			t.Fatalf("recovery %+v, error %v; want needs repair naming the hit on darkpanel-npm", out, err)
		}
		names(t, f)
	})
	t.Run("a row written before w12e whose API does not answer", func(t *testing.T) {
		f := urSetup(t)
		f.replace(urTo)
		w12eDone(t, f, w12eAppNet(), nil)
		recreating(t, f)
		w12eListing(f, func(context.Context, npmAPI) ([]npmapi.ProxyHost, error) {
			return nil, errors.New("npmapi: connection refused")
		})
		var out core.RecoveryOutcome
		var err error
		upDrive(t, f.h, func() { out, err = f.recover() })
		if err != nil || out.State != core.RecoveryNeedsRepair || !strings.Contains(out.Reason, "unverified") {
			t.Fatalf("recovery %+v, error %v; want needs repair naming the unverified start", out, err)
		}
		names(t, f)
	})
}
