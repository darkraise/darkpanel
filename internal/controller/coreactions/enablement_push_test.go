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
	"github.com/darkraise/darkpanel/internal/core/errcode"
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

// pushRepushes wraps a host directory and records the hosts whose enablement
// the action asked to have pushed again; agentErr, when set, is every
// Agent's answer.
type pushRepushes struct {
	core.HostDirectory
	agentErr error
	hosts    []string
}

func (r *pushRepushes) Agent(ctx context.Context, hostID string) (core.AgentClient, error) {
	if r.agentErr != nil {
		return nil, r.agentErr
	}
	return r.HostDirectory.Agent(ctx, hostID)
}

func (r *pushRepushes) RepushEnablement(hostID string) { r.hosts = append(r.hosts, hostID) }

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
	noRoute := errcode.New(errcode.NotFound, "no agent API route PUT /agent/v1/modules/demo/enabled")
	offline := errcode.New(errcode.HostOffline, "host h1 is offline")
	tests := []struct {
		name, action, logged string
		missing              bool
		pushErr, agentErr    error
		repushed             []string
	}{
		{"enable, push fails", "core.module.enable", gone.Error(), false, gone, nil, []string{"h1"}},
		{"disable, push fails", "core.module.disable", gone.Error(), false, gone, nil, []string{"h1"}},
		{"disable, host unknown", "core.module.disable", "host h1 not found", true, gone, nil, nil},
		{"enable, agent without the route", "core.module.enable", "no agent API route", false, noRoute, nil, nil},
		{"disable, host offline", "core.module.disable", "host h1 is offline", false, gone, offline, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newModuleEnv(t)
			e.pushAgent(tt.pushErr)
			if tt.missing {
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
			dir := &pushRepushes{HostDirectory: e.hosts, agentErr: tt.agentErr}
			env := core.Env{DB: e.db, Clock: e.clock, Log: slog.New(slog.NewTextHandler(&buf, nil)), Agents: dir,
				Principal: operator}
			if _, err := def.Handler.Apply(context.Background(), env, encodeInput(t, in)); err != nil {
				t.Fatalf("Apply = %v, want the push failure only logged", err)
			}
			log := buf.String()
			if !strings.Contains(log, `level=WARN msg="coreactions: push module enablement" host=h1 module=demo`) ||
				!strings.Contains(log, tt.logged) {
				t.Errorf("log = %q, want the failed push with %q", log, tt.logged)
			}
			if !reflect.DeepEqual(dir.hosts, tt.repushed) {
				t.Errorf("hosts asked for another push = %q, want %q", dir.hosts, tt.repushed)
			}
		})
	}
}
