package docker

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// cuConfigJSON is `docker compose config --format json` of a project named
// shop whose Compose file has one service, web, running nginx:1.27 (Compose
// v5.1.4).
const cuConfigJSON = `{
  "name": "shop",
  "networks": {"default": {"name": "shop_default", "ipam": {}}},
  "services": {
    "web": {"command": null, "entrypoint": null, "image": "nginx:1.27", "networks": {"default": null},
      "ports": [{"mode": "ingress", "target": 80, "published": "8080", "protocol": "tcp"}]}
  }
}`

// cuModule registers the four Compose actions the way the module's
// manifest will, so the registry's own rules check their declarations.
type cuModule struct{ k dk }

func (m cuModule) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, Actions: []core.ActionDef{
		m.k.composeUp(), m.k.composeDown(), m.k.composePull(), m.k.composeRestart(),
	}}
}

// cuExec runs a hook before the host's runner answers a command.
type cuExec struct {
	core.Runner
	before func(cmd core.Cmd)
}

func (e cuExec) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	if e.before != nil {
		e.before(cmd)
	}
	return e.Runner.Run(ctx, cmd)
}

func cuKey(args []string) string { return strings.Join(args, " ") }

// cuConfig makes `config --format json` of p answer cuConfigJSON for profiles.
func cuConfig(h *dkHost, p projectRow, profiles []string) {
	h.results[cuKey(composeArgs(p, profiles, "config", "--format", "json"))] = core.CmdResult{Stdout: []byte(cuConfigJSON)}
}

// cuProject puts the valid project shop, the system plugin files and the
// answer of its config command into h.
func cuProject(t *testing.T, h *dkHost) projectRow {
	t.Helper()
	p := ccProject(t, h, "shop")
	h.files[pluginDir] = dkFile{Mode: fs.ModeDir | 0o755}
	h.files[pluginDir+"/docker-compose"] = dkFile{Mode: 0o755}
	cuConfig(h, p, p.Profiles)
	return p
}

// cuContainers puts the containers of project shop into the fake daemon: web
// and cache running, db stopped, and a running container of another project.
func cuContainers(h *dkHost) {
	label := func(project string) func(*dockerapi.ContainerJSON) {
		return dkLabels(map[string]string{"com.docker.compose.project": project})
	}
	h.docker.put(dkContainer("shop-web-1", dkRunning, label("shop")))
	h.docker.put(dkContainer("shop-cache-1", dkRunning, label("shop")))
	h.docker.put(dkContainer("shop-db-1", label("shop")))
	h.docker.put(dkContainer("other-web-1", dkRunning, label("other")))
}

const cuInput = `{"project":"shop"}`

func cuApply(t *testing.T, h *dkHost, def core.ActionDef, input string) (json.RawMessage, error) {
	t.Helper()
	return def.Handler.Apply(context.Background(), h.env("chg1"), json.RawMessage(input))
}

func cuPlan(t *testing.T, h *dkHost, def core.ActionDef, input string) (core.PlanOutcome, error) {
	t.Helper()
	return def.Handler.Plan(context.Background(), h.env(""), json.RawMessage(input))
}

func cuRow(t *testing.T, h *dkHost) projectRow {
	t.Helper()
	row, ok, err := loadProject(context.Background(), h.db, "shop")
	if err != nil || !ok {
		t.Fatalf("loadProject = %v, %v, want the shop row", ok, err)
	}
	return row
}

func cuHasLine(diff []core.DiffLine, op, text string) bool {
	return slices.Contains(diff, core.DiffLine{Op: op, Text: text})
}

func cuLastCmd(t *testing.T, h *dkHost) core.Cmd {
	t.Helper()
	if len(h.cmds) == 0 {
		t.Fatal("no command ran")
	}
	return h.cmds[len(h.cmds)-1]
}

func TestCUActionSpecs(t *testing.T) {
	k := newDKHost(t).dk()
	tests := []struct {
		def     core.ActionDef
		name    string
		class   core.Class
		timeout time.Duration
		undo    bool
	}{
		{k.composeUp(), "docker.compose.up", core.ClassIrreversible, 15 * time.Minute, false},
		{k.composeDown(), "docker.compose.down", core.ClassIrreversible, 10 * time.Minute, false},
		{k.composePull(), "docker.compose.pull", core.ClassCompensable, 30 * time.Minute, true},
		{k.composeRestart(), "docker.compose.restart", core.ClassCompensable, 10 * time.Minute, true},
	}
	for _, tc := range tests {
		s := tc.def.Spec
		if s.Name != tc.name || s.Version != 1 || s.Class != tc.class || s.Side != core.SideAgent {
			t.Errorf("%s: spec %+v, want version 1, class %s on the agent", tc.name, s, tc.class)
		}
		if !slices.Equal(s.LockKeys, []string{"docker"}) || s.MinScope != core.ScopeAdmin || !s.LongRunning || s.ApplyTimeout != tc.timeout {
			t.Errorf("%s: spec %+v, want lock docker, admin scope, long-running, timeout %s", tc.name, s, tc.timeout)
		}
		if (tc.def.Handler.Inverse != nil) != tc.undo || (tc.undo && tc.def.Handler.Observe == nil) {
			t.Errorf("%s: undo handler %v, want %v with an observer", tc.name, tc.def.Handler.Inverse != nil, tc.undo)
		}
	}
	if _, err := core.NewRegistry(cuModule{k}); err != nil {
		t.Errorf("NewRegistry: %v", err)
	}
}

func TestCUPlans(t *testing.T) {
	const root = "runs the project's Compose file as root"
	tests := []struct {
		name    string
		def     func(dk) core.ActionDef
		confirm string
		lines   []core.DiffLine
		absent  []core.DiffLine
	}{
		{"up", dk.composeUp, "shop", []core.DiffLine{
			{Op: " ", Text: "containers are created or recreated; a recreated container loses its writable layer"},
		}, nil},
		{"down", dk.composeDown, "shop", []core.DiffLine{
			{Op: "-", Text: "remove container shop-web-1 and its writable layer"},
			{Op: "-", Text: "remove container shop-cache-1 and its writable layer"},
			{Op: "-", Text: "remove container shop-db-1 and its writable layer"},
		}, []core.DiffLine{{Op: "-", Text: "remove container other-web-1 and its writable layer"}}},
		{"pull", dk.composePull, "", []core.DiffLine{
			{Op: " ", Text: "images are pulled; a tag that moved is not reverted by an undo"},
		}, nil},
		{"restart", dk.composeRestart, "", []core.DiffLine{
			{Op: " ", Text: "restart container shop-web-1"},
			{Op: " ", Text: "restart container shop-cache-1"},
		}, []core.DiffLine{
			{Op: " ", Text: "restart container shop-db-1"},
			{Op: " ", Text: "restart container other-web-1"},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newDKHost(t)
			cuProject(t, h)
			cuContainers(h)
			out, err := cuPlan(t, h, tc.def(h.dk()), cuInput)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if out.Confirmation != tc.confirm {
				t.Errorf("confirmation %q, want %q", out.Confirmation, tc.confirm)
			}
			if !slices.ContainsFunc(out.Diff, func(l core.DiffLine) bool { return strings.Contains(l.Text, root) }) {
				t.Errorf("diff %+v lacks a line saying it %s", out.Diff, root)
			}
			for _, l := range tc.lines {
				if !cuHasLine(out.Diff, l.Op, l.Text) {
					t.Errorf("diff %+v lacks %+v", out.Diff, l)
				}
			}
			for _, l := range tc.absent {
				if cuHasLine(out.Diff, l.Op, l.Text) {
					t.Errorf("diff %+v has %+v", out.Diff, l)
				}
			}
			if out.StateHash == "" {
				t.Error("state hash is empty")
			}
			for _, c := range h.cmds {
				if c.Args[len(c.Args)-1] != "json" {
					t.Errorf("plan ran %q, want only the config command", cuKey(c.Args))
				}
			}
		})
	}
}

func TestCUPlanRestartWithNothingRunning(t *testing.T) {
	h := newDKHost(t)
	cuProject(t, h)
	h.docker.put(dkContainer("shop-db-1", dkLabels(map[string]string{"com.docker.compose.project": "shop"})))
	out, err := cuPlan(t, h, h.dk().composeRestart(), cuInput)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !cuHasLine(out.Diff, " ", "no container of the project is running") {
		t.Errorf("diff %+v lacks the line saying nothing is running", out.Diff)
	}
}

func TestCUPlanOfAnUnknownProjectRunsNothing(t *testing.T) {
	h := newDKHost(t)
	cuProject(t, h)
	_, err := cuPlan(t, h, h.dk().composeUp(), `{"project":"nope"}`)
	var coded *errcode.Error
	if !errors.As(err, &coded) {
		t.Fatalf("Plan = %v, want an *errcode.Error", err)
	}
	if len(h.cmds) != 0 {
		t.Errorf("ran %d commands for an unknown project, want none", len(h.cmds))
	}
}

func TestCUStateHashFollowsWhatTheRunActsOn(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	p := cuProject(t, h)
	cuContainers(h)
	def := h.dk().composeRestart()
	hash := func(input string) string {
		t.Helper()
		out, err := cuPlan(t, h, def, input)
		if err != nil {
			t.Fatalf("Plan %s: %v", input, err)
		}
		return out.StateHash
	}
	base := hash(cuInput)
	if again := hash(cuInput); again != base {
		t.Errorf("hash %q then %q for the same state, want equal", base, again)
	}

	if _, err := h.docker.StopContainer(ctx, dkID("shop-web-1"), time.Second); err != nil {
		t.Fatal(err)
	}
	stopped := hash(cuInput)
	if stopped == base {
		t.Error("hash is unchanged after a container of the project stopped")
	}
	if _, err := h.docker.StartContainer(ctx, dkID("shop-web-1")); err != nil {
		t.Fatal(err)
	}
	if restored := hash(cuInput); restored != base {
		t.Errorf("hash %q after the container started again, want %q", restored, base)
	}

	file := h.files[p.Files[0]]
	file.Data = append(slices.Clone(file.Data), []byte("\n# edited\n")...)
	h.files[p.Files[0]] = file
	if edited := hash(cuInput); edited == base {
		t.Error("hash is unchanged after the Compose file changed")
	}
	file.Data = file.Data[:len(file.Data)-len("\n# edited\n")]
	h.files[p.Files[0]] = file

	cuConfig(h, p, []string{"debug"})
	if withProfile := hash(`{"project":"shop","profiles":["debug"]}`); withProfile == base {
		t.Error("hash is unchanged after the profile list changed")
	}
}

func TestCUUpWritesTheDockerConfigThenRuns(t *testing.T) {
	h := newDKHost(t)
	p := cuProject(t, h)
	cuContainers(h)
	upKey := cuKey(composeArgs(p, p.Profiles, "up", "-d", "--no-build"))
	var ran bool
	var config string
	env := h.env("chg1")
	env.Exec = cuExec{Runner: h, before: func(c core.Cmd) {
		if cuKey(c.Args) == upKey {
			ran = true
			config = string(h.managed[dockerConfigDir+"/config.json"])
		}
	}}
	raw, err := h.dk().composeUp().Handler.Apply(context.Background(), env, json.RawMessage(cuInput))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !ran || config != "{}\n" {
		t.Errorf("up ran %v with docker-config/config.json %q, want it written as \"{}\\n\" before the run", ran, config)
	}
	last := cuLastCmd(t, h)
	if last.Path != "/usr/bin/docker" || cuKey(last.Args) != upKey {
		t.Errorf("last command %s %q, want /usr/bin/docker %q", last.Path, cuKey(last.Args), upKey)
	}
	keys := make([]string, len(h.cmds))
	for i, c := range h.cmds {
		keys[i] = cuKey(c.Args)
	}
	cfg := slices.Index(keys, cuKey(composeArgs(p, p.Profiles, "config", "--format", "json")))
	if cfg < 0 || cfg > slices.Index(keys, upKey) {
		t.Errorf("commands %q, want the config check before up", keys)
	}

	var out ComposeResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	want := []ContainerState{
		{ID: dkID("shop-cache-1"), Name: "shop-cache-1", Running: true},
		{ID: dkID("shop-db-1"), Name: "shop-db-1", Running: false},
		{ID: dkID("shop-web-1"), Name: "shop-web-1", Running: true},
	}
	if out.Project != "shop" || !slices.Equal(out.Containers, want) {
		t.Errorf("result %+v, want project shop with %+v", out, want)
	}
	row := cuRow(t, h)
	if row.RunHash == "" || row.RunHash != h.dk().configHash(p) || !row.LastRun.Equal(h.clock.Now()) {
		t.Errorf("run recorded as hash %q at %s, want the config hash %q at %s", row.RunHash, row.LastRun, h.dk().configHash(p), h.clock.Now())
	}
}

func TestCUOtherVerbsRunTheirVectorAndRecordTheRun(t *testing.T) {
	tests := []struct {
		name string
		def  func(dk) core.ActionDef
		verb []string
	}{
		{"down", dk.composeDown, []string{"down"}},
		{"pull", dk.composePull, []string{"pull"}},
		{"restart", dk.composeRestart, []string{"restart"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newDKHost(t)
			p := cuProject(t, h)
			cuContainers(h)
			if _, err := cuApply(t, h, tc.def(h.dk()), cuInput); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			last := cuLastCmd(t, h)
			if last.Path != "/usr/bin/docker" || cuKey(last.Args) != cuKey(composeArgs(p, p.Profiles, tc.verb...)) {
				t.Errorf("last command %s %q, want the %s vector", last.Path, cuKey(last.Args), tc.verb[0])
			}
			if got := h.managed[dockerConfigDir+"/config.json"]; string(got) != "{}\n" {
				t.Errorf("docker-config/config.json = %q, want \"{}\\n\"", got)
			}
			if row := cuRow(t, h); row.RunHash != h.dk().configHash(p) {
				t.Errorf("run hash %q, want %q", row.RunHash, h.dk().configHash(p))
			}
		})
	}
}

func TestCUProfilesInputReplacesTheStoredList(t *testing.T) {
	h := newDKHost(t)
	p := cuProject(t, h)
	cuConfig(h, p, []string{"debug", "dev"})
	def := h.dk().composeUp()
	if _, err := cuApply(t, h, def, `{"project":"shop","profiles":["debug","dev"]}`); err != nil {
		t.Fatalf("Apply with profiles: %v", err)
	}
	want := cuKey(composeArgs(p, []string{"debug", "dev"}, "up", "-d", "--no-build"))
	if got := cuKey(cuLastCmd(t, h).Args); got != want || !strings.Contains(got, "--profile debug --profile dev") {
		t.Errorf("up ran %q, want %q", got, want)
	}
	if row := cuRow(t, h); !slices.Equal(row.Profiles, []string{"debug", "dev"}) {
		t.Errorf("stored profiles %q, want [debug dev]", row.Profiles)
	}
	if _, err := cuApply(t, h, def, cuInput); err != nil {
		t.Fatalf("Apply without profiles: %v", err)
	}
	if got := cuKey(cuLastCmd(t, h).Args); got != want {
		t.Errorf("up ran %q after the list was stored, want %q", got, want)
	}
}

func TestCUFailedRunNamesTheVerbTheStatusAndTheLastStderrLine(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		code   int
		want   string
	}{
		{"last non-empty line", "pull access denied\nservice web failed to start\n\n", 1, "compose up exited with status 1: service web failed to start"},
		{"no stderr", "", 2, "compose up exited with status 2"},
		{"capped on a rune boundary", "a" + strings.Repeat("é", 200) + "\n", 1, "compose up exited with status 1: a" + strings.Repeat("é", 149)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newDKHost(t)
			p := cuProject(t, h)
			before := cuRow(t, h)
			h.results[cuKey(composeArgs(p, p.Profiles, "up", "-d", "--no-build"))] = core.CmdResult{ExitCode: tc.code, Stderr: []byte(tc.stderr)}
			_, err := cuApply(t, h, h.dk().composeUp(), cuInput)
			var coded *errcode.Error
			if !errors.As(err, &coded) || coded.Code != errcode.DockerComposeUnavailable || coded.Detail != tc.want {
				t.Fatalf("Apply = %v, want docker.compose_unavailable %q", err, tc.want)
			}
			if !utf8.ValidString(coded.Detail) {
				t.Errorf("detail %q is not valid UTF-8", coded.Detail)
			}
			if core.IsDefinite(err) {
				t.Error("a failed run is marked definite, but the run may have changed containers")
			}
			if after := cuRow(t, h); after.RunHash != before.RunHash || !after.LastRun.Equal(before.LastRun) {
				t.Errorf("run recorded after a failure: %+v, was %+v", after, before)
			}
		})
	}
}

func TestCURestartJournalsEveryRunningContainerBeforeTheRun(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	p := cuProject(t, h)
	cuContainers(h)
	restartKey := cuKey(composeArgs(p, p.Profiles, "restart"))
	intended := -1
	env := h.env("chg1")
	env.Exec = cuExec{Runner: h, before: func(c core.Cmd) {
		if cuKey(c.Args) == restartKey {
			intended = len(h.journal.effects())
		}
	}}
	def := h.dk().composeRestart()
	if _, err := def.Handler.Apply(ctx, env, json.RawMessage(cuInput)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if intended != 2 {
		t.Errorf("%d effects were journaled when restart ran, want 2", intended)
	}
	effs := h.journal.effects()
	if len(effs) != 2 {
		t.Fatalf("%d effects, want one for each of the two running containers", len(effs))
	}
	for i, name := range []string{"shop-cache-1", "shop-web-1"} {
		e := effs[i]
		c, ok := h.docker.container(dkID(name))
		if !ok {
			t.Fatalf("container %s is gone", name)
		}
		wantBefore, err := dstate.ContainerRunning(dkID(name), c.Created, true)
		if err != nil {
			t.Fatal(err)
		}
		wantPost, err := dstate.ContainerRun(c, dstate.Recorded)
		if err != nil {
			t.Fatal(err)
		}
		if e.Kind != core.EffectModify || e.Resource != containerResource(dkID(name)) || e.Local || e.CommitWait != 2*time.Minute {
			t.Errorf("effect %d = %+v, want a non-local modify of %s with a 2 minute commit wait", i, e, containerResource(dkID(name)))
		}
		if string(e.Before.State) != string(wantBefore) {
			t.Errorf("effect %d before %s, want %s", i, e.Before.State, wantBefore)
		}
		if e.Post == nil || string(e.Post.State) != string(wantPost) {
			t.Errorf("effect %d post %+v, want %s", i, e.Post, wantPost)
		}
		obs, err := def.Handler.Observe(ctx, env, e)
		if err != nil || string(obs.Current.State) != string(wantPost) {
			t.Errorf("Observe of effect %d = %s (%v), want %s", i, obs.Current.State, err, wantPost)
		}
	}
}

func TestCURestartThatFailsLeavesItsEffectsUnmade(t *testing.T) {
	h := newDKHost(t)
	p := cuProject(t, h)
	cuContainers(h)
	h.results[cuKey(composeArgs(p, p.Profiles, "restart"))] = core.CmdResult{ExitCode: 1, Stderr: []byte("daemon went away\n")}
	_, err := cuApply(t, h, h.dk().composeRestart(), cuInput)
	if err == nil || core.IsDefinite(err) {
		t.Fatalf("Apply = %v, want an indefinite error", err)
	}
	effs := h.journal.effects()
	if len(effs) != 2 {
		t.Fatalf("%d effects, want the two intended before the run", len(effs))
	}
	for i, e := range effs {
		if e.Post != nil {
			t.Errorf("effect %d has a post-state %+v, but the run failed", i, e.Post)
		}
	}
}

func TestCURestartInverseStartsWhatIsStopped(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	cuProject(t, h)
	cuContainers(h)
	def := h.dk().composeRestart()
	env := h.env("chg1")
	if _, err := def.Handler.Apply(ctx, env, json.RawMessage(cuInput)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	effs := h.journal.effects()
	if _, err := h.docker.StopContainer(ctx, dkID("shop-web-1"), time.Second); err != nil {
		t.Fatal(err)
	}
	for _, e := range effs {
		res, err := def.Handler.Inverse(ctx, env, e)
		if err != nil || res.Resource != "" {
			t.Errorf("Inverse of %s = %+v, %v, want an empty result", e.Resource, res, err)
		}
	}
	for _, name := range []string{"shop-web-1", "shop-cache-1"} {
		if c, _ := h.docker.container(dkID(name)); !c.State.Running {
			t.Errorf("%s is stopped after the undo, want it running as before", name)
		}
	}
	if c, _ := h.docker.container(dkID("shop-db-1")); c.State.Running {
		t.Error("shop-db-1 was started by the undo, but it was stopped before the restart")
	}
}

func TestCURestartInverseFailures(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		definite bool
	}{
		{"a refusal of the daemon", &dockerapi.APIError{StatusCode: 409, Message: "container is paused"}, true},
		{"a failure to reach the daemon", errors.New("connection reset"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			h := newDKHost(t)
			cuProject(t, h)
			cuContainers(h)
			def := h.dk().composeRestart()
			env := h.env("chg1")
			if _, err := def.Handler.Apply(ctx, env, json.RawMessage(cuInput)); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			e := h.journal.effects()[1]
			if _, err := h.docker.StopContainer(ctx, dkID("shop-web-1"), time.Second); err != nil {
				t.Fatal(err)
			}
			h.docker.fail("StartContainer", tc.err)
			_, err := def.Handler.Inverse(ctx, env, e)
			if err == nil || core.IsDefinite(err) != tc.definite {
				t.Errorf("Inverse = %v (definite %v), want an error with definite %v", err, core.IsDefinite(err), tc.definite)
			}
		})
	}
}

func TestCUPullUndoChangesNothing(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	cuProject(t, h)
	cuContainers(h)
	def := h.dk().composePull()
	env := h.env("chg1")
	if _, err := def.Handler.Apply(ctx, env, json.RawMessage(cuInput)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if n := len(h.journal.effects()); n != 0 {
		t.Fatalf("pull journaled %d effects, want none", n)
	}
	calls, cmds := len(h.docker.calls()), len(h.cmds)
	res, err := def.Handler.Inverse(ctx, env, core.Effect{ChangeID: "chg1"})
	if err != nil || res.Resource != "" {
		t.Errorf("Inverse = %+v, %v, want an empty result", res, err)
	}
	if len(h.docker.calls()) != calls || len(h.cmds) != cmds {
		t.Error("the undo of a pull called the daemon or ran a command")
	}
}

func TestCUVerifyChecksTheContainersThatShouldExist(t *testing.T) {
	ctx := context.Background()
	empty := json.RawMessage("{}")
	input := json.RawMessage(cuInput)
	tests := []struct {
		name    string
		def     func(dk) core.ActionDef
		present bool
		wantErr bool
	}{
		{"up with containers", dk.composeUp, true, false},
		{"up without containers", dk.composeUp, false, true},
		{"down with containers left", dk.composeDown, true, true},
		{"down without containers", dk.composeDown, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newDKHost(t)
			cuProject(t, h)
			if tc.present {
				cuContainers(h)
			} else {
				h.docker.put(dkContainer("other-web-1", dkRunning, dkLabels(map[string]string{"com.docker.compose.project": "other"})))
			}
			err := tc.def(h.dk()).Handler.Verify(ctx, h.env("chg1"), input, empty)
			if (err != nil) != tc.wantErr {
				t.Errorf("Verify = %v, want error %v", err, tc.wantErr)
			}
		})
	}
}
