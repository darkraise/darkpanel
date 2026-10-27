package firewall

import (
	"context"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
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
}
