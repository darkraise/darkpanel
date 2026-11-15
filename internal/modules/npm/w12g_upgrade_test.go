package npm

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const w12gUnansweredDetail = "location /x of proxy host 1 forwards to gone.invalid, which has no answer"

func TestW12gUpgradeRefusesAnUnansweredStaticForward(t *testing.T) {
	ctx := context.Background()
	s := w12aUpSetup(t)
	w12aSeed(s.h, "app.example.net", 8080, w12gLoc("/x", "gone.invalid"))
	s.n.d.resolve = w12eNoAnswer
	_, err := s.n.instanceUpgrade().Handler.Plan(ctx, s.h.env(""), json.RawMessage(`{}`))
	if coded := nwRefused(t, err, errcode.NPMForwardUnanswered); coded.Detail != w12gUnansweredDetail {
		t.Errorf("plan detail %q, want %q", coded.Detail, w12gUnansweredDetail)
	}
	_, err = s.apply()
	if coded := nwRefused(t, err, errcode.NPMForwardUnanswered); coded.Detail != w12gUnansweredDetail {
		t.Errorf("apply detail %q, want %q", coded.Detail, w12gUnansweredDetail)
	}
	if len(s.effects()) != 0 || s.dock.counts["StopContainer"] != 0 || len(s.dock.created) != 0 {
		t.Error("a refused upgrade journaled an effect, stopped NPM or created a container")
	}
}

// TestW12gUpgradeChecksTheForwardsAgainAfterThePull: a static forward name that answers at the first check and loses
// its answer during the pull is refused by the second check, before NPM is stopped.
func TestW12gUpgradeChecksTheForwardsAgainAfterThePull(t *testing.T) {
	s := w12aUpSetup(t)
	w12aSeed(s.h, "app.example.net", 8080, w12gLoc("/x", "gone.invalid"))
	pulled := false
	s.dock.missing[Image] = true // the pinned image must be pulled
	s.dock.trace = func(call string) {
		if call == "PullImage" {
			pulled, s.dock.missing[Image] = true, false
		}
	}
	s.n.d.resolve = func(ctx context.Context, host string) ([]netip.Addr, error) {
		if pulled {
			return w12eNoAnswer(ctx, host)
		}
		return []netip.Addr{netip.MustParseAddr("203.0.113.9")}, nil
	}
	_, err := s.apply()
	if !pulled {
		t.Fatal("the upgrade did not pull the image")
	}
	if coded := nwRefused(t, err, errcode.NPMForwardUnanswered); coded.Detail != w12gUnansweredDetail {
		t.Errorf("apply detail %q, want %q", coded.Detail, w12gUnansweredDetail)
	}
	if s.dock.counts["StopContainer"] != 0 || len(s.dock.created) != 0 {
		t.Error("an upgrade refused after the pull stopped NPM or created a container")
	}
}

func TestW12gUpgradeAllowsAnUnansweredDefaultForward(t *testing.T) {
	s := w12aUpSetup(t)
	w12aSeed(s.h, "gone.invalid", 8080)
	s.n.d.resolve = w12eNoAnswer
	if _, err := s.apply(); err != nil {
		t.Fatalf("an unanswered default forward host refused the upgrade: %v", err)
	}
}

func TestW12gUndoOfAFinishedUpgradeRefusesAnUnansweredStaticForward(t *testing.T) {
	f := urSetup(t)
	f.replace(urTo)
	w12eDone(t, f, w12eAppNet(), nil)
	w12aSeed(f.h, "app.example.net", 8080, w12gLoc("/x", "gone.invalid"))
	f.n.d.resolve = w12eNoAnswer
	mark := f.mark()
	err := f.inverse(urResource)
	if coded := nwRefused(t, err, errcode.NPMForwardUnanswered); coded.Detail != w12gUnansweredDetail {
		t.Errorf("detail %q, want %q", coded.Detail, w12gUnansweredDetail)
	}
	if urCalled(f.callsSince(mark), "RemoveContainer") || f.phase() != "done" {
		t.Errorf("the refused undo removed a container or changed phase done to %q", f.phase())
	}
	if c, ok := f.h.docker.container(urNewID); !ok || !c.State.Running {
		t.Errorf("container %s found %v running %v; want the upgraded one running", urNewID, ok, c.State.Running)
	}
}

// w12gStartHook runs at before each StartContainer reaches the test Engine.
type w12gStartHook struct {
	*thDocker
	at func()
}

func (d w12gStartHook) StartContainer(ctx context.Context, id string) (bool, error) {
	d.at()
	return d.thDocker.StartContainer(ctx, id)
}

func TestW12gAnUnverifiedStartIsWrittenToTheRow(t *testing.T) {
	ctx := context.Background()
	f := urSetup(t)
	f.replace(urTo)
	recorded := recordedTarget{Where: "proxy host 1", Host: "up.invalid", Name: true, Addr: netip.MustParseAddr("203.0.113.9")}
	w12eDone(t, f, w12eAppNet(), &[]recordedTarget{recorded})
	var failing atomic.Bool
	f.n.d.resolve = func(context.Context, string) ([]netip.Addr, error) {
		if failing.Load() {
			return nil, &net.DNSError{Err: "server misbehaving", Name: "up.invalid", IsTemporary: true}
		}
		return []netip.Addr{netip.MustParseAddr("203.0.113.9")}, nil
	}
	f.n.d.docker = w12gStartHook{f.h.docker, func() { failing.Store(true) }}
	err := f.inverse(urResource)
	var coded *errcode.Error
	if errors.As(err, &coded) && coded.Code == errcode.NPMReserved {
		t.Fatalf("an unverified start was reported as a hit: %v", err)
	}
	if f.phase() == "failed" {
		t.Error("an unverified start turned the undo into a failed rollback")
	}
	row, ok, lerr := loadUpgrade(ctx, f.h.db.Read(), urChange)
	if lerr != nil || !ok {
		t.Fatalf("the upgrade row: %v %v", ok, lerr)
	}
	if !strings.Contains(row.Result, "the started container's addresses were unverified") {
		t.Errorf("the row's result %q does not say the started container's addresses were unverified", row.Result)
	}
	if c, ok := f.h.docker.container(containerName); !ok || !c.State.Running {
		t.Error("NPM is not running after an unverified start")
	}
}

func TestW12gAnUnverifiedStartOfARollbackOrARecovery(t *testing.T) {
	ctx := context.Background()
	recorded := recordedTarget{Where: "proxy host 1", Host: "up.invalid", Name: true, Addr: netip.MustParseAddr("203.0.113.9")}
	const unverified = "the started container's addresses were unverified"
	lookups := func(failing *atomic.Bool) func(context.Context, string) ([]netip.Addr, error) {
		return func(context.Context, string) ([]netip.Addr, error) {
			if failing.Load() {
				return nil, &net.DNSError{Err: "server misbehaving", Name: "up.invalid", IsTemporary: true}
			}
			return []netip.Addr{netip.MustParseAddr("203.0.113.9")}, nil
		}
	}
	t.Run("recovery rollback from recreating", func(t *testing.T) {
		f := urSetup(t)
		f.replace(urTo)
		w12eDone(t, f, w12eAppNet(), &[]recordedTarget{recorded})
		if err := setUpgradePhase(ctx, f.h.db, urChange, "recreating", "", f.archives(), f.h.clock.Now()); err != nil {
			t.Fatal(err)
		}
		var failing atomic.Bool
		f.n.d.resolve = lookups(&failing)
		f.n.d.docker = w12gStartHook{f.h.docker, func() { failing.Store(true) }}
		out, err := f.recover()
		if err != nil || out.State != core.RecoveryRolledBack {
			t.Fatalf("recovery %+v, error %v; want rolled back", out, err)
		}
		row, _, _ := loadUpgrade(ctx, f.h.db.Read(), urChange)
		if !strings.Contains(row.Result, unverified) || row.Phase == "failed" {
			t.Errorf("phase %q result %q, want a rolled-back row whose result says %q", row.Phase, row.Result, unverified)
		}
	})
	t.Run("restart of the original from a recorded list", func(t *testing.T) {
		f := urSetup(t)
		w12eDone(t, f, w12eAppNet(), &[]recordedTarget{recorded})
		if err := setUpgradePhase(ctx, f.h.db, urChange, "stopped", "", nil, f.h.clock.Now()); err != nil {
			t.Fatal(err)
		}
		f.setRunning(f.inst.ContainerID, false)
		var failing atomic.Bool
		f.n.d.resolve = lookups(&failing)
		f.n.d.docker = w12gStartHook{f.h.docker, func() { failing.Store(true) }}
		out, err := f.recover()
		if err != nil || out.State != core.RecoveryRolledBack {
			t.Fatalf("recovery %+v, error %v; want rolled back", out, err)
		}
		row, _, _ := loadUpgrade(ctx, f.h.db.Read(), urChange)
		if !strings.Contains(row.Result, unverified) {
			t.Errorf("result %q, want it to say %q", row.Result, unverified)
		}
	})
	t.Run("an upgrade that fails anyway names it in the error", func(t *testing.T) {
		s := w12eUpSetup(t)
		w12aSeed(s.h, "up.invalid", 8080)
		var failing atomic.Bool
		s.n.d.resolve = lookups(&failing)
		s.dock.trace = func(call string) {
			if call == "StartContainer" {
				failing.Store(true)
			}
		}
		s.dock.fail = func(call string, _ int) error {
			if strings.HasPrefix(call, "CopyFromContainer") {
				return errors.New("the archive was cut off")
			}
			return nil
		}
		_, err := s.apply()
		if err == nil || !strings.Contains(err.Error(), unverified) {
			t.Errorf("error %v, want it to name %q", err, unverified)
		}
	})
}
