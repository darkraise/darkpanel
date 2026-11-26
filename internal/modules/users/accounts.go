package users

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

// The account databases the module reads.
const (
	passwdFile = "/etc/passwd"
	shadowFile = "/etc/shadow"
	groupFile  = "/etc/group"
	shellsFile = "/etc/shells"
)

// The login UIDs the module manages besides root's.
const (
	minLoginUID = 1000
	maxLoginUID = 59999
)

// lockedExpiry is the account expiry users.user.lock sets: day 1 of the
// epoch, which ends key and password sign-in alike.
const lockedExpiry = "1"

// Account is one login account: root or a UID of 1000-59999.
type Account struct {
	Name        string   `json:"name"`
	UID         int      `json:"uid"`
	Home        string   `json:"home"`
	Shell       string   `json:"shell"`
	Groups      []string `json:"groups"`
	Sudo        bool     `json:"sudo" jsonschema_description:"A member of the sudo group."`
	Locked      bool     `json:"locked" jsonschema_description:"The account expired today or earlier, as users.user.lock leaves it; a ! password alone is a key-only account and not locked."`
	PasswordSet bool     `json:"passwordSet" jsonschema_description:"passwd -S reports a usable password (status P)."`
	Keys        int      `json:"keys" jsonschema_description:"The number of authorized keys."`
}

// AccountList is the answer of users.user.list.
type AccountList struct {
	Accounts []Account `json:"accounts"`
}

// entry is one account as the databases hold it.
type entry struct {
	name       string
	uid, gid   int
	home       string
	shell      string
	hash       string // the shadow password field
	expiry     string // the shadow account expiry, days since the epoch; "" when none
	groups     []string
	passwdLine string
}

// host is the account databases of one read.
type host struct {
	entries map[string]*entry
	order   []string
	groups  map[string]bool
	shells  []string
}

func (m module) readHost() (*host, error) {
	passwd, err := m.d.readFile(passwdFile)
	if err != nil {
		return nil, fmt.Errorf("users: read %s: %w", passwdFile, err)
	}
	shadow, err := m.d.readFile(shadowFile)
	if err != nil {
		return nil, fmt.Errorf("users: read %s: %w", shadowFile, err)
	}
	group, err := m.d.readFile(groupFile)
	if err != nil {
		return nil, fmt.Errorf("users: read %s: %w", groupFile, err)
	}
	shells, err := m.d.readFile(shellsFile)
	if err != nil {
		return nil, fmt.Errorf("users: read %s: %w", shellsFile, err)
	}
	h := &host{entries: map[string]*entry{}, groups: map[string]bool{}}
	for line := range lines(passwd) {
		f := strings.Split(line, ":")
		if len(f) != 7 {
			continue
		}
		uid, err1 := strconv.Atoi(f[2])
		gid, err2 := strconv.Atoi(f[3])
		if err1 != nil || err2 != nil || h.entries[f[0]] != nil {
			continue
		}
		h.entries[f[0]] = &entry{name: f[0], uid: uid, gid: gid, home: f[5], shell: f[6], passwdLine: line}
		h.order = append(h.order, f[0])
	}
	for line := range lines(shadow) {
		f := strings.Split(line, ":")
		if e := h.entries[f[0]]; e != nil && len(f) >= 8 {
			e.hash, e.expiry = f[1], f[7]
		}
	}
	primary := map[int]string{}
	for line := range lines(group) {
		f := strings.Split(line, ":")
		if len(f) != 4 {
			continue
		}
		h.groups[f[0]] = true
		if gid, err := strconv.Atoi(f[2]); err == nil {
			if _, ok := primary[gid]; !ok {
				primary[gid] = f[0]
			}
		}
		for _, member := range strings.Split(f[3], ",") {
			if e := h.entries[member]; e != nil && !slices.Contains(e.groups, f[0]) {
				e.groups = append(e.groups, f[0])
			}
		}
	}
	for _, e := range h.entries {
		if g, ok := primary[e.gid]; ok && !slices.Contains(e.groups, g) {
			e.groups = append([]string{g}, e.groups...)
		}
	}
	for line := range lines(shells) {
		if !strings.HasPrefix(line, "#") {
			h.shells = append(h.shells, line)
		}
	}
	return h, nil
}

// lines yields the non-empty lines of b without their line ends.
func lines(b []byte) func(func(string) bool) {
	return func(yield func(string) bool) {
		for line := range bytes.Lines(b) {
			if s := strings.TrimRight(string(line), "\r\n"); s != "" && !yield(s) {
				return
			}
		}
	}
}

// managed reports whether the module manages e: root or a login UID, and
// never one of Darkpanel's own system users.
func (e *entry) managed() bool {
	if !platform.LoginName(e.name) {
		return false
	}
	return e.uid == 0 && e.name == "root" || e.uid >= minLoginUID && e.uid <= maxLoginUID
}

// locked reports whether the account expired on or before now's day.
func (e *entry) locked(now time.Time) bool {
	days, err := strconv.ParseInt(e.expiry, 10, 64)
	return err == nil && days <= now.Unix()/86400
}

func (e *entry) sudo() bool { return slices.Contains(e.groups, platform.SudoGroup) }

// expiryDate is the account's expiry as usermod --expiredate takes it:
// YYYY-MM-DD, or "" when it never expires.
func (e *entry) expiryDate() string {
	days, err := strconv.ParseInt(e.expiry, 10, 64)
	if err != nil {
		return ""
	}
	return time.Unix(days*86400, 0).UTC().Format(time.DateOnly)
}

// digest is the plan state of e: what a change of it reads.
func (e *entry) digest(extra ...string) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\n%s\n%s\n%v\n%v", e.passwdLine, e.hash, e.expiry, e.groups, extra))
	return hex.EncodeToString(sum[:])
}

// account rereads the databases and returns the account name names, refused
// unless the module manages it. Every change calls it right before each
// command it runs, so a UID changed since the plan stops the command.
func (m module) account(name string) (*entry, *host, error) {
	h, err := m.readHost()
	if err != nil {
		return nil, nil, err
	}
	e := h.entries[name]
	switch {
	case e == nil:
		return nil, nil, invalid("/name", "no account named "+name)
	case !e.managed():
		return nil, nil, invalid("/name", fmt.Sprintf("%s (UID %d) is not a login account Darkpanel manages", name, e.uid))
	}
	return e, h, nil
}

// passwordStatus maps each account to its passwd -S status: P, L or NP.
func passwordStatus(ctx context.Context, env core.Env) (map[string]string, error) {
	res, err := env.Exec.Run(ctx, core.Cmd{Path: platform.PasswdPath, Args: platform.PasswdStatusAll()})
	if err != nil {
		return nil, fmt.Errorf("users: run passwd: %w", err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("users: passwd -S exited %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	out := map[string]string{}
	for line := range lines(res.Stdout) {
		if f := strings.Fields(line); len(f) >= 2 {
			out[f[0]] = f[1]
		}
	}
	return out, nil
}

type lister struct{ m module }

func (l lister) Read(ctx context.Context, env core.Env, _ struct{}) (AccountList, error) {
	h, err := l.m.readHost()
	if err != nil {
		return AccountList{}, err
	}
	status, err := passwordStatus(ctx, env)
	if err != nil {
		return AccountList{}, err
	}
	now := env.Clock.Now()
	out := AccountList{Accounts: []Account{}}
	keys := func(name string) int {
		if env.Home == nil {
			return 0
		}
		data, err := env.Home.ReadAuthorizedKeys(ctx, name)
		if err != nil {
			env.Log.Warn("users: read authorized keys", "user", name, "error", err)
			return 0
		}
		return countKeys(data)
	}
	for _, name := range h.order {
		e := h.entries[name]
		if !e.managed() {
			continue
		}
		out.Accounts = append(out.Accounts, Account{
			Name: e.name, UID: e.uid, Home: e.home, Shell: e.shell, Groups: append([]string{}, e.groups...),
			Sudo: e.sudo(), Locked: e.locked(now), PasswordSet: status[e.name] == "P", Keys: keys(e.name),
		})
	}
	return out, nil
}

func invalid(path, msg string) error {
	return core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: msg, Fields: []errcode.FieldError{{Path: path, Message: msg}}})
}
