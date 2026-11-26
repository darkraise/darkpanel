package users

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/ssh"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

// lockKey serialises the module's account changes.
const lockKey = ID

// stateDir keeps, for each account users.user.lock locked, the expiry it
// had before, which users.user.unlock restores.
const stateDir = "/var/lib/darkpanel/users"

const defaultShell = "/bin/bash"

// The password bounds of spec section 3.
const (
	minPassword = 8
	maxPassword = 256
)

// NameInput names an account.
type NameInput struct {
	Name string `json:"name" jsonschema:"pattern=^[a-z_][a-z0-9_-]{0\\,31}$" jsonschema_description:"The account name."`
}

// CreateInput is the input of users.user.create.
type CreateInput struct {
	Name     string   `json:"name" jsonschema:"pattern=^[a-z_][a-z0-9_-]{0\\,31}$" jsonschema_description:"A new account name."`
	Shell    string   `json:"shell,omitempty" jsonschema_description:"A shell listed in /etc/shells; /bin/bash when omitted."`
	Sudo     bool     `json:"sudo,omitempty" jsonschema_description:"Add the account to the sudo group."`
	Password string   `json:"password,omitempty" secret:"erase" jsonschema:"minLength=8,maxLength=256" jsonschema_description:"An optional password, 8-256 characters with no control character."`
	Keys     []string `json:"keys,omitempty" jsonschema:"maxItems=64" jsonschema_description:"SSH public key lines to authorize, as users.key.add takes them."`
}

// PasswordInput is the input of users.password.set.
type PasswordInput struct {
	Name     string `json:"name" jsonschema:"pattern=^[a-z_][a-z0-9_-]{0\\,31}$" jsonschema_description:"The account name."`
	Password string `json:"password" secret:"erase" jsonschema:"minLength=8,maxLength=256" jsonschema_description:"8-256 characters with no control character."`
}

// DeleteInput is the input of users.user.delete.
type DeleteInput struct {
	core.Guarded
	Name       string `json:"name" jsonschema:"pattern=^[a-z_][a-z0-9_-]{0\\,31}$" jsonschema_description:"The account name."`
	RemoveHome bool   `json:"removeHome,omitempty" jsonschema_description:"Delete the home directory and mail spool too."`
}

// Changed names the account a change acted on.
type Changed struct {
	Name string `json:"name"`
}

func changed(name string) Changed { return Changed{Name: name} }

func (m module) accountActions() []core.ActionDef {
	spec := func(name, summary string, class core.Class) core.ActionSpec {
		return core.ActionSpec{
			Name: ID + "." + name, Version: 1, Summary: summary, Class: class, Side: core.SideAgent,
			LockKeys: []string{lockKey}, MinScope: core.ScopeAdmin,
		}
	}
	compensated := func(name, summary, by string) core.ActionSpec {
		s := spec(name, summary, core.ClassCompensable)
		s.CompensatedBy = ID + "." + by
		return s
	}
	forward := func(name, summary string) core.ActionSpec {
		s := spec(name, summary, core.ClassCompensable)
		s.ForwardOnly, s.Converges = true, true
		return s
	}
	create := compensated("user.create", "Create a login account with its home and group, optionally in sudo and with a password.", "user.delete")
	create.StepUp = true
	sudoAdd := compensated("sudo.add", "Add an account to the sudo group.", "sudo.remove")
	sudoAdd.StepUp = true
	passwordSet := forward("password.set", "Set an account's password; it is never logged or returned.")
	passwordSet.StepUp = true
	guarded := func(s core.ActionSpec) core.ActionSpec {
		s.Guards = []core.GuardSpec{{ID: GuardNoWayIn, Overridable: true}}
		return s
	}
	lock := guarded(compensated("user.lock", "Lock an account: its expiry ends key and password sign-in.", "user.unlock"))
	return []core.ActionDef{
		core.DefineRead[struct{}, AccountList](core.ActionSpec{
			Name: ID + ".user.list", Version: 1, Class: core.ClassRead, Side: core.SideAgent,
			Summary: "List root and the login accounts with UIDs 1000-59999.",
		}, lister{m: m}),
		core.DefineMutating[CreateInput, Changed](create, createHandler{m: m}),
		core.DefineMutating[GuardedNameInput, Changed](lock, nameGuarded{h: lockHandler{m: m, lock: true}, m: m, edit: lockEdit}),
		core.DefineMutating[NameInput, Changed](compensated("user.unlock", "Unlock an account, restoring the expiry it had.", "user.lock"), lockHandler{m: m}),
		core.DefineMutating[DeleteInput, Changed](guarded(spec("user.delete", "Delete an account, optionally with its home.", core.ClassIrreversible)), deleteHandler{m: m}),
		core.DefineMutating[PasswordInput, Changed](passwordSet, passwordHandler{m: m}),
		core.DefineMutating[GuardedNameInput, Changed](guarded(forward("password.remove", "Remove an account's password: its hash becomes *.")),
			nameGuarded{h: noPasswordHandler{m: m}, m: m, edit: noPasswordEdit}),
		core.DefineMutating[NameInput, Changed](sudoAdd, sudoHandler{m: m, add: true}),
		core.DefineMutating[NameInput, Changed](compensated("sudo.remove", "Remove an account from the sudo group.", "sudo.add"), sudoHandler{m: m}),
	}
}

// checkPassword refuses a password outside 8-256 characters, not UTF-8, or
// holding a control character: a CR or LF would end chpasswd's line.
func checkPassword(pw string) error {
	switch n := utf8.RuneCountInString(pw); {
	case !utf8.ValidString(pw):
		return invalid("/password", "the password is not UTF-8")
	case strings.ContainsFunc(pw, unicode.IsControl):
		return invalid("/password", "the password holds a control character")
	case n < minPassword || n > maxPassword:
		return invalid("/password", fmt.Sprintf("the password needs %d-%d characters", minPassword, maxPassword))
	}
	return nil
}

// run runs one account command; a non-zero exit is an error with its
// stderr, which never holds stdin.
func run(ctx context.Context, env core.Env, path string, args []string, stdin []byte) (core.CmdResult, error) {
	res, err := env.Exec.Run(ctx, core.Cmd{Path: path, Args: args, Stdin: stdin})
	if err != nil {
		return res, fmt.Errorf("users: run %s: %w", path, err)
	}
	if res.ExitCode != 0 {
		return res, fmt.Errorf("users: %s exited %d: %s", path, res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return res, nil
}

type createHandler struct{ m module }

// keyLines validates create's keys and returns the authorized_keys file
// they make.
func (in CreateInput) keyLines() ([]byte, error) {
	var out []byte
	seen := map[string]bool{}
	for i, k := range in.Keys {
		pub, line, err := parseNewKey(fmt.Sprintf("/keys/%d", i), k)
		if err != nil {
			return nil, err
		}
		fp := ssh.FingerprintSHA256(pub)
		if seen[fp] {
			return nil, invalid(fmt.Sprintf("/keys/%d", i), "the key "+fp+" is listed twice")
		}
		seen[fp] = true
		out = append(out, line+"\n"...)
	}
	return out, nil
}

func (h createHandler) check(ctx context.Context, env core.Env, in CreateInput) (string, error) {
	switch {
	case platform.SystemUser(in.Name):
		return "", invalid("/name", in.Name+" is Darkpanel's own system user")
	case !platform.LoginName(in.Name):
		return "", invalid("/name", "an account name is a lowercase letter or _, then up to 31 lowercase letters, digits, _ or -")
	}
	if in.Password != "" {
		if err := checkPassword(in.Password); err != nil {
			return "", err
		}
	}
	if _, err := in.keyLines(); err != nil {
		return "", err
	}
	if len(in.Keys) > 0 {
		if err := keysAvailable(ctx, env); err != nil {
			return "", invalid("/keys", err.Error())
		}
	}
	db, err := h.m.readHost()
	if err != nil {
		return "", err
	}
	shell := in.Shell
	if shell == "" {
		shell = defaultShell
	}
	switch {
	case db.entries[in.Name] != nil:
		return "", invalid("/name", "an account named "+in.Name+" exists")
	case db.groups[in.Name]:
		return "", invalid("/name", "a group named "+in.Name+" exists")
	case !slices.Contains(db.shells, shell):
		return "", invalid("/shell", shell+" is not listed in /etc/shells")
	}
	return shell, nil
}

func (h createHandler) Plan(ctx context.Context, env core.Env, in CreateInput) (core.PlanOutcome, error) {
	shell, err := h.check(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{{Op: "+", Text: fmt.Sprintf("Create the account %s with a home directory, its own group and the shell %s", in.Name, shell)}}
	if in.Sudo {
		diff = append(diff, core.DiffLine{Op: "+", Text: "Add " + in.Name + " to " + platform.SudoGroup})
	}
	if in.Password != "" {
		diff = append(diff, core.DiffLine{Op: "+", Text: "Set a password for " + in.Name})
	}
	if n := len(in.Keys); n > 0 {
		diff = append(diff, core.DiffLine{Op: "+", Text: fmt.Sprintf("Authorize %d SSH key(s) for %s", n, in.Name)})
	}
	return core.PlanOutcome{Diff: diff, StateHash: "absent"}, nil
}

func (h createHandler) Apply(ctx context.Context, env core.Env, in CreateInput) (Changed, error) {
	shell, err := h.check(ctx, env, in)
	if err != nil {
		return Changed{}, err
	}
	keys, err := in.keyLines()
	if err != nil {
		return Changed{}, err
	}
	if _, err := run(ctx, env, platform.UseraddPath, platform.LoginUseradd(in.Name, shell), nil); err != nil {
		return Changed{}, err
	}
	if in.Sudo {
		if _, _, err := h.m.account(in.Name); err != nil {
			return Changed{}, err
		}
		if _, err := run(ctx, env, platform.GpasswdPath, platform.GpasswdSudo(in.Name, true), nil); err != nil {
			return Changed{}, err
		}
	}
	if in.Password != "" {
		if err := setPassword(ctx, env, h.m, in.Name, in.Password); err != nil {
			return Changed{}, err
		}
	}
	if len(keys) > 0 {
		if _, _, err := h.m.account(in.Name); err != nil {
			return Changed{}, err
		}
		if err := env.Home.WriteAuthorizedKeys(ctx, in.Name, keys); err != nil {
			return Changed{}, err
		}
	}
	return changed(in.Name), nil
}

func (h createHandler) Verify(ctx context.Context, env core.Env, in CreateInput, _ Changed) error {
	e, _, err := h.m.account(in.Name)
	if err != nil {
		return err
	}
	if e.sudo() != in.Sudo {
		return fmt.Errorf("users: %s is not in %s as asked", in.Name, platform.SudoGroup)
	}
	if len(in.Keys) > 0 {
		data, err := h.m.readKeys(ctx, env, in.Name)
		if err != nil {
			return err
		}
		if n := countKeys(data); n != len(in.Keys) {
			return fmt.Errorf("users: %s has %d authorized keys, want %d", in.Name, n, len(in.Keys))
		}
	}
	if in.Password != "" {
		return verifyPassword(ctx, env, in.Name, true)
	}
	return nil
}

// setPassword feeds "name:password" to chpasswd after checking the account.
func setPassword(ctx context.Context, env core.Env, m module, name, pw string) error {
	if _, _, err := m.account(name); err != nil {
		return err
	}
	_, err := run(ctx, env, platform.ChpasswdPath, platform.Chpasswd(), []byte(name+":"+pw+"\n"))
	return err
}

// verifyPassword checks passwd -S reports a usable password for name, or
// none.
func verifyPassword(ctx context.Context, env core.Env, name string, set bool) error {
	status, err := passwordStatus(ctx, env)
	if err != nil {
		return err
	}
	if (status[name] == "P") != set {
		return fmt.Errorf("users: passwd -S reports %q for %s", status[name], name)
	}
	return nil
}

// lockHandler locks (lock) or unlocks an account through its expiry.
type lockHandler struct {
	m    module
	lock bool
}

func recordName(name string) string { return "expiry-" + name }

// recorded is the expiry users.user.lock recorded for name, "" when none.
func (h lockHandler) recorded(name string) (string, error) {
	b, err := h.m.d.readFile(stateDir + "/" + recordName(name))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("users: read the recorded expiry of %s: %w", name, err)
	}
	return strings.TrimSpace(string(b)), nil
}

// check refuses a lock of root or of a locked account, and an unlock of an
// account that is not locked; it returns the account and, for an unlock,
// the expiry to restore.
func (h lockHandler) check(env core.Env, name string) (*entry, string, error) {
	e, _, err := h.m.account(name)
	if err != nil {
		return nil, "", err
	}
	locked := e.locked(env.Clock.Now())
	switch {
	case h.lock && e.name == "root":
		return nil, "", invalid("/name", "root cannot be locked")
	case h.lock && locked:
		return nil, "", invalid("/name", name+" is locked already")
	case h.lock:
		return e, "", nil
	case !locked:
		return nil, "", invalid("/name", name+" is not locked")
	}
	restore, err := h.recorded(name)
	return e, restore, err
}

func (h lockHandler) Plan(_ context.Context, env core.Env, in NameInput) (core.PlanOutcome, error) {
	e, restore, err := h.check(env, in.Name)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	line := core.DiffLine{Op: "-", Text: "Lock " + in.Name + ": no key or password sign-in until it is unlocked"}
	if !h.lock {
		until := "it never expires"
		if restore != "" {
			until = "it expires on " + restore
		}
		line = core.DiffLine{Op: "+", Text: "Unlock " + in.Name + "; " + until}
	}
	return core.PlanOutcome{Diff: []core.DiffLine{line}, StateHash: e.digest(restore)}, nil
}

func (h lockHandler) Apply(ctx context.Context, env core.Env, in NameInput) (Changed, error) {
	e, restore, err := h.check(env, in.Name)
	if err != nil {
		return Changed{}, err
	}
	if h.lock {
		if err := env.Files.WriteManaged(ctx, ID, stateDir, recordName(in.Name), []byte(e.expiryDate()), 0o600, 0, 0); err != nil {
			return Changed{}, core.Definite(err)
		}
		if _, err := run(ctx, env, platform.UsermodPath, platform.UsermodExpiry(in.Name, lockedExpiry), nil); err != nil {
			return Changed{}, err
		}
		return changed(in.Name), nil
	}
	if _, err := run(ctx, env, platform.UsermodPath, platform.UsermodExpiry(in.Name, restore), nil); err != nil {
		return Changed{}, err
	}
	if err := env.Files.RemoveManaged(ctx, ID, stateDir, recordName(in.Name)); err != nil {
		return Changed{}, err
	}
	return changed(in.Name), nil
}

// Verify checks the account carries the lock's expiry, or no longer does.
func (h lockHandler) Verify(_ context.Context, _ core.Env, in NameInput, _ Changed) error {
	e, _, err := h.m.account(in.Name)
	if err != nil {
		return err
	}
	if (e.expiry == lockedExpiry) != h.lock {
		return fmt.Errorf("users: the expiry of %s is %q", in.Name, e.expiry)
	}
	return nil
}

type deleteHandler struct{ m module }

func (h deleteHandler) check(name string) (*entry, error) {
	e, _, err := h.m.account(name)
	if err != nil {
		return nil, err
	}
	if e.name == "root" {
		return nil, invalid("/name", "root cannot be deleted")
	}
	return e, nil
}

func (h deleteHandler) Plan(ctx context.Context, env core.Env, in DeleteInput) (core.PlanOutcome, error) {
	e, err := h.check(in.Name)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	guard, err := h.m.accountGuard(ctx, env, in.Guarded, in.Name, deleteEdit)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{{Op: "-", Text: "Delete the account " + in.Name}}
	if in.RemoveHome {
		diff = append(diff, core.DiffLine{Op: "-", Text: "Delete " + e.home + " and the account's mail spool"})
	} else {
		diff = append(diff, core.DiffLine{Op: " ", Text: "Keep " + e.home})
	}
	jobs, err := h.m.jobsRunningAs(ctx, env, in.Name)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	for _, slug := range jobs {
		diff = append(diff, core.DiffLine{Op: " ", Text: "Job " + slug + " runs as " + in.Name + " and fails once the account is gone"})
	}
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{guard}, StateHash: e.digest(fmt.Sprint(in.RemoveHome)), Confirmation: in.Name}, nil
}

// jobServices are the services of the jobs module's unit set (M11 spec
// section 6), whose User= line names the account a job runs as.
const (
	jobsModule  = "jobs"
	jobServices = "/etc/systemd/system/darkpanel-job-*.service"
)

// jobsRunningAs are the jobs whose service the ownership store records as
// the jobs module's and that run as name, sorted.
func (m module) jobsRunningAs(ctx context.Context, env core.Env, name string) ([]string, error) {
	paths, err := m.d.glob(jobServices)
	if err != nil {
		return nil, err
	}
	slices.Sort(paths)
	var out []string
	for _, p := range paths {
		ours, err := env.Files.Owns(ctx, jobsModule, p)
		if err != nil {
			return nil, err
		}
		if !ours {
			continue
		}
		data, err := m.d.readFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("users: read %s: %w", p, err)
		}
		for line := range strings.Lines(string(data)) {
			if strings.TrimSpace(line) == "User="+name {
				out = append(out, strings.TrimSuffix(strings.TrimPrefix(filepath.Base(p), "darkpanel-job-"), ".service"))
				break
			}
		}
	}
	return out, nil
}

// userdelInUse is userdel's exit status for an account with a running
// process.
const userdelInUse = 8

func (h deleteHandler) Apply(ctx context.Context, env core.Env, in DeleteInput) (Changed, error) {
	if _, err := h.check(in.Name); err != nil {
		return Changed{}, err
	}
	res, err := run(ctx, env, platform.UserdelPath, platform.LoginUserdel(in.Name, in.RemoveHome), nil)
	if res.ExitCode == userdelInUse {
		return Changed{}, core.Definite(errcode.New(errcode.InvalidInput, in.Name+" has a running process; end its sessions first"))
	}
	if err != nil {
		return Changed{}, err
	}
	if err := env.Files.RemoveManaged(ctx, ID, stateDir, recordName(in.Name)); err != nil {
		return Changed{}, err
	}
	return changed(in.Name), nil
}

func (h deleteHandler) Verify(_ context.Context, _ core.Env, in DeleteInput, _ Changed) error {
	hst, err := h.m.readHost()
	if err != nil {
		return err
	}
	if hst.entries[in.Name] != nil {
		return fmt.Errorf("users: %s still exists", in.Name)
	}
	return nil
}

type passwordHandler struct{ m module }

func (h passwordHandler) Plan(_ context.Context, _ core.Env, in PasswordInput) (core.PlanOutcome, error) {
	if err := checkPassword(in.Password); err != nil {
		return core.PlanOutcome{}, err
	}
	e, _, err := h.m.account(in.Name)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: "+", Text: "Set the password of " + in.Name}}, StateHash: e.digest()}, nil
}

func (h passwordHandler) Apply(ctx context.Context, env core.Env, in PasswordInput) (Changed, error) {
	if err := checkPassword(in.Password); err != nil {
		return Changed{}, err
	}
	if err := setPassword(ctx, env, h.m, in.Name, in.Password); err != nil {
		return Changed{}, err
	}
	return changed(in.Name), nil
}

func (h passwordHandler) Verify(ctx context.Context, env core.Env, in PasswordInput, _ Changed) error {
	return verifyPassword(ctx, env, in.Name, true)
}

// noHash is the password hash users.password.remove leaves.
const noHash = "*"

type noPasswordHandler struct{ m module }

// Plan plans an empty diff when the hash is * already.
func (h noPasswordHandler) Plan(_ context.Context, _ core.Env, in NameInput) (core.PlanOutcome, error) {
	e, _, err := h.m.account(in.Name)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if e.hash == noHash {
		return core.PlanOutcome{Diff: []core.DiffLine{}, StateHash: e.digest()}, nil
	}
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: "-", Text: "Remove the password of " + in.Name}}, StateHash: e.digest()}, nil
}

func (h noPasswordHandler) Apply(ctx context.Context, env core.Env, in NameInput) (Changed, error) {
	if _, _, err := h.m.account(in.Name); err != nil {
		return Changed{}, err
	}
	if _, err := run(ctx, env, platform.UsermodPath, platform.UsermodNoPassword(in.Name), nil); err != nil {
		return Changed{}, err
	}
	return changed(in.Name), nil
}

func (h noPasswordHandler) Verify(_ context.Context, _ core.Env, in NameInput, _ Changed) error {
	e, _, err := h.m.account(in.Name)
	if err != nil {
		return err
	}
	if e.hash != noHash {
		return fmt.Errorf("users: the password of %s is still set", in.Name)
	}
	return nil
}

// sudoHandler adds an account to the sudo group (add) or removes it.
type sudoHandler struct {
	m   module
	add bool
}

func (h sudoHandler) check(name string) (*entry, error) {
	e, _, err := h.m.account(name)
	if err != nil {
		return nil, err
	}
	switch in := e.sudo(); {
	case h.add && in:
		return nil, invalid("/name", name+" is in "+platform.SudoGroup+" already")
	case !h.add && !in:
		return nil, invalid("/name", name+" is not in "+platform.SudoGroup)
	}
	return e, nil
}

func (h sudoHandler) Plan(_ context.Context, _ core.Env, in NameInput) (core.PlanOutcome, error) {
	e, err := h.check(in.Name)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	line := core.DiffLine{Op: "+", Text: "Add " + in.Name + " to " + platform.SudoGroup}
	if !h.add {
		line = core.DiffLine{Op: "-", Text: "Remove " + in.Name + " from " + platform.SudoGroup}
	}
	return core.PlanOutcome{Diff: []core.DiffLine{line}, StateHash: e.digest()}, nil
}

func (h sudoHandler) Apply(ctx context.Context, env core.Env, in NameInput) (Changed, error) {
	if _, err := h.check(in.Name); err != nil {
		return Changed{}, err
	}
	if _, err := run(ctx, env, platform.GpasswdPath, platform.GpasswdSudo(in.Name, h.add), nil); err != nil {
		return Changed{}, err
	}
	return changed(in.Name), nil
}

func (h sudoHandler) Verify(_ context.Context, _ core.Env, in NameInput, _ Changed) error {
	e, _, err := h.m.account(in.Name)
	if err != nil {
		return err
	}
	if e.sudo() != h.add {
		return fmt.Errorf("users: %s is not where the change put it in %s", in.Name, platform.SudoGroup)
	}
	return nil
}
