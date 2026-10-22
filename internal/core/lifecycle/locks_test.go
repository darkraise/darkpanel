package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// lockEpoch returns the stored epoch of key, 0 when it has none.
func (f *fixture) lockEpoch(key string) int64 {
	f.t.Helper()
	var epoch int64
	err := f.db.Read().QueryRow("SELECT epoch FROM core_lock_epochs WHERE key = ?", key).Scan(&epoch)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		f.t.Fatal(err)
	}
	return epoch
}

// lockRecorded returns the lock epochs change id recorded at its intent.
func (f *fixture) lockRecorded(id string) map[string]int64 {
	f.t.Helper()
	epochs, err := recordedEpochs(context.Background(), f.db.Read(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return epochs
}

func (f *fixture) lockWindow(id string) string {
	f.t.Helper()
	key, err := windowOpen(context.Background(), f.db.Read(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return key
}

// lockedChange stores a completed change id whose intent took keys, as
// begin's intent transaction takes them, and releases its locks.
func (f *fixture) lockedChange(id string, keys ...string) {
	f.t.Helper()
	ctx := context.Background()
	now := formatTime(f.clock.Now())
	err := f.db.Write(ctx, func(tx *sql.Tx) error {
		epochs, err := takeLocks(ctx, tx, id, keys, true)
		if err != nil {
			return err
		}
		recorded, err := json.Marshal(epochs)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash, diff_json,
			 guards_json, confirmation, created_at, expires_at) VALUES (?, '', 'fix.item.add', 1, '', '', '', '[]', '[]', '', ?, ?)`,
			"plan-"+id, now, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state,
			 input_json, lock_epochs, created_at, updated_at)
			 VALUES (?, ?, '', 'fix.item.add', 1, 'compensable', '', '', 'completed', '{}', ?, ?, ?)`,
			id, "plan-"+id, string(recorded), now, now); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM core_locks WHERE owner = ?", id)
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
}

func TestIntentBumpsLockEpochs(t *testing.T) {
	f := newFixture(t)
	first := f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	second := f.apply(alice, f.plan(alice, refAdd, `{"name":"b","value":"1"}`).PlanID, core.ApplyOptions{})
	if got := f.lockRecorded(first.ID); !reflect.DeepEqual(got, map[string]int64{"fix": 1}) {
		t.Errorf("first change recorded %v, want fix 1", got)
	}
	if got := f.lockRecorded(second.ID); !reflect.DeepEqual(got, map[string]int64{"fix": 2}) {
		t.Errorf("second change recorded %v, want fix 2", got)
	}
	if f.lockWindow(first.ID) != "fix" || f.lockWindow(second.ID) != "" {
		t.Errorf("windows = %q, %q; want the first closed by fix and the second open", f.lockWindow(first.ID), f.lockWindow(second.ID))
	}
}

func TestAKeyListedTwiceBumpsOnce(t *testing.T) {
	f := newFixture(t)
	v := f.apply(alice, f.plan(alice, refRemove, `{"name":"a"}`).PlanID, core.ApplyOptions{Confirmation: "a"})
	if got := f.lockRecorded(v.ID); !reflect.DeepEqual(got, map[string]int64{"fix": 1}) || f.lockEpoch("fix") != 1 {
		t.Errorf("recorded %v, stored epoch %d; want fix 1 once", got, f.lockEpoch("fix"))
	}
}

func TestChangesThatLeftNothingGiveTheirEpochBack(t *testing.T) {
	cases := []struct {
		name  string
		ref   core.ActionRef
		input string
		end   func(f *fixture, id string)
		state string
	}{
		{"failed with nothing made", refAdd, `{"name":"b","value":"2","failApply":true}`, nil, core.StateFailed},
		{"failed apply of a revertible change", refSet, `{"name":"b","value":"2","failApply":true}`, nil, core.StateReverted},
		{"reverted by the operator", refSet, `{"name":"b","value":"2"}`, func(f *fixture, id string) {
			if _, err := f.eng.Revert(context.Background(), alice, id); err != nil {
				f.t.Fatal(err)
			}
		}, core.StateReverted},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			earlier := f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
			later := f.apply(alice, f.plan(alice, c.ref, c.input).PlanID, core.ApplyOptions{})
			if c.end != nil {
				if f.lockWindow(earlier.ID) != "fix" {
					t.Fatalf("earlier window = %q while the later change holds fix, want closed", f.lockWindow(earlier.ID))
				}
				c.end(f, later.ID)
			}
			if s := f.state(later.ID); s != c.state {
				t.Fatalf("later change is %s, want %s", s, c.state)
			}
			if f.lockEpoch("fix") != 1 || f.lockWindow(earlier.ID) != "" {
				t.Errorf("epoch = %d, earlier window = %q; want 1 and open again", f.lockEpoch("fix"), f.lockWindow(earlier.ID))
			}
		})
	}
}

func TestAFailedChangeThatMadeAnEffectKeepsItsEpoch(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	earlier := f.apply(alice, f.plan(alice, refJModify, `{"name":"a","value":"1","local":true}`).PlanID, core.ApplyOptions{})
	later := f.apply(alice, f.plan(alice, refJModify, `{"name":"a","value":"2","local":true,"failAfterCall":true}`).PlanID, core.ApplyOptions{})
	if later.State != core.StateFailed {
		t.Fatalf("later change is %s, want failed", later.State)
	}
	if f.lockEpoch("jfx") != 2 || f.lockWindow(earlier.ID) != "jfx" {
		t.Errorf("epoch = %d, earlier window = %q; want 2 and closed", f.lockEpoch("jfx"), f.lockWindow(earlier.ID))
	}
}

func TestWindowOpenNamesTheMovedKey(t *testing.T) {
	f := newFixture(t)
	f.lockedChange("c1", "a", "b")
	if f.lockWindow("c1") != "" {
		t.Fatalf("window = %q, want open", f.lockWindow("c1"))
	}
	f.lockedChange("c2", "b")
	if got := f.lockWindow("c1"); got != "b" {
		t.Errorf("window = %q, want closed by b", got)
	}
	f.lockedChange("c3", "a")
	if got := f.lockWindow("c1"); got != "a" {
		t.Errorf("window = %q, want the first moved key in key order, a", got)
	}
	if _, err := windowOpen(context.Background(), f.db.Read(), "nope"); err == nil {
		t.Error("windowOpen of a missing change succeeded")
	}
}

func TestTakeLocksWithoutBumpLeavesEpochs(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.lockedChange("c1", "a")
	err := f.db.Write(ctx, func(tx *sql.Tx) error {
		epochs, err := takeLocks(ctx, tx, "c1", []string{"a"}, false)
		if epochs != nil {
			t.Errorf("epochs = %v, want nil without bump", epochs)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.lockEpoch("a") != 1 || f.lockWindow("c1") != "" || f.count("SELECT count(*) FROM core_locks WHERE owner = 'c1'") != 1 {
		t.Errorf("epoch = %d, window = %q; want the lock taken and the epoch unchanged", f.lockEpoch("a"), f.lockWindow("c1"))
	}
}

func TestGiveBackOnlyWhileTheEpochIsTheRecordedOne(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.lockedChange("c1", "g")
	f.lockedChange("c2", "g")
	giveBack := func(id string) {
		if err := f.db.Write(ctx, func(tx *sql.Tx) error { return giveBackEpochs(ctx, tx, id) }); err != nil {
			t.Fatal(err)
		}
	}
	giveBack("c1")
	if f.lockEpoch("g") != 2 {
		t.Errorf("epoch = %d after giving back a superseded change, want 2", f.lockEpoch("g"))
	}
	giveBack("c2")
	if f.lockEpoch("g") != 1 || f.lockWindow("c1") != "" {
		t.Errorf("epoch = %d, c1 window = %q; want 1 and open", f.lockEpoch("g"), f.lockWindow("c1"))
	}
}
