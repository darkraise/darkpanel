package docs_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/registry"
)

const dgGuide = "docs/dev/module-guide.md"

// dgIO is the input and output type the generic names below are instantiated with.
type dgIO struct{}

// dgNames are the identifiers the guide teaches. Each is written as the Go expression the guide's reader would use,
// so renaming or removing one breaks this file's compilation; the guide must also mention it by the name here.
var dgNames = []struct {
	name string
	ref  any
}{
	{"core.DefineRead", core.DefineRead[dgIO, dgIO]},
	{"core.DefineMutating", core.DefineMutating[dgIO, dgIO]},
	{"core.DefineRevertible", core.DefineRevertible[dgIO, dgIO]},
	{"core.DefineComposite", core.DefineComposite[dgIO, dgIO]},
	{"core.DefineTask", core.DefineTask[dgIO, dgIO]},
	{"core.DefineReveal", core.DefineReveal[dgIO, dgIO]},
	{"core.DefineInternalRead", core.DefineInternalRead[dgIO, dgIO]},
	{"core.DefineResource", core.DefineResource[dgIO]},
	{"core.Reader", reflect.TypeFor[core.Reader[dgIO, dgIO]]()},
	{"core.Mutator", reflect.TypeFor[core.Mutator[dgIO, dgIO]]()},
	{"core.Reverter", reflect.TypeFor[core.Reverter[dgIO, dgIO]]()},
	{"core.Composer", reflect.TypeFor[core.Composer[dgIO, dgIO]]()},
	{"core.TaskMutator", reflect.TypeFor[core.TaskMutator[dgIO, dgIO]]()},
	{"core.Observer", reflect.TypeFor[core.Observer]()},
	{"core.Undoer", reflect.TypeFor[core.Undoer]()},
	{"core.Recoverer", reflect.TypeFor[core.Recoverer]()},
	{"core.Uninstaller", reflect.TypeFor[core.Uninstaller]()},
	{"core.Module", reflect.TypeFor[core.Module]()},
	{"core.Manifest", core.Manifest{}},
	{"core.ActionSpec", core.ActionSpec{}},
	{"core.PreflightCheck", core.PreflightCheck{}},
	{"core.OwnedPath", core.OwnedPath{}},
	{"core.OwnedFile", core.OwnedFile},
	{"core.LabelModule", core.LabelModule},
	{"core.ReservedNames", core.ReservedNames},
	{"core.NewRegistry", core.NewRegistry},
	{"modules.All", modules.All},
	{"registry.Build", registry.Build},
	{"cli.CommandPath", cli.CommandPath},
}

// dgPaths are the files the guide teaches, each a path that exists in the repository. A path under
// internal/modules/<id>/ stands for that directory of any module; derp has migrations.
var dgPaths = []string{
	"internal/modules/modules.go",
	"internal/cli/modules.go",
	"web/src/modules/registry.ts",
	"web/src/modules/types.ts",
	"internal/modules/<id>/migrations/",
	"internal/modules/hostinfo/hostinfo.go",
	"docs/adr/0002-module-contract.md",
	"docs/adr/0011-actors-cli-names-and-layout.md",
	"docs/adr/0014-v1-core-extensions.md",
	"CONTRIBUTING.md",
}

var dgTargets = []string{"make schemas", "make api-docs"}

// dgTests are the tests the guide names, with the file each is declared in.
var dgTests = []struct{ name, file string }{
	{"TestEveryActionHasACLICommand", "internal/modules/registry_test.go"},
	{"TestSnapshotsCurrent", "internal/modules/modules_test.go"},
	{"TestAdAPIDocsCurrent", "internal/apidocs/apidocs_test.go"},
}

// dgWebTypes are the interfaces of web/src/modules/types.ts the guide teaches.
var dgWebTypes = []string{"ModuleEntry", "ModuleRouteProps", "ContainerAction", "ContainerRef", "HostCreateExtension", "GlobalPage"}

// dgSections are the level-two headings of the guide, in order.
var dgSections = []string{
	"## What a module is",
	"## Create the module package",
	"## Declare actions",
	"## Declare resources",
	"## Preflight, owned paths and migrations",
	"## Register the module",
	"## Get the CLI for free",
	"## Add the web pages",
	"## Schemas and the API reference",
	"## Test the module",
	"## Rules the registry enforces",
}

var (
	dgCoreRE   = regexp.MustCompile(`\bcore\.([A-Z][A-Za-z0-9]*)`)
	dgPathRE   = regexp.MustCompile("`((?:internal|web|docs|cmd|schemas|scripts|release|test)/[^`\\s]*)`")
	dgFenceTag = []string{"sh", "json", "text"}
)

func TestDgGuideMentionsEveryName(t *testing.T) {
	var names []string
	for _, n := range dgNames {
		if n.ref == nil {
			t.Errorf("%s has no value", n.name)
		}
		names = append(names, n.name)
	}
	dcMentions(t, dgGuide, names...)
}

func TestDgGuideMentionsEveryPathAndTarget(t *testing.T) {
	dcMentions(t, dgGuide, dgPaths...)
	dcMentions(t, dgGuide, dgTargets...)
	root := dcRoot(t)
	for _, p := range dgPaths {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(strings.ReplaceAll(p, "<id>", "derp")))); err != nil {
			t.Errorf("the guide teaches %s, which does not exist: %v", p, err)
		}
	}
	makefile := dcRead(t, "Makefile")
	for _, target := range dgTargets {
		name := strings.TrimPrefix(target, "make ")
		if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `:`).MatchString(makefile) {
			t.Errorf("the guide teaches %q, but the Makefile has no target %s", target, name)
		}
	}
}

func TestDgEveryPathTheGuideNamesExists(t *testing.T) {
	root := dcRoot(t)
	found := dgPathRE.FindAllStringSubmatch(dcRead(t, dgGuide), -1)
	if len(found) == 0 {
		t.Fatalf("%s names no path in backticks", dgGuide)
	}
	for _, m := range found {
		p := m[1]
		if strings.HasPrefix(p, "internal/modules/<id>/") {
			p = strings.ReplaceAll(p, "<id>", "derp")
		}
		if strings.ContainsAny(p, "<>*{}") || strings.HasSuffix(p, "...") {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(strings.TrimRight(p, ".,;:")))); err != nil {
			t.Errorf("the guide names %s, which does not exist", m[1])
		}
	}
}

// dgExported returns the exported top-level names of the non-test Go files of the package in dir.
func dgExported(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.IsExported() {
					names[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						names[s.Name.Name] = s.Name.IsExported()
					case *ast.ValueSpec:
						for _, n := range s.Names {
							names[n.Name] = n.IsExported()
						}
					}
				}
			}
		}
	}
	return names
}

func TestDgEveryCoreNameTheGuideUsesExists(t *testing.T) {
	exported := dgExported(t, filepath.Join(dcRoot(t), "internal", "core"))
	uses := dgCoreRE.FindAllStringSubmatch(dcRead(t, dgGuide), -1)
	if len(uses) == 0 {
		t.Fatalf("%s uses no core name", dgGuide)
	}
	for _, m := range uses {
		if !exported[m[1]] {
			t.Errorf("the guide uses core.%s, which package core does not export", m[1])
		}
	}
}

func TestDgGuideNamesItsTestsAndTheyExist(t *testing.T) {
	for _, tc := range dgTests {
		dcMentions(t, dgGuide, tc.name)
		if !strings.Contains(dcRead(t, tc.file), "func "+tc.name+"(") {
			t.Errorf("%s does not declare %s, which the guide names", tc.file, tc.name)
		}
	}
}

func TestDgGuideTeachesTheWebTypes(t *testing.T) {
	dcMentions(t, dgGuide, dgWebTypes...)
	dcMentions(t, dgGuide, "moduleRegistry")
	types := dcRead(t, "web/src/modules/types.ts")
	for _, name := range dgWebTypes {
		if !strings.Contains(types, "export interface "+name+" ") {
			t.Errorf("web/src/modules/types.ts does not export the interface %s, which the guide teaches", name)
		}
	}
	if !strings.Contains(dcRead(t, "web/src/modules/registry.ts"), "export const moduleRegistry") {
		t.Error("web/src/modules/registry.ts does not export moduleRegistry, which the guide teaches")
	}
}

func TestDgGuideHasItsSections(t *testing.T) {
	lines := strings.Split(dcRead(t, dgGuide), "\n")
	titles, next := 0, 0
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "# "):
			titles++
		case next < len(dgSections) && line == dgSections[next]:
			next++
		}
	}
	if titles != 1 || !strings.HasPrefix(lines[0], "# ") {
		t.Errorf("%s has %d level-one headings and starts with %q, want one, on the first line", dgGuide, titles, lines[0])
	}
	if next < len(dgSections) {
		t.Errorf("%s lacks the heading %q, or has it out of order", dgGuide, dgSections[next])
	}
}

func TestDgGuideFollowsTheStyle(t *testing.T) {
	text := dcRead(t, dgGuide)
	open := false
	for i, line := range strings.Split(text, "\n") {
		rest, ok := strings.CutPrefix(line, "```")
		if !ok {
			continue
		}
		switch {
		case open && rest != "":
			t.Errorf("%s:%d: a closing fence carries %q", dgGuide, i+1, rest)
		case !open && !slices.Contains(dgFenceTag, rest):
			t.Errorf("%s:%d: a code block tagged %q, want one of %v", dgGuide, i+1, rest, dgFenceTag)
		}
		open = !open
	}
	if open {
		t.Errorf("%s ends inside a code block", dgGuide)
	}
	if m := regexp.MustCompile(`(?i)\b(wave [0-9]|register row|W[0-9]+-R[0-9]+|task [0-9]+|plan [0-9])`).FindString(text); m != "" {
		t.Errorf("%s names %q; a page never names a plan, wave, task or register row", dgGuide, m)
	}
}
