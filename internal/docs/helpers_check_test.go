package docs_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDcRootIsTheRepositoryRoot(t *testing.T) {
	root := dcRoot(t)
	for _, rel := range []string{"go.mod", filepath.Join("internal", "docs")} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("dcRoot = %s, which has no %s: %v", root, rel, err)
		}
	}
}

func TestDcReadReturnsTheFile(t *testing.T) {
	if got := dcRead(t, "go.mod"); !strings.HasPrefix(got, "module github.com/darkraise/darkpanel\n") {
		t.Errorf("dcRead(go.mod) starts %.60q, want the module line", got)
	}
}

func TestDcMentionsAcceptsAFileThatNamesEveryName(t *testing.T) {
	dcMentions(t, "go.mod", "module github.com/darkraise/darkpanel", "go 1.27")
}

func TestDcMissingListsEachAbsentName(t *testing.T) {
	got := dcMissing("alpha beta", []string{"alpha", "gamma", "Beta", "delta", "gamma"})
	if want := []string{"gamma", "Beta", "delta", "gamma"}; !slices.Equal(got, want) {
		t.Errorf("dcMissing = %q, want %q: every absent name in order, matched verbatim and case-sensitively", got, want)
	}
	if got := dcMissing("alpha", nil); len(got) != 0 {
		t.Errorf("dcMissing with no names = %q, want none", got)
	}
}

func TestDcModuleNames(t *testing.T) {
	if got, want := dcModuleNames(t, "hostinfo"), []string{"hostinfo.summary"}; !slices.Equal(got, want) {
		t.Errorf("dcModuleNames(hostinfo) = %q, want %q: a resource counts", got, want)
	}
	cf := dcModuleNames(t, "cloudflare")
	if !slices.IsSorted(cf) || len(slices.Compact(slices.Clone(cf))) != len(cf) {
		t.Errorf("dcModuleNames(cloudflare) = %q, want sorted names without duplicates", cf)
	}
	if !slices.Contains(cf, "cloudflare.tunnel.list") {
		t.Errorf("dcModuleNames(cloudflare) = %q, want cloudflare.tunnel.list among them", cf)
	}
	if slices.Contains(cf, "cloudflare.tunnel.token") {
		t.Errorf("dcModuleNames(cloudflare) lists cloudflare.tunnel.token, an Internal action")
	}
	if core := dcModuleNames(t, "core"); !slices.Contains(core, "core.token.create") {
		t.Errorf("dcModuleNames(core) = %q, want core.token.create among them", core)
	}
}

func TestDcBrokenLinks(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"docs/user/a.md", "docs/user/b.md", "docs/README.md", "docs/api/openapi.json"} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	text := strings.Join([]string{
		"[ok](b.md) [up](../README.md#top) [rooted](/docs/README.md) [missing](nope.md) [dir](../api/)",
		"[web](https://example.com/x) [mail](mailto:a@example.com) [anchor](#here) [titled](b.md \"A title\")",
		"[angled](<b.md>) [fragment](gone.md#sec) [query](b.md?x=1) [rootless](/docs/gone.md)",
		"![image](pic.png) and text [not a link] (b.md)",
		"```sh",
		"[hidden](hidden.md)",
		"```",
		"[after the fence](after.md)",
	}, "\n")
	got := dcBroken(root, "docs/user/a.md", text)
	want := []string{"nope.md", "gone.md#sec", "/docs/gone.md", "pic.png", "after.md"}
	if !slices.Equal(got, want) {
		t.Errorf("dcBroken = %q, want %q", got, want)
	}
}
