//go:build harness

package harness

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

func TestM5lUnitCommand(t *testing.T) {
	got := m5lCommand("/var/tmp/tslocal.test", "/var/run/tailscale/tailscaled.sock")
	want := "sudo env DARKPANEL_TAILSCALED_SOCKET=/var/run/tailscale/tailscaled.sock /var/tmp/tslocal.test" +
		" -test.run '^TestTailscaledLive$' -test.v -test.timeout 5m 2>&1"
	if got != want {
		t.Errorf("m5lCommand =\n%s\nwant\n%s", got, want)
	}
	got = m5lCommand("/var/tmp/my test", "/run/a b.sock")
	want = "sudo env DARKPANEL_TAILSCALED_SOCKET='/run/a b.sock' '/var/tmp/my test'" +
		" -test.run '^TestTailscaledLive$' -test.v -test.timeout 5m 2>&1"
	if got != want {
		t.Errorf("m5lCommand with spaces =\n%s\nwant\n%s", got, want)
	}
	if m5lSocket != tslocal.DefaultSocket {
		t.Errorf("m5lSocket = %q, want tailscaled's socket %q", m5lSocket, tslocal.DefaultSocket)
	}
}

func TestM5lUnitVerdict(t *testing.T) {
	const pass = "=== RUN   TestTailscaledLive\n--- PASS: TestTailscaledLive (2.34s)\nPASS\n"
	cases := []struct {
		name, out string
		want      string // "" when accepted, else a substring of the error
	}{
		{"pass", pass, ""},
		{"pass with logs", "    live_test.go:1: status\n" + pass, ""},
		{"skipped, which the binary still reports as PASS", "=== RUN   TestTailscaledLive\n    live_test.go:20: set DARKPANEL_TAILSCALED_SOCKET to drive a real tailscaled\n--- SKIP: TestTailscaledLive (0.00s)\nPASS\n", "--- SKIP"},
		{"failed", "=== RUN   TestTailscaledLive\n    live_test.go:90: boom\n--- FAIL: TestTailscaledLive (1.00s)\nFAIL\n", "--- FAIL"},
		{"failed without the summary line", "FAIL\n", "FAIL"},
		{"panic", "panic: runtime error\n", "panic:"},
		{"empty", "", "no --- PASS"},
		{"another test passed", "--- PASS: TestOther (0.00s)\nPASS\n", "no --- PASS"},
		{"the docker test passed", "--- PASS: TestDockerLive (1.00s)\nPASS\n", "no --- PASS"},
	}
	for _, c := range cases {
		err := m5lVerdict(c.out)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "TestTailscaledLive")):
			t.Errorf("%s: %v, want an error naming TestTailscaledLive and containing %q", c.name, err, c.want)
		}
	}
}

func TestM5lUnitStep(t *testing.T) {
	steps := m5pSteps(context.Background(), nil)
	if len(steps) != 4 {
		t.Fatalf("m5pSteps has %d steps, want install, package-install, pin-rollback, tailscaled-live", len(steps))
	}
	for i, want := range []struct {
		name string
		gate bool
	}{{"install", true}, {"package-install", true}, {"pin-rollback", false}, {"tailscaled-live", false}} {
		if steps[i].name != want.name || steps[i].gate != want.gate || steps[i].run == nil {
			t.Errorf("step %d = %q (gate %t, run set %t), want %q (gate %t)", i, steps[i].name, steps[i].gate, steps[i].run != nil, want.name, want.gate)
		}
	}
}

// m5lWorkflowDoc has the parts of harness.yml that m5lWorkflow reads.
const m5lWorkflowDoc = `jobs:
  build:
    runs-on: ubuntu-24.04
    steps:
      - run: make harness-builds
      - name: Build the dockerapi live test
        run: CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o bin/harness/dockerapi.test ./internal/platform/dockerapi
      - name: Build the tslocal live test
        run: CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o bin/harness/tslocal.test ./internal/platform/tslocal
      - uses: actions/upload-artifact@x
        with:
          path: bin/harness
  vm:
    needs: build
    timeout-minutes: 280
    steps:
      - name: Run the harness
        run: >-
          go test -tags harness -timeout 100m -v ./test/harness/
  vm-m5:
    needs: build
    runs-on: ubuntu-24.04
    timeout-minutes: 100
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
      - name: Run the M5 node harness
        run: >-
          go test -tags harness -timeout 70m -v -run '^TestM5Node$' ./test/harness/
          -args -os ${{ matrix.os }} -m5
          -m2 "$GITHUB_WORKSPACE/bin/harness"
          -workdir "$RUNNER_TEMP/harness-m5"
      - if: failure()
        uses: actions/upload-artifact@x
        with:
          name: logs-m5-${{ matrix.os }}
          path: ${{ runner.temp }}/harness-m5/**/*.log
  vm-m5p:
    needs: build
    runs-on: ubuntu-24.04
    timeout-minutes: 75
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
      - name: Run the M5 pin harness
        run: >-
          go test -tags harness -timeout 45m -v -run '^TestM5Pin$' ./test/harness/
          -args -os ${{ matrix.os }} -m5
          -m2 "$GITHUB_WORKSPACE/bin/harness"
          -tslocal-test "$GITHUB_WORKSPACE/bin/harness/tslocal.test"
          -workdir "$RUNNER_TEMP/harness-m5p"
      - if: failure()
        uses: actions/upload-artifact@x
        with:
          name: logs-m5p-${{ matrix.os }}
          path: ${{ runner.temp }}/harness-m5p/**/*.log
`

func TestM5lUnitWorkflow(t *testing.T) {
	if err := m5lWorkflow(m5lWorkflowDoc); err != nil {
		t.Fatalf("the fixture: %v", err)
	}
	const buildLine = "CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o bin/harness/tslocal.test ./internal/platform/tslocal"
	const buildStep = "      - name: Build the tslocal live test\n        run: " + buildLine + "\n"
	for name, tc := range map[string]struct{ from, to, want string }{
		"no build step":      {"Build the tslocal live test", "Build something", "Build the tslocal live test"},
		"dynamic binary":     {buildLine, strings.Replace(buildLine, "CGO_ENABLED=0 ", "", 1), "CGO_ENABLED=0"},
		"wrong architecture": {buildLine, strings.Replace(buildLine, "GOARCH=amd64", "GOARCH=arm64", 1), "GOARCH=amd64"},
		"wrong output":       {"-o bin/harness/tslocal.test", "-o bin/tslocal.test", "-o bin/harness/tslocal.test"},
		"wrong package":      {"./internal/platform/tslocal", "./internal/platform/other", "./internal/platform/tslocal"},
		"built after upload": {buildStep, "", "Build the tslocal live test"},
		"M5 left in vm":      {"go test -tags harness -timeout 100m -v ./test/harness/", "go test -tags harness -timeout 100m -v -run '^TestM5Pin$' ./test/harness/", "vm job"},
		"no vm-m5 job":       {"  vm-m5:\n", "  vm-other:\n", "no vm-m5 job"},
		"no needs":           {"  vm-m5:\n    needs: build\n", "  vm-m5:\n", "needs: build"},
		"fail-fast":          {"fail-fast: false", "fail-fast: true", "fail-fast: false"},
		"no matrix":          {"debian-13]", "debian-12]", "os: ["},
		"no KVM":             {"/dev/kvm", "/dev/null", "/dev/kvm"},
		"no fleet download":  {"name: darkpanel-harness\n", "name: other\n", "name: darkpanel-harness"},
		"no vet":             {"      - run: go vet -tags harness ./test/...\n", "", "go vet -tags harness"},
		"no run step":        {"Run the M5 node harness", "Run M5", "Run the M5 node harness"},
		"wrong node test":    {"'^TestM5Node$'", "'^TestM5Other$'", "TestM5Node"},
		"no flag":            {" -m5\n", "\n", "-m5"},
		"no fleet builds":    {"          -m2 \"$GITHUB_WORKSPACE/bin/harness\"\n", "", "-m2"},
		"no workdir":         {"          -workdir \"$RUNNER_TEMP/harness-m5\"\n", "", "-workdir"},
		"short timeout":      {"-timeout 70m -v -run", "-timeout 40m -v -run", "-timeout 40m"},
		"timeout over limit": {"-timeout 70m -v -run", "-timeout 80m -v -run", "-timeout 80m"},
		"no logs":            {"${{ runner.temp }}/harness-m5/**/*.log", "", "harness-m5/**/*.log"},
		"wrong artifact":     {"name: logs-m5-", "name: logs-", "logs-m5-"},
		"limit too small":    {"timeout-minutes: 100", "timeout-minutes: 90", "timeout-minutes 90"},
		"limit too large":    {"timeout-minutes: 100", "timeout-minutes: 130", "timeout-minutes 130"},
		"no vm-m5p job":      {"  vm-m5p:\n", "  vm-other:\n", "no vm-m5p job"},
		"no pin needs":       {"  vm-m5p:\n    needs: build\n", "  vm-m5p:\n", "needs: build"},
		"no pin step":        {"Run the M5 pin harness", "Run M5 pin", "Run the M5 pin harness"},
		"wrong pin test":     {"'^TestM5Pin$'", "'^TestM5Other$'", "TestM5Pin"},
		"no binary":          {"-tslocal-test", "-unknown", "-tslocal-test"},
		"no pin workdir":     {"          -workdir \"$RUNNER_TEMP/harness-m5p\"\n", "", "harness-m5p"},
		"pin short timeout":  {"-timeout 45m -v -run", "-timeout 40m -v -run", "-timeout 40m"},
		"pin timeout high":   {"-timeout 45m -v -run", "-timeout 50m -v -run", "-timeout 50m"},
		"no pin logs":        {"${{ runner.temp }}/harness-m5p/**/*.log", "", "harness-m5p/**/*.log"},
		"wrong pin artifact": {"name: logs-m5p-", "name: logs-", "logs-m5p-"},
		"pin limit small":    {"timeout-minutes: 75", "timeout-minutes: 70", "timeout-minutes 70"},
		"pin limit large":    {"timeout-minutes: 75", "timeout-minutes: 130", "timeout-minutes 130"},
	} {
		if !strings.Contains(m5lWorkflowDoc, tc.from) {
			t.Fatalf("%s: the fixture lacks %q", name, tc.from)
		}
		doc := strings.Replace(m5lWorkflowDoc, tc.from, tc.to, 1)
		if name == "built after upload" {
			doc = strings.Replace(doc, "  vm:\n", buildStep+"  vm:\n", 1)
		}
		if err := m5lWorkflow(doc); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", name, err, tc.want)
		}
	}
}

// TestM5lUnitWorkflowFile checks the committed workflow, which is the only
// place TestM5Pin and the live test run.
func TestM5lUnitWorkflowFile(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/harness.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := m5lWorkflow(string(b)); err != nil {
		t.Error(err)
	}
}
