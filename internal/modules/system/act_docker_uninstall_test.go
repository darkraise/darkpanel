package system

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

func TestDockerUninstallRefusals(t *testing.T) {
	cases := []struct {
		name  string
		input string
		setup func(h *sysHost)
		code  errcode.Code
		text  string
	}{
		{"integration on", `{"purge":false}`, func(h *sysHost) { h.firewallDockerIntegration(true) }, "system.firewall_integration_on", "firewall.docker.disable"},
		{"firewall change pending", `{"purge":false}`, func(h *sysHost) { h.pendingChange("c-fw", "firewall.rules.apply", "awaiting_confirmation") }, "system.firewall_pending", "c-fw"},
		{"labelled containers", `{"purge":false}`, func(h *sysHost) {
			h.dockerDaemon(true)
			h.dockerCounts(2, 1, 1, map[string]string{"npm-1": "npm", "derp-1": "derp"})
		}, "system.docker_labelled", "npm-1"},
		{"purge with daemon down", `{"purge":true}`, func(h *sysHost) { h.dockerDaemon(false) }, "system.purge_refused", "daemon"},
		{"purge onto a mount point", `{"purge":true}`, func(h *sysHost) {
			h.dockerDaemon(true)
			h.dockerCounts(0, 0, 0, nil)
			h.purge.checkErr = platform.ErrPurgeMount
		}, "system.purge_refused", "mount"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newSysHost(t)
			h.installed("docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin")
			c.setup(h)
			_, err := h.planErr("system.docker.uninstall", c.input)
			if errcode.From(err).Code != c.code || !strings.Contains(err.Error(), c.text) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestDockerUninstallKeepsOrPurgesData(t *testing.T) {
	h := newSysHost(t)
	h.installed("docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin")
	h.dockerDaemon(false)
	h.run(planVec("remove", sortedDockerPackages), "Remv docker-compose-plugin [2]\nRemv docker-buildx-plugin [0.17]\nRemv docker-ce [5:27.3]\nRemv docker-ce-cli [5:27.3]\nRemv containerd.io [1.7]\n0 upgraded, 0 newly installed, 5 to remove and 0 not upgraded.\n", 0)
	h.run(resimVec("remove", sortedDockerPackages), "Remv docker-compose-plugin [2]\nRemv docker-buildx-plugin [0.17]\nRemv docker-ce [5:27.3]\nRemv docker-ce-cli [5:27.3]\nRemv containerd.io [1.7]\n0 upgraded, 0 newly installed, 5 to remove and 0 not upgraded.\n", 0)
	h.run(runVec("remove", sortedDockerPackages), "", 0)
	plan := h.plan(t, "system.docker.uninstall", `{"purge":false}`)
	if !strings.Contains(plan.Text(), "labelled-container check could not run") || plan.Phrase != h.hostName() {
		t.Fatalf("plan:\n%s phrase=%q", plan.Text(), plan.Phrase)
	}
	if _, err := h.applyTask("system.docker.uninstall", `{"purge":false}`); err != nil {
		t.Fatal(err)
	}
	if len(h.purge.purged) != 0 || !slices.Contains(h.calls(), runVec("remove", sortedDockerPackages)) {
		t.Fatalf("purged=%v calls=%v", h.purge.purged, h.calls())
	}

	h2 := newSysHost(t)
	h2.installed("docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin")
	h2.dockerDaemon(true)
	h2.dockerCounts(3, 5, 2, nil)
	h2.file("/etc/docker/daemon.json", []byte(`{"data-root":"/srv/docker"}`))
	for _, v := range []string{planVec("remove", sortedDockerPackages), resimVec("remove", sortedDockerPackages)} {
		h2.run(v, "Remv docker-ce [5:27.3]\nRemv docker-ce-cli [5:27.3]\nRemv containerd.io [1.7]\nRemv docker-buildx-plugin [0.17]\nRemv docker-compose-plugin [2]\n0 upgraded, 0 newly installed, 5 to remove and 0 not upgraded.\n", 0)
	}
	h2.run(runVec("remove", sortedDockerPackages), "", 0)
	plan = h2.plan(t, "system.docker.uninstall", `{"purge":true}`)
	if plan.Phrase != "purge docker on "+h2.hostName() || !strings.Contains(plan.Text(), "3 containers, 5 images, 2 volumes") || !strings.Contains(plan.Text(), "data-root /srv/docker: its 3 containers, 5 images, 2 volumes are left alone") || strings.Contains(plan.Text(), "volumes: /var/lib/docker") {
		t.Fatalf("plan:\n%s phrase=%q", plan.Text(), plan.Phrase)
	}
	if _, err := h2.applyTask("system.docker.uninstall", `{"purge":true}`); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(h2.purge.purged, []string{"/var/lib/docker", "/var/lib/containerd"}) {
		t.Fatalf("purged = %v", h2.purge.purged)
	}
}

const removeDockerSim = "Remv containerd.io [1.7]\nRemv docker-buildx-plugin [0.17]\nRemv docker-ce [5:27.3]\nRemv docker-ce-cli [5:27.3]\nRemv docker-compose-plugin [2]\n0 upgraded, 0 newly installed, 5 to remove and 0 not upgraded.\n"

// dockerUninstallHost has the five packages installed, the daemon running with nothing in it, and the
// removal registered for plan, re-simulation and run.
func dockerUninstallHost(t *testing.T) *sysHost {
	h := newSysHost(t)
	h.installed(dockerPackages...)
	h.dockerDaemon(true)
	h.dockerCounts(0, 0, 0, nil)
	h.run(planVec("remove", sortedDockerPackages), removeDockerSim, 0)
	h.run(resimVec("remove", sortedDockerPackages), removeDockerSim, 0)
	h.runChanges(runVec("remove", sortedDockerPackages), "", 0, "bash")
	return h
}

func TestDockerUninstallRemovesOnlyOwnedRepoFiles(t *testing.T) {
	h := dockerUninstallHost(t)
	// The list file is Darkpanel's; the keyring was there before and stays.
	if err := (shFiles{h: h}).WriteFile(context.Background(), ID, dockerListPath, []byte("deb x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.file(dockerKeyringPath, []byte("key"))
	plan := h.plan(t, "system.docker.uninstall", `{"purge":false}`)
	if !strings.Contains(plan.Text(), dockerListPath) || strings.Contains(plan.Text(), dockerKeyringPath) {
		t.Fatalf("plan:\n%s", plan.Text())
	}
	out, err := h.applyTask("system.docker.uninstall", `{"purge":false}`)
	if err != nil {
		t.Fatal(err)
	}
	if h.exists(dockerListPath) || !h.exists(dockerKeyringPath) {
		t.Fatalf("list present %v, keyring present %v", h.exists(dockerListPath), h.exists(dockerKeyringPath))
	}
	for _, want := range []string{`"removed":["containerd.io","docker-buildx-plugin","docker-ce","docker-ce-cli","docker-compose-plugin"]`, `"files":["` + dockerListPath + `"]`, `"purged":[]`} {
		if !strings.Contains(out, want) {
			t.Errorf("output %s lacks %s", out, want)
		}
	}
}

func TestDockerUninstallStillRefusesOtherProtectedRemovals(t *testing.T) {
	h := dockerUninstallHost(t)
	h.run(planVec("remove", sortedDockerPackages), "Remv openssh-server [1:9.6]\n"+strings.Replace(removeDockerSim, "5 to remove", "6 to remove", 1), 0)
	_, err := h.planErr("system.docker.uninstall", `{"purge":false}`)
	if errcode.From(err).Code != errcode.SystemProtectedPackage || !strings.Contains(err.Error(), "openssh-server") || strings.Contains(err.Error(), "docker-ce") {
		t.Fatalf("err = %v", err)
	}
}

func TestDockerUninstallTaskRechecksTheRefusals(t *testing.T) {
	h := dockerUninstallHost(t)
	h.plan(t, "system.docker.uninstall", `{"purge":false}`)
	h.pendingChange("c-fw", "firewall.docker.enable", "applying")
	_, err := h.applyTask("system.docker.uninstall", `{"purge":false}`)
	if errcode.From(err).Code != errcode.SystemFirewallPending || slices.Contains(h.calls(), runVec("remove", sortedDockerPackages)) {
		t.Fatalf("err = %v calls=%v", err, h.calls())
	}
}

func TestDockerUninstallNamesEveryLabelledContainer(t *testing.T) {
	h := dockerUninstallHost(t)
	h.dockerCounts(2, 1, 1, map[string]string{"npm-1": "npm", "derp-1": "derp"})
	_, err := h.planErr("system.docker.uninstall", `{"purge":false}`)
	for _, want := range []string{"npm-1 (npm)", "derp-1 (derp)"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want %s", err, want)
		}
	}
}

func TestDockerUninstallIntegrationOnWithTheFirewallDisabled(t *testing.T) {
	h := dockerUninstallHost(t)
	h.firewallDockerIntegration(true)
	h.firewallEnabled(false)
	_, err := h.planErr("system.docker.uninstall", `{"purge":false}`)
	if errcode.From(err).Code != errcode.SystemFirewallIntegrationOn || !strings.Contains(err.Error(), "enable the firewall module") {
		t.Fatalf("err = %v", err)
	}
}

func TestFirewallDockerFlag(t *testing.T) {
	h := newSysHost(t)
	ctx := context.Background()
	if _, err := firewallDockerFlag(ctx, h.env()); err == nil {
		t.Fatal("no error without the firewall's table")
	}
	h.exec(`CREATE TABLE firewall_settings (id INTEGER PRIMARY KEY CHECK (id = 1), docker INTEGER NOT NULL DEFAULT 0, settings TEXT NOT NULL) STRICT`)
	if on, err := firewallDockerFlag(ctx, h.env()); err != nil || on {
		t.Fatalf("no row: on=%v err=%v", on, err)
	}
	h.exec(`INSERT INTO firewall_settings (id, docker, settings) VALUES (1, 1, '{}')`)
	if on, err := firewallDockerFlag(ctx, h.env()); err != nil || !on {
		t.Fatalf("row: on=%v err=%v", on, err)
	}
}

func TestUninstallAndEngineUpdateActOnTheInstalledDockerPackages(t *testing.T) {
	partial := []string{"containerd.io", "docker-ce", "docker-ce-cli"}
	h := newSysHost(t)
	h.installed(partial...)
	h.dockerDaemon(false)
	sim := "Remv docker-ce [5:27.3]\nRemv docker-ce-cli [5:27.3]\nRemv containerd.io [1.7]\n0 upgraded, 0 newly installed, 3 to remove and 0 not upgraded.\n"
	h.run(planVec("remove", partial), sim, 0)
	if _, err := h.planErr("system.docker.uninstall", `{"purge":false}`); err != nil {
		t.Fatalf("uninstall of a docker-ce without its plugins: %v", err)
	}
	h.run(planVec("install-only-upgrade", []string{"docker-ce", "docker-ce-cli", "containerd.io"}), "Inst docker-ce [5:27.0] (5:27.3 Docker:noble [amd64])\n1 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n", 0)
	if _, err := h.planErr("system.docker.engine-update", `{}`); err != nil {
		t.Fatalf("engine-update of a docker-ce without its plugins: %v", err)
	}
	none := newSysHost(t)
	if _, err := none.planErr("system.docker.engine-update", `{}`); errcode.From(err).Code != errcode.SystemNameInvalid {
		t.Fatalf("engine-update without docker-ce: err = %v", err)
	}
}

func TestDockerUninstallPurgeRefusalsTheFakeCanReach(t *testing.T) {
	h := dockerUninstallHost(t)
	h.file("/etc/docker/daemon.json", []byte(`{not json`))
	if _, err := h.planErr("system.docker.uninstall", `{"purge":true}`); errcode.From(err).Code != errcode.SystemPurgeRefused || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("invalid daemon.json: err = %v", err)
	}
	h2 := dockerUninstallHost(t)
	h2.purge.purgeErr = platform.ErrPurgeMount
	_, err := h2.applyTask("system.docker.uninstall", `{"purge":true}`)
	if errcode.From(err).Code != errcode.SystemPurgeRefused || !strings.Contains(err.Error(), "Docker was removed, but /var/lib/docker was not deleted") {
		t.Fatalf("purge failing after the removal: err = %v", err)
	}
	if !slices.Contains(h2.calls(), runVec("remove", sortedDockerPackages)) {
		t.Fatal("the packages were not removed before the purge")
	}
}
