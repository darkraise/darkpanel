package tailnet

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/tailscale"
)

// njDef returns the declaration of tailnet.node.join.
func njDef(t *testing.T) core.ActionDef {
	t.Helper()
	for _, a := range Module().Manifest().Actions {
		if a.Spec.Name == "tailnet.node.join" {
			return a
		}
	}
	t.Fatal("tailnet.node.join is not declared")
	return core.ActionDef{}
}

// njSteps asks the composite for its steps the way the engine does, through
// the erased handler.
func njSteps(t *testing.T, in NodeJoinInput) ([]core.Step, error) {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	return njDef(t).Handler.Steps(context.Background(), core.Env{}, raw)
}

func njJSON(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return v
}

// TestNJDeclaration: the join is a controller-side composite that unwinds in
// reverse and holds the composite lock of its module, which the registry
// accepts together with the tailscale module its steps run in.
func TestNJDeclaration(t *testing.T) {
	s := njDef(t).Spec
	if s.Version != 1 || s.Class != core.ClassComposite || s.Side != core.SideController || s.Unwind != core.UnwindReverse ||
		!slices.Equal(s.LockKeys, []string{"composite:tailnet"}) {
		t.Errorf("spec %+v, want version 1, a controller-side composite, unwind reverse, lock composite:tailnet", s)
	}
	if _, err := core.NewRegistry(Module(), tailscale.Module()); err != nil {
		t.Errorf("core.NewRegistry: %v", err)
	}
}

func TestNJStepsWithoutInstall(t *testing.T) {
	steps, err := njSteps(t, NodeJoinInput{HostID: "h1", Tags: []string{"tag:darkpanel", "tag:web"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 {
		t.Fatalf("%d steps, want key and login", len(steps))
	}
	key, login := steps[0], steps[1]
	if key.ID != "key" || key.Host != "" || key.Action != (core.ActionRef{Name: "tailnet.authkey.create", Version: 1}) || len(key.After) != 0 {
		t.Errorf("first step %+v, want key: tailnet.authkey.create/v1 on the controller, after nothing", key)
	}
	wantKey := map[string]any{"tags": []any{"tag:darkpanel", "tag:web"}, "preauthorized": true, "expirySeconds": float64(3600)}
	if got := njJSON(t, key.Input); !reflect.DeepEqual(got, wantKey) {
		t.Errorf("key input %s, want %v: pre-authorized, not reusable, not ephemeral, expiring after 3600 s", key.Input, wantKey)
	}
	if login.ID != "login" || login.Host != "h1" || login.Action != (core.ActionRef{Name: "tailscale.node.login", Version: 1}) || !slices.Equal(login.After, []string{"key"}) {
		t.Errorf("second step %+v, want login: tailscale.node.login/v1 on h1, after key", login)
	}
	if got := njJSON(t, login.Input); !reflect.DeepEqual(got, map[string]any{"authKey": "${steps.key.output.key}"}) {
		t.Errorf("login input %s, want the auth key as a reference to the key step's output and nothing else", login.Input)
	}
}

func TestNJStepsWithInstall(t *testing.T) {
	steps, err := njSteps(t, NodeJoinInput{HostID: "h1", Tags: []string{"tag:darkpanel"}, Install: true})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range steps {
		ids = append(ids, s.ID)
	}
	if !slices.Equal(ids, []string{"install", "key", "login"}) {
		t.Fatalf("steps %v, want install, key, login", ids)
	}
	install, login := steps[0], steps[2]
	if install.Host != "h1" || install.Action != (core.ActionRef{Name: "tailscale.package.install", Version: 1}) || len(install.After) != 0 {
		t.Errorf("install step %+v, want tailscale.package.install/v1 on h1, after nothing", install)
	}
	if got := njJSON(t, install.Input); !reflect.DeepEqual(got, map[string]any{}) {
		t.Errorf("install input %s, want {}", install.Input)
	}
	if !slices.Equal(login.After, []string{"install", "key"}) {
		t.Errorf("login follows %v, want install and key", login.After)
	}
}

func TestNJStepsRefuseNoTags(t *testing.T) {
	for _, tags := range [][]string{nil, {}} {
		_, err := njSteps(t, NodeJoinInput{HostID: "h1", Tags: tags})
		var coded *errcode.Error
		if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput ||
			!slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == "/tags" }) {
			t.Errorf("tags %v: %v, want %s naming /tags", tags, err, errcode.InvalidInput)
		}
	}
}

// TestNJStepsMatchTheRegistry: every step the composite returns names a
// registered action, and obeys the rules the engine checks at plan time: a
// controller action takes no host and an agent action needs one, no step is
// irreversible or a composite, every compensable step has an undo handler
// (the join unwinds in reverse), and each step's input is valid for its
// action's schema once the auth key reference is filled in.
func TestNJStepsMatchTheRegistry(t *testing.T) {
	reg, err := core.NewRegistry(Module(), tailscale.Module())
	if err != nil {
		t.Fatal(err)
	}
	steps, err := njSteps(t, NodeJoinInput{HostID: "h1", Tags: []string{"tag:darkpanel"}, Install: true})
	if err != nil {
		t.Fatal(err)
	}
	validator := schema.NewValidator()
	for _, s := range steps {
		def, ok := reg.Action(s.Action)
		if !ok {
			t.Errorf("step %s: %s is not registered", s.ID, s.Action)
			continue
		}
		switch {
		case def.Spec.Side == core.SideController && s.Host != "", def.Spec.Side == core.SideAgent && s.Host == "":
			t.Errorf("step %s: %s is %s-side but the step names host %q", s.ID, s.Action, def.Spec.Side, s.Host)
		case def.Spec.Class == core.ClassIrreversible, def.Spec.Class == core.ClassComposite:
			t.Errorf("step %s: %s is %s, which a step never is", s.ID, s.Action, def.Spec.Class)
		case def.Spec.Class == core.ClassCompensable && def.Handler.Inverse == nil:
			t.Errorf("step %s: %s has no undo handler, which a reverse composite needs", s.ID, s.Action)
		}
		input := json.RawMessage(strings.ReplaceAll(string(s.Input), "${steps.key.output.key}", "tskey-auth-example"))
		if err := validator.ValidateInput(def, input); err != nil {
			t.Errorf("step %s: input %s is invalid for %s: %v", s.ID, input, s.Action, err)
		}
	}
	created, err := json.Marshal(CreatedKey{ID: "k1", Key: "tskey-auth-example"})
	if err != nil || !strings.Contains(string(created), `"key":"tskey-auth-example"`) {
		t.Errorf("tailnet.authkey.create output %s, %v; want the member key that the login step references", created, err)
	}
}

func TestNJOutput(t *testing.T) {
	def := njDef(t)
	in := json.RawMessage(`{"hostId":"h1","tags":["tag:darkpanel"]}`)
	login := json.RawMessage(`{"backendState":"Running","dnsName":"h1.example.ts.net","addresses":["100.64.0.5","fd7a:115c:a1e0::5"]}`)
	out, err := def.Handler.Output(context.Background(), core.Env{}, in, map[string]json.RawMessage{"key": json.RawMessage(`{"id":"k1"}`), "login": login})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"backendState": "Running", "addresses": []any{"100.64.0.5", "fd7a:115c:a1e0::5"}}
	if got := njJSON(t, out); !reflect.DeepEqual(got, want) {
		t.Errorf("output %s, want %v", out, want)
	}
	if _, err := def.Handler.Output(context.Background(), core.Env{}, in, map[string]json.RawMessage{"key": json.RawMessage(`{"id":"k1"}`)}); err == nil || !strings.Contains(err.Error(), "login") {
		t.Errorf("output without the login step: %v, want an error naming login", err)
	}
}
