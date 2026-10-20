package coreactions_test

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/registry"
)

type declaration struct {
	class         core.Class
	breakGlass    bool
	lockKeys      []string
	compensatedBy string
	agent         bool
}

var declarations = map[string]declaration{
	"core.host.list":           {class: core.ClassRead},
	"core.host.revoke":         {class: core.ClassIrreversible},
	"core.host.create":         {class: core.ClassCompensable, compensatedBy: "core.host.delete"},
	"core.host.token":          {class: core.ClassIrreversible},
	"core.host.enroll-local":   {class: core.ClassIrreversible, breakGlass: true},
	"core.host.delete":         {class: core.ClassIrreversible},
	"core.host.update-policy":  {class: core.ClassCompensable, compensatedBy: "core.host.update-policy"},
	"core.module.list":         {class: core.ClassRead},
	"core.module.enable":       {class: core.ClassCompensable, lockKeys: []string{"core.module"}, compensatedBy: "core.module.disable"},
	"core.module.disable":      {class: core.ClassCompensable, lockKeys: []string{"core.module"}, compensatedBy: "core.module.enable"},
	"core.token.list":          {class: core.ClassRead},
	"core.token.create":        {class: core.ClassCompensable, compensatedBy: "core.token.revoke"},
	"core.token.revoke":        {class: core.ClassIrreversible},
	"core.audit.list":          {class: core.ClassRead},
	"core.user.list":           {class: core.ClassRead, breakGlass: true},
	"core.user.create":         {class: core.ClassCompensable, breakGlass: true, compensatedBy: "core.user.delete"},
	"core.user.activate":       {class: core.ClassCompensable, breakGlass: true, compensatedBy: "core.user.reset-totp"},
	"core.user.reset-totp":     {class: core.ClassIrreversible, breakGlass: true},
	"core.user.reset-password": {class: core.ClassIrreversible, breakGlass: true},
	"core.user.delete":         {class: core.ClassIrreversible, breakGlass: true},

	"core.update.apply":          {class: core.ClassCompensable, compensatedBy: "core.update.rollback", agent: true},
	"core.update.rollback":       {class: core.ClassIrreversible, agent: true},
	"core.update.status":         {class: core.ClassRead, agent: true},
	"core.update.rollout":        {class: core.ClassCompensable, compensatedBy: "core.update.rollout-cancel"},
	"core.update.rollout-cancel": {class: core.ClassIrreversible},
	"core.update.rollouts":       {class: core.ClassRead},
}

func coreActions(t *testing.T) []core.ActionDef {
	t.Helper()
	reg, err := registry.Build()
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	var out []core.ActionDef
	for _, a := range reg.Actions() {
		if strings.HasPrefix(a.Spec.Name, core.ModuleIDCore+".") {
			out = append(out, a)
		}
	}
	return out
}

func TestCoreActionDeclarations(t *testing.T) {
	actions := coreActions(t)
	var names []string
	for _, a := range actions {
		names = append(names, a.Spec.Name)
		want, ok := declarations[a.Spec.Name]
		if !ok {
			t.Errorf("unexpected core action %s", a.Ref())
			continue
		}
		s := a.Spec
		got := declaration{class: s.Class, breakGlass: s.BreakGlass, lockKeys: s.LockKeys, compensatedBy: s.CompensatedBy,
			agent: s.Side == core.SideAgent}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %+v, want %+v", a.Ref(), got, want)
		}
		if s.Version != 1 || (s.Side != core.SideController && s.Side != core.SideAgent) || s.Summary == "" {
			t.Errorf("%s: version %d, side %s, summary %q; want version 1, a known side, a summary", a.Ref(), s.Version, s.Side, s.Summary)
		}
	}
	if len(names) != len(declarations) {
		var want []string
		for name := range declarations {
			want = append(want, name)
		}
		sort.Strings(want)
		t.Errorf("core actions = %v, want %v", names, want)
	}
}

// schemaOf returns the object schema a top-level "$ref" points to, and the
// "$defs" it resolves against.
func schemaOf(t *testing.T, raw json.RawMessage) (map[string]any, map[string]any) {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	defs, _ := root["$defs"].(map[string]any)
	if ref, ok := root["$ref"].(string); ok {
		obj, _ := defs[strings.TrimPrefix(ref, "#/$defs/")].(map[string]any)
		return obj, defs
	}
	return root, defs
}

// secretProperties returns the top-level properties of raw marked x-secret,
// noting whether each is also writeOnly.
func secretProperties(t *testing.T, raw json.RawMessage) map[string]bool {
	t.Helper()
	obj, _ := schemaOf(t, raw)
	props, _ := obj["properties"].(map[string]any)
	out := map[string]bool{}
	for name, p := range props {
		prop, _ := p.(map[string]any)
		if prop["x-secret"] == true {
			out[name] = prop["writeOnly"] == true
		}
	}
	return out
}

func TestCoreActionSecrets(t *testing.T) {
	wantIn := map[string]map[string]bool{
		"core.user.create":         {"password": true},
		"core.user.reset-password": {"password": true},
		"core.user.activate":       {"code": true},
	}
	wantOut := map[string]map[string]bool{
		"core.token.create":      {"token": false},
		"core.host.create":       {"token": false},
		"core.host.token":        {"token": false},
		"core.host.enroll-local": {"token": false},
		"core.user.create":       {"totpSecret": false},
		"core.user.reset-totp":   {"totpSecret": false},
	}
	for _, a := range coreActions(t) {
		snap, err := schema.ActionSnapshot(a)
		if err != nil {
			t.Fatalf("%s: %v", a.Ref(), err)
		}
		var s struct{ Input, Output json.RawMessage }
		if err := json.Unmarshal(snap, &s); err != nil {
			t.Fatal(err)
		}
		in, out := secretProperties(t, s.Input), secretProperties(t, s.Output)
		if want := wantIn[a.Spec.Name]; !maps.Equal(in, want) {
			t.Errorf("%s: secret input fields (writeOnly) = %v, want %v", a.Ref(), in, want)
		}
		if want := wantOut[a.Spec.Name]; !maps.Equal(out, want) {
			t.Errorf("%s: secret output fields = %v, want %v", a.Ref(), out, want)
		}
	}
}

// TestCoreNullOutputsMatchSchemas validates outputs whose optional fields are
// null against the published output schemas, compiled as the production
// validator compiles input schemas.
func TestCoreNullOutputsMatchSchemas(t *testing.T) {
	at := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	fresh := auth.TokenInfo{ID: "01TOKEN0000000000000000000", Name: "ci", Scope: core.ScopeRead,
		CreatedAt: at, ExpiresAt: at.Add(90 * 24 * time.Hour)}
	revoked := fresh
	revoked.RevokedAt = &at
	samples := map[string]any{
		"core.token.list":   coreactions.TokenList{Tokens: []auth.TokenInfo{fresh}},
		"core.token.create": coreactions.NewToken{TokenInfo: fresh, Token: "dpt_01TOKEN0000000000000000000_x"}, //nolint:gosec // G101: a test output, not a credential
		"core.token.revoke": revoked,
		"core.audit.list": coreactions.AuditList{Entries: []coreactions.AuditEntry{{
			ID: "00000000000000000000000001", At: at, Actor: "anonymous", Action: "core.auth.login", Version: 1,
			Phase: "login", Input: coreactions.AuditInput(`{"username":"mallory"}`), Result: "ok",
		}}},
		"core.host.list": core.HostList{Hosts: []core.Host{{ID: "h1", Name: "local", IsLocal: true, State: core.HostPending,
			Online: true, Modules: []core.ModuleState{{ID: "demo", Version: 1}}}}},
		"core.module.disable":     core.ModuleState{ID: "demo", Version: 1},
		"core.host.update-policy": core.UpdatePolicy{HostID: "h1", Channel: core.ChannelStable},
	}
	defs := map[string]core.ActionDef{}
	for _, a := range coreActions(t) {
		defs[a.Spec.Name] = a
	}
	for name, sample := range samples {
		def, ok := defs[name]
		if !ok {
			t.Fatalf("no core action %s", name)
		}
		raw, err := schema.Output(def.OutputType)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		url := "urn:darkpanel:output:" + def.Ref().String()
		c := jsonschema.NewCompiler()
		c.AssertFormat()
		if err := c.AddResource(url, doc); err != nil {
			t.Fatal(err)
		}
		sch, err := c.Compile(url)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out, err := json.Marshal(sample)
		if err != nil {
			t.Fatal(err)
		}
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		if err := sch.Validate(inst); err != nil {
			t.Errorf("%s: output %s does not match its schema: %v", name, out, err)
		}
	}
}

// TestCoreInputFieldsAreDocumented guards the CLI generator's input: every
// input field becomes a flag or a prompt whose help is its description.
func TestCoreInputFieldsAreDocumented(t *testing.T) {
	for _, a := range coreActions(t) {
		raw, err := schema.Input(a.InputType)
		if err != nil {
			t.Fatal(err)
		}
		obj, _ := schemaOf(t, raw)
		props, _ := obj["properties"].(map[string]any)
		for name, p := range props {
			if prop, _ := p.(map[string]any); !documented(prop) {
				t.Errorf("%s: input field %s has no description", a.Ref(), name)
			}
		}
	}
}

// documented reports whether prop has a description. The reflector writes a
// nullable reference's description on its reference branch of oneOf; such
// a field is nested, so the CLI takes it from --input rather than a flag.
func documented(prop map[string]any) bool {
	if d, _ := prop["description"].(string); d != "" {
		return true
	}
	branches, _ := prop["oneOf"].([]any)
	for _, b := range branches {
		if branch, _ := b.(map[string]any); branch["$ref"] != nil {
			d, _ := branch["description"].(string)
			return d != ""
		}
	}
	return false
}

// TestZeroDepsFailClosed calls the handlers of the registry.Build() core
// module, which has no services: each refuses with internal instead of
// panicking.
func TestZeroDepsFailClosed(t *testing.T) {
	ctx := context.Background()
	defs := map[string]core.ActionDef{}
	for _, a := range coreActions(t) {
		defs[a.Spec.Name] = a
	}
	env := core.Env{Principal: core.Principal{Actor: "user:u1", UserID: "u1", Scope: core.ScopeAdmin}}
	calls := map[string]func() error{}
	for name, input := range map[string]string{
		"core.token.list": `{}`, "core.audit.list": `{}`, "core.user.list": `{}`,
	} {
		calls[name] = func() error { _, err := defs[name].Handler.Read(ctx, env, json.RawMessage(input)); return err }
	}
	for name, input := range map[string]string{ //nolint:gosec // G101: test inputs, not credentials
		"core.token.create":        `{"name":"ci","scope":"read"}`,
		"core.token.revoke":        `{"id":"t1"}`,
		"core.user.create":         `{"username":"alice","password":"correct horse battery"}`,
		"core.user.activate":       `{"username":"alice","code":"123456"}`,
		"core.user.reset-totp":     `{"username":"alice"}`,
		"core.user.reset-password": `{"username":"alice","password":"correct horse battery"}`,
		"core.user.delete":         `{"username":"alice"}`,
		"core.host.create":         `{"name":"web1"}`,
		"core.host.token":          `{"hostId":"h1"}`,
		"core.host.enroll-local":   `{}`,
	} {
		calls[name] = func() error { _, err := defs[name].Handler.Plan(ctx, env, json.RawMessage(input)); return err }
	}
	calls["core.module.enable apply"] = func() error {
		_, err := defs["core.module.enable"].Handler.Apply(ctx, env, json.RawMessage(`{"hostId":"h1","moduleId":"demo"}`))
		return err
	}
	var names []string
	for name := range calls {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		err := calls[name]()
		if err == nil || errcode.From(err).Code != errcode.Internal {
			t.Errorf("%s with zero Deps: err = %v, want internal", name, err)
		}
	}
}
