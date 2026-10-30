package docker

import (
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// ownerOf returns the module that labelled an Engine object, "" when none did.
func ownerOf(labels map[string]string) string { return labels[core.LabelModule] }

// refuseOwned refuses a change to an object another module manages; an
// unlabelled object is not refused.
func refuseOwned(kind, name string, labels map[string]string) error {
	owner := ownerOf(labels)
	if owner == "" {
		return nil
	}
	return core.Definite(errcode.New(errcode.DockerOwned, kind+" "+name+" belongs to module "+owner))
}
