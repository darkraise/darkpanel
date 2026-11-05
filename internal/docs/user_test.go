package docs_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules"
)

// duPages are the pages of docs/user this file's tests read; the README
// indexes them and the module pages.
var duPages = []string{
	"docs/user/install.md",
	"docs/user/first-admin.md",
	"docs/user/lockout-recovery.md",
	"docs/user/uninstall.md",
	"docs/user/administration.md",
}

// duIndexed are the link targets docs/user/README.md must carry, relative to
// docs/user.
var duIndexed = []string{
	"install.md", "first-admin.md", "lockout-recovery.md", "uninstall.md", "administration.md",
	"modules/hostinfo.md", "modules/firewall.md", "modules/docker.md", "modules/tailscale.md", "modules/tailnet.md",
	"modules/derp.md", "modules/npm.md", "modules/cloudflare.md", "modules/panel.md",
	"../dev/module-guide.md", "../api/README.md", "../versioning.md",
}

// duFlags returns the first submatch of every match of re in the file at rel,
// sorted and without duplicates, failing when none matches.
func duFlags(t *testing.T, rel string, re *regexp.Regexp) []string {
	t.Helper()
	var out []string
	for _, m := range re.FindAllStringSubmatch(dcRead(t, rel), -1) {
		out = append(out, m[1])
	}
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) == 0 {
		t.Fatalf("docs: no flag found in %s with %s", rel, re)
	}
	return out
}

func duDashed(flags []string) []string {
	out := make([]string, len(flags))
	for i, f := range flags {
		out[i] = "--" + f
	}
	return out
}

func TestDuIndexLinksEveryPage(t *testing.T) {
	var links []string
	for _, target := range duIndexed {
		links = append(links, "]("+target+")")
	}
	dcMentions(t, "docs/user/README.md", links...)
}

func TestDuInstall(t *testing.T) {
	// The flags install.sh accepts are its case labels in main.
	flags := duFlags(t, "install.sh.in", regexp.MustCompile(`(?m)^\t\t--([a-z][a-z-]*)\)`))
	want := []string{"agent-advertise", "agent-listen", "base-url", "role", "token-file", "version"}
	if !slices.Equal(flags, want) {
		t.Fatalf("docs: install.sh.in accepts %v, want %v; update this test and docs/user/install.md together", flags, want)
	}
	dcMentions(t, "docs/user/install.md", duDashed(flags)...)
	dcMentions(t, "docs/user/install.md",
		"install.sh", "sudo sh install.sh --role controller", "sudo sh install.sh --role agent",
		"sudo darkpanel agent enroll", "darkpanel host create", "darkpanel system install")
}

func TestDuFirstAdmin(t *testing.T) {
	dcMentions(t, "docs/user/first-admin.md",
		"sudo darkpanel user create --username", "darkpanel user activate", "sudo darkpanel user reset-totp", "TOTP", "authenticator")
}

func TestDuLockoutRecovery(t *testing.T) {
	// The commands are generated from the core action names, so the page
	// follows a rename of an action.
	var userCommands []string
	for _, verb := range []string{"core.user.list", "core.user.reset-password", "core.user.reset-totp", "core.user.delete"} {
		if !slices.Contains(dcModuleNames(t, "core"), verb) {
			t.Fatalf("docs: the registry has no action %s", verb)
		}
		userCommands = append(userCommands, "sudo darkpanel "+strings.Join(cli.CommandPath(verb), " "))
	}
	dcMentions(t, "docs/user/lockout-recovery.md", userCommands...)
	dcMentions(t, "docs/user/lockout-recovery.md",
		"sudo darkpanel update rollback --local", "sudo darkpanel update recover",
		"sudo darkpanel firewall rollback --local",
		"sudo darkpanel "+strings.Join(cli.CommandPath("firewall.snapshot.restore"), " ")+" --local")
}

func TestDuUninstall(t *testing.T) {
	// cmd/darkpanel is package main and cannot be imported, so the flags are
	// read from the source of the command that registers them.
	flags := duFlags(t, "cmd/darkpanel/uninstall.go", regexp.MustCompile(`cmd\.Flags\(\)\.[A-Za-z]+Var\(&\w+, "([a-z-]+)"`))
	want := []string{"abort", "keep-data", "module-option", "stop-tasks", "yes"}
	if !slices.Equal(flags, want) {
		t.Fatalf("docs: darkpanel uninstall registers %v, want %v; update this test and docs/user/uninstall.md together", flags, want)
	}
	dcMentions(t, "docs/user/uninstall.md", duDashed(flags)...)
	dcMentions(t, "docs/user/uninstall.md", "sudo darkpanel uninstall")

	var options []string
	for _, m := range modules.All() {
		un, ok := m.(core.Uninstaller)
		if !ok {
			continue
		}
		for _, o := range un.UninstallOptions() {
			for _, v := range o.Values {
				options = append(options, m.Manifest().ID+"."+o.Key+"="+v)
			}
		}
	}
	if len(options) == 0 {
		t.Fatal("docs: no module declares an uninstall option")
	}
	dcMentions(t, "docs/user/uninstall.md", options...)
}

func TestDuAdministration(t *testing.T) {
	dcMentions(t, "docs/user/administration.md", dcModuleNames(t, "core")...)

	// Backup, restore and the restore quarantine are hand-written commands
	// of package cli, so their flags are read from the files that add them.
	flag := regexp.MustCompile(`cmd\.Flags\(\)\.[A-Za-z]+\("([a-z0-9-]+)"`)
	backup := duFlags(t, "internal/cli/controller_backup.go", flag)
	if !slices.Contains(backup, "passphrase-file") {
		t.Fatalf("docs: controller_backup.go flags %v lack passphrase-file", backup)
	}
	dcMentions(t, "docs/user/administration.md", duDashed(backup)...)
	dcMentions(t, "docs/user/administration.md", duDashed(duFlags(t, "internal/cli/controller_quarantine.go", flag))...)
	dcMentions(t, "docs/user/administration.md",
		"darkpanel controller backup", "darkpanel controller restore", "darkpanel controller quarantine")
}

var (
	duHeadingRE = regexp.MustCompile(`^(#{1,6}) +\S`)
	duFenceRE   = regexp.MustCompile("^```([A-Za-z]*)\\s*$")
	duCodeRE    = regexp.MustCompile("`[^`]*`")
	duHTMLRE    = regexp.MustCompile(`</?[A-Za-z][^>]*>`)
	duLinkRE    = regexp.MustCompile(`\[([^\]]*)\]\(`)
	duPlanRE    = regexp.MustCompile(`(?i)\b(task [0-9]+|W9-R[0-9]+|register row|wave [0-9]+|coming soon|will be added|TODO)\b`)
)

// TestDuStyle holds the pages to the documentation style: one title first,
// headings that never skip a level, fenced blocks that are tagged, no HTML,
// links whose text says where they go, and no mention of the plan.
func TestDuStyle(t *testing.T) {
	for _, rel := range append([]string{"docs/user/README.md"}, duPages...) {
		lines := strings.Split(dcRead(t, rel), "\n")
		level, titles, inFence := 0, 0, false
		for i, line := range lines {
			at := func(msg string) { t.Errorf("docs: %s:%d: %s: %q", rel, i+1, msg, line) }
			if m := duFenceRE.FindStringSubmatch(line); m != nil || strings.HasPrefix(line, "```") {
				switch {
				case inFence:
					inFence = false
				case m == nil || !slices.Contains([]string{"sh", "json", "text"}, m[1]):
					at("a fenced block must be tagged sh, json or text")
					inFence = true
				default:
					inFence = true
				}
				continue
			}
			if inFence {
				continue
			}
			if m := duHeadingRE.FindStringSubmatch(line); m != nil {
				n := len(m[1])
				if n == 1 {
					titles++
					if i != 0 {
						at("the title must be the first line")
					}
				}
				if n > level+1 {
					at("a heading skips a level")
				}
				level = n
			}
			prose := duCodeRE.ReplaceAllString(line, "")
			if duHTMLRE.MatchString(prose) {
				at("HTML in a page")
			}
			for _, m := range duLinkRE.FindAllStringSubmatch(prose, -1) {
				if text := strings.ToLower(strings.TrimSpace(m[1])); slices.Contains([]string{"", "here", "click here", "link", "this link", "this page", "more"}, text) {
					at("link text does not say where the link goes")
				}
			}
			if duPlanRE.MatchString(prose) {
				at("a page names the plan or promises future work")
			}
		}
		if inFence {
			t.Errorf("docs: %s ends inside a fenced block", rel)
		}
		if titles != 1 {
			t.Errorf("docs: %s has %d titles, want 1", rel, titles)
		}
	}
}
