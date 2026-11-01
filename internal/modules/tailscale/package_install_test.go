package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

const (
	piApt     = "/usr/bin/apt-get"
	piVersion = "1.104.1"
	piStatus  = "/var/lib/dpkg/status"
)

// piDpkg is a dpkg status file that lists the tailscale package as installed.
func piDpkg(version string) []byte {
	return []byte("Package: bash\nStatus: install ok installed\nVersion: 5.2.21\n\n" +
		"Package: tailscale\nStatus: install ok installed\nPriority: optional\nArchitecture: amd64\nVersion: " + version + "\nDescription: The easiest, most secure way to use WireGuard\n\n")
}

func piVec(path string, args []string) string {
	return strings.Join(append([]string{path}, args...), " ")
}

// piRunner passes every vector to the host's runner and runs after for each
// one that succeeded, so a test can make apt-get change the dpkg status file
// and records the deadline of the context each call got.
type piRunner struct {
	next      core.Runner
	after     func(cmd core.Cmd)
	deadlines *[]time.Duration
}

func (r piRunner) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	if dl, ok := ctx.Deadline(); ok && r.deadlines != nil {
		*r.deadlines = append(*r.deadlines, time.Until(dl))
	}
	res, err := r.next.Run(ctx, cmd)
	if err == nil && res.ExitCode == 0 && r.after != nil {
		r.after(cmd)
	}
	return res, err
}

// piSetup is a supported host without tailscale. apt-get install of the
// package puts version in the dpkg status file and apt-get remove takes it out.
type piSetup struct {
	h         *tsHost
	t         ts
	env       core.Env
	deadlines []time.Duration
}

func piNewSetup(t *testing.T) *piSetup {
	t.Helper()
	h := newTSHost(t)
	s := &piSetup{h: h, t: piTS(h, "ubuntu", "noble", "amd64")}
	h.reads[piStatus] = []byte("Package: bash\nStatus: install ok installed\nVersion: 5.2.21\n\n")
	s.env = h.env("c1")
	s.env.Exec = piRunner{next: s.env.Exec, deadlines: &s.deadlines, after: func(cmd core.Cmd) {
		switch {
		case cmd.Path == piApt && slices.Equal(cmd.Args, platform.AptInstallTailscale()):
			h.reads[piStatus] = piDpkg(piVersion)
		case cmd.Path == piApt && slices.Equal(cmd.Args, platform.AptRemoveTailscale()):
			h.reads[piStatus] = []byte("Package: bash\nStatus: install ok installed\nVersion: 5.2.21\n\n")
		}
	}}
	return s
}

func (s *piSetup) installed() { s.h.reads[piStatus] = piDpkg(piVersion) }

func (s *piSetup) ranApt() [][]string {
	var out [][]string
	for _, v := range s.h.ran {
		if v[0] == piApt {
			out = append(out, v[1:])
		}
	}
	return out
}

func piRaw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func piRefused(t *testing.T, err error, code errcode.Code) {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != code || !core.IsDefinite(err) {
		t.Fatalf("error %v, want a definite %s", err, code)
	}
}

// piModule registers an action list, so the registry's own checks run on it.
type piModule struct{ defs []core.ActionDef }

func (m piModule) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, Actions: m.defs}
}

func TestPiPackageInstallSpec(t *testing.T) {
	def := piNewSetup(t).t.packageInstall()
	s, h := def.Spec, def.Handler
	if s.Name != "tailscale.package.install" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"tailscale"}) || s.ApplyTimeout != 15*time.Minute || !s.Task || !s.LongRunning ||
		s.Available != nil || len(s.Guards) != 0 || s.Internal {
		t.Errorf("spec %+v, want a long-running compensable task on the agent, lock tailscale, 15 minutes, no Available, no guard", s)
	}
	if h.Task == nil || h.Apply != nil || h.Plan == nil || h.Verify == nil || h.Observe == nil || h.Inverse == nil || h.Recover != nil {
		t.Errorf("handler has Task %v Apply %v Plan %v Verify %v Observe %v Inverse %v Recover %v, want a Task handler with an undo and no recovery handler",
			h.Task != nil, h.Apply != nil, h.Plan != nil, h.Verify != nil, h.Observe != nil, h.Inverse != nil, h.Recover != nil)
	}
	if _, err := core.NewRegistry(piModule{defs: []core.ActionDef{def}}); err != nil {
		t.Errorf("the registry refuses the action: %v", err)
	}
}

func TestPiPackageInstallPlan(t *testing.T) {
	s := piNewSetup(t)
	h := s.t.packageInstall().Handler
	out, err := h.Plan(context.Background(), s.env, piRaw(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Guards) != 0 || out.Confirmation != "" || out.StateHash == "" {
		t.Errorf("outcome %+v, want no guard, no confirmation and a state hash", out)
	}
	want := []core.DiffLine{
		{Op: "+", Text: "file " + keyringPath},
		{Op: "+", Text: "file " + sourcesPath},
		{Op: "+", Text: "package tailscale (the repository's current version)"},
	}
	if !slices.Equal(out.Diff, want) {
		t.Errorf("diff %+v, want %+v", out.Diff, want)
	}
	if len(s.h.ran) != 0 || len(s.h.files) != 0 {
		t.Errorf("a plan ran %v and wrote %v", s.h.ran, s.h.files)
	}
	again, err := h.Plan(context.Background(), s.env, piRaw(map[string]any{}))
	if err != nil || again.StateHash != out.StateHash {
		t.Errorf("a second plan hash %q (error %v), want %q", again.StateHash, err, out.StateHash)
	}

	s.h.reads[keyringPath] = piKeyring(t)
	same, err := h.Plan(context.Background(), s.env, piRaw(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	if same.StateHash == out.StateHash {
		t.Error("the state hash did not change when the keyring became present")
	}
	if len(same.Diff) != 3 || same.Diff[0].Op != " " || !strings.Contains(same.Diff[0].Text, keyringPath) || same.Diff[1].Op != "+" {
		t.Errorf("diff %+v, want the identical keyring shown as unchanged and the rest added", same.Diff)
	}

	other := piNewSetup(t)
	other.t = piTS(other.h, "debian", "bookworm", "arm64")
	other.h.reads[piStatus] = s.h.reads[piStatus]
	diff, err := other.t.packageInstall().Handler.Plan(context.Background(), other.env, piRaw(map[string]any{}))
	if err != nil || diff.StateHash == out.StateHash {
		t.Errorf("another OS gives hash %q (error %v), want one that differs from %q", diff.StateHash, err, out.StateHash)
	}
}

func TestPiPackageInstallPlanRefusals(t *testing.T) {
	t.Run("the package is present", func(t *testing.T) {
		s := piNewSetup(t)
		s.installed()
		_, err := s.t.packageInstall().Handler.Plan(context.Background(), s.env, piRaw(map[string]any{}))
		piRefused(t, err, errcode.TailscalePackagePresent)
		if !strings.Contains(err.Error(), piVersion) {
			t.Errorf("error %v does not name the installed version", err)
		}
	})
	t.Run("a repository file differs", func(t *testing.T) {
		s := piNewSetup(t)
		s.h.reads[sourcesPath] = []byte("deb https://example.com/ stable main\n")
		_, err := s.t.packageInstall().Handler.Plan(context.Background(), s.env, piRaw(map[string]any{}))
		piRefused(t, err, errcode.TailscaleRepoConflict)
	})
	t.Run("the OS is not supported", func(t *testing.T) {
		s := piNewSetup(t)
		s.t = piTS(s.h, "ubuntu", "focal", "amd64")
		_, err := s.t.packageInstall().Handler.Plan(context.Background(), s.env, piRaw(map[string]any{}))
		piRefused(t, err, errcode.TailscaleUnavailable)
	})
}

func TestPiPackageInstallTask(t *testing.T) {
	s := piNewSetup(t)
	def := s.t.packageInstall()
	raw, err := def.Handler.Task(context.Background(), s.env, piRaw(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	var out PackageInstalled
	if err := json.Unmarshal(raw, &out); err != nil || out.Version != piVersion {
		t.Fatalf("output %s (error %v), want version %s", raw, err, piVersion)
	}
	wantApt := [][]string{platform.AptUpdateTailscale(), platform.AptInstallTailscale()}
	if got := s.ranApt(); !slices.EqualFunc(got, wantApt, slices.Equal[[]string]) {
		t.Errorf("apt-get ran %v, want APT-1 then APT-2 %v", got, wantApt)
	}
	for _, d := range s.deadlines {
		if d <= 14*time.Minute || d > 15*time.Minute {
			t.Errorf("an apt-get call had %v left, want its context bounded by the 15 minute ApplyTimeout", d)
		}
	}
	if len(s.deadlines) != 2 {
		t.Errorf("%d calls saw a deadline, want 2", len(s.deadlines))
	}
	if !bytes.Equal(s.h.files[keyringPath], piKeyring(t)) || string(s.h.files[sourcesPath]) != piSources("ubuntu", "noble") {
		t.Errorf("files written %v, want the keyring and the source list", s.h.files)
	}

	effects, err := s.env.Effects.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var resources []string
	for _, e := range effects {
		resources = append(resources, e.Resource)
		if e.State != core.EffectMade || e.Kind != core.EffectCreate || string(e.Before.State) != "null" {
			t.Errorf("effect %+v, want a made creation from null", e)
		}
	}
	if want := []string{"tailscale.file:" + keyringPath, "tailscale.file:" + sourcesPath, "tailscale.package:tailscale"}; !slices.Equal(resources, want) {
		t.Errorf("effects %v, want %v", resources, want)
	}
	if post := effects[2].Post; post == nil || string(post.State) != `{"installed":true}` || !strings.Contains(string(post.Version), piVersion) {
		t.Errorf("package post-state %+v, want installed with the version", post)
	}

	id, version, ok, err := liveInstall(context.Background(), s.h.db.Read())
	if err != nil || !ok || id != "c1" || version != piVersion {
		t.Errorf("recorded install %q %q (found %v, error %v), want c1 %s", id, version, ok, err, piVersion)
	}
	if err := def.Handler.Verify(context.Background(), s.env, piRaw(map[string]any{}), raw); err != nil {
		t.Errorf("verify after the task: %v", err)
	}
}

func TestPiPackageInstallTaskLeavesIdenticalRepositoryFilesAlone(t *testing.T) {
	s := piNewSetup(t)
	s.h.reads[keyringPath] = piKeyring(t)
	s.h.reads[sourcesPath] = []byte(piSources("ubuntu", "noble"))
	if _, err := s.t.packageInstall().Handler.Task(context.Background(), s.env, piRaw(map[string]any{})); err != nil {
		t.Fatal(err)
	}
	if len(s.h.files) != 0 {
		t.Errorf("wrote %v over identical files", s.h.files)
	}
	effects, _ := s.env.Effects.List(context.Background())
	if len(effects) != 1 || effects[0].Resource != "tailscale.package:tailscale" {
		t.Errorf("effects %+v, want only the package", effects)
	}
}

func TestPiPackageInstallTaskRefusesAnInstalledPackage(t *testing.T) {
	s := piNewSetup(t)
	s.installed()
	_, err := s.t.packageInstall().Handler.Task(context.Background(), s.env, piRaw(map[string]any{}))
	piRefused(t, err, errcode.TailscalePackagePresent)
	if len(s.h.ran) != 0 || len(s.h.files) != 0 {
		t.Errorf("ran %v and wrote %v despite the refusal", s.h.ran, s.h.files)
	}
}

func TestPiPackageInstallTaskRefusesAConflictBeforeWriting(t *testing.T) {
	s := piNewSetup(t)
	s.h.reads[sourcesPath] = []byte("deb https://example.com/ stable main\n")
	_, err := s.t.packageInstall().Handler.Task(context.Background(), s.env, piRaw(map[string]any{}))
	piRefused(t, err, errcode.TailscaleRepoConflict)
	if len(s.h.ran) != 0 || len(s.h.files) != 0 {
		t.Errorf("ran %v and wrote %v despite the conflict", s.h.ran, s.h.files)
	}
}

func TestPiPackageInstallTaskFailures(t *testing.T) {
	apt1, apt2 := piVec(piApt, platform.AptUpdateTailscale()), piVec(piApt, platform.AptInstallTailscale())
	t.Run("the repository refresh fails", func(t *testing.T) {
		s := piNewSetup(t)
		s.h.script(apt1, tsRun{res: core.CmdResult{ExitCode: 100, Stderr: []byte("W: x\nE: Failed to fetch https://pkgs.tailscale.com\n")}})
		_, err := s.t.packageInstall().Handler.Task(context.Background(), s.env, piRaw(map[string]any{}))
		if err == nil || !strings.Contains(err.Error(), "update") || !strings.Contains(err.Error(), "Failed to fetch") || core.IsDefinite(err) {
			t.Errorf("error %v, want the apt-get failure, not definite", err)
		}
		if got := s.ranApt(); len(got) != 1 {
			t.Errorf("apt-get ran %v, want only APT-1", got)
		}
	})
	t.Run("the install fails and leaves its effect intended", func(t *testing.T) {
		s := piNewSetup(t)
		s.h.script(apt2, tsRun{res: core.CmdResult{ExitCode: 100, Stderr: []byte("E: Unable to locate package tailscale\n")}})
		_, err := s.t.packageInstall().Handler.Task(context.Background(), s.env, piRaw(map[string]any{}))
		if err == nil || !strings.Contains(err.Error(), "Unable to locate package") || core.IsDefinite(err) {
			t.Errorf("error %v, want the apt-get failure, not definite", err)
		}
		effects, _ := s.env.Effects.List(context.Background())
		if len(effects) != 3 || effects[2].State != core.EffectIntended {
			t.Errorf("effects %+v, want the package effect left intended for the engine to settle", effects)
		}
		if _, _, ok, _ := liveInstall(context.Background(), s.h.db.Read()); ok {
			t.Error("an install was recorded for a failed apt-get")
		}
	})
	t.Run("apt-get succeeds but the package is not installed", func(t *testing.T) {
		s := piNewSetup(t)
		s.env.Exec = s.h.env("c1").Exec
		_, err := s.t.packageInstall().Handler.Task(context.Background(), s.env, piRaw(map[string]any{}))
		if err == nil || !strings.Contains(err.Error(), "not installed") {
			t.Errorf("error %v, want a report that tailscale is not installed", err)
		}
	})
}

func TestPiPackageInstallVerify(t *testing.T) {
	s := piNewSetup(t)
	h := s.t.packageInstall().Handler
	if err := h.Verify(context.Background(), s.env, piRaw(map[string]any{}), piRaw(PackageInstalled{Version: piVersion})); err == nil {
		t.Error("verify passed without the package")
	}
	s.installed()
	for name, out := range map[string]PackageInstalled{"the recorded version": {Version: piVersion}, "no recorded version (recovery)": {}} {
		if err := h.Verify(context.Background(), s.env, piRaw(map[string]any{}), piRaw(out)); err != nil {
			t.Errorf("verify with %s: %v", name, err)
		}
	}
	if err := h.Verify(context.Background(), s.env, piRaw(map[string]any{}), piRaw(PackageInstalled{Version: "1.0.0"})); err == nil {
		t.Error("verify passed with a version other than the installed one")
	}
}

func TestPiPackageInstallObserve(t *testing.T) {
	s := piNewSetup(t)
	h := s.t.packageInstall().Handler
	observe := func(resource string) core.Observation {
		t.Helper()
		obs, err := h.Observe(context.Background(), s.env, core.Effect{Resource: resource, State: core.EffectIntended})
		if err != nil {
			t.Fatal(err)
		}
		if obs.Matches != 1 || !obs.Exclusive {
			t.Errorf("observation of %s %+v, want one exclusive match", resource, obs)
		}
		return obs
	}
	if got := observe("tailscale.package:tailscale").Current; string(got.State) != "null" {
		t.Errorf("absent package observed as %s, want null", got.State)
	}
	s.installed()
	obs := observe("tailscale.package:tailscale")
	if string(obs.Current.State) != `{"installed":true}` || !strings.Contains(string(obs.Current.Version), piVersion) {
		t.Errorf("installed package observed as %+v", obs.Current)
	}
	if got := observe("tailscale.file:" + sourcesPath).Current; string(got.State) != "null" {
		t.Errorf("absent file observed as %s, want null", got.State)
	}
	s.h.reads[sourcesPath] = []byte(piSources("ubuntu", "noble"))
	if got := observe("tailscale.file:" + sourcesPath).Current; got.State == nil || string(got.State) == "null" || !strings.Contains(string(got.State), "sha256") {
		t.Errorf("present file observed as %s, want its digest", got.State)
	}
	// What the task records after a write is what an observation finds.
	done := piNewSetup(t)
	if _, err := done.t.packageInstall().Handler.Task(context.Background(), done.env, piRaw(map[string]any{})); err != nil {
		t.Fatal(err)
	}
	done.h.reads[sourcesPath] = done.h.files[sourcesPath]
	effects, _ := done.env.Effects.List(context.Background())
	for _, e := range []core.Effect{effects[1], effects[2]} {
		obs, err := done.t.packageInstall().Handler.Observe(context.Background(), done.env, e)
		if err != nil || string(obs.Current.State) != string(e.Post.State) || string(obs.Current.Version) != string(e.Post.Version) {
			t.Errorf("observation of %s %+v (error %v) differs from the recorded post-state %+v", e.Resource, obs.Current, err, *e.Post)
		}
	}
	for _, bad := range []string{"", "docker.container:c1", "tailscale.file:/etc/passwd/x"} {
		if _, err := h.Observe(context.Background(), s.env, core.Effect{Resource: bad}); err == nil {
			t.Errorf("observed %q without an error", bad)
		}
	}
}

func TestPiPackageInstallInverse(t *testing.T) {
	apt6 := platform.AptRemoveTailscale()
	t.Run("removes the package this change installed", func(t *testing.T) {
		s := piNewSetup(t)
		def := s.t.packageInstall()
		if _, err := def.Handler.Task(context.Background(), s.env, piRaw(map[string]any{})); err != nil {
			t.Fatal(err)
		}
		s.h.ran = nil
		if _, err := def.Handler.Inverse(context.Background(), s.env, core.Effect{Resource: "tailscale.package:tailscale", State: core.EffectMade}); err != nil {
			t.Fatal(err)
		}
		if got := s.ranApt(); !slices.EqualFunc(got, [][]string{apt6}, slices.Equal[[]string]) {
			t.Errorf("apt-get ran %v, want APT-6 only", got)
		}
		if _, _, ok, err := liveInstall(context.Background(), s.h.db.Read()); err != nil || ok {
			t.Errorf("live install after the undo: found %v, error %v, want none", ok, err)
		}
	})
	t.Run("a package already gone is left", func(t *testing.T) {
		s := piNewSetup(t)
		if _, err := s.t.packageInstall().Handler.Inverse(context.Background(), s.env, core.Effect{Resource: "tailscale.package:tailscale"}); err != nil {
			t.Fatal(err)
		}
		if len(s.h.ran) != 0 {
			t.Errorf("ran %v for a package that is not installed", s.h.ran)
		}
	})
	t.Run("a failed removal is reported", func(t *testing.T) {
		s := piNewSetup(t)
		s.installed()
		s.h.script(piVec(piApt, apt6), tsRun{res: core.CmdResult{ExitCode: 100, Stderr: []byte("E: Could not get lock\n")}})
		_, err := s.t.packageInstall().Handler.Inverse(context.Background(), s.env, core.Effect{Resource: "tailscale.package:tailscale"})
		if err == nil || !strings.Contains(err.Error(), "Could not get lock") {
			t.Errorf("error %v, want the apt-get failure", err)
		}
	})
	t.Run("removes a repository file the change wrote", func(t *testing.T) {
		s := piNewSetup(t)
		s.h.files[sourcesPath] = []byte("x")
		s.h.files[keyringPath] = []byte("y")
		if _, err := s.t.packageInstall().Handler.Inverse(context.Background(), s.env, core.Effect{Resource: "tailscale.file:" + sourcesPath}); err != nil {
			t.Fatal(err)
		}
		if _, ok := s.h.files[sourcesPath]; ok {
			t.Error("the source list is still there")
		}
		if _, ok := s.h.files[keyringPath]; !ok {
			t.Error("the keyring was removed with it")
		}
	})
	t.Run("refuses a resource it did not make", func(t *testing.T) {
		s := piNewSetup(t)
		for _, bad := range []string{"", "tailscale.file:/etc/passwd", "tailscale.package:other", "docker.container:c1"} {
			if _, err := s.t.packageInstall().Handler.Inverse(context.Background(), s.env, core.Effect{Resource: bad}); err == nil {
				t.Errorf("inverse of %q succeeded", bad)
			}
		}
	})
}
