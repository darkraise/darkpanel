package system

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// dockerPurgePaths are the only paths the purge deletes, in this order.
var dockerPurgePaths = []string{"/var/lib/docker", "/var/lib/containerd"}

const dockerDaemonConfig = "/etc/docker/daemon.json"

// DockerUninstallInput is the input of system.docker.uninstall.
type DockerUninstallInput struct {
	Purge bool `json:"purge,omitempty" jsonschema_description:"Also delete every container, image and volume: /var/lib/docker and /var/lib/containerd."`
}

// DockerUninstalled is the output of system.docker.uninstall.
type DockerUninstalled struct {
	Removed []string `json:"removed" jsonschema_description:"The packages removed."`
	Files   []string `json:"files" jsonschema_description:"The repository files Darkpanel wrote and removed."`
	Purged  []string `json:"purged" jsonschema_description:"The data directories deleted."`
}

func (m module) dockerUninstallAction() core.ActionDef {
	return core.DefineTask[DockerUninstallInput, DockerUninstalled](core.ActionSpec{
		Name: ID + ".docker.uninstall", Version: 1,
		Summary:      "Remove Docker Engine and the repository files Darkpanel wrote; optionally delete all containers, images and volumes.",
		Class:        core.ClassIrreversible,
		Side:         core.SideAgent,
		LockKeys:     []string{aptLockKey},
		ApplyTimeout: 30 * time.Minute,
		Rechecks:     []string{"docker"},
	}, dockerUninstallHandler{m: m})
}

type dockerUninstallHandler struct{ m module }

// dockerInventory is what the uninstall found on the host after its refusals passed.
type dockerInventory struct {
	// unchecked says why the labelled-container check could not run; "" when it ran.
	unchecked                  string
	containers, images, volume int
	owned                      []string // the repository files the ownership store records as the module's
	dataRoot                   string   // a data-root daemon.json sets, "" when none
}

// inventory refuses, in this order, while the firewall's Docker integration is on, while a firewall change is
// unsettled, when Docker Engine is not installed, while a container carries a Darkpanel module label, and for a
// purge the daemon cannot inventory or a data path the purge would refuse.
func (h dockerUninstallHandler) inventory(ctx context.Context, env core.Env, in DockerUninstallInput) (dockerInventory, error) {
	var inv dockerInventory
	on, err := h.m.d.firewallIntegration(ctx, env)
	if err != nil {
		return inv, err
	}
	if on {
		detail := "the firewall's Docker integration is on; turn it off first with firewall.docker.disable"
		enabled, err := moduleEnabled(ctx, env, "firewall")
		if err != nil {
			return inv, err
		}
		if !enabled {
			detail += ", which needs you to enable the firewall module"
		}
		return inv, errcode.New(errcode.SystemFirewallIntegrationOn, detail)
	}
	if err := pendingFirewallChanges(ctx, env); err != nil {
		return inv, err
	}
	versions, err := installedVersions(ctx, env)
	if err != nil {
		return inv, err
	}
	if _, ok := versions["docker-ce"]; !ok {
		return inv, errcode.New(errcode.SystemNameInvalid, "Docker Engine (docker-ce) is not installed")
	}
	if info, err := h.m.d.docker.Info(ctx); err != nil {
		inv.unchecked = "the Docker daemon does not answer (" + err.Error() + ")"
	} else {
		inv.containers, inv.images = info.Containers, info.Images
		labelled, err := h.m.labelledContainers(ctx)
		switch {
		case err != nil:
			inv.unchecked = "the Docker daemon could not list its containers (" + err.Error() + ")"
		case len(labelled) > 0:
			names := make([]string, len(labelled))
			for i, l := range labelled {
				names[i] = l.Name + " (" + l.Module + ")"
			}
			return inv, errcode.New(errcode.SystemDockerLabelled,
				"remove these containers through their Darkpanel pages first: "+strings.Join(names, ", "))
		}
		if vols, err := h.m.d.docker.ListVolumes(ctx, nil); err != nil {
			inv.unchecked = "the Docker daemon could not list its volumes (" + err.Error() + ")"
		} else {
			inv.volume = len(vols)
		}
	}
	if in.Purge {
		if inv.unchecked != "" {
			return inv, errcode.New(errcode.SystemPurgeRefused,
				"deleting the data needs the Docker daemon to count and check the containers, but "+inv.unchecked+
					"; start the daemon, or uninstall without deleting the data")
		}
		for _, p := range dockerPurgePaths {
			if err := h.m.d.purge.CheckPurgeable(p); err != nil {
				return inv, errcode.New(errcode.SystemPurgeRefused, "cannot delete "+p+": "+err.Error())
			}
		}
		if inv.dataRoot, err = h.m.dockerDataRoot(); err != nil {
			return inv, err
		}
	}
	for _, p := range []string{dockerListPath, dockerKeyringPath} {
		owned, err := env.Files.Owns(ctx, ID, p)
		if err != nil {
			return inv, fmt.Errorf("system: read the ownership of %s: %w", p, err)
		}
		if owned {
			inv.owned = append(inv.owned, p)
		}
	}
	return inv, nil
}

func (h dockerUninstallHandler) Plan(ctx context.Context, env core.Env, in DockerUninstallInput) (core.PlanOutcome, error) {
	inv, err := h.inventory(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	names, err := installedDockerPackages(ctx, env, sortedDockerPackages)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	out, _, err := h.m.planSet(ctx, env, "remove", names, dockerPackages)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	state := []string{out.StateHash, "purge=" + strconv.FormatBool(in.Purge), "unchecked=" + strconv.FormatBool(inv.unchecked != "")}
	for _, p := range inv.owned {
		out.Diff = append(out.Diff, core.DiffLine{Op: "-", Text: "file " + p + " (Darkpanel wrote it)"})
		state = append(state, "owned="+p)
	}
	out.Diff = append(out.Diff, core.DiffLine{Op: " ", Text: "Docker's configuration in /etc/docker stays: the packages are removed, not purged."})
	host, err := h.m.hostname()
	if err != nil {
		return core.PlanOutcome{}, err
	}
	out.Confirmation = host
	switch {
	case in.Purge:
		counts := plural(inv.containers, "container") + ", " + plural(inv.images, "image") + ", " + plural(inv.volume, "volume")
		state = append(state, "counts="+counts)
		if inv.dataRoot == "" {
			out.Diff = append(out.Diff, core.DiffLine{Op: "-", Text: "Delete " + counts + ": " + strings.Join(dockerPurgePaths, " and ") + " are deleted"})
		} else {
			// The daemon's counts are of the data under data-root, which the
			// purge leaves; only the two default directories go.
			out.Diff = append(out.Diff,
				core.DiffLine{Op: "-", Text: "Delete " + strings.Join(dockerPurgePaths, " and ")},
				core.DiffLine{Op: " ", Text: dockerDaemonConfig + " sets data-root " + inv.dataRoot + ": its " + counts + " are left alone"})
			state = append(state, "data-root="+inv.dataRoot)
		}
		out.Confirmation = "purge docker on " + host
	case inv.unchecked != "":
		out.Diff = append(out.Diff, core.DiffLine{Op: " ", Text: "The labelled-container check could not run: " + inv.unchecked +
			"; containers Darkpanel modules created are not checked"})
	default:
		out.Diff = append(out.Diff, core.DiffLine{Op: " ", Text: "Containers, images and volumes stay in " + strings.Join(dockerPurgePaths, " and ")})
	}
	sum := sha256.Sum256([]byte(strings.Join(state, "\n")))
	out.StateHash = hex.EncodeToString(sum[:])
	return out, nil
}

// Task re-runs the refusals and the plan's simulation, removes the packages, then the repository files the
// ownership store records as the module's, then, with purge, the two data directories.
func (h dockerUninstallHandler) Task(ctx context.Context, env core.Env, in DockerUninstallInput) (DockerUninstalled, error) {
	out := DockerUninstalled{Removed: []string{}, Files: []string{}, Purged: []string{}}
	inv, err := h.inventory(ctx, env, in)
	if err != nil {
		return out, core.Definite(err)
	}
	names, err := installedDockerPackages(ctx, env, sortedDockerPackages)
	if err != nil {
		return out, core.Definite(err)
	}
	planned, err := h.m.simulateSet(ctx, env, "remove", names, dockerPackages)
	if err != nil {
		return out, core.Definite(err)
	}
	applied, err := runSet(ctx, env, "remove", names, planned)
	if len(applied.Removed) > 0 {
		out.Removed = applied.Removed
	}
	if err != nil {
		return out, err
	}
	for _, p := range inv.owned {
		if err := env.Files.Remove(ctx, ID, p); err != nil {
			return out, fmt.Errorf("system: remove %s: %w", p, err)
		}
		out.Files = append(out.Files, p)
	}
	if !in.Purge {
		return out, nil
	}
	for _, p := range dockerPurgePaths {
		// The plan checked both paths; this refusal means the host changed since.
		if err := h.m.d.purge.PurgeDir(p); err != nil {
			return out, errcode.Wrap(errcode.SystemPurgeRefused, err, "Docker was removed, but "+p+" was not deleted: "+err.Error())
		}
		out.Purged = append(out.Purged, p)
	}
	return out, nil
}

// Verify checks the five packages and the removed repository files are gone.
func (h dockerUninstallHandler) Verify(ctx context.Context, env core.Env, _ DockerUninstallInput, out DockerUninstalled) error {
	versions, err := installedVersions(ctx, env)
	if err != nil {
		return err
	}
	for _, n := range dockerPackages {
		if _, ok := versions[n]; ok {
			return fmt.Errorf("system: %s is still installed", n)
		}
	}
	for _, p := range out.Files {
		if _, err := h.m.d.readFile(p); !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("system: %s is still present", p)
		}
	}
	return nil
}

// dockerDataRoot returns the data-root daemon.json sets, "" when it sets none or names /var/lib/docker.
func (m module) dockerDataRoot() (string, error) {
	b, err := m.d.readFile(dockerDaemonConfig)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("system: read %s: %w", dockerDaemonConfig, err)
	}
	var cfg struct {
		DataRoot string `json:"data-root"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return "", errcode.New(errcode.SystemPurgeRefused, dockerDaemonConfig+" is not valid JSON, so the data-root it may set cannot be told: "+err.Error())
	}
	if root := strings.TrimRight(cfg.DataRoot, "/"); root != "" && root != dockerPurgePaths[0] {
		return root, nil
	}
	return "", nil
}

// pendingFirewallChanges refuses while a firewall change on the host is applying or awaiting confirmation: its
// automatic revert could turn the Docker integration back on for a host without Docker.
func pendingFirewallChanges(ctx context.Context, env core.Env) error {
	rows, err := env.DB.Read().QueryContext(ctx,
		`SELECT id, action, state FROM core_changes WHERE action LIKE 'firewall.%' AND state IN (?, ?) ORDER BY id`,
		core.StateApplying, core.StateAwaitingConfirmation)
	if err != nil {
		return fmt.Errorf("system: read the pending firewall changes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var pending []string
	for rows.Next() {
		var id, action, state string
		if err := rows.Scan(&id, &action, &state); err != nil {
			return fmt.Errorf("system: read the pending firewall changes: %w", err)
		}
		pending = append(pending, fmt.Sprintf("%s (%s, %s)", id, action, state))
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("system: read the pending firewall changes: %w", err)
	}
	if len(pending) > 0 {
		return errcode.New(errcode.SystemFirewallPending,
			"keep or revert these firewall changes first; a revert could turn the Docker integration back on: "+strings.Join(pending, ", "))
	}
	return nil
}

// firewallDockerFlag reads the firewall module's stored Docker integration flag from the agent DB; no row is
// off. The flag counts whether or not the module is enabled, because disabling it leaves the integration's rules.
func firewallDockerFlag(ctx context.Context, env core.Env) (bool, error) {
	var on bool
	err := env.DB.Read().QueryRowContext(ctx, "SELECT docker FROM firewall_settings WHERE id = 1").Scan(&on)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("system: read the firewall's Docker integration: %w", err)
	}
	return on, nil
}

// moduleEnabled reads the enabled flag the controller last pushed to the agent (core_meta
// module_enabled.<id>); with no row it reports enabled, so a message never claims a module is off unread.
func moduleEnabled(ctx context.Context, env core.Env, id string) (bool, error) {
	var v string
	err := env.DB.Read().QueryRowContext(ctx, "SELECT value FROM core_meta WHERE key = ?", "module_enabled."+id).Scan(&v)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("system: read whether the %s module is enabled: %w", id, err)
	}
	return v != "0", nil
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
