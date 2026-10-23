package runtime

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
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
