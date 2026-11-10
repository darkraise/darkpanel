package tailscale

import (
	"context"
	"errors"
	"io/fs"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

type w11c06Files struct {
	core.FileWriter
	failOn  string
	removed *[]string
}

func (f w11c06Files) WriteFile(ctx context.Context, module, path string, data []byte, mode fs.FileMode) error {
	if path == f.failOn {
		return errors.New("disk full")
	}
	return f.FileWriter.WriteFile(ctx, module, path, data, mode)
}

func (f w11c06Files) Remove(ctx context.Context, module, path string) error {
	*f.removed = append(*f.removed, path)
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
