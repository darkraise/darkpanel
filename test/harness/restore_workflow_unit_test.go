//go:build harness

package harness

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	m7rwRunStep = "Run the M7 restore harness"
	m7rwJob     = "vm-m7r"
	// m7rwStepTimeout is the -timeout in minutes of the TestM7Restore step
	// and m7rwJobLimit the job's timeout-minutes.
	m7rwStepTimeout = 50
	m7rwJobLimit    = 70
)

// m7rwWorkflow checks the text of harness.yml for TestM7Restore: the vm-m7r
// job has the vm jobs' setup, a timeout-minutes of m7rwJobLimit, and a step
// that runs the test with the lab flags, a work directory of its own, a
// -timeout of m7rwStepTimeout minutes and its failure logs; and none of the
// vm-m5, vm-m5d, vm-m6n and vm-m6c jobs holds the test.
func m7rwWorkflow(yml string) error {
	for _, name := range []string{"vm-m5", "vm-m5d", "vm-m6n", "vm-m6c"} {
		other, ok := m3dxExpoJob(yml, name)
		if !ok {
			return fmt.Errorf("harness: the workflow has no %s job", name)
		}
		for _, text := range []string{"TestM7Restore", "harness-m7r", "-m7r"} {
			if strings.Contains(other, text) {
				return fmt.Errorf("harness: the %s job holds %s, which belongs to the %s job", name, text, m7rwJob)
			}
		}
	}

	job, ok := m3dxExpoJob(yml, m7rwJob)
	if !ok {
		return fmt.Errorf("harness: the workflow has no %s job", m7rwJob)
	}
	musts := []string{"needs: build", "runs-on: ubuntu-24.04", "fail-fast: false", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]"}
	for _, want := range append(musts, m4lSetup...) {
		if !strings.Contains(job, want) {
			return fmt.Errorf("harness: the %s job lacks %s", m7rwJob, want)
		}
	}
	limit := m3dxExpoLimitRE.FindStringSubmatch(job)
	if limit == nil {
		return fmt.Errorf("harness: the %s job has no timeout-minutes", m7rwJob)
	}
	if have, _ := strconv.Atoi(limit[1]); have != m7rwJobLimit || have > m4lJobMax {
		return fmt.Errorf("harness: the %s job has timeout-minutes %d, want %d", m7rwJob, have, m7rwJobLimit)
	}

	run, ok := m4lStep(job, m7rwRunStep)
	if !ok {
		return fmt.Errorf("harness: the %s job has no step named %q", m7rwJob, m7rwRunStep)
	}
	for _, want := range []string{
		"-run '^TestM7Restore$'", " -m7r", "-os ${{ matrix.os }}",
		`-bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"`,
		`-m2 "$GITHUB_WORKSPACE/bin/harness"`,
		`-commit "$GITHUB_SHA"`,
		`-workdir "$RUNNER_TEMP/harness-m7r"`,
		`-cache "$HOME/.cache/darkpanel-harness"`,
	} {
		if !strings.Contains(run, want) {
			return fmt.Errorf("harness: the step %q lacks %s", m7rwRunStep, want)
		}
	}
	m := m3dxExpoTimeoutRE.FindStringSubmatch(run)
	if m == nil {
		return fmt.Errorf("harness: the step %q has no -timeout <n>m", m7rwRunStep)
	}
	n, _ := strconv.Atoi(m[1])
	if n != m7rwStepTimeout {
		return fmt.Errorf("harness: the step %q has -timeout %sm, want %dm", m7rwRunStep, m[1], m7rwStepTimeout)
	}
	if time.Duration(n)*time.Minute < m7rDeadline+10*time.Minute {
		return fmt.Errorf("harness: the step %q has -timeout %sm, under the scenario deadline %s plus 10 minutes", m7rwRunStep, m[1], m7rDeadline)
	}
	if limit, _ := strconv.Atoi(limit[1]); limit < n+20 {
		return fmt.Errorf("harness: the %s job has timeout-minutes %d, under the step's -timeout plus 20 minutes of setup", m7rwJob, limit)
	}
	for _, want := range []string{"name: logs-m7r-${{ matrix.os }}", "harness-m7r/**/*.log"} {
		if !strings.Contains(job, want) {
			return errors.New("harness: the " + m7rwJob + " job's failure upload lacks " + want)
		}
	}
	return nil
}

// m7rwWorkflowDoc has the parts of harness.yml that m7rwWorkflow reads.
const m7rwWorkflowDoc = `jobs:
  build:
    runs-on: ubuntu-24.04
    steps:
      - run: make harness-builds
  vm-m5:
    needs: build
    timeout-minutes: 100
    steps:
      - name: Run the M5 node harness
        run: >-
          go test -tags harness -timeout 70m -v -run '^TestM5Node$' ./test/harness/
          -args -os ${{ matrix.os }} -m5
  vm-m5d:
    needs: build
    timeout-minutes: 60
    steps:
      - name: Run the M5 DERP harness
        run: >-
          go test -tags harness -timeout 40m -v -run '^TestM5DERP$' ./test/harness/
          -args -os ${{ matrix.os }} -m5d
  vm-m6n:
    needs: build
    timeout-minutes: 70
    steps:
      - name: Run the M6 NPM harness
        run: >-
          go test -tags harness -timeout 50m -v -run '^TestM6NPM$' ./test/harness/
          -args -os ${{ matrix.os }} -m6n
  vm-m6c:
    needs: build
    timeout-minutes: 60
    steps:
      - name: Run the M6 Cloudflare harness
        run: >-
          go test -tags harness -timeout 40m -v -run '^TestM6Cloudflare$' ./test/harness/
          -args -os ${{ matrix.os }} -m6c
  vm-m7r:
    needs: build
    runs-on: ubuntu-24.04
    timeout-minutes: 70
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
      - name: Run the M7 restore harness
        run: >-
          go test -tags harness -timeout 50m -v -run '^TestM7Restore$' ./test/harness/
          -args -os ${{ matrix.os }} -m7r
          -bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"
          -m2 "$GITHUB_WORKSPACE/bin/harness"
          -commit "$GITHUB_SHA"
          -workdir "$RUNNER_TEMP/harness-m7r"
          -cache "$HOME/.cache/darkpanel-harness"
      - if: failure()
        uses: actions/upload-artifact@x
        with:
          name: logs-m7r-${{ matrix.os }}
          path: ${{ runner.temp }}/harness-m7r/**/*.log
`

func TestM7rUnitWorkflow(t *testing.T) {
	if err := m7rwWorkflow(m7rwWorkflowDoc); err != nil {
		t.Fatalf("the fixture: %v", err)
	}
	for _, tc := range []struct{ name, from, to, want string }{
		{"no vm-m7r job", "  vm-m7r:\n", "  vm-other:\n", "no vm-m7r job"},
		{"no vm-m6c job", "  vm-m6c:\n", "  vm-other:\n", "no vm-m6c job"},
		{"no vm-m6n job", "  vm-m6n:\n", "  vm-other:\n", "no vm-m6n job"},
		{"limit under 70", "runs-on: ubuntu-24.04\n    timeout-minutes: 70", "runs-on: ubuntu-24.04\n    timeout-minutes: 60", "timeout-minutes 60, want 70"},
		{"limit past the 120 minutes", "runs-on: ubuntu-24.04\n    timeout-minutes: 70", "runs-on: ubuntu-24.04\n    timeout-minutes: 130", "timeout-minutes 130, want 70"},
		{"no needs", "  vm-m7r:\n    needs: build\n", "  vm-m7r:\n    needs: other\n", "needs: build"},
		{"os matrix without debian-13", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]", "os: [ubuntu-22.04, ubuntu-24.04, debian-12]", "debian-13"},
		{"no KVM", "test -e /dev/kvm", "test -e /dev/none", "/dev/kvm"},
		{"no restore step", "Run the M7 restore harness", "Run M7 restore", `no step named "Run the M7 restore harness"`},
		{"wrong test", "'^TestM7Restore$'", "'^TestM7Other$'", "TestM7Restore"},
		{"no flag", " -m7r\n", "\n", "-m7r"},
		{"no fleet builds", "          -m2 \"$GITHUB_WORKSPACE/bin/harness\"\n", "", "-m2"},
		{"no commit", "          -commit \"$GITHUB_SHA\"\n", "", "-commit"},
		{"no image cache", "          -cache \"$HOME/.cache/darkpanel-harness\"\n", "", "-cache"},
		{"no workdir", "          -workdir \"$RUNNER_TEMP/harness-m7r\"\n", "", "-workdir"},
		{"workdir of vm-m6c", "-workdir \"$RUNNER_TEMP/harness-m7r\"", "-workdir \"$RUNNER_TEMP/harness-m6c\"", "harness-m7r"},
		{"short timeout", "-timeout 50m -v -run '^TestM7Restore$'", "-timeout 40m -v -run '^TestM7Restore$'", "-timeout 40m"},
		{"long timeout", "-timeout 50m -v -run '^TestM7Restore$'", "-timeout 60m -v -run '^TestM7Restore$'", "-timeout 60m"},
		{"no failure logs", "          path: ${{ runner.temp }}/harness-m7r/**/*.log\n", "", "harness-m7r/**/*.log"},
		{"artifact name of vm-m6c", "name: logs-m7r-${{ matrix.os }}", "name: logs-m6c-${{ matrix.os }}", "logs-m7r-"},
		{"test added to vm-m6c", "-run '^TestM6Cloudflare$'", "-run '^TestM6Cloudflare$|^TestM7Restore$'", "vm-m6c job holds TestM7Restore"},
		{"flag added to vm-m5d", "-args -os ${{ matrix.os }} -m5d", "-args -os ${{ matrix.os }} -m5d -m7r", "vm-m5d job holds -m7r"},
	} {
		if n := strings.Count(m7rwWorkflowDoc, tc.from); n != 1 {
			t.Fatalf("%s: the fixture holds %q %d times, want once", tc.name, tc.from, n)
		}
		doc := strings.Replace(m7rwWorkflowDoc, tc.from, tc.to, 1)
		if err := m7rwWorkflow(doc); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.want)
		}
	}
}

// TestM7rUnitWorkflowFile checks the committed workflow, which is the only
// place TestM7Restore runs.
func TestM7rUnitWorkflowFile(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/harness.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := m7rwWorkflow(string(b)); err != nil {
		t.Error(err)
	}
}
