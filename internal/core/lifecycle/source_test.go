package lifecycle

import (
	"context"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestJournalEntriesCarryTheSource(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	bob := core.Principal{Actor: "user:bob", UserID: "bob", Scope: core.ScopeOperate, Source: "192.0.2.7"}

	confirmed := f.plan(alice, refSet, `{"name":"a","value":"1"}`).PlanID
	v, err := f.eng.Apply(ctx, alice, confirmed, core.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.eng.Confirm(ctx, bob, v.ID); err != nil {
		t.Fatal(err)
	}
	reverted := f.plan(alice, refSet, `{"name":"b","value":"1"}`).PlanID
	if v, err = f.eng.Apply(ctx, alice, reverted, core.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.eng.Revert(ctx, bob, v.ID); err != nil {
		t.Fatal(err)
	}
	failed := f.plan(alice, refAdd, `{"name":"c","value":"1","failApply":true}`).PlanID
	if _, err := f.eng.Apply(ctx, alice, failed, core.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	timed := f.plan(alice, refSet, `{"name":"d","value":"1"}`).PlanID
	if _, err := f.eng.Apply(ctx, alice, timed, core.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(DefaultConfirmTimeout)
	left := f.plan(alice, refSet, `{"name":"e","value":"1"}`).PlanID
	if _, err := f.eng.Apply(ctx, alice, left, core.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	f.restart()
	f.recoverAll()

	entries, err := f.journal.Entries(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Phase+"/"+e.Actor+"/"+e.Source)
	}
	want := []string{
		"apply/user:alice/test", "confirm/user:bob/192.0.2.7",
		"apply/user:alice/test", "revert/user:bob/192.0.2.7",
		"apply/user:alice/test",
		"apply/user:alice/test", "revert/system:revert-timer/",
		"apply/user:alice/test", "recover/system:recovery/",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("journal phase/actor/source =\n%v\nwant\n%v", got, want)
	}
}
