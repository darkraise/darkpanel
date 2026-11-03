package cloudflare

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// connectorRun is cloudflare.connector.run: it deploys the cloudflared connector of one tunnel on this host, reading
// the tunnel token from a file only the connector can read.
func (c cf) connectorRun() core.ActionDef {
	return core.DefineMutating[ConnectorRunInput, Connector](core.ActionSpec{
		Name:         ID + ".connector.run",
		Summary:      "Run the cloudflared connector of a tunnel in a Darkpanel-owned container on this host.",
		Version:      1,
		Class:        core.ClassCompensable,
		Side:         core.SideAgent,
		LockKeys:     []string{"cloudflare"},
		LongRunning:  true,
		ApplyTimeout: 10 * time.Minute,
	}, connectorRunHandler{c})
}

// connectorRunHandler is the handler of cloudflare.connector.run.
type connectorRunHandler struct{ c cf }

// connectorCommand is the connector's command line. It names the token file, never the token.
func connectorCommand() []string {
	return []string{"tunnel", "--no-autoupdate", "run", "--token-file", tokenMount}
}

// connectorMount bind-mounts the tunnel's token file read-only where the connector reads it.
func connectorMount(tunnel string) dockerapi.Mount {
	return dockerapi.Mount{Type: "bind", Source: tokenDir + "/" + tokenFileName(tunnel), Target: tokenMount, ReadOnly: true}
}

// connectorCreate is the container of the tunnel's connector, on the default network and with the image's
// environment only.
func connectorCreate(tunnel string) dockerapi.ContainerCreate {
	return dockerapi.ContainerCreate{
		Name: connectorName(tunnel),
		ContainerConfig: dockerapi.ContainerConfig{
			Image:  Image,
			Cmd:    connectorCommand(),
			Labels: map[string]string{core.LabelModule: ID},
		},
		HostConfig: &dockerapi.HostConfig{
			RestartPolicy: dockerapi.RestartPolicy{Name: "unless-stopped"},
			Mounts:        []dockerapi.Mount{connectorMount(tunnel)},
		},
	}
}

// precheck refuses a run the host cannot take: a bad tunnel id or an empty token, a connector already recorded for the
// tunnel, or a container of the connector's name, whoever made it.
func (h connectorRunHandler) precheck(ctx context.Context, env core.Env, in ConnectorRunInput) error {
	var fields []errcode.FieldError
	if p := tunnelIDProblem(in.Tunnel); p != "" {
		fields = append(fields, errcode.FieldError{Path: "/tunnel", Message: p})
	}
	if in.Token == "" {
		fields = append(fields, errcode.FieldError{Path: "/token", Message: "token must not be empty"})
	}
	if len(fields) > 0 {
		msgs := make([]string, len(fields))
		for i, f := range fields {
			msgs[i] = f.Message
		}
		return core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: strings.Join(msgs, "; "), Fields: fields})
	}
	_, ok, err := loadConnector(ctx, env.DB.Read(), in.Tunnel)
	if err != nil {
		return err
	}
	if ok {
		return core.Definite(errcode.New(errcode.InvalidInput, fmt.Sprintf("a connector for tunnel %s is already deployed on this host", in.Tunnel)))
	}
	name := connectorName(in.Tunnel)
	_, err = h.c.a.docker.InspectContainer(ctx, name)
	switch {
	case dockerapi.IsNotFound(err):
		return nil
	case err != nil:
		return fmt.Errorf("cloudflare: inspect container %s: %w", name, engineCoded(err))
	default:
		return core.Definite(errcode.New(errcode.InvalidInput, fmt.Sprintf("a Docker container named %s already exists", name)))
	}
}

// Plan checks the host and lists what the run creates. It changes nothing and pulls no image.
func (h connectorRunHandler) Plan(ctx context.Context, env core.Env, in ConnectorRunInput) (core.PlanOutcome, error) {
	if err := h.precheck(ctx, env, in); err != nil {
		return core.PlanOutcome{}, err
	}
	sum := sha256.Sum256([]byte(Image + "\n" + in.Tunnel))
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "+", Text: fmt.Sprintf("token file %s/%s, mode 0400, owned by uid and gid %d", tokenDir, tokenFileName(in.Tunnel), imageUID)},
			{Op: "+", Text: "container " + connectorName(in.Tunnel) + " from " + Image},
			{Op: "+", Text: "restart policy unless-stopped; the token file is mounted read-only at " + tokenMount},
		},
		StateHash: hex.EncodeToString(sum[:]),
	}, nil
}

// Apply writes the token file, creates the container, records the connector and starts the container, each its own
// effect. The row is recorded before the container starts, so a running connector always has its token on record.
func (h connectorRunHandler) Apply(ctx context.Context, env core.Env, in ConnectorRunInput) (Connector, error) {
	if err := h.precheck(ctx, env, in); err != nil {
		return Connector{}, err
	}
	if err := h.ensureImage(ctx); err != nil {
		return Connector{}, err
	}
	docker := h.c.a.docker
	tunnel, name := in.Tunnel, connectorName(in.Tunnel)

	fileState, err := effTokenFileState(tunnel, len(in.Token))
	if err != nil {
		return Connector{}, err
	}
	file := connectorEffect(core.EffectCreate, resourceOf(nounTokenFile, "?"+tunnel), nil, 0)
	file.Local, file.InverseLocal, file.After = true, true, &core.StateDoc{State: fileState}
	err = h.c.journal(ctx, env, file, h.Observe, func(ctx context.Context) (string, error) {
		if err := env.Files.WriteManaged(ctx, ID, tokenDir, tokenFileName(tunnel), []byte(in.Token), 0o400, imageUID, imageUID); err != nil {
			return "", fmt.Errorf("cloudflare: write the token file of tunnel %s: %w", tunnel, err)
		}
		return resourceOf(nounTokenFile, tunnel), nil
	})
	if err != nil {
		return Connector{}, err
	}

	var id string
	err = h.c.journal(ctx, env, connectorEffect(core.EffectCreate, resourceOf(nounContainer, "?"+name), nil, engineCommitWait), h.Observe,
		func(ctx context.Context) (string, error) {
			var err error
			if id, err = docker.CreateContainer(ctx, connectorCreate(tunnel)); err != nil {
				return "", fmt.Errorf("cloudflare: create container %s: %w", name, engineCoded(err))
			}
			return resourceOf(nounContainer, id), nil
		})
	if err != nil {
		return Connector{}, err
	}

	rowState, err := effConnectorState(connectorRow{TunnelID: tunnel, ContainerID: id, ChangeID: env.ChangeID})
	if err != nil {
		return Connector{}, err
	}
	row := connectorEffect(core.EffectCreate, resourceOf(nounConnector, "?"+tunnel), nil, 0)
	row.Local, row.InverseLocal, row.After = true, true, &core.StateDoc{State: rowState}
	err = h.c.journal(ctx, env, row, h.Observe, func(ctx context.Context) (string, error) {
		sealed, err := env.Sealer.Seal(connectorSealAD(tunnel), []byte(in.Token))
		if err != nil {
			return "", core.Definite(fmt.Errorf("cloudflare: seal the token of tunnel %s: %w", tunnel, err))
		}
		err = env.DB.Write(ctx, func(tx *sql.Tx) error {
			return insertConnector(ctx, tx, connectorRow{
				TunnelID: tunnel, ContainerID: id, TokenCT: sealed, ChangeID: env.ChangeID, CreatedAt: env.Clock.Now(),
			})
		})
		if err != nil {
			return "", err
		}
		return resourceOf(nounConnector, tunnel), nil
	})
	if err != nil {
		return Connector{}, err
	}

	ctr, err := docker.InspectContainer(ctx, id)
	if err != nil {
		return Connector{}, fmt.Errorf("cloudflare: inspect container %s: %w", name, engineCoded(err))
	}
	before, err := dstate.ContainerRunning(id, ctr.Created, false)
	if err != nil {
		return Connector{}, fmt.Errorf("cloudflare: render container %s: %w", name, err)
	}
	err = h.c.journal(ctx, env, connectorEffect(core.EffectModify, resourceOf(nounContainer, id), before, engineCommitWait), h.Observe,
		func(ctx context.Context) (string, error) {
			if _, err := docker.StartContainer(ctx, id); err != nil {
				return "", fmt.Errorf("cloudflare: start container %s: %w", name, engineCoded(err))
			}
			return "", nil
		})
	if err != nil {
		return Connector{}, err
	}

	if ctr, err = docker.InspectContainer(ctx, id); err != nil {
		return Connector{}, fmt.Errorf("cloudflare: inspect container %s: %w", name, engineCoded(err))
	}
	return Connector{Tunnel: tunnel, Container: name, ContainerID: id, State: ctr.State.Status}, nil
}

// ensureImage pulls Image when the Engine does not have it. A failed pull changed nothing the module journals.
func (h connectorRunHandler) ensureImage(ctx context.Context) error {
	docker := h.c.a.docker
	_, err := docker.InspectImage(ctx, Image)
	switch {
	case err == nil:
		return nil
	case !dockerapi.IsNotFound(err):
		return fmt.Errorf("cloudflare: inspect image %s: %w", Image, engineCoded(err))
	}
	s, err := docker.PullImage(ctx, Image)
	if err != nil {
		return core.Definite(fmt.Errorf("cloudflare: pull %s: %w", Image, engineCoded(err)))
	}
	defer func() { _ = s.Close() }()
	for {
		m, err := s.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err == nil {
			err = m.Err()
		}
		if err != nil {
			return core.Definite(fmt.Errorf("cloudflare: pull %s: %w", Image, engineCoded(err)))
		}
	}
}

// Verify checks the connector from the host alone: its row and a live container configured as the run made it, which
// carries the token in neither its environment nor its command line. No message names the token.
func (h connectorRunHandler) Verify(ctx context.Context, env core.Env, in ConnectorRunInput, _ Connector) error {
	tunnel, name := in.Tunnel, connectorName(in.Tunnel)
	row, ok, err := loadConnector(ctx, env.DB.Read(), tunnel)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("cloudflare: no connector of tunnel %s is recorded on this host", tunnel)
	}
	ctr, err := h.c.a.docker.InspectContainer(ctx, name)
	switch {
	case dockerapi.IsNotFound(err):
		return fmt.Errorf("cloudflare: container %s does not exist", name)
	case err != nil:
		return fmt.Errorf("cloudflare: inspect container %s: %w", name, engineCoded(err))
	}
	if p := connectorProblem(ctr, row, tunnel, in.Token); p != "" {
		return fmt.Errorf("cloudflare: container %s %s", name, p)
	}
	return nil
}

// connectorProblem is why ctr is not the running connector of the tunnel that row records, or "" when it is. It never
// quotes the container's environment or command line, which may hold the token.
func connectorProblem(ctr dockerapi.ContainerJSON, row connectorRow, tunnel, token string) string {
	carries := func(values []string) bool {
		return token != "" && slices.ContainsFunc(values, func(v string) bool { return strings.Contains(v, token) })
	}
	switch {
	case carries(ctr.Config.Env) || carries(ctr.Config.Cmd):
		return "carries the tunnel token in its environment or command line"
	case ctr.ID != row.ContainerID:
		return "is not the container the connector of tunnel " + tunnel + " records"
	case ctr.Config.Image != Image:
		return "does not run " + Image
	case ctr.Config.Labels[core.LabelModule] != ID:
		return "does not carry the label " + core.LabelModule + "=" + ID
	case !slices.Equal(ctr.Config.Cmd, connectorCommand()):
		return "does not run the command " + strings.Join(connectorCommand(), " ")
	case ctr.HostConfig.RestartPolicy.Name != "unless-stopped":
		return "does not have the restart policy unless-stopped"
	case !slices.Equal(ctr.HostConfig.Mounts, []dockerapi.Mount{connectorMount(tunnel)}):
		return "does not mount only its token file, read-only at " + tokenMount
	case ctr.State.Status != "running" && ctr.State.Status != "restarting":
		return "is " + ctr.State.Status + ", not running"
	}
	return ""
}

// foreignConnector refuses a container of a connector's name that does not carry the label darkpanel.module=cloudflare:
// the module did not create it, so Darkpanel does not remove it.
func foreignConnector(ctr dockerapi.ContainerJSON, name string) error {
	switch owner := ctr.Config.Labels[core.LabelModule]; owner {
	case ID:
		return nil
	case "":
		return core.Definite(errcode.New(errcode.DockerOwned, fmt.Sprintf(
			"container %s is not labelled %s=%s; Darkpanel does not remove it", name, core.LabelModule, ID)))
	default:
		return core.Definite(errcode.New(errcode.DockerOwned, fmt.Sprintf(
			"container %s belongs to module %s, not %s; Darkpanel does not remove it", name, owner, ID)))
	}
}

// removeConnector removes the tunnel's connector from this host: its container, its token file and its row. Parts
// already gone are not errors; removed reports whether the container or the row existed. A container of the
// connector's name the module did not label is refused before anything is removed.
func removeConnector(ctx context.Context, env core.Env, c cf, tunnel string) (bool, error) {
	docker := c.a.docker
	name := connectorName(tunnel)
	removed := true
	ctr, err := docker.InspectContainer(ctx, name)
	switch {
	case dockerapi.IsNotFound(err):
		removed = false
	case err != nil:
		return false, core.Definite(fmt.Errorf("cloudflare: inspect container %s: %w", name, engineCoded(err)))
	default:
		if err := foreignConnector(ctr, name); err != nil {
			return false, err
		}
		// Removing by id leaves alone a container that took the name after the inspection.
		err = docker.RemoveContainer(ctx, ctr.ID, dockerapi.RemoveContainerOptions{Force: true})
		switch {
		case dockerapi.IsNotFound(err):
			removed = false
		case err != nil:
			return false, fmt.Errorf("cloudflare: remove container %s: %w", name, engineCoded(err))
		}
	}
	if err := env.Files.RemoveManaged(ctx, ID, tokenDir, tokenFileName(tunnel)); err != nil {
		return false, fmt.Errorf("cloudflare: remove the token file of tunnel %s: %w", tunnel, err)
	}
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		_, ok, err := loadConnector(ctx, tx, tunnel)
		if err != nil {
			return err
		}
		removed = removed || ok
		return deleteConnector(ctx, tx, tunnel)
	})
	if err != nil {
		return false, err
	}
	return removed, nil
}
