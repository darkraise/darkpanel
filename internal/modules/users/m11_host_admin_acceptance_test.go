package users_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/users"
	"github.com/darkraise/darkpanel/internal/platform"
)

type runner struct{ calls []core.Cmd }

func (r *runner) Run(_ context.Context, c core.Cmd) (core.CmdResult, error) {
	r.calls = append(r.calls, c)
	switch c.Path {
	case platform.SshdPath:
		return core.CmdResult{Stdout: []byte("port 22\nauthorizedkeysfile .ssh/authorized_keys .ssh/authorized_keys2\npubkeyauthentication yes\npasswordauthentication yes\nkbdinteractiveauthentication no\npermitrootlogin yes\nauthenticationmethods any\n")}, nil
	case platform.PasswdPath:
		return core.CmdResult{Stdout: []byte("root P 2024-01-01 0 99999 7 -1\n")}, nil
	}
	return core.CmdResult{}, nil
}

func (r *runner) Stream(context.Context, string, []string, func([]byte) error) error { return nil }

type home struct{ data map[string][]byte }

func (h *home) ReadAuthorizedKeys(_ context.Context, u string) ([]byte, error) { return h.data[u], nil }
func (h *home) WriteAuthorizedKeys(_ context.Context, u string, b []byte) error {
	h.data[u] = b
	return nil
}

var admin = core.Principal{Actor: "user:admin", UserID: "admin", Scope: core.ScopeAdmin, Source: "192.0.2.1"}

func env(t *testing.T, h *home) core.Env {
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
	if _, err := store.Migrate(ctx, db, store.MigrateOptions{Side: store.SideAgent, BinaryVersion: "test",
		BackupRoot: filepath.Join(t.TempDir(), "backups"), Keep: 5, Now: time.Now()}, set, nil); err != nil {
		t.Fatal(err)
	}
	return core.Env{HostID: "h1", Principal: admin, DB: db, Exec: &runner{}, Home: h}
}

func action(t *testing.T, name string) core.ActionDef {
	t.Helper()
	for _, a := range users.FixtureModule(t).Manifest().Actions {
		if a.Spec.Name == name {
			return a
		}
	}
	t.Fatalf("no action %s", name)
	return core.ActionDef{}
}

func ed25519Line(t *testing.T, comment string) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k))) + " " + comment
}

func rsaLine(t *testing.T, bits int) string {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ssh.NewPublicKey(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))) + " rsa"
}

func plan(t *testing.T, e core.Env, name string, in map[string]any) (core.PlanOutcome, error) {
	t.Helper()
	raw, _ := json.Marshal(in)
	return action(t, name).Handler.Plan(context.Background(), e, raw)
}

func TestA38_KeysWithOptionsNonKeysAndWeakRSAAreRefused(t *testing.T) {
	h := &home{data: map[string][]byte{"root": nil}}
	e := env(t, h)
	good := ed25519Line(t, "ok")
	if _, err := plan(t, e, "users.key.add", map[string]any{"name": "root", "key": good}); err != nil {
		t.Fatalf("a plain ed25519 key: %v", err)
	}
	cases := map[string]string{
		"options":      `command="/bin/false" ` + good,
		"from option":  `from="10.0.0.1" ` + good,
		"not a key":    "this is not a key line",
		"a comment":    "# ssh-ed25519 AAAA",
		"empty":        "",
		"rsa 2048":     rsaLine(t, 2048),
		"two lines":    good + "\n" + good,
		"truncated":    good[:30],
		"private line": "-----BEGIN OPENSSH PRIVATE KEY-----",
	}
	for name, key := range cases {
		if _, err := plan(t, e, "users.key.add", map[string]any{"name": "root", "key": key}); err == nil {
			t.Errorf("%s: the key was planned", name)
		}
	}
	if testing.Short() {
		return
	}
	if _, err := plan(t, e, "users.key.add", map[string]any{"name": "root", "key": rsaLine(t, 3072)}); err != nil {
		t.Errorf("rsa 3072 is allowed: %v", err)
	}
}

func TestA37_AddKeepsEveryLineAndRemoveRemovesOnlyItsOwn(t *testing.T) {
	existing := "# a comment kept\ncommand=\"/bin/true\",no-pty " + ed25519Line(t, "optioned") + "\n\n" + ed25519Line(t, "plain") + "  \n"
	h := &home{data: map[string][]byte{"root": []byte(existing)}}
	e := env(t, h)
	added := ed25519Line(t, "new")
	raw, _ := json.Marshal(map[string]any{"name": "root", "key": added})
	if _, err := action(t, "users.key.add").Handler.Apply(context.Background(), e, raw); err != nil {
		t.Fatalf("add: %v", err)
	}
	got := string(h.data["root"])
	if !strings.HasPrefix(got, existing) {
		t.Fatalf("an existing line changed:\nbefore %q\nafter  %q", existing, got)
	}
	if strings.TrimPrefix(got, existing) != added+"\n" {
		t.Errorf("appended text %q; want the key line", strings.TrimPrefix(got, existing))
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(added))
	if err != nil {
		t.Fatal(err)
	}
	rm, _ := json.Marshal(map[string]any{"name": "root", "fingerprint": ssh.FingerprintSHA256(pub)})
	if _, err := action(t, "users.key.remove").Handler.Apply(context.Background(), e, rm); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if string(h.data["root"]) != existing {
		t.Errorf("after removing the added key the file is\n%q\nwant\n%q", h.data["root"], existing)
	}
}

func TestA34_InvalidAndExistingNamesAreRefused(t *testing.T) {
	e := env(t, &home{data: map[string][]byte{}})
	bad := []string{"Root", "1abc", "a b", "-x", "a" + strings.Repeat("b", 32), "", "x;y", "ünï", "root", "daemon", "darkpanel", "darkpanel-derp"}
	for _, n := range bad {
		if _, err := plan(t, e, "users.user.create", map[string]any{"name": n}); err == nil {
			t.Errorf("create %q was planned", n)
		}
	}
	good := "qa-user_1"
	if _, err := plan(t, e, "users.user.create", map[string]any{"name": good}); err != nil {
		t.Errorf("create %q: %v", good, err)
	}
	if _, err := plan(t, e, "users.user.create", map[string]any{"name": "a" + strings.Repeat("b", 31)}); err != nil {
		t.Errorf("a 32 character name: %v", err)
	}
}

func TestA36_APasswordWithCRorLFIsRefusedAndNeverEchoed(t *testing.T) {
	e := env(t, &home{data: map[string][]byte{}})
	name := "users.password.set" // spec 3 spells it users.user.password.set
	for _, a := range users.Module().Manifest().Actions {
		if a.Spec.Name == "users.user.password.set" {
			name = a.Spec.Name
		}
	}
	for _, pw := range []string{"abcd1234\nroot:x", "abcd1234\rx", "abcd\x001234", "short", strings.Repeat("a", 257)} {
		if _, err := plan(t, e, name, map[string]any{"name": "root", "password": pw}); err == nil {
			t.Errorf("password %q was planned", pw)
		}
	}
	pw := "correct horse battery"
	out, err := plan(t, e, name, map[string]any{"name": "root", "password": pw})
	if err != nil {
		t.Fatalf("the control password was refused: %v", err)
	}
	blob, _ := json.Marshal(out)
	if strings.Contains(string(blob), pw) {
		t.Error("the plan output holds the password")
	}
}
