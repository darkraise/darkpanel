package firewall

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

const (
	drefAfter4   = "/etc/ufw/after.rules"
	drefAfter6   = "/etc/ufw/after6.rules"
	drefForeign4 = "-A DOCKER-USER -s 203.0.113.9/32 -j DROP\n"
	drefForeign6 = "-A DOCKER-USER -s 2001:db8::9/128 -j DROP\n"
)

// drefBlock is the block the integration writes for family f, with the
// default settings and no binding.
func drefBlock(f fwstate.Family) []byte {
	return dockerfw.Render(dockerfw.BlockInput{Settings: dockerfw.DefaultSettings()}, f)
}

// drefAfterFile is an after file with the block in it, or UFW's template
// without one.
func drefAfterFile(t *testing.T, f fwstate.Family, withBlock bool) []byte {
	t.Helper()
	data := []byte("*filter\n# after\nCOMMIT\n")
	if !withBlock {
		return data
	}
	out, err := dockerfw.WithBlock(data, drefBlock(f))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// drefConfig is a configuration whose after files hold the block (or not)
// and whose incoming policy is DROP.
func drefConfig(t *testing.T, withBlock bool) fwstate.Config {
	t.Helper()
	files := map[string]core.RestoreEntry{}
	for path, f := range map[string]fwstate.Family{drefAfter4: fwstate.V4, drefAfter6: fwstate.V6} {
		files[path] = core.RestoreEntry{Path: path, Present: true, Mode: 0o640, Data: drefAfterFile(t, f, withBlock)}
	}
	return fwstate.Config{Enabled: true, DefaultInput: "DROP", Files: files}
}

// drefSave is one family's loaded filter table: DOCKER-USER holding the
// block's own rules (withBlock) or only Docker's -j RETURN, then the
// foreign rule lines.
func drefSave(t *testing.T, f fwstate.Family, withBlock bool, foreign string) *netfilter.Ruleset {
	t.Helper()
	text := "*filter\n:DOCKER-USER - [0:0]\n-A DOCKER-USER -j RETURN\n" + foreign + "COMMIT\n"
	if withBlock {
		text = strings.Replace(string(drefBlock(f)), "COMMIT\n", foreign+"COMMIT\n", 1)
	}
	return fhParse(t, text)
}

func drefLoaded(t *testing.T, withBlock bool, foreign4, foreign6 string) fwstate.Loaded {
	t.Helper()
	return fwstate.Loaded{
		Variant: "nft",
		V4:      drefSave(t, fwstate.V4, withBlock, foreign4),
		V6:      drefSave(t, fwstate.V6, withBlock, foreign6),
	}
}

func TestDrefDockerUserInUse(t *testing.T) {
	tests := []struct {
		name    string
		flag    bool
		block   bool // the after files hold the block
		cur     func(t *testing.T) fwstate.Loaded
		want    []string // the refusal's detail holds all of these; nil: no refusal
		notWant []string
	}{
		{"the flag off ignores a foreign rule", false, true,
			func(t *testing.T) fwstate.Loaded { return drefLoaded(t, true, drefForeign4, "") }, nil, nil},
		{"only Docker's own -j RETURN", true, false,
			func(t *testing.T) fwstate.Loaded { return drefLoaded(t, false, "", "") }, nil, nil},
		{"the block loaded as the files hold it", true, true,
			func(t *testing.T) fwstate.Loaded { return drefLoaded(t, true, "", "") }, nil, nil},
		{"a foreign IPv4 rule beside the block", true, true,
			func(t *testing.T) fwstate.Loaded { return drefLoaded(t, true, drefForeign4, "") },
			[]string{"v4 -A DOCKER-USER -s 203.0.113.9/32 -j DROP"}, []string{"2001:db8::9"}},
		{"a foreign IPv6 rule beside the block", true, true,
			func(t *testing.T) fwstate.Loaded { return drefLoaded(t, true, "", drefForeign6) },
			[]string{"v6 -A DOCKER-USER -s 2001:db8::9/128 -j DROP"}, []string{"203.0.113.9"}},
		{"foreign rules of both families are all listed", true, true,
			func(t *testing.T) fwstate.Loaded { return drefLoaded(t, true, drefForeign4, drefForeign6) },
			[]string{"v4 -A DOCKER-USER -s 203.0.113.9/32 -j DROP", "v6 -A DOCKER-USER -s 2001:db8::9/128 -j DROP"}, nil},
		{"adoption: a foreign rule and no block in the files", true, false,
			func(t *testing.T) fwstate.Loaded { return drefLoaded(t, false, drefForeign4, "") },
			[]string{"v4 -A DOCKER-USER -s 203.0.113.9/32 -j DROP"}, nil},
		{"a host without ip6tables", true, true,
			func(t *testing.T) fwstate.Loaded {
				cur := drefLoaded(t, true, "", "")
				cur.V6 = nil
				return cur
			}, nil, nil},
		{"Docker's chain is not loaded", true, true,
			func(t *testing.T) fwstate.Loaded {
				rs := fhParse(t, "*filter\n:INPUT ACCEPT [0:0]\nCOMMIT\n")
				return fwstate.Loaded{Variant: "nft", V4: rs, V6: rs}
			}, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := dockerUserInUse(tt.cur(t), drefConfig(t, tt.block), tt.flag)
			if tt.want == nil {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("not refused")
			}
			e := errcode.From(err)
			if e.Code != errcode.FirewallDockerUserInUse {
				t.Fatalf("code %q (%v), want %q", e.Code, err, errcode.FirewallDockerUserInUse)
			}
			for _, w := range tt.want {
				if !strings.Contains(e.Detail, w) {
					t.Errorf("detail %q does not list %q", e.Detail, w)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(e.Detail, w) {
					t.Errorf("detail %q lists %q", e.Detail, w)
				}
			}
		})
	}
}

// TestDrefDockerUserInUseDamagedBlock: a block whose markers are duplicated
// cannot say which rules are the integration's, so the check fails with a
// plain error naming the file, never with the refusal an override clears.
func TestDrefDockerUserInUseDamagedBlock(t *testing.T) {
	cfg := drefConfig(t, true)
	e := cfg.Files[drefAfter4]
	e.Data = append(append([]byte{}, e.Data...), e.Data...)
	cfg.Files[drefAfter4] = e
	err := dockerUserInUse(drefLoaded(t, true, "", ""), cfg, true)
	if err == nil || !strings.Contains(err.Error(), "after.rules") {
		t.Fatalf("err = %v, want one naming after.rules", err)
	}
	if c := errcode.From(err).Code; c != errcode.Internal {
		t.Errorf("code %q, want a plain error (%q)", c, errcode.Internal)
	}
}

func TestDrefRefuseOpenIncoming(t *testing.T) {
	for _, tt := range []struct {
		name  string
		flag  bool
		input string
		deny  bool
	}{
		{"flag off, ACCEPT", false, "ACCEPT", false},
		{"flag on, DROP", true, "DROP", false},
		{"flag on, REJECT", true, "REJECT", false},
		{"flag on, ACCEPT", true, "ACCEPT", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := refuseOpenIncoming(tt.flag, fwstate.Config{DefaultInput: tt.input})
			if !tt.deny {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			e := errcode.From(err)
			if e.Code != errcode.FirewallUnsupportedIntegration || !strings.Contains(e.Detail, "DEFAULT_INPUT_POLICY") {
				t.Errorf("err = %v, want firewall.unsupported_integration naming DEFAULT_INPUT_POLICY", err)
			}
		})
	}
}

// drefHost is a fake host whose after files hold the block, whose loaded
// DOCKER-USER chain holds the block's rules and foreign (IPv4 lines), and
// whose stored integration flag is flag.
func drefHost(t *testing.T, flag bool, foreign string) (*fakeHost, fw) {
	t.Helper()
	h := newFakeHost(t)
	h.writeFile(drefAfter4, drefAfterFile(t, fwstate.V4, true))
	h.writeFile(drefAfter6, drefAfterFile(t, fwstate.V6, true))
	h.loaded = drefLoaded(t, true, foreign, "")
	err := h.db.Write(context.Background(), func(tx *sql.Tx) error {
		return writeDockerConfig(context.Background(), tx, dockerConfig{Enabled: flag, Settings: dockerfw.DefaultSettings()})
	})
	if err != nil {
		t.Fatal(err)
	}
	f, _ := newFW(h)
	return h, f
}

func drefBuild(c change) buildFunc {
	return func(fwstate.Config, fwstate.Loaded) (change, error) { return c, nil }
}

// TestDrefPrepare: prepare refuses a change while the integration is on
// before or after it and DOCKER-USER holds a foreign rule, unless the
// change discards the foreign rules, and does so before it predicts.
func TestDrefPrepare(t *testing.T) {
	on, off := true, false
	tests := []struct {
		name    string
		flag    bool // the stored flag
		foreign string
		c       change
		refused bool
	}{
		{"flag on, foreign rule", true, drefForeign4, change{}, true},
		{"flag on, foreign rule, the change discards it", true, drefForeign4, change{discardForeign: true}, false},
		{"flag off, foreign rule", false, drefForeign4, change{}, false},
		{"flag off, the change turns it on", false, drefForeign4, change{flag: &on}, true},
		{"flag on, the change turns it off", true, drefForeign4, change{flag: &off}, true},
		{"flag on, no foreign rule", true, "", change{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, f := drefHost(t, tt.flag, tt.foreign)
			_, err := f.prepare(context.Background(), h.env(), drefBuild(tt.c))
			if !tt.refused {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			e := errcode.From(err)
			if e.Code != errcode.FirewallDockerUserInUse || !strings.Contains(e.Detail, "203.0.113.9/32") {
				t.Fatalf("err = %v, want firewall.docker_user_in_use naming the rule", err)
			}
		})
	}
}

func TestDrefPrepareRefusesBeforePredicting(t *testing.T) {
	h, f := drefHost(t, true, drefForeign4)
	h.predictErr = errors.New("fake: predictor must not run")
	_, err := f.prepare(context.Background(), h.env(), drefBuild(change{}))
	if c := errcode.From(err).Code; c != errcode.FirewallDockerUserInUse {
		t.Fatalf("err = %v, want firewall.docker_user_in_use before the prediction", err)
	}
}

func TestDrefPrepareKeepsBuildError(t *testing.T) {
	h, f := drefHost(t, true, drefForeign4)
	errBuild := errors.New("fake: the builder refuses")
	_, err := f.prepare(context.Background(), h.env(), func(fwstate.Config, fwstate.Loaded) (change, error) {
		return change{}, errBuild
	})
	if !errors.Is(err, errBuild) {
		t.Fatalf("err = %v, want the builder's error first", err)
	}
}
