package docker

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"slices"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

type regAction struct {
	name   string
	class  core.Class
	scope  core.Scope // MinScope; "" keeps the class default
	long   bool
	apply  time.Duration // 0 keeps the 30 s default
	undoer bool
}

var regActions = []regAction{
	{"docker.container.start", core.ClassCompensable, "", false, 0, true},
	{"docker.container.stop", core.ClassCompensable, "", false, 0, true},
	{"docker.container.restart", core.ClassCompensable, "", false, 0, true},
	{"docker.container.remove", core.ClassIrreversible, "", false, 0, false},
	{"docker.container.inspect", core.ClassRead, "", false, 0, false},
	{"docker.container.logs", core.ClassRead, "", false, 0, false},
	{"docker.container.reveal-env", core.ClassRead, core.ScopeAdmin, false, 0, false},
	{"docker.image.pull", core.ClassCompensable, "", true, 30 * time.Minute, true},
	{"docker.image.remove", core.ClassIrreversible, "", false, 0, false},
	{"docker.image.prune", core.ClassIrreversible, "", false, 0, false},
	{"docker.network.create", core.ClassCompensable, "", false, 0, true},
	{"docker.network.remove", core.ClassIrreversible, "", false, 0, false},
	{"docker.network.connect", core.ClassCompensable, "", false, 0, true},
	{"docker.network.disconnect", core.ClassCompensable, "", false, 0, true},
	{"docker.volume.create", core.ClassCompensable, "", false, 0, true},
	{"docker.volume.remove", core.ClassIrreversible, "", false, 0, false},
	{"docker.compose.up", core.ClassIrreversible, core.ScopeAdmin, true, 15 * time.Minute, false},
	{"docker.compose.down", core.ClassIrreversible, core.ScopeAdmin, true, 10 * time.Minute, false},
	{"docker.compose.pull", core.ClassCompensable, core.ScopeAdmin, true, 30 * time.Minute, true},
	{"docker.compose.restart", core.ClassCompensable, core.ScopeAdmin, true, 10 * time.Minute, true},
}

func regModule(t *testing.T) module {
	t.Helper()
	return newModule(newDKHost(t).dk(), coretest.NewFakeClock(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)))
}

func TestRegManifestDeclaresTheActions(t *testing.T) {
	man := regModule(t).Manifest()
	if man.ID != ID || man.Version != 1 || !man.DefaultEnabled {
		t.Errorf("manifest %q v%d default-enabled %v, want docker v1 enabled by default", man.ID, man.Version, man.DefaultEnabled)
	}
	got := map[string]core.ActionDef{}
	for _, a := range man.Actions {
		if _, dup := got[a.Spec.Name]; dup {
			t.Errorf("%s is declared twice", a.Spec.Name)
		}
		got[a.Spec.Name] = a
	}
	for _, want := range regActions {
		a, ok := got[want.name]
		if !ok {
			t.Errorf("%s is not declared", want.name)
			continue
		}
		delete(got, want.name)
		s := a.Spec
		timeout := s.ApplyTimeout
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		wantTimeout := want.apply
		if wantTimeout == 0 {
			wantTimeout = 30 * time.Second
		}
		wantLocks := []string{"docker"}
		if want.class == core.ClassRead {
			wantLocks = nil
		}
		if s.Version != 1 || s.Class != want.class || s.Side != core.SideAgent || !slices.Equal(s.LockKeys, wantLocks) ||
			s.MinScope != want.scope || s.LongRunning != want.long || timeout != wantTimeout || s.Internal ||
			s.Reveal != (want.name == "docker.container.reveal-env") {
			t.Errorf("%s: spec %+v; want class %s on the agent, locks %v, min scope %q, long-running %v, timeout %s",
				want.name, s, want.class, wantLocks, want.scope, want.long, wantTimeout)
		}
		if (a.Handler.Inverse != nil) != want.undoer {
			t.Errorf("%s: undo handler %v, want %v", want.name, a.Handler.Inverse != nil, want.undoer)
		}
		// These four journal no effect (Tasks 29 and 41), and spec 1.2 requires an Observer only of an action that journals.
		journals := !slices.Contains([]string{"docker.image.remove", "docker.image.prune", "docker.compose.up", "docker.compose.down"}, want.name)
		if want.class != core.ClassRead && (a.Handler.Observe != nil) != journals {
			t.Errorf("%s: Observe handler %v, want %v", want.name, a.Handler.Observe != nil, journals)
		}
	}
	for name := range got {
		t.Errorf("%s is declared but is not in the action table", name)
	}
}

func TestRegManifestDeclaresTheRest(t *testing.T) {
	man := regModule(t).Manifest()

	var resources []string
	for _, r := range man.Resources {
		resources = append(resources, r.Name)
	}
	slices.Sort(resources)
	wantResources := []string{"docker.containers", "docker.engine", "docker.images", "docker.networks", "docker.projects", "docker.volumes"}
	if !slices.Equal(resources, wantResources) {
		t.Errorf("resources %v, want %v", resources, wantResources)
	}

	if len(man.Preflight) != 1 || man.Preflight[0].ID != "docker.engine" {
		t.Errorf("preflight %+v, want the one check docker.engine", man.Preflight)
	}
	if len(man.OwnedPaths) != 1 || man.OwnedPaths[0].Path != dockerConfigDir || man.OwnedPaths[0].Kind != core.OwnedManagedDir {
		t.Errorf("owned paths %+v, want the managed directory %s", man.OwnedPaths, dockerConfigDir)
	}
	migrations, ok := man.Migrations[core.SideAgent]
	if !ok || len(man.Migrations) != 1 {
		t.Fatalf("migrations %v, want the agent side only", man.Migrations)
	}
	if _, err := fs.Stat(migrations, "0001_docker.sql"); err != nil {
		t.Errorf("agent migrations: %v", err)
	}
}

func TestRegStreams(t *testing.T) {
	var m core.Module = regModule(t)
	streamer, ok := m.(core.Streamer)
	if !ok {
		t.Fatal("the module does not implement core.Streamer")
	}
	var names []string
	for _, s := range streamer.Streams() {
		names = append(names, s.Name)
		if s.Run == nil || s.ParamsType == nil || s.EventType == nil {
			t.Errorf("stream %s lacks its run function, parameter type or event type", s.Name)
		}
	}
	slices.Sort(names)
	if want := []string{"logs", "pull", "stats"}; !slices.Equal(names, want) {
		t.Errorf("streams %v, want %v", names, want)
	}
}

func TestRegRegistryAcceptsTheModule(t *testing.T) {
	reg, err := core.NewRegistry(regModule(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range regActions {
		if _, ok := reg.Action(core.ActionRef{Name: a.name, Version: 1}); !ok {
			t.Errorf("the registry has no %s", a.name)
		}
	}
	for _, s := range []string{"logs", "stats", "pull"} {
		if _, ok := reg.Stream(ID, s); !ok {
			t.Errorf("the registry has no stream docker.%s", s)
		}
	}
	if _, ok := reg.Resource("docker.engine"); !ok {
		t.Error("the registry has no docker.engine resource")
	}
}

func TestRegProductionModuleHoldsAPullHub(t *testing.T) {
	m, ok := Module().(module)
	if !ok || m.pulls == nil {
		t.Fatalf("Module() = %#v, want a module holding a pull hub", Module())
	}
}

var errRegEnough = errors.New("enough events")

// TestRegPullHubIsShared: the hub the module creates is the one docker.image.pull
// publishes to and the docker.pull stream reads from.
func TestRegPullHubIsShared(t *testing.T) {
	h := newDKHost(t)
	const ref = "alpine:3.20"
	h.docker.putImage(dkImage(ref, 1000))
	if h.docker.pulls == nil {
		h.docker.pulls = map[string][]dockerapi.PullMessage{}
	}
	h.docker.pulls[ref] = []dockerapi.PullMessage{{Status: "Pulling from library/alpine"}, {Status: "Pull complete", ID: "abc123"}}
	m := newModule(h.dk(), h.clock)

	var pull core.ActionDef
	for _, a := range m.Manifest().Actions {
		if a.Spec.Name == "docker.image.pull" {
			pull = a
		}
	}
	const changeID = "01J00000000000000000000001"
	if _, err := pull.Handler.Apply(context.Background(), h.env(changeID), json.RawMessage(`{"image":"`+ref+`"}`)); err != nil {
		t.Fatal(err)
	}

	var stream core.StreamDef
	for _, s := range m.Streams() {
		if s.Name == "pull" {
			stream = s
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var statuses []string
	_ = stream.Run(ctx, h.env(""), json.RawMessage(`{"changeId":"`+changeID+`"}`), func(event any) error {
		ev, ok := event.(PullEvent)
		if !ok {
			t.Errorf("stream emitted %T, want PullEvent", event)
			return errRegEnough
		}
		statuses = append(statuses, ev.Status)
		if slices.Contains(statuses, "Pull complete") {
			return errRegEnough
		}
		return nil
	})
	if want := []string{"Pulling from library/alpine", "Pull complete"}; !slices.Equal(statuses[:min(len(statuses), len(want))], want) {
		t.Errorf("pull stream replayed %q, want it to begin with %q", statuses, want)
	}
}
