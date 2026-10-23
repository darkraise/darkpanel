package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/darkraise/darkpanel/internal/agent/wire"
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
// journal. A request carrying a call context is refused, and so is a plan
// whose input carries a guard context: only the controller sends either, so
// a break-glass plan runs with the guard context empty (spec 1.5).
func NewSocketHandler(cfg Config, sock SocketConfig) http.Handler {
	h := &handler{cfg: cfg}
	h.principal = func(*http.Request) (core.Principal, error) { return socketPrincipal(sock.Identity()), nil }
	// The handler returned below refuses every request with the header, so
	// none that reaches a route carries a call.
	h.call = func(*http.Request) (*core.CallContext, error) { return nil, nil }
	h.allow = func(ctx context.Context, op, name string) error { return h.socketAllow(ctx, sock, op, name) }
	h.hello = func(*http.Request) error { return nil }
	h.planInput = refuseSuppliedGuard
	mux := h.routes(false)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.Header.Values(wire.HeaderCall)) > 0 {
			h.writeProblem(w, errcode.New(errcode.InvalidInput,
				"the "+wire.HeaderCall+" header is accepted only from the controller"))
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// refuseSuppliedGuard refuses a plan input with a top-level member that would
// be decoded as the guard context. encoding/json matches a member to a field
// ignoring case, so this does too. An input that is not an object passes; the
// agent's validator refuses it.
func refuseSuppliedGuard(input json.RawMessage) error {
	var obj map[string]json.RawMessage
	if json.Unmarshal(input, &obj) != nil {
		return nil
	}
	for key := range obj {
		if strings.EqualFold(key, "guard") {
			return &errcode.Error{Code: errcode.InvalidInput, Detail: "the guard context is filled by the controller",
				Fields: []errcode.FieldError{{Path: "/" + key, Message: "the controller fills the guard context; leave it out"}}}
		}
	}
	return nil
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
