package predict

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// The framework files "ufw default" rewrites.
const (
	afterRules  = "/etc/ufw/after.rules"
	afterRules6 = "/etc/ufw/after6.rules"
)

// Edit returns the configured state after ufw runs one argument vector on
// cfg (spec 2.3, 2.5): the tuples, Enabled, LogLevel and Default* as UFW
// 0.36 leaves them, and in Files the bytes of every file the command
// writes (user.rules and user6.rules as Files renders them, ufw.conf and
// /etc/default/ufw as set_default edits them, after.rules and after6.rules
// as "ufw default" rewrites them); a file the command leaves alone keeps
// its bytes. A rule that already exists, or a delete that matches nothing,
// changes nothing, as in UFW. The vectors are the ones parseVector
// accepts; any other vector, and a command ufw would fail (an invalid
// position, an IPv6 rule with IPv6 off, a file it must write that is
// missing), is an error. cfg is not modified.
func Edit(cfg fwstate.Config, args []string) (fwstate.Config, error) {
	v, err := parseVector(args)
	if err != nil {
		return fwstate.Config{}, err
	}
	if err := checkRenderable(cfg); err != nil {
		return fwstate.Config{}, err
	}
	out := cloneConfig(cfg)
	e := editor{cfg: &out}
	switch v.kind {
	case vecRule:
		err = e.rule(v)
	case vecDefault:
		err = e.setPolicy(v.policy, v.chain)
	case vecLogging:
		err = e.setLogging(v.level)
	case vecEnable:
		err = e.enable()
	case vecDisable:
		err = e.disable()
	case vecReload:
		err = e.reload()
	}
	if err != nil {
		return fwstate.Config{}, fmt.Errorf("predict: ufw %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

func cloneConfig(c fwstate.Config) fwstate.Config {
	c.Rules4, c.Rules6 = slices.Clone(c.Rules4), slices.Clone(c.Rules6)
	c.Files = maps.Clone(c.Files)
	c.Sysctl, c.Unsupported = slices.Clone(c.Sysctl), slices.Clone(c.Unsupported)
	return c
}

// editor applies one command to a configuration it owns.
type editor struct{ cfg *fwstate.Config }

// write replaces a file's bytes, keeping its mode and owner. UFW rewrites
// a file in place and fails when it is missing.
func (e editor) write(path string, data []byte) error {
	entry, ok := e.cfg.Files[path]
	if !ok || !entry.Present {
		return fmt.Errorf("%s is missing", path)
	}
	entry.Data = data
	e.cfg.Files[path] = entry
	return nil
}

// writeUser is _write_rules for one family. With IPv6 off UFW holds no
// IPv6 rules, so writing user6.rules empties it.
func (e editor) writeUser(v6 bool) error {
	if v6 && !e.cfg.IPv6 {
		e.cfg.Rules6 = nil
	}
	rules := e.cfg.Rules4
	if v6 {
		rules = e.cfg.Rules6
	}
	for i := range rules {
		rules[i].Raw = tupleLine(rules[i])
	}
	path := userRules
	if v6 {
		path = userRules6
	}
	return e.write(path, renderUser(*e.cfg, v6))
}

// updateLogging is update_logging's file part: both rule files rewritten.
func (e editor) updateLogging() error {
	if err := e.writeUser(false); err != nil {
		return err
	}
	return e.writeUser(true)
}

func (e editor) setConf(path, key, value string) error {
	return e.write(path, setDefault(e.cfg.Files[path].Data, key, value))
}

// setLogging is set_loglevel: "on" keeps the level, or chooses low when
// logging is off; the rule files are rewritten whether or not UFW runs.
func (e editor) setLogging(level string) error {
	if level == "on" {
		level = e.cfg.LogLevel
		if level == "off" {
			level = "low"
		}
	}
	if err := e.setConf(ufwConf, "LOGLEVEL", level); err != nil {
		return err
	}
	e.cfg.LogLevel = level
	return e.updateLogging()
}

// enable is set_enabled(True): ENABLED is written when it changes, and
// start_firewall rewrites the rule files.
func (e editor) enable() error {
	if !e.cfg.Enabled {
		if err := e.setConf(ufwConf, "ENABLED", "yes"); err != nil {
			return err
		}
		e.cfg.Enabled = true
	}
	return e.updateLogging()
}

// disable is set_enabled(False): stopping UFW writes no rule file.
func (e editor) disable() error {
	if !e.cfg.Enabled {
		return nil
	}
	if err := e.setConf(ufwConf, "ENABLED", "no"); err != nil {
		return err
	}
	e.cfg.Enabled = false
	return nil
}

// reload is a disable and an enable when UFW is enabled, and nothing
// otherwise.
func (e editor) reload() error {
	if !e.cfg.Enabled {
		return nil
	}
	if err := e.disable(); err != nil {
		return err
	}
	return e.enable()
}

// setPolicy is set_default_policy: the policy is written quoted, the
// catch-all log prefixes in after.rules and after6.rules switch between
// "UFW BLOCK" and "UFW ALLOW" whichever chain changed, and an enabled UFW
// restarts, which rewrites the rule files.
func (e editor) setPolicy(policy, chain string) error {
	value, from, to := "DROP", "UFW ALLOW", "UFW BLOCK"
	switch policy {
	case "allow":
		value, from, to = "ACCEPT", "UFW BLOCK", "UFW ALLOW"
	case "reject":
		value = "REJECT"
	}
	if err := e.setConf(defaultsUFW, "DEFAULT_"+chain+"_POLICY", `"`+value+`"`); err != nil {
		return err
	}
	switch chain {
	case "INPUT":
		e.cfg.DefaultInput = value
	case "OUTPUT":
		e.cfg.DefaultOutput = value
	default:
		e.cfg.DefaultForward = value
	}
	for _, path := range []string{afterRules, afterRules6} {
		if err := e.write(path, []byte(strings.ReplaceAll(string(e.cfg.Files[path].Data), from, to))); err != nil {
			return err
		}
	}
	if e.cfg.Enabled {
		return e.updateLogging()
	}
	return nil
}

// rule is the frontend's set_rule for one rule that names no application:
// the families the rule belongs to, and for an insert the position in each
// family's list, then the backend's set_rule per family.
func (e editor) rule(v vector) error {
	num4, num6 := ruleCount(e.cfg.Rules4), 0
	if e.cfg.IPv6 {
		num6 = ruleCount(e.cfg.Rules6)
	}
	if v.position > num4+num6 {
		return fmt.Errorf("invalid position '%d'", v.position)
	}
	if !e.cfg.IPv6 {
		if v.iptype == model.FamilyV6 {
			return fmt.Errorf("IPv6 support not enabled")
		}
		return e.setRule(v, model.FamilyV4, v.position)
	}
	switch v.iptype {
	case model.FamilyV4:
		if v.position > num4 {
			return fmt.Errorf("invalid position '%d'", v.position)
		}
		return e.setRule(v, model.FamilyV4, v.position)
	case model.FamilyV6:
		pos := v.position
		switch {
		case pos > num4:
			pos -= num4
		case pos != 0:
			return fmt.Errorf("invalid position '%d'", v.position)
		}
		return e.setRule(v, model.FamilyV6, pos)
	}
	userPos, pos := v.position, v.position
	if !v.remove && userPos > num4 {
		p, err := e.otherPosition(userPos-num4, true)
		if err != nil {
			return err
		}
		pos = p
	}
	if err := e.setRule(v, model.FamilyV4, pos); err != nil {
		return err
	}
	if !v.remove && userPos > 0 {
		num4, pos = ruleCount(e.cfg.Rules4), userPos+1
	}
	if !v.remove && pos > 0 && pos <= num4 {
		p, err := e.otherPosition(pos, false)
		if err != nil {
			return err
		}
		pos = p
	}
	if !v.remove && pos > num4 {
		pos -= num4
	}
	return e.setRule(v, model.FamilyV6, pos)
}

// appTuple is UFWRule.get_app_tuple, which groups the tuples of one
// application rule; "" for a rule without an application.
func appTuple(t model.Tuple) string {
	if t.DApp == "" && t.SApp == "" {
		return ""
	}
	tupl := fmt.Sprintf("%s %s %s %s", t.DApp, t.Dst, t.SApp, t.Src)
	if t.DApp == "" {
		tupl = fmt.Sprintf("%s %s %s %s", t.DPort, t.Dst, t.SApp, t.Src)
	}
	if t.SApp == "" {
		tupl = fmt.Sprintf("%s %s %s %s", t.DApp, t.Dst, t.SPort, t.Src)
	}
	if t.InIface == "" && t.OutIface == "" {
		return tupl + " " + string(t.Direction)
	}
	if t.InIface != "" {
		tupl += " in_" + t.InIface
	}
	if t.OutIface != "" {
		tupl += " out_" + t.OutIface
	}
	return tupl
}

// ruleCount is get_rules_count: the rules "ufw status numbered" lists, an
// application rule's tuples counting once.
func ruleCount(rules []model.Tuple) int {
	seen := map[string]bool{}
	n := 0
	for _, r := range rules {
		if a := appTuple(r); a != "" {
			if seen[a] {
				continue
			}
			seen[a] = true
		}
		n++
	}
	return n
}

// otherPosition is find_other_position: the 1-based position in the other
// family's list of the rule that matches the rule at the given user
// position of one family's list, or 0 when none matches.
func (e editor) otherPosition(position int, v6 bool) (int, error) {
	rules, others, fam := e.cfg.Rules4, e.cfg.Rules6, model.FamilyV6
	if v6 {
		rules, others, fam = e.cfg.Rules6, e.cfg.Rules4, model.FamilyV4
	}
	if position < 1 || position > len(rules) {
		return 0, fmt.Errorf("invalid position '%d'", position)
	}
	seen := map[string]bool{}
	offset := 0
	for i, r := range rules {
		if i >= position {
			break
		}
		if a := appTuple(r); a != "" {
			if seen[a] {
				offset++
			}
			seen[a] = true
		}
	}
	if position-1+offset >= len(rules) {
		return 0, fmt.Errorf("invalid position '%d'", position)
	}
	m := rules[position-1+offset]
	m.Family = fam
	for _, a := range []*string{&m.Src, &m.Dst} {
		switch {
		case fam == model.FamilyV6 && *a == "0.0.0.0/0":
			*a = "::/0"
		case fam == model.FamilyV4 && *a == "::/0":
			*a = "0.0.0.0/0"
		}
	}
	for i, r := range others {
		if matchTuple(r, m) == 0 {
			return i + 1, nil
		}
	}
	return 0, nil
}

// matchTuple is UFWRule.match: 0 for the same rule, -2 when only the
// comment differs, -1 when the action or log type differs too, 1 for a
// different rule.
func matchTuple(x, y model.Tuple) int {
	if x.DPort != y.DPort || x.SPort != y.SPort || x.Proto != y.Proto || x.Src != y.Src || x.Dst != y.Dst ||
		x.Family != y.Family || x.DApp != y.DApp || x.SApp != y.SApp || x.InIface != y.InIface ||
		x.OutIface != y.OutIface || x.Direction != y.Direction || x.Route != y.Route {
		return 1
	}
	switch {
	case x.Action == y.Action && x.Log == y.Log && x.Comment == y.Comment:
		return 0
	case x.Action == y.Action && x.Log == y.Log:
		return -2
	}
	return -1
}

// setRule is the backend's set_rule for one family: the new list is built
// as UFW builds it (an insert skipped when the rule exists, an add that
// only changes the action, log type or comment replacing the rule, a
// delete without a comment matching any comment), and the family's file is
// rewritten only when the list changes. Existing rules are normalized as
// UFW normalizes them.
func (e editor) setRule(v vector, fam model.Family, position int) error {
	rules := &e.cfg.Rules4
	if fam == model.FamilyV6 {
		rules = &e.cfg.Rules6
	}
	if position < 0 || position > len(*rules) {
		return fmt.Errorf("invalid position '%d'", position)
	}
	r, err := forFamily(v.rule, fam)
	if err != nil {
		return err
	}
	type appKey struct{ dst, src, dapp, sapp string }
	var newRules []model.Tuple
	found, modified, inserted := false, false, false
	matches := 0
	last := appKey{}
	for i, old := range *rules {
		count := i + 1
		old, err := normalizeTuple(old)
		if err != nil {
			return err
		}
		current := appKey{old.Dst, old.Src, old.DApp, old.SApp}
		if count == position {
			if (last.dapp == "" && last.sapp == "" && count > 1) || (current.dapp == "" && current.sapp == "") || last != current {
				inserted = true
				newRules = append(newRules, r)
				last = appKey{}
			} else {
				position++
			}
		}
		last = current
		ret := matchTuple(old, r)
		if ret < 1 {
			matches++
		}
		switch {
		case ret == 0 && !found && !inserted:
			found = true
			if !v.remove {
				newRules = append(newRules, r)
			}
		case ret == -2 && v.remove && r.Comment == "":
			found = true
		case ret < 0 && !v.remove && !inserted:
			found, modified = true, true
			newRules = append(newRules, r)
		default:
			newRules = append(newRules, old)
		}
	}
	if inserted {
		if matches > 0 {
			return nil
		}
	} else {
		if !found && !v.remove {
			newRules = append(newRules, r)
		}
		if (!found && v.remove) || (found && !v.remove && !modified) {
			return nil
		}
	}
	*rules = newRules
	return e.writeUser(fam == model.FamilyV6)
}
