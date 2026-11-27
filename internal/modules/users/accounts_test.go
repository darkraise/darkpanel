package users

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform"
)

// The host's account databases. darkpanel is given a login UID so that only
// its name keeps it out of the list.
//
//nolint:gosec // G101: test account databases, not credentials
const (
	fxPasswd = `root:x:0:0:root:/root:/bin/bash
daemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin
svc:x:998:998:svc:/var/lib/svc:/usr/sbin/nologin
darkpanel:x:1002:1002::/var/lib/darkpanel:/usr/sbin/nologin
alice:x:1000:1000:Alice:/home/alice:/bin/bash
bob:x:1001:1001:Bob:/home/bob:/bin/zsh
nobody:x:65534:65534:nobody:/nonexistent:/usr/sbin/nologin
`
	fxShadow = `root:$y$j9T$r00t:20000:0:99999:7:::
daemon:*:20000:0:99999:7:::
svc:!:20000::::::
darkpanel:!:20000::::::
alice:!:20000:0:99999:7:::
bob:$y$j9T$b0b:20000:0:99999:7::1:
nobody:*:20000:0:99999:7:::
`
	fxGroup = `root:x:0:
sudo:x:27:bob
staff:x:50:
svc:x:998:
darkpanel:x:1002:
alice:x:1000:
bob:x:1001:
`
	fxShells = `# /etc/shells: valid login shells
/bin/sh
/bin/bash
/usr/bin/bash
/bin/zsh
`
	// fxSshdConfig is sshd_config as Debian and Ubuntu ship it, in short.
	fxSshdConfig = `# This is the sshd server system-wide configuration file.

Include /etc/ssh/sshd_config.d/*.conf

KbdInteractiveAuthentication no
UsePAM yes
X11Forwarding yes
Subsystem sftp /usr/lib/openssh/sftp-server
`
	fxStatus = `root P 2024-10-01 0 99999 7 -1
daemon L 2024-10-01 0 99999 7 -1
svc L 2024-10-01 0 99999 7 -1
darkpanel L 2024-10-01 0 99999 7 -1
alice L 2024-10-01 0 99999 7 -1
bob P 2024-10-01 0 99999 7 -1
nobody L 2024-10-01 0 99999 7 -1
`
)

var (
	adminP = core.Principal{Actor: "user:admin", UserID: "admin", Scope: core.ScopeAdmin, Source: "192.0.2.1"}
	today  = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
)

// rig is one host: its files, the answers of allowlisted commands with what
// each does to the files, and one ordered log of what the module did.
type rig struct {
	t       *testing.T
	events  []string
	stdin   map[string]string
	answers map[string]core.CmdResult
	live    map[string]func() core.CmdResult // answers computed when the command runs, where answers has none
	effects map[string]func()
	files   map[string]string
	keys    map[string]string // authorized_keys per user
	homeErr map[string]error  // a HomeFiles refusal per user
	clock   *coretest.FakeClock
	sshd    string          // the ActiveState of ssh.service
	owns    map[string]bool // "<module> <path>" the ownership store records
}

func newRig(t *testing.T) *rig {
	r := &rig{
		t: t, stdin: map[string]string{}, answers: map[string]core.CmdResult{}, live: map[string]func() core.CmdResult{},
		effects: map[string]func(){},
		files: map[string]string{
			passwdFile: fxPasswd, shadowFile: fxShadow, groupFile: fxGroup, shellsFile: fxShells, sshdConfig: fxSshdConfig,
		},
		keys: map[string]string{}, homeErr: map[string]error{},
		clock: coretest.NewFakeClock(today), sshd: "active",
	}
	r.answer(platform.PasswdPath, platform.PasswdStatusAll(), fxStatus)
	r.answer(platform.SshdPath, platform.SshdEffective(), "port 22\nauthorizedkeysfile .ssh/authorized_keys .ssh/authorized_keys2\npubkeyauthentication yes\n")
	return r
}

func key(path string, args []string) string {
	return strings.Join(append([]string{path}, args...), " ")
}

// answer sets the output of a command, and then() what it does to the host.
func (r *rig) answer(path string, args []string, stdout string, then ...func()) {
	k := key(path, args)
	r.answers[k] = core.CmdResult{Stdout: []byte(stdout)}
	if len(then) > 0 {
		r.effects[k] = then[0]
	}
}

// edit replaces old with repl in a file of the rig.
func (r *rig) edit(file, old, repl string) {
	r.t.Helper()
	if !strings.Contains(r.files[file], old) {
		r.t.Fatalf("%s holds no %q", file, old)
	}
	r.files[file] = strings.Replace(r.files[file], old, repl, 1)
}

func (r *rig) module() module {
	return module{d: deps{
		readFile: func(name string) ([]byte, error) {
			b, ok := r.files[name]
			if !ok {
				return nil, fs.ErrNotExist
			}
			return []byte(b), nil
		},
		glob: func(pattern string) ([]string, error) {
			var out []string
			for name := range r.files {
				if ok, err := path.Match(pattern, name); err != nil {
					return nil, err
				} else if ok {
					out = append(out, name)
				}
			}
			slices.Sort(out)
			return out, nil
		},
	}}
}

func (r *rig) env(p core.Principal) core.Env {
	return core.Env{HostID: "h1", Principal: p, Exec: runner{r}, Files: files{r: r}, Clock: r.clock, Home: homes{r},
		Units: units{r: r}, Log: slog.New(slog.DiscardHandler)}
}

// runner answers the commands the rig knows, refusing any vector the
// agent's exec allowlist would refuse.
type runner struct{ r *rig }

func (x runner) Run(_ context.Context, cmd core.Cmd) (core.CmdResult, error) {
	k := key(cmd.Path, cmd.Args)
	allowed := false
	for _, rule := range platform.ProductionRules() {
		if rule.Path == cmd.Path && rule.Match(cmd.Args) == nil {
			allowed = true
		}
	}
	if !allowed {
		x.r.t.Errorf("%q is not in the exec allowlist", k)
		return core.CmdResult{}, fmt.Errorf("refused %q", k)
	}
	x.r.events = append(x.r.events, "run "+k)
	if len(cmd.Stdin) > 0 {
		x.r.stdin[k] += string(cmd.Stdin)
	}
	res, ok := x.r.answers[k]
	if live := x.r.live[k]; !ok && live != nil {
		res, ok = live(), true
	}
	if !ok {
		x.r.t.Errorf("unexpected command %q", k)
		return core.CmdResult{ExitCode: 1}, nil
	}
	if f := x.r.effects[k]; f != nil {
		f()
	}
	return res, nil
}

func (runner) Stream(context.Context, string, []string, func([]byte) error) error {
	return fmt.Errorf("no streams")
}

// units is the rig's unit client: ssh.service alone.
type units struct {
	core.Units
	r *rig
}

func (u units) State(_ context.Context, unit string) (core.UnitState, error) {
	if unit != sshUnit {
		return core.UnitState{}, fmt.Errorf("state of %s: not the module's unit", unit)
	}
	return core.UnitState{LoadState: "loaded", ActiveState: u.r.sshd}, nil
}

func (u units) ReloadUnit(_ context.Context, unit string) error {
	u.r.events = append(u.r.events, "reload "+unit)
	return nil
}

// files is the rig's FileWriter: the module's managed directory and its
// sshd drop-in.
type files struct {
	core.FileWriter
	r *rig
}

func (f files) Owns(_ context.Context, module, path string) (bool, error) {
	return f.r.owns[module+" "+path], nil
}

func (f files) WriteManaged(_ context.Context, module, dir, name string, data []byte, mode fs.FileMode, uid, gid int) error {
	if module != ID || dir != stateDir || mode != 0o600 || uid != 0 || gid != 0 {
		return fmt.Errorf("write %s %s/%s %v %d:%d: not the module's record", module, dir, name, mode, uid, gid)
	}
	f.r.events = append(f.r.events, "write "+name+" "+string(data))
	f.r.files[dir+"/"+name] = string(data)
	return nil
}

func (f files) WriteFile(_ context.Context, module, name string, data []byte, mode fs.FileMode) error {
	if module != ID || name != dropInFile || mode != 0o644 {
		return fmt.Errorf("write %s %s %v: not the module's file", module, name, mode)
	}
	f.r.events = append(f.r.events, "write "+name)
	f.r.files[name] = string(data)
	return nil
}

func (f files) Remove(_ context.Context, module, name string) error {
	if module != ID || name != dropInFile {
		return fmt.Errorf("remove %s %s: not the module's file", module, name)
	}
	f.r.events = append(f.r.events, "remove "+name)
	delete(f.r.files, name)
	return nil
}

func (f files) RemoveManaged(_ context.Context, module, dir, name string) error {
	if module != ID || dir != stateDir {
		return fmt.Errorf("remove %s %s/%s: not the module's record", module, dir, name)
	}
	f.r.events = append(f.r.events, "remove "+name)
	delete(f.r.files, dir+"/"+name)
	return nil
}

// homes is the rig's HomeFiles.
type homes struct{ r *rig }

func (h homes) ReadAuthorizedKeys(_ context.Context, user string) ([]byte, error) {
	if err := h.r.homeErr[user]; err != nil {
		return nil, err
	}
	b, ok := h.r.keys[user]
	if !ok {
		return nil, nil
	}
	return []byte(b), nil
}

func (h homes) WriteAuthorizedKeys(_ context.Context, user string, data []byte) error {
	if err := h.r.homeErr[user]; err != nil {
		return err
	}
	h.r.events = append(h.r.events, "write keys "+user)
	h.r.keys[user] = string(data)
	return nil
}

func wantCode(t *testing.T, what string, err error, code errcode.Code) {
	t.Helper()
	if got := errcode.From(err).Code; err == nil || got != code {
		t.Errorf("%s: err = %v (%s), want %s", what, err, got, code)
	}
}

func TestListAccounts(t *testing.T) {
	r := newRig(t)
	got, err := lister{m: r.module()}.Read(context.Background(), r.env(adminP), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	want := []Account{
		{Name: "root", UID: 0, Home: "/root", Shell: "/bin/bash", Groups: []string{"root"}, PasswordSet: true},
		{Name: "alice", UID: 1000, Home: "/home/alice", Shell: "/bin/bash", Groups: []string{"alice"}},
		{Name: "bob", UID: 1001, Home: "/home/bob", Shell: "/bin/zsh", Groups: []string{"bob", "sudo"}, Sudo: true,
			Locked: true, PasswordSet: true},
	}
	if !reflect.DeepEqual(got.Accounts, want) {
		t.Errorf("accounts\n got %+v\nwant %+v", got.Accounts, want)
	}

	// An expiry later than today is not a lock; today's is.
	r.edit(shadowFile, "alice:!:20000:0:99999:7:::", fmt.Sprintf("alice:!:20000:0:99999:7::%d:", today.Unix()/86400+1))
	r.edit(shadowFile, "bob:$y$j9T$b0b:20000:0:99999:7::1:", fmt.Sprintf("bob:$y$j9T$b0b:20000:0:99999:7::%d:", today.Unix()/86400))
	got, err = lister{m: r.module()}.Read(context.Background(), r.env(adminP), struct{}{})
	if err != nil || got.Accounts[1].Locked || !got.Accounts[2].Locked {
		t.Errorf("alice expiring tomorrow and bob today: %+v, %v", got.Accounts, err)
	}
}

func TestCreateValidation(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	h := createHandler{m: r.module()}
	for name, why := range map[string]string{
		"darkpanel":             "Darkpanel's own system user",
		"darkpanel-derp":        "Darkpanel's own system user",
		"root":                  "exists",
		"alice":                 "exists",
		"staff":                 "a group named staff exists",
		"Alice":                 "an account name is",
		"1x":                    "an account name is",
		strings.Repeat("a", 33): "an account name is",
		"-x":                    "an account name is",
		"carol\n":               "an account name is",
	} {
		_, err := h.Plan(ctx, r.env(adminP), CreateInput{Name: name})
		wantCode(t, "create "+name, err, errcode.InvalidInput)
		if err != nil && !strings.Contains(err.Error(), why) {
			t.Errorf("create %q: %v, want it to say %q", name, err, why)
		}
	}
	_, err := h.Plan(ctx, r.env(adminP), CreateInput{Name: "carol", Shell: "/bin/fish"})
	wantCode(t, "a shell not in /etc/shells", err, errcode.InvalidInput)
	if _, err := h.Plan(ctx, r.env(adminP), CreateInput{Name: strings.Repeat("a", 32)}); err != nil {
		t.Errorf("a 32-character name: %v", err)
	}

	in := CreateInput{Name: "carol", Sudo: true, Password: "correct horse"}
	plan, err := h.Plan(ctx, r.env(adminP), in)
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := []string{
		"+ Create the account carol with a home directory, its own group and the shell /bin/bash",
		"+ Add carol to sudo", "+ Set a password for carol",
	}
	if got := diffText(plan.Diff); !slices.Equal(got, wantDiff) {
		t.Errorf("plan diff %q, want %q", got, wantDiff)
	}
	useradd := platform.LoginUseradd("carol", "/bin/bash")
	r.answer(platform.UseraddPath, useradd, "", func() {
		r.files[passwdFile] += "carol:x:1003:1003::/home/carol:/bin/bash\n"
		r.files[shadowFile] += "carol:!:20418:0:99999:7:::\n"
		r.files[groupFile] += "carol:x:1003:\n"
	})
	r.answer(platform.GpasswdPath, platform.GpasswdSudo("carol", true), "", func() { r.edit(groupFile, "sudo:x:27:bob", "sudo:x:27:bob,carol") })
	r.answer(platform.ChpasswdPath, platform.Chpasswd(), "", func() {
		r.answers[key(platform.PasswdPath, platform.PasswdStatusAll())] = core.CmdResult{Stdout: []byte(fxStatus + "carol P 2026-11-26 0 99999 7 -1\n")}
	})
	if _, err := h.Apply(ctx, r.env(adminP), in); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"run " + key(platform.UseraddPath, []string{"--create-home", "--user-group", "--shell", "/bin/bash", "--", "carol"}),
		"run " + key(platform.GpasswdPath, []string{"--add", "carol", "sudo"}),
		"run " + platform.ChpasswdPath,
	}
	if !slices.Equal(r.events, want) {
		t.Errorf("events\n got %q\nwant %q", r.events, want)
	}
	if got := r.stdin[platform.ChpasswdPath]; got != "carol:correct horse\n" {
		t.Errorf("chpasswd read %q", got)
	}
	if err := h.Verify(ctx, r.env(adminP), in, Changed{}); err != nil {
		t.Errorf("verify: %v", err)
	}

	// Without sudo and a password, create runs useradd alone; the account
	// exists now, so a second create is refused.
	r.events = nil
	dave := CreateInput{Name: "dave", Shell: "/bin/zsh"}
	r.answer(platform.UseraddPath, platform.LoginUseradd("dave", "/bin/zsh"), "")
	if _, err := h.Apply(ctx, r.env(adminP), dave); err != nil || len(r.events) != 1 {
		t.Errorf("create dave: events %q, %v", r.events, err)
	}
	_, err = h.Plan(ctx, r.env(adminP), in)
	wantCode(t, "create carol again", err, errcode.InvalidInput)
}

func diffText(d []core.DiffLine) []string {
	out := make([]string, len(d))
	for i, l := range d {
		out[i] = l.Op + " " + l.Text
	}
	return out
}

func TestPasswordRules(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	bad := map[string]string{
		"a line feed":       "abcd\nroot:x",
		"a carriage return": "abcdefgh\r",
		"a tab":             "abcd\tefgh",
		"7 characters":      "abcdefg",
		"257 characters":    strings.Repeat("a", 257),
		"not UTF-8":         "abcdefgh\xff",
	}
	for why, pw := range bad {
		_, err := passwordHandler{m: r.module()}.Plan(ctx, r.env(adminP), PasswordInput{Name: "alice", Password: pw})
		wantCode(t, "password.set with "+why, err, errcode.InvalidInput)
		if err != nil && strings.Contains(err.Error(), pw) {
			t.Errorf("the password.set refusal %q repeats the password", err)
		}
		_, err = createHandler{m: r.module()}.Plan(ctx, r.env(adminP), CreateInput{Name: "carol", Password: pw})
		wantCode(t, "create with a password with "+why, err, errcode.InvalidInput)
		if err != nil && strings.Contains(err.Error(), pw) {
			t.Errorf("the refusal %q repeats the password", err)
		}
	}
	for _, pw := range []string{"abcdefgh", strings.Repeat("é", 256)} {
		if _, err := (passwordHandler{m: r.module()}).Plan(ctx, r.env(adminP), PasswordInput{Name: "alice", Password: pw}); err != nil {
			t.Errorf("a password of %d characters: %v", len([]rune(pw)), err)
		}
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[PasswordInput](), reflect.TypeFor[CreateInput]()} {
		f, _ := typ.FieldByName("Password")
		if f.Tag.Get("secret") != core.SecretTagErase {
			t.Errorf("%s.Password is tagged %q, want erase", typ.Name(), f.Tag.Get("secret"))
		}
	}

	// Through the agent's engine, neither password reaches the plan, the
	// change rows, the journal, the log or the sealed input once the change
	// has ended.
	//nolint:gosec // G101: test passwords the test looks for
	const setPW, createPW = "Tr0ub4dor&3-set", "c0rrect-horse-create"
	var logs bytes.Buffer
	eng, db, journal := engine(t, r, &logs)
	r.answer(platform.ChpasswdPath, platform.Chpasswd(), "", func() {
		r.answers[key(platform.PasswdPath, platform.PasswdStatusAll())] = core.CmdResult{
			Stdout: []byte(strings.Replace(fxStatus, "alice L", "alice P", 1) + "carol P 2026-11-26 0 99999 7 -1\n")}
	})
	r.answer(platform.UseraddPath, platform.LoginUseradd("carol", "/bin/bash"), "", func() {
		r.files[passwdFile] += "carol:x:1003:1003::/home/carol:/bin/bash\n"
		r.files[shadowFile] += "carol:!:20418:0:99999:7:::\n"
	})
	var views []string
	for _, c := range [][2]string{
		{"users.password.set", `{"name":"alice","password":"` + setPW + `"}`},
		{"users.user.create", `{"name":"carol","password":"` + createPW + `"}`},
	} {
		action, input := c[0], c[1]
		pv, err := eng.Plan(ctx, adminP, core.ActionRef{Name: action, Version: 1}, json.RawMessage(input), core.PlanOptions{})
		if err != nil {
			t.Fatalf("plan %s: %v", action, err)
		}
		cv, err := eng.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{})
		if err != nil || cv.State != core.StateCompleted {
			t.Fatalf("apply %s: %+v, %v", action, cv, err)
		}
		b, _ := json.Marshal([]any{pv, cv})
		views = append(views, string(b))
		var ct []byte
		if err := db.Read().QueryRow("SELECT input_ct FROM core_plans WHERE id = ?", pv.PlanID).Scan(&ct); err != nil || ct != nil {
			t.Errorf("%s: the plan row still holds a sealed input (%v)", action, err)
		}
	}
	stored := strings.Join(views, "\n") + logs.String()
	for _, e := range journal.Entries() {
		stored += string(e.Input)
	}
	rows, err := db.Read().Query("SELECT input_json FROM core_changes")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		stored += s
	}
	_ = rows.Close()
	for _, pw := range []string{setPW, createPW} {
		if strings.Contains(stored, pw) {
			t.Errorf("a password reached the plan, change, journal or log: %s", stored)
		}
	}
	if got, want := r.stdin[platform.ChpasswdPath], "alice:"+setPW+"\ncarol:"+createPW+"\n"; got != want {
		t.Errorf("chpasswd read %q, want %q", got, want)
	}
}

// engine is the agent's lifecycle engine over the rig, logging to logs.
func engine(t *testing.T, r *rig, logs *bytes.Buffer) (*lifecycle.Engine, *store.DB, *coretest.MemJournal) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideAgent, BinaryVersion: "test", BackupRoot: filepath.Join(t.TempDir(), "backups"), Keep: 5, Now: time.Now(),
	}, set, nil); err != nil {
		t.Fatal(err)
	}
	reg, err := core.NewRegistry(r.module())
	if err != nil {
		t.Fatal(err)
	}
	journal := &coretest.MemJournal{}
	env := r.env(adminP)
	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideAgent, Registry: reg, Validator: schema.NewValidator(), Journal: journal, Redact: schema.Redact,
		ModuleUnits: func(string) core.Units { return env.Units },
		Base: core.Env{HostID: "h1", DB: db, Exec: env.Exec, Files: env.Files, Home: env.Home, Sealer: coretest.Sealer{}, Clock: r.clock,
			Log: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	return eng, db, journal
}

func TestLockUnlock(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	lock, unlock := lockHandler{m: r.module(), lock: true}, lockHandler{m: r.module()}
	alice := NameInput{Name: "alice"}
	r.edit(shadowFile, "alice:!:20000:0:99999:7:::", "alice:!:20000:0:99999:7::20819:") // 2027-01-01
	before := r.files[shadowFile]

	plan, err := lock.Plan(ctx, r.env(adminP), alice)
	if err != nil || len(plan.Diff) != 1 || plan.Diff[0].Op != "-" {
		t.Fatalf("lock plan %+v, %v", plan, err)
	}
	r.answer(platform.UsermodPath, platform.UsermodExpiry("alice", "1"), "", func() { r.edit(shadowFile, "::20819:", "::1:") })
	if _, err := lock.Apply(ctx, r.env(adminP), alice); err != nil {
		t.Fatal(err)
	}
	want := []string{"write expiry-alice 2027-01-01", "run " + key(platform.UsermodPath, []string{"--expiredate", "1", "--", "alice"})}
	if !slices.Equal(r.events, want) {
		t.Errorf("lock events %q, want %q", r.events, want)
	}
	if err := lock.Verify(ctx, r.env(adminP), alice, Changed{}); err != nil {
		t.Errorf("verify lock: %v", err)
	}
	_, err = lock.Plan(ctx, r.env(adminP), alice)
	wantCode(t, "lock a locked account", err, errcode.InvalidInput)

	r.events = nil
	plan, err = unlock.Plan(ctx, r.env(adminP), alice)
	if err != nil || diffText(plan.Diff)[0] != "+ Unlock alice; it expires on 2027-01-01" {
		t.Fatalf("unlock plan %+v, %v", plan, err)
	}
	r.answer(platform.UsermodPath, platform.UsermodExpiry("alice", "2027-01-01"), "", func() { r.edit(shadowFile, "::1:", "::20819:") })
	if _, err := unlock.Apply(ctx, r.env(adminP), alice); err != nil {
		t.Fatal(err)
	}
	want = []string{"run " + key(platform.UsermodPath, []string{"--expiredate", "2027-01-01", "--", "alice"}), "remove expiry-alice"}
	if !slices.Equal(r.events, want) {
		t.Errorf("unlock events %q, want %q", r.events, want)
	}
	if r.files[shadowFile] != before {
		t.Errorf("shadow after lock and unlock\n%s\nwant\n%s", r.files[shadowFile], before)
	}
	if err := unlock.Verify(ctx, r.env(adminP), alice, Changed{}); err != nil {
		t.Errorf("verify unlock: %v", err)
	}
	_, err = unlock.Plan(ctx, r.env(adminP), alice)
	wantCode(t, "unlock an account that is not locked", err, errcode.InvalidInput)

	// bob was locked without a record: unlock clears the expiry. Neither
	// lock nor unlock touches a hash.
	r.events = nil
	r.answer(platform.UsermodPath, platform.UsermodExpiry("bob", ""), "")
	if _, err := unlock.Apply(ctx, r.env(adminP), NameInput{Name: "bob"}); err != nil {
		t.Fatal(err)
	}
	if want := "run " + key(platform.UsermodPath, []string{"--expiredate", "", "--", "bob"}); r.events[0] != want {
		t.Errorf("unlock bob ran %q, want %q", r.events, want)
	}
	for _, e := range r.events {
		if strings.Contains(e, "--password") || strings.Contains(e, "chpasswd") {
			t.Errorf("lock or unlock touched a hash: %q", e)
		}
	}

	// password.remove sets the hash to *, and plans nothing once it is.
	r.events = nil
	rm := noPasswordHandler{m: r.module()}
	r.answer(platform.UsermodPath, platform.UsermodNoPassword("alice"), "", func() { r.edit(shadowFile, "alice:!:", "alice:*:") })
	if plan, err := rm.Plan(ctx, r.env(adminP), alice); err != nil || len(plan.Diff) != 1 {
		t.Fatalf("password.remove plan %+v, %v", plan, err)
	}
	if _, err := rm.Apply(ctx, r.env(adminP), alice); err != nil {
		t.Fatal(err)
	}
	if want := "run " + key(platform.UsermodPath, []string{"--password", "*", "--", "alice"}); !slices.Equal(r.events, []string{want}) {
		t.Errorf("password.remove ran %q, want %q", r.events, want)
	}
	if err := rm.Verify(ctx, r.env(adminP), alice, Changed{}); err != nil {
		t.Errorf("verify password.remove: %v", err)
	}
	if plan, err := rm.Plan(ctx, r.env(adminP), alice); err != nil || len(plan.Diff) != 0 {
		t.Errorf("password.remove of a removed password: %+v, %v", plan, err)
	}

	_, err = lock.Plan(ctx, r.env(adminP), NameInput{Name: "root"})
	wantCode(t, "lock root", err, errcode.InvalidInput)
	for _, name := range []string{"svc", "darkpanel", "nobody", "zed"} {
		_, err = lock.Plan(ctx, r.env(adminP), NameInput{Name: name})
		wantCode(t, "lock "+name, err, errcode.InvalidInput)
	}
}

func TestUnlockPastExpiry(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		now  time.Time
		want string
	}{
		{time.Date(2026, 12, 5, 9, 0, 0, 0, time.UTC), "+ Unlock alice; its recorded expiry 2026-12-01 has passed, so the account stays expired"},
		{time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC), "+ Unlock alice; its recorded expiry 2026-12-01 has passed, so the account stays expired"},
		{time.Date(2026, 11, 30, 9, 0, 0, 0, time.UTC), "+ Unlock alice; it expires on 2026-12-01"},
	} {
		r := newRig(t)
		r.clock = coretest.NewFakeClock(c.now)
		r.edit(shadowFile, "alice:!:20000:0:99999:7:::", "alice:!:20000:0:99999:7::1:")
		r.files[stateDir+"/"+recordName("alice")] = "2026-12-01\n"
		plan, err := lockHandler{m: r.module()}.Plan(ctx, r.env(adminP), NameInput{Name: "alice"})
		if err != nil || len(plan.Diff) != 1 || diffText(plan.Diff)[0] != c.want {
			t.Errorf("unlock plan on %s: %+v, %v; want %q", c.now.Format(time.DateOnly), plan, err, c.want)
		}
	}
}

func TestCreateHalfMade(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		step string
		in   CreateInput
		fail func(r *rig)
	}{
		{"password", CreateInput{Name: "carol", Password: "abcdefgh1"}, func(r *rig) {
			r.answers[key(platform.ChpasswdPath, platform.Chpasswd())] = core.CmdResult{ExitCode: 1, Stderr: []byte("chpasswd: PAM failure")}
		}},
		{"sudo", CreateInput{Name: "carol", Sudo: true}, func(r *rig) {
			r.answers[key(platform.GpasswdPath, platform.GpasswdSudo("carol", true))] = core.CmdResult{ExitCode: 3, Stderr: []byte("gpasswd: no group")}
		}},
	} {
		r := newRig(t)
		r.answer(platform.UseraddPath, platform.LoginUseradd("carol", "/bin/bash"), "", func() {
			r.files[passwdFile] += "carol:x:1003:1003::/home/carol:/bin/bash\n"
			r.files[shadowFile] += "carol:!:20418:0:99999:7:::\n"
		})
		c.fail(r)
		_, err := createHandler{m: r.module()}.Apply(ctx, r.env(adminP), c.in)
		e := errcode.From(err)
		if err == nil || e.Code == errcode.Internal ||
			!strings.Contains(e.Detail, "carol was created, but setting its "+c.step+" failed") ||
			!strings.Contains(e.Detail, "Finish it from the user's page, or delete it") {
			t.Errorf("create with a failing %s step: %v; want a visible detail saying carol was created", c.step, err)
		}
		if strings.Contains(err.Error(), "abcdefgh1") {
			t.Errorf("the error %q repeats the password", err)
		}
	}
}

func TestUIDCheckBeforeExec(t *testing.T) {
	ctx := context.Background()
	alice := NameInput{Name: "alice"}
	type step func(m module, env core.Env) error
	for action, c := range map[string]struct{ plan, apply step }{
		"lock": {
			func(m module, env core.Env) error {
				_, err := lockHandler{m: m, lock: true}.Plan(ctx, env, alice)
				return err
			},
			func(m module, env core.Env) error {
				_, err := lockHandler{m: m, lock: true}.Apply(ctx, env, alice)
				return err
			},
		},
		"password.remove": {
			func(m module, env core.Env) error {
				_, err := noPasswordHandler{m: m}.Plan(ctx, env, alice)
				return err
			},
			func(m module, env core.Env) error {
				_, err := noPasswordHandler{m: m}.Apply(ctx, env, alice)
				return err
			},
		},
		"sudo.add": {
			func(m module, env core.Env) error {
				_, err := sudoHandler{m: m, add: true}.Plan(ctx, env, alice)
				return err
			},
			func(m module, env core.Env) error {
				_, err := sudoHandler{m: m, add: true}.Apply(ctx, env, alice)
				return err
			},
		},
		"password.set": {
			func(m module, env core.Env) error {
				_, err := passwordHandler{m: m}.Plan(ctx, env, PasswordInput{Name: "alice", Password: "abcdefgh"})
				return err
			},
			func(m module, env core.Env) error {
				_, err := passwordHandler{m: m}.Apply(ctx, env, PasswordInput{Name: "alice", Password: "abcdefgh"})
				return err
			},
		},
		"delete": {
			func(m module, env core.Env) error {
				_, err := deleteHandler{m: m}.Plan(ctx, env, DeleteInput{Name: "alice"})
				return err
			},
			func(m module, env core.Env) error {
				_, err := deleteHandler{m: m}.Apply(ctx, env, DeleteInput{Name: "alice"})
				return err
			},
		},
	} {
		r := newRig(t)
		if err := c.plan(r.module(), r.env(adminP)); err != nil {
			t.Fatalf("%s plan: %v", action, err)
		}
		r.events = nil // the plan's reads; the guard runs sshd -T and passwd -S
		r.edit(passwdFile, "alice:x:1000:", "alice:x:999:")
		wantCode(t, action+" apply after the UID changed", c.apply(r.module(), r.env(adminP)), errcode.InvalidInput)
		if len(r.events) != 0 {
			t.Errorf("%s apply ran %q after the UID changed", action, r.events)
		}
	}

	// create checks the new account before each command after useradd.
	r := newRig(t)
	r.answer(platform.UseraddPath, platform.LoginUseradd("carol", "/bin/bash"), "", func() {
		r.files[passwdFile] += "carol:x:60001:60001::/home/carol:/bin/bash\n"
	})
	_, err := createHandler{m: r.module()}.Apply(ctx, r.env(adminP), CreateInput{Name: "carol", Sudo: true})
	wantCode(t, "create given a UID outside 1000-59999", err, errcode.InvalidInput)
	if want := []string{"run " + key(platform.UseraddPath, platform.LoginUseradd("carol", "/bin/bash"))}; !slices.Equal(r.events, want) {
		t.Errorf("events %q, want useradd alone", r.events)
	}
}

func TestStepUpActions(t *testing.T) {
	r := newRig(t)
	specs := map[string]core.ActionSpec{}
	for _, a := range r.module().Manifest().Actions {
		specs[a.Spec.Name] = a.Spec
	}
	for name, step := range map[string]bool{
		"users.user.create": true, "users.password.set": true, "users.sudo.add": true,
		"users.user.lock": false, "users.user.unlock": false, "users.user.delete": false,
		"users.password.remove": false, "users.sudo.remove": false, "users.user.list": false,
	} {
		s, ok := specs[name]
		switch {
		case !ok:
			t.Errorf("%s is not declared", name)
		case s.StepUp != step:
			t.Errorf("%s StepUp %v, want %v", name, s.StepUp, step)
		case name != "users.user.list" && (s.MinScope != core.ScopeAdmin || !slices.Equal(s.LockKeys, []string{"users"})):
			t.Errorf("%s scope %q locks %v, want admin and users", name, s.MinScope, s.LockKeys)
		}
	}
	for name, by := range map[string]string{
		"users.user.create": "users.user.delete", "users.user.lock": "users.user.unlock", "users.user.unlock": "users.user.lock",
		"users.sudo.add": "users.sudo.remove", "users.sudo.remove": "users.sudo.add",
	} {
		if s := specs[name]; s.Class != core.ClassCompensable || s.CompensatedBy != by {
			t.Errorf("%s class %s compensated by %q, want compensable by %s", name, s.Class, s.CompensatedBy, by)
		}
	}
	for _, name := range []string{"users.password.set", "users.password.remove"} {
		if s := specs[name]; s.Class != core.ClassCompensable || !s.ForwardOnly || !s.Converges {
			t.Errorf("%s %+v, want compensable, ForwardOnly and Converges", name, s)
		}
	}
	if s := specs["users.user.delete"]; s.Class != core.ClassIrreversible {
		t.Errorf("delete class %s, want irreversible", s.Class)
	}
	if _, err := core.NewRegistry(r.module()); err != nil {
		t.Errorf("registry: %v", err)
	}
}

// TestDeleteWarnsJobs warns, in the plan of deleting an account, of every
// Darkpanel job that runs as it; a job unit Darkpanel did not write is not
// one.
func TestDeleteWarnsJobs(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	const backup, other, foreign = "/etc/systemd/system/darkpanel-job-backup.service",
		"/etc/systemd/system/darkpanel-job-report.service", "/etc/systemd/system/darkpanel-job-planted.service"
	r.files[backup] = "[Service]\nType=oneshot\nUser=alice\nExecStart=/bin/sh /var/lib/darkpanel/jobs/backup.sh\n"
	r.files[other] = "[Service]\nUser=bob\n"
	r.files[foreign] = "[Service]\nUser=alice\n"
	r.owns = map[string]bool{"jobs " + backup: true, "jobs " + other: true}
	plan, err := deleteHandler{m: r.module()}.Plan(ctx, r.env(adminP), DeleteInput{Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"- Delete the account alice", "  Keep /home/alice", "  Job backup runs as alice and fails once the account is gone"}
	if got := diffText(plan.Diff); !slices.Equal(got, want) {
		t.Errorf("diff %q, want %q", got, want)
	}
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	h := deleteHandler{m: r.module()}
	plan, err := h.Plan(ctx, r.env(adminP), DeleteInput{Name: "alice"})
	if err != nil || plan.Confirmation != "alice" || !slices.Equal(diffText(plan.Diff), []string{"- Delete the account alice", "  Keep /home/alice"}) {
		t.Fatalf("plan %+v, %v", plan, err)
	}
	plan, err = h.Plan(ctx, r.env(adminP), DeleteInput{Name: "alice", RemoveHome: true})
	if err != nil || diffText(plan.Diff)[1] != "- Delete /home/alice and the account's mail spool" {
		t.Fatalf("plan with the home %+v, %v", plan, err)
	}
	_, err = h.Plan(ctx, r.env(adminP), DeleteInput{Name: "root"})
	wantCode(t, "delete root", err, errcode.InvalidInput)

	// A running process refuses the delete with userdel's status 8.
	r.answers[key(platform.UserdelPath, platform.LoginUserdel("bob", false))] = core.CmdResult{ExitCode: 8,
		Stderr: []byte("userdel: user bob is currently used by process 4242\n")}
	_, err = h.Apply(ctx, r.env(adminP), DeleteInput{Name: "bob"})
	wantCode(t, "delete an account in use", err, errcode.InvalidInput)
	if err != nil && !core.IsDefinite(err) {
		t.Errorf("the in-use refusal %v is not definite", err)
	}

	r.events = nil
	r.answer(platform.UserdelPath, platform.LoginUserdel("alice", true), "", func() {
		r.edit(passwdFile, "alice:x:1000:1000:Alice:/home/alice:/bin/bash\n", "")
	})
	if _, err := h.Apply(ctx, r.env(adminP), DeleteInput{Name: "alice", RemoveHome: true}); err != nil {
		t.Fatal(err)
	}
	want := []string{"run " + key(platform.UserdelPath, []string{"--remove", "--", "alice"}), "remove expiry-alice"}
	if !slices.Equal(r.events, want) {
		t.Errorf("events %q, want %q", r.events, want)
	}
	if err := h.Verify(ctx, r.env(adminP), DeleteInput{Name: "alice"}, Changed{}); err != nil {
		t.Errorf("verify: %v", err)
	}
	r.events = nil
	r.answer(platform.UserdelPath, platform.LoginUserdel("bob", false), "")
	if _, err := h.Apply(ctx, r.env(adminP), DeleteInput{Name: "bob"}); err != nil || r.events[0] != "run "+key(platform.UserdelPath, []string{"--", "bob"}) {
		t.Errorf("delete bob kept the home? events %q, %v", r.events, err)
	}
}

func TestSudo(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	add, remove := sudoHandler{m: r.module(), add: true}, sudoHandler{m: r.module()}
	_, err := add.Plan(ctx, r.env(adminP), NameInput{Name: "bob"})
	wantCode(t, "add bob, in sudo already", err, errcode.InvalidInput)
	_, err = remove.Plan(ctx, r.env(adminP), NameInput{Name: "alice"})
	wantCode(t, "remove alice, not in sudo", err, errcode.InvalidInput)
	r.answer(platform.GpasswdPath, platform.GpasswdSudo("bob", false), "", func() { r.edit(groupFile, "sudo:x:27:bob", "sudo:x:27:") })
	if _, err := remove.Apply(ctx, r.env(adminP), NameInput{Name: "bob"}); err != nil {
		t.Fatal(err)
	}
	if err := remove.Verify(ctx, r.env(adminP), NameInput{Name: "bob"}, Changed{}); err != nil {
		t.Errorf("verify: %v", err)
	}
	if err := add.Verify(ctx, r.env(adminP), NameInput{Name: "bob"}, Changed{}); err == nil {
		t.Error("verify of an add the group does not show passed")
	}
}
