package install

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// optModule is module opt: an uninstall step declaring opts, which records
// the options each run received.
type optModule struct {
	opts []core.UninstallOption
	got  []map[string]string
}

func (m *optModule) Manifest() core.Manifest { return core.Manifest{ID: "opt", Version: 1} }

func (m *optModule) UninstallOptions() []core.UninstallOption { return m.opts }

func (m *optModule) Uninstall(_ context.Context, _ core.Env, _ bool, opts map[string]string) error {
	m.got = append(m.got, maps.Clone(opts))
	return nil
}

// optPlain is module plain, which has no uninstall step.
type optPlain struct{}

func (optPlain) Manifest() core.Manifest { return core.Manifest{ID: "plain", Version: 1} }

var optRules = core.UninstallOption{Key: "rules", Values: []string{"remove", "keep", "baseline"}, Default: "remove",
	Help: "what happens to the rules"}

// optConfig is h's uninstall of the demo module, om and optPlain with the
// --module-option values given.
func optConfig(h *testHost, demo *demoModule, om *optModule, given map[string]map[string]string) UninstallConfig {
	cfg := h.uninstallConfig(demo, false)
	cfg.Modules = append(cfg.Modules, om, optPlain{})
	cfg.ModuleOptions = given
	return cfg
}

func TestPlanUninstallFillsModuleOptions(t *testing.T) {
	for _, tc := range []struct {
		name  string
		given map[string]map[string]string
		want  map[string]string
	}{
		{"default", nil, map[string]string{"rules": "remove"}},
		{"flag", map[string]map[string]string{"opt": {"rules": "keep"}}, map[string]string{"rules": "keep"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, m := installHost(t, RoleController)
			om := &optModule{opts: []core.UninstallOption{optRules}}
			if out, err := runUninstall(t, optConfig(h, m, om, tc.given)); err != nil {
				t.Fatalf("Run: %v\n%s", err, out)
			}
			if want := []map[string]string{tc.want}; !reflect.DeepEqual(om.got, want) {
				t.Errorf("Uninstall received %v, want %v", om.got, want)
			}
		})
	}
}

func TestPlanUninstallRefusesModuleOptions(t *testing.T) {
	h, m := installHost(t, RoleController)
	for _, tc := range []struct {
		name  string
		given map[string]map[string]string
		want  string
	}{
		{"module not compiled in", map[string]map[string]string{"nope": {"rules": "keep"}}, "module nope is not compiled into this binary"},
		{"module without an uninstall step", map[string]map[string]string{"plain": {"rules": "keep"}}, "module plain has no uninstall step"},
		{"undeclared key", map[string]map[string]string{"opt": {"colour": "red"}}, "module opt has no uninstall option colour"},
		{"value outside the values", map[string]map[string]string{"opt": {"rules": "wipe"}},
			"opt.rules=wipe: the value must be one of remove, keep, baseline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			om := &optModule{opts: []core.UninstallOption{optRules}}
			_, err := PlanUninstall(context.Background(), optConfig(h, m, om, tc.given))
			if e := errcode.From(err); e.Code != errcode.InvalidInput || !strings.Contains(e.Detail, tc.want) {
				t.Fatalf("PlanUninstall = %v, want request.invalid_input with %q", err, tc.want)
			}
			if !exists(t, h.layout.Paths.Binary()) || len(om.got) != 0 || len(h.systemd.recorded()) != 0 {
				t.Error("a refused plan changed the host")
			}
		})
	}
}

func TestPlanUninstallRefusesBadDeclarations(t *testing.T) {
	h, m := installHost(t, RoleController)
	for _, tc := range []struct {
		name string
		opts []core.UninstallOption
		want string
	}{
		{"default outside the values", []core.UninstallOption{{Key: "rules", Values: []string{"remove", "keep"}, Default: "wipe"}},
			`the default "wipe" of the uninstall option rules is not one of its values`},
		{"no values", []core.UninstallOption{{Key: "rules", Default: "remove"}}, "the uninstall option rules has no values"},
		{"key with a capital", []core.UninstallOption{{Key: "Rules", Values: []string{"remove"}, Default: "remove"}},
			`the uninstall option key "Rules" does not match`},
		{"repeated key", []core.UninstallOption{optRules, optRules}, "the uninstall option rules is declared twice"},
		{"help on two lines", []core.UninstallOption{{Key: "rules", Values: []string{"remove"}, Default: "remove", Help: "a\nb"}},
			"the help of the uninstall option rules is more than one line"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := PlanUninstall(context.Background(), optConfig(h, m, &optModule{opts: tc.opts}, nil))
			var coded *errcode.Error
			if err == nil || errors.As(err, &coded) || !strings.Contains(err.Error(), "module opt: "+tc.want) {
				t.Fatalf("PlanUninstall = %v, want a plain error naming module opt and %q", err, tc.want)
			}
		})
	}
}

func TestDescribeModuleOptions(t *testing.T) {
	h, m := installHost(t, RoleController)
	om := &optModule{opts: []core.UninstallOption{optRules}}
	u, err := PlanUninstall(context.Background(), optConfig(h, m, om, map[string]map[string]string{"opt": {"rules": "keep"}}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = u.Close() }()
	var out bytes.Buffer
	u.Describe(&out)
	plan := out.String()
	step := strings.Index(plan, "run each module's uninstall step\n")
	line := strings.Index(plan, "     opt.rules=keep (default remove; what happens to the rules)\n")
	next := strings.Index(plan, ". marked blocks\n")
	if step < 0 || line < step || next < line {
		t.Errorf("the option line is not under the modules step:\n%s", plan)
	}
}
