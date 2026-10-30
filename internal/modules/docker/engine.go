package docker

import (
	"context"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
)

// engineResource is docker.engine: the daemon's version and counts, or the
// reason the engine cannot be used.
func (k dk) engineResource() core.ResourceDef {
	return core.DefineResource(ID+".engine", k.engine)
}

// engine answers an unusable engine as a successful read carrying the
// reason, so the web page can show it; only a read cut off by its own
// context fails.
func (k dk) engine(ctx context.Context, _ core.Env) (Engine, error) {
	v, err := k.probe(ctx)
	if err == nil {
		info, infoErr := k.d.docker.Info(ctx)
		if infoErr == nil {
			return Engine{
				Available:  true,
				Version:    v.Version,
				APIVersion: v.APIVersion,
				OS:         v.Os,
				Arch:       v.Arch,
				Containers: info.Containers,
				Running:    info.ContainersRunning,
				Images:     info.Images,
			}, nil
		}
		err = fmt.Errorf("docker: read the engine's counts: %w", infoErr)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Engine{}, ctxErr
	}
	return Engine{Reason: err.Error()}, nil
}
