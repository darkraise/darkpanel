//go:build harness

package harness

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestM4lCommand(t *testing.T) {
	got := m4lCommand("/var/tmp/dockerapi.test", "registry.example/alpine@sha256:"+strings.Repeat("a", 64))
	want := "sudo env DARKPANEL_DOCKER_LIVE=1 DARKPANEL_DOCKER_LIVE_IMAGE=registry.example/alpine@sha256:" + strings.Repeat("a", 64) +
		" /var/tmp/dockerapi.test -test.run '^TestDockerLive$' -test.v -test.timeout 5m 2>&1"
	if got != want {
		t.Errorf("m4lCommand =\n%s\nwant\n%s", got, want)
	}
	if !regexp.MustCompile(`^[a-z0-9.-]+(/[a-z0-9._-]+)+@sha256:[0-9a-f]{64}$`).MatchString(m4lImage) {
		t.Errorf("m4lImage = %q, want a registry reference pinned by digest alone", m4lImage)
	}
}

func TestM4lVerdict(t *testing.T) {
	const pass = "=== RUN   TestDockerLive\n--- PASS: TestDockerLive (12.34s)\nPASS\n"
	cases := []struct {
		name, out string
		want      string // "" when accepted, else a substring of the error
	}{
		{"pass", pass, ""},
		{"pass with logs", "    live_test.go:1: pulled\n" + pass, ""},
		{"skipped, which the binary still reports as PASS", "=== RUN   TestDockerLive\n    live_test.go:24: set DARKPANEL_DOCKER_LIVE=1\n--- SKIP: TestDockerLive (0.00s)\nPASS\n", "--- SKIP"},
		{"failed", "=== RUN   TestDockerLive\n    live_test.go:90: boom\n--- FAIL: TestDockerLive (1.00s)\nFAIL\n", "--- FAIL"},
		{"failed without the summary line", "FAIL\n", "FAIL"},
		{"panic", "panic: runtime error\n", "panic:"},
		{"empty", "", "no --- PASS"},
		{"another test passed", "--- PASS: TestOther (0.00s)\nPASS\n", "no --- PASS"},
	}
	for _, c := range cases {
		err := m4lVerdict(c.out)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: %v, want an error containing %q", c.name, err, c.want)
		}
	}
}

func TestM4lServerVersion(t *testing.T) {
	for in, want := range map[string]string{
		"5:29.9.1-1~ubuntu.22.04~jammy": "29.9.1",
		"5:29.9.1-1~debian.12~bookworm": "29.9.1",
		"29.9.1-1":                      "29.9.1",
		"29.9.1":                        "29.9.1",
	} {
		if got, err := m4lServerVersion(in); err != nil || got != want {
			t.Errorf("m4lServerVersion(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "5:", "5:-1~x"} {
		if got, err := m4lServerVersion(in); err == nil {
			t.Errorf("m4lServerVersion(%q) = %q, want an error", in, got)
		}
	}
	pins, err := LoadPins(PinsFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range pins.OS {
		for _, p := range o.Packages {
			if p.Name != "docker-ce" {
				continue
			}
			if got, err := m4lServerVersion(p.Version); err != nil || !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(got) {
				t.Errorf("%s pins docker-ce %q, which gives the server version %q, %v", o.ID, p.Version, got, err)
			}
		}
	}
}

// m4lWorkflowDoc has the parts of harness.yml that m4lWorkflow reads.
const m4lWorkflowDoc = `jobs:
  build:
    runs-on: ubuntu-24.04
    steps:
      - run: make harness-builds
      - name: Build the dockerapi live test
        run: CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o bin/harness/dockerapi.test ./internal/platform/dockerapi
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
  vm-m4:
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
      - name: Run the M4 Docker harness
        run: >-
          go test -tags harness -timeout 70m -v -run '^TestM4$' ./test/harness/
          -args -os ${{ matrix.os }} -m4
          -dockerapi-test "$GITHUB_WORKSPACE/bin/harness/dockerapi.test"
          -workdir "$RUNNER_TEMP/harness-m4"
      - if: failure()
        uses: actions/upload-artifact@x
        with:
          name: logs-m4-${{ matrix.os }}
          path: ${{ runner.temp }}/harness-m4/**/*.log
`

func TestM4lWorkflow(t *testing.T) {
	if err := m4lWorkflow(m4lWorkflowDoc, 60*time.Minute); err != nil {
		t.Fatalf("the fixture: %v", err)
	}
	const buildStep = "      - name: Build the dockerapi live test\n        run: CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o bin/harness/dockerapi.test ./internal/platform/dockerapi\n"
	for name, tc := range map[string]struct{ from, to, want string }{
		"no build step":      {"Build the dockerapi live test", "Build something", "Build the dockerapi live test"},
		"dynamic binary":     {"CGO_ENABLED=0 ", "", "CGO_ENABLED=0"},
		"wrong output":       {"-o bin/harness/dockerapi.test", "-o bin/dockerapi.test", "-o bin/harness/dockerapi.test"},
		"built after upload": {buildStep, "", "Build the dockerapi live test"},
		"M4 left in vm":      {"go test -tags harness -timeout 100m -v ./test/harness/", "go test -tags harness -timeout 100m -v -run '^TestM4$' ./test/harness/", "vm job"},
		"no vm-m4 job":       {"  vm-m4:\n", "  vm-other:\n", "no vm-m4 job"},
		"no needs":           {"  vm-m4:\n    needs: build\n", "  vm-m4:\n", "needs: build"},
		"fail-fast":          {"fail-fast: false", "fail-fast: true", "fail-fast: false"},
		"no matrix":          {"debian-13]", "debian-12]", "os: ["},
		"no KVM":             {"/dev/kvm", "/dev/null", "/dev/kvm"},
		"no fleet download":  {"name: darkpanel-harness\n", "name: other\n", "name: darkpanel-harness"},
		"no vet":             {"      - run: go vet -tags harness ./test/...\n", "", "go vet -tags harness"},
		"no run step":        {"Run the M4 Docker harness", "Run M4", "Run the M4 Docker harness"},
		"no flag":            {" -m4\n", "\n", "-m4"},
		"no binary":          {"-dockerapi-test", "-unknown", "-dockerapi-test"},
		"short timeout":      {"-timeout 70m -v -run", "-timeout 60m -v -run", "-timeout 60m"},
		"no logs":            {"${{ runner.temp }}/harness-m4/**/*.log", "", "harness-m4/**/*.log"},
		"wrong artifact":     {"name: logs-m4-", "name: logs-", "logs-m4-"},
		"limit too small":    {"timeout-minutes: 100", "timeout-minutes: 90", "timeout-minutes 90"},
		"limit too large":    {"timeout-minutes: 100", "timeout-minutes: 130", "timeout-minutes 130"},
	} {
		if !strings.Contains(m4lWorkflowDoc, tc.from) {
			t.Fatalf("%s: the fixture lacks %q", name, tc.from)
		}
		doc := strings.Replace(m4lWorkflowDoc, tc.from, tc.to, 1)
		if name == "built after upload" {
			doc = strings.Replace(doc, "  vm:\n", buildStep+"  vm:\n", 1)
		}
		if err := m4lWorkflow(doc, 60*time.Minute); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", name, err, tc.want)
		}
	}
}

// TestM4lWorkflowFile checks the committed workflow, which is the only place
// TestM4 runs.
func TestM4lWorkflowFile(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/harness.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := m4lWorkflow(string(b), 60*time.Minute); err != nil {
		t.Error(err)
	}
}
