package docs_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/update"
)

const v1r04Page = "docs/versioning.md"

// v1r04Refusal is the part of the updater's refusal across a major version that the page quotes.
const v1r04Refusal = "cross a major version, which is not supported"

// v1r04Section returns the text of the page's "Upgrade from 0.x" section, up to the next section.
func v1r04Section(t *testing.T) string {
	t.Helper()
	const heading = "\n## Upgrade from 0.x\n"
	_, after, ok := strings.Cut(dcRead(t, v1r04Page), heading)
	if !ok {
		t.Fatalf("%s has no section headed %q", v1r04Page, strings.TrimSpace(heading))
	}
	if i := strings.Index(after, "\n## "); i >= 0 {
		after = after[:i]
	}
	return after
}

// TestV1r04VersioningStatesTheReinstallBoundary: the page says that 0.x does not upgrade to 1.0, quotes the
// refusal the updater gives, and names the commands of a reinstall, which exist as the user guide documents them.
func TestV1r04VersioningStatesTheReinstallBoundary(t *testing.T) {
	section := v1r04Section(t)
	for _, name := range dcMissing(section, []string{
		"0.x does not upgrade to 1.0",
		"reinstall",
		v1r04Refusal,
		"sudo darkpanel uninstall",
		"--keep-data",
		"sudo sh install.sh --role controller",
		"darkpanel host create",
		"sudo sh install.sh --role agent",
		"](user/install.md)",
		"](user/uninstall.md)",
	}) {
		t.Errorf("the 0.x section does not mention %q", name)
	}

	_, err := update.Steps("0.9.0", "1.0.0-rc.1", core.ChannelBeta,
		[]update.Release{{Version: "1.0.0-rc.1", Channel: core.ChannelBeta}}, false)
	if err == nil || !strings.Contains(err.Error(), v1r04Refusal) {
		t.Errorf("update.Steps from 0.9.0 to 1.0.0-rc.1 = %v, want a refusal that says %q", err, v1r04Refusal)
	}
}

// TestV1r04VersioningFollowsTheDocumentationStyle: the page, with its new section, keeps one title, tagged code
// blocks, no HTML, no mention of a plan or of work to come, and links that resolve.
func TestV1r04VersioningFollowsTheDocumentationStyle(t *testing.T) {
	v1r04Section(t)
	text := dcRead(t, v1r04Page)
	level, titles, inFence := 0, 0, false
	for i, line := range strings.Split(text, "\n") {
		at := func(msg string) { t.Errorf("%s:%d: %s: %q", v1r04Page, i+1, msg, line) }
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
			at("HTML in the page")
		}
		if duPlanRE.MatchString(prose) {
			at("the page names the plan or promises future work")
		}
	}
	if inFence {
		t.Errorf("%s ends inside a fenced block", v1r04Page)
	}
	if titles != 1 {
		t.Errorf("%s has %d titles, want 1", v1r04Page, titles)
	}
	dcLinks(t, v1r04Page)
}
