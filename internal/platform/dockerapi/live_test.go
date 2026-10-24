package dockerapi

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestDockerLive drives the host's Docker daemon, so it runs only when
// DARKPANEL_DOCKER_LIVE=1. It proves the wire shapes the fake daemon only
// asserts. Every object it creates carries the label darkpanel.live and is
// removed again; DARKPANEL_DOCKER_LIVE_IMAGE names the image (default
// alpine:3), which the test pulls.
func TestDockerLive(t *testing.T) {
	if os.Getenv("DARKPANEL_DOCKER_LIVE") != "1" {
		t.Skip("set DARKPANEL_DOCKER_LIVE=1 to drive the host's Docker daemon")
	}
	image := os.Getenv("DARKPANEL_DOCKER_LIVE_IMAGE")
	if image == "" {
		image = "alpine:3"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c := New(DefaultSocket)
	defer c.Close()
	prefix := fmt.Sprintf("dp-live-%d-%d", os.Getpid(), time.Now().UnixNano())
	labels := map[string]string{"darkpanel.live": prefix}
	t.Cleanup(func() {
		bg := context.Background()
		_ = c.RemoveContainer(bg, prefix, RemoveContainerOptions{Force: true})
		_ = c.RemoveNetwork(bg, prefix)
		_ = c.RemoveVolume(bg, prefix, true)
	})

	v, err := c.APIVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cmp, _ := compareVersions(v, MaxAPIVersion); cmp > 0 {
		t.Fatalf("negotiated %s above %s", v, MaxAPIVersion)
	}
	info, err := c.Info(ctx)
	if err != nil || info.ServerVersion == "" {
		t.Fatalf("info = %+v, %v", info, err)
	}
	if v == MaxAPIVersion && (info.FirewallBackend == nil || info.FirewallBackend.Driver == "") {
		t.Fatalf("API %s reports no firewall backend: %+v", v, info)
	}

	pull, err := c.PullImage(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	for {
		_, err := pull.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	_ = pull.Close()
	if _, err := c.InspectImage(ctx, image); err != nil {
		t.Fatal(err)
	}

	events, err := c.Events(ctx, EventsOptions{Since: time.Now(), Filters: Filters{"label": {"darkpanel.live=" + prefix}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = events.Close() }()
	seen := make(chan string, 64)
	go func() {
		for {
			ev, err := events.Next()
			if err != nil {
				close(seen)
				return
			}
			seen <- ev.Type + "/" + ev.Action
		}
	}()

	if _, err := c.CreateVolume(ctx, VolumeCreate{Name: prefix, Labels: labels}); err != nil {
		t.Fatal(err)
	}
	netID, err := c.CreateNetwork(ctx, NetworkCreate{Name: prefix, Driver: "bridge", Labels: labels})
	if err != nil {
		t.Fatal(err)
	}
	id, err := c.CreateContainer(ctx, ContainerCreate{
		Name: prefix,
		ContainerConfig: ContainerConfig{
			Image:  image,
			Cmd:    []string{"sh", "-c", "echo out; echo err >&2; exec sleep 300"},
			Labels: labels,
		},
		HostConfig: &HostConfig{Mounts: []Mount{{Type: "volume", Source: prefix, Target: "/data"}}},
		NetworkingConfig: &NetworkingConfig{EndpointsConfig: map[string]*EndpointSettings{
			prefix: {Aliases: []string{"dpalias"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := c.StartContainer(ctx, id); err != nil || !changed {
		t.Fatalf("start = %v, %v", changed, err)
	}
	if changed, err := c.StartContainer(ctx, id); err != nil || changed {
		t.Fatalf("second start = %v, %v; want the daemon's 304", changed, err)
	}

	got, err := c.InspectContainer(ctx, prefix)
	if err != nil {
		t.Fatal(err)
	}
	ep := got.NetworkSettings.Networks[prefix]
	if !got.State.Running || got.ID != id || ep == nil || !slices.Contains(ep.Aliases, "dpalias") || !ep.IPAddress.IsValid() {
		t.Fatalf("inspect = %+v, endpoint %+v", got, ep)
	}
	if _, err := time.Parse(time.RFC3339Nano, got.Created); err != nil {
		t.Fatalf("Created %q: %v", got.Created, err)
	}
	if len(got.Mounts) != 1 || got.Mounts[0].Name != prefix {
		t.Fatalf("mounts = %+v", got.Mounts)
	}
	n, err := c.InspectNetwork(ctx, netID)
	if err != nil || n.Containers[id].IPv4Address.Addr() != ep.IPAddress {
		t.Fatalf("network = %+v, %v", n, err)
	}
	list, err := c.ListContainers(ctx, ListContainersOptions{Filters: Filters{"label": {"darkpanel.live=" + prefix}}})
	if err != nil || len(list) != 1 || list[0].ID != id {
		t.Fatalf("list = %+v, %v", list, err)
	}

	var stdout, stderr string
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		s, err := c.Logs(ctx, id, LogsOptions{Stdout: true, Stderr: true})
		if err != nil {
			t.Fatal(err)
		}
		stdout, stderr = "", ""
		for {
			f, err := s.Next()
			if err != nil {
				break
			}
			if f.Stream == "stderr" {
				stderr += string(f.Data)
			} else {
				stdout += string(f.Data)
			}
		}
		_ = s.Close()
		if stdout == "out\n" && stderr == "err\n" {
			break
		}
	}
	if stdout != "out\n" || stderr != "err\n" {
		t.Fatalf("logs stdout %q stderr %q", stdout, stderr)
	}

	stats, err := c.Stats(ctx, id, false)
	if err != nil {
		t.Fatal(err)
	}
	sample, err := stats.Next()
	_ = stats.Close()
	if err != nil || sample.Read == "" || sample.PidsStats.Current == 0 {
		t.Fatalf("stats = %+v, %v", sample, err)
	}

	var tarball bytes.Buffer
	tw := tar.NewWriter(&tarball)
	_ = tw.WriteHeader(&tar.Header{Name: "hello", Mode: 0o644, Size: 5})
	_, _ = tw.Write([]byte("world"))
	_ = tw.Close()
	if err := c.CopyToContainer(ctx, id, "/data", &tarball); err != nil {
		t.Fatal(err)
	}
	rc, err := c.CopyFromContainer(ctx, id, "/data/hello")
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(rc)
	if hdr, err := tr.Next(); err != nil || hdr.Name != "hello" {
		t.Fatalf("archive entry = %+v, %v", hdr, err)
	}
	if b, _ := io.ReadAll(tr); string(b) != "world" {
		t.Fatalf("archive content = %q", b)
	}
	_ = rc.Close()

	if err := c.DisconnectNetwork(ctx, netID, id, false); err != nil {
		t.Fatal(err)
	}
	if err := c.ConnectNetwork(ctx, netID, id, &EndpointSettings{Aliases: []string{"again"}}); err != nil {
		t.Fatal(err)
	}
	if got, err = c.InspectContainer(ctx, id); err != nil || !slices.Contains(got.NetworkSettings.Networks[prefix].Aliases, "again") {
		t.Fatalf("reconnected endpoint = %+v, %v", got.NetworkSettings.Networks[prefix], err)
	}
	if err := c.RemoveVolume(ctx, prefix, false); !IsConflict(err) {
		t.Fatalf("removing a volume in use err = %v, want 409", err)
	}
	if changed, err := c.StopContainer(ctx, id, 2*time.Second); err != nil || !changed {
		t.Fatalf("stop = %v, %v", changed, err)
	}
	if changed, err := c.StopContainer(ctx, id, 2*time.Second); err != nil || changed {
		t.Fatalf("second stop = %v, %v; want the daemon's 304", changed, err)
	}
	if err := c.RemoveContainer(ctx, id, RemoveContainerOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveNetwork(ctx, netID); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveVolume(ctx, prefix, false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InspectVolume(ctx, prefix); !IsNotFound(err) {
		t.Fatalf("removed volume err = %v", err)
	}

	want := []string{"container/create", "container/start", "container/die", "container/destroy"}
	var got2 []string
	timeout := time.After(10 * time.Second)
	for len(want) > 0 {
		select {
		case ev, ok := <-seen:
			if !ok {
				t.Fatalf("event stream ended; still waiting for %v (seen %v)", want, got2)
			}
			got2 = append(got2, ev)
			if ev == want[0] {
				want = want[1:]
			}
		case <-timeout:
			t.Fatalf("still waiting for %v; seen %s", want, strings.Join(got2, " "))
		}
	}
}
