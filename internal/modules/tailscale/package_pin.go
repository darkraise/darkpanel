package tailscale

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

const (
	// pinTxResource is the one effect of a pin: the tailscale package's version and hold.
	pinTxResource = "tailscale.package:tailscale"
	// pinTxArchive is where apt-get leaves the packages it downloads.
	pinTxArchive = "/var/cache/apt/archives"
	// pinTxPoll is the wait between two probes of the check and of the rollback's verification.
	pinTxPoll = 2 * time.Second
	// pinRollbackBudget bounds the rollback of a failed pin, which runs on a context of its own.
	pinRollbackBudget = 10 * time.Minute

	txAptGet  = "/usr/bin/apt-get"
	txAptMark = "/usr/bin/apt-mark"
	txDpkg    = "/usr/bin/dpkg"
)

var (
	// pinTxVersionPattern is a tailscale package version the exec allowlist admits in APT-3 and APT-4.
	pinTxVersionPattern = regexp.MustCompile(`^[0-9][0-9A-Za-z.+~-]{0,63}$`)
	// pinTxStemPattern is the exec allowlist's pattern of the part before ".deb" of an APT-5 package path, which
	// the platform package does not export.
	pinTxStemPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

// packagePin is tailscale.package.pin: it installs exactly one tailscale version and holds it, as an agent-local
// transaction that rolls back to the prior version, from a package staged on disk, when the node does not come
// back.
func (t ts) packagePin() core.ActionDef {
	return core.DefineTask[PinInput, Pinned](core.ActionSpec{
		Name: ID + ".package.pin", Summary: "Install exactly one tailscale version and hold it, rolling back when the node does not come back.",
		Version: 1, Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: []string{lockKey},
		ApplyTimeout: 20 * time.Minute,
		Available:    t.available,
	}, packagePinHandler{t: t})
}

// packagePinHandler is package.pin's handler. Every phase of the pin is recorded in its tailscale_pins row before
// the command it covers runs, so a crash leaves the row naming the last phase that may be half done.
type packagePinHandler struct{ t ts }

// txDebName is the flat file name of the package of version for this host's architecture.
func (h packagePinHandler) txDebName(version string) string {
	return "tailscale_" + version + "_" + h.t.d.arch + ".deb"
}

// txPackageDoc is the state of the tailscale package at version with the hold.
func txPackageDoc(version string, held bool) core.StateDoc {
	b, _ := json.Marshal(struct {
		Version string `json:"version"`
		Held    bool   `json:"held"`
	}{version, held})
	return core.StateDoc{State: b}
}

// precheck refuses a pin before anything is written or run other than apt-mark showhold, and returns the
// installed version and whether it is held.
func (h packagePinHandler) precheck(ctx context.Context, env core.Env, in PinInput) (prior string, held bool, err error) {
	if !pinTxVersionPattern.MatchString(in.Version) {
		detail := "the version is not a tailscale package version"
		return "", false, core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: detail,
			Fields: []errcode.FieldError{{Path: "/version", Message: detail}}})
	}
	prior, err = h.t.installedVersion(ctx, env)
	if err != nil {
		return "", false, err
	}
	if prior == "" {
		return "", false, core.Definite(errcode.New(errcode.TailscaleUnavailable,
			"the tailscale package is not installed; install it with tailscale.package.install"))
	}
	if _, _, err := h.t.repoFiles(); err != nil {
		return "", false, core.Definite(err)
	}
	// The rollback installs the staged prior package with APT-5, so a pin that could not roll back is refused.
	if !pinTxStemPattern.MatchString(strings.TrimSuffix(h.txDebName(prior), ".deb")) {
		return "", false, core.Definite(errcode.New(errcode.TailscalePinFailed,
			"tailscale "+prior+" cannot be staged for a rollback: its package file name is not one the exec allowlist admits"))
	}
	held, err = h.t.held(ctx, env)
	if err != nil {
		return "", false, err
	}
	return prior, held, nil
}

func (h packagePinHandler) Plan(ctx context.Context, env core.Env, in PinInput) (core.PlanOutcome, error) {
	prior, held, err := h.precheck(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	var diff []core.DiffLine
	if prior != in.Version {
		diff = append(diff, core.DiffLine{Op: "-", Text: "tailscale " + prior}, core.DiffLine{Op: "+", Text: "tailscale " + in.Version})
	} else {
		diff = append(diff, core.DiffLine{Op: " ", Text: "tailscale " + prior + " stays installed"})
	}
	if held {
		diff = append(diff, core.DiffLine{Op: " ", Text: "tailscale is held now; the hold moves to " + in.Version})
	} else {
		diff = append(diff, core.DiffLine{Op: "+", Text: "hold tailscale at " + in.Version})
	}
	diff = append(diff, core.DiffLine{Op: " ", Text: fmt.Sprintf("roll back to tailscale %s if the node is not back within %d s",
		prior, int(pinCheckWindow/time.Second))})
	state := strings.Join([]string{"installed=" + prior, "held=" + strconv.FormatBool(held), "version=" + in.Version, "arch=" + h.t.d.arch}, "\n")
	sum := sha256.Sum256([]byte(state))
	return core.PlanOutcome{Diff: diff, StateHash: hex.EncodeToString(sum[:])}, nil
}

// Task runs the pin in the transient unit: it records the prior state, journals the package effect, then runs
// the forward steps; the first that fails rolls the host back to the recorded state.
func (h packagePinHandler) Task(ctx context.Context, env core.Env, in PinInput) (Pinned, error) {
	prior, held, err := h.precheck(ctx, env, in)
	if err != nil {
		return Pinned{}, err
	}
	var st *tslocal.Status
	if err := txProbe(ctx, func(ctx context.Context) (err error) {
		st, err = h.t.d.local().Status(ctx)
		return err
	}); err != nil {
		return Pinned{}, core.Definite(errcode.Wrap(errcode.TailscaleUnavailable, err, "tailscaled does not answer on its socket"))
	}
	var viaTailnet bool
	if err := txProbe(ctx, func(ctx context.Context) (err error) {
		_, viaTailnet, err = h.t.d.session(ctx)
		return err
	}); err != nil {
		return Pinned{}, core.Definite(fmt.Errorf("tailscale: read the agent's controller session: %w", err))
	}
	p := pinRow{
		ChangeID: env.ChangeID, Version: in.Version, Arch: h.t.d.arch, PriorVersion: prior, PriorHeld: held,
		PriorBackend: st.BackendState, LinkTailnet: viaTailnet, Phase: "recorded", UpdatedAt: env.Clock.Now(),
	}
	if err := env.DB.Write(ctx, func(tx *sql.Tx) error { return insertPin(ctx, tx, p) }); err != nil {
		return Pinned{}, err
	}
	after := txPackageDoc(in.Version, true)
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectModify, Resource: pinTxResource, Local: true, InverseLocal: true,
		Before: txPackageDoc(prior, held), After: &after,
	}, nil)
	if err != nil {
		if rerr := h.rollback(ctx, env, p); rerr != nil {
			err = errors.Join(err, rerr)
		}
		return Pinned{}, err
	}
	// The pin's Observe and Inverse cover only the package, so the repository files are written as owned
	// (uninstall removes them) but not journaled.
	noJournal := env
	noJournal.Effects = nil
	if _, err := h.t.ensureRepo(ctx, noJournal); err != nil {
		if rerr := h.rollback(ctx, env, p); rerr != nil {
			return Pinned{}, errors.Join(err, rerr)
		}
		if serr := env.Effects.Skipped(ctx, seq); serr != nil {
			return Pinned{}, errors.Join(err, serr)
		}
		return Pinned{}, err
	}
	debName := h.txDebName(prior)
	steps := []struct {
		name, phase string
		run         func() error
	}{
		{"unhold", "unheld", func() error { return txRun(ctx, env, txAptMark, "unhold", "tailscale") }},
		{"stage-prior", "staged", func() error {
			if err := txRun(ctx, env, txAptGet, platform.AptUpdateTailscale()...); err != nil {
				return err
			}
			return txRun(ctx, env, txAptGet, platform.AptStageVersion(prior)...)
		}},
		{"stage-target", "", func() error { return txRun(ctx, env, txAptGet, platform.AptStageVersion(in.Version)...) }},
		{"copy-prior", "", func() error { return h.txCopyPrior(ctx, env, debName) }},
		{"install", "installed", func() error { return txRun(ctx, env, txAptGet, platform.AptInstallVersion(in.Version)...) }},
		{"hold", "held", func() error { return txRun(ctx, env, txAptMark, "hold", "tailscale") }},
		{"check", "checking", func() error { return txAwait(ctx, env, h.txCheckProbe(p)) }},
	}
	for _, s := range steps {
		if s.phase != "" {
			if err := setPinPhase(ctx, env.DB, env.ChangeID, s.phase, env.Clock.Now()); err != nil {
				return Pinned{}, h.txFail(ctx, env, seq, prior, s.name, err)
			}
		}
		if err := txStep(ctx, env, env.ChangeID, s.name, s.run); err != nil {
			return Pinned{}, h.txFail(ctx, env, seq, prior, s.name, err)
		}
	}
	if err := setPinPhase(ctx, env.DB, env.ChangeID, "done", env.Clock.Now()); err != nil {
		return Pinned{}, err
	}
	if err := env.Effects.Made(ctx, seq, pinTxResource, after); err != nil {
		return Pinned{}, err
	}
	return Pinned{Version: in.Version, PriorVersion: prior, PriorHeld: held}, nil
}

// txCopyPrior stages the prior package apt-get downloaded into the managed directory and records its name and
// SHA-256, so the rollback depends on neither the network nor the repository.
func (h packagePinHandler) txCopyPrior(ctx context.Context, env core.Env, name string) error {
	data, err := h.t.d.readFile(pinTxArchive + "/" + name)
	if err != nil {
		return fmt.Errorf("tailscale: read the downloaded package: %w", err)
	}
	if err := env.Files.WriteManaged(ctx, ID, debDir, name, data, 0o600, 0, 0); err != nil {
		return fmt.Errorf("tailscale: stage %s: %w", name, err)
	}
	sum := sha256.Sum256(data)
	return setPinStaged(ctx, env.DB, env.ChangeID, name, hex.EncodeToString(sum[:]), env.Clock.Now())
}

// txFail rolls back a pin whose forward step failed with cause and returns the pin's tailscale.pin_failed error.
// The pin effect is skipped only when the rollback verified that the host is as before. The rollback runs on a
// context detached from the forward one and bounded by pinRollbackBudget: a forward step that failed because the
// apply budget ran out would otherwise leave the host on the new version with nothing restored.
func (h packagePinHandler) txFail(ctx context.Context, env core.Env, seq int, prior, step string, cause error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pinRollbackBudget)
	defer cancel()
	p, ok, err := loadPin(ctx, env.DB.Read(), env.ChangeID)
	if err == nil && !ok {
		err = fmt.Errorf("tailscale: pin %s is not recorded", env.ChangeID)
	}
	if err == nil {
		err = h.rollback(ctx, env, p)
	}
	if err != nil {
		return errcode.Wrap(errcode.TailscalePinFailed, errors.Join(cause, err), fmt.Sprintf(
			"%s failed (%s) and the rollback to %s failed (%s); tailscale needs repair", step, txFirstLine(cause), prior, txFirstLine(err)))
	}
	detail := fmt.Sprintf("%s failed (%s); tailscale is back at %s", step, txFirstLine(cause), prior)
	if err := env.Effects.Skipped(ctx, seq); err != nil {
		return errcode.Wrap(errcode.TailscalePinFailed, errors.Join(cause, err), detail)
	}
	return errcode.Wrap(errcode.TailscalePinFailed, cause, detail)
}

// rollback restores the prior version and hold state of pin p, each step journaled with addPinStep: set phase
// rolling_back; "rollback-unhold" (apt-mark unhold tailscale, only when held); "rollback-configure"
// (dpkg --configure -a, only when phase 4 may have been interrupted: p.Phase is installed, or rolling_back with
// p.StagedDeb set); "rollback-install" (APT-5 with p.StagedDeb after checking its SHA-256 against p.StagedSHA256,
// only when the install may have started: p.Phase is installed, held, checking or done, or rolling_back with
// p.StagedDeb set); "rollback-hold" (apt-mark hold tailscale, only when p.PriorHeld); "rollback-restart"
// (env.Units restart tailscaled.service, only when the install may have started); "rollback-verify"
// (installedVersion == p.PriorVersion, held == p.PriorHeld, and the backend state back to p.PriorBackend within
// pinCheckWindow). A rolling_back row with p.StagedDeb empty installed nothing, because copy-prior records it
// before phase installed is written. It ends in phase rolled_back, or failed with the error of the
// first failing step (which it returns). A pin row in phase recorded changed nothing: rollback sets rolled_back and
// runs no step.
func (h packagePinHandler) rollback(ctx context.Context, env core.Env, p pinRow) error {
	if p.Phase == "recorded" {
		return setPinPhase(ctx, env.DB, p.ChangeID, "rolled_back", env.Clock.Now())
	}
	if err := setPinPhase(ctx, env.DB, p.ChangeID, "rolling_back", env.Clock.Now()); err != nil {
		return err
	}
	staged := p.StagedDeb != ""
	installStarted := slices.Contains([]string{"installed", "held", "checking", "done"}, p.Phase) || (p.Phase == "rolling_back" && staged)
	type step struct {
		name string
		run  func() error
	}
	var steps []step
	held, heldErr := h.t.held(ctx, env)
	if heldErr != nil || held {
		steps = append(steps, step{"rollback-unhold", func() error {
			if heldErr != nil {
				return heldErr
			}
			return txRun(ctx, env, txAptMark, "unhold", "tailscale")
		}})
	}
	if p.Phase == "installed" || (p.Phase == "rolling_back" && staged) {
		steps = append(steps, step{"rollback-configure", func() error { return txRun(ctx, env, txDpkg, "--configure", "-a") }})
	}
	if installStarted {
		steps = append(steps, step{"rollback-install", func() error {
			deb, err := h.txStaged(p)
			if err != nil {
				return err
			}
			return txRun(ctx, env, txAptGet, platform.AptInstallDeb(deb)...)
		}})
	}
	if p.PriorHeld {
		steps = append(steps, step{"rollback-hold", func() error { return txRun(ctx, env, txAptMark, "hold", "tailscale") }})
	}
	if installStarted {
		steps = append(steps, step{"rollback-restart", func() error {
			if env.Units == nil {
				return errors.New("tailscale: no unit client to restart " + tailscaledUnit)
			}
			return env.Units.Restart(ctx, tailscaledUnit)
		}})
	}
	steps = append(steps, step{"rollback-verify", func() error { return txAwait(ctx, env, h.txRestoredProbe(env, p)) }})
	for _, s := range steps {
		if err := txStep(ctx, env, p.ChangeID, s.name, s.run); err != nil {
			err = fmt.Errorf("%s: %w", s.name, err)
			if perr := setPinPhase(ctx, env.DB, p.ChangeID, "failed", env.Clock.Now()); perr != nil {
				return errors.Join(err, perr)
			}
			return err
		}
	}
	return setPinPhase(ctx, env.DB, p.ChangeID, "rolled_back", env.Clock.Now())
}

// txStaged resolves the staged prior package of p and checks it is the file the pin staged.
func (h packagePinHandler) txStaged(p pinRow) (string, error) {
	if p.StagedDeb == "" {
		return "", errors.New("tailscale: no prior package was staged")
	}
	deb := p.StagedDeb
	if !strings.Contains(deb, "/") {
		deb = debDir + "/" + deb
	}
	if path.Dir(deb) != debDir {
		return "", fmt.Errorf("tailscale: the staged package %s is not in %s", deb, debDir)
	}
	data, err := h.t.d.readFile(deb)
	if err != nil {
		return "", fmt.Errorf("tailscale: read the staged package: %w", err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != p.StagedSHA256 {
		return "", errors.New("tailscale: the staged package does not match its recorded SHA-256")
	}
	return deb, nil
}

// txCheckProbe is the check of a pin: tailscaled answers in the backend state it had before the pin and, when the
// agent reaches its controller over the tailnet, the agent's session to the controller is up.
func (h packagePinHandler) txCheckProbe(p pinRow) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := h.txBackendIs(ctx, p.PriorBackend); err != nil {
			return err
		}
		if !p.LinkTailnet {
			return nil
		}
		up, _, err := h.t.d.session(ctx)
		if err != nil {
			return fmt.Errorf("tailscale: read the agent's controller session: %w", err)
		}
		if !up {
			return errors.New("tailscale: the agent's session to the controller is down")
		}
		return nil
	}
}

// txRestoredProbe is the rollback's verification: the prior version, the prior hold state and the prior backend
// state.
func (h packagePinHandler) txRestoredProbe(env core.Env, p pinRow) func(context.Context) error {
	return func(ctx context.Context) error {
		version, err := h.t.installedVersion(ctx, env)
		if err != nil {
			return err
		}
		if version != p.PriorVersion {
			return fmt.Errorf("tailscale: the installed version is %q, not %s", version, p.PriorVersion)
		}
		held, err := h.t.held(ctx, env)
		if err != nil {
			return err
		}
		if held != p.PriorHeld {
			return fmt.Errorf("tailscale: the package's hold is %t, not %t", held, p.PriorHeld)
		}
		return h.txBackendIs(ctx, p.PriorBackend)
	}
}

// txBackendIs fails unless tailscaled answers in the backend state want.
func (h packagePinHandler) txBackendIs(ctx context.Context, want string) error {
	st, err := h.t.d.local().Status(ctx)
	if err != nil {
		return fmt.Errorf("tailscale: tailscaled does not answer: %w", err)
	}
	if st.BackendState != want {
		return fmt.Errorf("tailscale: the backend state is %s, not %s", st.BackendState, want)
	}
	return nil
}

// Verify checks the host from the input alone, as recovery calls it with the zero output.
func (h packagePinHandler) Verify(ctx context.Context, env core.Env, in PinInput, _ Pinned) error {
	version, err := h.t.installedVersion(ctx, env)
	if err != nil {
		return err
	}
	if version != in.Version {
		return fmt.Errorf("tailscale: the installed version is %q, not %s", version, in.Version)
	}
	held, err := h.t.held(ctx, env)
	if err != nil {
		return err
	}
	if !held {
		return errors.New("tailscale: the tailscale package is not held")
	}
	return nil
}

// txRun runs one vector and keeps only its error.
func txRun(ctx context.Context, env core.Env, path string, args ...string) error {
	_, err := run(ctx, env, path, args...)
	return err
}

// txStep runs one step of pin changeID and appends it to the pin's log, ok or failed with the error's first
// line. A step whose log entry cannot be written fails with that error too.
func txStep(ctx context.Context, env core.Env, changeID, name string, step func() error) error {
	err := step()
	result := "ok"
	if err != nil {
		reason := []rune(txFirstLine(err))
		if len(reason) > 200 {
			reason = reason[:200]
		}
		result = "failed: " + string(reason)
	}
	if jerr := addPinStep(ctx, env.DB, changeID, name, result, env.Clock.Now()); jerr != nil {
		return errors.Join(err, jerr)
	}
	return err
}

// txProbe runs one probe under the pinTxProbe bound. The bound is real time: it limits one call to a socket,
// while the change's clock measures the wait between probes.
func txProbe(ctx context.Context, probe func(ctx context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, pinTxProbe)
	defer cancel()
	return probe(ctx)
}

// txAwait probes at once and then every pinTxPoll on env.Clock until a probe succeeds, and fails with the last
// probe's error once pinCheckWindow has passed since the first.
func txAwait(ctx context.Context, env core.Env, probe func(ctx context.Context) error) error {
	deadline := env.Clock.Now().Add(pinCheckWindow)
	for {
		err := txProbe(ctx, probe)
		if err == nil {
			return nil
		}
		if !env.Clock.Now().Before(deadline) {
			return fmt.Errorf("tailscale: not reached within %s: %w", pinCheckWindow, err)
		}
		tick := make(chan struct{})
		timer := env.Clock.AfterFunc(pinTxPoll, func() { close(tick) })
		select {
		case <-tick:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

// txFirstLine is the first line of err's message.
func txFirstLine(err error) string {
	line, _, _ := strings.Cut(err.Error(), "\n")
	return line
}
