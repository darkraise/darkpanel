package docker

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// preflightTimeout bounds the check's call to the daemon: the runtime runs
// preflights in sequence while it opens, so a daemon that accepts the
// connection and never answers would otherwise hold up the agent's start.
const preflightTimeout = 10 * time.Second

// preflight is the module's preflight (spec 3): the Docker daemon answers on
// its socket with an API version of at least dockerapi.MinAPIVersion.
func (k dk) preflight() []core.PreflightCheck {
	return []core.PreflightCheck{{
		ID: ID + ".engine",
		Run: func(ctx context.Context, env core.Env) error {
			clock := env.Clock
			if clock == nil {
				clock = core.SystemClock{}
			}
			ctx, cancel := context.WithCancelCause(ctx)
			defer cancel(nil)
			timer := clock.AfterFunc(preflightTimeout, func() { cancel(errNoAnswer) })
			defer timer.Stop()
			_, err := k.probe(ctx)
			if err != nil && errors.Is(context.Cause(ctx), errNoAnswer) {
				return errNoAnswer
			}
			return err
		},
	}}
}

var errNoAnswer = fmt.Errorf("docker: the Docker daemon did not answer on %s within %s", dockerapi.DefaultSocket, preflightTimeout)

// probe asks the daemon for its version once and returns it, or the reason
// the engine cannot be used.
func (k dk) probe(ctx context.Context) (dockerapi.VersionInfo, error) {
	v, err := k.d.docker.Version(ctx)
	switch {
	case errors.Is(err, dockerapi.ErrUnavailable) && errors.Is(err, fs.ErrNotExist):
		return dockerapi.VersionInfo{}, fmt.Errorf("docker: the Docker socket %s is missing; install Docker Engine and start it", dockerapi.DefaultSocket)
	case errors.Is(err, dockerapi.ErrUnavailable):
		return dockerapi.VersionInfo{}, fmt.Errorf("docker: the Docker daemon is not reachable on %s: %w", dockerapi.DefaultSocket, err)
	case err != nil:
		return dockerapi.VersionInfo{}, fmt.Errorf("docker: ask the daemon for its version: %w", err)
	}
	got, ok := parseAPIVersion(v.APIVersion)
	if !ok {
		return dockerapi.VersionInfo{}, fmt.Errorf("docker: the daemon reports the API version %q, which is not <major>.<minor>", v.APIVersion)
	}
	need, _ := parseAPIVersion(dockerapi.MinAPIVersion)
	if got[0] < need[0] || got[0] == need[0] && got[1] < need[1] {
		return dockerapi.VersionInfo{}, fmt.Errorf("docker: the Docker API is %s; Docker Engine 25 (API %s) or later is needed", v.APIVersion, dockerapi.MinAPIVersion)
	}
	return v, nil
}

// parseAPIVersion reads "<major>.<minor>" as two non-negative integers, the
// way dockerapi's client reads it when it negotiates, so that the check and
// the client accept the same daemons.
func parseAPIVersion(s string) ([2]int, bool) {
	major, minor, found := strings.Cut(s, ".")
	maj, err1 := strconv.Atoi(major)
	mnr, err2 := strconv.Atoi(minor)
	if !found || err1 != nil || err2 != nil || maj < 0 || mnr < 0 {
		return [2]int{}, false
	}
	return [2]int{maj, mnr}, true
}
