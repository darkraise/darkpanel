package derp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
	"github.com/darkraise/darkpanel/internal/platform"
)

const (
	// uninstallPoll is how often an uninstall reads derper's unit while it waits for the unit to stop.
	uninstallPoll = 500 * time.Millisecond
	// uninstallStopWait is how long an uninstall waits for derper's unit to stop after it asked systemd to.
	uninstallStopWait = 20 * time.Second
)

// serverUninstall is derp.server.uninstall: it removes what derp.server.install made, the server record last. Its
// compensation is derp.server.install.
func (d dp) serverUninstall() core.ActionDef {
	return core.DefineMutating[ServerUninstallInput, ServerUninstalled](core.ActionSpec{
		Name: ID + ".server.uninstall", Summary: "Remove the DERP server from this host.",
		Version: 1, Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: []string{lockKey},
		CompensatedBy: ID + ".server.install", Converges: true, ApplyTimeout: 45 * time.Second,
	}, serverUninstallHandler{d: d})
}

// serverUninstallHandler is derp.server.uninstall's handler. Every decision about what exists comes from facts, so
// Plan, Apply, Observe and Verify read the host the same way.
type serverUninstallHandler struct{ d dp }

// uninstallFacts is what of derp.server.install's work exists on the host, read from the module's records. A file,
// directory or user the module did not record is someone else's and does not count.
type uninstallFacts struct {
	unitLoaded  bool   // systemd knows derper's unit
	unitActive  bool   // derper's unit is neither inactive nor failed
	unitEnabled bool   // derper's unit is loaded and its enablement link exists
	unitFile    bool   // the module records the unit file
	binary      string // the first of the binary, its .new and its .prev copy the module records; "" when none
	dir         bool   // the module records the service directory
	user        bool   // the ownership store records the service user
	server      bool   // the derp_server row exists
}

// uninstallRemoval is one of the six removals, in the order uninstall makes them.
type uninstallRemoval int

const (
	uninstallUnit uninstallRemoval = iota
	uninstallUnitFile
	uninstallBinary
	uninstallDir
	uninstallUser
	uninstallServer
)

// uninstallRemovals is every removal in the order they run. The server record goes last, so that a run cut off part
// way still finds it and converges when run again.
var uninstallRemovals = []uninstallRemoval{
	uninstallUnit, uninstallUnitFile, uninstallBinary, uninstallDir, uninstallUser, uninstallServer,
}

// resource is the effect resource of r.
func (r uninstallRemoval) resource() string {
	switch r {
	case uninstallUnit:
		return ID + ".unit:" + server.UnitName
	case uninstallUnitFile:
		return ID + ".file:" + server.UnitPath
	case uninstallBinary:
		return ID + ".file:" + server.BinaryPath
	case uninstallDir:
		return ID + ".dir:" + server.StateDir
	case uninstallUser:
		return ID + ".user:" + server.ServiceUser
	default:
		return ID + ".server:this"
	}
}

// exists reports whether the object of r is there to remove.
func (f uninstallFacts) exists(r uninstallRemoval) bool {
	switch r {
	case uninstallUnit:
		// A loaded unit that neither runs nor is enabled has nothing to stop or disable.
		return f.unitActive || f.unitEnabled
	case uninstallUnitFile:
		return f.unitFile
	case uninstallBinary:
		return f.binary != ""
	case uninstallDir:
		return f.dir
	case uninstallUser:
		return f.user
	default:
		return f.server
	}
}

// doc is the state document of r's object: the unit's active and enabled flags, or every other object's presence.
func (f uninstallFacts) doc(r uninstallRemoval) core.StateDoc {
	if r == uninstallUnit {
		return unitDoc(f.unitActive, f.unitEnabled)
	}
	return r.doc(f.exists(r))
}

// unitDoc is the state document of derper's unit with its active and enabled flags.
func unitDoc(active, enabled bool) core.StateDoc {
	b, _ := json.Marshal(map[string]bool{"active": active, "enabled": enabled})
	return core.StateDoc{State: b}
}

// doc is the state document of r's object with its one member, "present", set to v.
func (r uninstallRemoval) doc(v bool) core.StateDoc {
	b, _ := json.Marshal(map[string]bool{"present": v})
	return core.StateDoc{State: b}
}

// removed is the state document of r's object once r removed it.
func (r uninstallRemoval) removed() core.StateDoc {
	if r == uninstallUnit {
		return unitDoc(false, false)
	}
	return r.doc(false)
}

// facts reads what exists. It only reads.
func (h serverUninstallHandler) facts(ctx context.Context, env core.Env) (uninstallFacts, error) {
	var f uninstallFacts
	st, err := env.Units.State(ctx, server.UnitName)
	if err != nil {
		return f, err
	}
	f.unitLoaded = st.LoadState != "not-found"
	f.unitActive = f.unitLoaded && st.ActiveState != "inactive" && st.ActiveState != "failed"
	if f.unitLoaded {
		// Any error but a missing link counts as enabled, as installHandler.exists reads it.
		_, err := h.d.d.readFile(installWantsLink)
		f.unitEnabled = !errors.Is(err, fs.ErrNotExist)
	}
	if f.unitFile, err = env.Files.Owns(ctx, ID, server.UnitPath); err != nil {
		return f, err
	}
	for _, p := range []string{server.BinaryPath, server.BinaryNewPath, server.BinaryPrevPath} {
		owned, err := env.Files.Owns(ctx, ID, p)
		if err != nil {
			return f, err
		}
		if owned {
			f.binary = p
			break
		}
	}
	if f.dir, err = env.Files.Owns(ctx, ID, server.StateDir); err != nil {
		return f, err
	}
	if f.user, err = platform.NewOwnership(env.DB).Owns(ctx, ID, server.ServiceUser); err != nil {
		return f, err
	}
	_, f.server, err = loadLocalServer(ctx, env.DB.Read())
	return f, err
}

func (h serverUninstallHandler) Plan(ctx context.Context, env core.Env, _ ServerUninstallInput) (core.PlanOutcome, error) {
	f, err := h.facts(ctx, env)
	if err != nil {
		return core.PlanOutcome{}, core.Definite(err)
	}
	var diff []core.DiffLine
	var state []string
	for _, r := range uninstallRemovals {
		if !f.exists(r) {
			continue
		}
		diff = append(diff, core.DiffLine{Op: "-", Text: f.planText(r)})
		state = append(state, r.resource())
	}
	state = append(state, "active="+strconv.FormatBool(f.unitActive), "enabled="+strconv.FormatBool(f.unitEnabled))
	sum := sha256.Sum256([]byte(strings.Join(state, "\n")))
	return core.PlanOutcome{Diff: diff, StateHash: hex.EncodeToString(sum[:])}, nil
}

// planText is the diff line text of removal r.
func (f uninstallFacts) planText(r uninstallRemoval) string {
	switch r {
	case uninstallUnit:
		if f.unitActive {
			return "unit " + server.UnitName + ": stop and disable"
		}
		return "unit " + server.UnitName + ": disable"
	case uninstallUnitFile:
		return "file " + server.UnitPath
	case uninstallBinary:
		return "file " + server.BinaryPath + ", with its .new and .prev copies"
	case uninstallDir:
		return "directory " + server.StateDir + ", with the node key and the certificates in it"
	case uninstallUser:
		return "user " + server.ServiceUser
	default:
		return "the DERP server record of this host"
	}
}

// Apply reads the host again and removes what exists, journaling each removal as a delete effect before it touches
// the host. A failed removal leaves its effect intended for the engine to settle through Observe, and stops there.
func (h serverUninstallHandler) Apply(ctx context.Context, env core.Env, _ ServerUninstallInput) (ServerUninstalled, error) {
	f, err := h.facts(ctx, env)
	if err != nil {
		return ServerUninstalled{}, core.Definite(err)
	}
	out := ServerUninstalled{Removed: []string{}}
	for _, r := range uninstallRemovals {
		if !f.exists(r) {
			continue
		}
		after := r.removed()
		seq, err := env.Effects.Intend(ctx, core.Effect{
			Kind: core.EffectDelete, Resource: r.resource(), Local: true, Before: f.doc(r), After: &after,
		}, nil)
		if err != nil {
			return ServerUninstalled{}, err
		}
		if err := h.remove(ctx, env, r, f); err != nil {
			return ServerUninstalled{}, err
		}
		if err := env.Effects.Made(ctx, seq, r.resource(), after); err != nil {
			return ServerUninstalled{}, err
		}
		out.Removed = append(out.Removed, r.resource())
	}
	return out, nil
}

// remove makes removal r on the host.
func (h serverUninstallHandler) remove(ctx context.Context, env core.Env, r uninstallRemoval, f uninstallFacts) error {
	switch r {
	case uninstallUnit:
		if f.unitActive {
			if err := h.stop(ctx, env); err != nil {
				return err
			}
		}
		return env.Units.Disable(ctx, server.UnitName)
	case uninstallUnitFile:
		if err := env.Files.Remove(ctx, ID, server.UnitPath); err != nil {
			return err
		}
		return env.Units.Reload(ctx)
	case uninstallBinary:
		// The binary goes last, so that a run cut off before it still finds the binary recorded.
		for _, p := range []string{server.BinaryNewPath, server.BinaryPrevPath, server.BinaryPath} {
			if err := env.Files.Remove(ctx, ID, p); err != nil {
				return err
			}
		}
		return nil
	case uninstallDir:
		return env.Files.RemoveServiceDir(ctx, ID, server.StateDir)
	case uninstallUser:
		return h.removeUser(ctx, env)
	default:
		return deleteLocalServer(ctx, env.DB)
	}
}

// stop stops derper's unit and waits, polling every uninstallPoll, until systemd reports it inactive or failed with
// no job pending, for at most uninstallStopWait. The module's user cannot be deleted while derper still runs as it.
func (h serverUninstallHandler) stop(ctx context.Context, env core.Env) error {
	if err := env.Units.Stop(ctx, server.UnitName); err != nil {
		return err
	}
	deadline := env.Clock.Now().Add(uninstallStopWait)
	for {
		st, err := env.Units.State(ctx, server.UnitName)
		if err != nil {
			return err
		}
		if (st.ActiveState == "inactive" || st.ActiveState == "failed") && st.PendingJob == "" {
			return nil
		}
		if !env.Clock.Now().Before(deadline) {
			return fmt.Errorf("derp: %s did not stop within %s", server.UnitName, uninstallStopWait)
		}
		tick := make(chan struct{})
		timer := env.Clock.AfterFunc(uninstallPoll, func() { close(tick) })
		select {
		case <-tick:
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("derp: wait for %s to stop: %w", server.UnitName, ctx.Err())
		}
	}
}

// removeUser deletes the module's user when it exists on the host and forgets the record in every case.
func (h serverUninstallHandler) removeUser(ctx context.Context, env core.Env) error {
	_, _, exists, err := h.d.d.lookupUser(server.ServiceUser)
	if err != nil {
		return err
	}
	if exists {
		res, err := env.Exec.Run(ctx, core.Cmd{Path: "/usr/sbin/userdel", Args: []string{server.ServiceUser}})
		if err != nil {
			return fmt.Errorf("derp: userdel %s: %w", server.ServiceUser, err)
		}
		if res.ExitCode != 0 {
			if line := serverUninstallLastLine(res.Stderr); line != "" {
				return fmt.Errorf("derp: userdel %s exited %d: %s", server.ServiceUser, res.ExitCode, line)
			}
			return fmt.Errorf("derp: userdel %s exited %d", server.ServiceUser, res.ExitCode)
		}
	}
	return platform.NewOwnership(env.DB).Forget(ctx, ID, server.ServiceUser, "")
}

// serverUninstallLastLine is the last non-empty line of b, trimmed.
func serverUninstallLastLine(b []byte) string {
	lines := strings.Split(string(b), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

func (h serverUninstallHandler) Verify(ctx context.Context, env core.Env, _ ServerUninstallInput, _ ServerUninstalled) error {
	f, err := h.facts(ctx, env)
	if err != nil {
		return err
	}
	switch {
	case f.unitActive:
		return fmt.Errorf("derp: %s is still active", server.UnitName)
	case f.unitFile:
		return fmt.Errorf("derp: %s is still recorded as the module's", server.UnitPath)
	case f.binary != "":
		return fmt.Errorf("derp: %s is still recorded as the module's", f.binary)
	case f.dir:
		return fmt.Errorf("derp: %s is still recorded as the module's", server.StateDir)
	case f.user:
		return fmt.Errorf("derp: %s is still recorded as the module's", uninstallUser.resource())
	case f.server:
		return fmt.Errorf("derp: %s is still recorded", uninstallServer.resource())
	}
	return nil
}

// Observe reads the object of one of the six effects uninstall journals: the unit's active and enabled flags, or
// the presence of every other object, as the facts define them.
func (h serverUninstallHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	for _, r := range uninstallRemovals {
		if r.resource() != e.Resource {
			continue
		}
		f, err := h.facts(ctx, env)
		if err != nil {
			return core.Observation{}, err
		}
		return core.Observation{Current: f.doc(r), Matches: 1, Exclusive: true}, nil
	}
	return core.Observation{}, fmt.Errorf("derp: %q is not a resource derp.server.uninstall removes", e.Resource)
}
