package npm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const (
	// nginxReloadWait bounds one Reload: the user lookup, both commands and the wait for a new worker together.
	nginxReloadWait = 10 * time.Second
	nginxReloadPoll = 100 * time.Millisecond
	nginxPsArgs     = "-eo pid,ppid,args"
	nginxWorker     = "nginx: worker process"
)

var (
	errNginxTest = errors.New("nginx's configuration test failed")
	errNginxBusy = errors.New("an earlier nginx command is still running")
)

type execEngine interface {
	InspectContainer(ctx context.Context, id string) (dockerapi.ContainerJSON, error)
	ExecCreate(ctx context.Context, container string, o dockerapi.ExecCreate) (string, error)
	ExecStart(ctx context.Context, id string) error
	ExecInspect(ctx context.Context, id string) (dockerapi.ExecInspect, error)
	ContainerTop(ctx context.Context, container, psArgs string) (dockerapi.TopResult, error)
}

// nginxReloader tests and reloads nginx inside darkpanel-npm by exec, as NPM's own backend does, so no proxy host is
// sent back to NPM. pending is an exec that was still running when its call gave up: it can still signal nginx, so
// no later call starts another until it has ended.
type nginxReloader struct {
	e       execEngine
	mu      sync.Mutex
	wait    time.Duration
	poll    time.Duration
	pending string
}

func newNginxReloader(e execEngine) *nginxReloader {
	return &nginxReloader{e: e, wait: nginxReloadWait, poll: nginxReloadPoll}
}

func reloadFailure(format string, a ...any) error {
	return fmt.Errorf("npm: nginx reload: "+format, a...)
}

// Reload returns nil only once nginx -t passed, nginx -s reload exited 0 and a worker that was not running before
// the signal appeared: the signal alone does not say the master applied the configuration, and a master that
// rejects it keeps its old workers. A failed test is errNginxTest; every other failure is not.
func (r *nginxReloader) Reload(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, r.wait)
	defer cancel()
	if r.pending != "" {
		st, err := r.e.ExecInspect(ctx, r.pending)
		switch {
		case dockerapi.IsNotFound(err):
		case err != nil:
			return reloadFailure("%w", err)
		case st.Running:
			return errNginxBusy
		}
		r.pending = ""
	}
	user, err := r.user(ctx)
	if err != nil {
		return err
	}
	before, err := r.workers(ctx)
	if err != nil {
		return err
	}
	if err := r.run(ctx, user, []string{"nginx", "-t", "-g", "error_log off;"}, true); err != nil {
		return err
	}
	if err := r.run(ctx, user, []string{"nginx", "-s", "reload"}, false); err != nil {
		return err
	}
	return r.newWorker(ctx, before)
}

// user is the PUID:PGID nginx runs as in the image, root when neither is set.
func (r *nginxReloader) user(ctx context.Context) (string, error) {
	c, err := r.e.InspectContainer(ctx, containerName)
	if err != nil {
		return "", reloadFailure("%w", err)
	}
	if !c.State.Running {
		return "", reloadFailure("%s is not running", containerName)
	}
	uid, hasUID := envValue(c.Config.Env, "PUID")
	gid, hasGID := envValue(c.Config.Env, "PGID")
	switch {
	case !hasUID && !hasGID:
		return "0:0", nil
	case hasUID && hasGID && digitsOnly(uid) && digitsOnly(gid):
		return uid + ":" + gid, nil
	}
	return "", reloadFailure("%s has a PUID or PGID that is not a pair of numeric ids", containerName)
}

func envValue(env []string, key string) (string, bool) {
	value, found := "", false
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			value, found = v, true
		}
	}
	return value, found
}

func digitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (r *nginxReloader) workers(ctx context.Context) (map[string]bool, error) {
	top, err := r.e.ContainerTop(ctx, containerName, nginxPsArgs)
	if err != nil {
		return nil, reloadFailure("%w", err)
	}
	pidCol, cmdCol := -1, -1
	for i, t := range top.Titles {
		switch strings.ToUpper(t) {
		case "PID":
			pidCol = i
		case "COMMAND", "ARGS":
			cmdCol = i
		}
	}
	if pidCol < 0 || cmdCol < 0 {
		return nil, reloadFailure("the process listing has no PID or COMMAND column")
	}
	pids := map[string]bool{}
	for _, p := range top.Processes {
		if len(p) > pidCol && len(p) > cmdCol && strings.HasPrefix(p[cmdCol], nginxWorker) {
			pids[p[pidCol]] = true
		}
	}
	return pids, nil
}

func (r *nginxReloader) run(ctx context.Context, user string, cmd []string, isTest bool) error {
	id, err := r.e.ExecCreate(ctx, containerName, dockerapi.ExecCreate{Cmd: cmd, User: user})
	if err != nil {
		return reloadFailure("%w", err)
	}
	if err := r.e.ExecStart(ctx, id); err != nil {
		return reloadFailure("%w", err)
	}
	tick := time.NewTicker(r.poll)
	defer tick.Stop()
	for {
		st, err := r.e.ExecInspect(ctx, id)
		if err != nil && ctx.Err() == nil {
			return reloadFailure("%w", err)
		}
		if err == nil && !st.Running {
			switch {
			case st.ExitCode == nil:
				return reloadFailure("no exit code")
			case *st.ExitCode == 0:
				return nil
			case isTest:
				return errNginxTest
			default:
				return reloadFailure("nginx reload exited %d", *st.ExitCode)
			}
		}
		select {
		case <-ctx.Done():
			r.pending = id
			return reloadFailure("nginx command timed out")
		case <-tick.C:
		}
	}
}

func (r *nginxReloader) newWorker(ctx context.Context, before map[string]bool) error {
	tick := time.NewTicker(r.poll)
	defer tick.Stop()
	for {
		now, err := r.workers(ctx)
		if err != nil && ctx.Err() == nil {
			return err
		}
		for pid := range now {
			if !before[pid] {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return reloadFailure("no new nginx worker after the reload")
		case <-tick.C:
		}
	}
}
