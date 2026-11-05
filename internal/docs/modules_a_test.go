package docs_test

import (
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/modules/firewall/guard"
	"github.com/darkraise/darkpanel/internal/modules/hostinfo"
	"github.com/darkraise/darkpanel/internal/modules/tailscale/link"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
	"github.com/darkraise/darkpanel/internal/registry"
)

// dmPages maps each module this file covers to its page.
var dmPages = map[string]string{
	"hostinfo":  "docs/user/modules/hostinfo.md",
	"firewall":  "docs/user/modules/firewall.md",
	"docker":    "docs/user/modules/docker.md",
	"tailscale": "docs/user/modules/tailscale.md",
	"tailnet":   "docs/user/modules/tailnet.md",
}

var (
	dmCodeRE = regexp.MustCompile("`[^`]*`")
	dmHTMLRE = regexp.MustCompile(`</?[A-Za-z][^>]*>`)
)

// dmFields returns the JSON names of the fields of typ, with the fields of
// embedded structs flattened in and the "guard" of a guarded
// action left out.
func dmFields(typ reflect.Type) []string {
	var out []string
	for i := range typ.NumField() {
		f := typ.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		switch {
		case f.Anonymous && f.Type.Kind() == reflect.Struct && name == "":
			out = append(out, dmFields(f.Type)...)
		case name == "" || name == "-" || name == "guard" || name == "overrides" || !f.IsExported():
		default:
			out = append(out, name)
		}
	}
	return out
}

// dmInputFields returns the JSON names of the input of the action name.
func dmInputFields(t *testing.T, name string) []string {
	t.Helper()
	reg, err := registry.Build()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range reg.Actions() {
		if a.Spec.Name == name {
			fields := dmFields(a.InputType)
			if len(fields) == 0 {
				t.Fatalf("docs: %s has no input fields", name)
			}
			return fields
		}
	}
	t.Fatalf("docs: the registry has no action %s", name)
	return nil
}

// dmUninstallOptions returns module id's uninstall options as
// <module>.<key>=<value>, one per allowed value.
func dmUninstallOptions(t *testing.T, id string) []string {
	t.Helper()
	var out []string
	for _, m := range modules.All() {
		if un, ok := m.(core.Uninstaller); ok && m.Manifest().ID == id {
			for _, o := range un.UninstallOptions() {
				for _, v := range o.Values {
					out = append(out, id+"."+o.Key+"="+v)
				}
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("docs: module %s declares no uninstall option", id)
	}
	return out
}

func dmCodes(codes ...errcode.Code) []string {
	out := make([]string, len(codes))
	for i, c := range codes {
		out[i] = string(c)
	}
	return out
}

// TestDmEveryActionAndResourceIsDocumented holds each page to naming every
// action and resource of its module in the registry, with the command the
// CLI generates for it, and to linking only files that exist.
func TestDmEveryActionAndResourceIsDocumented(t *testing.T) {
	for id, page := range dmPages {
		names := dcModuleNames(t, id)
		if len(names) == 0 {
			t.Fatalf("docs: the registry has no action or resource of module %s", id)
		}
		dcMentions(t, page, names...)
		var commands []string
		for _, n := range names {
			commands = append(commands, "darkpanel "+strings.Join(cli.CommandPath(n), " "))
		}
		dcMentions(t, page, commands...)
		dcLinks(t, page)
	}
}

func TestDmHostinfo(t *testing.T) {
	dcMentions(t, dmPages["hostinfo"], dmFields(reflect.TypeFor[hostinfo.Summary]())...)
}

func TestDmFirewall(t *testing.T) {
	page := dmPages["firewall"]
	dcMentions(t, page, guard.GuardID, string(errcode.PlanGuardFailed))
	dcMentions(t, page, dmCodes(
		errcode.FirewallManualRequired, errcode.FirewallUnsupportedConfiguration, errcode.FirewallUnsupportedIntegration,
		errcode.FirewallDockerUserInUse, errcode.FirewallPredictionMismatch,
	)...)
	dcMentions(t, page, dmInputFields(t, "firewall.docker.set-sources")...)
	dcMentions(t, page, "--confirm-timeout", "--confirm", "900", "baseline", "firewall.reconcile", "darkpanel.module")
	dcMentions(t, page, "sudo darkpanel firewall rollback --local",
		"sudo darkpanel "+strings.Join(cli.CommandPath("firewall.snapshot.restore"), " ")+" --local")
	for _, o := range dmUninstallOptions(t, "firewall") {
		dcMentions(t, page, "--module-option "+o)
	}
}

func TestDmDocker(t *testing.T) {
	page := dmPages["docker"]
	dcMentions(t, page, dmCodes(errcode.DockerEngineUnavailable, errcode.DockerInUse, errcode.DockerOwned, errcode.DockerComposeUnavailable)...)
	dcMentions(t, page, dockerapi.MinAPIVersion, "[redacted]", "darkpanel.module", "provider", "--no-build", "include", "profile",
		"com.docker.compose.project")
}

func TestDmTailscale(t *testing.T) {
	page := dmPages["tailscale"]
	dcMentions(t, page, link.GuardID)
	dcMentions(t, page, dmCodes(
		errcode.TailscaleUnavailable, errcode.TailscaleRepoConflict, errcode.TailscalePackagePresent,
		errcode.TailscalePackageHeld, errcode.TailscalePinFailed,
	)...)
	dcMentions(t, page, dmInputFields(t, "tailscale.prefs.set")...)
	dcMentions(t, page, "STUN", "DERP")
	for _, o := range dmUninstallOptions(t, "tailscale") {
		dcMentions(t, page, "--module-option "+o)
	}
}

func TestDmTailnet(t *testing.T) {
	page := dmPages["tailnet"]
	dcMentions(t, page, dmCodes(errcode.TailnetPolicyConflict, errcode.TailnetPolicyInvalid)...)
	dcMentions(t, page, dmInputFields(t, "tailnet.account.connect")...)
	dcMentions(t, page, dmInputFields(t, "tailnet.authkey.create")...)
	dcMentions(t, page, "devices:core", "devices:routes", "auth_keys", "policy_file")
	dcMentions(t, page, "`set`", "`delete`", "`addItem`", "`removeItem`", "--tailnet-join", "ETag")
}

// TestDmStyle holds the module pages to the documentation style that
// matters to a reader of the source: one title on the first line, fenced
// blocks that are tagged, and no HTML.
func TestDmStyle(t *testing.T) {
	ids := make([]string, 0, len(dmPages))
	for id := range dmPages {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		rel := dmPages[id]
		titles, inFence := 0, false
		for i, line := range strings.Split(dcRead(t, rel), "\n") {
			if strings.HasPrefix(line, "```") {
				if !inFence && !slices.Contains([]string{"```sh", "```json", "```text"}, strings.TrimSpace(line)) {
					t.Errorf("docs: %s:%d: a fenced block must be tagged sh, json or text: %q", rel, i+1, line)
				}
				inFence = !inFence
				continue
			}
			if inFence {
				continue
			}
			if strings.HasPrefix(line, "# ") {
				titles++
				if i != 0 {
					t.Errorf("docs: %s:%d: the title must be the first line", rel, i+1)
				}
			}
			if prose := dmCodeRE.ReplaceAllString(line, ""); dmHTMLRE.MatchString(prose) {
				t.Errorf("docs: %s:%d: HTML in a page: %q", rel, i+1, line)
			}
		}
		if titles != 1 {
			t.Errorf("docs: %s has %d titles, want 1", rel, titles)
		}
	}
}
