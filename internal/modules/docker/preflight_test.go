package docker

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// mfAPI is a dockerAPI that answers only Version and Info; any other method
// panics on the nil embedded interface, which these tests never reach.
type mfAPI struct {
	dockerAPI
	version dockerapi.VersionInfo
	verErr  error
	info    dockerapi.SystemInfo
	infoErr error
}

func (a mfAPI) Version(context.Context) (dockerapi.VersionInfo, error) { return a.version, a.verErr }
func (a mfAPI) Info(context.Context) (dockerapi.SystemInfo, error)     { return a.info, a.infoErr }

func mfDK(a mfAPI) dk { return dk{d: deps{docker: a}} }

// mfDial is the error dockerapi's client returns when its dial fails: it
// wraps ErrUnavailable and the operating system's error.
func mfDial(errno syscall.Errno) error {
	return fmt.Errorf("dockerapi: GET /version: %w: %w", dockerapi.ErrUnavailable,
		&net.OpError{Op: "dial", Net: "unix", Err: os.NewSyscallError("connect", errno)})
}

func mfVersion(api string) dockerapi.VersionInfo {
	return dockerapi.VersionInfo{Version: "29.9.1", APIVersion: api, Os: "linux", Arch: "amd64"}
}

// mfRun runs the module's one preflight check against a.
func mfRun(t *testing.T, a mfAPI) error {
	t.Helper()
	checks := mfDK(a).preflight()
	if len(checks) != 1 || checks[0].ID != "docker.engine" {
		t.Fatalf("preflight = %+v, want the one check docker.engine", checks)
	}
	return checks[0].Run(context.Background(), core.Env{})
}

// TestMFPreflightPasses: a daemon whose API is 1.44 or later passes, compared
// as numbers (1.100 is later than 1.44, 2.0 later than 1.99).
func TestMFPreflightPasses(t *testing.T) {
	for _, api := range []string{"1.44", "1.45", "1.52", "1.100", "2.0"} {
		if err := mfRun(t, mfAPI{version: mfVersion(api)}); err != nil {
			t.Errorf("API %s: %v", api, err)
		}
	}
}

// TestMFPreflightFails: each reason is told apart, names what to do or what
// was found, and starts with the package name.
func TestMFPreflightFails(t *testing.T) {
	for name, c := range map[string]struct {
		api  mfAPI
		want []string
	}{
		"socket missing":           {mfAPI{verErr: mfDial(syscall.ENOENT)}, []string{"socket", "/var/run/docker.sock", "missing"}},
		"daemon refuses":           {mfAPI{verErr: mfDial(syscall.ECONNREFUSED)}, []string{"not reachable", "/var/run/docker.sock", "refused"}},
		"permission":               {mfAPI{verErr: mfDial(syscall.EACCES)}, []string{"not reachable", "permission denied"}},
		"API too old":              {mfAPI{version: mfVersion("1.43")}, []string{"1.43", "1.44"}},
		"API compared numerically": {mfAPI{version: mfVersion("1.9")}, []string{"1.9", "1.44"}},
		"old major":                {mfAPI{version: mfVersion("0.99")}, []string{"0.99"}},
		"unreadable API":           {mfAPI{version: mfVersion("abc")}, []string{`"abc"`}},
		"empty API":                {mfAPI{version: mfVersion("")}, []string{`""`}},
		"daemon error":             {mfAPI{verErr: &dockerapi.APIError{StatusCode: 500, Message: "boom"}}, []string{"500", "boom"}},
	} {
		err := mfRun(t, c.api)
		if err == nil {
			t.Errorf("%s: the check passed", name)
			continue
		}
		if !strings.HasPrefix(err.Error(), "docker: ") {
			t.Errorf("%s: error %q does not start with the package name", name, err)
		}
		for _, w := range c.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: error %q lacks %q", name, err, w)
			}
		}
	}
}

// mfSilent is a daemon that accepts the version request and never answers.
type mfSilent struct {
	*fakeDocker
	asked chan struct{}
}

func (a mfSilent) Version(ctx context.Context) (dockerapi.VersionInfo, error) {
	close(a.asked)
	<-ctx.Done()
	return dockerapi.VersionInfo{}, fmt.Errorf("dockerapi: GET /version: %w", ctx.Err())
}

// TestMFPreflightTimesOut: a daemon that never answers fails the check once
// preflightTimeout passes on the module's clock, not before.
func TestMFPreflightTimesOut(t *testing.T) {
	h := newDKHost(t)
	k := h.dk()
	silent := mfSilent{fakeDocker: h.docker, asked: make(chan struct{})}
	k.d.docker = silent
	done := make(chan error, 1)
	go func() { done <- k.preflight()[0].Run(context.Background(), h.env("")) }()
	<-silent.asked

	h.clock.Advance(preflightTimeout - time.Nanosecond)
	select {
	case err := <-done:
		t.Fatalf("the check ended before %v: %v", preflightTimeout, err)
	default:
	}
	h.clock.Advance(time.Nanosecond)
	select {
	case err := <-done:
		if err == nil || !strings.HasPrefix(err.Error(), "docker: ") ||
			!strings.Contains(err.Error(), "did not answer") || !strings.Contains(err.Error(), preflightTimeout.String()) {
			t.Errorf("error = %v, want the daemon's silence within %v", err, preflightTimeout)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the check still waits for the daemon after its bound")
	}
}

// TestMFProductionDKAvoidsTheHostSocket: in a test binary the production
// client dials absentSocket, never the host's Docker socket.
func TestMFProductionDKAvoidsTheHostSocket(t *testing.T) {
	_, err := productionDK().d.docker.Version(context.Background())
	if err == nil || !strings.Contains(err.Error(), absentSocket) {
		t.Errorf("Version error = %v, want a failed dial of %s", err, absentSocket)
	}
}

// TestMFManifestDeclaresPreflight: the module's manifest carries the engine
// check. The check is not run: that would reach the host's Docker socket.
func TestMFManifestDeclaresPreflight(t *testing.T) {
	m := Module().Manifest()
	if m.ID != ID || m.Version != 1 || !m.DefaultEnabled {
		t.Errorf("manifest = %+v", m)
	}
	if len(m.Preflight) != 1 || m.Preflight[0].ID != "docker.engine" || m.Preflight[0].Run == nil {
		t.Errorf("Preflight = %+v, want the one check docker.engine", m.Preflight)
	}
}
