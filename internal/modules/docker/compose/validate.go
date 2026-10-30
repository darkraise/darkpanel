package compose

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// The lists are the keys the compose-spec schema documents for the top level
// and for containers, networks, volumes, configs and secrets. Their source is
// the schema embedded in docker-compose v5.1.4, that is
// https://github.com/compose-spec/compose-go/blob/v2.11.0/schema/compose-spec.json,
// copied to testdata/schema/compose-spec.json. The top-level set is only the
// six members a resolved configuration has: a resolved configuration has
// neither version nor include. models and provider are left out of the
// service keys, and models of the top-level keys, because they hand work to a
// program on the host (Compose serves models' inference through a host plugin;
// it is outside the five object kinds spec 3 allows either way). Keys starting
// with x- are Compose extension fields and are always accepted.
var (
	// TopLevelKeys are the top-level keys Validate accepts.
	TopLevelKeys = []string{"configs", "name", "networks", "secrets", "services", "volumes"}
	// ServiceKeys are the keys of a service Validate accepts.
	ServiceKeys = []string{"annotations", "attach", "blkio_config", "build", "cap_add", "cap_drop", "cgroup", "cgroup_parent", "command", "configs", "container_name", "cpu_count", "cpu_percent", "cpu_period", "cpu_quota", "cpu_rt_period", "cpu_rt_runtime", "cpu_shares", "cpus", "cpuset", "credential_spec", "depends_on", "deploy", "develop", "device_cgroup_rules", "devices", "dns", "dns_opt", "dns_search", "domainname", "entrypoint", "env_file", "environment", "expose", "extends", "external_links", "extra_hosts", "gpus", "group_add", "healthcheck", "hostname", "image", "init", "ipc", "isolation", "label_file", "labels", "links", "logging", "mac_address", "mem_limit", "mem_reservation", "mem_swappiness", "memswap_limit", "network_mode", "networks", "oom_kill_disable", "oom_score_adj", "pid", "pids_limit", "platform", "ports", "post_start", "pre_stop", "privileged", "profiles", "pull_policy", "pull_refresh_after", "read_only", "restart", "runtime", "scale", "secrets", "security_opt", "shm_size", "stdin_open", "stop_grace_period", "stop_signal", "storage_opt", "sysctls", "tmpfs", "tty", "ulimits", "use_api_socket", "user", "userns_mode", "uts", "volumes", "volumes_from", "working_dir"}
	// NetworkKeys are the keys of a top-level network Validate accepts.
	NetworkKeys = []string{"attachable", "driver", "driver_opts", "enable_ipv4", "enable_ipv6", "external", "internal", "ipam", "labels", "name"}
	// VolumeKeys are the keys of a top-level volume Validate accepts.
	VolumeKeys = []string{"driver", "driver_opts", "external", "labels", "name"}
	// ConfigKeys are the keys of a top-level config Validate accepts.
	ConfigKeys = []string{"content", "environment", "external", "file", "labels", "name", "template_driver"}
	// SecretKeys are the keys of a top-level secret Validate accepts.
	SecretKeys = []string{"driver", "driver_opts", "environment", "external", "file", "labels", "name", "template_driver"}
)

const ownerLabel = "darkpanel.module"

// Facts are what Validate knows of the Engine. A nil ImagePresent finds no
// image and a nil Owner finds no owner, so a zero Facts is valid.
type Facts struct {
	ImagePresent func(ref string) bool          // an image is present locally
	Owner        func(kind, name string) string // the darkpanel.module label of an Engine object; kind "container" | "network" | "volume"; "" when unlabelled or absent
}

func (f Facts) imagePresent(ref string) bool {
	return f.ImagePresent != nil && f.ImagePresent(ref)
}

func (f Facts) owner(kind, name string) string {
	if f.Owner == nil {
		return ""
	}
	return f.Owner(kind, name)
}

// Validate refuses with *errcode.Error: docker.owned for a project named
// darkpanel* or a configuration naming an owned container, network or volume;
// docker.compose_unavailable for a provider service, a key outside the
// allowlists, or a service with a build member whose ImageName is not present.
// The detail names the service, key or object. It returns the first refusal
// in a fixed order, and nil when there is none.
func Validate(project string, c Config, f Facts) error {
	if strings.HasPrefix(strings.ToLower(project), "darkpanel") {
		return refuse(errcode.DockerOwned, "project %s is reserved for Darkpanel modules", project)
	}

	services := slices.Sorted(maps.Keys(c.Services))
	for _, name := range services {
		s := c.Services[name]
		if _, ok := s.Labels[ownerLabel]; ok {
			return refuse(errcode.DockerOwned, "service %s sets the %s label", name, ownerLabel)
		}
		if s.ContainerName != "" {
			if id := f.owner("container", s.ContainerName); id != "" {
				return refuse(errcode.DockerOwned, "container %s belongs to module %s", s.ContainerName, id)
			}
		}
		for _, ref := range s.ContainerRefs {
			if id := f.owner("container", ref); id != "" {
				return refuse(errcode.DockerOwned, "container %s belongs to module %s", ref, id)
			}
		}
	}
	for _, g := range []struct {
		kind    string
		objects map[string]Object
	}{{"network", c.Networks}, {"volume", c.Volumes}} {
		for _, key := range slices.Sorted(maps.Keys(g.objects)) {
			o := g.objects[key]
			if _, ok := o.Labels[ownerLabel]; ok {
				return refuse(errcode.DockerOwned, "%s %s sets the %s label", g.kind, key, ownerLabel)
			}
			name := o.Name
			if name == "" {
				name = project + "_" + key
			}
			if id := f.owner(g.kind, name); id != "" {
				return refuse(errcode.DockerOwned, "%s %s belongs to module %s", g.kind, name, id)
			}
		}
	}

	for _, name := range services {
		if c.Services[name].Provider {
			return refuse(errcode.DockerComposeUnavailable, "service %s uses a provider, which would run a program on the host", name)
		}
	}

	if k, ok := unsupportedKey(c.TopKeys, TopLevelKeys); ok {
		return refuse(errcode.DockerComposeUnavailable, "top-level key %q is not supported", k)
	}
	for _, name := range services {
		if k, ok := unsupportedKey(c.Services[name].Keys, ServiceKeys); ok {
			return refuse(errcode.DockerComposeUnavailable, "service %s: key %q is not supported", name, k)
		}
	}
	for _, g := range []struct {
		kind    string
		objects map[string]Object
		allowed []string
	}{
		{"network", c.Networks, NetworkKeys},
		{"volume", c.Volumes, VolumeKeys},
		{"config", c.Configs, ConfigKeys},
		{"secret", c.Secrets, SecretKeys},
	} {
		for _, key := range slices.Sorted(maps.Keys(g.objects)) {
			if k, ok := unsupportedKey(g.objects[key].Keys, g.allowed); ok {
				return refuse(errcode.DockerComposeUnavailable, "%s %s: key %q is not supported", g.kind, key, k)
			}
		}
	}

	for _, name := range services {
		s := c.Services[name]
		if !s.Build {
			continue
		}
		if ref := ImageName(project, name, s); !f.imagePresent(ref) {
			return refuse(errcode.DockerComposeUnavailable, "service %s builds image %s, which is not present locally; Compose is run with --no-build", name, ref)
		}
	}
	return nil
}

// unsupportedKey returns the first of keys that is neither an extension field
// nor in allowed.
func unsupportedKey(keys, allowed []string) (string, bool) {
	for _, k := range keys {
		if !strings.HasPrefix(k, "x-") && !slices.Contains(allowed, k) {
			return k, true
		}
	}
	return "", false
}

func refuse(code errcode.Code, format string, args ...any) error {
	return errcode.New(code, fmt.Sprintf(format, args...))
}
