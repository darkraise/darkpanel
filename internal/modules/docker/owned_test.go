package docker

import (
	"errors"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// TestEFOwner: the owner is the darkpanel.module label, and an owned object
// is refused with docker.owned naming the kind, the object and the module;
// the refusal committed nothing, so it is Definite.
func TestEFOwner(t *testing.T) {
	owned := map[string]string{core.LabelModule: "npm", "other": "x"}
	if got := ownerOf(owned); got != "npm" {
		t.Errorf("ownerOf = %q, want npm", got)
	}
	for _, labels := range []map[string]string{nil, {}, {"other": "x"}, {core.LabelModule: ""}} {
		if got := ownerOf(labels); got != "" {
			t.Errorf("ownerOf(%v) = %q, want empty", labels, got)
		}
		if err := refuseOwned("network", "web", labels); err != nil {
			t.Errorf("refuseOwned(%v) = %v, want nil", labels, err)
		}
	}
	err := refuseOwned("network", "web", owned)
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.DockerOwned || coded.Detail != "network web belongs to module npm" {
		t.Fatalf("refuseOwned = %#v, want docker.owned %q", err, "network web belongs to module npm")
	}
	if !core.IsDefinite(err) {
		t.Error("the refusal is not Definite")
	}
}
