package docs_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/registry"
)

// dnPage is one module page and the facts it must state, each verbatim: an error code a refusal answers with, a
// port, a path a module creates on the host, a name the page's reader types or finds.
type dnPage struct {
	module string
	path   string
	facts  []string
}

var dnPages = []dnPage{
	{"derp", "docs/user/modules/derp.md", []string{
		"derp.port_in_use", "derp.not_ready", "derp.region_taken", "derp.no_fallback", "derp.verify_failed",
		"80/tcp", "443/tcp", "3478/udp",
		"/usr/local/lib/darkpanel/derper", "/etc/systemd/system/darkpanel-derper.service", "/var/lib/darkpanel/derp", "darkpanel-derp",
		"-verify-clients", "letsencrypt", "sha256-raw:", "30 days", "omitDefaultRegions", "tailscaled", "900", "999",
	}},
	{"npm", "docs/user/modules/npm.md", []string{
		"npm.not_public", "npm.unsupported_target", "npm.reserved", "npm.setup_failed", "npm.unhealthy",
		"darkpanel-npm", "darkpanel-npm-data", "darkpanel-npm-letsencrypt", "127.0.0.1:81",
		"/var/lib/darkpanel/agent/npm-backups", "Docker 28.0", "user-defined", "--acme-email", "derp.port_in_use",
	}},
	{"cloudflare", "docs/user/modules/cloudflare.md", []string{
		"cloud.not_connected", "cloud.auth_failed", "cloud.permission_missing", "cloud.rate_limited", "cloud.conflict",
		"cloud.uncertain_outcome", "cloud.cleanup_pending", "cloudflare.reserved", "cloudflare.unsupported_target",
		"cloudflare.connector_missing", "Cloudflare Tunnel: Edit", "Zone Read", "DNS: Edit",
		"darkpanel-cloudflared-", "/var/lib/darkpanel/agent/cloudflare-tokens", "user-defined", "overwritten",
	}},
	{"panel", "docs/user/modules/panel.md", []string{
		"panel.wrong_host", "panel.totp_incomplete", "panel.already_exposed", "panel.not_exposed",
		"npm.not_public", "cloudflare.connector_missing", "darkpanel-panel", "dp-panel", "172.31.255.0/24", "9443",
		"sudo darkpanel panel listener revoke", "Panel exposed via", "Let's Encrypt",
	}},
}

// dnSections are the level-two headings every page has, in this order.
var dnSections = []string{
	"## Overview", "## Before you start", "## Use it in the web UI", "## Use it from the CLI",
	"## What it changes on the host", "## When something goes wrong",
}

var (
	dnFenceTags = []string{"sh", "json", "text"}
	// dnInternalRE finds words that name the plan, the wave or the register a page must never mention.
	dnInternalRE = regexp.MustCompile(`(?i)\b(wave [0-9]|register row|W[0-9]+-R[0-9]+|task [0-9]+|plan [0-9])`)
	// dnHTMLRE finds an HTML tag outside inline code, which dnInlineRE removes first.
	dnHTMLRE   = regexp.MustCompile(`</?[a-z][a-z0-9]*[ />]`)
	dnInlineRE = regexp.MustCompile("`[^`]*`")
)

func TestDnPagesNameEveryAction(t *testing.T) {
	for _, p := range dnPages {
		t.Run(p.module, func(t *testing.T) {
			names := dcModuleNames(t, p.module)
			if len(names) == 0 {
				t.Fatalf("module %s has no action or resource to document", p.module)
			}
			dcMentions(t, p.path, names...)
		})
	}
}

func TestDnPagesStateTheirFacts(t *testing.T) {
	for _, p := range dnPages {
		t.Run(p.module, func(t *testing.T) { dcMentions(t, p.path, p.facts...) })
	}
}

func TestDnPagesLinkOnlyToFiles(t *testing.T) {
	for _, p := range dnPages {
		t.Run(p.module, func(t *testing.T) { dcLinks(t, p.path) })
	}
}

func TestDnPagesHaveTheirSections(t *testing.T) {
	for _, p := range dnPages {
		t.Run(p.module, func(t *testing.T) {
			lines := strings.Split(dcRead(t, p.path), "\n")
			titles := 0
			next := 0
			for _, line := range lines {
				switch {
				case strings.HasPrefix(line, "# "):
					titles++
				case next < len(dnSections) && line == dnSections[next]:
					next++
				}
			}
			if titles != 1 || !strings.HasPrefix(lines[0], "# ") {
				t.Errorf("%s has %d level-one headings and starts with %q, want one, on the first line", p.path, titles, lines[0])
			}
			if next < len(dnSections) {
				t.Errorf("%s lacks the heading %q, or has it out of order", p.path, dnSections[next])
			}
		})
	}
}

func TestDnPagesFollowTheStyle(t *testing.T) {
	for _, p := range dnPages {
		t.Run(p.module, func(t *testing.T) {
			text := dcRead(t, p.path)
			open := false
			for i, line := range strings.Split(text, "\n") {
				if rest, ok := strings.CutPrefix(line, "```"); ok {
					switch {
					case open && rest != "":
						t.Errorf("%s:%d: a closing fence carries %q", p.path, i+1, rest)
					case !open && !slices.Contains(dnFenceTags, rest):
						t.Errorf("%s:%d: a code block tagged %q, want one of %v", p.path, i+1, rest, dnFenceTags)
					}
					open = !open
				}
				if !open && dnHTMLRE.MatchString(dnInlineRE.ReplaceAllString(line, "")) {
					t.Errorf("%s:%d: HTML in %q", p.path, i+1, line)
				}
			}
			if open {
				t.Errorf("%s ends inside a code block", p.path)
			}
			if m := dnInternalRE.FindString(text); m != "" {
				t.Errorf("%s names %q; a page never names a plan, wave, task or register row", p.path, m)
			}
		})
	}
}

// dnCommand is one command line of a code block tagged sh: the words that name the command and the flags it passes.
type dnCommand struct {
	line  string
	words []string
	flags []string
}

// dnCommands returns the darkpanel command lines of the sh blocks of md. A trailing backslash continues a line; the
// words are those before the first flag or placeholder.
func dnCommands(md string) []dnCommand {
	var cmds []dnCommand
	inSh, in := false, false
	var cur string
	for _, raw := range strings.Split(md, "\n") {
		if rest, ok := strings.CutPrefix(raw, "```"); ok {
			in = !in
			inSh = in && rest == "sh"
			cur = ""
			continue
		}
		if !inSh {
			continue
		}
		line := strings.TrimSpace(raw)
		if cur != "" {
			line = cur + " " + line
		}
		if strings.HasSuffix(line, "\\") {
			cur = strings.TrimSpace(strings.TrimSuffix(line, "\\"))
			continue
		}
		cur = ""
		line = strings.TrimPrefix(strings.TrimPrefix(line, "$ "), "sudo ")
		if !strings.HasPrefix(line, "darkpanel ") {
			continue
		}
		c := dnCommand{line: line}
		for _, f := range strings.Fields(line)[1:] {
			switch {
			case strings.HasPrefix(f, "--"):
				name, _, _ := strings.Cut(strings.TrimPrefix(f, "--"), "=")
				c.flags = append(c.flags, name)
			case len(c.flags) == 0 && !strings.ContainsAny(f[:1], "-<[$'\"|>"):
				c.words = append(c.words, f)
			}
		}
		cmds = append(cmds, c)
	}
	return cmds
}

func TestDnCommandsParse(t *testing.T) {
	md := "text\n```sh\n$ sudo darkpanel panel listener revoke --yes\ndarkpanel derp server deploy \\\n  --host-id <id> --mode=ip\necho hi\nsudo darkpanel npm instance status --local\n```\n```text\ndarkpanel nothing here\n```\n"
	got := dnCommands(md)
	want := []dnCommand{
		{"darkpanel panel listener revoke --yes", []string{"panel", "listener", "revoke"}, []string{"yes"}},
		{"darkpanel derp server deploy --host-id <id> --mode=ip", []string{"derp", "server", "deploy"}, []string{"host-id", "mode"}},
		{"darkpanel npm instance status --local", []string{"npm", "instance", "status"}, []string{"local"}},
	}
	if len(got) != len(want) {
		t.Fatalf("dnCommands found %d commands, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].line != want[i].line || !slices.Equal(got[i].words, want[i].words) || !slices.Equal(got[i].flags, want[i].flags) {
			t.Errorf("command %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestDnCommandsExist(t *testing.T) {
	reg, err := registry.Build()
	if err != nil {
		t.Fatal(err)
	}
	root, err := cli.NewRoot(cli.Options{Registry: reg, Commands: []*cobra.Command{{Use: "serve"}, {Use: "version"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range dnPages {
		t.Run(p.module, func(t *testing.T) {
			for _, c := range dnCommands(dcRead(t, p.path)) {
				cmd, rest, err := root.Find(c.words)
				if err != nil || len(rest) != 0 || cmd == root {
					t.Errorf("%s: %q names no command of the CLI (found %v, leftover %v, error %v)", p.path, c.line, cmd.CommandPath(), rest, err)
					continue
				}
				for _, f := range c.flags {
					if f != "help" && cmd.Flag(f) == nil {
						t.Errorf("%s: %q passes --%s, which %s does not have", p.path, c.line, f, cmd.CommandPath())
					}
				}
			}
		})
	}
}
