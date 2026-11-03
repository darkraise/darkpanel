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
	m6nwRunStep = "Run the M6 NPM harness"
	m6nwJob     = "vm-m6n"
	// m6nwStepTimeout is the -timeout in minutes of the TestM6NPM step and
	// m6nwJobLimit the job's timeout-minutes.
	m6nwStepTimeout = 50
	m6nwJobLimit    = 70
)

// m6nwWorkflow checks the text of harness.yml for TestM6NPM: the vm-m6n job
// has the vm jobs' setup, a timeout-minutes of m6nwJobLimit, and a step that
// runs the test with the lab flags, a work directory of its own, a -timeout of
// m6nwStepTimeout minutes and its failure logs; and neither the vm-m5 job nor
// the vm-m5d job holds the test.
func m6nwWorkflow(yml string) error {
	for _, name := range []string{"vm-m5", "vm-m5d"} {
		other, ok := m3dxExpoJob(yml, name)
		if !ok {
			return fmt.Errorf("harness: the workflow has no %s job", name)
		}
		for _, text := range []string{"TestM6NPM", "harness-m6n", "-m6n"} {
			if strings.Contains(other, text) {
				return fmt.Errorf("harness: the %s job holds %s, which belongs to the %s job", name, text, m6nwJob)
			}
		}
	}

	job, ok := m3dxExpoJob(yml, m6nwJob)
	if !ok {
		return fmt.Errorf("harness: the workflow has no %s job", m6nwJob)
	}
	musts := []string{"needs: build", "runs-on: ubuntu-24.04", "fail-fast: false", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]"}
	for _, want := range append(musts, m4lSetup...) {
		if !strings.Contains(job, want) {
			return fmt.Errorf("harness: the %s job lacks %s", m6nwJob, want)
		}
	}
	limit := m3dxExpoLimitRE.FindStringSubmatch(job)
	if limit == nil {
		return fmt.Errorf("harness: the %s job has no timeout-minutes", m6nwJob)
	}
	if have, _ := strconv.Atoi(limit[1]); have != m6nwJobLimit || have > m4lJobMax {
		return fmt.Errorf("harness: the %s job has timeout-minutes %d, want %d", m6nwJob, have, m6nwJobLimit)
	}

	run, ok := m4lStep(job, m6nwRunStep)
	if !ok {
		return fmt.Errorf("harness: the %s job has no step named %q", m6nwJob, m6nwRunStep)
	}
	for _, want := range []string{
		"-run '^TestM6NPM$'", " -m6n", "-os ${{ matrix.os }}",
		`-bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"`,
		`-m2 "$GITHUB_WORKSPACE/bin/harness"`,
		`-derper "$GITHUB_WORKSPACE/bin/harness/derper-$(cat release/derper.version)-linux-amd64"`,
		`-commit "$GITHUB_SHA"`,
		`-workdir "$RUNNER_TEMP/harness-m6n"`,
		`-cache "$HOME/.cache/darkpanel-harness"`,
	} {
		if !strings.Contains(run, want) {
			return fmt.Errorf("harness: the step %q lacks %s", m6nwRunStep, want)
		}
	}
	m := m3dxExpoTimeoutRE.FindStringSubmatch(run)
	if m == nil {
		return fmt.Errorf("harness: the step %q has no -timeout <n>m", m6nwRunStep)
	}
	n, _ := strconv.Atoi(m[1])
	if n != m6nwStepTimeout {
		return fmt.Errorf("harness: the step %q has -timeout %sm, want %dm", m6nwRunStep, m[1], m6nwStepTimeout)
	}
	if time.Duration(n)*time.Minute < m6nDeadline+10*time.Minute {
		return fmt.Errorf("harness: the step %q has -timeout %sm, under the scenario deadline %s plus 10 minutes", m6nwRunStep, m[1], m6nDeadline)
	}
	for _, want := range []string{"name: logs-m6n-${{ matrix.os }}", "harness-m6n/**/*.log"} {
		if !strings.Contains(job, want) {
			return errors.New("harness: the " + m6nwJob + " job's failure upload lacks " + want)
		}
	}
	return nil
}

// m6nwWorkflowDoc has the parts of harness.yml that m6nwWorkflow reads.
const m6nwWorkflowDoc = `jobs:
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
      - name: Run the M6 NPM harness
        run: >-
          go test -tags harness -timeout 50m -v -run '^TestM6NPM$' ./test/harness/
          -args -os ${{ matrix.os }} -m6n
          -bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"
          -m2 "$GITHUB_WORKSPACE/bin/harness"
          -derper "$GITHUB_WORKSPACE/bin/harness/derper-$(cat release/derper.version)-linux-amd64"
          -commit "$GITHUB_SHA"
          -workdir "$RUNNER_TEMP/harness-m6n"
          -cache "$HOME/.cache/darkpanel-harness"
      - if: failure()
        uses: actions/upload-artifact@x
        with:
          name: logs-m6n-${{ matrix.os }}
          path: ${{ runner.temp }}/harness-m6n/**/*.log
`

func TestM6nwUnitWorkflow(t *testing.T) {
	if err := m6nwWorkflow(m6nwWorkflowDoc); err != nil {
		t.Fatalf("the fixture: %v", err)
	}
	for _, tc := range []struct{ name, from, to, want string }{
		{"no vm-m6n job", "  vm-m6n:\n", "  vm-other:\n", "no vm-m6n job"},
		{"no vm-m5d job", "  vm-m5d:\n", "  vm-other:\n", "no vm-m5d job"},
		{"limit of vm-m5d", "timeout-minutes: 70", "timeout-minutes: 60", "timeout-minutes 60, want 70"},
		{"limit past the 120 minutes", "timeout-minutes: 70", "timeout-minutes: 130", "timeout-minutes 130, want 70"},
		{"no needs", "    needs: build\n    runs-on: ubuntu-24.04\n    timeout-minutes: 70", "    needs: other\n    runs-on: ubuntu-24.04\n    timeout-minutes: 70", "needs: build"},
		{"os matrix without debian-13", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]", "os: [ubuntu-22.04, ubuntu-24.04, debian-12]", "debian-13"},
		{"no KVM", "/dev/kvm", "/dev/none", "/dev/kvm"},
		{"no NPM step", "Run the M6 NPM harness", "Run M6 NPM", `no step named "Run the M6 NPM harness"`},
		{"wrong test", "'^TestM6NPM$'", "'^TestM6Other$'", "TestM6NPM"},
		{"no flag", " -m6n\n", "\n", "-m6n"},
		{"no fleet builds", "          -m2 \"$GITHUB_WORKSPACE/bin/harness\"\n", "", "-m2"},
		{"no derper binary", "-derper ", "-unknown ", "-derper"},
		{"no commit", "          -commit \"$GITHUB_SHA\"\n", "", "-commit"},
		{"no image cache", "          -cache \"$HOME/.cache/darkpanel-harness\"\n", "", "-cache"},
		{"no workdir", "          -workdir \"$RUNNER_TEMP/harness-m6n\"\n", "", "-workdir"},
		{"workdir of vm-m5d", "-workdir \"$RUNNER_TEMP/harness-m6n\"", "-workdir \"$RUNNER_TEMP/harness-m5d\"", "harness-m6n"},
		{"short timeout", "-timeout 50m -v -run", "-timeout 40m -v -run", "-timeout 40m"},
		{"long timeout", "-timeout 50m -v -run", "-timeout 60m -v -run", "-timeout 60m"},
		{"no failure logs", "          path: ${{ runner.temp }}/harness-m6n/**/*.log\n", "", "harness-m6n/**/*.log"},
		{"artifact name of vm-m5d", "name: logs-m6n-${{ matrix.os }}", "name: logs-m5d-${{ matrix.os }}", "logs-m6n-"},
		{"test added to vm-m5", "-run '^TestM5Node$'", "-run '^TestM5Node$|^TestM6NPM$'", "vm-m5 job holds TestM6NPM"},
		{"flag added to vm-m5d", "-args -os ${{ matrix.os }} -m5d", "-args -os ${{ matrix.os }} -m5d -m6n", "vm-m5d job holds -m6n"},
	} {
		if !strings.Contains(m6nwWorkflowDoc, tc.from) {
			t.Fatalf("%s: the fixture lacks %q", tc.name, tc.from)
		}
		doc := strings.Replace(m6nwWorkflowDoc, tc.from, tc.to, 1)
		if err := m6nwWorkflow(doc); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.want)
		}
	}
}

// TestM6nwUnitWorkflowFile checks the committed workflow, which is the only
// place TestM6NPM runs.
func TestM6nwUnitWorkflowFile(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/harness.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := m6nwWorkflow(string(b)); err != nil {
		t.Error(err)
	}
}
