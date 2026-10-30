package docker

import (
	"context"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

func (k dk) projectsResource() core.ResourceDef {
	return core.DefineResource(ID+".projects", k.projects)
}

// projects lists every recorded Compose project. A project stays listed after
// its containers are gone, so it can be brought up again.
func (k dk) projects(ctx context.Context, env core.Env) (ProjectList, error) {
	// Stopped containers still name their project.
	containers, err := k.d.docker.ListContainers(ctx, dockerapi.ListContainersOptions{All: true})
	if err != nil {
		return ProjectList{}, engineError("containers", "", err)
	}
	if err := discoverProjects(ctx, env.DB, env.Clock.Now(), containers); err != nil {
		return ProjectList{}, err
	}
	rows, err := listProjects(ctx, env.DB)
	if err != nil {
		return ProjectList{}, err
	}
	names := map[string][]string{}
	running := map[string]int{}
	for _, c := range containers {
		project := c.Labels[projectLabelName]
		if project == "" {
			continue
		}
		names[project] = append(names[project], projectContainerName(c.Names))
		if c.State == "running" {
			running[project]++
		}
	}
	out := make([]Project, 0, len(rows))
	for _, row := range rows {
		hash := k.configHash(row)
		members := slices.Clone(names[row.Name])
		if members == nil {
			members = []string{}
		}
		slices.Sort(members)
		p := Project{
			Name: row.Name, Directory: row.Directory, Files: row.Files, EnvFiles: row.EnvFiles, Profiles: row.Profiles,
			ConfigHash: hash, HashChanged: row.RunHash != "" && hash != "" && row.RunHash != hash,
			Containers: members, Running: running[row.Name], FirstSeen: row.FirstSeen,
		}
		if !row.LastRun.IsZero() {
			lastRun := row.LastRun
			p.LastRun = &lastRun
		}
		out = append(out, p)
	}
	return ProjectList{Projects: out}, nil
}

func projectContainerName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[0], "/")
}
