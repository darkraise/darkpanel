package docker

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

func mfRead(ctx context.Context, t *testing.T, a mfAPI) (json.RawMessage, error) {
	t.Helper()
	return mfDK(a).engineResource().Read(ctx, core.Env{})
}

// TestMFEngineResource: docker.engine is the resource of the Engine type.
func TestMFEngineResource(t *testing.T) {
	def := mfDK(mfAPI{}).engineResource()
	if def.Name != "docker.engine" || def.OutputType != reflect.TypeOf(Engine{}) {
		t.Errorf("resource = %q of %v", def.Name, def.OutputType)
	}
}

// TestMFEngineAvailable: an available engine reports the daemon's version
// and API, its platform and the container and image counts, and no reason.
func TestMFEngineAvailable(t *testing.T) {
	raw, err := mfRead(context.Background(), t, mfAPI{
		version: mfVersion("1.52"),
		info:    dockerapi.SystemInfo{Containers: 7, ContainersRunning: 3, Images: 12},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got Engine
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := Engine{Available: true, Version: "29.9.1", APIVersion: "1.52", OS: "linux", Arch: "amd64", Containers: 7, Running: 3, Images: 12}
	if got != want {
		t.Errorf("engine = %+v, want %+v", got, want)
	}
	if strings.Contains(string(raw), "reason") {
		t.Errorf("an available engine carries a reason: %s", raw)
	}
}

// TestMFEngineUnavailable: an engine that cannot be used is still a
// successful read: available false and the preflight's reason, with no
// counts.
func TestMFEngineUnavailable(t *testing.T) {
	for name, c := range map[string]struct {
		api  mfAPI
		want string
	}{
		"socket missing": {mfAPI{verErr: mfDial(syscall.ENOENT)}, "missing"},
		"daemon down":    {mfAPI{verErr: mfDial(syscall.ECONNREFUSED)}, "not reachable"},
		"API too old":    {mfAPI{version: mfVersion("1.43"), info: dockerapi.SystemInfo{Containers: 9}}, "1.43"},
		"info fails":     {mfAPI{version: mfVersion("1.52"), infoErr: errors.New("boom")}, "boom"},
	} {
		raw, err := mfRead(context.Background(), t, c.api)
		if err != nil {
			t.Errorf("%s: Read failed: %v", name, err)
			continue
		}
		var got Engine
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.Available || !strings.HasPrefix(got.Reason, "docker: ") || !strings.Contains(got.Reason, c.want) ||
			got.Version != "" || got.Containers != 0 || got.Images != 0 {
			t.Errorf("%s: engine = %+v", name, got)
		}
	}
}

// TestMFEngineCancelled: a read cut off by its context fails instead of
// reporting the engine unavailable.
func TestMFEngineCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := mfRead(ctx, t, mfAPI{verErr: context.Canceled}); !errors.Is(err, context.Canceled) {
		t.Errorf("Read error = %v, want context.Canceled", err)
	}
}
