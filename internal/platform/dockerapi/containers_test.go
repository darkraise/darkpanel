package dockerapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"reflect"
	"testing"
	"time"
)

const ctInspectJSON = `{
  "Id": "4fa6e0f0c678",
  "Created": "2026-10-23T09:15:02.123456789Z",
  "Name": "/web",
  "Image": "sha256:aaaa",
  "RestartCount": 1,
  "State": {"Status": "running", "Running": true, "Pid": 4242, "ExitCode": 0,
    "StartedAt": "2026-10-23T09:15:03.5Z", "FinishedAt": "0001-01-01T00:00:00Z"},
  "Config": {"Hostname": "4fa6e0f0c678", "Env": ["A=1", "SECRET=x"], "Cmd": ["nginx", "-g", "daemon off;"],
    "Image": "nginx:1.27", "Labels": {"com.docker.compose.project": "site"}, "ExposedPorts": {"80/tcp": {}}, "Tty": false},
  "HostConfig": {"NetworkMode": "site_default", "PortBindings": {"80/tcp": [{"HostIp": "", "HostPort": "8080"}]},
    "RestartPolicy": {"Name": "unless-stopped", "MaximumRetryCount": 0}},
  "NetworkSettings": {
    "Ports": {"80/tcp": [{"HostIp": "0.0.0.0", "HostPort": "8080"}, {"HostIp": "::", "HostPort": "8080"}]},
    "Networks": {"site_default": {"IPAMConfig": null, "Aliases": ["web"], "NetworkID": "net1", "EndpointID": "ep1",
      "Gateway": "172.20.0.1", "IPAddress": "172.20.0.2", "IPPrefixLen": 16, "IPv6Gateway": "",
      "GlobalIPv6Address": "", "GlobalIPv6PrefixLen": 0, "MacAddress": "02:42:ac:14:00:02", "DNSNames": ["web", "4fa6e0f0c678"]}}
  },
  "Mounts": [{"Type": "volume", "Name": "site_data", "Source": "/var/lib/docker/volumes/site_data/_data", "Destination": "/data", "RW": true}]
}`

func TestInspectContainer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1.49/containers/{id}/json", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "web" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"No such container: `+r.PathValue("id")+`"}`) //nolint:gosec // G705: a test fake answering JSON
			return
		}
		_, _ = io.WriteString(w, ctInspectJSON)
	})
	c := newFakeDaemon(t, "1.49", mux)
	got, err := c.InspectContainer(t.Context(), "web")
	if err != nil {
		t.Fatal(err)
	}
	ep := got.NetworkSettings.Networks["site_default"]
	if got.ID != "4fa6e0f0c678" || got.Created != "2026-10-23T09:15:02.123456789Z" || !got.State.Running ||
		got.State.StartedAt != "2026-10-23T09:15:03.5Z" || got.HostConfig.RestartPolicy.Name != "unless-stopped" ||
		got.Config.Labels["com.docker.compose.project"] != "site" || got.Mounts[0].Name != "site_data" {
		t.Fatalf("inspect = %+v", got)
	}
	if ep == nil || ep.IPAddress != netip.MustParseAddr("172.20.0.2") || ep.GlobalIPv6Address.IsValid() || ep.IPAMConfig != nil {
		t.Fatalf("endpoint = %+v", ep)
	}
	if b := got.NetworkSettings.Ports["80/tcp"]; len(b) != 2 || b[1].HostIP != "::" || b[1].HostPort != "8080" {
		t.Fatalf("ports = %+v", got.NetworkSettings.Ports)
	}
	if _, err := c.InspectContainer(t.Context(), "gone"); !IsNotFound(err) {
		t.Fatalf("missing container err = %v", err)
	}
}

func TestListContainers(t *testing.T) {
	mux := http.NewServeMux()
	var query string
	mux.HandleFunc("GET /v1.49/containers/json", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = io.WriteString(w, `[{"Id":"c1","Names":["/web"],"Image":"nginx","ImageID":"sha256:aaaa","Created":1761210902,`+
			`"State":"running","Status":"Up 2 minutes","Ports":[{"IP":"0.0.0.0","PrivatePort":80,"PublicPort":8080,"Type":"tcp"},{"PrivatePort":443,"Type":"tcp"}],`+
			`"Labels":{"darkpanel.module":"npm"},"NetworkSettings":{"Networks":{"bridge":{"IPAddress":"172.17.0.2"}}}}]`)
	})
	c := newFakeDaemon(t, "1.49", mux)
	got, err := c.ListContainers(t.Context(), ListContainersOptions{All: true, Filters: Filters{"label": {"darkpanel.module"}}})
	if err != nil {
		t.Fatal(err)
	}
	if query != "all=1&filters=%7B%22label%22%3A%5B%22darkpanel.module%22%5D%7D" {
		t.Fatalf("query = %s", query)
	}
	if len(got) != 1 || got[0].Names[0] != "/web" || got[0].Ports[0].PublicPort != 8080 || got[0].Ports[1].IP != "" ||
		got[0].NetworkSettings.Networks["bridge"].IPAddress != netip.MustParseAddr("172.17.0.2") {
		t.Fatalf("list = %+v", got)
	}
}

func TestCreateContainerBody(t *testing.T) {
	mux := http.NewServeMux()
	var name string
	var body map[string]any
	mux.HandleFunc("POST /v1.49/containers/create", func(w http.ResponseWriter, r *http.Request) {
		name = r.URL.Query().Get("name")
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type %q", r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"Id":"new1","Warnings":[]}`)
	})
	c := newFakeDaemon(t, "1.49", mux)
	id, err := c.CreateContainer(t.Context(), ContainerCreate{
		Name: "darkpanel-npm",
		ContainerConfig: ContainerConfig{
			Image:  "jc21/nginx-proxy-manager@sha256:bbbb",
			Labels: map[string]string{"darkpanel.module": "npm"},
		},
		HostConfig: &HostConfig{
			PortBindings:  map[string][]PortBinding{"81/tcp": {{HostIP: "127.0.0.1", HostPort: "81"}}},
			RestartPolicy: RestartPolicy{Name: "unless-stopped"},
			Mounts:        []Mount{{Type: "volume", Source: "darkpanel-npm-data", Target: "/data"}},
		},
		NetworkingConfig: &NetworkingConfig{EndpointsConfig: map[string]*EndpointSettings{
			"darkpanel-npm": {IPAMConfig: &EndpointIPAMConfig{IPv4Address: netip.MustParseAddr("10.9.0.2")}},
		}},
	})
	if err != nil || id != "new1" || name != "darkpanel-npm" {
		t.Fatalf("create = %q, %v (name %q)", id, err, name)
	}
	want := map[string]any{
		"Image":  "jc21/nginx-proxy-manager@sha256:bbbb",
		"Labels": map[string]any{"darkpanel.module": "npm"},
		"HostConfig": map[string]any{
			"PortBindings":  map[string]any{"81/tcp": []any{map[string]any{"HostIp": "127.0.0.1", "HostPort": "81"}}},
			"RestartPolicy": map[string]any{"Name": "unless-stopped"},
			"Mounts":        []any{map[string]any{"Type": "volume", "Source": "darkpanel-npm-data", "Target": "/data"}},
		},
		"NetworkingConfig": map[string]any{"EndpointsConfig": map[string]any{
			"darkpanel-npm": map[string]any{"IPAMConfig": map[string]any{"IPv4Address": "10.9.0.2"}},
		}},
	}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %#v", body)
	}
}

func TestStartStopRestartRemove(t *testing.T) {
	mux := http.NewServeMux()
	var seen []string
	mux.HandleFunc("POST /v1.49/containers/{id}/{op}", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.PathValue("op")+" "+r.PathValue("id")+" "+r.URL.RawQuery)
		switch r.PathValue("id") {
		case "already":
			w.WriteHeader(http.StatusNotModified)
		case "broken":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"message":"cannot start"}`)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	mux.HandleFunc("DELETE /v1.49/containers/{id}", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, "delete "+r.PathValue("id")+" "+r.URL.RawQuery)
		if r.PathValue("id") == "busy" {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"message":"container is running"}`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	c := newFakeDaemon(t, "1.49", mux)
	ctx := t.Context()
	if changed, err := c.StartContainer(ctx, "web"); err != nil || !changed {
		t.Fatalf("start = %v, %v", changed, err)
	}
	if changed, err := c.StartContainer(ctx, "already"); err != nil || changed {
		t.Fatalf("start of a running container = %v, %v", changed, err)
	}
	if changed, err := c.StopContainer(ctx, "web", 10*time.Second); err != nil || !changed {
		t.Fatalf("stop = %v, %v", changed, err)
	}
	if changed, err := c.StopContainer(ctx, "already", 0); err != nil || changed {
		t.Fatalf("stop of a stopped container = %v, %v", changed, err)
	}
	if changed, err := c.StartContainer(ctx, "broken"); err == nil || changed {
		t.Fatalf("start answered 500: changed = %v, err = %v", changed, err)
	}
	if changed, err := c.StopContainer(ctx, "broken", time.Second); err == nil || changed {
		t.Fatalf("stop answered 500: changed = %v, err = %v", changed, err)
	}
	if err := c.RestartContainer(ctx, "web", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveContainer(ctx, "web", RemoveContainerOptions{Force: true, Volumes: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveContainer(ctx, "busy", RemoveContainerOptions{}); !IsConflict(err) {
		t.Fatalf("remove of a running container err = %v", err)
	}
	want := []string{
		"start web ", "start already ", "stop web t=10", "stop already t=0", "start broken ", "stop broken t=1", "restart web t=5",
		"delete web force=true&v=true", "delete busy force=false&v=false",
	}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("requests = %q", seen)
	}
}
