package platform

import (
	"slices"
	"strings"
	"testing"
)

// execAllowed reports whether the production rules allow path with args,
// without running anything.
func execAllowed(path string, args ...string) bool {
	_, err := NewRunner(ProductionRules()).match(path, args)
	return err == nil
}

func TestProductionRules(t *testing.T) {
	compose := func(tail ...string) []string {
		return append([]string{"compose", "--project-directory", "/srv/a", "-p", "a"}, tail...)
	}
	type vec struct {
		args []string
		ok   bool
	}
	tests := map[string][]vec{
		"/usr/sbin/useradd": {
			{[]string{"--system", "--no-create-home", "--shell", "/usr/sbin/nologin", "darkpanel"}, true},
			{[]string{"--system", "--no-create-home", "--shell", "/usr/sbin/nologin", "darkpanel-derp"}, true},
			{[]string{"--system", "--no-create-home", "--shell", "/usr/sbin/nologin", "root"}, false},
			{[]string{"--system", "--shell", "/usr/sbin/nologin", "darkpanel"}, false},
			{[]string{"darkpanel"}, false},
		},
		"/usr/sbin/userdel": {
			{[]string{"darkpanel"}, true},
			{[]string{"darkpanel-derp"}, true},
			{[]string{"root"}, false},
			{[]string{"-r", "darkpanel"}, false},
		},
		"/usr/sbin/ufw": {
			{[]string{"--force", "allow", "22/tcp"}, true},
			{[]string{"--dry-run", "delete", "3"}, true},
			{[]string{"reload"}, true},
			{[]string{"status"}, false},
			{[]string{"--force", "--dry-run", "allow", "22"}, false},
			{nil, false},
		},
		"/usr/sbin/iptables":  {{[]string{"--version"}, true}, {[]string{"-L"}, false}, {nil, false}},
		"/usr/sbin/ip6tables": {{[]string{"--version"}, true}, {[]string{"-S"}, false}},
		"/usr/bin/apt-mark": {
			{[]string{"hold", "tailscale"}, true},
			{[]string{"unhold", "tailscale"}, true},
			{[]string{"showhold"}, true},
			{[]string{"hold", "nginx"}, false},
		},
		"/usr/bin/dpkg": {{[]string{"--configure", "-a"}, true}, {[]string{"-i", "/tmp/x.deb"}, false}},
		"/usr/sbin/nft": {
			{[]string{"list", "tables"}, true},
			{[]string{"list", "table", "inet", "filter"}, true},
			{[]string{"list", "table", "inet", "a/b"}, false},
			{[]string{"list", "table", "foo", "filter"}, false},
			{[]string{"flush", "ruleset"}, false},
		},
		"/usr/bin/docker": {
			{compose("-f", "/srv/a/c.yml", "up", "-d", "--no-build"), true},
			{compose("-f", "/srv/a/c.yml", "-f", "/srv/a/o.yml", "--env-file", "/srv/a/.env", "--profile", "web", "down"), true},
			{compose("-f", "/srv/a/c.yml", "pull"), true},
			{compose("-f", "/srv/a/c.yml", "restart"), true},
			{compose("-f", "/srv/a/c.yml", "config", "--format", "json"), true},
			{compose("-f", "c.yml", "up", "-d", "--no-build"), false},
			{compose("up", "-d", "--no-build"), false},
			{compose("-f", "/srv/a/c.yml", "exec", "web", "sh"), false},
			{compose("-f", "/srv/a/c.yml", "up", "-d"), false},
			{compose("-f", "/srv/a/c.yml", "--env-file", ".env", "down"), false},
			{[]string{"run", "alpine"}, false},
		},
		"/usr/bin/systemctl": {{[]string{"restart", "tailscaled.service"}, false}},
	}
	for _, family := range []string{"iptables", "ip6tables"} {
		for _, variant := range []string{"", "-legacy", "-nft"} {
			tests["/usr/sbin/"+family+variant+"-save"] = []vec{
				{[]string{"-t", "nat"}, true}, {[]string{"-t", "security"}, true}, {[]string{"-t", "bogus"}, false}, {[]string{"-t"}, false},
			}
			tests["/usr/sbin/"+family+variant+"-restore"] = []vec{
				{[]string{"-n"}, true}, {[]string{"-n", "-v"}, false}, {nil, false},
			}
		}
	}
	for path, vecs := range tests {
		for _, v := range vecs {
			if got := execAllowed(path, v.args...); got != v.ok {
				t.Errorf("%s %q allowed = %v, want %v", path, v.args, got, v.ok)
			}
		}
	}
}

func TestAptVectors(t *testing.T) {
	deb := TailscaleDebDir + "/tailscale_1.80.2_amd64.deb"
	tests := []struct {
		name  string
		got   []string
		words string // the spec's vector, word for word
	}{
		{"APT-1", AptUpdateTailscale(), "update -o DPkg::Lock::Timeout=120 -o Dir::Etc::sourcelist=/etc/apt/sources.list.d/tailscale.list " +
			"-o Dir::Etc::sourceparts=- -o APT::Get::List-Cleanup=0"},
		{"APT-2", AptInstallTailscale(), "install -o DPkg::Lock::Timeout=120 -y tailscale"},
		{"APT-3", AptInstallVersion("1.80.2"), "install -o DPkg::Lock::Timeout=120 -y --allow-downgrades tailscale=1.80.2"},
		{"APT-4", AptStageVersion("1.80.2"),
			"install -o DPkg::Lock::Timeout=120 -y --allow-downgrades --download-only --reinstall tailscale=1.80.2"},
		{"APT-5", AptInstallDeb(deb), "install -o DPkg::Lock::Timeout=120 -y --allow-downgrades " + deb},
		{"APT-6", AptRemoveTailscale(), "remove -o DPkg::Lock::Timeout=120 -y tailscale"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if want := strings.Fields(tt.words); !slices.Equal(tt.got, want) {
				t.Fatalf("vector %q, want %q", tt.got, want)
			}
			if !execAllowed("/usr/bin/apt-get", tt.got...) {
				t.Errorf("%q is refused", tt.got)
			}
			for i := range len(tt.got) - 1 {
				swapped := slices.Clone(tt.got)
				swapped[i], swapped[i+1] = swapped[i+1], swapped[i]
				if execAllowed("/usr/bin/apt-get", swapped...) {
					t.Errorf("the reordering %q is allowed", swapped)
				}
			}
		})
	}
	refused := map[string][]string{
		"a version outside the pattern":   AptInstallVersion("../x"),
		"an empty version":                AptStageVersion(""),
		"a version of 65 characters":      AptInstallVersion("1" + strings.Repeat("a", 64)),
		"a package outside the directory": AptInstallDeb("/tmp/x.deb"),
		"a file that is not a package":    AptInstallDeb(TailscaleDebDir + "/tailscale_1.80.2_amd64"),
		"a package in a subdirectory":     AptInstallDeb(TailscaleDebDir + "/sub/x.deb"),
		"a package without a name":        AptInstallDeb(TailscaleDebDir + "/.deb"),
		"another package":                 {"install", "-o", "DPkg::Lock::Timeout=120", "-y", "nginx"},
		"no arguments":                    nil,
	}
	for name, args := range refused {
		if execAllowed("/usr/bin/apt-get", args...) {
			t.Errorf("%s: %q is allowed", name, args)
		}
	}
}

func TestProductionRulesEnv(t *testing.T) {
	want := map[string][]string{
		"/usr/bin/apt-get": {"DEBIAN_FRONTEND=noninteractive"},
		"/usr/bin/dpkg":    {"DEBIAN_FRONTEND=noninteractive"},
		"/usr/bin/docker": {
			"DOCKER_CONFIG=/var/lib/darkpanel/agent/docker-config", "HOME=/var/lib/darkpanel/agent/docker-config",
			"COMPOSE_EXPERIMENTAL_GIT_REMOTE=false", "COMPOSE_EXPERIMENTAL_OCI_REMOTE=false",
		},
	}
	for _, rule := range ProductionRules() {
		if !slices.Equal(rule.Env, want[rule.Path]) {
			t.Errorf("%s Env = %q, want %q", rule.Path, rule.Env, want[rule.Path])
		}
	}
}
