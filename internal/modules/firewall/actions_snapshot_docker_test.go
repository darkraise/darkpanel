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

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// The container rules and settings of the restore tests: dsrCustom differs
// from the default private sources. Its exemption list is empty rather than
// nil because the journaled settings write stores a nil list as an empty one.
const (
	dsrRuleA = "01J00000000000000000000011"
	dsrRuleB = "01J00000000000000000000012"
)

var dsrCustom = dockerfw.Settings{PrivateSources: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}, ForwardExemptions: []dockerfw.Exemption{}}

// dsrLoaded is a filter table holding the integration's chains.
const dsrLoaded = "*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\n:ufw-user-input - [0:0]\n" +
	":DOCKER-USER - [0:0]\n:darkpanel-docker-bindings - [0:0]\n:darkpanel-docker-deny - [0:0]\n" +
	"-A INPUT -j ufw-user-input\n-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT\n-A DOCKER-USER -j RETURN\n" +
	"-A darkpanel-docker-bindings -j darkpanel-docker-deny\n-A darkpanel-docker-deny -j DROP\nCOMMIT\n"

// dsrRule is an allow row for every source of port 8080/tcp of container
// selector.
func dsrRule(id, selector string) dockerfw.ContainerRule {
	return dockerfw.ContainerRule{ID: id, Binding: dockerfw.Binding{Selector: selector, HostPort: 8080, Proto: "tcp"}, Kind: dockerfw.Allow}
}

// dsrHost is an active UFW whose Docker daemon cannot be reached.
func dsrHost(t *testing.T) (*fakeHost, fw) {
	t.Helper()
	h, f := acHost(t, true)
	d := newFakeDocker()
	d.err = dockerapi.ErrUnavailable
	f.d = fdDeps(h, d)
	return h, f
}

// dsrSet stores the integration's flag and settings and replaces the
// container rules.
func dsrSet(t *testing.T, h *fakeHost, c dockerConfig, rows ...dockerfw.ContainerRule) {
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

func dsrIDs(rows []dockerfw.ContainerRule) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

// TestDsrWithDockerRestore: a restore sets the flag and the settings when
// the snapshot's differ, deletes the rows the snapshot lacks and inserts
// the ones it holds, and asks for the cleanup only when it turns an
// integration that is on off. It keeps the change's other fields.
func TestDsrWithDockerRestore(t *testing.T) {
	a, b := dsrRule(dsrRuleA, "web"), dsrRule(dsrRuleB, "db")
	on := dockerConfig{Enabled: true, Settings: dsrCustom}
	off := dockerConfig{Settings: dockerfw.DefaultSettings()}
	offCustom := dockerConfig{Settings: dsrCustom}
	yes, no := true, false
	defaults := dockerfw.DefaultSettings()
	for _, c := range []struct {
		name      string
		now, want dockerRestoreSide
		flag      *bool
		sources   *dockerfw.Settings
		add, del  []string
		cleanup   bool
	}{
		{"rows the snapshot lacks are deleted", dockerRestoreSide{on, []dockerfw.ContainerRule{a, b}}, dockerRestoreSide{on, []dockerfw.ContainerRule{a}},
			nil, nil, nil, []string{dsrRuleB}, false},
		{"turning the integration off cleans up", dockerRestoreSide{on, []dockerfw.ContainerRule{a}}, dockerRestoreSide{off, nil},
			&no, &defaults, nil, []string{dsrRuleA}, true},
		{"turning it on restores flag, settings and rows", dockerRestoreSide{off, nil}, dockerRestoreSide{on, []dockerfw.ContainerRule{a, b}},
			&yes, &dsrCustom, []string{dsrRuleA, dsrRuleB}, nil, false},
		{"equal states change nothing", dockerRestoreSide{on, []dockerfw.ContainerRule{a}}, dockerRestoreSide{on, []dockerfw.ContainerRule{a}},
			nil, nil, nil, nil, false},
		{"settings alone", dockerRestoreSide{off, nil}, dockerRestoreSide{offCustom, nil},
			nil, &dsrCustom, nil, nil, false},
		{"rows alone with the integration off", dockerRestoreSide{off, []dockerfw.ContainerRule{a}}, dockerRestoreSide{off, nil},
			nil, nil, nil, []string{dsrRuleA}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			snap := &snapshot{ID: "s1"}
			got := withDockerRestore(change{restore: snap, repair: true}, c.now, c.want)
			if got.restore != snap || !got.repair {
				t.Errorf("restore %p repair %v, want the change's own fields kept", got.restore, got.repair)
			}
			if !reflect.DeepEqual(got.flag, c.flag) {
				t.Errorf("flag %v, want %v", got.flag, c.flag)
			}
			if !reflect.DeepEqual(got.sources, c.sources) {
				t.Errorf("sources %v, want %v", got.sources, c.sources)
			}
			if !slices.Equal(dsrIDs(got.rowsAdd), c.add) || !slices.Equal(got.rowsDel, c.del) {
				t.Errorf("rows add %q delete %q, want add %q delete %q", dsrIDs(got.rowsAdd), got.rowsDel, c.add, c.del)
			}
			if got.cleanup != c.cleanup || got.files != nil || got.reload {
				t.Errorf("cleanup %v files %v reload %v, want cleanup %v and no files or reload", got.cleanup, got.files, got.reload, c.cleanup)
			}
		})
	}
}

// TestDsrReadSide: the stored flag, settings and rows, rows in id order.
func TestDsrReadSide(t *testing.T) {
	h, _ := dsrHost(t)
	got, err := readDockerRestoreSide(context.Background(), h.db)
	if err != nil || got.cfg.Enabled || !reflect.DeepEqual(got.cfg.Settings, dockerfw.DefaultSettings()) || len(got.rows) != 0 {
		t.Fatalf("a host without the integration reads %+v (%v), want it off with the defaults and no rows", got, err)
	}
	dsrSet(t, h, dockerConfig{Enabled: true, Settings: dsrCustom}, dsrRule(dsrRuleB, "db"), dsrRule(dsrRuleA, "web"))
	got, err = readDockerRestoreSide(context.Background(), h.db)
	if err != nil || !got.cfg.Enabled || !reflect.DeepEqual(got.cfg.Settings, dsrCustom) || !slices.Equal(dsrIDs(got.rows), []string{dsrRuleA, dsrRuleB}) {
		t.Errorf("read %+v (%v), want the stored state with the rows in id order", got, err)
	}
}

// TestDsrRestorePlan: restoring a snapshot taken without the integration
// on a host that has it on plans turning it off, removing its rows and
// removing its chains.
func TestDsrRestorePlan(t *testing.T) {
	h, f := dsrHost(t)
	id := snCapture(t, h, f)
	dsrSet(t, h, dockerConfig{Enabled: true, Settings: dsrCustom}, dsrRule(dsrRuleA, "web"))
	h.loaded.V4, h.loaded.V6 = fhParse(t, dsrLoaded), fhParse(t, dsrLoaded)
	plan, err := f.snapshotRestore().Handler.Plan(context.Background(), h.env(), json.RawMessage(`{"id":"`+id+`"}`))
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

// TestDsrRestoreAppliesState: restoring a snapshot taken with the
// integration on brings back its settings and rows through the journal and
// leaves the flag alone.
func TestDsrRestoreAppliesState(t *testing.T) {
	ctx := context.Background()
	h, f := dsrHost(t)
	dsrSet(t, h, dockerConfig{Enabled: true, Settings: dsrCustom}, dsrRule(dsrRuleA, "web"))
	id := snCapture(t, h, f)
	dsrSet(t, h, dockerConfig{Enabled: true, Settings: dockerfw.DefaultSettings()}, dsrRule(dsrRuleA, "web"), dsrRule(dsrRuleB, "db"))

	_, ran := acRun(t, h, f.snapshotRestore(), `{"id":"`+id+`"}`)
	if !slices.Equal(ran, []string{"reload"}) {
		t.Errorf("ufw ran %q, want the reload", ran)
	}
	rows, err := listContainerRules(ctx, h.db)
	if err != nil || !slices.Equal(dsrIDs(rows), []string{dsrRuleA}) {
		t.Errorf("rows %q (%v), want only %s", dsrIDs(rows), err, dsrRuleA)
	}
	cfg, err := readDockerConfig(ctx, h.db)
	if err != nil || !cfg.Enabled || !reflect.DeepEqual(cfg.Settings, dsrCustom) {
		t.Errorf("config %+v (%v), want the integration on with the snapshot's settings", cfg, err)
	}
	effects := hkEffects(h)
	for _, want := range []string{"firewall.container:" + dsrRuleB + " made", "firewall.setting:sources made"} {
		if !slices.Contains(effects, want) {
			t.Errorf("effects %q, want %q", effects, want)
		}
	}
	if slices.Contains(effects, "firewall.setting:docker made") {
		t.Errorf("effects %q, want the flag left alone", effects)
	}
}

// TestDsrRestoreRefusesOpenIncoming: a restore whose configured incoming
// policy is ACCEPT is refused when the integration is on afterwards, which
// is the snapshot's flag, and not when it is off afterwards.
func TestDsrRestoreRefusesOpenIncoming(t *testing.T) {
	for _, c := range []struct {
		name              string
		snapFlag, nowFlag bool
		refused           bool
	}{
		{"snapshot with the integration on", true, true, true},
		{"snapshot with the integration off", false, false, false},
		{"integration on now, off in the snapshot", false, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, f := dsrHost(t)
			s := h.state()
			s.Input = "ACCEPT"
			h.setState(s)
			dsrSet(t, h, dockerConfig{Enabled: c.snapFlag, Settings: dsrCustom})
			id := snCapture(t, h, f)
			s.Input = "DROP"
			h.setState(s)
			dsrSet(t, h, dockerConfig{Enabled: c.nowFlag, Settings: dsrCustom})
			_, err := f.snapshotRestore().Handler.Plan(context.Background(), h.env(), json.RawMessage(`{"id":"`+id+`"}`))
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
