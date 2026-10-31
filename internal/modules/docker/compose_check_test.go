package docker

import (
	"context"
	"database/sql"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const (
	ccDirMode  = fs.ModeDir | 0o755
	ccSimple   = `{"name":"shop","networks":{"default":{"name":"shop_default","ipam":{}}},"services":{"web":{"image":"nginx:1.27","networks":{"default":null}}}}`
	ccProvider = `{"name":"shop","services":{"web":{"provider":{"type":"model"}}}}`
	ccBuild    = `{"name":"shop","services":{"web":{"build":{"context":"/srv/shop"}}}}`
	ccOwnedNet = `{"name":"shop","networks":{"npm":{"name":"npm_net","external":true}},"services":{"web":{"image":"nginx:1.27","networks":{"npm":null}}}}`
)

// ccDiscover records a project through the labels its containers carry.
func ccDiscover(t *testing.T, h *dkHost, name, dir string, files, envFiles []string) projectRow {
	t.Helper()
	ctx := context.Background()
	var tables int
	if err := h.db.Read().QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'docker_projects'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables == 0 {
		// The host database of newDKHost holds only the core tables.
		h.db = pjDB(t)
	}
	sum := dockerapi.ContainerSummary{
		ID: dkID(name + "-seed"), Names: []string{"/" + name + "-web-1"},
		Labels: map[string]string{
			"com.docker.compose.project":                  name,
			"com.docker.compose.project.working_dir":      dir,
			"com.docker.compose.project.config_files":     strings.Join(files, ","),
			"com.docker.compose.project.environment_file": strings.Join(envFiles, ","),
		},
	}
	if err := discoverProjects(ctx, h.db, h.clock.Now(), []dockerapi.ContainerSummary{sum}); err != nil {
		t.Fatal(err)
	}
	row, ok, err := loadProject(ctx, h.db, name)
	if err != nil || !ok {
		t.Fatalf("loadProject(%s) = %v, %v", name, ok, err)
	}
	return row
}

// ccProject puts a valid project, the system plugin directory and the Compose
// plugin into the host's files, records the project, and returns its row. The
// project's directory is /srv/<name> with one Compose file and one env file,
// all owned by root.
func ccProject(t *testing.T, h *dkHost, name string) projectRow {
	t.Helper()
	dir := "/srv/" + name
	h.files["/srv"] = dkFile{Mode: ccDirMode}
	h.files[dir] = dkFile{Mode: ccDirMode}
	h.files[dir+"/compose.yaml"] = dkFile{Mode: 0o644, Data: []byte("services:\n  web:\n    image: nginx:1.27\n")}
	h.files[dir+"/.env"] = dkFile{Mode: 0o600, Data: []byte("TAG=1.27\n")}
	h.files[pluginDir] = dkFile{Mode: ccDirMode}
	h.files[pluginDir+"/docker-compose"] = dkFile{Mode: 0o755, Data: []byte("plugin")}
	return ccDiscover(t, h, name, dir, []string{dir + "/compose.yaml"}, []string{dir + "/.env"})
}

// ccConfig scripts the output of `config --format json` for a project.
func ccConfig(h *dkHost, p projectRow, profiles []string, stdout string) {
	h.results[strings.Join(composeArgs(p, profiles, "config", "--format", "json"), " ")] = core.CmdResult{Stdout: []byte(stdout)}
}

func ccRefused(t *testing.T, err error, code errcode.Code, detail ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("no error, want %s", code)
	}
	e := errcode.From(err)
	if e.Code != code {
		t.Fatalf("code %s (%s), want %s", e.Code, e.Detail, code)
	}
	for _, d := range detail {
		if !strings.Contains(e.Detail, d) {
			t.Errorf("detail %q does not contain %q", e.Detail, d)
		}
	}
	if !core.IsDefinite(err) {
		t.Errorf("a refusal that committed nothing must be definite: %v", err)
	}
}

func ccPrepare(h *dkHost, name string, profiles *[]string) (composeRun, error) {
	return h.dk().prepareCompose(context.Background(), h.env(""), ComposeInput{Project: name, Profiles: profiles})
}

func TestCCOwnedPaths(t *testing.T) {
	want := []core.OwnedPath{{Path: "/var/lib/darkpanel/agent/docker-config", Kind: core.OwnedManagedDir, Pattern: `^config\.json$`}}
	if got := ownedPaths(); !reflect.DeepEqual(got, want) {
		t.Errorf("ownedPaths = %+v, want %+v", got, want)
	}
	if dockerConfigDir != "/var/lib/darkpanel/agent/docker-config" || pluginDir != "/usr/libexec/docker/cli-plugins" {
		t.Errorf("directories %q and %q do not match the spec", dockerConfigDir, pluginDir)
	}
}

func TestCCComposeArgsFollowTheAllowlistedOrder(t *testing.T) {
	p := projectRow{Name: "shop", Directory: "/srv/shop", Files: []string{"/srv/shop/a.yaml", "/srv/shop/b.yaml"}, EnvFiles: []string{"/srv/shop/.env"}}
	tests := []struct {
		name     string
		p        projectRow
		profiles []string
		verb     []string
		want     []string
	}{
		{"everything", p, []string{"debug", "web"}, []string{"up", "-d", "--no-build"},
			[]string{"compose", "--project-directory", "/srv/shop", "-p", "shop", "-f", "/srv/shop/a.yaml", "-f", "/srv/shop/b.yaml",
				"--env-file", "/srv/shop/.env", "--profile", "debug", "--profile", "web", "up", "-d", "--no-build"}},
		{"no env files or profiles", projectRow{Name: "shop", Directory: "/srv/shop", Files: []string{"/srv/shop/a.yaml"}}, nil, []string{"config", "--format", "json"},
			[]string{"compose", "--project-directory", "/srv/shop", "-p", "shop", "-f", "/srv/shop/a.yaml", "config", "--format", "json"}},
		{"down", p, nil, []string{"down"},
			[]string{"compose", "--project-directory", "/srv/shop", "-p", "shop", "-f", "/srv/shop/a.yaml", "-f", "/srv/shop/b.yaml", "--env-file", "/srv/shop/.env", "down"}},
	}
	for _, tc := range tests {
		if got := composeArgs(tc.p, tc.profiles, tc.verb...); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

func TestCCPrepareReturnsTheRunAndReadsOnly(t *testing.T) {
	h := newDKHost(t)
	p := ccProject(t, h, "shop")
	ccConfig(h, p, nil, ccSimple)

	run, err := ccPrepare(h, "shop", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.Project.Name != "shop" || run.Hash == "" || run.Hash != h.dk().configHash(p) || len(run.Profiles) != 0 {
		t.Errorf("run = %+v, want project shop, the current config hash and no profiles", run)
	}
	if _, ok := run.Config.Services["web"]; !ok || run.Config.Name != "shop" {
		t.Errorf("run.Config = %+v, want the parsed configuration of project shop", run.Config)
	}
	wantCmd := core.Cmd{Path: "/usr/bin/docker", Args: composeArgs(p, nil, "config", "--format", "json")}
	if len(h.cmds) != 1 || !reflect.DeepEqual(h.cmds[0], wantCmd) {
		t.Errorf("commands %+v, want exactly %+v", h.cmds, wantCmd)
	}
	if len(h.managed) != 0 {
		t.Errorf("a plan wrote managed files: %v", h.managed)
	}
}

func TestCCPrepareUsesGivenProfilesInsteadOfTheStoredList(t *testing.T) {
	h := newDKHost(t)
	p := ccProject(t, h, "shop")
	ccConfig(h, p, []string{"debug"}, ccSimple)

	run, err := ccPrepare(h, "shop", &[]string{"debug"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(run.Profiles, []string{"debug"}) {
		t.Errorf("run.Profiles = %v, want [debug]", run.Profiles)
	}
	want := composeArgs(p, []string{"debug"}, "config", "--format", "json")
	if len(h.cmds) != 1 || !reflect.DeepEqual(h.cmds[0].Args, want) {
		t.Errorf("commands %+v, want one with %q", h.cmds, want)
	}
}

func TestCCPrepareUsesTheStoredProfilesAndShowsAChangedHash(t *testing.T) {
	h := newDKHost(t)
	ccProject(t, h, "shop")
	err := h.db.Write(context.Background(), func(tx *sql.Tx) error {
		return setProjectRun(context.Background(), tx, "shop", []string{"web"}, "oldhash", h.clock.Now().Add(-time.Hour))
	})
	if err != nil {
		t.Fatal(err)
	}
	p, ok, err := loadProject(context.Background(), h.db, "shop")
	if err != nil || !ok {
		t.Fatalf("loadProject = %v, %v", ok, err)
	}
	ccConfig(h, p, []string{"web"}, ccSimple)

	run, err := ccPrepare(h, "shop", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(run.Profiles, []string{"web"}) || run.Project.RunHash != "oldhash" || run.Hash == "oldhash" {
		t.Errorf("run = %+v, want the stored profiles, the stored run hash and a new hash", run)
	}
}

func TestCCPrepareRefusesMalformedInput(t *testing.T) {
	bad := func(p ...string) *[]string { return &p }
	tests := []struct {
		name     string
		project  string
		profiles *[]string
	}{
		{"empty project", "", nil},
		{"project with a space", "my shop", nil},
		{"project that looks like a flag", "-x", nil},
		{"empty profile", "shop", bad("")},
		{"profile that looks like a flag", "shop", bad("-x")},
		{"profile with a space", "shop", bad("a b")},
	}
	for _, tc := range tests {
		h := newDKHost(t)
		ccProject(t, h, "shop")
		_, err := ccPrepare(h, tc.project, tc.profiles)
		if got := ccCode(err); got != errcode.InvalidInput {
			t.Errorf("%s: code %s, want %s", tc.name, got, errcode.InvalidInput)
		}
		if len(h.cmds) != 0 {
			t.Errorf("%s ran %v", tc.name, h.cmds)
		}
	}
}

func ccCode(err error) errcode.Code {
	if err == nil {
		return ""
	}
	return errcode.From(err).Code
}

func TestCCPrepareRefusesUnknownAndReservedProjects(t *testing.T) {
	h := newDKHost(t)
	h.db = pjDB(t)
	_, err := ccPrepare(h, "nosuch", nil)
	ccRefused(t, err, errcode.DockerComposeUnavailable, "nosuch")

	ccProject(t, h, "darkpanel-npm")
	_, err = ccPrepare(h, "darkpanel-npm", nil)
	ccRefused(t, err, errcode.DockerOwned, "darkpanel-npm")
	if len(h.cmds) != 0 {
		t.Errorf("a reserved project ran %v", h.cmds)
	}
}

func TestCCPrepareRefusesUnsafePaths(t *testing.T) {
	const dir = "/srv/shop"
	tests := []struct {
		name   string
		mutate func(h *dkHost)
		detail []string // empty: accepted
	}{
		{"valid", func(_ *dkHost) {}, nil},
		{"directory is a symlink", func(h *dkHost) { h.files[dir] = dkFile{Symlink: true, Mode: 0o777} }, []string{dir, "symlink"}},
		{"parent is a symlink", func(h *dkHost) { h.files["/srv"] = dkFile{Symlink: true, Mode: 0o777} }, []string{"/srv", "symlink"}},
		{"compose file is a symlink", func(h *dkHost) { h.files[dir+"/compose.yaml"] = dkFile{Symlink: true, Mode: 0o777} }, []string{dir + "/compose.yaml", "symlink"}},
		{"compose file is missing", func(h *dkHost) { delete(h.files, dir+"/compose.yaml") }, []string{dir + "/compose.yaml", "does not exist"}},
		{"env file is missing", func(h *dkHost) { delete(h.files, dir+"/.env") }, []string{dir + "/.env", "does not exist"}},
		{"compose file is a directory", func(h *dkHost) { h.files[dir+"/compose.yaml"] = dkFile{Mode: ccDirMode} }, []string{dir + "/compose.yaml", "not a regular file"}},
		{"directory is a file", func(h *dkHost) { h.files[dir] = dkFile{Mode: 0o644} }, []string{dir, "not a directory"}},
		{"file owned by a stranger", func(h *dkHost) {
			h.files[dir+"/compose.yaml"] = dkFile{Mode: 0o644, UID: 1001, Data: []byte("services: {}\n")}
		}, []string{dir + "/compose.yaml", "owned by"}},
		{"files owned by the directory's owner", func(h *dkHost) {
			h.files[dir] = dkFile{Mode: ccDirMode, UID: 1000}
			h.files[dir+"/compose.yaml"] = dkFile{Mode: 0o644, UID: 1000, Data: []byte("services: {}\n")}
			h.files[dir+"/.env"] = dkFile{Mode: 0o600, UID: 1000, Data: []byte("A=1\n")}
		}, nil},
		{"file owned by neither root nor the directory's owner", func(h *dkHost) {
			h.files[dir] = dkFile{Mode: ccDirMode, UID: 1000}
			h.files[dir+"/compose.yaml"] = dkFile{Mode: 0o644, UID: 1001, Data: []byte("services: {}\n")}
		}, []string{dir + "/compose.yaml", "owned by"}},
		{"directory group-writable", func(h *dkHost) { h.files[dir] = dkFile{Mode: fs.ModeDir | 0o775} }, []string{dir, "writable"}},
		{"parent other-writable", func(h *dkHost) { h.files["/srv"] = dkFile{Mode: fs.ModeDir | 0o777} }, []string{"/srv", "writable"}},
		{"parent sticky and root-owned is accepted", func(h *dkHost) {
			h.files["/srv"] = dkFile{Mode: fs.ModeDir | fs.ModeSticky | 0o777}
		}, nil},
		{"parent sticky but user-owned", func(h *dkHost) {
			h.files["/srv"] = dkFile{Mode: fs.ModeDir | fs.ModeSticky | 0o777, UID: 1000}
		}, []string{"/srv", "writable"}},
		{"directory sticky and other-writable", func(h *dkHost) {
			h.files[dir] = dkFile{Mode: fs.ModeDir | fs.ModeSticky | 0o777}
		}, []string{dir, "writable"}},
		{"compose file group-writable", func(h *dkHost) {
			h.files[dir+"/compose.yaml"] = dkFile{Mode: 0o664, Data: []byte("services: {}\n")}
		}, []string{dir + "/compose.yaml", "writable"}},
		{"env file other-writable", func(h *dkHost) {
			h.files[dir+"/.env"] = dkFile{Mode: 0o606, Data: []byte("A=1\n")}
		}, []string{dir + "/.env", "writable"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newDKHost(t)
			p := ccProject(t, h, "shop")
			tc.mutate(h)
			ccConfig(h, p, nil, ccSimple)
			_, err := ccPrepare(h, "shop", nil)
			if tc.detail == nil {
				if err != nil {
					t.Fatalf("prepareCompose = %v, want success", err)
				}
				return
			}
			ccRefused(t, err, errcode.DockerComposeUnavailable, tc.detail...)
			if len(h.cmds) != 0 {
				t.Errorf("an unsafe project ran %v", h.cmds)
			}
		})
	}
}

func TestCCPrepareRefusesRelativePaths(t *testing.T) {
	h := newDKHost(t)
	ccProject(t, h, "shop")
	ccDiscover(t, h, "rel", "srv/rel", []string{"srv/rel/compose.yaml"}, nil)
	_, err := ccPrepare(h, "rel", nil)
	ccRefused(t, err, errcode.DockerComposeUnavailable, "srv/rel", "not absolute")
}

func TestCCPrepareRefusesAProjectWithNoComposeFile(t *testing.T) {
	h := newDKHost(t)
	ccProject(t, h, "shop")
	p := ccDiscover(t, h, "bare", "/srv/shop", nil, nil)
	if len(p.Files) != 0 {
		t.Fatalf("recorded files %q, want none", p.Files)
	}
	_, err := ccPrepare(h, "bare", nil)
	ccRefused(t, err, errcode.DockerComposeUnavailable, "bare", "no Compose file")
	if len(h.cmds) != 0 {
		t.Errorf("a project with no Compose file ran %v", h.cmds)
	}
}

func TestCCPrepareChecksTheDockerConfigDirectory(t *testing.T) {
	cfgPath := dockerConfigDir + "/config.json"
	root := dkFile{Mode: fs.ModeDir | 0o700}
	tests := []struct {
		name   string
		mutate func(h *dkHost)
		detail []string // empty: accepted
	}{
		{"absent", func(_ *dkHost) {}, nil},
		{"empty config", func(h *dkHost) {
			h.files[dockerConfigDir] = root
			h.files[cfgPath] = dkFile{Mode: 0o600, Data: []byte("{}\n")}
		}, nil},
		{"config with other settings", func(h *dkHost) {
			h.files[dockerConfigDir] = root
			h.files[cfgPath] = dkFile{Mode: 0o600, Data: []byte(`{"auths":{},"psFormat":"x"}`)}
		}, nil},
		{"credsStore", func(h *dkHost) {
			h.files[dockerConfigDir] = root
			h.files[cfgPath] = dkFile{Mode: 0o600, Data: []byte(`{"credsStore":"desktop"}`)}
		}, []string{"credsStore"}},
		{"credHelpers", func(h *dkHost) {
			h.files[dockerConfigDir] = root
			h.files[cfgPath] = dkFile{Mode: 0o600, Data: []byte(`{"credHelpers":{"gcr.io":"gcloud"}}`)}
		}, []string{"credHelpers"}},
		{"cliPluginsExtraDirs", func(h *dkHost) {
			h.files[dockerConfigDir] = root
			h.files[cfgPath] = dkFile{Mode: 0o600, Data: []byte(`{"cliPluginsExtraDirs":["/tmp/x"]}`)}
		}, []string{"cliPluginsExtraDirs"}},
		{"config that is not JSON", func(h *dkHost) {
			h.files[dockerConfigDir] = root
			h.files[cfgPath] = dkFile{Mode: 0o600, Data: []byte("not json")}
		}, []string{cfgPath}},
		{"cli-plugins entry", func(h *dkHost) {
			h.files[dockerConfigDir] = root
			h.files[dockerConfigDir+"/cli-plugins"] = dkFile{Mode: ccDirMode}
		}, []string{"cli-plugins"}},
		{"directory owned by a user", func(h *dkHost) {
			h.files[dockerConfigDir] = dkFile{Mode: fs.ModeDir | 0o700, UID: 1000}
		}, []string{dockerConfigDir, "owned by"}},
		{"directory is a symlink", func(h *dkHost) {
			h.files[dockerConfigDir] = dkFile{Symlink: true, Mode: 0o777}
		}, []string{dockerConfigDir, "symlink"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newDKHost(t)
			p := ccProject(t, h, "shop")
			ccConfig(h, p, nil, ccSimple)
			tc.mutate(h)
			_, err := ccPrepare(h, "shop", nil)
			if tc.detail == nil {
				if err != nil {
					t.Fatalf("prepareCompose = %v, want success", err)
				}
				return
			}
			ccRefused(t, err, errcode.DockerComposeUnavailable, tc.detail...)
			if len(h.cmds) != 0 {
				t.Errorf("a refused docker-config directory still ran %v", h.cmds)
			}
		})
	}
}

func TestCCPrepareChecksThePluginDirectoryAndThePlugin(t *testing.T) {
	plugin := pluginDir + "/docker-compose"
	tests := []struct {
		name   string
		mutate func(h *dkHost)
		detail []string
	}{
		{"plugin directory missing", func(h *dkHost) { delete(h.files, pluginDir) }, []string{pluginDir, "does not exist"}},
		{"plugin directory group-writable", func(h *dkHost) { h.files[pluginDir] = dkFile{Mode: fs.ModeDir | 0o775} }, []string{pluginDir, "writable"}},
		{"plugin directory owned by a user", func(h *dkHost) { h.files[pluginDir] = dkFile{Mode: ccDirMode, UID: 1000} }, []string{pluginDir, "owned by"}},
		{"plugin directory is a symlink", func(h *dkHost) { h.files[pluginDir] = dkFile{Symlink: true, Mode: 0o777} }, []string{pluginDir, "symlink"}},
		{"plugin missing", func(h *dkHost) { delete(h.files, plugin) }, []string{plugin, "does not exist"}},
		{"plugin owned by a user", func(h *dkHost) { h.files[plugin] = dkFile{Mode: 0o755, UID: 1000} }, []string{plugin, "owned by"}},
		{"plugin writable by others", func(h *dkHost) { h.files[plugin] = dkFile{Mode: 0o757} }, []string{plugin, "writable"}},
		{"plugin is a symlink", func(h *dkHost) { h.files[plugin] = dkFile{Symlink: true, Mode: 0o777} }, []string{plugin, "symlink"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newDKHost(t)
			p := ccProject(t, h, "shop")
			ccConfig(h, p, nil, ccSimple)
			tc.mutate(h)
			_, err := ccPrepare(h, "shop", nil)
			ccRefused(t, err, errcode.DockerComposeUnavailable, tc.detail...)
			if len(h.cmds) != 0 {
				t.Errorf("a refused plugin still ran %v", h.cmds)
			}
		})
	}
}

func TestCCPrepareRefusesProjectsWithOwnedContainers(t *testing.T) {
	h := newDKHost(t)
	p := ccProject(t, h, "shop")
	ccConfig(h, p, nil, ccSimple)
	h.docker.put(dkContainer("other-web-1", dkLabels(map[string]string{
		"com.docker.compose.project": "other", core.LabelModule: "npm",
	})))
	if _, err := ccPrepare(h, "shop", nil); err != nil {
		t.Fatalf("an owned container of another project must not matter: %v", err)
	}

	h.docker.put(dkContainer("shop-db-1", dkLabels(map[string]string{
		"com.docker.compose.project": "shop", core.LabelModule: "npm",
	})))
	h.cmds = nil
	_, err := ccPrepare(h, "shop", nil)
	ccRefused(t, err, errcode.DockerOwned, "shop-db-1", "npm")
	if len(h.cmds) != 0 {
		t.Errorf("a project with an owned container still ran %v", h.cmds)
	}
}

func TestCCPrepareReportsAFailedConfigWithoutLeakingOutput(t *testing.T) {
	long := strings.Repeat("Z", 500)
	tests := []struct {
		name   string
		res    core.CmdResult
		detail []string
		absent []string
	}{
		{"non-zero exit", core.CmdResult{ExitCode: 1, Stderr: []byte("warning: first line\nservice \"web\" refers to undefined network front\n\n")},
			[]string{"exit status 1", `service "web" refers to undefined network front`}, []string{"first line"}},
		{"long stderr line", core.CmdResult{ExitCode: 15, Stderr: []byte(long + "\n")}, []string{"exit status 15", "ZZZZ"}, nil},
		{"unreadable output", core.CmdResult{Stdout: []byte(`{"name":"shop","services":"SECRET_TOKEN_VALUE"}`)},
			[]string{"resolved configuration"}, []string{"SECRET_TOKEN_VALUE"}},
		{"not JSON", core.CmdResult{Stdout: []byte("SECRET_TOKEN_VALUE")}, []string{"resolved configuration"}, []string{"SECRET_TOKEN_VALUE"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newDKHost(t)
			p := ccProject(t, h, "shop")
			h.results[strings.Join(composeArgs(p, nil, "config", "--format", "json"), " ")] = tc.res
			_, err := ccPrepare(h, "shop", nil)
			ccRefused(t, err, errcode.DockerComposeUnavailable, tc.detail...)
			d := errcode.From(err).Detail
			for _, a := range tc.absent {
				if strings.Contains(d, a) {
					t.Errorf("detail %q contains %q", d, a)
				}
			}
			if n := strings.Count(d, "Z"); n > 300 {
				t.Errorf("detail carries %d bytes of stderr, want at most 300", n)
			}
		})
	}
}

func TestCCPrepareAppliesTheConfigurationRefusals(t *testing.T) {
	tests := []struct {
		name   string
		config string
		setup  func(h *dkHost)
		code   errcode.Code // "" is accepted
		detail []string
	}{
		{"simple", ccSimple, nil, "", nil},
		{"provider service", ccProvider, nil, errcode.DockerComposeUnavailable, []string{"web", "provider"}},
		{"build without a local image", ccBuild, nil, errcode.DockerComposeUnavailable, []string{"web", "build"}},
		{"build with the built image present", ccBuild, func(h *dkHost) { h.docker.putImage(dkImage("shop-web", 1)) }, "", nil},
		{"external owned network", ccOwnedNet, func(h *dkHost) {
			h.docker.putNetwork(dkNetwork("npm_net", func(n *dockerapi.Network) { n.Labels = map[string]string{core.LabelModule: "npm"} }))
		}, errcode.DockerOwned, []string{"npm_net", "npm"}},
		{"external network of nobody", ccOwnedNet, func(h *dkHost) { h.docker.putNetwork(dkNetwork("npm_net")) }, "", nil},
		{"engine down while the owners are read", ccOwnedNet, func(h *dkHost) { h.docker.fail("ListNetworks", dockerapi.ErrUnavailable) }, errcode.DockerEngineUnavailable, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newDKHost(t)
			p := ccProject(t, h, "shop")
			ccConfig(h, p, nil, tc.config)
			if tc.setup != nil {
				tc.setup(h)
			}
			_, err := ccPrepare(h, "shop", nil)
			if tc.code == "" {
				if err != nil {
					t.Fatalf("prepareCompose = %v, want success", err)
				}
				return
			}
			if tc.code == errcode.DockerEngineUnavailable {
				if got := ccCode(err); got != tc.code {
					t.Errorf("code %s, want %s", got, tc.code)
				}
				return
			}
			ccRefused(t, err, tc.code, tc.detail...)
		})
	}
}

func TestCCPlanComposeRendersWhatTheRunDoes(t *testing.T) {
	old, now := strings.Repeat("a", 64), strings.Repeat("b", 64)
	project := projectRow{Name: "shop", Directory: "/srv/shop", Files: []string{"/srv/shop/a.yaml", "/srv/shop/b.yaml"}, EnvFiles: []string{"/srv/shop/.env"}, RunHash: old}
	head := []core.DiffLine{
		{Op: " ", Text: "runs the project's Compose file as root"},
		{Op: "+", Text: "docker compose up for project shop"},
		{Op: " ", Text: "directory /srv/shop"},
		{Op: " ", Text: "file /srv/shop/a.yaml"},
		{Op: " ", Text: "file /srv/shop/b.yaml"},
		{Op: " ", Text: "env file /srv/shop/.env"},
	}
	withProfile := append(ccClone(head), core.DiffLine{Op: " ", Text: "profile debug"})
	tests := []struct {
		name string
		run  composeRun
		want []core.DiffLine
	}{
		{"hash unchanged", composeRun{Project: project, Hash: old}, head},
		{"never run before", composeRun{Project: func() projectRow { p := project; p.RunHash = ""; return p }(), Hash: now}, head},
		{"profiles", composeRun{Project: project, Hash: old, Profiles: []string{"debug"}}, withProfile},
		{"hash changed", composeRun{Project: project, Hash: now}, append(ccClone(head),
			core.DiffLine{Op: "-", Text: "config hash aaaaaaaaaaaa"}, core.DiffLine{Op: "+", Text: "config hash bbbbbbbbbbbb"})},
	}
	for _, tc := range tests {
		if got := planCompose(tc.run, "up"); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s\n got %+v\nwant %+v", tc.name, got, tc.want)
		}
	}
}

func ccClone(l []core.DiffLine) []core.DiffLine { return append([]core.DiffLine(nil), l...) }
