package docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const w10tmPage = "docs/security/threat-model.md"

// w10tmOutline is every heading of level one to three, in order.
var w10tmOutline = []string{
	"# Threat model",
	"## Scope",
	"## Assets",
	"## Actors",
	"## Trust boundaries",
	"### Browser and controller",
	"### Proxy and controller",
	"### Controller and agent",
	"### Agent and host",
	"### Controller and cloud APIs",
	"## Areas",
	"### Authentication",
	"### Agent protocol",
	"### Command execution",
	"### Compose execution",
	"### Secret handling and delivery",
	"### Firewall",
	"### Cloud credentials",
	"### Exposure",
	"### DERP host",
	"## Known residual risks",
}

var w10tmParts = []string{"Threats", "Mitigations", "Residual risk"}

// w10tmMinBullets is the fewest bullets a section of the page holds.
var w10tmMinBullets = map[string]int{
	"Scope": 2, "Assets": 6, "Actors": 5,
	"Browser and controller": 2, "Proxy and controller": 2, "Controller and agent": 2,
	"Agent and host": 2, "Controller and cloud APIs": 2,
}

// w10tmFacts are the terms an area states somewhere in its three parts,
// verbatim.
var w10tmFacts = map[string][]string{
	"Authentication":               {"TOTP", "CSRF", "argon2id", "SameSite", "rate limit", "scope"},
	"Agent protocol":               {"client certificate", "enrollment token", "revoked", "SAN", "TLS 1.3"},
	"Command execution":            {"allowlist", "argument vector", "openat2", "root"},
	"Compose execution":            {"provider", "COMPOSE_EXPERIMENTAL_GIT_REMOTE", "--no-build", "operator-authored"},
	"Secret handling and delivery": {"XChaCha20-Poly1305", "reveal", "audit", "backup", "passphrase"},
	"Firewall":                     {"lockout", "revert", "snapshot", "break-glass", "lock"},
	"Cloud credentials":            {"sealed", "write-only", "uncertain", "token"},
	"Exposure":                     {"X-Darkpanel-Client", "revoked", "port 81", "banner", "unreadable"},
	"DERP host":                    {"-verify-clients", "darkpanel-derp", "tailscaled", "pinned", "signature"},
}

// w10tmMitigationPaths are the repository paths an area's mitigations name.
var w10tmMitigationPaths = map[string][]string{
	"Authentication":               {"internal/controller/auth", "internal/controller/api", "internal/controller/audit", "internal/core/store/migrations/controller"},
	"Agent protocol":               {"internal/transport", "internal/controller/fleet", "internal/agent/daemon"},
	"Command execution":            {"internal/platform"},
	"Compose execution":            {"internal/modules/docker/compose"},
	"Secret handling and delivery": {"internal/core/secret", "internal/controller/backup"},
	"Firewall":                     {"internal/modules/firewall"},
	"Cloud credentials":            {"internal/controller/cloud"},
	"Exposure":                     {"internal/controller/listen", "internal/modules/npm/npmapi", "web/src/modules/npm/banner.tsx"},
	"DERP host":                    {"internal/modules/derp/artifact", "release/releasetool"},
}

// w10tmOpen are the terms the open security questions use; each appears in
// a bullet of Known residual risks.
var w10tmOpen = []string{"Origin", "port 81", "port 3000", "openpgp", "enrollment budget", "forward host"}

var (
	w10tmHeadingRE  = regexp.MustCompile(`^(#{1,6}) +(\S.*?)\s*$`)
	w10tmSpanRE     = regexp.MustCompile("`([^`]+)`")
	w10tmRepoPathRE = regexp.MustCompile(`^(internal|cmd|docs|release|scripts|test|web|schemas|\.github)/\S*$`)
	w10tmInternalRE = regexp.MustCompile(`(?i)\b(wave [0-9]+|register row|W[0-9]+-R[0-9]+|task [0-9]+|plan [0-9]+|coming soon|will be added)\b`)
	w10tmRowRE      = regexp.MustCompile(`(?i)\b(register|rows? [0-9]+)\b`)
)

// w10tmDoc is the page split by heading: Lines holds each section's lines
// under its heading, keyed by the section name; an area's parts are keyed
// "<area>/<part>".
type w10tmDoc struct {
	headings []string // levels one to three, as "## Scope"
	parts    map[string][]string
	lines    map[string][]string
	level4   map[string][]string // the level-four headings under each level-three heading
	bad      []string            // headings of level five or six
}

func w10tmParse(text string) w10tmDoc {
	d := w10tmDoc{parts: map[string][]string{}, lines: map[string][]string{}, level4: map[string][]string{}}
	fenced := false
	var current, area, part string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
		}
		m := w10tmHeadingRE.FindStringSubmatch(line)
		if m == nil || fenced {
			switch {
			case part != "":
				d.parts[area+"/"+part] = append(d.parts[area+"/"+part], line)
			case current != "":
				d.lines[current] = append(d.lines[current], line)
			}
			continue
		}
		level, name := len(m[1]), m[2]
		switch {
		case level <= 3:
			d.headings = append(d.headings, m[1]+" "+name)
			current, part = name, ""
			if level == 3 {
				area = name
			}
		case level == 4:
			d.level4[area] = append(d.level4[area], name)
			part = name
		default:
			d.bad = append(d.bad, line)
		}
	}
	return d
}

// w10tmBullets returns the bullets among lines: a line that starts with "- "
// and the non-blank lines that follow it up to the next bullet.
func w10tmBullets(lines []string) []string {
	var out []string
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "- "):
			out = append(out, line)
		case len(out) > 0 && strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "#"):
			out[len(out)-1] += " " + strings.TrimSpace(line)
		}
	}
	return out
}

func TestW10TmHasItsOutline(t *testing.T) {
	d := w10tmParse(dcRead(t, w10tmPage))
	if !slices.Equal(d.headings, w10tmOutline) {
		t.Errorf("%s has the headings\n%s\nwant\n%s", w10tmPage, strings.Join(d.headings, "\n"), strings.Join(w10tmOutline, "\n"))
	}
	if len(d.bad) > 0 {
		t.Errorf("%s has headings below level four: %q", w10tmPage, d.bad)
	}
	for _, area := range w10tmAreas() {
		if !slices.Equal(d.level4[area], w10tmParts) {
			t.Errorf("area %q has the subheadings %q, want exactly %q", area, d.level4[area], w10tmParts)
		}
	}
	for section, subs := range d.level4 {
		if !slices.Contains(w10tmAreas(), section) && len(subs) > 0 {
			t.Errorf("%q has the subheadings %q; only an area has any", section, subs)
		}
	}
}

// w10tmAreas are the headings between "## Areas" and "## Known residual risks".
func w10tmAreas() []string {
	from, to := slices.Index(w10tmOutline, "## Areas"), slices.Index(w10tmOutline, "## Known residual risks")
	var areas []string
	for _, h := range w10tmOutline[from+1 : to] {
		areas = append(areas, strings.TrimPrefix(h, "### "))
	}
	return areas
}

func TestW10TmSectionsHoldBullets(t *testing.T) {
	d := w10tmParse(dcRead(t, w10tmPage))
	for section, least := range w10tmMinBullets {
		if got := len(w10tmBullets(d.lines[section])); got < least {
			t.Errorf("section %q has %d bullets, want at least %d", section, got, least)
		}
	}
	for _, area := range w10tmAreas() {
		for _, part := range w10tmParts {
			if len(w10tmBullets(d.parts[area+"/"+part])) == 0 {
				t.Errorf("area %q has no bullet under %q", area, part)
			}
		}
	}
}

func TestW10TmMitigationsNameExistingRepositoryPaths(t *testing.T) {
	root := dcRoot(t)
	d := w10tmParse(dcRead(t, w10tmPage))
	for _, area := range w10tmAreas() {
		for _, bullet := range w10tmBullets(d.parts[area+"/Mitigations"]) {
			found := 0
			for _, m := range w10tmSpanRE.FindAllStringSubmatch(bullet, -1) {
				if !w10tmRepoPathRE.MatchString(m[1]) {
					continue
				}
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(m[1], "/")))); err != nil {
					t.Errorf("area %q: the mitigation names `%s`, which does not exist: %v", area, m[1], err)
					continue
				}
				found++
			}
			if found == 0 {
				t.Errorf("area %q: a mitigation names no existing repository path in backticks: %.120s", area, bullet)
			}
		}
	}
}

func TestW10TmAreasStateTheirFacts(t *testing.T) {
	d := w10tmParse(dcRead(t, w10tmPage))
	for _, area := range w10tmAreas() {
		var all []string
		for _, part := range w10tmParts {
			all = append(all, d.parts[area+"/"+part]...)
		}
		for _, name := range dcMissing(strings.Join(all, "\n"), w10tmFacts[area]) {
			t.Errorf("area %q does not mention %q", area, name)
		}
		mitigations := strings.Join(d.parts[area+"/Mitigations"], "\n")
		for _, name := range dcMissing(mitigations, w10tmMitigationPaths[area]) {
			t.Errorf("the mitigations of area %q do not name `%s`", area, name)
		}
	}
}

func TestW10TmKnownResidualRisks(t *testing.T) {
	d := w10tmParse(dcRead(t, w10tmPage))
	bullets := w10tmBullets(d.lines["Known residual risks"])
	if len(bullets) == 0 {
		t.Fatal("Known residual risks has no bullets")
	}
	all := strings.Join(bullets, "\n")
	for _, name := range dcMissing(all, w10tmOpen) {
		t.Errorf("Known residual risks does not mention %q", name)
	}
	for _, re := range []*regexp.Regexp{w10tmInternalRE, w10tmRowRE} {
		if m := re.FindString(all); m != "" {
			t.Errorf("Known residual risks names %q; it never names a register row, plan, wave or task", m)
		}
	}
}

func TestW10TmFollowsTheDocumentationStyle(t *testing.T) {
	text := dcRead(t, w10tmPage)
	if !strings.HasPrefix(text, "# Threat model\n") {
		t.Errorf("%s must start with the title", w10tmPage)
	}
	open := false
	for i, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(line, "```"); ok {
			switch {
			case open && rest != "":
				t.Errorf("%s:%d: a closing fence carries %q", w10tmPage, i+1, rest)
			case !open && !slices.Contains(dnFenceTags, rest):
				t.Errorf("%s:%d: a code block tagged %q, want one of %v", w10tmPage, i+1, rest, dnFenceTags)
			}
			open = !open
			continue
		}
		if !open && dnHTMLRE.MatchString(dnInlineRE.ReplaceAllString(line, "")) {
			t.Errorf("%s:%d: HTML in %q", w10tmPage, i+1, line)
		}
	}
	if open {
		t.Errorf("%s ends inside a code block", w10tmPage)
	}
	if m := w10tmInternalRE.FindString(text); m != "" {
		t.Errorf("%s names %q; a page never names a plan, wave, task or register row, or promises future work", w10tmPage, m)
	}
	dcLinks(t, w10tmPage)
}
