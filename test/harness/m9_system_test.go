//go:build harness

package harness

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestM9SystemPackages(t *testing.T) {
	vm := bootM9VM(t)
	vm.waitModuleAvailable(t, "system", 2*time.Minute) // A14
	vm.apply(t, "system.index.refresh", `{}`, "")
	vm.sshMust(t, "apt-get -o DPkg::Lock::Timeout=600 remove -y jq || true")
	ch := vm.apply(t, "system.packages.install", `{"names":["jq"]}`, "")
	vm.sshMust(t, "command -v jq")
	vm.undo(t, ch)
	vm.sshFails(t, "command -v jq") // A18
	if _, err := vm.plan("system.packages.remove", `{"names":["openssh-server"]}`); err == nil || !strings.Contains(err.Error(), "system.protected_package") {
		t.Fatalf("A20: err = %v", err)
	}
	vm.apply(t, "system.autoupdates.set", `{"enabled":true}`, "")
	if out := vm.sshMust(t, "apt-config dump | grep -c 'Periodic::Unattended-Upgrade \"1\"'"); strings.TrimSpace(out) != "1" {
		t.Fatalf("A25: %s", out)
	}
	audit := vm.auditFor(t, ch)
	if !strings.Contains(audit, "jq") { // A37
		t.Fatalf("audit row lacks jq: %s", audit)
	}
	if n := len(vm.sshMust(t, "apt-cache pkgnames")); n > 8<<20 { // a quarter of the apt-cache rule's MaxOutput (32 MiB)
		t.Fatalf("pkgnames output %d bytes is over a quarter of the apt-cache rule's 32 MiB cap", n)
	}
}

func TestM9SystemUpgradeAndRepair(t *testing.T) {
	vm := bootM9VM(t)
	vm.apply(t, "system.index.refresh", `{}`, "")
	vm.apply(t, "system.packages.upgrade", `{"all":true}`, vm.name)
	u := vm.readJSON(t, "system", "updates")
	if len(u.Upgradable) != 0 { // A16: only held back remains
		t.Fatalf("upgradable after upgrade all: %v", u.Upgradable)
	}
	// An unpacked, unconfigured package is what an interrupted dpkg leaves.
	vm.sshMust(t, "apt-get -o DPkg::Lock::Timeout=600 install -y -q jq && cd \"$(mktemp -d)\" && apt-get download -q jq && dpkg --unpack jq_*.deb")
	err := eventually(vm.ctx, 2*time.Minute, func(ctx context.Context) error {
		var u m9Read
		if err := vm.f.api.Resource(ctx, vm.hostID, "system.updates", &u); err != nil {
			return err
		}
		if !u.DpkgInterrupted {
			return errors.New("dpkgInterrupted not reported")
		}
		return nil
	})
	if err != nil { // A46; the updates read is cached for 60 seconds
		t.Fatal(err)
	}
	vm.apply(t, "system.packages.repair", `{}`, vm.name)
	if out := vm.sshMust(t, "dpkg --audit"); strings.TrimSpace(out) != "" {
		t.Fatalf("dpkg --audit after repair: %s", out)
	}
}

func TestM9SystemDockerInstall(t *testing.T) {
	vm := bootM9VM(t)
	vm.sshMust(t, "ufw --force enable")
	ch := vm.applyComposite(t, "system.docker.install", `{"hostId":"`+vm.hostID+`","firewallIntegration":true}`, 15*time.Minute)
	vm.confirm(t, ch)
	vm.waitModuleAvailable(t, "docker", 2*time.Minute)       // A26, no agent restart
	if !vm.readJSON(t, "firewall", "docker").IntegrationOn { // A27
		t.Fatal("firewall Docker integration off after install")
	}
	probe, err := m3dxImage("pebble")
	if err != nil {
		t.Fatal(err)
	}
	vm.sshMust(t, "docker run -d -p 18080:14000 --name probe "+probe)
	m3dWaitRunning(vm.ctx, t, vm.f.agent, "probe")
	if open := scanPort(t, vm.addr, 18080); open { // A27: blocked until a rule allows it
		t.Fatal("published port reachable from the scanner")
	}
	vm.sshMust(t, "docker rm -f probe")
	vm.uninstallDocker(t, false)
	ch = vm.applyComposite(t, "system.docker.install", `{"hostId":"`+vm.hostID+`","firewallIntegration":true}`, 1*time.Minute)
	vm.awaitState(t, ch, "reverted", 5*time.Minute) // A42: not confirmed
	vm.sshFails(t, "dpkg -s docker-ce")
	vm.sshFails(t, "test -e /etc/apt/sources.list.d/docker.list || test -e /usr/share/keyrings/docker-archive-keyring.gpg")
}

func TestM9SystemDockerUpdateUninstall(t *testing.T) {
	vm := bootM9VM(t)
	vm.installDockerVersion(t, vm.olderDockerVersion(t))      // over SSH from Docker's repository
	vm.apply(t, "system.docker.engine-update", `{}`, vm.name) // A30
	if v := vm.readJSON(t, "system", "docker"); v.UpdateAvailable {
		t.Fatal("update still available after update")
	}
	probe, err := m3dxImage("pebble")
	if err != nil {
		t.Fatal(err)
	}
	vm.sshMust(t, "docker run -d --label "+core.LabelModule+"=npm --name npm-probe "+probe)
	if _, err := vm.plan("system.docker.uninstall", `{"purge":false}`); err == nil || !strings.Contains(err.Error(), "npm-probe") { // A44
		t.Fatalf("labelled container not named: %v", err)
	}
	vm.sshMust(t, "docker rm -f npm-probe && docker volume create keep")
	vm.apply(t, "system.docker.uninstall", `{"purge":false}`, vm.name) // A31: keeps data
	vm.sshMust(t, "test -d /var/lib/docker")
	vm.installDockerVersion(t, "")
	vm.apply(t, "system.docker.uninstall", `{"purge":true}`, "purge docker on "+vm.name)
	vm.sshFails(t, "test -e /var/lib/docker || test -e /var/lib/containerd")
}

func TestM9SystemReboot(t *testing.T) {
	onlyOn(t, "ubuntu-24.04")
	vm := bootM9VM(t)
	before := vm.bootTime(t)
	start := time.Now()
	vm.apply(t, "system.host.reboot", `{}`, vm.name)
	vm.waitOffline(t, 2*time.Minute)
	vm.waitOnline(t, 10*time.Minute)
	if d := time.Since(start); d < 10*time.Second { // A24
		t.Fatalf("rebooted after %v", d)
	}
	if !vm.bootTime(t).After(before) {
		t.Fatal("boot time unchanged")
	}
}

func TestM9SystemNeedrestart(t *testing.T) {
	onlyOn(t, "ubuntu-22.04", "ubuntu-24.04", "ubuntu-26.04")
	vm := bootM9VM(t)
	pid := vm.sshMust(t, "systemctl show -p MainPID --value ssh.service")
	libs := vm.sshMust(t, "for l in $(awk '{print $6}' /proc/"+strings.TrimSpace(pid)+"/maps | grep '\\.so' | sort -u); do dpkg -S $l 2>/dev/null | cut -d: -f1; done | sort -u")
	// The first library package whose older version can be installed alone,
	// without removing or downgrading anything else.
	var pkg, older string
	for _, p := range strings.Fields(libs) {
		v := m9xSecondVersion(vm.sshMust(t, "apt-cache madison "+m3Quote(p)))
		if v == "" {
			continue
		}
		if m9xDowngradeOnly(vm.sshMust(t, "apt-get -s -q install --allow-downgrades "+m3Quote(p+"="+v)+" || true"), p) {
			pkg, older = p, v
			break
		}
	}
	if pkg == "" {
		t.Skip("no library of sshd has an older version that installs alone")
	}
	vm.sshMust(t, "apt-get -o DPkg::Lock::Timeout=600 install -y --allow-downgrades "+pkg+"="+older)
	pid = vm.sshMust(t, "systemctl restart ssh && systemctl show -p MainPID --value ssh.service")
	vm.apply(t, "system.packages.upgrade", `{"all":false,"names":["`+pkg+`"]}`, vm.name)   // through the module's needrestart env
	if now := vm.sshMust(t, "systemctl show -p MainPID --value ssh.service"); now != pid { // A48
		t.Fatalf("ssh restarted: %s → %s", pid, now)
	}
	if !slices.Contains(vm.readJSON(t, "system", "reboot").Services, "ssh.service") {
		t.Fatal("reboot resource does not list ssh.service")
	}
}

func TestM9SystemDebianKernelAndDistroDocker(t *testing.T) {
	onlyOn(t, "debian-12", "debian-13")
	vm := bootM9VM(t)
	pv := vm.sshMust(t, "cat /proc/version")
	if !regexp.MustCompile(`Debian \d[^ ]*`).MatchString(pv) { // spec section 4 kernel source, Task 1 gap
		t.Fatalf("/proc/version names no Debian version: %s", pv)
	}
	if vm.os != "debian-12" {
		return
	}
	if !strings.Contains(vm.sshMust(t, "apt-cache policy docker.io"), "20.10") {
		t.Skip("A43: docker.io on this image is not 20.10")
	}
	vm.sshMust(t, "apt-get -o DPkg::Lock::Timeout=600 install -y docker.io")
	d := vm.readJSON(t, "system", "docker")
	if d.State != "distro" || !slices.Contains(d.Conflicts, "docker.io") { // A43
		t.Fatalf("docker = %+v", d)
	}
}
