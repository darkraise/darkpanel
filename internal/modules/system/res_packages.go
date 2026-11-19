package system

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
)

// searchLimit caps the names a search returns.
const searchLimit = 50

// InstalledPackage is one row of the packages resource.
type InstalledPackage struct {
	Name        string  `json:"name"`
	Version     string  `json:"version"`
	Arch        string  `json:"arch"`
	Description string  `json:"description"`
	Protected   *string `json:"protected"` // why no package change may remove it; null when it may
}

// SearchInput is the input of system.packages.search.
type SearchInput struct {
	Term string `json:"term"`
}

// SearchHit is one package a search found.
type SearchHit struct {
	Name      string `json:"name"`
	Candidate string `json:"candidate"`
	Installed bool   `json:"installed"`
}

func (m module) packagesResource() core.ResourceDef {
	return core.DefineResource(ID+".packages", m.packages)
}

// packages lists the installed packages with their protection.
func (m module) packages(ctx context.Context, env core.Env) ([]InstalledPackage, error) {
	pkgs, err := dpkgPackages(ctx, env)
	if err != nil {
		return nil, err
	}
	out := []InstalledPackage{}
	var names []string
	for _, p := range pkgs {
		if strings.HasSuffix(p.Status, " installed") {
			out = append(out, InstalledPackage{Name: p.Name, Version: p.Version, Arch: p.Arch, Description: p.Summary})
			names = append(names, p.Name)
		}
	}
	protected, err := m.protectedRemovals(ctx, env, names)
	if err != nil {
		return nil, err
	}
	reasons := map[string]string{}
	for _, p := range protected {
		reasons[p.Name] = p.Reason
	}
	for i := range out {
		if r, ok := reasons[out[i].Name]; ok {
			out[i].Protected = &r
		}
	}
	slices.SortFunc(out, func(a, b InstalledPackage) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.Arch, b.Arch))
	})
	return out, nil
}

func (m module) searchAction() core.ActionDef {
	return core.DefineRead[SearchInput, []SearchHit](core.ActionSpec{
		Name: ID + ".packages.search", Version: 1, Side: core.SideAgent,
		Summary: "Search the configured package sources for names containing a term.",
	}, packageSearch{})
}

type packageSearch struct{}

// Read finds up to 50 package names containing the term as a plain
// substring; the term reaches no program.
func (packageSearch) Read(ctx context.Context, env core.Env, in SearchInput) ([]SearchHit, error) {
	if !namePattern.MatchString(in.Term) {
		return nil, nameInvalid(in.Term, "is not a search term")
	}
	all, err := aptPackageNames(ctx, env)
	if err != nil {
		return nil, err
	}
	var names []string
	for n := range all {
		if strings.Contains(n, in.Term) && namePattern.MatchString(n) {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	if len(names) > searchLimit {
		names = names[:searchLimit]
	}
	hits := []SearchHit{}
	if len(names) == 0 {
		return hits, nil
	}
	policy, err := aptPolicy(ctx, env, names)
	if err != nil {
		return nil, err
	}
	installed, err := installedVersions(ctx, env)
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		_, inst := installed[n]
		hits = append(hits, SearchHit{Name: n, Candidate: policy[n].candidate, Installed: inst})
	}
	return hits, nil
}
