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
	m6pYmlRunStep = "Run the M6 panel harness"
	m6pYmlJob     = "vm-m6p"
	// m6pYmlStepTimeout is the -timeout in minutes of the TestM6Panel step and
	// m6pYmlJobLimit the job's timeout-minutes.
	m6pYmlStepTimeout = 50
	m6pYmlJobLimit    = 70
)

// m6pYmlCheck checks the text of harness.yml for TestM6Panel: the vm-m6p job
// has the vm jobs' setup, a timeout-minutes of m6pYmlJobLimit, and a step that
// runs the test with the lab flags, a work directory of its own, a -timeout of
// m6pYmlStepTimeout minutes and its failure logs; and none of the vm-m5, vm-m5d,
// vm-m6n, vm-m6c and vm-m7r jobs holds the test.
func m6pYmlCheck(yml string) error {
	for _, name := range []string{"vm-m5", "vm-m5d", "vm-m6n", "vm-m6c", "vm-m7r"} {
		other, ok := m3dxExpoJob(yml, name)
		if !ok {
			return fmt.Errorf("harness: the workflow has no %s job", name)
		}
		for _, text := range []string{"TestM6Panel", "harness-m6p", "-m6p"} {
			if strings.Contains(other, text) {
				return fmt.Errorf("harness: the %s job holds %s, which belongs to the %s job", name, text, m6pYmlJob)
			}
		}
	}

	job, ok := m3dxExpoJob(yml, m6pYmlJob)
	if !ok {
		return fmt.Errorf("harness: the workflow has no %s job", m6pYmlJob)
	}
	musts := []string{"needs: build", "runs-on: ubuntu-24.04", "fail-fast: false", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]"}
	for _, want := range append(musts, m4lSetup...) {
		if !strings.Contains(job, want) {
			return fmt.Errorf("harness: the %s job lacks %s", m6pYmlJob, want)
		}
	}
	limit := m3dxExpoLimitRE.FindStringSubmatch(job)
	if limit == nil {
		return fmt.Errorf("harness: the %s job has no timeout-minutes", m6pYmlJob)
	}
	if have, _ := strconv.Atoi(limit[1]); have != m6pYmlJobLimit || have > m4lJobMax {
		return fmt.Errorf("harness: the %s job has timeout-minutes %d, want %d", m6pYmlJob, have, m6pYmlJobLimit)
	}

	run, ok := m4lStep(job, m6pYmlRunStep)
	if !ok {
		return fmt.Errorf("harness: the %s job has no step named %q", m6pYmlJob, m6pYmlRunStep)
	}
	for _, want := range []string{
		"-run '^TestM6Panel$'", " -m6p", "-os ${{ matrix.os }}",
		`-bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"`,
		`-m2 "$GITHUB_WORKSPACE/bin/harness"`,
		`-commit "$GITHUB_SHA"`,
		`-workdir "$RUNNER_TEMP/harness-m6p"`,
		`-cache "$HOME/.cache/darkpanel-harness"`,
	} {
		if !strings.Contains(run, want) {
			return fmt.Errorf("harness: the step %q lacks %s", m6pYmlRunStep, want)
		}
	}
	m := m3dxExpoTimeoutRE.FindStringSubmatch(run)
	if m == nil {
		return fmt.Errorf("harness: the step %q has no -timeout <n>m", m6pYmlRunStep)
	}
	n, _ := strconv.Atoi(m[1])
	if n != m6pYmlStepTimeout {
		return fmt.Errorf("harness: the step %q has -timeout %sm, want %dm", m6pYmlRunStep, m[1], m6pYmlStepTimeout)
	}
	if time.Duration(n)*time.Minute < m6pDeadline+10*time.Minute {
		return fmt.Errorf("harness: the step %q has -timeout %sm, under the scenario deadline %s plus 10 minutes", m6pYmlRunStep, m[1], m6pDeadline)
	}
	for _, want := range []string{"name: logs-m6p-${{ matrix.os }}", "harness-m6p/**/*.log"} {
		if !strings.Contains(job, want) {
			return errors.New("harness: the " + m6pYmlJob + " job's failure upload lacks " + want)
		}
	}
	return nil
}

// m6pYmlDoc has the parts of harness.yml that m6pYmlCheck reads.
const m6pYmlDoc = `jobs:
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
  vm-m6p:
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
      - name: Run the M6 panel harness
        run: >-
          go test -tags harness -timeout 50m -v -run '^TestM6Panel$' ./test/harness/
          -args -os ${{ matrix.os }} -m6p
          -bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"
          -m2 "$GITHUB_WORKSPACE/bin/harness"
          -commit "$GITHUB_SHA"
          -workdir "$RUNNER_TEMP/harness-m6p"
          -cache "$HOME/.cache/darkpanel-harness"
      - if: failure()
        uses: actions/upload-artifact@x
        with:
          name: logs-m6p-${{ matrix.os }}
          path: ${{ runner.temp }}/harness-m6p/**/*.log
  vm-m7r:
    needs: build
    timeout-minutes: 70
    steps:
      - name: Run the M7 restore harness
        run: >-
          go test -tags harness -timeout 50m -v -run '^TestM7Restore$' ./test/harness/
          -args -os ${{ matrix.os }} -m7r
`

func TestM6pwUnitWorkflow(t *testing.T) {
	if err := m6pYmlCheck(m6pYmlDoc); err != nil {
		t.Fatalf("the fixture: %v", err)
	}
	for _, tc := range []struct{ name, from, to, want string }{
		{"no vm-m6p job", "  vm-m6p:\n", "  vm-other:\n", "no vm-m6p job"},
		{"no vm-m6c job", "  vm-m6c:\n", "  vm-other:\n", "no vm-m6c job"},
		{"no vm-m7r job", "  vm-m7r:\n", "  vm-other:\n", "no vm-m7r job"},
		{"limit under 70", "runs-on: ubuntu-24.04\n    timeout-minutes: 70", "runs-on: ubuntu-24.04\n    timeout-minutes: 60", "timeout-minutes 60, want 70"},
		{"limit past the 120 minutes", "runs-on: ubuntu-24.04\n    timeout-minutes: 70", "runs-on: ubuntu-24.04\n    timeout-minutes: 130", "timeout-minutes 130, want 70"},
		{"no needs", "  vm-m6p:\n    needs: build\n", "  vm-m6p:\n    needs: other\n", "needs: build"},
		{"os matrix without debian-13", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]", "os: [ubuntu-22.04, ubuntu-24.04, debian-12]", "debian-13"},
		{"no KVM", "test -e /dev/kvm", "test -e /dev/none", "/dev/kvm"},
		{"no panel step", "Run the M6 panel harness", "Run M6 panel", `no step named "Run the M6 panel harness"`},
		{"wrong test", "'^TestM6Panel$'", "'^TestM6Other$'", "TestM6Panel"},
		{"no flag", " -m6p\n", "\n", "-m6p"},
		{"no fleet builds", "          -m2 \"$GITHUB_WORKSPACE/bin/harness\"\n", "", "-m2"},
		{"no commit", "          -commit \"$GITHUB_SHA\"\n", "", "-commit"},
		{"no image cache", "          -cache \"$HOME/.cache/darkpanel-harness\"\n", "", "-cache"},
		{"no workdir", "          -workdir \"$RUNNER_TEMP/harness-m6p\"\n", "", "-workdir"},
		{"workdir of vm-m6n", "-workdir \"$RUNNER_TEMP/harness-m6p\"", "-workdir \"$RUNNER_TEMP/harness-m6n\"", "harness-m6p"},
		{"short timeout", "-timeout 50m -v -run '^TestM6Panel$'", "-timeout 40m -v -run '^TestM6Panel$'", "-timeout 40m"},
		{"long timeout", "-timeout 50m -v -run '^TestM6Panel$'", "-timeout 60m -v -run '^TestM6Panel$'", "-timeout 60m"},
		{"no failure logs", "          path: ${{ runner.temp }}/harness-m6p/**/*.log\n", "", "harness-m6p/**/*.log"},
		{"artifact name of vm-m6c", "name: logs-m6p-${{ matrix.os }}", "name: logs-m6c-${{ matrix.os }}", "logs-m6p-"},
		{"test added to vm-m6c", "-run '^TestM6Cloudflare$'", "-run '^TestM6Cloudflare$|^TestM6Panel$'", "vm-m6c job holds TestM6Panel"},
		{"test added to vm-m6n", "-run '^TestM6NPM$'", "-run '^TestM6NPM$|^TestM6Panel$'", "vm-m6n job holds TestM6Panel"},
		{"flag added to vm-m7r", "-args -os ${{ matrix.os }} -m7r", "-args -os ${{ matrix.os }} -m7r -m6p", "vm-m7r job holds -m6p"},
		{"flag added to vm-m5d", "-args -os ${{ matrix.os }} -m5d", "-args -os ${{ matrix.os }} -m5d -m6p", "vm-m5d job holds -m6p"},
	} {
		if n := strings.Count(m6pYmlDoc, tc.from); n != 1 {
			t.Fatalf("%s: the fixture holds %q %d times, want once", tc.name, tc.from, n)
		}
		doc := strings.Replace(m6pYmlDoc, tc.from, tc.to, 1)
		if err := m6pYmlCheck(doc); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.want)
		}
	}
}

// TestM6pwUnitWorkflowFile checks the committed workflow, which is the only
// place TestM6Panel runs.
func TestM6pwUnitWorkflowFile(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/harness.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := m6pYmlCheck(string(b)); err != nil {
		t.Error(err)
	}
}
