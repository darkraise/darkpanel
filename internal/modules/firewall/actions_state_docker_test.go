package firewall

import (
	"context"
	"database/sql"
	"encoding/json"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// dshSettings differ from the defaults, so a block rendered from them
// shows where it came from; dshPrivateLine is the line Render writes into
// after.rules for them.
var (
	dshSettings    = dockerfw.Settings{PrivateSources: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}}
	dshInput       = dockerfw.BlockInput{Settings: dshSettings}
	dshFiles       = []string{"/etc/ufw/after.rules", "/etc/ufw/after6.rules"}
	dshPrivateLine = "-A DOCKER-USER -s 198.51.100.0/24 -j RETURN"
)

// dshHost is acHost with the integration flag set to flag, the settings
// of dshSettings and a Docker daemon that cannot be reached, so the block
// holds no binding.
func dshHost(t *testing.T, enabled, flag bool) (*fakeHost, fw) {
	t.Helper()
	h, f := acHost(t, enabled)
	d := newFakeDocker()
	d.err = dockerapi.ErrUnavailable
	f.d = fdDeps(h, d)
	err := h.db.Write(context.Background(), func(tx *sql.Tx) error {
		return writeDockerConfig(context.Background(), tx, dockerConfig{Enabled: flag, Settings: dshSettings})
	})
	if err != nil {
		t.Fatal(err)
	}
	return h, f
}

// dshConfig reads the configuration of a host whose UFW is enabled or not
// and whose after files hold the block of dshInput or not.
func dshConfig(t *testing.T, enabled, block bool) fwstate.Config {
	t.Helper()
	h, _ := acHost(t, enabled)
	if block {
		for p, fam := range map[string]fwstate.Family{"/etc/ufw/after.rules": fwstate.V4, "/etc/ufw/after6.rules": fwstate.V6} {
			data, err := dockerfw.WithBlock(h.config().Files[p].Data, dockerfw.Render(dshInput, fam))
			if err != nil {
				t.Fatal(err)
			}
			h.writeFile(p, data)
		}
	}
	return h.config()
}

// TestDshIntegrationChange: with the flag on, enabling a stopped UFW
// writes the block into both after files, and disabling an active UFW
// writes them without it and asks for the owned-chain cleanup; a change
// that leaves UFW as it is writes no file.
func TestDshIntegrationChange(t *testing.T) {
	st := dockerState{cfg: dockerConfig{Enabled: true, Settings: dshSettings}, realized: dockerfw.Realized{Block: dshInput}}
	for _, c := range []struct {
		name                    string
		enable, ufwOn, hasBlock bool
		files                   []string
		block, cleanup          bool
	}{
		{"enable writes the block into both files", true, false, false, dshFiles, true, false},
		{"enable of an active UFW writes nothing", true, true, false, nil, false, false},
		{"disable removes the block and cleans up", false, true, true, dshFiles, false, true},
		{"disable cleans up although the files hold no block", false, true, false, nil, false, true},
		{"disable of a stopped UFW does nothing", false, false, false, nil, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := integrationChange(c.enable, dshConfig(t, c.ufwOn, c.hasBlock), st)
			if err != nil {
				t.Fatal(err)
			}
			if got.enable == nil || *got.enable != c.enable {
				t.Errorf("enable = %v, want %v", got.enable, c.enable)
			}
			if keys := slices.Sorted(maps.Keys(got.files)); !slices.Equal(keys, c.files) {
				t.Fatalf("files %q, want %q", keys, c.files)
			}
			for p, data := range got.files {
				_, ok, err := dockerfw.BlockOf(data)
				if err != nil || ok != c.block {
					t.Errorf("%s: block present %v (%v), want %v", p, ok, err, c.block)
				}
				if !strings.Contains(string(data), "# after") {
					t.Errorf("%s lost the file's own lines:\n%s", p, data)
				}
				if has := strings.Contains(string(data), dshPrivateLine); has != (c.block && p == "/etc/ufw/after.rules") {
					t.Errorf("%s holds the IPv4 private source line: %v", p, has)
				}
			}
			if got.cleanup != c.cleanup {
				t.Errorf("cleanup = %v, want %v", got.cleanup, c.cleanup)
			}
			if got.flag != nil || got.sources != nil || got.rowsAdd != nil || got.rowsDel != nil || got.reload {
				t.Errorf("change %+v touches the logical state or reloads", got)
			}
		})
	}
}

// TestDshEnableWritesBlock: firewall.state.enable with the flag on writes
// the block through the journal before it starts UFW; with the flag off
// it leaves the after files alone.
func TestDshEnableWritesBlock(t *testing.T) {
	h, f := dshHost(t, false, true)
	_, ran := acRun(t, h, f.stateEnable(), `{}`)
	if !slices.Equal(ran, []string{"--force enable"}) {
		t.Errorf("ufw ran %q, want the start only", ran)
	}
	start := slices.Index(h.calls, "run /usr/sbin/ufw --force enable")
	for _, p := range dshFiles {
		if i := slices.Index(h.calls, "restore "+p); i < 0 || start < 0 || i > start {
			t.Errorf("%s written at call %d, UFW started at call %d, want the write first", p, i, start)
		}
		if !slices.Contains(hkEffects(h), "firewall.file:"+p+" made") {
			t.Errorf("effects %v, want the write of %s journaled", hkEffects(h), p)
		}
		data := h.config().Files[p].Data
		if _, ok, err := dockerfw.BlockOf(data); err != nil || !ok {
			t.Errorf("%s holds no block (%v):\n%s", p, err, data)
		}
	}
	if !strings.Contains(string(h.config().Files[dshFiles[0]].Data), dshPrivateLine) {
		t.Error("the block does not carry the stored private sources")
	}

	h, f = dshHost(t, false, false)
	acRun(t, h, f.stateEnable(), `{}`)
	for _, c := range h.calls {
		if strings.HasPrefix(c, "restore ") {
			t.Errorf("flag off: %q, want no file written", c)
		}
	}
}

// TestDshRefusesOpenIncoming: with the flag on, enabling UFW and setting
// the default policies refuse a resulting incoming policy of ACCEPT
// (firewall.unsupported_integration), also when the policy is already
// ACCEPT and the request leaves it; disabling UFW and the flag off are not
// refused.
func TestDshRefusesOpenIncoming(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name          string
		enabled, flag bool
		input         string
		action        string
		raw           string
		refused       bool
	}{
		{"enable, flag on, incoming ACCEPT", false, true, "ACCEPT", "enable", `{}`, true},
		{"enable, flag off, incoming ACCEPT", false, false, "ACCEPT", "enable", `{}`, false},
		{"enable, flag on, incoming DROP", false, true, "DROP", "enable", `{}`, false},
		{"disable, flag on, incoming ACCEPT", true, true, "ACCEPT", "disable", `{}`, false},
		{"defaults to ACCEPT, flag on", true, true, "DROP", "defaults", `{"incoming":"ACCEPT"}`, true},
		{"defaults to ACCEPT, flag off", true, false, "DROP", "defaults", `{"incoming":"ACCEPT"}`, false},
		{"defaults to REJECT, flag on", true, true, "DROP", "defaults", `{"incoming":"REJECT"}`, false},
		{"defaults leave an ACCEPT incoming, flag on", true, true, "ACCEPT", "defaults", `{"outgoing":"DROP"}`, true},
		{"defaults replace an ACCEPT incoming, flag on", true, true, "ACCEPT", "defaults", `{"incoming":"DROP"}`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, f := dshHost(t, c.enabled, c.flag)
			s := h.state()
			s.Input = c.input
			h.setState(s)
			def := f.stateEnable()
			switch c.action {
			case "disable":
				def = f.stateDisable()
			case "defaults":
				def = f.defaultsSet()
			}
			_, err := def.Handler.Plan(ctx, h.env(), json.RawMessage(c.raw))
			if !c.refused {
				if err != nil {
					t.Fatalf("plan = %v, want it planned", err)
				}
				return
			}
			if e := errcode.From(err); e.Code != errcode.FirewallUnsupportedIntegration {
				t.Fatalf("plan = %v, want firewall.unsupported_integration", err)
			}
		})
	}
}
