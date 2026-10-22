package lifecycle

import (
	"context"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var (
	opA = ReservationOwner("01OPA")
	opB = ReservationOwner("01OPB")
)

func (f *fixture) reserve(owner string) error {
	f.t.Helper()
	return Reserve(context.Background(), f.db, ReserveAll, owner)
}

func TestReservationOwner(t *testing.T) {
	if got := ReservationOwner("01OP"); got != "op:01OP" {
		t.Errorf("ReservationOwner = %q, want op:01OP", got)
	}
}

func TestReserveTakesTheKey(t *testing.T) {
	f := newFixture(t)
	if err := f.reserve(opA); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := f.reserve(opA); err != nil {
		t.Fatalf("second Reserve by the same owner: %v", err)
	}
	if n := f.count("SELECT count(*) FROM core_locks WHERE key = ? AND owner = ?", ReserveAll, opA); n != 1 {
		t.Errorf("reservation rows = %d, want 1", n)
	}
	if n := f.count("SELECT count(*) FROM core_locks"); n != 1 {
		t.Errorf("lock rows = %d, want 1", n)
	}
}

func TestReserveRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *fixture)
		want  errcode.Code
	}{
		{"another operation holds every lock", func(f *fixture) {
			if err := f.reserve(opB); err != nil {
				f.t.Fatal(err)
			}
		}, errcode.UpdateInProgress},
		{"a change holds a lock", func(f *fixture) {
			if _, err := f.db.Writer().Exec("INSERT INTO core_locks (key, owner) VALUES ('fix', '01CHANGE')"); err != nil {
				f.t.Fatal(err)
			}
		}, errcode.UpdateUnconfirmedChanges},
		{"a revertible change is pending", func(f *fixture) {
			f.pending(`{"name":"a","value":"1"}`)
		}, errcode.UpdateUnconfirmedChanges},
		{"a pending revertible change holds no lock", func(f *fixture) {
			v := f.pending(`{"name":"a","value":"1"}`)
			if _, err := f.db.Writer().Exec("DELETE FROM core_locks WHERE owner = ?", v.ID); err != nil {
				f.t.Fatal(err)
			}
		}, errcode.UpdateUnconfirmedChanges},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			c.setup(f)
			before := f.count("SELECT count(*) FROM core_locks")
			wantCode(t, f.reserve(opA), c.want)
			if n := f.count("SELECT count(*) FROM core_locks WHERE owner = ?", opA); n != 0 {
				t.Errorf("refused Reserve left %d rows of its owner", n)
			}
			if n := f.count("SELECT count(*) FROM core_locks"); n != before {
				t.Errorf("lock rows = %d, want %d", n, before)
			}
		})
	}
}

func TestReserveAfterTheChangeFinishes(t *testing.T) {
	f := newFixture(t)
	f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	if err := f.reserve(opA); err != nil {
		t.Fatalf("Reserve after a completed change: %v", err)
	}
}

func TestReservationRefusesEveryLock(t *testing.T) {
	t.Run("planned before the reservation", func(t *testing.T) {
		f := newFixture(t)
		pl := f.plan(alice, refAdd, `{"name":"a","value":"1"}`)
		if err := f.reserve(opA); err != nil {
			t.Fatal(err)
		}
		_, err := f.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{})
		wantCode(t, err, errcode.LockHeld)
	})
	t.Run("planned after the reservation", func(t *testing.T) {
		f := newFixture(t)
		if err := f.reserve(opA); err != nil {
			t.Fatal(err)
		}
		pl := f.plan(alice, refAdd, `{"name":"a","value":"1"}`)
		_, err := f.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{})
		wantCode(t, err, errcode.LockHeld)
	})
	t.Run("reserved after the lock check passed", func(t *testing.T) {
		f := newFixture(t)
		pl := f.plan(alice, refSet, `{"name":"a","value":"1"}`)
		// Snapshot runs after the lock check and before the locks are taken.
		f.host.onSnapshot = func() {
			if err := f.reserve(opA); err != nil {
				t.Error(err)
			}
		}
		_, err := f.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{})
		wantCode(t, err, errcode.LockHeld)
		if n := f.count("SELECT count(*) FROM core_changes"); n != 0 {
			t.Errorf("changes = %d, want none", n)
		}
	})
	t.Run("released", func(t *testing.T) {
		f := newFixture(t)
		if err := f.reserve(opA); err != nil {
			t.Fatal(err)
		}
		if err := Release(context.Background(), f.db, opA); err != nil {
			t.Fatal(err)
		}
		if v := f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{}); v.State != core.StateCompleted {
			t.Errorf("state after the release = %s, want completed", v.State)
		}
	})
}

func TestReadsRunWhileReserved(t *testing.T) {
	f := newFixture(t)
	if err := f.reserve(opA); err != nil {
		t.Fatal(err)
	}
	if _, err := f.eng.Read(context.Background(), alice, refGet, []byte(`{"name":"a"}`)); err != nil {
		t.Errorf("Read while reserved: %v", err)
	}
}

func TestReleaseDeletesOnlyItsOwnersRows(t *testing.T) {
	f := newFixture(t)
	if err := f.reserve(opA); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Writer().Exec("INSERT INTO core_locks (key, owner) VALUES ('other', ?)", opB); err != nil {
		t.Fatal(err)
	}
	if err := Release(context.Background(), f.db, opA); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if n := f.count("SELECT count(*) FROM core_locks WHERE owner = ?", opA); n != 0 {
		t.Errorf("rows of the released owner = %d, want 0", n)
	}
	if n := f.count("SELECT count(*) FROM core_locks WHERE owner = ?", opB); n != 1 {
		t.Errorf("rows of the other owner = %d, want 1", n)
	}
}

func TestRecoverKeepsAReservation(t *testing.T) {
	f := newFixture(t)
	if err := f.reserve(opA); err != nil {
		t.Fatal(err)
	}
	f.restart()
	f.recoverAll()
	if n := f.count("SELECT count(*) FROM core_locks WHERE owner = ?", opA); n != 1 {
		t.Errorf("reservation rows after recovery = %d, want 1", n)
	}
}

// Reserve and an apply race for the lock table. Reserve starts from the
// snapshot hook, after the apply's lock check and just before it takes its
// locks, so either may reach the database writer first; the writer serialises
// them, exactly one wins, and the loser leaves nothing behind.
func TestReserveRacesAnApply(t *testing.T) {
	for range 50 {
		f := newFixture(t)
		pl := f.plan(alice, refSet, `{"name":"a","value":"1"}`)
		var wg sync.WaitGroup
		var reserveErr error
		f.host.onSnapshot = func() {
			wg.Add(1)
			go func() {
				defer wg.Done()
				reserveErr = Reserve(context.Background(), f.db, ReserveAll, opA)
			}()
		}
		_, applyErr := f.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{})
		wg.Wait()
		switch {
		case reserveErr == nil && applyErr == nil:
			t.Fatal("both the reservation and the apply succeeded")
		case reserveErr == nil:
			wantCode(t, applyErr, errcode.LockHeld)
			if n := f.count("SELECT count(*) FROM core_changes"); n != 0 {
				t.Fatalf("changes = %d after a refused apply, want 0", n)
			}
		case applyErr == nil:
			wantCode(t, reserveErr, errcode.UpdateUnconfirmedChanges)
			if n := f.count("SELECT count(*) FROM core_locks WHERE owner = ?", opA); n != 0 {
				t.Fatalf("reservation rows = %d after a refused Reserve, want 0", n)
			}
		default:
			t.Fatalf("both failed: reserve %v, apply %v", reserveErr, applyErr)
		}
	}
}

func TestReserveNamesTheFirstHeldLock(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *fixture) string // returns the detail Reserve must give
	}{
		{"a change holds it", func(f *fixture) string {
			id := f.recoverSeed("", refAdd, core.ClassCompensable, core.StateNeedsRepair, "")
			f.recoverExec("INSERT INTO core_locks (key, owner) VALUES ('zz', ?), ('fix', ?)", id, id)
			return "lock fix is held by change " + id + ", which is needs_repair; wait for it to finish"
		}},
		{"an owner that is not a change", func(f *fixture) string {
			f.recoverExec("INSERT INTO core_locks (key, owner) VALUES ('fix', '01CHANGE')")
			return "lock fix is held by 01CHANGE; wait for it to finish"
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			want := c.setup(f)
			err := f.reserve(opA)
			wantCode(t, err, errcode.UpdateUnconfirmedChanges)
			// core.update.apply fails with this error, and the rollout
			// scheduler shows the failed change's error message as the
			// host's error.
			if got := f.eng.changeError(refAdd, err).Message; got != want {
				t.Errorf("the message a refused update shows = %q, want %q", got, want)
			}
		})
	}
}
