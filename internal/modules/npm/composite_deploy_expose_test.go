package npm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/firewall"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// cxHost is the NPM host of these tests; the test host's own id ("h1") is deliberately another.
const cxHost = "h7"

// cxSteps asks def for its steps the way the engine does, through the erased handler.
func cxSteps(t *testing.T, def core.ActionDef, in any) ([]core.Step, error) {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	return def.Handler.Steps(context.Background(), core.Env{}, raw)
}

// cxGeneric is v as decoded JSON.
func cxGeneric(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

var cxRef = regexp.MustCompile(`\$\{([^}]*)\}`)

// cxResolve resolves the references of a step template against the composite's input in and the outputs of earlier
// steps, as the engine does: a string that is exactly one reference becomes the referenced value with its JSON type,
// a reference inside a longer string is replaced by the value's text, and a value that was substituted is never read
// as a template again. A reference to a member that does not exist fails the test, as it fails the plan.
func cxResolve(t *testing.T, tmpl json.RawMessage, in any, outputs map[string]any) json.RawMessage {
	t.Helper()
	input := cxGeneric(t, in)
	steps := map[string]any{}
	for id, v := range outputs {
		steps[id] = map[string]any{"output": cxGeneric(t, v)}
	}
	lookup := func(ref string) any {
		parts := strings.Split(ref, ".")
		var cur any
		switch parts[0] {
		case "input":
			cur = input
		case "steps":
			cur = steps
		default:
			t.Errorf("reference ${%s} is neither an input nor a step reference", ref)
			return nil
		}
		for _, p := range parts[1:] {
			switch c := cur.(type) {
			case map[string]any:
				next, ok := c[p]
				if !ok {
					t.Errorf("reference ${%s}: there is no member %q", ref, p)
					return nil
				}
				cur = next
			case []any:
				i, err := strconv.Atoi(p)
				if err != nil || i < 0 || i >= len(c) {
					t.Errorf("reference ${%s}: %q is not an index of the array", ref, p)
					return nil
				}
				cur = c[i]
			default:
				t.Errorf("reference ${%s}: %q is looked up in a value that has no members", ref, p)
				return nil
			}
		}
		return cur
	}
	var walk func(v any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			if m := cxRef.FindStringSubmatch(x); m != nil && m[0] == x {
				return lookup(m[1])
			}
			return cxRef.ReplaceAllStringFunc(x, func(s string) string { return fmt.Sprint(lookup(s[2 : len(s)-1])) })
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
	out, err := json.Marshal(walk(tv))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// cxStrict decodes raw into a T and refuses a member T does not have.
func cxStrict[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("input %s: %v", raw, err)
	}
	return v
}

// cxWantStep fails the test unless s is the step id on host running action after the steps after.
func cxWantStep(t *testing.T, s core.Step, id, host, action string, after []string) {
	t.Helper()
	if s.ID != id || s.Host != host || s.Action.String() != action || !slices.Equal(s.After, after) {
		t.Errorf("step %+v, want %s: %s on host %q after %v", s, id, action, host, after)
	}
}

// cxIDs are the ids of steps, in order.
func cxIDs(steps []core.Step) []string {
	var ids []string
	for _, s := range steps {
		ids = append(ids, s.ID)
	}
	return ids
}

// cxRefs counts the step references in the template of s.
func cxRefs(s core.Step) int { return strings.Count(string(s.Input), "${steps.") }

// cxAction returns the declaration of action name, version 1, of module m.
func cxAction(t *testing.T, m core.Module, name string) core.ActionDef {
	t.Helper()
	for _, a := range m.Manifest().Actions {
		if a.Spec.Name == name && a.Spec.Version == 1 {
			return a
		}
	}
	t.Fatalf("%s is not declared", name)
	return core.ActionDef{}
}

// cxJSON is v as the JSON a step output holds.
func cxJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestCxDeclarations: the three are controller-side composites that take the module's coordination lock; the
// exposure and the deployment unwind in reverse, the removal forward.
func TestCxDeclarations(t *testing.T) {
	n := newTHHost(t).nm()
	tests := []struct {
		name   string
		def    core.ActionDef
		unwind core.Unwind
	}{
		{"npm.instance.deploy-public", n.instanceDeployPublic(), core.UnwindReverse},
		{"npm.expose.container", n.exposeContainer(), core.UnwindReverse},
		{"npm.expose.remove", n.exposeRemove(), core.UnwindForward},
	}
	for _, tc := range tests {
		s := tc.def.Spec
		if s.Name != tc.name || s.Version != 1 || s.Summary == "" || s.Class != core.ClassComposite || s.Side != core.SideController ||
			s.Unwind != tc.unwind || !slices.Equal(s.LockKeys, []string{"composite:npm"}) {
			t.Errorf("%s: spec %+v, want version 1, a summary, a controller-side composite, unwind %v, lock composite:npm", tc.name, s, tc.unwind)
		}
		if tc.def.Handler.Steps == nil || tc.def.Handler.Output == nil {
			t.Errorf("%s: the handler lacks Steps or Output", tc.name)
		}
		if _, err := schema.ActionSnapshot(tc.def); err != nil {
			t.Errorf("%s: schema: %v", tc.name, err)
		}
	}
}

// TestCxDeployPublicSteps: the deployment checks the host first; the ports step exists only with the integration,
// is the last step, and is a valid input of the revertible firewall action.
func TestCxDeployPublicSteps(t *testing.T) {
	for _, integration := range []bool{false, true} {
		t.Run(fmt.Sprintf("integration %v", integration), func(t *testing.T) {
			in := DeployPublicInput{HostID: cxHost, AcmeEmail: "ops@example.com", Integration: integration}
			steps, err := cxSteps(t, newTHHost(t).nm().instanceDeployPublic(), in)
			if err != nil {
				t.Fatal(err)
			}
			wantIDs := []string{"check", "deploy"}
			if integration {
				wantIDs = append(wantIDs, "ports")
			}
			if !slices.Equal(cxIDs(steps), wantIDs) {
				t.Fatalf("steps %v, want %v", cxIDs(steps), wantIDs)
			}
			cxWantStep(t, steps[0], "check", "", "npm.instance.check/v1", nil)
			if got := cxStrict[CheckInput](t, cxResolve(t, steps[0].Input, in, nil)); got != (CheckInput{HostID: cxHost, Integration: integration}) {
				t.Errorf("check input %+v, want host %s and integration %v", got, cxHost, integration)
			}
			cxWantStep(t, steps[1], "deploy", cxHost, "npm.instance.deploy/v1", []string{"check"})
			if got := cxStrict[DeployInput](t, cxResolve(t, steps[1].Input, in, nil)); got != (DeployInput{AcmeEmail: "ops@example.com"}) {
				t.Errorf("deploy input %+v, want the ACME email", got)
			}
			if !integration {
				return
			}
			cxWantStep(t, steps[2], "ports", cxHost, "firewall.containers.apply/v1", []string{"deploy"})
			ports := cxStrict[struct {
				Ops []firewall.ContainerOp `json:"ops"`
			}](t, steps[2].Input)
			want := []firewall.ContainerOp{
				{Op: "allow", Binding: &dockerfw.Binding{Selector: "darkpanel-npm", HostPort: 80, Proto: "tcp"}},
				{Op: "allow", Binding: &dockerfw.Binding{Selector: "darkpanel-npm", HostPort: 443, Proto: "tcp"}},
			}
			if !reflect.DeepEqual(ports.Ops, want) {
				t.Errorf("ports ops %+v, want allow of 80/tcp and 443/tcp of darkpanel-npm from every address and source", ports.Ops)
			}
			def := cxAction(t, firewall.Module(), "firewall.containers.apply")
			if def.Spec.Class != core.ClassRevertible || def.Spec.Side != core.SideAgent {
				t.Errorf("firewall.containers.apply is %s on the %s side, want a revertible agent action: the one step that may be, last", def.Spec.Class, def.Spec.Side)
			}
			if err := schema.NewValidator().ValidateInput(def, steps[2].Input); err != nil {
				t.Errorf("ports input %s is invalid for firewall.containers.apply: %v", steps[2].Input, err)
			}
		})
	}
}

// TestCxDeployPublicKeepsInputOutOfTheTemplate: the engine reads every "${" of a template as a reference, so what the
// operator typed reaches the steps only through references to the input.
func TestCxDeployPublicKeepsInputOutOfTheTemplate(t *testing.T) {
	in := DeployPublicInput{HostID: "${steps.deploy.output.containerId}", AcmeEmail: "${steps.check.output.integration}@example.com", Integration: true}
	steps, err := cxSteps(t, newTHHost(t).nm().instanceDeployPublic(), in)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range steps {
		if cxRefs(s) != 0 {
			t.Errorf("step %s template %s holds a step reference, want references to the input only", s.ID, s.Input)
		}
	}
	if got := cxStrict[CheckInput](t, cxResolve(t, steps[0].Input, in, nil)); got.HostID != in.HostID {
		t.Errorf("check host %q, want %q", got.HostID, in.HostID)
	}
	if got := cxStrict[DeployInput](t, cxResolve(t, steps[1].Input, in, nil)); got.AcmeEmail != in.AcmeEmail {
		t.Errorf("deploy email %q, want %q", got.AcmeEmail, in.AcmeEmail)
	}
}

func TestCxDeployPublicOutput(t *testing.T) {
	def := newTHHost(t).nm().instanceDeployPublic()
	deployed := cxJSON(t, Deployed{ContainerID: "abc123", Image: "jc21/nginx-proxy-manager:2.13.5", AdminEmail: "ops@example.com"})
	tests := []struct {
		name        string
		integration bool
		steps       map[string]json.RawMessage
		want        DeployPublicOutput
	}{
		{"without the integration", false, map[string]json.RawMessage{"deploy": deployed}, DeployPublicOutput{ContainerID: "abc123"}},
		{"with the integration", true, map[string]json.RawMessage{"deploy": deployed, "ports": json.RawMessage(`{}`)}, DeployPublicOutput{ContainerID: "abc123", PortsOpened: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := cxJSON(t, DeployPublicInput{HostID: cxHost, AcmeEmail: "ops@example.com", Integration: tc.integration})
			out, err := def.Handler.Output(context.Background(), core.Env{}, in, tc.steps)
			if err != nil {
				t.Fatal(err)
			}
			if got := cxStrict[DeployPublicOutput](t, out); got != tc.want {
				t.Errorf("output %+v, want %+v", got, tc.want)
			}
		})
	}
	in := cxJSON(t, DeployPublicInput{HostID: cxHost, AcmeEmail: "ops@example.com"})
	if _, err := def.Handler.Output(context.Background(), core.Env{}, in, map[string]json.RawMessage{}); err == nil || !strings.Contains(err.Error(), "deploy") {
		t.Errorf("output without the deploy step: %v, want an error naming deploy", err)
	}
}

// TestCxExposeContainerSteps: a public instance is required first, the proxy is attached to the target's network
// and holds a certificate before the proxy host is added, and the proxy host forwards to the container by name.
func TestCxExposeContainerSteps(t *testing.T) {
	tests := []struct {
		name string
		in   ExposeInput
		cert int // the certificate id the cert step answers
		want ProxyHostInput
	}{
		{"every option", ExposeInput{HostID: cxHost, Container: "web", Port: 8080, Network: "appnet", Domains: []string{"www.example.com", "app.example.com", "cdn.example.com"}, Websockets: true, AccessListID: 3}, 42,
			ProxyHostInput{Domains: []string{"www.example.com", "app.example.com", "cdn.example.com"}, ForwardScheme: "http", ForwardHost: "web", ForwardPort: 8080, CertificateID: 42, AccessListID: 3, SSLForced: true, HSTS: true, Websockets: true}},
		{"no options", ExposeInput{HostID: cxHost, Container: "api", Port: 80, Network: "backend", Domains: []string{"api.example.com"}}, 7,
			ProxyHostInput{Domains: []string{"api.example.com"}, ForwardScheme: "http", ForwardHost: "api", ForwardPort: 80, CertificateID: 7, SSLForced: true, HSTS: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			steps, err := cxSteps(t, newTHHost(t).nm().exposeContainer(), tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"public", "network", "cert", "proxy"}; !slices.Equal(cxIDs(steps), want) {
				t.Fatalf("steps %v, want %v", cxIDs(steps), want)
			}
			cxWantStep(t, steps[0], "public", "", "npm.public.check/v1", nil)
			if got := cxStrict[PublicInput](t, cxResolve(t, steps[0].Input, tc.in, nil)); got != (PublicInput{HostID: cxHost}) {
				t.Errorf("public input %+v, want host %s", got, cxHost)
			}
			cxWantStep(t, steps[1], "network", cxHost, "npm.network.attach/v1", []string{"public"})
			wantAttach := AttachInput{Network: tc.in.Network, Claimant: "expose:" + slices.Min(tc.in.Domains), Target: tc.in.Container}
			if got := cxStrict[AttachInput](t, cxResolve(t, steps[1].Input, tc.in, nil)); got != wantAttach {
				t.Errorf("attach input %+v, want %+v: the claimant names the smallest domain and the target is the container", got, wantAttach)
			}
			cxWantStep(t, steps[2], "cert", cxHost, "npm.certificate.request/v1", []string{"public"})
			if got := cxStrict[CertificateInput](t, cxResolve(t, steps[2].Input, tc.in, nil)); !reflect.DeepEqual(got, CertificateInput{Domains: tc.in.Domains}) {
				t.Errorf("certificate input %+v, want every domain", got)
			}
			cxWantStep(t, steps[3], "proxy", cxHost, "npm.proxyhost.add/v1", []string{"network", "cert"})
			outputs := map[string]any{"cert": CertificateView{ID: tc.cert}}
			if got := cxStrict[ProxyHostInput](t, cxResolve(t, steps[3].Input, tc.in, outputs)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("proxy input %+v, want %+v", got, tc.want)
			}
			if cxRefs(steps[3]) != 1 || cxRefs(steps[0])+cxRefs(steps[1])+cxRefs(steps[2]) != 0 {
				t.Errorf("step references: public %d, network %d, cert %d, proxy %d; want only the proxy step's reference to the certificate",
					cxRefs(steps[0]), cxRefs(steps[1]), cxRefs(steps[2]), cxRefs(steps[3]))
			}
		})
	}
}

// TestCxExposeContainerKeepsInputOutOfTheTemplate: container, network and domain names are typed by the operator and
// reach the steps only through references to the input.
func TestCxExposeContainerKeepsInputOutOfTheTemplate(t *testing.T) {
	in := ExposeInput{
		HostID: cxHost, Container: "${steps.cert.output.id}", Port: 80, Network: "${steps.network.output.attached}",
		Domains: []string{"${input.port}.example.com", "b.example.com"},
	}
	steps, err := cxSteps(t, newTHHost(t).nm().exposeContainer(), in)
	if err != nil {
		t.Fatal(err)
	}
	outputs := map[string]any{"cert": CertificateView{ID: 5}}
	attach := cxStrict[AttachInput](t, cxResolve(t, steps[1].Input, in, outputs))
	if attach.Network != in.Network || attach.Target != in.Container || attach.Claimant != "expose:"+in.Domains[0] {
		t.Errorf("attach input %+v, want the typed values unchanged", attach)
	}
	proxy := cxStrict[ProxyHostInput](t, cxResolve(t, steps[3].Input, in, outputs))
	if proxy.ForwardHost != in.Container || !reflect.DeepEqual(proxy.Domains, in.Domains) || proxy.CertificateID != 5 {
		t.Errorf("proxy input %+v, want the typed values unchanged and the certificate 5", proxy)
	}
}

func TestCxExposeContainerOutput(t *testing.T) {
	def := newTHHost(t).nm().exposeContainer()
	in := cxJSON(t, ExposeInput{HostID: cxHost, Container: "web", Port: 80, Network: "appnet", Domains: []string{"app.example.com"}})
	steps := map[string]json.RawMessage{
		"network": cxJSON(t, Attachment{Network: "appnet", Claimant: "expose:app.example.com", Attached: true}),
		"cert":    cxJSON(t, CertificateView{ID: 7}),
		"proxy":   cxJSON(t, ProxyHostView{ID: 11}),
	}
	out, err := def.Handler.Output(context.Background(), core.Env{}, in, steps)
	if err != nil {
		t.Fatal(err)
	}
	if got := cxStrict[ExposeOutput](t, out); got != (ExposeOutput{ProxyHostID: 11, CertificateID: 7}) {
		t.Errorf("output %+v, want proxy host 11 and certificate 7", got)
	}
	for _, missing := range []string{"proxy", "cert"} {
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
	for _, noID := range []string{"proxy", "cert"} {
		partial := maps.Clone(steps)
		partial[noID] = json.RawMessage(`{}`)
		if _, err := def.Handler.Output(context.Background(), core.Env{}, in, partial); err == nil || !strings.Contains(err.Error(), noID) {
			t.Errorf("output with a %s step that holds no id: %v, want an error naming it", noID, err)
		}
	}
}

// TestCxExposeRemoveSteps: the proxy host goes first, then every claim of the exposure on the proxy's networks. The
// claim is named by the smallest domain of the removed host, whichever of its domains the removal was given.
func TestCxExposeRemoveSteps(t *testing.T) {
	in := ExposeRemoveInput{HostID: cxHost, Domain: "www.example.com"}
	steps, err := cxSteps(t, newTHHost(t).nm().exposeRemove(), in)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"proxy", "network"}; !slices.Equal(cxIDs(steps), want) {
		t.Fatalf("steps %v, want %v", cxIDs(steps), want)
	}
	cxWantStep(t, steps[0], "proxy", cxHost, "npm.proxyhost.remove/v1", nil)
	if got := cxStrict[ProxyHostRemoveInput](t, cxResolve(t, steps[0].Input, in, nil)); got != (ProxyHostRemoveInput{Domain: "www.example.com"}) {
		t.Errorf("remove input %+v, want the domain and no id", got)
	}
	cxWantStep(t, steps[1], "network", cxHost, "npm.network.detach/v1", []string{"proxy"})
	outputs := map[string]any{"proxy": ProxyHostView{Domains: []string{"app.example.com", "www.example.com"}}}
	if got := cxStrict[DetachInput](t, cxResolve(t, steps[1].Input, in, outputs)); got != (DetachInput{Network: "", Claimant: "expose:app.example.com"}) {
		t.Errorf("detach input %+v, want every claim of expose:app.example.com", got)
	}
	if cxRefs(steps[0]) != 0 || cxRefs(steps[1]) != 1 {
		t.Errorf("the templates %s and %s hold %d and %d step references, want none in the proxy step and one, to its domains, in the network step",
			steps[0].Input, steps[1].Input, cxRefs(steps[0]), cxRefs(steps[1]))
	}
}

func TestCxExposeRemoveOutput(t *testing.T) {
	def := newTHHost(t).nm().exposeRemove()
	for _, domain := range []string{"app.example.com", "www.example.com"} {
		in := cxJSON(t, ExposeRemoveInput{HostID: cxHost, Domain: domain})
		steps := map[string]json.RawMessage{"proxy": cxJSON(t, ProxyHostView{ID: 11}), "network": cxJSON(t, struct{}{})}
		out, err := def.Handler.Output(context.Background(), core.Env{}, in, steps)
		if err != nil {
			t.Fatal(err)
		}
		if got := cxStrict[ExposeRemoveOutput](t, out); got != (ExposeRemoveOutput{Domain: domain}) {
			t.Errorf("output %+v, want domain %s", got, domain)
		}
	}
	in := cxJSON(t, ExposeRemoveInput{HostID: cxHost, Domain: "app.example.com"})
	if _, err := def.Handler.Output(context.Background(), core.Env{}, in, map[string]json.RawMessage{"proxy": cxJSON(t, ProxyHostView{ID: 11})}); err == nil || !strings.Contains(err.Error(), "network") {
		t.Errorf("output without the network step: %v, want an error naming network", err)
	}
}

// TestW10NbExposeContainerRefusesNoDomains: npm.expose.container with an empty domain list is refused as an invalid
// input at the schema, before Steps would name a claimant from a domain that does not exist.
func TestW10NbExposeContainerRefusesNoDomains(t *testing.T) {
	def := newTHHost(t).nm().exposeContainer()
	v := schema.NewValidator()
	empty := ExposeInput{HostID: cxHost, Container: "web", Port: 8080, Network: "appnet", Domains: []string{}}
	if err := v.ValidateInput(def, cxJSON(t, empty)); !regRefusal(err, "/domains") {
		t.Errorf("input with no domains: %v, want request.invalid_input naming /domains", err)
	}
	one := empty
	one.Domains = []string{"app.example.com"}
	if err := v.ValidateInput(def, cxJSON(t, one)); err != nil {
		t.Errorf("input with one domain: %v, want it accepted", err)
	}
}

func TestW12cDeployPublicV2Steps(t *testing.T) {
	type tc struct {
		name                       string
		integration, firewall, ip6 bool
		wantIDs                    []string
		wantRules                  []string // ids of the ports step's rules.apply input; nil for none
		opened                     bool
	}
	for _, c := range []tc{
		{"integration on", true, true, true, []string{"check", "deploy", "ports"}, nil, true},
		{"off with IPv6", false, true, true, []string{"check", "deploy", "ports"}, []string{PortRule80V4, PortRule443V4, PortRule80V6, PortRule443V6}, true},
		{"off without IPv6", false, true, false, []string{"check", "deploy", "ports"}, []string{PortRule80V4, PortRule443V4}, true},
		{"off on a host without firewall support", false, false, false, []string{"check", "deploy"}, nil, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			def := newTHHost(t).nm().instanceDeployPublicV2()
			in := DeployPublicInputV2{HostID: cxHost, AcmeEmail: "ops@example.com", Integration: c.integration, Firewall: c.firewall, IPv6: c.ip6}
			steps, err := cxSteps(t, def, in)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(cxIDs(steps), c.wantIDs) {
				t.Fatalf("steps %v, want %v", cxIDs(steps), c.wantIDs)
			}
			check := cxStrict[CheckInput](t, cxResolve(t, steps[0].Input, in, nil))
			if check.HostID != cxHost || check.Integration != c.integration || check.Firewall == nil || *check.Firewall != c.firewall || check.IPv6 == nil || *check.IPv6 != c.ip6 {
				t.Errorf("check input %+v, want host, integration %v, firewall %v, ipv6 %v", check, c.integration, c.firewall, c.ip6)
			}
			again, err := cxSteps(t, def, in)
			if err != nil || !reflect.DeepEqual(again, steps) {
				t.Errorf("a second Steps call differs (%v): the apply would refuse with plan.state_changed", err)
			}
			if c.integration {
				cxWantStep(t, steps[2], "ports", cxHost, "firewall.containers.apply/v1", []string{"deploy"})
				return
			}
			if c.wantRules == nil {
				return
			}
			cxWantStep(t, steps[2], "ports", cxHost, "firewall.rules.apply/v1", []string{"deploy"})
			ports := cxStrict[firewall.RulesApplyInput](t, steps[2].Input)
			var ids []string
			for _, r := range ports.Add {
				ids = append(ids, r.ID)
				if r.Action != model.ActionAllow || r.Direction != model.DirIn || r.Proto != model.ProtoTCP || r.From != model.Any || r.To != model.Any || r.Comment != "" {
					t.Errorf("rule %+v, want an allow in of TCP from any to any with no comment", r)
				}
			}
			if !slices.Equal(ids, c.wantRules) || ports.Label != PortsLabel || !ports.KeepManual {
				t.Errorf("ports input label %q keepManual %v ids %v, want %q, true, %v", ports.Label, ports.KeepManual, ids, PortsLabel, c.wantRules)
			}
			rules := cxAction(t, firewall.Module(), "firewall.rules.apply")
			if err := schema.NewValidator().ValidateInput(rules, steps[2].Input); err != nil {
				t.Errorf("ports input %s is invalid for firewall.rules.apply: %v", steps[2].Input, err)
			}
		})
	}
}

func TestW12cDeployPublicV2Output(t *testing.T) {
	def := newTHHost(t).nm().instanceDeployPublicV2()
	if s := def.Spec; s.Name != "npm.instance.deploy-public" || s.Version != 2 || s.Unwind != core.UnwindReverse || !slices.Equal(s.LockKeys, []string{"composite:npm"}) {
		t.Errorf("spec %+v, want npm.instance.deploy-public version 2, unwind reverse, lock composite:npm", s)
	}
	deployed := cxJSON(t, Deployed{ContainerID: "abc123", Image: "jc21/nginx-proxy-manager:2.13.5", AdminEmail: "ops@example.com"})
	for _, c := range []struct{ integration, firewall, want bool }{{true, false, true}, {false, true, true}, {false, false, false}} {
		steps := map[string]json.RawMessage{"deploy": deployed}
		if c.want {
			steps["ports"] = json.RawMessage(`{}`)
		}
		in := cxJSON(t, DeployPublicInputV2{HostID: cxHost, AcmeEmail: "ops@example.com", Integration: c.integration, Firewall: c.firewall})
		out, err := def.Handler.Output(context.Background(), core.Env{}, in, steps)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := cxStrict[DeployPublicOutput](t, out), (DeployPublicOutput{ContainerID: "abc123", PortsOpened: c.want}); got != want {
			t.Errorf("integration %v firewall %v: output %+v, want %+v", c.integration, c.firewall, got, want)
		}
	}
}
