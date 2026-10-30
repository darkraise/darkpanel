package docker

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

var errDKBoom = errors.New("boom")

func dkAPIStatus(t *testing.T, err error, want int) {
	t.Helper()
	var e *dockerapi.APIError
	if !errors.As(err, &e) || e.StatusCode != want {
		t.Fatalf("error %v, want a daemon answer %d", err, want)
	}
}

func TestDKBuilders(t *testing.T) {
	if id := dkID("a"); len(id) != 64 || id != dkID("a") || id == dkID("b") || strings.Trim(id, "0123456789abcdef") != "" {
		t.Errorf("dkID(a) = %q", id)
	}
	c := dkContainer("web")
	if c.ID != dkID("web") || c.Name != "/web" || c.Created != "2026-10-01T00:00:00Z" || c.State.Running {
		t.Errorf("dkContainer(web) = %+v", c)
	}
	r := dkContainer("web", dkRunning, dkLabels(map[string]string{"a": "b"}), dkPorts("8080:80/tcp", "127.0.0.1:9090:90/udp"), dkOnNetwork("n1"))
	if !r.State.Running || r.State.Status != "running" || r.Config.Labels["a"] != "b" {
		t.Errorf("options not applied: %+v", r)
	}
	if got := r.HostConfig.PortBindings["80/tcp"]; !reflect.DeepEqual(got, []dockerapi.PortBinding{{HostIP: "0.0.0.0", HostPort: "8080"}}) {
		t.Errorf("80/tcp binding %v", got)
	}
	if got := r.NetworkSettings.Ports["90/udp"]; !reflect.DeepEqual(got, []dockerapi.PortBinding{{HostIP: "127.0.0.1", HostPort: "9090"}}) {
		t.Errorf("90/udp binding %v", got)
	}
	if ep := r.NetworkSettings.Networks["n1"]; ep == nil || ep.NetworkID != "n1" || ep.EndpointID == "" {
		t.Errorf("endpoint %+v", ep)
	}
	n := dkNetwork("net")
	if n.ID != dkID("network:net") || n.Name != "net" || n.Driver != "bridge" {
		t.Errorf("dkNetwork = %+v", n)
	}
	v := dkVolume("data", map[string]string{"k": "v"})
	if v.Name != "data" || v.Driver != "local" || v.Labels["k"] != "v" {
		t.Errorf("dkVolume = %+v", v)
	}
	i := dkImage("nginx:1.27", 100)
	if !reflect.DeepEqual(i.RepoTags, []string{"nginx:1.27"}) || i.Size != 100 || i.ID == "" {
		t.Errorf("dkImage = %+v", i)
	}
	d1, d2 := dkImage("<none>:a", 1), dkImage("<none>:b", 1)
	if len(d1.RepoTags) != 0 || d1.ID == d2.ID {
		t.Errorf("dangling images %+v %+v", d1, d2)
	}
}

func TestDKFakeContainerLifecycle(t *testing.T) {
	h := newDKHost(t)
	f, ctx := h.docker, context.Background()
	f.put(dkContainer("web"))

	started, err := f.StartContainer(ctx, "web")
	if err != nil || !started {
		t.Fatalf("start by name = %v, %v", started, err)
	}
	c, _ := f.container(dkID("web"))
	if !c.State.Running || c.State.StartedAt != "2026-10-30T12:00:00Z" {
		t.Errorf("after start: %+v", c.State)
	}
	if again, err := f.StartContainer(ctx, dkID("web")); err != nil || again {
		t.Errorf("start of a running container = %v, %v; want false", again, err)
	}
	h.clock.Advance(time.Minute)
	if stopped, err := f.StopContainer(ctx, "web", stopTimeout); err != nil || !stopped {
		t.Fatalf("stop = %v, %v", stopped, err)
	}
	c, _ = f.container("web")
	if c.State.Running || c.State.FinishedAt != "2026-10-30T12:01:00Z" {
		t.Errorf("after stop: %+v", c.State)
	}
	if again, err := f.StopContainer(ctx, "web", stopTimeout); err != nil || again {
		t.Errorf("stop of a stopped container = %v, %v; want false", again, err)
	}
	h.clock.Advance(time.Minute)
	if err := f.RestartContainer(ctx, "web", stopTimeout); err != nil {
		t.Fatal(err)
	}
	c, _ = f.container("web")
	if !c.State.Running || c.State.StartedAt != "2026-10-30T12:02:00Z" {
		t.Errorf("after restart: %+v", c.State)
	}
	if err := f.RemoveContainer(ctx, "web", dockerapi.RemoveContainerOptions{}); err == nil {
		t.Error("removed a running container without force")
	} else {
		dkAPIStatus(t, err, 409)
	}
	if err := f.RemoveContainer(ctx, "web", dockerapi.RemoveContainerOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.container("web"); ok {
		t.Error("container still present after removal")
	}
	_, err = f.InspectContainer(ctx, "web")
	dkAPIStatus(t, err, 404)
	if !dockerapi.IsNotFound(err) {
		t.Error("IsNotFound false for the fake's 404")
	}
	want := []string{"StartContainer", "StartContainer", "StopContainer", "StopContainer", "RestartContainer",
		"RemoveContainer", "RemoveContainer", "InspectContainer"}
	if got := f.calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("calls %v, want %v", got, want)
	}
}

func TestDKFakeFail(t *testing.T) {
	f, ctx := newDKHost(t).docker, context.Background()
	f.put(dkContainer("web"))
	f.fail("StartContainer", errDKBoom)
	if _, err := f.StartContainer(ctx, "web"); !errors.Is(err, errDKBoom) {
		t.Fatalf("error %v, want the queued failure", err)
	}
	if c, _ := f.container("web"); c.State.Running {
		t.Error("a failed start changed the container")
	}
	if started, err := f.StartContainer(ctx, "web"); err != nil || !started {
		t.Errorf("the next start = %v, %v; want it to work", started, err)
	}
	if got := f.calls(); !reflect.DeepEqual(got, []string{"StartContainer", "StartContainer"}) {
		t.Errorf("calls %v: a failed call is recorded too", got)
	}
}

func TestDKFakeListContainers(t *testing.T) {
	f, ctx := newDKHost(t).docker, context.Background()
	f.put(dkContainer("a", dkRunning, dkLabels(map[string]string{"com.docker.compose.project": "p"}), dkPorts("8080:80/tcp")))
	f.put(dkContainer("b", dkLabels(map[string]string{"com.docker.compose.project": "p", "x": "y"})))
	f.put(dkContainer("c", dkRunning))

	names := func(o dockerapi.ListContainersOptions) []string {
		cs, err := f.ListContainers(ctx, o)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range cs {
			out = append(out, c.Names[0])
		}
		return out
	}
	if got := names(dockerapi.ListContainersOptions{}); !reflect.DeepEqual(got, []string{"/a", "/c"}) {
		t.Errorf("running only: %v", got)
	}
	if got := names(dockerapi.ListContainersOptions{All: true}); !reflect.DeepEqual(got, []string{"/a", "/b", "/c"}) {
		t.Errorf("all: %v", got)
	}
	label := dockerapi.Filters{"label": {"com.docker.compose.project=p"}}
	if got := names(dockerapi.ListContainersOptions{All: true, Filters: label}); !reflect.DeepEqual(got, []string{"/a", "/b"}) {
		t.Errorf("label filter: %v", got)
	}
	two := dockerapi.Filters{"label": {"com.docker.compose.project", "x=y"}}
	if got := names(dockerapi.ListContainersOptions{All: true, Filters: two}); !reflect.DeepEqual(got, []string{"/b"}) {
		t.Errorf("two label filters: %v", got)
	}
	if _, err := f.ListContainers(ctx, dockerapi.ListContainersOptions{Filters: dockerapi.Filters{"status": {"exited"}}}); err == nil {
		t.Error("an unsupported filter was ignored")
	}
	cs, _ := f.ListContainers(ctx, dockerapi.ListContainersOptions{})
	if cs[0].ID != dkID("a") || cs[0].ImageID == "" || cs[0].Labels["com.docker.compose.project"] != "p" ||
		len(cs[0].Ports) != 1 || cs[0].Ports[0].PublicPort != 8080 || cs[0].Ports[0].PrivatePort != 80 || cs[0].Ports[0].Type != "tcp" {
		t.Errorf("summary %+v", cs[0])
	}
}

func TestDKFakeNetworks(t *testing.T) {
	f, ctx := newDKHost(t).docker, context.Background()
	f.put(dkContainer("web", dkRunning))
	id, err := f.CreateNetwork(ctx, dockerapi.NetworkCreate{Name: "front", Labels: map[string]string{"k": "v"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.CreateNetwork(ctx, dockerapi.NetworkCreate{Name: "front"}); err == nil {
		t.Error("created a network twice")
	} else {
		dkAPIStatus(t, err, 409)
	}
	if err := f.ConnectNetwork(ctx, "front", "web", &dockerapi.EndpointSettings{Aliases: []string{"w"}}); err != nil {
		t.Fatal(err)
	}
	c, _ := f.container("web")
	ep := c.NetworkSettings.Networks["front"]
	if ep == nil || ep.NetworkID != id || ep.EndpointID == "" || !reflect.DeepEqual(ep.Aliases, []string{"w"}) {
		t.Fatalf("endpoint %+v", ep)
	}
	first := ep.EndpointID
	if err := f.ConnectNetwork(ctx, "front", "web", nil); err == nil {
		t.Error("connected twice")
	} else {
		dkAPIStatus(t, err, 403)
	}
	n, err := f.InspectNetwork(ctx, id)
	if err != nil || len(n.Containers) != 1 || n.Containers[c.ID].Name != "web" || n.Containers[c.ID].EndpointID != first {
		t.Fatalf("InspectNetwork = %+v, %v", n, err)
	}
	listed, _ := f.ListNetworks(ctx, nil)
	if len(listed) != 1 || listed[0].Containers != nil {
		t.Errorf("ListNetworks must leave Containers empty: %+v", listed)
	}
	if err := f.RemoveNetwork(ctx, "front"); !dockerapi.IsConflict(err) {
		t.Errorf("RemoveNetwork of a used network = %v, want a 409", err)
	}
	if err := f.DisconnectNetwork(ctx, "front", "web", false); err != nil {
		t.Fatal(err)
	}
	if err := f.DisconnectNetwork(ctx, "front", "web", false); !dockerapi.IsNotFound(err) {
		t.Errorf("second disconnect = %v, want a 404", err)
	}
	if err := f.ConnectNetwork(ctx, "front", "web", nil); err != nil {
		t.Fatal(err)
	}
	c, _ = f.container("web")
	if c.NetworkSettings.Networks["front"].EndpointID == first {
		t.Error("reconnecting kept the endpoint id")
	}
	if err := f.DisconnectNetwork(ctx, "front", "web", false); err != nil {
		t.Fatal(err)
	}
	if err := f.RemoveNetwork(ctx, "front"); err != nil {
		t.Fatal(err)
	}
	if err := f.RemoveNetwork(ctx, "front"); !dockerapi.IsNotFound(err) {
		t.Errorf("RemoveNetwork of an absent network = %v, want a 404", err)
	}
}

func TestDKFakeNetworkOfDKOnNetwork(t *testing.T) {
	f, ctx := newDKHost(t).docker, context.Background()
	n := dkNetwork("back")
	f.putNetwork(n)
	f.put(dkContainer("db", dkOnNetwork(n.ID)))
	got, err := f.InspectNetwork(ctx, "back")
	if err != nil || len(got.Containers) != 1 {
		t.Fatalf("InspectNetwork = %+v, %v", got, err)
	}
	if err := f.RemoveNetwork(ctx, n.ID); !dockerapi.IsConflict(err) {
		t.Errorf("RemoveNetwork = %v, want a 409", err)
	}
}

func TestDKFakeVolumes(t *testing.T) {
	f, ctx := newDKHost(t).docker, context.Background()
	f.putVolume(dkVolume("data", map[string]string{"k": "v"}))
	got, err := f.CreateVolume(ctx, dockerapi.VolumeCreate{Name: "data", Labels: map[string]string{"other": "x"}})
	if err != nil || got.Labels["k"] != "v" || len(got.Labels) != 1 {
		t.Errorf("CreateVolume of an existing name = %+v, %v; want it unchanged", got, err)
	}
	created, err := f.CreateVolume(ctx, dockerapi.VolumeCreate{Name: "new"})
	if err != nil || created.Driver != "local" || created.CreatedAt != "2026-10-30T12:00:00Z" {
		t.Errorf("CreateVolume = %+v, %v", created, err)
	}
	user := dkContainer("web")
	user.Mounts = []dockerapi.MountPoint{{Type: "volume", Name: "data", Destination: "/d"}}
	f.put(user)
	if err := f.RemoveVolume(ctx, "data", false); !dockerapi.IsConflict(err) {
		t.Errorf("RemoveVolume of a used volume = %v, want a 409", err)
	}
	if err := f.RemoveVolume(ctx, "new", false); err != nil {
		t.Error(err)
	}
	if err := f.RemoveVolume(ctx, "new", false); !dockerapi.IsNotFound(err) {
		t.Errorf("RemoveVolume of an absent volume = %v, want a 404", err)
	}
	if _, err := f.InspectVolume(ctx, "new"); !dockerapi.IsNotFound(err) {
		t.Errorf("InspectVolume of an absent volume = %v, want a 404", err)
	}
	vs, err := f.ListVolumes(ctx, dockerapi.Filters{"label": {"k=v"}})
	if err != nil || len(vs) != 1 || vs[0].Name != "data" {
		t.Errorf("ListVolumes = %+v, %v", vs, err)
	}
}

func TestDKFakeImages(t *testing.T) {
	f, ctx := newDKHost(t).docker, context.Background()
	f.putImage(dkImage("nginx:1.27", 100))
	f.putImage(dkImage("<none>:a", 50))
	img := dkImage("busybox:1.36", 10)
	img.RepoTags = append(img.RepoTags, "busybox:stable")
	f.putImage(img)

	dangling, err := f.ListImages(ctx, dockerapi.ListImagesOptions{Filters: dockerapi.Filters{"dangling": {"true"}}})
	if err != nil || len(dangling) != 1 || dangling[0].Size != 50 {
		t.Errorf("dangling = %+v, %v", dangling, err)
	}
	if all, _ := f.ListImages(ctx, dockerapi.ListImagesOptions{}); len(all) != 3 {
		t.Errorf("all images: %d, want 3", len(all))
	}
	f.putImage(dkImage("redis:latest", 5))
	if in, err := f.InspectImage(ctx, "redis"); err != nil || in.ID != dkImage("redis:latest", 0).ID {
		t.Errorf("InspectImage(redis) = %+v, %v; a bare name means :latest", in, err)
	}
	if _, err := f.InspectImage(ctx, "nginx"); !dockerapi.IsNotFound(err) {
		t.Errorf("InspectImage(nginx) = %v, want a 404: only nginx:1.27 exists", err)
	}
}

func TestDKFakeImageInspectAndRemove(t *testing.T) {
	f, ctx := newDKHost(t).docker, context.Background()
	img := dkImage("busybox:1.36", 10)
	img.RepoTags = append(img.RepoTags, "busybox:stable")
	f.putImage(img)
	f.putImage(dkImage("nginx:1.27", 100))
	if in, err := f.InspectImage(ctx, "busybox:stable"); err != nil || in.ID != img.ID {
		t.Fatalf("InspectImage by tag = %+v, %v", in, err)
	}
	if _, err := f.InspectImage(ctx, "nope:1"); !dockerapi.IsNotFound(err) {
		t.Errorf("InspectImage of an absent image = %v, want a 404", err)
	}
	items, err := f.RemoveImage(ctx, "busybox:stable", dockerapi.RemoveImageOptions{})
	if err != nil || !reflect.DeepEqual(items, []dockerapi.ImageDeleteItem{{Untagged: "busybox:stable"}}) {
		t.Errorf("removing one of two tags = %v, %v", items, err)
	}
	user := dkContainer("web")
	user.Image = dkImage("nginx:1.27", 100).ID
	f.put(user)
	if _, err := f.RemoveImage(ctx, "nginx:1.27", dockerapi.RemoveImageOptions{}); !dockerapi.IsConflict(err) {
		t.Errorf("removing an image in use = %v, want a 409", err)
	}
	items, err = f.RemoveImage(ctx, "busybox:1.36", dockerapi.RemoveImageOptions{})
	if err != nil || !reflect.DeepEqual(items, []dockerapi.ImageDeleteItem{{Untagged: "busybox:1.36"}, {Deleted: img.ID}}) {
		t.Errorf("removing the last tag = %v, %v", items, err)
	}
	if _, err := f.InspectImage(ctx, "busybox:1.36"); !dockerapi.IsNotFound(err) {
		t.Errorf("image still present: %v", err)
	}
}

func TestDKFakePull(t *testing.T) {
	f, ctx := newDKHost(t).docker, context.Background()
	if _, err := f.PullImage(ctx, "ghost:1"); !dockerapi.IsNotFound(err) {
		t.Errorf("PullImage with no scripted pull = %v, want a 404", err)
	}
	f.pulls["busybox:1.36"] = []dockerapi.PullMessage{{Status: "Pulling"}, {Status: "Downloading", ID: "l1", ProgressDetail: &dockerapi.ProgressDetail{Current: 1, Total: 2}}}
	s, err := f.PullImage(ctx, "busybox:1.36")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Pulling", "Downloading"} {
		m, err := s.Next()
		if err != nil || m.Status != want {
			t.Fatalf("Next = %+v, %v; want %s", m, err, want)
		}
	}
	if _, err := f.InspectImage(ctx, "busybox:1.36"); !dockerapi.IsNotFound(err) {
		t.Error("the image appeared before the stream ended")
	}
	if _, err := s.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next at the end = %v, want io.EOF", err)
	}
	if _, err := f.InspectImage(ctx, "busybox:1.36"); err != nil {
		t.Errorf("the image is absent after the pull: %v", err)
	}

	f.pulls["bad:1"] = []dockerapi.PullMessage{{Status: "Pulling"}, {Error: "denied"}}
	s, _ = f.PullImage(ctx, "bad:1")
	_, _ = s.Next()
	if _, err := s.Next(); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Errorf("a failure message gave %v, want its error", err)
	}
	if _, err := f.InspectImage(ctx, "bad:1"); !dockerapi.IsNotFound(err) {
		t.Error("a failed pull left an image")
	}
}

func TestDKFakeLogs(t *testing.T) {
	f, ctx := newDKHost(t).docker, context.Background()
	f.put(dkContainer("web"))
	id := dkID("web")
	f.logs[id] = []dockerapi.LogFrame{{Stream: "stdout", Data: []byte("1")}, {Stream: "stderr", Data: []byte("2")},
		{Stream: "stdout", Data: []byte("3")}}
	read := func(o dockerapi.LogsOptions) string {
		s, err := f.Logs(ctx, "web", o)
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for {
			fr, err := s.Next()
			if errors.Is(err, io.EOF) {
				return b.String()
			}
			if err != nil {
				t.Fatal(err)
			}
			b.WriteString(fr.Stream[:3] + ":" + string(fr.Data) + " ")
		}
	}
	if got := read(dockerapi.LogsOptions{Stdout: true, Stderr: true}); got != "std:1 std:2 std:3 " {
		t.Errorf("both streams: %q", got)
	}
	if got := read(dockerapi.LogsOptions{Stderr: true}); got != "std:2 " {
		t.Errorf("stderr only: %q", got)
	}
	if got := read(dockerapi.LogsOptions{Stdout: true, Stderr: true, Tail: "2"}); got != "std:2 std:3 " {
		t.Errorf("tail 2: %q", got)
	}
	if _, err := f.Logs(ctx, "ghost", dockerapi.LogsOptions{Stdout: true}); !dockerapi.IsNotFound(err) {
		t.Errorf("Logs of an absent container = %v, want a 404", err)
	}
}

// TestDKFakeFollow: a followed stream serves its frames and then waits until
// it is closed or its context ends.
func TestDKFakeFollow(t *testing.T) {
	f := newDKHost(t).docker
	f.put(dkContainer("web"))
	f.logs[dkID("web")] = []dockerapi.LogFrame{{Stream: "stdout", Data: []byte("1")}}

	ctx, cancel := context.WithCancel(context.Background())
	s, err := f.Logs(ctx, "web", dockerapi.LogsOptions{Stdout: true, Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	if fr, err := s.Next(); err != nil || string(fr.Data) != "1" {
		t.Fatalf("first frame = %+v, %v", fr, err)
	}
	res := make(chan error, 1)
	go func() { _, err := s.Next(); res <- err }()
	select {
	case err := <-res:
		t.Fatalf("Next returned %v before the stream ended", err)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	if err := <-res; !errors.Is(err, context.Canceled) {
		t.Errorf("after cancel: %v, want context.Canceled", err)
	}

	s, _ = f.Logs(context.Background(), "web", dockerapi.LogsOptions{Stdout: true, Follow: true})
	_, _ = s.Next()
	go func() { _, err := s.Next(); res <- err }()
	time.Sleep(20 * time.Millisecond)
	_ = s.Close()
	if err := <-res; !errors.Is(err, io.EOF) {
		t.Errorf("after Close: %v, want io.EOF", err)
	}
}

func TestDKFakeStats(t *testing.T) {
	f, ctx := newDKHost(t).docker, context.Background()
	f.put(dkContainer("web", dkRunning))
	f.stats[dkID("web")] = []dockerapi.Stats{{Read: "a"}, {Read: "b"}}
	s, err := f.Stats(ctx, "web", false)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := s.Next(); err != nil || st.Read != "a" {
		t.Errorf("one-shot sample = %+v, %v", st, err)
	}
	if _, err := s.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("one-shot stream ended with %v, want io.EOF", err)
	}
	cctx, cancel := context.WithCancel(ctx)
	s, _ = f.Stats(cctx, "web", true)
	for _, want := range []string{"a", "b"} {
		if st, err := s.Next(); err != nil || st.Read != want {
			t.Errorf("streamed sample = %+v, %v; want %s", st, err, want)
		}
	}
	cancel()
	if _, err := s.Next(); !errors.Is(err, context.Canceled) {
		t.Errorf("after the samples: %v, want the context's error", err)
	}
	if _, err := f.Stats(ctx, "ghost", false); !dockerapi.IsNotFound(err) {
		t.Errorf("Stats of an absent container = %v, want a 404", err)
	}
}

func TestDKFakeInfoAndVersion(t *testing.T) {
	f, ctx := newDKHost(t).docker, context.Background()
	f.put(dkContainer("a", dkRunning))
	f.put(dkContainer("b"))
	f.putImage(dkImage("x:1", 1))
	v, err := f.Version(ctx)
	if err != nil || v.APIVersion != "1.52" || v.MinAPIVersion != "1.44" || v.Version != "29.9.1" {
		t.Errorf("Version = %+v, %v", v, err)
	}
	in, err := f.Info(ctx)
	if err != nil || in.Containers != 2 || in.ContainersRunning != 1 || in.Images != 1 {
		t.Errorf("Info = %+v, %v", in, err)
	}
}

func TestDKHostRunner(t *testing.T) {
	h := newDKHost(t)
	h.results["compose config"] = core.CmdResult{Stdout: []byte("{}"), ExitCode: 1}
	got, err := h.Run(context.Background(), core.Cmd{Path: "/usr/bin/docker", Args: []string{"compose", "config"}})
	if err != nil || got.ExitCode != 1 || string(got.Stdout) != "{}" {
		t.Errorf("scripted result = %+v, %v", got, err)
	}
	got, err = h.Run(context.Background(), core.Cmd{Path: "/usr/bin/docker", Args: []string{"version"}})
	if err != nil || got.ExitCode != 0 || got.Stdout != nil {
		t.Errorf("unscripted result = %+v, %v; want exit 0", got, err)
	}
	if len(h.cmds) != 2 || h.cmds[0].Args[1] != "config" {
		t.Errorf("recorded commands %+v", h.cmds)
	}
}

func TestDKHostFiles(t *testing.T) {
	h := newDKHost(t)
	ctx := context.Background()
	if err := h.WriteManaged(ctx, ID, "/var/lib/x", "config.json", []byte("{}"), 0o600, 0, 0); err != nil {
		t.Fatal(err)
	}
	if string(h.managed["/var/lib/x/config.json"]) != "{}" {
		t.Errorf("managed %v", h.managed)
	}
	if err := h.RemoveManaged(ctx, ID, "/var/lib/x", "config.json"); err != nil || len(h.managed) != 0 {
		t.Errorf("RemoveManaged: %v, %v", err, h.managed)
	}
	for name, err := range map[string]error{
		"WriteFile": h.WriteFile(ctx, ID, "/p", nil, 0o600), "Remove": h.Remove(ctx, ID, "/p"),
		"Rename": h.Rename(ctx, ID, "/a", "/b"), "WriteSysctl": h.WriteSysctl(ctx, ID, "k", "v"),
	} {
		if err == nil {
			t.Errorf("%s succeeded; only the managed-file methods work", name)
		}
	}

	h.files["/srv/p"] = dkFile{Mode: fs.ModeDir | 0o755, UID: 1000}
	h.files["/srv/p/compose.yaml"] = dkFile{Mode: 0o644, UID: 1000, Data: []byte("services: {}")}
	h.files["/srv/link"] = dkFile{Mode: 0o777, Symlink: true}
	k := h.dk()
	fi, err := k.d.lstat("/srv/p")
	if err != nil || !fi.IsDir() || fi.Sys().(*syscall.Stat_t).Uid != 1000 || fi.Name() != "p" {
		t.Errorf("lstat dir = %v, %v", fi, err)
	}
	fi, _ = k.d.lstat("/srv/link")
	if fi.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("lstat symlink mode %v", fi.Mode())
	}
	fi, _ = k.d.lstat("/srv/p/compose.yaml")
	if fi.Size() != 12 || fi.Mode().Perm() != 0o644 {
		t.Errorf("lstat file size %d mode %v", fi.Size(), fi.Mode())
	}
	if _, err := k.d.lstat("/srv/none"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("lstat of a missing path = %v, want fs.ErrNotExist", err)
	}
	if b, err := k.d.readFile("/srv/p/compose.yaml"); err != nil || string(b) != "services: {}" {
		t.Errorf("readFile = %q, %v", b, err)
	}
	if _, err := k.d.readFile("/srv/none"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("readFile of a missing path = %v, want fs.ErrNotExist", err)
	}
	h.secrets = [][]byte{[]byte("s3")}
	if s, err := k.d.secrets(ctx, h.env(""), "c"); err != nil || len(s) != 1 || string(s[0]) != "s3" {
		t.Errorf("secrets = %q, %v", s, err)
	}
	if k.d.docker != dockerAPI(h.docker) {
		t.Error("dk() does not use the host's fake Docker")
	}
}

func TestDKHostEnv(t *testing.T) {
	h := newDKHost(t)
	env := h.env("c1")
	if env.HostID != "h1" || env.ChangeID != "c1" || env.Principal.Scope != core.ScopeAdmin || env.Effects == nil ||
		env.DB != h.db || env.Sealer == nil || env.Clock == nil || env.Log == nil || env.Exec == nil || env.Files == nil {
		t.Errorf("env %+v", env)
	}
	if h.env("").Effects != nil {
		t.Error("an env without a change id must have no effects journal")
	}
	var n int
	if err := h.db.Read().QueryRow("SELECT count(*) FROM core_effects").Scan(&n); err != nil {
		t.Errorf("the host database lacks the core tables: %v", err)
	}
	sealed, err := env.Sealer.Seal("ad", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if b, err := env.Sealer.Open("ad", sealed); err != nil || string(b) != "x" {
		t.Errorf("sealer round trip = %q, %v", b, err)
	}
}

func TestDKJournal(t *testing.T) {
	h := newDKHost(t)
	ctx := context.Background()
	j := h.env("c7").Effects
	seq, err := j.Intend(ctx, core.Effect{Kind: core.EffectModify, Resource: "docker.container:a"}, []byte("secret"))
	if err != nil || seq != 1 {
		t.Fatalf("Intend = %d, %v", seq, err)
	}
	if err := j.Made(ctx, seq, "docker.container:a", core.StateDoc{State: []byte(`{"id":"a"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := j.Made(ctx, seq, "docker.container:a", core.StateDoc{}); err == nil {
		t.Error("Made of a made effect succeeded")
	}
	seq2, _ := j.Intend(ctx, core.Effect{Kind: core.EffectModify, Resource: "docker.container:b"}, nil)
	if err := j.Skipped(ctx, seq2); err != nil {
		t.Fatal(err)
	}
	if err := j.Skipped(ctx, 9); err == nil {
		t.Error("Skipped of an unknown effect succeeded")
	}
	es := h.journal.effects()
	if len(es) != 2 || es[0].State != core.EffectMade || es[0].ChangeID != "c7" || es[0].Seq != 1 ||
		string(es[0].Post.State) != `{"id":"a"}` || es[1].State != core.EffectSkipped {
		t.Errorf("effects %+v", es)
	}
	if s, err := j.Secret(ctx, 1); err != nil || string(s) != "secret" {
		t.Errorf("Secret = %q, %v", s, err)
	}
	if s, err := j.Secret(ctx, 2); err != nil || s != nil {
		t.Errorf("Secret of an effect with none = %q, %v", s, err)
	}
	if listed, err := j.List(ctx); err != nil || len(listed) != 2 {
		t.Errorf("List = %v, %v", listed, err)
	}
}
