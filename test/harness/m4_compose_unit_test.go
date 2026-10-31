//go:build harness

package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestM4cShims(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(root, "var", "log", "shims.log")
	bin := filepath.Join(root, "usr", "bin")
	plug := filepath.Join(root, "usr", "libexec", "docker", "cli-plugins")
	aside := filepath.Join(root, "var", "lib", "dp-m4c-real")
	for _, d := range []string{bin, plug} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	const original = "#!/bin/sh\necho original ssh\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(original), 0o755); err != nil { //nolint:gosec // G306: a stand-in program the test runs
		t.Fatal(err)
	}
	install, remove := m4cShimInstall(root, log), m4cShimRemove(root)
	bashSyntax(t, install)
	bashSyntax(t, remove)
	script := func(s string) {
		t.Helper()
		if out, err := exec.Command("bash", "-c", s).CombinedOutput(); err != nil { //nolint:gosec // G204: the test's own script
			t.Fatalf("%v\n%s", err, out)
		}
	}
	exit := func(name string, args ...string) (int, string) {
		t.Helper()
		out, err := exec.Command(name, args...).Output() //nolint:gosec // G204: the shim the test installed
		var ee *exec.ExitError
		switch {
		case err == nil:
			return 0, string(out)
		case errors.As(err, &ee):
			return ee.ExitCode(), string(out)
		}
		t.Fatal(err)
		return 0, ""
	}

	script(install)
	// A second install keeps the first install's original.
	script(install)
	if b, err := os.ReadFile(filepath.Join(aside, "ssh")); err != nil || string(b) != original { //nolint:gosec // G304: the test's own file
		t.Fatalf("the original ssh in %s = %q, %v; want it kept once", aside, b, err)
	}
	if _, err := os.Stat(filepath.Join(aside, "git")); err == nil {
		t.Error("a shim of a program the machine does not have was kept as its original")
	}
	for _, c := range []struct {
		path string
		args []string
	}{
		{filepath.Join(bin, "git"), []string{"clone", "x y"}},
		{filepath.Join(bin, "ssh"), []string{"-V"}},
		{filepath.Join(plug, "docker-buildx"), []string{"build", "."}},
	} {
		if code, _ := exit(c.path, c.args...); code != 97 {
			t.Errorf("%s exited %d, want 97", c.path, code)
		}
	}
	code, out := exit(filepath.Join(plug, "docker-buildx"), "docker-cli-plugin-metadata")
	var meta struct{ SchemaVersion, Vendor, Version string }
	if code != 0 || json.Unmarshal([]byte(out), &meta) != nil || meta.SchemaVersion == "" || meta.Vendor == "" || meta.Version == "" {
		t.Errorf("the plugin metadata call exited %d with %q, want exit 0 and a SchemaVersion, Vendor and Version", code, out)
	}
	b, err := os.ReadFile(log) //nolint:gosec // G304: the test's own file
	if err != nil {
		t.Fatal(err)
	}
	if got, want := m4cShimRuns(string(b)), []string{"git clone x y", "ssh -V", "docker-buildx build ."}; !slices.Equal(got, want) {
		t.Errorf("logged runs %q, want %q (the metadata call is not a run)", got, want)
	}

	script(remove)
	for _, p := range []string{filepath.Join(bin, "git"), filepath.Join(plug, "docker-buildx")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s is still there after the removal", p)
		}
	}
	if b, err := os.ReadFile(filepath.Join(bin, "ssh")); err != nil || string(b) != original { //nolint:gosec // G304: the test's own file
		t.Errorf("ssh after the removal = %q, %v; want the original", b, err)
	}
}

func TestM4cNoShimRuns(t *testing.T) {
	for _, log := range []string{"", "\n", "  \n\n"} {
		if err := m4cNoShimRuns(log); err != nil {
			t.Errorf("m4cNoShimRuns(%q) = %v, want nil", log, err)
		}
	}
	err := m4cNoShimRuns("git clone x\n\nssh -V\n")
	if err == nil || !strings.Contains(err.Error(), "git clone x") || !strings.Contains(err.Error(), "ssh -V") || !strings.Contains(err.Error(), "2 ") {
		t.Errorf("m4cNoShimRuns with two runs = %v, want an error counting and quoting both", err)
	}
}

func TestM4cRefusedWith(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string // "" when accepted, else a substring of the error
	}{
		{"nil", nil, "not refused"},
		{"the code", fmt.Errorf("plan docker.compose.up: %w", &APIError{Status: 422, Code: "docker.compose_unavailable", Detail: "d"}), ""},
		{"another code", &APIError{Status: 422, Code: "docker.owned", Detail: "belongs to npm"}, "docker.owned"},
		{"no API answer", errors.New("connection refused"), "connection refused"},
	}
	for _, c := range cases {
		err := m4cRefusedWith(c.err, "docker.compose_unavailable")
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: %v, want an error containing %q", c.name, err, c.want)
		}
	}
}

func TestM4cComposeFixtures(t *testing.T) {
	if out, err := exec.Command("docker", "compose", "version").CombinedOutput(); err != nil { //nolint:gosec // G204: a fixed argument vector
		t.Skipf("no docker compose here: %v\n%s", err, out)
	}
	image, err := m3dxImage("pebble-challtestsrv")
	if err != nil {
		t.Fatal(err)
	}
	config := func(content string) (string, error) {
		dir, cfg := t.TempDir(), t.TempDir()
		file := filepath.Join(dir, "compose.yaml")
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("docker", "compose", "--project-directory", dir, "-p", "m4c-web", "-f", file, "config", "--format", "json") //nolint:gosec // G204: a fixed argument vector over the test's own file
		cmd.Env = []string{
			"PATH=" + os.Getenv("PATH"), "HOME=" + cfg, "DOCKER_CONFIG=" + cfg,
			"COMPOSE_EXPERIMENTAL_GIT_REMOTE=false", "COMPOSE_EXPERIMENTAL_OCI_REMOTE=false",
		}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	type svc struct {
		Image  string            `json:"image"`
		Build  json.RawMessage   `json:"build"`
		Labels map[string]string `json:"labels"`
	}
	var cfg struct {
		Services map[string]svc `json:"services"`
	}
	out, err := config(m4cCompose(m4cOK, image, 7))
	if err != nil || json.Unmarshal([]byte(out), &cfg) != nil || cfg.Services["web"].Image != image || cfg.Services["web"].Labels["dp-m4c.rev"] != "7" {
		t.Errorf("the ok fixture: %v, %s; want service web on %s with label dp-m4c.rev 7", err, out, image)
	}
	if out, err = config(m4cCompose(m4cGit, image, 1)); err == nil || !strings.Contains(out, "git remote resource is disabled") {
		t.Errorf("the git fixture: %v, %s; want Compose to refuse the remote include itself", err, out)
	}
	cfg.Services = nil
	out, err = config(m4cCompose(m4cBuild, image, 1))
	if err != nil || json.Unmarshal([]byte(out), &cfg) != nil || len(cfg.Services["web"].Build) == 0 || cfg.Services["web"].Image != m4cBuiltImage {
		t.Errorf("the build fixture: %v, %s; want service web with a build member and image %s", err, out, m4cBuiltImage)
	}
}
