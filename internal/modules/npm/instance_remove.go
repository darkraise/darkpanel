package npm

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// instanceRemove is npm.instance.remove: it deletes the module's NPM instance with its data, the container, both
// volumes and the network, and the bookkeeping that only describes the instance. It cannot be undone.
func (n nm) instanceRemove() core.ActionDef {
	return core.DefineMutating[RemoveInput, Removed](core.ActionSpec{
		Name:     "npm.instance.remove",
		Summary:  "Remove the Nginx Proxy Manager instance and its data.",
		Version:  1,
		Class:    core.ClassIrreversible,
		Side:     core.SideAgent,
		LockKeys: []string{lockKey},
	}, instanceRemoveHandler{n: n})
}

// instanceRemoveHandler is the handler of npm.instance.remove.
type instanceRemoveHandler struct{ n nm }

// nounUpgradeArchive is the resource noun of an upgrade archive the removal deletes; a resource is
// "npm.archive:<file name>".
const nounUpgradeArchive = "npm.archive"

// instanceRemoveTarget is what a removal finds: the instance row, the Engine objects of the instance that exist, the
// upgrade archives on the host and the number of upgrade records.
type instanceRemoveTarget struct {
	row         instanceRow
	containerID string // "" when the container does not exist
	running     bool
	volumes     []string // the existing volumes, the data volume first
	networkID   string   // "" when the network does not exist
	archives    []string // the file names of the upgrade archives in backupDir, sorted
	upgrades    int      // the number of npm_upgrades rows
}

// instanceRemoveEngine marks a failed Engine read of the removal's checks Definite, as the Engine changed nothing,
// with an unreachable Engine coded docker.engine_unavailable.
func instanceRemoveEngine(what string, err error) error {
	return core.Definite(fmt.Errorf("npm: %s: %w", what, engineCoded(err)))
}

// instanceRemoveName is a listed container's name without its leading slash, or its id when it has none.
func instanceRemoveName(c dockerapi.ContainerSummary) string {
	if len(c.Names) == 0 {
		return c.ID
	}
	return strings.TrimPrefix(c.Names[0], "/")
}

// target reads what the removal deletes, the upgrade archives and the upgrade records included, and refuses a removal
// Darkpanel must not make: no instance, an exposure that still reserves an object of it, an object of the module's
// names it did not label, or another container that uses the network or a volume. It changes nothing, and every
// failure is Definite.
func (h instanceRemoveHandler) target(ctx context.Context, env core.Env) (instanceRemoveTarget, error) {
	row, ok, err := loadInstance(ctx, env.DB.Read())
	if err != nil {
		return instanceRemoveTarget{}, core.Definite(err)
	}
	if !ok {
		return instanceRemoveTarget{}, core.Definite(errcode.New(errcode.NotFound, "no Nginx Proxy Manager instance is deployed on this host"))
	}
	if err := h.checkReserved(ctx, env); err != nil {
		return instanceRemoveTarget{}, err
	}
	t := instanceRemoveTarget{row: row}
	docker := h.n.d.docker
	unlabelled := func(kind, name string, labels map[string]string) error {
		if labels[core.LabelModule] == ID {
			return nil
		}
		return core.Definite(errcode.New(errcode.InvalidInput,
			fmt.Sprintf("%s %s is not labelled %s=%s; Darkpanel does not remove it", kind, name, core.LabelModule, ID)))
	}

	c, err := docker.InspectContainer(ctx, containerName)
	switch {
	case dockerapi.IsNotFound(err):
	case err != nil:
		return instanceRemoveTarget{}, instanceRemoveEngine("inspect container "+containerName, err)
	default:
		if err := unlabelled("container", containerName, c.Config.Labels); err != nil {
			return instanceRemoveTarget{}, err
		}
		t.containerID, t.running = c.ID, c.State.Running
	}
	nw, err := docker.InspectNetwork(ctx, networkName)
	switch {
	case dockerapi.IsNotFound(err):
	case err != nil:
		return instanceRemoveTarget{}, instanceRemoveEngine("inspect network "+networkName, err)
	default:
		if err := unlabelled("network", networkName, nw.Labels); err != nil {
			return instanceRemoveTarget{}, err
		}
		t.networkID = nw.ID
	}
	for _, name := range []string{dataVolume, certVolume} {
		v, err := docker.InspectVolume(ctx, name)
		switch {
		case dockerapi.IsNotFound(err):
		case err != nil:
			return instanceRemoveTarget{}, instanceRemoveEngine("inspect volume "+name, err)
		default:
			if err := unlabelled("volume", name, v.Labels); err != nil {
				return instanceRemoveTarget{}, err
			}
			t.volumes = append(t.volumes, name)
		}
	}

	if err := h.checkUsers(ctx, t); err != nil {
		return instanceRemoveTarget{}, err
	}
	if t.archives, err = h.upgradeArchives(); err != nil {
		return instanceRemoveTarget{}, core.Definite(err)
	}
	if t.upgrades, err = countUpgrades(ctx, env); err != nil {
		return instanceRemoveTarget{}, core.Definite(err)
	}
	return t, nil
}

// upgradeArchives lists, sorted, the regular files in backupDir whose name the module's owned path admits. The
// directory belongs to the module alone, so each is an upgrade archive; a missing directory holds none.
func (h instanceRemoveHandler) upgradeArchives() ([]string, error) {
	entries, err := h.n.d.readDir(backupDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("npm: list %s: %w", backupDir, err)
	}
	pattern := regexp.MustCompile(ownedPaths()[0].Pattern)
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && pattern.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	return names, nil
}

// countUpgrades is the number of npm_upgrades rows.
func countUpgrades(ctx context.Context, env core.Env) (int, error) {
	var n int
	if err := env.DB.Read().QueryRowContext(ctx, "SELECT count(*) FROM npm_upgrades").Scan(&n); err != nil {
		return 0, fmt.Errorf("npm: read the upgrade records: %w", err)
	}
	return n, nil
}

// checkReserved refuses while an exposure reserves an object of the instance, naming every such exposure.
func (h instanceRemoveHandler) checkReserved(ctx context.Context, env core.Env) error {
	rows, err := env.DB.Read().QueryContext(ctx, "SELECT DISTINCT exposure FROM npm_reserved ORDER BY exposure")
	if err != nil {
		return core.Definite(fmt.Errorf("npm: read the reserved objects: %w", err))
	}
	defer func() { _ = rows.Close() }()
	var exposures []string
	for rows.Next() {
		var exposure string
		if err := rows.Scan(&exposure); err != nil {
			return core.Definite(fmt.Errorf("npm: read the reserved objects: %w", err))
		}
		exposures = append(exposures, exposure)
	}
	if err := rows.Err(); err != nil {
		return core.Definite(fmt.Errorf("npm: read the reserved objects: %w", err))
	}
	if len(exposures) == 0 {
		return nil
	}
	return core.Definite(errcode.New(errcode.NPMReserved, fmt.Sprintf(
		"the exposures %s reserve objects of this Nginx Proxy Manager instance; remove them first", strings.Join(exposures, ", "))))
}

// checkUsers refuses when a container other than the NPM container is attached to the network or mounts a volume of
// the instance: removing them would fail, or leave that container without the data it uses.
func (h instanceRemoveHandler) checkUsers(ctx context.Context, t instanceRemoveTarget) error {
	containers, err := h.n.d.docker.ListContainers(ctx, dockerapi.ListContainersOptions{All: true})
	if err != nil {
		return instanceRemoveEngine("list containers", err)
	}
	inUse := func(what, name string) error {
		return core.Definite(errcode.New(errcode.DockerInUse, fmt.Sprintf("%s is used by container %s", what, name)))
	}
	for _, c := range containers {
		if t.containerID != "" && c.ID == t.containerID {
			continue
		}
		if t.networkID != "" && c.NetworkSettings != nil {
			for key, ep := range c.NetworkSettings.Networks {
				if key == networkName || (ep != nil && ep.NetworkID == t.networkID) {
					return inUse("network "+networkName, instanceRemoveName(c))
				}
			}
		}
		for _, m := range c.Mounts {
			if m.Name == dataVolume || m.Name == certVolume {
				return inUse("volume "+m.Name, instanceRemoveName(c))
			}
		}
	}
	return nil
}

// Plan lists what the removal deletes, the upgrade archives and the upgrade records included. The hash covers the
// instance row, every object and archive found and the number of upgrade records, so a plan made before any of them
// changes, the container stopping included, is refused.
func (h instanceRemoveHandler) Plan(ctx context.Context, env core.Env, _ RemoveInput) (core.PlanOutcome, error) {
	t, err := h.target(ctx, env)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	var diff []core.DiffLine
	if t.containerID != "" {
		diff = append(diff, core.DiffLine{Op: "-", Text: "container " + containerName + " (" + t.containerID[:min(len(t.containerID), 12)] + ")"})
		if t.running {
			diff = append(diff, core.DiffLine{Op: " ", Text: "the running container is killed first"})
		}
	}
	for _, v := range t.volumes {
		diff = append(diff, core.DiffLine{Op: "-", Text: "volume " + v})
	}
	if len(t.volumes) > 0 {
		diff = append(diff, core.DiffLine{Op: " ", Text: "the proxy hosts, access lists, certificates and users in the volumes are deleted and cannot be restored"})
	}
	if t.networkID != "" {
		diff = append(diff, core.DiffLine{Op: "-", Text: "network " + networkName})
	}
	for _, name := range t.archives {
		diff = append(diff, core.DiffLine{Op: "-", Text: "upgrade archive " + name})
	}
	if len(t.archives) > 0 {
		diff = append(diff, core.DiffLine{Op: " ", Text: "the upgrade archives hold copies of the instance's data and certificates, private keys included, and are deleted with it"})
	}
	if t.upgrades > 0 {
		diff = append(diff, core.DiffLine{Op: "-", Text: "the records of the instance's past upgrades"})
	}
	diff = append(diff, core.DiffLine{Op: "-", Text: "the stored admin password and the attachment claims of this instance"})
	sum := sha256.Sum256([]byte(strings.Join([]string{
		storeTime(t.row.UpdatedAt), t.containerID, strconv.FormatBool(t.running), strings.Join(t.volumes, ","), t.networkID,
		strings.Join(t.archives, ","), strconv.Itoa(t.upgrades),
	}, "\n")))
	return core.PlanOutcome{
		Diff:         diff,
		Guards:       []core.Guard{},
		StateHash:    hex.EncodeToString(sum[:]),
		Confirmation: containerName,
	}, nil
}

// Apply removes the container, the volumes, the network and then the upgrade archives, each its own effect, and then
// the instance's bookkeeping with the upgrade records. The archives go last because until the data is gone they are
// the only way back. A failed removal keeps the instance row, so applying again continues with what is left.
func (h instanceRemoveHandler) Apply(ctx context.Context, env core.Env, _ RemoveInput) (Removed, error) {
	t, err := h.target(ctx, env)
	if err != nil {
		return Removed{}, err
	}
	docker := h.n.d.docker
	type removal struct {
		resource string
		call     func(ctx context.Context) error
	}
	var removals []removal
	if t.containerID != "" {
		removals = append(removals, removal{resourceOf(nounContainer, t.containerID), func(ctx context.Context) error {
			if err := docker.RemoveContainer(ctx, t.containerID, dockerapi.RemoveContainerOptions{Force: true}); err != nil {
				return fmt.Errorf("npm: remove container %s: %w", containerName, engineCoded(err))
			}
			return nil
		}})
	}
	for _, name := range t.volumes {
		removals = append(removals, removal{resourceOf(nounVolume, name), func(ctx context.Context) error {
			if err := docker.RemoveVolume(ctx, name, false); err != nil {
				return fmt.Errorf("npm: remove volume %s: %w", name, engineCoded(err))
			}
			return nil
		}})
	}
	if t.networkID != "" {
		removals = append(removals, removal{resourceOf(nounNetwork, t.networkID), func(ctx context.Context) error {
			if err := docker.RemoveNetwork(ctx, t.networkID); err != nil {
				return fmt.Errorf("npm: remove network %s: %w", networkName, engineCoded(err))
			}
			return nil
		}})
	}
	for _, name := range t.archives {
		removals = append(removals, removal{resourceOf(nounUpgradeArchive, name), func(ctx context.Context) error {
			if err := env.Files.RemoveManaged(ctx, ID, backupDir, name); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("npm: remove the archive %s: %w", name, err)
			}
			return nil
		}})
	}
	for _, r := range removals {
		if err := h.remove(ctx, env, r.resource, r.call); err != nil {
			return Removed{}, err
		}
	}
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		if err := deleteInstance(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM npm_attachments"); err != nil {
			return fmt.Errorf("npm: delete the network claims: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM npm_watch"); err != nil {
			return fmt.Errorf("npm: delete the watched identities: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM npm_upgrades"); err != nil {
			return fmt.Errorf("npm: delete the upgrade records: %w", err)
		}
		return nil
	})
	if err != nil {
		return Removed{}, err
	}
	id := t.containerID
	if id == "" {
		id = t.row.ContainerID
	}
	return Removed{ContainerID: id}, nil
}

// remove journals the deletion of the object resource, an Engine object or an upgrade archive, and makes it with
// call. Its before-state is read as a settlement of the intended effect would read it, so the two compare equal while
// the object is untouched. A failed before-state read is Definite: the effect is not yet intended and nothing of it
// has changed.
func (h instanceRemoveHandler) remove(ctx context.Context, env core.Env, resource string, call func(ctx context.Context) error) error {
	obs, err := h.observe(ctx, env, core.Effect{Kind: core.EffectDelete, Resource: resource, State: core.EffectIntended})
	if err != nil {
		return core.Definite(err)
	}
	e := npmEffect(core.EffectDelete, resource, obs.Current.State, commitWait)
	return h.n.run(ctx, env, e, h.observe, func(ctx context.Context) (string, error) {
		return resource, call(ctx)
	})
}

// observe reads the object of an effect of the removal: an upgrade archive from backupDir, any other from the Engine.
func (h instanceRemoveHandler) observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	noun, name, _, err := creationOf(e.Resource)
	if err != nil {
		return core.Observation{}, err
	}
	if noun != nounUpgradeArchive {
		return h.n.observeEngine(ctx, env, e)
	}
	return h.observeArchive(name)
}

// observeArchive reads the upgrade archive name: its file name while backupDir lists it as a regular file, the null
// state otherwise.
func (h instanceRemoveHandler) observeArchive(name string) (core.Observation, error) {
	entries, err := h.n.d.readDir(backupDir)
	if errors.Is(err, fs.ErrNotExist) {
		return named(nullState().State), nil
	}
	if err != nil {
		return core.Observation{}, fmt.Errorf("npm: read %s: %w", backupDir, err)
	}
	for _, e := range entries {
		if e.Name() != name || !e.Type().IsRegular() {
			continue
		}
		doc, err := json.Marshal(struct {
			File string `json:"file"`
		}{File: name})
		if err != nil {
			return core.Observation{}, fmt.Errorf("npm: render the archive %s: %w", name, err)
		}
		return named(doc), nil
	}
	return named(nullState().State), nil
}

// Verify checks from the host alone that nothing of the instance is left: no container, volume or network of the
// module's names, no upgrade archive, no instance row and no upgrade record.
func (h instanceRemoveHandler) Verify(ctx context.Context, env core.Env, _ RemoveInput, _ Removed) error {
	docker := h.n.d.docker
	var left []string
	present := func(what string, err error) error {
		switch {
		case err == nil:
			left = append(left, what)
		case !dockerapi.IsNotFound(err):
			return fmt.Errorf("npm: inspect %s: %w", what, engineCoded(err))
		}
		return nil
	}
	_, err := docker.InspectContainer(ctx, containerName)
	if err := present("container "+containerName, err); err != nil {
		return err
	}
	for _, name := range []string{dataVolume, certVolume} {
		_, err := docker.InspectVolume(ctx, name)
		if err := present("volume "+name, err); err != nil {
			return err
		}
	}
	_, err = docker.InspectNetwork(ctx, networkName)
	if err := present("network "+networkName, err); err != nil {
		return err
	}
	_, ok, err := loadInstance(ctx, env.DB.Read())
	if err != nil {
		return err
	}
	if ok {
		left = append(left, "the instance row")
	}
	archives, err := h.upgradeArchives()
	if err != nil {
		return err
	}
	for _, name := range archives {
		left = append(left, "the upgrade archive "+name)
	}
	upgrades, err := countUpgrades(ctx, env)
	if err != nil {
		return err
	}
	if upgrades > 0 {
		left = append(left, "the upgrade records")
	}
	if len(left) > 0 {
		return fmt.Errorf("npm: the removal left %s", strings.Join(left, ", "))
	}
	return nil
}

// Observe reads the Engine object or the upgrade archive of an effect of the removal.
func (h instanceRemoveHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	return h.observe(ctx, env, e)
}
