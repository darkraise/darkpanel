package docs_test

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/registry"
)

// dcRoot is the repository root, found from the test's working directory.
func dcRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod in the working directory or any directory above it")
		}
		dir = parent
	}
}

// dcRead returns the file at the slash path rel under the root, failing when it is missing.
func dcRead(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dcRoot(t), filepath.FromSlash(rel))) //nolint:gosec // G304: a path under the repository root
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return string(b)
}

// dcMissing returns the names that text does not contain verbatim, in order.
func dcMissing(text string, names []string) []string {
	var missing []string
	for _, name := range names {
		if !strings.Contains(text, name) {
			missing = append(missing, name)
		}
	}
	return missing
}

// dcMentions fails once per name that the file at rel does not contain verbatim, naming both.
func dcMentions(t *testing.T, rel string, names ...string) {
	t.Helper()
	for _, name := range dcMissing(dcRead(t, rel), names) {
		t.Errorf("%s does not mention %q", rel, name)
	}
}

// dcModuleNames returns the sorted names of every action and resource of module id in registry.Build() whose Spec is not Internal.
func dcModuleNames(t *testing.T, id string) []string {
	t.Helper()
	reg, err := registry.Build()
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	m, ok := reg.Module(id)
	if !ok {
		t.Fatalf("the registry has no module %q", id)
	}
	var names []string
	for _, a := range m.Actions {
		if !a.Spec.Internal {
			names = append(names, a.Spec.Name)
		}
	}
	for _, r := range m.Resources {
		names = append(names, r.Name)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// dcLinkPattern matches the target of an inline Markdown link or image: the
// text after "](" up to the closing parenthesis, a space before a title, or
// the closing angle bracket of a <target>.
var dcLinkPattern = regexp.MustCompile(`\]\(\s*(<[^>]*>|[^)\s]*)`)

// dcSchemePattern matches a URL scheme such as https: or mailto:.
var dcSchemePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

// dcBroken returns, in order of appearance and as written, the relative link
// targets in text, the content of the file at rel under root, that name no
// existing file or directory. Links inside fenced code blocks or inline code
// spans, links with a URL scheme and links to an anchor of the same file are
// skipped; a leading slash means the repository root.
func dcBroken(root, rel, text string) []string {
	var broken []string
	fenced := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " "), "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		for _, m := range dcLinkPattern.FindAllStringSubmatch(dcWithoutCode(line), -1) {
			written := m[1]
			target := strings.TrimSuffix(strings.TrimPrefix(written, "<"), ">")
			if i := strings.IndexAny(target, "#?"); i >= 0 {
				target = target[:i]
			}
			if target == "" || dcSchemePattern.MatchString(target) {
				continue
			}
			var file string
			if strings.HasPrefix(target, "/") {
				file = filepath.Join(root, filepath.FromSlash(target))
			} else {
				file = filepath.Join(root, filepath.FromSlash(path.Dir(rel)), filepath.FromSlash(target))
			}
			if _, err := os.Stat(file); err != nil {
				broken = append(broken, strings.TrimSuffix(strings.TrimPrefix(written, "<"), ">"))
			}
		}
	}
	return broken
}

// dcWithoutCode returns line with each inline code span removed: a run of
// backticks, the text up to the next run of as many backticks, and that run.
// A run with no closing run is left as it is, as Markdown does.
func dcWithoutCode(line string) string {
	var b strings.Builder
	for {
		start := strings.IndexByte(line, '`')
		if start < 0 {
			b.WriteString(line)
			return b.String()
		}
		n := start
		for n < len(line) && line[n] == '`' {
			n++
		}
		fence := line[start:n]
		end := -1
		for i := n; i < len(line); {
			j := strings.Index(line[i:], fence)
			if j < 0 {
				break
			}
			j += i
			k := j + len(fence)
			if k == len(line) || line[k] != '`' {
				end = k
				break
			}
			for k < len(line) && line[k] == '`' {
				k++
			}
			i = k
		}
		if end < 0 {
			b.WriteString(line[:n])
			line = line[n:]
			continue
		}
		b.WriteString(line[:start])
		line = line[end:]
	}
}

// dcLinks fails once per relative Markdown link in the file at rel whose target file does not exist.
func dcLinks(t *testing.T, rel string) {
	t.Helper()
	for _, target := range dcBroken(dcRoot(t), rel, dcRead(t, rel)) {
		t.Errorf("%s links %s, which does not exist", rel, target)
	}
}
