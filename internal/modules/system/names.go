package system

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

const aptCache = "/usr/bin/apt-cache"

// namePattern is what a package name must look like before any program
// sees it; the leading character also keeps a name from reading as an
// option.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]{1,63}$`)

// nameKind is the set a name must belong to.
type nameKind int

const (
	nameAvailable nameKind = iota // a package apt can install: an apt-cache pkgnames entry
	nameInstalled                 // a package dpkg has installed
)

// validateNames refuses, with system.name_invalid naming the first bad
// name, a name that does not match namePattern or is not exactly a member
// of the set want names. Every name is checked against the pattern before
// any program runs.
func validateNames(ctx context.Context, env core.Env, names []string, want nameKind) error {
	for _, n := range names {
		if !namePattern.MatchString(n) {
			return nameInvalid(n, "is not a package name")
		}
	}
	if len(names) == 0 {
		return nil
	}
	var known map[string]bool
	var why string
	switch want {
	case nameAvailable:
		pkgs, err := aptPackageNames(ctx, env)
		if err != nil {
			return err
		}
		known, why = pkgs, "is not a package apt knows"
	case nameInstalled:
		versions, err := installedVersions(ctx, env)
		if err != nil {
			return err
		}
		known, why = map[string]bool{}, "is not installed"
		for n := range versions {
			known[n] = true
		}
	default:
		return fmt.Errorf("system: unknown name kind %d", want)
	}
	for _, n := range names {
		if !known[n] {
			return nameInvalid(n, why)
		}
	}
	return nil
}

func nameInvalid(name, why string) error {
	return errcode.New(errcode.SystemNameInvalid, fmt.Sprintf("%q %s", name, why))
}

// aptPackageNames is the set apt-cache pkgnames lists.
func aptPackageNames(ctx context.Context, env core.Env) (map[string]bool, error) {
	res, err := env.Exec.Run(ctx, core.Cmd{Path: aptCache, Args: platform.SystemAptCachePkgnames()})
	if err != nil {
		return nil, fmt.Errorf("system: run apt-cache pkgnames: %w", err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("system: apt-cache pkgnames exited %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	names := map[string]bool{}
	for line := range strings.Lines(string(res.Stdout)) {
		if n := strings.TrimSpace(line); n != "" {
			names[n] = true
		}
	}
	return names, nil
}
