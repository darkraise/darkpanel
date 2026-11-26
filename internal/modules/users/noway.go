package users

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
)

// GuardNoWayIn is the guard of a change after which no account could sign
// in over SSH (spec section 3).
const GuardNoWayIn = ID + ".no_way_in"

// The values sshd takes, compiled in, for the keywords the guard reads when
// no file sets them.
var sshdDefaults = map[string]string{
	"passwordauthentication":       "yes",
	"kbdinteractiveauthentication": "yes",
	"permitrootlogin":              "prohibit-password",
}

// sshView is what the guard reads of sshd's effective settings.
type sshView struct {
	pubkey, password, kbd bool
	rootLogin             string
	methods               [][]string // AuthenticationMethods; nil is any
	allowUsers            []string
	allowGroups           []string
	denyUsers             []string
	denyGroups            []string
	keysFile              string // AuthorizedKeysFile when it is not the default, whose keys the guard cannot read
}

func viewOf(t sshdT) sshView {
	v := sshView{
		pubkey: t.first("pubkeyauthentication") == "yes", password: t.first("passwordauthentication") == "yes",
		kbd: t.first("kbdinteractiveauthentication") == "yes", rootLogin: rootLogin(t.first("permitrootlogin")),
	}
	list := func(k string) []string {
		var out []string
		for _, line := range t[k] {
			out = append(out, strings.Fields(line)...)
		}
		return out
	}
	v.allowUsers, v.allowGroups, v.denyUsers, v.denyGroups = list("allowusers"), list("allowgroups"), list("denyusers"), list("denygroups")
	if m := t.first("authenticationmethods"); m != "" && m != "any" {
		for _, l := range strings.Fields(m) {
			v.methods = append(v.methods, strings.Split(l, ","))
		}
	}
	if k := t.first("authorizedkeysfile"); k != "" && k != defaultKeysFile {
		v.keysFile = k
	}
	return v
}

// candidate is one account as the guard judges it.
type candidate struct {
	name     string
	root     bool
	groups   []string
	locked   bool
	password bool // passwd -S status P
	keys     int
}

// candidates are the accounts the module manages, with their key counts
// read when sshd reads the default key files.
func (m module) candidates(ctx context.Context, env core.Env, v sshView) ([]candidate, error) {
	h, err := m.readHost()
	if err != nil {
		return nil, err
	}
	status, err := passwordStatus(ctx, env)
	if err != nil {
		return nil, err
	}
	now := env.Clock.Now()
	var out []candidate
	for _, name := range h.order {
		e := h.entries[name]
		if !e.managed() {
			continue
		}
		c := candidate{name: name, root: name == "root", groups: e.groups, locked: e.locked(now), password: status[name] == "P"}
		if v.keysFile == "" && env.Home != nil {
			data, err := env.Home.ReadAuthorizedKeys(ctx, name)
			if err != nil {
				env.Log.Warn("users: read authorized keys for the lockout guard", "user", name, "error", err)
			}
			c.keys = countKeys(data)
		}
		out = append(out, c)
	}
	return out, nil
}

// wildmatch matches s against an sshd pattern of * and ?.
func wildmatch(pattern, s string) bool {
	for len(pattern) > 0 {
		switch pattern[0] {
		case '*':
			for i := len(s); i >= 0; i-- {
				if wildmatch(pattern[1:], s[i:]) {
					return true
				}
			}
			return false
		case '?':
			if s == "" {
				return false
			}
		default:
			if s == "" || s[0] != pattern[0] {
				return false
			}
		}
		pattern, s = pattern[1:], s[1:]
	}
	return s == ""
}

// userMatches reports whether a user pattern matches name. A pattern with
// a host part (USER@HOST) admits or denies the user from some hosts only,
// so it counts as admitting and not as denying: the guard asks whether the
// account could sign in from somewhere.
func userMatches(patterns []string, name string, allow bool) bool {
	return slices.ContainsFunc(patterns, func(p string) bool {
		user, _, host := strings.Cut(p, "@")
		return (allow || !host) && wildmatch(user, name)
	})
}

// groupMatches reports whether a group pattern matches one of groups; a
// negated pattern (!group) matches none, as in sshd.
func groupMatches(patterns, groups []string) bool {
	return slices.ContainsFunc(patterns, func(p string) bool {
		return !strings.HasPrefix(p, "!") && slices.ContainsFunc(groups, func(g string) bool { return wildmatch(p, g) })
	})
}

// admitted applies sshd's lists in its order: DenyUsers, AllowUsers,
// DenyGroups, AllowGroups.
func (v sshView) admitted(c candidate) bool {
	switch {
	case userMatches(v.denyUsers, c.name, false):
		return false
	case len(v.allowUsers) > 0 && !userMatches(v.allowUsers, c.name, true):
		return false
	case groupMatches(v.denyGroups, c.groups):
		return false
	case len(v.allowGroups) > 0 && !groupMatches(v.allowGroups, c.groups):
		return false
	}
	return true
}

// ways says how c could sign in, or, when it cannot, why not.
func (v sshView) ways(c candidate) (bool, string) {
	switch {
	case c.locked:
		return false, "locked"
	case !v.admitted(c):
		return false, "shut out by the allow and deny lists"
	case c.root && v.rootLogin == "no":
		return false, "PermitRootLogin no"
	}
	// forced-commands-only lets root run a key's command, never a shell.
	rootKeys := !c.root || v.rootLogin == "yes" || v.rootLogin == "prohibit-password"
	rootPassword := !c.root || v.rootLogin == "yes"
	keyOK := v.pubkey && c.keys > 0 && rootKeys
	pwOK := c.password && v.password && rootPassword
	kbdOK := c.password && v.kbd && rootPassword
	satisfied := keyOK || pwOK || kbdOK
	if v.methods != nil {
		satisfied = slices.ContainsFunc(v.methods, func(list []string) bool {
			keys := 0
			for _, m := range list {
				name, _, _ := strings.Cut(m, ":")
				switch {
				case name == "publickey":
					keys++
					if !keyOK || keys > c.keys {
						return false
					}
				case name == "password" && pwOK, name == "keyboard-interactive" && kbdOK:
				default:
					return false
				}
			}
			return true
		})
	}
	if satisfied {
		var how []string
		if keyOK {
			how = append(how, "key")
		}
		if pwOK || kbdOK {
			how = append(how, "password")
		}
		return true, strings.Join(how, " or ")
	}
	var why []string
	switch {
	case c.keys == 0:
		why = append(why, "no key")
	case !v.pubkey:
		why = append(why, "key sign-in off")
	case !rootKeys:
		why = append(why, "root key sign-in off")
	}
	switch {
	case !c.password:
		why = append(why, "no password")
	case !v.password && !v.kbd:
		why = append(why, "password sign-in off")
	case !rootPassword:
		why = append(why, "root password sign-in off")
	}
	if len(why) == 0 || keyOK || pwOK || kbdOK {
		return false, "AuthenticationMethods not met"
	}
	return false, strings.Join(why, ", ")
}

// noWayIn judges the accounts against v; matchIn is a file holding a Match
// block, whose overrides the check cannot see.
func noWayIn(v sshView, cs []candidate, g core.Guarded, matchIn string) core.Guard {
	var considered, able []string
	for _, c := range cs {
		ok, how := v.ways(c)
		considered = append(considered, c.name+" ("+how+")")
		if ok {
			able = append(able, c.name)
		}
	}
	list := "none"
	if len(considered) > 0 {
		list = strings.Join(considered, ", ")
	}
	var msg string
	if len(able) > 0 {
		msg = fmt.Sprintf("After the change %s could sign in over SSH. Considered: %s.", strings.Join(able, ", "), list)
	} else {
		msg = "After the change no account could sign in over SSH. Considered: " + list + "."
	}
	if v.keysFile != "" {
		msg += " sshd reads keys from " + v.keysFile + ", which this check does not read."
	}
	if matchIn != "" {
		msg += " " + matchIn + " holds a Match block, whose overrides this check cannot see."
	}
	out := core.Guard{ID: GuardNoWayIn, OK: len(able) > 0, Message: msg}
	if !out.OK && slices.Contains(g.Overrides, GuardNoWayIn) {
		out.OK, out.Overridden = true, true
	}
	return out
}

// accountGuard is users.no_way_in for a change of account name: edit
// applies the change to its candidate and returns false when the account
// goes. sshd's settings come from sshd -T; when it cannot run, the guard
// fails and says why. Without an SSH server the change cannot affect SSH
// sign-in, and the guard passes.
func (m module) accountGuard(ctx context.Context, env core.Env, g core.Guarded, name string, edit func(c *candidate) bool) (core.Guard, error) {
	f, ferr := m.readSSHD()
	if errors.Is(ferr, errNoSSHServer) {
		return core.Guard{ID: GuardNoWayIn, OK: true, Message: ferr.Error() + ", so the change does not affect SSH sign-in."}, nil
	}
	t, _, err := readSshdT(ctx, env)
	if err != nil {
		out := core.Guard{ID: GuardNoWayIn, Message: "sshd's settings cannot be read, so no account is known to be able to sign in: " + err.Error()}
		if slices.Contains(g.Overrides, GuardNoWayIn) {
			out.OK, out.Overridden = true, true
		}
		return out, nil
	}
	v := viewOf(t)
	cs, err := m.candidates(ctx, env, v)
	if err != nil {
		return core.Guard{}, err
	}
	kept := cs[:0]
	for _, c := range cs {
		if c.name != name || edit(&c) {
			kept = append(kept, c)
		}
	}
	matchIn := ""
	if ferr == nil {
		matchIn = f.matchIn()
	}
	return noWayIn(v, kept, g, matchIn), nil
}

// sshdGuard is users.no_way_in for users.sshd.apply: sshd -T's settings with
// those the input writes. A setting the input drops from the drop-in falls
// back to the other files, then to sshd's default. The lists are the input's
// when Darkpanel manages them and sshd -T's otherwise.
func (m module) sshdGuard(ctx context.Context, env core.Env, in SshdInput, f sshdFiles, t sshdT) (core.Guard, error) {
	cur := parseSettings(f.ours)
	after := maps.Clone(t)
	for _, k := range []string{"passwordauthentication", "kbdinteractiveauthentication", "permitrootlogin"} {
		field := sshdFields[slices.IndexFunc(sshdFields, func(f sshdField) bool { return strings.ToLower(f.keyword) == k })]
		if v, ok := field.text(&in.SshdSettings); ok {
			after[k] = []string{v}
		} else if _, was := field.text(&cur); was {
			v := f.firstValue(k)
			if v == "" {
				v = sshdDefaults[k]
			}
			after[k] = []string{v}
		}
	}
	if f.listsSetBy() == "" {
		after["allowusers"], after["allowgroups"] = in.AllowUsers, in.AllowGroups
	}
	v := viewOf(after)
	cs, err := m.candidates(ctx, env, v)
	if err != nil {
		return core.Guard{}, err
	}
	return noWayIn(v, cs, in.Guarded, f.matchIn()), nil
}

// GuardedNameInput names the account of a change that may leave no account
// able to sign in.
type GuardedNameInput struct {
	core.Guarded
	Name string `json:"name" jsonschema:"pattern=^[a-z_][a-z0-9_-]{0\\,31}$" jsonschema_description:"The account name."`
}

// nameGuarded runs a handler of NameInput for an action that declares
// users.no_way_in; edit is its change to the account as the guard sees it.
type nameGuarded struct {
	h    core.Mutator[NameInput, Changed]
	m    module
	edit func(c *candidate) bool
}

func (g nameGuarded) Plan(ctx context.Context, env core.Env, in GuardedNameInput) (core.PlanOutcome, error) {
	out, err := g.h.Plan(ctx, env, NameInput{Name: in.Name})
	if err != nil {
		return core.PlanOutcome{}, err
	}
	guard, err := g.m.accountGuard(ctx, env, in.Guarded, in.Name, g.edit)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	out.Guards = []core.Guard{guard}
	return out, nil
}

func (g nameGuarded) Apply(ctx context.Context, env core.Env, in GuardedNameInput) (Changed, error) {
	return g.h.Apply(ctx, env, NameInput{Name: in.Name})
}

func (g nameGuarded) Verify(ctx context.Context, env core.Env, in GuardedNameInput, out Changed) error {
	return g.h.Verify(ctx, env, NameInput{Name: in.Name}, out)
}

func lockEdit(c *candidate) bool       { c.locked = true; return true }
func noPasswordEdit(c *candidate) bool { c.password = false; return true }
func deleteEdit(*candidate) bool       { return false }
