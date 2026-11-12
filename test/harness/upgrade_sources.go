//go:build harness

package harness

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/test/harness/release"
)

const releaseChannel = "stable"

const (
	// upgBaseline is the version of the first source, which every later
	// release must still update from.
	upgBaseline = "1.0.0"
	// upgUnderTest is the version the build under test is stamped with, the
	// target of every source's update.
	upgUnderTest = "1.1.0"
	// upgBroken is the build under test's twin that fails its health check,
	// so that its rollout rolls back.
	upgBroken = "1.1.1"
)

// upgCommitPattern is the form of a source's commit: a full commit id, so
// that the build job checks out exactly the commit the file names.
var upgCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// upgSource is one line of the sources file: a released version and the
// commit it is built from.
type upgSource struct {
	Version, Commit string
}

// upgParseSources parses the text of the sources file and returns its sources
// in file order. It refuses a source the updater cannot step from to the build
// under test, a malformed commit, and versions that are not unique and
// ascending, and it requires the baseline as the first source.
func upgParseSources(text string) ([]upgSource, error) {
	under, err := update.ParseVersion(upgUnderTest)
	if err != nil {
		return nil, fmt.Errorf("harness: upgrade sources: %w", err)
	}
	var sources []upgSource
	var prev update.Version
	seen := map[string]bool{}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fail := func(format string, args ...any) error {
			return fmt.Errorf("harness: upgrade sources: line %d: "+format, append([]any{i + 1}, args...)...)
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fail("want <version> <commit>, got %q", line)
		}
		version, commit := fields[0], fields[1]
		v, err := update.ParseVersion(version)
		if err != nil {
			return nil, fail("%w", err)
		}
		if v.Major != under.Major {
			return nil, fail("version %s is not of major %d, and the updater refuses a step across majors", version, under.Major)
		}
		if v.Compare(under) >= 0 {
			return nil, fail("version %s is not older than the build under test, %s", version, upgUnderTest)
		}
		if !upgCommitPattern.MatchString(commit) {
			return nil, fail("commit %q is not 40 lower-case hexadecimal characters", commit)
		}
		if seen[version] {
			return nil, fail("version %s is listed twice", version)
		}
		if len(sources) > 0 && v.Compare(prev) <= 0 {
			return nil, fail("version %s is not above %s, which the line before names", version, prev)
		}
		seen[version] = true
		prev = v
		sources = append(sources, upgSource{Version: version, Commit: commit})
	}
	if len(sources) == 0 {
		return nil, errors.New("harness: upgrade sources: the file lists no source")
	}
	if sources[0].Version != upgBaseline {
		return nil, fmt.Errorf("harness: upgrade sources: the first source is %s, want the baseline %s", sources[0].Version, upgBaseline)
	}
	return sources, nil
}

// upgLoadSources reads and parses the sources file at path.
func upgLoadSources(path string) ([]upgSource, error) {
	b, err := os.ReadFile(path) //nolint:gosec // G304: the harness's own sources file
	if err != nil {
		return nil, fmt.Errorf("harness: %w", err)
	}
	return upgParseSources(string(b))
}

// upgBuildNames are the files under the builds directory that the matrix
// needs for src: the source's build, the build under test and its broken twin.
func upgBuildNames(src upgSource) []string {
	return []string{"darkpanel-" + src.Version, "darkpanel-" + upgUnderTest, "darkpanel-" + upgBroken}
}

// upgCheckBuilds reports the first build of sources missing from dir, so that
// a run without the build job's outputs fails before it boots a VM.
func upgCheckBuilds(dir string, sources []upgSource) error {
	for _, src := range sources {
		for _, name := range upgBuildNames(src) {
			if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
				return fmt.Errorf("harness: the upgrade matrix needs %s in %s; build it as the build job does: %w", name, dir, err)
			}
		}
	}
	return nil
}

// upgReleases are the releases the scanner serves for src, ascending: the
// source, the build under test and its broken twin, built into dir.
func upgReleases(dir string, src upgSource) []release.Version {
	var versions []release.Version
	for _, version := range []string{src.Version, upgUnderTest, upgBroken} {
		versions = append(versions, release.Version{
			Version: version,
			Channel: releaseChannel,
			Binary:  filepath.Join(dir, "darkpanel-"+version),
		})
	}
	return versions
}
