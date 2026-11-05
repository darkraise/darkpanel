// Command apidocsgen writes the API reference into docs/api. Run it through
// make api-docs; a test in internal/apidocs fails when the committed files are
// out of date.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/darkraise/darkpanel/internal/apidocs"
	"github.com/darkraise/darkpanel/internal/controller/api"
	"github.com/darkraise/darkpanel/internal/registry"
)

func main() {
	out := flag.String("out", "docs/api", "directory to write the reference into")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "apidocsgen:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	reg, err := registry.Build()
	if err != nil {
		return err
	}
	files, err := apidocs.Generate(reg, api.Routes())
	if err != nil {
		return err
	}
	return apidocs.Write(out, files)
}
