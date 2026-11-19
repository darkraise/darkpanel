package system

import (
	"regexp"
	"strings"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// aptCauses maps apt's and dpkg's messages to codes, first match wins: a
// held lock is checked first because a busy apt prints nothing else.
var aptCauses = []struct {
	code    errcode.Code
	phrases []string
}{
	{errcode.SystemAptBusy, []string{"Could not get lock", "Unable to acquire the dpkg frontend lock", "Unable to lock the administration directory"}},
	{errcode.SystemDpkgInterrupted, []string{"dpkg was interrupted"}},
	{errcode.SystemDiskFull, []string{"You don't have enough free space", "No space left on device"}},
	{errcode.SystemRepoSignature, []string{
		"is not signed", "GPG error", "signatures couldn't be verified", "signatures were invalid", "NO_PUBKEY", "EXPKEYSIG",
	}},
	{errcode.SystemBrokenPackages, []string{"held broken packages", "unmet dependencies", "Unmet dependencies", "Unable to correct problems"}},
}

// classifyApt returns the code of a failed apt or dpkg run from its output,
// system.apt_failed when no known cause appears.
func classifyApt(out []byte) errcode.Code {
	s := string(out)
	for _, c := range aptCauses {
		for _, p := range c.phrases {
			if strings.Contains(s, p) {
				return c.code
			}
		}
	}
	return errcode.SystemAptFailed
}

// aptFailedPackage matches the lines that name the package an error is about.
var aptFailedPackage = []*regexp.Regexp{
	regexp.MustCompile(`^dpkg: error processing package (\S+) `),
	regexp.MustCompile(`^E: Unable to locate package (\S+)$`),
	regexp.MustCompile(`^E: Package '([^']+)' has no installation candidate`),
}

// failureDetail returns the package a failed run names last, "" when none,
// and the last 40 lines of its output. dpkg lists the failed packages
// indented under "Errors were encountered while processing:".
func failureDetail(out []byte) (pkg string, tail []string) {
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	under := false
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "Errors were encountered while processing:"):
			under = true
			continue
		case under && strings.HasPrefix(l, " "):
			pkg = strings.TrimSpace(l)
			continue
		}
		under = false
		for _, re := range aptFailedPackage {
			if m := re.FindStringSubmatch(l); m != nil {
				pkg = m[1]
			}
		}
	}
	return pkg, lines[max(0, len(lines)-40):]
}
