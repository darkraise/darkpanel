package docs_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/controller/rollout"
	"github.com/darkraise/darkpanel/internal/registry"
	"github.com/darkraise/darkpanel/internal/update"
)

const w10rbPage = "docs/runbook.md"

// w10rbOutline is every heading of the runbook, in order.
var w10rbOutline = []string{
	"# Runbook",
	"## Lockout",
	"### Firewall break-glass",
	"### Tailscale preferences revert",
	"### Panel listener revoke",
	"## Failed update",
	"### Rollback",
	"### Recover",
	"## Lost controller",
	"### Restore from backup",
	"### Unsettled restore journal",
	"### Re-enroll agents",
}

// w10rbMainCommands are the hand-written commands of cmd/darkpanel. That
// directory is package main and cannot be imported, so each command's name
// and flags are read from the function that builds it.
var w10rbMainCommands = map[string]struct{ file, fn string }{
	"agent enroll":   {"cmd/darkpanel/agent.go", "newAgentEnrollCmd"},
	"update recover": {"cmd/darkpanel/update.go", "newUpdateRecoverCmd"},
	"system install": {"cmd/darkpanel/system.go", "newSystemInstallCmd"},
}

// w10rbWant are the lines each section must carry, verbatim.
var w10rbWant = map[string][]string{
	"Firewall break-glass": {
		"sudo darkpanel firewall rollback --local",
		"sudo darkpanel firewall snapshots get --local",
		"sudo darkpanel firewall snapshot restore --local --id <id> --confirm",
		"sudo darkpanel firewall state repair --local",
	},
	"Tailscale preferences revert": {
		"sudo darkpanel tailscale status get --local",
		"darkpanel change list --state pending",
		"darkpanel change revert <id>",
		"sudo darkpanel tailscale prefs set --local --input <file> --yes --confirm",
		"darkpanel change repair <id>",
	},
	"Panel listener revoke": {
		"sudo darkpanel panel listener revoke",
		"darkpanel panel exposure remove",
	},
	"Rollback": {
		"sudo darkpanel update rollback --local --yes",
	},
	"Recover": {
		"sudo darkpanel update recover",
	},
	"Restore from backup": {
		"sudo darkpanel system install --role controller --agent-advertise <host>:<port>",
		"sudo systemctl stop darkpanel-controller.service darkpanel-agent.service",
		"sudo darkpanel controller restore --in <backup-file> --passphrase-file <passphrase-file>",
		"sudo darkpanel controller restore --recover",
		"sudo systemctl start darkpanel-controller.service darkpanel-agent.service",
		"sudo darkpanel controller quarantine list",
		"sudo darkpanel controller quarantine resolve <record> done",
		"sudo darkpanel controller quarantine release",
	},
	"Unsettled restore journal": {
		"sudo journalctl -u darkpanel-controller.service",
		"sudo darkpanel controller restore --in <backup-file> --passphrase-file <passphrase-file>",
	},
	"Re-enroll agents": {
		"sudo darkpanel controller quarantine enroll-token <host>",
		"sudo darkpanel agent enroll --token-file <token-file>",
		"sudo systemctl restart darkpanel-agent.service",
		"sudo darkpanel host enroll-local",
		"darkpanel host list",
	},
}

var (
	w10rbHeadingRE  = regexp.MustCompile(`^(#{1,6}) +(\S.*?)\s*$`)
	w10rbInternalRE = regexp.MustCompile(`(?i)\b(wave [0-9]+|register row|W[0-9]+-R[0-9]+|task [0-9]+|plan [0-9]+|coming soon|will be added)\b`)
)

// w10rbSections returns the headings of text in order and the lines under
// each heading, ignoring everything inside fenced code blocks for headings.
func w10rbSections(text string) (headings []string, lines map[string][]string) {
	lines = map[string][]string{}
	fenced, current := false, ""
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
		}
		if m := w10rbHeadingRE.FindStringSubmatch(line); m != nil && !fenced {
			headings = append(headings, m[1]+" "+m[2])
			current = m[2]
			continue
		}
		if current != "" {
			lines[current] = append(lines[current], line)
		}
	}
	return headings, lines
}

// w10rbRoot is the command tree the runbook's commands are looked up in:
// every generated command and every hand-written command of package cli. The
// hand-written commands of cmd/darkpanel are checked through
// w10rbMainCommands instead.
func w10rbRoot(t *testing.T) *cobra.Command {
	t.Helper()
	reg, err := registry.Build()
	if err != nil {
		t.Fatal(err)
	}
	root, err := cli.NewRoot(cli.Options{
		Registry: reg,
		Commands: []*cobra.Command{{Use: "serve"}, {Use: "version"}, {Use: "update"}},
		Adopt: map[string][]string{"update": {
			update.ActionApply, update.ActionRollback, update.ActionStatus,
			rollout.ActionRollout, rollout.ActionRolloutCancel, rollout.ActionRollouts,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// w10rbMainFlags returns the name that the Use field of the command built by
// function fn in file declares, and the flags that function adds.
func w10rbMainFlags(t *testing.T, file, fn string) (use string, flags []string) {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dcRoot(t), filepath.FromSlash(file)), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var body *ast.BlockStmt
	for _, d := range parsed.Decls {
		if f, ok := d.(*ast.FuncDecl); ok && f.Name.Name == fn {
			body = f.Body
		}
	}
	if body == nil {
		t.Fatalf("%s declares no function %s", file, fn)
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.KeyValueExpr:
			if k, ok := n.Key.(*ast.Ident); ok && k.Name == "Use" {
				if lit, ok := n.Value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					s, _ := strconv.Unquote(lit.Value)
					use, _, _ = strings.Cut(s, " ")
				}
			}
		case *ast.CallExpr:
			if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "addDevFlag" {
				flags = append(flags, "dev")
			}
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if recv, ok := sel.X.(*ast.CallExpr); !ok || !strings.HasSuffix(w10rbCallName(recv), "Flags") {
				return true
			}
			for _, arg := range n.Args[:min(2, len(n.Args))] {
				if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					flags = append(flags, strings.Trim(lit.Value, `"`))
					break
				}
			}
		}
		return true
	})
	return use, flags
}

func w10rbCallName(c *ast.CallExpr) string {
	if sel, ok := c.Fun.(*ast.SelectorExpr); ok {
		return sel.Sel.Name
	}
	return ""
}

// w10rbProblems returns one message for each darkpanel command line of the
// sh blocks of md that names no command of the CLI or passes a flag its
// command does not declare. --local and --help are on every command.
func w10rbProblems(t *testing.T, root *cobra.Command, md string) []string {
	t.Helper()
	var problems []string
	for _, c := range dnCommands(md) {
		path := strings.Join(c.words, " ")
		matched := false
		for name, src := range w10rbMainCommands {
			if path != name && !strings.HasPrefix(path, name+" ") {
				continue
			}
			matched = true
			use, flags := w10rbMainFlags(t, src.file, src.fn)
			if want := name[strings.LastIndex(name, " ")+1:]; use != want {
				t.Errorf("%s: %s declares the command %q, want %q", src.file, src.fn, use, want)
			}
			for _, f := range c.flags {
				if f != "local" && f != "help" && !slices.Contains(flags, f) {
					problems = append(problems, c.line+": "+name+" has no flag --"+f)
				}
			}
		}
		if matched {
			continue
		}
		cmd, _, err := root.Find(c.words)
		switch {
		case err != nil || cmd == root:
			problems = append(problems, c.line+": no such command")
			continue
		case cmd.HasSubCommands():
			problems = append(problems, c.line+": names the group "+cmd.CommandPath()+", not a command of it")
			continue
		}
		for _, f := range c.flags {
			if f != "help" && cmd.Flag(f) == nil {
				problems = append(problems, c.line+": "+cmd.CommandPath()+" has no flag --"+f)
			}
		}
	}
	return problems
}

func TestW10RbCheckerFindsWhatItShould(t *testing.T) {
	root := w10rbRoot(t)
	md := strings.Join([]string{
		"```sh",
		"sudo darkpanel agent enroll --token-file <token-file>",
		"sudo darkpanel agent enroll --bogus",
		"sudo darkpanel update recover --boot",
		"sudo darkpanel update rollback --local --yes",
		"sudo darkpanel update rollback --bogus",
		"sudo darkpanel controller quarantine resolve <record> done",
		"darkpanel controller quarantine",
		"sudo darkpanel nothing here",
		"sudo systemctl restart darkpanel-agent.service",
		"```",
	}, "\n")
	want := []string{
		"darkpanel agent enroll --bogus: agent enroll has no flag --bogus",
		"darkpanel update rollback --bogus: darkpanel update rollback has no flag --bogus",
		"darkpanel controller quarantine: names the group darkpanel controller quarantine, not a command of it",
		"darkpanel nothing here: no such command",
	}
	if got := w10rbProblems(t, root, md); !slices.Equal(got, want) {
		t.Errorf("problems = %q, want %q", got, want)
	}
}

func TestW10RbRunbookHasItsOutline(t *testing.T) {
	headings, _ := w10rbSections(dcRead(t, w10rbPage))
	if !slices.Equal(headings, w10rbOutline) {
		t.Errorf("%s has the headings\n%s\nwant\n%s", w10rbPage, strings.Join(headings, "\n"), strings.Join(w10rbOutline, "\n"))
	}
}

func TestW10RbCommandsExistAndCarryOnlyTheirFlags(t *testing.T) {
	for _, p := range w10rbProblems(t, w10rbRoot(t), dcRead(t, w10rbPage)) {
		t.Errorf("%s: %s", w10rbPage, p)
	}
}

func TestW10RbSectionsCarryTheirProcedures(t *testing.T) {
	_, lines := w10rbSections(dcRead(t, w10rbPage))
	for section, want := range w10rbWant {
		text := strings.Join(lines[section], "\n")
		for _, line := range want {
			if !strings.Contains(text, "\n"+line+"\n") {
				t.Errorf("section %q has no line %q of its own inside a code block", section, line)
			}
		}
	}
}

func TestW10RbReEnrollUsesAgentEnrollAndARestart(t *testing.T) {
	_, lines := w10rbSections(dcRead(t, w10rbPage))
	text := strings.Join(lines["Re-enroll agents"], "\n")
	enroll := strings.Index(text, "sudo darkpanel agent enroll")
	restart := strings.Index(text, "sudo systemctl restart darkpanel-agent.service")
	if enroll < 0 || restart < enroll {
		t.Errorf("the re-enroll section runs agent enroll at %d and the agent restart at %d, want the restart after the enrollment", enroll, restart)
	}
	if strings.Contains(text, "system install --role agent") {
		t.Error("the re-enroll section names system install --role agent, which keeps credentials the lost controller still accepts and ignores the new token")
	}
}

func TestW10RbUserGuideLinksTheRunbook(t *testing.T) {
	dcMentions(t, "docs/user/README.md", "](../runbook.md)")
	dcLinks(t, w10rbPage)
}

func TestW10RbFollowsTheDocumentationStyle(t *testing.T) {
	text := dcRead(t, w10rbPage)
	open := false
	for i, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(line, "```"); ok {
			switch {
			case open && rest != "":
				t.Errorf("%s:%d: a closing fence carries %q", w10rbPage, i+1, rest)
			case !open && !slices.Contains(dnFenceTags, rest):
				t.Errorf("%s:%d: a code block tagged %q, want one of %v", w10rbPage, i+1, rest, dnFenceTags)
			}
			open = !open
			continue
		}
		if !open && dnHTMLRE.MatchString(dnInlineRE.ReplaceAllString(line, "")) {
			t.Errorf("%s:%d: HTML in %q", w10rbPage, i+1, line)
		}
	}
	if open {
		t.Errorf("%s ends inside a code block", w10rbPage)
	}
	if m := w10rbInternalRE.FindString(text); m != "" {
		t.Errorf("%s names %q; a page never names a plan, wave, task or register row, or promises future work", w10rbPage, m)
	}
	if !strings.HasPrefix(text, "# Runbook\n") {
		t.Errorf("%s must start with the title", w10rbPage)
	}
}
