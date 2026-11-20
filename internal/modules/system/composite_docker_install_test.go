package system

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/firewall"
)

func TestDockerInstallSteps(t *testing.T) {
	c := dockerInstallComposer{}
	steps, err := c.Steps(context.Background(), core.Env{}, dockerInstallIn{HostID: "h1", FirewallIntegration: true})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, s := range steps {
		ids = append(ids, s.ID+":"+s.Action.String())
	}
	want := []string{"check:system.docker.check/v1", "engine:system.docker.engine-install/v1", "firewall:firewall.docker.enable/v1"}
	if !slices.Equal(ids, want) {
		t.Fatalf("steps = %v", ids)
	}
	if !steps[2].DeferPlan || steps[1].DeferPlan {
		t.Fatal("only the firewall step defers its plan")
	}
	if steps[0].Host != "" || steps[1].Host != "h1" || steps[2].Host != "h1" {
		t.Fatalf("step hosts = %q, %q, %q", steps[0].Host, steps[1].Host, steps[2].Host)
	}
	var checkIn struct {
		HostID              string `json:"hostId"`
		FirewallIntegration bool   `json:"firewallIntegration"`
	}
	if err := json.Unmarshal(steps[0].Input, &checkIn); err != nil || checkIn.HostID != "${input.hostId}" || !checkIn.FirewallIntegration {
		t.Fatalf("check input = %s", steps[0].Input)
	}
	steps, _ = c.Steps(context.Background(), core.Env{}, dockerInstallIn{HostID: "h1", FirewallIntegration: false})
	if len(steps) != 2 {
		t.Fatalf("without the flag: %d steps", len(steps))
	}
}

func TestDockerCheckRefusesAMismatchedFlag(t *testing.T) {
	for _, c := range []struct {
		flag, moduleOn, ufwActive bool
		ok                        bool
	}{
		{true, true, true, true}, {false, false, false, true}, {false, true, true, false}, {true, true, false, false}, {true, false, true, false},
	} {
		env := fakeAgentsEnv(t, "h1", c.moduleOn, c.ufwActive)
		_, err := dockerCheck{}.Read(context.Background(), env, dockerCheckIn{HostID: "h1", FirewallIntegration: c.flag})
		if (err == nil) != c.ok || (err != nil && errcode.From(err).Code != "system.firewall_flag_mismatch") {
			t.Errorf("%+v: err = %v", c, err)
		}
	}
}

func TestDockerCheckTreatsAnEnabledButUnavailableFirewallAsOff(t *testing.T) {
	for _, c := range []struct {
		flag, ok bool
	}{{true, false}, {false, true}} {
		env := fakeAgentsEnv(t, "h1", true, true)
		if _, err := env.DB.Writer().ExecContext(context.Background(), `UPDATE core_module_state SET available = 0, reason = 'ufw is not installed' WHERE host_id = 'h1'`); err != nil {
			t.Fatal(err)
		}
		_, err := dockerCheck{}.Read(context.Background(), env, dockerCheckIn{HostID: "h1", FirewallIntegration: c.flag})
		if (err == nil) != c.ok || (err != nil && errcode.From(err).Code != "system.firewall_flag_mismatch") {
			t.Errorf("flag %v with the firewall enabled but unavailable: err = %v", c.flag, err)
		}
	}
}

// Additions to the plan's cases.

func TestDockerCheckTreatsAMissingRowAsOffAndReturnsAFailedStatusRead(t *testing.T) {
	env := fakeAgentsEnv(t, "h1", true, true)
	if _, err := (dockerCheck{}).Read(context.Background(), env, dockerCheckIn{HostID: "h2", FirewallIntegration: false}); err != nil {
		t.Fatalf("no row for h2 reads as off: %v", err)
	}
	env.Agents.(coretest.MapHosts)["h1"] = &coretest.FakeAgent{}
	_, err := dockerCheck{}.Read(context.Background(), env, dockerCheckIn{HostID: "h1", FirewallIntegration: false})
	if err == nil || errcode.From(err).Code == errcode.SystemFirewallFlagMismatch || core.IsDefinite(err) {
		t.Fatalf("a failed status read: err = %v; want the read's error, not a mismatch", err)
	}
}

func TestDockerInstallOutputIsTheEngineSet(t *testing.T) {
	out, err := dockerInstallComposer{}.Output(context.Background(), core.Env{}, dockerInstallIn{HostID: "h1"},
		map[string]json.RawMessage{"check": json.RawMessage(`{}`), "engine": json.RawMessage(`{"installed":["docker-ce"],"upgraded":[],"removed":[]}`)})
	if err != nil || !slices.Equal(out.Installed, []string{"docker-ce"}) {
		t.Fatalf("output = %+v, %v", out, err)
	}
	a, err := newSysHost(t).action("system.docker.install")
	if err != nil || a.Spec.Class != core.ClassComposite || !slices.Equal(a.Spec.Rechecks, []string{"docker"}) || !strings.Contains(a.Spec.Summary, "Docker") {
		t.Fatalf("spec = %+v, %v", a.Spec, err)
	}
}

// fakeAgentsEnv is a controller-side env: a migrated controller database with host's firewall core_module_state
// row (enabled and available both moduleOn), and an agent for host answering firewall.status as active or not.
func fakeAgentsEnv(t *testing.T, host string, moduleOn, ufwActive bool) core.Env {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	res, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideController, BinaryVersion: "test", BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: shStart,
	}, set, nil)
	if err != nil || len(res.Failed) > 0 {
		t.Fatalf("migrate: %v %+v", err, res.Failed)
	}
	at := shStart.Format(time.RFC3339Nano)
	for _, id := range []string{host, "h2"} {
		if _, err := db.Writer().ExecContext(ctx, `INSERT INTO core_hosts (id, name, is_local, created_at) VALUES (?, ?, 0, ?)`, id, id, at); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO core_module_state (host_id, module_id, enabled, available, reason, checked_at)
		VALUES (?, ?, ?, ?, NULL, ?)`, host, firewall.ID, moduleOn, moduleOn, at); err != nil {
		t.Fatal(err)
	}
	agent := &coretest.FakeAgent{ResourceFunc: func(_ context.Context, _ core.Principal, name string) (json.RawMessage, error) {
		if name != "firewall.status" {
			return nil, errcode.New(errcode.NotFound, "no resource "+name)
		}
		return json.Marshal(firewall.Status{Supported: true, Active: ufwActive})
	}}
	return core.Env{
		DB: db, Agents: coretest.MapHosts{host: agent},
		Principal: core.Principal{Actor: "user:u1", UserID: "u1", Scope: core.ScopeAdmin},
	}
}
