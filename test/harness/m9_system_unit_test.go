//go:build harness

package harness

import (
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestM9xSecondVersion(t *testing.T) {
	madison := ` docker-ce | 5:29.9.1-1~ubuntu.24.04~noble | https://download.docker.com/linux/ubuntu noble/stable amd64 Packages
 docker-ce | 5:29.9.0-1~ubuntu.24.04~noble | https://download.docker.com/linux/ubuntu noble/stable amd64 Packages
`
	if got := m9xSecondVersion(madison); got != "5:29.9.0-1~ubuntu.24.04~noble" {
		t.Errorf("second version = %q", got)
	}
	if got := m9xSecondVersion(" jq | 1.7.1-3build1 | http://archive.ubuntu.com/ubuntu noble/main amd64 Packages\n"); got != "" {
		t.Errorf("one version: second = %q, want empty", got)
	}
}

func TestM9xBootTime(t *testing.T) {
	got, err := m9xBootTime("cpu  1 2 3\nintr 5\nbtime 1795000000\nprocesses 9\n")
	if err != nil || !got.Equal(time.Unix(1795000000, 0)) {
		t.Errorf("boot time = %v, %v", got, err)
	}
	if _, err := m9xBootTime("cpu 1\n"); err == nil {
		t.Error("no btime line: want an error")
	}
}

func TestM9xDowngradeOnly(t *testing.T) {
	alone := "Inst libssl3t64 [3.0.13-0ubuntu3.6] (3.0.13-0ubuntu3 Ubuntu:24.04/noble [amd64])\nConf libssl3t64 (3.0.13-0ubuntu3 Ubuntu:24.04/noble [amd64])\n"
	if !m9xDowngradeOnly(alone, "libssl3t64") {
		t.Error("a lone downgrade: want true")
	}
	withOther := alone + "Inst openssl [3.0.13-0ubuntu3.6] (3.0.13-0ubuntu3 Ubuntu:24.04/noble [amd64])\n"
	removal := alone + "Remv libc-bin [2.39-0ubuntu8.6]\n"
	for name, sim := range map[string]string{"another package": withOther, "a removal": removal, "nothing": "", "another name": alone} {
		pkg := "libssl3t64"
		if name == "another name" {
			pkg = "libc6"
		}
		if m9xDowngradeOnly(sim, pkg) {
			t.Errorf("%s: want false", name)
		}
	}
}

func TestM9xDockerList(t *testing.T) {
	want := "deb [arch=amd64 signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/debian bookworm stable\n"
	if got := m9xDockerList("amd64", "debian", "bookworm"); got != want {
		t.Errorf("list = %q, want %q", got, want)
	}
}

func TestM9xAvailable(t *testing.T) {
	reason := "docker is not installed"
	h := core.Host{Name: "agent", Modules: []core.ModuleState{{ID: "system", Available: true}, {ID: "docker", Reason: &reason}}}
	if err := m9xAvailable(h, "system"); err != nil {
		t.Errorf("system: %v", err)
	}
	if err := m9xAvailable(h, "docker"); err == nil {
		t.Error("docker: want an error")
	}
	if err := m9xAvailable(h, "npm"); err == nil {
		t.Error("an unlisted module: want an error")
	}
}

func TestM9xPhrase(t *testing.T) {
	if err := m9xPhrase("system.host.reboot", "web-1", "web-1"); err != nil {
		t.Error(err)
	}
	if err := m9xPhrase("system.packages.install", "", ""); err != nil {
		t.Error(err)
	}
	if err := m9xPhrase("system.docker.uninstall", "web-1", "purge docker on web-1"); err == nil {
		t.Error("a different phrase: want an error")
	}
}
