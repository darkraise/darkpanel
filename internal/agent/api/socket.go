package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// SocketSource is the Principal.Source of every agent socket request.
const SocketSource = "socket"

// SocketConfig is what the agent socket's rules need.
type SocketConfig struct {
	// Identity reports this host's id and whether the agent holds
	// credentials; it is asked on every request, so an enrollment while the
	// socket serves takes effect at once.
	Identity func() (hostID string, enrolled bool)
}

// NewSocketHandler serves the agent API on the agent socket, whose peer the
// socket listener has already checked is root. The caller is always the
// local root user, whatever header the peer sends; before enrollment only
// hello and reads are served. Enablement is the controller's rule and is not
// applied, but a module whose preflight checks fail is refused. The journal
// routes are absent: the controller alone collects and acknowledges the
// journal.
func NewSocketHandler(cfg Config, sock SocketConfig) http.Handler {
	h := &handler{cfg: cfg}
	h.principal = func(*http.Request) (core.Principal, error) { return socketPrincipal(sock.Identity()), nil }
	h.allow = func(ctx context.Context, op, name string) error { return h.socketAllow(ctx, sock, op, name) }
	h.hello = func(*http.Request) error { return nil }
	return h.routes(false)
}

// socketPrincipal is the local root user, named after the host once the
// agent is enrolled.
func socketPrincipal(hostID string, enrolled bool) core.Principal {
	actor := core.ActorUnenrolled
	if enrolled {
		actor = "local:root@" + hostID
	}
	return core.Principal{Actor: actor, Scope: core.ScopeAdmin, Source: SocketSource}
}

func (h *handler) socketAllow(ctx context.Context, sock SocketConfig, op, name string) error {
	switch op {
	case opPlan, opApply, opConfirm, opRevert:
		if _, enrolled := sock.Identity(); !enrolled {
			return errcode.New(errcode.AgentNotEnrolled,
				"this agent is not enrolled; until it enrolls, its socket serves only hello and reads")
		}
	}
	switch op {
	case opRead, opResource, opPlan:
		return h.preflight(ctx, name)
	}
	return nil
}

// preflight runs the checks of the module name belongs to, which the
// controller's dispatcher would otherwise have enforced. The core has no
// preflight checks.
func (h *handler) preflight(ctx context.Context, name string) error {
	module, _, _ := strings.Cut(name, ".")
	if module == core.ModuleIDCore {
		return nil
	}
	st, err := h.cfg.Agent.Preflight(ctx, module)
	if err != nil {
		return err
	}
	if !st.Available {
		return errcode.New(errcode.ModuleUnavailable, fmt.Sprintf("module %s is unavailable: %s", module, st.Reason))
	}
	return nil
}
