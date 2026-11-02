package docker

import (
	"context"
	"fmt"
	"sync"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/docker/logfilter"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// SecretSource returns the secrets currently placed in a module's container,
// the one with the given id, so that its logs can be masked.
type SecretSource func(ctx context.Context, env core.Env, containerID string) ([][]byte, error)

// RegisterLogSecrets registers src as the source of the secrets placed in the
// containers of module, whose id is the value of their darkpanel.module label.
// It is called only from init functions, and it panics on an empty module id,
// a nil source or a second registration for the same module.
func RegisterLogSecrets(module string, src SecretSource) {
	logSecretRegistry.register(module, src)
}

// logSecretRegistry is the only registry production reads.
var logSecretRegistry = newSecretRegistry()

// secretRegistry maps a module id to the source of its containers' secrets.
type secretRegistry struct {
	mu      sync.RWMutex
	sources map[string]SecretSource
}

func newSecretRegistry() *secretRegistry {
	return &secretRegistry{sources: map[string]SecretSource{}}
}

func (r *secretRegistry) register(module string, src SecretSource) {
	if module == "" {
		panic("docker: RegisterLogSecrets needs a module id")
	}
	if src == nil {
		panic(fmt.Sprintf("docker: RegisterLogSecrets needs a source for module %q", module))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sources[module]; ok {
		panic(fmt.Sprintf("docker: log secrets of module %q are already registered", module))
	}
	r.sources[module] = src
}

func (r *secretRegistry) lookup(module string) (SecretSource, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	src, ok := r.sources[module]
	return src, ok
}

// secretsVia returns the secrets lookup of deps: it asks the source registered
// for the module that owns the container, read from the container's labels
// through d, and finds none for a container whose module registered no source.
func (r *secretRegistry) secretsVia(d dockerAPI) func(ctx context.Context, env core.Env, containerID string) ([][]byte, error) {
	return func(ctx context.Context, env core.Env, containerID string) ([][]byte, error) {
		c, err := d.InspectContainer(ctx, containerID)
		if err != nil {
			return nil, engineError("container", containerID, err)
		}
		module := ownerOf(c.Config.Labels)
		if module == "" {
			return nil, nil
		}
		src, ok := r.lookup(module)
		if !ok {
			return nil, nil
		}
		return src(ctx, env, c.ID)
	}
}

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
