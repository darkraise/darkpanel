package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// writePolicy writes a policy file with text and mode in dir and returns its
// path.
func writePolicy(t *testing.T, dir, text string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, "agent-policy.json")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// policyModule is a module named id with one agent read action,
// id.item.get, and a hook that records each call in calls.
type policyModule struct {
	id    string
	calls *[]bool
}

func (m policyModule) Manifest() core.Manifest {
	return core.Manifest{ID: m.id, Version: 1, Actions: []core.ActionDef{
		core.DefineRead(core.ActionSpec{Name: m.id + ".item.get", Version: 1, Side: core.SideAgent}, echo{}),
	}}
}

func (m policyModule) OnEnabled(_ context.Context, _ core.Env, enabled bool) error {
	*m.calls = append(*m.calls, enabled)
	return nil
}

// policyFixture is a runtime with the modules console and jobs, opened
// with the policy file text (none when text is "").
func policyFixture(t *testing.T, text string) (f *fixture, console, jobs *[]bool) {
	t.Helper()
	console, jobs = &[]bool{}, &[]bool{}
	h := &fixHost{}
	reg, err := core.NewRegistry(kvModule{}, badModule{h}, flakyModule{h}, boomModule{},
		policyModule{id: "console", calls: console}, policyModule{id: "jobs", calls: jobs})
	if err != nil {
		t.Fatal(err)
	}
	f = &fixture{t: t, host: h, clock: coretest.NewFakeClock(t0), reg: reg, dir: t.TempDir()}
	f.policy = filepath.Join(f.dir, "agent-policy.json")
	if text != "" {
		writePolicy(t, f.dir, text, 0o644)
	}
	f.open()
	return f, console, jobs
}

// helloStatus is module's status in the runtime's Hello.
func (f *fixture) helloStatus(module string) core.ModuleStatus {
	f.t.Helper()
	h, err := f.rt.Hello(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	for _, st := range h.Modules {
		if st.ID == module {
			return st
		}
	}
	f.t.Fatalf("Hello reports no module %s", module)
	return core.ModuleStatus{}
}

// wantDenied checks module reports unavailable with the policy's reason and
// that its actions are refused.
func (f *fixture) wantDenied(module string) {
	f.t.Helper()
	if st := f.helloStatus(module); st.Available || st.Reason != PolicyReason {
		f.t.Errorf("Hello: %s available %t reason %q, want unavailable with %q", module, st.Available, st.Reason, PolicyReason)
	}
	av, err := f.rt.Availability(context.Background(), module)
	if err != nil {
		f.t.Fatal(err)
	}
	if av.Available || av.Reason != PolicyReason {
		f.t.Errorf("Availability: %s available %t reason %q, want unavailable with %q", module, av.Available, av.Reason, PolicyReason)
	}
	_, err = f.rt.Read(context.Background(), alice, core.ActionRef{Name: module + ".item.get", Version: 1}, json.RawMessage(`{"name":"a"}`))
	if e := wantCode(f.t, err, errcode.ModuleUnavailable); !strings.Contains(e.Detail, PolicyReason) {
		f.t.Errorf("Read of a denied module: %q, want the policy's reason", e.Detail)
	}
}

func (f *fixture) wantAllowed(module string) {
	f.t.Helper()
	if st := f.helloStatus(module); !st.Available {
		f.t.Errorf("Hello: %s unavailable (%q), want available", module, st.Reason)
	}
	if _, err := f.rt.Read(context.Background(), alice, core.ActionRef{Name: module + ".item.get", Version: 1}, json.RawMessage(`{"name":"a"}`)); err != nil {
		f.t.Errorf("Read of %s: %v", module, err)
	}
}

func TestDeniedModule(t *testing.T) {
	f, console, _ := policyFixture(t, `{"denyModules":["console","jobs"]}`)
	e := wantCode(t, f.rt.SetModuleEnabled(context.Background(), "console", true), errcode.ModuleUnavailable)
	if !strings.Contains(e.Detail, PolicyReason) {
		t.Errorf("SetModuleEnabled(console, true): %q, want the policy's reason", e.Detail)
	}
	f.wantDenied("console")
	f.wantDenied("jobs")
	if !f.rt.Policy().Denied("jobs") || !f.rt.Policy().Denied("console") || f.rt.Policy().Denied("kv") {
		t.Errorf("Denied: console %t jobs %t kv %t, want true true false",
			f.rt.Policy().Denied("console"), f.rt.Policy().Denied("jobs"), f.rt.Policy().Denied("kv"))
	}
	if want := []bool{false, false}; !slices.Equal(*console, want) {
		t.Errorf("console hook calls %v, want %v", *console, want)
	}
	f.wantAllowed("kv")
}

func TestPolicyValidation(t *testing.T) {
	uid := uint32(os.Getuid()) //nolint:gosec // G115: a uid fits in 32 bits
	for _, tc := range []struct {
		name  string
		text  string
		mode  os.FileMode
		owner uint32
		err   string // "" when the file is valid
		deny  []string
	}{
		{name: "both", text: `{"denyModules":["console","jobs"]}`, mode: 0o644, owner: uid, deny: []string{"console", "jobs"}},
		{name: "one", text: `{"denyModules":["jobs"]}`, mode: 0o600, owner: uid, deny: []string{"jobs"}},
		{name: "none", text: `{"denyModules":[]}`, mode: 0o644, owner: uid},
		{name: "another module", text: `{"denyModules":["console","firewall"]}`, mode: 0o644, owner: uid, err: "firewall"},
		{name: "group writable", text: `{"denyModules":["jobs"]}`, mode: 0o664, owner: uid, err: "writable"},
		{name: "others writable", text: `{"denyModules":["jobs"]}`, mode: 0o646, owner: uid, err: "writable"},
		{name: "not root's", text: `{"denyModules":["jobs"]}`, mode: 0o644, owner: uid + 1, err: "owned"},
		{name: "unknown member", text: `{"denyModules":["jobs"],"allow":["console"]}`, mode: 0o644, owner: uid, err: "allow"},
		{name: "not JSON", text: `deny console`, mode: 0o644, owner: uid, err: "invalid"},
		{name: "trailing data", text: `{"denyModules":[]} {"denyModules":["jobs"]}`, mode: 0o644, owner: uid, err: "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writePolicy(t, t.TempDir(), tc.text, tc.mode)
			p, err := ReadPolicy(path, tc.owner)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("ReadPolicy = %v, want an error naming %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range []string{"console", "jobs"} {
				if p.Denied(m) != slices.Contains(tc.deny, m) {
					t.Errorf("Denied(%s) = %t, want %t", m, p.Denied(m), slices.Contains(tc.deny, m))
				}
			}
		})
	}
	t.Run("absent", func(t *testing.T) {
		p, err := ReadPolicy(filepath.Join(t.TempDir(), "agent-policy.json"), uid)
		if err != nil || p.Denied("console") || p.Denied("jobs") {
			t.Errorf("ReadPolicy of an absent file = %v, %v; want a policy denying nothing", p, err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := writePolicy(t, dir, `{"denyModules":[]}`, 0o644)
		link := filepath.Join(dir, "link.json")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadPolicy(link, uid); err == nil {
			t.Error("ReadPolicy followed a symlink")
		}
	})
	t.Run("invalid at start denies both", func(t *testing.T) {
		f, _, jobs := policyFixture(t, `{"denyModules":["firewall"]}`)
		f.wantDenied("console")
		f.wantDenied("jobs")
		if want := []bool{false}; !slices.Equal(*jobs, want) {
			t.Errorf("jobs hook calls %v, want %v", *jobs, want)
		}
	})
	t.Run("invalid at a reload keeps the previous policy", func(t *testing.T) {
		f, _, _ := policyFixture(t, `{"denyModules":["jobs"]}`)
		writePolicy(t, f.dir, `{"denyModules":["shell"]}`, 0o644)
		if err := f.rt.ReloadPolicy(context.Background()); err == nil || !strings.Contains(err.Error(), "shell") {
			t.Fatalf("ReloadPolicy of an invalid file = %v, want an error naming shell", err)
		}
		f.wantDenied("jobs")
		f.wantAllowed("console")
	})
}

func TestPolicyReloadDenies(t *testing.T) {
	ctx := context.Background()
	f, console, jobs := policyFixture(t, "")
	for _, m := range []string{"console", "jobs"} {
		if err := f.rt.SetModuleEnabled(ctx, m, true); err != nil {
			t.Fatal(err)
		}
		f.wantAllowed(m)
	}
	writePolicy(t, f.dir, `{"denyModules":["console","jobs"]}`, 0o644)
	if err := f.rt.ReloadPolicy(ctx); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"console", "jobs"} {
		f.wantDenied(m)
		if v, ok := f.enMeta(m); !ok || v != "1" {
			t.Errorf("module_enabled.%s = %q (present %t) after the policy denied it, want 1", m, v, ok)
		}
	}
	// A reload that changes nothing calls no hook.
	if err := f.rt.ReloadPolicy(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.policy); err != nil {
		t.Fatal(err)
	}
	if err := f.rt.ReloadPolicy(ctx); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"console", "jobs"} {
		f.wantAllowed(m)
	}
	// start (DefaultEnabled false), the push of true, the deny, the allow.
	for name, calls := range map[string][]bool{"console": *console, "jobs": *jobs} {
		if want := []bool{false, true, false, true}; !slices.Equal(calls, want) {
			t.Errorf("%s hook calls %v, want %v", name, calls, want)
		}
	}
}
