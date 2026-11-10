package tailscale

import (
	"context"
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

type w11c06Files struct {
	core.FileWriter
	failOn  string
	removed *[]string
	// removeErr fails every Remove, after it is recorded.
	removeErr error
}

func (f w11c06Files) WriteFile(ctx context.Context, module, path string, data []byte, mode fs.FileMode) error {
	if path == f.failOn {
		return errors.New("disk full")
	}
	return f.FileWriter.WriteFile(ctx, module, path, data, mode)
}

func (f w11c06Files) Remove(ctx context.Context, module, path string) error {
	*f.removed = append(*f.removed, path)
	if f.removeErr != nil {
		return f.removeErr
	}
	return f.FileWriter.Remove(ctx, module, path)
}

func TestW11c06FailedRepoStepRemovesWhatItWrote(t *testing.T) {
	s := pnNewSetup(t, true)
	delete(s.h.reads, keyringPath)
	delete(s.h.reads, sourcesPath)
	var removed []string
	s.env.Files = w11c06Files{FileWriter: s.h, failOn: sourcesPath, removed: &removed}

	_, err := s.t.packagePin().Handler.Task(context.Background(), s.env, pnInput(pnTarget))
	if err == nil {
		t.Fatal("the pin succeeded with a failing repository write")
	}
	if want := []string{keyringPath}; !reflect.DeepEqual(removed, want) {
		t.Errorf("removed %q, want %q", removed, want)
	}
}

func TestW11d02FailedRepoStepReportsAFileItCouldNotRemove(t *testing.T) {
	s := pnNewSetup(t, true)
	delete(s.h.reads, keyringPath)
	delete(s.h.reads, sourcesPath)
	var removed []string
	s.env.Files = w11c06Files{FileWriter: s.h, failOn: sourcesPath, removed: &removed, removeErr: errors.New("read-only file system")}

	_, err := s.t.packagePin().Handler.Task(context.Background(), s.env, pnInput(pnTarget))
	if err == nil || !strings.Contains(err.Error(), "read-only file system") || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("error %v, want it to carry the removal's failure and the write's", err)
	}
	if want := []string{keyringPath}; !reflect.DeepEqual(removed, want) {
		t.Errorf("removed %q, want %q", removed, want)
	}
	if e := pnPinEffect(t, s.h); e.State != core.EffectSkipped {
		t.Errorf("effect state %s, want skipped: the package was not touched", e.State)
	}
}

func TestW11d02LaterFailedStepKeepsTheRepoFiles(t *testing.T) {
	s := pnNewSetup(t, false)
	delete(s.h.reads, keyringPath)
	delete(s.h.reads, sourcesPath)
	s.apt.missing[pnPrior] = true
	var removed []string
	s.env.Files = w11c06Files{FileWriter: s.h, removed: &removed}

	_, err := s.t.packagePin().Handler.Task(context.Background(), s.env, pnInput(pnTarget))
	if coded := pnCoded(t, err, errcode.TailscalePinFailed); !strings.Contains(coded.Detail, "stage-prior") {
		t.Errorf("detail %q, want it to name stage-prior", coded.Detail)
	}
	if len(removed) != 0 {
		t.Errorf("removed %q, want the repository files left for uninstall", removed)
	}
}
