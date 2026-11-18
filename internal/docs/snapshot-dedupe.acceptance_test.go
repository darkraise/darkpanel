package docs_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestA12_MakeReleasePrepCopiesThenChecksThenWritesTheChangelog(t *testing.T) {
	makeBin, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is not installed")
	}
	cmd := exec.Command(makeBin, "-n", "release-prep", "VERSION=3.4.5-rc.2") //nolint:gosec // G204: make from PATH, a dry run of the repository's Makefile
	cmd.Dir = dcRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n release-prep: %v\n%s", err, out)
	}
	plan := string(out)
	copyAt := strings.Index(plan, "snapshots.sh copy v3.4.5-rc.2")
	checkAt := strings.Index(plan, "snapshots.sh check v3.4.5-rc.2")
	cliffAt := strings.Index(plan, "git-cliff --tag v3.4.5-rc.2")
	if copyAt < 0 || checkAt < 0 || cliffAt < 0 {
		t.Fatalf("release-prep lacks one of copy (%d), check (%d) or git-cliff (%d):\n%s", copyAt, checkAt, cliffAt, plan)
	}
	if copyAt >= checkAt || checkAt >= cliffAt {
		t.Errorf("release-prep order is copy at %d, check at %d, git-cliff at %d; want copy < check < git-cliff:\n%s", copyAt, checkAt, cliffAt, plan)
	}
}
