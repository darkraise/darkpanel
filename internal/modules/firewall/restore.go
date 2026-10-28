package firewall

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/conf"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/predict"
)

// restoreReport is what a restore did besides restoring.
type restoreReport struct {
	Transition fwstate.Transition
	Vanished   []string   // per-interface sysctl keys not written back: the interface has gone (spec 2.1)
	Excluded   []chainRef // before-image chains left as they are (spec 2.3)
}

// chooseTransition is the UFW transition that takes UFW from its current
// enabled state to the snapshot's (spec 2.5). "ufw reload" on an inactive
// UFW only prints that it skips, so the transition is chosen, never
// assumed.
func chooseTransition(snapEnabled, curEnabled bool) fwstate.Transition {
	switch {
	case snapEnabled && curEnabled:
		return fwstate.TransReload
	case snapEnabled:
		return fwstate.TransEnable
	case curEnabled:
		return fwstate.TransDisable
	}
	return fwstate.TransNone
}

// activeNow reports whether UFW is active, for choosing the transition of a
// restore that has no record yet. The configuration says so, unless a
// record of another snapshot's restore is still there: that restore was cut
// off, its files may be half written while UFW still runs as before, and
// the new restore replaces its record, which held the transition. Then the
// loaded chains say. A record or chains it cannot read are an error, never
// the configuration's word, because the caller records the transition it
// chooses and a retry reuses it.
func activeNow(ctx context.Context, env core.Env, d deps, cur fwstate.Config) (bool, error) {
	_, other, err := pendingRestore(ctx, env.DB)
	if err != nil {
		return false, err
	}
	if !other {
		return cur.Enabled, nil
	}
	st, err := d.readLoaded(ctx, env)
	if err != nil {
		return false, fmt.Errorf("firewall: restore: %w", err)
	}
	return ufwRunning(st), nil
}

// transitionArgs is the ufw vector of a transition; nil for none.
func transitionArgs(tr fwstate.Transition) []string {
	switch tr {
	case fwstate.TransReload:
		return []string{"reload"}
	case fwstate.TransEnable:
		return enableOp(true).Args
	case fwstate.TransDisable:
		return enableOp(false).Args
	}
	return nil
}

// restoreKeep selects the chains a restore verifies against the
// prediction: UFW's and Darkpanel's chains, and the filter table's built-in
// chains, which hold UFW's jumps and policies.
func restoreKeep(_ fwstate.Family, table, chain string) bool {
	return firewallChain(chain) || (table == "filter" && slices.Contains(builtinChains, chain))
}

// restore restores snapshot snap whole (spec 2.5). It records the restore
// with its UFW transition before the first write, chosen from the
// snapshot's ENABLED and the current one (when the current configuration
// cannot be read, an explicit enable or disable), so that a restore cut off
// by a crash, run again, finishes the same way. Then it writes every file
// with FileWriter.Restore and runs the transition, or, for a repair
// snapshot, loads its loaded-state before-image with *-restore -n instead
// (spec 2.3); writes the sysctl values back (spec 2.1); verifies ENABLED and
// the files, the loaded chains against d.predict of the restored
// configuration (or the before-image) and every sysctl value; and clears
// the record. Built-in chains the before-image had to leave as they are
// make it fail after the rest is restored and verified.
func restore(ctx context.Context, env core.Env, d deps, snap snapshot) (restoreReport, error) {
	return restoreOver(ctx, env, d, snap, nil)
}

// restoreOver is restore for the connectivity exception when emergency is
// not nil: the record of what the restore overwrites is kept with the
// restore record, from before the first write, so that finishing a cut-off
// restore still ends as an override.
func restoreOver(ctx context.Context, env core.Env, d deps, snap snapshot, emergency json.RawMessage) (restoreReport, error) {
	var rep restoreReport
	rec, recorded, err := restoreRecordOf(ctx, env.DB, snap.ID)
	if err != nil {
		return rep, err
	}
	tr := rec.Transition
	switch {
	case recorded && emergency != nil:
		if err := markEmergency(ctx, env.DB, snap.ID, emergency); err != nil {
			return rep, err
		}
	case !recorded:
		switch cur, err := d.readConfig(d.root); {
		case snap.State.Image != nil:
			tr = fwstate.TransNone
		case err != nil && snap.State.Enabled:
			tr = fwstate.TransEnable
		case err != nil:
			tr = fwstate.TransDisable
		default:
			active, err := activeNow(ctx, env, d, cur)
			if err != nil {
				return rep, err
			}
			tr = chooseTransition(snap.State.Enabled, active)
		}
		if err := beginRestoreRecord(ctx, env.DB, snap.ID, restoreRecord{Transition: tr, Emergency: emergency}, env.Clock.Now()); err != nil {
			return rep, err
		}
	}
	rep.Transition = tr
	before, err := d.readLoaded(ctx, env)
	if err != nil {
		return rep, fmt.Errorf("firewall: restore: %w", err)
	}
	if snap.State.Image == nil {
		cfgS, err := snapshotStart(snap)
		if err != nil {
			return rep, err
		}
		// An IPV6=no start replaces the whole IPv6 filter table, which no
		// snapshot can rebuild (spec 2.3); not even the connectivity
		// exception overrides this refusal. It is decided before the first
		// write, so a refused restore leaves no IPV6=no on disk for the next
		// reload or boot to start from.
		if err := predict.IPv6Refusal(cfgS, before, tr); err != nil {
			return rep, err
		}
	}
	for _, e := range snap.Entries {
		if err := env.Files.Restore(ctx, ID, e); err != nil {
			return rep, fmt.Errorf("firewall: restore %s: %w", e.Path, err)
		}
	}
	var plan imagePlan
	if img := snap.State.Image; img != nil {
		plan, err = loadImage(ctx, env, img, before)
		rep.Excluded = plan.Excluded
		if err != nil {
			return rep, err
		}
	} else if args := transitionArgs(tr); args != nil {
		if res, err := env.Exec.Run(ctx, core.Cmd{Path: ufwPath, Args: args}); err != nil || res.ExitCode != 0 {
			return rep, ufwFailure(args, res, err)
		}
	}
	after, err := d.readLoaded(ctx, env)
	if err != nil {
		return rep, fmt.Errorf("firewall: restore: %w", err)
	}
	if rep.Vanished, err = writeBackSysctl(ctx, env, snap.State.Sysctl, after.Sysctl); err != nil {
		return rep, err
	}
	if err := verifyRestore(d, snap, tr, before, after, plan); err != nil {
		return rep, err
	}
	if err := endRestore(ctx, env.DB, snap.ID); err != nil {
		return rep, err
	}
	if len(rep.Excluded) > 0 {
		return rep, excludedError(rep.Excluded)
	}
	return rep, nil
}

// loadImage loads before-image img over the loaded state before with
// *-restore -n, as planImage decides. It returns the plan even when a
// program fails.
func loadImage(ctx context.Context, env core.Env, img *beforeImage, before fwstate.Loaded) (imagePlan, error) {
	plan := planImage(img, before)
	for _, f := range []struct {
		fam   fwstate.Family
		input []byte
	}{{fwstate.V4, plan.Restore4}, {fwstate.V6, plan.Restore6}} {
		if f.input == nil {
			continue
		}
		cmd := core.Cmd{Path: restoreProgram(f.fam, before.Variant), Args: []string{"-n"}, Stdin: f.input}
		if res, err := env.Exec.Run(ctx, cmd); err != nil || res.ExitCode != 0 {
			return plan, fmt.Errorf("firewall: load the %s before-image: %w", f.fam, runFailure(cmd, res, err))
		}
	}
	return plan, nil
}

// loadVerifiedImage loads and verifies snap's before-image over the loaded
// state, as the end of a whole restore does. A revert by inverses ends with
// it, because the inverses of a repair's effects cannot take back what its
// reload did to the loaded chains: only the image holds that state.
func loadVerifiedImage(ctx context.Context, env core.Env, d deps, snap snapshot) error {
	before, err := d.readLoaded(ctx, env)
	if err != nil {
		return fmt.Errorf("firewall: load the before-image: %w", err)
	}
	plan, err := loadImage(ctx, env, snap.State.Image, before)
	if err != nil {
		return err
	}
	after, err := d.readLoaded(ctx, env)
	if err != nil {
		return fmt.Errorf("firewall: load the before-image: %w", err)
	}
	if bad := imageMismatch(snap.State.Image, after, plan.Excluded); len(bad) > 0 {
		return fmt.Errorf("firewall: verify the before-image: %s differ from it", joinRefs(bad))
	}
	if len(plan.Excluded) > 0 {
		return excludedError(plan.Excluded)
	}
	return nil
}

func excludedError(excluded []chainRef) error {
	return fmt.Errorf("firewall: the before-image left %s as they are: their rules other than UFW's and Darkpanel's changed since the snapshot, or they jump to a chain that no longer exists",
		joinRefs(excluded))
}

// snapshotStart is what IPv6Refusal reads of the configuration snap
// restores, taken from its entries before anything is written.
func snapshotStart(snap snapshot) (fwstate.Config, error) {
	var defaults, ufwconf []byte
	found := false
	for _, e := range snap.Entries {
		switch {
		case e.Path == "/etc/default/ufw" && e.Present:
			defaults, found = e.Data, true
		case e.Path == "/etc/ufw/ufw.conf" && e.Present:
			ufwconf = e.Data
		}
	}
	if !found {
		return fwstate.Config{}, fmt.Errorf("firewall: restore: snapshot %s holds no /etc/default/ufw", snap.ID)
	}
	return fwstate.Config{Enabled: snap.State.Enabled, IPv6: conf.ConfiguredIPv6(defaults, ufwconf)}, nil
}

func joinRefs(refs []chainRef) string {
	s := make([]string, len(refs))
	for i, r := range refs {
		s[i] = r.String()
	}
	return strings.Join(s, ", ")
}

// runFailure describes a program run that failed.
func runFailure(cmd core.Cmd, res core.CmdResult, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", cmd.Path, err)
	}
	return fmt.Errorf("%s exited %d: %s", cmd.Path, res.ExitCode, strings.TrimSpace(string(res.Stderr)))
}

// verifyRestore checks a restore's result: ENABLED and every file as the
// snapshot holds them, and the loaded chains as d.predict says the
// restored configuration loads with transition tr from before (or, for a
// before-image, as the image holds them, except the chains it left).
func verifyRestore(d deps, snap snapshot, tr fwstate.Transition, before, after fwstate.Loaded, plan imagePlan) error {
	cfg, err := d.readConfig(d.root)
	if err != nil {
		return fmt.Errorf("firewall: verify the restore: %w", err)
	}
	if cfg.Enabled != snap.State.Enabled {
		return fmt.Errorf("firewall: verify the restore: ENABLED is %v, the snapshot's is %v", cfg.Enabled, snap.State.Enabled)
	}
	for _, e := range snap.Entries {
		if got := fileHash(cfg.Files[e.Path]); got != fileHash(e) {
			return fmt.Errorf("firewall: verify the restore: %s is %s, the snapshot's is %s", e.Path, got, fileHash(e))
		}
	}
	if snap.State.Image != nil {
		if bad := imageMismatch(snap.State.Image, after, plan.Excluded); len(bad) > 0 {
			return fmt.Errorf("firewall: verify the restore: %s differ from the before-image", joinRefs(bad))
		}
		return nil
	}
	want, err := d.predict(cfg, before, tr)
	if err != nil {
		return fmt.Errorf("firewall: verify the restore: %w", err)
	}
	if diffs := d.compare(want, after, restoreKeep); len(diffs) > 0 {
		refs := make([]chainRef, len(diffs))
		for i, df := range diffs {
			refs[i] = chainRef{df.Family, df.Table, df.Chain}
		}
		return fmt.Errorf("firewall: verify the restore: %s differ from the prediction", joinRefs(refs))
	}
	return nil
}

// sysctlRank orders sysctl keys for writing back (spec 2.1): writing
// net.ipv4.ip_forward resets IPv4's per-interface values and the all keys
// change every interface, so the global keys go first, then all, then
// default, then each interface's.
func sysctlRank(key string) int {
	switch iface := sysctlIface(key); {
	case key == "net/ipv4/ip_forward":
		return 0
	case iface == "all":
		return 1
	case iface == "default":
		return 2
	}
	return 3
}

// sysctlIface returns the <iface> component of net/ipv4/conf/<iface>/...
// and net/ipv6/conf/<iface>/..., or "".
func sysctlIface(key string) string {
	parts := strings.Split(key, "/")
	if len(parts) == 5 && parts[0] == "net" && (parts[1] == "ipv4" || parts[1] == "ipv6") && parts[2] == "conf" {
		return parts[3]
	}
	return ""
}

// writeBackSysctl writes want's values in sysctlRank order and reads each
// back. A key absent from present (the live keys) cannot be written: a
// per-interface key belongs to an interface that has gone, and an all,
// default or global key is gone when the host booted without that
// protocol (ipv6.disable=1). It is not written and is returned instead, so
// that no restore, the emergency one included, fails on it every time.
func writeBackSysctl(ctx context.Context, env core.Env, want, present map[string]string) ([]string, error) {
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
	var vanished, written []string
	for _, k := range keys {
		if _, ok := present[k]; !ok {
			vanished = append(vanished, k)
			continue
		}
		if err := env.Files.WriteSysctl(ctx, ID, k, want[k]); err != nil {
			return vanished, fmt.Errorf("firewall: write sysctl %s back: %w", k, err)
		}
		written = append(written, k)
	}
	for _, k := range written {
		got, err := env.Files.ReadSysctl(ctx, ID, k)
		if err != nil {
			return vanished, fmt.Errorf("firewall: read sysctl %s back: %w", k, err)
		}
		if got != want[k] {
			return vanished, fmt.Errorf("firewall: sysctl %s reads back %q, the snapshot's value is %q", k, got, want[k])
		}
	}
	return vanished, nil
}
