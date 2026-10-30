package dstate

import (
	"encoding/json"

	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

type containerDoc struct {
	ID         string `json:"id"`
	Created    string `json:"created,omitempty"`
	Running    bool   `json:"running"`
	StartedAt  string `json:"startedAt,omitempty"`
	FinishedAt string `json:"finishedAt,omitempty"`
}

// ContainerRun renders a container's identity and run state. The Recorded form
// also holds startedAt and finishedAt; the Restored form leaves them out
// because an undo cannot bring them back.
func ContainerRun(c dockerapi.ContainerJSON, p Phase) (json.RawMessage, error) {
	if err := p.check(); err != nil {
		return nil, err
	}
	created, err := Instant(c.Created)
	if err != nil {
		return nil, err
	}
	doc := containerDoc{ID: c.ID, Created: created, Running: c.State.Running}
	if p == Recorded {
		if doc.StartedAt, err = Instant(c.State.StartedAt); err != nil {
			return nil, err
		}
		if doc.FinishedAt, err = Instant(c.State.FinishedAt); err != nil {
			return nil, err
		}
	}
	return json.Marshal(doc)
}

// ContainerRunning is the Restored document of a container with the given
// identity and running flag.
func ContainerRunning(id, created string, running bool) (json.RawMessage, error) {
	return ContainerRun(dockerapi.ContainerJSON{
		ID:      id,
		Created: created,
		State:   dockerapi.ContainerState{Running: running},
	}, Restored)
}
