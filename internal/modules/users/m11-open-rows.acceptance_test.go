package users

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform"
)

func TestA11_UnlockPastItsRecordedExpiry(t *testing.T) {
	for _, c := range []struct {
		name string
		now  time.Time
		want string
	}{
		{"expiry is today", time.Date(2026, 12, 1, 8, 0, 0, 0, time.UTC), "Unlock bob; its recorded expiry 2026-12-01 has passed, so the account stays expired"},
		{"expiry is earlier", time.Date(2026, 12, 20, 8, 0, 0, 0, time.UTC), "Unlock bob; its recorded expiry 2026-12-01 has passed, so the account stays expired"},
		{"expiry is in the future", time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC), "Unlock bob; it expires on 2026-12-01"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.clock = coretest.NewFakeClock(c.now)
			r.files[stateDir+"/"+recordName("bob")] = "2026-12-01\n"
			plan, err := lockHandler{m: r.module()}.Plan(context.Background(), r.env(adminP), NameInput{Name: "bob"})
			if err != nil {
				t.Fatal(err)
			}
			lines := diffText(plan.Diff)
			if !slices.Contains(lines, "+ "+c.want) {
				t.Errorf("unlock plan %q, want the line %q", lines, c.want)
			}
		})
	}
}

func TestA12_CreateWhosePasswordStepFailsNamesTheAccount(t *testing.T) {
	r := newRig(t)
	r.answer(platform.UseraddPath, platform.LoginUseradd("carol", "/bin/bash"), "", func() {
		r.files[passwdFile] += "carol:x:1003:1003::/home/carol:/bin/bash\n"
		r.files[shadowFile] += "carol:!:20418:0:99999:7:::\n"
		r.files[groupFile] += "carol:x:1003:\n"
	})
	r.answers[key(platform.ChpasswdPath, platform.Chpasswd())] = core.CmdResult{ExitCode: 1, Stderr: []byte("chpasswd: PAM failure")}
	_, err := createHandler{m: r.module()}.Apply(context.Background(), r.env(adminP), CreateInput{Name: "carol", Password: "Tr0ub4dor&3x"})
	if err == nil {
		t.Fatal("create with a failing password step succeeded")
	}
	text := err.Error() + " | " + errcode.From(err).Detail
	for _, want := range []string{"carol", "created", "user's page", "delete"} {
		if !strings.Contains(text, want) {
			t.Errorf("the error %q does not mention %q", text, want)
		}
	}
	if strings.Contains(text, "Tr0ub4dor&3x") {
		t.Errorf("the error %q repeats the password", text)
	}
}

func TestA13_WeakPasswordRefusalsDoNotEchoThePassword(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	for why, pw := range map[string]string{
		"too short":     "Qz7!mw",
		"a line feed":   "Qz7!mwPp\nroot:x",
		"a tab":         "Qz7!mw\tPp9",
		"not UTF-8":     "Qz7!mwPp\xff9",
		"over 256 long": strings.Repeat("Qz7!", 70),
	} {
		check := func(what string, err error) {
			t.Helper()
			if err == nil {
				t.Errorf("%s with a password that is %s: no refusal", what, why)
				return
			}
			if text := err.Error() + errcode.From(err).Detail + fmt.Sprintf("%+v", err); strings.Contains(text, pw) {
				t.Errorf("%s with a password that is %s: the refusal %q contains the password", what, why, text)
			}
		}
		_, err := createHandler{m: r.module()}.Plan(ctx, r.env(adminP), CreateInput{Name: "carol", Password: pw})
		check("create plan", err)
		_, err = createHandler{m: r.module()}.Apply(ctx, r.env(adminP), CreateInput{Name: "carol", Password: pw})
		check("create apply", err)
		_, err = passwordHandler{m: r.module()}.Plan(ctx, r.env(adminP), PasswordInput{Name: "alice", Password: pw})
		check("password.set plan", err)
		_, err = passwordHandler{m: r.module()}.Apply(ctx, r.env(adminP), PasswordInput{Name: "alice", Password: pw})
		check("password.set apply", err)
	}
}

func TestA14_KeyListErrorKinds(t *testing.T) {
	// The module reads authorized_keys through the real home-file reader,
	// over a temporary home that passwd names for alice.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	uid, gid := os.Getuid(), os.Getgid()
	if uid == 0 {
		uid, gid = 4242, 4243
	}
	home := filepath.Join(dir, "home", "alice")
	if err := os.MkdirAll(home, 0o755); err != nil { //nolint:gosec // G301: a home as useradd makes it
		t.Fatal(err)
	}
	passwd := filepath.Join(dir, "passwd")
	entry := fmt.Sprintf("alice:x:%d:%d::%s:/bin/bash\n", uid, gid, home)
	if err := os.WriteFile(passwd, []byte(entry), 0o644); err != nil { //nolint:gosec // G306: a test passwd file
		t.Fatal(err)
	}
	r := newRig(t)
	env := r.env(adminP)
	env.Home = platform.NewHomeFiles(passwd)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = keyLister{m: r.module()}.Read(cancelled, env, NameInput{Name: "alice"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("key list with a cancelled context: %v, want errors.Is(context.Canceled)", err)
	}
	if err != nil && errcode.From(err).Code == errcode.InvalidInput {
		t.Errorf("key list with a cancelled context answered invalid_input: %v", err)
	}

	target := filepath.Join(dir, "elsewhere")
	if err := os.WriteFile(target, []byte("ssh-ed25519 AAAA x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, ".ssh", "authorized_keys")); err != nil {
		t.Fatal(err)
	}
	_, err = keyLister{m: r.module()}.Read(context.Background(), env, NameInput{Name: "alice"})
	if got := errcode.From(err).Code; err == nil || got != errcode.InvalidInput {
		t.Errorf("key list with a symlinked authorized_keys: %v (%s), want invalid_input", err, got)
	}
}

func TestA16_RemovingAnRSA2048KeyWarnsThatAddCannotRestoreIt(t *testing.T) {
	r := newRig(t)
	weak, good := rsaLine(t, 2048), ed25519Line(t, "good@host")
	r.keys["alice"] = weak + "\n" + good + "\n"
	remove := removeHandler{keyHandler{m: r.module()}}
	const warn = "users.key.add cannot put this key back: RSA keys need at least 3072 bits"

	plan, err := remove.Plan(context.Background(), r.env(adminP), KeyRemoveInput{Name: "alice", Fingerprint: fingerprint(t, weak)})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(plan.Diff, func(l core.DiffLine) bool { return l.Text == warn }) {
		t.Errorf("removing an RSA-2048 key plans %q, want a line %q", diffText(plan.Diff), warn)
	}
	plan, err = remove.Plan(context.Background(), r.env(adminP), KeyRemoveInput{Name: "alice", Fingerprint: fingerprint(t, good)})
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(plan.Diff, func(l core.DiffLine) bool { return strings.Contains(l.Text, "cannot put this key back") }) {
		t.Errorf("removing an ed25519 key warns that add cannot restore it: %q", diffText(plan.Diff))
	}
}

func TestA17_AnEarlierDropInThatTurnsPasswordLoginOffStillMeansNoWayIn(t *testing.T) {
	const in = `{"passwordAuthentication":true,"allowUsers":["alice"]}`
	for _, c := range []struct {
		file string
		ok   bool
	}{{"00-cloud.conf", false}, {"50-cloud.conf", true}} {
		t.Run(c.file, func(t *testing.T) {
			r := newRig(t)
			r.liveSshd()
			// alice has a password and no key.
			r.answer(platform.PasswdPath, platform.PasswdStatusAll(), strings.Replace(fxStatus, "alice L", "alice P", 1))
			r.files[dropInDir+"/"+c.file] = "PasswordAuthentication no\nKbdInteractiveAuthentication no\n"
			eng, _, _ := engine(t, r, &bytes.Buffer{})
			pv := plan(t, eng, "users.sshd.apply", in)
			var g *core.Guard
			for i := range pv.Guards {
				if pv.Guards[i].ID == "users.no_way_in" {
					g = &pv.Guards[i]
				}
			}
			if g == nil {
				t.Fatalf("no users.no_way_in guard in %+v", pv.Guards)
			}
			if g.OK != c.ok {
				t.Errorf("with %s setting password login off and a hardening change setting it on for alice alone: users.no_way_in OK = %v, want %v (message %q)", c.file, g.OK, c.ok, g.Message)
			}
		})
	}
}

func TestA18_UnreadableSshdFilesAreNamedInTheGuardText(t *testing.T) {
	r := newRig(t)
	m := r.module()
	read := m.d.readFile
	m.d.readFile = func(name string) ([]byte, error) {
		if name == sshdConfig {
			return nil, fs.ErrPermission
		}
		return read(name)
	}
	g, err := m.accountGuard(context.Background(), r.env(adminP), core.Guarded{}, "alice", lockEdit)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.Message, fs.ErrPermission.Error()) {
		t.Errorf("guard text %q does not name the read error %q", g.Message, fs.ErrPermission.Error())
	}
	if !strings.Contains(g.Message, "Match blocks were not checked") {
		t.Errorf("guard text %q does not say Match blocks were not checked", g.Message)
	}
}

func TestA19_WildmatchStarBeforeLiteral(t *testing.T) {
	for _, c := range []struct {
		pattern, s string
		want       bool
	}{
		{"*b", "*xb", true},
		{"*b", "xyb", true},
		{"*b", "xyc", false},
		{"a*", "abc", true},
		{"?b", "*b", true},
		{"*", "", true},
	} {
		if got := wildmatch(c.pattern, c.s); got != c.want {
			t.Errorf("wildmatch(%q, %q) = %v, want %v", c.pattern, c.s, got, c.want)
		}
	}
}

func TestA20_SshdGetAnswersValuesOutsideTheInputRange(t *testing.T) {
	r := newRig(t)
	r.answer(platform.SshdPath, platform.SshdEffective(),
		"maxauthtries 20\npermitrootlogin forced-commands-only\nauthorizedkeysfile .ssh/authorized_keys .ssh/authorized_keys2\n")
	st, err := sshdGetter{m: r.module()}.Read(context.Background(), r.env(adminP), struct{}{})
	if err != nil {
		t.Fatalf("users.sshd.get: %v", err)
	}
	answer, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Effective struct {
			MaxAuthTries    *int   `json:"maxAuthTries"`
			PermitRootLogin string `json:"permitRootLogin"`
		} `json:"effective"`
	}
	if err := json.Unmarshal(answer, &got); err != nil {
		t.Fatal(err)
	}
	if got.Effective.MaxAuthTries == nil || *got.Effective.MaxAuthTries != 20 {
		t.Errorf("effective maxAuthTries = %v, want 20", got.Effective.MaxAuthTries)
	}
	if got.Effective.PermitRootLogin != "forced-commands-only" {
		t.Errorf("effective permitRootLogin = %q, want forced-commands-only", got.Effective.PermitRootLogin)
	}

	// The published schema file is what clients validate against.
	raw, err := os.ReadFile("../../../schemas/users.sshd.get/v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var published struct {
		Output json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(raw, &published); err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(published.Output))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("urn:qa:users.sshd.get", doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile("urn:qa:users.sshd.get")
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(answer))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Errorf("the answer %s is not admitted by the published schema: %v", answer, err)
	}
}

// qaLog is a log sink safe for the engine's goroutines.
type qaLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *qaLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *qaLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func TestA36_ErasedSecretsLeaveNoBytesInTheDatabaseOrItsWAL(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	path := filepath.Join(t.TempDir(), "agent.db")
	db, err := store.Open(ctx, path, store.WithBusyTimeout(100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate(ctx, db, store.MigrateOptions{Side: store.SideAgent, BinaryVersion: "test",
		BackupRoot: filepath.Join(t.TempDir(), "backups"), Keep: 5, Now: time.Now()}, set, nil); err != nil {
		t.Fatal(err)
	}
	var secure int
	if err := db.Writer().QueryRowContext(ctx, "PRAGMA secure_delete").Scan(&secure); err != nil || secure != 1 {
		t.Errorf("the writer connection reports secure_delete %d (%v), want 1", secure, err)
	}

	reg, err := core.NewRegistry(r.module())
	if err != nil {
		t.Fatal(err)
	}
	env := r.env(adminP)
	logs := &qaLog{}
	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideAgent, Registry: reg, Validator: schema.NewValidator(), Journal: &coretest.MemJournal{}, Redact: schema.Redact,
		ModuleUnits: func(string) core.Units { return env.Units },
		Base: core.Env{HostID: "h1", DB: db, Exec: env.Exec, Files: env.Files, Home: env.Home, Sealer: coretest.Sealer{}, Clock: r.clock,
			Log: slog.New(slog.NewTextHandler(logs, nil))},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	r.answer(platform.ChpasswdPath, platform.Chpasswd(), "", func() {
		r.answers[key(platform.PasswdPath, platform.PasswdStatusAll())] = core.CmdResult{
			Stdout: []byte(strings.Replace(fxStatus, "alice L", "alice P", 1))}
	})

	// sealed plans users.password.set with pw and returns the plan and the
	// ciphertext of its sealed input.
	sealed := func(pw string) (core.PlanView, []byte) {
		t.Helper()
		pv, err := eng.Plan(ctx, adminP, core.ActionRef{Name: "users.password.set", Version: 1},
			json.RawMessage(`{"name":"alice","password":"`+pw+`"}`), core.PlanOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var ct []byte
		if err := db.Read().QueryRowContext(ctx, "SELECT input_ct FROM core_plans WHERE id = ?", pv.PlanID).Scan(&ct); err != nil || len(ct) == 0 {
			t.Fatalf("the plan's sealed input: %d bytes, %v", len(ct), err)
		}
		return pv, ct
	}
	onDisk := func(b []byte) []string {
		t.Helper()
		var in []string
		for _, p := range []string{path, path + "-wal"} {
			data, err := os.ReadFile(p) //nolint:gosec // G304: the test's database
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			if bytes.Contains(data, b) {
				in = append(in, filepath.Base(p))
			}
		}
		return in
	}
	purge := func() {
		t.Helper()
		if err := eng.Purge(ctx); err != nil {
			t.Fatal(err)
		}
	}
	apply := func(pv core.PlanView) {
		t.Helper()
		cv, err := eng.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{})
		if err != nil || cv.State != core.StateCompleted {
			t.Fatalf("apply: %+v, %v", cv, err)
		}
	}

	// A change that ends, then one purge pass.
	pv, ct := sealed("Qm4!pelican-first")
	if len(onDisk(ct)) == 0 { // positive control: the search can find it
		t.Fatal("the sealed input is in neither the database nor its WAL while its change is open: the search finds nothing")
	}
	apply(pv)
	purge()
	if in := onDisk(ct); len(in) > 0 {
		t.Errorf("after the password.set change ended and a purge pass the sealed input is still in %v", in)
	}

	// A purge that erased a sealed input: a plan that expired unapplied.
	_, ct = sealed("Qm4!pelican-second")
	purge() // copies the page into the database file
	r.clock.Advance(lifecycle.PlanTTL + time.Second)
	purge()
	if in := onDisk(ct); len(in) > 0 {
		t.Errorf("after the purge erased an expired plan's sealed input it is still in %v", in)
	}

	// A reader open during the pass keeps the checkpoint from completing.
	pv, ct = sealed("Qm4!pelican-third")
	if len(onDisk(ct)) == 0 {
		t.Fatal("positive control: the third sealed input is in neither file")
	}
	tx, err := db.Read().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM core_plans").Scan(&n); err != nil {
		t.Fatal(err)
	}
	apply(pv)
	purge()
	if text := strings.ToLower(logs.String()); !strings.Contains(text, "checkpoint") || !(strings.Contains(text, "not complete") || strings.Contains(text, "incomplete")) {
		t.Errorf("the log %q does not say the checkpoint was incomplete", logs.String())
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	purge()
	if in := onDisk(ct); len(in) > 0 {
		t.Errorf("after the next pass with no reader the sealed input is still in %v", in)
	}
}
