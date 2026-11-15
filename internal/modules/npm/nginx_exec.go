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
	// nginxPendingExpiry is how long a pending exec blocks later reloads. nginx -t and nginx -s reload end in well under
	// a second; one still unfinished after this is an exec whose exit the Engine missed or whose start never reached it.
	nginxPendingExpiry = 2 * time.Minute
	nginxPsArgs        = "-eo pid,ppid,args"
	nginxMaster        = "nginx: master process"
	nginxWorker        = "nginx: worker process"
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
// sent back to NPM. pending is an exec that may still run after its call gave up, at the deadline or on an answer
// that was lost: it can still signal nginx, so no later call starts another until it has ended or, at the latest,
// until nginxPendingExpiry after it was recorded.
type nginxReloader struct {
	e         execEngine
	mu        sync.Mutex
	wait      time.Duration
	poll      time.Duration
	now       func() time.Time
	pending   string
	pendingAt time.Time
}

func newNginxReloader(e execEngine) *nginxReloader {
	return &nginxReloader{e: e, wait: nginxReloadWait, poll: nginxReloadPoll, now: time.Now}
}

func reloadFailure(format string, a ...any) error {
	return fmt.Errorf("npm: nginx reload: "+format, a...)
}

// Reload returns nil only once nginx -t passed, nginx -s reload exited 0 and a worker of the master that was not
// running just before the signal appeared: the signal alone does not say the master applied the configuration, and a
// master that rejects it keeps its old workers. A failed test is errNginxTest; every other failure is not.
func (r *nginxReloader) Reload(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, r.wait)
	defer cancel()
	if r.pending != "" && r.now().Sub(r.pendingAt) < nginxPendingExpiry {
		st, err := r.e.ExecInspect(ctx, r.pending)
		switch {
		case dockerapi.IsNotFound(err):
		case err != nil:
			return reloadFailure("%w", err)
		// An exec not running with no exit code has not started yet, and its start may still be in flight.
		case st.Running || st.ExitCode == nil:
			return errNginxBusy
		}
	}
	r.pending = ""
	user, err := r.user(ctx)
	if err != nil {
		return err
	}
	if err := r.run(ctx, user, []string{"nginx", "-t", "-g", "error_log off;"}, true); err != nil {
		return err
	}
	before, err := r.workers(ctx)
	if err != nil {
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

// workers lists the pids of the nginx master's workers; a process that only looks like a worker is not counted.
func (r *nginxReloader) workers(ctx context.Context) (map[string]bool, error) {
	top, err := r.e.ContainerTop(ctx, containerName, nginxPsArgs)
	if err != nil {
		return nil, reloadFailure("%w", err)
	}
	pidCol, ppidCol, cmdCol := -1, -1, -1
	for i, t := range top.Titles {
		switch strings.ToUpper(t) {
		case "PID":
			pidCol = i
		case "PPID":
			ppidCol = i
		case "COMMAND", "ARGS":
			cmdCol = i
		}
	}
	if pidCol < 0 || ppidCol < 0 || cmdCol < 0 {
		return nil, reloadFailure("the process listing has no PID, PPID or COMMAND column")
	}
	var rows [][]string
	masters := map[string]bool{}
	for _, p := range top.Processes {
		if len(p) > pidCol && len(p) > ppidCol && len(p) > cmdCol {
			rows = append(rows, p)
			if strings.HasPrefix(p[cmdCol], nginxMaster) {
				masters[p[pidCol]] = true
			}
		}
	}
	pids := map[string]bool{}
	for _, p := range rows {
		if strings.HasPrefix(p[cmdCol], nginxWorker) && masters[p[ppidCol]] {
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
		// A start cut off by the deadline, or whose answer was lost, may have reached the Engine, which cannot kill
		// the exec; only a refusal says it did not start.
		if ctx.Err() != nil || !refused(err) {
			r.hold(id)
		}
		return reloadFailure("%w", err)
	}
	tick := time.NewTicker(r.poll)
	defer tick.Stop()
	for {
		st, err := r.e.ExecInspect(ctx, id)
		if err != nil && ctx.Err() == nil {
			r.hold(id)
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
			r.hold(id)
			return reloadFailure("nginx command timed out")
		case <-tick.C:
		}
	}
}

// hold records id as pending from now.
func (r *nginxReloader) hold(id string) {
	r.pending, r.pendingAt = id, r.now()
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
