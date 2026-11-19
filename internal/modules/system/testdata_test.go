package system

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

var fixtureReleases = []string{"ubuntu-22.04", "ubuntu-24.04", "ubuntu-26.04", "debian-12", "debian-13"}

var fixtureNames = []string{"install", "remove", "purge", "upgrade", "dist-upgrade", "dist-upgrade-phased", "fix-install", "summary-only", "lock-held", "dpkg-interrupted", "update-bad-source", "proc-version", "sudo-provider", "docker-io-policy"}

// The riskiest assumption: every release prints one action line per package
// and a summary line. A release that breaks the format stops the plan here.
func TestFixturesCoverEveryRelease(t *testing.T) {
	action := regexp.MustCompile(`(?m)^(Inst|Conf|Remv|Purg) \S+`)
	summary := regexp.MustCompile(`(?m)^\d+ upgraded, \d+ newly installed, \d+ to remove and \d+ not upgraded\.$`)
	for _, rel := range fixtureReleases {
		for _, name := range fixtureNames {
			b, err := os.ReadFile(filepath.Join("testdata", rel, name+".txt"))
			if err != nil {
				t.Fatalf("%s/%s: %v", rel, name, err)
			}
			switch name {
			case "install", "remove", "purge", "dist-upgrade":
				if !action.Match(b) || !summary.Match(b) {
					t.Errorf("%s/%s: no action line or no summary line", rel, name)
				}
			}
			if name == "purge" && !regexp.MustCompile(`(?m)^Purg `).Match(b) {
				t.Errorf("%s/purge: no Purg line", rel)
			}
		}
	}
}
