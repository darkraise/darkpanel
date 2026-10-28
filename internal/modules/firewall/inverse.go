package firewall

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// inverse undoes one made effect (spec 2.5) from the journal alone, never
// from the change's input: a rule added is deleted by the tuples in its
// Post; a rule deleted is inserted again at its place among the
// snapshot's rules, the IPv4 and IPv6 halves of a both-family rule
// separately, because ufw puts the other half of one insert at the end; a
// default, the logging
// level or the enabled state is set back to its Before (after an enable or
// disable, with the snapshot's values of the sysctl keys not edited by hand
// since written back, since UFW's start applies its sysctl file); a file
// gets the snapshot's entry whose
// hash is its Before; a sysctl key gets its Before. The resource is then
// observed, and anything but its Before's State is an error.
func inverse(ctx context.Context, env core.Env, d deps, snap snapshot, e core.Effect) error {
	noun, ident, err := splitResource(e.Resource)
	if err != nil {
		return err
	}
	switch noun {
	case nounRule:
		if e.Kind == core.EffectCreate {
			err = deleteAdded(ctx, env, e)
		} else {
			err = reinsert(ctx, env, d, snap, e)
		}
	case nounDefault:
		var policy string
		if err = beforeValue(e, &policy); err == nil {
			var op ufwOp
			if op, err = defaultOp(ident, policy); err == nil {
				err = ufw(ctx, env, op.Args)
			}
		}
	case nounLogging:
		var level string
		if err = beforeValue(e, &level); err == nil {
			var op ufwOp
			if op, err = loggingOp(level); err == nil {
				err = ufw(ctx, env, op.Args)
			}
		}
	case nounState:
		var enabled bool
		if err = beforeValue(e, &enabled); err == nil {
			var target map[string]string
			if target, err = sysctlTarget(ctx, env, d, snap, e); err == nil {
				if err = ufw(ctx, env, enableOp(enabled).Args); err == nil {
					err = writeBackLive(ctx, env, d, target)
				}
			}
		}
	case nounFile:
		err = restoreFile(ctx, env, snap, ident, e.Before)
	case nounSysctl:
		var value string
		if err = beforeValue(e, &value); err == nil {
			err = env.Files.WriteSysctl(ctx, ID, ident, value)
		}
	default:
		err = fmt.Errorf("firewall: effect resource %q has no firewall noun", e.Resource)
	}
	if err != nil {
		return fmt.Errorf("firewall: undo effect %d (%s): %w", e.Seq, e.Resource, err)
	}
	obs, err := observe(ctx, env, d, e)
	if err != nil {
		return fmt.Errorf("firewall: undo effect %d (%s): %w", e.Seq, e.Resource, err)
	}
	if !jsonEqual(obs.Current.State, e.Before.State) {
		return fmt.Errorf("firewall: undo effect %d (%s) left %s, want %s", e.Seq, e.Resource, obs.Current.State, e.Before.State)
	}
	return nil
}

// beforeValue decodes a value effect's Before into v. JSON null would
// decode to the zero value and undo to it, so a damaged journal is refused
// before anything is written.
func beforeValue(e core.Effect, v any) error {
	if raw := bytes.TrimSpace(e.Before.State); len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return fmt.Errorf("effect %d has no before state", e.Seq)
	}
	return json.Unmarshal(e.Before.State, v)
}

// ufw runs one ufw vector and fails on a non-zero exit.
func ufw(ctx context.Context, env core.Env, args []string) error {
	res, err := env.Exec.Run(ctx, core.Cmd{Path: ufwPath, Args: args})
	if err != nil || res.ExitCode != 0 {
		return ufwFailure(args, res, err)
	}
	return nil
}

// stateTuples parses a ruleState's tuple lines.
func stateTuples(raw json.RawMessage) (rs ruleState, v4, v6 []model.Tuple, err error) {
	if err := json.Unmarshal(raw, &rs); err != nil {
		return rs, nil, nil, fmt.Errorf("rule state %s: %w", raw, err)
	}
	if rs.Matches > 0 || (rs.V4 == "" && rs.V6 == "") {
		return rs, nil, nil, fmt.Errorf("rule state %s names no single rule", raw)
	}
	for _, f := range []struct {
		line string
		v6   bool
		dst  *[]model.Tuple
	}{{rs.V4, false, &v4}, {rs.V6, true, &v6}} {
		if f.line == "" {
			continue
		}
		t, err := model.ParseTuple(f.line, f.v6)
		if err != nil {
			return rs, nil, nil, err
		}
		*f.dst = []model.Tuple{t}
	}
	return rs, v4, v6, nil
}

// ruleOf converts one tuple, or one rule's two halves, into the rule the
// argument vectors render.
func ruleOf(v4, v6 []model.Tuple) (model.Rule, error) {
	rules := model.Merge(v4, v6)
	if len(rules) != 1 {
		return model.Rule{}, fmt.Errorf("the tuples are %d rules, not one", len(rules))
	}
	if rules[0].Raw != "" {
		return model.Rule{}, fmt.Errorf("rule %s is outside the model and cannot be written", rules[0].ID)
	}
	return rules[0], nil
}

// deleteAdded deletes the rule a "rule added" effect made, as its Post
// records it.
func deleteAdded(ctx context.Context, env core.Env, e core.Effect) error {
	if e.Post == nil {
		return fmt.Errorf("effect %d has no post state", e.Seq)
	}
	_, v4, v6, err := stateTuples(e.Post.State)
	if err != nil {
		return err
	}
	r, err := ruleOf(v4, v6)
	if err != nil {
		return err
	}
	return ufw(ctx, env, model.DeleteArgs(r))
}

// reinsert adds the rule a "rule deleted" effect removed back at its place
// among the snapshot's rules: the IPv4 half among user.rules' tuples, then
// the IPv6 half among user6.rules' tuples, which ufw numbers after every
// IPv4 rule. The place is right after the nearest rule before it in the
// snapshot's file that is still there, so rules added or deleted by hand
// since keep the snapshot's order; the position its Before records is used
// only when the snapshot does not hold the rule. A place past the end
// appends, since ufw refuses to insert there.
func reinsert(ctx context.Context, env core.Env, d deps, snap snapshot, e core.Effect) error {
	_, v4, v6, err := stateTuples(e.Before.State)
	if err != nil {
		return err
	}
	var ver ufwVersion
	if err := json.Unmarshal(e.Before.Version, &ver); err != nil {
		return fmt.Errorf("rule positions %s: %w", e.Before.Version, err)
	}
	for _, half := range []struct {
		tuples []model.Tuple
		pos    int
		v6     bool
	}{{v4, ver.Pos4, false}, {v6, ver.Pos6, true}} {
		if half.tuples == nil {
			continue
		}
		var r model.Rule
		if half.v6 {
			r, err = ruleOf(nil, half.tuples)
		} else {
			r, err = ruleOf(half.tuples, nil)
		}
		if err != nil {
			return err
		}
		cfg, err := d.readConfig(d.root)
		if err != nil {
			return err
		}
		cur, offset, file := cfg.Rules4, 0, "/etc/ufw/user.rules"
		if half.v6 {
			cur, offset, file = cfg.Rules6, len(cfg.Rules4), "/etc/ufw/user6.rules"
		}
		place := half.pos
		if p, ok := snapshotPlace(snap, file, half.v6, half.tuples[0].Raw, cur); ok {
			place = p
		}
		args := model.AddArgs(r)
		if place >= 1 && place <= len(cur) {
			args = model.InsertArgs(offset+place, r)
		}
		if err := ufw(ctx, env, args); err != nil {
			return err
		}
	}
	return nil
}

// snapshotPlace returns the 1-based place among cur at which the tuple line
// raw goes to keep its order in the snapshot's copy of file: after the
// nearest tuple before it there that cur still holds, or first when cur
// holds none of them. ok is false when the snapshot does not hold raw.
func snapshotPlace(snap snapshot, file string, v6 bool, raw string, cur []model.Tuple) (place int, ok bool) {
	var lines []string
	for _, e := range snap.Entries {
		if e.Path != file || !e.Present {
			continue
		}
		for _, line := range strings.Split(string(e.Data), "\n") {
			if !strings.HasPrefix(line, model.TuplePrefix) {
				continue
			}
			if t, err := model.ParseTuple(line, v6); err == nil {
				lines = append(lines, t.Raw)
			}
		}
	}
	i := slices.Index(lines, raw)
	if i < 0 {
		return 0, false
	}
	for j := i - 1; j >= 0; j-- {
		if m := slices.IndexFunc(cur, func(t model.Tuple) bool { return t.Raw == lines[j] }); m >= 0 {
			return m + 2, true
		}
	}
	return 1, true
}

// restoreFile writes back the snapshot's entry of path, which must be the
// file as before (its hash is before's State).
func restoreFile(ctx context.Context, env core.Env, snap snapshot, path string, before core.StateDoc) error {
	for _, entry := range snap.Entries {
		if entry.Path == path && jsonEqual(mustJSON(fileHash(entry)), before.State) {
			return env.Files.Restore(ctx, ID, entry)
		}
	}
	return fmt.Errorf("snapshot %s holds no entry of %s with hash %s", snap.ID, path, before.State)
}

// sysctlTarget returns the sysctl values undoing the enable or disable e
// should leave, read before its ufw vector runs: the snapshot's value of
// every key the change left at the snapshot's value or at the value its
// transition predicts, and the live value of any other key, which was
// edited by hand since and stays (spec 2.5). Keys absent now are left out.
func sysctlTarget(ctx context.Context, env core.Env, d deps, snap snapshot, e core.Effect) (map[string]string, error) {
	if e.Post == nil {
		return nil, fmt.Errorf("effect %d has no post state", e.Seq)
	}
	var enabled bool
	if err := json.Unmarshal(e.Post.State, &enabled); err != nil {
		return nil, err
	}
	tr := fwstate.TransDisable
	if enabled {
		tr = fwstate.TransEnable
	}
	pre, err := d.readLoaded(ctx, env)
	if err != nil {
		return nil, err
	}
	cfg, err := d.readConfig(d.root)
	if err != nil {
		return nil, err
	}
	fwd, err := d.sysctl(cfg, snap.State.Sysctl, tr)
	if err != nil {
		return nil, err
	}
	target := make(map[string]string, len(snap.State.Sysctl))
	for k, v := range snap.State.Sysctl {
		cur, ok := pre.Sysctl[k]
		switch {
		case !ok:
		case cur == v || cur == fwd[k]:
			target[k] = v
		default:
			target[k] = cur
		}
	}
	return target, nil
}

// writeBackLive writes target's sysctl values back over the live ones; the
// keys the kernel no longer has are skipped and logged.
func writeBackLive(ctx context.Context, env core.Env, d deps, target map[string]string) error {
	st, err := d.readLoaded(ctx, env)
	if err != nil {
		return err
	}
	vanished, err := writeBackSysctl(ctx, env, target, st.Sysctl)
	logVanished(env, vanished)
	return err
}
