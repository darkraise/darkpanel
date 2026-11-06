package derp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/derp/artifact"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
)

const (
	// binaryUpdatePoll is the wait between two looks at derper during the health check and the rollback.
	binaryUpdatePoll = time.Second
	// binaryRollbackWindow is how long the rollback of a failed update may take: one restart, the wait for the unit up
	// to healthWindow, file moves and database writes.
	binaryRollbackWindow = 3 * time.Minute
	// binaryRecordWindow is how long one write of the rollback's bookkeeping may take.
	binaryRecordWindow = 30 * time.Second
	// binaryProbeWait is the bound of one look at derper in the health check, the limit stun.Probe applies to a
	// context that has no deadline.
	binaryProbeWait = 3 * time.Second
	// binaryEffectResource is the one effect of a binary update: the derper version and binary the server runs.
	binaryEffectResource = "derp.binary:derper"
)

// binaryUpdate is derp.binary.update: it replaces derper with the version this agent's release carries, as an
// agent-local transaction that rolls back to the previous binary, kept beside it, when the new one is not healthy.
func (d dp) binaryUpdate() core.ActionDef {
	return core.DefineTask[BinaryUpdateInput, BinaryUpdated](core.ActionSpec{
		Name: ID + ".binary.update", Summary: "Replace derper with the version this agent's release carries, rolling back when it is not healthy.",
		Version: 1, Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: []string{lockKey},
		ApplyTimeout: 15 * time.Minute, Available: d.available,
	}, binaryUpdateHandler{d: d})
}

// binaryUpdateHandler is derp.binary.update's handler. Every phase of the update is recorded in its derp_updates
// row before the change it covers, so a crash leaves the row naming the last phase that may be half done.
type binaryUpdateHandler struct{ d dp }

// binaryBound returns a child of ctx that ends after window, and the function that stops it. The window is on the
// host's clock so that tests can advance it.
func binaryBound(ctx context.Context, env core.Env, window time.Duration) (context.Context, func()) {
	bound, cancel := context.WithCancel(ctx)
	timer := env.Clock.AfterFunc(window, cancel)
	return bound, func() {
		timer.Stop()
		cancel()
	}
}

// binaryRecord runs write, one piece of the rollback's bookkeeping, within its own window. It is detached from
// ctx's cancellation because the bookkeeping must be recorded after ctx has ended or its window has run out. The
// window is real time, not env.Clock, because it bounds one call to the database, just as await bounds one probe.
func binaryRecord(ctx context.Context, write func(ctx context.Context) error) error {
	bound, cancel := context.WithTimeout(context.WithoutCancel(ctx), binaryRecordWindow)
	defer cancel()
	return write(bound)
}

// binaryDoc is the state of derper at version with the binary of SHA-256 sha.
func binaryDoc(version, sha string) core.StateDoc {
	b, _ := json.Marshal(struct {
		Version string `json:"version"`
		SHA256  string `json:"sha256"`
	}{version, sha})
	return core.StateDoc{State: b}
}

// precheck refuses an update before anything changes and returns the installed server.
func (h binaryUpdateHandler) precheck(ctx context.Context, env core.Env, in BinaryUpdateInput) (localServer, error) {
	if in.Version != artifact.DerperVersion {
		detail := "this agent's darkpanel release carries derper " + artifact.DerperVersion + "; update the agent first"
		return localServer{}, core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: detail,
			Fields: []errcode.FieldError{{Path: "/version", Message: detail}}})
	}
	row, ok, err := loadLocalServer(ctx, env.DB.Read())
	if err != nil {
		return localServer{}, err
	}
	if !ok {
		return localServer{}, core.Definite(errcode.New(errcode.DERPNotReady, "no DERP server is installed on this host"))
	}
	if row.DerperVersion == in.Version {
		detail := "derper " + in.Version + " is already installed"
		return localServer{}, core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: detail,
			Fields: []errcode.FieldError{{Path: "/version", Message: detail}}})
	}
	return row, nil
}

func (h binaryUpdateHandler) Plan(ctx context.Context, env core.Env, in BinaryUpdateInput) (core.PlanOutcome, error) {
	row, err := h.precheck(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{
		{Op: "-", Text: "derper " + row.DerperVersion},
		{Op: "+", Text: "derper " + in.Version},
		{Op: " ", Text: "the previous binary is kept as " + server.BinaryPrevPath},
		{Op: " ", Text: fmt.Sprintf("roll back to derper %s if the unit, the TLS probe, STUN and tailscaled are not healthy within %d s",
			row.DerperVersion, int(healthWindow/time.Second))},
	}
	state := strings.Join([]string{"from=" + row.DerperVersion, "sha=" + row.BinarySHA256, "to=" + in.Version, "arch=" + h.d.d.arch}, "\n")
	sum := sha256.Sum256([]byte(state))
	return core.PlanOutcome{Diff: diff, StateHash: hex.EncodeToString(sum[:])}, nil
}

// Task runs the update in the transient unit. It fetches derper before it journals anything, so a failed fetch
// changes nothing; then it journals the effect, records the update and runs the steps, each phase written before
// its change. The first step that fails rolls the host back to the previous binary.
func (h binaryUpdateHandler) Task(ctx context.Context, env core.Env, in BinaryUpdateInput) (BinaryUpdated, error) {
	row, err := h.precheck(ctx, env, in)
	if err != nil {
		return BinaryUpdated{}, err
	}
	bin, sha, err := h.d.d.fetch(ctx, h.d.d.version, artifact.DerperVersion, h.d.d.arch)
	if err != nil {
		return BinaryUpdated{}, core.Definite(err)
	}
	after := binaryDoc(in.Version, sha)
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectModify, Resource: binaryEffectResource, Local: true, InverseLocal: true,
		Before: binaryDoc(row.DerperVersion, row.BinarySHA256), After: &after,
	}, nil)
	if err != nil {
		return BinaryUpdated{}, err
	}
	u := updateRow{
		ChangeID: env.ChangeID, FromVersion: row.DerperVersion, FromSHA256: row.BinarySHA256, ToVersion: in.Version, ToSHA256: sha,
		Phase: "staged", UpdatedAt: env.Clock.Now(),
	}
	if err := insertUpdate(ctx, env.DB, u); err != nil {
		// Nothing on the host has changed, and without the row no rollback could tell what to restore.
		if serr := env.Effects.Skipped(ctx, seq); serr != nil {
			return BinaryUpdated{}, errors.Join(err, serr)
		}
		return BinaryUpdated{}, err
	}
	steps := []struct {
		name, phase string
		run         func() error
	}{
		{"writing the new binary", "", func() error { return env.Files.WriteFile(ctx, ID, server.BinaryNewPath, bin, 0o755) }},
		{"keeping the old binary", "kept", func() error { return env.Files.Rename(ctx, ID, server.BinaryPath, server.BinaryPrevPath) }},
		{"swapping the new binary in", "swapped", func() error { return env.Files.Rename(ctx, ID, server.BinaryNewPath, server.BinaryPath) }},
		{"restarting derper", "restarted", func() error { return env.Units.Restart(ctx, server.UnitName) }},
		{"the health check", "", func() error {
			return h.await(ctx, env, func(ctx context.Context) error { return h.healthy(ctx, env, row) })
		}},
		{"recording the new binary", "", func() error { return setLocalBinary(ctx, env.DB, in.Version, sha, env.Clock.Now()) }},
	}
	for _, s := range steps {
		var err error
		if s.phase != "" {
			err = setUpdatePhase(ctx, env.DB, env.ChangeID, s.phase, "", env.Clock.Now())
		}
		if err == nil {
			err = s.run()
		}
		if err != nil {
			return BinaryUpdated{}, h.fail(ctx, env, row.DerperVersion, s.name, err)
		}
	}
	if err := setUpdatePhase(ctx, env.DB, env.ChangeID, "done", "", env.Clock.Now()); err != nil {
		return BinaryUpdated{}, err
	}
	if err := env.Effects.Made(ctx, seq, binaryEffectResource, after); err != nil {
		return BinaryUpdated{}, err
	}
	return BinaryUpdated{From: row.DerperVersion, To: in.Version}, nil
}

// fail rolls back an update whose step failed with cause and returns the update's derp.verify_failed error. It is
// never Definite: the host changed before the step failed.
func (h binaryUpdateHandler) fail(ctx context.Context, env core.Env, from, step string, cause error) error {
	// The step may have failed because ctx ended, and the previous binary must still be restored.
	ctx, stop := binaryBound(context.WithoutCancel(ctx), env, binaryRollbackWindow)
	defer stop()
	u, ok, err := loadUpdate(ctx, env.DB.Read(), env.ChangeID)
	if err == nil && !ok {
		err = fmt.Errorf("derp: update %s is not recorded", env.ChangeID)
	}
	if err == nil {
		err = h.rollback(ctx, env, u)
	}
	if err != nil {
		return errcode.Wrap(errcode.DERPVerifyFailed, cause, fmt.Sprintf("%s failed (%s) and the rollback to %s failed (%s); derper needs repair",
			step, binaryFirstLine(cause), from, binaryFirstLine(err)))
	}
	return errcode.Wrap(errcode.DERPVerifyFailed, cause, fmt.Sprintf("%s failed (%s); derper is back at %s", step, binaryFirstLine(cause), from))
}

// healthy is one look at the updated server: its unit is active, the TLS probe and STUN answer on this host, and
// tailscaled answers.
func (h binaryUpdateHandler) healthy(ctx context.Context, env core.Env, row localServer) error {
	if err := h.unitActive(ctx, env); err != nil {
		return err
	}
	target := probeTarget{HostName: row.HostName, Address: "127.0.0.1", Port: server.HTTPSPort, CertName: row.CertName}
	if _, err := h.d.d.tlsProbe(ctx, target); err != nil {
		return fmt.Errorf("derp: the TLS probe of /derp/probe failed: %w", err)
	}
	if _, err := h.d.d.stun(ctx, netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), server.STUNPort)); err != nil {
		return fmt.Errorf("derp: the STUN probe failed: %w", err)
	}
	if _, err := h.d.d.local().Status(ctx); err != nil {
		return fmt.Errorf("derp: tailscaled does not answer: %w", err)
	}
	return nil
}

// unitActive fails unless derper's unit is active.
func (binaryUpdateHandler) unitActive(ctx context.Context, env core.Env) error {
	st, err := env.Units.State(ctx, server.UnitName)
	if err != nil {
		return fmt.Errorf("derp: read the state of %s: %w", server.UnitName, err)
	}
	if st.ActiveState != "active" {
		return fmt.Errorf("derp: %s is %s, not active", server.UnitName, st.ActiveState)
	}
	return nil
}

// await runs probe at once and then every binaryUpdatePoll on env.Clock until it succeeds, and fails with the last
// probe's error once healthWindow has passed since the first. Each probe is bounded by the time left in the window,
// and by at least one poll, so a probe that hangs ends with the window; it is also bounded to binaryProbeWait, so a
// lost STUN answer costs one probe and not the window.
func (binaryUpdateHandler) await(ctx context.Context, env core.Env, probe func(ctx context.Context) error) error {
	deadline := env.Clock.Now().Add(healthWindow)
	for {
		// The bound is real time: it limits one call to a socket, while env.Clock measures the wait between probes.
		pctx, cancel := context.WithTimeout(ctx, min(max(deadline.Sub(env.Clock.Now()), binaryUpdatePoll), binaryProbeWait))
		err := probe(pctx)
		cancel()
		if err == nil {
			return nil
		}
		if !env.Clock.Now().Before(deadline) {
			return err
		}
		tick := make(chan struct{})
		timer := env.Clock.AfterFunc(binaryUpdatePoll, func() { close(tick) })
		select {
		case <-tick:
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("derp: the wait for derper ended: %w; the last look: %w", ctx.Err(), err)
		}
	}
}

// rollback restores the previous binary of update u from the row alone, with no controller; u.Phase is the phase
// it was entered in. It ends in phase rolled_back with the update's effect skipped, or in phase failed with the
// error of the first failing step, which it returns.
func (h binaryUpdateHandler) rollback(ctx context.Context, env core.Env, u updateRow) error {
	phase := func(phase, result string) error {
		return binaryRecord(ctx, func(ctx context.Context) error {
			return setUpdatePhase(ctx, env.DB, u.ChangeID, phase, result, env.Clock.Now())
		})
	}
	if err := phase("rolling_back", ""); err != nil {
		return err
	}
	if err := h.restore(ctx, env, u); err != nil {
		reason := []rune(binaryFirstLine(err))
		if len(reason) > 200 {
			reason = reason[:200]
		}
		if perr := phase("failed", string(reason)); perr != nil {
			return errors.Join(err, perr)
		}
		return err
	}
	if err := phase("rolled_back", ""); err != nil {
		return err
	}
	return binaryRecord(ctx, func(ctx context.Context) error {
		effects, err := env.Effects.List(ctx)
		if err != nil {
			return err
		}
		for _, e := range effects {
			if e.Resource == binaryEffectResource && e.State == core.EffectIntended {
				if err := env.Effects.Skipped(ctx, e.Seq); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// restore puts the previous binary back in place, records it, restarts derper and waits for its unit, unless the
// update only staged the new binary; in every phase it then removes a staged derper.new. It runs in every phase but
// staged, including done, which is the undo of a finished update that Inverse runs, and a binary already back in
// place is left alone.
func (h binaryUpdateHandler) restore(ctx context.Context, env core.Env, u updateRow) error {
	if u.Phase != "staged" {
		current, err := h.fileSHA256(server.BinaryPath)
		if err != nil {
			return err
		}
		// A binary already back in place is left alone, so a derper.prev an earlier update left never replaces it.
		if current != u.FromSHA256 {
			prev, err := h.fileSHA256(server.BinaryPrevPath)
			if err != nil {
				return err
			}
			if prev != u.FromSHA256 {
				return errors.New("derp: derper.prev does not hold the previous binary")
			}
			if err := env.Files.Rename(ctx, ID, server.BinaryPrevPath, server.BinaryPath); err != nil {
				return fmt.Errorf("derp: restore %s: %w", server.BinaryPrevPath, err)
			}
		}
		if err := setLocalBinary(ctx, env.DB, u.FromVersion, u.FromSHA256, env.Clock.Now()); err != nil {
			return err
		}
		if err := env.Units.Restart(ctx, server.UnitName); err != nil {
			return fmt.Errorf("derp: restart %s: %w", server.UnitName, err)
		}
		if err := h.await(ctx, env, func(ctx context.Context) error { return h.unitActive(ctx, env) }); err != nil {
			return err
		}
	}
	_, err := h.d.d.readFile(server.BinaryNewPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("derp: read %s: %w", server.BinaryNewPath, err)
	}
	if err := env.Files.Remove(ctx, ID, server.BinaryNewPath); err != nil {
		return fmt.Errorf("derp: remove %s: %w", server.BinaryNewPath, err)
	}
	return nil
}

// fileSHA256 is the hex SHA-256 of the file at path; a missing file hashes as "".
func (h binaryUpdateHandler) fileSHA256(path string) (string, error) {
	b, err := h.d.d.readFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("derp: read %s: %w", path, err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Verify checks the host from the input alone, as recovery calls it without an output: the server is recorded at
// the input's version and derper's unit is active.
func (h binaryUpdateHandler) Verify(ctx context.Context, env core.Env, in BinaryUpdateInput, _ BinaryUpdated) error {
	row, ok, err := loadLocalServer(ctx, env.DB.Read())
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("derp: no DERP server is recorded on this host")
	}
	if row.DerperVersion != in.Version {
		return fmt.Errorf("derp: the recorded server runs derper %s, not %s", row.DerperVersion, in.Version)
	}
	return h.unitActive(ctx, env)
}

// binaryFirstLine is the first line of err's message.
func binaryFirstLine(err error) string {
	line, _, _ := strings.Cut(err.Error(), "\n")
	return line
}
