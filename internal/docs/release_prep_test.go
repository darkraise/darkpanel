package docs_test

import (
	"os/exec"
	"strings"
	"testing"
)

// release-prep must fail on a snapshot check before it rewrites the changelog,
// so neither the hand path nor the auto-release prepare job reaches a tag.
func TestReleasePrepChecksSnapshotsBeforeTheChangelog(t *testing.T) {
	makeBin, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is not installed")
	}
	cmd := exec.Command(makeBin, "-n", "release-prep", "VERSION=7.0.0-rc.3") //nolint:gosec // G204: make from PATH, a dry run of the repository's Makefile
	cmd.Dir = dcRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n release-prep: %v\n%s", err, out)
	}
	plan := string(out)
	copyAt := strings.Index(plan, "release/snapshots.sh copy v7.0.0-rc.3")
	checkAt := strings.Index(plan, "release/snapshots.sh check v7.0.0-rc.3")
	cliffAt := strings.Index(plan, "--tag v7.0.0-rc.3 -o CHANGELOG.md")
	if copyAt < 0 || checkAt < 0 || cliffAt < 0 {
		t.Fatalf("release-prep does not copy, check and rewrite the changelog:\n%s", plan)
	}
	if copyAt > checkAt || checkAt > cliffAt {
		t.Errorf("release-prep must copy, then check the snapshots, then rewrite the changelog:\n%s", plan)
	}
}
