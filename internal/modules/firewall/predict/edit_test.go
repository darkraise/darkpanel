package predict

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/conf"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// editTestRefused are the golden commands outside the vectors Darkpanel
// issues: an application rule and the short form.
var editTestRefused = []string{"ufw allow 'DP Web'", "ufw allow 8080/tcp comment 'hand rule'"}

var editTestSysctlRe = regexp.MustCompile(`^printf '(.*)' >> /etc/ufw/sysctl\.conf$`)

// editTestShell applies a golden command that is not ufw to the files and
// reads the configuration back.
func editTestShell(t *testing.T, cfg fwstate.Config, line string) fwstate.Config {
	t.Helper()
	files := cloneConfig(cfg).Files
	switch m := editTestSysctlRe.FindStringSubmatch(line); {
	case line == "sed -i 's/^IPV6=.*/IPV6=no/' /etc/default/ufw":
		e := files[defaultsUFW]
		e.Data = regexp.MustCompile(`(?m)^IPV6=.*$`).ReplaceAll(e.Data, []byte("IPV6=no"))
		files[defaultsUFW] = e
	case m != nil:
		e := files["/etc/ufw/sysctl.conf"]
		e.Data = append(slices.Clone(e.Data), strings.ReplaceAll(m[1], `\n`, "\n")...)
		files["/etc/ufw/sysctl.conf"] = e
	case strings.Contains(line, "/etc/ufw/applications.d/"):
		return cfg
	default:
		t.Fatalf("unknown golden command %q", line)
	}
	root := t.TempDir()
	for path, e := range files {
		if !e.Present {
			continue
		}
		p := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, e.Data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out, err := conf.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// editTestKeep drops the tuples the refused golden commands wrote.
func editTestKeep(ts []model.Tuple) []model.Tuple {
	return slices.DeleteFunc(slices.Clone(ts), func(t model.Tuple) bool { return t.DApp != "" || t.Comment == "hand rule" })
}

// TestEditGolden replays each golden scenario's commands from the fresh
// configuration and compares the tuples, the settings and the bytes of
// every state file with the capture. In the manual scenario the two
// refused commands are checked to be refused and their tuples left out.
func TestEditGolden(t *testing.T) {
	for _, img := range predictImages {
		for _, sc := range []string{"enabled", "rules", "disabled", "ipv6no", "logging", "manual", "sysctl"} {
			cfg := predictConfig(t, img, "fresh")
			data, err := os.ReadFile(filepath.Join(predictGolden, img, sc, "commands.txt")) //nolint:gosec // G304: a fixed name under the golden directory
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				if !strings.HasPrefix(line, "ufw ") {
					cfg = editTestShell(t, cfg, line)
					continue
				}
				next, err := Edit(cfg, predictShellSplit(line)[1:])
				if slices.Contains(editTestRefused, line) {
					if err == nil {
						t.Errorf("%s/%s: %q was not refused", img, sc, line)
					}
					continue
				}
				if err != nil {
					t.Fatalf("%s/%s: %q: %v", img, sc, line, err)
				}
				cfg = next
			}
			want := predictConfig(t, img, sc)
			if sc == "manual" {
				want.Rules4, want.Rules6 = editTestKeep(want.Rules4), editTestKeep(want.Rules6)
			}
			if cfg.Enabled != want.Enabled || cfg.LogLevel != want.LogLevel || cfg.IPv6 != want.IPv6 ||
				cfg.DefaultInput != want.DefaultInput || cfg.DefaultOutput != want.DefaultOutput || cfg.DefaultForward != want.DefaultForward {
				t.Errorf("%s/%s: settings %v %s %v %s %s %s, want %v %s %v %s %s %s", img, sc,
					cfg.Enabled, cfg.LogLevel, cfg.IPv6, cfg.DefaultInput, cfg.DefaultOutput, cfg.DefaultForward,
					want.Enabled, want.LogLevel, want.IPv6, want.DefaultInput, want.DefaultOutput, want.DefaultForward)
			}
			if !reflect.DeepEqual(cfg.Rules4, want.Rules4) || !reflect.DeepEqual(cfg.Rules6, want.Rules6) {
				t.Errorf("%s/%s: tuples\n%+v\n%+v\nwant\n%+v\n%+v", img, sc, cfg.Rules4, cfg.Rules6, want.Rules4, want.Rules6)
			}
			for _, path := range fwstate.StateFiles {
				if sc == "manual" && (path == userRules || path == userRules6) {
					continue
				}
				if got := cfg.Files[path].Data; string(got) != string(want.Files[path].Data) {
					t.Errorf("%s/%s %s:\n%s\nwant\n%s", img, sc, path, got, want.Files[path].Data)
				}
			}
		}
	}
}

// editTestFiles are the files whose checksums editTestSequence records.
var editTestFiles = [6]string{userRules, userRules6, ufwConf, defaultsUFW, afterRules, afterRules6}

// editTestSequence is a run of ufw commands from a fresh install, with
// whether ufw failed and the first 16 hex digits of each file's sha256
// afterwards. ufw 0.36.1-4ubuntu0.1 (ubuntu:22.04) and 0.36.2-9 (debian:13)
// gave identical output for every step.
var editTestSequence = []struct {
	cmd  string
	fail bool
	sha  [6]string
}{
	{"allow in proto tcp to any port 22", false, [6]string{"6398538b7da3fde0", "0e8688803c218f5e", "1c75b26d27f47cb5", "07db07bb07e5ac13", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"allow in proto tcp from 10.0.0.0/8 to any port 3000", false, [6]string{"f5c1c13c5109a869", "0e8688803c218f5e", "1c75b26d27f47cb5", "07db07bb07e5ac13", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"allow in proto tcp to any port 25", false, [6]string{"f079588a0a574442", "a3ee276789823fe2", "1c75b26d27f47cb5", "07db07bb07e5ac13", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"allow in proto udp from 2001:db8::/32 to any port 53", false, [6]string{"f079588a0a574442", "0acb6091b715d0e9", "1c75b26d27f47cb5", "07db07bb07e5ac13", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"insert 2 allow in proto tcp to any port 80", false, [6]string{"e20d8653713b750b", "159048bb22ae407c", "1c75b26d27f47cb5", "07db07bb07e5ac13", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"insert 6 deny in proto tcp from 2001:db8::/48 to any port 23", false, [6]string{"e20d8653713b750b", "3c5b89c499c7ec63", "1c75b26d27f47cb5", "07db07bb07e5ac13", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"insert 1 allow in proto tcp to any port 22", false, [6]string{"e20d8653713b750b", "3c5b89c499c7ec63", "1c75b26d27f47cb5", "07db07bb07e5ac13", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"allow in proto tcp to any port 22 comment 'first'", false, [6]string{"9edc29ddb3746d71", "229af8bf1d61b139", "1c75b26d27f47cb5", "07db07bb07e5ac13", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"deny in proto tcp to any port 22 comment 'first'", false, [6]string{"77354452095be786", "226db23a97676f7e", "1c75b26d27f47cb5", "07db07bb07e5ac13", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"delete allow in proto tcp to any port 25", false, [6]string{"9e829f2a64c2dbd2", "17f9055deae40d38", "1c75b26d27f47cb5", "07db07bb07e5ac13", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"delete deny in proto tcp to any port 22", false, [6]string{"da7ed74f751c9b80", "9fced9daca973e78", "1c75b26d27f47cb5", "07db07bb07e5ac13", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"delete allow in proto tcp to any port 9999", false, [6]string{"da7ed74f751c9b80", "9fced9daca973e78", "1c75b26d27f47cb5", "07db07bb07e5ac13", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"insert 7 allow in proto tcp to any port 443", true, [6]string{"da7ed74f751c9b80", "9fced9daca973e78", "1c75b26d27f47cb5", "07db07bb07e5ac13", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"insert 3 deny in proto udp from 2001:db8::/32 to any port 5353", false, [6]string{"da7ed74f751c9b80", "e00b8917a3f7d649", "1c75b26d27f47cb5", "07db07bb07e5ac13", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"insert 99 allow in proto tcp to any port 8443", true, [6]string{"da7ed74f751c9b80", "e00b8917a3f7d649", "1c75b26d27f47cb5", "07db07bb07e5ac13", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"route insert 1 deny in on eth0 out on docker0 log-all proto udp from 10.0.0.0/8 port 53 to 0.0.0.0/0 port 1000:2000,80 comment 'darkpanel:01J00000000000000000000000 dns'", false, [6]string{"7f2662045abd0c55", "e00b8917a3f7d649", "1c75b26d27f47cb5", "07db07bb07e5ac13", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"insert 2 allow in proto tcp from 192.0.2.0/24 to any port 8080", false, [6]string{"ddeab4b0854c67ac", "e00b8917a3f7d649", "1c75b26d27f47cb5", "07db07bb07e5ac13", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"--force enable", false, [6]string{"ddeab4b0854c67ac", "e00b8917a3f7d649", "254cb3ad232ca318", "07db07bb07e5ac13", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"allow in proto tcp to any port 9000", false, [6]string{"e05b272bb763df30", "e257419f22e1ca48", "254cb3ad232ca318", "07db07bb07e5ac13", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"logging off", false, [6]string{"0763c525d6e03e8d", "4a58b0bbd31b57f2", "0017389c2ea1b4e6", "07db07bb07e5ac13", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"logging on", false, [6]string{"e05b272bb763df30", "e257419f22e1ca48", "254cb3ad232ca318", "07db07bb07e5ac13", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"logging full", false, [6]string{"88e50d47338eedcf", "7ba359f3dbd6584a", "f108047e8156bcbc", "07db07bb07e5ac13", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"default reject incoming", false, [6]string{"88e50d47338eedcf", "7ba359f3dbd6584a", "f108047e8156bcbc", "40c000273427418a", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"default allow routed", false, [6]string{"6b0c8e1c2252a062", "8ffc040dde664e1c", "f108047e8156bcbc", "79631b37c8bbb380", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"disable", false, [6]string{"6b0c8e1c2252a062", "8ffc040dde664e1c", "ee9dbae274648eb5", "79631b37c8bbb380", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"default deny outgoing", false, [6]string{"6b0c8e1c2252a062", "8ffc040dde664e1c", "ee9dbae274648eb5", "b9bbd54b4f39c6e8", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"logging medium", false, [6]string{"dd399390926fcaf3", "49d82e28373cc4f4", "667945f85dffbd85", "b9bbd54b4f39c6e8", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"reload", false, [6]string{"dd399390926fcaf3", "49d82e28373cc4f4", "667945f85dffbd85", "b9bbd54b4f39c6e8", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"--force enable", false, [6]string{"dd399390926fcaf3", "49d82e28373cc4f4", "e48a62c3cebcd0d9", "b9bbd54b4f39c6e8", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"delete allow in proto tcp to any port 80", false, [6]string{"af2b981d863ea1ac", "dc6cce035eabb15c", "e48a62c3cebcd0d9", "b9bbd54b4f39c6e8", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"insert 3 allow in proto tcp to any port 80", false, [6]string{"dd399390926fcaf3", "730a90cc9a0701e6", "e48a62c3cebcd0d9", "b9bbd54b4f39c6e8", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"insert 5 allow in proto tcp to any port 21", false, [6]string{"bc213eb48ef4d581", "efe9c8b7685133b5", "e48a62c3cebcd0d9", "b9bbd54b4f39c6e8", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"insert 9 allow in proto tcp to any port 20", false, [6]string{"07e193f05d6d1dbb", "0a0e862692b943c3", "e48a62c3cebcd0d9", "b9bbd54b4f39c6e8", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"insert 12 allow in proto tcp to any port 19", false, [6]string{"0d5e83d1ed56243d", "1e9758e3b3a97b1c", "e48a62c3cebcd0d9", "b9bbd54b4f39c6e8", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
	{"insert 2 deny in proto tcp from 2001:db8::/48 to any port 23", true, [6]string{"0d5e83d1ed56243d", "1e9758e3b3a97b1c", "e48a62c3cebcd0d9", "b9bbd54b4f39c6e8", "c1be61c17e850ea1", "ee5f2e14d2ae6c33"}},
}

// TestEditSequence replays editTestSequence: inserts in either family and
// both (at a position of either family's range), a duplicate insert, a comment and then an action replacing a rule,
// deletes with and without a match, invalid positions, enable, logging,
// defaults, disable and reload.
func TestEditSequence(t *testing.T) {
	for _, img := range predictImages {
		cfg := predictConfig(t, img, "fresh")
		for i, step := range editTestSequence {
			next, err := Edit(cfg, predictShellSplit(step.cmd))
			if step.fail {
				if err == nil {
					t.Fatalf("%s step %d %q: no error", img, i, step.cmd)
				}
				continue
			}
			if err != nil {
				t.Fatalf("%s step %d %q: %v", img, i, step.cmd, err)
			}
			cfg = next
			for j, path := range editTestFiles {
				sum := sha256.Sum256(cfg.Files[path].Data)
				if got := hex.EncodeToString(sum[:])[:16]; got != step.sha[j] {
					t.Fatalf("%s step %d %q: %s is %s, want %s:\n%s", img, i, step.cmd, path, got, step.sha[j], cfg.Files[path].Data)
				}
			}
		}
	}
}

func TestEditRefuses(t *testing.T) {
	base := predictConfig(t, "debian-13", "rules")
	before := cloneConfig(base)
	noV6 := base
	noV6.IPv6 = false
	noUser := cloneConfig(base)
	noUser.Files[userRules] = core.RestoreEntry{Path: userRules}
	badLevel := base
	badLevel.LogLevel = "loud"
	for _, tc := range []struct {
		name string
		cfg  fwstate.Config
		args []string
	}{
		{"short form", base, []string{"allow", "22/tcp"}},
		{"IPv6 rule with IPv6 off", noV6, []string{"allow", "in", "from", "2001:db8::1"}},
		{"missing user.rules", noUser, []string{"allow", "in", "proto", "tcp", "to", "any", "port", "9"}},
		{"unknown log level", badLevel, []string{"reload"}},
		{"v6 insert in the v4 range", base, []string{"insert", "1", "allow", "in", "from", "2001:db8::1"}},
	} {
		if _, err := Edit(tc.cfg, tc.args); err == nil || !strings.HasPrefix(err.Error(), "predict: ") {
			t.Errorf("%s: err = %v, want a predict error", tc.name, err)
		}
	}
	if _, err := Edit(base, []string{"allow", "in", "proto", "tcp", "to", "any", "port", "9"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(base, before) {
		t.Error("Edit modified its input")
	}
}

// editTestDeepClone copies cfg down to each file's bytes, which cloneConfig
// shares with its argument.
func editTestDeepClone(cfg fwstate.Config) fwstate.Config {
	c := cfg
	c.Rules4, c.Rules6 = slices.Clone(cfg.Rules4), slices.Clone(cfg.Rules6)
	c.Sysctl, c.Unsupported = slices.Clone(cfg.Sysctl), slices.Clone(cfg.Unsupported)
	c.Files = make(map[string]core.RestoreEntry, len(cfg.Files))
	for path, e := range cfg.Files {
		e.Data = bytes.Clone(e.Data)
		c.Files[path] = e
	}
	return c
}

// TestEditKeepsInput replays editTestSequence from two scenarios with IPv6
// on and off, and checks after every step, failed or not, that the input
// configuration is unchanged down to its file bytes.
func TestEditKeepsInput(t *testing.T) {
	for _, sc := range []string{"fresh", "rules"} {
		for _, ipv6 := range []bool{true, false} {
			cfg := predictConfig(t, "debian-13", sc)
			cfg.IPv6 = ipv6
			for i, step := range editTestSequence {
				before := editTestDeepClone(cfg)
				next, err := Edit(cfg, predictShellSplit(step.cmd))
				if !reflect.DeepEqual(cfg, before) {
					t.Fatalf("%s ipv6=%v step %d %q: Edit modified its input", sc, ipv6, i, step.cmd)
				}
				if err == nil {
					cfg = next
				}
			}
		}
	}
}

// TestEditIPv6Off checks that with IPv6 off a rule for both families
// goes to user.rules only and user6.rules is left alone until a command
// rewrites both files, which empties it.
func TestEditIPv6Off(t *testing.T) {
	cfg := predictConfig(t, "debian-13", "rules")
	cfg.IPv6 = false
	six := cfg.Files[userRules6].Data
	got, err := Edit(cfg, []string{"allow", "in", "proto", "tcp", "to", "any", "port", "9"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rules4) != len(cfg.Rules4)+1 || !reflect.DeepEqual(got.Rules6, cfg.Rules6) || string(got.Files[userRules6].Data) != string(six) {
		t.Errorf("rule with IPv6 off: %d v4 tuples (want %d), v6 tuples or file changed", len(got.Rules4), len(cfg.Rules4)+1)
	}
	if got, err = Edit(got, []string{"logging", "low"}); err != nil {
		t.Fatal(err)
	}
	if got.Rules6 != nil || strings.Contains(string(got.Files[userRules6].Data), model.TuplePrefix) {
		t.Errorf("logging with IPv6 off left IPv6 tuples:\n%s", got.Files[userRules6].Data)
	}
}

// TestEditDefaultAfterRules checks the log prefix switch "ufw default"
// makes in after.rules and after6.rules, as ufw 0.36.1 and 0.36.2 did
// after these comment lines were appended: allow incoming turned every
// "UFW BLOCK" into "UFW ALLOW" in both files, and then reject routed turned
// every "UFW ALLOW" into "UFW BLOCK".
func TestEditDefaultAfterRules(t *testing.T) {
	cfg := cloneConfig(predictConfig(t, "debian-13", "fresh"))
	for path, tail := range map[string]string{afterRules: "# [UFW BLOCK] one UFW BLOCK\n# [UFW ALLOW] two\n", afterRules6: "# [UFW ALLOW] six\n"} {
		e := cfg.Files[path]
		e.Data = append(slices.Clone(e.Data), tail...)
		cfg.Files[path] = e
	}
	for _, step := range []struct {
		args        []string
		want, want6 string
	}{
		{[]string{"default", "allow", "incoming"}, "# [UFW ALLOW] one UFW ALLOW\n# [UFW ALLOW] two\n", "# [UFW ALLOW] six\n"},
		{[]string{"default", "reject", "routed"}, "# [UFW BLOCK] one UFW BLOCK\n# [UFW BLOCK] two\n", "# [UFW BLOCK] six\n"},
	} {
		var err error
		if cfg, err = Edit(cfg, step.args); err != nil {
			t.Fatal(err)
		}
		if got := string(cfg.Files[afterRules].Data); !strings.HasSuffix(got, step.want) {
			t.Errorf("%q: after.rules ends\n%s", step.args, got[max(0, len(got)-120):])
		}
		if got := string(cfg.Files[afterRules6].Data); !strings.HasSuffix(got, step.want6) {
			t.Errorf("%q: after6.rules ends\n%s", step.args, got[max(0, len(got)-120):])
		}
	}
}

// FuzzEdit runs arbitrary argument vectors on an enabled configuration UFW
// has written. Edit must not panic, and whatever it accepts must leave the
// rule files as Files renders the result and every tuple reading back as
// itself.
func FuzzEdit(f *testing.F) {
	for _, step := range editTestSequence {
		f.Add(step.cmd)
	}
	f.Add("default allow incoming")
	f.Add("route allow in on eth0 out on eth1 log proto udp from 10.0.0.0/8 port 1:2,5 to any comment x")
	base, err := conf.Read(filepath.Join(predictGolden, "debian-13", "rules", "files"))
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, line string) {
		got, err := Edit(base, strings.Fields(line))
		if err != nil {
			return
		}
		files, err := Files(got)
		if err != nil {
			t.Fatalf("%q: Files: %v", line, err)
		}
		for _, path := range []string{userRules, userRules6} {
			if string(files[path]) != string(got.Files[path].Data) {
				t.Fatalf("%q: %s is not what Files renders", line, path)
			}
		}
		for _, tp := range append(slices.Clone(got.Rules4), got.Rules6...) {
			back, err := model.ParseTuple(tp.Raw, tp.Family == model.FamilyV6)
			if err != nil || back != tp {
				t.Fatalf("%q: tuple %+v reads back as %+v (%v)", line, tp, back, err)
			}
		}
	})
}
