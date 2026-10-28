package firewall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// The resource nouns of the firewall's effects (spec 2.5). A resource is
// "<noun>:<identity>": a rule's id, a default's direction (incoming,
// outgoing, routed), "level" for logging, "enabled" for UFW's state, a file's
// absolute path or a sysctl key in /proc/sys form.
const (
	nounRule    = "firewall.rule"
	nounDefault = "firewall.default"
	nounLogging = "firewall.logging"
	nounState   = "firewall.state"
	nounFile    = "firewall.file"
	nounSysctl  = "firewall.sysctl"
)

// The fixed resources.
const (
	resourceLogging = nounLogging + ":level"
	resourceEnabled = nounState + ":enabled"
)

func ruleResource(id string) string     { return nounRule + ":" + id }
func defaultResource(dir string) string { return nounDefault + ":" + dir }
func fileResource(path string) string   { return nounFile + ":" + path }
func sysctlResource(key string) string  { return nounSysctl + ":" + key }

// ufwFiles are the files ufw rewrites when it edits a rule, a default, the
// logging level or its enabled state; every such effect's Version holds
// their hashes.
var ufwFiles = []string{"/etc/ufw/user.rules", "/etc/ufw/user6.rules", "/etc/ufw/ufw.conf", "/etc/default/ufw"}

// ruleState is the State of a rule effect: the rule's tuple lines by file.
// A rule that is absent is the JSON null instead; Matches is set, and the
// lines are not, when the rule's id names more than one rule.
type ruleState struct {
	V4      string `json:"v4,omitempty"`
	V6      string `json:"v6,omitempty"`
	Matches int    `json:"matches,omitempty"`
}

// ufwVersion is the Version of a ufw effect: what changes on every write,
// which an inverse cannot restore by itself.
type ufwVersion struct {
	Files map[string]string `json:"files"`          // fileHash of each of ufwFiles
	Pos4  int               `json:"pos4,omitempty"` // a rule effect: the rule's 1-based place among user.rules' tuples; 0 when absent
	Pos6  int               `json:"pos6,omitempty"` // the same in user6.rules
}

// ruleGroup is one rule of a configuration with the tuples it comes from.
type ruleGroup struct {
	Rule   model.Rule
	I4, I6 int // indexes into Config.Rules4 and Rules6; -1 when the rule has no tuple there
}

// ruleGroups returns the rules model.Merge returns for cfg, in the same
// order and with the same ids, together with their tuples' indexes. It
// pairs tuples exactly as Merge does, using Merge itself to decide whether
// two tuples are one rule's halves.
func ruleGroups(cfg fwstate.Config) []ruleGroup {
	paired := make([]bool, len(cfg.Rules6))
	out := make([]ruleGroup, 0, len(cfg.Rules4)+len(cfg.Rules6))
	for i, a := range cfg.Rules4 {
		g := ruleGroup{I4: i, I6: -1}
		for j, b := range cfg.Rules6 {
			if paired[j] {
				continue
			}
			if m := model.Merge([]model.Tuple{a}, []model.Tuple{b}); len(m) == 1 {
				paired[j], g.I6, g.Rule = true, j, m[0]
				break
			}
		}
		if g.I6 < 0 {
			g.Rule = model.Merge([]model.Tuple{a}, nil)[0]
		}
		out = append(out, g)
	}
	for j, b := range cfg.Rules6 {
		if !paired[j] {
			out = append(out, ruleGroup{Rule: model.Merge(nil, []model.Tuple{b})[0], I4: -1, I6: j})
		}
	}
	return out
}

// findRules returns the rules of cfg whose id is id.
func findRules(cfg fwstate.Config, id string) []ruleGroup {
	var out []ruleGroup
	for _, g := range ruleGroups(cfg) {
		if g.Rule.ID == id {
			out = append(out, g)
		}
	}
	return out
}

// splitResource returns a resource's noun and identity, without the "?" of
// a creation not yet made.
func splitResource(resource string) (noun, ident string, err error) {
	noun, ident, ok := strings.Cut(resource, ":")
	ident = strings.TrimPrefix(ident, "?")
	if !ok || ident == "" {
		return "", "", fmt.Errorf("firewall: effect resource %q is not <noun>:<identity>", resource)
	}
	return noun, ident, nil
}

// ufwHashes returns the fileHash of each of ufwFiles in files.
func ufwHashes(files map[string]core.RestoreEntry) map[string]string {
	out := make(map[string]string, len(ufwFiles))
	for _, p := range ufwFiles {
		out[p] = fileHash(files[p])
	}
	return out
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		// Only the fixed state shapes of this file are encoded here.
		panic(err)
	}
	return b
}

// policyOf returns the default policy of a direction in cfg.
func policyOf(cfg fwstate.Config, dir string) (string, error) {
	switch dir {
	case "incoming":
		return cfg.DefaultInput, nil
	case "outgoing":
		return cfg.DefaultOutput, nil
	case "routed":
		return cfg.DefaultForward, nil
	}
	return "", fmt.Errorf("firewall: default direction %q is not incoming, outgoing or routed", dir)
}

// ufwDoc is the StateDoc of a ufw resource (a rule, a default, the logging
// level, the enabled state) or a file in cfg; hashes are the ufwFiles
// hashes for the Version. For a rule it also returns the rules the id
// matches.
func ufwDoc(cfg fwstate.Config, hashes map[string]string, resource string) (core.StateDoc, []ruleGroup, error) {
	noun, ident, err := splitResource(resource)
	if err != nil {
		return core.StateDoc{}, nil, err
	}
	version := ufwVersion{Files: hashes}
	switch noun {
	case nounRule:
		groups := findRules(cfg, ident)
		switch len(groups) {
		case 0:
			return core.StateDoc{State: json.RawMessage("null"), Version: mustJSON(version)}, groups, nil
		case 1:
			return groupDoc(cfg, groups[0], hashes), groups, nil
		}
		return core.StateDoc{State: mustJSON(ruleState{Matches: len(groups)}), Version: mustJSON(version)}, groups, nil
	case nounDefault:
		p, err := policyOf(cfg, ident)
		if err != nil {
			return core.StateDoc{}, nil, err
		}
		return core.StateDoc{State: mustJSON(p), Version: mustJSON(version)}, nil, nil
	case nounLogging:
		if resource != resourceLogging {
			return core.StateDoc{}, nil, fmt.Errorf("firewall: effect resource %q is not %s", resource, resourceLogging)
		}
		return core.StateDoc{State: mustJSON(cfg.LogLevel), Version: mustJSON(version)}, nil, nil
	case nounState:
		if resource != resourceEnabled {
			return core.StateDoc{}, nil, fmt.Errorf("firewall: effect resource %q is not %s", resource, resourceEnabled)
		}
		return core.StateDoc{State: mustJSON(cfg.Enabled), Version: mustJSON(version)}, nil, nil
	case nounFile:
		e, ok := cfg.Files[ident]
		if !ok {
			return core.StateDoc{}, nil, fmt.Errorf("firewall: %s is not a UFW file the configuration records", ident)
		}
		return core.StateDoc{State: mustJSON(fileHash(e))}, nil, nil
	}
	return core.StateDoc{}, nil, fmt.Errorf("firewall: effect resource %q has no firewall noun", resource)
}

// groupDoc is the StateDoc of one rule of cfg.
func groupDoc(cfg fwstate.Config, g ruleGroup, hashes map[string]string) core.StateDoc {
	rs, version := ruleState{}, ufwVersion{Files: hashes}
	if g.I4 >= 0 {
		rs.V4, version.Pos4 = cfg.Rules4[g.I4].Raw, g.I4+1
	}
	if g.I6 >= 0 {
		rs.V6, version.Pos6 = cfg.Rules6[g.I6].Raw, g.I6+1
	}
	return core.StateDoc{State: mustJSON(rs), Version: mustJSON(version)}
}

// sysctlDoc is the StateDoc of a sysctl key: its value, or null when the
// key does not exist (an interface that has gone).
func sysctlDoc(ctx context.Context, env core.Env, key string) (core.StateDoc, error) {
	v, err := env.Files.ReadSysctl(ctx, ID, key)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return core.StateDoc{State: json.RawMessage("null")}, nil
	case err != nil:
		return core.StateDoc{}, fmt.Errorf("firewall: observe sysctl %s: %w", key, err)
	}
	return core.StateDoc{State: mustJSON(v)}, nil
}

// observe reads the resource of one effect (the Observer of every firewall
// action, spec 1.2 and 2.5). Current has the shape of the effect's Before,
// After and Post. A rule creation reports how many rules its id matches,
// with each one as a candidate when there are several, and a container
// rule creation 0 or 1; every other effect matches 1.
func observe(ctx context.Context, env core.Env, d deps, e core.Effect) (core.Observation, error) {
	noun, ident, err := splitResource(e.Resource)
	if err != nil {
		return core.Observation{}, err
	}
	switch noun {
	case nounSetting, nounContainer, nounChains:
		return deObserve(ctx, env, d, e, noun, ident)
	}
	if noun == nounSysctl {
		doc, err := sysctlDoc(ctx, env, ident)
		if err != nil {
			return core.Observation{}, err
		}
		return core.Observation{Current: doc, Matches: 1, Exclusive: true}, nil
	}
	cfg, err := d.readConfig(d.root)
	if err != nil {
		return core.Observation{}, fmt.Errorf("firewall: observe %s: %w", e.Resource, err)
	}
	hashes := ufwHashes(cfg.Files)
	doc, groups, err := ufwDoc(cfg, hashes, e.Resource)
	if err != nil {
		return core.Observation{}, err
	}
	obs := core.Observation{Current: doc, Matches: 1, Exclusive: true}
	if noun != nounRule || e.Kind != core.EffectCreate {
		return obs, nil
	}
	obs.Matches = len(groups)
	switch {
	case len(groups) == 1:
		obs.Resource = ruleResource(ident)
	case len(groups) > 1:
		obs.Exclusive = false
		for _, g := range groups {
			obs.Candidates = append(obs.Candidates, core.Candidate{Resource: ruleResource(ident), State: groupDoc(cfg, g, hashes)})
		}
	}
	return obs, nil
}
