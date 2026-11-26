package platform

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
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

// systemPackageNamePattern keeps a name from reading as an option (no
// leading "-") or carrying an architecture, version or release (no ":",
// "=", "/"). It does not make the name exact: apt still reads a trailing
// "+" or "-" as an action and a name with "." that matches no package as a
// regular expression, so the module admits only names it finds in
// apt-cache pkgnames or among the installed packages.
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

// SystemAptCheckWait is a task's lock probe: it waits for the dpkg lock as
// long as the mutating run does, so a busy apt delays the task instead of
// failing it.
func SystemAptCheckWait() []string {
	return []string{"-q", "-o", aptLockTimeout, "check"}
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
		// pkgnames prints 3.46 MiB on a stock Ubuntu 22.04 image; a list cut
		// short would refuse valid install names, so it fails over the cap.
		{Path: "/usr/bin/apt-cache", Match: execMatchAptCache, MaxOutput: 32 << 20},
		{Path: "/usr/bin/apt-config", Match: exactly(SystemAptConfigDump())},
		{Path: "/usr/sbin/needrestart", Match: exactly(SystemNeedrestart())},
		{Path: JournalctlPath, Match: execMatchJournalctl},
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
	if execOneOf(args, SystemAptCheck(), SystemAptCheckWait()) == nil {
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

// JournalctlPath is the journal reader of the services module.
const JournalctlPath = "/usr/bin/journalctl"

// journalctlBase opens every journal query and follow. --all keeps fields
// over 4 KiB, which the JSON output otherwise prints as null.
var journalctlBase = []string{
	"--output=json", "--no-pager", "--all",
	"--output-fields=__REALTIME_TIMESTAMP,PRIORITY,_SYSTEMD_UNIT,_PID,MESSAGE",
}

// JournalArgs are the filters of a journal query or follow. Zero values
// leave a filter out; Priority below 0 does.
type JournalArgs struct {
	Unit         string
	Priority     int
	Since, Until int64 // seconds since the epoch
	AfterCursor  string
	Grep         string // a pattern of literals only: \Q...\E pieces
	Lines        int
	Reverse      bool
	Follow       bool
}

// JournalctlRead is the journalctl vector of a.
func JournalctlRead(a JournalArgs) []string {
	v := slices.Clone(journalctlBase)
	if a.Unit != "" {
		v = append(v, "--unit="+a.Unit)
	}
	if a.Priority >= 0 {
		v = append(v, "--priority="+strconv.Itoa(a.Priority))
	}
	if a.Since > 0 {
		v = append(v, "--since=@"+strconv.FormatInt(a.Since, 10))
	}
	if a.Until > 0 {
		v = append(v, "--until=@"+strconv.FormatInt(a.Until, 10))
	}
	if a.AfterCursor != "" {
		v = append(v, "--after-cursor="+a.AfterCursor)
	}
	if a.Grep != "" {
		v = append(v, "--grep="+a.Grep, "--case-sensitive=no")
	}
	if a.Lines > 0 {
		v = append(v, "--lines="+strconv.Itoa(a.Lines))
	}
	if a.Reverse {
		v = append(v, "--reverse")
	}
	if a.Follow {
		v = append(v, "--follow")
	}
	return v
}

// JournalctlProbe fails when journalctl cannot match patterns: one built
// without PCRE2, or without the library to load. It prints nothing.
func JournalctlProbe() []string {
	return []string{"--no-pager", "--quiet", "--lines=0", "--grep=probe"}
}

var (
	// JournalUnitPattern is a unit name with its type, as systemd prints it.
	JournalUnitPattern = regexp.MustCompile(`^[A-Za-z0-9:_.\\@-]{1,240}\.(service|socket|timer|mount|path|target|swap|slice|scope|device|automount)$`)
	// JournalCursorPattern is a journal cursor: s=…;i=…;b=…;m=…;t=…;x=….
	JournalCursorPattern = regexp.MustCompile(`^[a-z]=[0-9a-f]{1,64}(;[a-z]=[0-9a-f]{1,64}){0,7}$`)
	journalSecondsArg    = regexp.MustCompile(`^@[0-9]{1,12}$`)
	journalLinesArg      = regexp.MustCompile(`^[0-9]{1,4}$`)
)

// journalMaxLines is the most --lines a query or follow asks for.
const journalMaxLines = 1001

// execMatchJournalctl allows the probe and JournalctlRead vectors: the base
// flags, then each filter at most once with a value of its shape. Every
// other option, --file, --directory, --root and --merge among them, is
// refused.
func execMatchJournalctl(args []string) error {
	if execOneOf(args, JournalctlProbe()) == nil {
		return nil
	}
	if len(args) < len(journalctlBase) || !slices.Equal(args[:len(journalctlBase)], journalctlBase) {
		return errExecVector
	}
	values := map[string]func(string) bool{
		"--unit":           JournalUnitPattern.MatchString,
		"--priority":       func(v string) bool { return len(v) == 1 && v[0] >= '0' && v[0] <= '7' },
		"--since":          journalSecondsArg.MatchString,
		"--until":          journalSecondsArg.MatchString,
		"--after-cursor":   JournalCursorPattern.MatchString,
		"--grep":           journalLiteralPattern,
		"--case-sensitive": func(v string) bool { return v == "no" },
		"--lines": func(v string) bool {
			n, err := strconv.Atoi(v)
			return journalLinesArg.MatchString(v) && err == nil && n >= 1 && n <= journalMaxLines
		},
	}
	seen := map[string]bool{}
	for _, a := range args[len(journalctlBase):] {
		name, value, hasValue := strings.Cut(a, "=")
		if seen[name] {
			return errExecVector
		}
		seen[name] = true
		switch {
		case !hasValue && (name == "--reverse" || name == "--follow"):
		case hasValue && values[name] != nil && values[name](value):
		default:
			return errExecVector
		}
	}
	return nil
}

// journalLiteralPattern reports whether p is \Q<s>\E with every \E inside
// s written as \E\\E\Q, so PCRE2 reads all of p as literal text.
func journalLiteralPattern(p string) bool {
	inner, ok := strings.CutPrefix(p, `\Q`)
	if !ok {
		return false
	}
	if inner, ok = strings.CutSuffix(inner, `\E`); !ok {
		return false
	}
	return !strings.Contains(strings.ReplaceAll(inner, `\E\\E\Q`, ""), `\E`)
}
