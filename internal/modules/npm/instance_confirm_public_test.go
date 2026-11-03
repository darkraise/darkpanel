package npm

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func stConfirm(h *thHost, n nm, id string) (PublicMark, error) {
	out, err := n.instanceConfirmPublic().Handler.Apply(context.Background(), h.env(id), json.RawMessage(`{}`))
	var m PublicMark
	if err == nil {
		if err := json.Unmarshal(out, &m); err != nil {
			h.t.Fatalf("output %s: %v", out, err)
		}
	}
	return m, err
}

func stDefinite(t *testing.T, err error, code errcode.Code) {
	t.Helper()
	var coded *errcode.Error
	if err == nil || !core.IsDefinite(err) || !errors.As(err, &coded) || coded.Code != code {
		t.Errorf("error %v, want a Definite %s", err, code)
	}
}

func stRow(t *testing.T, h *thHost) instanceRow {
	t.Helper()
	row, ok, err := loadInstance(context.Background(), h.db.Read())
	if err != nil || !ok {
		t.Fatalf("instance row: %v, %v", ok, err)
	}
	return row
}

func TestStConfirmSpec(t *testing.T) {
	def := newTHHost(t).nm().instanceConfirmPublic()
	s := def.Spec
	if s.Name != "npm.instance.confirm-public" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"npm"}) || s.LongRunning || s.Available != nil || s.Summary == "" || s.MinScope != "" {
		t.Errorf("spec %+v", s)
	}
	h := def.Handler
	if h.Plan == nil || h.Apply == nil || h.Verify == nil || h.Observe == nil || h.Inverse == nil {
		t.Error("the handler lacks a stage")
	}
}

func TestStConfirmPlan(t *testing.T) {
	ctx := context.Background()
	plan := func(h *thHost) (core.PlanOutcome, error) {
		return h.nm().instanceConfirmPublic().Handler.Plan(ctx, h.env(""), json.RawMessage(`{}`))
	}
	t.Run("a deployed host", func(t *testing.T) {
		h := newTHHost(t)
		h.deployed()
		out, err := plan(h)
		if err != nil {
			t.Fatal(err)
		}
		if want := []core.DiffLine{{Op: "-", Text: "public: no"}, {Op: "+", Text: "public: confirmed"}}; !slices.Equal(out.Diff, want) {
			t.Errorf("diff %+v, want %+v", out.Diff, want)
		}
		again, _ := plan(h)
		if len(out.StateHash) != 64 || out.StateHash != again.StateHash {
			t.Errorf("state hashes %q and %q, want a stable 64-digit hash", out.StateHash, again.StateHash)
		}
		stSetRow(t, h, func(r *instanceRow) { r.ContainerID = thID("another container") })
		if moved, _ := plan(h); moved.StateHash == out.StateHash {
			t.Error("the hash ignores which instance is marked")
		}
	})
	t.Run("no instance", func(t *testing.T) {
		_, err := plan(newTHHost(t))
		stDefinite(t, err, errcode.NotFound)
	})
	t.Run("already confirmed", func(t *testing.T) {
		h := newTHHost(t)
		h.deployed()
		stSetRow(t, h, func(r *instanceRow) { r.Public = "confirmed" })
		_, err := plan(h)
		stDefinite(t, err, errcode.InvalidInput)
	})
}

func TestStConfirmApply(t *testing.T) {
	ctx := context.Background()
	h := newTHHost(t)
	n := h.nm()
	h.deployed()
	before := stRow(t, h)
	h.clock.Advance(time.Minute)
	mark, err := stConfirm(h, n, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if mark != (PublicMark{Public: "confirmed"}) {
		t.Errorf("output %+v", mark)
	}
	row := stRow(t, h)
	want := before
	want.Public, want.UpdatedAt = "confirmed", thStart.Add(time.Minute)
	if row.Public != want.Public || !row.UpdatedAt.Equal(want.UpdatedAt) || !row.CreatedAt.Equal(before.CreatedAt) ||
		row.ContainerID != before.ContainerID || row.Image != before.Image || row.AdminEmail != before.AdminEmail || !bytes.Equal(row.PasswordCT, before.PasswordCT) {
		t.Errorf("row %+v, want %+v", row, want)
	}

	effs := h.journal.effects()
	if len(effs) != 1 {
		t.Fatalf("%d effects, want one", len(effs))
	}
	e := effs[0]
	if e.Kind != core.EffectModify || e.Resource != "npm.instance:public" || e.State != core.EffectMade || !e.Local || !e.InverseLocal ||
		e.CommitWait != 0 || e.Recovery != nil || e.ChangeID != "c1" {
		t.Errorf("effect %+v, want a made local modify of npm.instance:public", e)
	}
	if string(e.Before.State) != `{"public":"no"}` || e.After == nil || string(e.After.State) != `{"public":"confirmed"}` ||
		e.Post == nil || string(e.Post.State) != `{"public":"confirmed"}` {
		t.Errorf("states: before %s after %v post %v", e.Before.State, e.After, e.Post)
	}
	if calls := h.docker.calls(); len(calls) != 0 {
		t.Errorf("Engine calls %v", calls)
	}
	if reqs := h.npm.Requests(); len(reqs) != 0 {
		t.Errorf("NPM requests %v", reqs)
	}

	t.Run("a second confirmation is refused", func(t *testing.T) {
		_, err := stConfirm(h, n, "c2")
		stDefinite(t, err, errcode.InvalidInput)
		if len(h.journal.effects()) != 1 {
			t.Error("the refused call wrote an effect")
		}
	})
	t.Run("the status shows the mark", func(t *testing.T) {
		if got := stRead(t, h, n); got.Public != "confirmed" {
			t.Errorf("public %q", got.Public)
		}
	})
	t.Run("verify", func(t *testing.T) {
		if err := n.instanceConfirmPublic().Handler.Verify(ctx, h.env("c1"), json.RawMessage(`{}`), json.RawMessage(`{"public":"confirmed"}`)); err != nil {
			t.Errorf("verify after the apply: %v", err)
		}
	})
}

func TestStConfirmApplyRefuses(t *testing.T) {
	t.Run("no instance", func(t *testing.T) {
		h := newTHHost(t)
		_, err := stConfirm(h, h.nm(), "c1")
		stDefinite(t, err, errcode.NotFound)
		if len(h.journal.effects()) != 0 {
			t.Error("the refused call wrote an effect")
		}
	})
	t.Run("the instance disappears during the call", func(t *testing.T) {
		h := newTHHost(t)
		h.deployed()
		env := h.env("c1")
		env.Effects = stDropper{EffectJournal: env.Effects, drop: func() {
			_ = h.db.Write(context.Background(), func(tx *sql.Tx) error { return deleteInstance(context.Background(), tx) })
		}}
		_, err := h.nm().instanceConfirmPublic().Handler.Apply(context.Background(), env, json.RawMessage(`{}`))
		stDefinite(t, err, errcode.NotFound)
		if effs := h.journal.effects(); len(effs) != 1 || effs[0].State != core.EffectSkipped {
			t.Errorf("effects %+v, want the one effect skipped", effs)
		}
	})
}

// stDropper runs drop right after the journal records an intent.
type stDropper struct {
	core.EffectJournal
	drop func()
}

func (d stDropper) Intend(ctx context.Context, e core.Effect, secret []byte) (int, error) {
	seq, err := d.EffectJournal.Intend(ctx, e, secret)
	d.drop()
	return seq, err
}

func TestStConfirmVerify(t *testing.T) {
	ctx := context.Background()
	verify := func(h *thHost) error {
		return h.nm().instanceConfirmPublic().Handler.Verify(ctx, h.env("c1"), json.RawMessage(`{}`), json.RawMessage(`{}`))
	}
	h := newTHHost(t)
	if err := verify(h); err == nil {
		t.Error("verify passed on a host with no instance")
	}
	h.deployed()
	if err := verify(h); err == nil {
		t.Error("verify passed on a host that is not marked")
	}
	stSetRow(t, h, func(r *instanceRow) { r.Public = "confirmed" })
	if err := verify(h); err != nil {
		t.Errorf("verify on a marked host: %v", err)
	}
}

func TestStConfirmObserve(t *testing.T) {
	ctx := context.Background()
	h := newTHHost(t)
	observe := func() core.Observation {
		obs, err := h.nm().instanceConfirmPublic().Handler.Observe(ctx, h.env("c1"), core.Effect{Kind: core.EffectModify, Resource: "npm.instance:public", State: core.EffectMade})
		if err != nil {
			t.Fatal(err)
		}
		return obs
	}
	if obs := observe(); obs.Matches != 1 || !obs.Exclusive || string(obs.Current.State) != "null" {
		t.Errorf("no instance: %+v, want the null state", obs)
	}
	h.deployed()
	if obs := observe(); obs.Matches != 1 || !obs.Exclusive || string(obs.Current.State) != `{"public":"no"}` {
		t.Errorf("an unmarked instance: %+v", obs)
	}
	stSetRow(t, h, func(r *instanceRow) { r.Public = "confirmed" })
	if obs := observe(); string(obs.Current.State) != `{"public":"confirmed"}` {
		t.Errorf("a marked instance: %+v", obs)
	}
}

func TestStConfirmInverse(t *testing.T) {
	ctx := context.Background()
	h := newTHHost(t)
	n := h.nm()
	h.deployed()
	if _, err := stConfirm(h, n, "c1"); err != nil {
		t.Fatal(err)
	}
	handler := n.instanceConfirmPublic().Handler
	e := h.journal.effects()[0]

	obs, err := handler.Observe(ctx, h.env("c1"), e)
	if err != nil || !bytes.Equal(obs.Current.State, e.Post.State) {
		t.Fatalf("before the undo the mark is %s (%v), want its post-state %s", obs.Current.State, err, e.Post.State)
	}
	h.clock.Advance(time.Hour)
	res, err := handler.Inverse(ctx, h.env("c1"), e)
	if err != nil || res.Resource != "" {
		t.Fatalf("inverse = %+v, %v", res, err)
	}
	if row := stRow(t, h); row.Public != "no" || !row.UpdatedAt.Equal(thStart.Add(time.Hour)) {
		t.Errorf("row %+v, want the mark cleared at the undo's time", row)
	}
	e.State = core.EffectUndoing
	if obs, err := handler.Observe(ctx, h.env("c1"), e); err != nil || !bytes.Equal(obs.Current.State, e.Before.State) {
		t.Errorf("after the undo the mark is %s (%v), want %s", obs.Current.State, err, e.Before.State)
	}
	if got := stRead(t, h, n); got.Public != "no" {
		t.Errorf("status public %q after the undo", got.Public)
	}

	t.Run("a second undo", func(t *testing.T) {
		if _, err := handler.Inverse(ctx, h.env("c1"), e); err != nil {
			t.Errorf("error %v, want the repeated undo to pass", err)
		}
	})
	t.Run("it can be confirmed again", func(t *testing.T) {
		if _, err := stConfirm(h, n, "c2"); err != nil {
			t.Errorf("error %v", err)
		}
	})
	t.Run("a journal without a mark to restore", func(t *testing.T) {
		for _, before := range []string{`null`, `{}`, `{"public":"maybe"}`, `7`} {
			bad := e
			bad.Before.State = json.RawMessage(before)
			if _, err := handler.Inverse(ctx, h.env("c1"), bad); err == nil || !core.IsDefinite(err) {
				t.Errorf("before %s: error %v, want a Definite refusal", before, err)
			}
		}
		if row := stRow(t, h); row.Public != "confirmed" {
			t.Errorf("a refused undo changed the mark to %q", row.Public)
		}
	})
	t.Run("no instance", func(t *testing.T) {
		gone := newTHHost(t)
		_, err := gone.nm().instanceConfirmPublic().Handler.Inverse(ctx, gone.env("c1"), e)
		stDefinite(t, err, errcode.NotFound)
	})
}
