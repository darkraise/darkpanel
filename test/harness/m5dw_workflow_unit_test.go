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
	m5dwCacheStep = "Cache the derper build"
	m5dwBuildStep = "Build derper"
	m5dwRunStep   = "Run the M5 DERP harness"
	m5dwJob       = "vm-m5d"
	// m5dwStepTimeout is the -timeout in minutes of the TestM5DERP step and
	// m5dwJobLimit the job's timeout-minutes.
	m5dwStepTimeout = 40
	m5dwJobLimit    = 60
)

// m5dwUploadRE finds the line of the step that uploads the bin/harness
// directory, and not the cache step's path of derper files inside it.
var m5dwUploadRE = regexp.MustCompile(`(?m)^\s*path: bin/harness$`)

// m5dwWorkflow checks the text of harness.yml for TestM5DERP: the build job
// caches the derper build under the derper version and builds it for amd64
// only on a cache miss, both before it uploads bin/harness; the vm-m5d job
// has the vm jobs' setup, a timeout-minutes of m5dwJobLimit, and a step that
// runs the test with the lab flags, the derper binary, a work directory of its
// own, a -timeout of m5dwStepTimeout minutes and its failure logs; and the
// vm-m5 job does not hold the test.
func m5dwWorkflow(yml string) error {
	build, ok := m3dxExpoJob(yml, "build")
	if !ok {
		return errors.New("harness: the workflow has no build job")
	}
	upload := -1
	if loc := m5dwUploadRE.FindStringIndex(build); loc != nil {
		upload = loc[0]
	}
	for _, s := range []struct {
		name string
		want []string
	}{
		{m5dwCacheStep, []string{"id: derper-cache", "actions/cache@", "path: bin/harness/derper-*", "key: harness-derper-${{ hashFiles('release/derper.version', 'release/releasetool/derper.go', 'go.mod') }}"}},
		{m5dwBuildStep, []string{"if: steps.derper-cache.outputs.cache-hit != 'true'", "go run ./release/releasetool derper 0.0.0 bin/harness -arch amd64"}},
	} {
		step, ok := m4lStep(build, s.name)
		if !ok {
			return fmt.Errorf("harness: the build job has no step named %q", s.name)
		}
		for _, want := range s.want {
			if !strings.Contains(step, want) {
				return fmt.Errorf("harness: the step %q lacks %s", s.name, want)
			}
		}
		if at := strings.Index(build, "- name: "+s.name); upload < 0 || at < 0 || at > upload {
			return fmt.Errorf("harness: the build job does not run the step %q before it uploads path: bin/harness", s.name)
		}
	}

	node, ok := m3dxExpoJob(yml, "vm-m5")
	if !ok {
		return errors.New("harness: the workflow has no vm-m5 job")
	}
	for _, text := range []string{"TestM5DERP", "harness-m5d", "-m5d"} {
		if strings.Contains(node, text) {
			return fmt.Errorf("harness: the vm-m5 job holds %s, which belongs to the %s job", text, m5dwJob)
		}
	}

	job, ok := m3dxExpoJob(yml, m5dwJob)
	if !ok {
		return fmt.Errorf("harness: the workflow has no %s job", m5dwJob)
	}
	musts := []string{"needs: build", "runs-on: ubuntu-24.04", "fail-fast: false", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]"}
	for _, want := range append(musts, m4lSetup...) {
		if !strings.Contains(job, want) {
			return fmt.Errorf("harness: the %s job lacks %s", m5dwJob, want)
		}
	}
	limit := m3dxExpoLimitRE.FindStringSubmatch(job)
	if limit == nil {
		return fmt.Errorf("harness: the %s job has no timeout-minutes", m5dwJob)
	}
	if have, _ := strconv.Atoi(limit[1]); have != m5dwJobLimit || have > m4lJobMax {
		return fmt.Errorf("harness: the %s job has timeout-minutes %d, want %d", m5dwJob, have, m5dwJobLimit)
	}

	run, ok := m4lStep(job, m5dwRunStep)
	if !ok {
		return fmt.Errorf("harness: the %s job has no step named %q", m5dwJob, m5dwRunStep)
	}
	for _, want := range []string{
		"-run '^TestM5DERP$'", " -m5d", "-os ${{ matrix.os }}",
		`-bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"`,
		`-m2 "$GITHUB_WORKSPACE/bin/harness"`,
		`-derper "$GITHUB_WORKSPACE/bin/harness/derper-$(cat release/derper.version)-linux-amd64"`,
		`-commit "$GITHUB_SHA"`,
		`-workdir "$RUNNER_TEMP/harness-m5d"`,
		`-cache "$HOME/.cache/darkpanel-harness"`,
	} {
		if !strings.Contains(run, want) {
			return fmt.Errorf("harness: the step %q lacks %s", m5dwRunStep, want)
		}
	}
	m := m3dxExpoTimeoutRE.FindStringSubmatch(run)
	if m == nil {
		return fmt.Errorf("harness: the step %q has no -timeout <n>m", m5dwRunStep)
	}
	n, _ := strconv.Atoi(m[1])
	if n != m5dwStepTimeout {
		return fmt.Errorf("harness: the step %q has -timeout %sm, want %dm", m5dwRunStep, m[1], m5dwStepTimeout)
	}
	if time.Duration(n)*time.Minute < m5dDeadline+10*time.Minute {
		return fmt.Errorf("harness: the step %q has -timeout %sm, under the scenario deadline %s plus 10 minutes", m5dwRunStep, m[1], m5dDeadline)
	}
	for _, want := range []string{"name: logs-m5d-${{ matrix.os }}", "harness-m5d/**/*.log"} {
		if !strings.Contains(job, want) {
			return fmt.Errorf("harness: the %s job's failure upload lacks %s", m5dwJob, want)
		}
	}
	return nil
}

const (
	m5dwDerperSteps = `      - name: Cache the derper build
        id: derper-cache
        uses: actions/cache@x
        with:
          path: bin/harness/derper-*
          key: harness-derper-${{ hashFiles('release/derper.version', 'release/releasetool/derper.go', 'go.mod') }}
      - name: Build derper
        if: steps.derper-cache.outputs.cache-hit != 'true'
        run: go run ./release/releasetool derper 0.0.0 bin/harness -arch amd64
`
	m5dwUploadStep = `      - uses: actions/upload-artifact@x
        with:
          path: bin/harness
`
	// m5dwWorkflowDoc has the parts of harness.yml that m5dwWorkflow reads.
	m5dwWorkflowDoc = `jobs:
  build:
    runs-on: ubuntu-24.04
    steps:
      - run: make harness-builds
      - name: Build the tslocal live test
        run: CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o bin/harness/tslocal.test ./internal/platform/tslocal
` + m5dwDerperSteps + m5dwUploadStep + `  vm-m5:
    needs: build
    timeout-minutes: 100
    steps:
      - name: Run the M5 node harness
        run: >-
          go test -tags harness -timeout 70m -v -run '^TestM5Node$' ./test/harness/
          -args -os ${{ matrix.os }} -m5
  vm-m5d:
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
      - name: Run the M5 DERP harness
        run: >-
          go test -tags harness -timeout 40m -v -run '^TestM5DERP$' ./test/harness/
          -args -os ${{ matrix.os }} -m5d
          -bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"
          -m2 "$GITHUB_WORKSPACE/bin/harness"
          -derper "$GITHUB_WORKSPACE/bin/harness/derper-$(cat release/derper.version)-linux-amd64"
          -commit "$GITHUB_SHA"
          -workdir "$RUNNER_TEMP/harness-m5d"
          -cache "$HOME/.cache/darkpanel-harness"
      - if: failure()
        uses: actions/upload-artifact@x
        with:
          name: logs-m5d-${{ matrix.os }}
          path: ${{ runner.temp }}/harness-m5d/**/*.log
`
)

func TestM5dwUnitWorkflow(t *testing.T) {
	if err := m5dwWorkflow(m5dwWorkflowDoc); err != nil {
		t.Fatalf("the fixture: %v", err)
	}
	for _, tc := range []struct{ name, from, to, want string }{
		{"no cache step", "Cache the derper build", "Cache something", `no step named "Cache the derper build"`},
		{"cache of everything", "path: bin/harness/derper-*", "path: bin/harness/*", "path: bin/harness/derper-*"},
		{"key not the version", "hashFiles('release/derper.version',", "hashFiles('go.sum',", "release/derper.version"},
		{"key not the build source", ", 'release/releasetool/derper.go'", "", "release/releasetool/derper.go"},
		{"key not the toolchain", ", 'go.mod'", "", "go.mod"},
		{"no cache id", "        id: derper-cache\n", "", "id: derper-cache"},
		{"no build step", "Build derper", "Build something", `no step named "Build derper"`},
		{"build on every run", "        if: steps.derper-cache.outputs.cache-hit != 'true'\n", "", "cache-hit"},
		{"both architectures", " -arch amd64", "", "-arch amd64"},
		{"no vm-m5d job", "  vm-m5d:\n", "  vm-other:\n", "no vm-m5d job"},
		{"limit of vm-m5", "timeout-minutes: 60", "timeout-minutes: 100", "timeout-minutes 100, want 60"},
		{"no needs", "needs: build", "needs: other", "needs: build"},
		{"os matrix without debian-13", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]", "os: [ubuntu-22.04, ubuntu-24.04, debian-12]", "debian-13"},
		{"no KVM", "/dev/kvm", "/dev/none", "/dev/kvm"},
		{"no DERP step", "Run the M5 DERP harness", "Run M5 DERP", `no step named "Run the M5 DERP harness"`},
		{"wrong test", "'^TestM5DERP$'", "'^TestM5Other$'", "TestM5DERP"},
		{"no flag", " -m5d\n", "\n", "-m5d"},
		{"no derper binary", "-derper ", "-unknown ", "-derper"},
		{"binary not the pin", "derper-$(cat release/derper.version)-linux", "derper-v0.0.0-linux", "derper-$(cat release/derper.version)"},
		{"no fleet builds", "          -m2 \"$GITHUB_WORKSPACE/bin/harness\"\n", "", "-m2"},
		{"no commit", "          -commit \"$GITHUB_SHA\"\n", "", "-commit"},
		{"no image cache", "          -cache \"$HOME/.cache/darkpanel-harness\"\n", "", "-cache"},
		{"no workdir", "          -workdir \"$RUNNER_TEMP/harness-m5d\"\n", "", "-workdir"},
		{"workdir of vm-m5", "-workdir \"$RUNNER_TEMP/harness-m5d\"", "-workdir \"$RUNNER_TEMP/harness-m5\"", "harness-m5d"},
		{"short timeout", "-timeout 40m -v -run", "-timeout 30m -v -run", "-timeout 30m"},
		{"long timeout", "-timeout 40m -v -run", "-timeout 50m -v -run", "-timeout 50m"},
		{"no failure logs", "          path: ${{ runner.temp }}/harness-m5d/**/*.log\n", "", "harness-m5d/**/*.log"},
		{"artifact name of vm-m5", "name: logs-m5d-${{ matrix.os }}", "name: logs-m5-${{ matrix.os }}", "logs-m5d-"},
		{"test added to vm-m5", "-run '^TestM5Node$'", "-run '^TestM5Node$|^TestM5DERP$'", "vm-m5 job holds TestM5DERP"},
	} {
		if !strings.Contains(m5dwWorkflowDoc, tc.from) {
			t.Fatalf("%s: the fixture lacks %q", tc.name, tc.from)
		}
		doc := strings.ReplaceAll(m5dwWorkflowDoc, tc.from, tc.to)
		if err := m5dwWorkflow(doc); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.want)
		}
	}

	moved := strings.Replace(m5dwWorkflowDoc, m5dwDerperSteps, "", 1)
	moved = strings.Replace(moved, m5dwUploadStep, m5dwUploadStep+m5dwDerperSteps, 1)
	if err := m5dwWorkflow(moved); err == nil || !strings.Contains(err.Error(), "before it uploads path: bin/harness") {
		t.Errorf("derper steps after the upload: %v, want an error naming the upload", err)
	}
}

// TestM5dwUnitWorkflowFile checks the committed workflow, which is the only
// place TestM5DERP runs.
func TestM5dwUnitWorkflowFile(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/harness.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := m5dwWorkflow(string(b)); err != nil {
		t.Error(err)
	}
}
