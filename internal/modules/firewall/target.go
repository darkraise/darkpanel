package firewall

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

// change is what one mutating action asks of UFW, computed from its input
// and the configured state.
type change struct {
	ops     []ufwOp   // rule, default and logging commands, in order
	enable  *bool     // the UFW state the action sets; nil keeps it
	restore *snapshot // a snapshot whose files, UFW state and sysctl values the action brings back
	repair  bool      // from the actual loaded state to the target (spec 2.3, the repair path)
}

// target is where a change ends.
type target struct {
	cfgN  fwstate.Config     // after the change's own commands (a restore: the snapshot's configuration)
	final fwstate.Config     // after the transition commands too
	tr    fwstate.Transition // the transition the change causes
	cmds  [][]string         // the transition commands Apply runs after the change's own, in order
	pred  fwstate.Loaded     // the loaded state it predicts, with the sysctl values
}

// The transition commands.
var (
	enableArgs  = []string{"--force", "enable"}
	disableArgs = []string{"disable"}
	reloadArgs  = []string{"reload"}
)

// predictTarget predicts change c from the configuration cfg0 and the
// loaded state cur (spec 2.3), in the predictor's order: each command
// edits the configuration; then the IPv6 refusal and the prediction of the
// transition use the configuration from before the transition's own
// command, whose edit gives the final configuration. The transition
// follows UFW's state before and after: a start, a stop, a reload when the
// commands, a restore or a repair change an active UFW, or none. After
// "ufw --force enable", Apply also reloads when UFW never started (its
// first start loads the package's template files, which it rewrites only
// afterwards) and when UFW's chains are loaded already (ufw-init then loads
// nothing). The sysctl values are the prediction of UFW's sysctl file for
// the transition, with a restore's values over them for every key that
// still exists.
func (f fw) predictTarget(cfg0 fwstate.Config, cur fwstate.Loaded, c change) (target, error) {
	tg := target{cfgN: cfg0}
	for _, op := range c.ops {
		next, err := f.d.edit(tg.cfgN, op.Args)
		if err != nil {
			return target{}, errcode.New(errcode.InvalidInput,
				fmt.Sprintf("Darkpanel cannot predict ufw %s: %v", strings.Join(op.Args, " "), err))
		}
		tg.cfgN = next
	}
	want := tg.cfgN.Enabled
	var restored map[string]string
	if c.restore != nil {
		cfgS, err := f.snapshotConfig(*c.restore)
		if err != nil {
			return target{}, err
		}
		tg.cfgN, want, restored = cfgS, c.restore.State.Enabled, c.restore.State.Sysctl
	}
	if c.enable != nil {
		want = *c.enable
	}
	sysctlTr := fwstate.TransNone
	var err error
	switch running := ufwRunning(cur); {
	case want && !cfg0.Enabled:
		tg.tr, sysctlTr = fwstate.TransEnable, fwstate.TransEnable
		err = f.start(&tg, cur, running)
	case !want && (cfg0.Enabled || (c.repair && running)):
		tg.tr, sysctlTr = fwstate.TransDisable, fwstate.TransDisable
		err = f.transition(&tg, cur, fwstate.TransDisable, disableArgs)
	case want && (len(c.ops) > 0 || c.restore != nil || c.repair):
		tg.tr, sysctlTr = fwstate.TransReload, fwstate.TransReload
		err = f.transition(&tg, cur, fwstate.TransReload, reloadArgs)
	default:
		tg.tr, tg.final = fwstate.TransNone, tg.cfgN
		tg.pred, err = f.d.predict(tg.cfgN, cur, fwstate.TransNone)
	}
	if err != nil {
		return target{}, err
	}
	sysctl, err := f.d.sysctl(tg.final, cur.Sysctl, sysctlTr)
	if err != nil {
		return target{}, err
	}
	tg.pred.Sysctl = maps.Clone(sysctl)
	for k, v := range restored {
		if _, ok := tg.pred.Sysctl[k]; ok {
			tg.pred.Sysctl[k] = v
		}
	}
	return tg, nil
}

// transition predicts a reload or a stop: the IPv6 refusal, the predicted
// loaded state from the configuration before the command, and the command's
// edit.
func (f fw) transition(tg *target, cur fwstate.Loaded, tr fwstate.Transition, args []string) error {
	if err := f.ipv6Refusal(tg.cfgN, cur, tr); err != nil {
		return err
	}
	pred, err := f.d.predict(tg.cfgN, cur, tr)
	if err != nil {
		return fmt.Errorf("firewall: predict ufw %s: %w", strings.Join(args, " "), err)
	}
	final, err := f.edit(tg.cfgN, args)
	if err != nil {
		return err
	}
	tg.pred, tg.final, tg.cmds = pred, final, append(tg.cmds, args)
	return nil
}

// start predicts "ufw --force enable" and the reload that may follow it.
func (f fw) start(tg *target, cur fwstate.Loaded, running bool) error {
	enabled, err := f.edit(tg.cfgN, enableArgs)
	if err != nil {
		return err
	}
	if running {
		cfgN := tg.cfgN
		tg.cfgN = enabled
		err := f.transition(tg, cur, fwstate.TransReload, reloadArgs)
		tg.cfgN, tg.cmds = cfgN, append([][]string{enableArgs}, tg.cmds...)
		return err
	}
	if err := f.ipv6Refusal(tg.cfgN, cur, fwstate.TransEnable); err != nil {
		return err
	}
	pred, err := f.d.predict(tg.cfgN, cur, fwstate.TransEnable)
	if err != nil {
		return fmt.Errorf("firewall: predict ufw --force enable: %w", err)
	}
	tg.pred, tg.final, tg.cmds = pred, enabled, [][]string{enableArgs}
	if !ufwNeverStarted(cur) {
		return nil
	}
	if tg.pred, err = f.d.predict(enabled, pred, fwstate.TransReload); err != nil {
		return fmt.Errorf("firewall: predict ufw reload: %w", err)
	}
	if tg.final, err = f.edit(enabled, reloadArgs); err != nil {
		return err
	}
	tg.cmds = append(tg.cmds, reloadArgs)
	return nil
}

// edit predicts a transition command's edit of the configuration.
func (f fw) edit(cfg fwstate.Config, args []string) (fwstate.Config, error) {
	next, err := f.d.edit(cfg, args)
	if err != nil {
		return fwstate.Config{}, fmt.Errorf("firewall: predict ufw %s: %w", strings.Join(args, " "), err)
	}
	return next, nil
}

// snapshotConfig reads a snapshot's configuration: its files, written
// under a temporary root, read as the host's are.
func (f fw) snapshotConfig(s snapshot) (fwstate.Config, error) {
	root, err := os.MkdirTemp("", "dp-fw-snapshot")
	if err != nil {
		return fwstate.Config{}, fmt.Errorf("firewall: snapshot %s: %w", s.ID, err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	for _, e := range s.Entries {
		if !e.Present {
			continue
		}
		full := filepath.Join(root, e.Path)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			return fwstate.Config{}, fmt.Errorf("firewall: snapshot %s: %w", s.ID, err)
		}
		if err := os.WriteFile(full, e.Data, 0o600); err != nil {
			return fwstate.Config{}, fmt.Errorf("firewall: snapshot %s: %w", s.ID, err)
		}
	}
	cfg, err := f.d.readConfig(root)
	if err != nil {
		return fwstate.Config{}, fmt.Errorf("firewall: snapshot %s: %w", s.ID, err)
	}
	cfg.Files = maps.Clone(cfg.Files)
	for _, e := range s.Entries {
		cfg.Files[e.Path] = e
	}
	return cfg, nil
}
