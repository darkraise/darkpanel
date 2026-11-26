package platform

import (
	"slices"
	"strings"
	"testing"
)

func TestSystemAptVectors(t *testing.T) {
	r := NewRunner(ProductionRules())
	allowed := [][]string{
		append([]string{"/usr/bin/apt-get"}, SystemAptRun("install", []string{"jq"})...),
		append([]string{"/usr/bin/apt-get"}, SystemAptRun("dist-upgrade", nil)...),
		append([]string{"/usr/bin/apt-get"}, SystemAptRun("purge", []string{"jq", "libjq1"})...),
		append([]string{"/usr/bin/apt-get"}, SystemAptPlan("remove", []string{"jq"})...),
		append([]string{"/usr/bin/apt-get"}, SystemAptResim("install-only-upgrade", []string{"curl"})...),
		append([]string{"/usr/bin/apt-get"}, SystemAptRead("dist-upgrade", []string{"-o", "APT::Get::Always-Include-Phased-Updates=true"}, nil)...),
		append([]string{"/usr/bin/apt-get"}, SystemAptUpdate("")...),
		append([]string{"/usr/bin/dpkg"}, SystemDpkgConfigure()...),
		append([]string{"/usr/bin/dpkg"}, SystemDpkgAudit()...),
		append([]string{"/usr/bin/apt-get"}, SystemAptCheck()...),
		append([]string{"/usr/bin/apt-get"}, SystemAptCheckWait()...),
		append([]string{"/usr/bin/apt-cache"}, SystemAptCachePkgnames()...),
		append([]string{"/usr/sbin/needrestart"}, SystemNeedrestart()...),
		append([]string{"/usr/bin/apt-get"}, SystemAptUpdate("/etc/apt/sources.list.d/docker.list")...),
		append([]string{"/usr/bin/dpkg-query"}, SystemDpkgQuery()...),
		append([]string{"/usr/bin/apt-cache"}, SystemAptCachePolicy([]string{"jq"})...),
		append([]string{"/usr/bin/apt-config"}, SystemAptConfigDump()...),
		append([]string{"/usr/bin/apt-mark"}, SystemAptMarkShowhold()...),
	}
	for _, v := range allowed {
		if _, err := r.match(v[0], v[1:]); err != nil {
			t.Errorf("%v refused: %v", v, err)
		}
	}
	refused := [][]string{
		{"/usr/bin/apt-get", "-y", "-q", "install", "--", "-o"}, // option after --? still refused by name validation shape
		{"/usr/bin/apt-get", "install", "jq"},                   // missing the fixed options
		append([]string{"/usr/bin/apt-get"}, SystemAptRun("install", []string{"--allow-unauthenticated"})...),
		append([]string{"/usr/bin/apt-get"}, SystemAptRun("install", []string{"jq;rm"})...),
		append([]string{"/usr/bin/apt-get"}, SystemAptRun("install", []string{"Jq"})...),
		append([]string{"/usr/bin/apt-get"}, SystemAptRun("upgrade", nil)...), // verb not offered
		append([]string{"/usr/bin/apt-get"}, SystemAptRead("dist-upgrade", []string{"-o", "Dir::Etc=/tmp"}, nil)...),
	}
	for _, v := range refused {
		if _, err := r.match(v[0], v[1:]); err == nil {
			t.Errorf("%v allowed", v)
		}
	}
	env, _ := r.match("/usr/bin/apt-get", SystemAptRun("install", []string{"jq"}))
	for _, want := range []string{"DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_SUSPEND=1", "NEEDRESTART_MODE=l"} {
		if !slices.Contains(env, want) {
			t.Errorf("env lacks %s", want)
		}
	}
	if !slices.Equal(AptInstallTailscale(), []string{"install", "-o", aptLockTimeout, "-y", "tailscale"}) {
		t.Error("Tailscale's vector changed")
	}
}

// TestSystemAptVectorWords pins each builder to the spec's vector, word for word.
func TestSystemAptVectorWords(t *testing.T) {
	const run = "-y -q -o Dpkg::Use-Pty=0 -o DPkg::Lock::Timeout=120 -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold "
	tests := []struct {
		got   []string
		words string
	}{
		{SystemAptRun("install", []string{"jq", "curl"}), run + "install -- jq curl"},
		{SystemAptRun("install-only-upgrade", []string{"curl"}), run + "install --only-upgrade -- curl"},
		{SystemAptRun("dist-upgrade", nil), run + "dist-upgrade"},
		{SystemAptRun("fix-install", nil), run + "-f install"},
		{SystemAptRun("purge", []string{"jq"}), run + "purge -- jq"},
		{SystemAptPlan("remove", []string{"jq"}), "-s -q -o DPkg::Lock::Timeout=10 remove -- jq"},
		{SystemAptResim("fix-install", nil), "-s -q -o DPkg::Lock::Timeout=120 -f install"},
		{SystemAptRead("dist-upgrade", nil, nil), "-s -q -o Debug::NoLocking=true dist-upgrade"},
		{SystemAptRead("dist-upgrade", []string{"-o", "APT::Get::Always-Include-Phased-Updates=true"}, nil),
			"-s -q -o Debug::NoLocking=true -o APT::Get::Always-Include-Phased-Updates=true dist-upgrade"},
		{SystemAptCheck(), "-q -o DPkg::Lock::Timeout=10 check"},
		{SystemAptCheckWait(), "-q -o DPkg::Lock::Timeout=120 check"},
		{SystemAptUpdate(""), "-q -o DPkg::Lock::Timeout=120 update"},
		{SystemAptUpdate("/etc/apt/sources.list.d/docker.list"), "-q -o DPkg::Lock::Timeout=120 " +
			"-o Dir::Etc::sourcelist=/etc/apt/sources.list.d/docker.list -o Dir::Etc::sourceparts=- -o APT::Get::List-Cleanup=0 update"},
		{SystemDpkgAudit(), "--audit"},
		{SystemDpkgConfigure(), "--force-confdef --force-confold --configure -a"},
		{SystemAptCachePolicy([]string{"jq"}), "policy -- jq"},
		{SystemAptCachePkgnames(), "pkgnames"},
		{SystemAptConfigDump(), "dump"},
		{SystemNeedrestart(), "-b -r l"},
		{SystemAptMarkShowhold(), "showhold"},
	}
	for _, tt := range tests {
		if want := strings.Fields(tt.words); !slices.Equal(tt.got, want) {
			t.Errorf("vector %q, want %q", tt.got, want)
		}
	}
	if q := SystemDpkgQuery(); len(q) != 2 || q[0] != "-W" || q[1] != "-f="+DpkgQueryFormat {
		t.Errorf("dpkg-query vector %q", q)
	}
}

func TestSystemAptRulesRefuse(t *testing.T) {
	phased := []string{"-o", "APT::Get::Always-Include-Phased-Updates=true"}
	refused := map[string][]string{
		"install without names":         SystemAptRun("install", nil),
		"remove without names":          SystemAptPlan("remove", nil),
		"dist-upgrade with names":       SystemAptRun("dist-upgrade", []string{"jq"}),
		"fix-install with names":        SystemAptResim("fix-install", []string{"jq"}),
		"a one-character name":          SystemAptRun("install", []string{"j"}),
		"a 65-character name":           SystemAptRun("install", []string{"j" + strings.Repeat("q", 64)}),
		"a name with an architecture":   SystemAptRun("install", []string{"jq:amd64"}),
		"a name with a version":         SystemAptRun("install", []string{"jq=1.7"}),
		"an empty name":                 SystemAptRun("remove", []string{""}),
		"phased options on a plan":      append(slices.Clone(phased), SystemAptPlan("dist-upgrade", nil)...),
		"the read option twice":         SystemAptRead("dist-upgrade", append(slices.Clone(phased), phased...), nil),
		"a read without NoLocking":      {"-s", "-q", "dist-upgrade"},
		"a run without -y":              SystemAptRun("install", []string{"jq"})[1:],
		"a check with a name":           append(SystemAptCheck(), "--", "jq"),
		"a check with another timeout":  {"-q", "-o", "DPkg::Lock::Timeout=300", "check"},
		"another source list":           SystemAptUpdate("/etc/apt/sources.list.d/evil.list"),
		"a source list beside docker's": SystemAptUpdate("/etc/apt/sources.list.d/../docker.list"),
		"a simulation that installs":    slices.DeleteFunc(SystemAptPlan("install", []string{"jq"}), func(s string) bool { return s == "-s" }),
		"no arguments":                  nil,
	}
	for name, args := range refused {
		if execAllowed("/usr/bin/apt-get", args...) {
			t.Errorf("%s: %q is allowed", name, args)
		}
	}
	others := map[string][]string{
		"/usr/bin/dpkg":         {"--configure", "-a", "--force-confdef"},
		"/usr/bin/dpkg-query":   {"-W", "-f=${Package}\\n"},
		"/usr/bin/apt-cache":    {"policy", "jq"},
		"/usr/bin/apt-config":   {"dump", "APT"},
		"/usr/sbin/needrestart": {"-r", "a"},
	}
	for path, args := range others {
		if execAllowed(path, args...) {
			t.Errorf("%s %q is allowed", path, args)
		}
	}
	if execAllowed("/usr/bin/apt-cache", SystemAptCachePolicy(nil)...) || execAllowed("/usr/bin/apt-cache", SystemAptCachePolicy([]string{"-x"})...) {
		t.Error("apt-cache policy allowed without a valid name")
	}
}

func TestSystemRulesMaxOutput(t *testing.T) {
	rule, err := NewRunner(ProductionRules()).matchRule("/usr/bin/apt-cache", SystemAptCachePkgnames())
	if err != nil || rule.MaxOutput != 32<<20 || rule.KeepTail {
		t.Errorf("apt-cache pkgnames: MaxOutput %d, KeepTail %v, %v; want 32 MiB without KeepTail", rule.MaxOutput, rule.KeepTail, err)
	}
	for _, r := range ProductionRules() {
		if r.Path != "/usr/bin/apt-cache" && r.MaxOutput != 0 {
			t.Errorf("%s: MaxOutput %d, want the default", r.Path, r.MaxOutput)
		}
	}
}

func TestSystemRulesKeepTail(t *testing.T) {
	r := NewRunner(ProductionRules())
	tests := []struct {
		path string
		args []string
		want bool
	}{
		{"/usr/bin/apt-get", SystemAptRun("install", []string{"jq"}), true},
		{"/usr/bin/apt-get", SystemAptRun("fix-install", nil), true},
		{"/usr/bin/apt-get", SystemAptUpdate(""), true},
		{"/usr/bin/dpkg", SystemDpkgConfigure(), true},
		{"/usr/bin/apt-get", SystemAptPlan("install", []string{"jq"}), false},
		{"/usr/bin/apt-get", SystemAptCheck(), false},
		{"/usr/bin/apt-get", AptInstallTailscale(), false},
		{"/usr/bin/dpkg-query", SystemDpkgQuery(), false},
	}
	for _, tt := range tests {
		rule, err := r.matchRule(tt.path, tt.args)
		if err != nil || rule.KeepTail != tt.want {
			t.Errorf("%s %q: KeepTail = %v, %v; want %v", tt.path, tt.args, rule.KeepTail, err, tt.want)
		}
	}
	env, err := r.match("/usr/sbin/needrestart", SystemNeedrestart())
	if err != nil || slices.ContainsFunc(env, func(e string) bool { return strings.HasPrefix(e, "NEEDRESTART_") }) {
		t.Errorf("needrestart env = %q, %v; NEEDRESTART_SUSPEND would make it list nothing", env, err)
	}
}

func TestJournalctlRule(t *testing.T) {
	r := NewRunner(ProductionRules())
	all := JournalArgs{
		Unit: "systemd-networkd.service", Priority: 0, Since: 1764000000, Until: 1764003600,
		AfterCursor: "s=0a1b;i=2c;b=3d4e;m=5f;t=6a;x=7b", Grep: `\Qa(b[c.d\E\\E\Q\E`, Lines: 1001, Reverse: true,
	}
	allowed := [][]string{
		JournalctlProbe(),
		JournalctlRead(JournalArgs{Priority: -1}),
		JournalctlRead(all),
		JournalctlRead(JournalArgs{Unit: "user@1000.service", Priority: 7, Lines: 500, Follow: true}),
		JournalctlRead(JournalArgs{Priority: -1, Grep: `\Qplain\E`, Follow: true, Lines: 1}),
		JournalctlRead(JournalArgs{Unit: `systemd-fsck@dev-disk-by\x2duuid-1.service`, Priority: -1}),
	}
	for _, v := range allowed {
		if _, err := r.match(JournalctlPath, v); err != nil {
			t.Errorf("journalctl %v refused: %v", v, err)
		}
	}
	base := JournalctlRead(JournalArgs{Priority: -1})
	with := func(extra ...string) []string { return append(slices.Clone(base), extra...) }
	refused := map[string][]string{
		"--file":              with("--file=/var/log/journal/x/system.journal"),
		"--directory":         with("--directory=/home/u"),
		"--root":              with("--root=/mnt"),
		"--merge":             with("--merge"),
		"a bare unit name":    with("--unit=nginx"),
		"a unit with a slash": with("--unit=../x.service"),
		"a unit glob":         with("--unit=*.service"),
		"priority 8":          with("--priority=8"),
		"priority by name":    with("--priority=err"),
		"a relative since":    with("--since=yesterday"),
		"a cursor with junk":  with("--after-cursor=s=1;--file"),
		"a regular grep":      with("--grep=a.*b"),
		"an open quote":       with(`--grep=\Qa\Eb.*\E`),
		"lines over 1001":     with("--lines=5000"),
		"lines zero":          with("--lines=0"),
		"case sensitive":      with("--case-sensitive=yes"),
		"a flag twice":        with("--unit=a.service", "--unit=b.service"),
		"a positional match":  with("_PID=1"),
		"export output":       {"--output=export", "--no-pager", "--all", "--output-fields=__REALTIME_TIMESTAMP,PRIORITY,_SYSTEMD_UNIT,_PID,MESSAGE"},
		"no base flags":       {"--unit=nginx.service"},
		"the probe on a file": append(JournalctlProbe(), "--file=/x"),
	}
	for name, v := range refused {
		if _, err := r.match(JournalctlPath, v); err == nil {
			t.Errorf("%s: journalctl %v allowed", name, v)
		}
	}
	if _, err := r.match("/bin/journalctl", base); err == nil {
		t.Error("journalctl allowed at another path")
	}
}
