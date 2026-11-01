package tailscale

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// packageUpdate is tailscale.package.update: it refreshes Tailscale's apt repository and installs the version it
// offers. Nothing rolls an update back; a pin is the transactional way to change the version.
func (t ts) packageUpdate() core.ActionDef {
	return core.DefineTask[PackageUpdateInput, PackageUpdated](core.ActionSpec{
		Name: ID + ".package.update", Summary: "Update the tailscale package to the repository's current version.",
		Version: 1, Class: core.ClassIrreversible, Side: core.SideAgent, LockKeys: []string{lockKey},
		ApplyTimeout: 15 * time.Minute,
		Available:    t.available,
	}, packageUpdateHandler{t: t})
}

// packageUpdateHandler is package.update's handler. The version the repository offers is unknown until the
// repository is refreshed, so the plan names none and the task reports the versions before and after.
type packageUpdateHandler struct{ t ts }

func (h packageUpdateHandler) Plan(ctx context.Context, env core.Env, _ PackageUpdateInput) (core.PlanOutcome, error) {
	version, err := h.t.installedVersion(ctx, env)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if version == "" {
		return core.PlanOutcome{}, core.Definite(errcode.New(errcode.TailscaleUnavailable,
			"the tailscale package is not installed; install it with tailscale.package.install"))
	}
	// apt aborts a -y run on a held package, so a hold is refused rather than updated around.
	held, err := h.t.held(ctx, env)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if held {
		return core.PlanOutcome{}, core.Definite(errcode.New(errcode.TailscalePackageHeld,
			"the tailscale package is held at "+version+"; release the hold before updating"))
	}
	if _, _, err := h.t.repoFiles(); err != nil {
		return core.PlanOutcome{}, core.Definite(err)
	}
	host, err := h.t.d.hostname()
	if err != nil {
		return core.PlanOutcome{}, fmt.Errorf("tailscale: read the host name: %w", err)
	}
	sum := sha256.Sum256([]byte(version))
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: " ", Text: "tailscale " + version + " is installed"},
			{Op: "+", Text: "update to the version the Tailscale repository offers (apt-get update of that repository, then apt-get install tailscale)"},
		},
		Confirmation: host,
		StateHash:    hex.EncodeToString(sum[:]),
	}, nil
}

func (h packageUpdateHandler) Task(ctx context.Context, env core.Env, _ PackageUpdateInput) (PackageUpdated, error) {
	from, err := h.t.installedVersion(ctx, env)
	if err != nil {
		return PackageUpdated{}, err
	}
	// The engine refuses Intend for an action without Observe, so the repository files are written as owned
	// (uninstall removes them) but not journaled.
	noJournal := env
	noJournal.Effects = nil
	if _, err := h.t.ensureRepo(ctx, noJournal); err != nil {
		return PackageUpdated{}, err
	}
	if err := aptInstall(ctx, env); err != nil {
		return PackageUpdated{}, err
	}
	to, err := h.t.installedVersion(ctx, env)
	if err != nil {
		return PackageUpdated{}, err
	}
	if to == "" {
		return PackageUpdated{}, errors.New("tailscale: the package is not installed after the update")
	}
	return PackageUpdated{From: from, To: to}, nil
}

// Verify checks the package is installed, at the version the update reported when there is one: recovery
// passes the zero output.
func (h packageUpdateHandler) Verify(ctx context.Context, env core.Env, _ PackageUpdateInput, out PackageUpdated) error {
	version, err := h.t.installedVersion(ctx, env)
	if err != nil {
		return err
	}
	if version == "" {
		return errors.New("tailscale: the tailscale package is not installed")
	}
	if out.To != "" && version != out.To {
		return fmt.Errorf("tailscale: tailscale %s is installed, not %s", version, out.To)
	}
	return nil
}
