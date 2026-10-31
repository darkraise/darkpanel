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
)

var dockerapiTestFlag = flag.String("dockerapi-test", "", "path of the compiled dockerapi test binary")

// m4lImage is the image TestDockerLive creates containers from. Its command
// runs sh, which the pinned pebble image lacks, so this is Docker's official
// Alpine 3.14.0 as the containerd project mirrors it on ghcr.io, pinned by the
// digest of its multi-architecture index. ghcr.io is used because the harness
// already pulls the pebble images from it, while anonymous pulls from
// public.ecr.aws can exceed that registry's data quota.
const m4lImage = "ghcr.io/containerd/alpine@sha256:adab3844f497ab9171f070d4cae4114b5aec565ac772e2f2579405b78be67c96"

// m4lRemote is where the dockerapi test binary is copied on the agent.
const m4lRemote = "/var/tmp/dockerapi.test"

// m4lCommand is the command line that runs TestDockerLive from binary on a
// VM, against image.
func m4lCommand(binary, image string) string {
	return "sudo env DARKPANEL_DOCKER_LIVE=1 DARKPANEL_DOCKER_LIVE_IMAGE=" + m3dxShell(image) + " " + m3dxShell(binary) +
		" -test.run '^TestDockerLive$' -test.v -test.timeout 5m 2>&1"
}

// m4lVerdict reads the -test.v output of TestDockerLive. A skipped test
// still ends with PASS, so only its --- PASS line counts.
func m4lVerdict(out string) error {
	passed := false
	for _, line := range strings.Split(out, "\n") {
		for _, bad := range []string{"--- SKIP", "--- FAIL", "FAIL", "panic:"} {
			if strings.HasPrefix(line, bad) {
				return fmt.Errorf("harness: TestDockerLive did not pass: %q\n%s", line, out)
			}
		}
		if strings.HasPrefix(line, "--- PASS: TestDockerLive") {
			passed = true
		}
	}
	if !passed {
		return fmt.Errorf("harness: the output has no --- PASS: TestDockerLive line\n%s", out)
	}
	return nil
}

// m4lServerVersion is the Engine version of a pinned Debian package version:
// without the epoch and the Debian revision.
func m4lServerVersion(pkgVersion string) (string, error) {
	v := pkgVersion
	if _, after, ok := strings.Cut(v, ":"); ok {
		v = after
	}
	v, _, _ = strings.Cut(v, "-")
	if v == "" {
		return "", fmt.Errorf("harness: package version %q has no upstream version", pkgVersion)
	}
	return v, nil
}

// m4lLive runs TestDockerLive on the agent against its pinned Docker Engine.
func m4lLive(ctx context.Context, t *testing.T, f *fleet) {
	if *dockerapiTestFlag == "" {
		t.Fatal("harness: -dockerapi-test is required: the path of the dockerapi test binary the workflow builds")
	}
	want := ""
	for _, p := range f.os.Packages {
		if p.Name == "docker-ce" {
			v, err := m4lServerVersion(p.Version)
			if err != nil {
				t.Fatal(err)
			}
			want = v
		}
	}
	if want == "" {
		t.Fatalf("harness: %s pins no docker-ce package", f.os.ID)
	}
	if got := strings.TrimSpace(m3Run(ctx, t, f.agent, "sudo docker version --format '{{.Server.Version}}'")); got != want {
		t.Fatalf("harness: the agent runs Docker Engine %q, want the pinned %q", got, want)
	}
	if err := f.agent.CopyFile(ctx, *dockerapiTestFlag, m4lRemote, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = f.agent.Run(ctx, "sudo rm -f "+m3dxShell(m4lRemote))
	})
	out, err := f.agent.Run(ctx, m4lCommand(m4lRemote, m4lImage))
	t.Logf("TestDockerLive on the agent:\n%s", out)
	if err != nil {
		t.Fatalf("harness: TestDockerLive: %v", err)
	}
	if err := m4lVerdict(out); err != nil {
		t.Fatal(err)
	}
}

const (
	m4lBuildStep = "Build the dockerapi live test"
	m4lRunStep   = "Run the M4 Docker harness"
	m4lJob       = "vm-m4"
	// m4lJobMax is the longest a harness job may run: a job past 120
	// minutes is split per milestone (spec, Parallel decomposition).
	m4lJobMax = 120
)

// m4lSetup are the vm job's setup steps, which the vm-m4 job repeats.
var m4lSetup = []string{
	"/dev/kvm",
	"qemu-system-x86",
	"actions/checkout@",
	"actions/setup-go@",
	"name: darkpanel-linux-amd64",
	"name: darkpanel-harness",
	"actions/cache@",
	"go vet -tags harness ./test/...",
}

// m4lStep returns the step of job whose name line is name.
func m4lStep(job, name string) (string, bool) {
	for _, s := range m3dxExpoSteps(job) {
		for _, line := range strings.Split(s, "\n") {
			if strings.TrimPrefix(strings.TrimSpace(line), "- ") == "name: "+name {
				return s, true
			}
		}
	}
	return "", false
}

// m4lWorkflow checks the text of harness.yml: the build job compiles the
// dockerapi test binary into the uploaded bin/harness, the vm job leaves M4
// alone, and the vm-m4 job runs TestM4 with the vm job's setup, a -timeout
// that covers deadline, its failure logs, and a limit within m4lJobMax.
func m4lWorkflow(yml string, deadline time.Duration) error {
	build, ok := m3dxExpoJob(yml, "build")
	if !ok {
		return errors.New("harness: the workflow has no build job")
	}
	step, ok := m4lStep(build, m4lBuildStep)
	if !ok {
		return fmt.Errorf("harness: the build job has no step named %q", m4lBuildStep)
	}
	for _, want := range []string{"CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64", "go test -c", "-o bin/harness/dockerapi.test", "./internal/platform/dockerapi"} {
		if !strings.Contains(step, want) {
			return fmt.Errorf("harness: the step %q lacks %s", m4lBuildStep, want)
		}
	}
	upload := strings.Index(build, "path: bin/harness")
	if upload < 0 || strings.Index(build, strings.TrimRight(step, "\n")) > upload {
		return fmt.Errorf("harness: the build job does not run the step %q before it uploads path: bin/harness", m4lBuildStep)
	}

	vm, ok := m3dxExpoJob(yml, "vm")
	if !ok {
		return errors.New("harness: the workflow has no vm job")
	}
	if strings.Contains(vm, "TestM4") {
		return fmt.Errorf("harness: the vm job runs TestM4, which belongs to the %s job", m4lJob)
	}
	if strings.Contains(vm, "harness-m4") {
		return fmt.Errorf("harness: the vm job collects harness-m4 logs, which belong to the %s job", m4lJob)
	}

	job, ok := m3dxExpoJob(yml, m4lJob)
	if !ok {
		return fmt.Errorf("harness: the workflow has no %s job", m4lJob)
	}
	musts := []string{"needs: build", "runs-on: ubuntu-24.04", "fail-fast: false", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]"}
	for _, want := range append(musts, m4lSetup...) {
		if !strings.Contains(job, want) {
			return fmt.Errorf("harness: the %s job lacks %s", m4lJob, want)
		}
	}

	run, ok := m4lStep(job, m4lRunStep)
	if !ok {
		return fmt.Errorf("harness: the %s job has no step named %q", m4lJob, m4lRunStep)
	}
	for _, want := range []string{"-run '^TestM4$'", " -m4", "-os ${{ matrix.os }}", `-workdir "$RUNNER_TEMP/harness-m4"`, `-dockerapi-test "$GITHUB_WORKSPACE/bin/harness/dockerapi.test"`} {
		if !strings.Contains(run, want) {
			return fmt.Errorf("harness: the step %q lacks %s", m4lRunStep, want)
		}
	}
	m := m3dxExpoTimeoutRE.FindStringSubmatch(run)
	if m == nil {
		return fmt.Errorf("harness: the step %q has no -timeout <n>m, which must cover the deadline", m4lRunStep)
	}
	n, _ := strconv.Atoi(m[1])
	if time.Duration(n)*time.Minute < deadline+10*time.Minute {
		return fmt.Errorf("harness: the step %q has -timeout %sm, under the deadline %s plus 10 minutes", m4lRunStep, m[1], deadline)
	}

	for _, want := range []string{"name: logs-m4-${{ matrix.os }}", "harness-m4/**/*.log"} {
		if !strings.Contains(job, want) {
			return fmt.Errorf("harness: the %s job's failure upload lacks %s", m4lJob, want)
		}
	}

	limit := m3dxExpoLimitRE.FindStringSubmatch(job)
	if limit == nil {
		return fmt.Errorf("harness: the %s job has no timeout-minutes", m4lJob)
	}
	if have, _ := strconv.Atoi(limit[1]); have < n+30 || have > m4lJobMax {
		return fmt.Errorf("harness: the %s job has timeout-minutes %d, want between %d (the step's -timeout plus 30 minutes of setup) and %d", m4lJob, have, n+30, m4lJobMax)
	}
	return nil
}
