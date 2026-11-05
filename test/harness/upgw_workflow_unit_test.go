//go:build harness

package harness

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	upgwYmlSourcesStep = "Build the upgrade sources"
	upgwYmlTargetsStep = "Build the upgrade targets"
	upgwYmlRunStep     = "Run the upgrade matrix"
	upgwYmlJob         = "vm-upg"
	// upgwYmlStepTimeout is the -timeout in minutes of the TestUpgradeMatrix
	// step and upgwYmlJobLimit the job's timeout-minutes.
	upgwYmlStepTimeout = 70
	upgwYmlJobLimit    = 90
)

// upgwYmlCheck checks the text of harness.yml for TestUpgradeMatrix: the build
// job checks out the full history, builds every source of the sources file
// from its commit in a worktree, and builds the build under test and its
// broken twin, all before it uploads bin/harness; the vm-upg job has the vm
// jobs' setup, a timeout-minutes of upgwYmlJobLimit, and a step that runs the
// test with the lab flags, a work directory of its own, a -timeout of
// upgwYmlStepTimeout minutes and its failure logs; and nothing outside the job
// names the test, its flag or its work directory.
func upgwYmlCheck(yml string) error {
	build, ok := m3dxExpoJob(yml, "build")
	if !ok {
		return fmt.Errorf("harness: the workflow has no build job")
	}
	if !strings.Contains(build, "fetch-depth: 0") {
		return fmt.Errorf("harness: the build job does not check out with fetch-depth: 0, so the source commits are missing")
	}
	for _, s := range []struct {
		name  string
		wants []string
	}{
		{upgwYmlSourcesStep, []string{
			"test/harness/upgrade_sources.txt", "git worktree add --detach", `make -C "$worktree" harness-builds`,
			`HARNESS_BUILDS="$version:harnesskeys"`, "HARNESS_DIR=", `"bin/harness/darkpanel-$version"`, "git worktree remove",
		}},
		{upgwYmlTargetsStep, []string{
			fmt.Sprintf(`make harness-builds HARNESS_BUILDS="%s:harnesskeys %s:harnesskeys,harnessbroken"`, upgUnderTest, upgBroken),
		}},
	} {
		step, ok := m4lStep(build, s.name)
		if !ok {
			return fmt.Errorf("harness: the build job has no step named %q", s.name)
		}
		for _, want := range s.wants {
			if !strings.Contains(step, want) {
				return fmt.Errorf("harness: the step %q lacks %s", s.name, want)
			}
		}
		if !m4lStepBefore(build, step, "name: darkpanel-harness") {
			return fmt.Errorf("harness: the build job does not run the step %q before it uploads the darkpanel-harness artifact", s.name)
		}
	}

	job, ok := m3dxExpoJob(yml, upgwYmlJob)
	if !ok {
		return fmt.Errorf("harness: the workflow has no %s job", upgwYmlJob)
	}
	for _, text := range []string{"TestUpgradeMatrix", "harness-upg", " -upg"} {
		if strings.Contains(strings.Replace(yml, job, "", 1), text) {
			return fmt.Errorf("harness: the workflow holds %s outside the %s job", text, upgwYmlJob)
		}
	}
	musts := []string{"needs: build", "runs-on: ubuntu-24.04", "fail-fast: false", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]"}
	for _, want := range append(musts, m4lSetup...) {
		if !strings.Contains(job, want) {
			return fmt.Errorf("harness: the %s job lacks %s", upgwYmlJob, want)
		}
	}
	limit := m3dxExpoLimitRE.FindStringSubmatch(job)
	if limit == nil {
		return fmt.Errorf("harness: the %s job has no timeout-minutes", upgwYmlJob)
	}
	if have, _ := strconv.Atoi(limit[1]); have != upgwYmlJobLimit || have > m4lJobMax {
		return fmt.Errorf("harness: the %s job has timeout-minutes %d, want %d", upgwYmlJob, have, upgwYmlJobLimit)
	}

	run, ok := m4lStep(job, upgwYmlRunStep)
	if !ok {
		return fmt.Errorf("harness: the %s job has no step named %q", upgwYmlJob, upgwYmlRunStep)
	}
	for _, want := range []string{
		"-run '^TestUpgradeMatrix$'", " -upg", "-os ${{ matrix.os }}",
		`-bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"`,
		`-m2 "$GITHUB_WORKSPACE/bin/harness"`,
		`-commit "$GITHUB_SHA"`,
		`-workdir "$RUNNER_TEMP/harness-upg"`,
		`-cache "$HOME/.cache/darkpanel-harness"`,
	} {
		if !strings.Contains(run, want) {
			return fmt.Errorf("harness: the step %q lacks %s", upgwYmlRunStep, want)
		}
	}
	m := m3dxExpoTimeoutRE.FindStringSubmatch(run)
	if m == nil {
		return fmt.Errorf("harness: the step %q has no -timeout <n>m", upgwYmlRunStep)
	}
	n, _ := strconv.Atoi(m[1])
	if n != upgwYmlStepTimeout {
		return fmt.Errorf("harness: the step %q has -timeout %sm, want %dm", upgwYmlRunStep, m[1], upgwYmlStepTimeout)
	}
	if time.Duration(n)*time.Minute < upgDeadline+10*time.Minute {
		return fmt.Errorf("harness: the step %q has -timeout %sm, under the deadline %s plus 10 minutes", upgwYmlRunStep, m[1], upgDeadline)
	}
	for _, want := range []string{"name: logs-upg-${{ matrix.os }}", "harness-upg/**/*.log"} {
		if !strings.Contains(job, want) {
			return fmt.Errorf("harness: the %s job's failure upload lacks %s", upgwYmlJob, want)
		}
	}
	return nil
}

// upgwYmlDoc has the parts of harness.yml that upgwYmlCheck reads.
const upgwYmlDoc = `jobs:
  build:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@x
        with:
          persist-credentials: false
          fetch-depth: 0
      - run: make harness-builds
      - name: Build the upgrade sources
        run: |
          while read -r version commit <&3; do
            git worktree add --detach "$worktree" "$commit"
            make -C "$worktree" harness-builds HARNESS_BUILDS="$version:harnesskeys" HARNESS_DIR="$scratch/out-$version"
            cp "$scratch/out-$version/darkpanel-$version" "bin/harness/darkpanel-$version"
            git worktree remove --force "$worktree"
          done 3< test/harness/upgrade_sources.txt
      - name: Build the upgrade targets
        run: make harness-builds HARNESS_BUILDS="1.1.0:harnesskeys 1.1.1:harnesskeys,harnessbroken"
      - uses: actions/upload-artifact@x
        with:
          name: darkpanel-harness
          path: bin/harness
  vm-m7r:
    needs: build
    timeout-minutes: 70
    steps:
      - name: Run the M7 restore harness
        run: >-
          go test -tags harness -timeout 50m -v -run '^TestM7Restore$' ./test/harness/
          -args -os ${{ matrix.os }} -m7r
  vm-upg:
    needs: build
    runs-on: ubuntu-24.04
    timeout-minutes: 90
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]
    steps:
      - name: Enable KVM
        run: test -e /dev/kvm
      - run: sudo apt-get install -y qemu-system-x86
      - uses: actions/checkout@x
      - uses: actions/setup-go@x
      - uses: actions/download-artifact@x
        with:
          name: darkpanel-linux-amd64
      - uses: actions/download-artifact@x
        with:
          name: darkpanel-harness
      - uses: actions/cache@x
      - run: go vet -tags harness ./test/...
      - name: Run the upgrade matrix
        run: >-
          go test -tags harness -timeout 70m -v -run '^TestUpgradeMatrix$' ./test/harness/
          -args -os ${{ matrix.os }} -upg
          -bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"
          -m2 "$GITHUB_WORKSPACE/bin/harness"
          -commit "$GITHUB_SHA"
          -workdir "$RUNNER_TEMP/harness-upg"
          -cache "$HOME/.cache/darkpanel-harness"
      - if: failure()
        uses: actions/upload-artifact@x
        with:
          name: logs-upg-${{ matrix.os }}
          path: ${{ runner.temp }}/harness-upg/**/*.log
`

func TestUpgwUnitWorkflow(t *testing.T) {
	if err := upgwYmlCheck(upgwYmlDoc); err != nil {
		t.Fatalf("the fixture: %v", err)
	}
	for _, tc := range []struct{ name, from, to, want string }{
		{"no build job", "  build:\n", "  other:\n", "no build job"},
		{"shallow checkout", "          fetch-depth: 0\n", "", "fetch-depth: 0"},
		{"no sources step", "name: Build the upgrade sources", "name: Build sources", `no step named "Build the upgrade sources"`},
		{"sources not read from the file", "done 3< test/harness/upgrade_sources.txt", "done 3< sources.txt", "test/harness/upgrade_sources.txt"},
		{"no worktree", "git worktree add --detach", "git checkout", "git worktree add --detach"},
		{"built from the checkout", `make -C "$worktree" harness-builds`, "make harness-builds", `make -C "$worktree" harness-builds`},
		{"source built without the harness key", `HARNESS_BUILDS="$version:harnesskeys"`, `HARNESS_BUILDS="$version:"`, `HARNESS_BUILDS="$version:harnesskeys"`},
		{"source built into the checkout", `HARNESS_DIR="$scratch/out-$version"`, "", "HARNESS_DIR="},
		{"source not copied", `"bin/harness/darkpanel-$version"`, `"bin/other"`, `"bin/harness/darkpanel-$version"`},
		{"worktree kept", "git worktree remove --force", "true", "git worktree remove"},
		{"no targets step", "name: Build the upgrade targets", "name: Build targets", `no step named "Build the upgrade targets"`},
		{"wrong label", `"1.1.0:harnesskeys 1.1.1`, `"1.2.0:harnesskeys 1.1.1`, `HARNESS_BUILDS="1.1.0:harnesskeys 1.1.1:harnesskeys,harnessbroken"`},
		{"twin not broken", "1.1.1:harnesskeys,harnessbroken", "1.1.1:harnesskeys", "harnessbroken"},
		{"steps after the upload", "      - run: make harness-builds\n", "      - uses: actions/upload-artifact@x\n        with:\n          name: darkpanel-harness\n      - run: make harness-builds\n", "before it uploads"},
		{"no vm-upg job", "  vm-upg:\n", "  vm-other:\n", "no vm-upg job"},
		{"limit under 90", "runs-on: ubuntu-24.04\n    timeout-minutes: 90", "runs-on: ubuntu-24.04\n    timeout-minutes: 80", "timeout-minutes 80, want 90"},
		{"limit past the 120 minutes", "runs-on: ubuntu-24.04\n    timeout-minutes: 90", "runs-on: ubuntu-24.04\n    timeout-minutes: 130", "timeout-minutes 130, want 90"},
		{"no needs", "  vm-upg:\n    needs: build\n", "  vm-upg:\n    needs: other\n", "needs: build"},
		{"os matrix without debian-13", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]", "os: [ubuntu-22.04, ubuntu-24.04, debian-12]", "debian-13"},
		{"no KVM", "test -e /dev/kvm", "test -e /dev/none", "/dev/kvm"},
		{"no matrix step", "Run the upgrade matrix", "Run upgrades", `no step named "Run the upgrade matrix"`},
		{"wrong test", "'^TestUpgradeMatrix$'", "'^TestUpgradeOther$'", "TestUpgradeMatrix"},
		{"no flag", " -upg\n", "\n", " -upg"},
		{"no fleet builds", "          -m2 \"$GITHUB_WORKSPACE/bin/harness\"\n          -commit \"$GITHUB_SHA\"\n          -workdir \"$RUNNER_TEMP/harness-upg\"", "          -commit \"$GITHUB_SHA\"\n          -workdir \"$RUNNER_TEMP/harness-upg\"", "-m2"},
		{"no commit", "          -commit \"$GITHUB_SHA\"\n", "", "-commit"},
		{"no image cache", "          -cache \"$HOME/.cache/darkpanel-harness\"\n", "", "-cache"},
		{"no workdir", "          -workdir \"$RUNNER_TEMP/harness-upg\"\n", "", "-workdir"},
		{"workdir of vm-m7r", "-workdir \"$RUNNER_TEMP/harness-upg\"", "-workdir \"$RUNNER_TEMP/harness-m7r\"", "harness-upg"},
		{"short timeout", "-timeout 70m -v -run '^TestUpgradeMatrix$'", "-timeout 60m -v -run '^TestUpgradeMatrix$'", "-timeout 60m"},
		{"long timeout", "-timeout 70m -v -run '^TestUpgradeMatrix$'", "-timeout 80m -v -run '^TestUpgradeMatrix$'", "-timeout 80m"},
		{"no failure logs", "          path: ${{ runner.temp }}/harness-upg/**/*.log\n", "", "harness-upg/**/*.log"},
		{"artifact name of vm-m7r", "name: logs-upg-${{ matrix.os }}", "name: logs-m7r-${{ matrix.os }}", "logs-upg-"},
		{"test added to vm-m7r", "-run '^TestM7Restore$'", "-run '^TestM7Restore$|^TestUpgradeMatrix$'", "TestUpgradeMatrix outside the vm-upg job"},
		{"flag added to vm-m7r", "-args -os ${{ matrix.os }} -m7r", "-args -os ${{ matrix.os }} -m7r -upg", " -upg outside the vm-upg job"},
	} {
		if n := strings.Count(upgwYmlDoc, tc.from); n != 1 {
			t.Fatalf("%s: the fixture holds %q %d times, want once", tc.name, tc.from, n)
		}
		doc := strings.Replace(upgwYmlDoc, tc.from, tc.to, 1)
		if err := upgwYmlCheck(doc); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.want)
		}
	}
}

// TestUpgwUnitWorkflowFile checks the committed workflow, which is the only
// place TestUpgradeMatrix runs.
func TestUpgwUnitWorkflowFile(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/harness.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := upgwYmlCheck(string(b)); err != nil {
		t.Error(err)
	}
}
