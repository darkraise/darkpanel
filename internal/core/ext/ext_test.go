package ext

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// extAllowed are the only non-standard imports the package may have: a
// module package and internal/controller/server both import ext, so an
// import of either from here would be a cycle.
var extAllowed = map[string]bool{
	"github.com/darkraise/darkpanel/internal/core":       true,
	"github.com/darkraise/darkpanel/internal/core/store": true,
}

func TestExtImportsOnlyCoreStoreAndStandardLibrary(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name) //nolint:gosec // G304: this package's own source
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			// A standard-library path has no dot in its first element.
			if !strings.Contains(strings.SplitN(path, "/", 2)[0], ".") {
				continue
			}
			if !extAllowed[path] {
				t.Errorf("%s imports %s; ext may import only internal/core, internal/core/store and the standard library", name, path)
			}
		}
	}
}

func TestExtSurfaceWebIsTheZeroValue(t *testing.T) {
	// A Route literal that omits Surface must land on the web surface, the
	// one with authentication, CSRF and scope checks.
	if (Route{}).Surface != SurfaceWeb || SurfaceSocket == SurfaceWeb {
		t.Errorf("SurfaceWeb = %d, SurfaceSocket = %d; want the web surface as the zero value", SurfaceWeb, SurfaceSocket)
	}
}
