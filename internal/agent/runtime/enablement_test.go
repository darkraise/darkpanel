package runtime

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// enModule is a module with nothing but its id and its DefaultEnabled.
type enModule struct {
	id string
	on bool
}

func (m enModule) Manifest() core.Manifest {
	return core.Manifest{ID: m.id, Version: 1, DefaultEnabled: m.on}
}

// enMeta reads module's core_meta flag; ok is false when it has none.
func (f *fixture) enMeta(module string) (value string, ok bool) {
	f.t.Helper()
	err := f.db.Read().QueryRow("SELECT value FROM core_meta WHERE key = ?", MetaModuleEnabledPrefix+module).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	if err != nil {
		f.t.Fatal(err)
	}
	return value, true
}

// enEnabled is moduleEnabled that fails the test on an error.
func (f *fixture) enEnabled(module string) bool {
	f.t.Helper()
	on, err := f.rt.moduleEnabled(context.Background(), module)
	if err != nil {
		f.t.Fatal(err)
	}
	return on
}

func TestSetModuleEnabled(t *testing.T) {
	f := newFixtureWith(t, enModule{id: "en", on: true})
	ctx := context.Background()
	for _, tc := range []struct {
		enabled bool
		stored  string
	}{{false, "0"}, {true, "1"}, {false, "0"}} {
		if err := f.rt.SetModuleEnabled(ctx, "en", tc.enabled); err != nil {
			t.Fatal(err)
		}
		if v, ok := f.enMeta("en"); !ok || v != tc.stored {
			t.Errorf("after SetModuleEnabled(%t) module_enabled.en = %q (present %t), want %q", tc.enabled, v, ok, tc.stored)
		}
		if got := f.enEnabled("en"); got != tc.enabled {
			t.Errorf("after SetModuleEnabled(%t) moduleEnabled = %t", tc.enabled, got)
		}
	}
	f.restart()
	if f.enEnabled("en") {
		t.Error("after a restart moduleEnabled = true, want the stored false")
	}
}

func TestModuleEnabledDefault(t *testing.T) {
	f := newFixtureWith(t, enModule{id: "en", on: true}, enModule{id: "enoff"})
	for _, tc := range []struct {
		module string
		want   bool
	}{{"en", true}, {"enoff", false}} {
		if got := f.enEnabled(tc.module); got != tc.want {
			t.Errorf("moduleEnabled(%s) without a flag = %t, want its DefaultEnabled %t", tc.module, got, tc.want)
		}
		if _, ok := f.enMeta(tc.module); ok {
			t.Errorf("reading %s's flag wrote one", tc.module)
		}
	}
}

func TestSetModuleEnabledUnknown(t *testing.T) {
	f := newFixtureWith(t, enModule{id: "en", on: true})
	_ = wantCode(t, f.rt.SetModuleEnabled(context.Background(), "nope", true), errcode.NotFound)
	if n := f.count("SELECT count(*) FROM core_meta WHERE key LIKE ?", MetaModuleEnabledPrefix+"%"); n != 0 {
		t.Errorf("%d enabled flags stored, want none", n)
	}
}

// hookModule records each OnEnabled call and fails each one with fail. Its
// id is "hooked" unless id names another.
type hookModule struct {
	id    string
	calls *[]bool
	fail  error
}

func (m hookModule) Manifest() core.Manifest {
	id := m.id
	if id == "" {
		id = "hooked"
	}
	return core.Manifest{ID: id, Version: 1}
}

func (m hookModule) OnEnabled(_ context.Context, env core.Env, enabled bool) error {
	if env.DB == nil {
		return errors.New("no DB in the hook's env")
	}
	*m.calls = append(*m.calls, enabled)
	return m.fail
}

// TestEnablementHook calls a module's hook once at start with the stored
// flag and after every flag change; a failing hook leaves the flag stored.
func TestEnablementHook(t *testing.T) {
	ctx := context.Background()
	var calls []bool
	f := newFixtureWith(t, hookModule{calls: &calls, fail: errors.New("refused")})
	if want := []bool{false}; !slices.Equal(calls, want) {
		t.Fatalf("calls at start %v, want %v (the module's DefaultEnabled)", calls, want)
	}
	for _, enabled := range []bool{false, true} {
		if err := f.rt.SetModuleEnabled(ctx, "hooked", enabled); err != nil {
			t.Fatalf("SetModuleEnabled(%t) with a failing hook: %v", enabled, err)
		}
	}
	if want := []bool{false, false, true}; !slices.Equal(calls, want) {
		t.Errorf("calls %v, want %v", calls, want)
	}
	if v, ok := f.enMeta("hooked"); !ok || v != "1" {
		t.Errorf("module_enabled.hooked = %q (present %t) after a failing hook, want 1", v, ok)
	}
	calls = nil
	f.restart()
	if want := []bool{true}; !slices.Equal(calls, want) {
		t.Errorf("calls at a restart %v, want %v (the stored flag)", calls, want)
	}
	t.Run("denied by policy", testEnablementHookDenied)
}

// testEnablementHookDenied calls the hook of a module the policy file denies
// with false, at start and when the controller pushes true, which is refused.
func testEnablementHookDenied(t *testing.T) {
	ctx := context.Background()
	var calls []bool
	h := &fixHost{}
	reg, err := core.NewRegistry(kvModule{}, badModule{h}, flakyModule{h}, boomModule{}, hookModule{id: "jobs", calls: &calls})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, host: h, clock: coretest.NewFakeClock(t0), reg: reg, dir: t.TempDir()}
	f.policy = writePolicy(t, f.dir, `{"denyModules":["jobs"]}`, 0o644)
	f.open()
	if err := f.rt.SetModuleEnabled(ctx, "jobs", true); err == nil {
		t.Error("SetModuleEnabled(jobs, true) under a policy that denies jobs succeeded")
	}
	f.restart()
	if want := []bool{false, false, false}; !slices.Equal(calls, want) {
		t.Errorf("calls %v, want %v (start, the refused push, the restart with the stored true)", calls, want)
	}
	if v, ok := f.enMeta("jobs"); !ok || v != "1" {
		t.Errorf("module_enabled.jobs = %q (present %t), want the pushed 1 kept for when the policy allows it", v, ok)
	}
}
