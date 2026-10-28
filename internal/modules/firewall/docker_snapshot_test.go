package firewall

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/netip"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
)

func dsnSettings() dockerfw.Settings {
	return dockerfw.Settings{
		PrivateSources: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10")},
		ForwardExemptions: []dockerfw.Exemption{{
			OrigDestination: netip.MustParseAddr("192.0.2.10"), OrigPort: 2222,
			Destination: netip.MustParseAddr("10.9.0.5"), Proto: "tcp", Port: 22,
		}},
	}
}

// dsnRules returns three rows in id order: an allow from any source on a
// binding of every host address, an allow from a range on one host address,
// and a deny on the first binding.
func dsnRules() []dockerfw.ContainerRule {
	web := dockerfw.Binding{Selector: "web", HostPort: 8080, Proto: "tcp"}
	return []dockerfw.ContainerRule{
		{ID: "01J00000000000000000000010", Binding: web, Kind: dockerfw.Allow},
		{ID: "01J00000000000000000000011", Kind: dockerfw.Allow, From: netip.MustParsePrefix("198.51.100.0/24"),
			Binding: dockerfw.Binding{Selector: "shop/api", HostAddr: netip.MustParseAddr("192.0.2.10"), HostPort: 8443, Proto: "tcp"}},
		{ID: "01J00000000000000000000012", Binding: web, Kind: dockerfw.Deny, From: netip.MustParsePrefix("203.0.113.0/24")},
	}
}

func dsnSeed(t *testing.T, h *fakeHost, cfg dockerConfig, rows ...dockerfw.ContainerRule) {
	t.Helper()
	ctx := context.Background()
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		if err := writeDockerConfig(ctx, tx, cfg); err != nil {
			return err
		}
		return replaceContainerRules(ctx, tx, rows, h.clock.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDsnCaptureRecordsRowsSettingsAndFlag(t *testing.T) {
	for _, withImage := range []bool{false, true} {
		t.Run(map[bool]string{false: "change snapshot", true: "repair snapshot"}[withImage], func(t *testing.T) {
			ctx := context.Background()
			h := newFakeHost(t)
			dsnSeed(t, h, dockerConfig{Enabled: true, Settings: dsnSettings()}, dsnRules()...)
			raw, err := capture(ctx, h.env(), h.deps(), nil, withImage)
			if err != nil {
				t.Fatal(err)
			}
			s, err := getSnapshot(ctx, h.db, string(raw))
			if err != nil {
				t.Fatal(err)
			}
			if !s.State.Docker {
				t.Error("the snapshot lost the Docker integration flag")
			}
			if want := dsnSettings(); s.State.DockerSettings == nil || !reflect.DeepEqual(*s.State.DockerSettings, want) {
				t.Errorf("settings = %+v, want %+v", s.State.DockerSettings, want)
			}
			if want := dsnRules(); !reflect.DeepEqual(s.State.ContainerRules, want) {
				t.Errorf("rows = %+v, want %+v", s.State.ContainerRules, want)
			}
		})
	}
}

func TestDsnCaptureWithoutDockerConfig(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	raw, err := capture(ctx, h.env(), h.deps(), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	s, err := getSnapshot(ctx, h.db, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if want := dockerfw.DefaultSettings(); s.State.Docker || s.State.DockerSettings == nil || !reflect.DeepEqual(*s.State.DockerSettings, want) {
		t.Errorf("flag %v, settings %+v; want off with the default settings", s.State.Docker, s.State.DockerSettings)
	}
	if len(s.State.ContainerRules) != 0 {
		t.Errorf("rows = %+v, want none", s.State.ContainerRules)
	}
}

func TestDsnCaptureFailsWithoutDockerState(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DROP TABLE firewall_container_rules")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := capture(ctx, h.env(), h.deps(), nil, false); err == nil {
		t.Error("capture succeeded without the container rows, so a restore of its snapshot would delete them")
	}
	if ids := stTestIDs(t, h.db); len(ids) != 0 {
		t.Errorf("a failed capture saved %v", ids)
	}
}

func TestDsnStateJSON(t *testing.T) {
	ctx := context.Background()
	var legacy snapshotState
	if err := json.Unmarshal([]byte(`{"enabled":true,"sysctl":{},"docker":false}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.DockerSettings != nil || legacy.ContainerRules != nil {
		t.Errorf("a state stored before the integration decodes to %+v, want no settings and no rows", legacy)
	}

	settings := dsnSettings()
	in := stTestSnap(1, false)
	in.State.Docker, in.State.DockerSettings, in.State.ContainerRules = true, &settings, dsnRules()
	raw, err := json.Marshal(in.State)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"docker", "dockerSettings", "containerRules"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("the stored state has no %q key: %s", k, raw)
		}
	}
	empty, err := json.Marshal(snapshotState{Enabled: true, Sysctl: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	var emptyKeys map[string]json.RawMessage
	if err := json.Unmarshal(empty, &emptyKeys); err != nil {
		t.Fatal(err)
	}
	if _, ok := emptyKeys["containerRules"]; ok {
		t.Errorf("a state without rows stores a containerRules key: %s", empty)
	}

	db := fwTestDB(t)
	if _, err := saveSnapshot(ctx, db, in); err != nil {
		t.Fatal(err)
	}
	got, err := getSnapshot(ctx, db, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.State.Docker || got.State.DockerSettings == nil || !reflect.DeepEqual(*got.State.DockerSettings, settings) ||
		!reflect.DeepEqual(got.State.ContainerRules, dsnRules()) {
		t.Errorf("state after a save and a read = %+v", got.State)
	}
}
