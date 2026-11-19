package system

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

// EngineInstallInput is the input of system.docker.engine-install.
type EngineInstallInput struct{}

// EngineUpdateInput is the input of system.docker.engine-update.
type EngineUpdateInput struct{}

func (m module) engineInstallAction() core.ActionDef {
	return core.DefineTask[EngineInstallInput, Applied](core.ActionSpec{
		Name: ID + ".docker.engine-install", Version: 1,
		Summary:      "Install Docker Engine from Docker's repository; undo removes the packages and repository files it added.",
		Class:        core.ClassCompensable,
		Side:         core.SideAgent,
		LockKeys:     []string{aptLockKey},
		ApplyTimeout: 30 * time.Minute,
		Rechecks:     []string{"docker"},
	}, engineInstallHandler{m: m})
}

func (m module) engineUpdateAction() core.ActionDef {
	return core.DefineTask[EngineUpdateInput, Applied](core.ActionSpec{
		Name: ID + ".docker.engine-update", Version: 1,
		Summary:      "Upgrade the Docker Engine packages to the versions Docker's repository offers.",
		Class:        core.ClassIrreversible,
		Side:         core.SideAgent,
		LockKeys:     []string{aptLockKey},
		ApplyTimeout: 30 * time.Minute,
		Rechecks:     []string{"docker"},
	}, engineUpdateHandler{m: m})
}

// refuseDocker refuses while the docker resource reads distro or docker-ce, naming the packages.
func (m module) refuseDocker(ctx context.Context, env core.Env) error {
	st, err := m.dockerState(ctx, env)
	if err != nil {
		return err
	}
	switch st.State {
	case "distro":
		return errcode.New(errcode.SystemDockerPresent,
			"the distribution's Docker packages are installed ("+strings.Join(st.Conflicts, ", ")+"); remove them first")
	case "docker-ce":
		return errcode.New(errcode.SystemDockerPresent, "Docker Engine is already installed (docker-ce "+st.Version+")")
	}
	return nil
}

type engineInstallHandler struct{ m module }

// Plan lists the two repository files and the five packages by name: Docker's repository does not exist yet,
// so no simulation can run.
func (h engineInstallHandler) Plan(ctx context.Context, env core.Env, _ EngineInstallInput) (core.PlanOutcome, error) {
	if err := h.m.refuseDocker(ctx, env); err != nil {
		return core.PlanOutcome{}, err
	}
	files, err := h.m.dockerRepoStatus()
	if err != nil {
		return core.PlanOutcome{}, err
	}
	info, err := readOSRelease(h.m.d.readFile)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	state := []string{"os=" + info.ID + "/" + info.Codename, "arch=" + h.m.d.arch}
	var diff []core.DiffLine
	for _, f := range files {
		if f.present {
			diff = append(diff, core.DiffLine{Op: " ", Text: "file " + f.path + " is already present and identical; it stays"})
			state = append(state, f.path+"=present")
		} else {
			diff = append(diff, core.DiffLine{Op: "+", Text: "file " + f.path})
			state = append(state, f.path+"=absent")
		}
	}
	for _, n := range dockerPackages {
		diff = append(diff, core.DiffLine{Op: "+", Text: "package " + n + " (the repository's current version)"})
	}
	diff = append(diff, core.DiffLine{Op: " ", Text: "The install stops before changing any package if apt would remove one."})
	sum := sha256.Sum256([]byte(strings.Join(state, "\n")))
	return core.PlanOutcome{Diff: diff, StateHash: hex.EncodeToString(sum[:])}, nil
}

// Task writes the absent repository files, refreshes Docker's source alone and strictly, re-simulates the
// install and refuses any removal, then installs. It journals each file it wrote and the set it installed, so
// the undo removes exactly those.
func (h engineInstallHandler) Task(ctx context.Context, env core.Env, _ EngineInstallInput) (Applied, error) {
	defer invalidateUpdates(env.HostID)
	if err := h.m.refuseDocker(ctx, env); err != nil {
		return Applied{}, core.Definite(err)
	}
	files, err := h.m.dockerRepoStatus()
	if err != nil {
		return Applied{}, core.Definite(err)
	}
	written, err := writeDockerRepo(ctx, env, files)
	if err != nil {
		return Applied{}, err
	}
	// Until a file is written, a refusal has changed nothing.
	fail := func(err error) error {
		if len(written) == 0 {
			return core.Definite(err)
		}
		return err
	}
	out, err := aptExec(ctx, env, platform.SystemAptUpdate(dockerListPath))
	if err := strictRefresh(out, err); err != nil {
		return Applied{}, fail(err)
	}
	sim, err := aptResim(ctx, env, "install", dockerPackages)
	if err != nil {
		return Applied{}, fail(err)
	}
	if removals := sim.Removals(); len(removals) > 0 {
		return Applied{}, fail(errcode.New(errcode.SystemRemovalRefused,
			"installing Docker Engine would remove "+strings.Join(removals, ", ")+"; remove them by hand first if that is intended"))
	}
	before, err := installedVersions(ctx, env)
	if err != nil {
		return Applied{}, fail(err)
	}
	var installs []string
	for _, n := range sim.Installs() {
		if base, _, _ := strings.Cut(n, ":"); before[base] == "" {
			installs = append(installs, n)
		}
	}
	if len(installs) == 0 {
		applied, _, err := aptRun(ctx, env, "install", dockerPackages)
		return applied, err
	}
	seq, err := env.Effects.Intend(ctx, installEffect(installs), nil)
	if err != nil {
		return Applied{}, err
	}
	applied, _, err := aptRun(ctx, env, "install", dockerPackages)
	if err != nil {
		if errcode.From(err).Code == errcode.SystemAptBusy {
			// apt gave up waiting for the lock before changing anything.
			if serr := env.Effects.Skipped(ctx, seq); serr != nil {
				return Applied{}, serr
			}
			return Applied{}, fail(err)
		}
		// The effect stays intended and the engine settles it through Observe.
		return applied, err
	}
	set := installs
	if len(applied.Installed) > 0 {
		set = applied.Installed
	}
	post, err := installState(ctx, env, set)
	if err != nil {
		return applied, err
	}
	if err := env.Effects.Made(ctx, seq, installResource(set), post); err != nil {
		return applied, err
	}
	return applied, nil
}

// Verify checks the five packages are installed, and the reported sets.
func (h engineInstallHandler) Verify(ctx context.Context, env core.Env, _ EngineInstallInput, out Applied) error {
	versions, err := installedVersions(ctx, env)
	if err != nil {
		return err
	}
	for _, n := range dockerPackages {
		if _, ok := versions[n]; !ok {
			return fmt.Errorf("system: %s is not installed", n)
		}
	}
	return verifyApplied(ctx, env, out)
}

func (h engineInstallHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	if path := repoFilePath(e); path != "" {
		return h.m.observeRepoFile(path)
	}
	return installHandler(h).Observe(ctx, env, e)
}

// Inverse removes a repository file the install wrote, or the packages it installed that are still present.
// Removing those packages may take the Docker packages, which every other package change protects, but it
// refuses, having changed nothing, when apt would also remove another package or another protected one.
func (h engineInstallHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	if path := repoFilePath(e); path != "" {
		if err := env.Files.Remove(ctx, ID, path); err != nil {
			return core.InverseResult{}, fmt.Errorf("system: remove %s: %w", path, err)
		}
		return core.InverseResult{}, nil
	}
	defer invalidateUpdates(env.HostID)
	set, err := installSet(e)
	if err != nil {
		return core.InverseResult{}, core.Definite(err)
	}
	present, err := presentOf(ctx, env, set)
	if err != nil {
		return core.InverseResult{}, core.Definite(err)
	}
	if len(present) == 0 {
		return core.InverseResult{}, nil
	}
	names := make([]string, 0, len(present))
	for n := range present {
		names = append(names, n)
	}
	slices.Sort(names)
	sim, err := aptPlan(ctx, env, "remove", names)
	if err != nil {
		return core.InverseResult{}, core.Definite(err)
	}
	if err := h.m.undoRemovalRefusal(ctx, env, names, sim, dockerPackages); err != nil {
		return core.InverseResult{}, core.Definite(err)
	}
	if _, _, err := aptRun(ctx, env, "remove", names); err != nil {
		if errcode.From(err).Code == errcode.SystemAptBusy {
			return core.InverseResult{}, core.Definite(err)
		}
		return core.InverseResult{}, err
	}
	return core.InverseResult{}, nil
}

type engineUpdateHandler struct{ m module }

func (h engineUpdateHandler) Plan(ctx context.Context, env core.Env, _ EngineUpdateInput) (core.PlanOutcome, error) {
	out, _, err := h.m.planSet(ctx, env, "install-only-upgrade", dockerPackages, nil)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if out.Confirmation, err = h.m.hostname(); err != nil {
		return core.PlanOutcome{}, err
	}
	return out, nil
}

// Task re-runs the plan's simulation to learn the confirmed set, then runs it.
func (h engineUpdateHandler) Task(ctx context.Context, env core.Env, _ EngineUpdateInput) (Applied, error) {
	planned, err := h.m.simulateSet(ctx, env, "install-only-upgrade", dockerPackages, nil)
	if err != nil {
		return Applied{}, core.Definite(err)
	}
	return runSet(ctx, env, "install-only-upgrade", dockerPackages, planned)
}

func (h engineUpdateHandler) Verify(ctx context.Context, env core.Env, _ EngineUpdateInput, out Applied) error {
	return verifyApplied(ctx, env, out)
}
