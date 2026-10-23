package lifecycle

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var (
	sysTask = core.Principal{Actor: "system:gfx.sync", Scope: core.ScopeAdmin}
	sysRule = core.ActionRef{Name: "gfx.rule.set", Version: 1}
)

// sysFlag reads a change's core_changes.system.
func (f *fixture) sysFlag(changeID string) int {
	f.t.Helper()
	var system int
	if err := f.db.Read().QueryRow("SELECT system FROM core_changes WHERE id = ?", changeID).Scan(&system); err != nil {
		f.t.Fatal(err)
	}
	return system
}

func TestApplySystem(t *testing.T) {
	f, _ := newGfx(t)
	v, err := f.eng.ApplySystem(context.Background(), sysTask, sysRule, json.RawMessage(`{"name":"r1"}`))
	if err != nil || v.State != core.StateConfirmed || v.Actor != sysTask.Actor {
		t.Fatalf("ApplySystem = %+v, %v; want a confirmed change by %s", v, err, sysTask.Actor)
	}
	if got := f.sysFlag(v.ID); got != 1 {
		t.Errorf("system = %d, want 1", got)
	}
	if got, want := f.phases(), []string{"apply/ok", "confirm/ok"}; !reflect.DeepEqual(got, want) {
		t.Errorf("journal = %v, want %v", got, want)
	}
	entries, err := f.journal.Entries(context.Background(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Actor != sysTask.Actor || e.ChangeID != v.ID {
			t.Errorf("journal entry %s by %s for %s, want %s for %s", e.Phase, e.Actor, e.ChangeID, sysTask.Actor, v.ID)
		}
	}
}

func TestApplySystemRefuses(t *testing.T) {
	tests := []struct {
		name  string
		open  func(t *testing.T) *fixture
		ref   core.ActionRef
		input string
		plans int // the guard is seen only in a plan; the irreversible action is refused before one
		check func(t *testing.T, err error)
	}{
		{"failing guard", func(t *testing.T) *fixture { f, _ := newGfx(t); return f }, sysRule, `{"name":"r1","failing":["gfx.quota"]}`, 1, func(t *testing.T, err error) {
			wantCode(t, err, errcode.PlanGuardFailed)
			if detail := errcode.From(err).Detail; !strings.HasPrefix(detail, "guard gfx.quota failed: ") {
				t.Errorf("detail = %q, want it to start with %q", detail, "guard gfx.quota failed: ")
			}
		}},
		{"irreversible action", newFixture, refRemove, `{"name":"a"}`, 0, func(t *testing.T, err error) {
			if err == nil || !strings.Contains(err.Error(), "fix.item.remove") || !strings.Contains(err.Error(), "irreversible") {
				t.Errorf("err = %v, want a refusal naming the irreversible fix.item.remove", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := tt.open(t)
			_, err := f.eng.ApplySystem(context.Background(), sysTask, tt.ref, json.RawMessage(tt.input))
			tt.check(t, err)
			if n := f.count("SELECT count(*) FROM core_changes"); n != 0 {
				t.Errorf("%d changes, want none", n)
			}
			if n := f.count("SELECT count(*) FROM core_plans"); n != tt.plans {
				t.Errorf("%d plans, want %d", n, tt.plans)
			}
		})
	}
}

func TestOrdinaryApplyIsNotSystem(t *testing.T) {
	f, _ := newGfx(t)
	if _, err := f.eng.ApplySystem(context.Background(), sysTask, sysRule, json.RawMessage(`{"name":"r1"}`)); err != nil {
		t.Fatal(err)
	}
	pl := f.plan(alice, sysRule, `{"name":"r2"}`)
	v := f.apply(alice, pl.PlanID, core.ApplyOptions{ConfirmNow: true})
	if got := f.sysFlag(v.ID); got != 0 {
		t.Errorf("system of an operator's change = %d, want 0", got)
	}
}
