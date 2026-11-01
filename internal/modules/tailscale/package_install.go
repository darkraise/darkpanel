package tailscale

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

// packageInstallTimeout bounds package.install's apply and verify, and the Task's own context: the runner gives
// a context without a deadline only 30 seconds, and an apt-get install takes longer.
const packageInstallTimeout = 15 * time.Minute

// installPackageResource is the effect resource of the tailscale package.
const installPackageResource = ID + ".package:tailscale"

// installPackageDoc is the state of the tailscale package installed at version.
func installPackageDoc(version string) core.StateDoc {
	v, _ := json.Marshal(struct {
		Version string `json:"version"`
	}{version})
	return core.StateDoc{State: json.RawMessage(`{"installed":true}`), Version: v}
}

// packageInstall is tailscale.package.install: it adds Tailscale's apt repository when absent and installs the
// tailscale package on a host that does not have it. Its undo removes only what it added.
func (t ts) packageInstall() core.ActionDef {
	return core.DefineTask[PackageInstallInput, PackageInstalled](core.ActionSpec{
		Name: ID + ".package.install", Summary: "Install tailscale from Tailscale's apt repository, adding the repository when it is absent.",
		Version: 1, Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: []string{lockKey},
		ApplyTimeout: packageInstallTimeout,
	}, packageInstallHandler{t: t})
}

// packageInstallHandler is package.install's handler. It has no Recover: the engine's task sequence settles an
// interrupted install through Observe.
type packageInstallHandler struct{ t ts }

// refusePresent is the refusal of a host that already has the package.
func (packageInstallHandler) refusePresent(version string) error {
	return core.Definite(errcode.New(errcode.TailscalePackagePresent, "tailscale "+version+" is already installed"))
}

// definite marks a coded refusal of the repository check definite; it is raised before anything is written.
func (packageInstallHandler) definite(err error) error {
	var coded *errcode.Error
	if errors.As(err, &coded) {
		return core.Definite(err)
	}
	return err
}

func (h packageInstallHandler) Plan(ctx context.Context, env core.Env, _ PackageInstallInput) (core.PlanOutcome, error) {
	version, err := h.t.installedVersion(ctx, env)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if version != "" {
		return core.PlanOutcome{}, h.refusePresent(version)
	}
	files, err := h.t.repoStatus()
	if err != nil {
		return core.PlanOutcome{}, h.definite(err)
	}
	info, err := h.t.d.osRelease()
	if err != nil {
		return core.PlanOutcome{}, fmt.Errorf("tailscale: read the OS release: %w", err)
	}
	state := []string{"os=" + info.ID + "/" + info.Codename, "arch=" + h.t.d.arch}
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
	diff = append(diff, core.DiffLine{Op: "+", Text: "package tailscale (the repository's current version)"})
	sum := sha256.Sum256([]byte(strings.Join(state, "\n")))
	return core.PlanOutcome{Diff: diff, StateHash: hex.EncodeToString(sum[:])}, nil
}

// Task runs in the transient unit. It journals the package effect only once the package is known absent, and
// the file effects only for files it writes, so the undo removes only what this change added.
func (h packageInstallHandler) Task(ctx context.Context, env core.Env, _ PackageInstallInput) (PackageInstalled, error) {
	ctx, cancel := context.WithTimeout(ctx, packageInstallTimeout)
	defer cancel()
	version, err := h.t.installedVersion(ctx, env)
	if err != nil {
		return PackageInstalled{}, err
	}
	if version != "" {
		return PackageInstalled{}, h.refusePresent(version)
	}
	if _, err := h.t.ensureRepo(ctx, env); err != nil {
		return PackageInstalled{}, h.definite(err)
	}
	// The journal names a creation by its recovery identity until it is made.
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectCreate, Resource: ID + ".package:?tailscale",
		Recovery: &core.RecoveryID{Kind: "name", Value: "tailscale"}, Local: true, InverseLocal: true,
		Before: core.StateDoc{State: json.RawMessage("null")},
	}, nil)
	if err != nil {
		return PackageInstalled{}, err
	}
	// A failed apt-get leaves the effect intended; the engine settles it through Observe.
	if err := aptInstall(ctx, env); err != nil {
		return PackageInstalled{}, err
	}
	if version, err = h.t.installedVersion(ctx, env); err != nil {
		return PackageInstalled{}, err
	}
	if version == "" {
		return PackageInstalled{}, errors.New("tailscale: apt-get finished but tailscale is not installed")
	}
	if err := env.Effects.Made(ctx, seq, installPackageResource, installPackageDoc(version)); err != nil {
		return PackageInstalled{}, err
	}
	if err := env.DB.Write(ctx, func(tx *sql.Tx) error {
		return insertInstall(ctx, tx, env.ChangeID, version, env.Clock.Now())
	}); err != nil {
		return PackageInstalled{}, err
	}
	return PackageInstalled{Version: version}, nil
}

// Verify checks the package is installed, at the recorded version when there is one: recovery passes the zero
// output.
func (h packageInstallHandler) Verify(ctx context.Context, env core.Env, _ PackageInstallInput, out PackageInstalled) error {
	version, err := h.t.installedVersion(ctx, env)
	if err != nil {
		return err
	}
	if version == "" {
		return errors.New("tailscale: the tailscale package is not installed")
	}
	if out.Version != "" && version != out.Version {
		return fmt.Errorf("tailscale: tailscale %s is installed, not %s", version, out.Version)
	}
	return nil
}

// target is the object effect e names: the package, or one of the two repository files. A creation not yet
// made names its object by recovery identity.
func (packageInstallHandler) target(e core.Effect) (pkg bool, path string, err error) {
	noun, identity, _ := strings.Cut(e.Resource, ":")
	identity = strings.TrimPrefix(identity, "?")
	switch {
	case noun == ID+".package" && identity == "tailscale":
		return true, "", nil
	case noun == repoFileNoun && (identity == keyringPath || identity == sourcesPath):
		return false, identity, nil
	}
	return false, "", fmt.Errorf("tailscale: %q is not a resource tailscale.package.install makes", e.Resource)
}

func (h packageInstallHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	pkg, path, err := h.target(e)
	if err != nil {
		return core.Observation{}, err
	}
	absent := core.Observation{Current: core.StateDoc{State: json.RawMessage("null")}, Matches: 1, Exclusive: true}
	if pkg {
		version, err := h.t.installedVersion(ctx, env)
		if err != nil {
			return core.Observation{}, err
		}
		if version == "" {
			return absent, nil
		}
		return core.Observation{Current: installPackageDoc(version), Resource: installPackageResource, Matches: 1, Exclusive: true}, nil
	}
	b, err := h.t.d.readFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return absent, nil
	}
	if err != nil {
		return core.Observation{}, fmt.Errorf("tailscale: read %s: %w", path, err)
	}
	return core.Observation{Current: repoFileDoc(b), Resource: repoFileResource(path), Matches: 1, Exclusive: true}, nil
}

// Inverse removes the package when it is installed, or the repository file the effect names.
func (h packageInstallHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	pkg, path, err := h.target(e)
	if err != nil {
		return core.InverseResult{}, err
	}
	if !pkg {
		if err := env.Files.Remove(ctx, ID, path); err != nil {
			return core.InverseResult{}, fmt.Errorf("tailscale: remove %s: %w", path, err)
		}
		return core.InverseResult{}, nil
	}
	version, err := h.t.installedVersion(ctx, env)
	if err != nil || version == "" {
		return core.InverseResult{}, err
	}
	if _, err := run(ctx, env, "/usr/bin/apt-get", platform.AptRemoveTailscale()...); err != nil {
		return core.InverseResult{}, err
	}
	return core.InverseResult{}, setInstallRemoved(ctx, env.DB, env.ChangeID, env.Clock.Now())
}
