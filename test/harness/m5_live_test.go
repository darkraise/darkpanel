//go:build harness

package harness

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/platform/tslocal"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

var m5lBinaryFlag = flag.String("tslocal-test", "", "path of the compiled tslocal test binary")

const (
	// m5lSocket is the local API socket of the tailscaled the pin scenario
	// leaves running on each VM.
	m5lSocket = tslocal.DefaultSocket
	// m5lRemote is where the tslocal test binary is copied on a VM.
	m5lRemote = "/var/tmp/tslocal.test"
)

// m5lCommand is the command line that runs TestTailscaledLive from binary on
// a VM, against the tailscaled socket.
func m5lCommand(binary, socket string) string {
	return "sudo env DARKPANEL_TAILSCALED_SOCKET=" + m3dxShell(socket) + " " + m3dxShell(binary) +
		" -test.run '^TestTailscaledLive$' -test.v -test.timeout 5m 2>&1"
}

// m5lVerdict reads the -test.v output of TestTailscaledLive. A skipped test
// still ends with PASS, so only its --- PASS line counts.
func m5lVerdict(out string) error {
	passed := false
	for _, line := range strings.Split(out, "\n") {
		for _, bad := range []string{"--- SKIP", "--- FAIL", "FAIL", "panic:"} {
			if strings.HasPrefix(line, bad) {
				return fmt.Errorf("harness: TestTailscaledLive did not pass: %q\n%s", line, out)
			}
		}
		if strings.HasPrefix(line, "--- PASS: TestTailscaledLive") {
			passed = true
		}
	}
	if !passed {
		return fmt.Errorf("harness: the output has no --- PASS: TestTailscaledLive line\n%s", out)
	}
	return nil
}

// m5lLive runs TestTailscaledLive on the controller and on the agent, each
// against the tailscaled it runs.
func m5lLive(ctx context.Context, t *testing.T, f *fleet) {
	if *m5lBinaryFlag == "" {
		t.Fatal("harness: -tslocal-test is required: the path of the tslocal test binary the workflow builds")
	}
	for _, h := range []struct {
		name string
		vm   *vm.VM
	}{{"controller", f.controller}, {"agent", f.agent}} {
		t.Run(h.name, func(t *testing.T) {
			if got := unitState(ctx, h.vm, "tailscaled"); got != "active" {
				t.Fatalf("harness: tailscaled is %q on the %s, want active", got, h.name)
			}
			if err := h.vm.CopyFile(ctx, *m5lBinaryFlag, m5lRemote, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				_, _ = h.vm.Run(ctx, "sudo rm -f "+m3dxShell(m5lRemote))
			})
			out, err := h.vm.Run(ctx, m5lCommand(m5lRemote, m5lSocket))
			t.Logf("TestTailscaledLive on the %s:\n%s", h.name, out)
			if err != nil {
				t.Fatalf("harness: TestTailscaledLive: %v", err)
			}
			if err := m5lVerdict(out); err != nil {
				t.Fatal(err)
			}
		})
	}
}

const (
	m5lBuildStep = "Build the tslocal live test"
	m5lRunStep   = "Run the M5 node harness"
	m5lPinStep   = "Run the M5 pin harness"
	m5lJob       = "vm-m5"
	m5lPinJob    = "vm-m5p"
	// m5lJobLimit and m5lPinJobLimit are each job's step -timeout plus
	// m5lSetupMinutes, and stay within m4lJobMax.
	m5lJobLimit     = 100
	m5lPinJobLimit  = 75
	m5lSetupMinutes = 30
)

// m5lWorkflow checks the text of harness.yml: the build job compiles the
// tslocal test binary into the uploaded bin/harness, the vm job leaves M5
// alone, and the vm-m5 and vm-m5p jobs each run their test with the vm job's
// setup, a -timeout that covers its deadline, its failure logs, and a limit
// within m4lJobMax.
func m5lWorkflow(yml string) error {
	build, ok := m3dxExpoJob(yml, "build")
	if !ok {
		return errors.New("harness: the workflow has no build job")
	}
	step, ok := m4lStep(build, m5lBuildStep)
	if !ok {
		return fmt.Errorf("harness: the build job has no step named %q", m5lBuildStep)
	}
	for _, want := range []string{"CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64", "go test -c", "-o bin/harness/tslocal.test", "./internal/platform/tslocal"} {
		if !strings.Contains(step, want) {
			return fmt.Errorf("harness: the step %q lacks %s", m5lBuildStep, want)
		}
	}
	upload, at := strings.Index(build, "path: bin/harness"), strings.Index(build, strings.TrimRight(step, "\n"))
	if upload < 0 || at < 0 || at > upload {
		return fmt.Errorf("harness: the build job does not run the step %q before it uploads path: bin/harness", m5lBuildStep)
	}

	vmJob, ok := m3dxExpoJob(yml, "vm")
	if !ok {
		return errors.New("harness: the workflow has no vm job")
	}
	for _, text := range []string{"TestM5", "harness-m5"} {
		if strings.Contains(vmJob, text) {
			return fmt.Errorf("harness: the vm job holds %s, which belongs to the %s or %s job", text, m5lJob, m5lPinJob)
		}
	}

	if err := m5lJobCheck(yml, m5lJobSpec{
		job: m5lJob, step: m5lRunStep, limit: m5lJobLimit, deadline: m5Deadline,
		run:  []string{"-run '^TestM5Node$'", " -m5", "-os ${{ matrix.os }}", `-m2 "$GITHUB_WORKSPACE/bin/harness"`, `-workdir "$RUNNER_TEMP/harness-m5"`},
		logs: []string{"name: logs-m5-${{ matrix.os }}", "harness-m5/**/*.log"},
	}); err != nil {
		return err
	}
	return m5lJobCheck(yml, m5lJobSpec{
		job: m5lPinJob, step: m5lPinStep, limit: m5lPinJobLimit, deadline: m5pDeadline,
		run:  []string{"-run '^TestM5Pin$'", " -m5", "-os ${{ matrix.os }}", `-m2 "$GITHUB_WORKSPACE/bin/harness"`, `-workdir "$RUNNER_TEMP/harness-m5p"`, `-tslocal-test "$GITHUB_WORKSPACE/bin/harness/tslocal.test"`},
		logs: []string{"name: logs-m5p-${{ matrix.os }}", "harness-m5p/**/*.log"},
	})
}

// m5lJobSpec is what m5lJobCheck requires of one M5 job.
type m5lJobSpec struct {
	job, step string
	limit     int
	deadline  time.Duration
	run, logs []string // texts the run step and the failure upload hold
}

func m5lJobCheck(yml string, s m5lJobSpec) error {
	job, ok := m3dxExpoJob(yml, s.job)
	if !ok {
		return fmt.Errorf("harness: the workflow has no %s job", s.job)
	}
	musts := []string{"needs: build", "runs-on: ubuntu-24.04", "fail-fast: false", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]"}
	for _, want := range append(musts, m4lSetup...) {
		if !strings.Contains(job, want) {
			return fmt.Errorf("harness: the %s job lacks %s", s.job, want)
		}
	}

	run, ok := m4lStep(job, s.step)
	if !ok {
		return fmt.Errorf("harness: the %s job has no step named %q", s.job, s.step)
	}
	for _, want := range s.run {
		if !strings.Contains(run, want) {
			return fmt.Errorf("harness: the step %q lacks %s", s.step, want)
		}
	}
	m := m3dxExpoTimeoutRE.FindStringSubmatch(run)
	if m == nil {
		return fmt.Errorf("harness: the step %q has no -timeout <n>m, which must cover its deadline", s.step)
	}
	n, _ := strconv.Atoi(m[1])
	if time.Duration(n)*time.Minute < s.deadline+10*time.Minute {
		return fmt.Errorf("harness: the step %q has -timeout %sm, under its deadline %s plus 10 minutes", s.step, m[1], s.deadline)
	}
	if top := s.limit - m5lSetupMinutes; n > top {
		return fmt.Errorf("harness: the step %q has -timeout %sm, over %dm (the job's limit less its setup)", s.step, m[1], top)
	}

	for _, want := range s.logs {
		if !strings.Contains(job, want) {
			return fmt.Errorf("harness: the %s job's failure upload lacks %s", s.job, want)
		}
	}

	limit := m3dxExpoLimitRE.FindStringSubmatch(job)
	if limit == nil {
		return fmt.Errorf("harness: the %s job has no timeout-minutes", s.job)
	}
	if have, _ := strconv.Atoi(limit[1]); have != s.limit {
		return fmt.Errorf("harness: the %s job has timeout-minutes %d, want %d", s.job, have, s.limit)
	}
	return nil
}
