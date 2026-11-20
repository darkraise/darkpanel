package docs_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A38: README, docs/user/, docs/dev/design-language.md, ADR 0016 and 0017
// exist and the ADR checker and the README guard pass.
func TestA38_DocumentsExistAndTheCheckersPass(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"README.md", "docs/user/modules/system.md", "docs/user/modules/docker.md", "docs/dev/design-language.md",
		"docs/adr/0016-system-module.md", "docs/adr/0017-design-language.md",
	} {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil || len(strings.TrimSpace(string(b))) == 0 {
			t.Errorf("%s: missing or empty (%v)", rel, err)
		}
	}
	read := func(rel string) string {
		b, _ := os.ReadFile(filepath.Join(root, rel))
		return string(b)
	}
	if !strings.Contains(read("README.md"), "docs/user/modules/system.md") {
		t.Error("README.md does not link the System page")
	}
	if !strings.Contains(read("CONTRIBUTING.md"), "design-language.md") {
		t.Error("CONTRIBUTING.md does not link the design language standard")
	}
	if d := strings.ToLower(read("docs/user/modules/docker.md")); !strings.Contains(d, "docker.io") {
		t.Error("the Docker page does not explain replacing distro Docker by hand")
	}
	for _, c := range [][]string{
		{"bash", "scripts/check-adrs.sh", "--complete"},
		{"go", "test", "./internal/docs", "-run", "TestReadme", "-count=1"},
	} {
		cmd := exec.Command(c[0], c[1:]...) //nolint:gosec // G204: fixed repository checks
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%v: %v\n%s", c, err, out)
		}
	}
}
