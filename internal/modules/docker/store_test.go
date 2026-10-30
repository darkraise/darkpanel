package docker

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/docker/compose"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// pjDB is an agent database with the core and the docker migrations.
func pjDB(t *testing.T) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	res, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideAgent, BinaryVersion: "test", BackupRoot: filepath.Join(t.TempDir(), "backups"),
		Keep: 5, Now: time.Now(),
	}, set, []store.MigrationSet{{Module: ID, FS: agentMigrations()}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) > 0 {
		t.Fatalf("docker migrations failed: %v", res.Failed[0].Err)
	}
	return db
}

var pjNow = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

// pjSummary is a container of a Compose project as the Engine lists it; an empty
// label value leaves the label out.
func pjSummary(name, project, dir, files, envFiles string) dockerapi.ContainerSummary {
	labels := map[string]string{}
	for k, v := range map[string]string{
		"com.docker.compose.project":                  project,
		"com.docker.compose.project.working_dir":      dir,
		"com.docker.compose.project.config_files":     files,
		"com.docker.compose.project.environment_file": envFiles,
	} {
		if v != "" {
			labels[k] = v
		}
	}
	return dockerapi.ContainerSummary{ID: dkID(name), Names: []string{"/" + name}, State: "running", Labels: labels}
}

func TestPjMigrationCreatesTheTable(t *testing.T) {
	db := pjDB(t)
	rows, err := db.Read().Query(`SELECT name FROM pragma_table_info('docker_projects') ORDER BY cid`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, c)
	}
	want := []string{"name", "directory", "files_json", "env_files_json", "profiles_json", "run_hash", "first_seen", "last_run"}
	if !reflect.DeepEqual(cols, want) {
		t.Errorf("columns %v, want %v", cols, want)
	}
	var strict int
	if err := db.Read().QueryRow(`SELECT strict FROM pragma_table_list('docker_projects')`).Scan(&strict); err != nil || strict != 1 {
		t.Errorf("strict = %d (%v), want a STRICT table", strict, err)
	}
}

func TestPjDiscoverProjects(t *testing.T) {
	ctx := context.Background()
	db := pjDB(t)
	cs := []dockerapi.ContainerSummary{
		pjSummary("blog-web", "blog", "/srv/blog", "/srv/blog/compose.yaml,/srv/blog/compose.prod.yaml", "/srv/blog/.env"),
		pjSummary("blog-db", "blog", "/srv/blog", "/srv/blog/compose.yaml,/srv/blog/compose.prod.yaml", "/srv/blog/.env"),
		pjSummary("shop-web", "shop", "/srv/shop", "/srv/shop/compose.yaml", ""),
		pjSummary("loner", "", "", "", ""),
		pjSummary("half", "half", "", "", ""),
	}
	if err := discoverProjects(ctx, db, pjNow, cs); err != nil {
		t.Fatal(err)
	}
	got, err := listProjects(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	want := []projectRow{
		{Name: "blog", Directory: "/srv/blog", Files: []string{"/srv/blog/compose.yaml", "/srv/blog/compose.prod.yaml"},
			EnvFiles: []string{"/srv/blog/.env"}, Profiles: []string{}, FirstSeen: pjNow},
		{Name: "half", Files: []string{}, EnvFiles: []string{}, Profiles: []string{}, FirstSeen: pjNow},
		{Name: "shop", Directory: "/srv/shop", Files: []string{"/srv/shop/compose.yaml"}, EnvFiles: []string{}, Profiles: []string{}, FirstSeen: pjNow},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("projects\n got %+v\nwant %+v", got, want)
	}
}

func TestPjDiscoverNeverUpdatesARow(t *testing.T) {
	ctx := context.Background()
	db := pjDB(t)
	if err := discoverProjects(ctx, db, pjNow, []dockerapi.ContainerSummary{pjSummary("blog-web", "blog", "/srv/blog", "/srv/blog/compose.yaml", "")}); err != nil {
		t.Fatal(err)
	}
	before, _, _ := loadProject(ctx, db, "blog")
	moved := []dockerapi.ContainerSummary{pjSummary("blog-web", "blog", "/srv/moved", "/srv/moved/other.yaml", "/srv/moved/.env")}
	if err := discoverProjects(ctx, db, pjNow.Add(time.Hour), moved); err != nil {
		t.Fatal(err)
	}
	if err := discoverProjects(ctx, db, pjNow.Add(2*time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	after, ok, err := loadProject(ctx, db, "blog")
	if err != nil || !ok || !reflect.DeepEqual(after, before) {
		t.Errorf("row after rediscovery %+v (%v), want it unchanged: %+v", after, err, before)
	}
}

func TestPjLoadProject(t *testing.T) {
	ctx := context.Background()
	db := pjDB(t)
	if err := discoverProjects(ctx, db, pjNow, []dockerapi.ContainerSummary{pjSummary("a", "blog", "/srv/blog", "/srv/blog/compose.yaml", "")}); err != nil {
		t.Fatal(err)
	}
	if p, ok, err := loadProject(ctx, db, "blog"); err != nil || !ok || p.Name != "blog" || p.Directory != "/srv/blog" {
		t.Errorf("loadProject(blog) = %+v, %v, %v; want the row", p, ok, err)
	}
	if p, ok, err := loadProject(ctx, db, "ghost"); err != nil || ok || p.Name != "" {
		t.Errorf("loadProject(ghost) = %+v, %v, %v; want not found without an error", p, ok, err)
	}
}

func TestPjSetProjectRun(t *testing.T) {
	ctx := context.Background()
	db := pjDB(t)
	if err := discoverProjects(ctx, db, pjNow, []dockerapi.ContainerSummary{pjSummary("a", "blog", "/srv/blog", "/srv/blog/compose.yaml", "")}); err != nil {
		t.Fatal(err)
	}
	at := pjNow.Add(time.Minute)
	err := db.Write(ctx, func(tx *sql.Tx) error { return setProjectRun(ctx, tx, "blog", []string{"web", "db"}, "abc123", at) })
	if err != nil {
		t.Fatal(err)
	}
	p, _, _ := loadProject(ctx, db, "blog")
	if !reflect.DeepEqual(p.Profiles, []string{"web", "db"}) || p.RunHash != "abc123" || !p.LastRun.Equal(at) || !p.FirstSeen.Equal(pjNow) {
		t.Errorf("row %+v, want profiles, hash and last run set and first seen kept", p)
	}

	err = db.Write(ctx, func(tx *sql.Tx) error { return setProjectRun(ctx, tx, "blog", nil, "def456", at.Add(time.Minute)) })
	if p, _, _ = loadProject(ctx, db, "blog"); err != nil || !reflect.DeepEqual(p.Profiles, []string{}) || p.RunHash != "def456" {
		t.Errorf("row %+v (%v), want no profiles stored as an empty list and the new hash", p, err)
	}

	err = db.Write(ctx, func(tx *sql.Tx) error { return setProjectRun(ctx, tx, "ghost", nil, "x", at) })
	if err == nil {
		t.Error("setProjectRun of an unrecorded project succeeded, want an error")
	}
	if all, _ := listProjects(ctx, db); len(all) != 1 {
		t.Errorf("%d rows after the failed update, want 1", len(all))
	}
}

func TestPjConfigHash(t *testing.T) {
	h := newDKHost(t)
	k := h.dk()
	put := func(path, data string) { h.files[path] = dkFile{Data: []byte(data)} }
	put("/srv/blog/compose.yaml", "services: {}\n")
	put("/srv/blog/.env", "A=1\n")
	base := projectRow{Name: "blog", Files: []string{"/srv/blog/compose.yaml"}, EnvFiles: []string{"/srv/blog/.env"}}

	hash := k.configHash(base)
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(hash) || hash != k.configHash(base) {
		t.Fatalf("hash %q, want 64 hex digits that repeat", hash)
	}

	changed := func(name string, mutate func(*projectRow)) {
		t.Helper()
		p := base
		mutate(&p)
		if got := k.configHash(p); got == hash || got == "" {
			t.Errorf("%s: hash %q, want a different hash from %q", name, got, hash)
		}
	}
	put("/srv/blog/.env", "A=2\n")
	changed("an env file's bytes change", func(*projectRow) {})
	put("/srv/blog/.env", "A=1\n")
	put("/srv/blog/compose.yaml", "services: {web: {}}\n")
	changed("a compose file's bytes change", func(*projectRow) {})
	put("/srv/blog/compose.yaml", "services: {}\n")
	put("/srv/blog/other.yaml", "services: {}\n")
	changed("a file has another path and the same bytes", func(p *projectRow) { p.Files = []string{"/srv/blog/other.yaml"} })
	changed("an env file is dropped", func(p *projectRow) { p.EnvFiles = nil })

	put("/a", "ab")
	put("/b", "c")
	left := k.configHash(projectRow{Files: []string{"/a", "/b"}})
	put("/a", "a")
	put("/b", "bc")
	if right := k.configHash(projectRow{Files: []string{"/a", "/b"}}); right == left {
		t.Error("moving a byte from one file to the next gave the same hash, want the file boundary part of the hash")
	}
	put("/a", "x")
	put("/b", "y")
	if k.configHash(projectRow{Files: []string{"/a", "/b"}}) == k.configHash(projectRow{Files: []string{"/b", "/a"}}) {
		t.Error("two orders of the same files gave the same hash, want the order part of the hash")
	}

	if got := k.configHash(projectRow{Files: []string{"/srv/blog/compose.yaml", "/srv/blog/gone.yaml"}}); got != "" {
		t.Errorf("hash %q with an unreadable file, want empty", got)
	}
	if got := k.configHash(projectRow{Files: []string{"/srv/blog/compose.yaml"}, EnvFiles: []string{"/srv/blog/gone.env"}}); got != "" {
		t.Errorf("hash %q with an unreadable env file, want empty", got)
	}

	h.files["/srv/blog/fifo.yaml"] = dkFile{Mode: fs.ModeNamedPipe}
	h.files["/srv/blog/link.yaml"] = dkFile{Symlink: true, Data: []byte("services: {}\n")}
	h.files["/srv/blog/big.yaml"] = dkFile{Data: make([]byte, compose.MaxConfigBytes+1)}
	put("compose.yaml", "services: {}\n")
	for _, path := range []string{"/srv/blog/fifo.yaml", "/srv/blog/link.yaml", "/srv/blog/big.yaml", "compose.yaml"} {
		if got := k.configHash(projectRow{Files: []string{path}}); got != "" {
			t.Errorf("hash %q with %s, want empty", got, path)
		}
	}
}
