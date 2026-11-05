package docs_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// wcRequiredPages are the pages the documentation is not complete without;
// walking the directories alone would pass when a page was never written.
var wcRequiredPages = []string{
	"docs/accessibility.md",
	"docs/api/README.md",
	"docs/dev/module-guide.md",
	"docs/user/README.md",
	"docs/user/administration.md",
	"docs/user/first-admin.md",
	"docs/user/install.md",
	"docs/user/lockout-recovery.md",
	"docs/user/uninstall.md",
	"docs/user/modules/cloudflare.md",
	"docs/user/modules/derp.md",
	"docs/user/modules/docker.md",
	"docs/user/modules/firewall.md",
	"docs/user/modules/hostinfo.md",
	"docs/user/modules/npm.md",
	"docs/user/modules/panel.md",
	"docs/user/modules/tailnet.md",
	"docs/user/modules/tailscale.md",
}

// wcPages lists every Markdown page under the documentation directories, as
// slash paths relative to the repository root, and the conformance record.
func wcPages(t *testing.T, root string) []string {
	t.Helper()
	var pages []string
	for _, dir := range []string{"docs/user", "docs/dev", "docs/api"} {
		err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(dir)), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".md") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			pages = append(pages, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			t.Fatalf("docs: walk %s: %v", dir, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "accessibility.md")); err == nil {
		pages = append(pages, "docs/accessibility.md")
	}
	return pages
}

// TestWcDocsLinks checks that every relative link of the user documentation,
// the module guide, the API reference and the conformance record resolves to
// a file in the repository, and that none of the required pages is missing.
func TestWcDocsLinks(t *testing.T) {
	pages := wcPages(t, dcRoot(t))
	for _, want := range wcRequiredPages {
		if !slices.Contains(pages, want) {
			t.Errorf("docs: the page %s does not exist", want)
		}
	}
	for _, rel := range pages {
		t.Run(rel, func(t *testing.T) { dcLinks(t, rel) })
	}
}
