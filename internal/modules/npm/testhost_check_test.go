package npm

import (
	"archive/tar"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

var errSdBoom = errors.New("boom")

func sdAnswer(t *testing.T, err error, want int) {
	t.Helper()
	var e *dockerapi.APIError
	if !errors.As(err, &e) || e.StatusCode != want {
		t.Fatalf("error %v, want a daemon answer %d", err, want)
	}
}

func TestSdHostBuilders(t *testing.T) {
	if id := thID("a"); len(id) != 64 || id != thID("a") || id == thID("b") || strings.Trim(id, "0123456789abcdef") != "" {
		t.Errorf("thID(a) = %q", id)
	}
	if got := thRandomBytes(4); !bytes.Equal(got, []byte{1, 2, 3, 4}) {
		t.Errorf("thRandomBytes(4) = %v", got)
	}
	c := thContainer("web")
	if c.ID != thID("container:web") || c.Name != "/web" || c.State.Running || c.Config.Image != "web:latest" || c.State.Status != "exited" {
		t.Errorf("thContainer(web) = %+v", c)
	}
	r := thContainer("web", thRunning, thLabelled(map[string]string{"a": "b"}), thImage("img@sha256:1"), thEndpoint("app", "10.1.0.5"),
		thMounted("data", "/data"), thPublish("127.0.0.1:81:81/tcp", "8080:80/tcp"))
	ep := r.NetworkSettings.Networks["app"]
	if !r.State.Running || r.State.Status != "running" || r.Config.Labels["a"] != "b" || r.Config.Image != "img@sha256:1" ||
		ep == nil || ep.NetworkID != thID("network:app") || ep.IPAddress != netip.MustParseAddr("10.1.0.5") || ep.EndpointID == "" {
		t.Errorf("options not applied: %+v", r)
	}
	if got := r.Mounts; !reflect.DeepEqual(got, []dockerapi.MountPoint{{Type: "volume", Name: "data", Destination: "/data", RW: true}}) {
		t.Errorf("mounts %+v", got)
	}
	if got := r.NetworkSettings.Ports["81/tcp"]; !reflect.DeepEqual(got, []dockerapi.PortBinding{{HostIP: "127.0.0.1", HostPort: "81"}}) {
		t.Errorf("81/tcp binding %v", got)
	}
	if got := r.HostConfig.PortBindings["80/tcp"]; !reflect.DeepEqual(got, []dockerapi.PortBinding{{HostIP: "0.0.0.0", HostPort: "8080"}}) {
		t.Errorf("80/tcp binding %v", got)
	}
	n := thNetwork("app")
	if n.ID != thID("network:app") || n.Name != "app" || n.Driver != "bridge" || n.Scope != "local" || n.Internal {
		t.Errorf("thNetwork(app) = %+v", n)
	}
	m := thNetwork("macv", thDriver("macvlan"), thNetLabels(map[string]string{"k": "v"}), thSubnet("10.9.0.0/24", "10.9.0.1"), thInternal)
	if m.Driver != "macvlan" || m.Labels["k"] != "v" || !m.Internal || len(m.IPAM.Config) != 1 || m.IPAM.Config[0].Gateway != netip.MustParseAddr("10.9.0.1") ||
		m.IPAM.Config[0].Subnet != netip.MustParsePrefix("10.9.0.0/24") {
		t.Errorf("options not applied: %+v", m)
	}
}

func TestSdDockerContainers(t *testing.T) {
	h := newTHHost(t)
	d, ctx := h.docker, context.Background()
	if _, err := d.CreateContainer(ctx, instanceCreate(Image)); err == nil {
		t.Fatal("a container was created from an image the Engine does not have")
	} else {
		sdAnswer(t, err, 404)
	}
	d.putImage(Image)
	if _, err := d.CreateContainer(ctx, instanceCreate(Image)); err == nil {
		t.Fatal("a container was created on a network the Engine does not have")
	} else {
		sdAnswer(t, err, 404)
	}
	d.putNetwork(thNetwork(networkName))
	id, err := d.CreateContainer(ctx, instanceCreate(Image))
	if err != nil {
		t.Fatal(err)
	}
	c, ok := d.container("darkpanel-npm")
	if !ok || c.ID != id || c.Name != "/darkpanel-npm" || c.State.Running || c.State.Status != "created" || c.Config.Labels[core.LabelModule] != "npm" {
		t.Fatalf("created container %+v", c)
	}
	if again, _ := d.container(id); again.ID != id {
		t.Error("the container is not found by its id")
	}
	if ep := c.NetworkSettings.Networks[networkName]; ep == nil || ep.NetworkID != thID("network:"+networkName) {
		t.Errorf("endpoints %+v, want one on darkpanel-npm, keyed by the network's name", c.NetworkSettings.Networks)
	}
	if got := c.NetworkSettings.Ports["81/tcp"]; !reflect.DeepEqual(got, []dockerapi.PortBinding{{HostIP: "127.0.0.1", HostPort: "81"}}) {
		t.Errorf("published 81/tcp %v", got)
	}
	if len(c.Mounts) != 2 || c.Mounts[0].Name != dataVolume || c.Mounts[0].Destination != "/data" {
		t.Errorf("mounts %+v", c.Mounts)
	}
	if _, ok := d.volume(dataVolume); !ok {
		t.Error("a named volume the container mounts was not created")
	}
	if _, err := d.CreateContainer(ctx, instanceCreate(Image)); err == nil {
		t.Error("a second container of the same name was created")
	} else {
		sdAnswer(t, err, 409)
	}

	if started, err := d.StartContainer(ctx, "darkpanel-npm"); err != nil || !started {
		t.Fatalf("start = %v, %v", started, err)
	}
	if c, _ := d.container(id); !c.State.Running || c.State.StartedAt != "2026-11-02T12:00:00Z" {
		t.Errorf("after start: %+v", c.State)
	}
	if again, err := d.StartContainer(ctx, id); err != nil || again {
		t.Errorf("start of a running container = %v, %v; want false", again, err)
	}
	listed, err := d.ListContainers(ctx, dockerapi.ListContainersOptions{Filters: dockerapi.Filters{"status": {"running"}, "label": {"darkpanel.module=npm"}}})
	if err != nil || len(listed) != 1 || listed[0].ID != id || listed[0].Names[0] != "/darkpanel-npm" || len(listed[0].Mounts) != 2 {
		t.Errorf("running labelled containers %+v, %v", listed, err)
	}
	if err := d.RemoveContainer(ctx, id, dockerapi.RemoveContainerOptions{}); err == nil {
		t.Error("removed a running container without force")
	} else {
		sdAnswer(t, err, 409)
	}
	h.clock.Advance(time.Minute)
	if stopped, err := d.StopContainer(ctx, id, time.Second); err != nil || !stopped {
		t.Fatalf("stop = %v, %v", stopped, err)
	}
	if c, _ := d.container(id); c.State.Running || c.State.FinishedAt != "2026-11-02T12:01:00Z" {
		t.Errorf("after stop: %+v", c.State)
	}
	if again, err := d.StopContainer(ctx, id, time.Second); err != nil || again {
		t.Errorf("stop of a stopped container = %v, %v; want false", again, err)
	}
	if all, _ := d.ListContainers(ctx, dockerapi.ListContainersOptions{}); len(all) != 0 {
		t.Errorf("a stopped container listed without All: %+v", all)
	}
	if err := d.RemoveContainer(ctx, "darkpanel-npm", dockerapi.RemoveContainerOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.container(id); ok {
		t.Error("container still present after removal")
	}
	_, err = d.InspectContainer(ctx, id)
	sdAnswer(t, err, 404)
	if !dockerapi.IsNotFound(err) {
		t.Error("IsNotFound false for the fake's 404")
	}
	if _, ok := d.volume(dataVolume); !ok {
		t.Error("removing the container removed its volume")
	}
	d.putContainer(thContainer("web", thRunning))
	if err := d.RemoveContainer(ctx, "web", dockerapi.RemoveContainerOptions{Force: true}); err != nil {
		t.Errorf("forced removal of a running container: %v", err)
	}
	want := []string{"CreateContainer", "CreateContainer", "CreateContainer", "CreateContainer", "StartContainer", "StartContainer", "ListContainers",
		"RemoveContainer", "StopContainer", "StopContainer", "ListContainers", "RemoveContainer", "InspectContainer", "RemoveContainer"}
	if got := d.calls(); !slices.Equal(got, want) {
		t.Errorf("calls %v, want %v", got, want)
	}
}

func TestSdDockerFail(t *testing.T) {
	d, ctx := newTHHost(t).docker, context.Background()
	d.putContainer(thContainer("web"))
	d.Fail("StartContainer", errSdBoom)
	d.Fail("StartContainer", dockerapi.ErrUnavailable)
	if _, err := d.StartContainer(ctx, "web"); !errors.Is(err, errSdBoom) {
		t.Fatalf("error %v, want the first queued failure", err)
	}
	if c, _ := d.container("web"); c.State.Running {
		t.Error("a failed start changed the container")
	}
	if _, err := d.StartContainer(ctx, "web"); !errors.Is(err, dockerapi.ErrUnavailable) {
		t.Fatalf("error %v, want the second queued failure", err)
	}
	if started, err := d.StartContainer(ctx, "web"); err != nil || !started {
		t.Errorf("the third start = %v, %v; want it to work", started, err)
	}
	if got := d.calls(); !slices.Equal(got, []string{"StartContainer", "StartContainer", "StartContainer"}) {
		t.Errorf("calls %v: a failed call is recorded too", got)
	}
}

func TestSdDockerNetworks(t *testing.T) {
	h := newTHHost(t)
	d, ctx := h.docker, context.Background()
	id, err := d.CreateNetwork(ctx, dockerapi.NetworkCreate{Name: "darkpanel-npm", Labels: map[string]string{core.LabelModule: ID}})
	if err != nil || id != thID("network:darkpanel-npm") {
		t.Fatalf("create = %q, %v", id, err)
	}
	if _, err := d.CreateNetwork(ctx, dockerapi.NetworkCreate{Name: "darkpanel-npm"}); err == nil {
		t.Error("a second network of the same name was created")
	} else {
		sdAnswer(t, err, 409)
	}
	n, _ := d.network("darkpanel-npm")
	if n.Driver != "bridge" || n.Scope != "local" || n.Labels[core.LabelModule] != "npm" || n.Created != "2026-11-02T12:00:00Z" {
		t.Errorf("created network %+v", n)
	}
	d.putNetwork(thNetwork("darkpanel-npm-extra"))
	if list, err := d.ListNetworks(ctx, dockerapi.Filters{"name": {"darkpanel-npm"}}); err != nil || len(list) != 2 {
		t.Errorf("name filter listed %+v, %v; want Docker's substring match, both networks", list, err)
	}
	if list, _ := d.ListNetworks(ctx, dockerapi.Filters{"label": {"darkpanel.module=npm"}}); len(list) != 1 || list[0].Name != "darkpanel-npm" {
		t.Errorf("label filter listed %+v", list)
	}

	d.putContainer(thContainer("web"))
	if err := d.ConnectNetwork(ctx, "darkpanel-npm", "web", &dockerapi.EndpointSettings{
		Aliases: []string{"w"}, IPAMConfig: &dockerapi.EndpointIPAMConfig{IPv4Address: netip.MustParseAddr("172.20.0.9")}}); err != nil {
		t.Fatal(err)
	}
	c, _ := d.container("web")
	ep := c.NetworkSettings.Networks["darkpanel-npm"]
	if ep == nil || ep.NetworkID != id || ep.EndpointID == "" || ep.IPAddress != netip.MustParseAddr("172.20.0.9") || !slices.Equal(ep.Aliases, []string{"w"}) {
		t.Errorf("endpoint %+v", ep)
	}
	if err := d.ConnectNetwork(ctx, "darkpanel-npm", "web", nil); err == nil {
		t.Error("connected twice")
	} else {
		sdAnswer(t, err, 403)
	}
	in, err := d.InspectNetwork(ctx, id)
	if err != nil || len(in.Containers) != 1 || in.Containers[thID("container:web")].Name != "web" {
		t.Errorf("inspect lists %+v, %v; want the container web", in.Containers, err)
	}
	if list, _ := d.ListNetworks(ctx, nil); len(list) == 0 || list[0].Containers != nil {
		t.Errorf("the list endpoint filled Containers: %+v", list)
	}
	if err := d.RemoveNetwork(ctx, id); err == nil {
		t.Error("removed a network with an active endpoint")
	} else {
		sdAnswer(t, err, 403)
	}
	if err := d.DisconnectNetwork(ctx, "darkpanel-npm", "web", false); err != nil {
		t.Fatal(err)
	}
	if err := d.DisconnectNetwork(ctx, "darkpanel-npm", "web", false); err == nil {
		t.Error("disconnected twice")
	} else {
		sdAnswer(t, err, 403)
	}
	if err := d.ConnectNetwork(ctx, "nope", "web", nil); err == nil {
		t.Error("connected to a network that does not exist")
	} else {
		sdAnswer(t, err, 404)
	}
	if err := d.RemoveNetwork(ctx, "darkpanel-npm"); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.network(id); ok {
		t.Error("network still present after removal")
	}
}

func TestSdDockerVolumes(t *testing.T) {
	h := newTHHost(t)
	d, ctx := h.docker, context.Background()
	v, err := d.CreateVolume(ctx, dockerapi.VolumeCreate{Name: dataVolume, Labels: map[string]string{core.LabelModule: ID}})
	if err != nil || v.Name != dataVolume || v.Driver != "local" || v.Labels[core.LabelModule] != "npm" || v.Mountpoint == "" || v.CreatedAt != "2026-11-02T12:00:00Z" {
		t.Fatalf("create = %+v, %v", v, err)
	}
	h.clock.Advance(time.Hour)
	if again, err := d.CreateVolume(ctx, dockerapi.VolumeCreate{Name: dataVolume}); err != nil || again.CreatedAt != v.CreatedAt || again.Labels[core.LabelModule] != "npm" {
		t.Errorf("creating an existing volume = %+v, %v; want the volume unchanged", again, err)
	}
	if list, _ := d.ListVolumes(ctx, dockerapi.Filters{"label": {"darkpanel.module"}}); len(list) != 1 {
		t.Errorf("label filter listed %+v", list)
	}
	if _, err := d.InspectVolume(ctx, "missing"); err == nil {
		t.Error("inspected a volume that does not exist")
	} else {
		sdAnswer(t, err, 404)
	}
	d.putContainer(thContainer("web", thMounted(dataVolume, "/data")))
	if err := d.RemoveVolume(ctx, dataVolume, false); err == nil {
		t.Error("removed a volume a container mounts")
	} else {
		sdAnswer(t, err, 409)
	}
	if err := d.RemoveContainer(ctx, "web", dockerapi.RemoveContainerOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveVolume(ctx, dataVolume, false); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveVolume(ctx, dataVolume, false); err == nil {
		t.Error("removed a volume twice")
	} else {
		sdAnswer(t, err, 404)
	}
}

func TestSdDockerImagesAndArchives(t *testing.T) {
	h := newTHHost(t)
	d, ctx := h.docker, context.Background()
	if _, err := d.InspectImage(ctx, Image); err == nil {
		t.Error("inspected an image the Engine does not have")
	} else {
		sdAnswer(t, err, 404)
	}
	s, err := d.PullImage(ctx, Image)
	if err != nil {
		t.Fatal(err)
	}
	var msgs int
	for {
		_, err := s.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		msgs++
	}
	if msgs == 0 || s.Close() != nil {
		t.Errorf("the pull stream gave %d messages", msgs)
	}
	img, err := d.InspectImage(ctx, Image)
	if err != nil || !slices.Equal(img.RepoDigests, []string{Image}) || img.ID == "" {
		t.Errorf("inspect after the pull = %+v, %v", img, err)
	}
	d.Fail("PullImage", errSdBoom)
	if s, err := d.PullImage(ctx, "other:1"); !errors.Is(err, errSdBoom) || s != nil {
		t.Errorf("failed pull = %v, %v; want a nil stream", s, err)
	}
	if info, err := d.Info(ctx); err != nil || info.ServerVersion != "29.9.1" {
		t.Errorf("info = %+v, %v", info, err)
	}

	d.putContainer(thContainer("darkpanel-npm"))
	id := thID("container:darkpanel-npm")
	rc, err := d.CopyFromContainer(ctx, "darkpanel-npm", "/data")
	if err != nil {
		t.Fatal(err)
	}
	hdr, err := tar.NewReader(rc).Next()
	_ = rc.Close()
	if err != nil || hdr.Name != "data/marker" {
		t.Errorf("default archive entry %v, %v; want data/marker", hdr, err)
	}
	d.putArchive(id, "/data", []byte("stored archive"))
	rc, _ = d.CopyFromContainer(ctx, id, "/data")
	if b, _ := io.ReadAll(rc); string(b) != "stored archive" {
		t.Errorf("archive %q, want the stored one", b)
	}
	if err := d.CopyToContainer(ctx, id, "/etc", strings.NewReader("restored")); err != nil {
		t.Fatal(err)
	}
	if b, ok := d.archive(id, "/etc"); !ok || string(b) != "restored" {
		t.Errorf("archive(/etc) = %q, %v", b, ok)
	}
	if _, ok := d.archive(id, "/nowhere"); ok {
		t.Error("an archive nobody stored")
	}
	if _, err := d.CopyFromContainer(ctx, "missing", "/data"); err == nil {
		t.Error("copied from a container that does not exist")
	} else {
		sdAnswer(t, err, 404)
	}
	if err := d.CopyToContainer(ctx, "missing", "/data", strings.NewReader("x")); err == nil {
		t.Error("copied to a container that does not exist")
	} else {
		sdAnswer(t, err, 404)
	}
	if err := d.RemoveContainer(ctx, id, dockerapi.RemoveContainerOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.archive(id, "/etc"); ok {
		t.Error("an archive survived its container")
	}
}

func TestSdHostEnvs(t *testing.T) {
	h := newTHHost(t)
	plan := h.env("")
	if plan.HostID != "h1" || plan.ChangeID != "" || plan.Effects != nil || plan.DB != h.db || plan.Files == nil || plan.Sealer == nil ||
		plan.Clock != core.Clock(h.clock) || plan.Principal.Scope != core.ScopeAdmin || plan.Log == nil || plan.Agents != nil {
		t.Errorf("plan env %+v", plan)
	}
	apply := h.env("c1")
	if apply.ChangeID != "c1" || apply.Effects == nil {
		t.Errorf("apply env %+v, want a journal", apply)
	}
	ct, err := apply.Sealer.Seal("ad", []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if pt, err := apply.Sealer.Open("ad", ct); err != nil || string(pt) != "secret" {
		t.Errorf("sealer round trip = %q, %v", pt, err)
	}
	child := h.child("c2", "npm.admin.expose")
	if child.Call.ParentAction != "npm.admin.expose" || child.ChangeID != "c2" || child.Effects == nil {
		t.Errorf("child env %+v", child)
	}
	if _, err := apply.Exec.Run(context.Background(), core.Cmd{Path: "/usr/bin/docker"}); err == nil {
		t.Error("the host ran a program")
	}

	h.agents["h2"] = &coretest.FakeAgent{}
	ctl := h.ctlEnv("c3")
	if ctl.HostID != "" || ctl.DB != h.ctlDB || ctl.Agents == nil || ctl.ChangeID != "c3" || ctl.Effects == nil {
		t.Errorf("controller env %+v", ctl)
	}
	agent, err := h.ctlEnv("").Agents.Agent(context.Background(), "h2")
	if err != nil || agent == nil {
		t.Errorf("Agents.Agent(h2) = %v, %v", agent, err)
	}
	if _, err := h.ctlEnv("").Agents.Agent(context.Background(), "h9"); err == nil {
		t.Error("Agents.Agent(h9) found a host that does not exist")
	}
	if err := h.ctlDB.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO core_hosts (id, name, is_local, created_at) VALUES ('h1', 'alpha', 1, '')`)
		return err
	}); err != nil {
		t.Errorf("the controller database lacks the core tables: %v", err)
	}
	if _, err := h.db.Read().Exec(`SELECT 1 FROM npm_instances`); err != nil {
		t.Errorf("the agent database lacks the npm tables: %v", err)
	}
	if _, err := h.ctlDB.Read().Exec(`SELECT 1 FROM npm_instances`); err == nil {
		t.Error("the controller database holds the agent's npm tables")
	}
}

func TestSdHostFiles(t *testing.T) {
	h := newTHHost(t)
	ctx, f := context.Background(), h.env("").Files
	if err := f.WriteManaged(ctx, ID, backupDir, "c1-data.tar", []byte("tar"), 0o600, 0, 0); err != nil {
		t.Fatal(err)
	}
	key := backupDir + "/c1-data.tar"
	if string(h.managed[key]) != "tar" || string(h.files[key]) != "tar" {
		t.Errorf("managed %q and files %q, want the write in both", h.managed[key], h.files[key])
	}
	nm := h.nm()
	if b, err := nm.d.readFile(key); err != nil || string(b) != "tar" {
		t.Errorf("readFile = %q, %v", b, err)
	}
	if _, err := nm.d.readFile(backupDir + "/none.tar"); err == nil {
		t.Error("readFile found a file nobody wrote")
	}
	_ = f.WriteManaged(ctx, ID, backupDir, "c2-data.tar", []byte("x"), 0o600, 0, 0)
	entries, err := nm.d.readDir(backupDir)
	if err != nil || len(entries) != 2 || entries[0].Name() != "c1-data.tar" || entries[1].Name() != "c2-data.tar" {
		t.Errorf("readDir = %v, %v; want the two archives by name", entries, err)
	}
	if _, err := nm.d.readDir("/elsewhere"); err == nil {
		t.Error("readDir listed a directory nobody wrote to")
	}
	for _, c := range []struct{ module, dir, name string }{
		{"docker", backupDir, "a.tar"}, {ID, "/var/lib/darkpanel/agent", "a.tar"}, {ID, backupDir, "a.tar.gz"}, {ID, backupDir, "A.tar"},
	} {
		if err := f.WriteManaged(ctx, c.module, c.dir, c.name, nil, 0o600, 0, 0); err == nil {
			t.Errorf("WriteManaged(%q, %q, %q) was accepted", c.module, c.dir, c.name)
		}
	}
	if err := f.RemoveManaged(ctx, ID, backupDir, "c1-data.tar"); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.managed[key]; ok {
		t.Error("RemoveManaged left the managed copy")
	}
	if _, ok := h.files[key]; ok {
		t.Error("RemoveManaged left the readable copy")
	}
	if err := f.WriteFile(ctx, ID, "/etc/x", nil, 0o600); err == nil {
		t.Error("WriteFile was accepted: the module owns no plain file")
	}
}

func TestSdHostDeployed(t *testing.T) {
	h := newTHHost(t)
	ctx := context.Background()
	inst := h.deployed()
	if inst.ContainerID != thID("container:darkpanel-npm") || inst.Email == "" || inst.Password == "" {
		t.Fatalf("instance %+v", inst)
	}
	c, ok := h.docker.container("darkpanel-npm")
	if !ok || c.ID != inst.ContainerID || !c.State.Running || c.Config.Image != Image || c.Config.Labels[core.LabelModule] != "npm" ||
		c.NetworkSettings.Networks[networkName] == nil || len(c.Mounts) != 2 {
		t.Errorf("container %+v", c)
	}
	if n, ok := h.docker.network(networkName); !ok || n.Labels[core.LabelModule] != "npm" {
		t.Errorf("network %+v, %v", n, ok)
	}
	for _, v := range []string{dataVolume, certVolume} {
		if got, ok := h.docker.volume(v); !ok || got.Labels[core.LabelModule] != "npm" {
			t.Errorf("volume %s = %+v, %v", v, got, ok)
		}
	}
	if _, err := h.docker.InspectImage(ctx, Image); err != nil {
		t.Errorf("the image is not on the host: %v", err)
	}
	email, password, ok := h.npm.Admin()
	if !ok || email != inst.Email || password != inst.Password || !h.npm.Setup() {
		t.Errorf("NPM administrator %q, %q, %v", email, password, ok)
	}
	row, ok, err := loadInstance(ctx, h.db.Read())
	if err != nil || !ok || row.ContainerID != inst.ContainerID || row.Image != Image || row.AdminEmail != inst.Email || row.Public != "no" {
		t.Fatalf("instance row %+v, %v, %v", row, ok, err)
	}
	if pw, err := h.env("").Sealer.Open("npm_instances.password_ct.1", row.PasswordCT); err != nil || string(pw) != inst.Password {
		t.Errorf("sealed password opens to %q, %v", pw, err)
	}
	if _, err := h.nm().d.api(ctx, h.env("")); err != nil {
		t.Errorf("api for the seeded administrator: %v", err)
	}
}

func TestSdHostAddChange(t *testing.T) {
	h := newTHHost(t)
	h.addChange("c1", "npm.instance.upgrade")
	h.addChange("c2", "npm.proxyhost.add")
	var action, state string
	if err := h.db.Read().QueryRow(`SELECT action, state FROM core_changes WHERE id = 'c1'`).Scan(&action, &state); err != nil || action != "npm.instance.upgrade" || state != "applying" {
		t.Errorf("change c1: %q %q (error %v)", action, state, err)
	}
	var n int
	if err := h.db.Read().QueryRow(`SELECT count(*) FROM core_changes`).Scan(&n); err != nil || n != 2 {
		t.Errorf("%d changes (error %v), want 2", n, err)
	}
}

func TestSdJournal(t *testing.T) {
	h := newTHHost(t)
	ctx := context.Background()
	j := h.env("c1").Effects
	seq, err := j.Intend(ctx, core.Effect{Kind: core.EffectCreate, Resource: "npm.network:?x"}, []byte("sealed"))
	if err != nil || seq != 1 {
		t.Fatalf("Intend = %d, %v", seq, err)
	}
	seq2, _ := j.Intend(ctx, core.Effect{Kind: core.EffectModify, Resource: "npm.container:y"}, nil)
	if seq2 != 2 {
		t.Errorf("second Intend = %d", seq2)
	}
	if b, err := j.Secret(ctx, 1); err != nil || string(b) != "sealed" {
		t.Errorf("Secret(1) = %q, %v", b, err)
	}
	if b, _ := j.Secret(ctx, 2); len(b) != 0 {
		t.Errorf("Secret(2) = %q, want none", b)
	}
	if err := j.Made(ctx, 1, "npm.network:abc", core.StateDoc{State: []byte(`{"id":"abc"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := j.Skipped(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if err := j.Made(ctx, 1, "npm.network:abc", core.StateDoc{}); err == nil {
		t.Error("an effect was made twice")
	}
	if err := j.Skipped(ctx, 9); err == nil {
		t.Error("an effect that does not exist was skipped")
	}
	got := h.journal.effects()
	if len(got) != 2 || got[0].State != core.EffectMade || got[0].Resource != "npm.network:abc" || string(got[0].Post.State) != `{"id":"abc"}` ||
		got[1].State != core.EffectSkipped || got[0].ChangeID != "c1" || got[0].Seq != 1 {
		t.Errorf("effects %+v", got)
	}
	if list, _ := j.List(ctx); len(list) != 2 {
		t.Errorf("List returned %d effects", len(list))
	}
}
