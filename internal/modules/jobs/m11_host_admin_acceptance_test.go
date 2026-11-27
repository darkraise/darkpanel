package jobs_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/jobs"
)

type runner struct{ bad bool }

func (r runner) Run(_ context.Context, c core.Cmd) (core.CmdResult, error) {
	if strings.HasSuffix(c.Path, "systemd-analyze") {
		if r.bad {
			return core.CmdResult{ExitCode: 1, Stderr: []byte("Failed to parse calendar specification 'every other tuesday': Invalid argument\n")}, nil
		}
		return core.CmdResult{Stdout: []byte("  Original form: daily\nNormalized form: *-*-* 00:00:00\n    Next elapse: Fri 2026-11-27 00:00:00 UTC\n       (in UTC): Fri 2026-11-27 00:00:00 UTC\n       From now: 15h left\n       Iter. #2: Sat 2026-11-28 00:00:00 UTC\n       Iter. #3: Sun 2026-11-29 00:00:00 UTC\n")}, nil
	}
	return core.CmdResult{}, nil
}
func (runner) Stream(context.Context, string, []string, func([]byte) error) error { return nil }

var admin = core.Principal{Actor: "user:admin", UserID: "admin", Scope: core.ScopeAdmin, Source: "192.0.2.1"}

func action(t *testing.T, name string) core.ActionDef {
	t.Helper()
	for _, a := range jobs.Module().Manifest().Actions {
		if a.Spec.Name == name {
			return a
		}
	}
	t.Fatalf("no action %s", name)
	return core.ActionDef{}
}

func TestA52_AnInvalidScheduleIsRefusedWithTheSystemdAnalyzeError(t *testing.T) {
	in := func(sched string) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"slug": "backup", "schedule": sched, "user": "root", "command": "echo hi"})
		return b
	}
	env := core.Env{HostID: "h1", Principal: admin, Exec: runner{bad: true}}
	_, err := action(t, "jobs.job.create").Handler.Plan(context.Background(), env, in("every other tuesday"))
	if err == nil || !strings.Contains(err.Error(), "Failed to parse calendar specification") {
		t.Errorf("an invalid schedule: %v; want the systemd-analyze error in the refusal", err)
	}
	_, err = action(t, "jobs.schedule.check").Handler.Read(context.Background(), env, json.RawMessage(`{"schedule":"every other tuesday"}`))
	if err == nil || !strings.Contains(err.Error(), "Failed to parse calendar specification") {
		t.Errorf("schedule check of an invalid schedule: %v; want the systemd-analyze error", err)
	}
}
