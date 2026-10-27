package firewall

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/conf"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// ufwPath is UFW's command-line program.
const ufwPath = "/usr/sbin/ufw"

// ufwOp is one ufw invocation that runUfw journals as an effect.
type ufwOp struct {
	Kind     core.EffectKind
	Resource string   // the resource as made: firewall.rule:<id>, firewall.default:<direction>, firewall.logging:level, firewall.state:enabled
	Args     []string // the argument vector after ufwPath
}

// addRuleOp adds a Darkpanel rule (effect "rule added"). r must be valid
// and carry its id; its tag is its recovery identity.
func addRuleOp(r model.Rule) (ufwOp, error) {
	if r.Origin == model.OriginManual || r.ID == "" {
		return ufwOp{}, fmt.Errorf("firewall: only a Darkpanel rule with an id is added, not %q", r.ID)
	}
	return ufwOp{Kind: core.EffectCreate, Resource: ruleResource(r.ID), Args: model.AddArgs(r)}, nil
}

// deleteRuleOp deletes a rule of either origin (effect "rule deleted"); the
// effect's Before records the rule's tuples and positions for the inverse.
func deleteRuleOp(r model.Rule) ufwOp {
	return ufwOp{Kind: core.EffectDelete, Resource: ruleResource(r.ID), Args: model.DeleteArgs(r)}
}

// policyVerbs maps a default policy as /etc/default/ufw spells it to the
// verb "ufw default" takes.
var policyVerbs = map[string]string{"ACCEPT": "allow", "DROP": "deny", "REJECT": "reject"}

// defaultOp sets a direction's default policy (effect "default changed").
func defaultOp(dir, policy string) (ufwOp, error) {
	verb, ok := policyVerbs[policy]
	if !ok {
		return ufwOp{}, fmt.Errorf("firewall: default policy %q is not ACCEPT, DROP or REJECT", policy)
	}
	if !slices.Contains([]string{"incoming", "outgoing", "routed"}, dir) {
		return ufwOp{}, fmt.Errorf("firewall: default direction %q is not incoming, outgoing or routed", dir)
	}
	return ufwOp{Kind: core.EffectModify, Resource: defaultResource(dir), Args: []string{"default", verb, dir}}, nil
}

// loggingOp sets UFW's logging level (effect "logging changed").
func loggingOp(level string) (ufwOp, error) {
	if !slices.Contains(conf.LogLevels, level) {
		return ufwOp{}, fmt.Errorf("firewall: logging level %q is not one of %s", level, strings.Join(conf.LogLevels, ", "))
	}
	return ufwOp{Kind: core.EffectModify, Resource: resourceLogging, Args: []string{"logging", level}}, nil
}

// enableOp enables or disables UFW (effects "enabled" and "disabled").
// --force skips ufw's interactive question about SSH.
func enableOp(enable bool) ufwOp {
	args := []string{"disable"}
	if enable {
		args = []string{"--force", "enable"}
	}
	return ufwOp{Kind: core.EffectModify, Resource: resourceEnabled, Args: args}
}

// jsonEqual compares two JSON values as canonical JSON: key order,
// insignificant white space and number spelling do not matter. Empty means
// null.
func jsonEqual(a, b json.RawMessage) bool {
	va, errA := decodeJSON(a)
	vb, errB := decodeJSON(b)
	return errA == nil && errB == nil && reflect.DeepEqual(va, vb)
}

func decodeJSON(raw json.RawMessage) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var v any
	err := json.Unmarshal(raw, &v)
	return v, err
}

// stateEqual is spec 1.2's "State-equal"; docEqual compares State and
// Version.
func stateEqual(a, b core.StateDoc) bool { return jsonEqual(a.State, b.State) }
func docEqual(a, b core.StateDoc) bool {
	return jsonEqual(a.State, b.State) && jsonEqual(a.Version, b.Version)
}

// ufwFailure describes a ufw run that failed.
func ufwFailure(args []string, res core.CmdResult, err error) error {
	if err != nil {
		return fmt.Errorf("firewall: ufw %s: %w", strings.Join(args, " "), err)
	}
	return fmt.Errorf("firewall: ufw %s exited %d: %s", strings.Join(args, " "), res.ExitCode, strings.TrimSpace(string(res.Stderr)))
}

// runUfw runs one ufw vector as a journaled effect (spec 1.2, 2.5). Before
// is the resource now with the ufw files' hashes; After is the resource in
// the configuration d.edit predicts, with the hashes of the files d.files
// predicts ufw writes. The effect is intended before ufw runs and, from the
// observation after it, recorded made (State-equal to After), skipped
// (equal to Before: nothing changed, whatever ufw's exit) or left intended
// for the engine's settle with an error. A failed run is an error even
// when it changed nothing.
func runUfw(ctx context.Context, env core.Env, d deps, op ufwOp) error {
	cfg, err := d.readConfig(d.root)
	if err != nil {
		return fmt.Errorf("firewall: %s: %w", op.Resource, err)
	}
	before, groups, err := ufwDoc(cfg, ufwHashes(cfg.Files), op.Resource)
	if err != nil {
		return err
	}
	next, err := d.edit(cfg, op.Args)
	if err != nil {
		return fmt.Errorf("firewall: predict ufw %s: %w", strings.Join(op.Args, " "), err)
	}
	files, err := d.files(next)
	if err != nil {
		return fmt.Errorf("firewall: predict the files of ufw %s: %w", strings.Join(op.Args, " "), err)
	}
	predicted := map[string]string{}
	for _, p := range ufwFiles {
		data, ok := files[p]
		if !ok {
			return fmt.Errorf("firewall: the prediction of ufw %s has no %s", strings.Join(op.Args, " "), p)
		}
		sum := sha256.Sum256(data)
		predicted[p] = "sha256:" + hex.EncodeToString(sum[:])
	}
	after, _, err := ufwDoc(next, predicted, op.Resource)
	if err != nil {
		return err
	}
	e := core.Effect{Kind: op.Kind, Resource: op.Resource, Local: true, InverseLocal: true, Before: before, After: &after}
	if op.Kind == core.EffectCreate {
		if len(groups) > 0 {
			return fmt.Errorf("firewall: %s exists already", op.Resource)
		}
		_, id, _ := splitResource(op.Resource)
		// ufw replaces a rule that differs only in action, log type or
		// comment; journaled as a creation, its undo would lose that rule.
		if len(next.Rules4)-len(cfg.Rules4) != taggedTuples(next.Rules4, id) ||
			len(next.Rules6)-len(cfg.Rules6) != taggedTuples(next.Rules6, id) {
			return fmt.Errorf("firewall: ufw %s would replace an existing rule", strings.Join(op.Args, " "))
		}
		e.Resource, e.Recovery = nounRule+":?"+id, &core.RecoveryID{Kind: "marker", Value: id}
		e.Before = core.StateDoc{State: json.RawMessage("null"), Version: before.Version}
	}
	res, runErr := runJournaled(ctx, env, d, e, op.Resource, func() (core.CmdResult, error) {
		return env.Exec.Run(ctx, core.Cmd{Path: ufwPath, Args: op.Args})
	})
	if runErr != nil || res.ExitCode != 0 {
		return ufwFailure(op.Args, res, runErr)
	}
	return nil
}

// taggedTuples counts the tuples whose comment is a tag of id.
func taggedTuples(ts []model.Tuple, id string) int {
	n := 0
	for _, t := range ts {
		if got, _, ok := model.ParseTag(t.Comment); ok && got == id {
			n++
		}
	}
	return n
}

// runJournaled intends e, makes the call and records the outcome from an
// observation: made with resource when State-equal to e.After (a creation:
// exactly one match), skipped when equal to e.Before (a creation: no
// match), otherwise left intended with an error. A failed call is the
// caller's to report; runJournaled reports only what it could not record.
func runJournaled(ctx context.Context, env core.Env, d deps, e core.Effect, resource string, call func() (core.CmdResult, error)) (core.CmdResult, error) {
	seq, err := env.Effects.Intend(ctx, e, nil)
	if err != nil {
		return core.CmdResult{}, fmt.Errorf("firewall: journal %s: %w", e.Resource, err)
	}
	res, callErr := call()
	obs, obsErr := observe(ctx, env, d, e)
	failed := callErr != nil || res.ExitCode != 0
	creation := e.Kind == core.EffectCreate
	switch {
	case obsErr != nil:
		return res, errors.Join(callErr, fmt.Errorf("firewall: %s is unsettled: %w", e.Resource, obsErr))
	case !failed && stateEqual(obs.Current, *e.After) && (!creation || obs.Matches == 1):
		if err := env.Effects.Made(ctx, seq, resource, obs.Current); err != nil {
			return res, fmt.Errorf("firewall: journal %s: %w", e.Resource, err)
		}
		return res, callErr
	case (creation && obs.Matches == 0) || (!creation && docEqual(obs.Current, e.Before)):
		if err := env.Effects.Skipped(ctx, seq); err != nil {
			return res, errors.Join(callErr, fmt.Errorf("firewall: journal %s: %w", e.Resource, err))
		}
		return res, callErr
	}
	err = fmt.Errorf("firewall: %s is neither as before nor as predicted after the call", e.Resource)
	return res, errors.Join(callErr, err)
}

// writeFileEffect writes one state file as a journaled effect ("file
// written"); its inverse restores the snapshot's entry of the file.
func writeFileEffect(ctx context.Context, env core.Env, d deps, entry core.RestoreEntry) error {
	cfg, err := d.readConfig(d.root)
	if err != nil {
		return fmt.Errorf("firewall: %s: %w", fileResource(entry.Path), err)
	}
	resource := fileResource(entry.Path)
	before, _, err := ufwDoc(cfg, nil, resource)
	if err != nil {
		return err
	}
	after := core.StateDoc{State: mustJSON(fileHash(entry))}
	e := core.Effect{Kind: core.EffectModify, Resource: resource, Local: true, InverseLocal: true, Before: before, After: &after}
	_, err = runJournaled(ctx, env, d, e, resource, func() (core.CmdResult, error) {
		return core.CmdResult{}, env.Files.Restore(ctx, ID, entry)
	})
	return err
}

// writeSysctlEffect sets one sysctl key as a journaled effect ("sysctl
// changed"); the key must exist.
func writeSysctlEffect(ctx context.Context, env core.Env, d deps, key, value string) error {
	resource := sysctlResource(key)
	before, err := sysctlDoc(ctx, env, key)
	if err != nil {
		return err
	}
	if jsonEqual(before.State, json.RawMessage("null")) {
		return fmt.Errorf("firewall: sysctl %s does not exist", key)
	}
	after := core.StateDoc{State: mustJSON(value)}
	e := core.Effect{Kind: core.EffectModify, Resource: resource, Local: true, InverseLocal: true, Before: before, After: &after}
	_, err = runJournaled(ctx, env, d, e, resource, func() (core.CmdResult, error) {
		return core.CmdResult{}, env.Files.WriteSysctl(ctx, ID, key, value)
	})
	return err
}
