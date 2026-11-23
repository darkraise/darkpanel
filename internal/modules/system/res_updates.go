package system

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform"
)

const (
	aptMark          = "/usr/bin/apt-mark"
	dpkgBin          = "/usr/bin/dpkg"
	updatesTTL       = 60 * time.Second
	refreshStamp     = "/var/lib/apt/periodic/update-success-stamp"
	aptLists         = "/var/lib/apt/lists"
	failedSourcesDir = "/var/lib/darkpanel/system"
	// failedSourcesFile lists the sources the last refresh could not fetch,
	// one per line; the refresh action writes it in the module's managed
	// directory.
	failedSourcesFile = failedSourcesDir + "/failed-sources"
)

// phasedOpts makes a simulation include phased updates, so the names it adds
// over the plain one are the updates phasing holds back.
var phasedOpts = []string{"-o", "APT::Get::Always-Include-Phased-Updates=true"}

// Updates is the updates resource.
type Updates struct {
	Upgradable      []Upgradable `json:"upgradable"`
	Removals        []string     `json:"removals"`
	HeldBack        []Held       `json:"heldBack"`
	RefreshedAt     *string      `json:"refreshedAt" jsonschema:"nullable"`
	FailedSources   []string     `json:"failedSources"`
	DpkgInterrupted bool         `json:"dpkgInterrupted"`
}

// Upgradable is a package a full upgrade would install or upgrade.
type Upgradable struct {
	Name     string `json:"name"`
	From     string `json:"from"`
	To       string `json:"to"`
	Origin   string `json:"origin"`
	Security bool   `json:"security"`
}

// Held is a package with a newer version a full upgrade leaves alone.
type Held struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Reason  string `json:"reason"` // "held" or "phased"
}

// updatesKey keys the cache by host and agent database, so two agents in one
// process (tests) never share an answer.
type updatesKey struct {
	host string
	db   *store.DB
}

var updatesCache = struct {
	sync.Mutex
	m map[updatesKey]updatesEntry
}{m: map[updatesKey]updatesEntry{}}

type updatesEntry struct {
	at  time.Time
	out Updates
}

// invalidateUpdates drops the cached updates answer of hostID; every action
// that runs apt calls it when it ends.
func invalidateUpdates(hostID string) {
	updatesCache.Lock()
	defer updatesCache.Unlock()
	maps.DeleteFunc(updatesCache.m, func(k updatesKey, _ updatesEntry) bool { return k.host == hostID })
}

func (m module) updatesResource() core.ResourceDef {
	return core.DefineResource(ID+".updates", m.updates)
}

// updates answers from the cache for 60 seconds, else reads apt.
func (m module) updates(ctx context.Context, env core.Env) (Updates, error) {
	key := updatesKey{host: env.HostID, db: env.DB}
	now := env.Clock.Now()
	updatesCache.Lock()
	e, ok := updatesCache.m[key]
	updatesCache.Unlock()
	if ok && now.Sub(e.at) < updatesTTL && !now.Before(e.at) {
		return e.out, nil
	}
	out, err := m.readUpdates(ctx, env)
	if err != nil {
		return Updates{}, err
	}
	updatesCache.Lock()
	updatesCache.m[key] = updatesEntry{at: now, out: out}
	updatesCache.Unlock()
	return out, nil
}

func (m module) readUpdates(ctx context.Context, env core.Env) (Updates, error) {
	out := Updates{Upgradable: []Upgradable{}, Removals: []string{}, HeldBack: []Held{}}
	var err error
	if out.RefreshedAt, err = m.refreshedAt(); err != nil {
		return Updates{}, err
	}
	if out.FailedSources, err = m.failedSources(); err != nil {
		return Updates{}, err
	}
	audit, err := runOK(ctx, env, dpkgBin, platform.SystemDpkgAudit())
	if err != nil {
		return Updates{}, err
	}
	if strings.TrimSpace(string(audit)) != "" {
		out.DpkgInterrupted = true
		return out, nil
	}
	plain, err := aptRead(ctx, env, "dist-upgrade", nil, nil)
	if err != nil {
		return Updates{}, err
	}
	phased, err := aptRead(ctx, env, "dist-upgrade", phasedOpts, nil)
	if err != nil {
		return Updates{}, err
	}
	inPlain := map[string]bool{}
	for _, op := range plain.Ops {
		switch op.Kind {
		case "Inst":
			inPlain[op.Name] = true
			out.Upgradable = append(out.Upgradable, upgradable(op))
		case "Remv", "Purg":
			out.Removals = append(out.Removals, op.Name)
		}
	}
	held, err := heldPackages(ctx, env)
	if err != nil {
		return Updates{}, err
	}
	out.HeldBack = append(out.HeldBack, held...)
	for _, op := range phased.Ops {
		if op.Kind == "Inst" && !inPlain[op.Name] && !slices.ContainsFunc(held, func(h Held) bool { return h.Name == op.Name }) {
			out.HeldBack = append(out.HeldBack, Held{Name: op.Name, Version: op.To, Reason: "phased"})
		}
	}
	slices.SortFunc(out.Upgradable, func(a, b Upgradable) int { return strings.Compare(a.Name, b.Name) })
	slices.Sort(out.Removals)
	out.Removals = slices.Compact(out.Removals)
	slices.SortFunc(out.HeldBack, func(a, b Held) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// upgradable is op as an update; its origin is the security archive when apt
// names one, else the first archive.
func upgradable(op SimOp) Upgradable {
	u := Upgradable{Name: op.Name, From: op.From, To: op.To}
	for _, o := range op.Origins {
		if strings.HasSuffix(o, "-security") {
			u.Origin, u.Security = o, true
			return u
		}
	}
	if len(op.Origins) > 0 {
		u.Origin = op.Origins[0]
	}
	return u
}

// heldPackages are the apt-mark holds that have a newer candidate.
func heldPackages(ctx context.Context, env core.Env) ([]Held, error) {
	b, err := runOK(ctx, env, aptMark, platform.SystemAptMarkShowhold())
	if err != nil {
		return nil, err
	}
	names := parseShowhold(b)
	if len(names) == 0 {
		return nil, nil
	}
	policy, err := aptPolicy(ctx, env, names)
	if err != nil {
		return nil, err
	}
	var held []Held
	for _, n := range names {
		p := policy[n]
		if p.candidate != "" && p.candidate != p.installed {
			held = append(held, Held{Name: n, Version: p.candidate, Reason: "held"})
		}
	}
	return held, nil
}

// parseShowhold reads the package names of apt-mark showhold, skipping a line
// that is not a valid package name.
func parseShowhold(out []byte) []string {
	var names []string
	for line := range strings.Lines(string(out)) {
		if n := strings.TrimSpace(line); n != "" && namePattern.MatchString(n) {
			names = append(names, n)
		}
	}
	return names
}

type policyEntry struct{ installed, candidate string }

// aptPolicy reads the installed and candidate versions of names from
// apt-cache policy; "(none)" reads as "".
func aptPolicy(ctx context.Context, env core.Env, names []string) (map[string]policyEntry, error) {
	b, err := runOK(ctx, env, aptCache, platform.SystemAptCachePolicy(names))
	if err != nil {
		return nil, err
	}
	return parseAptPolicy(b), nil
}

// parseAptPolicy reads the Installed and Candidate lines of each package
// section of apt-cache policy.
func parseAptPolicy(b []byte) map[string]policyEntry {
	out := map[string]policyEntry{}
	var cur string
	for line := range strings.Lines(string(b)) {
		line = strings.TrimRight(line, "\n")
		if line != "" && line[0] != ' ' && strings.HasSuffix(line, ":") {
			cur = strings.TrimSuffix(line, ":")
			continue
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || cur == "" {
			continue
		}
		if value = strings.TrimSpace(value); value == "(none)" {
			value = ""
		}
		e := out[cur]
		switch key {
		case "Installed":
			e.installed = value
		case "Candidate":
			e.candidate = value
		}
		out[cur] = e
	}
	return out
}

// refreshedAt is when apt last refreshed its lists: the update-success stamp,
// else the lists directory; nil when neither exists.
func (m module) refreshedAt() (*string, error) {
	for _, p := range []string{refreshStamp, aptLists} {
		t, err := m.d.modTime(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("system: stat %s: %w", p, err)
		}
		s := t.UTC().Format(time.RFC3339)
		return &s, nil
	}
	return nil, nil
}

func (m module) failedSources() ([]string, error) {
	b, err := m.d.readFile(failedSourcesFile)
	if errors.Is(err, fs.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("system: read %s: %w", failedSourcesFile, err)
	}
	out := []string{}
	for line := range strings.Lines(string(b)) {
		if s := strings.TrimSpace(line); s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// runOK runs path with args and returns its standard output; a non-zero exit
// is an error carrying its standard error.
func runOK(ctx context.Context, env core.Env, path string, args []string) ([]byte, error) {
	res, err := env.Exec.Run(ctx, core.Cmd{Path: path, Args: args})
	if err != nil {
		return nil, fmt.Errorf("system: run %s: %w", path, err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("system: %s %s exited %d: %s", path, strings.Join(args, " "), res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return res.Stdout, nil
}
