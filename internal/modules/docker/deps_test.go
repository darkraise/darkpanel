package docker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

func TestDKConstants(t *testing.T) {
	if lockKey != "docker" || commitWait != 2*time.Minute || stopTimeout != 10*time.Second {
		t.Errorf("lockKey %q, commitWait %v, stopTimeout %v", lockKey, commitWait, stopTimeout)
	}
}

// TestDKProductionDK: the production wiring opens no connection, and its
// host functions are the operating system's.
func TestDKProductionDK(t *testing.T) {
	k := productionDK()
	if k.d.docker == nil || k.d.lstat == nil || k.d.readFile == nil || k.d.secrets == nil {
		t.Fatalf("incomplete wiring: %+v", k.d)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if fi, err := k.d.lstat(dir); err != nil || !fi.IsDir() {
		t.Errorf("lstat(dir) = %v, %v", fi, err)
	}
	if b, err := k.d.readFile(file); err != nil || string(b) != "data" {
		t.Errorf("readFile = %q, %v", b, err)
	}
	if s, err := k.d.secrets(context.Background(), core.Env{}, "abc"); s != nil || err != nil {
		t.Errorf("secrets = %v, %v; want none", s, err)
	}
}

func TestDKModuleWiresTheProductionDK(t *testing.T) {
	m, ok := Module().(module)
	if !ok || m.k.d.docker == nil {
		t.Fatalf("Module() = %#v, want a module over the production dk", Module())
	}
}

// TestDKAdapterReturnsNilInterfaces: a stream call that fails returns a nil
// interface, not the client's nil pointer inside one.
func TestDKAdapterReturnsNilInterfaces(t *testing.T) {
	c := newDockerClient(filepath.Join(t.TempDir(), "absent.sock"))
	ctx := context.Background()

	ps, err := c.PullImage(ctx, "busybox:1.36")
	if ps != nil || !errors.Is(err, dockerapi.ErrUnavailable) {
		t.Errorf("PullImage = %v, %v; want a nil interface and ErrUnavailable", ps, err)
	}
	ls, err := c.Logs(ctx, "abc", dockerapi.LogsOptions{Stdout: true})
	if ls != nil || !errors.Is(err, dockerapi.ErrUnavailable) {
		t.Errorf("Logs = %v, %v; want a nil interface and ErrUnavailable", ls, err)
	}
	ss, err := c.Stats(ctx, "abc", false)
	if ss != nil || !errors.Is(err, dockerapi.ErrUnavailable) {
		t.Errorf("Stats = %v, %v; want a nil interface and ErrUnavailable", ss, err)
	}
}
