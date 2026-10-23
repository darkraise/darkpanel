package lifecycle

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestCheckGuards(t *testing.T) {
	def := core.ActionDef{Spec: core.ActionSpec{Name: "gfx.rule.set", Version: 1,
		Guards: []core.GuardSpec{{ID: "gfx.lockout", Overridable: true}, {ID: "gfx.quota"}}}}
	lockout := core.Guard{ID: "gfx.lockout", OK: true, Overridden: true, Message: "m"}
	for _, tc := range []struct {
		name      string
		def       core.ActionDef
		overrides []string
		guards    []core.Guard
		want      []core.Guard // nil: fails with internal
		diff      []core.DiffLine
	}{
		{"declared guards", def, nil, []core.Guard{{ID: "gfx.lockout", Message: "m"}, {ID: "gfx.quota", OK: true}},
			[]core.Guard{{ID: "gfx.lockout", Message: "m", Overridable: true}, {ID: "gfx.quota", OK: true}}, nil},
		{"an overridden guard", def, []string{"gfx.lockout"}, []core.Guard{lockout},
			[]core.Guard{{ID: "gfx.lockout", OK: true, Overridden: true, Overridable: true, Message: "m"}},
			[]core.DiffLine{{Op: " ", Text: "guard gfx.lockout overridden: m"}}},
		{"an undeclared guard", def, nil, []core.Guard{{ID: "gfx.other", OK: true}}, nil, nil},
		{"overridden but not overridable", def, []string{"gfx.quota"},
			[]core.Guard{{ID: "gfx.quota", OK: true, Overridden: true}}, nil, nil},
		{"overridden but not in overrides", def, nil, []core.Guard{lockout}, nil, nil},
		{"overridden and failing", def, []string{"gfx.lockout"},
			[]core.Guard{{ID: "gfx.lockout", Overridden: true}}, nil, nil},
		{"an action without guards", core.ActionDef{Spec: core.ActionSpec{Name: "fix.item.set", Version: 1}}, nil,
			[]core.Guard{{ID: "anything", Overridden: true}}, []core.Guard{{ID: "anything", Overridden: true}}, nil},
	} {
		out := core.PlanOutcome{Guards: tc.guards}
		err := checkGuards(tc.def, tc.overrides, &out)
		switch {
		case tc.want == nil:
			if errcode.From(err).Code != errcode.Internal {
				t.Errorf("%s: checkGuards = %v, want an internal error", tc.name, err)
			}
		case err != nil || !reflect.DeepEqual(out.Guards, tc.want) || !reflect.DeepEqual(out.Diff, tc.diff):
			t.Errorf("%s: checkGuards = %v, guards %+v, diff %+v; want guards %+v, diff %+v",
				tc.name, err, out.Guards, out.Diff, tc.want, tc.diff)
		}
	}
}

func TestPlanRefusesUnknownOverride(t *testing.T) {
	for _, tc := range []struct {
		overrides string
		field     string
	}{
		{`["gfx.quota"]`, "/overrides/0"},
		{`["nope"]`, "/overrides/0"},
		{`["gfx.lockout","nope"]`, "/overrides/1"},
	} {
		f, _ := newGfx(t)
		_, err := f.eng.Plan(context.Background(), alice, refGfxSet,
			json.RawMessage(`{"name":"a","overrides":`+tc.overrides+`}`), core.PlanOptions{})
		wantCode(t, err, errcode.InvalidInput)
		if fields := errcode.From(err).Fields; len(fields) != 1 || fields[0].Path != tc.field {
			t.Errorf("overrides %s: field errors %+v, want %s", tc.overrides, fields, tc.field)
		}
		if n := f.count("SELECT count(*) FROM core_plans"); n != 0 {
			t.Errorf("overrides %s: %d plans stored, want none", tc.overrides, n)
		}
	}
}

func TestPlanOverriddenGuard(t *testing.T) {
	f, _ := newGfx(t)
	pl := f.plan(alice, refGfxSet, `{"name":"a","failing":["gfx.lockout"],"overrides":["gfx.lockout"]}`)
	want := []core.Guard{
		{ID: "gfx.lockout", OK: true, Overridden: true, Overridable: true, Message: "gfx.lockout fails"},
		{ID: "gfx.quota", OK: true, Message: "gfx.quota holds"},
	}
	if !reflect.DeepEqual(pl.Guards, want) {
		t.Errorf("guards %+v\nwant %+v", pl.Guards, want)
	}
	if line := (core.DiffLine{Op: " ", Text: "guard gfx.lockout overridden: gfx.lockout fails"}); !slices.Contains(pl.Diff, line) {
		t.Errorf("diff %+v lacks %+v", pl.Diff, line)
	}
	var stored []core.Guard
	var raw string
	if err := f.db.Read().QueryRow("SELECT guards_json FROM core_plans WHERE id = ?", pl.PlanID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(raw), &stored); err != nil || !reflect.DeepEqual(stored, want) {
		t.Errorf("stored guards %s (%v), want %+v", raw, err, want)
	}
	v := f.apply(alice, pl.PlanID, core.ApplyOptions{})
	if v.State != core.StatePending {
		t.Fatalf("state %s, want pending", v.State)
	}
	if e := f.lastEntry(); e.Phase != phaseApply || !strings.Contains(string(e.Input), `"overrides":["gfx.lockout"]`) {
		t.Errorf("journal entry %s with input %s, want the apply recording the override", e.Phase, e.Input)
	}
}

func TestPlanFailingGuards(t *testing.T) {
	for _, tc := range []struct {
		failing string
		want    core.Guard
	}{
		{"gfx.quota", core.Guard{ID: "gfx.quota", Message: "gfx.quota fails"}},
		{"gfx.lockout", core.Guard{ID: "gfx.lockout", Overridable: true, Message: "gfx.lockout fails"}},
	} {
		f, _ := newGfx(t)
		pl := f.plan(alice, refGfxSet, `{"name":"a","failing":["`+tc.failing+`"]}`)
		if !slices.Contains(pl.Guards, tc.want) {
			t.Errorf("%s: guards %+v, want %+v among them", tc.failing, pl.Guards, tc.want)
		}
		_, err := f.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{})
		wantCode(t, err, errcode.PlanGuardFailed)
	}
}

func TestPlanUndeclaredGuard(t *testing.T) {
	f, _ := newGfx(t)
	_, err := f.eng.Plan(context.Background(), alice, refGfxSet, json.RawMessage(`{"name":"a","extra":["gfx.extra"]}`), core.PlanOptions{})
	if errcode.From(err).Code != errcode.Internal {
		t.Errorf("Plan = %v, want an internal error", err)
	}
	if n := f.count("SELECT count(*) FROM core_plans"); n != 0 {
		t.Errorf("%d plans stored, want none", n)
	}
	// An action without Guards may report any guard.
	if pl := newFixture(t).plan(alice, refSet, `{"name":"a","value":"1"}`); len(pl.Guards) != 1 || pl.Guards[0].ID != "not-blocked" {
		t.Errorf("fix.item.set guards %+v, want its undeclared not-blocked guard", pl.Guards)
	}
}

func TestRecheckUndeclaredGuard(t *testing.T) {
	f, h := newGfx(t)
	pl := f.plan(alice, refGfxSet, `{"name":"a"}`)
	h.setExtra("gfx.extra")
	_, err := f.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{})
	if errcode.From(err).Code != errcode.Internal {
		t.Errorf("Apply = %v, want an internal error", err)
	}
	if n := f.count("SELECT count(*) FROM core_changes"); n != 0 {
		t.Errorf("%d changes created, want none", n)
	}
}
