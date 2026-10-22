package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var (
	refRvReveal   = core.ActionRef{Name: "rv.key.reveal", Version: 1}
	refRvInternal = core.ActionRef{Name: "rv.cert.get", Version: 1}
	refRvRead     = core.ActionRef{Name: "rv.key.get", Version: 1}
	rvAdmin       = core.Principal{Actor: "user:root", UserID: "root", Scope: core.ScopeAdmin, Source: "198.51.100.4"}
)

const rvSecret = "hunter2-rv"

type rvIn struct {
	Name string `json:"name"`
	Fail bool   `json:"fail,omitempty"`
}

type rvOut struct {
	Name     string `json:"name"`
	Password string `json:"password" secret:"true"`
}

func rvReveal(kind string) func(context.Context, core.Env, rvIn) (core.Revealed[rvOut], error) {
	return func(_ context.Context, env core.Env, in rvIn) (core.Revealed[rvOut], error) {
		if in.Fail {
			return core.Revealed[rvOut]{}, errors.New("rv vault is sealed")
		}
		return core.Revealed[rvOut]{Identity: "rv " + kind + ", host " + env.HostID,
			Value: rvOut{Name: in.Name, Password: rvSecret}}, nil
	}
}

type rvRead struct{}

func (rvRead) Read(context.Context, core.Env, rvIn) (rvOut, error) { return rvOut{Name: "plain"}, nil }

type rvModule struct{}

func (rvModule) Manifest() core.Manifest {
	return core.Manifest{ID: "rv", Version: 1, Actions: []core.ActionDef{
		core.DefineReveal(core.ActionSpec{Name: refRvReveal.Name, Version: 1, Side: core.SideAgent, MinScope: core.ScopeAdmin},
			rvReveal("key")),
		core.DefineInternalRead(core.ActionSpec{Name: refRvInternal.Name, Version: 1, Side: core.SideAgent},
			rvReveal("certificate")),
		core.DefineRead(core.ActionSpec{Name: refRvRead.Name, Version: 1, Side: core.SideAgent}, rvRead{}),
	}}
}

// rvJournal returns every journal entry of the fixture.
func (f *fixture) rvJournal() []core.JournalEntry {
	f.t.Helper()
	entries, err := f.journal.Entries(context.Background(), 0, 0)
	if err != nil {
		f.t.Fatal(err)
	}
	return entries
}

// rvFailingJournal refuses every append.
type rvFailingJournal struct{}

func (rvFailingJournal) Append(context.Context, *sql.Tx, core.JournalEntry) error {
	return errors.New("rv journal is full")
}

func TestEngineReveal(t *testing.T) {
	f := newFixtureWith(t, rvModule{})
	out, err := f.eng.Reveal(context.Background(), rvAdmin, refRvReveal, json.RawMessage(`{"name":"admin"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"name":"admin","password":"`+rvSecret+`"}` {
		t.Errorf("Reveal = %s, want the value unredacted", out)
	}
	entries := f.rvJournal()
	if len(entries) != 1 {
		t.Fatalf("journal = %+v, want one entry", entries)
	}
	e := entries[0]
	if e.Phase != "reveal" || e.Actor != rvAdmin.Actor || e.Source != rvAdmin.Source || e.Action != refRvReveal.Name ||
		e.Version != 1 || e.Result != "ok" || e.ChangeID != "" || e.ErrorCode != "" ||
		string(e.Input) != `{"identity":"rv key, host `+testHost+`"}` {
		t.Errorf("journal entry = %+v", e)
	}
	if n := f.count("SELECT count(*) FROM core_journal WHERE input_json LIKE ?", "%"+rvSecret+"%"); n != 0 {
		t.Errorf("%d journal entries hold the revealed value", n)
	}
}

func TestEngineReadInternal(t *testing.T) {
	f := newFixtureWith(t, rvModule{})
	out, err := f.eng.ReadInternal(context.Background(), rvAdmin, refRvInternal, json.RawMessage(`{"name":"web"}`))
	if err != nil || string(out) != `{"name":"web","password":"`+rvSecret+`"}` {
		t.Fatalf("ReadInternal = %s, %v; want the value unredacted", out, err)
	}
	entries := f.rvJournal()
	if len(entries) != 1 || entries[0].Phase != "reveal" || entries[0].Action != refRvInternal.Name ||
		string(entries[0].Input) != `{"identity":"rv certificate, host `+testHost+`"}` {
		t.Errorf("journal = %+v, want one reveal entry naming the certificate", entries)
	}
}

func TestRevealAuditsBeforeReturning(t *testing.T) {
	f := newFixtureWith(t, rvModule{})
	cfg := f.config()
	cfg.Journal = rvFailingJournal{}
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	ctx := context.Background()
	out, err := eng.Reveal(ctx, rvAdmin, refRvReveal, json.RawMessage(`{"name":"admin"}`))
	if err == nil || out != nil || !strings.Contains(err.Error(), "rv journal is full") {
		t.Errorf("Reveal = %s, %v; want the journal's error and no value", out, err)
	}
	out, err = eng.ReadInternal(ctx, rvAdmin, refRvInternal, json.RawMessage(`{"name":"web"}`))
	if err == nil || out != nil || !strings.Contains(err.Error(), "rv journal is full") {
		t.Errorf("ReadInternal = %s, %v; want the journal's error and no value", out, err)
	}
}

func TestRevealFailures(t *testing.T) {
	f := newFixtureWith(t, rvModule{})
	ctx := context.Background()
	_, err := f.eng.Reveal(ctx, rvAdmin, refRvReveal, json.RawMessage(`{"name":"admin","nope":1}`))
	wantCode(t, err, errcode.InvalidInput)
	out, err := f.eng.Reveal(ctx, rvAdmin, refRvReveal, json.RawMessage(`{"name":"admin","fail":true}`))
	if err == nil || out != nil || !strings.Contains(err.Error(), "rv vault is sealed") {
		t.Errorf("Reveal = %s, %v; want the handler's error", out, err)
	}
	if entries := f.rvJournal(); len(entries) != 0 {
		t.Errorf("failed reveals journaled %+v", entries)
	}
}

func TestRevealRefusesOtherActions(t *testing.T) {
	f := newFixtureWith(t, rvModule{})
	ctx := context.Background()
	in := json.RawMessage(`{"name":"admin"}`)
	for _, tc := range []struct {
		name string
		call func(context.Context, core.Principal, core.ActionRef, json.RawMessage) (json.RawMessage, error)
		ref  core.ActionRef
	}{
		{"Reveal of an internal read", f.eng.Reveal, refRvInternal},
		{"Reveal of a plain read", f.eng.Reveal, refRvRead},
		{"Reveal of an unknown action", f.eng.Reveal, core.ActionRef{Name: "rv.key.nope", Version: 1}},
		{"ReadInternal of a reveal", f.eng.ReadInternal, refRvReveal},
		{"ReadInternal of a plain read", f.eng.ReadInternal, refRvRead},
		{"Read of a reveal", f.eng.Read, refRvReveal},
		{"Read of an internal read", f.eng.Read, refRvInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := tc.call(ctx, rvAdmin, tc.ref, in)
			wantCode(t, err, errcode.ActionUnknown)
			if out != nil {
				t.Errorf("refusal answered %s", out)
			}
		})
	}
	if entries := f.rvJournal(); len(entries) != 0 {
		t.Errorf("refusals journaled %+v", entries)
	}
	if out, err := f.eng.Read(ctx, rvAdmin, refRvRead, in); err != nil || string(out) != `{"name":"plain","password":"[redacted]"}` {
		t.Errorf("Read of a plain read = %s, %v; want it served and redacted", out, err)
	}
}
