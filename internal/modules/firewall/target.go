package firewall

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// change is what one mutating action asks of UFW, computed from its input
// and the configured state.
type change struct {
	ops     []ufwOp                  // rule, default and logging commands, in order
	enable  *bool                    // the UFW state the action sets; nil keeps it
	restore *snapshot                // a snapshot whose files, UFW state and sysctl values the action brings back
	repair  bool                     // from the actual loaded state to the target (spec 2.3, the repair path)
	files   map[string][]byte        // whole after.rules / after6.rules contents to write before the ufw commands
	flag    *bool                    // the Docker integration flag to store; nil keeps it
	sources *dockerfw.Settings       // settings to store; nil keeps them
	rowsAdd []dockerfw.ContainerRule // container rules to insert
	rowsDel []string                 // container rule ids to delete
	cleanup bool                     // run the owned-chain cleanup before the transition commands
	reload  bool                     // the change needs `ufw reload` when UFW stays active (block files changed)
	notes   []core.DiffLine          // extra plan lines a builder adds
	// discardForeign is true only for a change that discards the foreign
	// DOCKER-USER rules on purpose (firewall.docker.repair with
	// DiscardForeign and Manual both set); prepare then skips
	// dockerUserInUse.
	discardForeign bool
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
//
// The after files a change writes replace the configured ones, also over a
// restore's configuration, since Apply writes them after a restore's files
// and before the ufw commands. Writing them changes nothing loaded until
// UFW reloads, so only reload, never files alone, makes a change to an
// active UFW a reload. The owned-chain cleanup runs before the transition
// commands, so every prediction starts from the loaded state without
// Darkpanel's chains, which a reload alone leaves loaded.
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
	if len(c.files) > 0 {
		cfgF, err := overlayBlockFiles(tg.cfgN, c.files)
		if err != nil {
			return target{}, err
		}
		tg.cfgN = cfgF
	}
	if c.enable != nil {
		want = *c.enable
	}
	if c.cleanup {
		cur = predictChainCleanup(cur)
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
	case want && (len(c.ops) > 0 || c.restore != nil || c.repair || c.reload):
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

// overlayBlockFiles returns cfg with the after files a Docker change writes
// in place of the configured ones, each keeping its mode and owner as
// predict.Edit's own writes do. cfg itself is left unchanged.
func overlayBlockFiles(cfg fwstate.Config, files map[string][]byte) (fwstate.Config, error) {
	out := cfg
	out.Files = maps.Clone(cfg.Files)
	for _, p := range slices.Sorted(maps.Keys(files)) {
		if p != "/etc/ufw/after.rules" && p != "/etc/ufw/after6.rules" {
			return fwstate.Config{}, fmt.Errorf("firewall: %s is not a file a Docker change writes", p)
		}
		e, ok := out.Files[p]
		if !ok || !e.Present {
			return fwstate.Config{}, fmt.Errorf("firewall: %s is missing", p)
		}
		e.Data = files[p]
		out.Files[p] = e
	}
	return out, nil
}

// predictChainCleanup predicts the owned-chain cleanup on cur: in each
// family's filter table every darkpanel-* and dp-b-* chain is gone and
// DOCKER-USER holds only -j RETURN, declared when it was not loaded, as the
// cleanup's restore input declares it. Nothing cur holds is modified.
func predictChainCleanup(cur fwstate.Loaded) fwstate.Loaded {
	parsed, err := netfilter.ParseRestore(strings.NewReader("*filter\n:DOCKER-USER - [0:0]\n-A DOCKER-USER -j RETURN\nCOMMIT\n"))
	if err != nil {
		panic(fmt.Sprintf("firewall: parse the cleanup's DOCKER-USER rule: %v", err))
	}
	ret := chainOf(parsed, "filter", "DOCKER-USER").Rules[0]
	clean := func(rs *netfilter.Ruleset) *netfilter.Ruleset {
		if rs == nil || rs.Table("filter") == nil {
			return rs
		}
		out := *rs
		out.Tables = slices.Clone(rs.Tables)
		filter := out.Table("filter")
		chains := make([]netfilter.Chain, 0, len(filter.Chains)+1)
		declared := false
		for _, c := range filter.Chains {
			switch {
			case strings.HasPrefix(c.Name, "darkpanel-"), strings.HasPrefix(c.Name, "dp-b-"):
				continue
			case c.Name == "DOCKER-USER":
				c.Rules, declared = []netfilter.Rule{ret}, true
			}
			chains = append(chains, c)
		}
		if !declared {
			chains = append(chains, netfilter.Chain{Name: "DOCKER-USER", Rules: []netfilter.Rule{ret}})
		}
		filter.Chains = chains
		return &out
	}
	cur.V4, cur.V6 = clean(cur.V4), clean(cur.V6)
	return cur
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
		if e.Path != ufwSysctlFile && !slices.Contains(fwstate.StateFiles, e.Path) {
			return fwstate.Config{}, fmt.Errorf("firewall: snapshot %s holds %q, which is not a UFW state file", s.ID, e.Path)
		}
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
