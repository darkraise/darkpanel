package cloudflare

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// cfHost is a test controller: a database with the core and Cloudflare
// migrations, a manual clock and an in-memory effects journal.
type cfHost struct {
	db      *store.DB
	clock   *coretest.FakeClock
	journal *cfJournal
}

func newCFHost(t *testing.T) *cfHost {
	t.Helper()
	return &cfHost{
		db:      cfDB(t),
		clock:   coretest.NewFakeClock(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)),
		journal: &cfJournal{},
	}
}

// env is the Env of an admin's controller-side change; Effects is the journal
// only when changeID is not empty.
func (h *cfHost) env(changeID string) core.Env {
	env := core.Env{
		Principal: core.Principal{Actor: "user:u1", Scope: core.ScopeAdmin},
		ChangeID:  changeID, DB: h.db, Sealer: coretest.Sealer{}, Clock: h.clock,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if changeID != "" {
		h.journal.changeID = changeID
		env.Effects = h.journal
	}
	return env
}

// cfJournal is an in-memory core.EffectJournal.
type cfJournal struct {
	changeID string
	list     []core.Effect
	secrets  map[int][]byte
}

func (j *cfJournal) effects() []core.Effect { return append([]core.Effect(nil), j.list...) }

func (j *cfJournal) Intend(_ context.Context, e core.Effect, secret []byte) (int, error) {
	e.Seq, e.State, e.ChangeID = len(j.list)+1, core.EffectIntended, j.changeID
	j.list = append(j.list, e)
	if secret != nil {
		if j.secrets == nil {
			j.secrets = map[int][]byte{}
		}
		j.secrets[e.Seq] = append([]byte(nil), secret...)
	}
	return e.Seq, nil
}

func (j *cfJournal) find(seq int) (*core.Effect, error) {
	if seq < 1 || seq > len(j.list) || j.list[seq-1].State != core.EffectIntended {
		return nil, fmt.Errorf("fake: effect %d is not intended", seq)
	}
	return &j.list[seq-1], nil
}

func (j *cfJournal) Made(_ context.Context, seq int, resource string, post core.StateDoc) error {
	e, err := j.find(seq)
	if err != nil {
		return err
	}
	e.State, e.Resource, e.Post = core.EffectMade, resource, &post
	return nil
}

func (j *cfJournal) Skipped(_ context.Context, seq int) error {
	e, err := j.find(seq)
	if err != nil {
		return err
	}
	e.State = core.EffectSkipped
	return nil
}

func (j *cfJournal) List(context.Context) ([]core.Effect, error) { return j.effects(), nil }

func (j *cfJournal) Secret(_ context.Context, seq int) ([]byte, error) {
	if s, ok := j.secrets[seq]; ok {
		return s, nil
	}
	return nil, errors.New("fake: no secret")
}

var _ core.EffectJournal = (*cfJournal)(nil)

// TestCFHostEnvAndJournal: env carries the host's database, sealer and clock,
// gives the journal only to a change, and the journal numbers effects from 1,
// moves them to made or skipped once, and keeps their secrets.
func TestCFHostEnvAndJournal(t *testing.T) {
	ctx := context.Background()
	h := newCFHost(t)
	if env := h.env(""); env.Effects != nil || env.DB != h.db || env.Sealer == nil || env.Clock == nil || env.ChangeID != "" {
		t.Errorf("env(\"\") = %+v", env)
	}
	env := h.env("c1")
	if env.Effects == nil || env.ChangeID != "c1" || env.Principal.Scope != core.ScopeAdmin {
		t.Fatalf("env(c1) = %+v", env)
	}
	s1, err := env.Effects.Intend(ctx, core.Effect{Kind: core.EffectCreate, Resource: "cloudflare.tunnel:?n"}, []byte("sealed"))
	if err != nil {
		t.Fatal(err)
	}
	s2, _ := env.Effects.Intend(ctx, core.Effect{Kind: core.EffectModify, Resource: "cloudflare.ingress:t/h"}, nil)
	if s1 != 1 || s2 != 2 {
		t.Fatalf("seqs %d, %d; want 1, 2", s1, s2)
	}
	if err := env.Effects.Made(ctx, s1, "cloudflare.tunnel:t1", core.StateDoc{State: []byte(`{"name":"n"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := env.Effects.Skipped(ctx, s2); err != nil {
		t.Fatal(err)
	}
	if err := env.Effects.Skipped(ctx, s2); err == nil {
		t.Error("an effect was skipped twice")
	}
	got := h.journal.effects()
	if len(got) != 2 || got[0].State != core.EffectMade || got[0].Resource != "cloudflare.tunnel:t1" || got[0].Post == nil ||
		got[0].ChangeID != "c1" || got[1].State != core.EffectSkipped {
		t.Errorf("effects = %+v", got)
	}
	if secret, err := env.Effects.Secret(ctx, s1); err != nil || string(secret) != "sealed" {
		t.Errorf("Secret = %q, %v", secret, err)
	}
}
