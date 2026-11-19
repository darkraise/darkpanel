package system

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform"
)

// RefreshInput is the input of system.index.refresh.
type RefreshInput struct{}

// Refreshed is the output of system.index.refresh.
type Refreshed struct {
	FailedSources []string `json:"failedSources" jsonschema_description:"The sources apt could not fetch or verify, as \"<uri> <suite>\"."`
}

func (m module) refreshAction() core.ActionDef {
	return core.DefineTask[RefreshInput, Refreshed](core.ActionSpec{
		Name: ID + ".index.refresh", Version: 1,
		Summary: "Refresh the package index of every configured source.",
		Class:   core.ClassCompensable,
		Side:    core.SideAgent,
		// It changes no installed software, so there is nothing to undo, and
		// running it again only refreshes again.
		ForwardOnly:  true,
		Converges:    true,
		LockKeys:     []string{aptLockKey},
		ApplyTimeout: 10 * time.Minute,
	}, refreshHandler{})
}

type refreshHandler struct{}

func (refreshHandler) Plan(context.Context, core.Env, RefreshInput) (core.PlanOutcome, error) {
	sum := sha256.Sum256([]byte(ID + ".index.refresh"))
	return core.PlanOutcome{
		Diff:      []core.DiffLine{{Op: "+", Text: "Refresh the package index of every configured source (apt-get update)"}},
		StateHash: hex.EncodeToString(sum[:]),
	}, nil
}

// Task runs apt-get update and records the sources that failed, which apt
// reports as warnings with exit 0, for the updates read.
func (refreshHandler) Task(ctx context.Context, env core.Env, _ RefreshInput) (Refreshed, error) {
	defer invalidateUpdates(env.HostID)
	out, err := aptExec(ctx, env, platform.SystemAptUpdate(""))
	if err != nil {
		return Refreshed{}, err
	}
	failed := failedSourcesOf(out)
	var data []byte
	for _, s := range failed {
		data = append(data, s+"\n"...)
	}
	dir, name := path.Split(failedSourcesFile)
	if err := env.Files.WriteManaged(ctx, ID, path.Clean(dir), name, data, 0o600, 0, 0); err != nil {
		return Refreshed{}, err
	}
	return Refreshed{FailedSources: failed}, nil
}

// Verify has nothing to check: a refresh leaves no state it could compare.
func (refreshHandler) Verify(context.Context, core.Env, RefreshInput, Refreshed) error { return nil }

// failedSourcesOf reads apt-get update's "Err:<n> <uri> <suite>[/<component>]
// ..." lines as "<uri> <suite>", each once, in apt's order.
func failedSourcesOf(out []byte) []string {
	failed := []string{}
	for line := range strings.Lines(string(out)) {
		if !strings.HasPrefix(line, "Err:") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		suite := f[2]
		if s, _, ok := strings.Cut(suite, "/"); ok && s != "" && s != "." {
			suite = s
		}
		if src := f[1] + " " + suite; !slices.Contains(failed, src) {
			failed = append(failed, src)
		}
	}
	return failed
}
