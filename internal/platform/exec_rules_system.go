package platform

import (
	"regexp"
	"slices"
)

// DpkgQueryFormat is the fixed dpkg-query format of the system module: one
// tab-separated line per package. dpkg-query expands the \t and \n escapes
// itself; the runner refuses control characters in an argument.
const DpkgQueryFormat = `${Package}\t${Version}\t${Architecture}\t${Status}\t${Essential}\t${Priority}\t${binary:Summary}\n`

const (
	aptPlanLockTimeout  = "DPkg::Lock::Timeout=10"
	aptPhasedOption     = "APT::Get::Always-Include-Phased-Updates=true"
	aptDockerSourceList = "/etc/apt/sources.list.d/docker.list"
)

// systemPackageNamePattern admits no leading "-", ":", "=", "/" or glob
// character, so apt reads a name as nothing but an exact package name.
var systemPackageNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]{1,63}$`)

// systemAptEnv is the environment of every system apt and dpkg run.
// NEEDRESTART_SUSPEND keeps Ubuntu's needrestart hook from restarting
// services, docker.service among them, after a library upgrade.
var systemAptEnv = []string{"DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_SUSPEND=1", "NEEDRESTART_MODE=l"}

var (
	aptRunPrefix = []string{
		"-y", "-q", "-o", "Dpkg::Use-Pty=0", "-o", aptLockTimeout,
		"-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold",
	}
	aptPlanPrefix  = []string{"-s", "-q", "-o", aptPlanLockTimeout}
	aptResimPrefix = []string{"-s", "-q", "-o", aptLockTimeout}
	aptReadPrefix  = []string{"-s", "-q", "-o", "Debug::NoLocking=true"}
)

// systemAptVerbs are the offered verbs: their words in the vector and
// whether they take package names (a verb either always does or never does).
var systemAptVerbs = map[string]struct {
	words []string
	names bool
}{
	"install":              {[]string{"install"}, true},
	"install-only-upgrade": {[]string{"install", "--only-upgrade"}, true},
	"dist-upgrade":         {[]string{"dist-upgrade"}, false},
	"remove":               {[]string{"remove"}, true},
	"purge":                {[]string{"purge"}, true},
	"fix-install":          {[]string{"-f", "install"}, false},
}

// aptVector builds prefix, opts, the verb's words, then "--" and the names
// when there are any. It does not validate: an unknown verb is its own word,
// and the exec rule refuses what it does not offer.
func aptVector(prefix, opts []string, verb string, names []string) []string {
	words := []string{verb}
	if v, ok := systemAptVerbs[verb]; ok {
		words = v.words
	}
	v := slices.Concat(prefix, opts, words)
	if len(names) > 0 {
		v = append(append(v, "--"), names...)
	}
	return v
}

// SystemAptRun is the mutating apt-get vector of verb.
func SystemAptRun(verb string, names []string) []string {
	return aptVector(aptRunPrefix, nil, verb, names)
}

// SystemAptPlan is the plan simulation of verb, waiting 10 seconds for the
// dpkg lock.
func SystemAptPlan(verb string, names []string) []string {
	return aptVector(aptPlanPrefix, nil, verb, names)
}

// SystemAptResim is the task's own re-simulation of verb, waiting as long
// for the lock as the mutating run does.
func SystemAptResim(verb string, names []string) []string {
	return aptVector(aptResimPrefix, nil, verb, names)
}

// SystemAptRead is the non-locking simulation a resource read runs; opts go
// before the verb.
func SystemAptRead(verb string, opts []string, names []string) []string {
	return aptVector(aptReadPrefix, opts, verb, names)
}

// SystemAptCheck is the plan lock probe: it takes the dpkg lock with a
// 10-second timeout and changes nothing.
func SystemAptCheck() []string {
	return []string{"-q", "-o", aptPlanLockTimeout, "check"}
}

// SystemAptUpdate refreshes every source, or only sourceList when it is
// not "", as APT-1 does for Tailscale's.
func SystemAptUpdate(sourceList string) []string {
	if sourceList == "" {
		return []string{"-q", "-o", aptLockTimeout, "update"}
	}
	return []string{
		"-q", "-o", aptLockTimeout, "-o", "Dir::Etc::sourcelist=" + sourceList,
		"-o", "Dir::Etc::sourceparts=-", "-o", "APT::Get::List-Cleanup=0", "update",
	}
}

// SystemDpkgAudit lists half-installed and unconfigured packages.
func SystemDpkgAudit() []string { return []string{"--audit"} }

// SystemDpkgConfigure finishes an interrupted dpkg run, keeping changed
// configuration files without a prompt.
func SystemDpkgConfigure() []string {
	return []string{"--force-confdef", "--force-confold", "--configure", "-a"}
}

// SystemDpkgQuery lists installed packages in DpkgQueryFormat.
func SystemDpkgQuery() []string { return []string{"-W", "-f=" + DpkgQueryFormat} }

// SystemAptCachePolicy is the policy of names.
func SystemAptCachePolicy(names []string) []string {
	return append([]string{"policy", "--"}, names...)
}

// SystemAptCachePkgnames lists every package name the sources offer.
func SystemAptCachePkgnames() []string { return []string{"pkgnames"} }

// SystemAptConfigDump prints apt's configuration.
func SystemAptConfigDump() []string { return []string{"dump"} }

// SystemNeedrestart lists, in batch mode, what needs a restart and restarts
// nothing.
func SystemNeedrestart() []string { return []string{"-b", "-r", "l"} }

// SystemAptMarkShowhold lists held packages.
func SystemAptMarkShowhold() []string { return []string{"showhold"} }

// systemRules is the exec allowlist of the system module. Mutating apt and
// dpkg runs keep their output's tail: one killed for its output would leave
// dpkg interrupted.
func systemRules() []ExecRule {
	env := func() []string { return slices.Clone(systemAptEnv) }
	exactly := func(vector []string) func([]string) error {
		return func(args []string) error { return execOneOf(args, vector) }
	}
	return []ExecRule{
		{Path: "/usr/bin/apt-get", Match: execMatchSystemAptRun, Env: env(), KeepTail: true},
		{Path: "/usr/bin/apt-get", Match: execMatchSystemAptSim, Env: env()},
		{Path: "/usr/bin/dpkg", Match: exactly(SystemDpkgConfigure()), Env: env(), KeepTail: true},
		{Path: "/usr/bin/dpkg", Match: exactly(SystemDpkgAudit()), Env: env()},
		{Path: "/usr/bin/dpkg-query", Match: exactly(SystemDpkgQuery())},
		{Path: "/usr/bin/apt-cache", Match: execMatchAptCache},
		{Path: "/usr/bin/apt-config", Match: exactly(SystemAptConfigDump())},
		{Path: "/usr/sbin/needrestart", Match: exactly(SystemNeedrestart())},
	}
}

// execMatchSystemAptRun allows the mutating runs of the offered verbs and a
// refresh of every source or of Docker's alone.
func execMatchSystemAptRun(args []string) error {
	if execOneOf(args, SystemAptUpdate(""), SystemAptUpdate(aptDockerSourceList)) == nil {
		return nil
	}
	return execMatchAptVerb(args, func(verb string, names []string) [][]string {
		return [][]string{SystemAptRun(verb, names)}
	})
}

// execMatchSystemAptSim allows the plan, re-simulation and read simulations
// of the offered verbs, and the lock probe.
func execMatchSystemAptSim(args []string) error {
	if execOneOf(args, SystemAptCheck()) == nil {
		return nil
	}
	return execMatchAptVerb(args, func(verb string, names []string) [][]string {
		return [][]string{
			SystemAptPlan(verb, names), SystemAptResim(verb, names),
			SystemAptRead(verb, nil, names), SystemAptRead(verb, []string{"-o", aptPhasedOption}, names),
		}
	})
}

// execMatchAptVerb takes the names after the first "--", checks each against
// the pattern, and allows args when it equals a vector that vectors builds
// for an offered verb with that many names.
func execMatchAptVerb(args []string, vectors func(verb string, names []string) [][]string) error {
	var names []string
	if i := slices.Index(args, "--"); i >= 0 {
		names = args[i+1:]
	}
	if !systemNamesValid(names) {
		return errExecVector
	}
	for verb, v := range systemAptVerbs {
		if v.names != (len(names) > 0) {
			continue
		}
		if execOneOf(args, vectors(verb, names)...) == nil {
			return nil
		}
	}
	return errExecVector
}

// execMatchAptCache allows pkgnames and the policy of one or more names.
func execMatchAptCache(args []string) error {
	if execOneOf(args, SystemAptCachePkgnames()) == nil {
		return nil
	}
	if len(args) < 3 || !systemNamesValid(args[2:]) {
		return errExecVector
	}
	return execOneOf(args, SystemAptCachePolicy(args[2:]))
}

func systemNamesValid(names []string) bool {
	for _, n := range names {
		if !systemPackageNamePattern.MatchString(n) {
			return false
		}
	}
	return true
}
