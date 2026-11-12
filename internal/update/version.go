// Package update is the agent's self-updater (ADR 0007): it chooses releases
// from the release index, verifies and stages them, swaps the installed
// binary, health-checks the restarted services and rolls back, keeping one
// transaction record per step.
package update

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// versionPattern is the semantic version pattern of the update action inputs:
// no "v" prefix and no build metadata.
var versionPattern = regexp.MustCompile(
	`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

// Version is a parsed semantic version.
type Version struct {
	Major, Minor, Patch uint64
	Pre                 string // prerelease without the leading "-"; empty for a release
}

// ParseVersion parses s, which must match the update actions' semantic
// version pattern.
func ParseVersion(s string) (Version, error) {
	m := versionPattern.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("update: %q is not a semantic version", s)
	}
	var v Version
	for i, dst := range []*uint64{&v.Major, &v.Minor, &v.Patch} {
		n, err := strconv.ParseUint(m[i+1], 10, 64)
		if err != nil {
			return Version{}, fmt.Errorf("update: %q is not a semantic version: %w", s, err)
		}
		*dst = n
	}
	v.Pre = m[4]
	return v, nil
}

func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Compare orders v and w by semantic version precedence: -1 when v is lower,
// 0 when they are equal, 1 when v is higher.
func (v Version) Compare(w Version) int {
	if c := cmp.Compare(v.Major, w.Major); c != 0 {
		return c
	}
	if c := cmp.Compare(v.Minor, w.Minor); c != 0 {
		return c
	}
	if c := cmp.Compare(v.Patch, w.Patch); c != 0 {
		return c
	}
	switch {
	case v.Pre == w.Pre:
		return 0
	case v.Pre == "":
		return 1
	case w.Pre == "":
		return -1
	}
	a, b := strings.Split(v.Pre, "."), strings.Split(w.Pre, ".")
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := comparePrerelease(a[i], b[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a), len(b))
}

// comparePrerelease orders two prerelease identifiers: numeric ones by value,
// below every alphanumeric one, which compare as ASCII. Numbers are compared
// as digit strings, so an identifier too large for uint64 still orders.
func comparePrerelease(a, b string) int {
	an, bn := isDigits(a), isDigits(b)
	switch {
	case an && bn:
		a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
		if c := cmp.Compare(len(a), len(b)); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	case an:
		return -1
	case bn:
		return 1
	}
	return strings.Compare(a, b)
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// Release is one entry of the release index: a published version and the
// channel its signature was made for.
type Release struct {
	Version string `json:"version"`
	Channel string `json:"channel"`
}

// InChannel returns the releases a host on channel may take, by ascending
// precedence: stable keeps the stable entries, beta keeps the stable and beta
// entries, and any other channel keeps none. Entries whose version does not
// parse are dropped.
func InChannel(releases []Release, channel string) []Release {
	type entry struct {
		r Release
		v Version
	}
	var kept []entry
	for _, r := range releases {
		switch {
		case r.Channel == core.ChannelStable && (channel == core.ChannelStable || channel == core.ChannelBeta):
		case r.Channel == core.ChannelBeta && channel == core.ChannelBeta:
		default:
			continue
		}
		v, err := ParseVersion(r.Version)
		if err != nil {
			continue
		}
		kept = append(kept, entry{r, v})
	}
	slices.SortStableFunc(kept, func(a, b entry) int { return a.v.Compare(b.v) })
	out := make([]Release, len(kept))
	for i, e := range kept {
		out[i] = e.r
	}
	return out
}

// Steps returns the releases an update from current to target passes through,
// target last, chosen from InChannel(releases, channel). An update never
// crosses more than one minor version at a time (ADR 0007): it steps through
// the newest release of every minor in between. A downgrade needs force and
// goes back at most one minor, in one step. Updates between major versions
// are refused, because the agent support window and the schema
// compatibility rules hold only within a major.
func Steps(current, target, channel string, releases []Release, force bool) ([]Release, error) {
	if channel != core.ChannelStable && channel != core.ChannelBeta {
		return nil, errcode.New(errcode.InvalidInput, fmt.Sprintf("unknown release channel %q", channel))
	}
	cur, err := ParseVersion(current)
	if err != nil {
		return nil, errcode.New(errcode.InvalidInput,
			fmt.Sprintf("this binary's version %s is not a release version, so it cannot be updated", current))
	}
	want, err := ParseVersion(target)
	if err != nil {
		return nil, errcode.New(errcode.InvalidInput, fmt.Sprintf("%q is not a semantic version", target))
	}
	avail := InChannel(releases, channel)
	idx := slices.IndexFunc(avail, func(r Release) bool {
		v, _ := ParseVersion(r.Version)
		return v.Compare(want) == 0
	})
	if idx < 0 {
		return nil, errcode.New(errcode.UpdateNotFound,
			fmt.Sprintf("release %s is not published in the %s channel", target, channel))
	}
	entry := avail[idx]
	if want.Major != cur.Major {
		return nil, errcode.New(errcode.InvalidInput,
			fmt.Sprintf("updates from %s to %s cross a major version, which is not supported", current, target))
	}
	switch c := want.Compare(cur); {
	case c == 0:
		return nil, errcode.New(errcode.InvalidInput, fmt.Sprintf("version %s is already running", current))
	case c < 0 && !force:
		return nil, errcode.New(errcode.UpdateDowngradeRefused,
			fmt.Sprintf("%s is older than the running %s; a downgrade needs force", target, current))
	case c < 0 && cur.Minor-want.Minor > 1:
		return nil, errcode.New(errcode.UpdateDowngradeRefused,
			fmt.Sprintf("a forced downgrade goes back at most one minor version, and %s is further behind %s", target, current))
	case c < 0:
		return []Release{entry}, nil
	}
	var steps []Release
	for minor := cur.Minor + 1; minor < want.Minor; minor++ {
		var newest *Release
		for i := range avail {
			v, _ := ParseVersion(avail[i].Version)
			if v.Major == cur.Major && v.Minor == minor {
				newest = &avail[i]
			}
		}
		if newest == nil {
			return nil, errcode.New(errcode.UpdateNotFound,
				fmt.Sprintf("no release of %d.%d is published in the %s channel, so %s cannot be reached one minor at a time",
					cur.Major, minor, channel, target))
		}
		steps = append(steps, *newest)
	}
	return append(steps, entry), nil
}
