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
	m6cwRunStep = "Run the M6 Cloudflare harness"
	m6cwJob     = "vm-m6c"
	// m6cwStepTimeout is the -timeout in minutes of the TestM6Cloudflare step
	// and m6cwJobLimit the job's timeout-minutes.
	m6cwStepTimeout = 40
	m6cwJobLimit    = 60
)

// m6cwWorkflow checks the text of harness.yml for TestM6Cloudflare: the
// vm-m6c job has the vm jobs' setup, a timeout-minutes of m6cwJobLimit, and a
// step that runs the test with the lab flags, a work directory of its own, a
// -timeout of m6cwStepTimeout minutes and its failure logs; and none of the
// vm-m5, vm-m5d and vm-m6n jobs holds the test.
func m6cwWorkflow(yml string) error {
	for _, name := range []string{"vm-m5", "vm-m5d", "vm-m6n"} {
		other, ok := m3dxExpoJob(yml, name)
		if !ok {
			return fmt.Errorf("harness: the workflow has no %s job", name)
		}
		for _, text := range []string{"TestM6Cloudflare", "harness-m6c", "-m6c"} {
			if strings.Contains(other, text) {
				return fmt.Errorf("harness: the %s job holds %s, which belongs to the %s job", name, text, m6cwJob)
			}
		}
	}

	job, ok := m3dxExpoJob(yml, m6cwJob)
	if !ok {
		return fmt.Errorf("harness: the workflow has no %s job", m6cwJob)
	}
	musts := []string{"needs: build", "runs-on: ubuntu-24.04", "fail-fast: false", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]"}
	for _, want := range append(musts, m4lSetup...) {
		if !strings.Contains(job, want) {
			return fmt.Errorf("harness: the %s job lacks %s", m6cwJob, want)
		}
	}
	limit := m3dxExpoLimitRE.FindStringSubmatch(job)
	if limit == nil {
		return fmt.Errorf("harness: the %s job has no timeout-minutes", m6cwJob)
	}
	if have, _ := strconv.Atoi(limit[1]); have != m6cwJobLimit || have > m4lJobMax {
		return fmt.Errorf("harness: the %s job has timeout-minutes %d, want %d", m6cwJob, have, m6cwJobLimit)
	}

	run, ok := m4lStep(job, m6cwRunStep)
	if !ok {
		return fmt.Errorf("harness: the %s job has no step named %q", m6cwJob, m6cwRunStep)
	}
	for _, want := range []string{
		"-run '^TestM6Cloudflare$'", " -m6c", "-os ${{ matrix.os }}",
		`-bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"`,
		`-m2 "$GITHUB_WORKSPACE/bin/harness"`,
		`-commit "$GITHUB_SHA"`,
		`-workdir "$RUNNER_TEMP/harness-m6c"`,
		`-cache "$HOME/.cache/darkpanel-harness"`,
	} {
		if !strings.Contains(run, want) {
			return fmt.Errorf("harness: the step %q lacks %s", m6cwRunStep, want)
		}
	}
	m := m3dxExpoTimeoutRE.FindStringSubmatch(run)
	if m == nil {
		return fmt.Errorf("harness: the step %q has no -timeout <n>m", m6cwRunStep)
	}
	n, _ := strconv.Atoi(m[1])
	if n != m6cwStepTimeout {
		return fmt.Errorf("harness: the step %q has -timeout %sm, want %dm", m6cwRunStep, m[1], m6cwStepTimeout)
	}
	if time.Duration(n)*time.Minute < m6cDeadline+10*time.Minute {
		return fmt.Errorf("harness: the step %q has -timeout %sm, under the scenario deadline %s plus 10 minutes", m6cwRunStep, m[1], m6cDeadline)
	}
	for _, want := range []string{"name: logs-m6c-${{ matrix.os }}", "harness-m6c/**/*.log"} {
		if !strings.Contains(job, want) {
			return errors.New("harness: the " + m6cwJob + " job's failure upload lacks " + want)
		}
	}
	return nil
}

// m6cwWorkflowDoc has the parts of harness.yml that m6cwWorkflow reads.
const m6cwWorkflowDoc = `jobs:
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
    runs-on: ubuntu-24.04
    timeout-minutes: 60
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
      - name: Run the M6 Cloudflare harness
        run: >-
          go test -tags harness -timeout 40m -v -run '^TestM6Cloudflare$' ./test/harness/
          -args -os ${{ matrix.os }} -m6c
          -bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"
          -m2 "$GITHUB_WORKSPACE/bin/harness"
          -commit "$GITHUB_SHA"
          -workdir "$RUNNER_TEMP/harness-m6c"
          -cache "$HOME/.cache/darkpanel-harness"
      - if: failure()
        uses: actions/upload-artifact@x
        with:
          name: logs-m6c-${{ matrix.os }}
          path: ${{ runner.temp }}/harness-m6c/**/*.log
`

func TestM6cwUnitWorkflow(t *testing.T) {
	if err := m6cwWorkflow(m6cwWorkflowDoc); err != nil {
		t.Fatalf("the fixture: %v", err)
	}
	for _, tc := range []struct{ name, from, to, want string }{
		{"no vm-m6c job", "  vm-m6c:\n", "  vm-other:\n", "no vm-m6c job"},
		{"no vm-m6n job", "  vm-m6n:\n", "  vm-other:\n", "no vm-m6n job"},
		{"no vm-m5d job", "  vm-m5d:\n", "  vm-other:\n", "no vm-m5d job"},
		{"limit under 60", "runs-on: ubuntu-24.04\n    timeout-minutes: 60", "runs-on: ubuntu-24.04\n    timeout-minutes: 50", "timeout-minutes 50, want 60"},
		{"limit past the 120 minutes", "runs-on: ubuntu-24.04\n    timeout-minutes: 60", "runs-on: ubuntu-24.04\n    timeout-minutes: 130", "timeout-minutes 130, want 60"},
		{"no needs", "  vm-m6c:\n    needs: build\n", "  vm-m6c:\n    needs: other\n", "needs: build"},
		{"os matrix without debian-13", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]", "os: [ubuntu-22.04, ubuntu-24.04, debian-12]", "debian-13"},
		{"no KVM", "test -e /dev/kvm", "test -e /dev/none", "/dev/kvm"},
		{"no Cloudflare step", "Run the M6 Cloudflare harness", "Run M6 Cloudflare", `no step named "Run the M6 Cloudflare harness"`},
		{"wrong test", "'^TestM6Cloudflare$'", "'^TestM6Other$'", "TestM6Cloudflare"},
		{"no flag", " -m6c\n", "\n", "-m6c"},
		{"no fleet builds", "          -m2 \"$GITHUB_WORKSPACE/bin/harness\"\n", "", "-m2"},
		{"no commit", "          -commit \"$GITHUB_SHA\"\n", "", "-commit"},
		{"no image cache", "          -cache \"$HOME/.cache/darkpanel-harness\"\n", "", "-cache"},
		{"no workdir", "          -workdir \"$RUNNER_TEMP/harness-m6c\"\n", "", "-workdir"},
		{"workdir of vm-m6n", "-workdir \"$RUNNER_TEMP/harness-m6c\"", "-workdir \"$RUNNER_TEMP/harness-m6n\"", "harness-m6c"},
		{"short timeout", "-timeout 40m -v -run '^TestM6Cloudflare$'", "-timeout 30m -v -run '^TestM6Cloudflare$'", "-timeout 30m"},
		{"long timeout", "-timeout 40m -v -run '^TestM6Cloudflare$'", "-timeout 50m -v -run '^TestM6Cloudflare$'", "-timeout 50m"},
		{"no failure logs", "          path: ${{ runner.temp }}/harness-m6c/**/*.log\n", "", "harness-m6c/**/*.log"},
		{"artifact name of vm-m6n", "name: logs-m6c-${{ matrix.os }}", "name: logs-m6n-${{ matrix.os }}", "logs-m6c-"},
		{"test added to vm-m6n", "-run '^TestM6NPM$'", "-run '^TestM6NPM$|^TestM6Cloudflare$'", "vm-m6n job holds TestM6Cloudflare"},
		{"flag added to vm-m5d", "-args -os ${{ matrix.os }} -m5d", "-args -os ${{ matrix.os }} -m5d -m6c", "vm-m5d job holds -m6c"},
	} {
		if n := strings.Count(m6cwWorkflowDoc, tc.from); n != 1 {
			t.Fatalf("%s: the fixture holds %q %d times, want once", tc.name, tc.from, n)
		}
		doc := strings.Replace(m6cwWorkflowDoc, tc.from, tc.to, 1)
		if err := m6cwWorkflow(doc); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.want)
		}
	}
}

// TestM6cwUnitWorkflowFile checks the committed workflow, which is the only
// place TestM6Cloudflare runs.
func TestM6cwUnitWorkflowFile(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/harness.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := m6cwWorkflow(string(b)); err != nil {
		t.Error(err)
	}
}
