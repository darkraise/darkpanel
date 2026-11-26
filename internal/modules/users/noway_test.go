package users

import (
	"bytes"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform"
)

// guardOf plans action through the engine and returns its users.no_way_in
// guard.
func guardOf(t *testing.T, r *rig, action, input string) core.Guard {
	t.Helper()
	eng, _, _ := engine(t, r, &bytes.Buffer{})
	pv := plan(t, eng, action, input)
	for _, g := range pv.Guards {
		if g.ID == GuardNoWayIn {
			return g
		}
	}
	t.Fatalf("plan %s reported no %s: %+v", action, GuardNoWayIn, pv.Guards)
	return core.Guard{}
}

// TestNoWayIn: root has a password, alice has neither a password nor (by
// default) a key, bob is locked; sshd -T starts key-only.
func TestNoWayIn(t *testing.T) {
	keyOnly := map[string]string{
		"authorizedkeysfile": defaultKeysFile, "pubkeyauthentication": "yes", "authenticationmethods": "any",
		"passwordauthentication": "no", "kbdinteractiveauthentication": "no", "permitrootlogin": "without-password",
	}
	tests := []struct {
		name          string
		sshd          map[string]string // sshd -T lines over keyOnly
		lists         []string          // allowusers, denyusers ... lines
		keys          []string          // the accounts with one key
		setup         func(r *rig)
		action, input string
		ok            bool
		says          []string
	}{
		{name: "key-only users with keys and passwords off", keys: []string{"alice"},
			action: "users.password.remove", input: `{"name":"root"}`, ok: true,
			says: []string{"After the change alice could sign in over SSH", "Considered: root (no key, no password), alice (key), bob (locked)."}},
		{name: "the last keyed user locked", keys: []string{"alice"},
			action: "users.user.lock", input: `{"name":"alice"}`,
			says: []string{"After the change no account could sign in over SSH", "alice (locked)"}},
		{name: "pubkeyauthentication no with passwords off", keys: []string{"alice"}, sshd: map[string]string{"pubkeyauthentication": "no"},
			action: "users.password.remove", input: `{"name":"root"}`, says: []string{"alice (key sign-in off, no password)"}},
		{name: "passwords on and a P account", sshd: map[string]string{"passwordauthentication": "yes", "permitrootlogin": "yes"},
			action: "users.user.lock", input: `{"name":"alice"}`, ok: true, says: []string{"root (password)"}},
		{name: "passwords on but no account with status P", sshd: map[string]string{"passwordauthentication": "yes", "permitrootlogin": "yes"},
			action: "users.password.remove", input: `{"name":"root"}`, says: []string{"root (no key, no password)", "alice (no key, no password)"}},
		{name: "keyboard-interactive on counts a password", sshd: map[string]string{"kbdinteractiveauthentication": "yes", "permitrootlogin": "yes"},
			action: "users.user.lock", input: `{"name":"alice"}`, ok: true, says: []string{"root (password)"}},
		{name: "authenticationmethods publickey,password with passwords off", keys: []string{"alice"},
			sshd: map[string]string{"authenticationmethods": "publickey,password"}, action: "users.password.remove", input: `{"name":"root"}`,
			says: []string{"alice (AuthenticationMethods not met)"}},
		{name: "a second method list a key meets", keys: []string{"alice"},
			sshd: map[string]string{"authenticationmethods": "publickey,password publickey"}, action: "users.password.remove", input: `{"name":"root"}`, ok: true},
		{name: "a Match block", keys: []string{"alice"},
			setup:  func(r *rig) { r.files[sshdConfig] = fxSshdConfig + "Match User alice\n\tPasswordAuthentication yes\n" },
			action: "users.user.lock", input: `{"name":"alice"}`,
			says: []string{sshdConfig + " holds a Match block, whose overrides this check cannot see."}},
		{name: "root's key with PermitRootLogin no", keys: []string{"root"}, sshd: map[string]string{"permitrootlogin": "no"},
			action: "users.user.lock", input: `{"name":"alice"}`, says: []string{"root (PermitRootLogin no)"}},
		{name: "root's key with prohibit-password", keys: []string{"root"},
			action: "users.user.lock", input: `{"name":"alice"}`, ok: true, says: []string{"root (key)"}},
		{name: "root's key with forced-commands-only", keys: []string{"root"}, sshd: map[string]string{"permitrootlogin": "forced-commands-only"},
			action: "users.user.lock", input: `{"name":"alice"}`, says: []string{"root (root key sign-in off, password sign-in off)"}},
		{name: "AllowUsers with a wildcard", keys: []string{"alice"}, lists: []string{"allowusers al*"},
			action: "users.password.remove", input: `{"name":"root"}`, ok: true},
		{name: "DenyUsers", keys: []string{"alice"}, lists: []string{"denyusers alice"},
			action: "users.password.remove", input: `{"name":"root"}`, says: []string{"alice (shut out by the allow and deny lists)"}},
		{name: "DenyUsers from one host", keys: []string{"alice"}, lists: []string{"denyusers alice@192.0.2.1"},
			action: "users.password.remove", input: `{"name":"root"}`, ok: true},
		{name: "AllowGroups sudo", keys: []string{"alice"}, lists: []string{"allowgroups sudo"},
			action: "users.password.remove", input: `{"name":"root"}`, says: []string{"alice (shut out by the allow and deny lists)"}},
		{name: "delete the last keyed user", keys: []string{"alice"},
			action: "users.user.delete", input: `{"name":"alice"}`, says: []string{"Considered: root (no key, password sign-in off), bob (locked)."}},
		{name: "remove the last key", keys: []string{"alice"},
			action: "users.key.remove", input: `{"name":"alice","fingerprint":"<alice>"}`, says: []string{"alice (no key, no password)"}},
		{name: "remove one of two keys", keys: []string{"alice"}, setup: func(r *rig) { r.keys["alice"] += ed25519Line(r.t, "second") + "\n" },
			action: "users.key.remove", input: `{"name":"alice","fingerprint":"<alice>"}`, ok: true},
		{name: "a key file the check cannot read", keys: []string{"alice"}, sshd: map[string]string{"authorizedkeysfile": "/etc/ssh/keys/%u"},
			action: "users.user.lock", input: `{"name":"alice"}`, says: []string{"sshd reads keys from /etc/ssh/keys/%u, which this check does not read."}},
		{name: "a drop-in setting removed falls back to the other files", setup: func(r *rig) {
			r.liveSshd()
			r.files[sshdConfig] = fxSshdConfig + "PermitRootLogin yes\n"
			r.files[dropInFile] = dropInHeader + "PasswordAuthentication yes\n"
			r.files[dropInDir+"/50-cloud-init.conf"] = "PasswordAuthentication no\n"
		}, action: "users.sshd.apply", input: `{"maxAuthTries":3}`, says: []string{"root (no key, password sign-in off)"}},
		{name: "a drop-in setting removed falls back to the default", setup: func(r *rig) {
			r.liveSshd()
			r.files[sshdConfig] = fxSshdConfig + "PermitRootLogin yes\n"
			r.files[dropInFile] = dropInHeader + "PasswordAuthentication no\n"
		}, action: "users.sshd.apply", input: `{"maxAuthTries":3}`, ok: true, says: []string{"root (password)"}},
		{name: "no SSH server", setup: func(r *rig) { delete(r.files, sshdConfig) },
			action: "users.user.lock", input: `{"name":"alice"}`, ok: true,
			says: []string{"the OpenSSH server is not installed: /etc/ssh/sshd_config is missing, so the change does not affect SSH sign-in."}},
		{name: "sshd -T fails", setup: func(r *rig) {
			r.answers[key(platform.SshdPath, platform.SshdEffective())] = core.CmdResult{ExitCode: 255, Stderr: []byte("no hostkeys available")}
		}, action: "users.user.lock", input: `{"name":"alice"}`, says: []string{"sshd's settings cannot be read", "no hostkeys available"}},
		{name: "passwords off for key-only users", keys: []string{"alice"}, sshd: map[string]string{"passwordauthentication": "yes"},
			setup:  func(r *rig) { r.liveSshd(); r.files[dropInDir+"/50-cloud-init.conf"] = "PasswordAuthentication yes\n" },
			action: "users.sshd.apply", input: `{"passwordAuthentication":false}`, ok: true, says: []string{"alice (key)"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			lines := maps.Clone(keyOnly)
			maps.Copy(lines, tt.sshd)
			var out []string
			for k, v := range lines {
				out = append(out, k+" "+v)
			}
			slices.Sort(out)
			r.answer(platform.SshdPath, platform.SshdEffective(), strings.Join(append(out, tt.lists...), "\n")+"\n")
			fps := map[string]string{}
			for _, name := range tt.keys {
				line := ed25519Line(t, name)
				r.keys[name] = line + "\n"
				fps["<"+name+">"] = fingerprint(t, line)
			}
			if tt.setup != nil {
				tt.setup(r)
			}
			input := tt.input
			for k, v := range fps {
				input = strings.ReplaceAll(input, k, v)
			}
			g := guardOf(t, r, tt.action, input)
			if g.OK != tt.ok || g.Overridden || !g.Overridable {
				t.Errorf("guard %+v, want OK %v", g, tt.ok)
			}
			for _, s := range tt.says {
				if !strings.Contains(g.Message, s) {
					t.Errorf("guard message %q does not say %q", g.Message, s)
				}
			}
			if tt.ok {
				return
			}
			// The override lets the change through.
			over := strings.TrimSuffix(input, "}") + `,"overrides":["` + GuardNoWayIn + `"]}`
			if g := guardOf(t, r, tt.action, over); !g.OK || !g.Overridden {
				t.Errorf("overridden guard %+v", g)
			}
		})
	}
}

func TestWildmatch(t *testing.T) {
	for _, c := range []struct {
		pattern, s string
		want       bool
	}{
		{"alice", "alice", true}, {"al*", "alice", true}, {"a?ice", "alice", true}, {"*", "", true}, {"*", "alice", true},
		{"?", "", false}, {"a*b*c", "axxbyyc", true}, {"a*b", "ab", true}, {"a*b", "a", false}, {"*x", "abc", false},
		{"*ice", "alice", true}, {"al*e", "alicee", true}, {"alice", "alic", false}, {"alic", "alice", false}, {"**a", "a", true},
	} {
		if got := wildmatch(c.pattern, c.s); got != c.want {
			t.Errorf("wildmatch(%q, %q) = %v, want %v", c.pattern, c.s, got, c.want)
		}
	}
	// Patterns that make a backtracking matcher take exponential time.
	done := make(chan bool)
	go func() {
		done <- wildmatch(strings.Repeat("*a", 16)+"b", strings.Repeat("a", 32)) ||
			wildmatch(strings.Repeat("*", 31)+"x", strings.Repeat("a", 32))
	}()
	select {
	case got := <-done:
		if got {
			t.Error("a pattern that cannot match matched")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wildmatch took over 2 s on a 32-character name")
	}
}
