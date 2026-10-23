package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var rvrAdmin = core.Principal{Actor: "user:root", UserID: "root", Scope: core.ScopeAdmin, Source: "198.51.100.4"}

type rvrIn struct {
	Name string `json:"name"`
}

type rvrOut struct {
	Secret string `json:"secret" secret:"true"`
}

func rvrValue(identity, secret string) func(context.Context, core.Env, rvrIn) (core.Revealed[rvrOut], error) {
	return func(context.Context, core.Env, rvrIn) (core.Revealed[rvrOut], error) {
		return core.Revealed[rvrOut]{Identity: identity, Value: rvrOut{Secret: secret}}, nil
	}
}

// rvrModule has a reveal and an internal read; with broken set its agent
// migration fails.
type rvrModule struct {
	id     string
	broken bool
}

func (m rvrModule) Manifest() core.Manifest {
	man := core.Manifest{ID: m.id, Version: 1, DefaultEnabled: true, Actions: []core.ActionDef{
		core.DefineReveal(core.ActionSpec{Name: m.id + ".key.reveal", Version: 1, Side: core.SideAgent, MinScope: core.ScopeAdmin},
			rvrValue(m.id+" key", "k-secret")),
		core.DefineInternalRead(core.ActionSpec{Name: m.id + ".cert.get", Version: 1, Side: core.SideAgent},
			rvrValue(m.id+" certificate", "c-secret")),
	}}
	if m.broken {
		man.Migrations = map[core.Side]fs.FS{core.SideAgent: fstest.MapFS{
			"0001_" + m.id + ".sql": {Data: []byte("CREATE TABLE " + m.id + "_items (")},
		}}
	}
	return man
}

func TestRuntimeReveal(t *testing.T) {
	f := newFixtureWith(t, rvrModule{id: "rvr"})
	ctx := context.Background()
	in := json.RawMessage(`{"name":"admin"}`)
	reveal := core.ActionRef{Name: "rvr.key.reveal", Version: 1}
	internal := core.ActionRef{Name: "rvr.cert.get", Version: 1}
	if out, err := f.rt.Reveal(ctx, rvrAdmin, reveal, in, false); err != nil || string(out) != `{"secret":"k-secret"}` {
		t.Errorf("Reveal = %s, %v; want the reveal's value", out, err)
	}
	if out, err := f.rt.Reveal(ctx, rvrAdmin, internal, in, true); err != nil || string(out) != `{"secret":"c-secret"}` {
		t.Errorf("internal Reveal = %s, %v; want the internal read's value", out, err)
	}
	for _, tc := range []struct {
		ref      core.ActionRef
		internal bool
	}{{internal, false}, {reveal, true}} {
		_, err := f.rt.Reveal(ctx, rvrAdmin, tc.ref, in, tc.internal)
		var e *errcode.Error
		if !errors.As(err, &e) || e.Code != errcode.ActionUnknown {
			t.Errorf("Reveal of %s with internal %v = %v, want action.unknown", tc.ref, tc.internal, err)
		}
	}
	entries, err := f.rt.Journal(ctx, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var identities []string
	for _, e := range entries {
		if e.Phase == "reveal" && e.Actor == rvrAdmin.Actor {
			identities = append(identities, string(e.Input))
		}
	}
	if len(identities) != 2 || identities[0] != `{"identity":"rvr key"}` || identities[1] != `{"identity":"rvr certificate"}` {
		t.Errorf("reveal journal inputs = %v, want the key's and the certificate's identities", identities)
	}
}

func TestRuntimeRevealBrokenModule(t *testing.T) {
	f := newFixtureWith(t, rvrModule{id: "rvb", broken: true})
	for _, tc := range []struct {
		ref      core.ActionRef
		internal bool
	}{{core.ActionRef{Name: "rvb.key.reveal", Version: 1}, false}, {core.ActionRef{Name: "rvb.cert.get", Version: 1}, true}} {
		_, err := f.rt.Reveal(context.Background(), rvrAdmin, tc.ref, json.RawMessage(`{"name":"admin"}`), tc.internal)
		var e *errcode.Error
		if !errors.As(err, &e) || e.Code != errcode.ModuleUnavailable {
			t.Errorf("Reveal of %s = %v, want module.unavailable", tc.ref, err)
		}
	}
}
