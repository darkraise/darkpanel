package npm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const (
	// instanceUpgradeAPIWait is how long the new container may take to answer before the upgrade is rolled back.
	instanceUpgradeAPIWait = 120 * time.Second
	// instanceUpgradePoll is the wait between two looks at the new container's API.
	instanceUpgradePoll = time.Second
	// instanceUpgradeKeep is the number of archive sets that pruning keeps.
	instanceUpgradeKeep = 3

	// instanceUpgradeStop is the stop grace of the container the upgrade replaces.
	instanceUpgradeStop = 30 * time.Second
	// instanceUpgradeResultMax is the longest result a failed transaction records.
	instanceUpgradeResultMax = 200
)

// instanceUpgradeArchiveMax is the most bytes an upgrade reads for the archive of one volume. The archive is held in
// memory whole, because the managed file writer takes a byte slice; NPM's data apart from /data/logs is a few MB.
// It is a variable so that a test can lower it.
var instanceUpgradeArchiveMax int64 = 256 << 20

// instanceUpgradeVolumes are the archived volumes in archive order, each with the path its archive is read from and the
// directory it is copied back into: the archive of /data holds the entries under data/.
var instanceUpgradeVolumes = []struct{ volume, from, to string }{
	{dataVolume, "/data", "/"},
	{certVolume, "/etc/letsencrypt", "/etc"},
}

// instanceUpgrade is npm.instance.upgrade: it replaces the instance's container with one of the pinned image, keeping
// both volumes, after archiving them so that a new container whose API does not answer is rolled back.
func (n nm) instanceUpgrade() core.ActionDef {
	return core.DefineMutating[UpgradeInput, Upgraded](core.ActionSpec{
		Name:         ID + ".instance.upgrade",
		Summary:      "Recreate the Nginx Proxy Manager container from the pinned image, archiving its volumes first.",
		Version:      1,
		Class:        core.ClassCompensable,
		Side:         core.SideAgent,
		LockKeys:     []string{lockKey},
		ApplyTimeout: 20 * time.Minute,
		LongRunning:  true,
	}, instanceUpgradeHandler{n: n})
}

// instanceUpgradeHandler is the handler of npm.instance.upgrade.
type instanceUpgradeHandler struct{ n nm }

// instanceUpgradeResource is the resource of the upgrade's one effect: the image the instance runs.
func instanceUpgradeResource() string { return resourceOf(nounInstance, "image") }

// instanceUpgradeImage is the state of the instance's image: {"image": ref}.
func instanceUpgradeImage(ref string) core.StateDoc {
	b, err := json.Marshal(struct {
		Image string `json:"image"`
	}{ref})
	if err != nil {
		// A struct of one string always encodes.
		panic(err)
	}
	return core.StateDoc{State: b}
}

// precheck returns the instance row and the container it names, refusing when no instance is recorded or its
// container is missing.
func (h instanceUpgradeHandler) precheck(ctx context.Context, env core.Env) (instanceRow, dockerapi.ContainerJSON, error) {
	row, ok, err := loadInstance(ctx, env.DB.Read())
	if err != nil {
		return instanceRow{}, dockerapi.ContainerJSON{}, err
	}
	if !ok {
		return instanceRow{}, dockerapi.ContainerJSON{}, core.Definite(errcode.New(errcode.NotFound, "no Nginx Proxy Manager instance is deployed on this host"))
	}
	c, err := h.n.d.docker.InspectContainer(ctx, row.ContainerID)
	switch {
	case dockerapi.IsNotFound(err):
		return instanceRow{}, dockerapi.ContainerJSON{}, core.Definite(errcode.New(errcode.NPMUnhealthy, "the Nginx Proxy Manager container is missing"))
	case err != nil:
		return instanceRow{}, dockerapi.ContainerJSON{}, core.Definite(errcode.Wrap(errcode.DockerEngineUnavailable, err,
			"the Nginx Proxy Manager container cannot be inspected"))
	}
	return row, c, nil
}

// waitText is the API wait as the plan and the errors state it.
func waitText() string { return fmt.Sprintf("%d s", int(instanceUpgradeAPIWait/time.Second)) }

// Plan checks the instance and lists the image change. It changes nothing.
func (h instanceUpgradeHandler) Plan(ctx context.Context, env core.Env, _ UpgradeInput) (core.PlanOutcome, error) {
	row, c, err := h.precheck(ctx, env)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	sum := sha256.Sum256([]byte(c.ID + "\n" + row.Image + "\n" + Image))
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "-", Text: "image " + row.Image},
			{Op: "+", Text: "image " + Image},
			{Op: " ", Text: "both volumes (" + dataVolume + ", " + certVolume + ") are archived to " + backupDir + " first"},
			{Op: " ", Text: "the original container and volumes are restored if the new container's API does not answer within " + waitText()},
		},
		StateHash: hex.EncodeToString(sum[:]),
	}, nil
}

// endpointOf keeps only the static addresses of an endpoint: Docker assigns every other field again.
func endpointOf(e *dockerapi.EndpointSettings) *dockerapi.EndpointSettings {
	out := &dockerapi.EndpointSettings{}
	if e != nil && e.IPAMConfig != nil {
		ipam := *e.IPAMConfig
		out.IPAMConfig = &ipam
	}
	return out
}

// instanceUpgradeConfig is what recreates the container c: its name, configuration and host configuration, its
// darkpanel-npm endpoint and every other network it is on.
func instanceUpgradeConfig(c dockerapi.ContainerJSON) containerConfig {
	name := strings.TrimPrefix(c.Name, "/")
	if name == "" {
		name = containerName
	}
	config := c.Config
	// Docker sets the hostname to the container's short id; a recreated container keeps its own.
	config.Hostname = ""
	host := c.HostConfig
	networks := map[string]*dockerapi.EndpointSettings{}
	for network, e := range c.NetworkSettings.Networks {
		if network != networkName {
			networks[network] = endpointOf(e)
		}
	}
	return containerConfig{
		Create: dockerapi.ContainerCreate{
			Name:            name,
			ContainerConfig: config,
			HostConfig:      &host,
			NetworkingConfig: &dockerapi.NetworkingConfig{EndpointsConfig: map[string]*dockerapi.EndpointSettings{
				networkName: endpointOf(c.NetworkSettings.Networks[networkName]),
			}},
		},
		Name:     name,
		Networks: networks,
	}
}

// Apply upgrades the instance. Each phase is recorded before the host change it covers, so the transaction row always
// names the last phase that may be half done.
func (h instanceUpgradeHandler) Apply(ctx context.Context, env core.Env, _ UpgradeInput) (Upgraded, error) {
	row, c, err := h.precheck(ctx, env)
	if err != nil {
		return Upgraded{}, err
	}
	if err := h.pull(ctx, Image); err != nil {
		return Upgraded{}, core.Definite(err)
	}
	config := instanceUpgradeConfig(c)

	after := instanceUpgradeImage(Image)
	e := core.Effect{Kind: core.EffectModify, Resource: instanceUpgradeResource(), Local: true, Before: instanceUpgradeImage(row.Image), After: &after}
	seq, err := env.Effects.Intend(ctx, e, nil)
	if err != nil {
		return Upgraded{}, fmt.Errorf("npm: journal %s: %w", e.Resource, err)
	}
	err = insertUpgrade(ctx, env.DB, upgradeRow{ChangeID: env.ChangeID, FromImage: row.Image, ToImage: Image, Config: config, UpdatedAt: env.Clock.Now()})
	if err != nil {
		if skipErr := env.Effects.Skipped(ctx, seq); skipErr != nil {
			return Upgraded{}, errors.Join(err, fmt.Errorf("npm: journal %s: %w", e.Resource, skipErr))
		}
		return Upgraded{}, core.Definite(err)
	}

	step, err := h.steps(ctx, env, row, config, seq)
	if err != nil {
		return Upgraded{}, h.fail(ctx, env, step, err)
	}
	if err := h.prune(ctx, env); err != nil && env.Log != nil {
		env.Log.Warn("npm: prune the upgrade archives", "error", err)
	}
	return Upgraded{FromImage: row.Image, ToImage: Image}, nil
}

// fail rolls back the transaction a failed step left and returns the step's failure, saying whether the original was
// restored.
func (h instanceUpgradeHandler) fail(ctx context.Context, env core.Env, step string, cause error) error {
	ctx = context.WithoutCancel(ctx)
	u, ok, rbErr := loadUpgrade(ctx, env.DB.Read(), env.ChangeID)
	if rbErr == nil && !ok {
		rbErr = fmt.Errorf("npm: upgrade %s is not recorded", env.ChangeID)
	}
	if rbErr == nil {
		rbErr = h.rollback(ctx, env, u)
	}
	if step == "wait" {
		if rbErr != nil {
			return errcode.Wrap(errcode.NPMUnhealthy, errors.Join(cause, fmt.Errorf("npm: roll back the upgrade: %w", rbErr)),
				"the new Nginx Proxy Manager container did not answer within "+waitText()+"; restoring the original failed and the instance needs repair")
		}
		return errcode.Wrap(errcode.NPMUnhealthy, cause,
			"the new Nginx Proxy Manager container did not answer within "+waitText()+"; the original container and volumes were restored")
	}
	if rbErr != nil {
		return fmt.Errorf("npm: upgrade step %s failed: %w; the rollback failed and the instance needs repair: %w", step, cause, rbErr)
	}
	return fmt.Errorf("npm: upgrade step %s failed: %w; the original instance was restored", step, cause)
}

// steps runs the upgrade from the stop of the original container to the bookkeeping of the new one. It returns the
// step that failed with its error.
func (h instanceUpgradeHandler) steps(ctx context.Context, env core.Env, row instanceRow, config containerConfig, seq int) (string, error) {
	docker := h.n.d.docker
	var archives []archiveRef
	phase := func(p string) error {
		return setUpgradePhase(ctx, env.DB, env.ChangeID, p, "", archives, env.Clock.Now())
	}

	if err := phase("stopped"); err != nil {
		return "stop", err
	}
	if _, err := docker.StopContainer(ctx, row.ContainerID, instanceUpgradeStop); err != nil {
		return "stop", fmt.Errorf("npm: stop container %s: %w", containerName, engineCoded(err))
	}

	for _, v := range instanceUpgradeVolumes {
		archives = append(archives, archiveRef{Volume: v.volume, File: archiveName(env.ChangeID, v.volume)})
	}
	if err := phase("archiving"); err != nil {
		return "archive", err
	}
	for i, v := range instanceUpgradeVolumes {
		sum, err := h.archive(ctx, env, row.ContainerID, v.from, archives[i].File)
		if err != nil {
			return "archive", err
		}
		archives[i].SHA256 = sum
	}
	if err := phase("archived"); err != nil {
		return "archive", err
	}

	if err := phase("recreating"); err != nil {
		return "recreate", err
	}
	if err := docker.RemoveContainer(ctx, row.ContainerID, dockerapi.RemoveContainerOptions{}); err != nil {
		return "recreate", fmt.Errorf("npm: remove container %s: %w", containerName, engineCoded(err))
	}
	id, err := docker.CreateContainer(ctx, instanceCreate(Image))
	if err != nil {
		return "recreate", fmt.Errorf("npm: create container %s: %w", containerName, engineCoded(err))
	}
	if err := h.connect(ctx, id, config.Networks); err != nil {
		return "recreate", err
	}
	if _, err := docker.StartContainer(ctx, id); err != nil {
		return "recreate", fmt.Errorf("npm: start container %s: %w", containerName, engineCoded(err))
	}

	if err := phase("waiting"); err != nil {
		return "wait", err
	}
	if err := h.await(ctx, env); err != nil {
		return "wait", err
	}

	row.ContainerID, row.Image, row.UpdatedAt = id, Image, env.Clock.Now()
	if err := env.DB.Write(ctx, func(tx *sql.Tx) error { return saveInstance(ctx, tx, row) }); err != nil {
		return "finish", err
	}
	if err := phase("done"); err != nil {
		return "finish", err
	}
	if err := env.Effects.Made(ctx, seq, instanceUpgradeResource(), instanceUpgradeImage(Image)); err != nil {
		return "finish", fmt.Errorf("npm: journal %s: %w", instanceUpgradeResource(), err)
	}
	return "", nil
}

// archive copies the directory path of the container into the managed file name and proves the file reads back as
// written. It returns the archive's hex SHA-256.
func (h instanceUpgradeHandler) archive(ctx context.Context, env core.Env, containerID, path, name string) (string, error) {
	rc, err := h.n.d.docker.CopyFromContainer(ctx, containerID, path)
	if err != nil {
		return "", fmt.Errorf("npm: copy %s from container %s: %w", path, containerName, engineCoded(err))
	}
	data, err := io.ReadAll(io.LimitReader(rc, instanceUpgradeArchiveMax+1))
	_ = rc.Close()
	if err != nil {
		return "", fmt.Errorf("npm: copy %s from container %s: %w", path, containerName, err)
	}
	if int64(len(data)) > instanceUpgradeArchiveMax {
		return "", fmt.Errorf("npm: the archive of %s in container %s is larger than %d MiB, the most an upgrade archives; remove old logs from /data/logs and upgrade again",
			path, containerName, instanceUpgradeArchiveMax>>20)
	}
	if err := env.Files.WriteManaged(ctx, ID, backupDir, name, data, 0o600, 0, 0); err != nil {
		return "", fmt.Errorf("npm: write the archive %s: %w", name, err)
	}
	// Hashing here makes this the last use of data, so the collector may free it while the file is read back and only
	// one full copy of the archive is held at a time.
	want := sha256.Sum256(data)
	back, err := h.n.d.readFile(backupDir + "/" + name)
	if err != nil {
		return "", fmt.Errorf("npm: read the archive %s back: %w", name, err)
	}
	if got := sha256.Sum256(back); want != got {
		return "", fmt.Errorf("npm: the archive %s does not read back as written", name)
	}
	return hex.EncodeToString(want[:]), nil
}

// connect attaches the container id to every network of networks, in name order.
func (h instanceUpgradeHandler) connect(ctx context.Context, id string, networks map[string]*dockerapi.EndpointSettings) error {
	for _, network := range slices.Sorted(maps.Keys(networks)) {
		if err := h.n.d.docker.ConnectNetwork(ctx, network, id, networks[network]); err != nil {
			return fmt.Errorf("npm: connect container %s to network %s: %w", containerName, network, engineCoded(err))
		}
	}
	return nil
}

// pull makes the image ref available: it pulls it unless the Engine already has it.
func (h instanceUpgradeHandler) pull(ctx context.Context, ref string) error {
	docker := h.n.d.docker
	if _, err := docker.InspectImage(ctx, ref); err == nil {
		return nil
	}
	s, err := docker.PullImage(ctx, ref)
	if err != nil {
		return fmt.Errorf("npm: pull %s: %w", ref, engineCoded(err))
	}
	err = drainPull(s)
	if closeErr := s.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err == nil {
		_, err = docker.InspectImage(ctx, ref)
	}
	if err != nil {
		return fmt.Errorf("npm: pull %s: %w", ref, engineCoded(err))
	}
	return nil
}

// drainPull reads a pull's progress to its end; a message that reports an error fails it.
func drainPull(s pullStream) error {
	for {
		m, err := s.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err == nil {
			err = m.Err()
		}
		if err != nil {
			return err
		}
	}
}

// probe reports whether the instance's API is ready: NPM has its administrator and lists its proxy hosts for the
// stored administrator.
func (h instanceUpgradeHandler) probe(ctx context.Context, env core.Env) error {
	health, err := h.n.d.anon().Health(ctx)
	if err != nil {
		return err
	}
	if !health.Setup {
		return errors.New("npm: Nginx Proxy Manager reports no administrator")
	}
	api, err := h.n.d.api(ctx, env)
	if err != nil {
		return err
	}
	if _, err := api.ProxyHosts(ctx); err != nil {
		return fmt.Errorf("npm: list the proxy hosts: %w", err)
	}
	return nil
}

// await probes the API at once and then every instanceUpgradePoll until it is ready or instanceUpgradeAPIWait has
// passed since the first probe, when it returns the last probe's error.
func (h instanceUpgradeHandler) await(ctx context.Context, env core.Env) error {
	start := env.Clock.Now()
	for {
		left := max(instanceUpgradeAPIWait-env.Clock.Now().Sub(start), instanceUpgradePoll)
		pctx, cancel := context.WithTimeout(ctx, left)
		err := h.probe(pctx, env)
		cancel()
		if err == nil {
			return nil
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if env.Clock.Now().Sub(start) >= instanceUpgradeAPIWait {
			return err
		}
		tick := make(chan struct{})
		timer := env.Clock.AfterFunc(instanceUpgradePoll, func() { close(tick) })
		select {
		case <-tick:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

// prune removes the archive sets that are neither among the instanceUpgradeKeep newest nor referred to by a
// transaction that is not finished. A set is the archives of one change, named after its change id, so a larger name
// is a newer set.
func (h instanceUpgradeHandler) prune(ctx context.Context, env core.Env) error {
	entries, err := h.n.d.readDir(backupDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("npm: list %s: %w", backupDir, err)
	}
	sets := map[string][]string{}
	for _, e := range entries {
		name := e.Name()
		i := strings.LastIndexByte(name, '-')
		if e.IsDir() || !strings.HasSuffix(name, ".tar") || i < 0 {
			continue
		}
		sets[name[:i]] = append(sets[name[:i]], name)
	}
	inUse, err := upgradeArchivesInUse(ctx, env.DB.Read())
	if err != nil {
		return err
	}
	keys := slices.Sorted(maps.Keys(sets))
	slices.Reverse(keys)
	var errs []error
	for i, key := range keys {
		if i < instanceUpgradeKeep || slices.ContainsFunc(sets[key], func(name string) bool { return inUse[name] }) {
			continue
		}
		for _, name := range sets[key] {
			if err := env.Files.RemoveManaged(ctx, ID, backupDir, name); err != nil {
				errs = append(errs, fmt.Errorf("npm: remove the archive %s: %w", name, err))
			}
		}
	}
	return errors.Join(errs...)
}

// upgradeArchivesInUse returns the archive files of every transaction that is neither done nor rolled back, as a
// set. A failed transaction still needs its archives.
func upgradeArchivesInUse(ctx context.Context, q queryer) (map[string]bool, error) {
	rows, err := q.QueryContext(ctx, "SELECT change_id, archives FROM npm_upgrades WHERE phase NOT IN ('done', 'rolled_back')")
	if err != nil {
		return nil, fmt.Errorf("npm: read the unfinished upgrades: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var (
			changeID, text string
			archives       []archiveRef
		)
		if err := rows.Scan(&changeID, &text); err != nil {
			return nil, fmt.Errorf("npm: read the unfinished upgrades: %w", err)
		}
		if err := json.Unmarshal([]byte(text), &archives); err != nil {
			return nil, fmt.Errorf("npm: decode the archives of upgrade %s: %w", changeID, err)
		}
		for _, a := range archives {
			out[a.File] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("npm: read the unfinished upgrades: %w", err)
	}
	return out, nil
}

// rollback ends the transaction u: before the original container is removed it starts it again, and afterwards it
// restores the original from u's configuration and verified archives. It works from u alone and records no phase
// while it runs, so it can run again; it ends in phase rolled_back, or failed with the error returned.
func (h instanceUpgradeHandler) rollback(ctx context.Context, env core.Env, u upgradeRow) error {
	var err error
	switch u.Phase {
	case "rolled_back":
		return nil
	case "recorded", "stopped", "archiving", "archived":
		err = h.resume(ctx, env, u)
	case "recreating", "waiting", "done", "failed":
		err = h.restore(ctx, env, u)
	default:
		err = fmt.Errorf("npm: upgrade %s is in the unknown phase %q", u.ChangeID, u.Phase)
	}
	if err == nil {
		err = setUpgradePhase(ctx, env.DB, u.ChangeID, "rolled_back", "", nil, env.Clock.Now())
	}
	if err == nil {
		err = h.skipIntended(ctx, env)
	}
	if err != nil {
		if phaseErr := setUpgradePhase(ctx, env.DB, u.ChangeID, "failed", upgradeResult(err), nil, env.Clock.Now()); phaseErr != nil {
			return errors.Join(err, phaseErr)
		}
		return err
	}
	return nil
}

// upgradeResult is the result a failed transaction records: the first line of err, at most instanceUpgradeResultMax
// characters.
func upgradeResult(err error) string {
	line, _, _ := strings.Cut(err.Error(), "\n")
	if r := []rune(line); len(r) > instanceUpgradeResultMax {
		line = string(r[:instanceUpgradeResultMax])
	}
	return line
}

// skipIntended records the change's effect on the instance's image as skipped while it is still intended.
func (h instanceUpgradeHandler) skipIntended(ctx context.Context, env core.Env) error {
	effects, err := env.Effects.List(ctx)
	if err != nil {
		return fmt.Errorf("npm: read the journal: %w", err)
	}
	for _, e := range effects {
		if e.Resource == instanceUpgradeResource() && e.State == core.EffectIntended {
			if err := env.Effects.Skipped(ctx, e.Seq); err != nil {
				return fmt.Errorf("npm: journal %s: %w", e.Resource, err)
			}
		}
	}
	return nil
}

// resume takes back an upgrade that has not removed the original container: it deletes the archives a cut-off
// archiving may have left half written and starts the original container again.
func (h instanceUpgradeHandler) resume(ctx context.Context, env core.Env, u upgradeRow) error {
	if u.Phase == "archiving" {
		for _, a := range u.Archives {
			if err := env.Files.RemoveManaged(ctx, ID, backupDir, a.File); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("npm: remove the archive %s: %w", a.File, err)
			}
		}
	}
	row, ok, err := loadInstance(ctx, env.DB.Read())
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("npm: no Nginx Proxy Manager instance is recorded")
	}
	if _, err := h.n.d.docker.StartContainer(ctx, row.ContainerID); err != nil {
		return fmt.Errorf("npm: start container %s: %w", containerName, engineCoded(err))
	}
	return nil
}

// restoreArchives reads both archives of u and checks each against its checksum. It returns their contents in the
// order of instanceUpgradeVolumes.
func (h instanceUpgradeHandler) restoreArchives(u upgradeRow) ([][]byte, error) {
	if len(u.Archives) != len(instanceUpgradeVolumes) {
		return nil, fmt.Errorf("npm: upgrade %s records %d archives, want %d", u.ChangeID, len(u.Archives), len(instanceUpgradeVolumes))
	}
	out := make([][]byte, len(instanceUpgradeVolumes))
	for i, v := range instanceUpgradeVolumes {
		j := slices.IndexFunc(u.Archives, func(a archiveRef) bool { return a.Volume == v.volume })
		if j < 0 || u.Archives[j].SHA256 == "" {
			return nil, fmt.Errorf("npm: upgrade %s has no verified archive of volume %s", u.ChangeID, v.volume)
		}
		a := u.Archives[j]
		data, err := h.n.d.readFile(backupDir + "/" + a.File)
		if err != nil {
			return nil, fmt.Errorf("npm: read the archive %s: %w", a.File, err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != a.SHA256 {
			return nil, fmt.Errorf("npm: the archive %s does not match its checksum", a.File)
		}
		out[i] = data
	}
	return out, nil
}

// restore puts the original instance back after its container was removed: it verifies the archives and makes the
// original image available before touching anything, removes the instance's container, recreates both volumes from
// the archives and recreates and starts the original container from u's configuration.
func (h instanceUpgradeHandler) restore(ctx context.Context, env core.Env, u upgradeRow) error {
	data, err := h.restoreArchives(u)
	if err != nil {
		return err
	}
	// Once the original container is gone its image is unused and may have been pruned since.
	if err := h.pull(ctx, u.Config.Create.Image); err != nil {
		return err
	}
	docker := h.n.d.docker
	err = docker.RemoveContainer(ctx, containerName, dockerapi.RemoveContainerOptions{Force: true})
	if err != nil && !dockerapi.IsNotFound(err) {
		return fmt.Errorf("npm: remove container %s: %w", containerName, engineCoded(err))
	}
	for _, v := range instanceUpgradeVolumes {
		if err := docker.RemoveVolume(ctx, v.volume, true); err != nil && !dockerapi.IsNotFound(err) {
			return fmt.Errorf("npm: remove volume %s: %w", v.volume, engineCoded(err))
		}
		if _, err := docker.CreateVolume(ctx, dockerapi.VolumeCreate{Name: v.volume, Labels: map[string]string{core.LabelModule: ID}}); err != nil {
			return fmt.Errorf("npm: create volume %s: %w", v.volume, engineCoded(err))
		}
	}
	create := u.Config.Create
	create.Name = u.Config.Name
	id, err := docker.CreateContainer(ctx, create)
	if err != nil {
		return fmt.Errorf("npm: create container %s: %w", u.Config.Name, engineCoded(err))
	}
	if err := h.connect(ctx, id, u.Config.Networks); err != nil {
		return err
	}
	for i, v := range instanceUpgradeVolumes {
		if err := docker.CopyToContainer(ctx, id, v.to, bytes.NewReader(data[i])); err != nil {
			return fmt.Errorf("npm: copy the archive of %s into container %s: %w", v.volume, u.Config.Name, engineCoded(err))
		}
	}
	if _, err := docker.StartContainer(ctx, id); err != nil {
		return fmt.Errorf("npm: start container %s: %w", u.Config.Name, engineCoded(err))
	}
	return env.DB.Write(ctx, func(tx *sql.Tx) error {
		row, ok, err := loadInstance(ctx, tx)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("npm: no Nginx Proxy Manager instance is recorded")
		}
		row.ContainerID, row.Image, row.UpdatedAt = id, u.FromImage, env.Clock.Now()
		return saveInstance(ctx, tx, row)
	})
}

// Observe reads the image of the running instance container; a missing container is the null state.
func (h instanceUpgradeHandler) Observe(ctx context.Context, _ core.Env, e core.Effect) (core.Observation, error) {
	if e.Resource != instanceUpgradeResource() {
		return core.Observation{}, fmt.Errorf("npm: %s is not the upgrade's effect", e.Resource)
	}
	c, err := h.n.d.docker.InspectContainer(ctx, containerName)
	switch {
	case dockerapi.IsNotFound(err):
		return named(nullState().State), nil
	case err != nil:
		return core.Observation{}, fmt.Errorf("npm: inspect container %s: %w", containerName, engineCoded(err))
	}
	return named(instanceUpgradeImage(c.Config.Image).State), nil
}

// Verify checks from the host alone that the instance runs the pinned image in the container its row names and that
// its API is ready. It does not read the output, which recovery does not have.
func (h instanceUpgradeHandler) Verify(ctx context.Context, env core.Env, _ UpgradeInput, _ Upgraded) error {
	row, ok, err := loadInstance(ctx, env.DB.Read())
	if err != nil {
		return err
	}
	if !ok {
		return errcode.New(errcode.NPMUnhealthy, "no Nginx Proxy Manager instance is recorded on this host")
	}
	if row.Image != Image {
		return errcode.New(errcode.NPMUnhealthy, "the instance is recorded with image "+row.Image+", not "+Image)
	}
	c, err := h.n.d.docker.InspectContainer(ctx, containerName)
	switch {
	case dockerapi.IsNotFound(err):
		return errcode.Wrap(errcode.NPMUnhealthy, err, "the Nginx Proxy Manager container does not exist")
	case err != nil:
		return fmt.Errorf("npm: inspect container %s: %w", containerName, engineCoded(err))
	case c.ID != row.ContainerID:
		return errcode.New(errcode.NPMUnhealthy, "the container named "+containerName+" is not the one the instance records")
	case !c.State.Running:
		return errcode.New(errcode.NPMUnhealthy, "the Nginx Proxy Manager container is not running")
	case c.Config.Image != Image:
		return errcode.New(errcode.NPMUnhealthy, "the Nginx Proxy Manager container runs "+c.Config.Image+", not "+Image)
	}
	if err := h.probe(ctx, env); err != nil {
		return errcode.Wrap(errcode.NPMUnhealthy, err, "the Nginx Proxy Manager API is not ready")
	}
	return nil
}
