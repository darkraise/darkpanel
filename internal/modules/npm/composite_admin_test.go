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
)

// caHost is the NPM host of these tests; the test host's own id ("h1") is deliberately another.
const caHost = "h7"

// caSteps asks def for its steps the way the engine does, through the erased handler.
func caSteps(t *testing.T, def core.ActionDef, in any) ([]core.Step, error) {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	return def.Handler.Steps(context.Background(), core.Env{}, raw)
}

// caGeneric is v as decoded JSON.
func caGeneric(t *testing.T, v any) any {
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

var caRef = regexp.MustCompile(`\$\{([^}]*)\}`)

// caResolve resolves the references of a step template against the composite's input in and the outputs of earlier
// steps, as the engine does: a string that is exactly one reference becomes the referenced value with its JSON type,
// a reference inside a longer string is replaced by the value's text, and a value that was substituted is never read
// as a template again. A reference to a member that does not exist fails the test, as it fails the plan.
func caResolve(t *testing.T, tmpl json.RawMessage, in any, outputs map[string]any) json.RawMessage {
	t.Helper()
	input := caGeneric(t, in)
	steps := map[string]any{}
	for id, v := range outputs {
		steps[id] = map[string]any{"output": caGeneric(t, v)}
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
			if m := caRef.FindStringSubmatch(x); m != nil && m[0] == x {
				return lookup(m[1])
			}
			return caRef.ReplaceAllStringFunc(x, func(s string) string { return fmt.Sprint(lookup(s[2 : len(s)-1])) })
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

// caStrict decodes raw into a T and refuses a member T does not have.
func caStrict[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("input %s: %v", raw, err)
	}
	return v
}

// caWantStep fails the test unless s is the step id on host running action after the steps after.
func caWantStep(t *testing.T, s core.Step, id, host, action string, after []string) {
	t.Helper()
	if s.ID != id || s.Host != host || s.Action.String() != action || !slices.Equal(s.After, after) {
		t.Errorf("step %+v, want %s: %s on host %q after %v", s, id, action, host, after)
	}
}

func caIDs(steps []core.Step) []string {
	var ids []string
	for _, s := range steps {
		ids = append(ids, s.ID)
	}
	return ids
}

// caRefs counts the step references in the template of s.
func caRefs(s core.Step) int { return strings.Count(string(s.Input), "${steps.") }

func caJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestCaDeclarations: both are controller-side composites that take the module's coordination lock; the exposure
// unwinds in reverse, the removal forward.
func TestCaDeclarations(t *testing.T) {
	n := newTHHost(t).nm()
	tests := []struct {
		name   string
		def    core.ActionDef
		unwind core.Unwind
	}{
		{"npm.admin.expose", n.adminExpose(), core.UnwindReverse},
		{"npm.admin.unexpose", n.adminUnexpose(), core.UnwindForward},
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

// TestCaAdminExposeSteps: a public instance is required first, the hostname gets a certificate, and the proxy host
// forwards it to NPM's own admin port over HTTP with forced SSL and HSTS, behind the access list when one is given.
func TestCaAdminExposeSteps(t *testing.T) {
	tests := []struct {
		name string
		in   AdminExposeInput
		want ProxyHostInput
	}{
		{"with an access list", AdminExposeInput{HostID: caHost, Hostname: "npm.example.com", AccessListID: 5},
			ProxyHostInput{Domains: []string{"npm.example.com"}, ForwardScheme: "http", ForwardHost: "127.0.0.1", ForwardPort: 81, CertificateID: 42, AccessListID: 5, SSLForced: true, HSTS: true}},
		{"without one", AdminExposeInput{HostID: caHost, Hostname: "admin.example.org"},
			ProxyHostInput{Domains: []string{"admin.example.org"}, ForwardScheme: "http", ForwardHost: "127.0.0.1", ForwardPort: 81, CertificateID: 42, SSLForced: true, HSTS: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			steps, err := caSteps(t, newTHHost(t).nm().adminExpose(), tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"public", "cert", "proxy"}; !slices.Equal(caIDs(steps), want) {
				t.Fatalf("steps %v, want %v", caIDs(steps), want)
			}
			caWantStep(t, steps[0], "public", "", "npm.public.check/v1", nil)
			if got := caStrict[PublicInput](t, caResolve(t, steps[0].Input, tc.in, nil)); got != (PublicInput{HostID: caHost}) {
				t.Errorf("public input %+v, want host %s", got, caHost)
			}
			caWantStep(t, steps[1], "cert", caHost, "npm.certificate.request/v1", []string{"public"})
			if got := caStrict[CertificateInput](t, caResolve(t, steps[1].Input, tc.in, nil)); !reflect.DeepEqual(got, CertificateInput{Domains: []string{tc.in.Hostname}}) {
				t.Errorf("certificate input %+v, want the hostname alone", got)
			}
			caWantStep(t, steps[2], "proxy", caHost, "npm.proxyhost.add/v1", []string{"cert"})
			outputs := map[string]any{"cert": CertificateView{ID: 42}}
			if got := caStrict[ProxyHostInput](t, caResolve(t, steps[2].Input, tc.in, outputs)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("proxy input %+v, want %+v", got, tc.want)
			}
			if caRefs(steps[2]) != 1 || caRefs(steps[0])+caRefs(steps[1]) != 0 {
				t.Errorf("step references: public %d, cert %d, proxy %d; want only the proxy step's reference to the certificate",
					caRefs(steps[0]), caRefs(steps[1]), caRefs(steps[2]))
			}
		})
	}
}

// TestCaAdminExposeKeepsInputOutOfTheTemplate: the hostname is typed by the operator and reaches the steps only
// through references to the input.
func TestCaAdminExposeKeepsInputOutOfTheTemplate(t *testing.T) {
	in := AdminExposeInput{HostID: caHost, Hostname: "${steps.cert.output.id}.example.com"}
	steps, err := caSteps(t, newTHHost(t).nm().adminExpose(), in)
	if err != nil {
		t.Fatal(err)
	}
	outputs := map[string]any{"cert": CertificateView{ID: 9}}
	if got := caStrict[CertificateInput](t, caResolve(t, steps[1].Input, in, outputs)); !reflect.DeepEqual(got.Domains, []string{in.Hostname}) {
		t.Errorf("certificate domains %v, want the typed hostname unchanged", got.Domains)
	}
	proxy := caStrict[ProxyHostInput](t, caResolve(t, steps[2].Input, in, outputs))
	if !reflect.DeepEqual(proxy.Domains, []string{in.Hostname}) || proxy.CertificateID != 9 {
		t.Errorf("proxy input %+v, want the typed hostname unchanged and certificate 9", proxy)
	}
}

func TestCaAdminExposeOutput(t *testing.T) {
	def := newTHHost(t).nm().adminExpose()
	in := caJSON(t, AdminExposeInput{HostID: caHost, Hostname: "npm.example.com"})
	steps := map[string]json.RawMessage{"cert": caJSON(t, CertificateView{ID: 7}), "proxy": caJSON(t, ProxyHostView{ID: 11})}
	out, err := def.Handler.Output(context.Background(), core.Env{}, in, steps)
	if err != nil {
		t.Fatal(err)
	}
	if got := caStrict[AdminExposeOutput](t, out); got != (AdminExposeOutput{ProxyHostID: 11, CertificateID: 7}) {
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

// TestCaAdminUnexposeSteps: the one step removes the proxy host of the hostname; the certificate stays.
func TestCaAdminUnexposeSteps(t *testing.T) {
	in := AdminUnexposeInput{HostID: caHost, Hostname: "npm.example.com"}
	steps, err := caSteps(t, newTHHost(t).nm().adminUnexpose(), in)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"proxy"}; !slices.Equal(caIDs(steps), want) {
		t.Fatalf("steps %v, want %v", caIDs(steps), want)
	}
	caWantStep(t, steps[0], "proxy", caHost, "npm.proxyhost.remove/v1", nil)
	if got := caStrict[ProxyHostRemoveInput](t, caResolve(t, steps[0].Input, in, nil)); got != (ProxyHostRemoveInput{Domain: "npm.example.com"}) {
		t.Errorf("remove input %+v, want the hostname as the domain and no id", got)
	}
	if caRefs(steps[0]) != 0 {
		t.Errorf("template %s holds a step reference, want references to the input only", steps[0].Input)
	}
	hostile := AdminUnexposeInput{HostID: caHost, Hostname: "${steps.proxy.output.id}"}
	steps, err = caSteps(t, newTHHost(t).nm().adminUnexpose(), hostile)
	if err != nil {
		t.Fatal(err)
	}
	if got := caStrict[ProxyHostRemoveInput](t, caResolve(t, steps[0].Input, hostile, nil)); got.Domain != hostile.Hostname {
		t.Errorf("remove domain %q, want the typed hostname unchanged", got.Domain)
	}
}

func TestCaAdminUnexposeOutput(t *testing.T) {
	def := newTHHost(t).nm().adminUnexpose()
	for _, hostname := range []string{"npm.example.com", "admin.example.org"} {
		in := caJSON(t, AdminUnexposeInput{HostID: caHost, Hostname: hostname})
		out, err := def.Handler.Output(context.Background(), core.Env{}, in, map[string]json.RawMessage{"proxy": caJSON(t, ProxyHostView{ID: 11})})
		if err != nil {
			t.Fatal(err)
		}
		if got := caStrict[AdminUnexposeOutput](t, out); got != (AdminUnexposeOutput{Hostname: hostname}) {
			t.Errorf("output %+v, want hostname %s", got, hostname)
		}
	}
	in := caJSON(t, AdminUnexposeInput{HostID: caHost, Hostname: "npm.example.com"})
	if _, err := def.Handler.Output(context.Background(), core.Env{}, in, map[string]json.RawMessage{}); err == nil || !strings.Contains(err.Error(), "proxy") {
		t.Errorf("output without the proxy step: %v, want an error naming proxy", err)
	}
}
