package system

import (
	"context"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// sortedDockerPackages are dockerPackages sorted, the order undo and
// uninstall pass them in.
var sortedDockerPackages = func() []string { s := slices.Clone(dockerPackages); slices.Sort(s); return s }() //nolint:unused // for the Docker install undo and uninstall

// dockerConflicts are the distribution's Docker packages, which conflict
// with Docker's own repository.
var dockerConflicts = []string{"docker.io", "docker-doc", "docker-compose", "docker-compose-v2", "podman-docker", "containerd", "runc"}

// DockerState is the docker resource.
type DockerState struct {
	State           string     `json:"state"` // "absent", "docker-ce" or "distro"
	Version         string     `json:"version,omitempty"`
	UpdateAvailable *bool      `json:"updateAvailable,omitempty" jsonschema:"nullable"`
	Running         *bool      `json:"running,omitempty" jsonschema:"nullable"`
	Conflicts       []string   `json:"conflicts,omitempty"`
	Containers      *int       `json:"containers,omitempty" jsonschema:"nullable"`
	Images          *int       `json:"images,omitempty" jsonschema:"nullable"`
	Volumes         *int       `json:"volumes,omitempty" jsonschema:"nullable"`
	Labelled        []Labelled `json:"labelled,omitempty"`
}

// Labelled is a container a Darkpanel module created.
type Labelled struct {
	Name   string `json:"name"`
	Module string `json:"module"`
}

func (m module) dockerResource() core.ResourceDef {
	return core.DefineResource(ID+".docker", m.dockerState)
}

// dockerState reads which Docker the host has and, when the daemon answers,
// its counts and the containers Darkpanel modules created.
func (m module) dockerState(ctx context.Context, env core.Env) (DockerState, error) {
	versions, err := installedVersions(ctx, env)
	if err != nil {
		return DockerState{}, err
	}
	var conflicts []string
	for _, n := range dockerConflicts {
		if _, ok := versions[n]; ok {
			conflicts = append(conflicts, n)
		}
	}
	slices.Sort(conflicts)
	version, ce := versions["docker-ce"]
	if !ce {
		if len(conflicts) == 0 {
			return DockerState{State: "absent"}, nil
		}
		return DockerState{State: "distro", Conflicts: conflicts}, nil
	}
	out := DockerState{State: "docker-ce", Version: version, Conflicts: conflicts}
	policy, err := aptPolicy(ctx, env, []string{"docker-ce"})
	if err != nil {
		return DockerState{}, err
	}
	update := policy["docker-ce"].candidate != "" && debVersionCompare(policy["docker-ce"].candidate, version) > 0
	out.UpdateAvailable = &update
	running := false
	out.Running = &running
	info, err := m.d.docker.Info(ctx)
	if err != nil {
		return out, nil
	}
	running = true
	containers, images := info.Containers, info.Images
	out.Containers, out.Images = &containers, &images
	if vols, err := m.d.docker.ListVolumes(ctx, nil); err == nil {
		n := len(vols)
		out.Volumes = &n
	}
	list, err := m.d.docker.ListContainers(ctx, dockerapi.ListContainersOptions{All: true, Filters: dockerapi.Filters{"label": {core.LabelModule}}})
	if err == nil {
		for _, c := range list {
			mod := c.Labels[core.LabelModule]
			if mod == "" || len(c.Names) == 0 {
				continue
			}
			out.Labelled = append(out.Labelled, Labelled{Name: strings.TrimPrefix(c.Names[0], "/"), Module: mod})
		}
		slices.SortFunc(out.Labelled, func(a, b Labelled) int { return strings.Compare(a.Name, b.Name) })
	}
	return out, nil
}
