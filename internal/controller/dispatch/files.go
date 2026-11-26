package dispatch

import (
	"context"
	"slices"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// FilesModule is the module that serves the file manager.
const FilesModule = "console"

// ReasonFilesUnserved is the module.unavailable message for an agent that
// does not report the console module.
const ReasonFilesUnserved = "Update the agent to use the file manager"

var _ core.DispatcherFiles = (*Dispatcher)(nil)

// File sends a file request to hostID's agent for p (M11 spec section 5).
// An agent whose last Hello does not report the console module, or whose
// client sends no file requests, is module.unavailable with
// ReasonFilesUnserved; then the agent must be in the support window and the
// console usable on the host.
func (d *Dispatcher) File(ctx context.Context, p core.Principal, hostID string, req core.FileRequest) (core.FileAnswer, error) {
	c, err := d.cfg.Hosts.Agent(ctx, hostID)
	if err != nil {
		return core.FileAnswer{}, err
	}
	hello, _ := d.cfg.Hosts.greetingOf(hostID)
	files, ok := c.(core.AgentFiles)
	if !ok || !slices.ContainsFunc(hello.Modules, func(m core.ModuleStatus) bool { return m.ID == FilesModule }) {
		return core.FileAnswer{}, errcode.New(errcode.ModuleUnavailable, ReasonFilesUnserved)
	}
	if _, err := d.agent(ctx, hostID, FilesModule+".files"); err != nil {
		return core.FileAnswer{}, err
	}
	return files.File(ctx, p, req)
}
