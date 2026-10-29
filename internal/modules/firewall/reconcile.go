package firewall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// The background task that keeps the Docker integration in step with the
// containers and networks (spec 2.8).
const (
	reconcileID    = ID + ".reconcile"
	reconcileEvery = 5 * time.Minute
)

// reconcileTask declares firewall.reconcile: a pass every five minutes and
// on each Docker event that can change what the stored rows realize.
func (f fw) reconcileTask() core.BackgroundTask {
	return core.BackgroundTask{
		ID:       reconcileID,
		Every:    reconcileEvery,
		LockKeys: []string{lockKey},
		Run:      f.reconcile,
		Watch:    f.reconcileWatch,
	}
}

// reconcile is one pass. It changes nothing itself: what it finds out of
// step it hands to tc.Apply as a recorded change. It never discards a
// foreign DOCKER-USER rule, so it skips while one is loaded.
func (f fw) reconcile(ctx context.Context, tc core.TaskContext) error {
	env := tc.Env
	// The first pass after an agent start is what repeats a cleanup a crash
	// interrupted, so this runs whatever the integration flag says.
	if err := f.finishCleanups(ctx, env); err != nil {
		return fmt.Errorf("firewall: reconcile: finish the owned-chain cleanups: %w", err)
	}
	dc, err := readDockerConfig(ctx, env.DB)
	if err != nil {
		return fmt.Errorf("firewall: reconcile: %w", err)
	}
	if !dc.Enabled {
		return nil
	}
	cfg, err := f.d.readConfig(f.d.root)
	if err != nil {
		return fmt.Errorf("firewall: reconcile: %w", err)
	}
	// With UFW off no block is loaded; firewall.state.enable writes it.
	if !cfg.Enabled {
		return nil
	}
	cur, err := f.d.readLoaded(ctx, env)
	if err != nil {
		return fmt.Errorf("firewall: reconcile: %w", err)
	}
	if err := dockerUserInUse(cur, cfg, true); err != nil {
		var inUse *errcode.Error
		if errors.As(err, &inUse) && inUse.Code == errcode.FirewallDockerUserInUse {
			env.Log.Warn("reconcile skipped: DOCKER-USER holds foreign rules", "error", err)
			return nil
		}
		return fmt.Errorf("firewall: reconcile: %w", err)
	}
	st, err := f.dockerState(ctx, env, cur)
	if err != nil {
		return fmt.Errorf("firewall: reconcile: %w", err)
	}
	return f.reconcileWith(ctx, tc, cfg, cur, st)
}

// reconcileWith decides, from the configuration cfg, the loaded state cur
// and the Docker state st, which action brings the host in step, and
// applies it. Only differences in Darkpanel's own chains are repaired: no
// automatic operation rewrites chains Darkpanel does not own, and a change
// applied over such a difference would fail its plan's guard.
func (f fw) reconcileWith(ctx context.Context, tc core.TaskContext, cfg fwstate.Config, cur fwstate.Loaded, st dockerState) error {
	log := tc.Env.Log
	if !st.available {
		log.Debug("reconcile skipped: Docker is unavailable", "reason", st.reason)
		return nil
	}
	if reasons := dockerUnsupported(st); len(reasons) > 0 {
		log.Warn("reconcile skipped: unsupported Docker integration", "reasons", reasons)
		return nil
	}
	diffs, err := f.syncDiffs(cfg, cur)
	if err != nil {
		return fmt.Errorf("firewall: reconcile: %w", err)
	}
	var action string
	var input any
	if len(diffs) > 0 {
		var outside []string
		for _, d := range diffs {
			if !dockerfw.Owned(d.Chain) {
				outside = append(outside, string(d.Family)+" "+d.Table+" "+d.Chain)
			}
		}
		if len(outside) > 0 {
			log.Warn("reconcile skipped: the host is out of sync outside the Docker integration's chains; firewall.state.repair is the operator's call", "chains", outside)
			return nil
		}
		action, input = "firewall.docker.repair", DockerRepairInput{}
	} else {
		files, err := blockFiles(cfg, st, true)
		if err != nil {
			return fmt.Errorf("firewall: reconcile: %w", err)
		}
		ops, err := containerRuleOps(cfg, st.realized.Rules, newULID(tc.Env))
		if err != nil {
			return fmt.Errorf("firewall: reconcile: %w", err)
		}
		inStep := len(ops) == 0
		for _, p := range []string{"/etc/ufw/after.rules", "/etc/ufw/after6.rules"} {
			inStep = inStep && bytes.Equal(files[p], cfg.Files[p].Data)
		}
		if inStep {
			log.Debug("reconcile: the host is in step")
			return nil
		}
		action, input = "firewall.containers.apply", ContainersApplyInput{Ops: []ContainerOp{}}
	}
	if _, err := tc.Apply(ctx, action, input); err != nil {
		return fmt.Errorf("firewall: reconcile: %s: %w", action, err)
	}
	log.Info("reconcile applied "+action, "action", action)
	return nil
}

// reconcileWatch wakes the task for each Docker event reconcileEvent
// accepts, until ctx ends (nil) or the stream fails. The stream's end is an
// error too, so the runtime restarts the watch and wakes a full pass.
func (f fw) reconcileWatch(ctx context.Context, _ core.Env, wake func()) error {
	stream, err := f.d.docker.Events(ctx, dockerapi.EventsOptions{Filters: dockerapi.Filters{
		"type":  {"container", "network"},
		"event": {"start", "die", "destroy", "connect", "disconnect", "create"},
	}})
	if err == nil {
		defer func() { _ = stream.Close() }()
		for {
			var ev dockerapi.Event
			if ev, err = stream.Next(); err != nil {
				break
			}
			if reconcileEvent(ev) {
				wake()
			}
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	return fmt.Errorf("firewall: reconcile: watch Docker events: %w", err)
}

// reconcileEvent reports whether ev is one of the events of spec 2.8.
// Docker's event filter alone also lets container create through, and
// actions such as "exec_start: sh" carry a suffix, so the whole action is
// compared.
func reconcileEvent(ev dockerapi.Event) bool {
	switch ev.Type {
	case "container":
		switch ev.Action {
		case "start", "die", "destroy":
			return true
		}
	case "network":
		switch ev.Action {
		case "connect", "disconnect", "create", "destroy":
			return true
		}
	}
	return false
}
