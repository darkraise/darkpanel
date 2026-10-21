package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// journalOf applies jfx.item.modify of item "a" locally, so its change has
// effect 1 made, and returns the change id and an Env whose journal is that
// change's.
func (f *jfixture) journalOf() (string, core.Env) {
	f.t.Helper()
	v := f.apply(alice, f.plan(alice, refJModify, `{"name":"a","value":"1","local":true}`).PlanID, core.ApplyOptions{})
	if v.State != core.StateCompleted {
		f.t.Fatalf("state = %s, want completed", v.State)
	}
	def, _ := f.reg.Action(refJModify)
	return v.ID, f.eng.changeEnv(alice, def, v.ID, core.CallContext{})
}

func modifyEffect(name string) core.Effect {
	return core.Effect{
		Kind: core.EffectModify, Resource: jIdentity(name), Local: true,
		Before: core.StateDoc{State: json.RawMessage(`{"value":"0"}`)},
		After:  &core.StateDoc{State: json.RawMessage(`{"value":"1"}`)},
	}
}

func TestEffectJournalTransitions(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	ctx := context.Background()
	id, env := f.journalOf()
	post := core.StateDoc{State: json.RawMessage(`{"value":"1"}`)}

	made, err := env.Effects.Intend(ctx, modifyEffect("b"), nil)
	if err != nil || made != 2 {
		t.Fatalf("Intend = %d, %v; want seq 2", made, err)
	}
	if err := env.Effects.Made(ctx, made, jIdentity("b"), post); err != nil {
		t.Fatalf("Made: %v", err)
	}
	skipped, err := env.Effects.Intend(ctx, modifyEffect("c"), nil)
	if err != nil || skipped != 3 {
		t.Fatalf("Intend = %d, %v; want seq 3", skipped, err)
	}
	if err := env.Effects.Skipped(ctx, skipped); err != nil {
		t.Fatalf("Skipped: %v", err)
	}
	if got, want := f.effectStates(id), []string{"1:made", "2:made", "3:skipped"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %v, want %v", got, want)
	}

	illegal := []struct {
		name string
		call func() error
	}{
		{"made twice", func() error { return env.Effects.Made(ctx, made, jIdentity("b"), post) }},
		{"skipped after made", func() error { return env.Effects.Skipped(ctx, made) }},
		{"made after skipped", func() error { return env.Effects.Made(ctx, skipped, jIdentity("c"), post) }},
		{"skipped twice", func() error { return env.Effects.Skipped(ctx, skipped) }},
		{"unknown seq", func() error { return env.Effects.Skipped(ctx, 9) }},
	}
	for _, c := range illegal {
		t.Run(c.name, func(t *testing.T) {
			before := f.effects(id)
			if err := c.call(); err == nil {
				t.Fatal("the transition was accepted")
			}
			if after := f.effects(id); !reflect.DeepEqual(after, before) {
				t.Errorf("a refused transition wrote: %+v, was %+v", after, before)
			}
		})
	}
}

func TestEffectJournalMadeChecksItsArguments(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	ctx := context.Background()
	_, env := f.journalOf()
	seq, err := env.Effects.Intend(ctx, modifyEffect("b"), nil)
	if err != nil {
		t.Fatal(err)
	}
	post := core.StateDoc{State: json.RawMessage(`{"value":"1"}`)}
	for name, call := range map[string]func() error{
		"resource without identity": func() error { return env.Effects.Made(ctx, seq, "jfx.item", post) },
		"resource not yet created":  func() error { return env.Effects.Made(ctx, seq, "jfx.item:?b", post) },
		"post not JSON": func() error {
			return env.Effects.Made(ctx, seq, jIdentity("b"), core.StateDoc{State: json.RawMessage(`{`)})
		},
	} {
		if err := call(); err == nil {
			t.Errorf("%s: Made accepted it", name)
		}
	}
}

func TestIntendValidatesTheEffect(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	ctx := context.Background()
	_, env := f.journalOf()
	create := func() core.Effect {
		return core.Effect{
			Kind: core.EffectCreate, Resource: "jfx.item:?n", Recovery: &core.RecoveryID{Kind: "name", Value: "n"},
			Before: core.StateDoc{State: json.RawMessage("null")},
		}
	}
	cases := []struct {
		name string
		edit func(*core.Effect)
		want string
	}{
		{"unknown kind", func(e *core.Effect) { e.Kind = "rename" }, "effect kind"},
		{"resource without identity", func(e *core.Effect) { e.Resource = "jfx.item" }, "<module>.<noun>:<identity>"},
		{"resource noun with a dash", func(e *core.Effect) { e.Resource = "jfx.it-em:?n" }, "<module>.<noun>:<identity>"},
		{"creation without recovery", func(e *core.Effect) { e.Recovery = nil }, "declares its Recovery"},
		{"modify with recovery", func(e *core.Effect) { e.Kind, e.Resource = core.EffectModify, "jfx.item:n" }, "only a creation"},
		{"creation resource not its recovery", func(e *core.Effect) { e.Resource = "jfx.item:?m" }, "jfx.item:?<recovery value>"},
		{"creation resource already named", func(e *core.Effect) { e.Resource = "jfx.item:n" }, "jfx.item:?<recovery value>"},
		{"modify of an object not yet created", func(e *core.Effect) {
			e.Kind, e.Resource, e.Recovery = core.EffectModify, "jfx.item:?n", nil
		}, "not yet created"},
		{"negative commit wait", func(e *core.Effect) { e.CommitWait = -time.Second }, "negative CommitWait"},
		{"unknown recovery kind", func(e *core.Effect) { e.Recovery.Kind = "guess" }, "recovery identity"},
		{"empty inverse recovery value", func(e *core.Effect) { e.InverseRecovery = &core.RecoveryID{Kind: "name"} }, "recovery identity"},
		{"before without state", func(e *core.Effect) { e.Before = core.StateDoc{} }, "Before.State"},
		{"before version not JSON", func(e *core.Effect) { e.Before.Version = json.RawMessage(`{`) }, "Before.Version"},
		{"creation before not null", func(e *core.Effect) { e.Before.State = json.RawMessage(`{"value":"0"}`) }, `{"state": null}`},
		{"after not JSON", func(e *core.Effect) { e.After = &core.StateDoc{State: json.RawMessage(`[`)} }, "After.State"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eff := create()
			c.edit(&eff)
			_, err := env.Effects.Intend(ctx, eff, nil)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Intend error = %v, want one containing %q", err, c.want)
			}
		})
	}
	if seq, err := env.Effects.Intend(ctx, create(), nil); err != nil || seq != 2 {
		t.Errorf("a valid creation: Intend = %d, %v; want seq 2", seq, err)
	}
}

func TestIntendNeedsAnObserver(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	id, _ := f.journalOf()
	def, _ := f.reg.Action(refAdd) // fix.item.add has no Observer
	env := f.eng.changeEnv(alice, def, id, core.CallContext{})
	_, err := env.Effects.Intend(context.Background(), modifyEffect("b"), nil)
	if err == nil || !strings.Contains(err.Error(), "has no Observer") {
		t.Fatalf("Intend error = %v, want the missing Observer", err)
	}
	if n := f.count("SELECT count(*) FROM core_effects WHERE change_id = ?", id); n != 1 {
		t.Errorf("effects = %d, want only the apply's", n)
	}
}

func TestIntendRecordsCommitBy(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	ctx := context.Background()
	id, env := f.journalOf()
	remote := modifyEffect("b")
	remote.Local, remote.CommitWait = false, 10*time.Minute
	seq, err := env.Effects.Intend(ctx, remote, nil)
	if err != nil {
		t.Fatal(err)
	}
	effs := f.effects(id)
	if want := f.clock.Now().Add(10 * time.Minute); !effs[seq-1].CommitBy.Equal(want) || effs[seq-1].CommitWait != 10*time.Minute {
		t.Errorf("remote effect CommitBy = %v, CommitWait = %v; want %v and 10m", effs[seq-1].CommitBy, effs[seq-1].CommitWait, want)
	}
	if !effs[0].CommitBy.IsZero() {
		t.Errorf("local effect CommitBy = %v, want none", effs[0].CommitBy)
	}
}

func TestEffectSecretSealing(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	ctx := context.Background()
	id, env := f.journalOf()
	first, err := env.Effects.Intend(ctx, modifyEffect("b"), []byte("token-b"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := env.Effects.Intend(ctx, modifyEffect("c"), []byte("token-c"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := env.Effects.Secret(ctx, first); err != nil || string(got) != "token-b" {
		t.Errorf("Secret(%d) = %q, %v; want token-b", first, got, err)
	}
	var raw []byte
	if err := f.db.Read().QueryRow("SELECT secret_ct FROM core_effects WHERE change_id = ? AND seq = ?", id, first).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "token-b") {
		t.Error("the secret is stored in clear")
	}
	// A ciphertext moved to another effect no longer opens: the associated
	// data binds it to its change and seq.
	if _, err := f.db.Writer().Exec("UPDATE core_effects SET secret_ct = ? WHERE change_id = ? AND seq = ?", raw, id, second); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Effects.Secret(ctx, second); err == nil {
		t.Error("a secret sealed for another effect opened")
	}
	if _, err := env.Effects.Secret(ctx, 1); err == nil {
		t.Error("Secret of an effect without one succeeded")
	}
}

func TestTerminalEffectClearsItsSecret(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	ctx := context.Background()
	id, env := f.journalOf()
	kept, err := env.Effects.Intend(ctx, modifyEffect("b"), []byte("kept"))
	if err != nil {
		t.Fatal(err)
	}
	if err := env.Effects.Made(ctx, kept, jIdentity("b"), core.StateDoc{State: json.RawMessage(`{"value":"1"}`)}); err != nil {
		t.Fatal(err)
	}
	gone, err := env.Effects.Intend(ctx, modifyEffect("c"), []byte("gone"))
	if err != nil {
		t.Fatal(err)
	}
	if err := env.Effects.Skipped(ctx, gone); err != nil {
		t.Fatal(err)
	}
	if got, err := env.Effects.Secret(ctx, kept); err != nil || string(got) != "kept" {
		t.Errorf("made effect secret = %q, %v; want it kept", got, err)
	}
	if n := f.count("SELECT count(*) FROM core_effects WHERE change_id = ? AND seq = ? AND secret_ct IS NULL", id, gone); n != 1 {
		t.Error("a skipped effect kept its secret")
	}
}

func TestSetEffectFollowsTheStateTable(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	ctx := context.Background()
	id, _ := f.journalOf()
	set := func(from []core.EffectState, u effectUpdate) error {
		return f.db.Write(ctx, func(tx *sql.Tx) error { return setEffect(ctx, tx, f.clock.Now(), id, 1, from, u) })
	}
	made := []core.EffectState{core.EffectMade}
	undoTo := core.StateDoc{State: json.RawMessage(`null`)}
	refused := []struct {
		name string
		from []core.EffectState
		u    effectUpdate
	}{
		{"not in from", []core.EffectState{core.EffectIntended}, effectUpdate{State: core.EffectSkipped}},
		{"made to skipped", made, effectUpdate{State: core.EffectSkipped}},
		{"made to accepted", made, effectUpdate{State: core.EffectAccepted}},
		{"unresolved without a reason", made, effectUpdate{State: core.EffectUnresolved}},
		{"undoing with a reason", made, effectUpdate{State: core.EffectUndoing, Reason: core.ReasonConflict}},
	}
	for _, c := range refused {
		if err := set(c.from, c.u); err == nil {
			t.Errorf("%s: setEffect accepted it", c.name)
		}
	}
	if err := set(made, effectUpdate{State: core.EffectUndoing, UndoTo: &undoTo}); err != nil {
		t.Fatalf("made to undoing: %v", err)
	}
	if err := set([]core.EffectState{core.EffectUndoing}, effectUpdate{State: core.EffectUnresolved, Reason: core.ReasonUncertain,
		Current: &core.StateDoc{State: json.RawMessage(`{"value":"x"}`)}}); err != nil {
		t.Fatalf("undoing to unresolved: %v", err)
	}
	eff := f.effects(id)[0]
	if eff.State != core.EffectUnresolved || eff.Reason != core.ReasonUncertain || !stateEqual(*eff.UndoTo, undoTo) ||
		string(eff.Current.State) != `{"value":"x"}` || eff.Post == nil {
		t.Errorf("effect = %+v, want unresolved/uncertain keeping UndoTo and Post and recording Current", eff)
	}
}

func TestEffectsListInSeqOrder(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	ctx := context.Background()
	_, env := f.journalOf()
	for _, name := range []string{"b", "c", "d"} {
		if _, err := env.Effects.Intend(ctx, modifyEffect(name), nil); err != nil {
			t.Fatal(err)
		}
	}
	effs, err := env.Effects.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range effs {
		got = append(got, e.Resource)
	}
	if want := []string{"jfx.item:a", "jfx.item:b", "jfx.item:c", "jfx.item:d"}; !reflect.DeepEqual(got, want) {
		t.Errorf("List = %v, want %v", got, want)
	}
}

func TestCanonicalJSON(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"b":1,"a":2}`, `{"a":2,"b":1}`},
		{` { "a" : [ 1.0 , 1e2, 2.50 ] } `, `{"a":[1,100,2.5]}`},
		{`{"z":{"y":true,"x":null}}`, `{"z":{"x":null,"y":true}}`},
		{``, `null`},
	}
	for _, c := range cases {
		got, err := canonicalJSON(json.RawMessage(c.in))
		if err != nil || string(got) != c.want {
			t.Errorf("canonicalJSON(%q) = %s, %v; want %s", c.in, got, err, c.want)
		}
	}
	if _, err := canonicalJSON(json.RawMessage(`{`)); err == nil {
		t.Error("canonicalJSON accepted invalid JSON")
	}
}

func TestStateDocComparisons(t *testing.T) {
	doc := func(state, version string) core.StateDoc {
		return core.StateDoc{State: json.RawMessage(state), Version: json.RawMessage(version)}
	}
	cases := []struct {
		name         string
		a, b         core.StateDoc
		equal, state bool
	}{
		{"same", doc(`{"v":1}`, `{"r":1}`), doc(`{ "v" : 1.0 }`, `{"r":1}`), true, true},
		{"version differs", doc(`{"v":1}`, `{"r":1}`), doc(`{"v":1}`, `{"r":2}`), false, true},
		{"state differs", doc(`{"v":1}`, `{"r":1}`), doc(`{"v":2}`, `{"r":1}`), false, false},
		{"no version and null version", doc(`null`, ``), doc(`null`, `null`), true, true},
		{"invalid JSON", doc(`{`, ``), doc(`{`, ``), false, false},
	}
	for _, c := range cases {
		if got := docEqual(c.a, c.b); got != c.equal {
			t.Errorf("%s: docEqual = %v, want %v", c.name, got, c.equal)
		}
		if got := stateEqual(c.a, c.b); got != c.state {
			t.Errorf("%s: stateEqual = %v, want %v", c.name, got, c.state)
		}
	}
	before := doc(`{"v":1}`, `{"r":7}`)
	if got := undoTo(core.Effect{Kind: core.EffectModify, Before: before}); string(got.State) != `{"v":1}` || got.Version != nil {
		t.Errorf("undoTo(modify) = %+v, want Before without Version", got)
	}
	if got := undoTo(core.Effect{Kind: core.EffectCreate, Before: before}); string(got.State) != "null" || got.Version != nil {
		t.Errorf("undoTo(create) = %+v, want {\"state\": null}", got)
	}
}

func TestHandlersGetTheChangeJournal(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	v := f.apply(alice, f.plan(alice, refJSet, `{"name":"a","value":"1","local":true}`).PlanID, core.ApplyOptions{})
	if got := f.effectStates(v.ID); !reflect.DeepEqual(got, []string{"1:made"}) {
		t.Errorf("effects = %v, want the apply's effect made", got)
	}
	if _, err := f.eng.Revert(context.Background(), alice, v.ID); err != nil {
		t.Fatal(err)
	}
	for _, call := range []string{"Apply", "Verify", "Revert"} {
		got := f.store.calls(call)
		if len(got) != 1 || got[0].ChangeID != v.ID || !got[0].Journal {
			t.Errorf("%s saw %+v, want change %s with its journal", call, got, v.ID)
		}
	}
	if got := f.store.calls("Plan"); len(got) == 0 || got[0].ChangeID != "" || got[0].Journal {
		t.Errorf("Plan saw %+v, want no change and no journal", got)
	}
}

func TestIntentIsDurableBeforeTheCall(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	pl := f.plan(alice, refJModify, `{"name":"a","value":"1","hangApply":true}`)
	go func() {
		if _, err := f.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{}); err != nil {
			t.Error(err)
		}
	}()
	f.store.waitEntered(t, "a")
	id := f.changeOf(pl.PlanID)
	if got := f.effectStates(id); !reflect.DeepEqual(got, []string{"1:intended"}) {
		t.Errorf("effects while the call waits = %v, want 1:intended committed", got)
	}
	f.store.open("a")
	f.waitState(id, core.StateCompleted)
}

func TestEffectView(t *testing.T) {
	commitBy := time.Date(2026, 10, 21, 12, 2, 0, 0, time.UTC)
	post := core.StateDoc{State: json.RawMessage(`{"value":"1"}`)}
	eff := core.Effect{
		ChangeID: "c1", Seq: 2, Kind: core.EffectModify, Resource: "jfx.item:a", InverseLocal: true,
		CommitBy: commitBy, Before: core.StateDoc{State: json.RawMessage(`{"value":"0"}`)}, Post: &post,
		State: core.EffectUnresolved, Reason: core.ReasonConflict,
	}
	v := effectView(eff)
	if v.ChangeID != "c1" || v.Seq != 2 || v.Kind != core.EffectModify || v.Resource != "jfx.item:a" || v.Local ||
		!v.InverseLocal || v.CommitBy == nil || !v.CommitBy.Equal(commitBy) || v.State != core.EffectUnresolved ||
		v.Reason != core.ReasonConflict || v.Post != &post || v.ResolvedAt != nil {
		t.Errorf("effectView = %+v", v)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"changeId":"c1"`, `"commitBy":"2026-10-21T12:02:00Z"`, `"resolvedAt":null`, `"undoTo":null`} {
		if !strings.Contains(string(b), field) {
			t.Errorf("view JSON %s lacks %s", b, field)
		}
	}
}
