package coreactions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
)

// pushAgent is h1's agent: its preflight passes, and each SetModuleEnabled
// is recorded with the enabled the controller had stored when it came, then
// answered with fail.
func (e *testEnv) pushAgent(fail error) *[]string {
	pushed := &[]string{}
	e.hosts["h1"] = &coretest.FakeAgent{
		PreflightFunc: func(_ context.Context, id string) (core.ModuleStatus, error) {
			return core.ModuleStatus{ID: id, Version: 3, Available: true}, nil
		},
		SetModuleEnabledFunc: func(ctx context.Context, id string, enabled bool) error {
			var stored bool
			if err := e.db.Read().QueryRowContext(ctx,
				"SELECT enabled FROM core_module_state WHERE host_id = 'h1' AND module_id = ?", id).Scan(&stored); err != nil {
				e.t.Error(err)
			}
			*pushed = append(*pushed, fmt.Sprintf("%s enabled=%t stored=%t", id, enabled, stored))
			return fail
		},
	}
	return pushed
}

func TestEnableModulePushesFlag(t *testing.T) {
	e := newModuleEnv(t)
	pushed := e.pushAgent(nil)
	in := ModuleInput{HostID: "h1", ModuleID: "demo"}
	e.run(operator, "core.module.enable", in, nil)
	e.run(operator, "core.module.disable", in, nil)
	if want := []string{"demo enabled=true stored=true", "demo enabled=false stored=false"}; !reflect.DeepEqual(*pushed, want) {
		t.Errorf("pushes = %q, want %q", *pushed, want)
	}
}

func TestEnablementPushFailureIsLogged(t *testing.T) {
	gone := errors.New("the agent went away")
	tests := []struct {
		name, action, logged string
		offline              bool
	}{
		{"enable, push fails", "core.module.enable", gone.Error(), false},
		{"disable, push fails", "core.module.disable", gone.Error(), false},
		{"disable, host offline", "core.module.disable", "host h1 not found", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newModuleEnv(t)
			e.pushAgent(gone)
			if tt.offline {
				delete(e.hosts, "h1")
			}
			in := ModuleInput{HostID: "h1", ModuleID: "demo"}
			e.run(operator, tt.action, in, nil)

			// The handler alone, with a log to read.
			def, ok := e.reg.Action(ref(tt.action))
			if !ok {
				t.Fatalf("%s is not registered", tt.action)
			}
			var buf bytes.Buffer
			env := core.Env{DB: e.db, Clock: e.clock, Log: slog.New(slog.NewTextHandler(&buf, nil)), Agents: e.hosts,
				Principal: operator}
			if _, err := def.Handler.Apply(context.Background(), env, encodeInput(t, in)); err != nil {
				t.Fatalf("Apply = %v, want the push failure only logged", err)
			}
			log := buf.String()
			if !strings.Contains(log, `level=WARN msg="coreactions: push module enablement" host=h1 module=demo`) ||
				!strings.Contains(log, tt.logged) {
				t.Errorf("log = %q, want the failed push with %q", log, tt.logged)
			}
		})
	}
}
