package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/docker/compose"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const (
	// dockerConfigDir is the DOCKER_CONFIG every Compose run uses, so no
	// user's credential helpers or CLI plugins take part in a root run.
	dockerConfigDir = "/var/lib/darkpanel/agent/docker-config"
	// pluginDir is the system directory of the Docker CLI plugins.
	pluginDir = "/usr/libexec/docker/cli-plugins"
)

const (
	composeDockerPath = "/usr/bin/docker"
	// composeStderrMax bounds the stderr a refusal quotes.
	composeStderrMax = 300
)

var (
	composeProjectPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	composeProfilePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
	// composeConfigMembers are the config.json members that make the Docker
	// CLI run a program of the file's choosing.
	composeConfigMembers = []string{"credsStore", "credHelpers", "cliPluginsExtraDirs"}
)

// ownedPaths are the host paths the module creates.
func ownedPaths() []core.OwnedPath {
	return []core.OwnedPath{{Path: dockerConfigDir, Kind: core.OwnedManagedDir, Pattern: `^config\.json$`}}
}

// composeRun is a checked project ready for a Compose run.
type composeRun struct {
	Project  projectRow
	Profiles []string // the input's, else the stored list
	Hash     string   // configHash now
	Config   compose.Config
}

// composeArgs builds the allowlisted argument vector: compose
// --project-directory, -p, every -f, every --env-file, every --profile, then
// verb.
func composeArgs(p projectRow, profiles []string, verb ...string) []string {
	args := []string{"compose", "--project-directory", p.Directory, "-p", p.Name}
	for _, f := range p.Files {
		args = append(args, "-f", f)
	}
	for _, f := range p.EnvFiles {
		args = append(args, "--env-file", f)
	}
	for _, name := range profiles {
		args = append(args, "--profile", name)
	}
	return append(args, verb...)
}

// refuse is a refusal that committed nothing.
func refuse(code errcode.Code, format string, args ...any) error {
	return core.Definite(errcode.New(code, fmt.Sprintf(format, args...)))
}

func composeUnavailable(format string, args ...any) error {
	return refuse(errcode.DockerComposeUnavailable, format, args...)
}

func composeInvalid(path, msg string) error {
	return core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: msg,
		Fields: []errcode.FieldError{{Path: path, Message: msg}}})
}

// prepareCompose runs every check of spec 3 "Validation" for a plan and again
// at apply: the stored project, absolute existing paths owned by root or the
// directory's owner with no symlink in any component and, except for
// root-owned sticky ancestors, none writable by group or others, the docker-config
// directory (absent, or holding at most a config.json without credsStore or
// credHelpers and no cli-plugins entry), the plugin directory and
// docker-compose root-owned and not group- or other-writable, current
// containers of the project not owned, `config --format json` run through
// env.Exec and parsed, and compose.Validate. Failures are
// docker.compose_unavailable or docker.owned with the reason.
func (k dk) prepareCompose(ctx context.Context, env core.Env, in ComposeInput) (composeRun, error) {
	if !composeProjectPattern.MatchString(in.Project) {
		return composeRun{}, composeInvalid("/project", "project must be lowercase letters, digits, - and _, starting with a letter or digit")
	}
	if in.Profiles != nil {
		for i, name := range *in.Profiles {
			if !composeProfilePattern.MatchString(name) {
				return composeRun{}, composeInvalid("/profiles/"+strconv.Itoa(i), "profile must be letters, digits, ., - and _, starting with a letter or digit")
			}
		}
	}
	if strings.HasPrefix(in.Project, "darkpanel") {
		return composeRun{}, refuse(errcode.DockerOwned, "project %s is reserved for Darkpanel", in.Project)
	}

	row, ok, err := loadProject(ctx, env.DB, in.Project)
	if err != nil {
		return composeRun{}, err
	}
	if !ok {
		return composeRun{}, composeUnavailable("project %s is not known on this host; start it once so that discovery records it", in.Project)
	}

	if err := k.checkProjectPaths(row); err != nil {
		return composeRun{}, err
	}
	hash := k.configHash(row)
	if hash == "" {
		return composeRun{}, composeUnavailable("the files of project %s cannot be read", row.Name)
	}
	if err := k.checkDockerConfig(); err != nil {
		return composeRun{}, err
	}
	if err := k.checkPlugins(); err != nil {
		return composeRun{}, err
	}
	if err := k.checkProjectContainers(ctx, row.Name); err != nil {
		return composeRun{}, err
	}

	profiles := row.Profiles
	if in.Profiles != nil {
		profiles = *in.Profiles
	}
	profiles = slices.Clone(profiles)
	if profiles == nil {
		profiles = []string{}
	}

	res, err := env.Exec.Run(ctx, core.Cmd{Path: composeDockerPath, Args: composeArgs(row, profiles, "config", "--format", "json")})
	if err != nil {
		return composeRun{}, fmt.Errorf("docker: run compose config: %w", err)
	}
	if res.ExitCode != 0 {
		msg := "docker compose config failed with exit status " + strconv.Itoa(res.ExitCode)
		if line := composeLastLine(res.Stderr); line != "" {
			msg += ": " + line
		}
		return composeRun{}, composeUnavailable("%s", msg)
	}
	cfg, err := compose.Parse(res.Stdout)
	if err != nil {
		return composeRun{}, core.Definite(errcode.Wrap(errcode.DockerComposeUnavailable, err,
			"the resolved configuration of project "+row.Name+" could not be read"))
	}

	if err := k.validateConfig(ctx, row.Name, cfg); err != nil {
		return composeRun{}, err
	}
	return composeRun{Project: row, Profiles: profiles, Hash: hash, Config: cfg}, nil
}

// checkProjectPaths checks the project's directory, Compose files and env
// files. Compose runs as root and reads these files, so a path through a
// symlink, or a file another user can replace, would let that user choose
// what root runs.
func (k dk) checkProjectPaths(p projectRow) error {
	if len(p.Files) == 0 {
		return composeUnavailable("project %s records no Compose file", p.Name)
	}
	dir, err := k.composeLstatPath(p.Directory)
	if err != nil {
		return err
	}
	if !dir.IsDir() {
		return composeUnavailable("%s is not a directory", p.Directory)
	}
	dirOwner, err := composeOwner(p.Directory, dir)
	if err != nil {
		return err
	}
	for _, path := range append(slices.Clone(p.Files), p.EnvFiles...) {
		info, err := k.composeLstatPath(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return composeUnavailable("%s is not a regular file", path)
		}
		uid, err := composeOwner(path, info)
		if err != nil {
			return err
		}
		if uid != 0 && uid != dirOwner {
			return composeUnavailable("%s is owned by uid %d, not root or the directory's owner", path, uid)
		}
	}
	return nil
}

// composeLstatPath refuses a path that is not absolute and clean, has a
// missing or symlinked component, or has a component its group or others can
// write, and returns the path's own information. A writable ancestor would
// let another user rename the project away and put their own in its place;
// only a root-owned sticky ancestor, such as /tmp, prevents that.
func (k dk) composeLstatPath(path string) (fs.FileInfo, error) {
	if !filepath.IsAbs(path) {
		return nil, composeUnavailable("%s is not absolute", path)
	}
	if filepath.Clean(path) != path {
		return nil, composeUnavailable("%s is not a clean path", path)
	}
	prefixes := []string{"/"}
	if path != "/" {
		prefixes = nil
		for i := 1; i <= len(path); i++ {
			if i == len(path) || path[i] == '/' {
				prefixes = append(prefixes, path[:i])
			}
		}
	}
	var info fs.FileInfo
	for i, prefix := range prefixes {
		var err error
		info, err = k.d.lstat(prefix)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, composeUnavailable("%s does not exist", prefix)
		}
		if err != nil {
			return nil, fmt.Errorf("docker: check %s: %w", prefix, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return nil, composeUnavailable("%s is a symlink", prefix)
		}
		if info.Mode().Perm()&0o022 == 0 {
			continue
		}
		if i < len(prefixes)-1 && info.Mode()&fs.ModeSticky != 0 {
			uid, err := composeOwner(prefix, info)
			if err != nil {
				return nil, err
			}
			if uid == 0 {
				continue
			}
		}
		return nil, composeUnavailable("%s is writable by its group or others", prefix)
	}
	return info, nil
}

// composeOwner is the uid that owns path.
func composeOwner(path string, info fs.FileInfo) (uint32, error) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, composeUnavailable("%s has no readable owner", path)
	}
	return st.Uid, nil
}

// checkDockerConfig checks the DOCKER_CONFIG of the runs. It may be absent,
// because the first run creates it; a credential helper, a plugin directory
// or a cli-plugins entry there would make the root CLI run another program.
func (k dk) checkDockerConfig() error {
	info, err := k.d.lstat(dockerConfigDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("docker: check %s: %w", dockerConfigDir, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return composeUnavailable("%s is a symlink", dockerConfigDir)
	}
	if !info.IsDir() {
		return composeUnavailable("%s is not a directory", dockerConfigDir)
	}
	uid, err := composeOwner(dockerConfigDir, info)
	if err != nil {
		return err
	}
	if uid != 0 {
		return composeUnavailable("%s is owned by uid %d, not root", dockerConfigDir, uid)
	}

	plugins := dockerConfigDir + "/cli-plugins"
	_, err = k.d.lstat(plugins)
	if err == nil {
		return composeUnavailable("%s must not exist", plugins)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("docker: check %s: %w", plugins, err)
	}

	cfgPath := dockerConfigDir + "/config.json"
	data, err := k.d.readFile(cfgPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return core.Definite(errcode.Wrap(errcode.DockerComposeUnavailable, err, cfgPath+" cannot be read"))
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil || members == nil {
		return composeUnavailable("%s is not a JSON object", cfgPath)
	}
	for _, name := range composeConfigMembers {
		for key := range members {
			// The Docker CLI decodes config.json with encoding/json, which
			// matches member names without regard to case.
			if strings.EqualFold(key, name) {
				return composeUnavailable("%s must not set %s", cfgPath, name)
			}
		}
	}
	return nil
}

// checkPlugins checks the plugin directory and the Compose plugin, which the
// root CLI runs: only root may be able to replace either.
func (k dk) checkPlugins() error {
	if err := k.composeSystemPath(pluginDir, true, ""); err != nil {
		return err
	}
	return k.composeSystemPath(pluginDir+"/docker-compose", false, "; install the Docker Compose plugin")
}

func (k dk) composeSystemPath(path string, dir bool, missingHint string) error {
	info, err := k.d.lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return composeUnavailable("%s does not exist%s", path, missingHint)
	}
	if err != nil {
		return fmt.Errorf("docker: check %s: %w", path, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return composeUnavailable("%s is a symlink", path)
	}
	if dir && !info.IsDir() {
		return composeUnavailable("%s is not a directory", path)
	}
	if !dir && !info.Mode().IsRegular() {
		return composeUnavailable("%s is not a regular file", path)
	}
	uid, err := composeOwner(path, info)
	if err != nil {
		return err
	}
	if uid != 0 {
		return composeUnavailable("%s is owned by uid %d, not root", path, uid)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return composeUnavailable("%s is writable by its group or others", path)
	}
	return nil
}

// checkProjectContainers refuses a project one of whose current containers
// another module manages.
func (k dk) checkProjectContainers(ctx context.Context, project string) error {
	cs, err := k.d.docker.ListContainers(ctx, dockerapi.ListContainersOptions{
		All:     true,
		Filters: dockerapi.Filters{"label": {projectLabelName + "=" + project}},
	})
	if err != nil {
		return engineError("project", project, err)
	}
	for _, c := range cs {
		// The daemon's filter is not relied on.
		if c.Labels[projectLabelName] != project {
			continue
		}
		name := c.ID
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		if err := refuseOwned("container", name, c.Labels); err != nil {
			return err
		}
	}
	return nil
}

// validateConfig applies compose.Validate with the Engine's answers. An
// Engine error is answered as absent and returned afterwards in preference to
// Validate's result, so an outage neither passes nor fails a project.
func (k dk) validateConfig(ctx context.Context, project string, cfg compose.Config) error {
	var (
		engineErr        error
		errKind, errName string
	)
	remember := func(kind, name string, err error) {
		if engineErr == nil {
			engineErr, errKind, errName = err, kind, name
		}
	}
	facts := compose.Facts{
		ImagePresent: func(ref string) bool {
			_, err := k.d.docker.InspectImage(ctx, ref)
			if err != nil && !dockerapi.IsNotFound(err) {
				remember("image", ref, err)
			}
			return err == nil
		},
		Owner: func(kind, name string) string {
			labels, err := k.composeObjectLabels(ctx, kind, name)
			if err != nil {
				remember(kind, name, err)
				return ""
			}
			return ownerOf(labels)
		},
	}
	err := compose.Validate(project, cfg, facts)
	if engineErr != nil {
		return engineError(errKind, errName, engineErr)
	}
	return core.Definite(err)
}

// composeObjectLabels returns the labels of the Engine object of kind named
// name, nil when there is none.
func (k dk) composeObjectLabels(ctx context.Context, kind, name string) (map[string]string, error) {
	switch kind {
	case "container":
		cs, err := k.d.docker.ListContainers(ctx, dockerapi.ListContainersOptions{All: true})
		if err != nil {
			return nil, err
		}
		for _, c := range cs {
			if c.ID == name || slices.Contains(c.Names, "/"+name) {
				return c.Labels, nil
			}
		}
		return nil, nil
	case "network":
		ns, err := k.d.docker.ListNetworks(ctx, dockerapi.Filters{"name": {name}})
		if err != nil {
			return nil, err
		}
		for _, n := range ns {
			if n.Name == name {
				return n.Labels, nil
			}
		}
		return nil, nil
	case "volume":
		v, err := k.d.docker.InspectVolume(ctx, name)
		if dockerapi.IsNotFound(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return v.Labels, nil
	default:
		return nil, fmt.Errorf("docker: no owner lookup for %s %s", kind, name)
	}
}

// composeLastLine is the last non-empty line of a program's stderr, cut to at
// most composeStderrMax bytes on a character boundary.
func composeLastLine(stderr []byte) string {
	lines := bytes.Split(stderr, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		if len(line) > composeStderrMax {
			line = line[:composeStderrMax]
			for len(line) > 0 && !utf8.Valid(line) {
				line = line[:len(line)-1]
			}
		}
		return string(line)
	}
	return ""
}

// planCompose renders the diff every Compose action shows: "runs the
// project's Compose file as root", the verb, the files, the profiles, and the
// hash change when HashChanged.
func planCompose(r composeRun, verb string) []core.DiffLine {
	p := r.Project
	lines := []core.DiffLine{
		{Op: " ", Text: "runs the project's Compose file as root"},
		{Op: "+", Text: "docker compose " + verb + " for project " + p.Name},
		{Op: " ", Text: "directory " + p.Directory},
	}
	for _, f := range p.Files {
		lines = append(lines, core.DiffLine{Op: " ", Text: "file " + f})
	}
	for _, f := range p.EnvFiles {
		lines = append(lines, core.DiffLine{Op: " ", Text: "env file " + f})
	}
	for _, name := range r.Profiles {
		lines = append(lines, core.DiffLine{Op: " ", Text: "profile " + name})
	}
	if p.RunHash != "" && p.RunHash != r.Hash {
		lines = append(lines,
			core.DiffLine{Op: "-", Text: "config hash " + composeShortHash(p.RunHash)},
			core.DiffLine{Op: "+", Text: "config hash " + composeShortHash(r.Hash)})
	}
	return lines
}

func composeShortHash(h string) string { return h[:min(12, len(h))] }
