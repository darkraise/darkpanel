package lifecycle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestNewRequiresDependencies(t *testing.T) {
	f := newFixture(t)
	breaks := map[string]func(*Config){
		"side":      func(c *Config) { c.Side = "browser" },
		"registry":  func(c *Config) { c.Registry = nil },
		"validator": func(c *Config) { c.Validator = nil },
		"journal":   func(c *Config) { c.Journal = nil },
		"redact":    func(c *Config) { c.Redact = nil },
		"db":        func(c *Config) { c.Base.DB = nil },
		"sealer":    func(c *Config) { c.Base.Sealer = nil },
		"clock":     func(c *Config) { c.Base.Clock = nil },
		"log":       func(c *Config) { c.Base.Log = nil },
	}
	for name, brk := range breaks {
		c := f.config()
		brk(&c)
		if _, err := New(c); err == nil {
			t.Errorf("%s: New accepted an incomplete config", name)
		}
	}
}

func TestReadServesReadActionsOfItsSide(t *testing.T) {
	f := newFixture(t)
	f.host.items["a"] = "1"
	out, err := f.eng.Read(context.Background(), alice, refGet, json.RawMessage(`{"name":"a"}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"actor":"user:alice","hostId":"host-1","token":"[redacted]","value":"1"}`; string(out) != want {
		t.Errorf("Read = %s, want %s with the secret redacted", out, want)
	}
	cases := []struct {
		name  string
		ref   core.ActionRef
		input string
		code  errcode.Code
	}{
		{"empty input", refGet, ``, errcode.InvalidInput},
		{"unknown field", refGet, `{"name":"a","x":1}`, errcode.InvalidInput},
		{"mutating action", refSet, `{"name":"a"}`, errcode.ActionUnknown},
		{"other side", refCtl, `{"name":"a"}`, errcode.ActionUnknown},
		{"unknown version", core.ActionRef{Name: refGet.Name, Version: 2}, `{"name":"a"}`, errcode.ActionUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := f.eng.Read(context.Background(), alice, c.ref, json.RawMessage(c.input))
			wantCode(t, err, c.code)
		})
	}
}

func TestPlanStoresSealedInput(t *testing.T) {
	f := newFixture(t)
	f.host.items["a"] = "old"
	v := f.plan(alice, refSet, `{"value":"new","secret":"s3cret","name":"a"}`)

	if v.PlanID == "" || v.HostID != testHost || v.Action != refSet.Name || v.Version != 1 || v.Confirmation != "" {
		t.Errorf("plan view = %+v", v)
	}
	if want := f.clock.Now().Add(PlanTTL); !v.ExpiresAt.Equal(want) {
		t.Errorf("expires at %v, want %v", v.ExpiresAt, want)
	}
	wantDiff := []core.DiffLine{{Op: "-", Text: "a=old"}, {Op: "+", Text: "a=new"}}
	wantGuards := []core.Guard{{ID: "not-blocked", OK: true, Message: "the host is blocked"}}
	if !reflect.DeepEqual(v.Diff, wantDiff) || !reflect.DeepEqual(v.Guards, wantGuards) {
		t.Errorf("diff %+v, guards %+v", v.Diff, v.Guards)
	}
	sum := sha256.Sum256([]byte(`{"name":"a","secret":"[redacted]","value":"new"}`)) // testRedact's output
	if want := hex.EncodeToString(sum[:]); v.InputHash != want {
		t.Errorf("input hash = %s, want %s", v.InputHash, want)
	}

	var format int
	var actor string
	var ct []byte
	if err := f.db.Read().QueryRow("SELECT format, actor, input_ct FROM core_plans WHERE id = ?", v.PlanID).
		Scan(&format, &actor, &ct); err != nil {
		t.Fatal(err)
	}
	if format != 1 || actor != alice.Actor {
		t.Errorf("format %d, actor %s; want 1 and %s", format, actor, alice.Actor)
	}
	if bytes.Contains(ct, []byte("s3cret")) {
		t.Error("stored plan input contains the secret in clear")
	}
	plain, err := coretest.Sealer{}.Open("core_plans.input_ct."+v.PlanID, ct)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"name":"a","value":"new","secret":"s3cret"}`; string(plain) != want {
		t.Errorf("sealed input = %s, want the canonical input %s", plain, want)
	}
	if _, err := (coretest.Sealer{}).Open("core_plans.input_ct.other", ct); err == nil {
		t.Error("the sealed input opened under another row's associated data")
	}
}

func TestPlanInputHash(t *testing.T) {
	f := newFixture(t)
	hash := func(in string) string { return f.plan(alice, refSet, in).InputHash }
	base := hash(`{"name":"a","value":"1","secret":"x"}`)
	if hash(`{"secret":"x","value":"1","name":"a"}`) != base {
		t.Error("field order changed the input hash")
	}
	if hash(`{"name":"a","value":"1","secret":"y"}`) != base {
		t.Error("the secret changed the input hash")
	}
	if hash(`{"name":"a","value":"2","secret":"x"}`) == base {
		t.Error("a different value kept the input hash")
	}
}

func TestPlanConfirmationOnlyForIrreversible(t *testing.T) {
	f := newFixture(t)
	if v := f.plan(alice, refRemove, `{"name":"a"}`); v.Confirmation != "a" {
		t.Errorf("irreversible confirmation = %q, want the handler's phrase", v.Confirmation)
	}
	if v := f.plan(alice, refAdd, `{"name":"a"}`); v.Confirmation != "" {
		t.Errorf("compensable confirmation = %q, want none", v.Confirmation)
	}
}

func TestPlanRefusals(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name  string
		ref   core.ActionRef
		input string
		code  errcode.Code
	}{
		{"read action", refGet, `{"name":"a"}`, errcode.ActionUnknown},
		{"empty input", refSet, ``, errcode.InvalidInput},
		{"unknown action", core.ActionRef{Name: "fix.item.nope", Version: 1}, `{}`, errcode.ActionUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := f.eng.Plan(context.Background(), alice, c.ref, json.RawMessage(c.input))
			wantCode(t, err, c.code)
		})
	}
	var n int
	if err := f.db.Read().QueryRow("SELECT count(*) FROM core_plans").Scan(&n); err != nil || n != 0 {
		t.Errorf("plans stored = %d, %v; want 0", n, err)
	}
}

func TestChangeNotFound(t *testing.T) {
	f := newFixture(t)
	_, err := f.eng.Change(context.Background(), "01NOPE")
	wantCode(t, err, errcode.ChangeNotFound)
	if cs, err := f.eng.Changes(context.Background(), core.ChangeFilter{}); err != nil || len(cs) != 0 {
		t.Errorf("Changes = %v, %v; want none", cs, err)
	}
}
