package firewall

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/guard"
)

// The declaration every mutating firewall action shares (section 5).
const (
	lockKey      = ID
	repairAction = ID + ".state.repair"
	// applyTimeout bounds apply and verify: a batch runs ufw once per
	// rule and then reloads UFW, each run taking up to seconds.
	applyTimeout = 45 * time.Second
)

// ChangeOutput is the output of every mutating firewall action (v1).
type ChangeOutput struct {
	Snapshot      string   `json:"snapshot"`                // the logical snapshot taken before the change
	Rules         []string `json:"rules,omitempty"`         // the ids of the Darkpanel rules the change added
	RepairOutcome string   `json:"repairOutcome,omitempty"` // firewall.state.repair of a change: restored or kept
}

// mutation is the handler of one mutating firewall action (spec 2.3-2.5):
// Plan and Apply share the plan pipeline, Verify compares the loaded state
// with the configuration's prediction, and Snapshot, Revert and Observe are
// the snapshot part's.
type mutation[In any] struct {
	f fw
	// image makes the snapshot hold a loaded-state before-image
	// (firewall.state.repair, spec 2.3).
	image   bool
	guarded func(in In) core.Guarded
	manual  func(in In) bool // nil: never
	build   func(ctx context.Context, env core.Env, in In) buildFunc
	// check is the action's own verify on the configuration after the
	// change, run before the comparison; nil means none.
	check func(ctx context.Context, env core.Env, in In, cfg fwstate.Config) error
	// finish fills the action's own output fields after a successful
	// apply; nil means none.
	finish func(ctx context.Context, env core.Env, in In, out *ChangeOutput) error
	// keep names a snapshot the apply reads; nil means none.
	keep func(in In) string
}

// defineMutation declares a mutating firewall action from spec's Name,
// Summary and Converges: revertible, agent-side, under the firewall lock,
// repaired by firewall.state.repair, with the overridable lockout guard.
func defineMutation[In any](spec core.ActionSpec, m mutation[In]) core.ActionDef {
	spec.Version, spec.Side = 1, core.SideAgent
	spec.LockKeys, spec.RepairedBy, spec.ApplyTimeout = []string{lockKey}, repairAction, applyTimeout
	spec.Guards = []core.GuardSpec{{ID: guard.GuardID, Overridable: true}}
	return core.DefineRevertible[In, ChangeOutput](spec, m)
}

// Plan plans the change (fw.plan).
func (m mutation[In]) Plan(ctx context.Context, env core.Env, in In) (core.PlanOutcome, error) {
	req := planReq{input: in, guarded: m.guarded(in), build: m.build(ctx, env, in)}
	if m.manual != nil {
		req.manual = m.manual(in)
	}
	return m.f.plan(ctx, env, req)
}

// Snapshot captures the logical snapshot with the plan's guard context. The
// engine calls it before it records the change, and the capture prunes by
// retention counting its own new row, so a snapshot the apply reads (keep)
// is pinned for the capture; otherwise restoring the oldest retained
// snapshot would delete it.
func (m mutation[In]) Snapshot(ctx context.Context, env core.Env, in In) ([]byte, error) {
	if m.keep != nil {
		id := m.keep(in)
		s, err := getSnapshot(ctx, env.DB, id)
		if err != nil {
			return nil, err
		}
		if !s.Pinned {
			if err := setPinned(ctx, env.DB, id, true); err != nil {
				return nil, err
			}
			defer func() {
				if err := setPinned(context.WithoutCancel(ctx), env.DB, id, false); err != nil {
					env.Log.Warn("firewall: cannot unpin the snapshot a change restores", "snapshot", id, "error", err)
				}
			}()
		}
	}
	return capture(ctx, env, m.f.d, m.guarded(in).Guard, m.image)
}

// Apply attaches the snapshot, makes the change as journaled effects
// (fw.run) and records the post-apply file hashes.
func (m mutation[In]) Apply(ctx context.Context, env core.Env, in In) (ChangeOutput, error) {
	id, err := attach(ctx, env)
	if err != nil {
		return ChangeOutput{}, err
	}
	p, err := m.f.prepare(ctx, env, m.build(ctx, env, in))
	if err != nil {
		return ChangeOutput{}, err
	}
	if err := m.f.run(ctx, env, p); err != nil {
		return ChangeOutput{}, err
	}
	if err := recordPostHashes(ctx, env, m.f.d, id); err != nil {
		return ChangeOutput{}, err
	}
	out := ChangeOutput{Snapshot: id}
	if m.finish != nil {
		if err := m.finish(ctx, env, in, &out); err != nil {
			return ChangeOutput{}, err
		}
	}
	return out, nil
}

// Verify runs the action's own check, then compares the loaded state with
// what the configuration now predicts, over UFW's and Darkpanel's chains
// and the built-in chains of every table (fw.verifyDiffs), and the sysctl
// values the change's transition sets with the live ones
// (fw.verifySysctl): a difference means UFW or the kernel did not do what
// the prediction said (firewall.prediction_mismatch, which reverts). It
// reads only the host, the change's journal and snapshot, so it also serves
// startup recovery.
func (m mutation[In]) Verify(ctx context.Context, env core.Env, in In, _ ChangeOutput) error {
	cfg, err := m.f.d.readConfig(m.f.d.root)
	if err != nil {
		return fmt.Errorf("firewall: verify: %w", err)
	}
	if m.check != nil {
		if err := m.check(ctx, env, in, cfg); err != nil {
			return err
		}
	}
	st, err := m.f.d.readLoaded(ctx, env)
	if err != nil {
		return fmt.Errorf("firewall: verify: %w", err)
	}
	diffs, err := m.f.verifyDiffs(cfg, st)
	if err != nil {
		return fmt.Errorf("firewall: verify: %w", err)
	}
	if len(diffs) > 0 {
		texts := make([]string, len(diffs))
		for i, d := range diffs {
			texts[i] = chainDiffText(d)
		}
		return errcode.New(errcode.FirewallPredictionMismatch, "the loaded state differs from the prediction: "+strings.Join(texts, "; "))
	}
	return m.f.verifySysctl(ctx, env, cfg, st.Sysctl)
}

// verifySysctl compares the live sysctl values live with the ones the
// change's transition sets (spec 2.1, 2.3), predicted from the values in
// the change's snapshot, which were live before the change. A change whose
// journal shows no start of UFW and no sysctl write predicts none.
func (f fw) verifySysctl(ctx context.Context, env core.Env, cfg fwstate.Config, live map[string]string) error {
	effects, err := env.Effects.List(ctx)
	if err != nil {
		return fmt.Errorf("firewall: verify: read the change's effects: %w", err)
	}
	rec, err := sysctlRecordOf(effects)
	if err != nil {
		return fmt.Errorf("firewall: verify: %w", err)
	}
	if !rec.started && len(rec.written) == 0 {
		return nil
	}
	id, err := snapshotOfChange(ctx, env.DB, env.ChangeID)
	if err != nil {
		return fmt.Errorf("firewall: verify: %w", err)
	}
	snap, err := getSnapshot(ctx, env.DB, id)
	if err != nil {
		return fmt.Errorf("firewall: verify: %w", err)
	}
	want, err := f.expectedSysctl(cfg, snap.State.Sysctl, rec)
	if err != nil {
		return fmt.Errorf("firewall: verify: %w", err)
	}
	if bad := sysctlMismatches(want, live); len(bad) > 0 {
		return mismatch("the loaded sysctl values differ from the prediction: %s", strings.Join(bad, "; "))
	}
	return nil
}

// Revert reverts the change from its snapshot (revert).
func (m mutation[In]) Revert(ctx context.Context, env core.Env, snapshot []byte) (core.RevertOutcome, error) {
	return revert(ctx, env, m.f.d, snapshot)
}

// Observe reads the resource of one effect (observe).
func (m mutation[In]) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	return observe(ctx, env, m.f.d, e)
}

// run makes a prepared change, each step a journaled effect (spec 2.5),
// stopping at the first failure: a restore's files, the change's ufw
// commands, the transition commands, and a restore's sysctl values.
func (f fw) run(ctx context.Context, env core.Env, p prepared) error {
	if p.c.restore != nil {
		if err := f.writeRestored(ctx, env, p); err != nil {
			return err
		}
	}
	for _, op := range p.c.ops {
		if err := runUfw(ctx, env, f.d, op); err != nil {
			return err
		}
	}
	for _, args := range p.tg.cmds {
		if err := runUfw(ctx, env, f.d, transitionOp(args)); err != nil {
			return err
		}
	}
	if p.c.restore != nil {
		return f.restoreSysctl(ctx, env, p.c.restore.State.Sysctl)
	}
	return nil
}

// transitionOp is the effect of a transition command: UFW's enabled state.
func transitionOp(args []string) ufwOp {
	switch {
	case slices.Equal(args, enableArgs):
		return enableOp(true)
	case slices.Equal(args, disableArgs):
		return enableOp(false)
	}
	return ufwOp{Kind: core.EffectModify, Resource: resourceEnabled, Args: args}
}

// writeRestored writes each file of a restore's snapshot that differs from
// the host's, as one effect each. Each file is written once, before any ufw
// command rewrites it, so its effect's before-state is the change's own
// snapshot's entry, which the inverse restores. When the restore changes
// UFW's enabled state, ufw.conf is written with the current ENABLED value
// (as UFW edits it), and the enable or disable that follows sets it, as a
// separate effect whose inverse sets it back.
func (f fw) writeRestored(ctx context.Context, env core.Env, p prepared) error {
	snap := p.c.restore
	for _, e := range snap.Entries {
		want := e
		if e.Path == "/etc/ufw/ufw.conf" && snap.State.Enabled != p.cfg0.Enabled {
			args := disableArgs
			if !snap.State.Enabled {
				args = enableArgs
			}
			cfg, err := f.edit(p.tg.cfgN, args)
			if err != nil {
				return err
			}
			want.Data = cfg.Files[e.Path].Data
		}
		cur, err := f.d.readConfig(f.d.root)
		if err != nil {
			return fmt.Errorf("firewall: restore %s: %w", e.Path, err)
		}
		if sameFile(cur.Files[e.Path], want) {
			continue
		}
		if err := writeFileEffect(ctx, env, f.d, want); err != nil {
			return err
		}
	}
	return nil
}

// restoreSysctl writes back each of a snapshot's sysctl values that
// differs from the live one, as one effect each, in the order of spec 2.1
// (net.ipv4.ip_forward, all, default, each interface). A key the kernel no
// longer has (an interface that has gone, or a protocol the host booted
// without) cannot be written: it is skipped and logged.
func (f fw) restoreSysctl(ctx context.Context, env core.Env, want map[string]string) error {
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int {
		if ra, rb := sysctlRank(a), sysctlRank(b); ra != rb {
			return ra - rb
		}
		return strings.Compare(a, b)
	})
	var vanished []string
	for _, k := range keys {
		live, err := env.Files.ReadSysctl(ctx, ID, k)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			vanished = append(vanished, k)
			continue
		case err != nil:
			return fmt.Errorf("firewall: read sysctl %s: %w", k, err)
		case live == want[k]:
			continue
		}
		if err := writeSysctlEffect(ctx, env, f.d, k, want[k]); err != nil {
			return err
		}
	}
	logVanished(env, vanished)
	return nil
}
