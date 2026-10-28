package firewall

import (
	"context"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// deps are the firewall's host-facing functions. Production values come
// from conf, loaded, predict and guard; tests replace them.
type deps struct {
	root       string // "/" in production; the root conf.Read reads under
	readConfig func(root string) (fwstate.Config, error)
	readLoaded func(ctx context.Context, env core.Env) (fwstate.Loaded, error)
	predict    func(cfg fwstate.Config, cur fwstate.Loaded, tr fwstate.Transition) (fwstate.Loaded, error)
	edit       func(cfg fwstate.Config, args []string) (fwstate.Config, error)
	files      func(cfg fwstate.Config) (map[string][]byte, error)
	compare    func(a, b fwstate.Loaded, keep func(f fwstate.Family, table, chain string) bool) []fwstate.ChainDiff
	sysctl     func(cfg fwstate.Config, cur map[string]string, tr fwstate.Transition) (map[string]string, error)
	// lockout evaluates the lockout guard on st for the connectivity
	// exception; gc is the GuardContext the snapshot recorded.
	lockout func(ctx context.Context, env core.Env, st fwstate.Loaded, gc *core.GuardContext) (ok bool, detail string, err error)

	docker      dockerAPI
	readVariant func(ctx context.Context, env core.Env, f fwstate.Family, variant string, tables []string) (*netfilter.Ruleset, error)
	sockets     func() ([]netstat.Socket, error)
	interfaces  func() ([]netstat.Interface, error)
	routeGet    func(q netstat.RouteQuery) (netstat.Route, error)
	// shieldsUp reads Tailscale's shields-up preference; known is false
	// when tailscaled cannot be reached.
	shieldsUp func(ctx context.Context) (on, known bool, err error)
}

// dockerEvents is a stream of the daemon's events.
type dockerEvents interface {
	Next() (dockerapi.Event, error)
	Close() error
}

// dockerAPI is the part of the Docker Engine API the firewall reads.
type dockerAPI interface {
	Info(ctx context.Context) (dockerapi.SystemInfo, error)
	Version(ctx context.Context) (dockerapi.VersionInfo, error)
	ListContainers(ctx context.Context, o dockerapi.ListContainersOptions) ([]dockerapi.ContainerSummary, error)
	InspectContainer(ctx context.Context, id string) (dockerapi.ContainerJSON, error)
	ListNetworks(ctx context.Context, f dockerapi.Filters) ([]dockerapi.Network, error)
	Events(ctx context.Context, o dockerapi.EventsOptions) (dockerEvents, error)
}
