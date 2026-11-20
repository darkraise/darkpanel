package system

import (
	"strings"
	"testing"
)

func TestRebootResource(t *testing.T) {
	cases := []struct {
		name  string
		setup func(h *sysHost)
		want  string
	}{
		{"reboot-required file", func(h *sysHost) {
			h.file("/run/reboot-required", nil)
			h.file("/run/reboot-required.pkgs", []byte("linux-image-6.8.0-48-generic\nlibc6\n"))
		}, `"required":true,"packages":["libc6","linux-image-6.8.0-48-generic"]`},
		{"debian kernel newer than running", func(h *sysHost) {
			h.osRelease("debian", "bookworm", "amd64")
			h.unameR("6.1.0-18-amd64")
			h.file("/proc/version", []byte("Linux version 6.1.0-18-amd64 (debian-kernel@lists.debian.org) #1 SMP PREEMPT_DYNAMIC Debian 6.1.76-1 (2024-02-01)\n"))
			h.dpkgVersions(map[string]string{"linux-image-6.1.0-18-amd64": "6.1.90-1"})
		}, `"required":true,"packages":[],"kernel":{"running":"6.1.76-1","newest":"6.1.90-1"}`},
		{"nothing pending", func(h *sysHost) {
			h.osRelease("debian", "bookworm", "amd64")
			h.unameR("6.1.0-18-amd64")
			h.file("/proc/version", []byte("Linux version 6.1.0-18-amd64 #1 SMP PREEMPT_DYNAMIC Debian 6.1.90-1 (2024-05-01)\n"))
			h.dpkgVersions(map[string]string{"linux-image-6.1.0-18-amd64": "6.1.90-1"})
		}, `"required":false`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newSysHost(t)
			c.setup(h)
			h.run("/usr/sbin/needrestart -b -r l", "NEEDRESTART-SVC: ssh.service\n", 0)
			out := h.readResource(t, "reboot")
			if !strings.Contains(out, c.want) || !strings.Contains(out, `"services":["ssh.service"]`) {
				t.Fatalf("reboot = %s", out)
			}
		})
	}
}

func TestRebootKernelIgnoresMetaPackagesAndReadsTheKernelsOwnDebianVersion(t *testing.T) {
	cases := []struct {
		name  string
		setup func(h *sysHost)
		want  string
	}{
		{"ubuntu meta-package dotted version", func(h *sysHost) {
			h.unameR("6.8.0-45-generic")
			h.dpkgVersions(map[string]string{"linux-image-6.8.0-45-generic": "6.8.0-45.45", "linux-image-generic": "6.8.0.45.45", "linux-image-virtual": "6.8.0.45.45"})
		}, `"required":false`},
		{"ubuntu unsigned kernel", func(h *sysHost) {
			h.unameR("6.8.0-45-generic")
			h.dpkgVersions(map[string]string{"linux-image-unsigned-6.8.0-45-generic": "6.8.0-45.45", "linux-image-unsigned-6.8.0-48-generic": "6.8.0-48.48"})
		}, `"kernel":{"running":"6.8.0-45.45","newest":"6.8.0-48.48"}`},
		{"debian compiler string first", func(h *sysHost) {
			h.osRelease("debian", "bookworm", "amd64")
			h.unameR("6.1.0-18-amd64")
			h.file("/proc/version", []byte("Linux version 6.1.0-18-amd64 (debian-kernel@lists.debian.org) (gcc-12 (Debian 12.2.0-14) 12.2.0, GNU ld (GNU Binutils for Debian) 2.40) #1 SMP PREEMPT_DYNAMIC Debian 6.1.76-1 (2024-02-01)\n"))
			h.dpkgVersions(map[string]string{"linux-image-6.1.0-18-amd64": "6.1.76-1", "linux-image-6.1.0-21-amd64": "6.1.90-1", "linux-image-amd64": "6.1.90-1", "linux-image-6.1.0-21-amd64-dbg": "6.1.99-1"})
		}, `"kernel":{"running":"6.1.76-1","newest":"6.1.90-1"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newSysHost(t)
			c.setup(h)
			h.run("/usr/sbin/needrestart -b -r l", "", 0)
			if out := h.readResource(t, "reboot"); !strings.Contains(out, c.want) {
				t.Fatalf("reboot = %s", out)
			}
		})
	}
}

func TestRebootKernelOnUbuntu(t *testing.T) {
	h := newSysHost(t)
	h.unameR("6.8.0-45-generic")
	h.dpkgVersions(map[string]string{"linux-image-6.8.0-45-generic": "6.8.0-45.45", "linux-image-6.8.0-48-generic": "6.8.0-48.48"})
	if out := h.readResource(t, "reboot"); !strings.Contains(out, `"kernel":{"running":"6.8.0-45.45","newest":"6.8.0-48.48"}`) {
		t.Fatalf("reboot = %s", out)
	}
}

// The brief's docker-ce cases register no apt-cache policy answer, which the
// updateAvailable read needs.
const (
	dockerPolicy    = "/usr/bin/apt-cache policy -- docker-ce"
	dockerPolicyOut = "docker-ce:\n  Installed: 1.0-1\n  Candidate: 5:27.3.1-1~ubuntu.24.04~noble\n"
)

func TestDockerResource(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(h *sysHost)
		substrs []string
	}{
		{"absent, config-files leftover counts as absent", func(h *sysHost) { h.dpkgStatus("docker.io", "deinstall ok config-files") }, []string{`"state":"absent"`}},
		{"distro", func(h *sysHost) { h.installed("docker.io", "runc") }, []string{`"state":"distro","conflicts":["docker.io","runc"]`}},
		{"docker-ce with daemon down and a newer candidate", func(h *sysHost) {
			h.installed("docker-ce")
			h.dockerDaemon(false)
			h.run(dockerPolicy, dockerPolicyOut, 0)
		}, []string{`"state":"docker-ce","version":`, `"running":false`, `"updateAvailable":true`}},
		{"docker-ce with counts and labelled", func(h *sysHost) {
			h.installed("docker-ce")
			h.run(dockerPolicy, dockerPolicyOut, 0)
			h.dockerDaemon(true)
			h.dockerCounts(3, 7, 2, map[string]string{"npm-1": "npm"})
		}, []string{`"running":true`, `"containers":3,"images":7,"volumes":2,"labelled":[{"name":"npm-1","module":"npm"}]`}},
		{"docker-ce at its candidate", func(h *sysHost) {
			h.installed("docker-ce")
			h.run(dockerPolicy, "docker-ce:\n  Installed: 1.0-1\n  Candidate: 1.0-1\n", 0)
			h.dockerDaemon(true)
		}, []string{`"updateAvailable":false`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newSysHost(t)
			c.setup(h)
			out := h.readResource(t, "docker")
			for _, want := range c.substrs {
				if !strings.Contains(out, want) {
					t.Fatalf("docker = %s, want %s", out, want)
				}
			}
		})
	}
}

func TestAutoupdatesResource(t *testing.T) {
	h := newSysHost(t)
	h.installed("unattended-upgrades")
	h.run("/usr/bin/apt-config dump", "APT::Periodic::Update-Package-Lists \"1\";\nAPT::Periodic::Unattended-Upgrade \"1\";\n", 0)
	if out := h.readResource(t, "autoupdates"); out != `{"installed":true,"enabled":true}` {
		t.Fatalf("autoupdates = %s", out)
	}
}
