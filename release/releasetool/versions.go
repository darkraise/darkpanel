package main

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// semverPattern is the semver.org 2.0.0 grammar without build metadata,
// which has no place in a tag or a release file name.
var semverPattern = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?$`)

type version struct {
	text       string
	core       [3]uint64
	prerelease []string
}

func parseVersion(s string) (version, error) {
	m := semverPattern.FindStringSubmatch(s)
	if m == nil {
		return version{}, fmt.Errorf("%q is not a semantic version x.y.z or x.y.z-prerelease", s)
	}
	v := version{text: s}
	for i := range 3 {
		n, err := strconv.ParseUint(m[i+1], 10, 64)
		if err != nil {
			return version{}, fmt.Errorf("%q: %w", s, err)
		}
		v.core[i] = n
	}
	if m[4] != "" {
		v.prerelease = strings.Split(m[4], ".")
	}
	return v, nil
}

// parseTag accepts a release tag, v<semantic version>.
func parseTag(tag string) (version, error) {
	s, ok := strings.CutPrefix(tag, "v")
	if !ok {
		return version{}, fmt.Errorf("tag %q does not start with v", tag)
	}
	return parseVersion(s)
}

func (v version) channel() string {
	if len(v.prerelease) > 0 {
		return "beta"
	}
	return "stable"
}

// compare orders versions by semantic-version precedence (semver.org
// section 11): a prerelease sorts before its release.
func compare(a, b version) int {
	for i := range 3 {
		if c := cmp.Compare(a.core[i], b.core[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a.prerelease) == 0 && len(b.prerelease) == 0:
		return 0
	case len(a.prerelease) == 0:
		return 1
	case len(b.prerelease) == 0:
		return -1
	}
	for i := 0; i < len(a.prerelease) && i < len(b.prerelease); i++ {
		if c := compareIdentifier(a.prerelease[i], b.prerelease[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a.prerelease), len(b.prerelease))
}

// compareIdentifier compares numeric identifiers numerically and sorts them
// before alphanumeric ones, which compare in ASCII order.
func compareIdentifier(a, b string) int {
	an, aErr := strconv.ParseUint(a, 10, 64)
	bn, bErr := strconv.ParseUint(b, 10, 64)
	switch {
	case aErr == nil && bErr == nil:
		return cmp.Compare(an, bn)
	case aErr == nil:
		return -1
	case bErr == nil:
		return 1
	}
	return strings.Compare(a, b)
}

// ghRelease is one entry of `gh release list --json tagName,isDraft`.
type ghRelease struct {
	TagName string `json:"tagName"`
	IsDraft bool   `json:"isDraft"`
}

// indexEntry is one release object of releases.json. Readers, install.sh
// included, find its members by name, not by position (ADR 0012).
type indexEntry struct {
	Version string `json:"version"`
	Channel string `json:"channel"`
}

// releaseIndex is releases.json. It only selects versions; the updater and
// install.sh take trust from each version's own signatures.
type releaseIndex struct {
	FormatVersion int          `json:"formatVersion"`
	Releases      []indexEntry `json:"releases"`
}

// buildIndex lists every published v<semver> release, newest first. Drafts
// and other tags, such as the rolling index release, are skipped.
func buildIndex(list []ghRelease) releaseIndex {
	var versions []version
	for _, r := range list {
		if r.IsDraft {
			continue
		}
		v, err := parseTag(r.TagName)
		if err != nil {
			continue
		}
		versions = append(versions, v)
	}
	slices.SortFunc(versions, func(a, b version) int { return compare(b, a) })
	idx := releaseIndex{FormatVersion: 1, Releases: []indexEntry{}}
	for _, v := range versions {
		idx.Releases = append(idx.Releases, indexEntry{Version: v.text, Channel: v.channel()})
	}
	return idx
}

// isLatest reports whether the release of v becomes the repository's Latest
// release on GitHub: a stable version that no published stable release
// outranks. GitHub marks each new publication Latest unless told otherwise,
// so a patch on an older line would otherwise displace the newest line.
func isLatest(list []ghRelease, v version) bool {
	if v.channel() != "stable" {
		return false
	}
	for _, r := range list {
		if r.IsDraft {
			continue
		}
		p, err := parseTag(r.TagName)
		if err != nil || p.channel() != "stable" {
			continue
		}
		if compare(p, v) >= 0 {
			return false
		}
	}
	return true
}
