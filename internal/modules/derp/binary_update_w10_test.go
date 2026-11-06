package derp

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
)

// w10buHungUnits lets the first allow restarts through and hangs every later one until its context ends.
type w10buHungUnits struct {
	core.Units
	allow int
	ended chan error
}

func (u *w10buHungUnits) Restart(ctx context.Context, unit string) error {
	if u.allow > 0 {
		u.allow--
		return u.Units.Restart(ctx, unit)
	}
	<-ctx.Done()
	u.ended <- ctx.Err()
	return ctx.Err()
}

// TestW10BuRollbackIsBoundedWhenARestartHangs: the update's health check fails, the rollback's restart never
// answers, and the rollback ends when its window on the host's clock ends, with the update recorded as failed.
func TestW10BuRollbackIsBoundedWhenARestartHangs(t *testing.T) {
	s := buNewSetup(t)
	delete(s.h.tls, "127.0.0.1")
	hung := &w10buHungUnits{Units: s.units, allow: 1, ended: make(chan error, 1)}
	s.env.Units = hung
	start := s.h.clock.Now()
	_, err := s.run(t)
	coded := buCoded(t, err, errcode.DERPVerifyFailed)
	for _, want := range []string{"the rollback to " + buFrom + " failed", "derper needs repair"} {
		if !strings.Contains(coded.Detail, want) {
			t.Errorf("detail %q lacks %q", coded.Detail, want)
		}
	}
	select {
	case got := <-hung.ended:
		if !errors.Is(got, context.Canceled) {
			t.Errorf("the hung restart ended with %v, want the rollback's cancellation", got)
		}
	default:
		t.Fatal("the rollback's restart never ended")
	}
	floor := healthWindow + binaryRollbackWindow
	if waited := s.h.clock.Now().Sub(start); waited < floor || waited > floor+3*binaryUpdatePoll {
		t.Errorf("the task ended after %v, want the %v health window plus the %v rollback window", waited, healthWindow, binaryRollbackWindow)
	}
	if u := s.update(t); u.Phase != "failed" || !strings.Contains(u.Result, "context canceled") {
		t.Errorf("update row %+v, want phase failed with the cancelled restart as the result", u)
	}
	if string(s.h.files[server.BinaryPath]) != string(buOld) {
		t.Errorf("binary %q, want the old one, restored before the restart", s.h.files[server.BinaryPath])
	}
}

// TestW10BuHealthProbesAreBoundedLikeSTUN: every look at the server in the health check carries a deadline of at
// most binaryProbeWait, the limit stun.Probe applies when its context has none.
func TestW10BuHealthProbesAreBoundedLikeSTUN(t *testing.T) {
	s := buNewSetup(t)
	var bounds []time.Duration
	stun := s.d.d.stun
	s.d.d.stun = func(ctx context.Context, a netip.AddrPort) (time.Duration, error) {
		if d, ok := ctx.Deadline(); ok {
			bounds = append(bounds, time.Until(d))
		} else {
			bounds = append(bounds, 0)
		}
		return stun(ctx, a)
	}
	if _, err := s.run(t); err != nil {
		t.Fatal(err)
	}
	if len(bounds) == 0 {
		t.Fatal("the health check never asked STUN")
	}
	for _, b := range bounds {
		if b <= 0 || b > binaryProbeWait {
			t.Errorf("a STUN probe was bounded to %v, want more than 0 and at most %v", b, binaryProbeWait)
		}
	}
}

// TestW10BuInverseRunsAgainAfterAFailedRollback: an update whose rollback failed (phase failed) can be undone once
// the operator has put right what stopped it.
func TestW10BuInverseRunsAgainAfterAFailedRollback(t *testing.T) {
	ctx := context.Background()
	h, d := rcSetup(t)
	def := rcDef(t, d)
	rcLayout(h, "swapped")
	rcAdd(t, h, "c1", "failed", "derper.prev does not hold the previous binary")
	if err := setLocalBinary(ctx, h.db, rcTo, rcSum(rcNew), h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	var res core.InverseResult
	var err error
	rcDrive(t, h, func() {
		res, err = def.Inverse(ctx, h.env("c1"), core.Effect{ChangeID: "c1", Resource: rcResource, State: core.EffectUndoing})
	})
	if err != nil || res != (core.InverseResult{}) {
		t.Fatalf("inverse = %+v, %v; want an empty result", res, err)
	}
	rcRestored(t, h)
	if got := rcPhase(t, h, "c1"); got != "rolled_back" {
		t.Errorf("update phase %q, want rolled_back", got)
	}
}
