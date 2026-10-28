package platform

import (
	"errors"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// TailscaleDebDir is the tailscale module's managed-dir of staged packages
// (spec 4.1).
const TailscaleDebDir = "/var/lib/darkpanel/agent/tailscale-debs"

const aptLockTimeout = "DPkg::Lock::Timeout=120"

var (
	aptVersionPattern = regexp.MustCompile(`^[0-9][0-9A-Za-z.+~-]{0,63}$`)
	// aptDebStemPattern is the managed-dir default name pattern, which a
	// staged package's name before ".deb" matches.
	aptDebStemPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	execNftNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_.-]{0,63}$`)
)

// errExecVector is a Match refusal; the runner reports it without the
// arguments.
var errExecVector = errors.New("argument vector not allowed")

var (
	execSystemUsers = []string{"darkpanel", "darkpanel-derp"}
	execUfwVerbs    = []string{
		"allow", "deny", "reject", "limit", "route", "insert", "prepend", "delete", "default", "logging",
		"enable", "disable", "reload",
	}
	execSaveTables  = []string{"filter", "nat", "raw", "mangle", "security"}
	execNftFamilies = []string{"ip", "ip6", "inet", "arp", "bridge", "netdev"}
)

// AptUpdateTailscale is APT-1: refresh the Tailscale repository only.
func AptUpdateTailscale() []string {
	return []string{
		"update", "-o", aptLockTimeout, "-o", "Dir::Etc::sourcelist=/etc/apt/sources.list.d/tailscale.list",
		"-o", "Dir::Etc::sourceparts=-", "-o", "APT::Get::List-Cleanup=0",
	}
}

// AptInstallTailscale is APT-2: install or update to the repository's
// current version.
func AptInstallTailscale() []string {
	return []string{"install", "-o", aptLockTimeout, "-y", "tailscale"}
}

// AptInstallVersion is APT-3: install an exact version.
func AptInstallVersion(version string) []string {
	return []string{"install", "-o", aptLockTimeout, "-y", "--allow-downgrades", "tailscale=" + version}
}

// AptStageVersion is APT-4: download a version's package without
// installing it.
func AptStageVersion(version string) []string {
	return []string{
		"install", "-o", aptLockTimeout, "-y", "--allow-downgrades", "--download-only", "--reinstall", "tailscale=" + version,
	}
}

// AptInstallDeb is APT-5: install a staged package, TailscaleDebDir/<name>.deb.
func AptInstallDeb(deb string) []string {
	return []string{"install", "-o", aptLockTimeout, "-y", "--allow-downgrades", deb}
}

// AptRemoveTailscale is APT-6: uninstall.
func AptRemoveTailscale() []string {
	return []string{"remove", "-o", aptLockTimeout, "-y", "tailscale"}
}

// ProductionRules is the complete exec allowlist of the agent binary (spec
// 1.8); nothing else may run. Which caller may run a vector is a code rule,
// not a runner rule, and systemd stays on D-Bus (ADR 0012).
func ProductionRules() []ExecRule {
	exactly := func(vectors ...[]string) func([]string) error {
		return func(args []string) error { return execOneOf(args, vectors...) }
	}
	noninteractive := []string{"DEBIAN_FRONTEND=noninteractive"}
	rules := []ExecRule{
		{Path: "/usr/sbin/useradd", Match: execMatchUseradd},
		{Path: "/usr/sbin/userdel", Match: execMatchUserdel},
		{Path: "/usr/sbin/ufw", Match: execMatchUfw},
		{Path: "/usr/sbin/iptables", Match: exactly([]string{"--version"})},
		{Path: "/usr/sbin/ip6tables", Match: exactly([]string{"--version"})},
		{Path: "/usr/bin/apt-get", Match: execMatchAptGet, Env: noninteractive},
		{Path: "/usr/bin/apt-mark", Match: exactly([]string{"hold", "tailscale"}, []string{"unhold", "tailscale"}, []string{"showhold"})},
		{Path: "/usr/bin/dpkg", Match: exactly([]string{"--configure", "-a"}), Env: slices.Clone(noninteractive)},
		{Path: "/usr/sbin/nft", Match: execMatchNft},
		{Path: "/usr/bin/docker", Match: execMatchDocker, Env: []string{
			"DOCKER_CONFIG=/var/lib/darkpanel/agent/docker-config", "HOME=/var/lib/darkpanel/agent/docker-config",
			"COMPOSE_EXPERIMENTAL_GIT_REMOTE=false", "COMPOSE_EXPERIMENTAL_OCI_REMOTE=false",
		}},
	}
	for _, family := range []string{"iptables", "ip6tables"} {
		for _, variant := range []string{"", "-legacy", "-nft"} {
			rules = append(rules,
				ExecRule{Path: "/usr/sbin/" + family + variant + "-save", Match: execMatchSave},
				ExecRule{Path: "/usr/sbin/" + family + variant + "-restore", Match: exactly([]string{"-n"})},
			)
		}
	}
	return rules
}

func execOneOf(args []string, vectors ...[]string) error {
	for _, v := range vectors {
		if slices.Equal(args, v) {
			return nil
		}
	}
	return errExecVector
}

func execMatchUseradd(args []string) error {
	if len(args) == 0 || !slices.Contains(execSystemUsers, args[len(args)-1]) {
		return errExecVector
	}
	return execOneOf(args, []string{"--system", "--no-create-home", "--shell", "/usr/sbin/nologin", args[len(args)-1]})
}

func execMatchUserdel(args []string) error {
	if len(args) != 1 || !slices.Contains(execSystemUsers, args[0]) {
		return errExecVector
	}
	return nil
}

// execMatchUfw allows [--force | --dry-run] <verb> <arg>...; the arguments
// after the verb are constrained by the firewall module's input schema.
func execMatchUfw(args []string) error {
	if len(args) > 0 && (args[0] == "--force" || args[0] == "--dry-run") {
		args = args[1:]
	}
	if len(args) == 0 || !slices.Contains(execUfwVerbs, args[0]) {
		return errExecVector
	}
	return nil
}

func execMatchSave(args []string) error {
	if len(args) != 2 || args[0] != "-t" || !slices.Contains(execSaveTables, args[1]) {
		return errExecVector
	}
	return nil
}

func execMatchNft(args []string) error {
	if slices.Equal(args, []string{"list", "tables"}) {
		return nil
	}
	if len(args) != 4 || args[0] != "list" || args[1] != "table" ||
		!slices.Contains(execNftFamilies, args[2]) || !execNftNamePattern.MatchString(args[3]) {
		return errExecVector
	}
	return nil
}

// execMatchAptGet allows exactly the vectors APT-1 to APT-6.
func execMatchAptGet(args []string) error {
	if len(args) == 0 {
		return errExecVector
	}
	last := args[len(args)-1]
	allowed := [][]string{AptUpdateTailscale(), AptInstallTailscale(), AptRemoveTailscale()}
	if version, ok := strings.CutPrefix(last, "tailscale="); ok && aptVersionPattern.MatchString(version) {
		allowed = append(allowed, AptInstallVersion(version), AptStageVersion(version))
	}
	if aptStagedDeb(last) {
		allowed = append(allowed, AptInstallDeb(last))
	}
	return execOneOf(args, allowed...)
}

// aptStagedDeb reports whether deb is TailscaleDebDir/<name>.deb.
func aptStagedDeb(deb string) bool {
	dir, file := filepath.Split(deb)
	stem, ok := strings.CutSuffix(file, ".deb")
	return ok && dir == TailscaleDebDir+"/" && aptDebStemPattern.MatchString(stem)
}

// execMatchDocker allows compose --project-directory <dir> -p <name>
// (-f <file>)+ (--env-file <f>)* (--profile <p>)* <verb>, every path
// absolute and clean.
func execMatchDocker(args []string) error {
	if len(args) < 5 || args[0] != "compose" || args[1] != "--project-directory" || !execCleanAbs(args[2]) ||
		args[3] != "-p" || args[4] == "" {
		return errExecVector
	}
	rest := args[5:]
	files := 0
	for len(rest) >= 2 && rest[0] == "-f" && execCleanAbs(rest[1]) {
		rest, files = rest[2:], files+1
	}
	for len(rest) >= 2 && rest[0] == "--env-file" && execCleanAbs(rest[1]) {
		rest = rest[2:]
	}
	for len(rest) >= 2 && rest[0] == "--profile" && rest[1] != "" {
		rest = rest[2:]
	}
	if files == 0 {
		return errExecVector
	}
	return execOneOf(rest, []string{"up", "-d", "--no-build"}, []string{"down"}, []string{"pull"}, []string{"restart"},
		[]string{"config", "--format", "json"})
}

func execCleanAbs(p string) bool { return filepath.IsAbs(p) && filepath.Clean(p) == p }
