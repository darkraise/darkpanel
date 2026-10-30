package docker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// pjDocker lists the containers a test scripts, whatever the fake engine holds.
type pjDocker struct {
	*fakeDocker
	listed []dockerapi.ContainerSummary
	opts   []dockerapi.ListContainersOptions
}

func (d *pjDocker) ListContainers(ctx context.Context, o dockerapi.ListContainersOptions) ([]dockerapi.ContainerSummary, error) {
	d.opts = append(d.opts, o)
	if _, err := d.fakeDocker.ListContainers(ctx, o); err != nil {
		return nil, err
	}
	return d.listed, nil
}

func pjRead(t *testing.T, h *dkHost, k dk) (ProjectList, error) {
	t.Helper()
	raw, err := k.projectsResource().Read(context.Background(), h.env(""))
	var out ProjectList
	if err == nil {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
	}
	return out, err
}

func pjHost(t *testing.T, listed ...dockerapi.ContainerSummary) (*dkHost, dk, *pjDocker) {
	t.Helper()
	h := newDKHost(t)
	h.db = pjDB(t)
	d := &pjDocker{fakeDocker: h.docker, listed: listed}
	k := h.dk()
	k.d.docker = d
	return h, k, d
}

func TestPjProjectsResourceDefinition(t *testing.T) {
	_, k, _ := pjHost(t)
	def := k.projectsResource()
	if def.Name != "docker.projects" || def.OutputType != reflect.TypeFor[ProjectList]() {
		t.Errorf("resource %s with output %v, want docker.projects with ProjectList", def.Name, def.OutputType)
	}
}

func TestPjProjectsResourceListsDiscoveredProjects(t *testing.T) {
	ctx := context.Background()
	h, k, d := pjHost(t,
		pjSummary("blog-web", "blog", "/srv/blog", "/srv/blog/compose.yaml", "/srv/blog/.env"),
		pjSummary("blog-db", "blog", "/srv/blog", "/srv/blog/compose.yaml", "/srv/blog/.env"),
		dockerapi.ContainerSummary{ID: dkID("solo"), Names: []string{"/solo"}, State: "running"},
	)
	d.listed[1].State = "exited"
	defer func() {
		if len(d.opts) == 0 || !d.opts[0].All {
			t.Errorf("containers listed with %+v, want stopped containers included", d.opts)
		}
	}()
	h.files["/srv/blog/compose.yaml"] = dkFile{Data: []byte("services: {}\n")}
	h.files["/srv/blog/.env"] = dkFile{Data: []byte("A=1\n")}

	got, err := pjRead(t, h, k)
	if err != nil || len(got.Projects) != 1 {
		t.Fatalf("projects %+v (%v), want only blog", got.Projects, err)
	}
	row, _, _ := loadProject(ctx, h.db, "blog")
	hash := k.configHash(row)
	want := Project{
		Name: "blog", Directory: "/srv/blog", Files: []string{"/srv/blog/compose.yaml"}, EnvFiles: []string{"/srv/blog/.env"},
		Profiles: []string{}, ConfigHash: hash, Containers: []string{"blog-db", "blog-web"}, Running: 1, FirstSeen: h.clock.Now(),
	}
	if hash == "" || !reflect.DeepEqual(got.Projects[0], want) {
		t.Errorf("project\n got %+v\nwant %+v", got.Projects[0], want)
	}

	// A run records the hash; the project then reports a changed file.
	ran := h.clock.Now().Add(time.Minute)
	if err := h.db.Write(ctx, func(tx *sql.Tx) error { return setProjectRun(ctx, tx, "blog", []string{"web"}, hash, ran) }); err != nil {
		t.Fatal(err)
	}
	got, _ = pjRead(t, h, k)
	if p := got.Projects[0]; p.HashChanged || p.LastRun == nil || !p.LastRun.Equal(ran) || !reflect.DeepEqual(p.Profiles, []string{"web"}) {
		t.Errorf("after a run: %+v, want the run recorded and the hash unchanged", p)
	}
	h.files["/srv/blog/compose.yaml"] = dkFile{Data: []byte("services: {web: {}}\n")}
	got, _ = pjRead(t, h, k)
	if p := got.Projects[0]; !p.HashChanged || p.ConfigHash == hash || p.ConfigHash == "" {
		t.Errorf("after an edit: %+v, want hashChanged with a new hash", p)
	}

	// An unreadable file has no hash and is not reported as a change.
	delete(h.files, "/srv/blog/compose.yaml")
	got, _ = pjRead(t, h, k)
	if p := got.Projects[0]; p.HashChanged || p.ConfigHash != "" {
		t.Errorf("with a missing file: %+v, want an empty hash and no change", p)
	}

	// The project stays listed once its containers are gone (after down).
	d.listed = nil
	got, err = pjRead(t, h, k)
	if err != nil || len(got.Projects) != 1 || got.Projects[0].Name != "blog" || got.Projects[0].Running != 0 ||
		!reflect.DeepEqual(got.Projects[0].Containers, []string{}) {
		t.Errorf("after down: %+v (%v), want blog listed without containers", got.Projects, err)
	}
}

func TestPjProjectsResourceEmpty(t *testing.T) {
	h, k, _ := pjHost(t)
	raw, err := k.projectsResource().Read(context.Background(), h.env(""))
	if err != nil || string(raw) != `{"projects":[]}` {
		t.Errorf("read = %s (%v), want {\"projects\":[]}", raw, err)
	}
}

func TestPjProjectsResourceEngineUnavailable(t *testing.T) {
	h, k, _ := pjHost(t)
	h.docker.fail("ListContainers", fmt.Errorf("%w: dial unix", dockerapi.ErrUnavailable))
	_, err := pjRead(t, h, k)
	if e := errcode.From(err); e.Code != errcode.DockerEngineUnavailable {
		t.Errorf("err = %v, want %s", err, errcode.DockerEngineUnavailable)
	}
}
