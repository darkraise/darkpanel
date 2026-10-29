package firewall

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// The rows and settings of the repair tests: drpCustom differs from the
// default private sources, and drpLoaded is a filter table holding the
// integration's chains.
const (
	drpRuleA  = "01J00000000000000000000021"
	drpRuleB  = "01J00000000000000000000022"
	drpLoaded = "*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\n:ufw-user-input - [0:0]\n" +
		":DOCKER-USER - [0:0]\n:darkpanel-docker-bindings - [0:0]\n:darkpanel-docker-deny - [0:0]\n" +
		"-A INPUT -j ufw-user-input\n-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT\n-A DOCKER-USER -j RETURN\n" +
		"-A darkpanel-docker-bindings -j darkpanel-docker-deny\n-A darkpanel-docker-deny -j DROP\nCOMMIT\n"
)

// ForwardExemptions is empty, not nil: a restore stores settings with nil
// lists made empty, so a nil list would not read back equal.
var drpCustom = dockerfw.Settings{PrivateSources: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}, ForwardExemptions: []dockerfw.Exemption{}}

// drpRule is an allow row for every source of port 8080/tcp of container
// selector.
func drpRule(id, selector string) dockerfw.ContainerRule {
	return dockerfw.ContainerRule{ID: id, Binding: dockerfw.Binding{Selector: selector, HostPort: 8080, Proto: "tcp"}, Kind: dockerfw.Allow}
}

// drpHost is ruHost with a Docker daemon that cannot be reached; the
// returned env repairs change rpRepaired.
func drpHost(t *testing.T) (*fakeHost, fw, core.Env) {
	t.Helper()
	h, f, env := ruHost(t)
	d := newFakeDocker()
	d.err = dockerapi.ErrUnavailable
	f.d = fdDeps(h, d)
	env.Call.RepairOf = rpRepaired
	return h, f, env
}

// drpSet stores the integration's flag and settings and replaces the
// container rules.
func drpSet(t *testing.T, h *fakeHost, c dockerConfig, rows ...dockerfw.ContainerRule) {
	t.Helper()
	ctx := context.Background()
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		if err := writeDockerConfig(ctx, tx, c); err != nil {
			return err
		}
		return replaceContainerRules(ctx, tx, rows, h.clock.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestDrpRefusesOpenIncoming: with the integration on afterwards, a repair
// refuses a configured incoming policy of ACCEPT
// (firewall.unsupported_integration): on its own and with outcome keep
// the flag is the stored one, with outcome restore it is the snapshot's.
func TestDrpRefusesOpenIncoming(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name                string
		repairOf            bool
		raw                 string
		snapInput, nowInput string
		snapFlag, nowFlag   bool
		refused             bool
	}{
		{"on its own, integration on", false, `{}`, "DROP", "ACCEPT", false, true, true},
		{"on its own, integration off", false, `{}`, "DROP", "ACCEPT", false, false, false},
		{"keep, integration on", true, `{"outcome":"keep"}`, "ACCEPT", "ACCEPT", true, true, true},
		{"restore to a snapshot with the integration on", true, `{}`, "ACCEPT", "DROP", true, true, true},
		{"restore to a snapshot with the integration on and DROP", true, `{}`, "DROP", "DROP", true, true, false},
		{"restore to a snapshot with the integration off", true, `{}`, "ACCEPT", "DROP", false, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, f, env := drpHost(t)
			s := h.state()
			s.Input = c.snapInput
			h.setState(s)
			drpSet(t, h, dockerConfig{Enabled: c.snapFlag, Settings: drpCustom})
			id := rpTarget(t, h, f)
			s.Input = c.nowInput
			h.setState(s)
			drpSet(t, h, dockerConfig{Enabled: c.nowFlag, Settings: drpCustom})
			if err := setPostHashes(ctx, h.db, id, fileHashes(h.config().Files)); err != nil {
				t.Fatal(err)
			}
			if !c.repairOf {
				env.Call.RepairOf = ""
			}
			_, err := f.stateRepair().Handler.Plan(ctx, env, json.RawMessage(c.raw))
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

// TestDrpRestoreTurnsTheIntegrationOff: repairing a change whose snapshot
// was taken without the integration, while it is on, plans turning it off,
// removing its rows and removing its chains.
func TestDrpRestoreTurnsTheIntegrationOff(t *testing.T) {
	h, f, env := drpHost(t)
	rpTarget(t, h, f)
	drpSet(t, h, dockerConfig{Enabled: true, Settings: drpCustom}, drpRule(drpRuleA, "web"))
	h.loaded.V4, h.loaded.V6 = fhParse(t, drpLoaded), fhParse(t, drpLoaded)
	plan, err := f.stateRepair().Handler.Plan(context.Background(), env, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	diff := strings.Join(acDiff(plan), "\n")
	for _, want := range []string{"docker integration off", "web all:8080/tcp allow any", "Docker chains removed"} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff\n%s\nwant a line with %q", diff, want)
		}
	}
}

// TestDrpRestoreAppliesState: repairing a change whose snapshot was taken
// with the integration on brings back the snapshot's settings and rows
// through the journal, and leaves the flag alone.
func TestDrpRestoreAppliesState(t *testing.T) {
	ctx := context.Background()
	h, f, env := drpHost(t)
	drpSet(t, h, dockerConfig{Enabled: true, Settings: drpCustom}, drpRule(drpRuleA, "web"))
	rpTarget(t, h, f)
	drpSet(t, h, dockerConfig{Enabled: true, Settings: dockerfw.DefaultSettings()}, drpRule(drpRuleA, "web"), drpRule(drpRuleB, "db"))

	_, out := rpRun(t, h, f, env, `{}`)
	if out.RepairOutcome != "restored" {
		t.Errorf("output %+v, want restored", out)
	}
	rows, err := listContainerRules(ctx, h.db)
	if err != nil || len(rows) != 1 || rows[0].ID != drpRuleA {
		t.Errorf("rows %+v (%v), want only %s", rows, err, drpRuleA)
	}
	cfg, err := readDockerConfig(ctx, h.db)
	if err != nil || !cfg.Enabled || !reflect.DeepEqual(cfg.Settings, drpCustom) {
		t.Errorf("config %+v (%v), want the integration on with the snapshot's settings", cfg, err)
	}
	effects := hkEffects(h)
	for _, want := range []string{"firewall.container:" + drpRuleB + " made", "firewall.setting:sources made"} {
		if !slices.Contains(effects, want) {
			t.Errorf("effects %q, want %q", effects, want)
		}
	}
	if slices.Contains(effects, "firewall.setting:docker made") {
		t.Errorf("effects %q, want the flag left alone", effects)
	}
}
