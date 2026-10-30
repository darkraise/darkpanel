package compose

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// cvConfig is a valid resolved configuration of the project "shop"; each
// mutation changes it for one case.
func cvConfig(mutate ...func(*Config)) Config {
	c := Config{
		Name:    "shop",
		TopKeys: []string{"name", "networks", "services", "volumes"},
		Services: map[string]Service{
			"web": {Keys: []string{"image", "networks", "volumes"}, Image: "nginx:1.27", Networks: []string{"default"}, Volumes: []string{"data"}},
		},
		Networks: map[string]Object{"default": {Keys: []string{"name"}, Name: "shop_default"}},
		Volumes:  map[string]Object{"data": {Keys: []string{"name"}, Name: "shop_data"}},
	}
	for _, m := range mutate {
		m(&c)
	}
	return c
}

func cvService(c *Config, name string, edit func(*Service)) {
	s := c.Services[name]
	edit(&s)
	c.Services[name] = s
}

// cvFacts answers Owner from owners, keyed "<kind> <name>", and ImagePresent
// for the listed references.
func cvFacts(images []string, owners map[string]string) Facts {
	return Facts{
		ImagePresent: func(ref string) bool { return slices.Contains(images, ref) },
		Owner:        func(kind, name string) string { return owners[kind+" "+name] },
	}
}

func cvRefusal(t *testing.T, err error, code errcode.Code, detail string) {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) {
		t.Fatalf("Validate returned %v, want an *errcode.Error with code %s", err, code)
	}
	if e.Code != code || e.Detail != detail {
		t.Fatalf("Validate refused with %s %q, want %s %q", e.Code, e.Detail, code, detail)
	}
}

func TestCVValidateRefuses(t *testing.T) {
	const (
		owned       = errcode.DockerOwned
		unavailable = errcode.DockerComposeUnavailable
	)
	rows := []struct {
		name    string
		project string
		mutate  func(*Config)
		facts   Facts
		code    errcode.Code
		detail  string
	}{
		{"project darkpanel", "darkpanel", nil, cvFacts(nil, nil), owned, "project darkpanel is reserved for Darkpanel modules"},
		{"project with the prefix", "darkpanel-npm", nil, cvFacts(nil, nil), owned, "project darkpanel-npm is reserved for Darkpanel modules"},
		{"project prefix in another case", "DarkPanelEdge", nil, cvFacts(nil, nil), owned, "project DarkPanelEdge is reserved for Darkpanel modules"},
		{"service sets the owner label", "shop", func(c *Config) {
			cvService(c, "web", func(s *Service) { s.Labels = map[string]string{"darkpanel.module": "npm"} })
		}, cvFacts(nil, nil), owned, "service web sets the darkpanel.module label"},
		{"network sets the owner label", "shop", func(c *Config) {
			c.Networks["default"] = Object{Keys: []string{"labels", "name"}, Name: "shop_default", Labels: map[string]string{"darkpanel.module": "npm"}}
		}, cvFacts(nil, nil), owned, "network default sets the darkpanel.module label"},
		{"volume sets the owner label", "shop", func(c *Config) {
			c.Volumes["data"] = Object{Keys: []string{"labels", "name"}, Name: "shop_data", Labels: map[string]string{"darkpanel.module": "npm"}}
		}, cvFacts(nil, nil), owned, "volume data sets the darkpanel.module label"},
		{"container name of an owned container", "shop", func(c *Config) {
			cvService(c, "web", func(s *Service) { s.ContainerName = "npm-app" })
		}, cvFacts(nil, map[string]string{"container npm-app": "npm"}), owned, "container npm-app belongs to module npm"},
		{"network of an owned network", "shop", nil,
			cvFacts(nil, map[string]string{"network shop_default": "panel"}), owned, "network shop_default belongs to module panel"},
		{"volume of an owned volume", "shop", nil,
			cvFacts(nil, map[string]string{"volume shop_data": "npm"}), owned, "volume shop_data belongs to module npm"},
		{"network without a resolved name uses the project prefix", "shop", func(c *Config) {
			c.Networks["default"] = Object{Keys: []string{}}
		}, cvFacts(nil, map[string]string{"network shop_default": "panel"}), owned, "network shop_default belongs to module panel"},
		{"provider service", "shop", func(c *Config) {
			c.Services["llm"] = Service{Keys: []string{"provider"}, Provider: true}
		}, cvFacts(nil, nil), unavailable, "service llm uses a provider, which would run a program on the host"},
		{"top-level key outside the allowlist", "shop", func(c *Config) {
			c.TopKeys = []string{"models", "name", "services"}
		}, cvFacts(nil, nil), unavailable, `top-level key "models" is not supported`},
		{"service key outside the allowlist", "shop", func(c *Config) {
			cvService(c, "web", func(s *Service) { s.Keys = []string{"image", "models"} })
		}, cvFacts(nil, nil), unavailable, `service web: key "models" is not supported`},
		{"provider key without the provider flag", "shop", func(c *Config) {
			cvService(c, "web", func(s *Service) { s.Keys = []string{"image", "provider"} })
		}, cvFacts(nil, nil), unavailable, `service web: key "provider" is not supported`},
		{"network key outside the allowlist", "shop", func(c *Config) {
			c.Networks["default"] = Object{Keys: []string{"name", "wireguard"}, Name: "shop_default"}
		}, cvFacts(nil, nil), unavailable, `network default: key "wireguard" is not supported`},
		{"volume key outside the allowlist", "shop", func(c *Config) {
			c.Volumes["data"] = Object{Keys: []string{"name", "snapshot"}, Name: "shop_data"}
		}, cvFacts(nil, nil), unavailable, `volume data: key "snapshot" is not supported`},
		{"config key outside the allowlist", "shop", func(c *Config) {
			c.TopKeys = []string{"configs", "name", "services"}
			c.Configs = map[string]Object{"app": {Keys: []string{"content", "exec"}}}
		}, cvFacts(nil, nil), unavailable, `config app: key "exec" is not supported`},
		{"secret key outside the allowlist", "shop", func(c *Config) {
			c.TopKeys = []string{"name", "secrets", "services"}
			c.Secrets = map[string]Object{"pw": {Keys: []string{"file", "command"}}}
		}, cvFacts(nil, nil), unavailable, `secret pw: key "command" is not supported`},
		{"build of an image that is not present", "shop", func(c *Config) {
			cvService(c, "web", func(s *Service) { s.Keys = []string{"build"}; s.Image = ""; s.Build = true })
		}, cvFacts([]string{"nginx:1.27"}, nil), unavailable, "service web builds image shop-web, which is not present locally; Compose is run with --no-build"},
		{"build with a named image that is not present", "shop", func(c *Config) {
			cvService(c, "web", func(s *Service) { s.Keys = []string{"build", "image"}; s.Image = "app:1"; s.Build = true })
		}, cvFacts([]string{"shop-web"}, nil), unavailable, "service web builds image app:1, which is not present locally; Compose is run with --no-build"},
		{"build with no facts about images", "shop", func(c *Config) {
			cvService(c, "web", func(s *Service) { s.Keys = []string{"build"}; s.Image = ""; s.Build = true })
		}, Facts{}, unavailable, "service web builds image shop-web, which is not present locally; Compose is run with --no-build"},
		{"the first provider by service name", "shop", func(c *Config) {
			c.Services["b"] = Service{Keys: []string{"provider"}, Provider: true}
			c.Services["a"] = Service{Keys: []string{"provider"}, Provider: true}
		}, cvFacts(nil, nil), unavailable, "service a uses a provider, which would run a program on the host"},
		{"ownership is checked before the other refusals", "shop", func(c *Config) {
			c.Services["llm"] = Service{Keys: []string{"provider"}, Provider: true}
			cvService(c, "web", func(s *Service) { s.ContainerName = "npm-app" })
		}, cvFacts(nil, map[string]string{"container npm-app": "npm"}), owned, "container npm-app belongs to module npm"},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			var mutate []func(*Config)
			if r.mutate != nil {
				mutate = append(mutate, r.mutate)
			}
			cvRefusal(t, Validate(r.project, cvConfig(mutate...), r.facts), r.code, r.detail)
		})
	}
}

func TestCVValidateAccepts(t *testing.T) {
	rows := []struct {
		name   string
		mutate func(*Config)
		facts  Facts
	}{
		{"a plain project", nil, cvFacts(nil, nil)},
		{"extension keys at every level", func(c *Config) {
			c.TopKeys = []string{"name", "networks", "services", "volumes", "x-meta"}
			cvService(c, "web", func(s *Service) { s.Keys = append(s.Keys, "x-note") })
			c.Networks["default"] = Object{Keys: []string{"name", "x-net"}, Name: "shop_default"}
			c.Volumes["data"] = Object{Keys: []string{"name", "x-vol"}, Name: "shop_data"}
		}, cvFacts(nil, nil)},
		{"every allowed service key", func(c *Config) {
			cvService(c, "web", func(s *Service) { s.Keys = slices.Clone(ServiceKeys) })
		}, cvFacts(nil, nil)},
		{"a build whose image is present", func(c *Config) {
			cvService(c, "web", func(s *Service) { s.Keys = []string{"build"}; s.Image = ""; s.Build = true })
		}, cvFacts([]string{"shop-web"}, nil)},
		{"a build with a named image that is present", func(c *Config) {
			cvService(c, "web", func(s *Service) { s.Keys = []string{"build", "image"}; s.Image = "app:1"; s.Build = true })
		}, cvFacts([]string{"app:1"}, nil)},
		{"objects another project owns nothing of", nil, cvFacts(nil, map[string]string{"network other_default": "panel"})},
		{"no facts at all", nil, Facts{}},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			var mutate []func(*Config)
			if r.mutate != nil {
				mutate = append(mutate, r.mutate)
			}
			if err := Validate("shop", cvConfig(mutate...), r.facts); err != nil {
				t.Fatalf("Validate = %v, want nil", err)
			}
		})
	}
}

func TestCVValidateAsksForTheEngineNamesOnly(t *testing.T) {
	var asked []string
	facts := Facts{
		ImagePresent: func(string) bool { return true },
		Owner: func(kind, name string) string {
			asked = append(asked, kind+" "+name)
			return ""
		},
	}
	c := cvConfig(func(c *Config) {
		c.Services["db"] = Service{Keys: []string{"container_name", "image"}, Image: "postgres:17", ContainerName: "db1"}
	})
	if err := Validate("shop", c, facts); err != nil {
		t.Fatalf("Validate = %v, want nil", err)
	}
	sort.Strings(asked)
	want := []string{"container db1", "network shop_default", "volume shop_data"}
	if !slices.Equal(asked, want) {
		t.Fatalf("Owner was asked about %q, want %q", asked, want)
	}
}

// TestCVValidateContainerRefs parses each configuration, so the reference
// reaches Validate through the member that names it.
func TestCVValidateContainerRefs(t *testing.T) {
	owners := map[string]string{"container npm-app": "npm", "container npm-data": "npm"}
	refuses := []struct{ name, in, detail string }{
		{"an owned container through network_mode",
			`{"name":"shop","services":{"web":{"image":"nginx:1.27","network_mode":"container:npm-app"}}}`,
			"container npm-app belongs to module npm"},
		{"an owned container through volumes_from, after an unowned container_name",
			`{"name":"shop","services":{"web":{"image":"nginx:1.27","container_name":"shop-web","volumes_from":["db","container:npm-data:ro"]}}}`,
			"container npm-data belongs to module npm"},
	}
	for _, r := range refuses {
		t.Run(r.name, func(t *testing.T) {
			c, err := Parse([]byte(r.in))
			if err != nil {
				t.Fatal(err)
			}
			cvRefusal(t, Validate("shop", c, cvFacts(nil, owners)), errcode.DockerOwned, r.detail)
		})
	}

	t.Run("unowned containers", func(t *testing.T) {
		var asked []string
		facts := Facts{Owner: func(kind, name string) string {
			asked = append(asked, kind+" "+name)
			return owners[kind+" "+name]
		}}
		c, err := Parse([]byte(`{"name":"shop","services":{"web":{"image":"nginx:1.27",` +
			`"pid":"container:other-pid","volumes_from":["container:other-data"],"external_links":["other-link:alias"]}}}`))
		if err != nil {
			t.Fatal(err)
		}
		if err := Validate("shop", c, facts); err != nil {
			t.Fatalf("Validate = %v, want nil", err)
		}
		want := []string{"container other-data", "container other-link", "container other-pid"}
		if !slices.Equal(asked, want) {
			t.Fatalf("Owner was asked about %q, want %q", asked, want)
		}
	})
}

// cvSchema is the part of the compose-spec JSON schema the allowlists come from.
type cvSchema struct {
	Properties map[string]json.RawMessage `json:"properties"`
	Defs       map[string]struct {
		Properties map[string]json.RawMessage `json:"properties"`
	} `json:"$defs"`
}

func cvKeys(m map[string]json.RawMessage, without ...string) []string {
	var keys []string
	for k := range m {
		if !slices.Contains(without, k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func TestCVKeyListsAreTheSchemasMinusHostPrograms(t *testing.T) {
	data, err := os.ReadFile("testdata/schema/compose-spec.json")
	if err != nil {
		t.Fatal(err)
	}
	var s cvSchema
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"models", "provider"} {
		if _, ok := s.Defs["service"].Properties[k]; !ok {
			t.Fatalf("the schema's service has no %q member; the exclusions below would mean nothing", k)
		}
	}
	top := cvKeys(s.Properties, "version", "include", "models")
	rows := []struct {
		name string
		got  []string
		want []string
	}{
		{"TopLevelKeys", TopLevelKeys, top},
		{"ServiceKeys", ServiceKeys, cvKeys(s.Defs["service"].Properties, "models", "provider")},
		{"NetworkKeys", NetworkKeys, cvKeys(s.Defs["network"].Properties)},
		{"VolumeKeys", VolumeKeys, cvKeys(s.Defs["volume"].Properties)},
		{"ConfigKeys", ConfigKeys, cvKeys(s.Defs["config"].Properties)},
		{"SecretKeys", SecretKeys, cvKeys(s.Defs["secret"].Properties)},
	}
	if !slices.Equal(top, []string{"configs", "name", "networks", "secrets", "services", "volumes"}) {
		t.Fatalf("the schema's top-level members are %q", top)
	}
	for _, r := range rows {
		if !slices.Equal(r.got, r.want) {
			t.Errorf("%s = %q, want %q", r.name, r.got, r.want)
		}
		for _, k := range r.got {
			if strings.HasPrefix(k, "x-") {
				t.Errorf("%s lists the extension key %q; extension keys are accepted by prefix", r.name, k)
			}
		}
	}
}
