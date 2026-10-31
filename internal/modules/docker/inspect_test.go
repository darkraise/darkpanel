package docker

import (
	"context"
	"encoding/json"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// rdSecretValues are the environment values of rdWeb that must never appear in an inspect answer.
var rdSecretValues = []string{"hunter2", "a=b=c", "/usr/local/bin"}

// rdWeb is a running Compose container with every member the inspect answer reports.
func rdWeb(opts ...func(*dockerapi.ContainerJSON)) dockerapi.ContainerJSON {
	base := func(c *dockerapi.ContainerJSON) {
		c.Created = "2026-10-01T02:00:00.5+02:00"
		c.Image = "sha256:" + strings.Repeat("a", 64)
		c.State = dockerapi.ContainerState{Status: "running", Running: true, ExitCode: 0,
			StartedAt: "2026-10-01T10:00:00.25+02:00", FinishedAt: "0001-01-01T00:00:00Z"}
		c.Config = dockerapi.ContainerConfig{
			Image:      "nginx:1.27",
			Env:        []string{"PATH=/usr/local/bin", "DB_PASSWORD=hunter2", "EMPTY=", "FLAG", "TOKEN=a=b=c"},
			Cmd:        []string{"nginx", "-g", "daemon off;"},
			Entrypoint: []string{"/docker-entrypoint.sh"},
			WorkingDir: "/srv",
			User:       "nginx",
			Labels:     map[string]string{"com.docker.compose.project": "shop", "com.docker.compose.service": "web", "tier": "front"},
		}
		c.HostConfig.RestartPolicy = dockerapi.RestartPolicy{Name: "unless-stopped"}
		c.NetworkSettings = dockerapi.NetworkSettings{
			Ports: map[string][]dockerapi.PortBinding{
				"80/tcp":   {{HostIP: "::", HostPort: "8080"}, {HostIP: "0.0.0.0", HostPort: "8080"}},
				"53/udp":   {{HostIP: "", HostPort: "5353"}},
				"443/tcp":  nil,
				"9000/tcp": {{HostIP: "127.0.0.1", HostPort: ""}},
			},
			Networks: map[string]*dockerapi.EndpointSettings{
				"bridge": {NetworkID: dkID("bridge"), EndpointID: "ep-bridge", IPAddress: netip.MustParseAddr("172.17.0.2")},
				"app": {NetworkID: dkID("app"), EndpointID: "ep-app", Aliases: []string{"web", "api"},
					IPAddress: netip.MustParseAddr("10.0.0.2"), GlobalIPv6Address: netip.MustParseAddr("fd00::2")},
			},
		}
		c.Mounts = []dockerapi.MountPoint{
			{Type: "volume", Name: "data", Source: "/var/lib/docker/volumes/data/_data", Destination: "/data", RW: true},
			{Type: "bind", Source: "/etc/web.conf", Destination: "/etc/nginx/nginx.conf", RW: false},
		}
	}
	return dkContainer("web", append([]func(*dockerapi.ContainerJSON){base}, opts...)...)
}

func rdOwned(c *dockerapi.ContainerJSON) { c.Config.Labels["darkpanel.module"] = "npm" }

// rdRead runs a read action's handler with a raw JSON input.
func rdRead(t *testing.T, def core.ActionDef, h *dkHost, input string) (json.RawMessage, error) {
	t.Helper()
	return def.Handler.Read(context.Background(), h.env(""), json.RawMessage(input))
}

// rdSame compares two JSON documents without regard to member order.
func rdSame(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("answer is not JSON: %v\n%s", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("expectation is not JSON: %v", err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("answer\n%s\nwant\n%s", got, want)
	}
}

func rdCode(err error) errcode.Code { return errcode.From(err).Code }

const rdWebDetail = `{
  "id": "%ID%", "name": "web", "image": "nginx:1.27", "imageId": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "created": "2026-10-01T00:00:00.5Z", "state": "running", "running": true,
  "startedAt": "2026-10-01T08:00:00.25Z", "exitCode": 0, "restartPolicy": "unless-stopped",
  "cmd": ["nginx", "-g", "daemon off;"], "entrypoint": ["/docker-entrypoint.sh"], "workingDir": "/srv", "user": "nginx",
  "env": [
    {"name": "PATH", "value": "[redacted]"}, {"name": "DB_PASSWORD", "value": "[redacted]"}, {"name": "EMPTY", "value": "[redacted]"},
    {"name": "FLAG", "value": "[redacted]"}, {"name": "TOKEN", "value": "[redacted]"}
  ],
  "labels": {"com.docker.compose.project": "shop", "com.docker.compose.service": "web", "tier": "front"},
  "ports": [
    {"privatePort": 53, "publicPort": 5353, "proto": "udp"},
    {"ip": "0.0.0.0", "privatePort": 80, "publicPort": 8080, "proto": "tcp"},
    {"ip": "::", "privatePort": 80, "publicPort": 8080, "proto": "tcp"}
  ],
  "mounts": [
    {"type": "volume", "name": "data", "source": "/var/lib/docker/volumes/data/_data", "destination": "/data", "rw": true},
    {"type": "bind", "source": "/etc/web.conf", "destination": "/etc/nginx/nginx.conf", "rw": false}
  ],
  "networks": [
    {"network": "app", "networkId": "%APP%", "container": "web", "endpointId": "ep-app", "aliases": ["web", "api"], "ipv4": "10.0.0.2", "ipv6": "fd00::2"},
    {"network": "bridge", "networkId": "%BRIDGE%", "container": "web", "endpointId": "ep-bridge", "ipv4": "172.17.0.2"}
  ],
  "project": "shop", "service": "web"
}`

func rdExpectedDetail(owner string) string {
	s := strings.NewReplacer("%ID%", dkID("web"), "%APP%", dkID("app"), "%BRIDGE%", dkID("bridge")).Replace(rdWebDetail)
	if owner != "" {
		s = strings.Replace(s, `"service": "web"`, `"service": "web", "owner": "`+owner+`"`, 1)
		s = strings.Replace(s, `"tier": "front"`, `"tier": "front", "darkpanel.module": "`+owner+`"`, 1)
	}
	return s
}

func TestRdInspectReportsTheContainer(t *testing.T) {
	for _, tc := range []struct {
		name  string
		opts  []func(*dockerapi.ContainerJSON)
		owner string
	}{
		{"unlabelled", nil, ""},
		{"owned by a module", []func(*dockerapi.ContainerJSON){rdOwned}, "npm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDKHost(t)
			h.docker.put(rdWeb(tc.opts...))
			for _, ref := range []string{"web", dkID("web")} {
				raw, err := rdRead(t, h.dk().containerInspect(), h, `{"container":"`+ref+`"}`)
				if err != nil {
					t.Fatalf("inspect %q: %v", ref, err)
				}
				rdSame(t, raw, rdExpectedDetail(tc.owner))
				for _, secret := range rdSecretValues {
					if strings.Contains(string(raw), secret) {
						t.Errorf("inspect %q leaks the environment value %q:\n%s", ref, secret, raw)
					}
				}
			}
		})
	}
}

func TestRdInspectOfAStoppedContainerKeepsEmptyListsAsArrays(t *testing.T) {
	h := newDKHost(t)
	h.docker.put(dkContainer("job", func(c *dockerapi.ContainerJSON) {
		c.Image = "sha256:" + strings.Repeat("b", 64)
		c.State = dockerapi.ContainerState{Status: "exited", ExitCode: 137,
			StartedAt: "0001-01-01T00:00:00Z", FinishedAt: "2026-10-01T11:00:00+01:00"}
		c.Config = dockerapi.ContainerConfig{Image: "busybox"}
	}))
	raw, err := rdRead(t, h.dk().containerInspect(), h, `{"container":"job"}`)
	if err != nil {
		t.Fatal(err)
	}
	rdSame(t, raw, `{"id":"`+dkID("job")+`","name":"job","image":"busybox","imageId":"sha256:`+strings.Repeat("b", 64)+`",
	  "created":"2026-10-01T00:00:00Z","state":"exited","running":false,"finishedAt":"2026-10-01T10:00:00Z","exitCode":137,
	  "env":[],"ports":[],"mounts":[],"networks":[]}`)
}

func TestRdInspectRefusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		prep  func(h *dkHost)
		code  errcode.Code
		path  string
	}{
		{"an empty container", `{"container":""}`, nil, errcode.InvalidInput, "/container"},
		{"an absent container", `{"container":"ghost"}`, nil, errcode.NotFound, ""},
		{"an unreachable daemon", `{"container":"web"}`, func(h *dkHost) { h.docker.fail("InspectContainer", dockerapi.ErrUnavailable) }, errcode.DockerEngineUnavailable, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDKHost(t)
			h.docker.put(rdWeb())
			if tc.prep != nil {
				tc.prep(h)
			}
			_, err := rdRead(t, h.dk().containerInspect(), h, tc.input)
			if rdCode(err) != tc.code {
				t.Fatalf("code %q (%v), want %q", rdCode(err), err, tc.code)
			}
			if tc.path != "" {
				if f := errcode.From(err).Fields; len(f) != 1 || f[0].Path != tc.path {
					t.Errorf("fields %+v, want one at %s", f, tc.path)
				}
			}
		})
	}
}

func TestRdRevealEnvReturnsTheValuesAndNamesTheContainerForTheAudit(t *testing.T) {
	h := newDKHost(t)
	h.docker.put(rdWeb())
	def := h.dk().containerRevealEnv()
	identity, value, err := def.Handler.Reveal(context.Background(), h.env(""), json.RawMessage(`{"container":"web"}`))
	if err != nil {
		t.Fatal(err)
	}
	if identity != "environment of container web, host h1" {
		t.Errorf("identity %q", identity)
	}
	rdSame(t, value, `{"env":[
	  {"name":"PATH","value":"/usr/local/bin"},{"name":"DB_PASSWORD","value":"hunter2"},{"name":"EMPTY","value":""},
	  {"name":"FLAG","value":""},{"name":"TOKEN","value":"a=b=c"}]}`)
	if !def.Spec.Reveal || def.Spec.Class != core.ClassRead || def.Spec.MinScope != core.ScopeAdmin || def.Spec.Side != core.SideAgent {
		t.Errorf("spec %+v: want a reveal read action on the agent for admin", def.Spec)
	}
}

func TestRdRevealEnvRefusesAnOwnedContainerWithoutReadingItsValues(t *testing.T) {
	h := newDKHost(t)
	h.docker.put(rdWeb(rdOwned))
	_, value, err := h.dk().containerRevealEnv().Handler.Reveal(context.Background(), h.env(""), json.RawMessage(`{"container":"web"}`))
	if rdCode(err) != errcode.DockerOwned || !core.IsDefinite(err) {
		t.Fatalf("error %v: want a definite docker.owned", err)
	}
	if d := errcode.From(err).Detail; d != "container web belongs to module npm" {
		t.Errorf("detail %q", d)
	}
	if len(value) != 0 {
		t.Errorf("value %s returned with the refusal", value)
	}
}

func TestRdRevealEnvRefusals(t *testing.T) {
	h := newDKHost(t)
	for input, code := range map[string]errcode.Code{`{"container":""}`: errcode.InvalidInput, `{"container":"ghost"}`: errcode.NotFound} {
		if _, _, err := h.dk().containerRevealEnv().Handler.Reveal(context.Background(), h.env(""), json.RawMessage(input)); rdCode(err) != code {
			t.Errorf("reveal %s: code %q (%v), want %q", input, rdCode(err), err, code)
		}
	}
}

func TestRdReadActionsAreUnlockedAgentReads(t *testing.T) {
	k := newDKHost(t).dk()
	for name, def := range map[string]core.ActionDef{"docker.container.inspect": k.containerInspect(), "docker.container.logs": k.containerLogs()} {
		s := def.Spec
		if s.Name != name || s.Version != 1 || s.Class != core.ClassRead || s.Side != core.SideAgent || len(s.LockKeys) != 0 || s.Reveal || s.MinScope != "" {
			t.Errorf("%s: spec %+v", name, s)
		}
		if def.Handler.Read == nil || def.Handler.Reveal != nil {
			t.Errorf("%s: want a Read handler and no Reveal handler", name)
		}
	}
	if s := k.containerRevealEnv().Spec; s.Name != "docker.container.reveal-env" || s.Version != 1 || len(s.LockKeys) != 0 {
		t.Errorf("reveal-env spec %+v", s)
	}
}
