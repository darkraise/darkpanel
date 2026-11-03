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
	exHost       = "h7"
	exTunnelID   = "8e6f7a8b-9c0d-4e1f-a2b3-c4d5e6f70819"
	exTunnelName = "darkpanel-01JD0000000000000000000002"
)

// exSeed stores the managed tunnel and an active hostname row for each name.
func exSeed(t *testing.T, h *cfHost, hostnames ...string) {
	t.Helper()
	cfWrite(t, h.db, func(ctx context.Context, tx *sql.Tx) error {
		err := insertTunnel(ctx, tx, tunnelRow{ID: exTunnelID, Name: exTunnelName, ChangeID: "01JD0000000000000000000002", CreatedAt: cfT0})
		if err != nil {
			return err
		}
		for _, name := range hostnames {
			if err := insertHostname(ctx, tx, cfHostname(name, exTunnelID, "active")); err != nil {
				return err
			}
		}
		return nil
	})
}

// exBase is a valid exposure input; a test changes the member it examines.
func exBase() ExposeInput {
	return ExposeInput{HostID: exHost, Tunnel: exTunnelID, Container: "web", Port: 8080, Network: "appnet", Hostname: "app.example.com"}
}

// exJSON is v as the JSON a step output or an input holds.
func exJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// exSteps asks def for its steps the way the engine does, through the erased handler, with the host's controller
// database.
func exSteps(t *testing.T, h *cfHost, def core.ActionDef, in any) ([]core.Step, error) {
	t.Helper()
	return def.Handler.Steps(context.Background(), h.env(""), exJSON(t, in))
}

// exGeneric is v as decoded JSON.
func exGeneric(t *testing.T, v any) any {
	t.Helper()
	var out any
	if err := json.Unmarshal(exJSON(t, v), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

var exRef = regexp.MustCompile(`\$\{([^}]*)\}`)

// exResolve resolves the references of a step template against the composite's input in and the outputs of earlier
// steps, as the engine does: a string that is exactly one reference becomes the referenced value with its JSON type,
// a reference inside a longer string is replaced by the value's text, and a substituted value is never read as a
// template again. A reference to a member that does not exist fails the test, as it fails the plan.
func exResolve(t *testing.T, tmpl json.RawMessage, in any, outputs map[string]any) json.RawMessage {
	t.Helper()
	steps := map[string]any{}
	for id, v := range outputs {
		steps[id] = map[string]any{"output": exGeneric(t, v)}
	}
	root := map[string]any{"input": exGeneric(t, in), "steps": steps}
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
			if m := exRef.FindStringSubmatch(x); m != nil && m[0] == x {
				return lookup(m[1])
			}
			return exRef.ReplaceAllStringFunc(x, func(s string) string { return fmt.Sprint(lookup(s[2 : len(s)-1])) })
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
	return exJSON(t, walk(tv))
}

// exStrict decodes raw into a T and refuses a member T does not have.
func exStrict[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("input %s: %v", raw, err)
	}
	return v
}

// exWantStep fails the test unless s is the step id on host running action after the steps after.
func exWantStep(t *testing.T, s core.Step, id, host, action string, after []string) {
	t.Helper()
	if s.ID != id || s.Host != host || s.Action.String() != action || !slices.Equal(s.After, after) {
		t.Errorf("step %+v, want %s: %s on host %q after %v", s, id, action, host, after)
	}
}

func exIDs(steps []core.Step) []string {
	var ids []string
	for _, s := range steps {
		ids = append(ids, s.ID)
	}
	return ids
}

// exRefs counts the step references in the template of s.
func exRefs(s core.Step) int { return strings.Count(string(s.Input), "${steps.") }

// exRefused fails the test unless err is a Definite refusal with the code, and answers it.
func exRefused(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("no error, want a refusal with code %s", code)
	}
	coded := errcode.From(err)
	if coded.Code != code {
		t.Errorf("error %v has code %s, want %s", err, coded.Code, code)
	}
	if !core.IsDefinite(err) {
		t.Errorf("error %v is not marked definite", err)
	}
	return coded
}

// TestExDeclarations: both are controller-side composites that take the module's coordination lock; the exposure
// unwinds in reverse, the removal forward.
func TestExDeclarations(t *testing.T) {
	tests := []struct {
		name   string
		def    core.ActionDef
		unwind core.Unwind
	}{
		{"cloudflare.expose.container", cf{}.exposeContainer(), core.UnwindReverse},
		{"cloudflare.expose.remove", cf{}.exposeRemove(), core.UnwindForward},
	}
	for _, tc := range tests {
		s := tc.def.Spec
		if s.Name != tc.name || s.Version != 1 || s.Summary == "" || s.Class != core.ClassComposite || s.Side != core.SideController ||
			s.Unwind != tc.unwind || !slices.Equal(s.LockKeys, []string{"composite:cloudflare"}) {
			t.Errorf("%s: spec %+v, want version 1, a summary, a controller-side composite, unwind %v, lock composite:cloudflare", tc.name, s, tc.unwind)
		}
		if tc.def.Handler.Steps == nil || tc.def.Handler.Output == nil {
			t.Errorf("%s: the handler lacks Steps or Output", tc.name)
		}
		if _, err := schema.ActionSnapshot(tc.def); err != nil {
			t.Errorf("%s: schema: %v", tc.name, err)
		}
	}
	if got := exposeClaimantPrefix; got != "expose:" {
		t.Errorf("claimant prefix %q, want expose:", got)
	}
}

// TestExExposeContainerSteps: the connector is attached to the target's network first, under the claim of the
// hostname, and the hostname is added after it with the service http://<container>:<port>. The tunnel is named by
// its id in both steps, whether the input gave its id or its name.
func TestExExposeContainerSteps(t *testing.T) {
	tests := []struct {
		name        string
		in          ExposeInput
		wantService string
	}{
		{"tunnel by id", exBase(), "http://web:8080"},
		{"tunnel by name", ExposeInput{HostID: exHost, Tunnel: exTunnelName, Container: "api.v2_x", Port: 3000, Network: "backend", Hostname: "api.example.com"}, "http://api.v2_x:3000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newCFHost(t)
			exSeed(t, h)
			steps, err := exSteps(t, h, cf{}.exposeContainer(), tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"network", "hostname"}; !slices.Equal(exIDs(steps), want) {
				t.Fatalf("steps %v, want %v", exIDs(steps), want)
			}
			exWantStep(t, steps[0], "network", exHost, "cloudflare.network.attach/v1", nil)
			exWantStep(t, steps[1], "hostname", "", "cloudflare.hostname.add/v1", []string{"network"})

			attach := exStrict[NetworkAttachInput](t, exResolve(t, steps[0].Input, tc.in, nil))
			wantAttach := NetworkAttachInput{Tunnel: exTunnelID, Network: tc.in.Network, Claimant: "expose:" + tc.in.Hostname, Target: tc.in.Container}
			if attach != wantAttach {
				t.Errorf("attach input %+v, want %+v", attach, wantAttach)
			}
			add := exResolve(t, steps[1].Input, tc.in, nil)
			wantAdd := HostnameAddInput{Tunnel: exTunnelID, Hostname: tc.in.Hostname, Service: tc.wantService}
			if got := exStrict[HostnameAddInput](t, add); got != wantAdd {
				t.Errorf("hostname input %+v, want %+v", got, wantAdd)
			}
			if err := schema.NewValidator().ValidateInput(cf{}.hostnameAdd(), add); err != nil {
				t.Errorf("hostname input %s is invalid for cloudflare.hostname.add: %v", add, err)
			}
			if exRefs(steps[0]) != 0 || exRefs(steps[1]) != 0 {
				t.Errorf("the templates %s and %s hold step references, want references to the input only", steps[0].Input, steps[1].Input)
			}
			if strings.Contains(string(steps[0].Input)+string(steps[1].Input), exTunnelName) {
				t.Errorf("the templates hold the tunnel's name, want its id")
			}
		})
	}
}

// TestExExposeContainerKeepsTypedTextOutOfTheTemplate: the engine reads every "${" of a template as a reference, so
// the hostname the operator typed reaches the steps only through references to the input, and the container name,
// which is written into the service literally, cannot hold one.
func TestExExposeContainerKeepsTypedTextOutOfTheTemplate(t *testing.T) {
	h := newCFHost(t)
	exSeed(t, h)
	in := exBase()
	in.Hostname = "${steps.network.output.network}.example.com"
	steps, err := exSteps(t, h, cf{}.exposeContainer(), in)
	if err != nil {
		t.Fatal(err)
	}
	outputs := map[string]any{"network": NetworkAttachment{Network: "other"}}
	if got := exStrict[NetworkAttachInput](t, exResolve(t, steps[0].Input, in, outputs)).Claimant; got != "expose:"+in.Hostname {
		t.Errorf("claimant %q, want the typed hostname unchanged", got)
	}
	if got := exStrict[HostnameAddInput](t, exResolve(t, steps[1].Input, in, outputs)).Hostname; got != in.Hostname {
		t.Errorf("hostname %q, want the typed hostname unchanged", got)
	}

	in = exBase()
	in.Container = "web${input.port}"
	_, err = exSteps(t, h, cf{}.exposeContainer(), in)
	if coded := exRefused(t, err, errcode.InvalidInput); len(coded.Fields) == 0 || coded.Fields[0].Path != "/container" {
		t.Errorf("fields %+v, want a refusal of /container", coded.Fields)
	}
}

// TestExExposeContainerRefusals: an input the steps cannot be made from is refused before any step runs, naming
// the member, and a tunnel Darkpanel does not manage is not found.
func TestExExposeContainerRefusals(t *testing.T) {
	with := func(change func(in *ExposeInput)) ExposeInput {
		in := exBase()
		change(&in)
		return in
	}
	tests := []struct {
		name string
		in   ExposeInput
		code errcode.Code
		path string
	}{
		{"empty container", with(func(in *ExposeInput) { in.Container = "" }), errcode.InvalidInput, "/container"},
		{"container with a space", with(func(in *ExposeInput) { in.Container = "web app" }), errcode.InvalidInput, "/container"},
		{"container starting with a hyphen", with(func(in *ExposeInput) { in.Container = "-web" }), errcode.InvalidInput, "/container"},
		{"container with a slash", with(func(in *ExposeInput) { in.Container = "a/b" }), errcode.InvalidInput, "/container"},
		{"container too long", with(func(in *ExposeInput) { in.Container = strings.Repeat("a", 129) }), errcode.InvalidInput, "/container"},
		{"empty network", with(func(in *ExposeInput) { in.Network = "" }), errcode.InvalidInput, "/network"},
		{"network with a separator", with(func(in *ExposeInput) { in.Network = "app|net" }), errcode.InvalidInput, "/network"},
		{"port 0", with(func(in *ExposeInput) { in.Port = 0 }), errcode.InvalidInput, "/port"},
		{"negative port", with(func(in *ExposeInput) { in.Port = -1 }), errcode.InvalidInput, "/port"},
		{"port 65536", with(func(in *ExposeInput) { in.Port = 65536 }), errcode.InvalidInput, "/port"},
		{"empty hostname", with(func(in *ExposeInput) { in.Hostname = "" }), errcode.InvalidInput, "/hostname"},
		{"unknown tunnel", with(func(in *ExposeInput) { in.Tunnel = "missing" }), errcode.NotFound, ""},
		{"no tunnel", with(func(in *ExposeInput) { in.Tunnel = "" }), errcode.NotFound, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newCFHost(t)
			exSeed(t, h)
			_, err := exSteps(t, h, cf{}.exposeContainer(), tc.in)
			coded := exRefused(t, err, tc.code)
			if tc.path != "" && (len(coded.Fields) == 0 || coded.Fields[0].Path != tc.path) {
				t.Errorf("fields %+v, want a refusal of %s", coded.Fields, tc.path)
			}
		})
	}
	// The boundary values are accepted.
	for _, port := range []int{1, 65535} {
		h := newCFHost(t)
		exSeed(t, h)
		if _, err := exSteps(t, h, cf{}.exposeContainer(), with(func(in *ExposeInput) { in.Port = port })); err != nil {
			t.Errorf("port %d: %v", port, err)
		}
	}
}

// TestExExposeContainerRefusalsNameTheFirstCharacter: a name that starts with a separator is refused with a detail
// that says a name starts with a letter or digit.
func TestExExposeContainerRefusalsNameTheFirstCharacter(t *testing.T) {
	h := newCFHost(t)
	exSeed(t, h)
	for path, in := range map[string]ExposeInput{
		"/container": func() ExposeInput { in := exBase(); in.Container = "-web"; return in }(),
		"/network":   func() ExposeInput { in := exBase(); in.Network = ".net"; return in }(),
	} {
		_, err := exSteps(t, h, cf{}.exposeContainer(), in)
		coded := exRefused(t, err, errcode.InvalidInput)
		if len(coded.Fields) != 1 || coded.Fields[0].Path != path || !strings.Contains(coded.Detail, "starts with a letter or digit") {
			t.Errorf("%s: fields %+v, detail %q; want one refusal of %s that says a name starts with a letter or digit", path, coded.Fields, coded.Detail, path)
		}
	}
}

// TestExExposeRemoveOutputDecodesItsSteps: the removal's output reads its steps' outputs as the exposure's does, so
// one that is not its step's output is an error naming the step.
func TestExExposeRemoveOutputDecodesItsSteps(t *testing.T) {
	in := exJSON(t, ExposeRemoveInput{HostID: exHost, Hostname: "app.example.com"})
	for _, bad := range []string{"hostname", "network"} {
		steps := map[string]json.RawMessage{
			"hostname": exJSON(t, HostnameView{Hostname: "app.example.com", Phase: "removed"}),
			"network":  exJSON(t, NetworkAttachment{Tunnel: exTunnelID, Claimant: "expose:app.example.com"}),
		}
		steps[bad] = json.RawMessage(`"not an object"`)
		_, err := cf{}.exposeRemove().Handler.Output(context.Background(), core.Env{}, in, steps)
		if err == nil || !strings.Contains(err.Error(), "exposure's "+bad+" output") {
			t.Errorf("a malformed %s output: %v, want an error naming it", bad, err)
		}
	}
}

func TestExExposeContainerOutput(t *testing.T) {
	def := cf{}.exposeContainer()
	in := exJSON(t, exBase())
	steps := map[string]json.RawMessage{
		"network":  exJSON(t, NetworkAttachment{Tunnel: exTunnelID, Network: "appnet", Claimant: "expose:app.example.com", Attached: true}),
		"hostname": exJSON(t, HostnameView{Hostname: "app.example.com", Tunnel: exTunnelID, Service: "http://web:8080", Phase: "active", Health: "ok"}),
	}
	out, err := def.Handler.Output(context.Background(), core.Env{}, in, steps)
	if err != nil {
		t.Fatal(err)
	}
	want := ExposeOutput{Hostname: "app.example.com", Service: "http://web:8080", Network: "appnet"}
	if got := exStrict[ExposeOutput](t, out); got != want {
		t.Errorf("output %+v, want %+v", got, want)
	}
	for _, missing := range []string{"network", "hostname"} {
		partial := map[string]json.RawMessage{}
		for k, v := range steps {
			if k != missing {
				partial[k] = v
			}
		}
		if _, err := def.Handler.Output(context.Background(), core.Env{}, in, partial); err == nil || !strings.Contains(err.Error(), missing) {
			t.Errorf("output without the %s step: %v, want an error naming it", missing, err)
		}
	}
}

// TestExExposeRemoveSteps: the hostname goes first, then every claim of the exposure on the tunnel's connector,
// whatever network it was made on. The tunnel is the one the hostname row names.
func TestExExposeRemoveSteps(t *testing.T) {
	h := newCFHost(t)
	exSeed(t, h, "app.example.com")
	in := ExposeRemoveInput{HostID: exHost, Hostname: "app.example.com"}
	steps, err := exSteps(t, h, cf{}.exposeRemove(), in)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"hostname", "network"}; !slices.Equal(exIDs(steps), want) {
		t.Fatalf("steps %v, want %v", exIDs(steps), want)
	}
	exWantStep(t, steps[0], "hostname", "", "cloudflare.hostname.remove/v1", nil)
	exWantStep(t, steps[1], "network", exHost, "cloudflare.network.detach/v1", []string{"hostname"})

	remove := exResolve(t, steps[0].Input, in, nil)
	if got := exStrict[HostnameInput](t, remove); got != (HostnameInput{Hostname: "app.example.com"}) {
		t.Errorf("remove input %+v, want the hostname", got)
	}
	if err := schema.NewValidator().ValidateInput(cf{}.hostnameRemove(), remove); err != nil {
		t.Errorf("remove input %s is invalid for cloudflare.hostname.remove: %v", remove, err)
	}
	detach := exStrict[NetworkDetachInput](t, exResolve(t, steps[1].Input, in, nil))
	if want := (NetworkDetachInput{Tunnel: exTunnelID, Network: "", Claimant: "expose:app.example.com"}); detach != want {
		t.Errorf("detach input %+v, want %+v: every claim of the exposure on the hostname's tunnel", detach, want)
	}
	if exRefs(steps[0]) != 0 || exRefs(steps[1]) != 0 {
		t.Errorf("the templates %s and %s hold step references, want references to the input only", steps[0].Input, steps[1].Input)
	}
}

// TestExExposeRemoveUnknownHostname: a hostname Darkpanel does not manage is refused before any step runs.
func TestExExposeRemoveUnknownHostname(t *testing.T) {
	h := newCFHost(t)
	exSeed(t, h, "app.example.com")
	for _, typed := range []string{"other.example.com", "", "${steps.hostname.output.hostname}"} {
		_, err := exSteps(t, h, cf{}.exposeRemove(), ExposeRemoveInput{HostID: exHost, Hostname: typed})
		_ = exRefused(t, err, errcode.NotFound)
	}
}

func TestExExposeRemoveOutput(t *testing.T) {
	def := cf{}.exposeRemove()
	in := exJSON(t, ExposeRemoveInput{HostID: exHost, Hostname: "app.example.com"})
	steps := map[string]json.RawMessage{
		"hostname": exJSON(t, HostnameView{Hostname: "app.example.com", Phase: "removed"}),
		"network":  exJSON(t, NetworkAttachment{Tunnel: exTunnelID, Claimant: "expose:app.example.com"}),
	}
	out, err := def.Handler.Output(context.Background(), core.Env{}, in, steps)
	if err != nil {
		t.Fatal(err)
	}
	if got := exStrict[ExposeRemoveOutput](t, out); got != (ExposeRemoveOutput{Hostname: "app.example.com"}) {
		t.Errorf("output %+v, want the hostname", got)
	}
	for _, missing := range []string{"hostname", "network"} {
		partial := map[string]json.RawMessage{}
		for k, v := range steps {
			if k != missing {
				partial[k] = v
			}
		}
		if _, err := def.Handler.Output(context.Background(), core.Env{}, in, partial); err == nil || !strings.Contains(err.Error(), missing) {
			t.Errorf("output without the %s step: %v, want an error naming it", missing, err)
		}
	}
}
