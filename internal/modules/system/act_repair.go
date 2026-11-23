package system

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

// RepairInput is the input of system.packages.repair.
type RepairInput struct{}

func (m module) repairAction() core.ActionDef {
	return core.DefineTask[RepairInput, Applied](core.ActionSpec{
		Name: ID + ".packages.repair", Version: 1,
		Summary:      "Finish an interrupted package run and fix broken dependencies.",
		Class:        core.ClassIrreversible,
		Side:         core.SideAgent,
		LockKeys:     []string{aptLockKey},
		ApplyTimeout: 30 * time.Minute,
		Rechecks:     []string{"docker"},
	}, repairHandler{m: m})
}

type repairHandler struct{ m module }

// Plan lists what dpkg --audit reports. It simulates nothing: a locking apt
// run refuses while dpkg's journal is dirty.
func (h repairHandler) Plan(ctx context.Context, env core.Env, _ RepairInput) (core.PlanOutcome, error) {
	audit, err := runOK(ctx, env, dpkgBin, platform.SystemDpkgAudit())
	if err != nil {
		return core.PlanOutcome{}, err
	}
	var diff []core.DiffLine
	for _, l := range auditLines(audit) {
		diff = append(diff, core.DiffLine{Op: " ", Text: l})
	}
	if len(diff) == 0 {
		diff = append(diff, core.DiffLine{Op: " ", Text: "dpkg reports no interrupted package"})
	}
	diff = append(diff,
		core.DiffLine{Op: "+", Text: "Configure every unpacked package, keeping changed configuration files (dpkg --configure -a)"},
		core.DiffLine{Op: "+", Text: "Dependencies are fixed afterwards (apt-get -f install); a fix that would remove a protected package stops with the removal list"})
	host, err := h.m.hostname()
	if err != nil {
		return core.PlanOutcome{}, err
	}
	sum := sha256.Sum256(audit)
	return core.PlanOutcome{Diff: diff, StateHash: hex.EncodeToString(sum[:]), Confirmation: host}, nil
}

func (h repairHandler) Task(ctx context.Context, env core.Env, _ RepairInput) (Applied, error) {
	defer invalidateUpdates(env.HostID)
	before, err := installedVersions(ctx, env)
	if err != nil {
		return Applied{}, core.Definite(err)
	}
	res, err := env.Exec.Run(ctx, core.Cmd{Path: dpkgBin, Args: platform.SystemDpkgConfigure()})
	if err != nil {
		return Applied{}, fmt.Errorf("system: run dpkg --configure -a: %w", err)
	}
	if res.ExitCode != 0 {
		return Applied{}, aptFailure(slices.Concat(res.Stdout, res.Stderr))
	}
	sim, err := aptResim(ctx, env, "fix-install", nil)
	if err != nil {
		return Applied{}, err
	}
	protected, err := h.m.protectedRemovals(ctx, env, sim.Removals())
	if err != nil {
		return Applied{}, err
	}
	if len(protected) > 0 {
		list := make([]string, len(protected))
		for i, p := range protected {
			list[i] = p.Name + " (" + p.Reason + ")"
		}
		return Applied{}, errcode.New(errcode.SystemProtectedPackage, "fixing the dependencies would remove protected packages: "+
			strings.Join(list, ", ")+"; it would remove: "+strings.Join(sim.Removals(), ", "))
	}
	if _, _, err := aptRun(ctx, env, "fix-install", nil); err != nil {
		return Applied{}, err
	}
	after, err := installedVersions(ctx, env)
	if err != nil {
		return Applied{}, err
	}
	return diffInstalled(before, after), nil
}

// Verify checks dpkg reports no interrupted package.
func (h repairHandler) Verify(ctx context.Context, env core.Env, _ RepairInput, _ Applied) error {
	audit, err := runOK(ctx, env, dpkgBin, platform.SystemDpkgAudit())
	if err != nil {
		return err
	}
	if s := strings.TrimSpace(string(audit)); s != "" {
		return errors.New("system: dpkg still reports interrupted packages: " + s)
	}
	return nil
}

// auditLines reads the non-blank lines of dpkg --audit, trimmed.
func auditLines(out []byte) []string {
	var lines []string
	for line := range strings.Lines(string(out)) {
		if l := strings.TrimSpace(line); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}
