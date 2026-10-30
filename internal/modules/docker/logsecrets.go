package docker

import (
	"context"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/docker/logfilter"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// logFilter builds the filter for one read of c's logs: it masks the secrets
// the owning module registered for c, and passes the logs of a container no
// module owns through unchanged without looking any secret up.
func (k dk) logFilter(ctx context.Context, env core.Env, c dockerapi.ContainerJSON) (*logfilter.Filter, error) {
	if ownerOf(c.Config.Labels) == "" {
		return logfilter.New(nil), nil
	}
	secrets, err := k.d.secrets(ctx, env, c.ID)
	if err != nil {
		return nil, err
	}
	return logfilter.New(secrets), nil
}
