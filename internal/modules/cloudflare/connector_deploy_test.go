package cloudflare

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

const (
	cdHost       = "h7"
	cdTunnelID   = "5c1d2f3a-0b4e-4c6d-8e7f-9a0b1c2d3e4f"
	cdTunnelName = "darkpanel-01JD0000000000000000000001"
	cdSecret     = "cd-token-secret-value"
)

// cdSeedTunnel stores the managed tunnel a named deployment resolves.
func cdSeedTunnel(t *testing.T, h *cfHost) {
	t.Helper()
	cfWrite(t, h.db, func(ctx context.Context, tx *sql.Tx) error {
		return insertTunnel(ctx, tx, tunnelRow{ID: cdTunnelID, Name: cdTunnelName, ChangeID: "01JD0000000000000000000001", CreatedAt: cfT0})
	})
}

// cdJSON is v as the JSON a step output or an input holds.
func cdJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// cdSteps asks the composite for its steps the way the engine does, through the erased handler, with the host's
// controller database.
func cdSteps(t *testing.T, h *cfHost, in ConnectorDeployInput) ([]core.Step, error) {
	t.Helper()
	return cf{}.connectorDeploy().Handler.Steps(context.Background(), h.env(""), cdJSON(t, in))
}

// cdGeneric is v as decoded JSON.
func cdGeneric(t *testing.T, v any) any {
	t.Helper()
	var out any
	if err := json.Unmarshal(cdJSON(t, v), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

var cdRef = regexp.MustCompile(`\$\{([^}]*)\}`)

// cdResolve resolves the references of a step template against the composite's input in and the outputs of earlier
// steps, as the engine does: a string that is exactly one reference becomes the referenced value with its JSON type,
// a reference inside a longer string is replaced by the value's text, and a substituted value is never read as a
// template again. A reference to a member that does not exist fails the test, as it fails the plan.
func cdResolve(t *testing.T, tmpl json.RawMessage, in any, outputs map[string]any) json.RawMessage {
	t.Helper()
	steps := map[string]any{}
	for id, v := range outputs {
		steps[id] = map[string]any{"output": cdGeneric(t, v)}
	}
	root := map[string]any{"input": cdGeneric(t, in), "steps": steps}
	lookup := func(ref string) any {
		var cur any = root
		for _, p := range strings.Split(ref, ".") {
			m, ok := cur.(map[string]any)
			if !ok {
				t.Errorf("reference ${%s}: %q is looked up in a value that has no members", ref, p)
				return nil
			}
			if cur, ok = m[p]; !ok {
				t.Errorf("reference ${%s}: there is no member %q", ref, p)
				return nil
			}
		}
		return cur
	}
	var walk func(v any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			if m := cdRef.FindStringSubmatch(x); m != nil && m[0] == x {
				return lookup(m[1])
			}
			return cdRef.ReplaceAllStringFunc(x, func(s string) string { return fmt.Sprint(lookup(s[2 : len(s)-1])) })
		case map[string]any:
			for k, e := range x {
				x[k] = walk(e)
			}
		case []any:
			for i, e := range x {
				x[i] = walk(e)
			}
		}
		return v
	}
	var tv any
	if err := json.Unmarshal(tmpl, &tv); err != nil {
		t.Fatalf("template %s: %v", tmpl, err)
	}
	return cdJSON(t, walk(tv))
}

// cdStrict decodes raw into a T and refuses a member T does not have.
func cdStrict[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("input %s: %v", raw, err)
	}
	return v
}

// cdWantStep fails the test unless s is the step id on host running action after the steps after.
func cdWantStep(t *testing.T, s core.Step, id, host, action string, after []string) {
	t.Helper()
	if s.ID != id || s.Host != host || s.Action.String() != action || !slices.Equal(s.After, after) {
		t.Errorf("step %+v, want %s: %s on host %q after %v", s, id, action, host, after)
	}
}

func cdIDs(steps []core.Step) []string {
	var ids []string
	for _, s := range steps {
		ids = append(ids, s.ID)
	}
	return ids
}

// cdRefs counts the step references in the template of s.
func cdRefs(s core.Step) int { return strings.Count(string(s.Input), "${steps.") }

// cdRefused fails the test unless err is a Definite refusal with the code.
func cdRefused(t *testing.T, err error, code errcode.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("no error, want a refusal with code %s", code)
	}
	if got := errcode.From(err).Code; got != code {
		t.Errorf("error %v has code %s, want %s", err, got, code)
	}
	if !core.IsDefinite(err) {
		t.Errorf("error %v is not marked definite", err)
	}
}

// TestCdDeclaration: the deployment is a controller-side composite that takes the module's coordination lock and
// unwinds in reverse.
func TestCdDeclaration(t *testing.T) {
	def := cf{}.connectorDeploy()
	s := def.Spec
	if s.Name != "cloudflare.connector.deploy" || s.Version != 1 || s.Summary == "" || s.Class != core.ClassComposite ||
		s.Side != core.SideController || s.Unwind != core.UnwindReverse || !slices.Equal(s.LockKeys, []string{"composite:cloudflare"}) {
		t.Errorf("spec %+v, want version 1, a summary, a controller-side composite, unwind reverse, lock composite:cloudflare", s)
	}
	if def.Handler.Steps == nil || def.Handler.Output == nil {
		t.Error("the handler lacks Steps or Output")
	}
	if _, err := schema.ActionSnapshot(def); err != nil {
		t.Errorf("schema: %v", err)
	}
}

// TestCdStepsCreateTunnel: without a named tunnel the deployment creates one, reads its token and runs the
// connector on the host. The token reaches the run only as a reference to the token step's output.
func TestCdStepsCreateTunnel(t *testing.T) {
	h := newCFHost(t)
	in := ConnectorDeployInput{HostID: cdHost}
	steps, err := cdSteps(t, h, in)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"tunnel", "token", "run"}; !slices.Equal(cdIDs(steps), want) {
		t.Fatalf("steps %v, want %v", cdIDs(steps), want)
	}
	cdWantStep(t, steps[0], "tunnel", "", "cloudflare.tunnel.create/v1", nil)
	cdWantStep(t, steps[1], "token", "", "cloudflare.tunnel.token/v1", []string{"tunnel"})
	cdWantStep(t, steps[2], "run", cdHost, "cloudflare.connector.run/v1", []string{"token"})

	outputs := map[string]any{"tunnel": TunnelRef{ID: cdTunnelID, Name: cdTunnelName}, "token": TunnelToken{Token: cdSecret}}
	create := cdResolve(t, steps[0].Input, in, outputs)
	cdStrict[TunnelCreateInput](t, create)
	if err := schema.NewValidator().ValidateInput(cf{}.tunnelCreate(), create); err != nil {
		t.Errorf("tunnel input %s is invalid for cloudflare.tunnel.create: %v", create, err)
	}
	token := cdResolve(t, steps[1].Input, in, outputs)
	if got := cdStrict[TunnelInput](t, token); got != (TunnelInput{Tunnel: cdTunnelID}) {
		t.Errorf("token input %+v, want the created tunnel's id", got)
	}
	if err := schema.NewValidator().ValidateInput(cf{}.tunnelToken(), token); err != nil {
		t.Errorf("token input %s is invalid for cloudflare.tunnel.token: %v", token, err)
	}
	if got := cdStrict[ConnectorRunInput](t, cdResolve(t, steps[2].Input, in, outputs)); got != (ConnectorRunInput{Tunnel: cdTunnelID, Token: cdSecret}) {
		t.Errorf("run input %+v, want the created tunnel's id and the token the token step revealed", got)
	}
	if got := cdStrict[ConnectorRunInput](t, steps[2].Input); got.Tunnel != "${steps.tunnel.output.id}" || got.Token != "${steps.token.output.token}" {
		t.Errorf("run template %+v, want references to the tunnel's id and the token step's token", got)
	}
	if cdRefs(steps[0]) != 0 || cdRefs(steps[1]) != 1 || cdRefs(steps[2]) != 2 {
		t.Errorf("step references: tunnel %d, token %d, run %d; want 0, 1 and 2", cdRefs(steps[0]), cdRefs(steps[1]), cdRefs(steps[2]))
	}
}

// TestCdStepsNamedTunnel: a named tunnel, by id or by name, is resolved to its id when the steps are made, so the
// deployment has no tunnel step and every template holds the id, never the typed name.
func TestCdStepsNamedTunnel(t *testing.T) {
	for _, named := range []string{cdTunnelID, cdTunnelName} {
		t.Run(named, func(t *testing.T) {
			h := newCFHost(t)
			cdSeedTunnel(t, h)
			in := ConnectorDeployInput{HostID: cdHost, Tunnel: named}
			steps, err := cdSteps(t, h, in)
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"token", "run"}; !slices.Equal(cdIDs(steps), want) {
				t.Fatalf("steps %v, want %v", cdIDs(steps), want)
			}
			cdWantStep(t, steps[0], "token", "", "cloudflare.tunnel.token/v1", nil)
			cdWantStep(t, steps[1], "run", cdHost, "cloudflare.connector.run/v1", []string{"token"})
			outputs := map[string]any{"token": TunnelToken{Token: cdSecret}}
			token := cdResolve(t, steps[0].Input, in, outputs)
			if got := cdStrict[TunnelInput](t, token); got != (TunnelInput{Tunnel: cdTunnelID}) {
				t.Errorf("token input %+v, want the tunnel's id", got)
			}
			if err := schema.NewValidator().ValidateInput(cf{}.tunnelToken(), token); err != nil {
				t.Errorf("token input %s is invalid for cloudflare.tunnel.token: %v", token, err)
			}
			if got := cdStrict[ConnectorRunInput](t, cdResolve(t, steps[1].Input, in, outputs)); got != (ConnectorRunInput{Tunnel: cdTunnelID, Token: cdSecret}) {
				t.Errorf("run input %+v, want the tunnel's id and the revealed token", got)
			}
			if got := cdStrict[ConnectorRunInput](t, steps[1].Input); got.Tunnel != cdTunnelID || got.Token != "${steps.token.output.token}" {
				t.Errorf("run template %+v, want the literal id and a reference to the token", got)
			}
			if cdRefs(steps[0]) != 0 || cdRefs(steps[1]) != 1 {
				t.Errorf("step references: token %d, run %d; want 0 and 1", cdRefs(steps[0]), cdRefs(steps[1]))
			}
			if strings.Contains(string(steps[0].Input)+string(steps[1].Input), cdTunnelName) {
				t.Errorf("the templates %s and %s hold the tunnel's name, want its id", steps[0].Input, steps[1].Input)
			}
		})
	}
}

// TestCdStepsUnknownTunnel: a tunnel Darkpanel does not manage is refused before any step runs, and what the
// operator typed is not read as a reference.
func TestCdStepsUnknownTunnel(t *testing.T) {
	h := newCFHost(t)
	cdSeedTunnel(t, h)
	for _, typed := range []string{"missing", "${steps.token.output.token}", "5c1d2f3a-0b4e-4c6d-8e7f-000000000000"} {
		_, err := cdSteps(t, h, ConnectorDeployInput{HostID: cdHost, Tunnel: typed})
		cdRefused(t, err, errcode.NotFound)
	}
	_, err := cdSteps(t, newCFHost(t), ConnectorDeployInput{HostID: cdHost, Tunnel: cdTunnelID})
	cdRefused(t, err, errcode.NotFound)
}

// TestCdStepsRefuseAnEmptyHost: a deployment that names no host is refused as an invalid /hostId before any step,
// with or without a named tunnel.
func TestCdStepsRefuseAnEmptyHost(t *testing.T) {
	h := newCFHost(t)
	cdSeedTunnel(t, h)
	for _, tunnel := range []string{"", cdTunnelID} {
		steps, err := cdSteps(t, h, ConnectorDeployInput{Tunnel: tunnel})
		cdRefused(t, err, errcode.InvalidInput)
		if fields := errcode.From(err).Fields; len(fields) != 1 || fields[0].Path != "/hostId" || steps != nil {
			t.Errorf("tunnel %q: fields %+v, steps %v; want one field error at /hostId and no step", tunnel, fields, steps)
		}
	}
}

// TestCdOutputRefusesAnotherTunnel: an output whose run step started another tunnel's connector than the deployment's
// is an error, whether the deployment created the tunnel or named it.
func TestCdOutputRefusesAnotherTunnel(t *testing.T) {
	other := Connector{Tunnel: "7a8b9c0d-1e2f-4a3b-8c4d-5e6f7a8b9c0d", Container: "x", ContainerID: "c0ffee", State: "running"}
	def := cf{}.connectorDeploy()
	for _, tc := range []struct {
		name  string
		in    ConnectorDeployInput
		steps map[string]json.RawMessage
	}{
		{"created", ConnectorDeployInput{HostID: cdHost},
			map[string]json.RawMessage{"tunnel": cdJSON(t, TunnelRef{ID: cdTunnelID, Name: cdTunnelName}), "run": cdJSON(t, other)}},
		{"named", ConnectorDeployInput{HostID: cdHost, Tunnel: cdTunnelName}, map[string]json.RawMessage{"run": cdJSON(t, other)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCFHost(t)
			cdSeedTunnel(t, h)
			_, err := def.Handler.Output(context.Background(), h.env(""), cdJSON(t, tc.in), tc.steps)
			if err == nil || !strings.Contains(err.Error(), other.Tunnel) || !strings.Contains(err.Error(), cdTunnelID) {
				t.Errorf("error %v, want one naming both tunnels", err)
			}
		})
	}
}

func TestCdOutput(t *testing.T) {
	conn := Connector{Tunnel: cdTunnelID, Container: "darkpanel-cloudflared-" + cdTunnelID, ContainerID: "c0ffee", State: "running"}
	tests := []struct {
		name  string
		in    ConnectorDeployInput
		steps map[string]json.RawMessage
		want  ConnectorDeployOutput
	}{
		{
			"created", ConnectorDeployInput{HostID: cdHost},
			map[string]json.RawMessage{"tunnel": cdJSON(t, TunnelRef{ID: cdTunnelID, Name: cdTunnelName}), "run": cdJSON(t, conn)},
			ConnectorDeployOutput{Tunnel: cdTunnelID, Created: true, Connector: conn},
		},
		{
			"named by id", ConnectorDeployInput{HostID: cdHost, Tunnel: cdTunnelID},
			map[string]json.RawMessage{"run": cdJSON(t, conn)},
			ConnectorDeployOutput{Tunnel: cdTunnelID, Connector: conn},
		},
		{
			"named by name", ConnectorDeployInput{HostID: cdHost, Tunnel: cdTunnelName},
			map[string]json.RawMessage{"run": cdJSON(t, conn)},
			ConnectorDeployOutput{Tunnel: cdTunnelID, Connector: conn},
		},
	}
	def := cf{}.connectorDeploy()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newCFHost(t)
			cdSeedTunnel(t, h)
			out, err := def.Handler.Output(context.Background(), h.env(""), cdJSON(t, tc.in), tc.steps)
			if err != nil {
				t.Fatal(err)
			}
			if got := cdStrict[ConnectorDeployOutput](t, out); got != tc.want {
				t.Errorf("output %+v, want %+v", got, tc.want)
			}
			if bytes.Contains(out, []byte("token")) {
				t.Errorf("output %s mentions a token", out)
			}
		})
	}

	failures := []struct {
		name  string
		seed  bool
		in    ConnectorDeployInput
		steps map[string]json.RawMessage
		want  string
	}{
		{"created without the run step", true, ConnectorDeployInput{HostID: cdHost},
			map[string]json.RawMessage{"tunnel": cdJSON(t, TunnelRef{ID: cdTunnelID})}, "run"},
		{"created without the tunnel step", true, ConnectorDeployInput{HostID: cdHost},
			map[string]json.RawMessage{"run": cdJSON(t, conn)}, "tunnel"},
		{"created with a tunnel step that holds no id", true, ConnectorDeployInput{HostID: cdHost},
			map[string]json.RawMessage{"tunnel": json.RawMessage(`{}`), "run": cdJSON(t, conn)}, "tunnel"},
		{"named without the run step", true, ConnectorDeployInput{HostID: cdHost, Tunnel: cdTunnelID},
			map[string]json.RawMessage{}, "run"},
		{"named tunnel no longer stored", false, ConnectorDeployInput{HostID: cdHost, Tunnel: cdTunnelID},
			map[string]json.RawMessage{"run": cdJSON(t, conn)}, cdTunnelID},
	}
	for _, tc := range failures {
		t.Run(tc.name, func(t *testing.T) {
			h := newCFHost(t)
			if tc.seed {
				cdSeedTunnel(t, h)
			}
			_, err := def.Handler.Output(context.Background(), h.env(""), cdJSON(t, tc.in), tc.steps)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v, want one naming %q", err, tc.want)
			}
		})
	}
}
