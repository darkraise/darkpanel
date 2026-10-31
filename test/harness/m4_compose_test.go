//go:build harness

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/docker"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

const (
	// m4cRoot is the parent directory of the scenario projects.
	m4cRoot = "/srv/dp-m4c"
	// m4cShimLog is where the program shims record their runs.
	m4cShimLog = "/var/log/dp-m4c-shims.log"
	// m4cShimMarker is the comment on the second line of every shim.
	m4cShimMarker = "dp-m4c-shim"
	// m4cBuiltImage is the tag of the build fixture; no machine has it.
	m4cBuiltImage = "dp-m4c-built:1"
	// m4cRefusedCode is the code of a Compose file the module refuses to run.
	m4cRefusedCode = "docker.compose_unavailable"
)

// The Compose fixture kinds of m4cCompose.
const (
	m4cOK    = "ok"
	m4cGit   = "git"
	m4cBuild = "build"
)

// m4cPluginMetadata is what a Docker CLI plugin prints when the CLI looks it up.
const m4cPluginMetadata = `{"SchemaVersion":"0.1.0","Vendor":"darkpanel-harness","Version":"v0.0.0-shim","ShortDescription":"logging shim"}`

// m4cShimPrograms are the programs a Compose file could make Compose start:
// the paths of their shims below the root, and the name the log records.
var m4cShimPrograms = []struct{ path, name string }{
	{"/usr/bin/git", "git"},
	{"/usr/bin/ssh", "ssh"},
	{"/usr/libexec/docker/cli-plugins/docker-buildx", "docker-buildx"},
}

// m4cShimScript is the text of the shim of the program name.
func m4cShimScript(name, log string) string {
	head := "#!/bin/sh\n# " + m4cShimMarker + "\n"
	run := "echo \"" + name + " $*\" >> " + m3Quote(log) + "\nexit 97\n"
	if name != "docker-buildx" {
		return head + run
	}
	return head + "if [ \"${1:-}\" = docker-cli-plugin-metadata ]; then\n  echo " + m3Quote(m4cPluginMetadata) + "\n  exit 0\nfi\n" + run
}

// m4cShimInstall is a bash script that replaces git, ssh and the buildx
// plugin below root with logging shims that exit 97. It keeps an existing
// program once in the aside directory and never stores a shim as an original.
func m4cShimInstall(root, log string) string {
	aside := root + "/var/lib/dp-m4c-real"
	var b strings.Builder
	b.WriteString("set -eu\naside=" + m3Quote(aside) + "\n")
	b.WriteString("mkdir -p " + m3Quote(root+"/usr/bin") + " " + m3Quote(root+"/usr/libexec/docker/cli-plugins") + ` "$aside" "$(dirname ` + m3Quote(log) + ")\"\n")
	b.WriteString(": > " + m3Quote(log) + "\n")
	b.WriteString(`install_shim() {
  target=$1
  name=$2
  if [ -e "$target" ] || [ -L "$target" ]; then
    if ! grep -qa ` + m4cShimMarker + ` "$target" && [ ! -e "$aside/$name" ]; then
      mv "$target" "$aside/$name"
    fi
  fi
  rm -f "$target"
  cat > "$target"
  chmod 0755 "$target"
}
`)
	for _, p := range m4cShimPrograms {
		b.WriteString("install_shim " + m3Quote(root+p.path) + " " + m3Quote(p.name) + " <<'DP_M4C_SHIM_EOF'\n")
		b.WriteString(m4cShimScript(p.name, log))
		b.WriteString("DP_M4C_SHIM_EOF\n")
	}
	return b.String()
}

// m4cShimRemove is a bash script that undoes m4cShimInstall: it removes each
// shim below root and puts the program kept aside back.
func m4cShimRemove(root string) string {
	var b strings.Builder
	b.WriteString("set -eu\naside=" + m3Quote(root+"/var/lib/dp-m4c-real") + "\n")
	b.WriteString(`restore_program() {
  target=$1
  name=$2
  if [ -e "$target" ] && grep -qa ` + m4cShimMarker + ` "$target"; then
    rm -f "$target"
  fi
  if [ -e "$aside/$name" ]; then
    mv "$aside/$name" "$target"
  fi
}
`)
	for _, p := range m4cShimPrograms {
		b.WriteString("restore_program " + m3Quote(root+p.path) + " " + m3Quote(p.name) + "\n")
	}
	return b.String()
}

// m4cShimRuns is the trimmed, non-empty lines of the shim log.
func m4cShimRuns(log string) []string {
	var runs []string
	for _, l := range strings.Split(log, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			runs = append(runs, l)
		}
	}
	return runs
}

// m4cNoShimRuns is nil when the shim log records no run.
func m4cNoShimRuns(log string) error {
	runs := m4cShimRuns(log)
	if len(runs) == 0 {
		return nil
	}
	return fmt.Errorf("harness: %d forbidden program runs during the Compose actions:\n%s", len(runs), strings.Join(runs, "\n"))
}

// m4cRefusedWith is nil when err is an API refusal with code.
func m4cRefusedWith(err error, code string) error {
	if err == nil {
		return fmt.Errorf("harness: the plan was not refused, want %s", code)
	}
	var ae *APIError
	if !errors.As(err, &ae) {
		return fmt.Errorf("harness: the plan failed without an API refusal, want %s: %w", code, err)
	}
	if ae.Code != code {
		return fmt.Errorf("harness: the plan was refused with %s, want %s: %s", ae.Code, code, ae.Detail)
	}
	return nil
}

// m4cCompose is the Compose file of the fixture kind, with image and, for
// the ok kind, the revision label rev.
func m4cCompose(kind, image string, rev int) string {
	switch kind {
	case m4cOK:
		return "services:\n  web:\n    image: " + image + "\n    labels:\n      dp-m4c.rev: \"" + fmt.Sprint(rev) + "\"\n"
	case m4cGit:
		return "include:\n  - path: git@127.0.0.1:dp-m4c/repo.git\nservices:\n  web:\n    image: " + image + "\n"
	case m4cBuild:
		return "services:\n  web:\n    build: .\n    image: " + m4cBuiltImage + "\n"
	}
	panic("harness: unknown Compose fixture " + kind)
}

// m4cPlanDoc is the part of a plan the Compose scenarios read.
type m4cPlanDoc struct {
	PlanID       string          `json:"planId"`
	Confirmation *string         `json:"confirmation"`
	Diff         []core.DiffLine `json:"diff"`
}

// m4cText is the diff, one line per entry, for searches and messages.
func (p m4cPlanDoc) m4cText() string {
	var b strings.Builder
	for _, d := range p.Diff {
		b.WriteString(d.Op + " " + d.Text + "\n")
	}
	return b.String()
}

// m4cPlan plans version 1 of action on host with input and does not apply it.
// A refusal is returned wrapped, so errors.As still finds its *APIError.
func (a *API) m4cPlan(ctx context.Context, host, action string, input any) (m4cPlanDoc, error) {
	p, err := actionPath(host, action+"/v1", "plan")
	if err != nil {
		return m4cPlanDoc{}, err
	}
	var doc m4cPlanDoc
	if err := a.do(ctx, http.MethodPost, p, map[string]any{"input": input}, &doc); err != nil {
		return m4cPlanDoc{}, fmt.Errorf("plan %s: %w", action, err)
	}
	return doc, nil
}

// m4cWrite writes content to file on v as root, creating its directory.
func m4cWrite(ctx context.Context, t *testing.T, v *vm.VM, file, content string) {
	t.Helper()
	m3Run(ctx, t, v, "sudo install -d -m 0755 -o root -g root "+m3dxShell(path.Dir(file)))
	if out, err := v.RunInput(ctx, "sudo tee "+m3dxShell(file)+" >/dev/null", strings.NewReader(content)); err != nil {
		t.Fatalf("write %s: %v\n%s", file, err, out)
	}
}

// m4cImage is the pinned image the scenario projects run.
func m4cImage(t *testing.T) string {
	t.Helper()
	image, err := m3dxImage("pebble-challtestsrv")
	if err != nil {
		t.Fatal(err)
	}
	return image
}

// m4cProject writes the ok fixture at revision rev to m4cRoot/name on the
// agent and starts it as an operator would, outside Darkpanel; that run is
// what makes the project discoverable. The project is taken down and its
// directory removed when the test ends.
func m4cProject(ctx context.Context, t *testing.T, f *fleet, name string, rev int) string {
	t.Helper()
	dir := m4cRoot + "/" + name
	m4cWrite(ctx, t, f.agent, dir+"/compose.yaml", m4cCompose(m4cOK, m4cImage(t), rev))
	compose := []string{"compose", "--project-directory", dir, "-p", name, "-f", dir + "/compose.yaml"}
	m4Docker(ctx, t, f.agent, append(slices.Clone(compose), "up", "-d")...)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, _ = f.agent.Run(ctx, "sudo docker "+m3dxShell(append(slices.Clone(compose), "down", "--remove-orphans")...))
		_, _ = f.agent.Run(ctx, "sudo rm -rf "+m3dxShell(dir))
	})
	return dir
}

// m4cWaitProject polls the docker.projects resource until the project name is
// listed and check accepts it. Reading the resource is what records a
// discovered project.
func m4cWaitProject(ctx context.Context, t *testing.T, f *fleet, name string, check func(docker.Project) error) docker.Project {
	t.Helper()
	var found docker.Project
	err := eventually(ctx, time.Minute, func(ctx context.Context) error {
		var list docker.ProjectList
		if err := f.api.Resource(ctx, f.agentID, "docker.projects", &list); err != nil {
			return err
		}
		for _, p := range list.Projects {
			if p.Name == name {
				if err := check(p); err != nil {
					return err
				}
				found = p
				return nil
			}
		}
		return fmt.Errorf("harness: project %s is not listed", name)
	})
	if err != nil {
		t.Fatalf("project %s of host %s: %v", name, f.agentID, err)
	}
	return found
}

// m4cResult decodes the output of a completed Compose change and requires it
// to name project with one running container.
func m4cResult(t *testing.T, ch Change, project string) docker.ComposeResult {
	t.Helper()
	var res docker.ComposeResult
	if err := json.Unmarshal(ch.Output, &res); err != nil {
		t.Fatalf("change %s: output %s: %v", ch.ID, ch.Output, err)
	}
	if res.Project != project || len(res.Containers) != 1 || res.Containers[0].Name == "" {
		t.Fatalf("change %s: output %s, want project %s with one container", ch.ID, ch.Output, project)
	}
	return res
}

// m4cInstallShims puts the logging shims on the agent and proves that they
// log, that sudo finds them and that Compose still works beside the plugin shim.
func m4cInstallShims(ctx context.Context, t *testing.T, f *fleet) {
	if out, err := f.agent.RunInput(ctx, "sudo bash -s", strings.NewReader(m4cShimInstall("", m4cShimLog))); err != nil {
		t.Fatalf("install the shims: %v\n%s", err, out)
	}
	m3Run(ctx, t, f.agent, "sudo git --version; sudo ssh -V; sudo /usr/libexec/docker/cli-plugins/docker-buildx version; true")
	log := m3Run(ctx, t, f.agent, "sudo cat "+m4cShimLog)
	if got, want := m4cShimRuns(log), []string{"git --version", "ssh -V", "docker-buildx version"}; !slices.Equal(got, want) {
		t.Fatalf("the control runs logged %q, want %q", got, want)
	}
	m3Run(ctx, t, f.agent, "sudo docker compose version")
	m3Run(ctx, t, f.agent, "sudo truncate -s 0 "+m4cShimLog)
}

// m4cLifecycle runs one project through restart, pull, the plan of a changed
// file, up and down.
func m4cLifecycle(ctx context.Context, t *testing.T, f *fleet) {
	const name = "m4c-web"
	dir := m4cProject(ctx, t, f, name, 1)
	input := docker.ComposeInput{Project: name}
	listed := func(p docker.Project) error {
		switch {
		case p.Directory != dir:
			return fmt.Errorf("directory %q, want %q", p.Directory, dir)
		case !slices.Equal(p.Files, []string{dir + "/compose.yaml"}):
			return fmt.Errorf("files %q, want only %s/compose.yaml", p.Files, dir)
		case p.Running != 1 || len(p.Containers) != 1:
			return fmt.Errorf("%d running of containers %q, want one", p.Running, p.Containers)
		case p.LastRun != nil:
			return fmt.Errorf("last run %v, want none before an action", p.LastRun)
		case p.ConfigHash == "":
			return errors.New("no config hash")
		}
		return nil
	}
	m4cWaitProject(ctx, t, f, name, listed)

	plan, err := f.api.m4cPlan(ctx, f.agentID, "docker.compose.up", input)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Confirmation == nil || *plan.Confirmation != name || !strings.Contains(plan.m4cText(), "runs the project's Compose file as root") {
		t.Errorf("the plan of docker.compose.up has confirmation %v and diff %q, want confirmation %q and a line saying it runs the file as root", plan.Confirmation, plan.m4cText(), name)
	}

	for _, action := range []string{"docker.compose.restart", "docker.compose.pull"} {
		ch := m4ApplyAsync(ctx, t, f.api, f.agentID, action, input)
		res := m4cResult(t, ch, name)
		container := res.Containers[0].Name
		if !res.Containers[0].Running {
			t.Errorf("%s: container %s is not reported running", action, container)
		}
		if got := strings.TrimSpace(m4Docker(ctx, t, f.agent, "inspect", "-f", "{{.State.Running}}", container)); got != "true" {
			t.Errorf("%s: container %s Running = %q, want true", action, container, got)
		}
		if err := m4Undo(ctx, f.api, ch.ID); err != nil {
			t.Errorf("undo of %s: %v", action, err)
		}
		if got := strings.TrimSpace(m4Docker(ctx, t, f.agent, "inspect", "-f", "{{.State.Running}}", container)); got != "true" {
			t.Errorf("after the undo of %s container %s Running = %q, want true", action, container, got)
		}
		m4cWaitProject(ctx, t, f, name, func(p docker.Project) error {
			if p.LastRun == nil {
				return errors.New("no last run after " + action)
			}
			return nil
		})
	}

	unchanged, err := f.api.m4cPlan(ctx, f.agentID, "docker.compose.up", input)
	if err != nil {
		t.Fatal(err)
	}
	m4cWaitProject(ctx, t, f, name, func(p docker.Project) error {
		if p.HashChanged {
			return errors.New("the hash differs from the last run, want it unchanged")
		}
		return nil
	})
	m4cWrite(ctx, t, f.agent, dir+"/compose.yaml", m4cCompose(m4cOK, m4cImage(t), 2))
	m4cWaitProject(ctx, t, f, name, func(p docker.Project) error {
		if !p.HashChanged {
			return errors.New("the hash equals that of the last run, want it changed")
		}
		return nil
	})
	changed, err := f.api.m4cPlan(ctx, f.agentID, "docker.compose.up", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed.Diff) <= len(unchanged.Diff) {
		t.Errorf("the plan of the changed file has %d diff lines (%q), want more than the %d (%q) of the unchanged one",
			len(changed.Diff), changed.m4cText(), len(unchanged.Diff), unchanged.m4cText())
	}

	ch := m4ApplyAsync(ctx, t, f.api, f.agentID, "docker.compose.up", input)
	container := m4cResult(t, ch, name).Containers[0].Name
	if got := strings.TrimSpace(m4Docker(ctx, t, f.agent, "inspect", "-f", `{{index .Config.Labels "dp-m4c.rev"}}`, container)); got != "2" {
		t.Errorf("container %s has label dp-m4c.rev %q after up, want 2", container, got)
	}
	m4cWaitProject(ctx, t, f, name, func(p docker.Project) error {
		if p.HashChanged || p.Running != 1 {
			return fmt.Errorf("hashChanged %v and %d running after up, want false and 1", p.HashChanged, p.Running)
		}
		return nil
	})

	m4ApplyAsync(ctx, t, f.api, f.agentID, "docker.compose.down", input)
	if out := strings.TrimSpace(m4Docker(ctx, t, f.agent, "ps", "-aq", "--filter", "label=com.docker.compose.project="+name)); out != "" {
		t.Errorf("containers %q remain after down, want none", out)
	}
	m4cWaitProject(ctx, t, f, name, func(p docker.Project) error {
		if p.Running != 0 || len(p.Containers) != 0 {
			return fmt.Errorf("%d running of containers %q after down, want none", p.Running, p.Containers)
		}
		return nil
	})
}

// m4cRefuse edits a started project into the fixture kind and requires every
// Compose action to refuse it at the plan, without applying anything.
func m4cRefuse(ctx context.Context, t *testing.T, f *fleet, name, kind string) {
	dir, image := m4cProject(ctx, t, f, name, 1), m4cImage(t)
	// Cleanups run last in, first out, so this restores a file that Compose
	// can read before the project is taken down.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = f.agent.RunInput(ctx, "sudo tee "+m3dxShell(dir+"/compose.yaml")+" >/dev/null", strings.NewReader(m4cCompose(m4cOK, image, 1)))
	})
	running := func(p docker.Project) error {
		if p.Running != 1 {
			return fmt.Errorf("%d running, want 1", p.Running)
		}
		return nil
	}
	m4cWaitProject(ctx, t, f, name, running)
	input := docker.ComposeInput{Project: name}
	if _, err := f.api.m4cPlan(ctx, f.agentID, "docker.compose.up", input); err != nil {
		t.Fatalf("the unmodified project: %v, want a valid plan", err)
	}
	m4cWrite(ctx, t, f.agent, dir+"/compose.yaml", m4cCompose(kind, image, 1))
	for _, action := range []string{"docker.compose.up", "docker.compose.down", "docker.compose.pull", "docker.compose.restart"} {
		_, err := f.api.m4cPlan(ctx, f.agentID, action, input)
		if rerr := m4cRefusedWith(err, m4cRefusedCode); rerr != nil {
			t.Errorf("%s of the %s project: %v", action, kind, rerr)
			continue
		}
		var ae *APIError
		if errors.As(err, &ae) {
			t.Logf("%s of the %s project: %s", action, kind, ae.Detail)
		}
	}
	m4cWaitProject(ctx, t, f, name, running)
}

// m4cRefuseGit requires the refusal of a Compose file with a git include.
func m4cRefuseGit(ctx context.Context, t *testing.T, f *fleet) {
	m4cRefuse(ctx, t, f, "m4c-git", m4cGit)
}

// m4cRefuseBuild requires the refusal of a Compose file that builds an image.
func m4cRefuseBuild(ctx context.Context, t *testing.T, f *fleet) {
	m4cRefuse(ctx, t, f, "m4c-build", m4cBuild)
}

// m4cNoPrograms requires that no shim ran during the Compose steps and then
// removes the shims.
func m4cNoPrograms(ctx context.Context, t *testing.T, f *fleet) {
	log := m3Run(ctx, t, f.agent, "sudo cat "+m4cShimLog)
	if err := m4cNoShimRuns(log); err != nil {
		t.Error(err)
	}
	if out, err := f.agent.RunInput(ctx, "sudo bash -s", strings.NewReader(m4cShimRemove(""))); err != nil {
		t.Fatalf("remove the shims: %v\n%s", err, out)
	}
	m3Run(ctx, t, f.agent, "sudo test ! -e /usr/libexec/docker/cli-plugins/docker-buildx")
}
