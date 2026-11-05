//go:build harness

package harness

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	fpYmlBuildStep = "Build the footprint agent"
	fpYmlUnitStep  = "Harness unit tests"
	fpYmlRunStep   = "Run the footprint harness"
	fpYmlJob       = "vm-fp"
	// fpYmlStepTimeout is the -timeout in minutes of the TestFootprint step and fpYmlJobLimit the job's
	// timeout-minutes.
	fpYmlStepTimeout = 60
	fpYmlJobLimit    = 80
)

var (
	// fpYmlUploadRE finds the line of the step that uploads the bin/harness directory, and not the path of a cache
	// step that names files inside it.
	fpYmlUploadRE = regexp.MustCompile(`(?m)^\s*path: bin/harness$`)
	// fpYmlJobRE finds the key of a job: two spaces, a name and a colon.
	fpYmlJobRE = regexp.MustCompile(`(?m)^  ([a-z0-9][a-z0-9-]*):\s*$`)
)

// fpYmlWorkflow checks the text of harness.yml for TestFootprint: the build job builds the extra agent's binary with
// the harness tags and the version of the M2 install, before it uploads bin/harness, and its unit test step also
// runs the tests of cmd/darkpanel under the harness tag; the vm-fp job has the vm jobs' setup, a timeout-minutes
// of fpYmlJobLimit, and a step that runs the test with the lab flags, a work directory of its own, a -timeout of
// fpYmlStepTimeout minutes and its failure logs; and no other job holds the test.
func fpYmlWorkflow(yml string) error {
	build, ok := m3dxExpoJob(yml, "build")
	if !ok {
		return errors.New("harness: the workflow has no build job")
	}
	step, ok := m4lStep(build, fpYmlBuildStep)
	if !ok {
		return fmt.Errorf("harness: the build job has no step named %q", fpYmlBuildStep)
	}
	for _, want := range []string{
		"CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64", "go build", "-tags harnesskeys,harness",
		"-X github.com/darkraise/darkpanel/internal/buildinfo.Version=" + versionN,
		"-o bin/harness/darkpanel-" + versionN + "-state", "./cmd/darkpanel",
	} {
		if !strings.Contains(step, want) {
			return fmt.Errorf("harness: the step %q lacks %s", fpYmlBuildStep, want)
		}
	}
	upload := fpYmlUploadRE.FindStringIndex(build)
	if at := strings.Index(build, "- name: "+fpYmlBuildStep); upload == nil || at < 0 || at > upload[0] {
		return fmt.Errorf("harness: the build job does not run the step %q before it uploads path: bin/harness", fpYmlBuildStep)
	}
	units, ok := m4lStep(build, fpYmlUnitStep)
	if !ok {
		return fmt.Errorf("harness: the build job has no step named %q", fpYmlUnitStep)
	}
	for _, want := range []string{"go test -tags harness ./test/harness/...", "go test -tags harness ./cmd/darkpanel/"} {
		if !strings.Contains(units, want) {
			return fmt.Errorf("harness: the step %q lacks %s", fpYmlUnitStep, want)
		}
	}

	job, ok := m3dxExpoJob(yml, fpYmlJob)
	if !ok {
		return fmt.Errorf("harness: the workflow has no %s job", fpYmlJob)
	}

	for _, name := range fpYmlJobs(yml) {
		if name == fpYmlJob || name == "build" {
			continue
		}
		other, _ := m3dxExpoJob(yml, name)
		for _, text := range []string{"TestFootprint", "harness-fp", " -fp"} {
			if strings.Contains(other, text) {
				return fmt.Errorf("harness: the %s job holds %s, which belongs to the %s job", name, text, fpYmlJob)
			}
		}
	}

	musts := []string{"needs: build", "runs-on: ubuntu-24.04", "fail-fast: false", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]"}
	for _, want := range append(musts, m4lSetup...) {
		if !strings.Contains(job, want) {
			return fmt.Errorf("harness: the %s job lacks %s", fpYmlJob, want)
		}
	}
	limit := m3dxExpoLimitRE.FindStringSubmatch(job)
	if limit == nil {
		return fmt.Errorf("harness: the %s job has no timeout-minutes", fpYmlJob)
	}
	if have, _ := strconv.Atoi(limit[1]); have != fpYmlJobLimit || have > m4lJobMax {
		return fmt.Errorf("harness: the %s job has timeout-minutes %d, want %d", fpYmlJob, have, fpYmlJobLimit)
	}

	run, ok := m4lStep(job, fpYmlRunStep)
	if !ok {
		return fmt.Errorf("harness: the %s job has no step named %q", fpYmlJob, fpYmlRunStep)
	}
	for _, want := range []string{
		"-run '^TestFootprint$'", " -fp", "-os ${{ matrix.os }}",
		`-bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"`,
		`-m2 "$GITHUB_WORKSPACE/bin/harness"`,
		`-commit "$GITHUB_SHA"`,
		`-workdir "$RUNNER_TEMP/harness-fp"`,
		`-cache "$HOME/.cache/darkpanel-harness"`,
	} {
		if !strings.Contains(run, want) {
			return fmt.Errorf("harness: the step %q lacks %s", fpYmlRunStep, want)
		}
	}
	m := m3dxExpoTimeoutRE.FindStringSubmatch(run)
	if m == nil {
		return fmt.Errorf("harness: the step %q has no -timeout <n>m", fpYmlRunStep)
	}
	n, _ := strconv.Atoi(m[1])
	if n != fpYmlStepTimeout {
		return fmt.Errorf("harness: the step %q has -timeout %sm, want %dm", fpYmlRunStep, m[1], fpYmlStepTimeout)
	}
	if time.Duration(n)*time.Minute < fpDeadline+10*time.Minute {
		return fmt.Errorf("harness: the step %q has -timeout %sm, under the scenario deadline %s plus 10 minutes", fpYmlRunStep, m[1], fpDeadline)
	}
	for _, want := range []string{"name: logs-fp-${{ matrix.os }}", "harness-fp/**/*.log"} {
		if !strings.Contains(job, want) {
			return fmt.Errorf("harness: the %s job's failure upload lacks %s", fpYmlJob, want)
		}
	}
	return nil
}

// fpYmlJobs returns the names of the jobs of the workflow text, in order.
func fpYmlJobs(yml string) []string {
	_, jobs, ok := strings.Cut(yml, "\njobs:\n")
	if !ok {
		return nil
	}
	var names []string
	for _, m := range fpYmlJobRE.FindAllStringSubmatch(jobs, -1) {
		names = append(names, m[1])
	}
	return names
}

const (
	fpYmlBuildSteps = `      - name: Build the footprint agent
        run: >-
          CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -tags harnesskeys,harness
          -ldflags "-X github.com/darkraise/darkpanel/internal/buildinfo.Version=0.2.0"
          -o bin/harness/darkpanel-0.2.0-state ./cmd/darkpanel
      - name: Harness unit tests
        run: |
          go test -tags harness ./test/harness/...
          go test -tags harness ./cmd/darkpanel/
`
	fpYmlUploadStep = `      - uses: actions/upload-artifact@x
        with:
          path: bin/harness
`
	// fpYmlWorkflowDoc has the parts of harness.yml that fpYmlWorkflow reads.
	fpYmlWorkflowDoc = `name: Harness
jobs:
  build:
    runs-on: ubuntu-24.04
    steps:
      - run: make harness-builds
      - name: Cache the derper build
        uses: actions/cache@x
        with:
          path: bin/harness/derper-*
` + fpYmlBuildSteps + fpYmlUploadStep + `  vm-m5d:
    needs: build
    runs-on: ubuntu-24.04
    timeout-minutes: 60
    steps:
      - name: Run the M5 DERP harness
        run: >-
          go test -tags harness -timeout 40m -v -run '^TestM5DERP$' ./test/harness/
          -args -os ${{ matrix.os }} -m5d
  vm-upg:
    needs: build
    runs-on: ubuntu-24.04
    timeout-minutes: 90
    steps:
      - name: Run the upgrade matrix
        run: >-
          go test -tags harness -timeout 70m -v -run '^TestUpgradeMatrix$' ./test/harness/
          -args -os ${{ matrix.os }} -upg
  vm-fp:
    needs: build
    runs-on: ubuntu-24.04
    timeout-minutes: 80
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
      - name: Run the footprint harness
        run: >-
          go test -tags harness -timeout 60m -v -run '^TestFootprint$' ./test/harness/
          -args -os ${{ matrix.os }} -fp
          -bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"
          -m2 "$GITHUB_WORKSPACE/bin/harness"
          -commit "$GITHUB_SHA"
          -workdir "$RUNNER_TEMP/harness-fp"
          -cache "$HOME/.cache/darkpanel-harness"
      - if: failure()
        uses: actions/upload-artifact@x
        with:
          name: logs-fp-${{ matrix.os }}
          path: ${{ runner.temp }}/harness-fp/**/*.log
`
)

func TestFpwUnitWorkflow(t *testing.T) {
	if err := fpYmlWorkflow(fpYmlWorkflowDoc); err != nil {
		t.Fatalf("the fixture: %v", err)
	}
	if got := fpYmlJobs(fpYmlWorkflowDoc); !strings.EqualFold(strings.Join(got, " "), "build vm-m5d vm-upg vm-fp") {
		t.Fatalf("fpYmlJobs = %v, want build vm-m5d vm-upg vm-fp", got)
	}
	for _, tc := range []struct{ name, from, to, want string }{
		{"no build job", "  build:\n", "  compile:\n", "no build job"},
		{"no agent build step", "Build the footprint agent", "Build something", `no step named "Build the footprint agent"`},
		{"both architectures", "GOARCH=amd64 go build", "go build", "GOARCH=amd64"},
		{"no harness tag", "-tags harnesskeys,harness\n", "-tags harnesskeys\n", "-tags harnesskeys,harness"},
		{"no keys tag", "-tags harnesskeys,harness\n", "-tags harness\n", "-tags harnesskeys,harness"},
		{"no version stamp", "buildinfo.Version=0.2.0", "buildinfo.Commit=abc", "buildinfo.Version=0.2.0"},
		{"version of another build", "buildinfo.Version=0.2.0", "buildinfo.Version=0.3.0", "buildinfo.Version=0.2.0"},
		{"binary name", "-o bin/harness/darkpanel-0.2.0-state", "-o bin/darkpanel-0.2.0-state", "bin/harness/darkpanel-0.2.0-state"},
		{"no unit test step", "Harness unit tests", "Unit tests", `no step named "Harness unit tests"`},
		{"no cmd test", "          go test -tags harness ./cmd/darkpanel/\n", "", "./cmd/darkpanel/"},
		{"cmd test without the tag", "go test -tags harness ./cmd/darkpanel/", "go test ./cmd/darkpanel/", "go test -tags harness ./cmd/darkpanel/"},
		{"no harness package tests", "go test -tags harness ./test/harness/...", "go test ./test/harness/...", "./test/harness/..."},
		{"no vm-fp job", "  vm-fp:\n", "  vm-other:\n", "no vm-fp job"},
		{"limit of vm-m5d", "timeout-minutes: 80", "timeout-minutes: 60", "timeout-minutes 60, want 80"},
		{"limit past 120", "timeout-minutes: 80", "timeout-minutes: 130", "timeout-minutes 130, want 80"},
		{"no needs", "    needs: build\n    runs-on: ubuntu-24.04\n    timeout-minutes: 80", "    runs-on: ubuntu-24.04\n    timeout-minutes: 80", "needs: build"},
		{"os matrix without debian-13", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]", "os: [ubuntu-22.04, ubuntu-24.04, debian-12]", "debian-13"},
		{"no KVM", "/dev/kvm", "/dev/none", "/dev/kvm"},
		{"no run step", "Run the footprint harness", "Run footprint", `no step named "Run the footprint harness"`},
		{"wrong test", "'^TestFootprint$'", "'^TestOther$'", "TestFootprint"},
		{"no flag", " -fp\n", "\n", " -fp"},
		{"no fleet builds", "          -m2 \"$GITHUB_WORKSPACE/bin/harness\"\n", "", "-m2"},
		{"no commit", "          -commit \"$GITHUB_SHA\"\n", "", "-commit"},
		{"no image cache", "          -cache \"$HOME/.cache/darkpanel-harness\"\n", "", "-cache"},
		{"no workdir", "          -workdir \"$RUNNER_TEMP/harness-fp\"\n", "", "-workdir"},
		{"workdir of another job", "-workdir \"$RUNNER_TEMP/harness-fp\"", "-workdir \"$RUNNER_TEMP/harness-m5d\"", "harness-fp"},
		{"short timeout", "-timeout 60m -v -run", "-timeout 50m -v -run", "-timeout 50m"},
		{"long timeout", "-timeout 60m -v -run", "-timeout 70m -v -run", "-timeout 70m"},
		{"no failure logs", "          path: ${{ runner.temp }}/harness-fp/**/*.log\n", "", "harness-fp/**/*.log"},
		{"artifact name of another job", "name: logs-fp-${{ matrix.os }}", "name: logs-m5d-${{ matrix.os }}", "logs-fp-"},
		{"test added to vm-m5d", "-run '^TestM5DERP$'", "-run '^TestM5DERP$|^TestFootprint$'", "vm-m5d job holds TestFootprint"},
		{"flag added to vm-upg", "-upg\n", "-upg -fp\n", "vm-upg job holds  -fp"},
	} {
		if !strings.Contains(fpYmlWorkflowDoc, tc.from) {
			t.Fatalf("%s: the fixture lacks %q", tc.name, tc.from)
		}
		doc := strings.ReplaceAll(fpYmlWorkflowDoc, tc.from, tc.to)
		if err := fpYmlWorkflow(doc); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.want)
		}
	}

	moved := strings.Replace(fpYmlWorkflowDoc, fpYmlBuildSteps, "", 1)
	moved = strings.Replace(moved, fpYmlUploadStep, fpYmlUploadStep+fpYmlBuildSteps, 1)
	if err := fpYmlWorkflow(moved); err == nil || !strings.Contains(err.Error(), "before it uploads path: bin/harness") {
		t.Errorf("the agent build after the upload: %v, want an error naming the upload", err)
	}
}

// TestFpwUnitWorkflowFile checks the committed workflow, which is the only place TestFootprint runs.
func TestFpwUnitWorkflowFile(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/harness.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := fpYmlWorkflow(string(b)); err != nil {
		t.Error(err)
	}
}
