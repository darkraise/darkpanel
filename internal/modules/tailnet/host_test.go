package tailnet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
)

var tnT0 = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

// tnHost is the controller side of one module test: a database with the core
// and tailnet migrations, a fake clock and an in-memory effects journal.
type tnHost struct {
	db      *store.DB
	clock   *coretest.FakeClock
	journal *tnJournal
}

func newTNHost(t *testing.T) *tnHost {
	t.Helper()
	return &tnHost{db: tnDB(t), clock: coretest.NewFakeClock(tnT0), journal: &tnJournal{}}
}

// env is the call environment of a controller-side action. The journal is
// attached only for a change; a plan or a read has none.
func (h *tnHost) env(changeID string) core.Env {
	env := core.Env{
		ChangeID:  changeID,
		Principal: core.Principal{Actor: "user:u1", UserID: "u1", Scope: core.ScopeAdmin},
		DB:        h.db,
		Sealer:    coretest.Sealer{},
		Clock:     h.clock,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if changeID != "" {
		h.journal.changeID = changeID
		env.Effects = h.journal
	}
	return env
}

// tnJournal is an in-memory core.EffectJournal that keeps each effect's
// secret, as the real one does sealed.
type tnJournal struct {
	changeID string
	list     []core.Effect
	secrets  map[int][]byte
}

func (j *tnJournal) Intend(_ context.Context, e core.Effect, secret []byte) (int, error) {
	e.ChangeID, e.Seq, e.State = j.changeID, len(j.list)+1, core.EffectIntended
	j.list = append(j.list, e)
	if secret != nil {
		if j.secrets == nil {
			j.secrets = map[int][]byte{}
		}
		j.secrets[e.Seq] = append([]byte(nil), secret...)
	}
	return e.Seq, nil
}

func (j *tnJournal) intended(seq int) (*core.Effect, error) {
	if seq < 1 || seq > len(j.list) || j.list[seq-1].State != core.EffectIntended {
		return nil, fmt.Errorf("fake: effect %d is not intended", seq)
	}
	return &j.list[seq-1], nil
}

func (j *tnJournal) Made(_ context.Context, seq int, resource string, post core.StateDoc) error {
	e, err := j.intended(seq)
	if err != nil {
		return err
	}
	e.State, e.Resource, e.Post = core.EffectMade, resource, &post
	return nil
}

func (j *tnJournal) Skipped(_ context.Context, seq int) error {
	e, err := j.intended(seq)
	if err != nil {
		return err
	}
	e.State = core.EffectSkipped
	return nil
}

func (j *tnJournal) List(context.Context) ([]core.Effect, error) { return j.effects(), nil }

func (j *tnJournal) Secret(_ context.Context, seq int) ([]byte, error) {
	if seq < 1 || seq > len(j.list) {
		return nil, fmt.Errorf("fake: no effect %d", seq)
	}
	return j.secrets[seq], nil
}

// effects are the journaled effects in the order intended.
func (j *tnJournal) effects() []core.Effect { return append([]core.Effect(nil), j.list...) }

func TestTnHostEnvAndJournal(t *testing.T) {
	h := newTNHost(t)
	if e := h.env(""); e.Effects != nil || e.ChangeID != "" {
		t.Errorf("env(\"\") = change %q, journal %v, want neither", e.ChangeID, e.Effects)
	}

	env := h.env("c1")
	if env.ChangeID != "c1" || env.HostID != "" || env.Principal.Scope != core.ScopeAdmin || env.DB != h.db || env.Effects == nil || env.Log == nil {
		t.Fatalf("env(c1) = %+v", env)
	}
	if !env.Clock.Now().Equal(tnT0) {
		t.Errorf("clock reads %v, want %v", env.Clock.Now(), tnT0)
	}
	ct, err := env.Sealer.Seal("ad", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if pt, err := env.Sealer.Open("ad", ct); err != nil || string(pt) != "x" {
		t.Errorf("sealer round trip = %q, %v", pt, err)
	}

	ctx := context.Background()
	first, err := env.Effects.Intend(ctx, core.Effect{Kind: core.EffectModify, Resource: "tailnet.policy:example.com"}, []byte("before text"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := env.Effects.Intend(ctx, core.Effect{Kind: core.EffectCreate, Resource: "tailnet.key:?darkpanel-c1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first != 1 || second != 2 {
		t.Fatalf("seqs = %d, %d, want 1, 2", first, second)
	}
	post := core.StateDoc{State: json.RawMessage(`{"policy":{}}`), Version: json.RawMessage(`"etag-2"`)}
	if err := env.Effects.Made(ctx, first, "tailnet.policy:example.com", post); err != nil {
		t.Fatal(err)
	}
	if err := env.Effects.Skipped(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := env.Effects.Made(ctx, first, "tailnet.policy:example.com", post); err == nil {
		t.Error("an effect already made was made again")
	}
	got := h.journal.effects()
	if len(got) != 2 || got[0].ChangeID != "c1" || got[0].State != core.EffectMade || got[0].Post == nil || string(got[0].Post.Version) != `"etag-2"` || got[1].State != core.EffectSkipped {
		t.Errorf("effects = %+v", got)
	}
	if s, err := env.Effects.Secret(ctx, first); err != nil || string(s) != "before text" {
		t.Errorf("Secret = %q, %v", s, err)
	}
}
