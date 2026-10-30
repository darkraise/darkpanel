package compose

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// cpLoad reads testdata/<name>.json.
func cpLoad(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".json")) //nolint:gosec // G304: name is a constant of the calling test.
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func cpKeys(k ...string) []string { return k }

// TestCPParseScenarios parses real `docker compose config --format json`
// output, one file per scenario of testdata/capture.sh.
func TestCPParseScenarios(t *testing.T) {
	tests := []struct {
		name string
		want Config
	}{
		{"simple", Config{
			Name:    "simple",
			TopKeys: cpKeys("name", "networks", "services", "volumes"),
			Services: map[string]Service{
				"db": {Keys: cpKeys("command", "entrypoint", "image", "networks"), Image: "postgres:16",
					Networks: cpKeys("back")},
				"web": {
					Keys: cpKeys("command", "container_name", "entrypoint", "environment", "image", "labels", "networks",
						"ports", "volumes"),
					Image: "nginx:1.27", ContainerName: "simple-web", Networks: cpKeys("back", "front"),
					Volumes: cpKeys("data"), Labels: map[string]string{"com.example.tier": "front"}},
			},
			Networks: map[string]Object{
				"back": {Keys: cpKeys("internal", "ipam", "labels", "name"), Name: "simple_back",
					Labels: map[string]string{"com.example.net": "back"}},
				"front": {Keys: cpKeys("ipam", "name"), Name: "simple_front"},
			},
			Volumes: map[string]Object{
				"data": {Keys: cpKeys("labels", "name"), Name: "simple_data",
					Labels: map[string]string{"com.example.vol": "data"}},
			},
		}},
		{"build", Config{
			Name:    "build",
			TopKeys: cpKeys("name", "networks", "services"),
			Services: map[string]Service{
				"app":    {Keys: cpKeys("build", "command", "entrypoint", "networks"), Build: true, Networks: cpKeys("default")},
				"plain":  {Keys: cpKeys("command", "entrypoint", "image", "networks"), Image: "busybox:1.36", Networks: cpKeys("default")},
				"tagged": {Keys: cpKeys("build", "command", "entrypoint", "image", "networks"), Build: true, Image: "example/tagged:1", Networks: cpKeys("default")},
			},
			Networks: map[string]Object{"default": {Keys: cpKeys("ipam", "name"), Name: "build_default"}},
		}},
		{"provider", Config{
			Name:    "provider",
			TopKeys: cpKeys("name", "networks", "services"),
			Services: map[string]Service{
				"ext": {Keys: cpKeys("command", "entrypoint", "provider"), Provider: true},
				"web": {Keys: cpKeys("command", "entrypoint", "image", "networks"), Image: "nginx:1.27", Networks: cpKeys("default")},
			},
			Networks: map[string]Object{"default": {Keys: cpKeys("ipam", "name"), Name: "provider_default"}},
		}},
		{"profiles", Config{
			Name:    "profiles",
			TopKeys: cpKeys("name", "networks", "services"),
			Services: map[string]Service{
				"debug": {Keys: cpKeys("command", "entrypoint", "image", "networks", "profiles"), Image: "busybox:1.36", Networks: cpKeys("default")},
				"web":   {Keys: cpKeys("command", "entrypoint", "image", "networks"), Image: "nginx:1.27", Networks: cpKeys("default")},
			},
			Networks: map[string]Object{"default": {Keys: cpKeys("ipam", "name"), Name: "profiles_default"}},
		}},
		{"external", Config{
			Name:    "external",
			TopKeys: cpKeys("configs", "name", "networks", "secrets", "services", "volumes", "x-note"),
			Services: map[string]Service{
				"api": {Keys: cpKeys("command", "entrypoint", "image", "labels", "networks", "volumes"), Image: "nginx:1.27",
					Labels: map[string]string{"a": "b", "c": ""}, Networks: cpKeys("shared"), Volumes: cpKeys("plain")},
				"web": {Keys: cpKeys("command", "configs", "entrypoint", "image", "network_mode", "secrets", "volumes"),
					Image: "nginx:1.27", Volumes: cpKeys("ext", "other")},
			},
			Networks: map[string]Object{"shared": {Keys: cpKeys("external", "ipam", "name"), Name: "my-shared-net", External: true}},
			Volumes: map[string]Object{
				"ext":   {Keys: cpKeys("external", "name"), Name: "ext", External: true},
				"other": {Keys: cpKeys("external", "name"), Name: "real-other", External: true},
				"plain": {Keys: cpKeys("name"), Name: "external_plain"},
			},
			Configs: map[string]Object{"cfg": {Keys: cpKeys("content", "labels", "name"), Name: "external_cfg",
				Labels: map[string]string{"k": "v"}}},
			Secrets: map[string]Object{"sec": {Keys: cpKeys("environment", "name"), Name: "external_sec"}},
		}},
		{"include", Config{
			Name:    "include",
			TopKeys: cpKeys("name", "networks", "services"),
			Services: map[string]Service{
				"included": {Keys: cpKeys("command", "entrypoint", "image", "networks"), Image: "busybox:1.36", Networks: cpKeys("default")},
				"main":     {Keys: cpKeys("command", "entrypoint", "image", "networks"), Image: "nginx:1.27", Networks: cpKeys("default")},
			},
			Networks: map[string]Object{"default": {Keys: cpKeys("ipam", "name"), Name: "include_default"}},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(cpLoad(t, tc.name))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

// TestCPParseRecordsUnknownKeys: members Parse does not read are recorded by
// name only, whatever their value.
func TestCPParseRecordsUnknownKeys(t *testing.T) {
	in := `{"name":"p","x-ext":1,"include":[{"path":"x"}],` +
		`"services":{"a":{"image":"i","zzz":{"deep":[1,2]},"extends":{"service":"b"}}},` +
		`"volumes":{"v":{"driver":"local","driver_opts":{"o":"1"}}}}`
	got, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if want := cpKeys("include", "name", "services", "volumes", "x-ext"); !reflect.DeepEqual(got.TopKeys, want) {
		t.Errorf("TopKeys %v, want %v", got.TopKeys, want)
	}
	if want := cpKeys("extends", "image", "zzz"); !reflect.DeepEqual(got.Services["a"].Keys, want) {
		t.Errorf("service Keys %v, want %v", got.Services["a"].Keys, want)
	}
	if want := cpKeys("driver", "driver_opts"); !reflect.DeepEqual(got.Volumes["v"].Keys, want) {
		t.Errorf("volume Keys %v, want %v", got.Volumes["v"].Keys, want)
	}
}

// TestCPParseNamedVolumes: only mounts of type volume with a source count,
// once each.
func TestCPParseNamedVolumes(t *testing.T) {
	in := `{"services":{"a":{"volumes":[` +
		`{"type":"volume","source":"zed","target":"/z"},{"type":"volume","source":"alpha","target":"/a"},` +
		`{"type":"volume","source":"zed","target":"/z2"},{"type":"volume","target":"/anon"},` +
		`{"type":"bind","source":"/srv","target":"/s"},{"type":"tmpfs","target":"/t"},{"target":"/none"}]}}}`
	got, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if want := cpKeys("alpha", "zed"); !reflect.DeepEqual(got.Services["a"].Volumes, want) {
		t.Errorf("Volumes %v, want %v", got.Services["a"].Volumes, want)
	}
}

// TestCPParseMemberPresence: build and provider count as present whatever
// their value, so a service cannot hide one behind an odd value.
func TestCPParseMemberPresence(t *testing.T) {
	got, err := Parse([]byte(`{"services":{"a":{"build":".","provider":{"type":"x"}},"b":{"build":null},"c":{"provider":null},"d":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]bool{"a": {true, true}, "b": {true, false}, "c": {false, true}, "d": {false, false}}
	for name, w := range want {
		if s := got.Services[name]; s.Build != w[0] || s.Provider != w[1] {
			t.Errorf("service %s: Build %v Provider %v, want %v %v", name, s.Build, s.Provider, w[0], w[1])
		}
	}
}

func TestCPParseRejects(t *testing.T) {
	tests := []struct{ name, in string }{
		{"empty input", ``},
		{"an array", `[]`},
		{"a string", `"x"`},
		{"two objects", `{"name":"a"} {"name":"b"}`},
		{"trailing garbage", `{"name":"a"}x`},
		{"truncated", `{"name":"a"`},
		{"duplicate top-level key", `{"name":"a","name":"b"}`},
		{"duplicate service", `{"services":{"a":{"image":"x"},"a":{"image":"y"}}}`},
		{"duplicate service key", `{"services":{"a":{"image":"x","image":"y"}}}`},
		{"name is a number", `{"name":3}`},
		{"name is null", `{"name":null}`},
		{"services is an array", `{"services":[]}`},
		{"a service is an array", `{"services":{"a":[]}}`},
		{"image is a number", `{"services":{"a":{"image":1}}}`},
		{"image is null", `{"services":{"a":{"image":null}}}`},
		{"container_name is a bool", `{"services":{"a":{"container_name":true}}}`},
		{"service networks is an array", `{"services":{"a":{"networks":["x"]}}}`},
		{"service labels value is a number", `{"services":{"a":{"labels":{"k":1}}}}`},
		{"service labels is an array", `{"services":{"a":{"labels":["k=v"]}}}`},
		{"service volumes is an object", `{"services":{"a":{"volumes":{}}}}`},
		{"a service volume is a string", `{"services":{"a":{"volumes":["/a:/b"]}}}`},
		{"a service volume type is a number", `{"services":{"a":{"volumes":[{"type":1}]}}}`},
		{"a service volume source is an object", `{"services":{"a":{"volumes":[{"type":"volume","source":{}}]}}}`},
		{"networks is an array", `{"networks":[]}`},
		{"a secret is a number", `{"secrets":{"s":1}}`},
		{"external is a string", `{"volumes":{"v":{"external":"yes"}}}`},
		{"external is an object", `{"volumes":{"v":{"external":{"name":"x"}}}}`},
		{"object name is a number", `{"volumes":{"v":{"name":1}}}`},
		{"object labels is an array", `{"networks":{"n":{"labels":[]}}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := Parse([]byte(tc.in)); err == nil || !strings.HasPrefix(err.Error(), "compose:") {
				t.Errorf("Parse(%s) = %+v, %v; want an error starting with \"compose:\"", tc.in, got, err)
			}
		})
	}
}

func TestCPParseSizeLimit(t *testing.T) {
	doc := []byte(`{"name":"x"}`)
	at := make([]byte, MaxConfigBytes)
	copy(at, doc)
	for i := len(doc); i < len(at); i++ {
		at[i] = ' '
	}
	if c, err := Parse(at); err != nil || c.Name != "x" {
		t.Errorf("a configuration of exactly MaxConfigBytes: %+v, %v", c, err)
	}
	if _, err := Parse(append(at, ' ')); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("a configuration of MaxConfigBytes+1: error %v, want one saying it exceeds the limit", err)
	}
}

func TestCPImageName(t *testing.T) {
	tests := []struct {
		project, service string
		s                Service
		want             string
	}{
		{"web", "app", Service{Image: "nginx:1.27"}, "nginx:1.27"},
		{"web", "app", Service{Image: "example/tagged:1", Build: true}, "example/tagged:1"},
		{"web", "app", Service{Build: true}, "web-app"},
		{"my-proj", "db", Service{}, "my-proj-db"},
	}
	for _, tc := range tests {
		if got := ImageName(tc.project, tc.service, tc.s); got != tc.want {
			t.Errorf("ImageName(%q, %q, %+v) = %q, want %q", tc.project, tc.service, tc.s, got, tc.want)
		}
	}
}
